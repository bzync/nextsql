package binder

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
)

type ResourceGroupLookup func(name string) (*catalog.ResourceGroup, bool)
type ResourceGroupList func() []*catalog.ResourceGroup

type (
	CreateResourceGroup struct {
		Group       *catalog.ResourceGroup
		IfNotExists bool
		Existing    bool
	}
	AlterResourceGroup struct {
		Group  *catalog.ResourceGroup
		Result *catalog.ResourceGroup
	}
	DropResourceGroup struct {
		Group    *catalog.ResourceGroup
		IfExists bool
	}
)

func (CreateResourceGroup) bound() {}
func (AlterResourceGroup) bound()  {}
func (DropResourceGroup) bound()   {}

func bindResourceGroup(stmt ast.Stmt, groups ResourceGroupLookup, nextID uint32, owner string) (Bound, bool, error) {
	switch s := stmt.(type) {
	case ast.CreateResourceGroup:
		if existing, ok := groups(s.Name); ok {
			if s.IfNotExists {
				return CreateResourceGroup{Group: existing, IfNotExists: true, Existing: true}, true, nil
			}
			return nil, true, nerr.New(nerr.AlreadyExists, "sql.binder", "resource group already exists")
		}
		if owner == "" {
			return nil, true, nerr.New(nerr.InvalidArgument, "sql.binder", "resource group owner is required")
		}
		group := &catalog.ResourceGroup{
			ID:             nextID,
			Name:           s.Name,
			Owner:          owner,
			MaxConcurrency: int32(s.MaxConcurrency),
			MemoryBytes:    s.MemoryBytes,
			Workers:        int32(s.Workers),
			Priority:       int32(s.Priority),
		}
		if _, err := catalog.EncodeResourceGroup(group); err != nil {
			return nil, true, err
		}
		return CreateResourceGroup{Group: group, IfNotExists: s.IfNotExists}, true, nil
	case ast.AlterResourceGroup:
		group, ok := groups(s.Name)
		if !ok {
			return nil, true, nerr.New(nerr.NotFound, "sql.binder", "resource group not found")
		}
		result := group.Clone()
		if result == nil {
			return nil, true, nerr.New(nerr.InvalidFormat, "sql.binder", "invalid resource group descriptor")
		}
		if s.HasMaxConcurrency {
			result.MaxConcurrency = int32(s.MaxConcurrency)
		}
		if s.HasMemoryBytes {
			result.MemoryBytes = s.MemoryBytes
		}
		if s.HasWorkers {
			result.Workers = int32(s.Workers)
		}
		if s.HasPriority {
			result.Priority = int32(s.Priority)
		}
		if _, err := catalog.EncodeResourceGroup(result); err != nil {
			return nil, true, err
		}
		return AlterResourceGroup{Group: group, Result: result}, true, nil
	case ast.DropResourceGroup:
		group, ok := groups(s.Name)
		if !ok {
			if s.IfExists {
				return DropResourceGroup{IfExists: true}, true, nil
			}
			return nil, true, nerr.New(nerr.NotFound, "sql.binder", "resource group not found")
		}
		return DropResourceGroup{Group: group, IfExists: s.IfExists}, true, nil
	default:
		return nil, false, nil
	}
}
