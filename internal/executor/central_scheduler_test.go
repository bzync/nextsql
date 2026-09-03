package executor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
)

// trackedLister wraps a fixed set of *DB behind a DatabaseLister, counting
// outstanding (handed-out-but-not-yet-released) refs — the invariant every
// test below checks is that this count returns to zero once expected,
// proving CentralScheduler never leaks or double-releases a ref.
type trackedLister struct {
	dbs         []*DB
	outstanding atomic.Int64
	releases    atomic.Int64
}

func (l *trackedLister) list() []DBRef {
	out := make([]DBRef, len(l.dbs))
	var released [16]atomic.Bool // guards against a double-Release per handed-out ref; 16 is well past any test's db count
	for i, db := range l.dbs {
		i := i
		l.outstanding.Add(1)
		out[i] = DBRef{DB: db, Release: func() {
			if released[i].Swap(true) {
				panic("DBRef.Release called more than once")
			}
			l.outstanding.Add(-1)
			l.releases.Add(1)
		}}
	}
	return out
}

func setupDueWorkflow(t *testing.T, db *DB, scheduleName string) (*catalog.Schedule, string) {
	t.Helper()
	s := db.Session()
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE SCHEDULE `+scheduleName+` EVERY '1h' RUN WORKFLOW record('`+scheduleName+`')`)
	schedule := makeScheduleDue(t, db, scheduleName)
	return schedule, scheduledTaskID(schedule.ID, schedule.NextFireNS)
}

func waitTaskSucceeded(t *testing.T, db *DB, id string, deadline time.Time) {
	t.Helper()
	for {
		task, ok, err := db.task(id)
		if err != nil {
			t.Fatal(err)
		}
		if ok && task.State == catalog.TaskSucceeded {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s did not succeed: %+v ok=%v", id, task, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCentralSchedulerAcrossTwoDatabases proves M2-3b-3b's actual point: one
// CentralScheduler, with no per-database poll loop at all, claims and
// executes due work for two independently-registered databases sharing one
// single-worker TaskPool.
func TestCentralSchedulerAcrossTwoDatabases(t *testing.T) {
	dbA := testDB(t)
	dbB := testDB(t)
	_, idA := setupDueWorkflow(t, dbA, "a")
	_, idB := setupDueWorkflow(t, dbB, "b")

	pool, err := NewTaskPool(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	lister := &trackedLister{dbs: []*DB{dbA, dbB}}
	sched, err := StartCentralScheduler(context.Background(), pool, lister.list, TaskRuntimeConfig{
		Batch: 1, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sched.Close()

	deadline := time.Now().Add(3 * time.Second)
	waitTaskSucceeded(t, dbA, idA, deadline)
	waitTaskSucceeded(t, dbB, idB, deadline)
}

// TestCentralSchedulerReleasesEveryRefEventually proves every DBRef handed
// out by the lister is eventually released exactly once — including on
// ticks that submit real, executing work, not just idle ticks with nothing
// due.
func TestCentralSchedulerReleasesEveryRefEventually(t *testing.T) {
	dbA := testDB(t)
	_, idA := setupDueWorkflow(t, dbA, "a")

	pool, err := NewTaskPool(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	lister := &trackedLister{dbs: []*DB{dbA}}
	sched, err := StartCentralScheduler(context.Background(), pool, lister.list, TaskRuntimeConfig{
		Batch: 1, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sched.Close()

	deadline := time.Now().Add(3 * time.Second)
	waitTaskSucceeded(t, dbA, idA, deadline)

	// Let a few more ticks run (now idle — nothing due) to accumulate
	// several list()/release() rounds, then confirm every ref is released.
	// outstanding legitimately oscillates 0->1->0 within a tick — list()
	// takes the ref at the top of cycleOne, ref.Release() drops it at the
	// bottom after the claim/dispatch transactions — so poll for it to
	// settle at zero rather than sampling once at an arbitrary instant.
	settleBy := time.Now().Add(2 * time.Second)
	for lister.outstanding.Load() != 0 {
		if time.Now().After(settleBy) {
			t.Fatalf("outstanding refs = %d, want 0", lister.outstanding.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := lister.releases.Load(); got == 0 {
		t.Fatalf("releases = %d, want > 0", got)
	}
}

// TestCentralSchedulerCloseWaitsOutstandingRefs mirrors
// TestTaskRuntimeCloseAllowsSafeDBCloseWhilePoolShared's real-world shape
// for the centralized case: Close must not return while any ref handed out
// on the last tick is still unreleased, so a caller (dbmanager eviction)
// can safely close the underlying database the instant Close returns.
func TestCentralSchedulerCloseWaitsOutstandingRefs(t *testing.T) {
	dbA := testDB(t)
	_, idA := setupDueWorkflow(t, dbA, "a")

	pool, err := NewTaskPool(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	lister := &trackedLister{dbs: []*DB{dbA}}
	sched, err := StartCentralScheduler(context.Background(), pool, lister.list, TaskRuntimeConfig{
		Batch: 1, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	waitTaskSucceeded(t, dbA, idA, time.Now().Add(3*time.Second))
	if err := sched.Close(); err != nil {
		t.Fatal(err)
	}
	if got := lister.outstanding.Load(); got != 0 {
		t.Fatalf("outstanding refs after Close = %d, want 0", got)
	}
	// Safe to close the database now — proves no pool worker could still be
	// holding it, the same property TaskRuntime.Close's inFlight gives a
	// single database.
	if err := dbA.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartCentralSchedulerValidatesArgs(t *testing.T) {
	pool, err := NewTaskPool(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := StartCentralScheduler(context.Background(), nil, func() []DBRef { return nil }, TaskRuntimeConfig{}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("nil pool err=%v", err)
	}
	if _, err := StartCentralScheduler(context.Background(), pool, nil, TaskRuntimeConfig{}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("nil lister err=%v", err)
	}
}
