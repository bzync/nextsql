package catalog

import (
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func simpleTable() *Table {
	return &Table{
		ID:   1,
		Name: "t",
		Columns: []Column{
			{Name: "id", Type: types.Int64(), Primary: true, NotNull: true},
			{Name: "n", Type: types.Int64()},
		},
		PK:        []int{0},
		CDCImages: CDCImagesKeys,
	}
}

func encodedVersion(t *testing.T, tab *Table) uint16 {
	t.Helper()
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	v, err := encoding.ReadU16(raw, 4)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	return v
}

// A descriptor is written at the oldest version that can express it. A database
// that declares no CHECK constraint must keep producing v13 bytes, so a release
// that predates v14 can still read it — the rollback window the upgrade fixtures
// enforce.
func TestTableWithoutChecksStaysAtV13(t *testing.T) {
	if got := encodedVersion(t, simpleTable()); got != 13 {
		t.Fatalf("a table with no CHECK encoded as v%d, want v13", got)
	}
}

func TestTableWithChecksMovesToV14(t *testing.T) {
	tab := simpleTable()
	tab.Checks = []Check{{Name: "pos", Expr: ast.Binary{Op: ">", Left: ast.Ident{Name: "n"}, Right: ast.Literal{Value: types.Int64Value(0)}}}}
	if got := encodedVersion(t, tab); got != 14 {
		t.Fatalf("a table with a CHECK encoded as v%d, want v14", got)
	}
}

func TestCheckRoundTrip(t *testing.T) {
	tab := simpleTable()
	tab.Checks = []Check{
		{Name: "pos", Expr: ast.Binary{Op: ">", Left: ast.Ident{Name: "n"}, Right: ast.Literal{Value: types.Int64Value(0)}}},
		{Name: "lt", Expr: ast.Binary{Op: "<", Left: ast.Ident{Name: "n"}, Right: ast.Literal{Value: types.Int64Value(100)}}},
	}
	raw, err := EncodeTable(tab)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeTable(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Checks) != 2 {
		t.Fatalf("decoded %d checks, want 2", len(got.Checks))
	}
	for i, c := range got.Checks {
		if c.Name != tab.Checks[i].Name {
			t.Fatalf("check %d name = %q, want %q", i, c.Name, tab.Checks[i].Name)
		}
		if !ExprEqual(c.Expr, tab.Checks[i].Expr) {
			t.Fatalf("check %d predicate did not round-trip: %s vs %s", i, FormatExpr(c.Expr), FormatExpr(tab.Checks[i].Expr))
		}
	}
}

func TestCheckEncodeRefusesInvalid(t *testing.T) {
	tab := simpleTable()
	tab.Checks = []Check{{Name: "", Expr: ast.Ident{Name: "n"}}}
	if _, err := EncodeTable(tab); err == nil {
		t.Fatal("encoded a check with no name")
	}
	tab.Checks = []Check{{Name: "c", Expr: nil}}
	if _, err := EncodeTable(tab); err == nil {
		t.Fatal("encoded a check with no predicate")
	}
	tab.Checks = nil
	for i := 0; i < MaxChecksPerTable+1; i++ {
		tab.Checks = append(tab.Checks, Check{Name: string(rune('a' + i)), Expr: ast.Ident{Name: "n"}})
	}
	if _, err := EncodeTable(tab); err == nil {
		t.Fatal("encoded more checks than the cap allows")
	}
}
