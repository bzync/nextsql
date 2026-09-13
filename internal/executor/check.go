package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// checkRowConstraints evaluates every CHECK constraint on tab against one row.
//
// SQL refuses a row only when a check evaluates to FALSE: UNKNOWN satisfies the
// constraint. That is why `CHECK (n > 0)` admits a NULL n — NOT NULL is the
// constraint that rejects it — and the rule is deliberate here rather than
// incidental, since it decides what an existing NULL column does when a check
// is added over it.
//
// Checks run on the leader as part of the writing transaction, before the row
// reaches the heap, so a violation aborts the statement with nothing written.
// Followers apply the resulting WAL and never re-evaluate the predicate, which
// is why the binder refuses a non-deterministic one.
func (s *Session) checkRowConstraints(tab *catalog.Table, row []types.Value) error {
	if tab == nil || len(tab.Checks) == 0 {
		return nil
	}
	for _, c := range tab.Checks {
		v, err := s.eval(c.Expr, tab, row)
		if err != nil {
			return err
		}
		if v.Null {
			continue
		}
		if v.Typ.Kind != types.KindBool {
			return nerr.New(nerr.InvalidArgument, "executor.check", "CHECK "+c.Name+" is not a boolean predicate")
		}
		if !v.Bool {
			return nerr.New(nerr.InvalidArgument, "executor.check", "CHECK constraint "+c.Name+" violated")
		}
	}
	return nil
}

// validateExistingChecks re-evaluates checks over every existing row. ALTER
// TABLE ADD CONSTRAINT ... CHECK uses it so a constraint can never be added
// over data that contradicts it — otherwise the table would hold rows no write
// could reproduce, and a later export/restore would fail on its own data.
func (s *Session) validateExistingChecks(tab *catalog.Table, added []catalog.Check) error {
	if tab == nil || len(added) == 0 {
		return nil
	}
	probe := *tab
	probe.Checks = added
	heap, err := s.heapOf(tab)
	if err != nil {
		return err
	}
	htx := s.x.use(heap)
	return htx.Range(nil, nil, func(_, val []byte) error {
		row, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		return s.checkRowConstraints(&probe, row)
	})
}
