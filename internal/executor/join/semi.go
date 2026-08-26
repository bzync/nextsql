package join

import (
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
)

// HashSemiJoin emits each left row at most once when a matching right row exists.
func HashSemiJoin(left, right [][]types.Value, lKeys, rKeys []int, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	return hashMark(left, right, lKeys, rKeys, false, pred, b)
}

// HashAntiJoin emits each left row that has no matching right row.
func HashAntiJoin(left, right [][]types.Value, lKeys, rKeys []int, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	return hashMark(left, right, lKeys, rKeys, true, pred, b)
}

func hashMark(left, right [][]types.Value, lKeys, rKeys []int, anti bool, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	if b == nil {
		b = scheduler.NewBudget(nil, scheduler.DefaultLimits())
	}
	if err := b.Check(); err != nil {
		return nil, err
	}
	if len(lKeys) == 0 && len(rKeys) == 0 {
		return nestedMark(left, right, anti, pred, b)
	}
	ht, err := build(right, rKeys, b)
	if err != nil {
		return markWithSpill(left, right, lKeys, rKeys, anti, pred, b)
	}
	var out [][]types.Value
	for _, lrow := range left {
		if err := b.Check(); err != nil {
			return nil, err
		}
		hit := false
		if !unmatchedKey(lrow, lKeys) {
			ks, err := keyString(lrow, lKeys)
			if err != nil {
				return nil, err
			}
			for _, rrow := range ht.keys[ks] {
				ok := true
				if pred != nil {
					ok, err = pred(lrow, rrow)
					if err != nil {
						return nil, err
					}
				}
				if ok {
					hit = true
					break
				}
			}
		}
		if anti == !hit {
			if err := b.ChargeMem(int64(16 * len(lrow))); err != nil {
				return nil, err
			}
			out = append(out, lrow)
		}
	}
	return out, nil
}

func nestedMark(left, right [][]types.Value, anti bool, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	if pred == nil {
		if len(right) == 0 {
			if anti {
				return chargeLeft(left, b)
			}
			return nil, nil
		}
		if anti {
			return nil, nil
		}
		return chargeLeft(left, b)
	}
	var out [][]types.Value
	for _, lrow := range left {
		if err := b.Check(); err != nil {
			return nil, err
		}
		hit := false
		for _, rrow := range right {
			ok, err := pred(lrow, rrow)
			if err != nil {
				return nil, err
			}
			if ok {
				hit = true
				break
			}
		}
		if anti == !hit {
			if err := b.ChargeMem(int64(16 * len(lrow))); err != nil {
				return nil, err
			}
			out = append(out, lrow)
		}
	}
	return out, nil
}

func chargeLeft(left [][]types.Value, b *scheduler.Budget) ([][]types.Value, error) {
	var out [][]types.Value
	for _, lrow := range left {
		if err := b.Check(); err != nil {
			return nil, err
		}
		if err := b.ChargeMem(int64(16 * len(lrow))); err != nil {
			return nil, err
		}
		out = append(out, lrow)
	}
	return out, nil
}

func markWithSpill(left, right [][]types.Value, lKeys, rKeys []int, anti bool, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	rTypes := inferTypes(right, nil)
	if len(right) == 0 {
		if anti {
			return chargeLeft(left, b)
		}
		return nil, nil
	}
	sp, err := scheduler.NewSpill(b)
	if err != nil {
		return nil, err
	}
	defer sp.Close()
	const parts = 8
	for _, row := range right {
		if unmatchedKey(row, rKeys) {
			continue
		}
		ks, err := keyString(row, rKeys)
		if err != nil {
			return nil, err
		}
		if err := sp.Write(partition(ks, parts), [][]types.Value{row}); err != nil {
			return nil, err
		}
	}
	var out [][]types.Value
	buckets := make([][][]types.Value, parts)
	loaded := make([]bool, parts)
	for _, lrow := range left {
		if err := b.Check(); err != nil {
			return nil, err
		}
		hit := false
		if !unmatchedKey(lrow, lKeys) {
			ks, err := keyString(lrow, lKeys)
			if err != nil {
				return nil, err
			}
			p := partition(ks, parts)
			if !loaded[p] {
				raws, err := sp.ReadRaw(p)
				if err != nil {
					return nil, err
				}
				var rows [][]types.Value
				for _, raw := range raws {
					row, err := types.DecodeRow(raw, rTypes)
					if err != nil {
						return nil, err
					}
					rows = append(rows, row)
				}
				buckets[p] = rows
				loaded[p] = true
			}
			for _, rrow := range buckets[p] {
				rks, err := keyString(rrow, rKeys)
				if err != nil {
					return nil, err
				}
				if rks != ks {
					continue
				}
				ok := true
				if pred != nil {
					ok, err = pred(lrow, rrow)
					if err != nil {
						return nil, err
					}
				}
				if ok {
					hit = true
					break
				}
			}
		}
		if anti == !hit {
			if err := b.ChargeMem(int64(16 * len(lrow))); err != nil {
				return nil, err
			}
			out = append(out, lrow)
		}
	}
	return out, nil
}
