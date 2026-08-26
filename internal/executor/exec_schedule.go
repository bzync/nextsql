package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
)

func (s *Session) execCreateSchedule(p planner.CreateSchedule) (*Result, error) {
	if p.Existing {
		return &Result{}, nil
	}
	if p.Schedule == nil {
		return nil, nerr.New(nerr.Internal, "executor.CreateSchedule", "missing schedule")
	}
	if _, ok := s.lookupSchedule(p.Schedule.Name); ok {
		if p.IfNotExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateSchedule", "schedule already exists")
	}
	raw, err := catalog.EncodeSchedule(p.Schedule)
	if err != nil {
		return nil, err
	}
	tx := s.x.use(s.db.CatTree)
	if err := tx.Insert(catalog.ScheduleKey(p.Schedule.Name), raw); err != nil {
		return nil, err
	}
	if err := tx.Insert(catalog.ScheduleDueKey(p.Schedule.NextFireNS, p.Schedule.ID), []byte(p.Schedule.Name)); err != nil {
		return nil, err
	}
	s.scheduleOverlay[p.Schedule.Name] = p.Schedule.Clone()
	s.db.Cat.SetNextID(p.Schedule.ID + 1)
	return &Result{}, nil
}

func (s *Session) execAlterSchedule(p planner.AlterSchedule) (*Result, error) {
	if p.Schedule == nil || p.Result == nil {
		return nil, nerr.New(nerr.Internal, "executor.AlterSchedule", "missing schedule")
	}
	current, ok := s.lookupSchedule(p.Schedule.Name)
	if !ok || current.ID != p.Schedule.ID {
		return nil, nerr.New(nerr.NotFound, "executor.AlterSchedule", "schedule not found")
	}
	if _, exists := s.lookupSchedule(p.Result.Name); exists {
		return nil, nerr.New(nerr.AlreadyExists, "executor.AlterSchedule", "schedule already exists")
	}
	raw, err := catalog.EncodeSchedule(p.Result)
	if err != nil {
		return nil, err
	}
	tx := s.x.use(s.db.CatTree)
	if err := tx.Delete(catalog.ScheduleKey(p.Schedule.Name)); err != nil {
		return nil, err
	}
	if err := tx.Insert(catalog.ScheduleKey(p.Result.Name), raw); err != nil {
		return nil, err
	}
	if p.Result.Enabled {
		if err := tx.Update(catalog.ScheduleDueKey(p.Result.NextFireNS, p.Result.ID), []byte(p.Result.Name)); err != nil {
			return nil, err
		}
	}
	s.scheduleOverlay[p.Schedule.Name] = nil
	s.scheduleOverlay[p.Result.Name] = p.Result.Clone()
	return &Result{}, nil
}

func (s *Session) execDropSchedule(p planner.DropSchedule) (*Result, error) {
	if p.Schedule == nil {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropSchedule", "schedule not found")
	}
	current, ok := s.lookupSchedule(p.Schedule.Name)
	if !ok || current.ID != p.Schedule.ID {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropSchedule", "schedule not found")
	}
	tx := s.x.use(s.db.CatTree)
	if err := tx.Delete(catalog.ScheduleKey(p.Schedule.Name)); err != nil {
		return nil, err
	}
	if current.Enabled {
		if err := tx.Delete(catalog.ScheduleDueKey(current.NextFireNS, current.ID)); err != nil {
			return nil, err
		}
	}
	s.scheduleOverlay[p.Schedule.Name] = nil
	return &Result{}, nil
}
