package scheduler

import (
	"context"
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
// rejects; it never starts unbounded work.
type Admission struct {
	maxInflight int
	maxQueue    int
	wait        time.Duration

	slots    chan struct{}
	queued   atomic.Int64
	inflight atomic.Int64
	admitted atomic.Int64
	rejected atomic.Int64
	timedout atomic.Int64
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
		slots:       make(chan struct{}, cfg.MaxInflight),
	}
}

// Acquire reserves one in-flight slot. The caller must invoke the
// returned release function exactly once on success.
func (a *Admission) Acquire(ctx context.Context) (func(), error) {
	if a == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case a.slots <- struct{}{}:
		return a.enter(), nil
	default:
	}
	q := a.queued.Add(1)
	if q > int64(a.maxQueue) {
		a.queued.Add(-1)
		a.rejected.Add(1)
		return nil, nerr.New(nerr.Unavailable, "scheduler.Admission", "admission queue is full")
	}
	defer a.queued.Add(-1)

	timer := time.NewTimer(a.wait)
	defer timer.Stop()
	select {
	case a.slots <- struct{}{}:
		return a.enter(), nil
	case <-ctx.Done():
		a.rejected.Add(1)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, nerr.New(nerr.Exhausted, "scheduler.Admission", "admission wait exceeded")
		}
		return nil, nerr.New(nerr.Canceled, "scheduler.Admission", "query cancelled while queued")
	case <-timer.C:
		a.timedout.Add(1)
		a.rejected.Add(1)
		return nil, nerr.New(nerr.Unavailable, "scheduler.Admission", "admission queue wait exceeded")
	}
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
		<-a.slots
	}
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
