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
	if err := lm.Acquire(1, []byte("a"), Shared); err != nil {
		t.Fatal(err)
	}
	if err := lm.Acquire(2, []byte("a"), Shared); err != nil {
		t.Fatal(err)
	}
	lm.ReleaseAll(1)
	lm.ReleaseAll(2)
}

func TestDeadlockAbortsOne(t *testing.T) {
	lm := NewLockManager()
	if err := lm.Acquire(1, []byte("a"), Exclusive); err != nil {
		t.Fatal(err)
	}
	if err := lm.Acquire(2, []byte("b"), Exclusive); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := lm.Acquire(1, []byte("b"), Exclusive)
		errCh <- err
	}()
	// Give the first waiter a moment to register.
	time.Sleep(20 * time.Millisecond)
	go func() {
		defer wg.Done()
		err := lm.Acquire(2, []byte("a"), Exclusive)
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
	if err := lm.AcquireRange(1, []byte("a"), []byte("c"), Shared); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- lm.Acquire(2, []byte("b"), Exclusive)
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
