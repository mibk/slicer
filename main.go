package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	workspace = flag.String("w", "./slicer-ws", "`workspace` directory; it will be truncated")
	verbose   = flag.Bool("v", false, "verbose mode")

	srcDir string
)

const (
	coverVarPrefix = "SliceCover_"
	dstPackage     = "sliced"
)

func main() {
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	tmplFile := flag.Arg(0)
	if tmplFile == "" {
		flag.Usage()
		os.Exit(2)
	}

	if err := os.RemoveAll(*workspace); err != nil {
		log.Fatal(err)
	}
	slice(tmplFile)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <template>\n\nFlags:\n", os.Args[0])
	flag.PrintDefaults()
}

func slice(tmplFile string) {
	fset := token.NewFileSet()
	pf, err := parser.ParseFile(fset, tmplFile, nil, 0)
	if err != nil {
		log.Fatal(err)
	}

	imports := make(Imports, 0, len(pf.Imports))
	var covers []CoverFile
	srcDir = filepath.Dir(tmplFile)
	for _, im := range pf.Imports {
		path, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			log.Panic("invalid quoted string returned by parser")
		}
		name := ""
		if im.Name != nil {
			name = im.Name.Name
		}
		imports.Append(name, path)
		p, err := build.Import(path, srcDir, 0)
		if err != nil {
			log.Fatalf("Importing package: %v", err)
		}
		newDir := filepath.Join(*workspace, "src/cover/vendor", path)
		if err := os.MkdirAll(newDir, 0755); err != nil {
			log.Fatal(err)
		}
		for i, file := range p.GoFiles {
			filePath := filepath.Join(p.Dir, file)
			covers = append(covers, CoverFile{p.Name, filePath, coverVarPrefix + strconv.Itoa(i)})
			err := genCoverFile(i, filepath.Join(newDir, file), filePath)
			if err != nil {
				log.Fatalf("Generating cover file %s: %v", filePath, err)
			}
		}
	}

	var sliceFunc *ast.FuncDecl
	for _, decl := range pf.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fd.Name.Name, "Slice") ||
			fd.Recv != nil || fd.Type.Params.List != nil || fd.Type.Results != nil {
			continue
		}
		sliceFunc = fd

		// TODO: Add support for multiple functions.
		break
	}

	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, sliceFunc); err != nil {
		log.Fatalf("Printing slice func %s: %v", sliceFunc.Name.Name, err)
	}

	coverProg := filepath.Join(*workspace, "src/cover", "cover.go")
	err = WriteTmplToFile(coverProg, TemplateStruct{
		Imports: imports.CopyWithFmt().String(),
		Func:    buf.String(),
		Name:    sliceFunc.Name.Name,
		Files:   covers,
	})
	if err != nil {
		log.Fatal("Writing template %s: %v", coverProg, err)
	}

	var stdout bytes.Buffer
	cmd := exec.Command("go", "run", coverProg)
	gopath, err := filepath.Abs(*workspace)
	if err != nil {
		log.Fatalf("Executing coverage program: %v", err)
	}
	cmd.Env = []string{"GOPATH=" + gopath}
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}

	clearFiles, err := parseCoverOutput(&stdout)
	if err != nil {
		log.Fatalf("Parsing the output of the coverage program: %v", err)
	}

	dstDir := filepath.Join(*workspace, "src", dstPackage)
	for _, cr := range clearFiles {
		if err := sliceFile(dstDir, cr); err != nil {
			log.Fatalf("Slicing %s: %v", cr.Filename, err)
		}
	}

	usageProg := filepath.Join(dstDir, "main.go")
	err = WriteTmplToFile(usageProg, TemplateStruct{
		Imports: imports.String(),
		Func:    buf.String(),
		Name:    sliceFunc.Name.Name,
	})
	if err != nil {
		log.Fatalf("Writing template %s: %v", usageProg, err)
	}

	pruneUnusedObjs(*workspace, append(imports.Paths(), dstPackage))
}

type CoverResult struct {
	Filename string
	Removes  []CoverPos
}

type CoverPos struct {
	Line0, Col0 int
	Line1, Col1 int
}

func (r CoverPos) String() string {
	return fmt.Sprintf("%d:%d,%d:%d", r.Line0, r.Col0, r.Line1, r.Col1)
}

var rangeRx = regexp.MustCompile(`^#(\d+):(\d+),(\d+):(\d+)$`)

func parseCoverOutput(r io.Reader) ([]*CoverResult, error) {
	var results []*CoverResult
	sc := bufio.NewScanner(r)
	var cur *CoverResult
	for sc.Scan() {
		l := sc.Text()
		if !strings.HasPrefix(l, "#") {
			if cur != nil {
				results = append(results, cur)
			}
			cur = &CoverResult{Filename: l}
			continue
		}
		if m := rangeRx.FindStringSubmatch(l); m != nil {
			cur.Removes = append(cur.Removes, CoverPos{
				Line0: toInt(m[1]),
				Col0:  toInt(m[2]),
				Line1: toInt(m[3]),
				Col1:  toInt(m[4]),
			})
			continue
		}
		return nil, fmt.Errorf("error parsing: %q", l)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	results = append(results, cur)
	return results, nil
}

func toInt(a string) int {
	i, err := strconv.Atoi(a)
	if err != nil {
		log.Panic(err)
	}
	return i
}

func genCoverFile(key int, dst, src string) error {
	cmd := exec.Command("go", "tool", "cover", "-mode=set", "-var="+coverVarPrefix+strconv.Itoa(key), "-o", dst, src)
	return cmd.Run()
}

func Verbosef(format string, args ...interface{}) {
	if *verbose {
		log.Printf(format, args...)
	}
}
