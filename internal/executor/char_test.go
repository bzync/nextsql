package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// TestCharVarcharInsertSelectRoundTrip covers D4 (Datatype expansion track):
// CHAR(n) space-padding, VARCHAR(n) length ceiling, PK usage, and catalog
// persist/reopen.
func TestCharVarcharInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (code CHAR(4) PRIMARY KEY, name VARCHAR(10))`)

	execOK(t, s, `INSERT INTO t (code, name) VALUES ('ab', 'alice')`)
	execOK(t, s, `INSERT INTO t (code, name) VALUES ('abcd', 'bob')`)

	got, err := s.Exec(`SELECT code, name FROM t WHERE code = 'ab'`)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	if row[0].Typ.Kind != types.KindChar || row[0].Str != "ab  " {
		t.Fatalf("CHAR not padded: %q (%+v)", row[0].Str, row[0].Typ)
	}
	if row[1].Typ.Kind != types.KindVarchar || row[1].Str != "alice" {
		t.Fatalf("VARCHAR mismatch: %q", row[1].Str)
	}

	// A CHAR literal compared to the padded stored value must match.
	if r, err := s.Exec(`SELECT name FROM t WHERE code = 'ab  '`); err != nil || len(r.Rows) != 1 {
		t.Fatalf("padded-literal equality: %v rows=%d", err, len(r.Rows))
	}

	// VARCHAR ceiling is enforced.
	if _, err := s.Exec(`INSERT INTO t (code, name) VALUES ('zz', 'this-is-far-too-long')`); err == nil {
		t.Fatal("expected VARCHAR(10) overflow to be rejected")
	}
	// CHAR overflow with real content is rejected.
	if _, err := s.Exec(`INSERT INTO t (code, name) VALUES ('abcde', 'x')`); err == nil {
		t.Fatal("expected CHAR(4) overflow to be rejected")
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
	after, err := rs.Exec(`SELECT code, name FROM t WHERE code = 'abcd'`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][0].Str != "abcd" || after.Rows[0][0].Typ.Precision != 4 {
		t.Fatalf("CHAR type/precision did not survive restart: %+v", after.Rows[0][0])
	}
	if after.Rows[0][1].Typ.Precision != 10 {
		t.Fatalf("VARCHAR precision did not survive restart: %+v", after.Rows[0][1])
	}
}

// TestCharOrderBy covers CHAR(n)'s index-key ordering: comparison is over the
// already-space-padded stored form (docs/design-datatypes.md D4).
func TestCharOrderBy(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, v CHAR(3))`)
	vals := []string{"b", "ab", "a", "abc", "ba"}
	for i, v := range vals {
		execOK(t, s, "INSERT INTO t (id, v) VALUES ("+intLit(i+1)+", '"+v+"')")
	}
	got, err := s.Exec(`SELECT v FROM t ORDER BY v ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a  ", "ab ", "abc", "b  ", "ba "}
	if len(got.Rows) != len(want) {
		t.Fatalf("row count: %d", len(got.Rows))
	}
	for i, row := range got.Rows {
		if row[0].Str != want[i] {
			t.Fatalf("ORDER BY CHAR wrong at %d: %q want %q", i, row[0].Str, want[i])
		}
	}
}

// TestCharStringFunctions covers CHAR/VARCHAR flowing through the string
// builtins as plain STRING, with CHAR padding not significant.
func TestCharStringFunctions(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, c CHAR(6), v VARCHAR(6))`)
	execOK(t, s, `INSERT INTO t (id, c, v) VALUES (1, 'Ab', 'Cd')`)

	got, err := s.Exec(`SELECT lower(c), length(c), upper(v), concat(c, v) FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Rows[0]
	if r[0].Str != "ab" {
		t.Fatalf("lower(CHAR): %q", r[0].Str)
	}
	if r[1].Dec.String() != "2" {
		t.Fatalf("length(CHAR) ignores padding: %s", r[1].Dec.String())
	}
	if r[2].Str != "CD" {
		t.Fatalf("upper(VARCHAR): %q", r[2].Str)
	}
	if r[3].Str != "AbCd" {
		t.Fatalf("concat: %q", r[3].Str)
	}
}

// TestCharForeignKey covers CHAR(n) as an ordinary FK-eligible scalar
// (same declared width on both sides).
func TestCharForeignKey(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parent (code CHAR(3) PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE child (id INT64 PRIMARY KEY, pc CHAR(3) NOT NULL REFERENCES parent(code))`)
	execOK(t, s, `INSERT INTO parent (code) VALUES ('us')`)
	execOK(t, s, `INSERT INTO child (id, pc) VALUES (1, 'us')`)
	if _, err := s.Exec(`INSERT INTO child (id, pc) VALUES (2, 'zz')`); err == nil {
		t.Fatal("expected FK violation for unknown parent code")
	}
}

// TestCharVarcharNotEncryptedClient documents that CHAR/VARCHAR are
// deliberately excluded from the ENCRYPTED CLIENT allow-list for now (their
// PADSPACE semantics under client-side encryption are unspecified) —
// docs/design-datatypes.md D4.
func TestCharVarcharNotEncryptedClient(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE t (id STRING PRIMARY KEY, c CHAR(4) ENCRYPTED CLIENT)`); err == nil {
		t.Fatal("expected CHAR ENCRYPTED CLIENT to be rejected")
	}
	if _, err := s.Exec(`CREATE TABLE t2 (id STRING PRIMARY KEY, v VARCHAR(4) ENCRYPTED CLIENT)`); err == nil {
		t.Fatal("expected VARCHAR ENCRYPTED CLIENT to be rejected")
	}
}

// TestCharParamRoundTrip covers CHAR/VARCHAR values passed as bound
// parameters (protocol value path) and returned in results.
func TestCharParamRoundTrip(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, v CHAR(5))`)
	ct, _ := types.CharType(5)
	pv, err := types.Coerce(types.StringValue("hi"), ct)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t (id, v) VALUES (1, $1)`,
		[]Param{{Value: pv}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Exec(`SELECT v FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Str != "hi   " {
		t.Fatalf("param round trip: %q", got.Rows[0][0].Str)
	}
}
