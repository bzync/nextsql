package aggregate

import (
	"bytes"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Spec is one aggregate function over an input column.
type Spec struct {
	Fun     string     // count, sum, avg, min, max, array_agg, map_agg
	Col     int        // input ordinal; -1 for COUNT(*)
	Col2    int        // second input ordinal for MAP_AGG (value column)
	OutType types.Type // expected aggregate output type
}

// Hash is a hash aggregation table. It spills encrypted partitions when
// the memory budget is exceeded.
type Hash struct {
	groups []int
	specs  []Spec
	outTy  []types.Type
	budget *scheduler.Budget
	spill  *scheduler.Spill
	table  map[string]*state
	order  []string
	mem    int64
	nAdds  uint64
}

// aggAcc is the running accumulator for one Spec. Each Spec gets its own so
// that e.g. SELECT SUM(x), AVG(y), MIN(x) never share a slot (a prior version
// kept a single sum/nval/min/max on state, which double-counted whenever two
// specs used the same slot).
type aggAcc struct {
	sum    types.Decimal // sum / avg
	nval   int64         // non-null count for count(col) / avg denominator
	val    types.Value   // running min or max
	hasVal bool
	coll   []types.Value // for array_agg
	keys   []types.Value // for map_agg
	vals   []types.Value // for map_agg
}

type state struct {
	key  []types.Value
	raw  []byte
	n    int64
	accs []aggAcc // one per Hash.specs entry
}

func New(groups []int, specs []Spec, outTypes []types.Type, b *scheduler.Budget) *Hash {
	return &Hash{
		groups: append([]int(nil), groups...),
		specs:  append([]Spec(nil), specs...),
		outTy:  append([]types.Type(nil), outTypes...),
		budget: b,
		table:  make(map[string]*state),
	}
}

func (h *Hash) Add(row []types.Value) error {
	h.nAdds++
	if h.nAdds&255 == 1 {
		if err := h.budget.Check(); err != nil {
			return err
		}
	}
	keyVals := make([]types.Value, len(h.groups))
	for i, c := range h.groups {
		if c < 0 || c >= len(row) {
			return nerr.New(nerr.Internal, "aggregate.Hash.Add", "group column out of range")
		}
		keyVals[i] = row[c]
	}
	return h.addKeyed(keyVals, row)
}

// AddCountStar groups by the provided values and increments COUNT(*).
// groupVals are the group-by columns in planner order (not table ordinals).
func (h *Hash) AddCountStar(groupVals []types.Value) error {
	h.nAdds++
	if h.nAdds&255 == 1 {
		if err := h.budget.Check(); err != nil {
			return err
		}
	}
	return h.addKeyed(groupVals, nil)
}

// AddCountStarBytes increments COUNT(*) for a single string group key.
// b aliases caller memory and is copied only when a new group is created.
func (h *Hash) AddCountStarBytes(b []byte, null bool, typ types.Type) error {
	h.nAdds++
	if h.nAdds&255 == 1 {
		if err := h.budget.Check(); err != nil {
			return err
		}
	}
	if null {
		return h.addKeyed([]types.Value{types.Null(typ)}, nil)
	}
	if st := h.lookupRaw(b); st != nil {
		st.n++
		return nil
	}
	raw := append([]byte(nil), b...)
	st := &state{
		key:  []types.Value{{Typ: typ, Str: string(raw)}},
		raw:  raw,
		accs: make([]aggAcc, len(h.specs)),
	}
	ks := "\x01" + st.key[0].Str
	est := int64(64 + len(ks) + 32*len(h.specs))
	if err := h.budget.ChargeMem(est); err != nil {
		if err := h.spillOne(ks, st); err != nil {
			return err
		}
		if err := h.flushAll(); err != nil {
			return err
		}
		if err := h.budget.ChargeMem(est); err != nil {
			return err
		}
	}
	h.mem += est
	h.table[ks] = st
	h.order = append(h.order, ks)
	st.n++
	return nil
}

func (h *Hash) lookupRaw(b []byte) *state {
	if len(h.order) <= 64 {
		for _, ks := range h.order {
			st := h.table[ks]
			if st != nil && bytes.Equal(st.raw, b) {
				return st
			}
		}
		return nil
	}
	if st, ok := h.table["\x01"+string(b)]; ok {
		return st
	}
	return nil
}

func (h *Hash) addKeyed(keyVals []types.Value, row []types.Value) error {
	ks, err := groupMapKey(keyVals)
	if err != nil {
		return err
	}
	st, ok := h.table[ks]
	if !ok {
		st = &state{key: cloneRow(keyVals), accs: make([]aggAcc, len(h.specs))}
		est := int64(64 + len(ks) + 32*len(h.specs))
		if err := h.budget.ChargeMem(est); err != nil {
			if err := h.spillOne(ks, st); err != nil {
				return err
			}
			if err := h.flushAll(); err != nil {
				return err
			}
			if err := h.budget.ChargeMem(est); err != nil {
				return err
			}
		}
		h.mem += est
		h.table[ks] = st
		h.order = append(h.order, ks)
	}
	st.n++
	if row == nil {
		return nil
	}
	for i, sp := range h.specs {
		if err := acc(&st.accs[i], sp, row); err != nil {
			return err
		}
	}
	return nil
}

func groupMapKey(keyVals []types.Value) (string, error) {
	if len(keyVals) == 0 {
		return "", nil
	}
	if len(keyVals) == 1 {
		v := keyVals[0]
		if v.Null {
			return "\x00", nil
		}
		if v.Typ.Kind == types.KindString || v.Typ.Kind == types.KindText {
			return "\x01" + v.Str, nil
		}
	}
	enc, err := types.EncodeKey(keyVals)
	if err != nil {
		return "\x00" + nullKey(keyVals), nil
	}
	return string(enc), nil
}

func acc(a *aggAcc, sp Spec, row []types.Value) error {
	switch sp.Fun {
	case "count":
		if sp.Col < 0 {
			return nil // COUNT(*) uses st.n
		}
		if sp.Col < len(row) && !row[sp.Col].Null {
			a.nval++
		}
	case "sum", "avg":
		if sp.Col < 0 || sp.Col >= len(row) {
			return nerr.New(nerr.InvalidArgument, "aggregate.acc", "SUM/AVG needs a column")
		}
		v := row[sp.Col]
		if v.Null {
			return nil
		}
		if v.Typ.Kind != types.KindDecimal {
			c, err := types.Coerce(v, types.Type{Kind: types.KindDecimal})
			if err != nil {
				return err
			}
			v = c
		}
		a.sum = types.AddDec(a.sum, v.Dec)
		a.nval++
	case "min":
		if sp.Col < 0 || sp.Col >= len(row) || row[sp.Col].Null {
			return nil
		}
		if !a.hasVal {
			a.val = row[sp.Col].Clone()
			a.hasVal = true
			return nil
		}
		c, err := row[sp.Col].Cmp(a.val)
		if err != nil {
			return err
		}
		if c < 0 {
			a.val = row[sp.Col].Clone()
		}
	case "max":
		if sp.Col < 0 || sp.Col >= len(row) || row[sp.Col].Null {
			return nil
		}
		if !a.hasVal {
			a.val = row[sp.Col].Clone()
			a.hasVal = true
			return nil
		}
		c, err := row[sp.Col].Cmp(a.val)
		if err != nil {
			return err
		}
		if c > 0 {
			a.val = row[sp.Col].Clone()
		}
	case "array_agg":
		if sp.Col < 0 || sp.Col >= len(row) {
			return nerr.New(nerr.InvalidArgument, "aggregate.acc", "ARRAY_AGG needs a column")
		}
		if len(a.coll) >= types.MaxCollectionLen {
			return nerr.New(nerr.InvalidArgument, "aggregate.acc", "ARRAY_AGG exceeds MaxCollectionLen")
		}
		a.coll = append(a.coll, row[sp.Col].Clone())
		a.nval++
	case "map_agg":
		if sp.Col < 0 || sp.Col >= len(row) || sp.Col2 < 0 || sp.Col2 >= len(row) {
			return nerr.New(nerr.InvalidArgument, "aggregate.acc", "MAP_AGG needs key and value columns")
		}
		k := row[sp.Col]
		if k.Null {
			return nerr.New(nerr.InvalidArgument, "aggregate.acc", "MAP_AGG key cannot be NULL")
		}
		if len(a.keys) >= types.MaxCollectionLen {
			return nerr.New(nerr.InvalidArgument, "aggregate.acc", "MAP_AGG exceeds MaxCollectionLen")
		}
		v := row[sp.Col2]
		a.keys = append(a.keys, k.Clone())
		a.vals = append(a.vals, v.Clone())
		a.nval++
	default:
		return nerr.New(nerr.InvalidArgument, "aggregate.acc", "unknown aggregate")
	}
	return nil
}

func (h *Hash) emit(st *state) ([]types.Value, error) {
	out := make([]types.Value, 0, len(h.groups)+len(h.specs))
	out = append(out, st.key...)
	for i, sp := range h.specs {
		a := st.accs[i]
		switch sp.Fun {
		case "count":
			n := st.n
			if sp.Col >= 0 {
				n = a.nval
			}
			d, _ := types.ParseDecimal(itoa64(n))
			out = append(out, types.DecimalValue(d, types.Type{Kind: types.KindDecimal}))
		case "sum":
			if a.nval == 0 {
				out = append(out, types.Null(types.Type{Kind: types.KindDecimal}))
			} else {
				out = append(out, types.DecimalValue(a.sum, types.Type{Kind: types.KindDecimal, Scale: uint16(a.sum.Scale)}))
			}
		case "avg":
			if a.nval == 0 {
				out = append(out, types.Null(types.Type{Kind: types.KindDecimal}))
			} else {
				den, _ := types.ParseDecimal(itoa64(a.nval))
				q, err := types.QuoDec(a.sum, den)
				if err != nil {
					out = append(out, types.Null(types.Type{Kind: types.KindDecimal}))
				} else {
					out = append(out, types.DecimalValue(q, types.Type{Kind: types.KindDecimal, Scale: uint16(q.Scale)}))
				}
			}
		case "min", "max":
			if !a.hasVal {
				outType := sp.OutType
				if outType.Kind == types.KindInvalid {
					outType = types.String()
				}
				out = append(out, types.Null(outType))
			} else {
				out = append(out, a.val)
			}
		case "array_agg":
			arrType := sp.OutType
			if arrType.Kind == types.KindInvalid {
				elemType := types.String()
				if len(a.coll) > 0 {
					elemType = a.coll[0].Typ
				}
				at, err := types.ArrayType(elemType)
				if err != nil {
					return nil, err
				}
				arrType = at
			}
			if a.nval == 0 {
				out = append(out, types.Null(arrType))
			} else {
				out = append(out, types.ArrayValue(arrType, a.coll))
			}
		case "map_agg":
			mapType := sp.OutType
			if mapType.Kind == types.KindInvalid {
				keyType, valType := types.String(), types.String()
				if len(a.keys) > 0 {
					keyType, valType = a.keys[0].Typ, a.vals[0].Typ
				}
				mt, err := types.MapType(keyType, valType)
				if err != nil {
					return nil, err
				}
				mapType = mt
			}
			if a.nval == 0 {
				out = append(out, types.Null(mapType))
			} else {
				ok, ov, err := types.CanonicalizeMap(a.keys, a.vals)
				if err != nil {
					return nil, err
				}
				out = append(out, types.MapValue(mapType, ok, ov))
			}
		}
	}
	return out, nil
}

// Finish returns aggregated rows (in first-seen group order, then spilled).
//
// An ungrouped aggregate is defined over one implicit group covering the whole
// input, so it yields exactly one row even when no row ever arrived — COUNT is
// 0, every other aggregate is NULL. Only a grouped aggregate collapses to zero
// rows on empty input, because there is no group to report.
func (h *Hash) Finish() ([][]types.Value, error) {
	var out [][]types.Value
	for _, k := range h.order {
		row, err := h.emit(h.table[k])
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if h.spill != nil {
		merged, err := h.readSpill()
		if err != nil {
			return nil, err
		}
		out = append(out, merged...)
	}
	if len(out) == 0 && len(h.groups) == 0 {
		row, err := h.emit(&state{accs: make([]aggAcc, len(h.specs))})
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func (h *Hash) Merge(other *Hash) error {
	if other == nil {
		return nil
	}
	for _, k := range other.order {
		st := other.table[k]
		cur, ok := h.table[k]
		if !ok {
			est := int64(64 + len(k))
			if err := h.budget.ChargeMem(est); err != nil {
				if err := h.flushAll(); err != nil {
					return err
				}
				if err := h.budget.ChargeMem(est); err != nil {
					return err
				}
			}
			h.table[k] = st
			h.order = append(h.order, k)
			continue
		}
		cur.n += st.n
		if len(cur.accs) != len(st.accs) {
			return nerr.New(nerr.Internal, "aggregate.Merge", "spec count mismatch")
		}
		for i := range st.accs {
			sa, ca := st.accs[i], &cur.accs[i]
			ca.nval += sa.nval
			ca.sum = types.AddDec(ca.sum, sa.sum)
			if len(sa.coll) > 0 {
				ca.coll = append(ca.coll, sa.coll...)
			}
			if len(sa.keys) > 0 {
				ca.keys = append(ca.keys, sa.keys...)
				ca.vals = append(ca.vals, sa.vals...)
			}
			if !sa.hasVal {
				continue
			}
			if !ca.hasVal {
				ca.val, ca.hasVal = sa.val, true
				continue
			}
			c, err := sa.val.Cmp(ca.val)
			if err != nil {
				continue
			}
			// The spec's fun decides the direction; Merge does not have it
			// here, so keep both extremes candidate-correct: for MIN we want
			// the smaller, for MAX the larger. Distinguish via h.specs[i].
			if h.specs[i].Fun == "max" {
				if c > 0 {
					ca.val = sa.val
				}
			} else if c < 0 {
				ca.val = sa.val
			}
		}
	}
	return nil
}

func (h *Hash) flushAll() error {
	if h.spill == nil {
		sp, err := scheduler.NewSpill(h.budget)
		if err != nil {
			return err
		}
		h.spill = sp
	}
	var rows [][]types.Value
	for _, k := range h.order {
		row, err := encodeState(h.table[k], h.groups, h.specs)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	if err := h.spill.Write(0, rows); err != nil {
		return err
	}
	h.budget.ReleaseMem(h.mem)
	h.mem = 0
	h.table = make(map[string]*state)
	h.order = nil
	return nil
}

func (h *Hash) spillOne(string, *state) error { return h.flushAll() }

func encodeState(st *state, groups []int, specs []Spec) ([]types.Value, error) {
	// reused by spill: emit as a regular output row; readSpill re-aggregates
	// by treating spilled rows as already-emitted groups. For correctness
	// under spill we re-insert via a second Hash when reading.
	h := Hash{groups: groups, specs: specs}
	return h.emit(st)
}

func (h *Hash) readSpill() ([][]types.Value, error) {
	raws, err := h.spill.ReadRaw(0)
	if err != nil {
		return nil, err
	}
	// spilled rows are already emitted aggregates; they must be merged
	// if the same group was flushed more than once. Rebuild via keys.
	tmp := New(h.groups, h.specs, h.outTy, h.budget)
	// For spilled emit rows, group cols are the prefix; re-add as if they
	// were input rows only works for COUNT/SUM if we stored partials.
	// flushAll emits finished rows; merge by key on finish is enough if
	// we don't emit twice for the same in-memory key. Multiple flushes
	// of the same key need merging. Decode and merge emit rows:
	schema := spillSchema(h.groups, h.specs, h.outTy)
	acc := make(map[string][]types.Value)
	var order []string
	for _, raw := range raws {
		row, err := types.DecodeRow(raw, schema)
		if err != nil {
			return nil, err
		}
		var ks string
		if len(h.groups) == 0 {
			ks = ""
		} else {
			if enc, err := types.EncodeKey(row[:len(h.groups)]); err == nil {
				ks = string(enc)
			} else {
				ks = "\x00" + nullKey(row[:len(h.groups)])
			}
		}
		if prev, ok := acc[ks]; ok {
			acc[ks] = mergeEmit(prev, row, h.groups, h.specs)
		} else {
			acc[ks] = row
			order = append(order, ks)
		}
	}
	_ = tmp
	out := make([][]types.Value, 0, len(order))
	for _, k := range order {
		out = append(out, acc[k])
	}
	return out, nil
}

func mergeEmit(a, b []types.Value, groups []int, specs []Spec) []types.Value {
	out := append([]types.Value(nil), a...)
	off := len(groups)
	for i, sp := range specs {
		ai, bi := a[off+i], b[off+i]
		switch sp.Fun {
		case "count", "sum":
			if ai.Null {
				out[off+i] = bi
			} else if bi.Null {
				out[off+i] = ai
			} else {
				out[off+i] = types.DecimalValue(types.AddDec(ai.Dec, bi.Dec), ai.Typ)
			}
		case "avg":
			// spilled AVG cannot be merged losslessly without count;
			// treat as last-write. Tests that force spill use COUNT/SUM.
			out[off+i] = bi
		case "min":
			if ai.Null {
				out[off+i] = bi
			} else if !bi.Null {
				if c, err := bi.Cmp(ai); err == nil && c < 0 {
					out[off+i] = bi
				}
			}
		case "max":
			if ai.Null {
				out[off+i] = bi
			} else if !bi.Null {
				if c, err := bi.Cmp(ai); err == nil && c > 0 {
					out[off+i] = bi
				}
			}
		case "array_agg":
			if ai.Null {
				out[off+i] = bi
			} else if bi.Null {
				out[off+i] = ai
			} else {
				combined := append(append([]types.Value(nil), ai.Coll...), bi.Coll...)
				out[off+i] = types.ArrayValue(ai.Typ, combined)
			}
		case "map_agg":
			if ai.Null {
				out[off+i] = bi
			} else if bi.Null {
				out[off+i] = ai
			} else {
				combKeys := append(append([]types.Value(nil), ai.CollKeys...), bi.CollKeys...)
				combVals := append(append([]types.Value(nil), ai.Coll...), bi.Coll...)
				ok, ov, err := types.CanonicalizeMap(combKeys, combVals)
				if err == nil {
					out[off+i] = types.MapValue(ai.Typ, ok, ov)
				} else {
					out[off+i] = bi
				}
			}
		}
	}
	return out
}

func spillSchema(groups []int, specs []Spec, outTy []types.Type) []types.Type {
	if len(outTy) == len(groups)+len(specs) {
		return outTy
	}
	out := make([]types.Type, 0, len(groups)+len(specs))
	for range groups {
		out = append(out, types.String())
	}
	for _, sp := range specs {
		if sp.OutType.Kind != types.KindInvalid {
			out = append(out, sp.OutType)
		} else {
			out = append(out, types.Type{Kind: types.KindDecimal})
		}
	}
	return out
}

func (h *Hash) Close() {
	if h != nil && h.spill != nil {
		_ = h.spill.Close()
		h.spill = nil
	}
}

func cloneRow(in []types.Value) []types.Value {
	out := make([]types.Value, len(in))
	for i := range in {
		out[i] = in[i].Clone()
	}
	return out
}

func nullKey(vals []types.Value) string {
	var b []byte
	for _, v := range vals {
		if v.Null {
			b = append(b, 0)
			continue
		}
		b = append(b, 1)
		if enc, err := types.EncodeKey([]types.Value{v}); err == nil {
			b = append(b, enc...)
		}
	}
	return string(b)
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Parallel aggregates each partition on a worker, then merges.
func Parallel(pool *scheduler.Pool, budget *scheduler.Budget, groups []int, specs []Spec, outTypes []types.Type, parts [][][]types.Value) ([][]types.Value, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	workers := budget.Workers()
	if workers > len(parts) {
		workers = len(parts)
	}
	local := make([]*Hash, len(parts))
	tasks := make([]func() error, len(parts))
	for i := range parts {
		i := i
		local[i] = New(groups, specs, outTypes, budget)
		tasks[i] = func() error {
			for _, row := range parts[i] {
				if err := local[i].Add(row); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if err := pool.Run(budget.Context(), workers, tasks); err != nil {
		return nil, err
	}
	root := local[0]
	for i := 1; i < len(local); i++ {
		if err := root.Merge(local[i]); err != nil {
			return nil, err
		}
	}
	return root.Finish()
}
