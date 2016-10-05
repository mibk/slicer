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
	fx := &Fixer{fset, offsets, make(map[*ast.Object]bool)}
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

	delObjs map[*ast.Object]bool
}

func (fx *Fixer) Update(node ast.Node) {
	ast.Walk(fx, node)
}

func (fx *Fixer) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.ImportSpec:
		if fx.ShouldRemove(n.Pos()) {
			n.Name = ast.NewIdent("_")
		}
	case *ast.Ident:
		if (n.Obj != nil && fx.delObjs[n.Obj]) || fx.ShouldRemove(n.Pos()) {
			if n.Obj != nil {
				fx.delObjs[n.Obj] = true
			}
			n.Name = "_"
		}
	case *ast.RangeStmt:
		if fx.ShouldRemove(n.TokPos) {
			n.Tok = token.ASSIGN
		}
	case *ast.AssignStmt:
		if fx.ShouldRemove(n.TokPos) {
			n.Tok = token.ASSIGN
		}
	}
	return fx
}

func (fx *Fixer) ShouldRemove(pos token.Pos) bool {
	for _, off := range fx.offsets {
		if fx.fset.Position(pos).Offset == off {
			return true
		}
	}
	return false
}
