package scheduler

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestAdmissionPriorityOrdersQueuedWaiters proves both priority ordering
// and FIFO tie-break among equal priorities: with the sole slot held,
// enqueue low/high/low (in that order), release, and expect the high
// waiter admitted first, then the two lows in their original order.
func TestAdmissionPriorityOrdersQueuedWaiters(t *testing.T) {
	a := NewAdmission(AdmissionConfig{MaxInflight: 1, MaxQueue: 8, QueueWait: 2 * time.Second})
	rel, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var order []string
	launch := func(name string, priority int32) {
		go func() {
			r, err := a.AcquireWithPriority(context.Background(), priority)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				return
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			r()
		}()
	}

	launch("low1", 0)
	time.Sleep(5 * time.Millisecond)
	launch("high", 5)
	time.Sleep(5 * time.Millisecond)
	launch("low2", 0)
	time.Sleep(5 * time.Millisecond)

	rel()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for all waiters, got %v", order)
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	want := []string{"high", "low1", "low2"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestAdmissionStressNoLeakUnderChurn drives many goroutines with random
// priorities and a mix of background/short-deadline contexts through the
// gate concurrently, then asserts every slot and queue entry is fully
// accounted for afterward — no leaked slot, no leaked waiter.
func TestAdmissionStressNoLeakUnderChurn(t *testing.T) {
	a := NewAdmission(AdmissionConfig{MaxInflight: 4, MaxQueue: 50, QueueWait: 50 * time.Millisecond})
	const n = 500
	var wg sync.WaitGroup
	wg.Add(n)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < n; i++ {
		priority := int32(rng.Intn(10))
		useTimeout := rng.Intn(3) == 0
		var d time.Duration
		if useTimeout {
			d = time.Duration(rng.Intn(20)+1) * time.Millisecond
		}
		holdMillis := rng.Intn(2)
		go func(priority int32, useTimeout bool, d time.Duration, holdMillis int) {
			defer wg.Done()
			ctx := context.Background()
			if useTimeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, d)
				defer cancel()
			}
			rel, err := a.AcquireWithPriority(ctx, priority)
			if err != nil {
				return
			}
			time.Sleep(time.Duration(holdMillis) * time.Millisecond)
			rel()
		}(priority, useTimeout, d, holdMillis)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("stress test hung — possible deadlock or leaked waiter")
	}

	a.mu.Lock()
	free := a.free
	waiting := a.waiters.Len()
	a.mu.Unlock()
	if free != a.maxInflight {
		t.Fatalf("free = %d, want %d (leaked slot)", free, a.maxInflight)
	}
	if waiting != 0 {
		t.Fatalf("waiters.Len() = %d, want 0 (leaked waiter)", waiting)
	}
	st := a.Stats()
	if st.Inflight != 0 {
		t.Fatalf("Stats().Inflight = %d, want 0", st.Inflight)
	}
	if st.Queued != 0 {
		t.Fatalf("Stats().Queued = %d, want 0", st.Queued)
	}
}

// TestAdmissionCancelRaceAtRelease repeatedly races a waiter's context
// deadline against a concurrent release landing at roughly the same
// instant, to exercise the cancelWaiter/release handoff described in
// admit.go: the slot must never be left "granted but unclaimed" (which
// would deadlock the caller) nor double-counted (which would leak
// capacity).
func TestAdmissionCancelRaceAtRelease(t *testing.T) {
	for i := 0; i < 500; i++ {
		a := NewAdmission(AdmissionConfig{MaxInflight: 1, MaxQueue: 4, QueueWait: time.Second})
		holdRel, err := a.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}

		const timeout = time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		holdReleased := make(chan struct{})
		jitter := time.Duration(rand.Int63n(int64(time.Millisecond))) - time.Millisecond/2
		time.AfterFunc(timeout+jitter, func() {
			holdRel()
			close(holdReleased)
		})

		type outcome struct {
			rel func()
			err error
		}
		result := make(chan outcome, 1)
		go func() {
			rel, err := a.AcquireWithPriority(ctx, 0)
			result <- outcome{rel, err}
		}()

		select {
		case o := <-result:
			if o.err == nil {
				o.rel()
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: timed out — possible deadlock", i)
		}
		select {
		case <-holdReleased:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: original holder's release never fired", i)
		}
		if st := a.Stats(); st.Inflight != 0 {
			t.Fatalf("iter %d: after settling, Stats().Inflight = %d, want 0", i, st.Inflight)
		}
		cancel()
	}
}
