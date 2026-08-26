package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
)

func (s *Session) execCreateTrigger(p planner.CreateTrigger) (*Result, error) {
	if p.Existing {
		return &Result{}, nil
	}
	if p.Trigger == nil {
		return nil, nerr.New(nerr.Internal, "executor.CreateTrigger", "missing trigger")
	}
	if _, ok := s.lookupTrigger(p.Trigger.Name); ok {
		if p.IfNotExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateTrigger", "trigger already exists")
	}
	raw, err := catalog.EncodeTrigger(p.Trigger)
	if err != nil {
		return nil, err
	}
	if err := s.x.use(s.db.CatTree).Insert(catalog.TriggerKey(p.Trigger.Name), raw); err != nil {
		return nil, err
	}
	s.triggerOverlay[p.Trigger.Name] = p.Trigger.Clone()
	s.db.Cat.SetNextID(p.Trigger.ID + 1)
	return &Result{}, nil
}

func (s *Session) execAlterTrigger(p planner.AlterTrigger) (*Result, error) {
	if p.Trigger == nil || p.Result == nil {
		return nil, nerr.New(nerr.Internal, "executor.AlterTrigger", "missing trigger")
	}
	current, ok := s.lookupTrigger(p.Trigger.Name)
	if !ok || current.ID != p.Trigger.ID {
		return nil, nerr.New(nerr.NotFound, "executor.AlterTrigger", "trigger not found")
	}
	if _, exists := s.lookupTrigger(p.Result.Name); exists {
		return nil, nerr.New(nerr.AlreadyExists, "executor.AlterTrigger", "trigger already exists")
	}
	raw, err := catalog.EncodeTrigger(p.Result)
	if err != nil {
		return nil, err
	}
	tx := s.x.use(s.db.CatTree)
	if err := tx.Delete(catalog.TriggerKey(p.Trigger.Name)); err != nil {
		return nil, err
	}
	if err := tx.Insert(catalog.TriggerKey(p.Result.Name), raw); err != nil {
		return nil, err
	}
	s.triggerOverlay[p.Trigger.Name] = nil
	s.triggerOverlay[p.Result.Name] = p.Result.Clone()
	return &Result{}, nil
}

func (s *Session) execDropTrigger(p planner.DropTrigger) (*Result, error) {
	if p.Trigger == nil {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropTrigger", "trigger not found")
	}
	current, ok := s.lookupTrigger(p.Trigger.Name)
	if !ok || current.ID != p.Trigger.ID {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropTrigger", "trigger not found")
	}
	if err := s.x.use(s.db.CatTree).Delete(catalog.TriggerKey(p.Trigger.Name)); err != nil {
		return nil, err
	}
	s.triggerOverlay[p.Trigger.Name] = nil
	return &Result{}, nil
}
