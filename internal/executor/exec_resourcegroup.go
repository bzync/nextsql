package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
)

func (s *Session) execCreateResourceGroup(p planner.CreateResourceGroup) (*Result, error) {
	if p.Existing {
		return &Result{}, nil
	}
	if p.Group == nil {
		return nil, nerr.New(nerr.Internal, "executor.CreateResourceGroup", "missing resource group")
	}
	if _, ok := s.lookupResourceGroup(p.Group.Name); ok {
		if p.IfNotExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.AlreadyExists, "executor.CreateResourceGroup", "resource group already exists")
	}
	raw, err := catalog.EncodeResourceGroup(p.Group)
	if err != nil {
		return nil, err
	}
	tx := s.x.use(s.db.CatTree)
	if err := tx.Insert(catalog.ResourceGroupKey(p.Group.Name), raw); err != nil {
		return nil, err
	}
	s.resourceGroupOverlay[p.Group.Name] = p.Group.Clone()
	s.db.Cat.SetNextID(p.Group.ID + 1)
	return &Result{}, nil
}

func (s *Session) execAlterResourceGroup(p planner.AlterResourceGroup) (*Result, error) {
	if p.Group == nil || p.Result == nil {
		return nil, nerr.New(nerr.Internal, "executor.AlterResourceGroup", "missing resource group")
	}
	current, ok := s.lookupResourceGroup(p.Group.Name)
	if !ok || current.ID != p.Group.ID {
		return nil, nerr.New(nerr.NotFound, "executor.AlterResourceGroup", "resource group not found")
	}
	raw, err := catalog.EncodeResourceGroup(p.Result)
	if err != nil {
		return nil, err
	}
	tx := s.x.use(s.db.CatTree)
	if err := tx.Update(catalog.ResourceGroupKey(p.Group.Name), raw); err != nil {
		return nil, err
	}
	s.resourceGroupOverlay[p.Group.Name] = p.Result.Clone()
	return &Result{}, nil
}

func (s *Session) execDropResourceGroup(p planner.DropResourceGroup) (*Result, error) {
	if p.Group == nil {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropResourceGroup", "resource group not found")
	}
	current, ok := s.lookupResourceGroup(p.Group.Name)
	if !ok || current.ID != p.Group.ID {
		if p.IfExists {
			return &Result{}, nil
		}
		return nil, nerr.New(nerr.NotFound, "executor.DropResourceGroup", "resource group not found")
	}
	tx := s.x.use(s.db.CatTree)
	if err := tx.Delete(catalog.ResourceGroupKey(p.Group.Name)); err != nil {
		return nil, err
	}
	s.resourceGroupOverlay[p.Group.Name] = nil
	return &Result{}, nil
}
