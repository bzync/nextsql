package catalog

import (
	"bytes"
	"encoding/binary"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
)

const (
	taskMagic             = "NSTK"
	taskVersion           = 1
	KeyTask          byte = 'K'
	KeyTaskDue       byte = 'L'
	KeyTaskActive    byte = 'M'
	KeyTaskRetention byte = 'N'
	KeyTaskWorkflow  byte = 'O'
	KeyTaskOwner     byte = 'P'

	MaxTaskIDBytes                = 192
	MaxTaskIdempotencyBytes       = 256
	MaxTaskErrorBytes             = 4096
	MaxTaskArgs                   = MaxWorkflowParams
	MaxTaskDescriptor             = security.MaxSQLBytes
	MaxTaskAttempts               = 100
	MaxTaskDurationNS       int64 = 24 * 60 * 60 * 1_000_000_000
)

type TaskState uint8
type TaskSource uint8
type TaskConcurrency uint8

const (
	TaskPending TaskState = iota + 1
	TaskRunning
	TaskSucceeded
	TaskFailed
	TaskCancelled
	TaskRetrying
	TaskFinalFailed
)

const (
	TaskSourceManual TaskSource = iota + 1
	TaskSourceTrigger
	TaskSourceSchedule
)

const (
	TaskConcurrencyForbid TaskConcurrency = iota + 1
	TaskConcurrencyAllow
)

// Task is the durable, versioned unit executed by the bounded P19 worker
// runtime. Time fields are Unix nanoseconds. Error fields are bounded metadata,
// never workflow arguments or row values.
type Task struct {
	ID               string
	State            TaskState
	Source           TaskSource
	Owner            string
	Tenant           string
	WorkflowID       uint32
	Workflow         string
	Args             []ast.Expr
	ScheduleID       uint32
	Schedule         string
	TriggerID        uint32
	Trigger          string
	DueNS            int64
	CreatedNS        int64
	UpdatedNS        int64
	Attempt          uint32
	MaxAttempts      uint32
	TimeoutNS        int64
	RetryBackoffNS   int64
	LeaseUntilNS     int64
	IdempotencyKey   string
	Concurrency      TaskConcurrency
	CancelRequested  bool
	ErrorCode        string
	ErrorMessage     string
	RetentionUntilNS int64
}

func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	raw, err := EncodeTask(t)
	if err != nil {
		return nil
	}
	out, err := DecodeTask(raw)
	if err != nil {
		return nil
	}
	return out
}

func TaskKey(id string) []byte {
	k := make([]byte, 1+len(id))
	k[0] = KeyTask
	copy(k[1:], id)
	return k
}

func EncodeTask(t *Task) ([]byte, error) {
	if err := validateTask(t); err != nil {
		return nil, err
	}
	buf := append([]byte(nil), taskMagic...)
	buf = appendU16(buf, taskVersion)
	buf = appendString(buf, t.ID)
	buf = append(buf, byte(t.State), byte(t.Source), byte(t.Concurrency))
	if t.CancelRequested {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	for _, value := range []string{t.Owner, t.Tenant, t.Workflow} {
		buf = appendString(buf, value)
	}
	buf = appendU32(buf, t.WorkflowID)
	buf = appendU16(buf, uint16(len(t.Args)))
	for _, arg := range t.Args {
		var err error
		buf, err = appendExpr(buf, arg)
		if err != nil {
			return nil, err
		}
	}
	buf = appendU32(buf, t.ScheduleID)
	buf = appendString(buf, t.Schedule)
	buf = appendU32(buf, t.TriggerID)
	buf = appendString(buf, t.Trigger)
	for _, value := range []int64{t.DueNS, t.CreatedNS, t.UpdatedNS, t.TimeoutNS, t.RetryBackoffNS, t.LeaseUntilNS, t.RetentionUntilNS} {
		buf = appendU64(buf, uint64(value))
	}
	buf = appendU32(buf, t.Attempt)
	buf = appendU32(buf, t.MaxAttempts)
	buf = appendString(buf, t.IdempotencyKey)
	buf = appendString(buf, t.ErrorCode)
	buf = appendString(buf, t.ErrorMessage)
	if len(buf) > MaxTaskDescriptor {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "task descriptor exceeds size limit")
	}
	return buf, nil
}

func DecodeTask(raw []byte) (*Task, error) {
	if len(raw) > MaxTaskDescriptor || len(raw) < len(taskMagic) || !bytes.Equal(raw[:len(taskMagic)], []byte(taskMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTask", "invalid task descriptor")
	}
	off := len(taskMagic)
	version, off, err := takeU16(raw, off)
	if err != nil || version != taskVersion {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTask", "unsupported task version")
	}
	t := &Task{}
	t.ID, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	if off+4 > len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTask", "truncated task flags")
	}
	t.State, t.Source, t.Concurrency = TaskState(raw[off]), TaskSource(raw[off+1]), TaskConcurrency(raw[off+2])
	if raw[off+3] > 1 {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTask", "invalid cancel flag")
	}
	t.CancelRequested = raw[off+3] == 1
	off += 4
	t.Owner, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	t.Tenant, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	t.Workflow, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	t.WorkflowID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	var n uint16
	n, off, err = takeU16(raw, off)
	if err != nil || n > MaxTaskArgs {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTask", "invalid task argument count")
	}
	for i := 0; i < int(n); i++ {
		arg, next, err := takeExpr(raw, off)
		if err != nil {
			return nil, err
		}
		t.Args = append(t.Args, arg)
		off = next
	}
	t.ScheduleID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	t.Schedule, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	t.TriggerID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	t.Trigger, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	fields := []*int64{&t.DueNS, &t.CreatedNS, &t.UpdatedNS, &t.TimeoutNS, &t.RetryBackoffNS, &t.LeaseUntilNS, &t.RetentionUntilNS}
	for _, field := range fields {
		var value uint64
		value, off, err = takeU64(raw, off)
		if err != nil {
			return nil, err
		}
		*field = int64(value)
	}
	t.Attempt, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	t.MaxAttempts, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	t.IdempotencyKey, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	t.ErrorCode, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	t.ErrorMessage, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTask", "trailing task bytes")
	}
	if err := validateTask(t); err != nil {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTask", err.Error())
	}
	return t, nil
}

func validateTask(t *Task) error {
	if t == nil || t.ID == "" || len(t.ID) > MaxTaskIDBytes || t.Owner == "" || t.WorkflowID == 0 || t.Workflow == "" || t.CreatedNS <= 0 || t.UpdatedNS < t.CreatedNS {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "invalid task identity")
	}
	if t.State < TaskPending || t.State > TaskFinalFailed || t.Source < TaskSourceManual || t.Source > TaskSourceSchedule || (t.Concurrency != TaskConcurrencyForbid && t.Concurrency != TaskConcurrencyAllow) {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "invalid task enum")
	}
	for _, value := range []string{t.Owner, t.Tenant, t.Workflow, t.Schedule, t.Trigger, t.ErrorCode} {
		if len(value) > MaxWorkflowNameBytes {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "task metadata exceeds limit")
		}
	}
	if len(t.IdempotencyKey) == 0 || len(t.IdempotencyKey) > MaxTaskIdempotencyBytes || len(t.ErrorMessage) > MaxTaskErrorBytes {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "invalid task idempotency/error metadata")
	}
	if len(t.Args) > MaxTaskArgs || t.DueNS < 0 || t.MaxAttempts == 0 || t.MaxAttempts > MaxTaskAttempts || t.Attempt > t.MaxAttempts || t.TimeoutNS <= 0 || t.TimeoutNS > MaxTaskDurationNS || t.RetryBackoffNS < 0 || t.RetryBackoffNS > MaxTaskDurationNS || t.RetentionUntilNS < t.UpdatedNS {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "invalid task limits")
	}
	if t.State == TaskRunning {
		if t.Attempt == 0 || t.LeaseUntilNS <= t.UpdatedNS {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "running task requires an active lease")
		}
	} else if t.LeaseUntilNS != 0 {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "non-running task cannot retain a lease")
	}
	for _, arg := range t.Args {
		if !catalogScheduleLiteral(arg) {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "task arguments must be literals")
		}
	}
	switch t.Source {
	case TaskSourceSchedule:
		if t.ScheduleID == 0 || t.Schedule == "" || t.TriggerID != 0 || t.Trigger != "" {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "invalid scheduled task source")
		}
	case TaskSourceTrigger:
		if t.TriggerID == 0 || t.Trigger == "" || t.ScheduleID != 0 || t.Schedule != "" {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "invalid trigger task source")
		}
	case TaskSourceManual:
		if t.ScheduleID != 0 || t.Schedule != "" || t.TriggerID != 0 || t.Trigger != "" {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeTask", "invalid manual task source")
		}
	}
	return nil
}

// TaskDueKey indexes runnable tasks by their next eligible Unix nanosecond.
// Due times are non-negative, so big-endian uint64 ordering is chronological.
func TaskDueKey(dueNS int64, id string) []byte {
	k := make([]byte, 1+8+len(id))
	k[0] = KeyTaskDue
	binary.BigEndian.PutUint64(k[1:9], uint64(dueNS))
	copy(k[9:], id)
	return k
}

func TaskDueRangeEnd(nowNS int64) []byte {
	if nowNS == int64(^uint64(0)>>1) {
		return []byte{KeyTaskDue + 1}
	}
	return TaskDueKey(nowNS+1, "")
}

func ParseTaskDueKey(key []byte) (int64, string, error) {
	if len(key) < 10 || key[0] != KeyTaskDue {
		return 0, "", nerr.New(nerr.InvalidFormat, "catalog.ParseTaskDueKey", "invalid task due key")
	}
	due := binary.BigEndian.Uint64(key[1:9])
	if due > uint64(^uint64(0)>>1) || len(key)-9 > MaxTaskIDBytes {
		return 0, "", nerr.New(nerr.InvalidFormat, "catalog.ParseTaskDueKey", "invalid task due key")
	}
	return int64(due), string(key[9:]), nil
}

// TaskActiveKey serializes FORBID tasks for one schedule. Other source types
// gain their own scope byte without changing the key family.
func TaskActiveKey(source TaskSource, stableID uint32) []byte {
	k := make([]byte, 6)
	k[0] = KeyTaskActive
	k[1] = byte(source)
	binary.BigEndian.PutUint32(k[2:], stableID)
	return k
}

func TaskRetentionKey(untilNS int64, id string) []byte {
	k := make([]byte, 1+8+len(id))
	k[0] = KeyTaskRetention
	binary.BigEndian.PutUint64(k[1:9], uint64(untilNS))
	copy(k[9:], id)
	return k
}

func TaskWorkflowKey(workflowID uint32, id string) []byte {
	k := make([]byte, 1+4+len(id))
	k[0] = KeyTaskWorkflow
	binary.BigEndian.PutUint32(k[1:5], workflowID)
	copy(k[5:], id)
	return k
}

func TaskWorkflowRange(workflowID uint32) ([]byte, []byte) {
	start := TaskWorkflowKey(workflowID, "")
	if workflowID == ^uint32(0) {
		return start, []byte{KeyTaskWorkflow + 1}
	}
	return start, TaskWorkflowKey(workflowID+1, "")
}

func TaskOwnerKey(owner, id string) []byte {
	k := make([]byte, 1+2+len(owner)+len(id))
	k[0] = KeyTaskOwner
	binary.BigEndian.PutUint16(k[1:3], uint16(len(owner)))
	copy(k[3:], owner)
	copy(k[3+len(owner):], id)
	return k
}

func TaskOwnerRange(owner string) ([]byte, []byte) {
	prefix := TaskOwnerKey(owner, "")
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] != 0xff {
			end[i]++
			return prefix, end[:i+1]
		}
	}
	return prefix, []byte{KeyTaskOwner + 1}
}

func TaskRetentionRangeEnd(nowNS int64) []byte {
	if nowNS == int64(^uint64(0)>>1) {
		return []byte{KeyTaskRetention + 1}
	}
	return TaskRetentionKey(nowNS+1, "")
}

func ParseTaskRetentionKey(key []byte) (int64, string, error) {
	if len(key) < 10 || key[0] != KeyTaskRetention {
		return 0, "", nerr.New(nerr.InvalidFormat, "catalog.ParseTaskRetentionKey", "invalid task retention key")
	}
	until := binary.BigEndian.Uint64(key[1:9])
	if until > uint64(^uint64(0)>>1) || len(key)-9 > MaxTaskIDBytes {
		return 0, "", nerr.New(nerr.InvalidFormat, "catalog.ParseTaskRetentionKey", "invalid task retention key")
	}
	return int64(until), string(key[9:]), nil
}
