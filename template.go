package main

import (
	"bytes"
	"fmt"
	"os"
	"text/template"
)

func WriteTmplToFile(filename string, ts TemplateStruct) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return sliceTmpl.Execute(f, ts)
}

type Imports []imp

func (imps *Imports) Append(name, path string) {
	*imps = append(*imps, imp{name, path})
}

func (imps Imports) CopyWithFmt() Imports {
	for _, im := range imps {
		if im.Path == "fmt" {
			// TODO: fmt is indeed imported, but the
			// local name could be different.
			return imps
		}
	}
	imps.Append("", "fmt")
	return imps
}

func (imps Imports) Paths() []string {
	paths := make([]string, 0, len(imps))
	for _, im := range imps {
		paths = append(paths, im.Path)
	}
	return paths
}

func (imps Imports) String() string {
	var buf bytes.Buffer
	buf.WriteString("import (\n")
	for _, im := range imps {
		fmt.Fprintf(&buf, "\t%s %q\n", im.Name, im.Path)
	}
	buf.WriteString(")\n")
	return buf.String()
}

type imp struct {
	Name string
	Path string
}

type TemplateStruct struct {
	Imports string
	Func    string
	Name    string
	Files   []CoverFile
}

type CoverFile struct {
	Package string
	Name    string
	Var     string
}

var sliceTmpl = template.Must(template.New("main").Parse(`package main

{{ .Imports }}

func main() {
	{{ .Name }}()
	{{ range .Files }}
	fmt.Println({{ .Name | printf "%q" }})
	for i, cnt := range {{ .Package }}.{{ .Var }}.Count {
		if cnt > 0 {
			continue
		}
		line0 := {{ .Package }}.{{ .Var }}.Pos[i*3+0]
		col0 := uint16({{ .Package }}.{{ .Var }}.Pos[i*3+2])
		line1 := {{ .Package }}.{{ .Var }}.Pos[i*3+1]
		col1 := uint16({{ .Package }}.{{ .Var }}.Pos[i*3+2] >> 16)
		fmt.Printf("#%d:%d,%d:%d\n", line0, col0, line1, col1)
	}
	{{ end }}
}

{{ .Func }}
`))
