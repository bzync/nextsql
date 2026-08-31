package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestWorkflowCatalogReloadAndIsolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	want := &catalog.Workflow{
		ID:    7,
		Name:  "cleanup",
		Owner: "alice",
		Body:  []ast.Stmt{ast.Delete{Table: "queue"}},
	}
	raw, err := catalog.EncodeWorkflow(want)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.CatTree.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert(catalog.WorkflowKey(want.Name), raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.reloadCatalog(); err != nil {
		t.Fatal(err)
	}
	got, ok := db.workflow(want.Name)
	if !ok || got.ID != want.ID || got.Owner != want.Owner || len(got.Body) != 1 {
		t.Fatalf("workflow %+v ok=%v", got, ok)
	}
	got.Body[0] = ast.Delete{Table: "mutated"}
	again, ok := db.workflow(want.Name)
	if !ok || again.Body[0].(ast.Delete).Table != "queue" {
		t.Fatalf("committed workflow was mutable: %+v", again)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, ok = db.workflow(want.Name)
	if !ok || got.ID != want.ID || got.Body[0].(ast.Delete).Table != "queue" {
		t.Fatalf("restart workflow %+v ok=%v", got, ok)
	}
}

func TestWorkflowCatalogRejectsKeyNameMismatch(t *testing.T) {
	db := testDB(t)
	raw, err := catalog.EncodeWorkflow(&catalog.Workflow{
		ID:    1,
		Name:  "actual",
		Owner: "alice",
		Body:  []ast.Stmt{ast.Delete{Table: "queue"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.CatTree.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert(catalog.WorkflowKey("wrong"), raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.reloadCatalog(); err == nil {
		t.Fatal("accepted workflow key/name mismatch")
	}
}

func TestWorkflowCatalogRejectsDependencyMismatch(t *testing.T) {
	db := testDB(t)
	raw, err := catalog.EncodeWorkflow(&catalog.Workflow{
		ID:    11,
		Name:  "bad_dependency",
		Owner: "alice",
		Body:  []ast.Stmt{ast.Delete{Table: "missing"}},
		Dependencies: []catalog.WorkflowDependency{
			{Kind: catalog.WorkflowDependencyTable, ID: 999, Name: "missing"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.CatTree.BeginTxn(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert(catalog.WorkflowKey("bad_dependency"), raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.reloadCatalog(); err == nil {
		t.Fatal("accepted mismatched workflow dependency")
	}
}

func TestWorkflowManualRuntimeAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE jobs (id STRING PRIMARY KEY, state STRING NOT NULL)`)
	execOK(t, s, `CREATE WORKFLOW put_job(id STRING, state STRING) AS BEGIN INSERT INTO jobs (id, state) VALUES ($id, $state); END`)
	table, _ := s.lookup("jobs")
	workflow, _ := s.lookupWorkflow("put_job")
	if table == nil || workflow == nil || table.ID == workflow.ID {
		t.Fatalf("catalog identities table=%v workflow=%v", table, workflow)
	}
	for _, sql := range []string{`DROP TABLE jobs`, `ALTER TABLE jobs ADD COLUMN note STRING`} {
		if _, err := s.Exec(sql); err == nil {
			t.Fatalf("referenced table change must be blocked: %s", sql)
		}
	}
	res := execOK(t, s, `RUN WORKFLOW put_job('j1', 'queued')`)
	if res.Affected != 1 {
		t.Fatalf("affected=%d", res.Affected)
	}

	execOK(t, s, `CREATE WORKFLOW finish_job(id STRING) AS BEGIN UPDATE jobs SET state = 'done' WHERE id = $id; END`)
	execOK(t, s, `CREATE WORKFLOW finish_nested(id STRING) AS BEGIN RUN WORKFLOW finish_job($id); END`)
	if got := execOK(t, s, `RUN WORKFLOW finish_nested('j1')`).Affected; got != 1 {
		t.Fatalf("nested affected=%d", got)
	}
	rows := execOK(t, s, `SELECT state FROM jobs WHERE id = 'j1'`).Rows
	if len(rows) != 1 || rows[0][0].String() != "done" {
		t.Fatalf("rows=%v", rows)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := execOK(t, s, `RUN WORKFLOW put_job('j2', 'ready')`).Affected; got != 1 {
		t.Fatalf("restart affected=%d", got)
	}
	prepared, err := s.ExecContext(context.Background(), `RUN WORKFLOW put_job($1, $2)`, []Param{
		{Value: types.StringValue("j3")},
		{Value: types.StringValue("prepared")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Affected != 1 {
		t.Fatalf("prepared affected=%d", prepared.Affected)
	}
	execOK(t, s, `ALTER WORKFLOW finish_nested RENAME TO finish_all`)
	if _, err := s.Exec(`RUN WORKFLOW finish_nested('j2')`); err == nil {
		t.Fatal("old workflow name remained visible")
	}
	if got := execOK(t, s, `RUN WORKFLOW finish_all('j2')`).Affected; got != 1 {
		t.Fatalf("renamed affected=%d", got)
	}
	execOK(t, s, `DROP WORKFLOW finish_all`)
	if _, err := s.Exec(`RUN WORKFLOW finish_all('j2')`); err == nil {
		t.Fatal("dropped workflow remained visible")
	}
}

func TestWorkflowAtomicFailureAndRollback(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE jobs (id STRING PRIMARY KEY, state STRING NOT NULL)`)
	execOK(t, s, `CREATE WORKFLOW duplicate_pair(a STRING, b STRING) AS BEGIN INSERT INTO jobs (id, state) VALUES ($a, 'new'); INSERT INTO jobs (id, state) VALUES ($b, 'new'); END`)
	if _, err := s.Exec(`RUN WORKFLOW duplicate_pair('same', 'same')`); err == nil {
		t.Fatal("expected duplicate-key failure")
	}
	if rows := execOK(t, s, `SELECT id FROM jobs`).Rows; len(rows) != 0 {
		t.Fatalf("workflow failure was not atomic: %v", rows)
	}
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO jobs (id, state) VALUES ('before', 'new')`)
	if _, err := s.Exec(`RUN WORKFLOW duplicate_pair('again', 'again')`); err == nil {
		t.Fatal("expected explicit-transaction workflow failure")
	}
	if s.InTxn() {
		t.Fatal("failed workflow must abort the explicit transaction")
	}
	if rows := execOK(t, s, `SELECT id FROM jobs`).Rows; len(rows) != 0 {
		t.Fatalf("explicit transaction was not rolled back: %v", rows)
	}

	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE WORKFLOW rolled_back(id STRING) AS BEGIN INSERT INTO jobs (id, state) VALUES ($id, 'new'); END`)
	execOK(t, s, `ROLLBACK`)
	if _, err := s.Exec(`RUN WORKFLOW rolled_back('j1')`); err == nil {
		t.Fatal("rolled-back workflow remained visible")
	}
}

func TestWorkflowInvokerRights(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"dba", "app"} {
		if err := users.Upsert(user, "pw"); err != nil {
			t.Fatal(err)
		}
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	execOK(t, admin, `CREATE TABLE jobs (id STRING PRIMARY KEY, state STRING NOT NULL)`)
	execOK(t, admin, `CREATE WORKFLOW put_job(id STRING) AS BEGIN INSERT INTO jobs (id, state) VALUES ($id, 'new'); END`)

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if _, err := app.Exec(`RUN WORKFLOW put_job('j1')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("EXECUTE must be required: %v", err)
	}
	if err := acl.Grant("app", security.PrivExecute, security.ScopeFunction, "put_job"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`RUN WORKFLOW put_job('j1')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("invoker INSERT must be required: %v", err)
	}
	if err := acl.Grant("app", security.PrivInsert, security.ScopeTable, "jobs"); err != nil {
		t.Fatal(err)
	}
	if got := execOK(t, app, `RUN WORKFLOW put_job('j1')`).Affected; got != 1 {
		t.Fatalf("affected=%d", got)
	}
}

func TestWorkflowNestingLimit(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW w8() AS BEGIN INSERT INTO sink (id) VALUES ('too-deep'); END`)
	for i := 7; i >= 0; i-- {
		execOK(t, s, fmt.Sprintf(`CREATE WORKFLOW w%d() AS BEGIN RUN WORKFLOW w%d(); END`, i, i+1))
	}
	if _, err := s.Exec(`RUN WORKFLOW w0()`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("expected nesting limit: %v", err)
	}
	if rows := execOK(t, s, `SELECT id FROM sink`).Rows; len(rows) != 0 {
		t.Fatalf("depth failure was not atomic: %v", rows)
	}
}

func TestWorkflowAuditRedactsArguments(t *testing.T) {
	db := testDB(t)
	path := filepath.Join(t.TempDir(), "audit.log")
	audit, err := security.OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	s := db.Session()
	s.SetIdentity("local")
	s.SetAudit(audit)
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW put(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `RUN WORKFLOW put('workflow-secret-value')`)
	execOK(t, s, `ALTER WORKFLOW put RENAME TO put2`)
	execOK(t, s, `DROP WORKFLOW put2`)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, action := range []string{security.ActionWorkflowCreate, security.ActionWorkflowRun, security.ActionWorkflowAlter, security.ActionWorkflowDrop} {
		if !strings.Contains(text, action) {
			t.Fatalf("missing %s audit event: %s", action, text)
		}
	}
	if strings.Contains(text, "workflow-secret-value") {
		t.Fatalf("workflow argument leaked to audit: %s", text)
	}
}

func TestWorkflowCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE WORKFLOW transient(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s = db.Session()
	if _, err := s.Exec(`RUN WORKFLOW transient('lost')`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("uncommitted workflow survived crash: %v", err)
	}
	execOK(t, s, `CREATE WORKFLOW durable(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s = db.Session()
	execOK(t, s, `BEGIN`)
	execOK(t, s, `RUN WORKFLOW durable('uncommitted')`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if rows := execOK(t, s, `SELECT id FROM sink`).Rows; len(rows) != 0 {
		t.Fatalf("uncommitted workflow effects survived crash: %v", rows)
	}
	if got := execOK(t, s, `RUN WORKFLOW durable('committed')`).Affected; got != 1 {
		t.Fatalf("recovered workflow affected=%d", got)
	}
}

func TestWorkflowLeaderGate(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW put(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	db.SetGate(denyWriteGate{})
	for _, sql := range []string{
		`RUN WORKFLOW put('follower')`,
		`ALTER WORKFLOW put RENAME TO put2`,
		`DROP WORKFLOW put`,
		`CREATE WORKFLOW other() AS BEGIN DELETE FROM sink WHERE id = 'x'; END`,
	} {
		if _, err := s.Exec(sql); !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("follower mutation %q: %v", sql, err)
		}
	}
	db.SetGate(nil)
	if got := execOK(t, s, `RUN WORKFLOW put('leader')`).Affected; got != 1 {
		t.Fatalf("affected=%d", got)
	}
}

func TestWorkflowCancellationAndDistinctLimit(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW put(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ExecContext(ctx, `RUN WORKFLOW put('cancelled')`, nil); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if rows := execOK(t, s, `SELECT id FROM sink`).Rows; len(rows) != 0 {
		t.Fatalf("cancelled workflow changed data: %v", rows)
	}

	var body strings.Builder
	for i := 0; i < maxWorkflowVisited; i++ {
		name := fmt.Sprintf("leaf_%02d", i)
		execOK(t, s, fmt.Sprintf(`CREATE WORKFLOW %s() AS BEGIN DELETE FROM sink WHERE id = 'none'; END`, name))
		fmt.Fprintf(&body, "RUN WORKFLOW %s();", name)
	}
	execOK(t, s, `CREATE WORKFLOW too_many() AS BEGIN `+body.String()+` END`)
	if _, err := s.Exec(`RUN WORKFLOW too_many()`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("expected distinct-workflow limit: %v", err)
	}
}
