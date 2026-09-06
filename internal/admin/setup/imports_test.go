package setup

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSetupImportsNoEnginePackages asserts Setup mode never directly
// imports the storage engine, or even internal/setup / internal/sysinfo —
// every effect must go through a subprocess call to the already-tested
// `nextsql` binary (see docs/design-admin.md §1). A direct import of
// any of these would defeat the whole point of the subprocess boundary: the
// GUI process holding no key material because it never can.
func TestSetupImportsNoEnginePackages(t *testing.T) {
	forbidden := []string{
		"internal/storage",
		"internal/wal",
		"internal/undo",
		"internal/recovery",
		"internal/catalog",
		"internal/crypto",
		"internal/txn",
		"internal/executor",
		"internal/setup",
		"internal/sysinfo",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if path == "github.com/bzync/nextsql/"+bad || strings.HasPrefix(path, "github.com/bzync/nextsql/"+bad+"/") {
					t.Errorf("%s imports %s — Setup mode must drive nextsql as a subprocess, never link the engine directly", name, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no setup source files were scanned")
	}
}
