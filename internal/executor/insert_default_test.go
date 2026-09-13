package executor

import (
	"context"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// A column default fills a column the statement did not name. A NULL the
// statement supplied -- as a literal, a bound parameter or a query value -- is
// a value: stored as NULL, or refused by NOT NULL. INSERT used to fill every
// NULL cell from the default, so a driver binding NULL got the default stored
// and a logical export/import replaced stored NULLs with defaults.

func defaultsSession(t *testing.T) *Session {
	t.Helper()
	s := testDB(t).Session()
	execOK(t, s, `CREATE TABLE d (id INT64 PRIMARY KEY, note STRING DEFAULT 'unset', qty INT64 NOT NULL DEFAULT 7, seen TIMESTAMPTZ DEFAULT NOW())`)
	return s
}

func defaultRow(t *testing.T, s *Session, sql string) []types.Value {
	t.Helper()
	res := execOK(t, s, sql)
	if len(res.Rows) != 1 {
		t.Fatalf("%s returned %d rows", sql, len(res.Rows))
	}
	return res.Rows[0]
}

func TestOmittedColumnTakesItsDefault(t *testing.T) {
	s := defaultsSession(t)
	execOK(t, s, `INSERT INTO d (id) VALUES (1)`)
	row := defaultRow(t, s, `SELECT note, qty, seen FROM d WHERE id = 1`)
	if row[0].Str != "unset" || row[1].Int != 7 || row[2].Null {
		t.Fatalf("omitted columns = %v, want their defaults", row)
	}
}

func TestExplicitNullIsStoredNotDefaulted(t *testing.T) {
	s := defaultsSession(t)
	execOK(t, s, `INSERT INTO d (id, note, qty, seen) VALUES (1, NULL, 3, NULL)`)
	row := defaultRow(t, s, `SELECT note, seen FROM d WHERE id = 1`)
	if !row[0].Null || !row[1].Null {
		t.Fatalf("explicit NULLs stored as %v, want NULL", row)
	}
	// The same through a bound parameter, which is how every driver sends it.
	if _, err := s.ExecContext(context.Background(), `INSERT INTO d (id, note, qty) VALUES ($1, $2, $3)`,
		[]Param{{Value: types.Int64Value(2)}, {Value: types.Null(types.String())}, {Value: types.Int64Value(1)}}); err != nil {
		t.Fatal(err)
	}
	if row := defaultRow(t, s, `SELECT note FROM d WHERE id = 2`); !row[0].Null {
		t.Fatalf("NULL parameter stored as %v, want NULL", row[0])
	}
}

func TestExplicitNullIntoNotNullWithDefaultIsRefused(t *testing.T) {
	s := defaultsSession(t)
	if _, err := s.Exec(`INSERT INTO d (id, qty) VALUES (1, NULL)`); err == nil {
		t.Fatal("NULL into NOT NULL DEFAULT 7 was accepted (the default was substituted)")
	}
	if _, err := s.ExecContext(context.Background(), `INSERT INTO d (id, qty) VALUES ($1, $2)`,
		[]Param{{Value: types.Int64Value(2)}, {Value: types.Null(types.Int64())}}); err == nil {
		t.Fatal("NULL parameter into NOT NULL DEFAULT 7 was accepted")
	}
	if _, err := s.Exec(`UPSERT INTO d (id, qty) VALUES (3, NULL)`); err == nil {
		t.Fatal("UPSERT of NULL into NOT NULL DEFAULT 7 was accepted")
	}
	execOK(t, s, `CREATE TABLE src (id INT64 PRIMARY KEY, q INT64)`)
	execOK(t, s, `INSERT INTO src VALUES (4, NULL)`)
	if _, err := s.Exec(`INSERT INTO d (id, qty) SELECT id, q FROM src`); err == nil {
		t.Fatal("INSERT ... SELECT of NULL into NOT NULL DEFAULT 7 was accepted")
	}
}

func TestQuerySourcedNullIsStored(t *testing.T) {
	s := defaultsSession(t)
	execOK(t, s, `CREATE TABLE src (id INT64 PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO src VALUES (1, NULL)`)
	execOK(t, s, `INSERT INTO d (id, note) SELECT id, n FROM src`)
	row := defaultRow(t, s, `SELECT note, qty FROM d WHERE id = 1`)
	if !row[0].Null || row[1].Int != 7 {
		t.Fatalf("INSERT ... SELECT stored %v, want NULL note and the qty default", row)
	}
}

func TestUpsertNullIsStored(t *testing.T) {
	s := defaultsSession(t)
	execOK(t, s, `UPSERT INTO d (id, note) VALUES (1, NULL)`)
	if row := defaultRow(t, s, `SELECT note, qty FROM d WHERE id = 1`); !row[0].Null || row[1].Int != 7 {
		t.Fatalf("UPSERT stored %v, want NULL note and the qty default", row)
	}
}

// AI() still requests the next value, and an explicit value still advances the
// counter; only an explicit NULL changed meaning.
func TestAIDefaultSemantics(t *testing.T) {
	s := testDB(t).Session()
	execOK(t, s, `CREATE TABLE a (id DECIMAL(10,0) PRIMARY KEY DEFAULT AI(), v STRING)`)
	execOK(t, s, `INSERT INTO a (v) VALUES ('omitted')`)
	execOK(t, s, `INSERT INTO a (id, v) VALUES (AI(), 'requested')`)
	execOK(t, s, `INSERT INTO a (id, v) VALUES (10, 'explicit')`)
	execOK(t, s, `INSERT INTO a (v) VALUES ('after explicit')`)
	res := execOK(t, s, `SELECT id, v FROM a ORDER BY id`)
	want := []string{"1", "2", "10", "11"}
	if len(res.Rows) != len(want) {
		t.Fatalf("rows %v", res.Rows)
	}
	for i, w := range want {
		if res.Rows[i][0].Dec.String() != w {
			t.Fatalf("AI ids %v, want %v", res.Rows, want)
		}
	}
	if _, err := s.Exec(`INSERT INTO a (id, v) VALUES (NULL, 'null key')`); err == nil {
		t.Fatal("an explicit NULL primary key was accepted")
	}
}
