package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func TestCTEBasic(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cte_src (id STRING PRIMARY KEY, value STRING)`)
	execOK(t, s, `INSERT INTO cte_src (id, value) VALUES ('1', 'a'), ('2', 'b'), ('3', 'a')`)

	got := execOK(t, s, `WITH c AS (SELECT value FROM cte_src) SELECT value FROM c ORDER BY value`)
	if len(got.Rows) != 3 || got.Rows[0][0].Str != "a" || got.Rows[2][0].Str != "b" {
		t.Fatalf("simple CTE: %+v", got.Rows)
	}

	got = execOK(t, s, `WITH c(x) AS (SELECT value FROM cte_src) SELECT x FROM c WHERE x = 'b'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "b" {
		t.Fatalf("CTE column alias: %+v", got.Rows)
	}

	got = execOK(t, s, `WITH a AS (SELECT value FROM cte_src WHERE value = 'a'), b AS (SELECT value FROM a) SELECT value FROM b`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "a" {
		t.Fatalf("multiple CTEs: %+v", got.Rows)
	}

	got = execOK(t, s, `WITH c AS (SELECT value FROM cte_src) SELECT value FROM c WHERE value IS NULL`)
	if len(got.Rows) != 0 {
		t.Fatalf("NULL CTE filter: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT d.value FROM (WITH c AS (SELECT value FROM cte_src) SELECT value FROM c) AS d WHERE d.value = 'b'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "b" {
		t.Fatalf("derived WITH: %+v", got.Rows)
	}

	got = execOK(t, s, `WITH c AS (SELECT value FROM cte_src) SELECT id FROM cte_src WHERE value IN (SELECT value FROM c) ORDER BY id`)
	if len(got.Rows) != 3 {
		t.Fatalf("IN subquery CTE: %+v", got.Rows)
	}
}

func TestCTERBAC(t *testing.T) {
	db := testDB(t)
	local := db.Session()
	execOK(t, local, `CREATE TABLE cte_acl (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, local, `INSERT INTO cte_acl (id, value) VALUES ('1', 'a'), ('2', 'b')`)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if _, err := app.Exec(`WITH c AS (SELECT id FROM cte_acl) SELECT id FROM c`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("CTE must require underlying SELECT: %v", err)
	}
	if err := acl.Grant("app", security.PrivSelect, security.ScopeTable, "cte_acl"); err != nil {
		t.Fatal(err)
	}
	got := execOK(t, app, `WITH c AS (SELECT id FROM cte_acl) SELECT id FROM c ORDER BY id`)
	if len(got.Rows) != 2 {
		t.Fatalf("granted CTE rows: %+v", got.Rows)
	}
}

func TestCTEMaterializeAndInline(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cte_mat (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO cte_mat (id, value) VALUES ('1', 'a'), ('2', 'b')`)

	plan := execOK(t, s, `EXPLAIN WITH c AS (SELECT value FROM cte_mat) SELECT value FROM c`)
	if explainHas(plan, "Materialize") || explainHas(plan, "CTEScan") {
		t.Fatalf("single-ref CTE should inline: %+v", plan.Rows)
	}

	plan = execOK(t, s, `EXPLAIN WITH c AS (SELECT value, COUNT(*) AS n FROM cte_mat GROUP BY value) SELECT value FROM c UNION ALL SELECT value FROM c`)
	if !explainHas(plan, "Materialize") || !explainHas(plan, "CTEScan") {
		t.Fatalf("multi-ref aggregate CTE should materialize: %+v", plan.Rows)
	}

	plan = execOK(t, s, `EXPLAIN WITH c AS NOT MATERIALIZED (SELECT value, COUNT(*) AS n FROM cte_mat GROUP BY value) SELECT value FROM c UNION ALL SELECT value FROM c`)
	if explainHas(plan, "Materialize") {
		t.Fatalf("NOT MATERIALIZED should inline: %+v", plan.Rows)
	}

	plan = execOK(t, s, `EXPLAIN WITH c AS MATERIALIZED (SELECT value FROM cte_mat) SELECT value FROM c`)
	if !explainHas(plan, "Materialize") {
		t.Fatalf("MATERIALIZED hint: %+v", plan.Rows)
	}

	got := execOK(t, s, `WITH c AS (SELECT value, COUNT(*) AS n FROM cte_mat GROUP BY value) SELECT value FROM c UNION ALL SELECT value FROM c ORDER BY value`)
	if len(got.Rows) != 4 {
		t.Fatalf("materialized reuse: %+v", got.Rows)
	}
}

func TestCTEJoinAndRecursive(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE org (id STRING PRIMARY KEY, parent STRING)`)
	execOK(t, s, `INSERT INTO org (id, parent) VALUES ('root', NULL), ('a', 'root'), ('b', 'root'), ('a1', 'a')`)

	got := execOK(t, s, `WITH RECURSIVE walk AS (
		SELECT id, parent FROM org WHERE parent IS NULL
		UNION ALL
		SELECT o.id, o.parent FROM org o JOIN walk ON o.parent = walk.id
	) SELECT id FROM walk ORDER BY id`)
	if len(got.Rows) != 4 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "a1" || got.Rows[2][0].Str != "b" || got.Rows[3][0].Str != "root" {
		t.Fatalf("recursive walk: %+v", got.Rows)
	}

	plan := execOK(t, s, `EXPLAIN WITH RECURSIVE walk AS (
		SELECT id, parent FROM org WHERE parent IS NULL
		UNION ALL
		SELECT o.id, o.parent FROM org o JOIN walk ON o.parent = walk.id
	) SELECT id FROM walk`)
	if !explainHas(plan, "RecursiveCTE") {
		t.Fatalf("recursive plan: %+v", plan.Rows)
	}

	got = execOK(t, s, `WITH RECURSIVE walk AS (
		SELECT id, parent FROM org WHERE parent IS NULL
		UNION
		SELECT o.id, o.parent FROM org o JOIN walk ON o.parent = walk.id
	) SELECT id FROM walk ORDER BY id`)
	if len(got.Rows) != 4 {
		t.Fatalf("recursive UNION distinct: %+v", got.Rows)
	}

	if _, err := s.Exec(`WITH RECURSIVE t AS (SELECT id FROM org WHERE id = 'root' UNION ALL SELECT t.id FROM t) SELECT id FROM t`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("recursive depth must be bounded: %v", err)
	}
}

func TestCTERestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE cte_rst (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO cte_rst (id, value) VALUES ('1', 'ok')`)
	got := execOK(t, s, `WITH c AS (SELECT value FROM cte_rst) SELECT value FROM c`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "ok" {
		t.Fatalf("before restart: %+v", got.Rows)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s = db.Session()
	got = execOK(t, s, `WITH c AS (SELECT value FROM cte_rst) SELECT value FROM c`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "ok" {
		t.Fatalf("after restart: %+v", got.Rows)
	}
}
