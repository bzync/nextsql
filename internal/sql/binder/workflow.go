package binder

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

// WorkflowLookup resolves a workflow visible to the current transaction.
type WorkflowLookup func(name string) (*catalog.Workflow, bool)

// WorkflowList returns every workflow visible to the current transaction. It
// is required for dependency-safe ALTER and DROP binding.
type WorkflowList func() []*catalog.Workflow

type (
	CreateWorkflow struct {
		Workflow    *catalog.Workflow
		IfNotExists bool
		Existing    bool
	}
	RunWorkflow struct {
		Workflow *catalog.Workflow
		Args     []ast.Expr
	}
	AlterWorkflow struct {
		Workflow *catalog.Workflow
		Result   *catalog.Workflow
	}
	DropWorkflow struct {
		Workflow *catalog.Workflow
		IfExists bool
	}
)

func (CreateWorkflow) bound() {}
func (RunWorkflow) bound()    {}
func (AlterWorkflow) bound()  {}
func (DropWorkflow) bound()   {}

// BindWorkflow binds the workflow statements that need catalog-wide context.
// Non-workflow statements continue through Bind so the existing SQL binding
// API and its recursive query handling remain unchanged.
func BindWorkflow(stmt ast.Stmt, tables Lookup, workflows WorkflowLookup, list WorkflowList, nextID uint32, owner string) (Bound, error) {
	switch s := stmt.(type) {
	case ast.CreateWorkflow:
		if existing, ok := workflows(s.Name); ok {
			if s.IfNotExists {
				return CreateWorkflow{Workflow: existing, IfNotExists: true, Existing: true}, nil
			}
			return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "workflow already exists")
		}
		if owner == "" {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow owner is required")
		}
		deps := make([]catalog.WorkflowDependency, 0, len(s.Body))
		seenDeps := make(map[string]struct{})
		for _, body := range s.Body {
			if run, ok := body.(ast.RunWorkflow); ok {
				if run.Name == s.Name {
					return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow cannot invoke itself")
				}
				bound, err := bindRunWorkflow(run, workflows)
				if err != nil {
					return nil, err
				}
				addWorkflowDependency(&deps, seenDeps, catalog.WorkflowDependencyWorkflow, bound.(RunWorkflow).Workflow.ID, run.Name)
				continue
			}
			bound, err := Bind(body, tables, nextID)
			if err != nil {
				return nil, err
			}
			if table := workflowBoundTable(bound); table != nil {
				addWorkflowDependency(&deps, seenDeps, catalog.WorkflowDependencyTable, table.ID, table.Name)
			}
		}
		w := &catalog.Workflow{ID: nextID, Name: s.Name, Owner: owner, Params: s.Params, Body: s.Body, Dependencies: deps}
		if _, err := catalog.EncodeWorkflow(w); err != nil {
			return nil, err
		}
		return CreateWorkflow{Workflow: w, IfNotExists: s.IfNotExists}, nil
	case ast.RunWorkflow:
		return bindRunWorkflow(s, workflows)
	case ast.AlterWorkflow:
		w, ok := workflows(s.Name)
		if !ok {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "workflow not found")
		}
		if _, ok := workflows(s.NewName); ok {
			return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "workflow already exists")
		}
		if dependentWorkflow(list, s.Name) != "" {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow has dependent workflows")
		}
		result := w.Clone()
		if result == nil {
			return nil, nerr.New(nerr.InvalidFormat, "sql.binder", "invalid workflow descriptor")
		}
		result.Name = s.NewName
		return AlterWorkflow{Workflow: w, Result: result}, nil
	case ast.DropWorkflow:
		w, ok := workflows(s.Name)
		if !ok {
			if s.IfExists {
				return DropWorkflow{IfExists: true}, nil
			}
			return nil, nerr.New(nerr.NotFound, "sql.binder", "workflow not found")
		}
		if dependentWorkflow(list, s.Name) != "" {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow has dependent workflows")
		}
		return DropWorkflow{Workflow: w, IfExists: s.IfExists}, nil
	default:
		bound, err := Bind(stmt, tables, nextID)
		if err != nil {
			return nil, err
		}
		var table *catalog.Table
		switch b := bound.(type) {
		case DropTable:
			table = b.Table
		case AlterTable:
			table = b.Table
		}
		if table != nil {
			if dependent := dependentOnTable(list, table.ID); dependent != "" {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "table is referenced by workflow "+dependent)
			}
		}
		return bound, nil
	}
}

func workflowBoundTable(bound Bound) *catalog.Table {
	switch b := bound.(type) {
	case Insert:
		return b.Table
	case Upsert:
		return b.Table
	case Update:
		return b.Table
	case Delete:
		return b.Table
	default:
		return nil
	}
}

func addWorkflowDependency(out *[]catalog.WorkflowDependency, seen map[string]struct{}, kind catalog.WorkflowDependencyKind, id uint32, name string) {
	key := string([]byte{byte(kind)}) + name
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*out = append(*out, catalog.WorkflowDependency{Kind: kind, ID: id, Name: name})
}

func dependentOnTable(list WorkflowList, id uint32) string {
	if list == nil {
		return "unknown"
	}
	for _, w := range list() {
		if w == nil {
			continue
		}
		for _, dep := range w.Dependencies {
			if dep.Kind == catalog.WorkflowDependencyTable && dep.ID == id {
				return w.Name
			}
		}
	}
	return ""
}

func bindRunWorkflow(s ast.RunWorkflow, workflows WorkflowLookup) (Bound, error) {
	w, ok := workflows(s.Name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "sql.binder", "workflow not found")
	}
	if len(s.Args) != len(w.Params) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow argument count mismatch")
	}
	return RunWorkflow{Workflow: w, Args: s.Args}, nil
}

func dependentWorkflow(list WorkflowList, target string) string {
	if list == nil {
		return "unknown"
	}
	for _, w := range list() {
		if w == nil || w.Name == target {
			continue
		}
		for _, stmt := range w.Body {
			if run, ok := stmt.(ast.RunWorkflow); ok && run.Name == target {
				return w.Name
			}
		}
	}
	return ""
}
