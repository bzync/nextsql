package binder

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

type TriggerLookup func(name string) (*catalog.Trigger, bool)
type TriggerList func() []*catalog.Trigger

type (
	CreateTrigger struct {
		Trigger     *catalog.Trigger
		IfNotExists bool
		Existing    bool
	}
	AlterTrigger struct {
		Trigger *catalog.Trigger
		Result  *catalog.Trigger
	}
	DropTrigger struct {
		Trigger  *catalog.Trigger
		IfExists bool
	}
)

func (CreateTrigger) bound() {}
func (AlterTrigger) bound()  {}
func (DropTrigger) bound()   {}

// BindAutomation adds trigger-wide dependency context to workflow binding.
func BindAutomation(stmt ast.Stmt, tables Lookup, workflows WorkflowLookup, workflowList WorkflowList, triggers TriggerLookup, triggerList TriggerList, schedules ScheduleLookup, scheduleList ScheduleList, resourceGroups ResourceGroupLookup, nextID uint32, owner string) (Bound, error) {
	switch s := stmt.(type) {
	case ast.ShowTasks:
		if s.Limit < 1 || s.Limit > 256 || len(s.After) > catalog.MaxTaskIDBytes {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "invalid SHOW TASKS bounds")
		}
		return ShowTasks{After: s.After, Limit: s.Limit}, nil
	case ast.CancelTask:
		if s.ID == "" || len(s.ID) > catalog.MaxTaskIDBytes {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "invalid task id")
		}
		return CancelTask{ID: s.ID}, nil
	}
	if bound, handled, err := bindSchedule(stmt, workflows, schedules, nextID, owner); handled {
		return bound, err
	}
	if bound, handled, err := bindResourceGroup(stmt, resourceGroups, nextID, owner); handled {
		return bound, err
	}
	switch s := stmt.(type) {
	case ast.CreateTrigger:
		if existing, ok := triggers(s.Name); ok {
			if s.IfNotExists {
				return CreateTrigger{Trigger: existing, IfNotExists: true, Existing: true}, nil
			}
			return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "trigger already exists")
		}
		if owner == "" {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "trigger owner is required")
		}
		table, ok := tables(s.Table)
		if !ok {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "trigger table not found")
		}
		workflow, ok := workflows(s.Workflow)
		if !ok {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "trigger workflow not found")
		}
		if len(s.Args) != len(workflow.Params) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "trigger workflow argument count mismatch")
		}
		for _, arg := range s.Args {
			if err := bindTriggerExpr(arg, table, s.Event); err != nil {
				return nil, err
			}
		}
		trigger := &catalog.Trigger{
			ID: nextID, Name: s.Name, Owner: owner, Timing: s.Timing, Event: s.Event,
			TableID: table.ID, Table: table.Name, WorkflowID: workflow.ID, Workflow: workflow.Name, Args: s.Args,
		}
		if triggerGraphHasCycle(trigger, triggerListSafe(triggerList), workflowList) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "trigger/workflow mutation cycle")
		}
		if _, err := catalog.EncodeTrigger(trigger); err != nil {
			return nil, err
		}
		return CreateTrigger{Trigger: trigger, IfNotExists: s.IfNotExists}, nil
	case ast.AlterTrigger:
		trigger, ok := triggers(s.Name)
		if !ok {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "trigger not found")
		}
		if _, exists := triggers(s.NewName); exists {
			return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "trigger already exists")
		}
		result := trigger.Clone()
		if result == nil {
			return nil, nerr.New(nerr.InvalidFormat, "sql.binder", "invalid trigger descriptor")
		}
		result.Name = s.NewName
		return AlterTrigger{Trigger: trigger, Result: result}, nil
	case ast.DropTrigger:
		trigger, ok := triggers(s.Name)
		if !ok {
			if s.IfExists {
				return DropTrigger{IfExists: true}, nil
			}
			return nil, nerr.New(nerr.NotFound, "sql.binder", "trigger not found")
		}
		return DropTrigger{Trigger: trigger, IfExists: s.IfExists}, nil
	}

	bound, err := BindWorkflow(stmt, tables, workflows, workflowList, nextID, owner)
	if err != nil {
		return nil, err
	}
	for _, trigger := range triggerListSafe(triggerList) {
		switch b := bound.(type) {
		case DropTable:
			if b.Table != nil && trigger.TableID == b.Table.ID {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "table owns trigger "+trigger.Name)
			}
		case AlterTable:
			if b.Table != nil && trigger.TableID == b.Table.ID {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "table owns trigger "+trigger.Name)
			}
		case AlterWorkflow:
			if b.Workflow != nil && trigger.WorkflowID == b.Workflow.ID {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow is referenced by trigger "+trigger.Name)
			}
		case DropWorkflow:
			if b.Workflow != nil && trigger.WorkflowID == b.Workflow.ID {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow is referenced by trigger "+trigger.Name)
			}
		}
	}
	for _, schedule := range scheduleListSafe(scheduleList) {
		switch b := bound.(type) {
		case AlterWorkflow:
			if b.Workflow != nil && schedule.WorkflowID == b.Workflow.ID {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow is referenced by schedule "+schedule.Name)
			}
		case DropWorkflow:
			if b.Workflow != nil && schedule.WorkflowID == b.Workflow.ID {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "workflow is referenced by schedule "+schedule.Name)
			}
		}
	}
	return bound, nil
}

func triggerGraphHasCycle(candidate *catalog.Trigger, existing []*catalog.Trigger, workflowList WorkflowList) bool {
	if workflowList == nil {
		return true
	}
	workflows := make(map[uint32]*catalog.Workflow)
	for _, workflow := range workflowList() {
		if workflow != nil {
			workflows[workflow.ID] = workflow
		}
	}
	edges := make(map[uint32]map[uint32]struct{})
	all := append(append([]*catalog.Trigger(nil), existing...), candidate)
	for _, trigger := range all {
		if trigger == nil {
			continue
		}
		targets := make(map[uint32]struct{})
		workflowMutationTables(trigger.WorkflowID, workflows, make(map[uint32]struct{}), targets)
		if len(targets) == 0 {
			continue
		}
		if edges[trigger.TableID] == nil {
			edges[trigger.TableID] = make(map[uint32]struct{})
		}
		for target := range targets {
			edges[trigger.TableID][target] = struct{}{}
		}
	}
	visiting, visited := make(map[uint32]bool), make(map[uint32]bool)
	var visit func(uint32) bool
	visit = func(node uint32) bool {
		if visiting[node] {
			return true
		}
		if visited[node] {
			return false
		}
		visiting[node] = true
		for next := range edges[node] {
			if visit(next) {
				return true
			}
		}
		visiting[node] = false
		visited[node] = true
		return false
	}
	for node := range edges {
		if visit(node) {
			return true
		}
	}
	return false
}

func workflowMutationTables(id uint32, workflows map[uint32]*catalog.Workflow, visiting map[uint32]struct{}, out map[uint32]struct{}) {
	if _, seen := visiting[id]; seen {
		return
	}
	workflow := workflows[id]
	if workflow == nil {
		return
	}
	visiting[id] = struct{}{}
	defer delete(visiting, id)
	for _, dependency := range workflow.Dependencies {
		switch dependency.Kind {
		case catalog.WorkflowDependencyTable:
			out[dependency.ID] = struct{}{}
		case catalog.WorkflowDependencyWorkflow:
			workflowMutationTables(dependency.ID, workflows, visiting, out)
		}
	}
}

func triggerListSafe(list TriggerList) []*catalog.Trigger {
	if list == nil {
		return nil
	}
	return list()
}

func bindTriggerExpr(expr ast.Expr, table *catalog.Table, event ast.TriggerEvent) error {
	if expr == nil {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "nil trigger argument")
	}
	switch x := expr.(type) {
	case ast.Literal:
		return nil
	case ast.Path:
		if len(x.Parts) != 2 || (x.Parts[0] != "old" && x.Parts[0] != "new") || (event == ast.TriggerInsert && x.Parts[0] == "old") || (event == ast.TriggerDelete && x.Parts[0] == "new") {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "invalid trigger row reference")
		}
		if _, ok := table.ColIndex(x.Parts[1]); !ok {
			return nerr.New(nerr.NotFound, "sql.binder", "trigger column not found")
		}
		return nil
	case ast.Unary:
		return bindTriggerExpr(x.Right, table, event)
	case ast.Binary:
		if err := bindTriggerExpr(x.Left, table, event); err != nil {
			return err
		}
		return bindTriggerExpr(x.Right, table, event)
	case ast.Between:
		for _, item := range []ast.Expr{x.Expr, x.Low, x.High} {
			if err := bindTriggerExpr(item, table, event); err != nil {
				return err
			}
		}
		return nil
	case ast.IsNull:
		return bindTriggerExpr(x.Expr, table, event)
	case ast.Case:
		if x.Operand != nil {
			if err := bindTriggerExpr(x.Operand, table, event); err != nil {
				return err
			}
		}
		for _, arm := range x.Whens {
			if err := bindTriggerExpr(arm.When, table, event); err != nil {
				return err
			}
			if err := bindTriggerExpr(arm.Then, table, event); err != nil {
				return err
			}
		}
		if x.Else != nil {
			return bindTriggerExpr(x.Else, table, event)
		}
		return nil
	default:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "unsupported trigger expression")
	}
}
