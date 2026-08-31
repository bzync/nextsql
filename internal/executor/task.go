package executor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
)

const (
	maxDispatchBatch     = 256
	defaultTaskAttempts  = 3
	defaultTaskTimeout   = 30 * time.Second
	defaultTaskBackoff   = time.Second
	defaultTaskRetention = 7 * 24 * time.Hour
	maxTaskScanFactor    = 16
)

var errTaskScanLimit = errors.New("task scan limit reached")

func scheduledTaskID(scheduleID uint32, dueNS int64) string {
	return fmt.Sprintf("s/%08x/%019d", scheduleID, dueNS)
}

// DispatchDueSchedules atomically creates at most limit durable tasks and
// advances their schedule cursors. It is leader-gated and performs no workflow
// execution; the bounded TASK worker runtime consumes the resulting records.
func (db *DB) DispatchDueSchedules(ctx context.Context, now time.Time, limit int) (int, error) {
	if db == nil {
		return 0, nerr.New(nerr.InvalidArgument, "executor.DispatchDueSchedules", "nil database")
	}
	if limit <= 0 || limit > maxDispatchBatch {
		return 0, nerr.New(nerr.InvalidArgument, "executor.DispatchDueSchedules", "dispatch limit must be between 1 and 256")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, nerr.New(nerr.Canceled, "executor.DispatchDueSchedules", "dispatch cancelled")
	}
	if db.gate != nil {
		if err := db.gate.AllowWrite(); err != nil {
			return 0, err
		}
	}
	nowNS := now.UTC().UnixNano()
	if nowNS <= 0 {
		return 0, nerr.New(nerr.InvalidArgument, "executor.DispatchDueSchedules", "invalid dispatch time")
	}
	s := db.Session()
	if err := s.start(txn.SnapshotIsolation); err != nil {
		return 0, err
	}
	tx := s.x.use(db.CatTree)
	type dueSchedule struct {
		key      []byte
		schedule *catalog.Schedule
	}
	candidates := make([]dueSchedule, 0, limit)
	err := tx.Range(catalog.ScheduleDueKey(0, 1), catalog.ScheduleDueRangeEnd(nowNS), func(key, value []byte) error {
		if len(candidates) >= limit {
			return errTaskScanLimit
		}
		dueNS, stableID, err := catalog.ParseScheduleDueKey(key)
		if err != nil || len(value) == 0 {
			return nerr.New(nerr.InvalidFormat, "executor.DispatchDueSchedules", "invalid schedule due index")
		}
		raw, err := tx.Lookup(catalog.ScheduleKey(string(value)))
		if err != nil {
			return nerr.New(nerr.InvalidFormat, "executor.DispatchDueSchedules", "dangling schedule due index")
		}
		schedule, err := catalog.DecodeSchedule(raw)
		if err != nil || schedule.Name != string(value) || schedule.ID != stableID || !schedule.Enabled || schedule.NextFireNS != dueNS {
			return nerr.New(nerr.InvalidFormat, "executor.DispatchDueSchedules", "schedule due index mismatch")
		}
		candidates = append(candidates, dueSchedule{key: append([]byte(nil), key...), schedule: schedule})
		return nil
	})
	if err != nil && !errors.Is(err, errTaskScanLimit) {
		_ = s.abort()
		return 0, err
	}
	if len(candidates) == 0 {
		_ = s.abort()
		return 0, nil
	}
	created := 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			_ = s.abort()
			return 0, nerr.New(nerr.Canceled, "executor.DispatchDueSchedules", "dispatch cancelled")
		}
		current := candidate.schedule
		dueNS := current.NextFireNS
		id := scheduledTaskID(current.ID, dueNS)
		task := &catalog.Task{
			ID: id, State: catalog.TaskPending, Source: catalog.TaskSourceSchedule,
			Owner: current.Owner, Tenant: current.Tenant, WorkflowID: current.WorkflowID, Workflow: current.Workflow,
			Args: cloneScheduleArgs(current.Args), ScheduleID: current.ID, Schedule: current.Name,
			DueNS: dueNS, CreatedNS: nowNS, UpdatedNS: nowNS, MaxAttempts: defaultTaskAttempts,
			TimeoutNS: int64(defaultTaskTimeout), RetryBackoffNS: int64(defaultTaskBackoff),
			IdempotencyKey: id, Concurrency: catalog.TaskConcurrencyForbid,
			RetentionUntilNS: nowNS + int64(defaultTaskRetention),
		}
		activeKey := catalog.TaskActiveKey(task.Source, task.ScheduleID)
		activeID, activeErr := tx.Lookup(activeKey)
		if activeErr != nil && !nerr.HasCode(activeErr, nerr.NotFound) {
			_ = s.abort()
			return 0, activeErr
		}
		if activeErr == nil {
			activeTask, err := lookupTaskTxn(tx, string(activeID))
			if err != nil || activeTask.Source != task.Source || activeTask.ScheduleID != task.ScheduleID || activeTask.Concurrency != catalog.TaskConcurrencyForbid || taskTerminal(activeTask.State) {
				_ = s.abort()
				return 0, nerr.New(nerr.InvalidFormat, "executor.DispatchDueSchedules", "task active index mismatch")
			}
		}
		suppressed := activeErr == nil && string(activeID) != id
		if !suppressed {
			if existingRaw, err := tx.Lookup(catalog.TaskKey(id)); err == nil {
				existing, decodeErr := catalog.DecodeTask(existingRaw)
				if decodeErr != nil || existing.IdempotencyKey != id || existing.ScheduleID != current.ID || existing.DueNS != dueNS {
					_ = s.abort()
					return 0, nerr.New(nerr.InvalidFormat, "executor.DispatchDueSchedules", "task idempotency collision")
				}
				if nerr.HasCode(activeErr, nerr.NotFound) {
					_ = s.abort()
					return 0, nerr.New(nerr.InvalidFormat, "executor.DispatchDueSchedules", "scheduled task active index missing")
				}
			} else if !nerr.HasCode(err, nerr.NotFound) {
				_ = s.abort()
				return 0, err
			} else {
				raw, err := catalog.EncodeTask(task)
				if err != nil {
					_ = s.abort()
					return 0, err
				}
				for _, entry := range []struct {
					key, value []byte
				}{
					{catalog.TaskKey(id), raw},
					{catalog.TaskDueKey(task.DueNS, task.ID), []byte(task.ID)},
					{catalog.TaskWorkflowKey(task.WorkflowID, task.ID), []byte(task.ID)},
					{catalog.TaskOwnerKey(task.Owner, task.ID), []byte(task.ID)},
					{activeKey, []byte(task.ID)},
				} {
					if err := tx.Insert(entry.key, entry.value); err != nil {
						_ = s.abort()
						return 0, err
					}
				}
				created++
			}
		}
		if err := tx.Delete(candidate.key); err != nil {
			_ = s.abort()
			return 0, err
		}
		current.LastFireNS = dueNS
		switch current.Kind {
		case ast.ScheduleAt:
			current.Enabled = false
			current.NextFireNS = 0
		case ast.ScheduleEvery:
			steps := (nowNS-dueNS)/current.SpecNS + 1
			if steps <= 0 || steps > (math.MaxInt64-dueNS)/current.SpecNS {
				_ = s.abort()
				return 0, nerr.New(nerr.Exhausted, "executor.DispatchDueSchedules", "schedule cursor overflow")
			}
			current.NextFireNS = dueNS + steps*current.SpecNS
		default:
			_ = s.abort()
			return 0, nerr.New(nerr.InvalidFormat, "executor.DispatchDueSchedules", "invalid schedule kind")
		}
		raw, err := catalog.EncodeSchedule(current)
		if err != nil {
			_ = s.abort()
			return 0, err
		}
		if err := tx.Update(catalog.ScheduleKey(current.Name), raw); err != nil {
			_ = s.abort()
			return 0, err
		}
		if current.Enabled {
			if err := tx.Insert(catalog.ScheduleDueKey(current.NextFireNS, current.ID), []byte(current.Name)); err != nil {
				_ = s.abort()
				return 0, err
			}
		}
		s.scheduleOverlay[current.Name] = current.Clone()
	}
	if _, err := s.commit(); err != nil {
		return 0, err
	}
	return created, nil
}

type taskDueEntry struct {
	key  []byte
	id   string
	due  int64
	task *catalog.Task
}

// ClaimDueTasks durably leases at most limit runnable tasks. The due index
// bounds work independently of retained task history. A RUNNING task is
// indexed at its lease deadline so a new leader can recover it after expiry.
func (db *DB) ClaimDueTasks(ctx context.Context, now time.Time, limit int) ([]*catalog.Task, error) {
	if db == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.ClaimDueTasks", "nil database")
	}
	if limit <= 0 || limit > maxDispatchBatch {
		return nil, nerr.New(nerr.InvalidArgument, "executor.ClaimDueTasks", "claim limit must be between 1 and 256")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nerr.New(nerr.Canceled, "executor.ClaimDueTasks", "claim cancelled")
	}
	if db.gate != nil {
		if err := db.gate.AllowWrite(); err != nil {
			return nil, err
		}
	}
	nowNS := now.UTC().UnixNano()
	if nowNS <= 0 {
		return nil, nerr.New(nerr.InvalidArgument, "executor.ClaimDueTasks", "invalid claim time")
	}
	s := db.Session()
	if err := s.start(txn.SnapshotIsolation); err != nil {
		return nil, err
	}
	tx := s.x.use(db.CatTree)
	scanLimit := limit * maxTaskScanFactor
	if scanLimit > maxDispatchBatch*maxTaskScanFactor {
		scanLimit = maxDispatchBatch * maxTaskScanFactor
	}
	entries := make([]taskDueEntry, 0, limit)
	err := tx.Range(catalog.TaskDueKey(0, ""), catalog.TaskDueRangeEnd(nowNS), func(key, value []byte) error {
		if len(entries) >= scanLimit {
			return errTaskScanLimit
		}
		due, id, err := catalog.ParseTaskDueKey(key)
		if err != nil || id == "" || string(value) != id {
			return nerr.New(nerr.InvalidFormat, "executor.ClaimDueTasks", "invalid task due index")
		}
		raw, err := tx.Lookup(catalog.TaskKey(id))
		if err != nil {
			return nerr.New(nerr.InvalidFormat, "executor.ClaimDueTasks", "dangling task due index")
		}
		task, err := catalog.DecodeTask(raw)
		if err != nil || task.ID != id || task.DueNS != due {
			return nerr.New(nerr.InvalidFormat, "executor.ClaimDueTasks", "task due index mismatch")
		}
		if task.State != catalog.TaskPending && task.State != catalog.TaskRetrying && !(task.State == catalog.TaskRunning && task.LeaseUntilNS == due) {
			return nerr.New(nerr.InvalidFormat, "executor.ClaimDueTasks", "non-runnable task has due index")
		}
		entries = append(entries, taskDueEntry{key: append([]byte(nil), key...), id: id, due: due, task: task})
		return nil
	})
	if err != nil && !errors.Is(err, errTaskScanLimit) {
		_ = s.abort()
		return nil, err
	}
	claimed := make([]*catalog.Task, 0, limit)
	for _, entry := range entries {
		if len(claimed) >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			_ = s.abort()
			return nil, nerr.New(nerr.Canceled, "executor.ClaimDueTasks", "claim cancelled")
		}
		task := entry.task
		if task.CancelRequested {
			if err := terminalizeTask(tx, task, entry.key, nowNS, catalog.TaskCancelled, "canceled", "task cancellation requested"); err != nil {
				_ = s.abort()
				return nil, err
			}
			continue
		}
		activeKey, err := taskActiveKey(task)
		if err != nil {
			_ = s.abort()
			return nil, err
		}
		if task.Concurrency == catalog.TaskConcurrencyForbid {
			activeID, lookupErr := tx.Lookup(activeKey)
			if lookupErr != nil || string(activeID) != task.ID {
				_ = s.abort()
				return nil, nerr.New(nerr.InvalidFormat, "executor.ClaimDueTasks", "task active index mismatch")
			}
		}
		if task.Attempt >= task.MaxAttempts {
			if err := terminalizeTask(tx, task, entry.key, nowNS, catalog.TaskFinalFailed, "attempts_exhausted", "task attempts exhausted"); err != nil {
				_ = s.abort()
				return nil, err
			}
			continue
		}
		if err := tx.Delete(entry.key); err != nil {
			_ = s.abort()
			return nil, err
		}
		task.State = catalog.TaskRunning
		task.Attempt++
		task.UpdatedNS = nowNS
		task.LeaseUntilNS = saturatingAddNS(nowNS, task.TimeoutNS)
		task.DueNS = task.LeaseUntilNS
		task.ErrorCode = ""
		task.ErrorMessage = ""
		raw, err := catalog.EncodeTask(task)
		if err != nil {
			_ = s.abort()
			return nil, err
		}
		if err := tx.Update(catalog.TaskKey(task.ID), raw); err != nil {
			_ = s.abort()
			return nil, err
		}
		if err := tx.Insert(catalog.TaskDueKey(task.DueNS, task.ID), []byte(task.ID)); err != nil {
			_ = s.abort()
			return nil, err
		}
		claimed = append(claimed, task.Clone())
	}
	if _, err := s.commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

// ExecuteClaimedTask runs one leased workflow as its stored owner.
// Workflow effects and SUCCEEDED are committed atomically. Failure metadata is
// persisted only after the body transaction has rolled back.
func (db *DB) ExecuteClaimedTask(ctx context.Context, claimed *catalog.Task, acl *security.ACL, audit *security.Log, limits scheduler.Limits) error {
	return db.executeClaimedTask(ctx, claimed, acl, audit, limits, func() time.Time { return time.Now().UTC() })
}

func (db *DB) executeClaimedTask(ctx context.Context, claimed *catalog.Task, acl *security.ACL, audit *security.Log, limits scheduler.Limits, now func() time.Time) error {
	if db == nil || claimed == nil || claimed.ID == "" || claimed.Attempt == 0 {
		return nerr.New(nerr.InvalidArgument, "executor.ExecuteClaimedTask", "invalid task claim")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if db.gate != nil {
		if err := db.gate.AllowWrite(); err != nil {
			return err
		}
	}
	s := db.Session()
	s.SetIdentity(claimed.Owner)
	s.SetACL(acl)
	s.SetAudit(audit)
	if limits.Workers == 0 && limits.Memory == 0 {
		limits = scheduler.DefaultLimits()
	}
	timeout := time.Duration(claimed.TimeoutNS)
	if limits.Time <= 0 || limits.Time > timeout {
		limits.Time = timeout
	}
	s.SetLimits(limits)
	s.qbudget = scheduler.NewBudget(ctx, limits)
	defer func() {
		s.qbudget.Close()
		s.qbudget = nil
	}()
	if err := s.start(txn.SnapshotIsolation); err != nil {
		return err
	}
	tx := s.x.use(db.CatTree)
	current, err := lookupTaskTxn(tx, claimed.ID)
	if err != nil {
		_ = s.abort()
		return err
	}
	if current.State != catalog.TaskRunning || current.Attempt != claimed.Attempt || current.LeaseUntilNS != claimed.LeaseUntilNS || now().UTC().UnixNano() >= current.LeaseUntilNS {
		_ = s.abort()
		return nerr.New(nerr.Conflict, "executor.ExecuteClaimedTask", "task lease is no longer current")
	}
	if current.CancelRequested {
		_ = s.abort()
		return db.persistTaskFailureAt(current, nerr.New(nerr.Canceled, "executor.ExecuteClaimedTask", "task cancellation requested"), now())
	}
	wraw, err := tx.Lookup(catalog.WorkflowKey(current.Workflow))
	if err != nil {
		_ = s.abort()
		return db.persistTaskFailureAt(current, nerr.New(nerr.NotFound, "executor.ExecuteClaimedTask", "workflow not found"), now())
	}
	workflow, err := catalog.DecodeWorkflow(wraw)
	if err != nil || workflow.ID != current.WorkflowID {
		_ = s.abort()
		return db.persistTaskFailureAt(current, nerr.New(nerr.InvalidFormat, "executor.ExecuteClaimedTask", "workflow identity mismatch"), now())
	}
	if err := s.authorize(ast.RunWorkflow{Name: workflow.Name}); err != nil {
		_ = s.abort()
		return db.persistTaskFailureAt(current, err, now())
	}
	_, runErr := s.execRunWorkflow(planner.RunWorkflow{Workflow: workflow, Args: cloneScheduleArgs(current.Args)})
	if runErr == nil {
		current.State = catalog.TaskSucceeded
		current.UpdatedNS = now().UTC().UnixNano()
		current.DueNS = 0
		current.LeaseUntilNS = 0
		current.CancelRequested = false
		current.ErrorCode = ""
		current.ErrorMessage = ""
		current.RetentionUntilNS = saturatingAddNS(current.UpdatedNS, int64(defaultTaskRetention))
		raw, encodeErr := catalog.EncodeTask(current)
		if encodeErr == nil {
			encodeErr = tx.Update(catalog.TaskKey(current.ID), raw)
		}
		if encodeErr == nil {
			encodeErr = tx.Delete(catalog.TaskDueKey(claimed.LeaseUntilNS, current.ID))
		}
		if encodeErr == nil && current.Concurrency == catalog.TaskConcurrencyForbid {
			encodeErr = deleteTaskActiveReservation(tx, current)
		}
		if encodeErr == nil {
			encodeErr = tx.Delete(catalog.TaskWorkflowKey(current.WorkflowID, current.ID))
		}
		if encodeErr == nil {
			encodeErr = tx.Insert(catalog.TaskRetentionKey(current.RetentionUntilNS, current.ID), []byte(current.ID))
		}
		if encodeErr != nil {
			_ = s.abort()
			runErr = encodeErr
		} else if _, commitErr := s.commit(); commitErr != nil {
			runErr = commitErr
		}
	} else {
		_ = s.abort()
	}
	s.auditRecord(security.ActionWorkflowRun, current.Workflow, runErr)
	if runErr != nil {
		return db.persistTaskFailureAt(current, runErr, now())
	}
	return nil
}

// RequestTaskCancellation durably cancels queued work or marks an active
// lease for cancellation. A concurrent worker must update the same task row in
// its body transaction, so the cancellation write fences a late success.
func (db *DB) RequestTaskCancellation(ctx context.Context, id string, now time.Time) (*catalog.Task, error) {
	if db == nil || id == "" || len(id) > catalog.MaxTaskIDBytes {
		return nil, nerr.New(nerr.InvalidArgument, "executor.RequestTaskCancellation", "invalid task id")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nerr.New(nerr.Canceled, "executor.RequestTaskCancellation", "cancellation request cancelled")
	}
	if db.gate != nil {
		if err := db.gate.AllowWrite(); err != nil {
			return nil, err
		}
	}
	nowNS := now.UTC().UnixNano()
	if nowNS <= 0 {
		return nil, nerr.New(nerr.InvalidArgument, "executor.RequestTaskCancellation", "invalid cancellation time")
	}
	s := db.Session()
	if err := s.start(txn.SnapshotIsolation); err != nil {
		return nil, err
	}
	tx := s.x.use(db.CatTree)
	task, changed, err := requestTaskCancellationTxn(tx, id, nowNS)
	if err != nil {
		_ = s.abort()
		return nil, err
	}
	if !changed {
		_ = s.abort()
		return task, nil
	}
	if _, err := s.commit(); err != nil {
		return nil, err
	}
	db.signalTaskCancellation(id)
	return task.Clone(), nil
}

func requestTaskCancellationTxn(tx *btree.Txn, id string, nowNS int64) (*catalog.Task, bool, error) {
	task, err := lookupTaskTxn(tx, id)
	if err != nil {
		return nil, false, err
	}
	switch task.State {
	case catalog.TaskPending, catalog.TaskRetrying:
		task.CancelRequested = true
		if err := terminalizeTask(tx, task, catalog.TaskDueKey(task.DueNS, task.ID), nowNS, catalog.TaskCancelled, "canceled", "task cancellation requested"); err != nil {
			return nil, false, err
		}
	case catalog.TaskRunning:
		task.CancelRequested = true
		if nowNS >= task.LeaseUntilNS {
			if err := terminalizeTask(tx, task, catalog.TaskDueKey(task.DueNS, task.ID), nowNS, catalog.TaskCancelled, "canceled", "task cancellation requested"); err != nil {
				return nil, false, err
			}
			break
		}
		task.UpdatedNS = nowNS
		if task.RetentionUntilNS < nowNS {
			task.RetentionUntilNS = saturatingAddNS(nowNS, int64(defaultTaskRetention))
		}
		raw, err := catalog.EncodeTask(task)
		if err == nil {
			err = tx.Update(catalog.TaskKey(task.ID), raw)
		}
		if err != nil {
			return nil, false, err
		}
	case catalog.TaskSucceeded, catalog.TaskFailed, catalog.TaskCancelled, catalog.TaskFinalFailed:
		return task, false, nil
	default:
		return nil, false, nerr.New(nerr.InvalidFormat, "executor.RequestTaskCancellation", "invalid task state")
	}
	return task, true, nil
}

// PurgeTaskHistory deletes at most limit expired terminal task descriptors.
// The retention index keeps cleanup bounded regardless of history size.
func (db *DB) PurgeTaskHistory(ctx context.Context, now time.Time, limit int) (int, error) {
	if db == nil || limit <= 0 || limit > maxDispatchBatch {
		return 0, nerr.New(nerr.InvalidArgument, "executor.PurgeTaskHistory", "purge limit must be between 1 and 256")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, nerr.New(nerr.Canceled, "executor.PurgeTaskHistory", "purge cancelled")
	}
	if db.gate != nil {
		if err := db.gate.AllowWrite(); err != nil {
			return 0, err
		}
	}
	nowNS := now.UTC().UnixNano()
	if nowNS <= 0 {
		return 0, nerr.New(nerr.InvalidArgument, "executor.PurgeTaskHistory", "invalid purge time")
	}
	s := db.Session()
	if err := s.start(txn.SnapshotIsolation); err != nil {
		return 0, err
	}
	tx := s.x.use(db.CatTree)
	entries := make([]taskDueEntry, 0, limit)
	err := tx.Range(catalog.TaskRetentionKey(0, ""), catalog.TaskRetentionRangeEnd(nowNS), func(key, value []byte) error {
		if len(entries) >= limit {
			return errTaskScanLimit
		}
		until, id, err := catalog.ParseTaskRetentionKey(key)
		if err != nil || id == "" || string(value) != id {
			return nerr.New(nerr.InvalidFormat, "executor.PurgeTaskHistory", "invalid task retention index")
		}
		task, err := lookupTaskTxn(tx, id)
		if err != nil || task.RetentionUntilNS != until || !taskTerminal(task.State) {
			return nerr.New(nerr.InvalidFormat, "executor.PurgeTaskHistory", "task retention index mismatch")
		}
		entries = append(entries, taskDueEntry{key: append([]byte(nil), key...), id: id, task: task})
		return nil
	})
	if err != nil && !errors.Is(err, errTaskScanLimit) {
		_ = s.abort()
		return 0, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			_ = s.abort()
			return 0, nerr.New(nerr.Canceled, "executor.PurgeTaskHistory", "purge cancelled")
		}
		if err := tx.Delete(entry.key); err != nil {
			_ = s.abort()
			return 0, err
		}
		if err := tx.Delete(catalog.TaskKey(entry.id)); err != nil {
			_ = s.abort()
			return 0, err
		}
		if err := tx.Delete(catalog.TaskOwnerKey(entry.task.Owner, entry.id)); err != nil {
			_ = s.abort()
			return 0, err
		}
	}
	if _, err := s.commit(); err != nil {
		return 0, err
	}
	return len(entries), nil
}

func (db *DB) persistTaskFailure(claimed *catalog.Task, runErr error) error {
	return db.persistTaskFailureAt(claimed, runErr, time.Now().UTC())
}

func (db *DB) persistTaskFailureAt(claimed *catalog.Task, runErr error, now time.Time) error {
	if err := db.finishTaskFailure(context.Background(), claimed, now, runErr); err != nil {
		return err
	}
	return runErr
}

func (db *DB) finishTaskFailure(ctx context.Context, claimed *catalog.Task, now time.Time, runErr error) error {
	if db == nil || claimed == nil {
		return nerr.New(nerr.InvalidArgument, "executor.finishTaskFailure", "invalid task claim")
	}
	if db.gate != nil {
		if err := db.gate.AllowWrite(); err != nil {
			return err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nerr.New(nerr.Canceled, "executor.finishTaskFailure", "task update cancelled")
	}
	nowNS := now.UTC().UnixNano()
	s := db.Session()
	if err := s.start(txn.SnapshotIsolation); err != nil {
		return err
	}
	tx := s.x.use(db.CatTree)
	current, err := lookupTaskTxn(tx, claimed.ID)
	if err != nil {
		_ = s.abort()
		return err
	}
	if current.State != catalog.TaskRunning || current.Attempt != claimed.Attempt || current.LeaseUntilNS != claimed.LeaseUntilNS {
		_ = s.abort()
		if current.State == catalog.TaskCancelled || current.State == catalog.TaskSucceeded || current.State == catalog.TaskFinalFailed {
			return nil
		}
		return nerr.New(nerr.Conflict, "executor.finishTaskFailure", "task lease is no longer current")
	}
	if err := tx.Delete(catalog.TaskDueKey(current.LeaseUntilNS, current.ID)); err != nil {
		_ = s.abort()
		return err
	}
	current.UpdatedNS = nowNS
	current.LeaseUntilNS = 0
	current.RetentionUntilNS = saturatingAddNS(nowNS, int64(defaultTaskRetention))
	current.ErrorCode, current.ErrorMessage = safeTaskError(runErr)
	nonRetryable := nerr.HasCode(runErr, nerr.Corruption) || nerr.HasCode(runErr, nerr.InvalidFormat)
	terminal := current.CancelRequested || nonRetryable || current.Attempt >= current.MaxAttempts
	if current.CancelRequested {
		current.State = catalog.TaskCancelled
		current.DueNS = 0
		current.ErrorCode = "canceled"
		current.ErrorMessage = "task cancellation requested"
	} else if nonRetryable {
		current.State = catalog.TaskFailed
		current.DueNS = 0
	} else if current.Attempt >= current.MaxAttempts {
		current.State = catalog.TaskFinalFailed
		current.DueNS = 0
	} else {
		current.State = catalog.TaskRetrying
		current.DueNS = saturatingAddNS(nowNS, retryDelayNS(current))
	}
	if terminal && current.Concurrency == catalog.TaskConcurrencyForbid {
		if err := deleteTaskActiveReservation(tx, current); err != nil {
			_ = s.abort()
			return err
		}
	}
	raw, err := catalog.EncodeTask(current)
	if err == nil {
		err = tx.Update(catalog.TaskKey(current.ID), raw)
	}
	if err == nil {
		if terminal {
			err = tx.Delete(catalog.TaskWorkflowKey(current.WorkflowID, current.ID))
			if err == nil {
				err = tx.Insert(catalog.TaskRetentionKey(current.RetentionUntilNS, current.ID), []byte(current.ID))
			}
		} else {
			err = tx.Insert(catalog.TaskDueKey(current.DueNS, current.ID), []byte(current.ID))
		}
	}
	if err != nil {
		_ = s.abort()
		return err
	}
	_, err = s.commit()
	return err
}

func lookupTaskTxn(tx *btree.Txn, id string) (*catalog.Task, error) {
	raw, err := tx.Lookup(catalog.TaskKey(id))
	if err != nil {
		return nil, err
	}
	task, err := catalog.DecodeTask(raw)
	if err != nil {
		return nil, err
	}
	if task.ID != id {
		return nil, nerr.New(nerr.InvalidFormat, "executor.lookupTaskTxn", "task key identity mismatch")
	}
	return task, nil
}

func taskActiveKey(task *catalog.Task) ([]byte, error) {
	if task == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.taskActiveKey", "nil task")
	}
	var stableID uint32
	switch task.Source {
	case catalog.TaskSourceSchedule:
		stableID = task.ScheduleID
	case catalog.TaskSourceTrigger:
		stableID = task.TriggerID
	case catalog.TaskSourceManual:
		stableID = task.WorkflowID
	}
	if stableID == 0 {
		return nil, nerr.New(nerr.InvalidFormat, "executor.taskActiveKey", "task concurrency identity missing")
	}
	return catalog.TaskActiveKey(task.Source, stableID), nil
}

func deleteTaskActiveReservation(tx *btree.Txn, task *catalog.Task) error {
	activeKey, err := taskActiveKey(task)
	if err != nil {
		return err
	}
	activeID, err := tx.Lookup(activeKey)
	if err != nil || string(activeID) != task.ID {
		return nerr.New(nerr.InvalidFormat, "executor.taskActiveReservation", "task active index mismatch")
	}
	return tx.Delete(activeKey)
}

func terminalizeTask(tx *btree.Txn, task *catalog.Task, dueKey []byte, nowNS int64, state catalog.TaskState, code, message string) error {
	if err := tx.Delete(dueKey); err != nil {
		return err
	}
	task.State = state
	task.DueNS = 0
	task.UpdatedNS = nowNS
	task.LeaseUntilNS = 0
	task.RetentionUntilNS = saturatingAddNS(nowNS, int64(defaultTaskRetention))
	task.ErrorCode = code
	task.ErrorMessage = message
	if task.Concurrency == catalog.TaskConcurrencyForbid {
		if err := deleteTaskActiveReservation(tx, task); err != nil {
			return err
		}
	}
	raw, err := catalog.EncodeTask(task)
	if err == nil {
		err = tx.Update(catalog.TaskKey(task.ID), raw)
	}
	if err == nil {
		err = tx.Delete(catalog.TaskWorkflowKey(task.WorkflowID, task.ID))
	}
	if err == nil {
		err = tx.Insert(catalog.TaskRetentionKey(task.RetentionUntilNS, task.ID), []byte(task.ID))
	}
	return err
}

func retryDelayNS(task *catalog.Task) int64 {
	if task == nil || task.RetryBackoffNS <= 0 {
		return 0
	}
	shift := task.Attempt - 1
	if shift > 30 {
		shift = 30
	}
	if task.RetryBackoffNS > math.MaxInt64>>shift {
		return catalog.MaxTaskDurationNS
	}
	delay := task.RetryBackoffNS << shift
	if delay > catalog.MaxTaskDurationNS {
		return catalog.MaxTaskDurationNS
	}
	return delay
}

func saturatingAddNS(base, delta int64) int64 {
	if delta > 0 && base > math.MaxInt64-delta {
		return math.MaxInt64
	}
	return base + delta
}

func safeTaskError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	var typed *nerr.Error
	if errors.As(err, &typed) {
		return string(typed.Code), "workflow execution failed: " + string(typed.Code)
	}
	return string(nerr.Internal), "workflow execution failed"
}

func taskTerminal(state catalog.TaskState) bool {
	switch state {
	case catalog.TaskSucceeded, catalog.TaskFailed, catalog.TaskCancelled, catalog.TaskFinalFailed:
		return true
	default:
		return false
	}
}

func (s *Session) execShowTasks(p planner.ShowTasks) (*Result, error) {
	if s == nil || s.x == nil || p.Limit < 1 || p.Limit > maxDispatchBatch {
		return nil, nerr.New(nerr.InvalidArgument, "executor.ShowTasks", "active transaction and bounded limit are required")
	}
	tx := s.x.use(s.db.CatTree)
	admin := s.acl == nil || s.isAdmin()
	var start, end []byte
	if admin {
		start = catalog.TaskKey(p.After)
		end = []byte{catalog.KeyTask + 1}
	} else {
		start, end = catalog.TaskOwnerRange(s.user)
		if p.After != "" {
			start = catalog.TaskOwnerKey(s.user, p.After)
		}
	}
	tasks := make([]*catalog.Task, 0, p.Limit)
	err := tx.Range(start, end, func(key, value []byte) error {
		if len(tasks) >= p.Limit {
			return errTaskScanLimit
		}
		var id string
		var raw []byte
		if admin {
			if len(key) < 2 || key[0] != catalog.KeyTask {
				return nerr.New(nerr.InvalidFormat, "executor.ShowTasks", "invalid task key")
			}
			id, raw = string(key[1:]), value
		} else {
			id = string(value)
			var err error
			raw, err = tx.Lookup(catalog.TaskKey(id))
			if err != nil {
				return nerr.New(nerr.InvalidFormat, "executor.ShowTasks", "dangling task owner index")
			}
		}
		if p.After != "" && id <= p.After {
			return nil
		}
		task, err := catalog.DecodeTask(raw)
		if err != nil || task.ID != id || (!admin && task.Owner != s.user) {
			return nerr.New(nerr.InvalidFormat, "executor.ShowTasks", "task index identity mismatch")
		}
		tasks = append(tasks, task)
		return nil
	})
	if err != nil && !errors.Is(err, errTaskScanLimit) {
		return nil, err
	}
	result := &Result{Columns: []string{"task_id", "state", "source", "workflow", "schedule", "owner", "tenant", "attempt", "max_attempts", "due_at", "updated_at", "error_code", "error_message", "cancel_requested"}}
	for _, task := range tasks {
		due := types.Null(types.TimestampTZ())
		if task.DueNS > 0 {
			due = types.TimeValue(task.DueNS)
		}
		result.Rows = append(result.Rows, []types.Value{
			types.StringValue(task.ID), types.StringValue(taskStateName(task.State)), types.StringValue(taskSourceName(task.Source)),
			types.StringValue(task.Workflow), types.StringValue(task.Schedule), types.StringValue(task.Owner), types.StringValue(task.Tenant),
			decimalIntValue(int64(task.Attempt)), decimalIntValue(int64(task.MaxAttempts)), due, types.TimeValue(task.UpdatedNS),
			types.StringValue(task.ErrorCode), types.StringValue(task.ErrorMessage), types.BoolValue(task.CancelRequested),
		})
	}
	return result, nil
}

func (s *Session) execCancelTask(p planner.CancelTask) (*Result, error) {
	if s == nil || s.x == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CancelTask", "active transaction required")
	}
	tx := s.x.use(s.db.CatTree)
	task, err := lookupTaskTxn(tx, p.ID)
	if err != nil {
		return nil, err
	}
	if s.acl != nil && !s.isAdmin() && task.Owner != s.user {
		return nil, security.Deny("executor.CancelTask")
	}
	task, changed, err := requestTaskCancellationTxn(tx, p.ID, time.Now().UTC().UnixNano())
	if err != nil {
		return nil, err
	}
	if !changed {
		return &Result{}, nil
	}
	s.pending.taskCancels = append(s.pending.taskCancels, task.ID)
	return &Result{Affected: 1}, nil
}

func taskStateName(state catalog.TaskState) string {
	switch state {
	case catalog.TaskPending:
		return "PENDING"
	case catalog.TaskRunning:
		return "RUNNING"
	case catalog.TaskSucceeded:
		return "SUCCEEDED"
	case catalog.TaskFailed:
		return "FAILED"
	case catalog.TaskCancelled:
		return "CANCELLED"
	case catalog.TaskRetrying:
		return "RETRYING"
	case catalog.TaskFinalFailed:
		return "FINAL_FAILED"
	default:
		return "UNKNOWN"
	}
}

func taskSourceName(source catalog.TaskSource) string {
	switch source {
	case catalog.TaskSourceManual:
		return "MANUAL"
	case catalog.TaskSourceTrigger:
		return "TRIGGER"
	case catalog.TaskSourceSchedule:
		return "SCHEDULE"
	default:
		return "UNKNOWN"
	}
}

func cloneScheduleArgs(args []ast.Expr) []ast.Expr {
	out := make([]ast.Expr, len(args))
	copy(out, args)
	return out
}

func (db *DB) task(id string) (*catalog.Task, bool, error) {
	if db == nil {
		return nil, false, nerr.New(nerr.InvalidArgument, "executor.task", "nil database")
	}
	db.applyMu.RLock()
	defer db.applyMu.RUnlock()
	if db.CatTree == nil {
		return nil, false, nerr.New(nerr.InvalidArgument, "executor.task", "nil database")
	}
	raw, err := db.CatTree.Lookup(catalog.TaskKey(id))
	if nerr.HasCode(err, nerr.NotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	task, err := catalog.DecodeTask(raw)
	if err != nil {
		return nil, false, err
	}
	return task, true, nil
}
