package binder

import (
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

type ScheduleLookup func(name string) (*catalog.Schedule, bool)
type ScheduleList func() []*catalog.Schedule

type (
	CreateSchedule struct {
		Schedule    *catalog.Schedule
		IfNotExists bool
		Existing    bool
	}
	AlterSchedule struct {
		Schedule *catalog.Schedule
		Result   *catalog.Schedule
	}
	DropSchedule struct {
		Schedule *catalog.Schedule
		IfExists bool
	}
)

func (CreateSchedule) bound() {}
func (AlterSchedule) bound()  {}
func (DropSchedule) bound()   {}

const (
	minScheduleEvery = time.Second
	maxScheduleEvery = 365 * 24 * time.Hour
)

func bindSchedule(stmt ast.Stmt, workflows WorkflowLookup, schedules ScheduleLookup, nextID uint32, owner, tenant string) (Bound, bool, error) {
	switch s := stmt.(type) {
	case ast.CreateSchedule:
		if existing, ok := schedules(s.Name); ok {
			if s.IfNotExists {
				return CreateSchedule{Schedule: existing, IfNotExists: true, Existing: true}, true, nil
			}
			return nil, true, nerr.New(nerr.AlreadyExists, "sql.binder", "schedule already exists")
		}
		if owner == "" {
			return nil, true, nerr.New(nerr.InvalidArgument, "sql.binder", "schedule owner is required")
		}
		workflow, ok := workflows(s.Workflow)
		if !ok {
			return nil, true, nerr.New(nerr.NotFound, "sql.binder", "schedule workflow not found")
		}
		if len(s.Args) != len(workflow.Params) {
			return nil, true, nerr.New(nerr.InvalidArgument, "sql.binder", "schedule workflow argument count mismatch")
		}
		var specNS int64
		switch s.Kind {
		case ast.ScheduleEvery:
			d, err := time.ParseDuration(s.Spec)
			if err != nil || d < minScheduleEvery || d > maxScheduleEvery {
				return nil, true, nerr.New(nerr.InvalidArgument, "sql.binder", "EVERY must be between 1s and 8760h")
			}
			specNS = int64(d)
		case ast.ScheduleAt:
			at, err := time.Parse(time.RFC3339Nano, s.Spec)
			if err != nil || at.UnixNano() <= 0 {
				return nil, true, nerr.New(nerr.InvalidArgument, "sql.binder", "AT must be an RFC3339 timestamp after the Unix epoch")
			}
			specNS = at.UTC().UnixNano()
		default:
			return nil, true, nerr.New(nerr.InvalidArgument, "sql.binder", "invalid schedule kind")
		}
		createdNS := time.Now().UTC().UnixNano()
		nextFireNS := specNS
		if s.Kind == ast.ScheduleEvery {
			nextFireNS = createdNS + specNS
		} else if nextFireNS <= createdNS {
			return nil, true, nerr.New(nerr.InvalidArgument, "sql.binder", "AT timestamp must be in the future")
		}
		schedule := &catalog.Schedule{ID: nextID, Name: s.Name, Owner: owner, Tenant: tenant, Kind: s.Kind, SpecNS: specNS, WorkflowID: workflow.ID, Workflow: workflow.Name, Args: s.Args, CreatedNS: createdNS, NextFireNS: nextFireNS, Enabled: true}
		if _, err := catalog.EncodeSchedule(schedule); err != nil {
			return nil, true, err
		}
		return CreateSchedule{Schedule: schedule, IfNotExists: s.IfNotExists}, true, nil
	case ast.AlterSchedule:
		schedule, ok := schedules(s.Name)
		if !ok {
			return nil, true, nerr.New(nerr.NotFound, "sql.binder", "schedule not found")
		}
		if _, exists := schedules(s.NewName); exists {
			return nil, true, nerr.New(nerr.AlreadyExists, "sql.binder", "schedule already exists")
		}
		result := schedule.Clone()
		if result == nil {
			return nil, true, nerr.New(nerr.InvalidFormat, "sql.binder", "invalid schedule descriptor")
		}
		result.Name = s.NewName
		return AlterSchedule{Schedule: schedule, Result: result}, true, nil
	case ast.DropSchedule:
		schedule, ok := schedules(s.Name)
		if !ok {
			if s.IfExists {
				return DropSchedule{IfExists: true}, true, nil
			}
			return nil, true, nerr.New(nerr.NotFound, "sql.binder", "schedule not found")
		}
		return DropSchedule{Schedule: schedule, IfExists: s.IfExists}, true, nil
	default:
		return nil, false, nil
	}
}

func scheduleListSafe(list ScheduleList) []*catalog.Schedule {
	if list == nil {
		return nil
	}
	return list()
}
