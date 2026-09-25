package scheduler

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	Batch1024          = 1024
	Batch2048          = 2048
	Batch4096          = 4096
	DefaultBatch       = Batch1024
	DefaultMemory      = 64 << 20
	DefaultDisk        = 256 << 20
	DefaultIO          = 1 << 30
	DefaultTimeout     = 30 * time.Second
	DefaultResultRows  = 1_000_000
	DefaultResultBytes = 64 << 20
	MaxWorkers         = 8
)

// Limits is the per-query resource budget.
type Limits struct {
	Workers     int
	Memory      int64
	Disk        int64
	IO          int64
	Time        time.Duration
	BatchSize   int
	ResultRows  int
	ResultBytes int64
}

// DefaultLimits returns conservative process defaults.
func DefaultLimits() Limits {
	w := runtime.GOMAXPROCS(0)
	if w < 1 {
		w = 1
	}
	if w > MaxWorkers {
		w = MaxWorkers
	}
	return Limits{
		Workers:     w,
		Memory:      DefaultMemory,
		Disk:        DefaultDisk,
		IO:          DefaultIO,
		Time:        DefaultTimeout,
		BatchSize:   DefaultBatch,
		ResultRows:  DefaultResultRows,
		ResultBytes: DefaultResultBytes,
	}
}

// NormalizeBatch snaps n to 1024, 2048, or 4096.
func NormalizeBatch(n int) int {
	switch {
	case n >= Batch4096:
		return Batch4096
	case n >= Batch2048:
		return Batch2048
	default:
		return Batch1024
	}
}

func (l Limits) normalized() Limits {
	if l.Workers < 1 {
		l.Workers = 1
	}
	if l.Workers > MaxWorkers {
		l.Workers = MaxWorkers
	}
	if l.Memory <= 0 {
		l.Memory = DefaultMemory
	}
	if l.Disk <= 0 {
		l.Disk = DefaultDisk
	}
	if l.IO <= 0 {
		l.IO = DefaultIO
	}
	if l.ResultRows <= 0 {
		l.ResultRows = DefaultResultRows
	}
	if l.ResultBytes <= 0 {
		l.ResultBytes = DefaultResultBytes
	}
	l.BatchSize = NormalizeBatch(l.BatchSize)
	return l
}

// Budget tracks charged resources and cancellation for one query.
type Budget struct {
	Limits Limits

	mu     sync.Mutex
	mem    int64
	peak   int64
	disk   int64
	io     int64
	start  time.Time
	ctx    context.Context
	cancel context.CancelFunc

	// fanOut is the widest parallel step actually run since the last
	// ResetFanOut, and workNS the summed elapsed time of those steps' tasks.
	// EXPLAIN used to report Limits.Workers here — the configured ceiling,
	// not a measurement, so a serial COUNT(*) fast path still claimed the
	// full limit — and copied wall time into the cpu column. These are what
	// actually happened.
	fanOut int
	workNS int64
}

// NewBudget starts a query budget. Time=0 means no deadline.
func NewBudget(parent context.Context, lim Limits) *Budget {
	if parent == nil {
		parent = context.Background()
	}
	lim = lim.normalized()
	ctx, cancel := context.WithCancel(parent)
	if lim.Time > 0 {
		ctx, cancel = context.WithTimeout(parent, lim.Time)
	}
	return &Budget{
		Limits: lim,
		start:  time.Now(),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (b *Budget) Context() context.Context {
	if b == nil {
		return context.Background()
	}
	return b.ctx
}

// ResetFanOut clears the measured parallelism so the next traced operator
// reports its own fan-out rather than one inherited from an earlier operator
// in the same statement.
func (b *Budget) ResetFanOut() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.fanOut, b.workNS = 0, 0
	b.mu.Unlock()
}

// NoteFanOut records that a parallel step ran on n goroutines for a summed
// work time of d. The widest step wins, because EXPLAIN reports one number
// per operator and the widest is what the operator was capable of using.
func (b *Budget) NoteFanOut(n int, d time.Duration) {
	if b == nil || n < 1 {
		return
	}
	b.mu.Lock()
	if n > b.fanOut {
		b.fanOut = n
	}
	if d > 0 {
		b.workNS += d.Nanoseconds()
	}
	b.mu.Unlock()
}

// FanOut is the measured parallelism since the last reset: 1 when nothing
// fanned out, which is the truthful answer for a serial path.
func (b *Budget) FanOut() int {
	if b == nil {
		return 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fanOut < 1 {
		return 1
	}
	return b.fanOut
}

// WorkNS is the summed elapsed time of the parallel tasks run since the last
// reset, or 0 when nothing fanned out. It is real work time across workers,
// not wall time, so it exceeds wall time on a genuinely parallel step.
func (b *Budget) WorkNS() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.workNS
}

func (b *Budget) Workers() int {
	if b == nil || b.Limits.Workers < 1 {
		return 1
	}
	return b.Limits.Workers
}

func (b *Budget) BatchSize() int {
	if b == nil {
		return DefaultBatch
	}
	return NormalizeBatch(b.Limits.BatchSize)
}

func (b *Budget) ResultRows() int {
	if b == nil || b.Limits.ResultRows < 1 {
		return DefaultResultRows
	}
	return b.Limits.ResultRows
}

func (b *Budget) ResultBytes() int64 {
	if b == nil || b.Limits.ResultBytes < 1 {
		return DefaultResultBytes
	}
	return b.Limits.ResultBytes
}

func (b *Budget) Cancel() {
	if b != nil && b.cancel != nil {
		b.cancel()
	}
}

func (b *Budget) Close() {
	b.Cancel()
}

func (b *Budget) PeakMemory() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

func (b *Budget) Disk() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.disk
}

func (b *Budget) IO() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.io
}

// Check reports cancellation or an exceeded time budget.
func (b *Budget) Check() error {
	if b == nil {
		return nil
	}
	if err := b.ctx.Err(); err != nil {
		if err == context.DeadlineExceeded {
			// A time budget is a resource, so exhausting it is Exhausted.
			return nerr.New(nerr.Exhausted, "scheduler.Budget", "execution time budget exceeded")
		}
		// Cancellation is not resource exhaustion. scheduler.Admission and
		// internal/protocol already report it as Canceled; these two sites
		// were the outliers, and reporting them as Exhausted made a deliberate
		// cancel indistinguishable from a memory or time bound.
		return nerr.New(nerr.Canceled, "scheduler.Budget", "query cancelled")
	}
	return nil
}

func (b *Budget) ChargeMem(n int64) error {
	if b == nil || n <= 0 {
		return b.Check()
	}
	if err := b.Check(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.mem+n > b.Limits.Memory {
		return nerr.New(nerr.Exhausted, "scheduler.Budget", "memory budget exceeded")
	}
	b.mem += n
	if b.mem > b.peak {
		b.peak = b.mem
	}
	return nil
}

func (b *Budget) ReleaseMem(n int64) {
	if b == nil || n <= 0 {
		return
	}
	b.mu.Lock()
	if b.mem >= n {
		b.mem -= n
	} else {
		b.mem = 0
	}
	b.mu.Unlock()
}

func (b *Budget) ChargeDisk(n int64) error {
	if b == nil || n <= 0 {
		return b.Check()
	}
	if err := b.Check(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.disk+n > b.Limits.Disk {
		return nerr.New(nerr.Exhausted, "scheduler.Budget", "disk spill budget exceeded")
	}
	b.disk += n
	return nil
}

func (b *Budget) ChargeIO(n int64) error {
	if b == nil || n <= 0 {
		return b.Check()
	}
	if err := b.Check(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.io+n > b.Limits.IO {
		return nerr.New(nerr.Exhausted, "scheduler.Budget", "I/O budget exceeded")
	}
	b.io += n
	return nil
}
