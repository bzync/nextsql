package scheduler

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/bzync/nextsql/internal/nerr"
)

// Pool is a process-wide bounded worker set. Queries take at most
// Limits.Workers slots; the pool never starts unbounded goroutines.
type Pool struct {
	slots chan struct{}
}

// NewPool creates a pool with cap slots. cap < 1 becomes 1.
func NewPool(cap int) *Pool {
	if cap < 1 {
		cap = 1
	}
	if cap > MaxWorkers*4 {
		cap = MaxWorkers * 4
	}
	return &Pool{slots: make(chan struct{}, cap)}
}

// DefaultPool is the process scheduler used by local sessions.
var DefaultPool = NewPool(MaxWorkers * 2)

// Run executes tasks with at most workers goroutines. On the first
// task error remaining work is cancelled. ctx cancellation stops new work.
func (p *Pool) Run(ctx context.Context, workers int, tasks []func() error) error {
	if p == nil {
		p = DefaultPool
	}
	n := len(tasks)
	if n == 0 {
		return nil
	}
	if workers < 1 {
		workers = 1
	}
	if workers > n {
		workers = n
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		next  atomic.Int64
		done  atomic.Int64
		mu    sync.Mutex
		first error
		wg    sync.WaitGroup
	)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case p.slots <- struct{}{}:
			}
			defer func() { <-p.slots }()
			for {
				if ctx.Err() != nil {
					return
				}
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				if err := tasks[i](); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
					cancel()
					return
				}
				done.Add(1)
			}
		}()
	}
	wg.Wait()
	if first != nil {
		return first
	}
	if int(done.Load()) < n {
		if err := ctx.Err(); err != nil {
			if err == context.DeadlineExceeded {
				return nerr.New(nerr.Exhausted, "scheduler.Pool", "execution time budget exceeded")
			}
			return nerr.New(nerr.Exhausted, "scheduler.Pool", "query cancelled")
		}
		return nerr.New(nerr.Internal, "scheduler.Pool", "incomplete work")
	}
	return nil
}
