package binder

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestBindScheduleLifecycleAndDependency(t *testing.T) {
	workflow := &catalog.Workflow{ID: 2, Name: "rollup", Owner: "alice", Params: []ast.WorkflowParam{{Name: "bucket", Type: types.String()}}}
	workflows := func(name string) (*catalog.Workflow, bool) { return workflow, name == workflow.Name }
	scheduleMap := map[string]*catalog.Schedule{}
	schedules := func(name string) (*catalog.Schedule, bool) { value, ok := scheduleMap[name]; return value, ok }
	scheduleList := func() []*catalog.Schedule {
		out := make([]*catalog.Schedule, 0, len(scheduleMap))
		for _, schedule := range scheduleMap {
			out = append(out, schedule)
		}
		return out
	}
	lookupTable := func(string) (*catalog.Table, bool) { return nil, false }
	lookupTrigger := func(string) (*catalog.Trigger, bool) { return nil, false }
	resourceGroups := func(string) (*catalog.ResourceGroup, bool) { return nil, false }

	stmt, err := parser.Parse(`CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW rollup('hour')`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindAutomation(stmt, lookupTable, workflows, func() []*catalog.Workflow { return []*catalog.Workflow{workflow} }, lookupTrigger, nil, schedules, scheduleList, resourceGroups, 3, "alice")
	if err != nil {
		t.Fatal(err)
	}
	created := bound.(CreateSchedule).Schedule
	if created.WorkflowID != workflow.ID || created.SpecNS != int64(time.Hour) || created.Tenant != "" {
		t.Fatalf("schedule=%+v", created)
	}
	scheduleMap[created.Name] = created

	dropWorkflow, _ := parser.Parse(`DROP WORKFLOW rollup`)
	if _, err := BindAutomation(dropWorkflow, lookupTable, workflows, func() []*catalog.Workflow { return []*catalog.Workflow{workflow} }, lookupTrigger, nil, schedules, scheduleList, resourceGroups, 4, "alice"); err == nil {
		t.Fatal("schedule did not protect workflow dependency")
	}
	rename, _ := parser.Parse(`ALTER SCHEDULE hourly RENAME TO hourly2`)
	if _, err := BindAutomation(rename, lookupTable, workflows, nil, lookupTrigger, nil, schedules, scheduleList, resourceGroups, 4, "alice"); err != nil {
		t.Fatal(err)
	}
	drop, _ := parser.Parse(`DROP SCHEDULE hourly`)
	if _, err := BindAutomation(drop, lookupTable, workflows, nil, lookupTrigger, nil, schedules, scheduleList, resourceGroups, 4, "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestBindScheduleCron(t *testing.T) {
	workflow := &catalog.Workflow{ID: 2, Name: "rollup", Owner: "alice", Params: []ast.WorkflowParam{{Name: "bucket", Type: types.String()}}}
	workflows := func(name string) (*catalog.Workflow, bool) { return workflow, name == workflow.Name }
	schedules := func(string) (*catalog.Schedule, bool) { return nil, false }
	lookupTable := func(string) (*catalog.Table, bool) { return nil, false }
	lookupTrigger := func(string) (*catalog.Trigger, bool) { return nil, false }
	resourceGroups := func(string) (*catalog.ResourceGroup, bool) { return nil, false }

	stmt, err := parser.Parse(`CREATE SCHEDULE nightly CRON '30 3 * * 1-5' RUN WORKFLOW rollup('night')`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindAutomation(stmt, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, resourceGroups, 3, "alice")
	if err != nil {
		t.Fatal(err)
	}
	created := bound.(CreateSchedule).Schedule
	if created.Kind != ast.ScheduleCron || created.Cron != "30 3 * * 1-5" || created.SpecNS != 0 {
		t.Fatalf("schedule=%+v", created)
	}
	if created.NextFireNS <= created.CreatedNS {
		t.Fatalf("next fire %d not after created %d", created.NextFireNS, created.CreatedNS)
	}
	// The stored cursor must be a real cron match (03:30 UTC, Mon-Fri).
	next := time.Unix(0, created.NextFireNS).UTC()
	if next.Minute() != 30 || next.Hour() != 3 || next.Weekday() == time.Saturday || next.Weekday() == time.Sunday {
		t.Fatalf("next fire %s is not a valid boundary", next.Format(time.RFC3339))
	}

	for _, sql := range []string{
		`CREATE SCHEDULE bad CRON 'not a cron' RUN WORKFLOW rollup('x')`,
		`CREATE SCHEDULE impossible CRON '0 0 30 2 *' RUN WORKFLOW rollup('x')`,
		`CREATE SCHEDULE short CRON '* * * *' RUN WORKFLOW rollup('x')`,
	} {
		s, perr := parser.Parse(sql)
		if perr != nil {
			t.Fatalf("parse %s: %v", sql, perr)
		}
		if _, berr := BindAutomation(s, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, resourceGroups, 3, "alice"); berr == nil {
			t.Fatalf("accepted invalid cron schedule: %s", sql)
		}
	}
}

func TestBindScheduleRejectsInvalidSpecsAndArity(t *testing.T) {
	workflow := &catalog.Workflow{ID: 2, Name: "rollup", Owner: "alice", Params: []ast.WorkflowParam{{Name: "bucket", Type: types.String()}}}
	workflows := func(name string) (*catalog.Workflow, bool) { return workflow, name == workflow.Name }
	schedules := func(string) (*catalog.Schedule, bool) { return nil, false }
	lookupTable := func(string) (*catalog.Table, bool) { return nil, false }
	lookupTrigger := func(string) (*catalog.Trigger, bool) { return nil, false }
	resourceGroups := func(string) (*catalog.ResourceGroup, bool) { return nil, false }
	for _, sql := range []string{
		`CREATE SCHEDULE fast EVERY '1ms' RUN WORKFLOW rollup('x')`,
		`CREATE SCHEDULE huge EVERY '9000h' RUN WORKFLOW rollup('x')`,
		`CREATE SCHEDULE bad AT 'tomorrow' RUN WORKFLOW rollup('x')`,
		`CREATE SCHEDULE wrong EVERY '1h' RUN WORKFLOW rollup()`,
	} {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BindAutomation(stmt, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, resourceGroups, 3, "alice"); err == nil {
			t.Fatalf("accepted invalid schedule: %s", sql)
		}
	}
}
