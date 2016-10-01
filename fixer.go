package main

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"os"
)

func fix(filename string, offsets []int) {
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		log.Fatal("Parsing file %s: %v", filename, err)
	}
	fx := &Fixer{fset, offsets}
	fx.Update(fileAST)

	f, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := printer.Fprint(f, fset, fileAST); err != nil {
		log.Fatal(err)
	}
}

type Fixer struct {
	fset    *token.FileSet
	offsets []int
}

func (fx *Fixer) Update(node ast.Node) ast.Node {
	switch n := node.(type) {
	case *ast.File:
		var newDecls []ast.Decl
		for _, decl := range n.Decls {
			if decl := fx.Update(decl); decl != nil {
				newDecls = append(newDecls, decl.(ast.Decl))
			}
		}
		n.Decls = newDecls
		return n
	case *ast.GenDecl:
		if n.Tok != token.IMPORT {
			return n
		}
		var newImports []ast.Spec
		for _, spec := range n.Specs {
			if spec := fx.Update(spec); spec != nil {
				newImports = append(newImports, spec.(ast.Spec))
			}
		}
		if newImports == nil {
			return nil
		}
		n.Specs = newImports
	case *ast.FuncDecl:
		var newStmts []ast.Stmt
		for _, stmt := range n.Body.List {
			if stmt := fx.Update(stmt); stmt != nil {
				newStmts = append(newStmts, stmt.(ast.Stmt))
			}
		}
		if newStmts == nil {
			return nil
		}
		n.Body.List = newStmts
		return n
	case *ast.AssignStmt:
		var newLhs, newRhs []ast.Expr
		for i, e := range n.Lhs {
			if fx.ShouldRemove(e) {
				n.Lhs[i] = &ast.Ident{Name: "_"}
			} else {
				// Try to remove where possible. Using _
				// is a plan B.
				newLhs = append(newLhs, e)
				if len(n.Lhs) == len(n.Rhs) {
					newRhs = append(newRhs, n.Rhs[i])
				}
			}
		}
		if newLhs == nil {
			return nil
		}
		if newRhs != nil {
			n.Lhs = newLhs
			n.Rhs = newRhs
		}
		return n
	}
	if fx.ShouldRemove(node) {
		return nil
	}
	return node
}

func (fx *Fixer) ShouldRemove(node ast.Node) bool {
	// log.Printf("%T:%d  <-- %v", node, fx.fset.Position(node.Pos()).Offset, f.offsets)
	for _, off := range fx.offsets {
		if fx.fset.Position(node.Pos()).Offset == off {
			return true
		}
	}
	return false
}
