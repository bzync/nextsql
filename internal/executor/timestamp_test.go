package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// TestPlainTimestampInsertSelectRoundTrip covers D7 (Datatype expansion
// track): plain TIMESTAMP (no timezone) CRUD via string coercion, PK usage,
// index ordering, and catalog persist/reopen.
func TestPlainTimestampInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE ev (id INT64 PRIMARY KEY, ts TIMESTAMP)`)
	execOK(t, s, `INSERT INTO ev (id, ts) VALUES (1, '2024-01-15 13:45:00')`)
	execOK(t, s, `INSERT INTO ev (id, ts) VALUES (2, '2024-01-15T09:00:00.25')`)
	execOK(t, s, `INSERT INTO ev (id, ts) VALUES (3, '1999-12-31')`)

	got, err := s.Exec(`SELECT ts FROM ev WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Typ.Kind != types.KindTimestamp || got.Rows[0][0].String() != "2024-01-15 13:45:00" {
		t.Fatalf("unexpected: %+v", got.Rows[0][0])
	}

	// A tz-qualified literal must be rejected for a plain TIMESTAMP column.
	if _, err := s.Exec(`INSERT INTO ev (id, ts) VALUES (4, '2024-01-15T13:45:00Z')`); err == nil {
		t.Fatal("expected offset-qualified text to be rejected")
	}

	ordered, err := s.Exec(`SELECT id FROM ev ORDER BY ts ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{3, 2, 1}
	for i, row := range ordered.Rows {
		if row[0].Int != want[i] {
			t.Fatalf("ORDER BY TIMESTAMP wrong at %d: %d want %d", i, row[0].Int, want[i])
		}
	}

	if _, err := s.Exec(`SELECT ts + ts FROM ev WHERE id = 1`); err == nil {
		t.Fatal("expected TIMESTAMP arithmetic to be rejected")
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.Session().Exec(`SELECT ts FROM ev WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][0].String() != "2024-01-15 09:00:00.25" {
		t.Fatalf("did not survive restart: %q", after.Rows[0][0].String())
	}
}

// TestPlainTimestampForeignKeyAndMinMax covers TIMESTAMP as an ordinary
// FK-eligible scalar and MIN/MAX via the generic Value.Cmp path.
func TestPlainTimestampForeignKeyAndMinMax(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parent (ts TIMESTAMP PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE child (id INT64 PRIMARY KEY, p TIMESTAMP NOT NULL REFERENCES parent(ts))`)
	execOK(t, s, `INSERT INTO parent (ts) VALUES ('2024-01-01 00:00:00')`)
	execOK(t, s, `INSERT INTO parent (ts) VALUES ('2024-06-01 12:00:00')`)
	execOK(t, s, `INSERT INTO child (id, p) VALUES (1, '2024-01-01 00:00:00')`)
	if _, err := s.Exec(`INSERT INTO child (id, p) VALUES (2, '2020-01-01 00:00:00')`); err == nil {
		t.Fatal("expected FK violation")
	}
	got, err := s.Exec(`SELECT MIN(ts), MAX(ts) FROM parent`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].String() != "2024-01-01 00:00:00" || got.Rows[0][1].String() != "2024-06-01 12:00:00" {
		t.Fatalf("MIN/MAX: %+v", got.Rows[0])
	}
	if _, err := s.Exec(`SELECT SUM(ts) FROM parent`); err == nil {
		t.Fatal("expected SUM(TIMESTAMP) to be rejected")
	}
}
