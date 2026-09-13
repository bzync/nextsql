package txn

import (
	"context"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// waitQueued blocks until n requests are queued in lm.
func waitQueued(t *testing.T, lm *LockManager, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lm.mu.Lock()
		got := len(lm.waiters)
		lm.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters = %d, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// A deadlock that forms after a lock changes hands must still be detected.
//
// T3 holds B and waits for A, which T1 holds; T2 also waits for A. When T1
// releases A it is granted to T2, but T3's wait-for edge used to keep pointing
// at T1 -- the transaction that had just released -- because wake() never
// recomputed the edges of the waiters it left queued. When T2 then asked for
// B, the cycle check walked T2 -> T3 -> T1, found T1 waiting on nothing, and
// let T2 block: T2 waits for T3, T3 waits for T2, and neither is ever told.
// Under concurrent transfers this held every admission slot for the whole
// statement budget (tests/integration TestSustainedMixedLoadKeepsExactInvariant).
func TestDeadlockFormedAfterHandoffIsDetected(t *testing.T) {
	lm := NewLockManager()
	const t1, t2, t3 = 1, 2, 3
	a, b := []byte("A"), []byte("B")
	if err := lm.Acquire(t3, b, Exclusive, ""); err != nil {
		t.Fatal(err)
	}
	if err := lm.Acquire(t1, a, Exclusive, ""); err != nil {
		t.Fatal(err)
	}
	t2Granted := make(chan error, 1)
	go func() { t2Granted <- lm.Acquire(t2, a, Exclusive, "") }()
	waitQueued(t, lm, 1)
	t3Result := make(chan error, 1)
	go func() { t3Result <- lm.Acquire(t3, a, Exclusive, "") }()
	waitQueued(t, lm, 2)

	lm.ReleaseAll(t1) // A goes to T2, the first waiter; T3 keeps waiting
	if err := <-t2Granted; err != nil {
		t.Fatalf("T2 was not granted A: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := lm.AcquireContext(ctx, t2, b, Exclusive, "")
	if !nerr.HasCode(err, nerr.Deadlock) {
		t.Fatalf("T2 requesting B while T3 (holding B) waits for T2's A: got %v, want an immediate deadlock", err)
	}
	// The victim's refusal leaves T3 able to proceed once T2 finishes.
	lm.ReleaseAll(t2)
	select {
	case err := <-t3Result:
		if err != nil {
			t.Fatalf("T3 after T2 released: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("T3 was never granted A after T2 released it")
	}
}

// The same staleness through a compatible grant: T1 holds A shared and T2
// waits for A exclusively. T3 is granted A shared at once (compatible with
// T1), so T2 is now blocked by T3 too. T3 then waits for B, which T2 holds.
// With T2's edge still naming only T1, that cycle went undetected.
func TestDeadlockThroughCompatibleGrantIsDetected(t *testing.T) {
	lm := NewLockManager()
	const t1, t2, t3 = 1, 2, 3
	a, b := []byte("A"), []byte("B")
	if err := lm.Acquire(t2, b, Exclusive, ""); err != nil {
		t.Fatal(err)
	}
	if err := lm.Acquire(t1, a, Shared, ""); err != nil {
		t.Fatal(err)
	}
	t2Result := make(chan error, 1)
	go func() { t2Result <- lm.Acquire(t2, a, Exclusive, "") }()
	waitQueued(t, lm, 1)
	if err := lm.Acquire(t3, a, Shared, ""); err != nil {
		t.Fatalf("T3 shared A beside T1: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := lm.AcquireContext(ctx, t3, b, Exclusive, ""); !nerr.HasCode(err, nerr.Deadlock) {
		t.Fatalf("T3 requesting B while T2 (holding B) waits on T3's A: got %v, want deadlock", err)
	}
	lm.ReleaseAll(t3)
	lm.ReleaseAll(t1)
	select {
	case err := <-t2Result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("T2 never got A")
	}
}
