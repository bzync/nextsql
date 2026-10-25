package docs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/system"
)

// Studio must not advertise grammar the native parser cannot accept. Keep
// executable examples with commands so checks don't mistake incomplete SQL
// for an unsupported feature. These examples are never executed by Studio.
func TestStudioCompletionCommandsParse(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/admin/frontend/src/studio/sqlCommands.json"))
	if err != nil {
		t.Fatal(err)
	}
	var commands []struct{ Label, Sample string }
	if err := json.Unmarshal(b, &commands); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, command := range commands {
		if seen[command.Label] {
			t.Errorf("duplicate command %s", command.Label)
		}
		seen[command.Label] = true
		if _, err := parser.Parse(command.Sample); err != nil {
			t.Errorf("Studio advertises %s, but its example fails: %v", command.Label, err)
		}
	}
	for keyword := range statements {
		found := false
		for label := range seen {
			found = found || strings.Fields(label)[0] == keyword
		}
		if !found {
			t.Errorf("Studio lacks the documented %s statement", keyword)
		}
	}
}

func TestStudioCompletionTypesParseAndCoverNativeTypes(t *testing.T) {
	source := readDoc(t, "internal/admin/frontend/src/studio/resultTools.ts")
	_, tail, ok := strings.Cut(source, "export const NEXTSQL_TYPE_SUGGESTIONS:")
	if !ok {
		t.Fatal("Studio type suggestion list missing")
	}
	list, _, ok := strings.Cut(tail, "];")
	if !ok {
		t.Fatal("Studio type suggestion list unterminated")
	}
	seen := map[string]bool{}
	for _, match := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(list, -1) {
		spelling := match[1]
		if _, err := parser.Parse("CREATE TABLE t (c " + spelling + ")"); err != nil {
			t.Errorf("Studio offers invalid type %s: %v", spelling, err)
		}
		name := spelling
		if i := strings.IndexAny(name, "(<"); i >= 0 {
			name = name[:i]
		}
		seen[name] = true
	}
	for name := range columnTypes {
		if !seen[name] {
			t.Errorf("Studio lacks native column type %s", name)
		}
	}
}

func TestStudioCompletionNativeInventory(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/admin/frontend/src/studio/sqlReference.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Functions    []string `json:"functions"`
		SystemTables []string `json:"systemTables"`
	}
	if err := json.Unmarshal(b, &inventory); err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (*catalog.Table, bool) { return nil, false }
	for _, name := range inventory.Functions {
		sample := "SELECT " + name + "()"
		if name == "cast" {
			sample = "SELECT CAST(1 AS INT64)"
		}
		stmt, err := parser.Parse(sample)
		if err != nil {
			t.Errorf("Studio offers %s(), but call syntax fails: %v", name, err)
			continue
		}
		// Wrong arity/type/context is expected for an empty call. Unknown
		// functions are not: this protects the actual binder contract.
		if _, err := binder.Bind(stmt, lookup, 1); err != nil && strings.Contains(err.Error(), "unknown function") {
			t.Errorf("Studio offers an unknown built-in %s: %v", name, err)
		}
	}
	if strings.Join(inventory.SystemTables, ",") != strings.Join(system.List(), ",") {
		t.Fatal("Studio system-table inventory differs from the native registry")
	}
}
