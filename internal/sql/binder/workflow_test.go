package binder

import (
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
)

func TestBindWorkflowLifecycle(t *testing.T) {
	tableStmt, err := parser.Parse(`CREATE TABLE jobs (id STRING PRIMARY KEY, state STRING NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	tableBound, err := Bind(tableStmt, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	table := tableBound.(CreateTable).Table
	tables := func(name string) (*catalog.Table, bool) { return table, name == table.Name }
	workflows := map[string]*catalog.Workflow{}
	lookup := func(name string) (*catalog.Workflow, bool) { w, ok := workflows[name]; return w, ok }
	list := func() []*catalog.Workflow {
		out := make([]*catalog.Workflow, 0, len(workflows))
		for _, w := range workflows {
			out = append(out, w)
		}
		return out
	}

	stmt, err := parser.Parse(`CREATE WORKFLOW advance(id STRING) AS BEGIN UPDATE jobs SET state = 'done' WHERE id = $id; END`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindWorkflow(stmt, tables, lookup, list, 2, "alice")
	if err != nil {
		t.Fatal(err)
	}
	created := bound.(CreateWorkflow)
	if created.Workflow.ID != 2 || created.Workflow.Owner != "alice" {
		t.Fatalf("created workflow: %+v", created.Workflow)
	}
	if len(created.Workflow.Dependencies) != 1 || created.Workflow.Dependencies[0].Kind != catalog.WorkflowDependencyTable || created.Workflow.Dependencies[0].ID != table.ID {
		t.Fatalf("dependencies: %+v", created.Workflow.Dependencies)
	}
	workflows[created.Workflow.Name] = created.Workflow
	for _, sql := range []string{`DROP TABLE jobs`, `ALTER TABLE jobs ADD COLUMN note STRING`} {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BindWorkflow(stmt, tables, lookup, list, 3, "alice"); err == nil {
			t.Fatalf("expected table dependency rejection for %s", sql)
		}
	}

	runStmt, err := parser.Parse(`RUN WORKFLOW advance('j1')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindWorkflow(runStmt, tables, lookup, list, 3, "alice"); err != nil {
		t.Fatal(err)
	}
	badArity, err := parser.Parse(`RUN WORKFLOW advance()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindWorkflow(badArity, tables, lookup, list, 3, "alice"); err == nil {
		t.Fatal("expected argument-count rejection")
	}

	renameStmt, err := parser.Parse(`ALTER WORKFLOW advance RENAME TO finish`)
	if err != nil {
		t.Fatal(err)
	}
	rename, err := BindWorkflow(renameStmt, tables, lookup, list, 3, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if rename.(AlterWorkflow).Result.Name != "finish" {
		t.Fatalf("rename result: %+v", rename)
	}

	dropStmt, err := parser.Parse(`DROP WORKFLOW advance`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindWorkflow(dropStmt, tables, lookup, list, 3, "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestBindWorkflowDependenciesAndBody(t *testing.T) {
	lookupTable := func(string) (*catalog.Table, bool) { return nil, false }
	base := &catalog.Workflow{ID: 1, Name: "base", Owner: "alice", Body: []ast.Stmt{ast.Delete{Table: "missing"}}}
	caller := &catalog.Workflow{ID: 2, Name: "caller", Owner: "alice", Body: []ast.Stmt{ast.RunWorkflow{Name: "base"}}}
	workflows := map[string]*catalog.Workflow{"base": base, "caller": caller}
	lookup := func(name string) (*catalog.Workflow, bool) { w, ok := workflows[name]; return w, ok }
	list := func() []*catalog.Workflow { return []*catalog.Workflow{base, caller} }

	for _, sql := range []string{`DROP WORKFLOW base`, `ALTER WORKFLOW base RENAME TO renamed`} {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BindWorkflow(stmt, lookupTable, lookup, list, 3, "alice"); err == nil {
			t.Fatalf("expected dependency rejection for %s", sql)
		}
	}

	self, err := parser.Parse(`CREATE WORKFLOW loop() AS BEGIN RUN WORKFLOW loop(); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindWorkflow(self, lookupTable, lookup, list, 3, "alice"); err == nil {
		t.Fatal("expected self-invocation rejection")
	}
	badBody, err := parser.Parse(`CREATE WORKFLOW bad(id STRING) AS BEGIN DELETE FROM missing WHERE id = $id; END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindWorkflow(badBody, lookupTable, lookup, list, 3, "alice"); err == nil {
		t.Fatal("expected body table binding rejection")
	}
}
