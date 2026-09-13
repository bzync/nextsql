// Column types are the second surface written out in all four documents, and
// they drift the same way statements do. Log #283 found the sharpest form of
// that drift: the engine had carried a BOOL value kind since the beginning --
// encoded on disk, ordered in index keys, returned by every comparison -- but
// no CREATE TABLE could declare one, so a derived BOOL column's own canonical
// DDL would not re-parse. A type the engine has and the dialect cannot spell is
// invisible to a reader of any of the four documents, so these tests derive the
// type surface from the parser too.
package docs

import (
	"regexp"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog/ddl"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/types"
)

// columnTypes maps each declarable column type to the spelling a CREATE TABLE
// uses for it. A parameterized type is given minimal arguments; the point is
// the type name, not the arguments.
var columnTypes = map[string]string{
	"UUID":         "UUID",
	"STRING":       "STRING",
	"TEXT":         "TEXT",
	"BLOB":         "BLOB",
	"CHAR":         "CHAR(4)",
	"VARCHAR":      "VARCHAR(8)",
	"ENUM":         "ENUM('a', 'b')",
	"BOOL":         "BOOL",
	"INT8":         "INT8",
	"INT16":        "INT16",
	"INT32":        "INT32",
	"INT64":        "INT64",
	"UINT8":        "UINT8",
	"UINT16":       "UINT16",
	"UINT32":       "UINT32",
	"UINT64":       "UINT64",
	"FLOAT32":      "FLOAT32",
	"FLOAT64":      "FLOAT64",
	"DECIMAL":      "DECIMAL(10,2)",
	"DATE":         "DATE",
	"TIME":         "TIME",
	"TIMESTAMP":    "TIMESTAMP",
	"TIMESTAMPTZ":  "TIMESTAMPTZ",
	"INTERVAL":     "INTERVAL",
	"JSON":         "JSON",
	"STRUCT":       "STRUCT<a INT64>",
	"ARRAY":        "ARRAY<INT64>",
	"MAP":          "MAP<STRING, INT64>",
	"VECTOR":       "VECTOR<F32, 3>",
	"BITVECTOR":    "BITVECTOR<8>",
	"SPARSEVECTOR": "SPARSEVECTOR<8>",
	"POINT":        "POINT",
	"BOX":          "BOX",
	"LINESTRING":   "LINESTRING",
	"POLYGON":      "POLYGON",
	"GEOMETRY":     "GEOMETRY",
	"GEOGRAPHY":    "GEOGRAPHY",
}

// declaredType parses a one-column CREATE TABLE and returns the column's type.
func declaredType(t *testing.T, spelling string) (types.Type, bool) {
	t.Helper()
	stmt, err := parser.Parse("CREATE TABLE t (c " + spelling + ")")
	if err != nil {
		t.Errorf("%s is documented as a column type but does not parse: %v", spelling, err)
		return types.Type{}, false
	}
	ct, ok := stmt.(ast.CreateTable)
	if !ok || len(ct.Columns) != 1 {
		t.Errorf("%s did not parse as a one-column CREATE TABLE", spelling)
		return types.Type{}, false
	}
	return ct.Columns[0].Type, true
}

// Every documented column type must actually parse as one.
func TestDocumentedTypesParse(t *testing.T) {
	for _, spelling := range columnTypes {
		declaredType(t, spelling)
	}
}

// typeFamilySpelling records the compact family spelling a document may use
// instead of naming each width on its own: the agent reference writes
// `INT8/16/32/64` in one table cell, which names INT16 to a reader even though
// the word "INT16" never appears.
var typeFamilySpelling = map[string]string{
	"INT16": "INT8/16/32/64", "INT32": "INT8/16/32/64", "INT64": "INT8/16/32/64",
	"UINT16": "UINT8/16/32/64", "UINT32": "UINT8/16/32/64", "UINT64": "UINT8/16/32/64",
}

// Every column type the parser accepts must be named by every document that
// enumerates the type surface. The match is whole-word: a substring scan would
// let "UINT8" satisfy "INT8" and "BOOLEAN" satisfy "BOOL", which is exactly the
// drift these tests exist to catch.
func TestEveryTypeIsDocumented(t *testing.T) {
	for _, doc := range statementDocs {
		body := strings.ToUpper(readDoc(t, doc))
		for name := range columnTypes {
			if regexp.MustCompile(`\b` + name + `\b`).MatchString(body) {
				continue
			}
			if family, ok := typeFamilySpelling[name]; ok && strings.Contains(body, family) {
				continue
			}
			t.Errorf("%s does not mention the %s column type", doc, name)
		}
	}
}

// A word the list does not claim must not quietly become a column type: if one
// of these starts parsing, it is a new surface that needs documenting in all
// four places.
func TestUndocumentedWordsAreNotTypes(t *testing.T) {
	for _, word := range []string{"BOOLEAN", "INT", "BIGINT", "SERIAL", "MONEY", "BYTEA", "NUMERIC", "REAL", "DOUBLE"} {
		if _, err := parser.Parse("CREATE TABLE t (c " + word + ")"); err == nil {
			t.Errorf("%s now parses as a column type: add it to `columnTypes` and to every document in statementDocs", word)
		}
	}
}

// Every declarable type must survive the round trip through the DDL renderer
// the system catalog and logical export use, or a table holding it could not be
// exported and restored. This is the check log #283 built into CREATE TABLE AS,
// made general: it is a property of the type surface, not of one statement.
func TestEveryTypeRoundTripsThroughDDL(t *testing.T) {
	for name, spelling := range columnTypes {
		declared, ok := declaredType(t, spelling)
		if !ok {
			continue
		}
		rendered := ddl.SQLType(declared)
		back, ok := declaredType(t, rendered)
		if !ok {
			t.Errorf("%s renders as %q, which does not parse", name, rendered)
			continue
		}
		if !back.Equals(declared) {
			t.Errorf("%s renders as %q, which parses back as %s", name, rendered, back.String())
		}
	}
}

// Every value kind the engine defines must have a declarable spelling, or be
// listed here with the reason it has none. A kind with neither is the log #283
// defect: storable, queryable, and impossible to declare or restore.
func TestEveryValueKindIsDeclarableOrExplained(t *testing.T) {
	undeclarable := map[types.Kind]string{
		types.KindInvalid: "the zero value, not a type",
		types.KindNull:    "the type of the bare NULL literal; no column has it",
	}
	declarable := map[types.Kind]bool{}
	for _, spelling := range columnTypes {
		if declared, ok := declaredType(t, spelling); ok {
			declarable[declared.Kind] = true
		}
	}
	for k := types.KindInvalid; k <= types.KindGeography; k++ {
		if declarable[k] {
			continue
		}
		if _, explained := undeclarable[k]; explained {
			continue
		}
		t.Errorf("value kind %s (%d) has no declarable column type and no recorded reason", k.String(), k)
	}
}
