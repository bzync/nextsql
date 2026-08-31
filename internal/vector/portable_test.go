package vector

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestPortableProductionPath keeps architecture-specific acceleration behind an
// explicit review. Introducing unsafe, cgo, or assembly requires profiling,
// isolation, tests, fuzzing, and a measured win before this policy is changed.
// It covers the ANN package and the element-type codecs that feed it
// (float16 / int8vec / bitvec).
func TestPortableProductionPath(t *testing.T) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate vector package")
	}
	vectorDir := filepath.Dir(thisFile)
	internalDir := filepath.Dir(vectorDir)
	for _, dir := range []string{
		vectorDir,
		filepath.Join(internalDir, "float16"),
		filepath.Join(internalDir, "int8vec"),
		filepath.Join(internalDir, "bitvec"),
	} {
		assertPortableDir(t, dir)
	}
}

func assertPortableDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".s" || ext == ".asm" {
			t.Errorf("production vector assembly %s/%s requires profiling and policy review", filepath.Base(dir), name)
			continue
		}
		if ext != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			continue
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Errorf("parse import in %s: %v", path, err)
				continue
			}
			if importPath == "unsafe" || importPath == "C" {
				t.Errorf("production vector file %s imports %q; acceleration requires profiling and policy review", path, importPath)
			}
		}
	}
}
