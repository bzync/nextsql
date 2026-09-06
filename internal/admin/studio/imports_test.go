package studio

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestStudioImportsNoEnginePackages pins the client boundary: Studio may use
// protocol value types and the official driver adapter in Operations mode,
// but it must never gain direct access to durable engine internals.
func TestStudioImportsNoEnginePackages(t *testing.T) {
	forbidden := []string{
		"internal/storage", "internal/wal", "internal/undo", "internal/recovery",
		"internal/catalog", "internal/crypto", "internal/txn", "internal/executor",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if path == "github.com/bzync/nextsql/"+bad || strings.HasPrefix(path, "github.com/bzync/nextsql/"+bad+"/") {
					t.Errorf("%s imports %s — Studio must use official interfaces", name, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Studio source files were scanned")
	}
}
