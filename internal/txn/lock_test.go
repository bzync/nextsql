package txn

import (
	"sync"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func TestLockSharedCompatible(t *testing.T) {
	lm := NewLockManager()
	if err := lm.Acquire(1, []byte("a"), Shared, ""); err != nil {
		t.Fatal(err)
	}
	if err := lm.Acquire(2, []byte("a"), Shared, ""); err != nil {
		t.Fatal(err)
	}
	lm.ReleaseAll(1)
	lm.ReleaseAll(2)
}

func TestDeadlockAbortsOne(t *testing.T) {
	lm := NewLockManager()
	if err := lm.Acquire(1, []byte("a"), Exclusive, ""); err != nil {
		t.Fatal(err)
	}
	if err := lm.Acquire(2, []byte("b"), Exclusive, ""); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := lm.Acquire(1, []byte("b"), Exclusive, "")
		errCh <- err
	}()
	// Give the first waiter a moment to register.
	time.Sleep(20 * time.Millisecond)
	go func() {
		defer wg.Done()
		err := lm.Acquire(2, []byte("a"), Exclusive, "")
		if nerr.HasCode(err, nerr.Deadlock) {
			lm.ReleaseAll(2)
		}
		errCh <- err
	}()
	wg.Wait()
	close(errCh)
	deadlock := 0
	ok := 0
	for err := range errCh {
		if err == nil {
			ok++
			continue
		}
		if nerr.HasCode(err, nerr.Deadlock) {
			deadlock++
			continue
		}
		t.Fatalf("unexpected %v", err)
	}
	if deadlock != 1 {
		t.Fatalf("expected one deadlock, got deadlock=%d ok=%d", deadlock, ok)
	}
	lm.ReleaseAll(1)
	lm.ReleaseAll(2)
	_ = format.TxnID(0)
}

func TestRangeConflictsWithInsert(t *testing.T) {
	lm := NewLockManager()
	if err := lm.AcquireRange(1, []byte("a"), []byte("c"), Shared, ""); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- lm.Acquire(2, []byte("b"), Exclusive, "")
	}()
	select {
	case err := <-done:
		t.Fatalf("insert should wait on range lock, got %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	lm.ReleaseAll(1)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("insert did not wake")
	}
	lm.ReleaseAll(2)
}

func TestLockSnapshotTag(t *testing.T) {
	lm := NewLockManager()
	if err := lm.Acquire(1, []byte("row-a"), Shared, "orders"); err != nil {
		t.Fatal(err)
	}
	if err := lm.AcquireRange(2, []byte("m"), []byte("z"), Shared, "customers"); err != nil {
		t.Fatal(err)
	}
	got := lm.Snapshot()
	if len(got) != 2 {
		t.Fatalf("Snapshot() len = %d, want 2: %+v", len(got), got)
	}
	var sawKey, sawRange bool
	for _, li := range got {
		switch li.Txn {
		case 1:
			sawKey = true
			if li.Tag != "orders" || li.Mode != Shared || li.Range {
				t.Fatalf("key lock info = %+v", li)
			}
		case 2:
			sawRange = true
			if li.Tag != "customers" || li.Mode != Shared || !li.Range {
				t.Fatalf("range lock info = %+v", li)
			}
		default:
			t.Fatalf("unexpected txn id in snapshot: %+v", li)
		}
	}
	if !sawKey || !sawRange {
		t.Fatalf("missing rows: key=%v range=%v", sawKey, sawRange)
	}

	// A second holder sharing the same key (both Shared, so it grants
	// immediately) keeps the first-set tag even though it passes none.
	if err := lm.Acquire(3, []byte("row-a"), Shared, ""); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, li := range lm.Snapshot() {
		if li.Txn == 3 {
			found = true
			if li.Tag != "orders" {
				t.Fatalf("tag not preserved from first holder: %+v", li)
			}
		}
	}
	if !found {
		t.Fatal("txn 3's key lock missing from snapshot")
	}

	lm.ReleaseAll(1)
	lm.ReleaseAll(2)
	lm.ReleaseAll(3)
	if got := lm.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() after ReleaseAll = %+v, want empty", got)
	}
}

func TestLockWaitTimeoutZeroBlocksIndefinitely(t *testing.T) {
	lm := NewLockManager()
	if err := lm.Acquire(1, []byte("a"), Exclusive, ""); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- lm.Acquire(2, []byte("a"), Exclusive, "") }()
	select {
	case err := <-done:
		t.Fatalf("Acquire returned early with no timeout configured: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	lm.ReleaseAll(1)
	if err := <-done; err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	lm.ReleaseAll(2)
}

func TestLockWaitTimeoutExceeded(t *testing.T) {
	lm := NewLockManager()
	lm.SetWaitTimeout(30 * time.Millisecond)
	if err := lm.Acquire(1, []byte("a"), Exclusive, "orders"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := lm.Acquire(2, []byte("a"), Exclusive, "")
	if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("Acquire error = %v, want Exhausted", err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond || elapsed > time.Second {
		t.Fatalf("Acquire returned after %v, want ~30ms", elapsed)
	}
	// The timed-out waiter must not have leaked a phantom holder: only txn 1
	// still holds the key, and a third transaction can still not acquire it
	// (proving the state is exactly "txn 1 holds, nobody else").
	snap := lm.Snapshot()
	if len(snap) != 1 || snap[0].Txn != 1 || snap[0].Tag != "orders" {
		t.Fatalf("Snapshot() after timeout = %+v, want only txn 1 holding", snap)
	}
	lm.ReleaseAll(1)
	if err := lm.Acquire(3, []byte("a"), Exclusive, ""); err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	lm.ReleaseAll(3)
}

func TestLockWaitTimeoutRangeExceeded(t *testing.T) {
	lm := NewLockManager()
	lm.SetWaitTimeout(30 * time.Millisecond)
	if err := lm.AcquireRange(1, []byte("a"), []byte("z"), Exclusive, ""); err != nil {
		t.Fatal(err)
	}
	err := lm.AcquireRange(2, []byte("b"), []byte("c"), Exclusive, "")
	if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("AcquireRange error = %v, want Exhausted", err)
	}
	lm.ReleaseAll(1)
	if err := lm.AcquireRange(3, []byte("b"), []byte("c"), Exclusive, ""); err != nil {
		t.Fatalf("AcquireRange after release: %v", err)
	}
	lm.ReleaseAll(3)
}

// TestLockWaitTimeoutRaceWithGrant exercises the narrow window where the
// blocked waiter is granted the instant the wait timeout fires: the holder
// releases right at the configured deadline, repeatedly, so both outcomes
// (a clean grant and a timeout) are exercised across iterations. Either
// outcome is acceptable, but the lock must never end up "held by nobody
// that anyone can observe" nor double-granted — checked by requiring a
// third transaction to always be able to acquire the key soon after.
func TestLockWaitTimeoutRaceWithGrant(t *testing.T) {
	lm := NewLockManager()
	const timeout = 15 * time.Millisecond
	lm.SetWaitTimeout(timeout)
	for i := 0; i < 200; i++ {
		txn1, txn2, txn3 := format.TxnID(i*3+1), format.TxnID(i*3+2), format.TxnID(i*3+3)
		if err := lm.Acquire(txn1, []byte("k"), Exclusive, ""); err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(timeout)
			lm.ReleaseAll(txn1)
		}()
		err := lm.Acquire(txn2, []byte("k"), Exclusive, "")
		if err != nil && !nerr.HasCode(err, nerr.Exhausted) {
			t.Fatalf("iteration %d: Acquire error = %v", i, err)
		}
		if err == nil {
			lm.ReleaseAll(txn2)
		}
		// Whichever way it went, the key must be free (or held only by
		// txn2, already released above) within one more timeout window.
		done := make(chan error, 1)
		go func() { done <- lm.Acquire(txn3, []byte("k"), Exclusive, "") }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("iteration %d: txn3 Acquire = %v", i, err)
			}
			lm.ReleaseAll(txn3)
		case <-time.After(2 * timeout):
			t.Fatalf("iteration %d: txn3 never acquired the key — lock leaked", i)
		}
	}
}
