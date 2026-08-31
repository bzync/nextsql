package executor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/txn"
)

func createDueScheduledTask(t *testing.T, db *DB, scheduleName string) (*catalog.Schedule, string) {
	t.Helper()
	schedule := makeScheduleDue(t, db, scheduleName)
	if got, err := db.DispatchDueSchedules(context.Background(), time.Unix(0, schedule.NextFireNS), 1); err != nil || got != 1 {
		t.Fatalf("dispatch got=%d err=%v", got, err)
	}
	return schedule, scheduledTaskID(schedule.ID, schedule.NextFireNS)
}

func TestTaskOwnerIsolationAndInvokerRights(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	execOK(t, admin, `CREATE TABLE sink_acl (id STRING PRIMARY KEY)`)
	execOK(t, admin, `CREATE WORKFLOW record_acl(id STRING) AS BEGIN INSERT INTO sink_acl (id) VALUES ($id); END`)
	for _, grant := range []struct {
		user   string
		priv   security.Privilege
		scope  security.ScopeKind
		object string
	}{
		{"app", security.PrivConnect, security.ScopeDatabase, ""},
		{"app", security.PrivCreate, security.ScopeDatabase, ""},
		{"app", security.PrivExecute, security.ScopeFunction, "record_acl"},
		{"app", security.PrivInsert, security.ScopeTable, "sink_acl"},
		{"other", security.PrivConnect, security.ScopeDatabase, ""},
	} {
		if err := acl.Grant(grant.user, grant.priv, grant.scope, grant.object); err != nil {
			t.Fatal(err)
		}
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	execOK(t, app, `CREATE SCHEDULE private_acl EVERY '1h' RUN WORKFLOW record_acl('private')`)
	schedule, id := createDueScheduledTask(t, db, "private_acl")
	other := db.Session()
	other.SetIdentity("other")
	other.SetACL(acl)
	if rows := execOK(t, other, `SHOW TASKS`).Rows; len(rows) != 0 {
		t.Fatalf("cross-owner rows=%+v", rows)
	}
	if _, err := other.Exec(`CANCEL TASK '` + id + `'`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("cross-owner cancel=%v", err)
	}
	claim := claimOneTask(t, db, schedule.NextFireNS)
	if err := acl.Revoke("app", security.PrivInsert, security.ScopeTable, "sink_acl"); err != nil {
		t.Fatal(err)
	}
	if err := db.ExecuteClaimedTask(context.Background(), claim, acl, nil, scheduler.DefaultLimits()); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("revoked invoker right err=%v", err)
	}
	if got := execOK(t, app, `CANCEL TASK '`+id+`'`); got.Affected != 1 {
		t.Fatalf("owner cancel affected=%d", got.Affected)
	}
}

func makeScheduleDue(t *testing.T, db *DB, scheduleName string) *catalog.Schedule {
	t.Helper()
	schedule, ok := db.schedule(scheduleName)
	if !ok {
		t.Fatalf("schedule %q missing", scheduleName)
	}
	oldNext := schedule.NextFireNS
	schedule.NextFireNS = schedule.CreatedNS
	raw, err := catalog.EncodeSchedule(schedule)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	if err := s.start(txn.SnapshotIsolation); err != nil {
		t.Fatal(err)
	}
	tx := s.x.use(db.CatTree)
	if err := tx.Update(catalog.ScheduleKey(schedule.Name), raw); err != nil {
		_ = s.abort()
		t.Fatal(err)
	}
	if err := tx.Delete(catalog.ScheduleDueKey(oldNext, schedule.ID)); err != nil {
		_ = s.abort()
		t.Fatal(err)
	}
	if err := tx.Insert(catalog.ScheduleDueKey(schedule.NextFireNS, schedule.ID), []byte(schedule.Name)); err != nil {
		_ = s.abort()
		t.Fatal(err)
	}
	s.scheduleOverlay[schedule.Name] = schedule.Clone()
	if _, err := s.commit(); err != nil {
		t.Fatal(err)
	}
	return schedule
}

func TestTaskRuntimeAutomaticallyDispatchesAndExecutes(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE automatic EVERY '1h' RUN WORKFLOW record('automatic')`)
	schedule := makeScheduleDue(t, db, "automatic")
	id := scheduledTaskID(schedule.ID, schedule.NextFireNS)
	errCh := make(chan error, 8)
	runtime, err := StartTaskRuntime(context.Background(), db, TaskRuntimeConfig{
		Workers: 1, Batch: 1, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour,
		OnError: func(err error) { errCh <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, ok, taskErr := db.task(id)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		if ok && task.State == catalog.TaskSucceeded {
			break
		}
		select {
		case runtimeErr := <-errCh:
			t.Fatalf("runtime error: %v", runtimeErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("task did not succeed: %+v ok=%v", task, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rows := execOK(t, s, `SELECT id FROM sink WHERE id = 'automatic'`); len(rows.Rows) != 1 {
		t.Fatalf("automatic rows=%+v", rows.Rows)
	}
}

func TestTaskRuntimeBoundsAndFollowerDoNotDispatch(t *testing.T) {
	db := testDB(t)
	if _, err := StartTaskRuntime(context.Background(), db, TaskRuntimeConfig{Workers: maxTaskWorkers + 1}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("unbounded workers err=%v", err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE follower EVERY '1h' RUN WORKFLOW record('follower')`)
	schedule := makeScheduleDue(t, db, "follower")
	db.SetGate(denyWriteGate{})
	runtime, err := StartTaskRuntime(context.Background(), db, TaskRuntimeConfig{Workers: 1, Batch: 1, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.task(scheduledTaskID(schedule.ID, schedule.NextFireNS)); err != nil || ok {
		t.Fatalf("follower created task ok=%v err=%v", ok, err)
	}
}

func TestActiveTaskProtectsWorkflowDependency(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE dependency EVERY '1h' RUN WORKFLOW record('dependency')`)
	_, id := createDueScheduledTask(t, db, "dependency")
	execOK(t, s, `DROP SCHEDULE dependency`)
	if _, err := s.Exec(`DROP WORKFLOW record`); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("active task did not protect workflow: %v", err)
	}
	if _, err := db.RequestTaskCancellation(context.Background(), id, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	execOK(t, s, `DROP WORKFLOW record`)
}

func TestShowTasksPaginationAndSQLCancellation(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE first EVERY '1h' RUN WORKFLOW record('first')`)
	execOK(t, s, `CREATE SCHEDULE second EVERY '1h' RUN WORKFLOW record('second')`)
	_, firstID := createDueScheduledTask(t, db, "first")
	_, secondID := createDueScheduledTask(t, db, "second")
	show := execOK(t, s, `SHOW TASKS LIMIT 1`)
	if len(show.Rows) != 1 || len(show.Columns) != 14 {
		t.Fatalf("show rows=%+v columns=%v", show.Rows, show.Columns)
	}
	firstPageID := show.Rows[0][0].Str
	next := execOK(t, s, `SHOW TASKS AFTER '`+firstPageID+`' LIMIT 1`)
	if len(next.Rows) != 1 || next.Rows[0][0].Str == firstPageID {
		t.Fatalf("next rows=%+v", next.Rows)
	}
	if firstPageID != firstID && firstPageID != secondID {
		t.Fatalf("unexpected task id %q", firstPageID)
	}
	if got := execOK(t, s, `CANCEL TASK '`+firstID+`'`); got.Affected != 1 {
		t.Fatalf("cancel affected=%d", got.Affected)
	}
	task, ok, err := db.task(firstID)
	if err != nil || !ok || task.State != catalog.TaskCancelled {
		t.Fatalf("task=%+v ok=%v err=%v", task, ok, err)
	}
	if got := execOK(t, s, `CANCEL TASK '`+firstID+`'`); got.Affected != 0 {
		t.Fatalf("repeat cancel affected=%d", got.Affected)
	}
}

func TestRunningTaskLeaseRecoversAfterCrashRestart(t *testing.T) {
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
	execOK(t, s, `CREATE SCHEDULE crash EVERY '1h' RUN WORKFLOW record('after-crash')`)
	schedule, id := createDueScheduledTask(t, db, "crash")
	first := claimOneTask(t, db, schedule.NextFireNS)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	second := claimOneTask(t, db, first.LeaseUntilNS)
	if second.Attempt != 2 {
		t.Fatalf("recovered claim=%+v", second)
	}
	execAt := time.Unix(0, second.UpdatedNS+1)
	if err := db.executeClaimedTask(context.Background(), second, nil, nil, scheduler.DefaultLimits(), func() time.Time { return execAt }); err != nil {
		t.Fatal(err)
	}
	task, ok, err := db.task(id)
	if err != nil || !ok || task.State != catalog.TaskSucceeded || task.Attempt != 2 {
		t.Fatalf("recovered task=%+v ok=%v err=%v", task, ok, err)
	}
	if rows := execOK(t, db.Session(), `SELECT id FROM sink WHERE id = 'after-crash'`); len(rows.Rows) != 1 {
		t.Fatalf("recovered rows=%+v", rows.Rows)
	}
}

func TestTaskDueIndexCorruptionFailsClosed(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE corrupt EVERY '1h' RUN WORKFLOW record('x')`)
	schedule, id := createDueScheduledTask(t, db, "corrupt")
	if err := s.start(txn.SnapshotIsolation); err != nil {
		t.Fatal(err)
	}
	if err := s.x.use(db.CatTree).Delete(catalog.TaskKey(id)); err != nil {
		_ = s.abort()
		t.Fatal(err)
	}
	if _, err := s.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimDueTasks(context.Background(), time.Unix(0, schedule.NextFireNS), 1); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("dangling due index err=%v", err)
	}
}

func TestTaskMissingActiveReservationFailsClosed(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE missing_active EVERY '1h' RUN WORKFLOW record('x')`)
	schedule, id := createDueScheduledTask(t, db, "missing_active")
	if err := s.start(txn.SnapshotIsolation); err != nil {
		t.Fatal(err)
	}
	if err := s.x.use(db.CatTree).Delete(catalog.TaskActiveKey(catalog.TaskSourceSchedule, schedule.ID)); err != nil {
		_ = s.abort()
		t.Fatal(err)
	}
	if _, err := s.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimDueTasks(context.Background(), time.Unix(0, schedule.NextFireNS), 1); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("missing active reservation for task %q err=%v", id, err)
	}
	task, ok, err := db.task(id)
	if err != nil || !ok || task.State != catalog.TaskPending {
		t.Fatalf("corrupt task mutated task=%+v ok=%v err=%v", task, ok, err)
	}
}

func TestScheduledTaskMissingActiveIndexFailsClosed(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE corrupt_active EVERY '1h' RUN WORKFLOW record('x')`)
	original, id := createDueScheduledTask(t, db, "corrupt_active")
	advanced, ok := db.schedule("corrupt_active")
	if !ok {
		t.Fatal("advanced schedule missing")
	}
	advanced.NextFireNS = original.NextFireNS
	raw, err := catalog.EncodeSchedule(advanced)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.start(txn.SnapshotIsolation); err != nil {
		t.Fatal(err)
	}
	tx := s.x.use(db.CatTree)
	if err := tx.Update(catalog.ScheduleKey(advanced.Name), raw); err == nil {
		err = tx.Delete(catalog.ScheduleDueKey(original.NextFireNS+int64(time.Hour), original.ID))
	}
	if err == nil {
		err = tx.Insert(catalog.ScheduleDueKey(original.NextFireNS, original.ID), []byte(original.Name))
	}
	if err == nil {
		err = tx.Delete(catalog.TaskActiveKey(catalog.TaskSourceSchedule, original.ID))
	}
	if err != nil {
		_ = s.abort()
		t.Fatal(err)
	}
	s.scheduleOverlay[advanced.Name] = advanced.Clone()
	if _, err := s.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DispatchDueSchedules(context.Background(), time.Unix(0, original.NextFireNS), 1); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("missing active index for task %q err=%v", id, err)
	}
}

func claimOneTask(t *testing.T, db *DB, at int64) *catalog.Task {
	t.Helper()
	claimed, err := db.ClaimDueTasks(context.Background(), time.Unix(0, at), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed=%d", len(claimed))
	}
	return claimed[0]
}

func TestTaskClaimExecuteAndRetentionAreDurable(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW record('task-success')`)
	schedule, id := createDueScheduledTask(t, db, "hourly")
	claim := claimOneTask(t, db, schedule.NextFireNS)
	if claim.ID != id || claim.State != catalog.TaskRunning || claim.Attempt != 1 || claim.LeaseUntilNS <= claim.UpdatedNS || claim.DueNS != claim.LeaseUntilNS {
		t.Fatalf("claim=%+v", claim)
	}
	if err := db.ExecuteClaimedTask(context.Background(), claim, nil, nil, scheduler.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	task, ok, err := db.task(id)
	if err != nil || !ok || task.State != catalog.TaskSucceeded || task.LeaseUntilNS != 0 || task.DueNS != 0 || task.Attempt != 1 {
		t.Fatalf("task=%+v ok=%v err=%v", task, ok, err)
	}
	rows := execOK(t, s, `SELECT id FROM sink WHERE id = 'task-success'`)
	if len(rows.Rows) != 1 || rows.Rows[0][0].Str != "task-success" {
		t.Fatalf("rows=%+v", rows.Rows)
	}
	if got, err := db.PurgeTaskHistory(context.Background(), time.Unix(0, task.RetentionUntilNS-1), 1); err != nil || got != 0 {
		t.Fatalf("early purge got=%d err=%v", got, err)
	}
	if got, err := db.PurgeTaskHistory(context.Background(), time.Unix(0, task.RetentionUntilNS), 1); err != nil || got != 1 {
		t.Fatalf("purge got=%d err=%v", got, err)
	}
	if _, ok, err := db.task(id); err != nil || ok {
		t.Fatalf("purged task ok=%v err=%v", ok, err)
	}
}

func TestTaskFailureRollsBackAndRetriesToFinalFailed(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `INSERT INTO sink (id) VALUES ('duplicate')`)
	execOK(t, s, `CREATE WORKFLOW fail_atomically(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); INSERT INTO sink (id) VALUES ('duplicate'); END`)
	execOK(t, s, `CREATE SCHEDULE retrying EVERY '1h' RUN WORKFLOW fail_atomically('must-rollback')`)
	schedule, id := createDueScheduledTask(t, db, "retrying")
	pending, _, err := db.task(id)
	if err != nil {
		t.Fatal(err)
	}
	pending.MaxAttempts = 2
	pending.RetryBackoffNS = 0
	txSession := db.Session()
	if err := txSession.start(txn.SnapshotIsolation); err != nil {
		t.Fatal(err)
	}
	raw, err := catalog.EncodeTask(pending)
	if err == nil {
		err = txSession.x.use(db.CatTree).Update(catalog.TaskKey(pending.ID), raw)
	}
	if err != nil {
		_ = txSession.abort()
		t.Fatal(err)
	}
	if _, err := txSession.commit(); err != nil {
		t.Fatal(err)
	}
	claim := claimOneTask(t, db, schedule.NextFireNS)
	if err := db.ExecuteClaimedTask(context.Background(), claim, nil, nil, scheduler.DefaultLimits()); err == nil {
		t.Fatal("failing workflow succeeded")
	}
	task, _, err := db.task(id)
	if err != nil || task.State != catalog.TaskRetrying || task.Attempt != 1 || task.LeaseUntilNS != 0 || task.ErrorCode == "" {
		t.Fatalf("retry task=%+v err=%v", task, err)
	}
	if rows := execOK(t, s, `SELECT id FROM sink WHERE id = 'must-rollback'`); len(rows.Rows) != 0 {
		t.Fatalf("failed body committed rows=%+v", rows.Rows)
	}
	claim = claimOneTask(t, db, task.DueNS)
	if err := db.ExecuteClaimedTask(context.Background(), claim, nil, nil, scheduler.DefaultLimits()); err == nil {
		t.Fatal("second failing workflow succeeded")
	}
	task, _, err = db.task(id)
	if err != nil || task.State != catalog.TaskFinalFailed || task.Attempt != 2 || task.ErrorCode == "" {
		t.Fatalf("final task=%+v err=%v", task, err)
	}
}

func TestTaskPermanentCatalogFailureDoesNotRetry(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE corrupt EVERY '1h' RUN WORKFLOW record('never-run')`)
	schedule, id := createDueScheduledTask(t, db, "corrupt")
	claim := claimOneTask(t, db, schedule.NextFireNS)
	failureAt := time.Unix(0, claim.UpdatedNS+1)
	if err := db.finishTaskFailure(context.Background(), claim, failureAt, nerr.New(nerr.Corruption, "test", "catalog corrupt")); err != nil {
		t.Fatal(err)
	}
	task, ok, err := db.task(id)
	if err != nil || !ok || task.State != catalog.TaskFailed || task.DueNS != 0 || task.LeaseUntilNS != 0 || task.ErrorCode != string(nerr.Corruption) {
		t.Fatalf("permanent failure task=%+v ok=%v err=%v", task, ok, err)
	}
	if claims, err := db.ClaimDueTasks(context.Background(), failureAt.Add(24*time.Hour), 1); err != nil || len(claims) != 0 {
		t.Fatalf("permanent failure was retryable claims=%d err=%v", len(claims), err)
	}
}

func TestTaskLeaseRecoveryAndCancellation(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE leased EVERY '1h' RUN WORKFLOW record('never-run')`)
	schedule, _ := createDueScheduledTask(t, db, "leased")
	first := claimOneTask(t, db, schedule.NextFireNS)
	if claims, err := db.ClaimDueTasks(context.Background(), time.Unix(0, first.LeaseUntilNS-1), 1); err != nil || len(claims) != 0 {
		t.Fatalf("premature reclaim=%d err=%v", len(claims), err)
	}
	second := claimOneTask(t, db, first.LeaseUntilNS)
	if second.Attempt != 2 || second.LeaseUntilNS <= first.LeaseUntilNS {
		t.Fatalf("reclaimed=%+v first=%+v", second, first)
	}

	execOK(t, s, `CREATE SCHEDULE cancelled EVERY '1h' RUN WORKFLOW record('never-run-cancelled')`)
	cancelSchedule, cancelID := createDueScheduledTask(t, db, "cancelled")
	cancelClaim := claimOneTask(t, db, cancelSchedule.NextFireNS)
	cancelAt := cancelClaim.UpdatedNS + 1
	cancelled, err := db.RequestTaskCancellation(context.Background(), cancelID, time.Unix(0, cancelAt))
	if err != nil || !cancelled.CancelRequested || cancelled.State != catalog.TaskRunning {
		t.Fatalf("cancelled=%+v err=%v", cancelled, err)
	}
	if err := db.ExecuteClaimedTask(context.Background(), cancelClaim, nil, nil, scheduler.DefaultLimits()); !nerr.HasCode(err, nerr.Canceled) {
		t.Fatalf("cancel execution err=%v", err)
	}
	task, _, err := db.task(cancelID)
	if err != nil || task.State != catalog.TaskCancelled || task.LeaseUntilNS != 0 {
		t.Fatalf("cancelled task=%+v err=%v", task, err)
	}
	if rows := execOK(t, s, `SELECT id FROM sink WHERE id = 'never-run-cancelled'`); len(rows.Rows) != 0 {
		t.Fatalf("cancelled task committed rows=%+v", rows.Rows)
	}
}
