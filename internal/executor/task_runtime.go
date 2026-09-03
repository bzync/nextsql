package executor

import (
	"context"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/security"
)

const (
	defaultTaskPollInterval = 250 * time.Millisecond
	minTaskPollInterval     = 10 * time.Millisecond
	maxTaskPollInterval     = time.Minute
	defaultTaskWorkers      = 2
	maxTaskWorkers          = 16
	defaultTaskBatch        = 16
	defaultTaskPurgeEvery   = time.Minute
)

type TaskRuntimeConfig struct {
	Batch        int
	PollInterval time.Duration
	PurgeEvery   time.Duration
	Limits       scheduler.Limits
	ACL          *security.ACL
	Audit        *security.Log
	Now          func() time.Time
	OnError      func(error)
}

// TaskRuntime is one database's coordinator: it polls that database's own
// due tasks/schedules on its own schedule and submits claims to a shared
// TaskPool (M2-3b-3a) rather than owning any worker goroutines itself —
// before this, each TaskRuntime spawned its own fixed worker set, so total
// task-execution goroutines scaled with the number of open databases.
type TaskRuntime struct {
	db     *DB
	pool   *TaskPool
	config TaskRuntimeConfig

	ctx    context.Context
	cancel context.CancelFunc

	// inFlight tracks claims this runtime has submitted to pool.jobs that a
	// pool worker has not yet finished executing. Close waits it out after
	// stopping its own coordinator, so by the time Close returns, no pool
	// worker holds or will pick up a reference to this runtime's db —
	// letting the caller safely close the database right after (this is the
	// correctness hazard a shared pool introduces that per-runtime workers
	// never had: Close used to synchronously stop the exact goroutines that
	// could touch db, since they belonged to this runtime alone).
	inFlight sync.WaitGroup
	wg       sync.WaitGroup
	once     sync.Once
}

func StartTaskRuntime(parent context.Context, db *DB, pool *TaskPool, config TaskRuntimeConfig) (*TaskRuntime, error) {
	if db == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartTaskRuntime", "nil database")
	}
	if pool == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartTaskRuntime", "nil task pool")
	}
	if parent == nil {
		parent = context.Background()
	}
	if config.Batch == 0 {
		config.Batch = defaultTaskBatch
	}
	if config.Batch < 1 || config.Batch > maxDispatchBatch {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartTaskRuntime", "task batch must be between 1 and 256")
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultTaskPollInterval
	}
	if config.PollInterval < minTaskPollInterval || config.PollInterval > maxTaskPollInterval {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartTaskRuntime", "task poll interval must be between 10ms and 1m")
	}
	if config.PurgeEvery == 0 {
		config.PurgeEvery = defaultTaskPurgeEvery
	}
	if config.PurgeEvery < config.PollInterval {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartTaskRuntime", "task purge interval must not be shorter than polling")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	ctx, cancel := context.WithCancel(parent)
	runtime := &TaskRuntime{
		db: db, pool: pool, config: config, ctx: ctx, cancel: cancel,
	}
	runtime.wg.Add(1)
	go runtime.coordinate()
	return runtime, nil
}

// Close stops this runtime's own polling and waits for every claim it has
// already submitted to the shared pool to finish executing (see inFlight's
// doc comment) — it does not stop the pool itself, which is shared by every
// other open database's runtime.
func (r *TaskRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.once.Do(r.cancel)
	r.wg.Wait()
	r.inFlight.Wait()
	return nil
}

func (r *TaskRuntime) coordinate() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.config.PollInterval)
	defer ticker.Stop()
	purge := time.NewTicker(r.config.PurgeEvery)
	defer purge.Stop()
	r.cycle()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.cycle()
		case <-purge.C:
			if _, err := r.db.PurgeTaskHistory(r.ctx, r.config.Now(), r.config.Batch); err != nil && !nerr.HasCode(err, nerr.Unavailable) && !nerr.HasCode(err, nerr.Canceled) {
				r.report(err)
			}
		}
	}
}

func (r *TaskRuntime) cycle() {
	available := 0
	for available < r.config.Batch {
		select {
		case <-r.ctx.Done():
			r.pool.releaseSlots(available)
			return
		case <-r.pool.slots:
			available++
		default:
			goto claimedSlots
		}
	}

claimedSlots:
	if available == 0 {
		return
	}
	now := r.config.Now()
	claims, err := r.db.ClaimDueTasks(r.ctx, now, available)
	if err != nil {
		r.pool.releaseSlots(available)
		if !nerr.HasCode(err, nerr.Unavailable) && !nerr.HasCode(err, nerr.Canceled) {
			r.report(err)
		}
		return
	}
	remaining := available - len(claims)
	if remaining > 0 {
		if _, err := r.db.DispatchDueSchedules(r.ctx, now, remaining); err != nil {
			if !nerr.HasCode(err, nerr.Unavailable) && !nerr.HasCode(err, nerr.Canceled) {
				r.report(err)
			}
		} else {
			more, claimErr := r.db.ClaimDueTasks(r.ctx, now, remaining)
			if claimErr != nil {
				if !nerr.HasCode(claimErr, nerr.Unavailable) && !nerr.HasCode(claimErr, nerr.Canceled) {
					r.report(claimErr)
				}
			} else {
				claims = append(claims, more...)
			}
		}
	}
	r.pool.releaseSlots(available - len(claims))
	for i, claim := range claims {
		// inFlight must be counted before the send resolves either way, so
		// Close (which waits it out after stopping this coordinator) can
		// never race a claim that is in the middle of being handed off.
		r.inFlight.Add(1)
		task := claim
		job := taskJob{
			ctx: r.ctx, db: r.db, task: task, config: r.config,
			onDone: func(err error) {
				defer r.inFlight.Done()
				if err != nil && !nerr.HasCode(err, nerr.Canceled) && !nerr.HasCode(err, nerr.Unavailable) {
					r.report(err)
				}
			},
		}
		select {
		case <-r.ctx.Done():
			r.inFlight.Done()
			r.pool.releaseSlots(len(claims) - i)
			return
		case r.pool.jobs <- job:
		}
	}
}

func (r *TaskRuntime) report(err error) {
	if err != nil && r.config.OnError != nil {
		r.config.OnError(err)
	}
}
