package scheduler

import (
	"container/heap"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	DefaultMaxInflight = 32
	DefaultMaxQueue    = 128
	DefaultQueueWait   = 5 * time.Second
)

// Admission is the process-wide query gate. Overload queues, then
// rejects; it never starts unbounded work. Queued waiters are granted in
// priority order (higher first), FIFO among equal priorities.
type Admission struct {
	maxInflight int
	maxQueue    int
	wait        time.Duration

	mu      sync.Mutex
	free    int
	waiters waiterHeap
	seq     int64

	queued   atomic.Int64
	inflight atomic.Int64
	admitted atomic.Int64
	rejected atomic.Int64
	timedout atomic.Int64
}

// waiter is one blocked Acquire call, ordered by the heap below.
type waiter struct {
	priority int32
	seq      int64
	grant    chan struct{} // buffered 1; release() sends exactly once
	index    int           // heap index; -1 once popped/removed
}

// waiterHeap orders waiters by priority descending, then by seq ascending
// (FIFO among equal priorities). index is kept in sync by Swap so a
// waiter can be located and heap.Remove'd in O(log n) on cancellation.
type waiterHeap []*waiter

func (h waiterHeap) Len() int { return len(h) }
func (h waiterHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority > h[j].priority
	}
	return h[i].seq < h[j].seq
}
func (h waiterHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *waiterHeap) Push(x any) {
	w := x.(*waiter)
	w.index = len(*h)
	*h = append(*h, w)
}
func (h *waiterHeap) Pop() any {
	old := *h
	n := len(old)
	w := old[n-1]
	old[n-1] = nil
	w.index = -1
	*h = old[:n-1]
	return w
}

// AdmissionConfig tunes the gate. Zero values become defaults.
type AdmissionConfig struct {
	MaxInflight int
	MaxQueue    int
	QueueWait   time.Duration
}

// DefaultAdmission returns conservative process defaults.
func DefaultAdmission() *Admission {
	return NewAdmission(AdmissionConfig{})
}

// NewAdmission builds a gate. maxInflight < 1 uses DefaultMaxInflight.
func NewAdmission(cfg AdmissionConfig) *Admission {
	if cfg.MaxInflight < 1 {
		cfg.MaxInflight = DefaultMaxInflight
	}
	if cfg.MaxQueue < 0 {
		cfg.MaxQueue = 0
	}
	if cfg.MaxQueue == 0 && cfg.QueueWait == 0 {
		cfg.MaxQueue = DefaultMaxQueue
	}
	if cfg.QueueWait <= 0 {
		cfg.QueueWait = DefaultQueueWait
	}
	return &Admission{
		maxInflight: cfg.MaxInflight,
		maxQueue:    cfg.MaxQueue,
		wait:        cfg.QueueWait,
		free:        cfg.MaxInflight,
	}
}

// Acquire reserves one in-flight slot at normal (0) priority. The caller
// must invoke the returned release function exactly once on success.
func (a *Admission) Acquire(ctx context.Context) (func(), error) {
	return a.acquire(ctx, 0)
}

// AcquireWithPriority is Acquire with an explicit priority: 0 is normal,
// higher values (matching catalog.ResourceGroup.Priority) are favored
// over lower ones when multiple callers are queued for the same slot.
// Priority only affects queue ordering — it never lets a caller bypass
// maxInflight/maxQueue, and it does not change a waiter's own QueueWait
// bound.
func (a *Admission) AcquireWithPriority(ctx context.Context, priority int32) (func(), error) {
	return a.acquire(ctx, priority)
}

func (a *Admission) acquire(ctx context.Context, priority int32) (func(), error) {
	if a == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	// waiters.Len()==0 is required, not just free>0: otherwise a fresh
	// concurrent caller could steal a just-freed slot ahead of a
	// longer-waiting, possibly higher-priority waiter already queued.
	if a.free > 0 && a.waiters.Len() == 0 {
		a.free--
		a.mu.Unlock()
		return a.enter(), nil
	}
	q := a.queued.Add(1)
	if q > int64(a.maxQueue) {
		a.queued.Add(-1)
		a.mu.Unlock()
		a.rejected.Add(1)
		return nil, nerr.New(nerr.Unavailable, "scheduler.Admission", "admission queue is full")
	}
	a.seq++
	w := &waiter{priority: priority, seq: a.seq, grant: make(chan struct{}, 1)}
	heap.Push(&a.waiters, w)
	a.mu.Unlock()
	defer a.queued.Add(-1)

	timer := time.NewTimer(a.wait)
	defer timer.Stop()
	select {
	case <-w.grant:
		return a.enter(), nil
	case <-ctx.Done():
		if a.cancelWaiter(w) {
			a.rejected.Add(1)
			if ctx.Err() == context.DeadlineExceeded {
				return nil, nerr.New(nerr.Exhausted, "scheduler.Admission", "admission wait exceeded")
			}
			return nil, nerr.New(nerr.Canceled, "scheduler.Admission", "query cancelled while queued")
		}
		// Lost the race: release() already popped and committed this
		// slot to us before cancellation landed. Must consume the
		// grant, not discard it, or the slot leaks forever.
		<-w.grant
		return a.enter(), nil
	case <-timer.C:
		if a.cancelWaiter(w) {
			a.timedout.Add(1)
			a.rejected.Add(1)
			return nil, nerr.New(nerr.Unavailable, "scheduler.Admission", "admission queue wait exceeded")
		}
		<-w.grant
		return a.enter(), nil
	}
}

// cancelWaiter removes w from the heap iff it is still pending there
// (w.index >= 0). It returns false if release() already popped w — in
// that case a slot has already been committed to w and the caller must
// treat this as a grant, not a cancellation.
func (a *Admission) cancelWaiter(w *waiter) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if w.index < 0 {
		return false
	}
	heap.Remove(&a.waiters, w.index)
	return true
}

func (a *Admission) enter() func() {
	a.inflight.Add(1)
	a.admitted.Add(1)
	var once atomic.Bool
	return func() {
		if once.Swap(true) {
			return
		}
		a.inflight.Add(-1)
		a.release()
	}
}

// release hands the freed slot to the highest-priority queued waiter, if
// any, otherwise returns it to the free pool.
func (a *Admission) release() {
	a.mu.Lock()
	if a.waiters.Len() > 0 {
		w := heap.Pop(&a.waiters).(*waiter)
		a.mu.Unlock()
		w.grant <- struct{}{} // buffered 1, sole sender, never blocks
		return
	}
	a.free++
	a.mu.Unlock()
}

// Stats is a snapshot of the gate.
type AdmissionStats struct {
	Inflight int64
	Queued   int64
	Admitted int64
	Rejected int64
	TimedOut int64
	Capacity int
}

func (a *Admission) Stats() AdmissionStats {
	if a == nil {
		return AdmissionStats{}
	}
	return AdmissionStats{
		Inflight: a.inflight.Load(),
		Queued:   a.queued.Load(),
		Admitted: a.admitted.Load(),
		Rejected: a.rejected.Load(),
		TimedOut: a.timedout.Load(),
		Capacity: a.maxInflight,
	}
}
