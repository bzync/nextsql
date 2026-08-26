package executor

import (
	"errors"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

const maxWorkflowDepth = 8
const maxWorkflowVisited = 64

func (s *Session) execRunWorkflow(p planner.RunWorkflow) (*Result, error) {
	if p.Workflow == nil {
		return nil, nerr.New(nerr.Internal, "executor.RunWorkflow", "missing workflow")
	}
	root := s.workflowDepth == 0
	if root {
		s.workflowVisited = make(map[string]struct{})
		defer func() { s.workflowVisited = nil }()
	}
	if _, seen := s.workflowVisited[p.Workflow.Name]; !seen {
		if len(s.workflowVisited) >= maxWorkflowVisited {
			return nil, nerr.New(nerr.Exhausted, "executor.RunWorkflow", "distinct workflow limit exceeded")
		}
		s.workflowVisited[p.Workflow.Name] = struct{}{}
	}
	if s.workflowDepth >= maxWorkflowDepth {
		return nil, nerr.New(nerr.Exhausted, "executor.RunWorkflow", "workflow nesting depth exceeded")
	}
	if len(p.Args) != len(p.Workflow.Params) {
		return nil, nerr.New(nerr.InvalidArgument, "executor.RunWorkflow", "workflow argument count mismatch")
	}
	boundParams := make([]Param, len(p.Args))
	for i, expr := range p.Args {
		value, err := s.eval(expr, nil, nil)
		if err != nil {
			return nil, err
		}
		value, err = types.Coerce(value, p.Workflow.Params[i].Type)
		if err != nil {
			return nil, err
		}
		boundParams[i] = Param{Name: p.Workflow.Params[i].Name, Value: value}
	}

	savedParams := s.params
	s.params = boundParams
	s.workflowDepth++
	defer func() {
		s.workflowDepth--
		s.params = savedParams
	}()

	result := &Result{}
	for _, body := range p.Workflow.Body {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		stmt, err := s.applyTenant(body)
		if err != nil {
			return nil, err
		}
		if err := s.authorize(stmt); err != nil {
			return nil, err
		}
		owner := s.user
		if owner == "" && s.acl == nil {
			owner = "local"
		}
		bound, err := binder.BindWorkflow(stmt, s.lookup, s.lookupWorkflow, s.listWorkflows, s.db.Cat.PeekNext(), owner)
		if err != nil {
			return nil, err
		}
		plan, err := planner.Plan(bound)
		if err != nil {
			return nil, err
		}
		out, err := optimizer.Optimize(optimizer.Request{Plan: plan, Stats: s.lookupStats})
		if err != nil {
			return nil, err
		}
		part, err := s.execPlan(out.Plan)
		if err != nil {
			return nil, err
		}
		if part != nil {
			result.Affected += part.Affected
		}
	}
	return result, nil
}

func (s *Session) execCreateWorkflow(p planner.CreateWorkflow) (*Result, error) {
	if p.Existing {
		return &Result{}, nil
	}
	if p.Workflow == nil {
		return nil, nerr.New(nerr.Internal, "executor.CreateWorkflow", "missing workflow")
	}
	if _, ok := s.lookupWorkflow(p.Workflow.Name); ok {
		if p.IfNotExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateWorkflow", "workflow already exists")
	}
	raw, err := catalog.EncodeWorkflow(p.Workflow)
	if err != nil {
		return nil, err
	}
	if err := s.x.use(s.db.CatTree).Insert(catalog.WorkflowKey(p.Workflow.Name), raw); err != nil {
		return nil, err
	}
	s.workflowOverlay[p.Workflow.Name] = p.Workflow.Clone()
	s.db.Cat.SetNextID(p.Workflow.ID + 1)
	return &Result{}, nil
}

func (s *Session) execAlterWorkflow(p planner.AlterWorkflow) (*Result, error) {
	if p.Workflow == nil || p.Result == nil {
		return nil, nerr.New(nerr.Internal, "executor.AlterWorkflow", "missing workflow")
	}
	current, ok := s.lookupWorkflow(p.Workflow.Name)
	if !ok || current.ID != p.Workflow.ID {
		return nil, nerr.New(nerr.NotFound, "executor.AlterWorkflow", "workflow not found")
	}
	if _, exists := s.lookupWorkflow(p.Result.Name); exists {
		return nil, nerr.New(nerr.AlreadyExists, "executor.AlterWorkflow", "workflow already exists")
	}
	if active, err := s.workflowHasActiveTask(current.ID); err != nil {
		return nil, err
	} else if active {
		return nil, nerr.New(nerr.Conflict, "executor.AlterWorkflow", "workflow has active tasks")
	}
	raw, err := catalog.EncodeWorkflow(p.Result)
	if err != nil {
		return nil, err
	}
	tx := s.x.use(s.db.CatTree)
	if err := tx.Delete(catalog.WorkflowKey(p.Workflow.Name)); err != nil {
		return nil, err
	}
	if err := tx.Insert(catalog.WorkflowKey(p.Result.Name), raw); err != nil {
		return nil, err
	}
	s.workflowOverlay[p.Workflow.Name] = nil
	s.workflowOverlay[p.Result.Name] = p.Result.Clone()
	return &Result{}, nil
}

func (s *Session) execDropWorkflow(p planner.DropWorkflow) (*Result, error) {
	if p.Workflow == nil {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropWorkflow", "workflow not found")
	}
	current, ok := s.lookupWorkflow(p.Workflow.Name)
	if !ok || current.ID != p.Workflow.ID {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropWorkflow", "workflow not found")
	}
	if active, err := s.workflowHasActiveTask(current.ID); err != nil {
		return nil, err
	} else if active {
		return nil, nerr.New(nerr.Conflict, "executor.DropWorkflow", "workflow has active tasks")
	}
	if err := s.x.use(s.db.CatTree).Delete(catalog.WorkflowKey(p.Workflow.Name)); err != nil {
		return nil, err
	}
	s.workflowOverlay[p.Workflow.Name] = nil
	return &Result{}, nil
}

func (s *Session) workflowHasActiveTask(workflowID uint32) (bool, error) {
	if s == nil || s.x == nil || workflowID == 0 {
		return false, nerr.New(nerr.InvalidArgument, "executor.workflowHasActiveTask", "active transaction and workflow id are required")
	}
	start, end := catalog.TaskWorkflowRange(workflowID)
	found := false
	err := s.x.use(s.db.CatTree).Range(start, end, func(_, _ []byte) error {
		found = true
		return errTaskScanLimit
	})
	if err != nil && !errors.Is(err, errTaskScanLimit) {
		return false, err
	}
	return found, nil
}
