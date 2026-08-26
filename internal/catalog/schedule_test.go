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

func FuzzDecodeSchedule(f *testing.F) {
	seed, err := EncodeSchedule(&Schedule{ID: 1, Name: "once", Owner: "root", Kind: ast.ScheduleAt, SpecNS: 1, WorkflowID: 2, Workflow: "run", Args: []ast.Expr{ast.Literal{Value: types.StringValue("arg")}}, CreatedNS: 1, NextFireNS: 1, Enabled: true})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
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
