package ops

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestOpsImportsNoEnginePackages asserts Operations mode never directly
// imports the storage engine internals. It must reach server state only
// through the official driver + NSQL protocol; a direct import of these
// would be a way to read raw pages / WAL / catalog / keys, which
// PROJECT.md forbids.
func TestOpsImportsNoEnginePackages(t *testing.T) {
	forbidden := []string{
		"internal/storage",
		"internal/wal",
		"internal/undo",
		"internal/recovery",
		"internal/catalog",
		"internal/crypto",
		"internal/txn",
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
					t.Errorf("%s imports %s — Operations mode must not touch the storage engine directly", name, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no ops source files were scanned")
	}
}
