package btree

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

func testKeys(t testing.TB) *crypto.MemoryKeyProvider {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func testEngine(t testing.TB, pages int) *storage.Engine {
	t.Helper()
	e, err := storage.Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), pages)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func testTree(t testing.TB, pages int) (*Tree, *storage.Engine) {
	t.Helper()
	e := testEngine(t, pages)
	tr, err := Create(e)
	if err != nil {
		t.Fatal(err)
	}
	return tr, e
}

func keyOf(i int) []byte { return []byte(fmt.Sprintf("k%06d", i)) }
func valOf(i int) []byte { return []byte(fmt.Sprintf("v%06d", i)) }
func fatVal(i int) []byte {
	return []byte(fmt.Sprintf("v%06d-%s", i, bytes.Repeat([]byte("x"), 120)))
}

func TestLeafFitsMatchesRebuild(t *testing.T) {
	for n := 0; n <= 80; n += 5 {
		ents := make([]leafEntry, n)
		for i := 0; i < n; i++ {
			ents[i] = leafEntry{key: keyOf(i), value: fatVal(i)}
		}
		p, err := rebuildLeaf(1, nodeHeader{}, ents)
		got := leafFits(ents)
		want := err == nil && p != nil
		if got != want {
			t.Fatalf("n=%d leafFits=%v rebuild ok=%v err=%v", n, got, want, err)
		}
	}
	tooBig := []leafEntry{{key: []byte("k"), value: bytes.Repeat([]byte("x"), maxLeafRecord)}}
	if leafFits(tooBig) {
		t.Fatal("oversize record should not fit")
	}
}

func TestCreateOpenRejectsDuplicate(t *testing.T) {
	e := testEngine(t, 16)
	if _, err := Create(e); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(e); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("expected already exists, got %v", err)
	}
}

type failingReplicator struct{ err error }

func (f failingReplicator) Replicate(recs []wal.Record) error { return f.err }

// TestInsertOrphansLocalCommitOnReplicateFailure documents and exercises
// the residual, deliberately-unclosable case of TODO.md's Phase 27 exit
// gate item "Local commit precedes replication acknowledgment": a
// Replicate failure that is ambiguous/in-doubt — raft.Apply was actually
// called, but the quorum wait itself failed (e.g. lost leadership,
// enqueue timeout, mid-flight shutdown) — cannot safely be told apart from
// "it actually reached quorum after all", so storage.Engine keeps this
// case's local commit visible rather than guessing wrong and silently
// diverging from the cluster (see storage.NotProposedError's doc comment,
// and Engine.commitAndReplicate, for the definite case this is NOT: a
// rejection before raft.Apply is even attempted, e.g. this node was never
// the leader — see TestInsertDiscardsLocalCommitOnDefiniteReplicateFailure
// below, which is the case this fix does close). The caller correctly
// sees the failure and can safely retry — no acknowledged write is lost —
// and metrics.AddReplicationOrphan gives operators an observable signal
// that it happened, which this test is the coverage for.
func TestInsertOrphansLocalCommitOnReplicateFailure(t *testing.T) {
	tr, e := testTree(t, 16)

	before := metrics.Default().Snapshot().ReplicationOrphans

	failure := nerr.New(nerr.Unavailable, "test", "quorum commit failed")
	e.SetReplicator(failingReplicator{err: failure})

	insertErr := tr.Insert([]byte("orphan"), []byte("value"))
	if !nerr.HasCode(insertErr, nerr.Unavailable) {
		t.Fatalf("Insert = %v, want the Replicate failure surfaced", insertErr)
	}

	after := metrics.Default().Snapshot().ReplicationOrphans
	if after != before+1 {
		t.Fatalf("ReplicationOrphans = %d, want %d", after, before+1)
	}

	// The ambiguous case stays open by design: despite Insert reporting
	// failure, the key is still durably visible locally.
	e.SetReplicator(nil)
	got, err := tr.Lookup([]byte("orphan"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "value" {
		t.Fatalf("got %q", got)
	}
}

// notProposedFailure marks a test Replicator failure as definite — the
// entry never reached Raft at all — by implementing the same NotProposed()
// bool capability *replication.Cluster's real not-leader rejection does
// (storage.NotProposedError, checked structurally by commitAndReplicate).
type notProposedFailure struct{ error }

func (notProposedFailure) NotProposed() bool { return true }
func (f notProposedFailure) Unwrap() error   { return f.error }

// TestInsertDiscardsLocalCommitOnDefiniteReplicateFailure proves the
// structural fix: a Replicate failure that is definite (never reached
// Raft — e.g. this node was rejected before raft.Apply was ever called)
// leaves no local orphan at all, unlike the ambiguous case exercised by
// TestInsertOrphansLocalCommitOnReplicateFailure above. The key must not
// be visible afterward, and — since nothing was ever left in a state that
// needs an operator to reconcile — no orphan should be reported either.
func TestInsertDiscardsLocalCommitOnDefiniteReplicateFailure(t *testing.T) {
	tr, e := testTree(t, 16)

	before := metrics.Default().Snapshot().ReplicationOrphans

	failure := notProposedFailure{nerr.New(nerr.Unavailable, "test", "not the leader")}
	rep := &failingReplicatorReporting{err: failure}
	e.SetReplicator(rep)

	insertErr := tr.Insert([]byte("not-proposed"), []byte("value"))
	if !nerr.HasCode(insertErr, nerr.Unavailable) {
		t.Fatalf("Insert = %v, want the Replicate failure surfaced", insertErr)
	}

	after := metrics.Default().Snapshot().ReplicationOrphans
	if after != before {
		t.Fatalf("ReplicationOrphans = %d, want unchanged at %d (definite failures are not orphans)", after, before)
	}
	if rep.reported.Load() {
		t.Fatal("ReportReplicationOrphan called on a definite (never-proposed) failure")
	}

	e.SetReplicator(nil)
	if _, err := tr.Lookup([]byte("not-proposed")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("Lookup after a discarded commit = %v, want NotFound", err)
	}
}

// blockingReplicator holds Replicate open until release is closed, so a
// test can observe engine behavior while one commit's replication is
// genuinely in flight.
type blockingReplicator struct{ release chan struct{} }

func (b *blockingReplicator) Replicate(recs []wal.Record) error {
	<-b.release
	return nil
}

// TestUnrelatedReadProceedsWhileACommitHoldsReplicate proves
// Engine.commitAndReplicate does not hold e.mu across the Replicate
// round-trip: this is the deadlock hazard the structural fix's design
// explicitly had to avoid (Raft's own FSM-apply goroutine can need e.mu to
// process an unrelated, earlier-queued entry the pending commit's own
// raft.Apply() future may transitively depend on to resolve). An unrelated
// read, which briefly needs e.mu to capture its MVCC snapshot, must
// complete promptly rather than blocking on an in-flight commit whose
// Replicate call hasn't returned yet.
func TestUnrelatedReadProceedsWhileACommitHoldsReplicate(t *testing.T) {
	tr, e := testTree(t, 16)
	if err := tr.Insert([]byte("pre"), []byte("value")); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	e.SetReplicator(&blockingReplicator{release: release})

	heldDone := make(chan error, 1)
	go func() {
		heldDone <- tr.Insert([]byte("held"), []byte("value"))
	}()

	select {
	case err := <-heldDone:
		t.Fatalf("held insert returned before Replicate was released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	lookupDone := make(chan error, 1)
	go func() {
		_, err := tr.Lookup([]byte("pre"))
		lookupDone <- err
	}()
	select {
	case err := <-lookupDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unrelated read never completed — e.mu appears held across the Replicate round-trip")
	}

	close(release)
	select {
	case err := <-heldDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("held insert never completed after Replicate was released")
	}

	if _, err := tr.Lookup([]byte("held")); err != nil {
		t.Fatal(err)
	}
}

// failingReplicatorReporting is failingReplicator plus the optional
// storage.ReplicationOrphanReporter capability, so tests can observe
// whether commitAndReplicate notified it.
type failingReplicatorReporting struct {
	err      error
	reported atomic.Bool
}

func (f *failingReplicatorReporting) Replicate(recs []wal.Record) error { return f.err }
func (f *failingReplicatorReporting) ReportReplicationOrphan()          { f.reported.Store(true) }

// TestInsertReportsReplicationOrphanToReporter proves the mitigation landed
// alongside the known gap above (TODO.md Phase 27 exit gate, "Local commit
// precedes replication acknowledgment"): a Replicator that implements
// storage.ReplicationOrphanReporter is notified on exactly the same
// Replicate failure that increments metrics.AddReplicationOrphan, so it can
// protect STRONG reads (see replication.Cluster.ReportReplicationOrphan)
// until an operator reconciles the node.
func TestInsertReportsReplicationOrphanToReporter(t *testing.T) {
	tr, e := testTree(t, 16)
	failure := nerr.New(nerr.Unavailable, "test", "quorum commit failed")
	rep := &failingReplicatorReporting{err: failure}
	e.SetReplicator(rep)

	if err := tr.Insert([]byte("orphan2"), []byte("value")); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("Insert = %v, want the Replicate failure surfaced", err)
	}
	if !rep.reported.Load() {
		t.Fatal("ReportReplicationOrphan was not called on a Replicator that implements it")
	}
}

// TestInsertDoesNotReportOrphanOnSuccessfulReplicate proves the reporter is
// only ever notified on an actual Replicate failure, never as a side
// effect of a normal successful commit.
func TestInsertDoesNotReportOrphanOnSuccessfulReplicate(t *testing.T) {
	tr, e := testTree(t, 16)
	rep := &failingReplicatorReporting{err: nil}
	e.SetReplicator(rep)

	if err := tr.Insert([]byte("ok"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if rep.reported.Load() {
		t.Fatal("ReportReplicationOrphan called despite a successful Replicate")
	}
}

func TestInsertLookupDelete(t *testing.T) {
	tr, _ := testTree(t, 16)
	if err := tr.Insert([]byte("alpha"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("beta"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("alpha"), []byte("dup")); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("duplicate: %v", err)
	}
	got, err := tr.Lookup([]byte("beta"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "two" {
		t.Fatalf("lookup %q", got)
	}
	if _, err := tr.Lookup([]byte("missing")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := tr.Delete([]byte("alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Lookup([]byte("alpha")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatal("deleted key still present")
	}
	if err := tr.Delete([]byte("alpha")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("double delete: %v", err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestTxnLogsOnePageImagePerDirtyPage(t *testing.T) {
	tr, e := testTree(t, 64)
	tx, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	const n = 200
	for i := 0; i < n; i++ {
		if err := tx.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	recs, _, err := e.WAL.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	var images, inserts int
	for _, r := range recs {
		switch r.Type {
		case wal.RecPageImage:
			images++
		case wal.RecInsert:
			inserts++
		}
	}
	if inserts != n {
		t.Fatalf("logical inserts %d want %d", inserts, n)
	}
	if images < 1 {
		t.Fatal("expected at least one redo page image")
	}
	if images >= n {
		t.Fatalf("page images %d should be far below inserts %d", images, n)
	}
	for i := 0; i < n; i++ {
		got, err := tr.Lookup(keyOf(i))
		if err != nil {
			t.Fatalf("lookup %d: %v", i, err)
		}
		if !bytes.Equal(got, fatVal(i)) {
			t.Fatalf("lookup %d: %q", i, got)
		}
	}
}

func TestInsertBatchSequential(t *testing.T) {
	tr, _ := testTree(t, 128)
	tx, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	const n = 4000
	keys := make([][]byte, n)
	vals := make([][]byte, n)
	for i := 0; i < n; i++ {
		keys[i] = keyOf(i)
		vals[i] = valOf(i)
	}
	if err := tx.InsertBatch(keys, vals); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i += 97 {
		got, err := tr.Lookup(keys[i])
		if err != nil {
			t.Fatalf("lookup %d: %v", i, err)
		}
		if !bytes.Equal(got, vals[i]) {
			t.Fatalf("lookup %d: %q", i, got)
		}
	}
	var count int
	if err := tr.Range(nil, nil, func(_, _ []byte) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Fatalf("range %d want %d", count, n)
	}
}

func TestUpdateReplacesValue(t *testing.T) {
	tr, _ := testTree(t, 16)
	if err := tr.Insert([]byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Update([]byte("k"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Lookup([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Fatalf("got %q", got)
	}
	if err := tr.Update([]byte("missing"), []byte("x")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("missing update: %v", err)
	}
}

func TestEmptyKeyRejected(t *testing.T) {
	tr, _ := testTree(t, 8)
	if err := tr.Insert(nil, []byte("x")); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("nil key: %v", err)
	}
	if err := tr.Insert([]byte{}, []byte("x")); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("empty key: %v", err)
	}
	if err := tr.Insert(bytes.Repeat([]byte("k"), MaxKeySize+1), []byte("x")); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("oversize key: %v", err)
	}
	if err := tr.Insert([]byte("ok"), bytes.Repeat([]byte("v"), maxLeafRecord)); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("oversize value: %v", err)
	}
}

func TestRangeScan(t *testing.T) {
	tr, _ := testTree(t, 32)
	for i := 0; i < 50; i++ {
		if err := tr.Insert(keyOf(i), valOf(i)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	if err := tr.Range(keyOf(10), keyOf(20), func(k, v []byte) error {
		got = append(got, string(k)+"="+string(v))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("range len %d %v", len(got), got)
	}
	if got[0] != "k000010=v000010" || got[9] != "k000019=v000019" {
		t.Fatalf("range %v", got)
	}
	n := 0
	if err := tr.Range(nil, nil, func(k, v []byte) error {
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 50 {
		t.Fatalf("full scan %d", n)
	}
	n = 0
	if err := tr.Range(keyOf(40), keyOf(40), func(k, v []byte) error {
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("empty half-open range should yield nothing")
	}
}

func TestSplitAndInvariants(t *testing.T) {
	tr, _ := testTree(t, 64)
	const n = 400
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if tr.Height() < 2 {
		t.Fatalf("expected a split, height %d", tr.Height())
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		got, err := tr.Lookup(keyOf(i))
		if err != nil {
			t.Fatalf("lookup %d: %v", i, err)
		}
		if !bytes.Equal(got, fatVal(i)) {
			t.Fatalf("lookup %d got %q", i, got)
		}
	}
}

func TestSplitKeysPartitions(t *testing.T) {
	tr, _ := testTree(t, 64)
	const n = 400
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if tr.Height() < 2 {
		t.Fatalf("need interior separators, height %d", tr.Height())
	}
	splits, err := tr.SplitKeys(4)
	if err != nil {
		t.Fatal(err)
	}
	if len(splits) == 0 {
		t.Fatal("expected split keys on a multi-leaf tree")
	}
	ranges := [][2][]byte{{nil, splits[0]}}
	for i := 0; i < len(splits)-1; i++ {
		ranges = append(ranges, [2][]byte{splits[i], splits[i+1]})
	}
	ranges = append(ranges, [2][]byte{splits[len(splits)-1], nil})
	seen := make(map[string]int, n)
	var total int
	for _, r := range ranges {
		if err := tr.Range(r[0], r[1], func(k, _ []byte) error {
			seen[string(k)]++
			total++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if total != n {
		t.Fatalf("partitioned scan %d want %d (splits=%d)", total, n, len(splits))
	}
	for i := 0; i < n; i++ {
		if seen[string(keyOf(i))] != 1 {
			t.Fatalf("key %d counted %d", i, seen[string(keyOf(i))])
		}
	}
}

func TestRangeCount(t *testing.T) {
	tr, _ := testTree(t, 64)
	const n = 200
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := tr.Count(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != n {
		t.Fatalf("full count %d want %d", got, n)
	}
	got, err = tr.Count(keyOf(10), keyOf(20))
	if err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Fatalf("range count %d", got)
	}
	if err := tr.Delete(keyOf(15)); err != nil {
		t.Fatal(err)
	}
	got, err = tr.Count(keyOf(10), keyOf(20))
	if err != nil {
		t.Fatal(err)
	}
	if got != 9 {
		t.Fatalf("count after delete %d", got)
	}
	got, err = tr.Count(nil, nil)
	if err != nil || got != n-1 {
		t.Fatalf("full count after delete %d %v", got, err)
	}
}

func TestReverseInsertSplit(t *testing.T) {
	tr, _ := testTree(t, 64)
	const n = 300
	for i := n - 1; i >= 0; i-- {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	var prev []byte
	count := 0
	if err := tr.Range(nil, nil, func(k, v []byte) error {
		if prev != nil && bytes.Compare(prev, k) >= 0 {
			return fmt.Errorf("out of order %q >= %q", prev, k)
		}
		prev = append(prev[:0], k...)
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Fatalf("scan count %d", count)
	}
}

func hugeVal(i int) []byte {
	return []byte(fmt.Sprintf("v%06d-%s", i, bytes.Repeat([]byte("x"), 400)))
}

func TestDeleteLeftHalfKeepsBalance(t *testing.T) {
	tr, _ := testTree(t, 512)
	const n = 40_000
	// Batch inserts/deletes to avoid 60k fdatasyncs; each batch still
	// fsyncs WAL so durability is preserved (group-commit style).
	const batch = 4096
	tx, err := tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if i > 0 && i%batch == 0 {
			if err := tx.Commit(); err != nil {
				t.Fatalf("insert commit %d: %v", i, err)
			}
			tx, err = tr.Begin()
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Insert(keyOf(i), hugeVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("insert final commit: %v", err)
	}
	if tr.Height() < 3 {
		t.Fatalf("need height >= 3 to hit non-root collapse, got %d", tr.Height())
	}
	tx, err = tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n/2; i++ {
		if i > 0 && i%batch == 0 {
			if err := tx.Commit(); err != nil {
				t.Fatalf("delete commit %d: %v", i, err)
			}
			tx, err = tr.Begin()
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Delete(keyOf(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("delete final commit: %v", err)
	}
	if err := tr.Check(); err != nil {
		t.Fatalf("check after left-half delete: %v", err)
	}
	var got int
	if err := tr.Range(nil, nil, func(_, _ []byte) error {
		got++
		return nil
	}); err != nil {
		t.Fatalf("range: %v", err)
	}
	if got != n/2 {
		t.Fatalf("range %d want %d", got, n/2)
	}
	if _, err := tr.Lookup(keyOf(n - 1)); err != nil {
		t.Fatalf("lookup last: %v", err)
	}
}

func TestSequentialDeleteHeight3Empty(t *testing.T) {
	tr, _ := testTree(t, 512)
	const n = 40_000
	const batch = 4096
	tx, err := tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if i > 0 && i%batch == 0 {
			if err := tx.Commit(); err != nil {
				t.Fatalf("insert commit %d: %v", i, err)
			}
			tx, err = tr.Begin()
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Insert(keyOf(i), hugeVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("insert final commit: %v", err)
	}
	if tr.Height() < 3 {
		t.Fatalf("need height >= 3 to empty the last internal under root, got %d", tr.Height())
	}
	tx, err = tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if i > 0 && i%batch == 0 {
			if err := tx.Commit(); err != nil {
				t.Fatalf("delete commit %d: %v", i, err)
			}
			tx, err = tr.Begin()
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Delete(keyOf(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("delete final commit: %v", err)
	}
	if err := tr.Check(); err != nil {
		t.Fatalf("check after emptying height-3 tree: %v", err)
	}
	if tr.Height() != 1 {
		t.Fatalf("expected collapsed root, height %d", tr.Height())
	}
	nscan := 0
	if err := tr.Range(nil, nil, func(_, _ []byte) error {
		nscan++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if nscan != 0 {
		t.Fatalf("empty tree scan %d", nscan)
	}
	if err := tr.Insert([]byte("again"), []byte("yes")); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Lookup([]byte("again"))
	if err != nil || string(got) != "yes" {
		t.Fatalf("reinsert %q %v", got, err)
	}
}

func TestReuseAfterEmptyHeight3(t *testing.T) {
	tr, e := testTree(t, 512)
	const n = 40_000
	const batch = 4096
	tx, err := tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if i > 0 && i%batch == 0 {
			if err := tx.Commit(); err != nil {
				t.Fatalf("insert commit %d: %v", i, err)
			}
			tx, err = tr.Begin()
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Insert(keyOf(i), hugeVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("insert final commit: %v", err)
	}
	tx, err = tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if i > 0 && i%batch == 0 {
			if err := tx.Commit(); err != nil {
				t.Fatalf("delete commit %d: %v", i, err)
			}
			tx, err = tr.Begin()
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Delete(keyOf(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("delete final commit: %v", err)
	}
	if err := e.BeginWrite(); err != nil {
		t.Fatal(err)
	}
	other, err := CreateDetached(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 256; i++ {
		val := bytes.Repeat([]byte("n"), 32+i*4)
		if err := other.Insert(keyOf(i), val); err != nil {
			t.Fatalf("reuse insert %d: %v", i, err)
		}
		if i%7 == 0 {
			val = bytes.Repeat([]byte("N"), 64+i*8)
			if err := other.Update(keyOf(i), val); err != nil {
				t.Fatalf("reuse update %d: %v", i, err)
			}
		}
	}
	if err := tr.Check(); err != nil {
		t.Fatalf("emptied primary: %v", err)
	}
	got, err := other.Lookup(keyOf(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 32 {
		t.Fatalf("reuse lookup %d bytes", len(got))
	}
	nscan := 0
	if err := other.Range(nil, nil, func(_, _ []byte) error {
		nscan++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if nscan != 256 {
		t.Fatalf("reuse scan %d", nscan)
	}
}

func TestReuseAfterSQLStylePurge(t *testing.T) {
	e := testEngine(t, 2048)
	if err := e.BeginWrite(); err != nil {
		t.Fatal(err)
	}
	heap, err := CreateDetached(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Commit(); err != nil {
		t.Fatal(err)
	}
	const n = 100_000
	tx, err := heap.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if i > 0 && i%8192 == 0 {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			tx, err = heap.Begin()
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Insert(keyOf(i), valOf(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = heap.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if i > 0 && i%8192 == 0 {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			tx, err = heap.Begin()
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Delete(keyOf(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := e.BeginWrite(); err != nil {
		t.Fatal(err)
	}
	other, err := CreateDetached(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 256; i++ {
		val := bytes.Repeat([]byte("n"), 32+i*4)
		if err := other.Insert(keyOf(i), val); err != nil {
			t.Fatalf("reuse insert %d: %v", i, err)
		}
		val = bytes.Repeat([]byte("N"), 64+i*8)
		if err := other.Update(keyOf(i), val); err != nil {
			t.Fatalf("reuse update %d: %v", i, err)
		}
		if err := other.Range(nil, nil, func(_, _ []byte) error { return nil }); err != nil {
			t.Fatalf("reuse range after %d: %v", i, err)
		}
	}
}

func TestRandomizedDeleteMerges(t *testing.T) {
	tr, _ := testTree(t, 256)
	const n = 8_000
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if tr.Height() < 2 {
		t.Fatalf("need splits, height %d", tr.Height())
	}
	rng := rand.New(rand.NewSource(0xDE1E))
	alive := make([]int, n)
	for i := range alive {
		alive[i] = i
	}
	for len(alive) > n/4 {
		j := rng.Intn(len(alive))
		k := alive[j]
		alive[j] = alive[len(alive)-1]
		alive = alive[:len(alive)-1]
		if err := tr.Delete(keyOf(k)); err != nil {
			t.Fatalf("delete %d: %v", k, err)
		}
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Lookup(keyOf(alive[0])); err != nil {
		t.Fatal(err)
	}
	nscan := 0
	if err := tr.Range(nil, nil, func(_, _ []byte) error {
		nscan++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if nscan != len(alive) {
		t.Fatalf("scan %d want %d", nscan, len(alive))
	}
}

func TestRandomizedLargeInvariants(t *testing.T) {
	ops := 6_000
	pages := 512
	if v := os.Getenv("NEXTSQL_BTREE_OPS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("NEXTSQL_BTREE_OPS=%q", v)
		}
		ops = n
		pages = 4096
		if ops >= 1_000_000 {
			// A larger resident pool keeps most of a multi-GiB tree cached, so
			// random-key operations stop paying a buffer miss (a page read plus
			// a dirty-eviction write) each. That miss traffic, not CPU, is the
			// dominant cost of the 100M soak. NEXTSQL_BTREE_POOL_PAGES overrides
			// this for hosts with more (or less) RAM to spare.
			pages = 12_288
		}
		if ops >= 10_000_000 {
			pages = 24_576
		}
	} else if testing.Short() {
		t.Skip("short")
	}
	if v := os.Getenv("NEXTSQL_BTREE_POOL_PAGES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 64 {
			t.Fatalf("NEXTSQL_BTREE_POOL_PAGES=%q", v)
		}
		pages = n
	}
	tr, e := testTree(t, pages)
	rng := rand.New(rand.NewSource(0x10000000))
	space := ops / 2
	if v := os.Getenv("NEXTSQL_BTREE_SPACE"); v != "" {
		// Capping the key space bounds the resident tree so it fits in the pool
		// on a RAM-constrained host. The soak still performs `ops` insert/
		// delete/merge operations; it just churns them over fewer keys.
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("NEXTSQL_BTREE_SPACE=%q", v)
		}
		space = n
	}
	if space < 1 {
		space = 1
	}
	present := make([]bool, space)
	value := make([]int32, space) // op index fits int32; half the RAM of []int
	live := 0
	batchSize := 1
	if ops >= 1_000_000 {
		// This is an invariant soak, not a per-operation fsync benchmark. Keep
		// transactions bounded while avoiding tens of millions of durability
		// barriers in the official large run.
		batchSize = 4096
	}
	// flushEvery is a cheap checkpoint + checkpoint-obsolete WAL discard. It
	// bounds WAL disk footprint only (redo is full 16 KiB page images, so
	// segments since the last checkpoint grow with the dirty-page count); the
	// pool is self-bounding via lazy eviction once WAL is durable, so this does
	// not govern memory. Zero on small runs, where the checkEvery checkpoint
	// suffices. Large enough that checkpoint overhead stays negligible.
	flushEvery := 0
	if ops >= 1_000_000 {
		flushEvery = 1_000_000
	}
	// checkEvery is the expensive, rare full structural invariant walk plus a
	// scan count. Its map/slice transients are freed to the OS afterwards.
	checkEvery := 1000
	if ops > 10_000 {
		checkEvery = ops / 10
		if checkEvery < 500_000 {
			checkEvery = 500_000
		}
	}
	flush := func(op int) {
		t.Helper()
		if err := e.Checkpoint(); err != nil {
			t.Fatalf("op %d checkpoint: %v", op, err)
		}
		if err := e.WAL.DiscardCheckpointedSegments(); err != nil {
			t.Fatalf("op %d discard checkpointed WAL: %v", op, err)
		}
	}
	var write *WriteTxn
	commit := func(op int) {
		t.Helper()
		if write == nil {
			return
		}
		if err := write.Commit(); err != nil {
			t.Fatalf("op %d commit: %v", op, err)
		}
		write = nil
	}
	for i := 0; i < ops; i++ {
		if write == nil {
			var err error
			write, err = tr.Begin()
			if err != nil {
				t.Fatalf("op %d begin: %v", i, err)
			}
		}
		k := rng.Intn(space)
		switch rng.Intn(5) {
		case 0, 1, 2:
			err := write.Insert(keyOf(k), valOf(i))
			if present[k] {
				if !nerr.HasCode(err, nerr.AlreadyExists) {
					t.Fatalf("op %d dup: %v", i, err)
				}
				break
			}
			if err != nil {
				t.Fatalf("op %d insert: %v", i, err)
			}
			present[k] = true
			value[k] = int32(i)
			live++
		default:
			err := write.Delete(keyOf(k))
			if !present[k] {
				if !nerr.HasCode(err, nerr.NotFound) {
					t.Fatalf("op %d missing: %v", i, err)
				}
				break
			}
			if err != nil {
				t.Fatalf("op %d delete: %v", i, err)
			}
			present[k] = false
			live--
		}
		if (i+1)%batchSize == 0 {
			commit(i)
		}
		if flushEvery > 0 && (i+1)%flushEvery == 0 {
			commit(i)
			flush(i)
		}
		if (i+1)%checkEvery == 0 {
			commit(i)
			if err := tr.Check(); err != nil {
				t.Fatalf("op %d check: %v", i, err)
			}
			// Checkpoint after a clean structural check so durable page flush
			// and WAL recycling run against a verified tree.
			flush(i)
			t.Logf("op %d live=%d", i+1, live)
			// Return the Check() walk's transient maps/slices to the OS so a
			// RAM-constrained soak host does not drift toward the OOM killer.
			runtime.GC()
			debug.FreeOSMemory()
		}
	}
	commit(ops - 1)
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	nscan := 0
	if err := tr.Range(nil, nil, func(_, _ []byte) error {
		nscan++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if nscan != live {
		t.Fatalf("scan %d want %d", nscan, live)
	}
	for k, ok := range present {
		got, err := tr.Lookup(keyOf(k))
		if !ok {
			if err == nil || !nerr.HasCode(err, nerr.NotFound) {
				t.Fatalf("lookup missing %d: %v", k, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("lookup %d: %v", k, err)
		}
		if !bytes.Equal(got, valOf(int(value[k]))) {
			t.Fatalf("lookup %d: got %q want %q", k, got, valOf(int(value[k])))
		}
	}
}

func TestSequentialDeleteManyLeaves(t *testing.T) {
	tr, _ := testTree(t, 256)
	const n = 2000
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if tr.Height() < 2 {
		t.Fatalf("need splits, height %d", tr.Height())
	}
	for i := 0; i < n; i++ {
		if err := tr.Delete(keyOf(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
		if i%200 == 199 {
			if err := tr.Check(); err != nil {
				t.Fatalf("check after %d: %v", i+1, err)
			}
			var got int
			if err := tr.Range(nil, nil, func(_, _ []byte) error {
				got++
				return nil
			}); err != nil {
				t.Fatalf("range after %d: %v", i+1, err)
			}
			if got != n-i-1 {
				t.Fatalf("range count %d want %d after delete %d", got, n-i-1, i)
			}
		}
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteMergeAndRootCollapse(t *testing.T) {
	tr, _ := testTree(t, 64)
	const n = 250
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatal(err)
		}
	}
	if tr.Height() < 2 {
		t.Fatalf("need height >= 2 to test collapse, got %d", tr.Height())
	}
	for i := 0; i < n; i++ {
		if err := tr.Delete(keyOf(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
		if i%25 == 0 {
			if err := tr.Check(); err != nil {
				t.Fatalf("after delete %d: %v", i, err)
			}
		}
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	if tr.Height() != 1 {
		t.Fatalf("expected collapsed root, height %d", tr.Height())
	}
	nscan := 0
	if err := tr.Range(nil, nil, func(k, v []byte) error {
		nscan++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if nscan != 0 {
		t.Fatalf("empty tree scan %d", nscan)
	}
	if err := tr.Insert([]byte("again"), []byte("yes")); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Lookup([]byte("again"))
	if err != nil || string(got) != "yes" {
		t.Fatalf("reinsert %q %v", got, err)
	}
}

func TestInterleavedDeleteKeepsInvariants(t *testing.T) {
	tr, _ := testTree(t, 64)
	const n = 200
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i += 2 {
		if err := tr.Delete(keyOf(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		got, err := tr.Lookup(keyOf(i))
		if i%2 == 0 {
			if !nerr.HasCode(err, nerr.NotFound) {
				t.Fatalf("even key %d should be gone: %v", i, err)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, fatVal(i)) {
			t.Fatalf("odd key %d: %q %v", i, got, err)
		}
	}
}

func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := Create(e)
	if err != nil {
		t.Fatal(err)
	}
	const n = 180
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Delete(keyOf(3)); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e, err = storage.Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	tr, err = Open(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Lookup(keyOf(3)); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatal("deleted key survived restart")
	}
	got, err := tr.Lookup(keyOf(17))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fatVal(17)) {
		t.Fatalf("restart lookup %q", got)
	}
	if err := tr.Delete(keyOf(17)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tr.Range(nil, nil, func(k, v []byte) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != n-2 {
		t.Fatalf("scan after restart delete: %d", count)
	}
}

func TestDetachedOwnedPagesAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := e.StartTxn()
	if err != nil {
		t.Fatal(err)
	}
	e.Enter(owner)
	tree, err := CreateDetached(e)
	e.Leave(owner)
	if err != nil {
		t.Fatal(err)
	}
	tx := tree.Attach(owner, nil)
	for i := 0; i < 4000; i++ {
		key := []byte(fmt.Sprintf("k%06d", i))
		if err := tx.Insert(key, []byte("value")); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.PersistMeta(); err != nil {
		t.Fatal(err)
	}
	if err := e.CommitTxn(owner); err != nil {
		t.Fatal(err)
	}
	tx.MarkDone()
	before, err := tree.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 3 || !slices.Contains(before, tree.Meta()) || !slices.Contains(before, tree.Root()) {
		t.Fatalf("owned pages: %v", before)
	}
	for i := 1; i < len(before); i++ {
		if before[i-1] >= before[i] {
			t.Fatalf("pages not unique/sorted: %v", before)
		}
	}
	meta := tree.Meta()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err = storage.Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	tree, err = OpenDetached(e, meta)
	if err != nil {
		t.Fatal(err)
	}
	after, err := tree.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, after) {
		t.Fatalf("before=%v after=%v", before, after)
	}
}

func TestOpenWithoutTree(t *testing.T) {
	e := testEngine(t, 8)
	if _, err := Open(e); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestConcurrentReaders(t *testing.T) {
	tr, _ := testTree(t, 32)
	const n = 80
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), valOf(i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			for i := 0; i < n; i++ {
				got, err := tr.Lookup(keyOf((i + off) % n))
				if err != nil {
					errCh <- err
					return
				}
				if len(got) == 0 {
					errCh <- fmt.Errorf("empty value")
					return
				}
			}
			count := 0
			if err := tr.Range(nil, nil, func(k, v []byte) error {
				count++
				return nil
			}); err != nil {
				errCh <- err
				return
			}
			if count != n {
				errCh <- fmt.Errorf("scan %d", count)
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestNodeRecordRoundTrip(t *testing.T) {
	rec, err := encodeLeaf([]byte("key"), []byte("row-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	k, v, err := decodeLeaf(rec)
	if err != nil {
		t.Fatal(err)
	}
	if string(k) != "key" || string(v) != "row-bytes" {
		t.Fatalf("%q %q", k, v)
	}
	irec, err := encodeInternal([]byte("sep"), 9)
	if err != nil {
		t.Fatal(err)
	}
	ik, child, err := decodeInternal(irec)
	if err != nil {
		t.Fatal(err)
	}
	if string(ik) != "sep" || child != 9 {
		t.Fatalf("%q %d", ik, child)
	}
	if _, err := encodeInternal([]byte("sep"), format.PageIDSuperblock); err == nil {
		t.Fatal("child 0 must be rejected")
	}
	if _, _, err := decodeLeaf([]byte{1}); err == nil {
		t.Fatal("truncated leaf")
	}
	if _, _, err := decodeInternal([]byte{1, 0}); err == nil {
		t.Fatal("truncated internal")
	}
}

func TestPageTypesKnown(t *testing.T) {
	if !format.PageTypeBTreeLeaf.Known() || !format.PageTypeBTreeInternal.Known() {
		t.Fatal("btree page types must be known")
	}
	if format.PageType(99).Known() {
		t.Fatal("unknown type")
	}
}
