package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/maintenance"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/undo"
)

func TestAutomaticMaintenanceSchedulingPolicy(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE auto_maint (id STRING PRIMARY KEY)`)
	var sql strings.Builder
	sql.WriteString(`INSERT INTO auto_maint (id) VALUES `)
	for i := 0; i < autoMaintenanceMinChanges; i++ {
		if i > 0 {
			sql.WriteByte(',')
		}
		fmt.Fprintf(&sql, "('%d')", i)
	}
	execOK(t, s, sql.String())
	deleted := execOK(t, s, `DELETE FROM auto_maint`)
	if deleted.Affected != autoMaintenanceMinChanges {
		t.Fatalf("deleted %d rows", deleted.Affected)
	}
	heap, _ := db.heap("auto_maint")
	if rows := physicalRows(t, heap); rows != 0 {
		t.Fatalf("automatic maintenance retained %d physical rows", rows)
	}
	status := db.MaintenanceStatus()
	if status.Last == nil || status.Last.Scope != "auto_maint" || status.Last.Failed {
		t.Fatalf("automatic maintenance status: %+v", status)
	}
}

func TestMaintainSQLObeysAdmission(t *testing.T) {
	db := testDB(t)
	db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{MaxInflight: 1, MaxQueue: 0, QueueWait: time.Millisecond}))
	release, err := db.Admission().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Session().Exec(`MAINTAIN DATABASE`)
	release()
	if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("maintenance bypassed admission: %v", err)
	}
	if db.MaintenanceStatus().Last != nil {
		t.Fatal("rejected maintenance entered coordinator")
	}
	if db.Metrics().Snapshot().Rejected != 1 {
		t.Fatal("maintenance admission rejection not counted")
	}
}

func TestMaintainMemoryBudgetPreservesPendingTombstone(t *testing.T) {
	db := testDB(t)
	reader := db.Session()
	writer := db.Session()
	execOK(t, writer, `CREATE TABLE budget_rows (id STRING PRIMARY KEY)`)
	execOK(t, writer, `INSERT INTO budget_rows (id) VALUES ('a-long-key')`)
	execOK(t, reader, `BEGIN SNAPSHOT`)
	execOK(t, reader, `SELECT id FROM budget_rows`)
	execOK(t, writer, `DELETE FROM budget_rows`)
	execOK(t, reader, `COMMIT`)
	if err := db.SetMaintenanceLimits(maintenance.Limits{CPU: time.Second, Memory: 1, IO: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`MAINTAIN TABLE budget_rows`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("memory budget: %v", err)
	}
	heap, _ := db.heap("budget_rows")
	if physicalRows(t, heap) != 1 {
		t.Fatal("memory exhaustion removed pending tombstone")
	}
	if st := db.MaintenanceStatus(); st.Last == nil || !st.Last.Failed {
		t.Fatalf("budget failure status %+v", st)
	}
	if err := db.SetMaintenanceLimits(maintenance.Limits{CPU: time.Second, Memory: 1 << 20, IO: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`MAINTAIN TABLE budget_rows`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("I/O budget: %v", err)
	}
	if physicalRows(t, heap) != 1 {
		t.Fatal("I/O exhaustion removed pending tombstone")
	}
	if st := db.MaintenanceStatus(); st.Last == nil || st.Last.IOUsed != 1 {
		t.Fatalf("I/O budget status %+v", st)
	}
	if err := db.SetMaintenanceLimits(maintenance.DefaultLimits); err != nil {
		t.Fatal(err)
	}
	if got := execOK(t, writer, `MAINTAIN TABLE budget_rows`); got.Affected != 1 {
		t.Fatalf("resumed bounded cleanup = %d", got.Affected)
	}
}

func TestMaintainDatabaseCompactsUndoAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE undo_rows (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO undo_rows (id, value) VALUES ('a', 'old')`)
	execOK(t, s, `UPDATE undo_rows SET value = 'new' WHERE id = 'a'`)
	logPath := filepath.Join(undo.DirFor(path), "undo.log")
	before, err := os.Stat(logPath)
	if err != nil || before.Size() == 0 {
		t.Fatalf("undo before maintenance: %v size=%d", err, before.Size())
	}
	execOK(t, s, `MAINTAIN DATABASE`)
	after, err := os.Stat(logPath)
	if err != nil || after.Size() >= before.Size() {
		t.Fatalf("undo after maintenance: %v before=%d after=%d", err, before.Size(), after.Size())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got := execOK(t, db.Session(), `SELECT value FROM undo_rows WHERE id = 'a'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "new" {
		t.Fatalf("restart after undo compaction %+v", got.Rows)
	}
}

func physicalRows(t *testing.T, tr *btree.Tree) int64 {
	t.Helper()
	tx, err := tr.BeginRead(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	err = tx.RangeLive(func(_, _ []byte) error {
		n++
		return nil
	})
	tx.MarkDone()
	tr.Engine().TM.EndRead(tx.Handle().ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCleanupDeadVersionsWaitsForSnapshotAndHonorsLimit(t *testing.T) {
	db := testDB(t)
	reader := db.Session()
	writer := db.Session()
	execOK(t, writer, `CREATE TABLE cleanup_rows (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, writer, `INSERT INTO cleanup_rows (id, value) VALUES ('a', 'old-a'), ('b', 'old-b')`)
	execOK(t, reader, `BEGIN SNAPSHOT`)
	if got := execOK(t, reader, `SELECT id FROM cleanup_rows ORDER BY id`); len(got.Rows) != 2 {
		t.Fatalf("initial snapshot rows = %d", len(got.Rows))
	}
	execOK(t, writer, `DELETE FROM cleanup_rows`)

	type outcome struct {
		n   int
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		n, err := db.CleanupDeadVersions(1)
		done <- outcome{n: n, err: err}
	}()
	select {
	case got := <-done:
		t.Fatalf("cleanup crossed live snapshot: %+v", got)
	case <-time.After(40 * time.Millisecond):
	}
	if got := execOK(t, reader, `SELECT id FROM cleanup_rows ORDER BY id`); len(got.Rows) != 2 {
		t.Fatalf("snapshot rows after delete = %d", len(got.Rows))
	}
	execOK(t, reader, `COMMIT`)
	select {
	case got := <-done:
		if got.err != nil || got.n != 1 {
			t.Fatalf("bounded cleanup = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not resume after snapshot")
	}
	if n, err := db.CleanupDeadVersions(8); err != nil || n != 1 {
		t.Fatalf("remaining cleanup = %d, %v", n, err)
	}
}

func TestCleanupDeadVersionsRemovesFulltextPostings(t *testing.T) {
	db := testDB(t)
	reader := db.Session()
	writer := db.Session()
	execOK(t, writer, `CREATE TABLE docs (id STRING PRIMARY KEY, body TEXT NOT NULL)`)
	execOK(t, writer, `CREATE FULLTEXT INDEX ix_body ON docs (body)`)
	execOK(t, writer, `INSERT INTO docs (id, body) VALUES ('a', 'cat database performance')`)
	execOK(t, reader, `BEGIN SNAPSHOT`)
	execOK(t, reader, `SELECT id FROM docs SEARCH body FOR 'cat'`)
	execOK(t, writer, `DELETE FROM docs WHERE id = 'a'`)
	ix, err := db.index("docs", "ix_body")
	if err != nil {
		t.Fatal(err)
	}
	if n := physicalRows(t, ix); n == 0 {
		t.Fatal("full-text tombstones were removed while a snapshot was live")
	}
	if got := execOK(t, reader, `SELECT id FROM docs SEARCH body FOR 'cat'`); len(got.Rows) != 1 {
		t.Fatalf("snapshot search rows = %d", len(got.Rows))
	}
	execOK(t, reader, `COMMIT`)
	if n, err := db.CleanupDeadVersions(32); err != nil || n < 2 {
		t.Fatalf("cleanup = %d, %v", n, err)
	}
	if n := physicalRows(t, ix); n != 0 {
		t.Fatalf("physical full-text records after cleanup = %d", n)
	}
	if got := execOK(t, writer, `SELECT id FROM docs SEARCH body FOR 'cat'`); len(got.Rows) != 0 {
		t.Fatalf("deleted document returned after cleanup: %d", len(got.Rows))
	}
}

func TestMaintainSQLScopesCleanup(t *testing.T) {
	db := testDB(t)
	reader := db.Session()
	writer := db.Session()
	for _, table := range []string{"a", "b"} {
		execOK(t, writer, `CREATE TABLE `+table+` (id STRING PRIMARY KEY)`)
		execOK(t, writer, `INSERT INTO `+table+` (id) VALUES ('dead')`)
	}
	execOK(t, reader, `BEGIN SNAPSHOT`)
	execOK(t, reader, `SELECT id FROM a`)
	execOK(t, writer, `DELETE FROM a`)
	execOK(t, writer, `DELETE FROM b`)
	execOK(t, reader, `COMMIT`)

	res := execOK(t, writer, `MAINTAIN TABLE a`)
	if res.Affected != 1 {
		t.Fatalf("table maintenance affected = %d", res.Affected)
	}
	a, _ := db.heap("a")
	b, _ := db.heap("b")
	if n := physicalRows(t, a); n != 0 {
		t.Fatalf("table a physical rows = %d", n)
	}
	if n := physicalRows(t, b); n != 1 {
		t.Fatalf("table b was cleaned by scoped maintenance: %d", n)
	}
	res = execOK(t, writer, `MAINTAIN DATABASE`)
	if res.Affected != 1 || physicalRows(t, b) != 0 {
		t.Fatalf("database maintenance affected=%d b=%d", res.Affected, physicalRows(t, b))
	}
	st := db.MaintenanceStatus()
	if st.Active != nil || st.Last == nil || st.Last.Scope != "database" || st.Last.Affected != 1 || st.Last.Failed {
		t.Fatalf("maintenance status %+v", st)
	}
	ms := db.metrics.Snapshot()
	if ms.MaintenanceRuns != 2 || ms.MaintenanceRows != 2 || ms.MaintenanceFailures != 0 {
		t.Fatalf("maintenance metrics %+v", ms)
	}
	db.PauseMaintenance()
	if _, err := writer.Exec(`MAINTAIN DATABASE`); err == nil {
		t.Fatal("paused maintenance accepted")
	}
	if !db.MaintenanceStatus().Paused {
		t.Fatal("paused state not observable")
	}
	db.ResumeMaintenance()
	execOK(t, writer, `MAINTAIN DATABASE`)
	if _, err := writer.Exec(`MAINTAIN TABLE missing`); err == nil {
		t.Fatal("unknown maintenance table accepted")
	}
	execOK(t, writer, `BEGIN`)
	if _, err := writer.Exec(`MAINTAIN DATABASE`); err == nil {
		t.Fatal("maintenance inside transaction accepted")
	}
	execOK(t, writer, `ROLLBACK`)
}

func TestMaintainIndexScopesCleanupAndRejectsAmbiguity(t *testing.T) {
	db := testDB(t)
	reader := db.Session()
	writer := db.Session()
	execOK(t, writer, `CREATE TABLE docs (id STRING PRIMARY KEY, tag STRING NOT NULL)`)
	execOK(t, writer, `CREATE INDEX ix_tag ON docs (tag)`)
	execOK(t, writer, `INSERT INTO docs (id, tag) VALUES ('a', 'old')`)
	execOK(t, reader, `BEGIN SNAPSHOT`)
	execOK(t, reader, `SELECT id FROM docs WHERE tag = 'old'`)
	execOK(t, writer, `DELETE FROM docs`)
	execOK(t, reader, `COMMIT`)
	heap, _ := db.heap("docs")
	ix, _ := db.index("docs", "ix_tag")
	if physicalRows(t, heap) != 1 || physicalRows(t, ix) != 1 {
		t.Fatal("expected deferred heap and index tombstones")
	}
	res := execOK(t, writer, `MAINTAIN INDEX ix_tag`)
	if res.Affected != 1 || physicalRows(t, ix) != 0 || physicalRows(t, heap) != 1 {
		t.Fatalf("index maintenance affected=%d index=%d heap=%d", res.Affected, physicalRows(t, ix), physicalRows(t, heap))
	}
	st := db.MaintenanceStatus()
	if st.Last == nil || st.Last.Scope != "docs.ix_tag" {
		t.Fatalf("index maintenance status %+v", st)
	}
	execOK(t, writer, `CREATE TABLE other (id STRING PRIMARY KEY, tag STRING NOT NULL)`)
	execOK(t, writer, `CREATE INDEX ix_tag ON other (tag)`)
	if _, err := writer.Exec(`MAINTAIN INDEX ix_tag`); err == nil {
		t.Fatal("ambiguous index maintenance accepted")
	}
}

func TestMaintainPartitionedTableAndIndexScopesEveryLocalTree(t *testing.T) {
	db := testDB(t)
	reader := db.Session()
	writer := db.Session()
	execOK(t, writer, `CREATE TABLE partition_maint (
		region STRING NOT NULL,
		id STRING NOT NULL,
		tag STRING NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, writer, `CREATE INDEX ix_partition_maint_tag ON partition_maint (tag)`)
	execOK(t, writer, `INSERT INTO partition_maint (region, id, tag) VALUES
		('us', '1', 'old-us'),
		('eu', '2', 'old-eu')`)

	execOK(t, reader, `BEGIN SNAPSHOT`)
	execOK(t, reader, `SELECT id FROM partition_maint ORDER BY id`)
	execOK(t, writer, `DELETE FROM partition_maint`)
	execOK(t, reader, `COMMIT`)

	tab, ok := db.Cat.Get("partition_maint")
	if !ok || tab.Partitioning == nil || len(tab.Partitioning.Partitions) != 2 {
		t.Fatalf("partitioned table missing: %+v", tab)
	}
	for _, part := range tab.Partitioning.Partitions {
		heap, err := db.partitionHeap(tab.Name, part.ID)
		if err != nil {
			t.Fatal(err)
		}
		idx, err := db.partitionIndex(tab.Name, part.ID, "ix_partition_maint_tag")
		if err != nil {
			t.Fatal(err)
		}
		if physicalRows(t, heap) != 1 || physicalRows(t, idx) != 1 {
			t.Fatalf("partition %s expected deferred heap/index tombstones", part.Name)
		}
	}

	res := execOK(t, writer, `MAINTAIN INDEX ix_partition_maint_tag`)
	if res.Affected != 2 {
		t.Fatalf("partitioned index maintenance affected=%d", res.Affected)
	}
	for _, part := range tab.Partitioning.Partitions {
		heap, _ := db.partitionHeap(tab.Name, part.ID)
		idx, _ := db.partitionIndex(tab.Name, part.ID, "ix_partition_maint_tag")
		if physicalRows(t, idx) != 0 || physicalRows(t, heap) != 1 {
			t.Fatalf("partition %s index scope crossed into heap", part.Name)
		}
	}

	res = execOK(t, writer, `MAINTAIN TABLE partition_maint`)
	if res.Affected != 2 {
		t.Fatalf("partitioned table maintenance affected=%d", res.Affected)
	}
	for _, part := range tab.Partitioning.Partitions {
		heap, _ := db.partitionHeap(tab.Name, part.ID)
		if physicalRows(t, heap) != 0 {
			t.Fatalf("partition %s heap tombstone survived table maintenance", part.Name)
		}
	}

	part := tab.Partitioning.Partitions[0]
	key := partitionIndexKey(tab.Name, part.ID, "ix_partition_maint_tag")
	db.mu.Lock()
	missing := db.partIdxs[key]
	delete(db.partIdxs, key)
	db.mu.Unlock()
	if _, err := writer.Exec(`MAINTAIN INDEX ix_partition_maint_tag`); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("missing partition-local root did not fail closed: %v", err)
	}
	db.mu.Lock()
	db.partIdxs[key] = missing
	db.mu.Unlock()
}

// TestMaintainIndexConcurrentWrites is an adversarial stress test for
// MAINTAIN INDEX/TABLE running repeatedly while ordinary DML keeps writing
// to the same table and index — the same shape of test
// (TestRebuildIndexOnlineConcurrentWrites, internal/executor/online_rebuild_test.go)
// that found a real data-integrity bug in REBUILD INDEX ... ONLINE
// (TODO.md log #93). MAINTAIN's own existing tests (above) each drive one
// specific, hand-arranged tombstone scenario; none run it concurrently
// against live writers the way this does.
func TestMaintainIndexConcurrentWrites(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(10,0) PRIMARY KEY, v DECIMAL(6,0) NOT NULL)`)
	const rows = 1200
	const distinct = 32
	for i := 1; i <= rows; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, v) VALUES (%d, %d)`, i, i%distinct))
	}
	execOK(t, s, `CREATE INDEX ix_v ON t (v)`)

	var stop atomic.Bool
	var writes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(4)
	for w := 0; w < 3; w++ {
		go func(seed int) {
			defer wg.Done()
			ws := db.Session()
			r := uint64(seed*2654435761 + 1)
			tolerant := func(err error) bool {
				return nerr.HasCode(err, nerr.Serialization) || nerr.HasCode(err, nerr.Deadlock) ||
					nerr.HasCode(err, nerr.AlreadyExists) || nerr.HasCode(err, nerr.NotFound) ||
					nerr.HasCode(err, nerr.Unavailable)
			}
			for !stop.Load() {
				r = r*6364136223846793005 + 1442695040888963407
				val := int(r>>17) % distinct
				var q string
				switch r >> 61 & 3 {
				case 0: // insert into a churn range above the seeded rows
					q = fmt.Sprintf(`INSERT INTO t (id, v) VALUES (%d, %d)`, rows+1+int(r>>33)%400, val)
				case 1: // delete from the churn range
					q = fmt.Sprintf(`DELETE FROM t WHERE id = %d`, rows+1+int(r>>33)%400)
				default: // update a stable row
					q = fmt.Sprintf(`UPDATE t SET v = %d WHERE id = %d`, val, int(r>>33)%rows+1)
				}
				if _, err := ws.Exec(q); err != nil {
					if tolerant(err) {
						continue
					}
					t.Errorf("concurrent write %q: %v", q, err)
					return
				}
				writes.Add(1)
			}
		}(w + 1)
	}
	// Dedicated maintainer goroutine: MAINTAIN's own concurrency limit
	// allows only one active pass per database, so this alone contends the
	// real path an operator's periodic maintenance job would take.
	go func() {
		defer wg.Done()
		ms := db.Session()
		for !stop.Load() {
			if _, err := ms.Exec(`MAINTAIN INDEX ix_v`); err != nil &&
				!nerr.HasCode(err, nerr.Unavailable) {
				t.Errorf("MAINTAIN INDEX ix_v: %v", err)
				return
			}
			if _, err := ms.Exec(`MAINTAIN TABLE t`); err != nil &&
				!nerr.HasCode(err, nerr.Unavailable) {
				t.Errorf("MAINTAIN TABLE t: %v", err)
				return
			}
		}
	}()

	for writes.Load() < 400 {
		time.Sleep(time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()

	if err := db.LastReclaimError(); err != nil {
		t.Fatalf("reclaim error: %v", err)
	}
	assertIndexMatchesHeap(t, s, distinct)
}
