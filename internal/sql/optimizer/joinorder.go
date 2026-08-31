package optimizer

import (
	"math/bits"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
)

type joinDP struct {
	plan  planner.Logical
	node  *Node
	cost  int64
	order []int
}

type joinRel struct {
	plan planner.Logical
	cols int
	off  int
}

func tryReorderJoins(n planner.Join, stats StatsFunc) (planner.Logical, *Node, bool) {
	if !innerFlattenable(n) || containsRank(n) {
		return nil, nil, false
	}
	plans, preds := flattenInner(n)
	if len(plans) < 2 || len(plans) > 8 {
		return nil, nil, false
	}
	orig := n.Schema
	if orig == nil {
		orig = origJoinSchema(n, plans)
	}
	rels, ok := relMeta(plans, orig)
	if !ok {
		return nil, nil, false
	}
	rels, preds = attachLocalFilters(rels, preds, orig)
	best := dpLeftDeep(rels, preds, orig, stats)
	if best == nil || best.plan == nil {
		return nil, nil, false
	}
	plan, node := restoreJoinOrder(best.plan, best.node, orig)
	return plan, node, true
}

func innerFlattenable(n planner.Join) bool {
	switch n.Kind {
	case ast.JoinInner, ast.JoinCross:
		return true
	default:
		return false
	}
}

func flattenInner(p planner.Logical) ([]planner.Logical, []ast.Expr) {
	switch n := p.(type) {
	case planner.Filter:
		if j, ok := n.Input.(planner.Join); ok && innerFlattenable(j) {
			rels, preds := flattenInner(j)
			return rels, append(preds, conjuncts(n.Pred)...)
		}
		return []planner.Logical{p}, nil
	case planner.Join:
		if !innerFlattenable(n) {
			return []planner.Logical{p}, nil
		}
		lrels, lpreds := flattenInner(n.Left)
		rrels, rpreds := flattenInner(n.Right)
		preds := append(lpreds, rpreds...)
		if n.Pred != nil {
			preds = append(preds, conjuncts(n.Pred)...)
		}
		return append(lrels, rrels...), preds
	default:
		return []planner.Logical{p}, nil
	}
}

func attachLocalFilters(rels []joinRel, preds []ast.Expr, orig *catalog.Table) ([]joinRel, []ast.Expr) {
	if len(preds) == 0 {
		return rels, preds
	}
	locals := make([][]ast.Expr, len(rels))
	var joinPreds []ast.Expr
	for _, p := range preds {
		m := predRelMask(p, rels, orig)
		if bits.OnesCount16(m) != 1 {
			joinPreds = append(joinPreds, p)
			continue
		}
		for i := range rels {
			if m&(1<<i) != 0 {
				locals[i] = append(locals[i], p)
				break
			}
		}
	}
	out := append([]joinRel(nil), rels...)
	for i, cs := range locals {
		if len(cs) == 0 {
			continue
		}
		out[i].plan = planner.Filter{Input: out[i].plan, Pred: adaptPredToRel(andAll(cs), out[i].plan)}
	}
	return out, joinPreds
}

func relMeta(plans []planner.Logical, orig *catalog.Table) ([]joinRel, bool) {
	out := make([]joinRel, len(plans))
	off := 0
	for i, p := range plans {
		n := colCount(p)
		if n <= 0 {
			return nil, false
		}
		out[i] = joinRel{plan: p, cols: n, off: off}
		off += n
	}
	if orig != nil && len(orig.Columns) != off {
		return nil, false
	}
	return out, true
}

func dpLeftDeep(rels []joinRel, preds []ast.Expr, orig *catalog.Table, stats StatsFunc) *joinDP {
	n := len(rels)
	leaves := make([]*joinDP, n)
	for i, rel := range rels {
		plan, node := chooseAt(rel.plan, stats, true)
		cost := int64(0)
		if node != nil {
			cost = node.EstCost
		}
		leaves[i] = &joinDP{plan: plan, node: node, cost: cost, order: []int{i}}
	}
	size := 1 << n
	best := make([]*joinDP, size)
	for i := 0; i < n; i++ {
		best[1<<i] = leaves[i]
	}
	full := uint16(size - 1)
	for mask := 1; mask < size; mask++ {
		if bits.OnesCount(uint(mask)) < 2 {
			continue
		}
		umask := uint16(mask)
		pick := func(connectedOnly bool) {
			for i := 0; i < n; i++ {
				bit := uint16(1 << i)
				if umask&bit == 0 {
					continue
				}
				leftMask := umask ^ bit
				left := best[leftMask]
				if left == nil {
					continue
				}
				pred, connected := joinPredsFor(preds, rels, orig, leftMask, bit, full)
				if connectedOnly && !connected {
					continue
				}
				if !connectedOnly && connected {
					continue
				}
				order := append(append([]int(nil), left.order...), i)
				j, jn := finishJoin(planner.Join{
					Left:   left.plan,
					Right:  leaves[i].plan,
					Pred:   pred,
					Kind:   joinKindFor(pred),
					Cross:  pred == nil,
					Schema: permuteJoinSchema(orig, rels, order),
				}, left.plan, left.node, leaves[i].plan, leaves[i].node, stats)
				cand := &joinDP{plan: j, node: jn, cost: 0, order: order}
				if jn != nil {
					cand.cost = jn.EstCost
				}
				if betterJoinDP(cand, best[mask]) {
					best[mask] = cand
				}
			}
		}
		pick(true)
		if best[mask] == nil {
			pick(false)
		}
	}
	return best[size-1]
}

func joinKindFor(pred ast.Expr) ast.JoinKind {
	if pred == nil {
		return ast.JoinCross
	}
	return ast.JoinInner
}

func joinPredsFor(preds []ast.Expr, rels []joinRel, orig *catalog.Table, leftMask, rightMask, full uint16) (ast.Expr, bool) {
	both := leftMask | rightMask
	var cs []ast.Expr
	connected := false
	for _, p := range preds {
		m := predRelMask(p, rels, orig)
		if m == 0 {
			if both == full && !predIsTrue(p) {
				cs = append(cs, p)
			}
			continue
		}
		if m&both != m {
			continue
		}
		if m&rightMask == 0 {
			continue
		}
		cs = append(cs, p)
		if m&leftMask != 0 {
			connected = true
		}
	}
	return andAll(cs), connected
}

func predRelMask(e ast.Expr, rels []joinRel, orig *catalog.Table) uint16 {
	var mask uint16
	for _, name := range identNames(e) {
		if orig != nil {
			if i, ok := orig.ColIndex(name); ok {
				for r, rel := range rels {
					if i >= rel.off && i < rel.off+rel.cols {
						mask |= 1 << r
						break
					}
				}
				continue
			}
		}
		var matched uint16
		for i, r := range rels {
			if hasCol(r.plan, name) {
				matched |= 1 << i
			}
		}
		mask |= matched
	}
	return mask
}

func permuteJoinSchema(orig *catalog.Table, rels []joinRel, order []int) *catalog.Table {
	if orig == nil || len(orig.Columns) == 0 {
		return nil
	}
	cols := make([]catalog.Column, 0, len(orig.Columns))
	for _, i := range order {
		if i < 0 || i >= len(rels) {
			return nil
		}
		r := rels[i]
		if r.off < 0 || r.off+r.cols > len(orig.Columns) {
			return nil
		}
		cols = append(cols, orig.Columns[r.off:r.off+r.cols]...)
	}
	if len(cols) == 0 {
		return nil
	}
	out := orig.Clone()
	out.Columns = cols
	out.PK = nil
	return out
}

func betterJoinDP(cand, cur *joinDP) bool {
	if cand == nil {
		return false
	}
	if cur == nil {
		return true
	}
	if cand.cost != cur.cost {
		return cand.cost < cur.cost
	}
	n := len(cand.order)
	if len(cur.order) < n {
		n = len(cur.order)
	}
	for i := 0; i < n; i++ {
		if cand.order[i] != cur.order[i] {
			return cand.order[i] < cur.order[i]
		}
	}
	return false
}

func origJoinSchema(n planner.Join, rels []planner.Logical) *catalog.Table {
	if n.Schema != nil {
		return n.Schema
	}
	out := &catalog.Table{}
	for _, r := range rels {
		if t := tableOf(r); t != nil {
			if out.Name == "" {
				out.Name = t.Name
			} else {
				out.Name += "+" + t.Name
			}
			out.Columns = append(out.Columns, t.Columns...)
		}
	}
	return out
}

func concatJoinSchema(left, right planner.Logical) *catalog.Table {
	lt, rt := tableOf(left), tableOf(right)
	out := &catalog.Table{}
	if lt != nil {
		out.Name = lt.Name
		out.Columns = append(out.Columns, lt.Columns...)
	}
	if rt != nil {
		if out.Name != "" {
			out.Name += "+" + rt.Name
		} else {
			out.Name = rt.Name
		}
		out.Columns = append(out.Columns, rt.Columns...)
	}
	return out
}

func restoreJoinOrder(plan planner.Logical, node *Node, orig *catalog.Table) (planner.Logical, *Node) {
	if orig == nil || len(orig.Columns) == 0 || plan == nil {
		return plan, node
	}
	phys := tableOf(plan)
	if phys == nil || len(phys.Columns) != len(orig.Columns) {
		return plan, node
	}
	same := true
	for i := range orig.Columns {
		if phys.Columns[i].Name != orig.Columns[i].Name {
			same = false
			break
		}
	}
	if same {
		return plan, node
	}
	cols := make([]int, len(orig.Columns))
	exprs := make([]ast.Expr, len(orig.Columns))
	names := make([]string, len(orig.Columns))
	for i, c := range orig.Columns {
		j, ok := phys.ColIndex(c.Name)
		if !ok {
			return plan, node
		}
		cols[i] = j
		names[i] = c.Name
		exprs[i] = ast.Ident{Name: c.Name}
	}
	rows, cost := int64(0), int64(0)
	if node != nil {
		rows, cost = node.EstRows, satAdd(node.EstCost, satMul(node.EstRows, cpuProject))
	}
	return planner.Project{Input: plan, Cols: cols, Exprs: exprs, Names: names},
		&Node{Op: "Project", Detail: joinNames(names), EstRows: rows, EstCost: cost, Kids: kids(node)}
}

func containsRank(p planner.Logical) bool {
	switch n := p.(type) {
	case planner.Search, planner.Nearest, planner.Rerank, planner.Candidates:
		return true
	case planner.Facet:
		return containsRank(n.Input)
	case planner.Filter:
		return containsRank(n.Input)
	case planner.Limit:
		return containsRank(n.Input)
	case planner.Project:
		return containsRank(n.Input)
	case planner.Sort:
		return containsRank(n.Input)
	case planner.Aggregate:
		return containsRank(n.Input)
	case planner.Window:
		return containsRank(n.Input)
	case planner.Join:
		return containsRank(n.Left) || containsRank(n.Right)
	case planner.With:
		return containsRank(n.Query)
	case planner.SetOperation:
		return containsRank(n.Left) || containsRank(n.Right)
	default:
		return false
	}
}
