package catalog

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func validTask() *Task {
	return &Task{
		ID: "s/7/1787616000000000000", State: TaskPending, Source: TaskSourceSchedule,
		Owner: "scheduler", Tenant: "tenant-a", WorkflowID: 9, Workflow: "rollup",
		Args:       []ast.Expr{ast.Literal{Value: types.StringValue("hour")}},
		ScheduleID: 7, Schedule: "hourly", DueNS: 1787616000000000000,
		CreatedNS: 1787616000000000000, UpdatedNS: 1787616000000000000,
		MaxAttempts: 3, TimeoutNS: 30_000_000_000, RetryBackoffNS: 1_000_000_000,
		IdempotencyKey: "schedule/7/1787616000000000000", Concurrency: TaskConcurrencyForbid,
		RetentionUntilNS: 1788220800000000000,
	}
}

func TestTaskCodec(t *testing.T) {
	want := validTask()
	raw, err := EncodeTask(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTask(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.State != want.State || got.Source != want.Source || got.WorkflowID != want.WorkflowID || got.ScheduleID != want.ScheduleID || got.IdempotencyKey != want.IdempotencyKey || len(got.Args) != 1 {
		t.Fatalf("task=%+v", got)
	}
}

func TestTaskCodecRejectsInvalidLimits(t *testing.T) {
	for _, mutate := range []func(*Task){
		func(task *Task) { task.MaxAttempts = 0 },
		func(task *Task) { task.Attempt = task.MaxAttempts + 1 },
		func(task *Task) { task.TimeoutNS = MaxTaskDurationNS + 1 },
		func(task *Task) { task.IdempotencyKey = "" },
		func(task *Task) { task.ScheduleID = 0 },
	} {
		task := validTask()
		mutate(task)
		if _, err := EncodeTask(task); err == nil {
			t.Fatalf("accepted invalid task: %+v", task)
		}
	}
}

func FuzzDecodeTask(f *testing.F) {
	seed, err := EncodeTask(validTask())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Fuzz(func(t *testing.T, raw []byte) {
		task, err := DecodeTask(raw)
		if err != nil {
			return
		}
		reencoded, err := EncodeTask(task)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeTask(reencoded); err != nil {
			t.Fatal(err)
		}
	})
}

func FuzzParseTaskIndexKeys(f *testing.F) {
	f.Add(TaskDueKey(123, "task"))
	f.Add(TaskRetentionKey(456, "task"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _, _ = ParseTaskDueKey(raw)
		_, _, _ = ParseTaskRetentionKey(raw)
	})
}
