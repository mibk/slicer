package main

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"honnef.co/go/unused"
)

func pruneUnusedObjs(workspace string, packages []string) {
	ch := unused.NewChecker(unused.CheckAll)
	ch.WholeProgram = true

	gopath, err := filepath.Abs(workspace)
	if err != nil {
		log.Fatal(err)
	}
	build.Default.GOPATH = gopath

	// For unused.Checker it is important to be in the package directory
	// in order to recognise vendored packages.
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(workspace, "src", dstPackage)); err != nil {
		log.Fatal(err)
	}
	defer os.Chdir(wd)

	us, err := ch.Check(packages)
	if err != nil {
		// Program slicing can make resulting programs invalid (because
		// of unused imports or variables). Try to fix it.

		if errs, ok := err.(unused.Error); ok {
			invalid := make(map[token.Pos]*token.FileSet)
			isSerious := false
			for p, errs := range errs.Errors {
				for _, err := range errs {
					if terr, ok := err.(types.Error); ok && terr.Soft {
						invalid[terr.Pos] = terr.Fset
						continue
					}
					// Unused variables or imports are soft errors. If
					// there are other errors than soft, report them
					// and exit afterwards.
					isSerious = true
					log.Printf("%s: %v", p, err)
				}
			}
			if !isSerious {
				files := make(map[string][]int)
				for p, fset := range invalid {
					f := fset.File(p)
					files[f.Name()] = append(files[f.Name()], f.Position(p).Offset)
				}
				for f, offs := range files {
					Verbosef("Fixing: %s @ %v", f, offs)
					fix(f, offs)
				}
				us, err = ch.Check(packages)
				if err != nil {
					log.Panicf("Fixed program shouldn't contain errors: %v", err)
				}
				Verbosef("")
				goto Fixed
			}
		}
		log.Fatalf("Checking unused packages: %v", err)
	}

Fixed:
	unused := map[string][]Unused{}
	for _, u := range us {
		Verbosef("%s:#%d (%T)\n", u.Position.Filename, u.Position.Offset, u.Obj)
		unused[u.Position.Filename] = append(unused[u.Position.Filename], Unused{u.Obj, u.Position.Offset})
	}
	Verbosef("")

	Verbosef("Pruning unused objects")
	for file, us := range unused {
		pruneFileUnused(file, us)
	}
}

func pruneFileUnused(filename string, us []Unused) {
	Verbosef("Pruning %s", filename)
	fset := token.NewFileSet()
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatal(err)
	}
	fileAST, err := parser.ParseFile(fset, filename, b, 0)
	if err != nil {
		log.Fatal(err)
	}

	op := NewObjPruner(fset, us)
	op.Update(fileAST)
	f, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := printer.Fprint(f, fset, fileAST); err != nil {
		log.Fatal(err)
	}
}

type Unused struct {
	obj types.Object
	pos int
}

type ObjPruner struct {
	fset   *token.FileSet
	unused []Unused
}

func NewObjPruner(fset *token.FileSet, unused []Unused) *ObjPruner {
	return &ObjPruner{fset, unused}
}

func (op *ObjPruner) Update(node ast.Node) ast.Node {
	switch n := node.(type) {
	case *ast.File:
		var newDecls []ast.Decl
		for _, decl := range n.Decls {
			if decl := op.Update(decl); decl != nil {
				newDecls = append(newDecls, decl.(ast.Decl))
			}
		}
		n.Decls = newDecls
	case *ast.GenDecl:
		switch n.Tok {
		case token.CONST, token.TYPE, token.VAR:
			var newSpecs []ast.Spec
			for _, spec := range n.Specs {
				if spec := op.Update(spec); spec != nil {
					newSpecs = append(newSpecs, spec.(ast.Spec))
				}
			}
			if newSpecs == nil {
				return nil
			}
			n.Specs = newSpecs
		}
	case *ast.FuncDecl:
		if op.ShouldRemove(n.Name) {
			return nil
		} else if op.ShouldRemoveMethod(n) {
			return nil
		}
		// log.Printf("%d (%d) [%s] -- %v", n.Pos(), n.Name.Pos(), n.Name.Name, op.unused)
		return n
	case *ast.TypeSpec:
		typ := op.Update(n.Type)
		if typ == nil {
			return nil
		}
		n.Type = typ.(ast.Expr)
	case *ast.StructType:
		var newFields []*ast.Field
		for _, f := range n.Fields.List {
			if f := op.Update(f); f != nil {
				newFields = append(newFields, f.(*ast.Field))
			}
		}
		if newFields == nil {
			return nil
		}
		n.Fields.List = newFields
	case *ast.Field:
		if len(n.Names) == 1 {
			break
		}
		var newIdents []*ast.Ident
		for _, id := range n.Names {
			if id := op.Update(id); id != nil {
				newIdents = append(newIdents, id.(*ast.Ident))
			}
		}
		if newIdents == nil {
			return nil
		}
		n.Names = newIdents
		// Return now to prevent removing the whole declaration list
		// as the position of the list is equal to the position of the
		// first identificator in that list.
		return n
	default:
		// fmt.Printf("%T: #%d,%d\n", node, node.Pos(), node.End())
	}
	if op.ShouldRemove(node) {
		return nil
	}
	return node
}

func (op *ObjPruner) ShouldRemove(node ast.Node) bool {
	for _, p := range op.unused {
		if op.fset.Position(node.Pos()).Offset == p.pos {
			return true
		}
	}
	return false
}

func (op *ObjPruner) ShouldRemoveMethod(fd *ast.FuncDecl) bool {
	if fd.Recv == nil {
		// It's not a method.
		return false
	}
	var name string
	switch expr := fd.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := expr.X.(*ast.Ident); ok {
			name = id.Name
		} else {
			log.Printf("Unknown *ast.StarExpr.X: %T", expr.X)
			return false
		}
	default:
		log.Printf("Unknown receiver type: %T", expr)
		return false
	}
	for _, p := range op.unused {
		if p.obj.Name() == name {
			return true
		}
	}
	return false
}
