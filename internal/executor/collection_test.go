package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// TestStructInsertSelectRoundTrip covers the Collections track C1: a
// STRUCT<...> column, constructor INSERT, field access in SELECT/WHERE,
// ORDER BY on the whole struct, and catalog persist/reopen of the recursive
// descriptor.
func TestStructInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, addr STRUCT<street TEXT, zip INT32>)`)
	execOK(t, s, `INSERT INTO t (id, addr) VALUES (1, STRUCT('Main St' AS street, 90210 AS zip))`)
	execOK(t, s, `INSERT INTO t (id, addr) VALUES (2, STRUCT('2nd Ave' AS street, 10001 AS zip))`)

	got, err := s.Exec(`SELECT addr.street, addr.zip FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Str != "Main St" || got.Rows[0][1].Int != 90210 {
		t.Fatalf("field access: %+v", got.Rows[0])
	}

	// WHERE on a nested field.
	got, err = s.Exec(`SELECT id FROM t WHERE addr.zip = 10001`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Int != 2 {
		t.Fatalf("WHERE on struct field: %+v", got.Rows)
	}

	// ORDER BY the whole struct (lexicographic: '2nd Ave' < 'Main St').
	got, err = s.Exec(`SELECT id FROM t ORDER BY addr`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || got.Rows[0][0].Int != 2 || got.Rows[1][0].Int != 1 {
		t.Fatalf("ORDER BY struct: %+v", got.Rows)
	}

	// Whole-struct select returns the typed collection value.
	got, err = s.Exec(`SELECT addr FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Typ.Kind != types.KindStruct || len(got.Rows[0][0].Coll) != 2 {
		t.Fatalf("whole struct select: %+v", got.Rows[0][0])
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rs := reopened.Session()
	after, err := rs.Exec(`SELECT addr.zip FROM t WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][0].Int != 10001 {
		t.Fatalf("STRUCT did not survive restart: %+v", after.Rows[0])
	}
}

func TestArrayAndMapInsertSelect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, tags ARRAY<TEXT>, scores MAP<TEXT, INT32>)`)
	execOK(t, s, `INSERT INTO t (id, tags, scores) VALUES (1, ARRAY('a', 'b', 'c'), MAP('x', 1, 'y', 2))`)

	got, err := s.Exec(`SELECT cardinality(tags), element_at(tags, 2), element_at(scores, 'y'), map_contains_key(scores, 'z') FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Rows[0]
	if r[0].Int != 3 || r[1].Str != "b" || r[2].Int != 2 || r[3].Bool {
		t.Fatalf("collection fns: %+v", r)
	}

	got, err = s.Exec(`SELECT id FROM t WHERE array_contains(tags, 'b')`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("array_contains WHERE: %+v", got.Rows)
	}
}
