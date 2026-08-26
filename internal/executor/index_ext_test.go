package executor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestCoveringPartialExpressionIndexes(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE items (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		status STRING NOT NULL,
		note TEXT,
		qty DECIMAL(10,0)
	)`)
	execOK(t, s, `INSERT INTO items (name, status, note, qty) VALUES
		('Alpha', 'active', 'a1', 1),
		('Beta', 'inactive', 'b1', 2),
		('Gamma', 'active', 'g1', 3)`)
	execOK(t, s, `CREATE INDEX ix_cover ON items (name) INCLUDE (note, qty)`)
	execOK(t, s, `CREATE INDEX ix_active ON items (name) WHERE status = 'active'`)
	execOK(t, s, `CREATE INDEX ix_lower ON items (LOWER(name))`)

	got := execOK(t, s, `SELECT name, note, qty FROM items WHERE name = 'Beta'`)
	if len(got.Rows) != 1 || got.Rows[0][1].Str != "b1" {
		t.Fatalf("covering lookup: %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT name, note, qty FROM items WHERE name = 'Beta'`)
	if !explainHas(plan, "IndexScan") || !explainHas(plan, "ix_cover") || !explainHas(plan, "covering") {
		t.Fatalf("covering plan: %+v", explainOps(plan))
	}

	active := execOK(t, s, `SELECT name FROM items WHERE name = 'Alpha' AND status = 'active'`)
	if len(active.Rows) != 1 || active.Rows[0][0].Str != "Alpha" {
		t.Fatalf("partial hit: %+v", active.Rows)
	}
	plan = execOK(t, s, `EXPLAIN SELECT name FROM items WHERE name = 'Alpha' AND status = 'active'`)
	if !explainHas(plan, "ix_active") {
		t.Fatalf("partial plan: %+v", explainOps(plan))
	}
	miss := execOK(t, s, `SELECT name FROM items WHERE name = 'Beta' AND status = 'inactive'`)
	if len(miss.Rows) != 1 || miss.Rows[0][0].Str != "Beta" {
		t.Fatalf("inactive row must still be visible: %+v", miss.Rows)
	}
	plan = execOK(t, s, `EXPLAIN SELECT name FROM items WHERE name = 'Beta' AND status = 'inactive'`)
	if explainHas(plan, "ix_active") {
		t.Fatalf("partial index used without implication: %+v", explainOps(plan))
	}

	lower := execOK(t, s, `SELECT name FROM items WHERE LOWER(name) = 'alpha'`)
	if len(lower.Rows) != 1 || lower.Rows[0][0].Str != "Alpha" {
		t.Fatalf("expression lookup: %+v", lower.Rows)
	}
	plan = execOK(t, s, `EXPLAIN SELECT name FROM items WHERE LOWER(name) = 'alpha'`)
	if !explainHas(plan, "ix_lower") {
		t.Fatalf("expression plan: %+v", explainOps(plan))
	}

	execOK(t, s, `UPDATE items SET note = 'b2', qty = 20 WHERE name = 'Beta'`)
	got = execOK(t, s, `SELECT note, qty FROM items WHERE name = 'Beta'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "b2" || got.Rows[0][1].Dec.String() != "20" {
		t.Fatalf("covering after update: %+v", got.Rows)
	}
	execOK(t, s, `UPDATE items SET status = 'inactive' WHERE name = 'Alpha'`)
	active = execOK(t, s, `SELECT name FROM items WHERE name = 'Alpha' AND status = 'active'`)
	if len(active.Rows) != 0 {
		t.Fatalf("partial should drop updated row: %+v", active.Rows)
	}
	execOK(t, s, `UPDATE items SET name = 'Alpha2' WHERE name = 'Alpha'`)
	lower = execOK(t, s, `SELECT name FROM items WHERE LOWER(name) = 'alpha2'`)
	if len(lower.Rows) != 1 || lower.Rows[0][0].Str != "Alpha2" {
		t.Fatalf("expression after rename: %+v", lower.Rows)
	}
}

func TestCoveringIndexRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), name STRING NOT NULL, note TEXT, status STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (name, note, status) VALUES ('a', 'n1', 'active'), ('b', 'n2', 'inactive')`)
	execOK(t, s, `CREATE INDEX ix ON t (LOWER(name)) INCLUDE (note) WHERE status = 'active'`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got := execOK(t, s, `SELECT name, note FROM t WHERE LOWER(name) = 'a' AND status = 'active'`)
	if len(got.Rows) != 1 || got.Rows[0][1].Str != "n1" {
		t.Fatalf("restart: %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT name, note FROM t WHERE LOWER(name) = 'a' AND status = 'active'`)
	if !explainHas(plan, "ix") {
		t.Fatalf("restart plan: %+v", explainOps(plan))
	}
}

func TestIndexExtensionRejections(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), name STRING NOT NULL, body TEXT)`)
	if _, err := s.Exec(`CREATE INDEX ix ON t (UUID())`); err == nil || !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("uuid: %v", err)
	}
	if _, err := s.Exec(`CREATE INDEX ix ON t (NOW())`); err == nil || !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("now: %v", err)
	}
	if _, err := s.Exec(`CREATE FULLTEXT INDEX ix ON t (body) INCLUDE (name)`); err == nil {
		t.Fatal("expected fulltext INCLUDE rejection")
	}
}

func TestUniqueCoveringIndex(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), email STRING NOT NULL, note TEXT)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux ON t (email) INCLUDE (note)`)
	execOK(t, s, `INSERT INTO t (email, note) VALUES ('a@x', 'one')`)
	if _, err := s.Exec(`INSERT INTO t (email, note) VALUES ('a@x', 'two')`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("unique covering insert: %v", err)
	}
	got := execOK(t, s, `SELECT email, note FROM t WHERE email = 'a@x'`)
	if len(got.Rows) != 1 || got.Rows[0][1].Str != "one" {
		t.Fatalf("%+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT email, note FROM t WHERE email = 'a@x'`)
	if !explainHas(plan, "covering") {
		t.Fatalf("%+v", explainOps(plan))
	}
}

func TestPartialIndexDoesNotIndexNonMatchingRows(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), name STRING NOT NULL, status STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (name, status) VALUES ('keep', 'active'), ('skip', 'inactive')`)
	execOK(t, s, `CREATE INDEX ix ON t (name) WHERE status = 'active'`)
	got := execOK(t, s, `SELECT name FROM t WHERE name = 'skip'`)
	if len(got.Rows) != 1 {
		t.Fatalf("heap still has inactive row: %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT name FROM t WHERE name = 'skip' AND status = 'active'`)
	ops := strings.Join(explainOps(plan), " ")
	if strings.Contains(ops, "ix") && !strings.Contains(ops, "SeqScan") {
		// query implies the partial predicate, but no matching row; IndexScan is allowed
		_ = ops
	}
	got = execOK(t, s, `SELECT name FROM t WHERE name = 'skip' AND status = 'active'`)
	if len(got.Rows) != 0 {
		t.Fatalf("inactive row leaked through partial: %+v", got.Rows)
	}
}
