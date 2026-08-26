package window

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/executor/sort"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Eval evaluates an expression against a table row.
type Eval func(ast.Expr, *catalog.Table, []types.Value) (types.Value, error)

// Apply computes window function columns and appends them to each input row.
func Apply(rows [][]types.Value, tab *catalog.Table, specs []planner.WindowSpec, eval Eval, budget *scheduler.Budget) ([][]types.Value, error) {
	if len(specs) == 0 {
		return rows, nil
	}
	if len(rows) == 0 {
		return rows, nil
	}
	results := make([][]types.Value, len(rows))
	for i := range results {
		results[i] = make([]types.Value, len(specs))
	}
	groups := groupSpecs(specs)
	sharedPart := sharedPartition(specs)
	if err := runGroups(rows, tab, specs, groups, results, eval, budget); err != nil {
		if nerr.HasCode(err, nerr.Exhausted) && sharedPart && budget != nil {
			if serr := runSpilled(rows, tab, specs, groups, results, eval, budget); serr == nil {
				return appendResults(rows, results), nil
			} else if !nerr.HasCode(serr, nerr.Exhausted) {
				return nil, serr
			}
		}
		return nil, err
	}
	return appendResults(rows, results), nil
}

func runGroups(rows [][]types.Value, tab *catalog.Table, specs []planner.WindowSpec, groups [][]int, results [][]types.Value, eval Eval, budget *scheduler.Budget) error {
	for _, g := range groups {
		if err := runGroup(rows, tab, specs, g, results, eval, budget); err != nil {
			return err
		}
	}
	return nil
}

func runGroup(rows [][]types.Value, tab *catalog.Table, specs []planner.WindowSpec, group []int, results [][]types.Value, eval Eval, budget *scheduler.Budget) error {
	sp0 := specs[group[0]]
	orderKeys, err := evalKeys(rows, tab, append(append([]ast.Expr(nil), sp0.Partition...), orderExprs(sp0.Order)...), eval, budget)
	if err != nil {
		return err
	}
	npart := len(sp0.Partition)
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sortKeys := make([]sort.Key, len(sp0.Partition)+len(sp0.Order))
	for i := range sortKeys {
		sortKeys[i] = sort.Key{Col: i, Desc: false}
		if i >= npart {
			sortKeys[i].Desc = sp0.Order[i-npart].Desc
		}
	}
	if err := sortIndex(idx, orderKeys, sortKeys); err != nil {
		return err
	}
	argVals := make([][][]types.Value, len(group))
	for gi, si := range group {
		sp := specs[si]
		if sp.Star || len(sp.Args) == 0 {
			continue
		}
		vals, err := evalArgs(rows, tab, sp.Args, eval, budget)
		if err != nil {
			return err
		}
		argVals[gi] = vals
	}
	for start := 0; start < len(idx); {
		if err := check(budget, start); err != nil {
			return err
		}
		end := start + 1
		for end < len(idx) {
			eq, err := keysEqual(orderKeys[idx[start]], orderKeys[idx[end]], npart)
			if err != nil {
				return err
			}
			if !eq {
				break
			}
			end++
		}
		part := idx[start:end]
		for gi, si := range group {
			if err := evalPartition(part, orderKeys, npart, specs[si], argVals[gi], results, si, budget); err != nil {
				return err
			}
		}
		start = end
	}
	return nil
}

func runSpilled(rows [][]types.Value, tab *catalog.Table, specs []planner.WindowSpec, groups [][]int, results [][]types.Value, eval Eval, budget *scheduler.Budget) error {
	spill, err := scheduler.NewSpill(budget)
	if err != nil {
		return err
	}
	defer func() { _ = spill.Close() }()
	const parts = 16
	partKeys, err := evalKeys(rows, tab, specs[0].Partition, eval, budget)
	if err != nil {
		return err
	}
	for i, row := range rows {
		if err := check(budget, i); err != nil {
			return err
		}
		h := hashVals(partKeys[i]) % parts
		stored := append(append([]types.Value{}, row...), types.DecimalValue(types.DecimalFromInt64(int64(i)), types.Type{Kind: types.KindDecimal}))
		enc, err := types.EncodeRow(stored)
		if err != nil {
			return err
		}
		if err := budget.ChargeDisk(int64(len(enc))); err != nil {
			return err
		}
		if err := spill.Write(h, [][]types.Value{stored}); err != nil {
			return err
		}
	}
	for p := 0; p < parts; p++ {
		if err := check(budget, p); err != nil {
			return err
		}
		partRows, err := spill.Read(p)
		if err != nil {
			continue
		}
		if len(partRows) == 0 {
			continue
		}
		orig := make([][]types.Value, len(partRows))
		localIdx := make([]int, len(partRows))
		for i, row := range partRows {
			if len(row) == 0 {
				continue
			}
			orig[i] = row[:len(row)-1]
			if !row[len(row)-1].Null && row[len(row)-1].Dec.Coef != nil {
				localIdx[i] = int(row[len(row)-1].Dec.Coef.Int64())
			}
		}
		localResults := make([][]types.Value, len(orig))
		for i := range localResults {
			localResults[i] = make([]types.Value, len(specs))
		}
		if err := runGroups(orig, tab, specs, groups, localResults, eval, budget); err != nil {
			return err
		}
		for i, src := range localIdx {
			if src >= 0 && src < len(results) {
				copy(results[src], localResults[i])
			}
		}
	}
	return nil
}

func evalPartition(part []int, orderKeys [][]types.Value, npart int, sp planner.WindowSpec, args [][]types.Value, results [][]types.Value, specOrd int, budget *scheduler.Budget) error {
	n := len(part)
	switch sp.Fun {
	case "row_number":
		for i, idx := range part {
			if err := check(budget, i); err != nil {
				return err
			}
			results[idx][specOrd] = intDec(int64(i + 1))
		}
		return nil
	case "rank", "dense_rank":
		rank, dense := int64(1), int64(1)
		for i, idx := range part {
			if err := check(budget, i); err != nil {
				return err
			}
			if i > 0 {
				eq, err := orderEqual(orderKeys[part[i-1]], orderKeys[idx], npart)
				if err != nil {
					return err
				}
				if !eq {
					rank = int64(i + 1)
					dense++
				}
			}
			if sp.Fun == "rank" {
				results[idx][specOrd] = intDec(rank)
			} else {
				results[idx][specOrd] = intDec(dense)
			}
		}
		return nil
	case "lag", "lead":
		off := int64(1)
		if len(sp.Args) >= 2 {
			n, err := intLiteral(sp.Args[1])
			if err != nil {
				return err
			}
			off = n
		}
		for i, idx := range part {
			if err := check(budget, i); err != nil {
				return err
			}
			src := i
			if sp.Fun == "lag" {
				src = i - int(off)
			} else {
				src = i + int(off)
			}
			if src < 0 || src >= n {
				if len(sp.Args) >= 3 {
					results[idx][specOrd] = argsDefault(args, part[i], 2)
				} else {
					results[idx][specOrd] = typedNull(args, part, i)
				}
				continue
			}
			results[idx][specOrd] = argAt(args, part[src], 0)
		}
		return nil
	case "first_value", "last_value", "count", "sum", "avg", "min", "max":
		for i, idx := range part {
			if err := check(budget, i); err != nil {
				return err
			}
			lo, hi, err := frameBounds(i, part, orderKeys, npart, sp.Frame)
			if err != nil {
				return err
			}
			v, err := frameValue(sp, part[lo:hi], args, i)
			if err != nil {
				return err
			}
			results[idx][specOrd] = v
		}
		return nil
	default:
		return nerr.New(nerr.InvalidArgument, "executor.window", "unsupported window function")
	}
}

func frameBounds(i int, part []int, keys [][]types.Value, npart int, fr ast.Frame) (int, int, error) {
	n := len(part)
	peerStart, peerEnd := i, i+1
	if fr.Mode == ast.FrameRange {
		for peerStart > 0 {
			eq, err := orderEqual(keys[part[peerStart-1]], keys[part[i]], npart)
			if err != nil {
				return 0, 0, err
			}
			if !eq {
				break
			}
			peerStart--
		}
		for peerEnd < n {
			eq, err := orderEqual(keys[part[peerEnd]], keys[part[i]], npart)
			if err != nil {
				return 0, 0, err
			}
			if !eq {
				break
			}
			peerEnd++
		}
	}
	start, err := boundIndex(fr.Start, i, n, peerStart, peerEnd, true)
	if err != nil {
		return 0, 0, err
	}
	end, err := boundIndex(fr.End, i, n, peerStart, peerEnd, false)
	if err != nil {
		return 0, 0, err
	}
	if start < 0 {
		start = 0
	}
	if end > n {
		end = n
	}
	if start > end {
		start = end
	}
	return start, end, nil
}

func boundIndex(b ast.FrameBound, i, n, peerStart, peerEnd int, isStart bool) (int, error) {
	switch b.Kind {
	case ast.BoundUnboundedPreceding:
		return 0, nil
	case ast.BoundUnboundedFollowing:
		return n, nil
	case ast.BoundCurrentRow:
		if isStart {
			return peerStart, nil
		}
		return peerEnd, nil
	case ast.BoundPreceding:
		off, err := intLiteral(b.Offset)
		if err != nil {
			return 0, err
		}
		v := i - int(off)
		if isStart {
			return v, nil
		}
		return v + 1, nil
	case ast.BoundFollowing:
		off, err := intLiteral(b.Offset)
		if err != nil {
			return 0, err
		}
		v := i + int(off)
		if isStart {
			return v, nil
		}
		return v + 1, nil
	default:
		if isStart {
			return peerStart, nil
		}
		return peerEnd, nil
	}
}

func frameValue(sp planner.WindowSpec, frame []int, args [][]types.Value, _ int) (types.Value, error) {
	switch sp.Fun {
	case "first_value":
		if len(frame) == 0 {
			return typedNull(args, frame, 0), nil
		}
		return argAt(args, frame[0], 0), nil
	case "last_value":
		if len(frame) == 0 {
			return typedNull(args, frame, 0), nil
		}
		return argAt(args, frame[len(frame)-1], 0), nil
	case "count":
		if sp.Star {
			return intDec(int64(len(frame))), nil
		}
		var n int64
		for _, idx := range frame {
			v := argAt(args, idx, 0)
			if !v.Null {
				n++
			}
		}
		return intDec(n), nil
	case "sum", "avg":
		var sum types.Decimal
		var n int64
		for _, idx := range frame {
			v := argAt(args, idx, 0)
			if v.Null {
				continue
			}
			if v.Typ.Kind != types.KindDecimal {
				c, err := types.Coerce(v, types.Type{Kind: types.KindDecimal})
				if err != nil {
					return types.Value{}, err
				}
				v = c
			}
			sum = types.AddDec(sum, v.Dec)
			n++
		}
		if n == 0 {
			return types.Null(types.Type{Kind: types.KindDecimal}), nil
		}
		if sp.Fun == "sum" {
			return types.DecimalValue(sum, types.Type{Kind: types.KindDecimal, Scale: uint16(sum.Scale)}), nil
		}
		den := types.DecimalFromInt64(n)
		q, err := types.QuoDec(sum, den)
		if err != nil {
			return types.Null(types.Type{Kind: types.KindDecimal}), nil
		}
		return types.DecimalValue(q, types.Type{Kind: types.KindDecimal, Scale: uint16(q.Scale)}), nil
	case "min", "max":
		var best types.Value
		has := false
		for _, idx := range frame {
			v := argAt(args, idx, 0)
			if v.Null {
				continue
			}
			if !has {
				best = v.Clone()
				has = true
				continue
			}
			c, err := v.Cmp(best)
			if err != nil {
				return types.Value{}, err
			}
			if sp.Fun == "min" && c < 0 {
				best = v.Clone()
			}
			if sp.Fun == "max" && c > 0 {
				best = v.Clone()
			}
		}
		if !has {
			return typedNull(args, frame, 0), nil
		}
		return best, nil
	default:
		return types.Value{}, nerr.New(nerr.Internal, "executor.window", "unknown framed function")
	}
}

func groupSpecs(specs []planner.WindowSpec) [][]int {
	var groups [][]int
	used := make([]bool, len(specs))
	for i := range specs {
		if used[i] {
			continue
		}
		g := []int{i}
		used[i] = true
		for j := i + 1; j < len(specs); j++ {
			if used[j] {
				continue
			}
			if sameSort(specs[i], specs[j]) {
				g = append(g, j)
				used[j] = true
			}
		}
		groups = append(groups, g)
	}
	return groups
}

func sameSort(a, b planner.WindowSpec) bool {
	if len(a.Partition) != len(b.Partition) || len(a.Order) != len(b.Order) {
		return false
	}
	for i := range a.Partition {
		if !exprEq(a.Partition[i], b.Partition[i]) {
			return false
		}
	}
	for i := range a.Order {
		if a.Order[i].Desc != b.Order[i].Desc || !exprEq(a.Order[i].Expr, b.Order[i].Expr) {
			return false
		}
	}
	return true
}

func sharedPartition(specs []planner.WindowSpec) bool {
	if len(specs) == 0 {
		return false
	}
	for i := 1; i < len(specs); i++ {
		if len(specs[i].Partition) != len(specs[0].Partition) {
			return false
		}
		for j := range specs[0].Partition {
			if !exprEq(specs[0].Partition[j], specs[i].Partition[j]) {
				return false
			}
		}
	}
	return len(specs[0].Partition) > 0
}

func exprEq(a, b ast.Expr) bool {
	return astEqual(a, b)
}

func astEqual(a, b ast.Expr) bool {
	sa, ok1 := a.(ast.Ident)
	sb, ok2 := b.(ast.Ident)
	if ok1 && ok2 {
		return sa.Name == sb.Name
	}
	return false
}

func orderExprs(items []ast.OrderItem) []ast.Expr {
	out := make([]ast.Expr, len(items))
	for i, it := range items {
		out[i] = it.Expr
	}
	return out
}

func evalKeys(rows [][]types.Value, tab *catalog.Table, exprs []ast.Expr, eval Eval, budget *scheduler.Budget) ([][]types.Value, error) {
	out := make([][]types.Value, len(rows))
	for i, row := range rows {
		if err := check(budget, i); err != nil {
			return nil, err
		}
		keys := make([]types.Value, len(exprs))
		for j, e := range exprs {
			v, err := eval(e, tab, row)
			if err != nil {
				return nil, err
			}
			keys[j] = v
		}
		if err := charge(budget, keys); err != nil {
			return nil, err
		}
		out[i] = keys
	}
	return out, nil
}

func evalArgs(rows [][]types.Value, tab *catalog.Table, exprs []ast.Expr, eval Eval, budget *scheduler.Budget) ([][]types.Value, error) {
	return evalKeys(rows, tab, exprs, eval, budget)
}

func sortIndex(idx []int, keys [][]types.Value, sortKeys []sort.Key) error {
	if len(sortKeys) == 0 || len(idx) < 2 {
		return nil
	}
	tmp := make([][]types.Value, len(idx))
	for i, id := range idx {
		row := append([]types.Value(nil), keys[id]...)
		row = append(row, types.DecimalValue(types.DecimalFromInt64(int64(id)), types.Type{Kind: types.KindDecimal}))
		tmp[i] = row
	}
	sk := append([]sort.Key(nil), sortKeys...)
	if err := sort.Rows(tmp, sk); err != nil {
		return err
	}
	for i, row := range tmp {
		idx[i] = int(row[len(row)-1].Dec.Coef.Int64())
	}
	return nil
}

func keysEqual(a, b []types.Value, n int) (bool, error) {
	if n > len(a) {
		n = len(a)
	}
	if n > len(b) {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].Null && b[i].Null {
			continue
		}
		if a[i].Null || b[i].Null {
			return false, nil
		}
		c, err := a[i].Cmp(b[i])
		if err != nil {
			return false, err
		}
		if c != 0 {
			return false, nil
		}
	}
	return true, nil
}

func orderEqual(a, b []types.Value, npart int) (bool, error) {
	if npart >= len(a) && npart >= len(b) {
		return true, nil
	}
	if npart > len(a) || npart > len(b) {
		return false, nil
	}
	return keysEqual(a[npart:], b[npart:], len(a)-npart)
}

func argAt(args [][]types.Value, row, col int) types.Value {
	if args == nil || row < 0 || row >= len(args) || col >= len(args[row]) {
		return types.Null(types.NullType())
	}
	return args[row][col]
}

func argsDefault(args [][]types.Value, row, col int) types.Value {
	return argAt(args, row, col)
}

func typedNull(args [][]types.Value, part []int, i int) types.Value {
	if args != nil && i >= 0 && i < len(part) && part[i] < len(args) && len(args[part[i]]) > 0 {
		return types.Null(args[part[i]][0].Typ)
	}
	return types.Null(types.NullType())
}

func intLiteral(e ast.Expr) (int64, error) {
	lit, ok := e.(ast.Literal)
	if !ok || lit.Value.Null || lit.Value.Typ.Kind != types.KindDecimal || lit.Value.Dec.Coef == nil {
		return 0, nerr.New(nerr.InvalidArgument, "executor.window", "expected a non-negative integer")
	}
	return lit.Value.Dec.Coef.Int64(), nil
}

func intDec(n int64) types.Value {
	return types.DecimalValue(types.DecimalFromInt64(n), types.Type{Kind: types.KindDecimal})
}

func appendResults(rows [][]types.Value, results [][]types.Value) [][]types.Value {
	out := make([][]types.Value, len(rows))
	for i, row := range rows {
		dst := make([]types.Value, len(row)+len(results[i]))
		copy(dst, row)
		copy(dst[len(row):], results[i])
		out[i] = dst
	}
	return out
}

func check(b *scheduler.Budget, i int) error {
	if b == nil {
		return nil
	}
	if i&255 == 0 {
		return b.Check()
	}
	return nil
}

func charge(b *scheduler.Budget, vals []types.Value) error {
	if b == nil {
		return nil
	}
	n := int64(16 * (len(vals) + 1))
	for _, v := range vals {
		n += int64(len(v.Str) + len(v.JSON) + 4*len(v.Vec))
	}
	return b.ChargeMem(n)
}

func hashVals(vals []types.Value) int {
	h := 0
	enc, err := types.EncodeRow(vals)
	if err != nil {
		return 0
	}
	for _, b := range enc {
		h = h*31 + int(b)
	}
	if h < 0 {
		h = -h
	}
	return h
}
