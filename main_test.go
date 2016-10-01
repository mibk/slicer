package main

import (
	"bytes"
	"flag"
	"go/build"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testDir = "testdata"

func init() {
	flag.BoolVar(verbose, "debug", false, "debug")
}

func TestMain(t *testing.T) {
	flag.Parse()
	dir, err := os.Open(testDir)
	if err != nil {
		t.Fatal(err)
	}
	names, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".tmpl") {
			name := strings.TrimSuffix(name, ".tmpl")
			t.Run(name, func(t *testing.T) {
				testTmpl(name, t)
			})
		}
	}
}

func testTmpl(name string, t *testing.T) {
	gopath := filepath.Join(os.TempDir(), "slicer-test-dir")
	*workspace = filepath.Join(gopath, "src/workspace")
	if err := os.RemoveAll(gopath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(gopath, "src/cover/vendor/slicer/P"), 0755); err != nil {
		t.Fatal(err)
	}

	tmpl := filepath.Join(gopath, "src/cover/main.go")
	if err := copyFile(tmpl, filepath.Join(testDir, name+".tmpl")); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(filepath.Join(gopath, "src/cover/vendor/slicer/P/P.go"), filepath.Join(testDir, name+".input")); err != nil {
		t.Fatal(err)
	}
	build.Default.GOPATH = gopath
	slice(tmpl)

	wantFilename := filepath.Join(testDir, name+".want")
	want, err := ioutil.ReadFile(wantFilename)
	if err != nil {
		t.Fatal(err)
	}
	gotFilename := filepath.Join(*workspace, "src", dstPackage, "vendor/slicer/P/P.go")
	got, err := ioutil.ReadFile(gotFilename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s not equal to %s", gotFilename, wantFilename)
	}
}

func copyFile(dst, src string) error {
	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer df.Close()
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()
	_, err = io.Copy(df, sf)
	return err
}
