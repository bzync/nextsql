package executor

import (
	"sort"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

const maxTriggerDepth = 8

func (s *Session) hasTriggers(tableID uint32) bool {
	for _, trigger := range s.listTriggers() {
		if trigger.TableID == tableID {
			return true
		}
	}
	return false
}

func (s *Session) fireTriggers(table *catalog.Table, event ast.TriggerEvent, timing ast.TriggerTiming, oldRow, newRow []types.Value) (err error) {
	triggers := make([]*catalog.Trigger, 0)
	for _, trigger := range s.listTriggers() {
		if trigger.TableID == table.ID && trigger.Event == event && trigger.Timing == timing {
			triggers = append(triggers, trigger)
		}
	}
	if len(triggers) == 0 {
		return nil
	}
	sort.Slice(triggers, func(i, j int) bool { return triggers[i].ID < triggers[j].ID })
	if s.triggerDepth >= maxTriggerDepth {
		s.triggerBroken = true
		return nerr.New(nerr.Exhausted, "executor.trigger", "trigger nesting depth exceeded")
	}
	s.triggerDepth++
	defer func() { s.triggerDepth-- }()
	for _, trigger := range triggers {
		if err := s.budget().Check(); err != nil {
			s.triggerBroken = true
			return err
		}
		workflow, ok := s.lookupWorkflow(trigger.Workflow)
		if !ok || workflow.ID != trigger.WorkflowID {
			s.triggerBroken = true
			return nerr.New(nerr.InvalidFormat, "executor.trigger", "trigger workflow dependency mismatch")
		}
		if err := s.authorize(ast.RunWorkflow{Name: workflow.Name}); err != nil {
			s.triggerBroken = true
			return err
		}
		args := make([]ast.Expr, len(trigger.Args))
		for i, expr := range trigger.Args {
			materialized, err := materializeTriggerExpr(expr, table, oldRow, newRow)
			if err != nil {
				s.triggerBroken = true
				return err
			}
			value, err := s.eval(materialized, nil, nil)
			if err != nil {
				s.triggerBroken = true
				return err
			}
			args[i] = ast.Literal{Value: value}
		}
		_, runErr := s.execRunWorkflow(planner.RunWorkflow{Workflow: workflow, Args: args})
		s.auditRecord(security.ActionTriggerFire, trigger.Name, runErr)
		s.auditRecord(security.ActionWorkflowRun, workflow.Name, runErr)
		if runErr != nil {
			s.triggerBroken = true
			return runErr
		}
	}
	return nil
}

func materializeTriggerExpr(expr ast.Expr, table *catalog.Table, oldRow, newRow []types.Value) (ast.Expr, error) {
	if expr == nil {
		return nil, nerr.New(nerr.InvalidFormat, "executor.trigger", "nil trigger expression")
	}
	switch x := expr.(type) {
	case ast.Literal:
		return x, nil
	case ast.Path:
		if len(x.Parts) != 2 {
			return nil, nerr.New(nerr.InvalidFormat, "executor.trigger", "invalid trigger row reference")
		}
		column, ok := table.ColIndex(x.Parts[1])
		if !ok {
			return nil, nerr.New(nerr.InvalidFormat, "executor.trigger", "trigger column dependency mismatch")
		}
		var row []types.Value
		switch x.Parts[0] {
		case "old":
			row = oldRow
		case "new":
			row = newRow
		default:
			return nil, nerr.New(nerr.InvalidFormat, "executor.trigger", "invalid trigger row source")
		}
		if column >= len(row) {
			return nil, nerr.New(nerr.InvalidFormat, "executor.trigger", "trigger row is unavailable")
		}
		return ast.Literal{Value: row[column].Clone()}, nil
	case ast.Unary:
		right, err := materializeTriggerExpr(x.Right, table, oldRow, newRow)
		x.Right = right
		return x, err
	case ast.Binary:
		left, err := materializeTriggerExpr(x.Left, table, oldRow, newRow)
		if err != nil {
			return nil, err
		}
		right, err := materializeTriggerExpr(x.Right, table, oldRow, newRow)
		x.Left, x.Right = left, right
		return x, err
	case ast.Between:
		items := []*ast.Expr{&x.Expr, &x.Low, &x.High}
		for _, item := range items {
			value, err := materializeTriggerExpr(*item, table, oldRow, newRow)
			if err != nil {
				return nil, err
			}
			*item = value
		}
		return x, nil
	case ast.IsNull:
		value, err := materializeTriggerExpr(x.Expr, table, oldRow, newRow)
		x.Expr = value
		return x, err
	case ast.Case:
		if x.Operand != nil {
			value, err := materializeTriggerExpr(x.Operand, table, oldRow, newRow)
			if err != nil {
				return nil, err
			}
			x.Operand = value
		}
		for i := range x.Whens {
			when, err := materializeTriggerExpr(x.Whens[i].When, table, oldRow, newRow)
			if err != nil {
				return nil, err
			}
			then, err := materializeTriggerExpr(x.Whens[i].Then, table, oldRow, newRow)
			if err != nil {
				return nil, err
			}
			x.Whens[i].When, x.Whens[i].Then = when, then
		}
		if x.Else != nil {
			value, err := materializeTriggerExpr(x.Else, table, oldRow, newRow)
			if err != nil {
				return nil, err
			}
			x.Else = value
		}
		return x, nil
	default:
		return nil, nerr.New(nerr.InvalidFormat, "executor.trigger", "unsupported trigger expression")
	}
}
