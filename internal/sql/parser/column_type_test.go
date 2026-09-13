package parser

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func oneColumnType(t *testing.T, spelling string) types.Type {
	t.Helper()
	stmt, err := Parse("CREATE TABLE t (c " + spelling + ")")
	if err != nil {
		t.Fatalf("%s: %v", spelling, err)
	}
	ct, ok := stmt.(ast.CreateTable)
	if !ok || len(ct.Columns) != 1 {
		t.Fatalf("%s did not parse as a one-column CREATE TABLE: %#v", spelling, stmt)
	}
	return ct.Columns[0].Type
}

// BOOL is a declarable column type, spelled one way.
func TestParseBoolColumnType(t *testing.T) {
	for _, spelling := range []string{"BOOL", "bool", "Bool"} {
		if got := oneColumnType(t, spelling); !got.Equals(types.Bool()) {
			t.Fatalf("%s parsed as %s, want BOOL", spelling, got.String())
		}
	}
	if _, err := Parse("CREATE TABLE t (c BOOLEAN)"); err == nil {
		t.Fatal("BOOLEAN parsed; the dialect spells the type BOOL only")
	}
	if _, err := Parse("CREATE TABLE t (bool INT64)"); err == nil {
		t.Fatal("bare bool parsed as an identifier; it is a type keyword")
	}
	if _, err := Parse(`CREATE TABLE t ("bool" INT64)`); err != nil {
		t.Fatalf("quoted \"bool\" identifier: %v", err)
	}
}

// A general spatial column that accepts any subtype but carries an SRID is
// rendered with the subtype spelled Geometry -- every plain GEOGRAPHY column,
// whose SRID defaults to WGS84, renders that way. The grammar has to accept
// what the renderer emits or the column cannot be exported and restored.
func TestParseAnySubtypeGeoColumnRoundTrips(t *testing.T) {
	for _, spelling := range []string{"GEOGRAPHY", "GEOMETRY", "GEOMETRY(Point, 3857)", "GEOGRAPHY(Polygon)"} {
		declared := oneColumnType(t, spelling)
		back := oneColumnType(t, declared.String())
		if !back.Equals(declared) {
			t.Fatalf("%s renders as %q, which parses back as %s", spelling, declared.String(), back.String())
		}
	}
	any := oneColumnType(t, "GEOGRAPHY(geometry, 4326)")
	if any.Kind != types.KindGeography || any.Scale != types.GeomSubAny || any.Precision != types.SRIDWGS84 {
		t.Fatalf("GEOGRAPHY(geometry, 4326) parsed as %+v, want any subtype at 4326", any)
	}
}
