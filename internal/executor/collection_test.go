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

func TestSubscriptSugar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()

	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, tags ARRAY<TEXT>, scores MAP<TEXT, INT32>, matrix ARRAY<ARRAY<INT32>>)`)
	execOK(t, s, `INSERT INTO t (id, tags, scores, matrix) VALUES (1, ARRAY('alpha', 'beta', 'gamma'), MAP('math', 95, 'eng', 88), ARRAY(ARRAY(11, 12), ARRAY(21, 22)))`)

	// 1-based array indexing and map string key indexing in SELECT
	got, err := s.Exec(`SELECT tags[1], tags[2], tags[3], scores['math'], scores['eng'], matrix[2][1] FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Rows[0]
	if r[0].Str != "alpha" || r[1].Str != "beta" || r[2].Str != "gamma" {
		t.Fatalf("array subscript error: %v, %v, %v", r[0], r[1], r[2])
	}
	if r[3].Int != 95 || r[4].Int != 88 {
		t.Fatalf("map subscript error: %v, %v", r[3], r[4])
	}
	if r[5].Int != 21 {
		t.Fatalf("nested matrix subscript error: %v", r[5])
	}

	// Subscript in WHERE
	got, err = s.Exec(`SELECT id FROM t WHERE tags[2] = 'beta' AND scores['math'] > 90`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Int != 1 {
		t.Fatalf("subscript in WHERE error: %+v", got.Rows)
	}

	// Out of bounds returns NULL
	got, err = s.Exec(`SELECT tags[99], scores['nonexistent'] FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Rows[0][0].Null || !got.Rows[0][1].Null {
		t.Fatalf("out-of-bounds subscript should be NULL: %+v", got.Rows[0])
	}
}

func TestArrayAggAndMapAggSQL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()

	execOK(t, s, `CREATE TABLE employees (id INT64 PRIMARY KEY, dept TEXT, name TEXT, salary INT32)`)
	execOK(t, s, `INSERT INTO employees VALUES (1, 'eng', 'alice', 100), (2, 'eng', 'bob', 120), (3, 'sales', 'carol', 110)`)

	// Ungrouped ARRAY_AGG
	got, err := s.Exec(`SELECT ARRAY_AGG(name) FROM employees`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || len(got.Rows[0][0].Coll) != 3 {
		t.Fatalf("ungrouped ARRAY_AGG error: %+v", got.Rows)
	}

	// Grouped ARRAY_AGG and MAP_AGG
	got, err = s.Exec(`SELECT dept, ARRAY_AGG(salary), MAP_AGG(name, salary) FROM employees GROUP BY dept ORDER BY dept`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("grouped agg error, expected 2 rows, got %d", len(got.Rows))
	}
	// 'eng'
	if got.Rows[0][0].Str != "eng" {
		t.Fatalf("expected eng, got %s", got.Rows[0][0].Str)
	}
	engArr := got.Rows[0][1]
	if len(engArr.Coll) != 2 || engArr.Coll[0].Int != 100 || engArr.Coll[1].Int != 120 {
		t.Fatalf("eng salaries: %+v", engArr.Coll)
	}
	engMap := got.Rows[0][2]
	if len(engMap.CollKeys) != 2 {
		t.Fatalf("eng map keys count: %d", len(engMap.CollKeys))
	}

	// Empty table returns NULL
	execOK(t, s, `CREATE TABLE empty_tab (id INT64 PRIMARY KEY, v INT32)`)
	got, err = s.Exec(`SELECT ARRAY_AGG(v), MAP_AGG(id, v) FROM empty_tab`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || !got.Rows[0][0].Null || !got.Rows[0][1].Null {
		t.Fatalf("empty table aggregate should return NULLs, got: %+v", got.Rows)
	}

	// Duplicate MAP_AGG key fails
	execOK(t, s, `CREATE TABLE dups (id INT64 PRIMARY KEY, k TEXT, v INT32)`)
	execOK(t, s, `INSERT INTO dups VALUES (1, 'x', 1), (2, 'x', 2)`)
	if _, err := s.Exec(`SELECT MAP_AGG(k, v) FROM dups`); err == nil {
		t.Fatal("expected error on duplicate MAP_AGG key, got nil")
	}

	// NULL MAP_AGG key fails
	execOK(t, s, `CREATE TABLE nullkeys (id INT64 PRIMARY KEY, k TEXT, v INT32)`)
	execOK(t, s, `INSERT INTO nullkeys VALUES (1, NULL, 1)`)
	if _, err := s.Exec(`SELECT MAP_AGG(k, v) FROM nullkeys`); err == nil {
		t.Fatal("expected error on NULL MAP_AGG key, got nil")
	}
}

func TestUnnestInFrom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()

	// 1. UNNEST ARRAY
	got, err := s.Exec(`SELECT v FROM UNNEST(ARRAY(10, 20, 30)) AS u(v)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 3 {
		t.Fatalf("expected 3 rows from UNNEST array, got %d", len(got.Rows))
	}
	if got.Rows[0][0].Dec.String() != "10" || got.Rows[1][0].Dec.String() != "20" || got.Rows[2][0].Dec.String() != "30" {
		t.Fatalf("unexpected unnested values: %+v", got.Rows)
	}

	// 2. UNNEST ARRAY WITH OFFSET
	got, err = s.Exec(`SELECT v, off FROM UNNEST(ARRAY('first', 'second')) AS u(v) WITH OFFSET AS off`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got.Rows))
	}
	if got.Rows[0][0].Str != "first" || got.Rows[0][1].Int != 1 {
		t.Fatalf("row 0 mismatch: %+v", got.Rows[0])
	}
	if got.Rows[1][0].Str != "second" || got.Rows[1][1].Int != 2 {
		t.Fatalf("row 1 mismatch: %+v", got.Rows[1])
	}

	// 3. UNNEST MAP
	got, err = s.Exec(`SELECT k, v FROM UNNEST(MAP('a', 100, 'b', 200)) AS u(k, v) ORDER BY k`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("expected 2 rows from UNNEST map, got %d", len(got.Rows))
	}
	if got.Rows[0][0].Str != "a" || got.Rows[0][1].Dec.String() != "100" {
		t.Fatalf("map row 0 mismatch: %+v", got.Rows[0])
	}
	if got.Rows[1][0].Str != "b" || got.Rows[1][1].Dec.String() != "200" {
		t.Fatalf("map row 1 mismatch: %+v", got.Rows[1])
	}

	// 4. Filtering, Sorting, Limiting UNNEST
	got, err = s.Exec(`SELECT v FROM UNNEST(ARRAY(5, 1, 9, 3)) AS u(v) WHERE v >= 3 ORDER BY v LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || got.Rows[0][0].Dec.String() != "3" || got.Rows[1][0].Dec.String() != "5" {
		t.Fatalf("filter/sort/limit on UNNEST failed: %+v", got.Rows)
	}

	// 5. Combining UNNEST with ARRAY_AGG
	got, err = s.Exec(`SELECT ARRAY_AGG(v) FROM UNNEST(ARRAY(1, 2, 3)) AS u(v) WHERE v > 1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || len(got.Rows[0][0].Coll) != 2 {
		t.Fatalf("combining UNNEST and ARRAY_AGG failed: %+v", got.Rows)
	}
}

