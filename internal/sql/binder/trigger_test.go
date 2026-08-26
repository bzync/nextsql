package binder

import (
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
)

func TestBindTriggerLifecycleAndDependencies(t *testing.T) {
	tableStmt, _ := parser.Parse(`CREATE TABLE orders (id STRING PRIMARY KEY, state STRING)`)
	tableBound, err := Bind(tableStmt, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	table := tableBound.(CreateTable).Table
	workflow := &catalog.Workflow{ID: 2, Name: "record", Owner: "alice", Params: []ast.WorkflowParam{{Name: "id", Type: table.Columns[0].Type}}, Body: []ast.Stmt{ast.Delete{Table: "orders"}}}
	tables := func(name string) (*catalog.Table, bool) { return table, name == table.Name }
	workflows := func(name string) (*catalog.Workflow, bool) { return workflow, name == workflow.Name }
	workflowList := func() []*catalog.Workflow { return []*catalog.Workflow{workflow} }
	triggerMap := map[string]*catalog.Trigger{}
	triggers := func(name string) (*catalog.Trigger, bool) { value, ok := triggerMap[name]; return value, ok }
	triggerList := func() []*catalog.Trigger {
		out := make([]*catalog.Trigger, 0, len(triggerMap))
		for _, trigger := range triggerMap {
			out = append(out, trigger)
		}
		return out
	}
	stmt, err := parser.Parse(`CREATE TRIGGER audit AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record(NEW.id)`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindAutomation(stmt, tables, workflows, workflowList, triggers, triggerList, func(string) (*catalog.Schedule, bool) { return nil, false }, nil, 3, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	created := bound.(CreateTrigger).Trigger
	if created.TableID != table.ID || created.WorkflowID != workflow.ID {
		t.Fatalf("trigger=%+v", created)
	}
	triggerMap[created.Name] = created
	for _, sql := range []string{`DROP TABLE orders`, `ALTER TABLE orders ADD COLUMN note STRING`, `DROP WORKFLOW record`, `ALTER WORKFLOW record RENAME TO record2`} {
		candidate, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BindAutomation(candidate, tables, workflows, workflowList, triggers, triggerList, func(string) (*catalog.Schedule, bool) { return nil, false }, nil, 4, "alice", ""); err == nil {
			t.Fatalf("expected trigger dependency rejection: %s", sql)
		}
	}
	rename, _ := parser.Parse(`ALTER TRIGGER audit RENAME TO audit2`)
	if _, err := BindAutomation(rename, tables, workflows, workflowList, triggers, triggerList, func(string) (*catalog.Schedule, bool) { return nil, false }, nil, 4, "alice", ""); err != nil {
		t.Fatal(err)
	}
	drop, _ := parser.Parse(`DROP TRIGGER audit`)
	if _, err := BindAutomation(drop, tables, workflows, workflowList, triggers, triggerList, func(string) (*catalog.Schedule, bool) { return nil, false }, nil, 4, "alice", ""); err != nil {
		t.Fatal(err)
	}
}

func TestBindTriggerRejectsUnknownColumnAndArity(t *testing.T) {
	table := &catalog.Table{ID: 1, Name: "orders", Columns: []catalog.Column{{Name: "id"}}}
	workflow := &catalog.Workflow{ID: 2, Name: "record", Owner: "alice", Params: []ast.WorkflowParam{{Name: "id"}}, Body: []ast.Stmt{ast.Delete{Table: "orders"}}}
	tables := func(name string) (*catalog.Table, bool) { return table, name == "orders" }
	workflows := func(name string) (*catalog.Workflow, bool) { return workflow, name == "record" }
	for _, sql := range []string{
		`CREATE TRIGGER bad AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record(NEW.missing)`,
		`CREATE TRIGGER bad AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record()`,
	} {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BindAutomation(stmt, tables, workflows, nil, func(string) (*catalog.Trigger, bool) { return nil, false }, nil, func(string) (*catalog.Schedule, bool) { return nil, false }, nil, 3, "alice", ""); err == nil {
			t.Fatalf("accepted %s", sql)
		}
	}
}
