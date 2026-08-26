package executor

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

func (s *Session) execWith(p planner.With) (*Result, error) {
	ids := make([]uint64, 0, len(p.CTEs))
	defer s.dropCTEs(ids)
	for _, cte := range p.CTEs {
		var rows [][]types.Value
		var err error
		if cte.RecursiveOn {
			rows, err = s.execRecursiveCTE(cte)
		} else {
			rows, err = s.collectPlan(cte.Input)
		}
		if err != nil {
			return nil, err
		}
		s.putCTE(cte.ID, rows)
		ids = append(ids, cte.ID)
	}
	return s.execPlan(p.Query)
}

func (s *Session) execRecursiveCTE(cte planner.CTE) ([][]types.Value, error) {
	anchor, err := s.collectPlan(cte.Anchor)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var result [][]types.Value
	add := func(rows [][]types.Value) ([][]types.Value, error) {
		var next [][]types.Value
		for _, row := range rows {
			if err := s.budget().Check(); err != nil {
				return nil, err
			}
			if len(result)+len(next) >= s.budget().ResultRows() {
				return nil, nerr.New(nerr.Exhausted, "executor.recursiveCTE", "recursive CTE exceeds row limit")
			}
			if cte.Distinct {
				key, err := types.EncodeRow(row)
				if err != nil {
					return nil, err
				}
				if _, ok := seen[string(key)]; ok {
					continue
				}
				if err := s.budget().ChargeMem(int64(len(key) + 16)); err != nil {
					return nil, err
				}
				seen[string(key)] = struct{}{}
			}
			rowBytes := int64(len(row) * 16)
			for _, v := range row {
				rowBytes += int64(len(v.Str) + len(v.JSON) + 4*len(v.Vec))
			}
			if err := s.budget().ChargeMem(rowBytes); err != nil {
				return nil, err
			}
			next = append(next, row)
		}
		result = append(result, next...)
		return next, nil
	}
	working, err := add(anchor)
	if err != nil {
		return nil, err
	}
	for depth := 0; len(working) > 0; depth++ {
		if depth >= security.MaxRecursiveDepth {
			return nil, nerr.New(nerr.Exhausted, "executor.recursiveCTE", "recursive CTE exceeds depth limit")
		}
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		s.putCTE(cte.ID, working)
		rec, err := s.collectPlan(cte.Recursive)
		if err != nil {
			return nil, err
		}
		working, err = add(rec)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Session) putCTE(id uint64, rows [][]types.Value) {
	if s.cteRows == nil {
		s.cteRows = make(map[uint64][][]types.Value)
	}
	s.cteRows[id] = rows
}

func (s *Session) getCTE(id uint64) ([][]types.Value, bool) {
	if s == nil || s.cteRows == nil {
		return nil, false
	}
	rows, ok := s.cteRows[id]
	return rows, ok
}

func (s *Session) dropCTEs(ids []uint64) {
	if s == nil || s.cteRows == nil {
		return
	}
	for _, id := range ids {
		delete(s.cteRows, id)
	}
}

func (s *Session) scanCTE(n planner.CTEScan) ([][]types.Value, error) {
	rows, ok := s.getCTE(n.ID)
	if !ok {
		return nil, nerr.New(nerr.Internal, "executor.cteScan", "CTE is not materialized")
	}
	out := make([][]types.Value, len(rows))
	copy(out, rows)
	return out, nil
}
