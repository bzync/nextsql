package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
)

func TestTriggerCatalogLifecycleRestartAndDependencies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY, state STRING)`)
	execOK(t, s, `CREATE TABLE audit_log (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO audit_log (id) VALUES ($id); END`)
	execOK(t, s, `CREATE TRIGGER audit AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record(NEW.id)`)
	trigger, ok := s.lookupTrigger("audit")
	if !ok || trigger.Table != "orders" || trigger.Workflow != "record" {
		t.Fatalf("trigger=%+v ok=%v", trigger, ok)
	}
	for _, sql := range []string{`DROP TABLE orders`, `ALTER TABLE orders ADD COLUMN note STRING`, `DROP WORKFLOW record`, `ALTER WORKFLOW record RENAME TO record2`} {
		if _, err := s.Exec(sql); err == nil {
			t.Fatalf("trigger dependency did not block %s", sql)
		}
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
	if _, ok := s.lookupTrigger("audit"); !ok {
		t.Fatal("trigger missing after restart")
	}
	execOK(t, s, `ALTER TRIGGER audit RENAME TO audit2`)
	if _, ok := s.lookupTrigger("audit"); ok {
		t.Fatal("old trigger name remained visible")
	}
	execOK(t, s, `DROP TRIGGER audit2`)
	if _, ok := s.lookupTrigger("audit2"); ok {
		t.Fatal("dropped trigger remained visible")
	}
	execOK(t, s, `DROP WORKFLOW record`)
	execOK(t, s, `DROP TABLE orders`)
}

func TestTriggerCatalogRollbackAndLeaderGate(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE audit_log (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO audit_log (id) VALUES ($id); END`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE TRIGGER rolled_back AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record(NEW.id)`)
	execOK(t, s, `ROLLBACK`)
	if _, ok := s.lookupTrigger("rolled_back"); ok {
		t.Fatal("rolled-back trigger remained visible")
	}
	db.SetGate(denyWriteGate{})
	if _, err := s.Exec(`CREATE TRIGGER follower AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record(NEW.id)`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("follower trigger DDL: %v", err)
	}
	db.SetGate(nil)
	if _, ok := s.lookupTrigger("follower"); ok {
		t.Fatal("rejected follower DDL changed catalog")
	}
}

func TestTriggerCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE source (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE audit (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO audit (id) VALUES ($id); END`)
	execOK(t, s, `CREATE TRIGGER durable AFTER INSERT ON source FOR EACH ROW RUN WORKFLOW record(NEW.id)`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s = db.Session()
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO source (id) VALUES ('uncommitted')`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if rows := execOK(t, s, `SELECT id FROM source`).Rows; len(rows) != 0 {
		t.Fatalf("uncommitted source row survived crash: %v", rows)
	}
	if rows := execOK(t, s, `SELECT id FROM audit`).Rows; len(rows) != 0 {
		t.Fatalf("uncommitted trigger effect survived crash: %v", rows)
	}
	execOK(t, s, `INSERT INTO source (id) VALUES ('committed')`)
	if rows := execOK(t, s, `SELECT id FROM audit WHERE id = 'committed'`).Rows; len(rows) != 1 {
		t.Fatalf("recovered trigger did not fire: %v", rows)
	}
}

func TestTriggerFiresBeforeAndAfterAllDML(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY, state STRING)`)
	execOK(t, s, `CREATE TABLE events (seq DECIMAL(18,0) PRIMARY KEY DEFAULT AI(), label STRING NOT NULL, id STRING NOT NULL, old_state STRING, new_state STRING)`)
	execOK(t, s, `CREATE WORKFLOW record(label STRING, id STRING, old_state STRING, new_state STRING) AS BEGIN INSERT INTO events (label, id, old_state, new_state) VALUES ($label, $id, $old_state, $new_state); END`)
	for _, sql := range []string{
		`CREATE TRIGGER bi BEFORE INSERT ON orders FOR EACH ROW RUN WORKFLOW record('before_insert', NEW.id, NULL, NEW.state)`,
		`CREATE TRIGGER ai AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record('after_insert', NEW.id, NULL, NEW.state)`,
		`CREATE TRIGGER bu BEFORE UPDATE ON orders FOR EACH ROW RUN WORKFLOW record('before_update', NEW.id, OLD.state, NEW.state)`,
		`CREATE TRIGGER au AFTER UPDATE ON orders FOR EACH ROW RUN WORKFLOW record('after_update', NEW.id, OLD.state, NEW.state)`,
		`CREATE TRIGGER bd BEFORE DELETE ON orders FOR EACH ROW RUN WORKFLOW record('before_delete', OLD.id, OLD.state, NULL)`,
		`CREATE TRIGGER ad AFTER DELETE ON orders FOR EACH ROW RUN WORKFLOW record('after_delete', OLD.id, OLD.state, NULL)`,
	} {
		execOK(t, s, sql)
	}
	if got := execOK(t, s, `INSERT INTO orders (id, state) VALUES ('o1', 'new')`).Affected; got != 1 {
		t.Fatalf("insert affected=%d", got)
	}
	if got := execOK(t, s, `UPDATE orders SET state = 'done' WHERE id = 'o1'`).Affected; got != 1 {
		t.Fatalf("update affected=%d", got)
	}
	if got := execOK(t, s, `DELETE FROM orders WHERE id = 'o1'`).Affected; got != 1 {
		t.Fatalf("delete affected=%d", got)
	}
	rows := execOK(t, s, `SELECT label FROM events ORDER BY seq`).Rows
	want := []string{"before_insert", "after_insert", "before_update", "after_update", "before_delete", "after_delete"}
	if len(rows) != len(want) {
		t.Fatalf("events=%v", rows)
	}
	for i := range want {
		if rows[i][0].Str != want[i] {
			t.Fatalf("event %d=%s want %s", i, rows[i][0].Str, want[i])
		}
	}
}

func TestTriggerFailureIsAtomic(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE events (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW collide() AS BEGIN INSERT INTO events (id) VALUES ('fixed'); END`)
	execOK(t, s, `CREATE TRIGGER collide_each AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW collide()`)
	if _, err := s.Exec(`INSERT INTO orders (id) VALUES ('a'), ('b')`); err == nil {
		t.Fatal("expected trigger workflow failure")
	}
	if rows := execOK(t, s, `SELECT id FROM orders`).Rows; len(rows) != 0 {
		t.Fatalf("trigger failure retained source rows: %v", rows)
	}
	if rows := execOK(t, s, `SELECT id FROM events`).Rows; len(rows) != 0 {
		t.Fatalf("trigger failure retained workflow rows: %v", rows)
	}
}

func TestTriggerCycleDetection(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE a (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE b (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW write_a(id STRING) AS BEGIN INSERT INTO a (id) VALUES ($id); END`)
	execOK(t, s, `CREATE WORKFLOW write_b(id STRING) AS BEGIN INSERT INTO b (id) VALUES ($id); END`)
	if _, err := s.Exec(`CREATE TRIGGER self_cycle AFTER INSERT ON a FOR EACH ROW RUN WORKFLOW write_a(NEW.id)`); err == nil {
		t.Fatal("accepted direct trigger cycle")
	}
	execOK(t, s, `CREATE TRIGGER a_to_b AFTER INSERT ON a FOR EACH ROW RUN WORKFLOW write_b(NEW.id)`)
	if _, err := s.Exec(`CREATE TRIGGER b_to_a AFTER INSERT ON b FOR EACH ROW RUN WORKFLOW write_a(NEW.id)`); err == nil {
		t.Fatal("accepted indirect trigger cycle")
	}
}

func TestTriggerRuntimeDepthDefense(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE loop_rows (id UUID PRIMARY KEY)`)
	table, _ := s.lookup("loop_rows")
	legacy := &catalog.Workflow{
		ID: db.Cat.NextID(), Name: "legacy_loop", Owner: "local",
		Body: []ast.Stmt{ast.Insert{Table: "loop_rows", Columns: []string{"id"}, Rows: [][]ast.Expr{{ast.Call{Name: "uuid"}}}}},
		// An old descriptor may lack dependency metadata; runtime depth remains
		// a mandatory defense even when static cycle analysis cannot see it.
	}
	raw, err := catalog.EncodeWorkflow(legacy)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.CatTree.BeginTxn(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert(catalog.WorkflowKey(legacy.Name), raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.reloadCatalog(); err != nil {
		t.Fatal(err)
	}
	if table == nil {
		t.Fatal("table missing")
	}
	s = db.Session()
	execOK(t, s, `CREATE TRIGGER legacy_cycle AFTER INSERT ON loop_rows FOR EACH ROW RUN WORKFLOW legacy_loop()`)
	if _, err := s.Exec(`INSERT INTO loop_rows (id) VALUES (UUID())`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("expected trigger depth exhaustion: %v", err)
	}
	if rows := execOK(t, s, `SELECT id FROM loop_rows`).Rows; len(rows) != 0 {
		t.Fatalf("depth exhaustion retained rows: %v", rows)
	}
}

func TestTriggerInvokerRightsAndTenantIsolation(t *testing.T) {
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
	execOK(t, admin, `CREATE TABLE orders (id STRING PRIMARY KEY, tenant_id UUID NOT NULL)`)
	execOK(t, admin, `CREATE TABLE events (id STRING PRIMARY KEY, tenant_id UUID NOT NULL)`)
	execOK(t, admin, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO events (id) VALUES ($id); END`)
	execOK(t, admin, `CREATE TRIGGER audit AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record(NEW.id)`)
	if err := acl.Grant("app", security.PrivInsert, security.ScopeTable, "orders"); err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	execOK(t, app, `SET TENANT = '`+tenantA+`'`)
	if _, err := app.Exec(`INSERT INTO orders (id) VALUES ('no-execute')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("trigger EXECUTE must be required: %v", err)
	}
	if err := acl.Grant("app", security.PrivExecute, security.ScopeFunction, "record"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`INSERT INTO orders (id) VALUES ('no-body-privilege')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("trigger body privilege must be required: %v", err)
	}
	if err := acl.Grant("app", security.PrivInsert, security.ScopeTable, "events"); err != nil {
		t.Fatal(err)
	}
	execOK(t, app, `INSERT INTO orders (id) VALUES ('tenant-a')`)
	execOK(t, app, `SET TENANT = '`+tenantB+`'`)
	execOK(t, app, `INSERT INTO orders (id) VALUES ('tenant-b')`)
	rows := execOK(t, admin, `SELECT id, tenant_id FROM events ORDER BY id`).Rows
	if len(rows) != 2 || rows[0][0].Str != "tenant-a" || rows[1][0].Str != "tenant-b" || rows[0][1].String() == rows[1][1].String() {
		t.Fatalf("tenant trigger rows=%v", rows)
	}
}

func TestTriggerAuditDoesNotLeakRows(t *testing.T) {
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
	execOK(t, s, `CREATE TABLE source (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW copy_row(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE TRIGGER copy_trigger AFTER INSERT ON source FOR EACH ROW RUN WORKFLOW copy_row(NEW.id)`)
	execOK(t, s, `INSERT INTO source (id) VALUES ('trigger-secret-row')`)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, action := range []string{security.ActionTriggerCreate, security.ActionTriggerFire, security.ActionWorkflowRun} {
		if !strings.Contains(text, action) {
			t.Fatalf("missing %s: %s", action, text)
		}
	}
	if strings.Contains(text, "trigger-secret-row") {
		t.Fatalf("trigger row leaked to audit: %s", text)
	}
}
