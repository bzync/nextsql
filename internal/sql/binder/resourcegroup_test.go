package binder

import (
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/parser"
)

func TestBindResourceGroupLifecycle(t *testing.T) {
	groupMap := map[string]*catalog.ResourceGroup{}
	groups := func(name string) (*catalog.ResourceGroup, bool) { value, ok := groupMap[name]; return value, ok }
	lookupTable := func(string) (*catalog.Table, bool) { return nil, false }
	workflows := func(string) (*catalog.Workflow, bool) { return nil, false }
	lookupTrigger := func(string) (*catalog.Trigger, bool) { return nil, false }
	schedules := func(string) (*catalog.Schedule, bool) { return nil, false }

	stmt, err := parser.Parse(`CREATE RESOURCE GROUP reporting WITH (MAX_CONCURRENCY = 8, MEMORY = 1073741824, WORKERS = 2, PRIORITY = 3)`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindAutomation(stmt, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 5, "admin")
	if err != nil {
		t.Fatal(err)
	}
	created := bound.(CreateResourceGroup).Group
	if created.ID != 5 || created.Name != "reporting" || created.Owner != "admin" || created.MaxConcurrency != 8 || created.MemoryBytes != 1073741824 || created.Workers != 2 || created.Priority != 3 {
		t.Fatalf("group=%+v", created)
	}
	groupMap[created.Name] = created

	// CREATE IF NOT EXISTS against an existing group is a no-op, not an error.
	stmt2, _ := parser.Parse(`CREATE RESOURCE GROUP IF NOT EXISTS reporting`)
	bound2, err := BindAutomation(stmt2, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 6, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !bound2.(CreateResourceGroup).Existing {
		t.Fatal("expected Existing=true for IF NOT EXISTS re-create")
	}

	// Plain CREATE against an existing name is rejected.
	stmt3, _ := parser.Parse(`CREATE RESOURCE GROUP reporting`)
	if _, err := BindAutomation(stmt3, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 6, "admin"); err == nil {
		t.Fatal("expected AlreadyExists")
	}

	// ALTER only touches the options that were supplied.
	alter, _ := parser.Parse(`ALTER RESOURCE GROUP reporting WITH (WORKERS = 4)`)
	boundAlter, err := BindAutomation(alter, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 0, "admin")
	if err != nil {
		t.Fatal(err)
	}
	result := boundAlter.(AlterResourceGroup).Result
	if result.Workers != 4 || result.MaxConcurrency != 8 || result.MemoryBytes != 1073741824 || result.Priority != 3 {
		t.Fatalf("altered group should keep untouched fields: %+v", result)
	}
	groupMap[result.Name] = result

	// ALTER on an unknown group is rejected.
	alterMissing, _ := parser.Parse(`ALTER RESOURCE GROUP nosuch WITH (WORKERS = 1)`)
	if _, err := BindAutomation(alterMissing, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 0, "admin"); err == nil {
		t.Fatal("expected NotFound")
	}

	// DROP.
	drop, _ := parser.Parse(`DROP RESOURCE GROUP reporting`)
	boundDrop, err := BindAutomation(drop, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 0, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if boundDrop.(DropResourceGroup).Group.Name != "reporting" {
		t.Fatalf("drop=%+v", boundDrop)
	}

	// DROP IF EXISTS on a missing group is a no-op, not an error.
	delete(groupMap, "reporting")
	dropIfExists, _ := parser.Parse(`DROP RESOURCE GROUP IF EXISTS reporting`)
	if _, err := BindAutomation(dropIfExists, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 0, "admin"); err != nil {
		t.Fatal(err)
	}

	// Plain DROP on a missing group is rejected.
	dropMissing, _ := parser.Parse(`DROP RESOURCE GROUP reporting`)
	if _, err := BindAutomation(dropMissing, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 0, "admin"); err == nil {
		t.Fatal("expected NotFound")
	}
}

func TestBindResourceGroupRejectsOutOfRangeOptions(t *testing.T) {
	groups := func(string) (*catalog.ResourceGroup, bool) { return nil, false }
	lookupTable := func(string) (*catalog.Table, bool) { return nil, false }
	workflows := func(string) (*catalog.Workflow, bool) { return nil, false }
	lookupTrigger := func(string) (*catalog.Trigger, bool) { return nil, false }
	schedules := func(string) (*catalog.Schedule, bool) { return nil, false }
	stmt, err := parser.Parse(`CREATE RESOURCE GROUP huge WITH (MAX_CONCURRENCY = 4294967295)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindAutomation(stmt, lookupTable, workflows, nil, lookupTrigger, nil, schedules, nil, groups, 1, "admin"); err == nil {
		t.Fatal("expected out-of-range MAX_CONCURRENCY to be rejected")
	}
}
