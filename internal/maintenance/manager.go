// Package maintenance coordinates bounded database maintenance work.
package maintenance

import (
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// Run describes a current or completed maintenance pass.
type Run struct {
	Kind, Scope string
	Started     time.Time
	Finished    time.Time
	Affected    int
	Failed      bool
	CPUUsed     time.Duration
	MemoryUsed  int64
	IOUsed      int64
}

type Limits struct {
	CPU    time.Duration
	Memory int64
	IO     int64
}

var DefaultLimits = Limits{CPU: 30 * time.Second, Memory: 8 << 20, IO: 500_000}

// Budget is owned by one synchronous run.
type Budget struct {
	started    time.Time
	limits     Limits
	memory     int64
	memoryPeak int64
	io         int64
}

func (b *Budget) Check() error {
	if b != nil && b.limits.CPU > 0 && time.Since(b.started) > b.limits.CPU {
		return nerr.New(nerr.Exhausted, "maintenance.Budget", "CPU-time budget exhausted")
	}
	return nil
}

func (b *Budget) ReserveMemory(n int64) error {
	if b == nil || n <= 0 {
		return b.Check()
	}
	if b.limits.Memory > 0 && (b.memory > b.limits.Memory-n) {
		return nerr.New(nerr.Exhausted, "maintenance.Budget", "memory budget exhausted")
	}
	b.memory += n
	if b.memory > b.memoryPeak {
		b.memoryPeak = b.memory
	}
	return b.Check()
}

func (b *Budget) ReleaseMemory(n int64) {
	if b == nil || n <= 0 {
		return
	}
	if n >= b.memory {
		b.memory = 0
	} else {
		b.memory -= n
	}
}

// ConsumeIO charges conservative logical page-I/O work before it starts.
func (b *Budget) ConsumeIO(n int64) error {
	if b == nil || n <= 0 {
		return b.Check()
	}
	if b.limits.IO > 0 && b.io > b.limits.IO-n {
		return nerr.New(nerr.Exhausted, "maintenance.Budget", "I/O budget exhausted")
	}
	b.io += n
	return b.Check()
}

// Status is a race-free coordinator snapshot.
type Status struct {
	Paused bool
	Active *Run
	Last   *Run
}

// Manager serializes maintenance. Work executes synchronously in the caller;
// the manager never creates independent goroutines or an unbounded queue.
type Manager struct {
	mu     sync.Mutex
	paused bool
	active *Run
	last   *Run
	limits Limits
}

func New() *Manager { return &Manager{limits: DefaultLimits} }

func (m *Manager) Run(kind, scope string, fn func() (int, error)) (int, error) {
	if fn == nil {
		return 0, nerr.New(nerr.InvalidArgument, "maintenance.Run", "work is required")
	}
	return m.RunBudgeted(kind, scope, func(_ *Budget) (int, error) { return fn() })
}

func (m *Manager) RunBudgeted(kind, scope string, fn func(*Budget) (int, error)) (int, error) {
	if m == nil || fn == nil {
		return 0, nerr.New(nerr.InvalidArgument, "maintenance.Run", "manager and work are required")
	}
	m.mu.Lock()
	if m.paused {
		m.mu.Unlock()
		return 0, nerr.New(nerr.Unavailable, "maintenance.Run", "maintenance is paused")
	}
	if m.active != nil {
		m.mu.Unlock()
		return 0, nerr.New(nerr.Unavailable, "maintenance.Run", "another maintenance pass is active")
	}
	run := &Run{Kind: kind, Scope: scope, Started: time.Now().UTC()}
	limits := m.limits
	m.active = run
	m.mu.Unlock()

	budget := &Budget{started: time.Now(), limits: limits}
	n, err := fn(budget)
	m.mu.Lock()
	run.Affected = n
	run.Failed = err != nil
	run.Finished = time.Now().UTC()
	run.CPUUsed = time.Since(budget.started)
	run.MemoryUsed = budget.memoryPeak
	run.IOUsed = budget.io
	m.last = cloneRun(run)
	m.active = nil
	m.mu.Unlock()
	return n, err
}

func (m *Manager) SetLimits(l Limits) error {
	if m == nil || l.CPU <= 0 || l.Memory <= 0 || l.IO <= 0 {
		return nerr.New(nerr.InvalidArgument, "maintenance.SetLimits", "positive CPU, memory, and I/O limits are required")
	}
	m.mu.Lock()
	m.limits = l
	m.mu.Unlock()
	return nil
}

func (m *Manager) Pause() {
	if m != nil {
		m.mu.Lock()
		m.paused = true
		m.mu.Unlock()
	}
}

func (m *Manager) Resume() {
	if m != nil {
		m.mu.Lock()
		m.paused = false
		m.mu.Unlock()
	}
}

func (m *Manager) Status() Status {
	if m == nil {
		return Status{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{Paused: m.paused, Active: cloneRun(m.active), Last: cloneRun(m.last)}
}

func cloneRun(r *Run) *Run {
	if r == nil {
		return nil
	}
	out := *r
	return &out
}
