package executor

import (
	"context"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// DBRef is one currently-open database, as reported by a DatabaseLister —
// e.g. dbmanager.Manager.Snapshot, bridged by the caller rather than
// imported directly: dbmanager already imports executor (for *DB), so
// executor importing dbmanager back would cycle. Release must be called
// exactly once, no earlier than every task CentralScheduler claimed from DB
// on the tick it was returned for has actually finished executing (not
// merely been submitted) — CentralScheduler itself guarantees this; a
// DatabaseLister implementation only needs to hand out a ref-paired handle
// (dbmanager.Manager.Snapshot already does, via its own Acquire/release
// refcounting) and honor exactly-once Release semantics.
type DBRef struct {
	DB      *DB
	Release func()
}

// DatabaseLister returns every currently open database as of the call,
// each already ref-held. A nil DatabaseLister is rejected by
// StartCentralScheduler.
type DatabaseLister func() []DBRef

// CentralScheduler (M2-3b-3b) replaces one TaskRuntime-per-open-database
// with a single poll loop that, each tick, asks a DatabaseLister for every
// currently open database and claims/dispatches/submits each one's due
// work to one shared TaskPool — reducing polling goroutines from O(open
// databases) (one TaskRuntime.coordinate per database) to O(1), on top of
// M2-3b-3a's already-shared execution worker pool. Intended for the
// dbmanager-routed multi-database case; a non-hosted single-database
// deployment has nothing to centralize and should keep using a plain
// TaskRuntime directly.
type CentralScheduler struct {
	pool   *TaskPool
	list   DatabaseLister
	config TaskRuntimeConfig

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// inFlight tracks the per-database completion-waiter goroutines cycle
	// spawns (see cycleOne) — Close waits it out after stopping the
	// coordinator, so it never returns while a DBRef acquired on the last
	// tick is still unreleased, the same guarantee TaskRuntime.inFlight
	// gives a single database.
	inFlight sync.WaitGroup
	once     sync.Once
}

// StartCentralScheduler validates config exactly like StartTaskRuntime
// (they share every bound) and starts polling immediately.
func StartCentralScheduler(parent context.Context, pool *TaskPool, list DatabaseLister, config TaskRuntimeConfig) (*CentralScheduler, error) {
	if pool == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartCentralScheduler", "nil task pool")
	}
	if list == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartCentralScheduler", "nil database lister")
	}
	if parent == nil {
		parent = context.Background()
	}
	if config.Batch == 0 {
		config.Batch = defaultTaskBatch
	}
	if config.Batch < 1 || config.Batch > maxDispatchBatch {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartCentralScheduler", "task batch must be between 1 and 256")
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultTaskPollInterval
	}
	if config.PollInterval < minTaskPollInterval || config.PollInterval > maxTaskPollInterval {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartCentralScheduler", "task poll interval must be between 10ms and 1m")
	}
	if config.PurgeEvery == 0 {
		config.PurgeEvery = defaultTaskPurgeEvery
	}
	if config.PurgeEvery < config.PollInterval {
		return nil, nerr.New(nerr.InvalidArgument, "executor.StartCentralScheduler", "task purge interval must not be shorter than polling")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	ctx, cancel := context.WithCancel(parent)
	s := &CentralScheduler{pool: pool, list: list, config: config, ctx: ctx, cancel: cancel}
	s.wg.Add(1)
	go s.coordinate()
	return s, nil
}

// Close stops polling and waits for every database ref acquired on the last
// tick to be released, including any still-executing task claimed from it —
// it does not stop the shared TaskPool, which may still be serving other
// submitters.
func (s *CentralScheduler) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(s.cancel)
	s.wg.Wait()
	s.inFlight.Wait()
	return nil
}

func (s *CentralScheduler) coordinate() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()
	purge := time.NewTicker(s.config.PurgeEvery)
	defer purge.Stop()
	s.cycle()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.cycle()
		case <-purge.C:
			s.purgeAll()
		}
	}
}

func (s *CentralScheduler) purgeAll() {
	for _, ref := range s.list() {
		if _, err := ref.DB.PurgeTaskHistory(s.ctx, s.config.Now(), s.config.Batch); err != nil &&
			!nerr.HasCode(err, nerr.Unavailable) && !nerr.HasCode(err, nerr.Canceled) {
			s.report(err)
		}
		ref.Release()
	}
}

func (s *CentralScheduler) cycle() {
	for _, ref := range s.list() {
		s.cycleOne(ref)
	}
}

// cycleOne claims and submits one database's due work for this tick, then
// releases ref. Every return path releases ref exactly once: synchronously
// on every early-out below (nothing was submitted, so nothing to wait for),
// or — once at least one claim was actually submitted to the pool —
// asynchronously via a tracked goroutine that waits for every submitted
// claim's own completion before releasing, so ref (and therefore the
// database behind it) can never be evicted while a job from this tick is
// still executing.
func (s *CentralScheduler) cycleOne(ref DBRef) {
	available := 0
	for available < s.config.Batch {
		select {
		case <-s.ctx.Done():
			s.pool.releaseSlots(available)
			ref.Release()
			return
		case <-s.pool.slots:
			available++
		default:
			goto claimedSlots
		}
	}

claimedSlots:
	if available == 0 {
		ref.Release()
		return
	}
	now := s.config.Now()
	claims, err := ref.DB.ClaimDueTasks(s.ctx, now, available)
	if err != nil {
		s.pool.releaseSlots(available)
		if !nerr.HasCode(err, nerr.Unavailable) && !nerr.HasCode(err, nerr.Canceled) {
			s.report(err)
		}
		ref.Release()
		return
	}
	remaining := available - len(claims)
	if remaining > 0 {
		if _, err := ref.DB.DispatchDueSchedules(s.ctx, now, remaining); err != nil {
			if !nerr.HasCode(err, nerr.Unavailable) && !nerr.HasCode(err, nerr.Canceled) {
				s.report(err)
			}
		} else {
			more, claimErr := ref.DB.ClaimDueTasks(s.ctx, now, remaining)
			if claimErr != nil {
				if !nerr.HasCode(claimErr, nerr.Unavailable) && !nerr.HasCode(claimErr, nerr.Canceled) {
					s.report(claimErr)
				}
			} else {
				claims = append(claims, more...)
			}
		}
	}
	s.pool.releaseSlots(available - len(claims))
	if len(claims) == 0 {
		ref.Release()
		return
	}

	var wg sync.WaitGroup
claimLoop:
	for i, claim := range claims {
		wg.Add(1)
		task := claim
		job := taskJob{
			ctx: s.ctx, db: ref.DB, task: task, config: s.config,
			onDone: func(err error) {
				defer wg.Done()
				if err != nil && !nerr.HasCode(err, nerr.Canceled) && !nerr.HasCode(err, nerr.Unavailable) {
					s.report(err)
				}
			},
		}
		select {
		case <-s.ctx.Done():
			wg.Done()
			s.pool.releaseSlots(len(claims) - i)
			break claimLoop
		case s.pool.jobs <- job:
		}
	}
	s.inFlight.Add(1)
	go func() {
		defer s.inFlight.Done()
		wg.Wait()
		ref.Release()
	}()
}

func (s *CentralScheduler) report(err error) {
	if err != nil && s.config.OnError != nil {
		s.config.OnError(err)
	}
}
