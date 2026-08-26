package executor

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestUpsertPKAndUniqueIndex(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE items (id STRING PRIMARY KEY, email STRING NOT NULL, n STRING)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_email ON items (email)`)

	ins := execOK(t, s, `UPSERT INTO items (id, email, n) VALUES ('1', 'a@b', 'x') RETURNING id, n`)
	if ins.Affected != 1 || len(ins.Rows) != 1 || ins.Rows[0][0].Str != "1" || ins.Rows[0][1].Str != "x" {
		t.Fatalf("insert returning: %+v", ins)
	}

	upd := execOK(t, s, `UPSERT INTO items (id, email, n) VALUES ('1', 'a@b', 'y')`)
	if upd.Affected != 1 {
		t.Fatalf("pk update affected %d", upd.Affected)
	}
	got := execOK(t, s, `SELECT id, n FROM items`)
	if len(got.Rows) != 1 || got.Rows[0][1].Str != "y" {
		t.Fatalf("after pk upsert: %+v", got.Rows)
	}

	email := execOK(t, s, `UPSERT INTO items (id, email, n) VALUES ('2', 'a@b', 'z') ON UNIQUE (email)`)
	if email.Affected != 1 {
		t.Fatalf("unique upsert affected %d", email.Affected)
	}
	got = execOK(t, s, `SELECT id, n FROM items`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "1" || got.Rows[0][1].Str != "z" {
		t.Fatalf("default SET must keep PK and copy n: %+v", got.Rows)
	}

	set := execOK(t, s, `UPSERT INTO items (id, email, n) VALUES ('9', 'a@b', 'kept') ON UNIQUE (email) SET n = CONCAT(excluded.n, '-up') RETURNING id, n`)
	if len(set.Rows) != 1 || set.Rows[0][0].Str != "1" || set.Rows[0][1].Str != "kept-up" {
		t.Fatalf("excluded set: %+v", set.Rows)
	}

	plan := execOK(t, s, `EXPLAIN UPSERT INTO items (id, email, n) VALUES ('3', 'c@d', 'q') ON UNIQUE (email)`)
	if !explainHas(plan, "Upsert") {
		t.Fatalf("explain missing Upsert: %+v", plan.Rows)
	}
}

func TestReturningInsertUpdateDelete(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	ins := execOK(t, s, `INSERT INTO t (id, n) VALUES ('1', 'a'), ('2', 'b') RETURNING id, n`)
	if len(ins.Columns) != 2 || len(ins.Rows) != 2 || ins.Affected != 2 {
		t.Fatalf("insert returning: cols=%v rows=%+v affected=%d", ins.Columns, ins.Rows, ins.Affected)
	}
	upd := execOK(t, s, `UPDATE t SET n = CONCAT(n, 'x') WHERE id = '1' RETURNING n`)
	if len(upd.Rows) != 1 || upd.Rows[0][0].Str != "ax" || upd.Affected != 1 {
		t.Fatalf("update returning: %+v", upd)
	}
	del := execOK(t, s, `DELETE FROM t WHERE id = '2' RETURNING *`)
	if len(del.Rows) != 1 || del.Rows[0][0].Str != "2" || del.Rows[0][1].Str != "b" {
		t.Fatalf("delete returning: %+v", del)
	}
	empty := execOK(t, s, `UPDATE t SET n = 'z' WHERE id = 'missing' RETURNING n`)
	if len(empty.Columns) != 1 || len(empty.Rows) != 0 || empty.Affected != 0 {
		t.Fatalf("empty returning: %+v", empty)
	}

	q, err := s.Query(`INSERT INTO t (id, n) VALUES ('3', 'c') RETURNING id`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.NextBatch()
	if err != nil || b == nil || b.Count != 1 {
		t.Fatalf("query returning batch: %v %+v", err, b)
	}
	end, err := q.NextBatch()
	if err != nil || end != nil {
		t.Fatalf("query returning end: %v %+v", err, end)
	}
}

func TestUpsertRestartAndReplica(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `UPSERT INTO t (id, n) VALUES ('k', 'one')`)
	execOK(t, s, `UPSERT INTO t (id, n) VALUES ('k', 'two')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got := execOK(t, db.Session(), `SELECT n FROM t WHERE id = 'k'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "two" {
		t.Fatalf("after reopen: %+v", got.Rows)
	}
}

func TestUpsertConcurrentUnique(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, email STRING NOT NULL, n DECIMAL(10,0))`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_email ON t (email)`)

	const workers = 8
	var wg sync.WaitGroup
	errc := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ss := db.Session()
			_, err := ss.Exec(`UPSERT INTO t (id, email, n) VALUES ('` + string(rune('a'+i)) + `', 'same@x', 1) ON UNIQUE (email) SET n = n + 1`)
			errc <- err
		}(i)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := execOK(t, s, `SELECT email, n FROM t`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "same@x" {
		t.Fatalf("concurrent upsert rows: %+v", got.Rows)
	}
	if got.Rows[0][1].Dec.String() == "0" {
		t.Fatalf("n not updated: %+v", got.Rows)
	}
}

func TestUpsertRBACAndTenant(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("dba", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	execOK(t, admin, `CREATE TABLE t (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, email STRING NOT NULL, n STRING)`)
	execOK(t, admin, `CREATE UNIQUE INDEX ux_email ON t (email)`)
	execOK(t, admin, `CREATE USER app IDENTIFIED BY 'pw'`)
	execOK(t, admin, `GRANT INSERT ON TABLE t TO app`)

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if _, err := app.Exec(`UPSERT INTO t (id, tenant_id, email, n) VALUES ('1', '11111111-1111-1111-1111-111111111111', 'a@b', 'x')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("upsert needs UPDATE: %v", err)
	}
	execOK(t, admin, `GRANT UPDATE ON TABLE t TO app`)
	if _, err := app.Exec(`UPSERT INTO t (id, tenant_id, email, n) VALUES ('1', '11111111-1111-1111-1111-111111111111', 'a@b', 'x') RETURNING n`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("returning needs SELECT: %v", err)
	}
	execOK(t, admin, `GRANT SELECT ON TABLE t TO app`)
	execOK(t, app, `SET TENANT = '11111111-1111-1111-1111-111111111111'`)
	if _, err := app.Exec(`UPSERT INTO t (id, email, n) VALUES ('1', 'a@b', 'x') RETURNING n`); err != nil {
		t.Fatal(err)
	}

	ten := db.Session()
	execOK(t, ten, `SET TENANT = '11111111-1111-1111-1111-111111111111'`)
	execOK(t, ten, `UPSERT INTO t (id, email, n) VALUES ('2', 'b@c', 'ten') ON UNIQUE (email)`)
	got := execOK(t, ten, `SELECT email FROM t WHERE id = '2'`)
	if len(got.Rows) != 1 {
		t.Fatalf("tenant upsert: %+v", got.Rows)
	}
}

func TestUpsertRejectsJSONPathUnique(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, meta JSON)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_cat ON t (meta.category)`)
	if _, err := s.Exec(`UPSERT INTO t (id, meta) VALUES ('1', '{"category":"a"}') ON UNIQUE (meta)`); err == nil {
		t.Fatal("expected JSON-path unique rejection")
	}
}

func TestReturningPreparedAndTxn(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `BEGIN`)
	got, err := s.ExecContext(context.Background(), `INSERT INTO t (id, n) VALUES ($1, $2) RETURNING n`, []Param{{Value: types.StringValue("1")}, {Value: types.StringValue("a")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" {
		t.Fatalf("%+v", got.Rows)
	}
	execOK(t, s, `ROLLBACK`)
	got = execOK(t, s, `SELECT * FROM t`)
	if len(got.Rows) != 0 {
		t.Fatalf("rollback returning insert: %+v", got.Rows)
	}
}
