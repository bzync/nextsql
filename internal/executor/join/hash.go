package join

import (
	"strconv"
	"sync/atomic"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// unmatchedSeq distinguishes NULL join keys so they never share a hash bucket.
var unmatchedSeq atomic.Uint64

// unmatchedKey reports that this row must not match any other row.
// SQL: NULL = x is unknown.
func unmatchedKey(row []types.Value, cols []int) bool {
	for _, c := range cols {
		if c >= 0 && c < len(row) && row[c].Null {
			return true
		}
	}
	return false
}

// Pred is an optional residual predicate on concatenated (left||right) rows.
type Pred func(left, right []types.Value) (bool, error)

type table struct {
	keys map[string][][]types.Value
}

// Table is a hash-join build side.
type Table = table

// Build hashes rows by keyCols.
func Build(rows [][]types.Value, keyCols []int, b *scheduler.Budget) (*Table, error) {
	return build(rows, keyCols, b)
}

// ProbeCount is the number of build rows that match row's join keys.
func (t *Table) ProbeCount(row []types.Value, keyCols []int) (int, error) {
	if t == nil {
		return 0, nil
	}
	if unmatchedKey(row, keyCols) {
		return 0, nil
	}
	ks, err := keyString(row, keyCols)
	if err != nil {
		return 0, err
	}
	return len(t.keys[ks]), nil
}

func build(rows [][]types.Value, keyCols []int, b *scheduler.Budget) (*table, error) {
	t := &table{keys: make(map[string][][]types.Value)}
	for _, row := range rows {
		if unmatchedKey(row, keyCols) {
			continue
		}
		ks, err := keyString(row, keyCols)
		if err != nil {
			return nil, err
		}
		est := int64(64 + len(ks) + 16*len(row))
		if err := b.ChargeMem(est); err != nil {
			return nil, err
		}
		t.keys[ks] = append(t.keys[ks], clone(row))
	}
	return t, nil
}

func keyString(row []types.Value, cols []int) (string, error) {
	if len(cols) == 0 {
		return "", nil
	}
	if unmatchedKey(row, cols) {
		n := unmatchedSeq.Add(1)
		return "\x00unmatched\x00" + strconv.FormatUint(n, 10), nil
	}
	vals := make([]types.Value, len(cols))
	for i, c := range cols {
		if c < 0 || c >= len(row) {
			return "", nerr.New(nerr.Internal, "join.keyString", "join key out of range")
		}
		vals[i] = row[c]
	}
	enc, err := types.EncodeKey(vals)
	if err != nil {
		return "", err
	}
	return string(enc), nil
}

func clone(row []types.Value) []types.Value {
	out := make([]types.Value, len(row))
	for i := range row {
		out[i] = row[i].Clone()
	}
	return out
}

func concat(a, b []types.Value) []types.Value {
	out := make([]types.Value, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func inferTypes(rows [][]types.Value, hint []types.Type) []types.Type {
	if len(hint) > 0 {
		return hint
	}
	if len(rows) == 0 {
		return nil
	}
	out := make([]types.Type, len(rows[0]))
	for i, v := range rows[0] {
		out[i] = v.Typ
	}
	return out
}

func nullExtend(left []types.Value, rTypes []types.Type) []types.Value {
	out := make([]types.Value, 0, len(left)+len(rTypes))
	out = append(out, left...)
	for _, ty := range rTypes {
		out = append(out, types.Null(ty))
	}
	return out
}

func isLeft(kind ast.JoinKind) bool { return kind == ast.JoinLeft }
func isFull(kind ast.JoinKind) bool { return kind == ast.JoinFull }
func isOuter(kind ast.JoinKind) bool {
	return kind == ast.JoinLeft || kind == ast.JoinFull
}

func nullPrefix(right []types.Value, lTypes []types.Type) []types.Value {
	out := make([]types.Value, 0, len(lTypes)+len(right))
	for _, ty := range lTypes {
		out = append(out, types.Null(ty))
	}
	out = append(out, right...)
	return out
}

func fullNoSpill() error {
	return nerr.New(nerr.Exhausted, "join.HashJoin", "FULL OUTER JOIN exceeds memory")
}

// HashJoin builds a hash table on right and probes with left.
func HashJoin(left, right [][]types.Value, lKeys, rKeys []int, kind ast.JoinKind, lTypes, rTypes []types.Type, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	if b == nil {
		b = scheduler.NewBudget(nil, scheduler.DefaultLimits())
	}
	if err := b.Check(); err != nil {
		return nil, err
	}
	lTypes = inferTypes(left, lTypes)
	rTypes = inferTypes(right, rTypes)
	if len(lKeys) == 0 && len(rKeys) == 0 {
		if isFull(kind) {
			return nestedFull(left, right, lTypes, rTypes, pred, b)
		}
		if isLeft(kind) {
			return nestedLeft(left, right, rTypes, pred, b)
		}
		return nested(left, right, pred, b)
	}
	if isFull(kind) {
		return hashFull(left, right, lKeys, rKeys, lTypes, rTypes, pred, b)
	}
	ht, err := build(right, rKeys, b)
	if err != nil {
		// spill right side and probe from disk
		return hashWithSpill(left, right, lKeys, rKeys, kind, rTypes, pred, b)
	}
	var out [][]types.Value
	for _, lrow := range left {
		if err := b.Check(); err != nil {
			return nil, err
		}
		if unmatchedKey(lrow, lKeys) {
			if isLeft(kind) {
				row := nullExtend(lrow, rTypes)
				if err := b.ChargeMem(int64(16 * len(row))); err != nil {
					return nil, err
				}
				out = append(out, row)
			}
			continue
		}
		ks, err := keyString(lrow, lKeys)
		if err != nil {
			return nil, err
		}
		matched := false
		for _, rrow := range ht.keys[ks] {
			ok := true
			if pred != nil {
				ok, err = pred(lrow, rrow)
				if err != nil {
					return nil, err
				}
			}
			if ok {
				row := concat(lrow, rrow)
				if err := b.ChargeMem(int64(16 * len(row))); err != nil {
					return nil, err
				}
				out = append(out, row)
				matched = true
			}
		}
		if !matched && isLeft(kind) {
			row := nullExtend(lrow, rTypes)
			if err := b.ChargeMem(int64(16 * len(row))); err != nil {
				return nil, err
			}
			out = append(out, row)
		}
	}
	return out, nil
}

func hashFull(left, right [][]types.Value, lKeys, rKeys []int, lTypes, rTypes []types.Type, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	// v1 FULL is memory-capped: refuse rather than spill.
	idx := make(map[string][]int)
	for i, row := range right {
		if unmatchedKey(row, rKeys) {
			continue
		}
		ks, err := keyString(row, rKeys)
		if err != nil {
			return nil, err
		}
		est := int64(64 + len(ks) + 8)
		if err := b.ChargeMem(est); err != nil {
			return nil, fullNoSpill()
		}
		idx[ks] = append(idx[ks], i)
	}
	if err := b.ChargeMem(int64(len(right) + 8)); err != nil {
		return nil, fullNoSpill()
	}
	matched := make([]bool, len(right))
	var out [][]types.Value
	for _, lrow := range left {
		if err := b.Check(); err != nil {
			return nil, err
		}
		if unmatchedKey(lrow, lKeys) {
			row := nullExtend(lrow, rTypes)
			if err := b.ChargeMem(int64(16 * len(row))); err != nil {
				return nil, fullNoSpill()
			}
			out = append(out, row)
			continue
		}
		ks, err := keyString(lrow, lKeys)
		if err != nil {
			return nil, err
		}
		hit := false
		for _, ri := range idx[ks] {
			ok := true
			if pred != nil {
				ok, err = pred(lrow, right[ri])
				if err != nil {
					return nil, err
				}
			}
			if ok {
				row := concat(lrow, right[ri])
				if err := b.ChargeMem(int64(16 * len(row))); err != nil {
					return nil, fullNoSpill()
				}
				out = append(out, row)
				matched[ri] = true
				hit = true
			}
		}
		if !hit {
			row := nullExtend(lrow, rTypes)
			if err := b.ChargeMem(int64(16 * len(row))); err != nil {
				return nil, fullNoSpill()
			}
			out = append(out, row)
		}
	}
	for i, rrow := range right {
		if matched[i] {
			continue
		}
		if err := b.Check(); err != nil {
			return nil, err
		}
		row := nullPrefix(rrow, lTypes)
		if err := b.ChargeMem(int64(16 * len(row))); err != nil {
			return nil, fullNoSpill()
		}
		out = append(out, row)
	}
	return out, nil
}

func nested(left, right [][]types.Value, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	var out [][]types.Value
	for _, lrow := range left {
		for _, rrow := range right {
			if err := b.Check(); err != nil {
				return nil, err
			}
			ok := true
			var err error
			if pred != nil {
				ok, err = pred(lrow, rrow)
				if err != nil {
					return nil, err
				}
			}
			if ok {
				if err := b.ChargeMem(int64(16 * (len(lrow) + len(rrow)))); err != nil {
					return nil, err
				}
				out = append(out, concat(lrow, rrow))
			}
		}
	}
	return out, nil
}

func nestedLeft(left, right [][]types.Value, rTypes []types.Type, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	var out [][]types.Value
	for _, lrow := range left {
		if err := b.Check(); err != nil {
			return nil, err
		}
		matched := false
		for _, rrow := range right {
			ok := true
			var err error
			if pred != nil {
				ok, err = pred(lrow, rrow)
				if err != nil {
					return nil, err
				}
			}
			if ok {
				if err := b.ChargeMem(int64(16 * (len(lrow) + len(rrow)))); err != nil {
					return nil, err
				}
				out = append(out, concat(lrow, rrow))
				matched = true
			}
		}
		if !matched {
			row := nullExtend(lrow, rTypes)
			if err := b.ChargeMem(int64(16 * len(row))); err != nil {
				return nil, err
			}
			out = append(out, row)
		}
	}
	return out, nil
}

func nestedFull(left, right [][]types.Value, lTypes, rTypes []types.Type, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	if err := b.ChargeMem(int64(len(left) + len(right) + 16)); err != nil {
		return nil, fullNoSpill()
	}
	leftHit := make([]bool, len(left))
	rightHit := make([]bool, len(right))
	var out [][]types.Value
	for i, lrow := range left {
		for j, rrow := range right {
			if err := b.Check(); err != nil {
				return nil, err
			}
			ok := true
			var err error
			if pred != nil {
				ok, err = pred(lrow, rrow)
				if err != nil {
					return nil, err
				}
			}
			if ok {
				if err := b.ChargeMem(int64(16 * (len(lrow) + len(rrow)))); err != nil {
					return nil, fullNoSpill()
				}
				out = append(out, concat(lrow, rrow))
				leftHit[i] = true
				rightHit[j] = true
			}
		}
	}
	for i, lrow := range left {
		if leftHit[i] {
			continue
		}
		row := nullExtend(lrow, rTypes)
		if err := b.ChargeMem(int64(16 * len(row))); err != nil {
			return nil, fullNoSpill()
		}
		out = append(out, row)
	}
	for j, rrow := range right {
		if rightHit[j] {
			continue
		}
		row := nullPrefix(rrow, lTypes)
		if err := b.ChargeMem(int64(16 * len(row))); err != nil {
			return nil, fullNoSpill()
		}
		out = append(out, row)
	}
	return out, nil
}

func hashWithSpill(left, right [][]types.Value, lKeys, rKeys []int, kind ast.JoinKind, rTypes []types.Type, pred Pred, b *scheduler.Budget) ([][]types.Value, error) {
	if isFull(kind) {
		return nil, fullNoSpill()
	}
	rTypes = inferTypes(right, rTypes)
	if len(right) == 0 {
		if !isLeft(kind) {
			return nil, nil
		}
		var out [][]types.Value
		for _, lrow := range left {
			if err := b.Check(); err != nil {
				return nil, err
			}
			row := nullExtend(lrow, rTypes)
			if err := b.ChargeMem(int64(16 * len(row))); err != nil {
				return nil, err
			}
			out = append(out, row)
		}
		return out, nil
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
		p := partition(ks, parts)
		if err := sp.Write(p, [][]types.Value{row}); err != nil {
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
		if unmatchedKey(lrow, lKeys) {
			if isLeft(kind) {
				row := nullExtend(lrow, rTypes)
				if err := b.ChargeMem(int64(16 * len(row))); err != nil {
					return nil, err
				}
				out = append(out, row)
			}
			continue
		}
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
		matched := false
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
				row := concat(lrow, rrow)
				if err := b.ChargeMem(int64(16 * len(row))); err != nil {
					return nil, err
				}
				out = append(out, row)
				matched = true
			}
		}
		if !matched && isLeft(kind) {
			row := nullExtend(lrow, rTypes)
			if err := b.ChargeMem(int64(16 * len(row))); err != nil {
				return nil, err
			}
			out = append(out, row)
		}
	}
	return out, nil
}

func partition(ks string, n int) int {
	h := 2166136261
	for i := 0; i < len(ks); i++ {
		h ^= int(ks[i])
		h *= 16777619
	}
	if h < 0 {
		h = -h
	}
	return h % n
}

// ParallelHash partitions both sides and joins each partition on a worker.
func ParallelHash(pool *scheduler.Pool, b *scheduler.Budget, left, right [][]types.Value, lKeys, rKeys []int, kind ast.JoinKind, lTypes, rTypes []types.Type, pred Pred) ([][]types.Value, error) {
	n := b.Workers()
	if n < 2 || (len(left)+len(right)) < 64 {
		return HashJoin(left, right, lKeys, rKeys, kind, lTypes, rTypes, pred, b)
	}
	lTypes = inferTypes(left, lTypes)
	rTypes = inferTypes(right, rTypes)
	lp := make([][][]types.Value, n)
	rp := make([][][]types.Value, n)
	var unmatchedLeft [][]types.Value
	var unmatchedRight [][]types.Value
	for _, row := range left {
		if unmatchedKey(row, lKeys) {
			if isOuter(kind) {
				unmatchedLeft = append(unmatchedLeft, row)
			}
			continue
		}
		ks, err := keyString(row, lKeys)
		if err != nil {
			return nil, err
		}
		p := partition(ks, n)
		lp[p] = append(lp[p], row)
	}
	for _, row := range right {
		if unmatchedKey(row, rKeys) {
			if isFull(kind) {
				unmatchedRight = append(unmatchedRight, row)
			}
			continue
		}
		ks, err := keyString(row, rKeys)
		if err != nil {
			return nil, err
		}
		p := partition(ks, n)
		rp[p] = append(rp[p], row)
	}
	outs := make([][][]types.Value, n)
	tasks := make([]func() error, n)
	for i := 0; i < n; i++ {
		i := i
		tasks[i] = func() error {
			got, err := HashJoin(lp[i], rp[i], lKeys, rKeys, kind, lTypes, rTypes, pred, b)
			if err != nil {
				return err
			}
			outs[i] = got
			return nil
		}
	}
	if err := pool.Run(b.Context(), n, tasks); err != nil {
		return nil, err
	}
	var all [][]types.Value
	for _, p := range outs {
		all = append(all, p...)
	}
	if isOuter(kind) {
		for _, lrow := range unmatchedLeft {
			row := nullExtend(lrow, rTypes)
			if err := b.ChargeMem(int64(16 * len(row))); err != nil {
				if isFull(kind) {
					return nil, fullNoSpill()
				}
				return nil, err
			}
			all = append(all, row)
		}
	}
	if isFull(kind) {
		for _, rrow := range unmatchedRight {
			row := nullPrefix(rrow, lTypes)
			if err := b.ChargeMem(int64(16 * len(row))); err != nil {
				return nil, fullNoSpill()
			}
			all = append(all, row)
		}
	}
	return all, nil
}
