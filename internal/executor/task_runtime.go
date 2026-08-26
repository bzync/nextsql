package executor

import (
	"context"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
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
	Workers      int
	Batch        int
	PollInterval time.Duration
	PurgeEvery   time.Duration
	Limits       scheduler.Limits
	ACL          *security.ACL
	Audit        *security.Log
	Now          func() time.Time
	OnError      func(error)
}

// TaskRuntime is one fixed-size worker set plus one coordinator. It uses no
// goroutine per task and claims only work for which a worker slot is reserved.
type TaskRuntime struct {
	db     *DB
	config TaskRuntimeConfig

	ctx    context.Context
	cancel context.CancelFunc
	jobs   chan *catalog.Task
	slots  chan struct{}

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
	once    sync.Once
}

func StartTaskRuntime(parent context.Context, db *DB, config TaskRuntimeConfig) (*TaskRuntime, error) {
	if db == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartTaskRuntime", "nil database")
	}
	if parent == nil {
		parent = context.Background()
	}
	if config.Workers == 0 {
		config.Workers = defaultTaskWorkers
	}
	if config.Workers < 1 || config.Workers > maxTaskWorkers {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartTaskRuntime", "task workers must be between 1 and 16")
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
		db: db, config: config, ctx: ctx, cancel: cancel,
		jobs: make(chan *catalog.Task, config.Workers), slots: make(chan struct{}, config.Workers),
		running: make(map[string]context.CancelFunc),
	}
	for i := 0; i < config.Workers; i++ {
		runtime.slots <- struct{}{}
		runtime.wg.Add(1)
		go runtime.worker()
	}
	runtime.wg.Add(1)
	go runtime.coordinate()
	return runtime, nil
}

func (r *TaskRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.once.Do(r.cancel)
	r.wg.Wait()
	return nil
}

// Cancel writes the durable cancellation request before signaling a local
// worker. Failover therefore cannot lose a cancellation acknowledged here.
func (r *TaskRuntime) Cancel(ctx context.Context, id string) (*catalog.Task, error) {
	if r == nil || r.db == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.TaskRuntime.Cancel", "task runtime is not active")
	}
	task, err := r.db.RequestTaskCancellation(ctx, id, r.config.Now())
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	cancel := r.running[id]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return task, nil
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
			r.releaseSlots(available)
			return
		case <-r.slots:
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
		r.releaseSlots(available)
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
	r.releaseSlots(available - len(claims))
	for i, claim := range claims {
		select {
		case <-r.ctx.Done():
			r.releaseSlots(len(claims) - i)
			return
		case r.jobs <- claim:
		}
	}
}

func (r *TaskRuntime) worker() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case task := <-r.jobs:
			if task == nil {
				r.releaseSlots(1)
				continue
			}
			taskCtx, cancel := context.WithCancel(r.ctx)
			r.db.registerTaskCancel(task.ID, cancel)
			r.mu.Lock()
			r.running[task.ID] = cancel
			r.mu.Unlock()
			err := r.db.executeClaimedTask(taskCtx, task, r.config.ACL, r.config.Audit, r.config.Limits, r.config.Now)
			cancel()
			r.db.unregisterTaskCancel(task.ID)
			r.mu.Lock()
			delete(r.running, task.ID)
			r.mu.Unlock()
			r.releaseSlots(1)
			if err != nil && !nerr.HasCode(err, nerr.Canceled) && !nerr.HasCode(err, nerr.Unavailable) {
				r.report(err)
			}
		}
	}
}

func (r *TaskRuntime) releaseSlots(n int) {
	for i := 0; i < n; i++ {
		r.slots <- struct{}{}
	}
}

func (r *TaskRuntime) report(err error) {
	if err != nil && r.config.OnError != nil {
		r.config.OnError(err)
	}
}
