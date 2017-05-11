package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"

	multierror "github.com/hashicorp/go-multierror"
	"github.com/tenntenn/goq"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(paths []string) (rerr error) {
	srcDir, err := os.Getwd()
	if err != nil {
		return err
	}

	for i := range paths {
		if err := checkByPkg(paths[i], srcDir); err != nil {
			rerr = multierror.Append(rerr, err)
		}
	}

	return
}

func checkByPkg(path, srcDir string) error {
	pkg, err := build.Import(path, srcDir, build.IgnoreVendor)
	if err != nil {
		return err
	}

	var files []*ast.File
	fset := token.NewFileSet()
	for i := range pkg.GoFiles {
		gofile := filepath.Join(pkg.Dir, pkg.GoFiles[i])
		f, err := parser.ParseFile(fset, gofile, nil, 0)
		if err != nil {
			return err
		}
		files = append(files, f)
	}

	config := &types.Config{
		Importer: importer.Default(),
	}

	info := &types.Info{
		Uses: map[*ast.Ident]types.Object{},
		Defs: map[*ast.Ident]types.Object{},
	}

	if _, err := config.Check(pkg.ImportPath, fset, files, info); err != nil {
		return err
	}

	ctxPkg, err := config.Importer.Import("golang.org/x/net/context")
	if err != nil {
		return err
	}

	contextType := ctxPkg.Scope().Lookup("Context").Type()
	if contextType == nil {
		return nil
	}

	results := goq.New(fset, files, info).Query(&goq.Type{
		Identical: contextType,
	})

	type key struct {
		Scope *types.Scope
		Name  string
	}

	rs := map[key]goq.Results{}
	tops := map[key]token.Pos{}

	for _, r := range results {
		if o := r.Object; o != nil && o.Name() != "_" {
			k := key{
				Scope: o.Parent(),
				Name:  o.Name(),
			}
			rs[k] = append(rs[k], r)
			if top, ok := tops[k]; !ok || r.Node.Pos() < top {
				tops[k] = r.Node.Pos()
			}
		}
	}

	for k := range rs {
		assigns := rs[k].Filter(&goq.Node{
			Files: files,
			Path: []goq.Query{
				&goq.AssignStmtNode{
					Lhs: goq.NewSet(nil).Put(goq.QueryFunc(func(v interface{}) bool {
						n, ok := v.(*ast.Ident)
						if !ok {
							return false
						}

						o := info.Uses[n]
						if o == nil {
							return false
						}

						return types.Identical(o.Type(), contextType)
					})),
				},
			}})

		if len(assigns) >= 2 {
			fmt.Fprintln(os.Stderr, "[Overwrite]")
			for i := range assigns {
				fmt.Fprintf(os.Stderr, "\t %v\n", fset.Position(assigns[i].Node.Pos()))
			}
			continue
		}

		if len(assigns) == 1 && assigns[0].Node.Pos() > tops[k] {
			fmt.Fprintln(os.Stderr, "[Overwrite]")
			fmt.Fprintf(os.Stderr, "\t %v\n", fset.Position(assigns[0].Node.Pos()))
			continue
		}
	}

	return nil
}
