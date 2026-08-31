package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

func TestP18SQLGateNullTxnPrepared(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE p18_src (
		id STRING PRIMARY KEY,
		k STRING,
		n DECIMAL(10,0)
	)`)
	execOK(t, s, `INSERT INTO p18_src (id, k, n) VALUES
		('1', 'a', 1),
		('2', 'a', 2),
		('3', 'b', 3),
		('4', NULL, NULL),
		('5', 'b', NULL)`)

	got := execOK(t, s, `SELECT DISTINCT k FROM p18_src ORDER BY k`)
	if len(got.Rows) != 3 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" || !got.Rows[2][0].Null {
		t.Fatalf("DISTINCT NULL collapse: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT k, COUNT(*) AS total FROM p18_src GROUP BY k HAVING total >= 2 ORDER BY k`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("HAVING: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT id, CASE k WHEN 'a' THEN 'alpha' WHEN 'b' THEN 'beta' ELSE COALESCE(k, 'missing') END FROM p18_src WHERE id = '1' OR id = '4' ORDER BY id`)
	if len(got.Rows) != 2 || got.Rows[0][1].Str != "alpha" || got.Rows[1][1].Str != "missing" {
		t.Fatalf("CASE/COALESCE: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT k FROM p18_src WHERE k IS NULL UNION SELECT k FROM p18_src WHERE k IS NULL`)
	if len(got.Rows) != 1 || !got.Rows[0][0].Null {
		t.Fatalf("UNION NULL: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT k FROM p18_src WHERE id = '1' INTERSECT SELECT k FROM p18_src WHERE k = 'a'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" {
		t.Fatalf("INTERSECT: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT k FROM p18_src WHERE k = 'a' EXCEPT SELECT k FROM p18_src WHERE id = '3'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" {
		t.Fatalf("EXCEPT: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT id FROM p18_src WHERE k IN (SELECT k FROM p18_src WHERE id = '1') ORDER BY id`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "1" || got.Rows[1][0].Str != "2" {
		t.Fatalf("IN subquery: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id FROM p18_src WHERE k NOT IN (SELECT k FROM p18_src WHERE k IS NOT NULL)`)
	if len(got.Rows) != 0 {
		t.Fatalf("nullable NOT IN must be unknown: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id FROM p18_src o WHERE EXISTS (SELECT id FROM p18_src i WHERE i.k = o.k AND i.id <> o.id) ORDER BY id`)
	if len(got.Rows) != 4 || got.Rows[0][0].Str != "1" || got.Rows[1][0].Str != "2" || got.Rows[2][0].Str != "3" || got.Rows[3][0].Str != "5" {
		t.Fatalf("correlated EXISTS: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT d.k FROM (SELECT DISTINCT k FROM p18_src WHERE k IS NOT NULL) d ORDER BY d.k`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("derived DISTINCT: %+v", got.Rows)
	}

	got = execOK(t, s, `WITH c AS (SELECT k FROM p18_src WHERE k IS NOT NULL) SELECT DISTINCT k FROM c ORDER BY k`)
	if len(got.Rows) != 2 {
		t.Fatalf("CTE: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id, ROW_NUMBER() OVER (PARTITION BY k ORDER BY id) AS n FROM p18_src WHERE k = 'a' ORDER BY id`)
	if len(got.Rows) != 2 || got.Rows[0][1].Dec.String() != "1" || got.Rows[1][1].Dec.String() != "2" {
		t.Fatalf("window: %+v", got.Rows)
	}

	execOK(t, s, `BEGIN`)
	got, err := s.ExecContext(context.Background(),
		`UPSERT INTO p18_src (id, k, n) VALUES ($1, $2, $3) RETURNING k`,
		[]Param{
			{Value: types.StringValue("1")},
			{Value: types.StringValue("a")},
			{Value: types.DecimalValue(mustDec(t, "9"), types.Type{Kind: types.KindDecimal, Precision: 10})},
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" {
		t.Fatalf("prepared UPSERT RETURNING: %+v", got.Rows)
	}
	got, err = s.ExecContext(context.Background(),
		`SELECT k FROM p18_src WHERE id = $1 UNION ALL SELECT k FROM p18_src WHERE k = $2`,
		[]Param{{Value: types.StringValue("1")}, {Value: types.StringValue("b")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 3 {
		t.Fatalf("prepared UNION ALL: %+v", got.Rows)
	}
	execOK(t, s, `ROLLBACK`)
	got = execOK(t, s, `SELECT n FROM p18_src WHERE id = '1'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("rollback prepared UPSERT: %+v", got.Rows)
	}

	execOK(t, s, `BEGIN`)
	execOK(t, s, `UPDATE p18_src SET k = 'z' WHERE id = '2' RETURNING k`)
	execOK(t, s, `COMMIT`)
	got = execOK(t, s, `SELECT k FROM p18_src WHERE id = '2'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "z" {
		t.Fatalf("committed RETURNING update: %+v", got.Rows)
	}
}

func TestP18SQLGateRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE p18_rst (
		id STRING PRIMARY KEY,
		k STRING,
		n STRING,
		qty DECIMAL(10,0)
	)`)
	execOK(t, s, `CREATE INDEX ix_p18_rst ON p18_rst (LOWER(k)) INCLUDE (n) WHERE qty IS NOT NULL`)
	execOK(t, s, `INSERT INTO p18_rst (id, k, n, qty) VALUES
		('1', 'Alpha', 'keep', 1),
		('2', 'Beta', 'gone', NULL)`)
	execOK(t, s, `UPSERT INTO p18_rst (id, k, n, qty) VALUES ('3', 'Gamma', 'new', 3) RETURNING id`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `UPSERT INTO p18_rst (id, k, n, qty) VALUES ('4', 'Delta', 'lost', 4)`)
	execOK(t, s, `UPDATE p18_rst SET n = 'dirty' WHERE id = '1' RETURNING n`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s = db.Session()

	got := execOK(t, s, `SELECT id FROM p18_rst ORDER BY id`)
	if len(got.Rows) != 3 || got.Rows[0][0].Str != "1" || got.Rows[1][0].Str != "2" || got.Rows[2][0].Str != "3" {
		t.Fatalf("kill recovered rows: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT n FROM p18_rst WHERE id = '1'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "keep" {
		t.Fatalf("uncommitted RETURNING update survived kill: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT DISTINCT k FROM p18_rst ORDER BY k`)
	if len(got.Rows) != 3 {
		t.Fatalf("DISTINCT after restart: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT n FROM p18_rst WHERE id = '1' UNION SELECT n FROM p18_rst WHERE id = '3'`)
	if !strSet(got, "keep", "new") {
		t.Fatalf("UNION after restart: %+v", got.Rows)
	}
	got = execOK(t, s, `WITH c AS (SELECT k FROM p18_rst WHERE id = '3') SELECT k FROM c`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "Gamma" {
		t.Fatalf("CTE after restart: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id, ROW_NUMBER() OVER (ORDER BY id) FROM p18_rst`)
	if len(got.Rows) != 3 {
		t.Fatalf("window after restart: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT k, n FROM p18_rst WHERE LOWER(k) = 'alpha' AND qty IS NOT NULL`)
	if len(got.Rows) != 1 || got.Rows[0][1].Str != "keep" {
		t.Fatalf("expression/covering after restart: %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT k, n FROM p18_rst WHERE LOWER(k) = 'alpha' AND qty IS NOT NULL`)
	if !explainHas(plan, "ix_p18_rst") {
		t.Fatalf("partial expression index missing after restart: %+v", plan.Rows)
	}
}

func strSet(res *Result, want ...string) bool {
	if res == nil || len(res.Rows) != len(want) {
		return false
	}
	seen := make(map[string]int, len(want))
	for _, row := range res.Rows {
		if len(row) == 0 {
			return false
		}
		key := ""
		if !row[0].Null {
			key = row[0].Str
		}
		seen[key]++
	}
	for _, w := range want {
		if seen[w] != 1 {
			return false
		}
	}
	return true
}
