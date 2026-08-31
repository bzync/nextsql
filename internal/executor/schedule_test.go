package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/txn"
)

func TestScheduleCatalogLifecycleRestartAndDependencies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW record('hour')`)
	schedule, ok := s.lookupSchedule("hourly")
	if !ok || schedule.Kind != ast.ScheduleEvery || schedule.SpecNS != int64(time.Hour) || schedule.Tenant != "" {
		t.Fatalf("schedule=%+v ok=%v", schedule, ok)
	}
	if _, err := s.Exec(`DROP WORKFLOW record`); err == nil {
		t.Fatal("schedule did not protect workflow drop")
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
	if schedule, ok = s.lookupSchedule("hourly"); !ok || schedule.Workflow != "record" || schedule.WorkflowID == 0 {
		t.Fatalf("reloaded schedule=%+v ok=%v", schedule, ok)
	}
	execOK(t, s, `ALTER SCHEDULE hourly RENAME TO hourly2`)
	if _, ok := s.lookupSchedule("hourly"); ok {
		t.Fatal("old schedule name remained visible")
	}
	execOK(t, s, `DROP SCHEDULE hourly2`)
	execOK(t, s, `DROP WORKFLOW record`)
}

func TestScheduleRollbackAndLeaderGate(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE SCHEDULE rolled_back EVERY '1h' RUN WORKFLOW record('x')`)
	execOK(t, s, `ROLLBACK`)
	if _, ok := s.lookupSchedule("rolled_back"); ok {
		t.Fatal("rolled-back schedule remained visible")
	}
	db.SetGate(denyWriteGate{})
	if _, err := s.Exec(`CREATE SCHEDULE follower EVERY '1h' RUN WORKFLOW record('x')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("follower schedule create: %v", err)
	}
	if _, ok := s.lookupSchedule("follower"); ok {
		t.Fatal("follower schedule mutation became visible")
	}
}

func TestScheduleAtCanonicalTimestamp(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	local := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second).
		In(time.FixedZone("UTC+8", 8*60*60))
	execOK(t, s, fmt.Sprintf(`CREATE SCHEDULE once AT '%s' RUN WORKFLOW record('once')`, local.Format(time.RFC3339)))
	schedule, ok := s.lookupSchedule("once")
	if !ok {
		t.Fatal("schedule missing")
	}
	want := local.UTC()
	if schedule.Kind != ast.ScheduleAt || schedule.SpecNS != want.UnixNano() {
		t.Fatalf("schedule=%+v", schedule)
	}
}

func TestScheduleCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE SCHEDULE transient EVERY '1h' RUN WORKFLOW record('x')`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s = db.Session()
	if _, ok := s.lookupSchedule("transient"); ok {
		t.Fatal("uncommitted schedule survived crash")
	}
	execOK(t, s, `CREATE SCHEDULE durable EVERY '1h' RUN WORKFLOW record('x')`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if schedule, ok := db.Session().lookupSchedule("durable"); !ok || schedule.Workflow != "record" {
		t.Fatalf("durable schedule missing after crash: %+v ok=%v", schedule, ok)
	}
}

func TestScheduleReloadRejectsMissingDueIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE corrupt EVERY '1h' RUN WORKFLOW record('x')`)
	schedule, _ := db.schedule("corrupt")
	if err := s.start(txn.SnapshotIsolation); err != nil {
		t.Fatal(err)
	}
	if err := s.x.use(db.CatTree).Delete(catalog.ScheduleDueKey(schedule.NextFireNS, schedule.ID)); err != nil {
		_ = s.abort()
		t.Fatal(err)
	}
	if _, err := s.commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(path, keys, 32); !nerr.HasCode(err, nerr.InvalidFormat) {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatalf("missing due index open=%v", err)
	}
}

func TestScheduleDefinitionRBAC(t *testing.T) {
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
	execOK(t, admin, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, admin, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if _, err := app.Exec(`CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW record('x')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("CREATE must be required: %v", err)
	}
	if err := acl.Grant("app", security.PrivCreate, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW record('x')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("workflow EXECUTE must be required: %v", err)
	}
	if err := acl.Grant("app", security.PrivExecute, security.ScopeFunction, "record"); err != nil {
		t.Fatal(err)
	}
	execOK(t, app, `CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW record('x')`)
	if schedule, _ := app.lookupSchedule("hourly"); schedule == nil || schedule.Owner != "app" {
		t.Fatalf("schedule owner=%+v", schedule)
	}
	if _, err := app.Exec(`ALTER SCHEDULE hourly RENAME TO hourly2`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ALTER must be required: %v", err)
	}
	if err := acl.Grant("app", security.PrivAlter, security.ScopeFunction, "hourly"); err != nil {
		t.Fatal(err)
	}
	execOK(t, app, `ALTER SCHEDULE hourly RENAME TO hourly2`)
	if _, err := app.Exec(`DROP SCHEDULE hourly2`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("DROP must be required: %v", err)
	}
	if err := acl.Grant("app", security.PrivDrop, security.ScopeFunction, "hourly2"); err != nil {
		t.Fatal(err)
	}
	execOK(t, app, `DROP SCHEDULE hourly2`)
}

func TestScheduleAuditRedactsArguments(t *testing.T) {
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
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE private EVERY '1h' RUN WORKFLOW record('schedule-secret-value')`)
	execOK(t, s, `ALTER SCHEDULE private RENAME TO private2`)
	execOK(t, s, `DROP SCHEDULE private2`)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, action := range []string{security.ActionScheduleCreate, security.ActionScheduleAlter, security.ActionScheduleDrop} {
		if !strings.Contains(text, action) {
			t.Fatalf("missing %s audit event: %s", action, text)
		}
	}
	if strings.Contains(text, "schedule-secret-value") {
		t.Fatalf("schedule argument leaked to audit: %s", text)
	}
}

func TestDispatchDueSchedulesCreatesDurableTaskAndAdvancesCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW record('hour')`)
	schedule, _ := s.lookupSchedule("hourly")
	due := schedule.NextFireNS
	if got, err := db.DispatchDueSchedules(context.Background(), time.Unix(0, due), 16); err != nil || got != 1 {
		t.Fatalf("dispatch got=%d err=%v", got, err)
	}
	id := scheduledTaskID(schedule.ID, due)
	task, ok, err := db.task(id)
	if err != nil || !ok {
		t.Fatalf("task=%+v ok=%v err=%v", task, ok, err)
	}
	if task.State != catalog.TaskPending || task.Source != catalog.TaskSourceSchedule || task.ScheduleID != schedule.ID || task.WorkflowID != schedule.WorkflowID || task.Tenant != schedule.Tenant || task.IdempotencyKey != id {
		t.Fatalf("task=%+v", task)
	}
	advanced, _ := db.schedule("hourly")
	if advanced.LastFireNS != due || advanced.NextFireNS != due+int64(time.Hour) || !advanced.Enabled {
		t.Fatalf("advanced schedule=%+v", advanced)
	}
	if got, err := db.DispatchDueSchedules(context.Background(), time.Unix(0, due), 16); err != nil || got != 0 {
		t.Fatalf("duplicate dispatch got=%d err=%v", got, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if task, ok, err = db.task(id); err != nil || !ok || task.IdempotencyKey != id {
		t.Fatalf("reloaded task=%+v ok=%v err=%v", task, ok, err)
	}
	if advanced, ok = db.schedule("hourly"); !ok || advanced.NextFireNS != due+int64(time.Hour) {
		t.Fatalf("reloaded schedule=%+v ok=%v", advanced, ok)
	}
}

func TestDispatchOneShotDisablesAndSkipsMissedIntervals(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE recurring EVERY '1h' RUN WORKFLOW record('r')`)
	recurring, _ := db.schedule("recurring")
	now := recurring.NextFireNS + int64(5*time.Hour) + 1
	if got, err := db.DispatchDueSchedules(context.Background(), time.Unix(0, now), 16); err != nil || got != 1 {
		t.Fatalf("recurring dispatch got=%d err=%v", got, err)
	}
	recurring, _ = db.schedule("recurring")
	if recurring.NextFireNS <= now || recurring.LastFireNS == 0 {
		t.Fatalf("missed interval cursor=%+v", recurring)
	}

	at := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	execOK(t, s, `CREATE SCHEDULE once AT '`+at.Format(time.RFC3339)+`' RUN WORKFLOW record('once')`)
	once, _ := db.schedule("once")
	if got, err := db.DispatchDueSchedules(context.Background(), time.Unix(0, once.NextFireNS), 16); err != nil || got != 1 {
		t.Fatalf("one-shot dispatch got=%d err=%v", got, err)
	}
	once, _ = db.schedule("once")
	if once.Enabled || once.NextFireNS != 0 || once.LastFireNS == 0 {
		t.Fatalf("one-shot schedule=%+v", once)
	}
}

func TestDispatchForbidBoundsOutstandingTaskPerSchedule(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE bounded EVERY '1s' RUN WORKFLOW record('bounded')`)
	schedule, _ := db.schedule("bounded")
	if got, err := db.DispatchDueSchedules(context.Background(), time.Unix(0, schedule.NextFireNS), 1); err != nil || got != 1 {
		t.Fatalf("first dispatch got=%d err=%v", got, err)
	}
	advanced, _ := db.schedule("bounded")
	if got, err := db.DispatchDueSchedules(context.Background(), time.Unix(0, advanced.NextFireNS), 1); err != nil || got != 0 {
		t.Fatalf("blocked dispatch got=%d err=%v", got, err)
	}
	shown := execOK(t, s, `SHOW TASKS LIMIT 10`)
	if len(shown.Rows) != 1 {
		t.Fatalf("outstanding tasks=%d rows=%+v", len(shown.Rows), shown.Rows)
	}
	after, _ := db.schedule("bounded")
	if after.NextFireNS <= advanced.NextFireNS {
		t.Fatalf("blocked schedule cursor did not advance: before=%+v after=%+v", advanced, after)
	}
}

func TestDispatchDueSchedulesBoundsCancellationAndLeader(t *testing.T) {
	db := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.DispatchDueSchedules(ctx, time.Now(), 1); !nerr.HasCode(err, nerr.Canceled) {
		t.Fatalf("cancelled dispatch: %v", err)
	}
	if _, err := db.DispatchDueSchedules(context.Background(), time.Now(), maxDispatchBatch+1); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("unbounded dispatch: %v", err)
	}
	db.SetGate(denyWriteGate{})
	if _, err := db.DispatchDueSchedules(context.Background(), time.Now(), 1); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("follower dispatch: %v", err)
	}
}
