package join

import (
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// MergeJoin joins two inputs already sorted on the join keys.
func MergeJoin(left, right [][]types.Value, lKeys, rKeys []int, kind ast.JoinKind, rTypes []types.Type, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	if b == nil {
		b = scheduler.NewBudget(nil, scheduler.DefaultLimits())
	}
	rTypes = inferTypes(right, rTypes)
	var out [][]types.Value
	i, j := 0, 0
	emitLeft := func(lrow []types.Value) error {
		row := nullExtend(lrow, rTypes)
		if err := b.ChargeMem(int64(16 * len(row))); err != nil {
			return err
		}
		out = append(out, row)
		return nil
	}
	for i < len(left) && j < len(right) {
		if err := b.Check(); err != nil {
			return nil, err
		}
		if unmatchedKey(left[i], lKeys) {
			if isLeft(kind) {
				if err := emitLeft(left[i]); err != nil {
					return nil, err
				}
			}
			i++
			continue
		}
		if unmatchedKey(right[j], rKeys) {
			j++
			continue
		}
		c, err := cmpKeys(left[i], right[j], lKeys, rKeys)
		if err != nil {
			return nil, err
		}
		switch {
		case c < 0:
			if isLeft(kind) {
				if err := emitLeft(left[i]); err != nil {
					return nil, err
				}
			}
			i++
		case c > 0:
			j++
		default:
			// equal group
			i0 := i
			for i < len(left) {
				if unmatchedKey(left[i], lKeys) {
					break
				}
				eq, err := cmpKeys(left[i], left[i0], lKeys, lKeys)
				if err != nil {
					return nil, err
				}
				if eq != 0 {
					break
				}
				i++
			}
			j0 := j
			for j < len(right) {
				if unmatchedKey(right[j], rKeys) {
					break
				}
				eq, err := cmpKeys(right[j], right[j0], rKeys, rKeys)
				if err != nil {
					return nil, err
				}
				if eq != 0 {
					break
				}
				j++
			}
			for a := i0; a < i; a++ {
				matched := false
				for d := j0; d < j; d++ {
					ok := true
					if pred != nil {
						ok, err = pred(left[a], right[d])
						if err != nil {
							return nil, err
						}
					}
					if ok {
						if err := b.ChargeMem(int64(16 * (len(left[a]) + len(right[d])))); err != nil {
							return nil, err
						}
						out = append(out, concat(left[a], right[d]))
						matched = true
					}
				}
				if !matched && isLeft(kind) {
					if err := emitLeft(left[a]); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if isLeft(kind) {
		for ; i < len(left); i++ {
			if err := b.Check(); err != nil {
				return nil, err
			}
			if err := emitLeft(left[i]); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// cmpKeys is a total order for merge grouping (NULL-first).
// A NULL component is never a join match; callers must check unmatchedKey
// before emitting an equal group.
func cmpKeys(a, b []types.Value, aCols, bCols []int) (int, error) {
	n := len(aCols)
	if len(bCols) < n {
		n = len(bCols)
	}
	for i := 0; i < n; i++ {
		av, bv := a[aCols[i]], b[bCols[i]]
		if av.Null || bv.Null {
			if av.Null && bv.Null {
				continue
			}
			if av.Null {
				return -1, nil
			}
			return 1, nil
		}
		c, err := av.Cmp(bv)
		if err != nil {
			return 0, err
		}
		if c != 0 {
			return c, nil
		}
	}
	return 0, nil
}

// Sorted reports whether rows are non-decreasing on cols.
func Sorted(rows [][]types.Value, cols []int) bool {
	for i := 1; i < len(rows); i++ {
		c, err := cmpKeys(rows[i-1], rows[i], cols, cols)
		if err != nil || c > 0 {
			return false
		}
	}
	return true
}
