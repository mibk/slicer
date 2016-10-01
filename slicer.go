package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func sliceFile(dstDir string, cr *CoverResult) error {
	code, err := ioutil.ReadFile(cr.Filename)
	if err != nil {
		return err
	}

	path := makePathRelative(cr.Filename)
	if path == "" {
		return fmt.Errorf("cannot find package for file: %s", cr.Filename)
	}
	Verbosef("Slicing file: %s", path)
	offsets := make([]Uncovered, 0, len(cr.Removes))
	for _, rem := range cr.Removes {
		off0, off1 := findOffsets(code, rem)
		Verbosef("#%d,%d\n", off0, off1)
		offsets = append(offsets, Uncovered{off0, off1})
	}
	Verbosef("")

	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, cr.Filename, code, 0)
	if err != nil {
		return err
	}

	sp := NewStmtPruner(fset, offsets)
	if sp.Update(fileAST) == nil {
		return nil
	}
	filename := filepath.Join(dstDir, "vendor", path)
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	return printer.Fprint(file, fset, fileAST)
}

func makePathRelative(filename string) string {
	vendor := filepath.Join(srcDir, "vendor")
	if strings.HasPrefix(filename, vendor) {
		return filename[len(vendor)+1:]
	}
	gopath := filepath.Join(build.Default.GOPATH, "src")
	if strings.HasPrefix(filename, gopath) {
		return filename[len(gopath)+1:]
	}
	goroot := filepath.Join(build.Default.GOROOT, "src")
	if strings.HasPrefix(filename, goroot) {
		return filename[len(goroot)+1:]
	}
	panic(fmt.Sprintf("cannot make path %q relative", filename))
}

func findOffsets(buf []byte, rem CoverPos) (off0, off1 int) {
	line := 1
	col := 1
	for i, b := range buf {
		if line == rem.Line0 && col == rem.Col0 {
			off0 = i
		} else if line == rem.Line1 && col == rem.Col1 {
			off1 = i + 1
			return
		}
		if b == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	panic("shouldn't happen")
}

type Uncovered struct {
	Pos int
	End int
}

func (c Uncovered) Match(fset *token.FileSet, n ast.Node) bool {
	return fset.Position(n.Pos()).Offset >= c.Pos && fset.Position(n.End()).Offset <= c.End
}

type StmtPruner struct {
	fset    *token.FileSet
	offsets []Uncovered
}

func NewStmtPruner(fset *token.FileSet, offsets []Uncovered) *StmtPruner {
	return &StmtPruner{fset, offsets}
}

func (sp *StmtPruner) Update(node ast.Node) ast.Node {
	if sp.ShouldRemove(node) {
		return nil
	}
	switch n := node.(type) {
	case *ast.File:
		var newDecls []ast.Decl
		for _, decl := range n.Decls {
			if decl := sp.Update(decl); decl != nil {
				newDecls = append(newDecls, decl.(ast.Decl))
			}
		}
		if len(newDecls) == 0 {
			return nil
		}
		n.Decls = newDecls
		return n
	case *ast.FuncDecl:
		lastStmt := n.Body.List[len(n.Body.List)-1]
		body := sp.Update(n.Body)
		if body == nil {
			// Let the unused.Checker get rid of that.
			return n
		}
		newBlStmt := body.(*ast.BlockStmt)
		if _, ok := lastStmt.(*ast.ReturnStmt); ok {
			if newBlStmt.List[len(newBlStmt.List)-1] != lastStmt {
				// Preserve required return statement.
				newBlStmt.List = append(newBlStmt.List, lastStmt)

				// TODO: This is not enough. There could be some else-branch
				// missing, or the default case of a switch, that could make
				// the resulting program incorrect.
			}
		}
		n.Body = newBlStmt
		return n
	case *ast.BlockStmt:
		var newBlStmts []ast.Stmt
		for _, stmt := range n.List {
			if stmt := sp.Update(stmt); stmt != nil {
				newBlStmts = append(newBlStmts, stmt.(ast.Stmt))
			}
		}
		if len(newBlStmts) == 0 {
			return nil
		}
		n.List = newBlStmts
		return n
	case *ast.IfStmt:
		body := sp.Update(n.Body)
		if body == nil {
			return nil
		}
		n.Body = body.(*ast.BlockStmt)
		return n
	case *ast.ForStmt:
		body := sp.Update(n.Body)
		if body == nil {
			return nil
		}
		n.Body = body.(*ast.BlockStmt)
		return n
	case *ast.RangeStmt:
		body := sp.Update(n.Body)
		if body == nil {
			return nil
		}
		n.Body = body.(*ast.BlockStmt)
		return n
	case *ast.SwitchStmt:
		body := sp.Update(n.Body)
		if body == nil {
			return nil
		}
		n.Body = body.(*ast.BlockStmt)
		return n
	case *ast.CaseClause:
		var newBlStmts []ast.Stmt
		for _, stmt := range n.Body {
			if stmt := sp.Update(stmt); stmt != nil {
				newBlStmts = append(newBlStmts, stmt.(ast.Stmt))
			}
		}
		if len(newBlStmts) == 0 {
			return nil
		}
		n.Body = newBlStmts
		return n
	case *ast.LabeledStmt:
		stmt := sp.Update(n.Stmt)
		if stmt == nil {
			return nil
		}
		n.Stmt = stmt.(ast.Stmt)
		return n
	}
	return node
}

func (sp *StmtPruner) ShouldRemove(node ast.Node) bool {
	for _, p := range sp.offsets {
		if p.Match(sp.fset, node) {
			return true
		}
	}
	return false
}
