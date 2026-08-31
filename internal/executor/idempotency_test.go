package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestExecIdempotentReplayAndConflict(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE idem_items (id STRING PRIMARY KEY, value STRING NOT NULL)`)

	const insert = `INSERT INTO idem_items (id, value) VALUES ('1', 'once') RETURNING id, value`
	first, err := s.ExecIdempotent(context.Background(), "create-1", insert, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.IdempotentReplay || len(first.Rows) != 1 || first.Rows[0][1].Str != "once" {
		t.Fatalf("first result: %+v", first)
	}
	replay, err := s.ExecIdempotent(context.Background(), "create-1", insert, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.IdempotentReplay || len(replay.Rows) != 1 || replay.Rows[0][1].Str != "once" {
		t.Fatalf("replay result: %+v", replay)
	}
	count := execOK(t, s, `SELECT COUNT(*) FROM idem_items`)
	if len(count.Rows) != 1 || count.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("mutation ran more than once: %+v", count.Rows)
	}

	_, err = s.ExecIdempotent(context.Background(), "create-1", `INSERT INTO idem_items (id, value) VALUES ('2', 'different')`, nil)
	if !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("key reuse error=%v", err)
	}
	count = execOK(t, s, `SELECT COUNT(*) FROM idem_items`)
	if count.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("conflicting request executed: %+v", count.Rows)
	}

	if _, err := s.ExecIdempotent(context.Background(), "read", `SELECT * FROM idem_items`, nil); err == nil {
		t.Fatal("expected read-only idempotency rejection")
	}
}

func FuzzDecodeIdempotentResult(f *testing.F) {
	seed, err := encodeIdempotentResult(&Result{
		Columns:  []string{"value"},
		Rows:     [][]types.Value{{types.StringValue("ok")}},
		Affected: 1,
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("NSIR"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = decodeIdempotentResult(raw)
	})
}

func TestExecIdempotentIdentityScopeAndFailureRollback(t *testing.T) {
	db := testDB(t)
	setup := db.Session()
	execOK(t, setup, `CREATE TABLE idem_scope (id STRING PRIMARY KEY, value STRING NOT NULL)`)

	alice := db.Session()
	alice.SetIdentity("alice")
	if _, err := alice.ExecIdempotent(context.Background(), "same", `INSERT INTO idem_scope (id, value) VALUES ('a', 'alice')`, nil); err != nil {
		t.Fatal(err)
	}
	bob := db.Session()
	bob.SetIdentity("bob")
	if _, err := bob.ExecIdempotent(context.Background(), "same", `INSERT INTO idem_scope (id, value) VALUES ('b', 'bob')`, nil); err != nil {
		t.Fatal(err)
	}

	bad := `INSERT INTO idem_scope (id, value) VALUES ('a', 'duplicate')`
	if _, err := alice.ExecIdempotent(context.Background(), "failed", bad, nil); err == nil {
		t.Fatal("expected duplicate-key failure")
	}
	// A failed transaction does not consume its key.
	if _, err := alice.ExecIdempotent(context.Background(), "failed", `INSERT INTO idem_scope (id, value) VALUES ('c', 'retry')`, nil); err != nil {
		t.Fatal(err)
	}
	count := execOK(t, setup, `SELECT COUNT(*) FROM idem_scope`)
	if count.Rows[0][0].Dec.String() != "3" {
		t.Fatalf("scope/failure rows: %+v", count.Rows)
	}
}

func TestExecIdempotentSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE idem_restart (id STRING PRIMARY KEY)`)
	const mutation = `INSERT INTO idem_restart (id) VALUES ('1') RETURNING id`
	if _, err := s.ExecIdempotent(context.Background(), "restart-key", mutation, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	replay, err := db.Session().ExecIdempotent(context.Background(), "restart-key", mutation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.IdempotentReplay || len(replay.Rows) != 1 || replay.Rows[0][0].Str != "1" {
		t.Fatalf("restart replay: %+v", replay)
	}
	count := execOK(t, db.Session(), `SELECT COUNT(*) FROM idem_restart`)
	if count.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("restart duplicate: %+v", count.Rows)
	}
}
