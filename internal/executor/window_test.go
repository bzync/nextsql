package executor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestWindowRanking(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE w (id STRING PRIMARY KEY, k STRING, v DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO w (id, k, v) VALUES ('1', 'a', 10), ('2', 'a', 20), ('3', 'a', 20), ('4', 'b', 5), ('5', 'b', 15)`)

	got := execOK(t, s, `SELECT id, ROW_NUMBER() OVER (PARTITION BY k ORDER BY v, id) AS n FROM w ORDER BY id`)
	if len(got.Rows) != 5 {
		t.Fatalf("rows: %+v", got.Rows)
	}
	want := []string{"1", "2", "3", "1", "2"}
	for i, row := range got.Rows {
		if row[1].Dec.String() != want[i] {
			t.Fatalf("row_number %d: %s want %s (%+v)", i, row[1].Dec.String(), want[i], got.Rows)
		}
	}

	got = execOK(t, s, `SELECT id, RANK() OVER (PARTITION BY k ORDER BY v) AS r, DENSE_RANK() OVER (PARTITION BY k ORDER BY v) AS d FROM w ORDER BY id`)
	if got.Rows[0][1].Dec.String() != "1" || got.Rows[1][1].Dec.String() != "2" || got.Rows[2][1].Dec.String() != "2" {
		t.Fatalf("rank ties: %+v", got.Rows)
	}
	if got.Rows[1][2].Dec.String() != "2" || got.Rows[2][2].Dec.String() != "2" {
		t.Fatalf("dense_rank ties: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT id, ROW_NUMBER() OVER () AS n FROM w ORDER BY n`)
	if len(got.Rows) != 5 || got.Rows[0][1].Dec.String() != "1" || got.Rows[4][1].Dec.String() != "5" {
		t.Fatalf("empty over: %+v", got.Rows)
	}

	plan := execOK(t, s, `EXPLAIN SELECT ROW_NUMBER() OVER (ORDER BY v) FROM w`)
	if !explainHas(plan, "Window") {
		t.Fatalf("explain missing Window: %+v", plan.Rows)
	}

	got, err := s.ExecContext(context.Background(), `SELECT id FROM w WHERE k = $1 ORDER BY ROW_NUMBER() OVER (ORDER BY v, id)`, []Param{{Value: types.StringValue("a")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 3 || got.Rows[0][0].Str != "1" {
		t.Fatalf("param + window order: %+v", got.Rows)
	}
	execOK(t, s, `BEGIN`)
	got = execOK(t, s, `SELECT COUNT(*) OVER () FROM w`)
	if len(got.Rows) != 5 || got.Rows[0][0].Dec.String() != "5" {
		t.Fatalf("window in txn: %+v", got.Rows)
	}
	execOK(t, s, `ROLLBACK`)
}

func TestWindowLagLeadValues(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE w (id STRING PRIMARY KEY, k STRING, v DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO w (id, k, v) VALUES ('1', 'a', 10), ('2', 'a', 20), ('3', 'a', 30)`)

	got := execOK(t, s, `SELECT id, LAG(v) OVER (ORDER BY id), LEAD(v, 1, 0) OVER (ORDER BY id) FROM w ORDER BY id`)
	if !got.Rows[0][1].Null || got.Rows[0][2].Dec.String() != "20" {
		t.Fatalf("lag/lead first: %+v", got.Rows[0])
	}
	if got.Rows[1][1].Dec.String() != "10" || got.Rows[1][2].Dec.String() != "30" {
		t.Fatalf("lag/lead mid: %+v", got.Rows[1])
	}
	if got.Rows[2][1].Dec.String() != "20" || got.Rows[2][2].Dec.String() != "0" {
		t.Fatalf("lag/lead last: %+v", got.Rows[2])
	}

	got = execOK(t, s, `SELECT id, FIRST_VALUE(v) OVER (ORDER BY id), LAST_VALUE(v) OVER (ORDER BY id) FROM w ORDER BY id`)
	if got.Rows[0][1].Dec.String() != "10" || got.Rows[0][2].Dec.String() != "10" {
		t.Fatalf("default last_value is current row: %+v", got.Rows[0])
	}
	got = execOK(t, s, `SELECT id, LAST_VALUE(v) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM w ORDER BY id`)
	if got.Rows[0][1].Dec.String() != "30" || got.Rows[2][1].Dec.String() != "30" {
		t.Fatalf("unbounded last_value: %+v", got.Rows)
	}
}

func TestWindowAggregatesAndFrames(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE w (id STRING PRIMARY KEY, k STRING, v DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO w (id, k, v) VALUES ('1', 'a', 10), ('2', 'a', 20), ('3', 'a', NULL), ('4', 'b', 5)`)

	got := execOK(t, s, `SELECT id, SUM(v) OVER (PARTITION BY k ORDER BY id) AS s, COUNT(*) OVER (PARTITION BY k) AS c, COUNT(v) OVER (PARTITION BY k) AS cv FROM w ORDER BY id`)
	if got.Rows[0][1].Dec.String() != "10" || got.Rows[1][1].Dec.String() != "30" || got.Rows[2][1].Dec.String() != "30" {
		t.Fatalf("running sum: %+v", got.Rows)
	}
	if got.Rows[0][2].Dec.String() != "3" || got.Rows[3][2].Dec.String() != "1" {
		t.Fatalf("count(*) partition: %+v", got.Rows)
	}
	if got.Rows[0][3].Dec.String() != "2" {
		t.Fatalf("count(v) skips null: %+v", got.Rows[0])
	}

	got = execOK(t, s, `SELECT id, SUM(v) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM w ORDER BY id`)
	if got.Rows[0][1].Dec.String() != "10" || got.Rows[1][1].Dec.String() != "30" {
		t.Fatalf("rows frame: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT k, SUM(v) AS s, RANK() OVER (ORDER BY SUM(v)) AS r FROM w GROUP BY k ORDER BY k`)
	if len(got.Rows) != 2 || got.Rows[0][2].Dec.String() != "2" || got.Rows[1][2].Dec.String() != "1" {
		t.Fatalf("window after group: %+v", got.Rows)
	}
}

func TestWindowNullsDistinctLimit(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE w (id STRING PRIMARY KEY, k STRING, v DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO w (id, k, v) VALUES ('1', NULL, 1), ('2', NULL, 2), ('3', 'a', 3)`)

	got := execOK(t, s, `SELECT id, ROW_NUMBER() OVER (PARTITION BY k ORDER BY v) FROM w ORDER BY id`)
	if got.Rows[0][1].Dec.String() != "1" || got.Rows[1][1].Dec.String() != "2" || got.Rows[2][1].Dec.String() != "1" {
		t.Fatalf("null partition: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT DISTINCT n FROM (SELECT ROW_NUMBER() OVER (ORDER BY id) AS n FROM w) AS d`)
	if len(got.Rows) != 3 {
		t.Fatalf("distinct derived window: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT id, ROW_NUMBER() OVER (ORDER BY id) AS n FROM w ORDER BY n DESC LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "3" {
		t.Fatalf("order/limit window: %+v", got.Rows)
	}
}

func TestWindowRejects(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE w (id STRING PRIMARY KEY, k STRING, v DECIMAL(10,0))`)
	if _, err := s.Exec(`SELECT ROW_NUMBER() FROM w`); err == nil {
		t.Fatal("ROW_NUMBER without OVER")
	}
	if _, err := s.Exec(`SELECT k FROM w WHERE ROW_NUMBER() OVER () = 1`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("window in WHERE: %v", err)
	}
	if _, err := s.Exec(`SELECT k FROM w GROUP BY ROW_NUMBER() OVER ()`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("window in GROUP BY: %v", err)
	}
	if _, err := s.Exec(`SELECT SUM(v) OVER (ORDER BY k RANGE BETWEEN 1 PRECEDING AND CURRENT ROW) FROM w`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("RANGE offset: %v", err)
	}
}

func TestWindowTenantRBACRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE wten (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, k STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO wten (id, tenant_id, k) VALUES ('a1', '`+tenantA+`', 'x'), ('b1', '`+tenantB+`', 'x')`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	got := execOK(t, s, `SELECT id, ROW_NUMBER() OVER (ORDER BY id) FROM wten`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a1" {
		t.Fatalf("tenant leaked: %+v", got.Rows)
	}
	execOK(t, s, `RESET TENANT`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s = db.Session()
	got = execOK(t, s, `SELECT id, ROW_NUMBER() OVER (ORDER BY id) FROM wten ORDER BY id`)
	if len(got.Rows) != 2 {
		t.Fatalf("after restart: %+v", got.Rows)
	}

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
	execOK(t, admin, `CREATE USER app IDENTIFIED BY 'pw'`)
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if _, err := app.Exec(`SELECT ROW_NUMBER() OVER (ORDER BY id) FROM wten`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("window must require SELECT: %v", err)
	}
	execOK(t, admin, `GRANT SELECT ON TABLE wten TO app`)
	execOK(t, app, `SET TENANT = '`+tenantA+`'`)
	got = execOK(t, app, `SELECT id, ROW_NUMBER() OVER (ORDER BY id) FROM wten`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a1" {
		t.Fatalf("granted window: %+v", got.Rows)
	}
}

func TestWindowSpillAndCancel(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE w (id STRING PRIMARY KEY, k STRING NOT NULL, v DECIMAL(10,0) NOT NULL)`)
	for i := 0; i < 40; i++ {
		execOK(t, s, `INSERT INTO w (id, k, v) VALUES ('`+itoa(i)+`', '`+itoa(i%8)+`', `+itoa(i)+`)`)
	}
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 256, Disk: 1 << 20, IO: 1 << 20, Time: time.Minute, BatchSize: 1024})
	got, err := s.Exec(`SELECT k, COUNT(*) OVER (PARTITION BY k) FROM w`)
	if err != nil {
		if !nerr.HasCode(err, nerr.Exhausted) {
			t.Fatalf("spill/exhaust: %v", err)
		}
	} else if len(got.Rows) != 40 {
		t.Fatalf("window spill rows: %d", len(got.Rows))
	}

	s = db.Session()
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 64 << 20, Disk: 8 << 20, IO: 1 << 20, Time: time.Nanosecond, BatchSize: 1024})
	if _, err := s.Exec(`SELECT ROW_NUMBER() OVER (ORDER BY id) FROM w`); err != nil && !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("cancel: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
