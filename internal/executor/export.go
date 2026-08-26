package executor

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/txn"
)

// ForEachVisible visits every snapshot-visible row of table, hydrating
// detached VECTOR payloads. If no transaction is active a snapshot is
// started and committed around the scan.
func (s *Session) ForEachVisible(name string, fn func([]types.Value) error) error {
	if s == nil || s.db == nil {
		return nerr.New(nerr.InvalidArgument, "executor.ForEachVisible", "nil session")
	}
	if fn == nil {
		return nerr.New(nerr.InvalidArgument, "executor.ForEachVisible", "nil callback")
	}
	tab, ok := s.lookup(name)
	if !ok {
		return nerr.New(nerr.NotFound, "executor.ForEachVisible", "unknown table")
	}
	auto := false
	if s.x == nil {
		if err := s.start(txn.SnapshotIsolation); err != nil {
			return err
		}
		auto = true
	}
	err := s.scanHeap(tab, nil, nil, true, false, func(row []types.Value) error {
		if !s.tenantVisible(tab, row) {
			return nil
		}
		return fn(row)
	})
	if auto {
		if err != nil {
			_ = s.abort()
			return err
		}
		if _, cerr := s.commit(); cerr != nil {
			return cerr
		}
	}
	return err
}
