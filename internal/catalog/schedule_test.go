package catalog

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestScheduleCodec(t *testing.T) {
	stmt, err := parser.Parse(`CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW rollup('hour', -1)`)
	if err != nil {
		t.Fatal(err)
	}
	create := stmt.(ast.CreateSchedule)
	want := &Schedule{ID: 7, Name: create.Name, Owner: "scheduler", Tenant: "tenant-a", Kind: create.Kind, SpecNS: 3600000000000, WorkflowID: 9, Workflow: create.Workflow, Args: create.Args, CreatedNS: 100, NextFireNS: 3600000000100, Enabled: true}
	raw, err := EncodeSchedule(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeSchedule(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Name != want.Name || got.Owner != want.Owner || got.Tenant != want.Tenant || got.Kind != want.Kind || got.SpecNS != want.SpecNS || got.WorkflowID != want.WorkflowID || got.Workflow != want.Workflow || got.CreatedNS != want.CreatedNS || got.NextFireNS != want.NextFireNS || !got.Enabled || len(got.Args) != 2 {
		t.Fatalf("schedule=%#v", got)
	}
}

func TestScheduleCronCodec(t *testing.T) {
	want := &Schedule{ID: 3, Name: "nightly", Owner: "ops", Kind: ast.ScheduleCron, Cron: "30 3 * * 1-5", WorkflowID: 4, Workflow: "rollup", CreatedNS: 100, NextFireNS: 200, Enabled: true}
	raw, err := EncodeSchedule(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeSchedule(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != ast.ScheduleCron || got.Cron != want.Cron || got.SpecNS != 0 || got.NextFireNS != want.NextFireNS {
		t.Fatalf("schedule=%#v", got)
	}
}

func TestScheduleCronRejectsBadExpr(t *testing.T) {
	for _, bad := range []*Schedule{
		{ID: 1, Name: "a", Owner: "o", Kind: ast.ScheduleCron, Cron: "", WorkflowID: 1, Workflow: "w", CreatedNS: 1, NextFireNS: 2, Enabled: true},
		{ID: 1, Name: "a", Owner: "o", Kind: ast.ScheduleCron, Cron: "not a cron", WorkflowID: 1, Workflow: "w", CreatedNS: 1, NextFireNS: 2, Enabled: true},
		{ID: 1, Name: "a", Owner: "o", Kind: ast.ScheduleCron, Cron: "* * * * *", SpecNS: 5, WorkflowID: 1, Workflow: "w", CreatedNS: 1, NextFireNS: 2, Enabled: true},
		{ID: 1, Name: "a", Owner: "o", Kind: ast.ScheduleEvery, SpecNS: 5, Cron: "* * * * *", WorkflowID: 1, Workflow: "w", CreatedNS: 1, NextFireNS: 2, Enabled: true},
	} {
		if _, err := EncodeSchedule(bad); err == nil {
			t.Fatalf("EncodeSchedule(%#v) = nil error, want error", bad)
		}
	}
}

// encodeScheduleV1 mirrors the pre-cron on-disk layout so the decoder's
// v1 backward-compatibility path stays covered.
func encodeScheduleV1(s *Schedule) []byte {
	buf := append([]byte(nil), scheduleMagic...)
	buf = appendU16(buf, 1)
	buf = appendU32(buf, s.ID)
	buf = appendString(buf, s.Name)
	buf = appendString(buf, s.Owner)
	buf = appendString(buf, s.Tenant)
	buf = append(buf, byte(s.Kind))
	buf = appendU64(buf, uint64(s.SpecNS))
	buf = appendU32(buf, s.WorkflowID)
	buf = appendString(buf, s.Workflow)
	buf = appendU16(buf, 0) // no args
	for _, v := range []int64{s.CreatedNS, s.NextFireNS, s.LastFireNS} {
		buf = appendU64(buf, uint64(v))
	}
	if s.Enabled {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	return buf
}

func TestScheduleV1DecodesWithoutCron(t *testing.T) {
	v1 := encodeScheduleV1(&Schedule{ID: 5, Name: "legacy", Owner: "root", Kind: ast.ScheduleEvery, SpecNS: int64(3600000000000), WorkflowID: 2, Workflow: "run", CreatedNS: 10, NextFireNS: 3600000000010, Enabled: true})
	got, err := DecodeSchedule(v1)
	if err != nil {
		t.Fatalf("v1 decode: %v", err)
	}
	if got.Cron != "" || got.Kind != ast.ScheduleEvery || got.SpecNS != int64(3600000000000) {
		t.Fatalf("schedule=%#v", got)
	}
	// Re-encoding upgrades it to the current version and still round-trips.
	reencoded, err := EncodeSchedule(got)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSchedule(reencoded); err != nil {
		t.Fatalf("v2 re-decode: %v", err)
	}
}

func FuzzDecodeSchedule(f *testing.F) {
	seed, err := EncodeSchedule(&Schedule{ID: 1, Name: "once", Owner: "root", Kind: ast.ScheduleAt, SpecNS: 1, WorkflowID: 2, Workflow: "run", Args: []ast.Expr{ast.Literal{Value: types.StringValue("arg")}}, CreatedNS: 1, NextFireNS: 1, Enabled: true})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	if cronSeed, err := EncodeSchedule(&Schedule{ID: 3, Name: "nightly", Owner: "root", Kind: ast.ScheduleCron, Cron: "0 3 * * 1-5", WorkflowID: 2, Workflow: "run", CreatedNS: 1, NextFireNS: 2, Enabled: true}); err == nil {
		f.Add(cronSeed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := DecodeSchedule(raw)
		if err != nil {
			return
		}
		reencoded, err := EncodeSchedule(got)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeSchedule(reencoded); err != nil {
			t.Fatal(err)
		}
	})
}

func FuzzParseScheduleDueKey(f *testing.F) {
	f.Add(ScheduleDueKey(123, 7))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _, _ = ParseScheduleDueKey(raw)
	})
}
