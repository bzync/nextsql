package executor

import (
	"context"
	"sync"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
)

// taskJob tags a claimed task with the database, execution config, and
// parent context it was claimed under, plus an optional completion hook —
// deliberately not tied to *TaskRuntime, so both TaskRuntime's own per-database
// poll loop (M2-3b-3a) and CentralScheduler's single cross-database poll
// loop (M2-3b-3b) can submit compatible jobs to one shared worker. Cancel
// registration/lookup (db.registerTaskCancel/db.taskCancels) is per-database
// state on *DB itself, not per-submitter, so CANCEL TASK works identically
// regardless of which one submitted a given job.
type taskJob struct {
	ctx    context.Context
	db     *DB
	task   *catalog.Task
	config TaskRuntimeConfig
	// onDone, if non-nil, runs after execution (and after the pool has
	// already released the job's slot) with the execution error, letting
	// the submitter do its own completion bookkeeping (TaskRuntime.inFlight/
	// report; CentralScheduler's per-tick per-database ref release).
	onDone func(err error)
}

// runClaimedTask executes one claimed task against db, wiring the durable
// per-database cancel registry (db.registerTaskCancel/db.taskCancels) —
// that registry is what makes CANCEL TASK take effect immediately on a
// running task, and it needs no per-submitter state at all.
func runClaimedTask(ctx context.Context, db *DB, task *catalog.Task, config TaskRuntimeConfig) error {
	taskCtx, cancel := context.WithCancel(ctx)
	db.registerTaskCancel(task.ID, cancel)
	defer func() {
		cancel()
		db.unregisterTaskCancel(task.ID)
	}()
	return db.executeClaimedTask(taskCtx, task, config.ACL, config.Audit, config.Limits, config.Now)
}

// TaskPool is a fixed-size worker set shared, process-wide, by every open
// database's task execution. Before M2-3b-3a, each database's own
// TaskRuntime spawned its own Workers+1 goroutines, so total
// task-execution goroutines scaled with the number of open databases
// (unbounded process-wide, unlike M2-3b-2's shared buffer-memory budget).
type TaskPool struct {
	jobs  chan taskJob
	slots chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// NewTaskPool starts a pool of workers workers (0 uses defaultTaskWorkers,
// matching StartTaskRuntime's own pre-M2-3b-3a default). A nil parent
// defaults to context.Background — TaskPool's lifecycle is driven entirely
// by Close, not by an external context, since it must outlive every
// TaskRuntime that submits to it (Close is safe to call only after every
// such TaskRuntime has itself already been closed; see cmd/nextsqld's
// shutdown ordering).
func NewTaskPool(parent context.Context, workers int) (*TaskPool, error) {
	if workers == 0 {
		workers = defaultTaskWorkers
	}
	if workers < 1 || workers > maxTaskWorkers {
		return nil, nerr.New(nerr.InvalidArgument, "executor.NewTaskPool", "task pool workers must be between 1 and 16")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p := &TaskPool{
		jobs:  make(chan taskJob, workers),
		slots: make(chan struct{}, workers),
		ctx:   ctx, cancel: cancel,
	}
	for i := 0; i < workers; i++ {
		p.slots <- struct{}{}
		p.wg.Add(1)
		go p.worker()
	}
	return p, nil
}

// Close stops accepting new jobs and waits for every in-progress one to
// finish. It must only be called once every submitter (every TaskRuntime,
// and any CentralScheduler) submitting to this pool has itself already been
// closed (both wait out their own in-flight submissions before returning) —
// closing the pool first would leave a still-open submitter's poll loop
// blocked trying to send to a jobs channel nobody drains anymore.
func (p *TaskPool) Close() error {
	if p == nil {
		return nil
	}
	p.once.Do(p.cancel)
	p.wg.Wait()
	return nil
}

func (p *TaskPool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.jobs:
			err := runClaimedTask(job.ctx, job.db, job.task, job.config)
			p.releaseSlots(1)
			if job.onDone != nil {
				job.onDone(err)
			}
		}
	}
}

func (p *TaskPool) releaseSlots(n int) {
	for i := 0; i < n; i++ {
		p.slots <- struct{}{}
	}
}
