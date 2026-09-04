package optimizer

import (
	"bytes"
	"sort"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

type colRange struct {
	ord           int
	eq            bool
	isNull        bool
	low, high     *types.Value
	lowIncl       bool
	highIncl      bool
	unboundedLow  bool
	unboundedHigh bool
}

func choose(p planner.Logical, stats StatsFunc) (planner.Logical, *Node) {
	return chooseAt(p, stats, false)
}

func chooseAt(p planner.Logical, stats StatsFunc, underJoin bool) (planner.Logical, *Node) {
	switch n := p.(type) {
	case planner.With:
		nodes := make([]*Node, 0, len(n.CTEs)+1)
		for i, cte := range n.CTEs {
			var kid *Node
			if cte.RecursiveOn {
				anchor, ak := chooseAt(cte.Anchor, stats, underJoin)
				rec, rk := chooseAt(cte.Recursive, stats, underJoin)
				n.CTEs[i].Anchor = anchor
				n.CTEs[i].Recursive = rec
				rows := int64(0)
				cost := int64(0)
				if ak != nil {
					rows += ak.EstRows
					cost += ak.EstCost
				}
				if rk != nil {
					cost += rk.EstCost
				}
				n.CTEs[i].EstRows = rows
				kid = &Node{Op: "RecursiveCTE", Detail: cte.Name, EstRows: rows, EstCost: cost, Kids: []*Node{ak, rk}}
			} else {
				in, k := chooseAt(cte.Input, stats, underJoin)
				n.CTEs[i].Input = in
				rows, cost := int64(0), int64(0)
				if k != nil {
					rows, cost = k.EstRows, k.EstCost
				}
				n.CTEs[i].EstRows = rows
				op := "Inline"
				if cte.Materialize {
					op = "Materialize"
				}
				kid = &Node{Op: op, Detail: cte.Name, EstRows: rows, EstCost: cost, Kids: kids(k)}
			}
			nodes = append(nodes, kid)
		}
		q, qk := chooseAt(n.Query, stats, underJoin)
		n.Query = q
		rows, cost := int64(0), int64(0)
		if qk != nil {
			rows, cost = qk.EstRows, qk.EstCost
		}
		for _, k := range nodes {
			if k != nil {
				cost += k.EstCost
			}
		}
		nodes = append(nodes, qk)
		return n, &Node{Op: "With", EstRows: rows, EstCost: cost, Kids: nodes}
	case planner.CTEScan:
		rows := n.EstRows
		if rows <= 0 {
			rows = defaultRows
		}
		return n, &Node{Op: "CTEScan", Detail: n.Name, EstRows: rows, EstCost: rows * cpuTuple}
	case planner.SetOperation:
		left, lk := chooseAt(n.Left, stats, underJoin)
		right, rk := chooseAt(n.Right, stats, underJoin)
		rows, cost := int64(0), int64(0)
		if lk != nil {
			rows += lk.EstRows
			cost += lk.EstCost
		}
		if rk != nil {
			rows += rk.EstRows
			cost += rk.EstCost
		}
		op := "UnionAll"
		if n.Op == "intersect" {
			op = "Intersect"
		} else if n.Op == "except" {
			op = "Except"
		} else if !n.All {
			op = "Union"
		}
		if n.Op == "intersect" || n.Op == "except" || !n.All {
			cost += rows * cpuProject
		}
		return planner.SetOperation{Left: left, Right: right, Op: n.Op, All: n.All, Names: n.Names}, &Node{Op: op, EstRows: rows, EstCost: cost, Kids: []*Node{lk, rk}}
	case planner.Project:
		in, kid := chooseAt(n.Input, stats, underJoin)
		rows := int64(0)
		cost := int64(0)
		if kid != nil {
			rows = kid.EstRows
			cost = kid.EstCost + rows*cpuProject
		}
		plan := planner.Project{Input: in, Cols: n.Cols, Exprs: n.Exprs, Names: n.Names, Distinct: n.Distinct, DistinctIndex: n.DistinctIndex}
		project := &Node{Op: "Project", Detail: joinNames(n.Names), EstRows: rows, EstCost: cost, Kids: kids(kid)}
		if n.DistinctIndex != "" {
			return plan, &Node{Op: "IndexDistinct", Detail: n.DistinctIndex, EstRows: rows, EstCost: cost, Kids: kids(project)}
		}
		if n.Distinct {
			return plan, &Node{Op: "HashDistinct", EstRows: rows, EstCost: cost + rows*cpuProject, Kids: kids(project)}
		}
		return plan, project
	case planner.Limit:
		in, kid := chooseAt(n.Input, stats, underJoin)
		rows, cost := int64(0), int64(0)
		if kid != nil {
			rows = kid.EstRows - n.Offset
			if rows < 0 {
				rows = 0
			}
			cost = kid.EstCost
		}
		if n.N >= 0 && (kid == nil || n.N < rows) {
			rows = n.N
		}
		detail := itoa64(n.N)
		if n.N < 0 {
			detail = "ALL"
		}
		if n.Offset > 0 {
			detail += " OFFSET " + itoa64(n.Offset)
		}
		return planner.Limit{Input: in, N: n.N, Offset: n.Offset},
			&Node{Op: "Limit", Detail: detail, EstRows: rows, EstCost: cost, Kids: kids(kid)}
	case planner.Sort:
		in, kid := chooseAt(n.Input, stats, underJoin)
		rows, cost := int64(0), int64(0)
		if kid != nil {
			rows, cost = kid.EstRows, kid.EstCost
			if rows > 1 {
				lg := int64(0)
				for x := rows; x > 1; x >>= 1 {
					lg++
				}
				if lg < 1 {
					lg = 1
				}
				cost += rows * lg * cpuSort
			}
		}
		plan := planner.Sort{Input: in, Keys: n.Keys, Hidden: n.Hidden, OrderedDistinct: n.OrderedDistinct, TopN: n.TopN}
		op := "Sort"
		detail := sortDetail(n)
		if n.TopN > 0 {
			op = "TopNSort"
			detail = "fetch=" + itoa64(n.TopN) + " " + detail
			if int64Rows := n.TopN; int64Rows < rows {
				rows = int64Rows
			}
		}
		sortNode := &Node{Op: op, Detail: detail, EstRows: rows, EstCost: cost, Kids: kids(kid)}
		if n.OrderedDistinct {
			return plan, &Node{Op: "OrderedDistinct", EstRows: rows, EstCost: cost + rows*cpuTuple, Kids: kids(sortNode)}
		}
		return plan, sortNode
	case planner.Update:
		in, kid := chooseAccess(n.Input, stats)
		rows, cost := int64(0), int64(0)
		if kid != nil {
			rows, cost = kid.EstRows, kid.EstCost+kid.EstRows*cpuTuple
		}
		return planner.Update{Input: in, Table: n.Table, Sets: n.Sets, Limit: n.Limit, Returning: n.Returning},
			&Node{Op: "Update", Detail: tableName(n.Table), EstRows: rows, EstCost: cost, Kids: kids(kid)}
	case planner.Delete:
		in, kid := chooseAccess(n.Input, stats)
		rows, cost := int64(0), int64(0)
		if kid != nil {
			rows, cost = kid.EstRows, kid.EstCost+kid.EstRows*cpuTuple
		}
		return planner.Delete{Input: in, Table: n.Table, Limit: n.Limit, Returning: n.Returning},
			&Node{Op: "Delete", Detail: tableName(n.Table), EstRows: rows, EstCost: cost, Kids: kids(kid)}
	case planner.Empty:
		return n, &Node{Op: "Empty", EstRows: 0, EstCost: 0}
	case planner.Join:
		if plan, node, ok := tryReorderJoins(n, stats); ok {
			return plan, node
		}
		return chooseJoinPair(n, stats)
	case planner.Window:
		in, kid := chooseAt(n.Input, stats, underJoin)
		rows, cost := int64(0), int64(0)
		if kid != nil {
			rows, cost = kid.EstRows, kid.EstCost
			if rows > 1 {
				lg := int64(0)
				for x := rows; x > 1; x >>= 1 {
					lg++
				}
				if lg < 1 {
					lg = 1
				}
				cost += rows * lg * cpuSort
			}
			cost += rows * cpuTuple
		}
		n.Input = in
		return n, &Node{Op: "Window", Detail: windowDetail(n), EstRows: rows, EstCost: cost, Kids: kids(kid)}
	case planner.Aggregate:
		in, kid := chooseAt(n.Input, stats, underJoin)
		rows, cost := int64(1), int64(0)
		if kid != nil {
			if len(n.Groups) > 0 {
				rows = kid.EstRows / 4
				if rows < 1 {
					rows = 1
				}
			}
			cost = kid.EstCost + kid.EstRows*cpuTuple
		}
		plan := planner.Aggregate{Input: in, Groups: n.Groups, Specs: n.Specs, Exprs: n.Exprs, Names: n.Names, Schema: n.Schema, Distinct: n.Distinct, Having: n.Having}
		aggDetail := joinNames(n.Names)
		if sc, ok := in.(planner.SeqScan); ok && sc.Table != nil && sc.Table.Partitioning != nil {
			aggDetail = strings.TrimSpace(aggDetail + " partition-wise")
		}
		agg := &Node{Op: "Aggregate", Detail: aggDetail, EstRows: rows, EstCost: cost, Kids: kids(kid)}
		if n.Having != nil {
			agg = &Node{Op: "Having", Detail: formatExpr(n.Having), EstRows: rows / 2, EstCost: cost + rows*cpuPred, Kids: kids(agg)}
		}
		if n.Distinct {
			return plan, &Node{Op: "HashDistinct", EstRows: rows, EstCost: cost + rows*cpuProject, Kids: kids(agg)}
		}
		return plan, agg
	case planner.Search:
		return chooseSearch(n, stats)
	case planner.Facet:
		return chooseFacet(n, stats, underJoin)
	case planner.Nearest:
		switch n.Input.(type) {
		case planner.Search, planner.Nearest:
			return chooseHybrid(n, stats, underJoin)
		}
		return chooseNearest(n, stats)
	case planner.Rerank:
		in, kid := chooseAt(n.Input, stats, underJoin)
		n.Input = in
		rows, cost := int64(0), int64(0)
		if kid != nil {
			rows, cost = kid.EstRows, kid.EstCost
		}
		var extraKids []*Node
		if kid != nil {
			extraKids = append(extraKids, kid)
		}
		if len(n.Extra) > 0 {
			extra := make([]planner.Logical, len(n.Extra))
			for i, e := range n.Extra {
				ee, ek := chooseAt(e, stats, underJoin)
				extra[i] = ee
				if ek != nil {
					extraKids = append(extraKids, ek)
					rows = satAdd(rows, ek.EstRows)
					cost = satAdd(cost, ek.EstCost)
				}
			}
			n.Extra = extra
		}
		if n.K > 0 && rows > n.K {
			rows = n.K
		}
		return n, &Node{Op: "Rerank", Detail: rerankDetail(n), EstRows: rows, EstCost: cost + rows*cpuRerank, Kids: extraKids}
	case planner.Candidates:
		return chooseCandidates(n, stats)
	case planner.Filter, planner.Scan, planner.SeqScan, planner.IndexScan:
		return chooseAccess(p, stats)
	default:
		return p, leafTrace(p)
	}
}

func kids(n *Node) []*Node {
	if n == nil {
		return nil
	}
	return []*Node{n}
}

func chooseJoinPair(n planner.Join, stats StatsFunc) (planner.Logical, *Node) {
	l, lk := chooseAt(n.Left, stats, true)
	r, rk := chooseAt(n.Right, stats, true)
	return finishJoin(n, l, lk, r, rk, stats)
}

func finishJoin(n planner.Join, l planner.Logical, lk *Node, r planner.Logical, rk *Node, stats StatsFunc) (planner.Logical, *Node) {
	lr, rr := int64(0), int64(0)
	lc, rc := int64(0), int64(0)
	if lk != nil {
		lr, lc = lk.EstRows, lk.EstCost
	}
	if rk != nil {
		rr, rc = rk.EstRows, rk.EstCost
	}
	schema := n.Schema
	if schema == nil {
		schema = concatJoinSchema(l, r)
	}
	out := satMul(lr, rr)
	if n.Pred != nil {
		out = applySel(uint64(out), predSel(n.Pred, schema, lookupStats(stats, schema)))
	}
	if n.Kind == ast.JoinLeft && lr > out {
		out = lr
	}
	if n.Kind == ast.JoinFull {
		if lr > out {
			out = lr
		}
		if rr > out {
			out = rr
		}
	}
	lkys, rkys := joinKeys(n.Pred, schema, l, r)
	method := "hash"
	op := "HashJoin"
	left := n.Kind == ast.JoinLeft
	full := n.Kind == ast.JoinFull
	semi := n.Kind == ast.JoinSemi
	anti := n.Kind == ast.JoinAnti
	if semi || anti {
		matched := applySel(uint64(lr), predSel(n.Pred, schema, lookupStats(stats, tableOf(l))))
		if matched > lr {
			matched = lr
		}
		if semi {
			out = matched
			op = "HashSemiJoin"
		} else {
			out = lr - matched
			if out < 0 {
				out = 0
			}
			op = "HashAntiJoin"
		}
		method = "hash"
	} else if full {
		op = "FullJoin"
		method = "hash"
	} else if left {
		op = "LeftJoin"
		if mergeSorted(l, r, lkys, rkys) {
			method = "merge"
		}
	} else if n.Cross || n.Kind == ast.JoinCross {
		op = "CrossJoin"
		method = "hash"
	} else if mergeSorted(l, r, lkys, rkys) {
		method = "merge"
		op = "MergeJoin"
	}
	cost := joinPairCost(lc, rc, lr, rr, len(lkys) == 0 && len(rkys) == 0, method, semi || anti)
	detail := formatExpr(n.Pred)
	if method != "merge" {
		if _, aligned := catalog.AlignedPartitionJoin(seqLeafTable(l), seqLeafTable(r), lkys, rkys); aligned {
			detail = strings.TrimSpace(detail + " partition-wise")
		}
	}
	return planner.Join{Left: l, Right: r, Pred: n.Pred, Kind: n.Kind, Cross: n.Cross && !left && !full && !semi && !anti, Method: method, LeftKeys: lkys, RightKeys: rkys, Schema: schema},
		&Node{Op: op, Detail: detail, EstRows: out, EstCost: cost, Kids: []*Node{lk, rk}}
}

// seqLeafTable returns the table of a join input that is a plain partitioned
// scan (optionally under a residual Filter), or nil when the input is anything
// else. It is used only to tag partition-aligned joins in EXPLAIN.
func seqLeafTable(p planner.Logical) *catalog.Table {
	switch n := p.(type) {
	case planner.SeqScan:
		return n.Table
	case planner.Filter:
		if sc, ok := n.Input.(planner.SeqScan); ok {
			return sc.Table
		}
	}
	return nil
}

func joinPairCost(lc, rc, lr, rr int64, cartesian bool, method string, mark bool) int64 {
	cost := satAdd(lc, rc)
	if mark {
		return satAdd(cost, satMul(lr, cpuPred))
	}
	if method == "merge" {
		return satAdd(cost, satMul(satAdd(lr, rr), cpuPred))
	}
	cost = satAdd(cost, satMul(rr, cpuHash))
	cost = satAdd(cost, satMul(lr, cpuProbe))
	if cartesian {
		cost = satAdd(cost, satMul(satMul(lr, rr), cpuPred))
	}
	return cost
}

func joinNames(names []string) string {
	s := ""
	for i, n := range names {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}

func lookupStats(fn StatsFunc, tab *catalog.Table) *catalog.TableStats {
	if fn == nil || tab == nil {
		return nil
	}
	st, ok := fn(tab.Name)
	if !ok {
		return nil
	}
	return st
}

func chooseAccess(p planner.Logical, stats StatsFunc) (planner.Logical, *Node) {
	tab, pred, needed, ok := peelAccess(p)
	if !ok || tab == nil {
		if _, isEmpty := p.(planner.Empty); isEmpty {
			return p, &Node{Op: "Empty"}
		}
		return p, leafTrace(p)
	}
	st := lookupStats(stats, tab)
	if pred != nil {
		pred = foldExpr(pred)
		if predIsFalse(pred) {
			return planner.Empty{Names: namesOf(p)}, &Node{Op: "Empty", EstRows: 0}
		}
		if predIsTrue(pred) {
			pred = nil
		}
	}
	spans, pruned := pruneSegments(tab, pred, st, needed)
	if pruned && len(spans) == 0 {
		return planner.Empty{Names: namesOf(p)}, &Node{Op: "Empty", Detail: "segment prune", EstRows: 0}
	}

	type cand struct {
		plan  planner.Logical
		node  *Node
		cost  int64
		rank  int // 0=pk, 1=index, 2=seq
		iname string
	}
	var alts []cand

	partitionIDs, partitionSuffix := partitionAccessDetail(tab, pred)
	costStats := partitionCostStats(st, partitionIDs)
	s := predSel(pred, tab, costStats)
	rows := partitionRows(st, partitionIDs, tableRows(st))
	scost, sout := seqCost(rows, s)
	seq := planner.SeqScan{Table: tab, Needed: needed, Segments: spans, Partitions: partitionIDs}
	var seqPlan planner.Logical = seq
	detail := tableName(tab)
	detail += partitionSuffix
	seqNode := &Node{Op: "SeqScan", Detail: detail, EstRows: sout, EstCost: scost}
	if pred != nil {
		seqPlan = planner.Filter{Input: seq, Pred: pred}
		seqNode = &Node{Op: "Filter", Detail: formatExpr(pred), EstRows: sout, EstCost: scost, Kids: []*Node{seqNode}}
	}
	alts = append(alts, cand{plan: seqPlan, node: seqNode, cost: scost, rank: 2})

	ranges := extractRanges(pred, tab)
	// clustered PK
	if alt, ok := indexAlt(tab, nil, true, ranges, pred, needed, costStats, rows); ok {
		alts = append(alts, alt)
	}
	idxs := append([]catalog.Index(nil), tab.Indexes...)
	sort.Slice(idxs, func(i, j int) bool { return idxs[i].Name < idxs[j].Name })
	for i := range idxs {
		idx := idxs[i]
		if idx.Predicate != nil && !impliesPredicate(pred, idx.Predicate, tab) {
			continue
		}
		if idx.Spatial {
			if alt, ok := spatialAlt(tab, idx, pred, needed, costStats, rows); ok {
				alts = append(alts, alt)
			}
			continue
		}
		if idx.Fulltext || idx.Vector {
			continue
		}
		if idx.HasExpr() && idx.KeyIsExpr(0) {
			if alt, ok := exprIndexAlt(tab, idx, pred, needed, costStats, rows); ok {
				alts = append(alts, cand{plan: alt.plan, node: alt.node, cost: alt.cost, rank: alt.rank, iname: alt.iname})
			} else if alt, ok := coveringPartialAlt(tab, idx, pred, needed, costStats, rows); ok {
				alts = append(alts, cand{plan: alt.plan, node: alt.node, cost: alt.cost, rank: alt.rank, iname: alt.iname})
			}
			continue
		}
		if len(idx.Path) > 0 {
			if alt, ok := pathIndexAlt(tab, idx, pred, needed, costStats, rows); ok {
				alts = append(alts, alt)
			}
			continue
		}
		matched := false
		if alt, ok := indexAlt(tab, &idx, false, ranges, pred, needed, costStats, rows); ok {
			alts = append(alts, alt)
			matched = true
		}
		if !matched {
			if alt, ok := coveringPartialAlt(tab, idx, pred, needed, costStats, rows); ok {
				alts = append(alts, cand{plan: alt.plan, node: alt.node, cost: alt.cost, rank: alt.rank, iname: alt.iname})
			}
		}
	}

	best := alts[0]
	for _, a := range alts[1:] {
		if a.cost < best.cost || (a.cost == best.cost && (a.rank < best.rank || (a.rank == best.rank && a.iname < best.iname))) {
			best = a
		}
	}
	best.plan = setAccessPartitions(best.plan, partitionIDs)
	addPartitionNodeDetail(best.node, partitionSuffix)
	return best.plan, best.node
}

// partitionCostStats merges compact NSPS sketches only when every requested
// stable identity has a local record. Any missing/stale record returns the
// global NSST snapshot, preventing DDL between ANALYZE runs from manufacturing
// unsupported selectivity claims.
func partitionCostStats(st *catalog.TableStats, ids []uint32) *catalog.TableStats {
	if st == nil || ids == nil {
		return st
	}
	if len(ids) == 0 {
		return &catalog.TableStats{Table: st.Table, TableID: st.TableID}
	}
	byID := make(map[uint32]catalog.PartitionStats, len(st.Partitions))
	for _, part := range st.Partitions {
		byID[part.ID] = part
	}
	parts := make([]catalog.PartitionStats, 0, len(ids))
	for _, id := range ids {
		part, ok := byID[id]
		if !ok || len(part.Columns) == 0 {
			return st
		}
		parts = append(parts, part)
	}
	out := &catalog.TableStats{Table: st.Table, TableID: st.TableID}
	for _, part := range parts {
		if ^uint64(0)-out.Rows < part.Rows {
			return st
		}
		out.Rows += part.Rows
	}

	for _, first := range parts[0].Columns {
		merged := first
		merged.Min, merged.Max = first.Min.Clone(), first.Max.Clone()
		merged.Histogram, merged.MCV = nil, nil
		weight := float64(parts[0].Rows)
		weightedCorrelation := first.Correlation * weight
		complete := true
		for _, part := range parts[1:] {
			var next catalog.ColumnStats
			found := false
			for _, candidate := range part.Columns {
				if candidate.Ord == first.Ord {
					next, found = candidate, true
					break
				}
			}
			if !found {
				complete = false
				break
			}
			merged.Nulls = satAddU64(merged.Nulls, next.Nulls)
			merged.NDV = satAddU64(merged.NDV, next.NDV)
			if next.HasMinMax {
				if !merged.HasMinMax {
					merged.Min, merged.Max, merged.HasMinMax = next.Min.Clone(), next.Max.Clone(), true
				} else {
					if cmp, err := next.Min.Cmp(merged.Min); err == nil && cmp < 0 {
						merged.Min = next.Min.Clone()
					}
					if cmp, err := next.Max.Cmp(merged.Max); err == nil && cmp > 0 {
						merged.Max = next.Max.Clone()
					}
				}
			}
			partWeight := float64(part.Rows)
			weightedCorrelation += next.Correlation * partWeight
			weight += partWeight
		}
		if !complete {
			continue
		}
		nonNull := out.Rows - min(out.Rows, merged.Nulls)
		if merged.NDV > nonNull {
			merged.NDV = nonNull
		}
		if weight > 0 {
			merged.Correlation = weightedCorrelation / weight
		}
		out.Columns = append(out.Columns, merged)
	}

	for _, first := range parts[0].Indexes {
		merged := first
		complete := true
		for _, part := range parts[1:] {
			found := false
			for _, candidate := range part.Indexes {
				if candidate.Name == first.Name {
					merged.NDV = satAddU64(merged.NDV, candidate.NDV)
					found = true
					break
				}
			}
			if !found {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		if merged.NDV > out.Rows {
			merged.NDV = out.Rows
		}
		if merged.Unique && out.Rows > 0 {
			merged.NDV, merged.Selectivity = out.Rows, 1/float64(out.Rows)
		} else if merged.NDV > 0 {
			merged.Selectivity = 1 / float64(merged.NDV)
		}
		out.Indexes = append(out.Indexes, merged)
	}

	for _, first := range parts[0].Vectors {
		merged := first
		complete := true
		for _, part := range parts[1:] {
			found := false
			for _, candidate := range part.Vectors {
				if candidate.Ord == first.Ord {
					merged.Count = satAddU64(merged.Count, candidate.Count)
					found = true
					break
				}
			}
			if !found {
				complete = false
				break
			}
		}
		if complete {
			out.Vectors = append(out.Vectors, merged)
		}
	}
	return out
}

func satAddU64(a, b uint64) uint64 {
	if ^uint64(0)-a < b {
		return ^uint64(0)
	}
	return a + b
}

// partitionRows returns the exact ANALYZE row count for a pruned stable-ID
// set. Missing/stale per-partition entries fall back to the global estimate so
// DDL between ANALYZE runs cannot produce a falsely empty plan.
func partitionRows(st *catalog.TableStats, ids []uint32, fallback uint64) uint64 {
	if ids == nil || st == nil || len(st.Partitions) == 0 {
		return fallback
	}
	if len(ids) == 0 {
		return 0
	}
	byID := make(map[uint32]uint64, len(st.Partitions))
	for _, part := range st.Partitions {
		byID[part.ID] = part.Rows
	}
	var rows uint64
	for _, id := range ids {
		n, ok := byID[id]
		if !ok {
			return fallback
		}
		if ^uint64(0)-rows < n {
			return fallback
		}
		rows += n
	}
	return rows
}

func partitionAccessDetail(tab *catalog.Table, pred ast.Expr) ([]uint32, string) {
	if tab == nil || tab.Partitioning == nil {
		return nil, ""
	}
	pruned := prunePartitionsForExplain(tab, pred)
	names := make([]string, 0, len(pruned))
	for _, part := range pruned {
		names = append(names, part.Name)
	}
	suffix := ""
	switch {
	case len(pruned) == 0:
		suffix = " partitions=[] (pruned all)"
	case len(pruned) < len(tab.Partitioning.Partitions):
		suffix = " partitions=[" + strings.Join(names, ",") + "]"
	default:
		suffix = " partitions=all[" + strconv.Itoa(len(pruned)) + "]"
		return nil, suffix
	}
	ids := make([]uint32, len(pruned))
	for i := range pruned {
		ids[i] = pruned[i].ID
	}
	return ids, suffix
}

func setAccessPartitions(plan planner.Logical, ids []uint32) planner.Logical {
	switch node := plan.(type) {
	case planner.SeqScan:
		node.Partitions = append([]uint32(nil), ids...)
		if ids != nil && len(ids) == 0 {
			node.Partitions = make([]uint32, 0)
		}
		return node
	case planner.IndexScan:
		node.Partitions = append([]uint32(nil), ids...)
		if ids != nil && len(ids) == 0 {
			node.Partitions = make([]uint32, 0)
		}
		return node
	case planner.Filter:
		node.Input = setAccessPartitions(node.Input, ids)
		return node
	default:
		return plan
	}
}

func addPartitionNodeDetail(node *Node, suffix string) {
	if node == nil || suffix == "" {
		return
	}
	if (node.Op == "SeqScan" || node.Op == "IndexScan") && !strings.Contains(node.Detail, " partitions=") {
		node.Detail += suffix
		return
	}
	for _, kid := range node.Kids {
		addPartitionNodeDetail(kid, suffix)
	}
}

func chooseSearch(n planner.Search, stats StatsFunc) (planner.Logical, *Node) {
	tab := n.Table
	pred := n.Residual
	needed := n.Needed
	if t, p, nd, ok := peelAccess(n.Input); ok {
		if tab == nil {
			tab = t
		}
		if p != nil {
			if pred != nil {
				pred = andAll([]ast.Expr{pred, p})
			} else {
				pred = p
			}
		}
		if needed == nil {
			needed = nd
		}
	}
	if tab == nil {
		return n, &Node{Op: "Search"}
	}
	if pred != nil {
		pred = foldExpr(pred)
		if predIsFalse(pred) {
			return planner.Empty{Names: namesOf(n)}, &Node{Op: "Empty", EstRows: 0}
		}
		if predIsTrue(pred) {
			pred = nil
		}
	}
	st := lookupStats(stats, tab)
	rows := tableRows(st)
	var idx *catalog.Index
	for i := range tab.Indexes {
		ix := tab.Indexes[i]
		if !ix.Fulltext || !catalog.IntsEqual(ix.Columns, n.Columns) {
			continue
		}
		if idx == nil || ix.Name < idx.Name {
			cp := ix
			idx = &cp
		}
	}
	if idx != nil {
		s := searchSel(st, idx.Name)
		cost, out := idxCost(rows, s, predSel(pred, tab, st), false, 0)
		plan := planner.Search{
			Table:     tab,
			IndexName: idx.Name,
			Columns:   append([]int(nil), n.Columns...),
			Weights:   append([]float64(nil), n.Weights...),
			Query:     n.Query,
			Residual:  pred,
			Needed:    needed,
		}
		detail := tableName(tab) + " " + idx.Name + " fulltext"
		if name := (fulltext.Analyzer{ID: idx.FTAnalyzer, Version: idx.FTVersion}).Name(); name != "" && name != "simple" {
			detail += " analyzer=" + name
		}
		detail += formatSearchWeights(n.Weights)
		if pred != nil {
			detail += " residual=" + formatExpr(pred)
		}
		return plan, &Node{Op: "Search", Detail: detail, EstRows: out, EstCost: cost, Index: idx.Name}
	}
	in, kid := chooseAccess(n.Input, stats)
	est, cost := int64(0), int64(0)
	if kid != nil {
		est, cost = kid.EstRows, kid.EstCost+kid.EstRows*cpuPred
	}
	plan := planner.Search{
		Input:   in,
		Table:   tab,
		Columns: append([]int(nil), n.Columns...),
		Weights: append([]float64(nil), n.Weights...),
		Query:   n.Query,
		Needed:  needed,
	}
	return plan, &Node{Op: "Search", Detail: tableName(tab) + " seq" + formatSearchWeights(n.Weights), EstRows: est, EstCost: cost, Kids: kids(kid)}
}

func chooseFacet(n planner.Facet, stats StatsFunc, underJoin bool) (planner.Logical, *Node) {
	in, kid := chooseAt(n.Input, stats, underJoin)
	n.Input = in
	rows, cost := int64(0), int64(0)
	if kid != nil {
		rows = kid.EstRows
		if len(n.Columns) > 0 {
			rows = kid.EstRows / 4
			if rows < 1 && kid.EstRows > 0 {
				rows = 1
			}
			rows *= int64(len(n.Columns))
		}
		cost = kid.EstCost + kid.EstRows*cpuTuple
	}
	if n.Limit >= 0 {
		capRows := n.Limit * int64(len(n.Columns))
		if n.Limit == 0 {
			rows = 0
		} else if capRows < rows {
			rows = capRows
		}
	}
	detail := strings.Join(n.Names, ",")
	if n.Limit >= 0 {
		detail += " limit=" + itoa64(n.Limit)
	}
	return n, &Node{Op: "Facet", Detail: detail, EstRows: rows, EstCost: cost, Kids: kids(kid)}
}

func formatSearchWeights(weights []float64) string {
	if fulltext.UniformWeights(weights) {
		return ""
	}
	parts := make([]string, len(weights))
	for i, w := range weights {
		parts[i] = strconv.FormatFloat(w, 'g', -1, 64)
	}
	return " weights=" + strings.Join(parts, ",")
}

func chooseNearest(n planner.Nearest, stats StatsFunc) (planner.Logical, *Node) {
	tab := n.Table
	pred := n.Residual
	needed := n.Needed
	if t, p, nd, ok := peelAccess(n.Input); ok {
		if tab == nil {
			tab = t
		}
		if p != nil {
			if pred != nil {
				pred = andAll([]ast.Expr{pred, p})
			} else {
				pred = p
			}
		}
		if needed == nil {
			needed = nd
		}
	}
	if tab == nil {
		return n, &Node{Op: "Nearest"}
	}
	if pred != nil {
		pred = foldExpr(pred)
		if predIsFalse(pred) {
			return planner.Empty{Names: namesOf(n)}, &Node{Op: "Empty", EstRows: 0}
		}
		if predIsTrue(pred) {
			pred = nil
		}
	}
	partitionIDs, partitionSuffix := partitionAccessDetail(tab, pred)
	st := lookupStats(stats, tab)
	rows := tableRows(st)
	var idx *catalog.Index
	for i := range tab.Indexes {
		ix := tab.Indexes[i]
		if !ix.Vector || len(ix.Columns) != 1 || ix.Columns[0] != n.Column {
			continue
		}
		if idx == nil || ix.Name < idx.Name {
			cp := ix
			idx = &cp
		}
	}
	outK := n.K
	if outK <= 0 {
		outK = int64(rows)
		if outK < 1 {
			outK = 10
		}
	}
	if idx != nil {
		s := sel(100_000)
		cost, out := idxCost(rows, s, predSel(pred, tab, partitionCostStats(st, partitionIDs)), false, 0)
		if out > outK {
			out = outK
		}
		plan := planner.Nearest{
			Table:      tab,
			IndexName:  idx.Name,
			Column:     n.Column,
			Query:      n.Query,
			Metric:     n.Metric,
			Residual:   pred,
			Needed:     needed,
			K:          n.K,
			Partitions: partitionIDs,
		}
		method := "hnsw"
		switch idx.VecMethod {
		case catalog.VecMethodIVF:
			method = "ivf"
		case catalog.VecMethodIVFPQ:
			method = "ivfpq"
		case catalog.VecMethodSPARSE:
			method = "sparse"
		}
		detail := tableName(tab) + " " + idx.Name + " " + method
		if n.Metric != "" {
			detail += " " + n.Metric
		}
		if pred != nil {
			detail += " residual=" + formatExpr(pred)
		}
		detail += partitionSuffix
		return plan, &Node{Op: "Nearest", Detail: detail, EstRows: out, EstCost: cost, Index: idx.Name}
	}
	in, kid := chooseAccess(n.Input, stats)
	est, cost := int64(0), int64(0)
	if kid != nil {
		est, cost = kid.EstRows, kid.EstCost+kid.EstRows*cpuPred
		if est > outK {
			est = outK
		}
	}
	plan := planner.Nearest{
		Input:      in,
		Table:      tab,
		Column:     n.Column,
		Query:      n.Query,
		Metric:     n.Metric,
		Needed:     needed,
		K:          n.K,
		Partitions: partitionIDs,
	}
	return plan, &Node{Op: "Nearest", Detail: tableName(tab) + " flat" + partitionSuffix, EstRows: est, EstCost: cost, Kids: kids(kid)}
}

func peelAccess(p planner.Logical) (*catalog.Table, ast.Expr, []int, bool) {
	switch n := p.(type) {
	case planner.Scan:
		return n.Table, nil, n.Needed, true
	case planner.SeqScan:
		return n.Table, nil, n.Needed, true
	case planner.IndexScan:
		return n.Table, n.Residual, n.Needed, true
	case planner.Filter:
		tab, innerPred, needed, ok := peelAccess(n.Input)
		if !ok {
			return nil, nil, nil, false
		}
		pred := n.Pred
		if innerPred != nil {
			pred = andAll([]ast.Expr{innerPred, n.Pred})
		}
		return tab, pred, needed, true
	default:
		return nil, nil, nil, false
	}
}

func indexAlt(tab *catalog.Table, idx *catalog.Index, pk bool, ranges map[int]colRange, pred ast.Expr, needed []int, st *catalog.TableStats, rows uint64) (struct {
	plan  planner.Logical
	node  *Node
	cost  int64
	rank  int
	iname string
}, bool) {
	var zero struct {
		plan  planner.Logical
		node  *Node
		cost  int64
		rank  int
		iname string
	}
	cols := tab.PK
	name := ""
	unique := true
	if !pk {
		if idx == nil || len(idx.Columns) == 0 {
			return zero, false
		}
		cols = idx.Columns
		name = idx.Name
		unique = idx.Unique
	}
	if len(cols) == 0 {
		return zero, false
	}
	lead, ok := ranges[cols[0]]
	if !ok {
		return zero, false
	}
	used := map[int]struct{}{cols[0]: {}}
	var low, high []types.Value
	lowIncl, highIncl := true, true
	eqPrefix := 0
	if lead.eq && lead.low != nil {
		v, err := coerceBound(*lead.low, tab, cols[0])
		if err != nil {
			return zero, false
		}
		low = append(low, v)
		high = append(high, v)
		eqPrefix = 1
		for i := 1; i < len(cols); i++ {
			cr, ok := ranges[cols[i]]
			if !ok || !cr.eq || cr.low == nil {
				break
			}
			cv, err := coerceBound(*cr.low, tab, cols[i])
			if err != nil {
				break
			}
			low = append(low, cv)
			high = append(high, cv)
			used[cols[i]] = struct{}{}
			eqPrefix++
		}
	} else {
		if lead.low != nil && !lead.unboundedLow {
			v, err := coerceBound(*lead.low, tab, cols[0])
			if err != nil {
				return zero, false
			}
			low = append(low, v)
			lowIncl = lead.lowIncl
		}
		if lead.high != nil && !lead.unboundedHigh {
			v, err := coerceBound(*lead.high, tab, cols[0])
			if err != nil {
				return zero, false
			}
			high = append(high, v)
			highIncl = lead.highIncl
		}
		if len(low) == 0 && len(high) == 0 && !lead.isNull {
			return zero, false
		}
		if lead.isNull {
			nv := types.Null(tab.Columns[cols[0]].Type)
			low = []types.Value{nv}
			high = []types.Value{nv}
		}
	}

	var residual []ast.Expr
	for _, c := range conjuncts(pred) {
		if !conjunctCovered(c, tab, used) {
			residual = append(residual, c)
		}
	}
	res := andAll(residual)

	uniqueEq := unique && eqPrefix == len(cols)
	idxSel := predSel(rangePred(lead, tab), tab, st)
	if uniqueEq {
		idxSel = sel((uint64(selUnit)) / maxU64(rows, 1))
		if idxSel <= 0 {
			idxSel = 1
		}
	} else if eqPrefix > 0 && st != nil {
		if cs, ok := st.Column(cols[0]); ok && cs.NDV > 0 {
			idxSel = sel(uint64(selUnit) / cs.NDV)
		}
	}
	resSel := predSel(res, tab, st)
	corr := 0.0
	if cs, ok := statsCol(st, cols[0]); ok {
		corr = cs.Correlation
	}
	cost, out := idxCost(rows, idxSel, resSel, uniqueEq, corr)
	if pk && !uniqueEq {
		cost, out = clusteredRangeCost(rows, idxSel, resSel)
	}

	if !pk && idx != nil && idx.Predicate != nil {
		res = andAll(dropImplied(res, idx.Predicate))
	}
	scan := planner.IndexScan{
		Table:     tab,
		IndexName: name,
		PK:        pk,
		Unique:    unique,
		Columns:   append([]int(nil), cols...),
		Low:       low,
		High:      high,
		LowIncl:   lowIncl,
		HighIncl:  highIncl,
		Residual:  res,
		Needed:    needed,
	}
	detail := tableName(tab)
	iname := name
	rank := 1
	if pk {
		detail += " pk"
		iname = ""
		rank = 0
	} else {
		detail += " " + name
	}
	if !pk && idx != nil {
		cover := coveringCols(*idx, tab, needed, res)
		if idx.Covers(cover, tab) {
			scan.IndexOnly = true
			detail += " covering"
			if cost > seqPage {
				cost -= seqPage
			}
		}
	}
	if res != nil {
		detail += " residual=" + formatExpr(res)
	}
	node := &Node{Op: "IndexScan", Detail: detail, EstRows: out, EstCost: cost, Index: name}
	if pk {
		node.Index = "pk"
	}
	return struct {
		plan  planner.Logical
		node  *Node
		cost  int64
		rank  int
		iname string
	}{plan: scan, node: node, cost: cost, rank: rank, iname: iname}, true
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func coerceBound(v types.Value, tab *catalog.Table, ord int) (types.Value, error) {
	if ord < 0 || ord >= len(tab.Columns) {
		return v, nil
	}
	return types.Coerce(v, tab.Columns[ord].Type)
}

func rangePred(r colRange, tab *catalog.Table) ast.Expr {
	if r.eq && r.low != nil {
		return ast.Binary{Op: "=", Left: ast.Ident{Name: tab.Columns[r.ord].Name}, Right: ast.Literal{Value: *r.low}}
	}
	if r.ord < 0 || r.ord >= len(tab.Columns) {
		return ast.Literal{Value: types.BoolValue(true)}
	}
	id := ast.Ident{Name: tab.Columns[r.ord].Name}
	var bounds []ast.Expr
	if r.low != nil && !r.unboundedLow {
		op := ">"
		if r.lowIncl {
			op = ">="
		}
		bounds = append(bounds, ast.Binary{Op: op, Left: id, Right: ast.Literal{Value: *r.low}})
	}
	if r.high != nil && !r.unboundedHigh {
		op := "<"
		if r.highIncl {
			op = "<="
		}
		bounds = append(bounds, ast.Binary{Op: op, Left: id, Right: ast.Literal{Value: *r.high}})
	}
	if pred := andAll(bounds); pred != nil {
		return pred
	}
	return ast.Literal{Value: types.BoolValue(true)}
}

func conjunctCovered(e ast.Expr, tab *catalog.Table, used map[int]struct{}) bool {
	switch x := e.(type) {
	case ast.Binary:
		ord, _, _, ok := sargable(x, tab)
		if !ok {
			return false
		}
		_, ok = used[ord]
		return ok
	case ast.Between:
		id, ok := x.Expr.(ast.Ident)
		if !ok {
			return false
		}
		ord, ok := tab.ColIndex(id.Name)
		if !ok {
			return false
		}
		_, ok = used[ord]
		return ok
	case ast.IsNull:
		id, ok := x.Expr.(ast.Ident)
		if !ok {
			return false
		}
		ord, ok := tab.ColIndex(id.Name)
		if !ok {
			return false
		}
		_, ok = used[ord]
		return ok
	default:
		return false
	}
}

func extractRanges(pred ast.Expr, tab *catalog.Table) map[int]colRange {
	out := map[int]colRange{}
	if pred == nil || tab == nil {
		return out
	}
	for _, c := range conjuncts(pred) {
		switch x := c.(type) {
		case ast.Binary:
			ord, val, op, ok := sargable(x, tab)
			if !ok {
				continue
			}
			cur, exists := out[ord]
			if !exists {
				cur = colRange{ord: ord, unboundedLow: true, unboundedHigh: true}
			}
			v := val
			switch op {
			case "=":
				cur.eq = true
				cur.low, cur.high = &v, &v
				cur.lowIncl, cur.highIncl = true, true
				cur.unboundedLow, cur.unboundedHigh = false, false
			case "<":
				cur.high = &v
				cur.highIncl = false
				cur.unboundedHigh = false
			case "<=":
				cur.high = &v
				cur.highIncl = true
				cur.unboundedHigh = false
			case ">":
				cur.low = &v
				cur.lowIncl = false
				cur.unboundedLow = false
			case ">=":
				cur.low = &v
				cur.lowIncl = true
				cur.unboundedLow = false
			default:
				continue
			}
			out[ord] = cur
		case ast.Between:
			if x.Not {
				continue
			}
			id, ok := x.Expr.(ast.Ident)
			if !ok {
				continue
			}
			ord, ok := tab.ColIndex(id.Name)
			if !ok {
				continue
			}
			lo, ok1 := constValue(x.Low)
			hi, ok2 := constValue(x.High)
			if !ok1 || !ok2 {
				continue
			}
			out[ord] = colRange{ord: ord, low: &lo, high: &hi, lowIncl: true, highIncl: true}
		case ast.IsNull:
			if x.Not {
				continue
			}
			id, ok := x.Expr.(ast.Ident)
			if !ok {
				continue
			}
			ord, ok := tab.ColIndex(id.Name)
			if !ok {
				continue
			}
			out[ord] = colRange{ord: ord, isNull: true, eq: true}
		}
	}
	return out
}

func pruneSegments(tab *catalog.Table, pred ast.Expr, st *catalog.TableStats, needed []int) ([]planner.SegmentSpan, bool) {
	if st == nil || len(st.Segments) == 0 || pred == nil {
		return nil, false
	}
	ranges := extractRanges(pred, tab)
	if len(ranges) == 0 {
		return nil, false
	}
	var keep []planner.SegmentSpan
	for _, seg := range st.Segments {
		if !seg.HasBounds {
			keep = append(keep, spanOf(seg))
			continue
		}
		if segmentPossible(seg, ranges) {
			keep = append(keep, spanOf(seg))
		}
	}
	return keep, true
}

func spanOf(seg catalog.SegmentStats) planner.SegmentSpan {
	return planner.SegmentSpan{
		ID:       seg.ID,
		Low:      cloneVals(seg.LowPK),
		High:     cloneVals(seg.HighPK),
		LowIncl:  true,
		HighIncl: true,
	}
}

func cloneVals(in []types.Value) []types.Value {
	if in == nil {
		return nil
	}
	out := make([]types.Value, len(in))
	for i := range in {
		out[i] = in[i].Clone()
	}
	return out
}

func segmentPossible(seg catalog.SegmentStats, ranges map[int]colRange) bool {
	for ord, r := range ranges {
		if ord < 0 || ord >= len(seg.ColMin) || ord >= len(seg.ColMax) {
			continue
		}
		min, max := seg.ColMin[ord], seg.ColMax[ord]
		if min.Null && max.Null {
			if r.eq && !r.isNull {
				return false
			}
			continue
		}
		if r.isNull {
			continue
		}
		if r.eq && r.low != nil {
			if (!min.Null && cmpLT(*r.low, min)) || (!max.Null && cmpGT(*r.low, max)) {
				return false
			}
		}
		if r.low != nil && !r.unboundedLow && !max.Null {
			if r.lowIncl && cmpGT(*r.low, max) {
				return false
			}
			if !r.lowIncl && cmpGE(*r.low, max) {
				return false
			}
		}
		if r.high != nil && !r.unboundedHigh && !min.Null {
			if r.highIncl && cmpLT(*r.high, min) {
				return false
			}
			if !r.highIncl && cmpLE(*r.high, min) {
				return false
			}
		}
	}
	return true
}

func spatialAlt(tab *catalog.Table, idx catalog.Index, pred ast.Expr, needed []int, st *catalog.TableStats, rows uint64) (struct {
	plan  planner.Logical
	node  *Node
	cost  int64
	rank  int
	iname string
}, bool) {
	var zero struct {
		plan  planner.Logical
		node  *Node
		cost  int64
		rank  int
		iname string
	}
	if !idx.Spatial || len(idx.Columns) != 1 {
		return zero, false
	}
	ord := idx.Columns[0]
	sp, ok := matchSpatial(pred, tab, ord)
	if !ok {
		return zero, false
	}
	var startH, endH uint64
	var nbits int
	switch sp.kind {
	case "dwithin":
		var w, s, e, n float64
		var world bool
		switch {
		case sp.planar:
			// GEOMETRY column: the radius is in the SRID's own coordinate
			// units, so grow the bbox directly. (The Z-order grid is WGS84
			// degree-scaled — a projected-CRS column still works via the
			// residual but with a wide range; docs/design-spatial.md §2.6.)
			bw, bs, be, bn, _, ok := types.GeoBBox(sp.center)
			if !ok {
				world = true
			} else {
				w, s, e, n = bw-sp.radius, bs-sp.radius, be+sp.radius, bn+sp.radius
			}
		case sp.center.IsPoint():
			w, s, e, n, world = types.CircleBBox(sp.center.Lon, sp.center.Lat, sp.radius)
		default:
			bw, bs, be, bn, wrap, ok := types.GeoBBox(sp.center)
			if !ok || wrap {
				world = true
			} else {
				w, s, e, n, world = types.ExpandBBox(bw, bs, be, bn, sp.radius)
			}
		}
		if world {
			startH, endH, nbits = 0, 0, 0
		} else {
			startH, endH, nbits = types.GeoHashRange(w, s, e, n)
		}
	case "within":
		w, s, e, n, wrap, ok := types.GeoBBox(sp.box)
		if !ok || wrap {
			startH, endH, nbits = 0, 0, 0
		} else {
			startH, endH, nbits = types.GeoHashRange(w, s, e, n)
		}
	default:
		return zero, false
	}
	lo, hi := types.GeoKeyBounds(startH, endH, nbits)
	idxSel := predSel(sp.expr, tab, st)
	resSel := predSel(andAll(dropConjunct(pred, sp.expr)), tab, st)
	cost, out := idxCost(rows, idxSel, resSel, false, 0)
	scan := planner.IndexScan{
		Table:     tab,
		IndexName: idx.Name,
		Spatial:   true,
		Columns:   append([]int(nil), idx.Columns...),
		GeoStart:  lo,
		GeoEnd:    hi,
		Residual:  pred,
		Needed:    needed,
	}
	node := &Node{
		Op:      "IndexScan",
		Detail:  tableName(tab) + " " + idx.Name + " spatial",
		EstRows: out,
		EstCost: cost,
		Index:   idx.Name,
	}
	return struct {
		plan  planner.Logical
		node  *Node
		cost  int64
		rank  int
		iname string
	}{plan: scan, node: node, cost: cost, rank: 1, iname: idx.Name}, true
}

type spatialMatch struct {
	kind   string
	center types.Value
	radius float64
	box    types.Value
	planar bool // GEOMETRY column: radius is in raw coordinate units, not metres
	expr   ast.Expr
}

func matchSpatial(pred ast.Expr, tab *catalog.Table, ord int) (spatialMatch, bool) {
	for _, c := range conjuncts(pred) {
		if m, ok := oneSpatial(c, tab, ord); ok {
			return m, true
		}
	}
	return spatialMatch{}, false
}

func oneSpatial(e ast.Expr, tab *catalog.Table, ord int) (spatialMatch, bool) {
	// General GEOMETRY / GEOGRAPHY columns: ST_Intersects / ST_DWithin /
	// ST_Contains / ST_Within against a constant geometry become a bbox
	// Z-order prefix range with the exact predicate kept as a residual
	// (docs/design-spatial.md §2.6).
	if ord >= 0 && ord < len(tab.Columns) && types.IsGeneralSpatial(tab.Columns[ord].Type.Kind) {
		if call, ok := e.(ast.Call); ok {
			name := strings.ToLower(call.Name)
			switch name {
			case "st_intersects", "st_contains", "st_within", "st_covers", "st_coveredby":
				if len(call.Args) != 2 {
					return spatialMatch{}, false
				}
				g, ok := geoGeneralColConst(call.Args[0], call.Args[1], tab, ord)
				if !ok {
					g, ok = geoGeneralColConst(call.Args[1], call.Args[0], tab, ord)
				}
				if !ok {
					return spatialMatch{}, false
				}
				return spatialMatch{kind: "within", box: g, expr: e}, true
			case "st_dwithin":
				if len(call.Args) != 3 {
					return spatialMatch{}, false
				}
				g, ok := geoGeneralColConst(call.Args[0], call.Args[1], tab, ord)
				if !ok {
					g, ok = geoGeneralColConst(call.Args[1], call.Args[0], tab, ord)
				}
				if !ok {
					return spatialMatch{}, false
				}
				r, ok := constValue(call.Args[2])
				if !ok || r.Null {
					return spatialMatch{}, false
				}
				meters, err := strconv.ParseFloat(r.String(), 64)
				if err != nil || meters < 0 {
					return spatialMatch{}, false
				}
				return spatialMatch{
					kind:   "dwithin",
					center: g,
					radius: meters,
					planar: tab.Columns[ord].Type.Kind == types.KindGeometry,
					expr:   e,
				}, true
			}
		}
	}
	switch x := e.(type) {
	case ast.Call:
		switch types.CanonGeoName(x.Name) {
		case "dwithin":
			if len(x.Args) != 3 {
				return spatialMatch{}, false
			}
			geom, ok := geoColGeom(x.Args[0], x.Args[1], tab, ord)
			if !ok {
				geom, ok = geoColGeom(x.Args[1], x.Args[0], tab, ord)
			}
			if !ok {
				return spatialMatch{}, false
			}
			r, ok := constValue(x.Args[2])
			if !ok || r.Null {
				return spatialMatch{}, false
			}
			meters, err := strconv.ParseFloat(r.String(), 64)
			if err != nil || meters < 0 {
				return spatialMatch{}, false
			}
			return spatialMatch{kind: "dwithin", center: geom, radius: meters, expr: e}, true
		case "within":
			if len(x.Args) != 2 {
				return spatialMatch{}, false
			}
			reg, ok := geoColRegion(x.Args[0], x.Args[1], tab, ord)
			if !ok {
				return spatialMatch{}, false
			}
			return spatialMatch{kind: "within", box: reg, expr: e}, true
		case "covers":
			if len(x.Args) != 2 {
				return spatialMatch{}, false
			}
			reg, ok := geoColRegion(x.Args[1], x.Args[0], tab, ord)
			if !ok {
				return spatialMatch{}, false
			}
			return spatialMatch{kind: "within", box: reg, expr: e}, true
		}
	case ast.Binary:
		if x.Op != "<" && x.Op != "<=" {
			return spatialMatch{}, false
		}
		call, ok := x.Left.(ast.Call)
		if !ok || types.CanonGeoName(call.Name) != "distance" || len(call.Args) != 2 {
			return spatialMatch{}, false
		}
		_, pt, ok := geoColConst(call.Args[0], call.Args[1], tab, ord, types.KindPoint)
		if !ok {
			_, pt, ok = geoColConst(call.Args[1], call.Args[0], tab, ord, types.KindPoint)
		}
		if !ok {
			return spatialMatch{}, false
		}
		r, ok := constValue(x.Right)
		if !ok || r.Null {
			return spatialMatch{}, false
		}
		meters, err := strconv.ParseFloat(r.String(), 64)
		if err != nil || meters < 0 {
			return spatialMatch{}, false
		}
		return spatialMatch{kind: "dwithin", center: pt, radius: meters, expr: e}, true
	}
	return spatialMatch{}, false
}

func geoColConst(a, b ast.Expr, tab *catalog.Table, ord int, want types.Kind) (int, types.Value, bool) {
	id, ok := a.(ast.Ident)
	if !ok {
		return 0, types.Value{}, false
	}
	i, ok := tab.ColIndex(id.Name)
	if !ok || i != ord {
		return 0, types.Value{}, false
	}
	v, ok := constValue(b)
	if !ok || v.Null {
		return 0, types.Value{}, false
	}
	if v.Typ.Kind != want {
		c, err := types.Coerce(v, types.Type{Kind: want})
		if err != nil {
			return 0, types.Value{}, false
		}
		v = c
	}
	return i, v, true
}

func geoColGeom(colExpr, constExpr ast.Expr, tab *catalog.Table, ord int) (types.Value, bool) {
	_, v, ok := geoColConst(colExpr, constExpr, tab, ord, types.KindPoint)
	if ok && (v.IsPoint() || v.IsLine() || v.IsPolygon()) {
		return v, true
	}
	id, ok := colExpr.(ast.Ident)
	if !ok {
		return types.Value{}, false
	}
	i, ok := tab.ColIndex(id.Name)
	if !ok || i != ord {
		return types.Value{}, false
	}
	v, ok = constValue(constExpr)
	if !ok || v.Null {
		return types.Value{}, false
	}
	switch v.Typ.Kind {
	case types.KindPoint, types.KindLine, types.KindPolygon:
		return v, true
	case types.KindString, types.KindText:
		g, err := types.ParseWKT(v.Str)
		if err != nil {
			return types.Value{}, false
		}
		switch g.Typ.Kind {
		case types.KindPoint, types.KindLine, types.KindPolygon:
			return g, true
		}
	}
	return types.Value{}, false
}

// geoGeneralColConst matches "<general-spatial column> <op> <constant
// geometry>" and evaluates the constant to a KindGeometry value whose bbox
// drives the spatial-index range.
func geoGeneralColConst(colExpr, constExpr ast.Expr, tab *catalog.Table, ord int) (types.Value, bool) {
	id, ok := colExpr.(ast.Ident)
	if !ok {
		return types.Value{}, false
	}
	if i, ok := tab.ColIndex(id.Name); !ok || i != ord {
		return types.Value{}, false
	}
	return geoConstGeom(constExpr)
}

// geoConstGeom evaluates a constant geometry expression: a WKT/EWKT string
// literal, or an ST_GeomFromText / ST_GeogFromText / ST_GeomFromEWKT /
// ST_Point / ST_MakePoint call whose arguments are all literals.
func geoConstGeom(e ast.Expr) (types.Value, bool) {
	if v, ok := constValue(e); ok && !v.Null {
		switch v.Typ.Kind {
		case types.KindString, types.KindText:
			if g, err := types.ParseGeneralWKT(v.Str, 0); err == nil {
				return types.Value{Typ: types.Type{Kind: types.KindGeometry}, Geom: g}, true
			}
		}
		if types.IsGeneralSpatial(v.Typ.Kind) {
			return v, true
		}
	}
	call, ok := e.(ast.Call)
	if !ok {
		return types.Value{}, false
	}
	nums := func(args []ast.Expr) ([]float64, bool) {
		out := make([]float64, 0, len(args))
		for _, a := range args {
			cv, ok := constValue(a)
			if !ok || cv.Null {
				return nil, false
			}
			f, err := strconv.ParseFloat(cv.String(), 64)
			if err != nil {
				return nil, false
			}
			out = append(out, f)
		}
		return out, true
	}
	switch strings.ToLower(call.Name) {
	case "st_geomfromtext", "st_geometryfromtext", "st_geogfromtext", "st_geographyfromtext", "st_geomfromewkt":
		if len(call.Args) < 1 {
			return types.Value{}, false
		}
		sv, ok := constValue(call.Args[0])
		if !ok || sv.Null {
			return types.Value{}, false
		}
		g, err := types.ParseGeneralWKT(sv.String(), 0)
		if err != nil {
			return types.Value{}, false
		}
		return types.Value{Typ: types.Type{Kind: types.KindGeometry}, Geom: g}, true
	case "st_point", "st_makepoint":
		xy, ok := nums(call.Args)
		if !ok || len(xy) < 2 {
			return types.Value{}, false
		}
		return types.Value{Typ: types.Type{Kind: types.KindGeometry}, Geom: &types.Geom{Type: 1, Coords: []float64{xy[0], xy[1]}}}, true
	}
	return types.Value{}, false
}

func geoColRegion(colExpr, constExpr ast.Expr, tab *catalog.Table, ord int) (types.Value, bool) {
	if _, box, ok := geoColConst(colExpr, constExpr, tab, ord, types.KindBox); ok {
		return box, true
	}
	id, ok := colExpr.(ast.Ident)
	if !ok {
		return types.Value{}, false
	}
	i, ok := tab.ColIndex(id.Name)
	if !ok || i != ord {
		return types.Value{}, false
	}
	v, ok := constValue(constExpr)
	if !ok || v.Null {
		return types.Value{}, false
	}
	switch v.Typ.Kind {
	case types.KindBox, types.KindPolygon:
		return v, true
	case types.KindString, types.KindText:
		g, err := types.ParseWKT(v.Str)
		if err != nil {
			return types.Value{}, false
		}
		if g.IsBox() || g.IsPolygon() {
			return g, true
		}
	}
	return types.Value{}, false
}

func joinKeys(pred ast.Expr, schema *catalog.Table, left, right planner.Logical) ([]int, []int) {
	nLeft := 0
	if lt := tableOf(left); lt != nil {
		nLeft = len(lt.Columns)
	}
	var lk, rk []int
	for _, c := range conjuncts(pred) {
		b, ok := c.(ast.Binary)
		if !ok || b.Op != "=" {
			continue
		}
		a, aok := identOrd(b.Left, schema)
		d, dok := identOrd(b.Right, schema)
		if !aok || !dok {
			continue
		}
		if a < nLeft && d >= nLeft {
			lk = append(lk, a)
			rk = append(rk, d-nLeft)
		} else if d < nLeft && a >= nLeft {
			lk = append(lk, d)
			rk = append(rk, a-nLeft)
		}
	}
	return lk, rk
}

func identOrd(e ast.Expr, tab *catalog.Table) (int, bool) {
	if tab == nil {
		return 0, false
	}
	switch x := e.(type) {
	case ast.Ident:
		return tab.ColIndex(x.Name)
	case ast.Path:
		if len(x.Parts) == 2 {
			if i, ok := tab.ColIndex(x.Parts[0] + "." + x.Parts[1]); ok {
				return i, true
			}
			return tab.ColIndex(x.Parts[1])
		}
	}
	return 0, false
}

func mergeSorted(l, r planner.Logical, lk, rk []int) bool {
	if len(lk) == 0 || len(rk) == 0 {
		return false
	}
	return sortedOn(l, lk) && sortedOn(r, rk)
}

func sortedOn(p planner.Logical, keys []int) bool {
	switch n := p.(type) {
	case planner.IndexScan:
		if len(n.Columns) < len(keys) {
			return false
		}
		for i, k := range keys {
			if n.Columns[i] != k {
				return false
			}
		}
		return true
	case planner.Filter:
		return sortedOn(n.Input, keys)
	case planner.Project:
		return sortedOn(n.Input, keys)
	default:
		return false
	}
}

func pathIndexAlt(tab *catalog.Table, idx catalog.Index, pred ast.Expr, needed []int, st *catalog.TableStats, rows uint64) (struct {
	plan  planner.Logical
	node  *Node
	cost  int64
	rank  int
	iname string
}, bool) {
	var zero struct {
		plan  planner.Logical
		node  *Node
		cost  int64
		rank  int
		iname string
	}
	if len(idx.Path) == 0 || len(idx.Columns) != 1 {
		return zero, false
	}
	lead, usedExpr, ok := matchPathRange(pred, tab, idx.Columns[0], idx.Path)
	if !ok {
		return zero, false
	}
	var low, high []types.Value
	lowIncl, highIncl := true, true
	eqPrefix := 0
	if lead.eq && lead.low != nil {
		low = []types.Value{*lead.low}
		high = []types.Value{*lead.low}
		eqPrefix = 1
	} else if lead.isNull {
		nv := types.Null(types.JSON())
		low = []types.Value{nv}
		high = []types.Value{nv}
	} else {
		if lead.low != nil && !lead.unboundedLow {
			low = []types.Value{*lead.low}
			lowIncl = lead.lowIncl
		}
		if lead.high != nil && !lead.unboundedHigh {
			high = []types.Value{*lead.high}
			highIncl = lead.highIncl
		}
		if len(low) == 0 && len(high) == 0 {
			return zero, false
		}
	}
	res := andAll(dropConjunct(pred, usedExpr))
	uniqueEq := idx.Unique && eqPrefix == 1
	idxSel := predSel(usedExpr, tab, st)
	if uniqueEq {
		idxSel = sel((uint64(selUnit)) / maxU64(rows, 1))
		if idxSel <= 0 {
			idxSel = 1
		}
	}
	resSel := predSel(res, tab, st)
	cost, out := idxCost(rows, idxSel, resSel, uniqueEq, 0)
	if idx.Predicate != nil {
		res = andAll(dropImplied(res, idx.Predicate))
	}
	scan := planner.IndexScan{
		Table:     tab,
		IndexName: idx.Name,
		Unique:    idx.Unique,
		Columns:   append([]int(nil), idx.Columns...),
		Low:       low,
		High:      high,
		LowIncl:   lowIncl,
		HighIncl:  highIncl,
		Residual:  res,
		Needed:    needed,
	}
	detail := tableName(tab) + " " + idx.Name + " json"
	cover := coveringCols(idx, tab, needed, res)
	if idx.Covers(cover, tab) {
		scan.IndexOnly = true
		detail += " covering"
		if cost > seqPage {
			cost -= seqPage
		}
	}
	if res != nil {
		detail += " residual=" + formatExpr(res)
	}
	node := &Node{Op: "IndexScan", Detail: detail, EstRows: out, EstCost: cost, Index: idx.Name}
	return struct {
		plan  planner.Logical
		node  *Node
		cost  int64
		rank  int
		iname string
	}{plan: scan, node: node, cost: cost, rank: 1, iname: idx.Name}, true
}

func matchPathRange(pred ast.Expr, tab *catalog.Table, ord int, path []string) (colRange, ast.Expr, bool) {
	for _, c := range conjuncts(pred) {
		if r, ok := onePathRange(c, tab, ord, path); ok {
			return r, c, true
		}
	}
	return colRange{}, nil, false
}

func onePathRange(e ast.Expr, tab *catalog.Table, ord int, path []string) (colRange, bool) {
	switch x := e.(type) {
	case ast.Binary:
		p, val, op, ok := sargablePath(x, tab, ord, path)
		if !ok {
			return colRange{}, false
		}
		cur := colRange{ord: p, unboundedLow: true, unboundedHigh: true}
		v := val
		switch op {
		case "=":
			cur.eq = true
			cur.low, cur.high = &v, &v
			cur.lowIncl, cur.highIncl = true, true
			cur.unboundedLow, cur.unboundedHigh = false, false
		case "<":
			cur.high = &v
			cur.highIncl = false
			cur.unboundedHigh = false
		case "<=":
			cur.high = &v
			cur.highIncl = true
			cur.unboundedHigh = false
		case ">":
			cur.low = &v
			cur.lowIncl = false
			cur.unboundedLow = false
		case ">=":
			cur.low = &v
			cur.lowIncl = true
			cur.unboundedLow = false
		default:
			return colRange{}, false
		}
		return cur, true
	case ast.Between:
		if x.Not || !pathExprMatch(x.Expr, tab, ord, path) {
			return colRange{}, false
		}
		lo, ok1 := constValue(x.Low)
		hi, ok2 := constValue(x.High)
		if !ok1 || !ok2 {
			return colRange{}, false
		}
		return colRange{ord: ord, low: &lo, high: &hi, lowIncl: true, highIncl: true}, true
	case ast.IsNull:
		if x.Not || !pathExprMatch(x.Expr, tab, ord, path) {
			return colRange{}, false
		}
		return colRange{ord: ord, isNull: true, eq: true}, true
	default:
		return colRange{}, false
	}
}

func sargablePath(x ast.Binary, tab *catalog.Table, ord int, path []string) (int, types.Value, string, bool) {
	if pathExprMatch(x.Left, tab, ord, path) {
		if v, ok := constValue(x.Right); ok {
			return ord, v, x.Op, true
		}
	}
	if pathExprMatch(x.Right, tab, ord, path) {
		if v, ok := constValue(x.Left); ok {
			return ord, v, flipOp(x.Op), true
		}
	}
	return 0, types.Value{}, "", false
}

func pathExprMatch(e ast.Expr, tab *catalog.Table, ord int, path []string) bool {
	p, ok := e.(ast.Path)
	if !ok || tab == nil || len(p.Parts) < 2 {
		return false
	}
	i, ok := tab.ColIndex(p.Parts[0])
	if !ok || i != ord {
		return false
	}
	if len(p.Parts)-1 != len(path) {
		return false
	}
	for j, part := range path {
		if p.Parts[j+1] != part {
			return false
		}
	}
	return true
}

func dropConjunct(pred, drop ast.Expr) []ast.Expr {
	var out []ast.Expr
	ds := formatExpr(drop)
	for _, c := range conjuncts(pred) {
		if formatExpr(c) != ds {
			out = append(out, c)
		}
	}
	return out
}

// prunePartitionsForExplain returns candidate partitions for EXPLAIN detail.
// Conservative: if predicate cannot be analyzed, returns all partitions.
// For RANGE we handle single-column equality/range; HASH/LIST/TENANT use equality.
func prunePartitionsForExplain(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	if tab == nil || tab.Partitioning == nil {
		return nil
	}
	all := tab.Partitioning.Partitions
	if where == nil {
		return all
	}
	switch tab.Partitioning.Kind {
	case catalog.PartitionRange:
		return pruneRangeForExplain(tab, where)
	case catalog.PartitionHash:
		return pruneHashForExplain(tab, where)
	case catalog.PartitionList:
		return pruneValueForExplain(tab, where)
	case catalog.PartitionLegacyTenant:
		return pruneValueForExplain(tab, where)
	default:
		return all
	}
}

// pruneRangeForExplain prunes RANGE partitions from a predicate. RANGE partition
// bounds are lexicographically ordered tuples covering the half-open interval
// [lower, upper) (lower inclusive, upper exclusive; nil is unbounded). The
// predicate is reduced to a query lower/upper bound prefix over the ordered
// partition-key columns: successive equality constraints extend both prefixes,
// and the first non-equality constraint contributes its own lower/upper literal
// and terminates the walk, because tuples between that column's bounds range
// freely over every later partition column. A partition survives only when the
// query bound interval can intersect its [lower, upper) tuple interval, so a
// predicate that also pins trailing partition-key columns prunes partitions that
// merely share a leading value.
func pruneRangeForExplain(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	p := tab.Partitioning
	if p == nil || len(p.Columns) == 0 {
		return p.Partitions
	}
	var qlo, qhi []types.Value
	// qloInc/qhiInc report whether the terminating bound of the corresponding
	// prefix is inclusive; qloRest/qhiRest order the unconstrained suffix
	// (-1 == -infinity, +1 == +infinity) when the prefix is shorter than a
	// partition bound tuple.
	qloInc, qhiInc := true, true
	for _, ord := range p.Columns {
		colType := tab.Columns[ord].Type
		eq, lower, upper, lowerInc, upperInc := extractRangeConstraintsForExplain(where, tab.Columns[ord].Name)
		if eq != nil {
			cv, err := types.Coerce(*eq, colType)
			if err != nil {
				break
			}
			qlo = append(qlo, cv)
			qhi = append(qhi, cv)
			continue
		}
		if lower != nil {
			if cv, err := types.Coerce(*lower, colType); err == nil {
				qlo = append(qlo, cv)
				qloInc = lowerInc
			}
		}
		if upper != nil {
			if cv, err := types.Coerce(*upper, colType); err == nil {
				qhi = append(qhi, cv)
				qhiInc = upperInc
			}
		}
		break
	}
	if len(qlo) == 0 && len(qhi) == 0 {
		return p.Partitions
	}
	qloRest := -1
	if !qloInc {
		qloRest = 1
	}
	qhiRest := 1
	if !qhiInc {
		qhiRest = -1
	}
	var out []catalog.Partition
	for _, part := range p.Partitions {
		lo, hi := part.Values[0], part.Values[1]
		prune := false
		// Query maximum tuple below the partition's inclusive lower bound.
		if lo != nil && len(qhi) > 0 {
			if c, ok := cmpBoundTuple(qhi, qhiRest, lo); ok {
				if c < 0 || (c == 0 && !qhiInc) {
					prune = true
				}
			}
		}
		// Query minimum tuple at or above the partition's exclusive upper bound.
		if !prune && hi != nil && len(qlo) > 0 {
			if c, ok := cmpBoundTuple(qlo, qloRest, hi); ok && c >= 0 {
				prune = true
			}
		}
		if !prune {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []catalog.Partition{}
	}
	return out
}

// cmpBoundTuple compares a query bound prefix q against a full partition bound
// tuple pt using the order-preserving key encoding. When q is a strict prefix of
// pt (all shared elements equal), restInf decides the result: -1 orders q's
// missing suffix as -infinity, +1 as +infinity. ok is false only when a value
// pair cannot be compared, in which case the caller must not prune.
func cmpBoundTuple(q []types.Value, restInf int, pt []types.Value) (int, bool) {
	m := len(q)
	if len(pt) < m {
		m = len(pt)
	}
	for i := 0; i < m; i++ {
		c, err := compareValuesForExplain([]types.Value{q[i]}, []types.Value{pt[i]})
		if err != nil {
			return 0, false
		}
		if c != 0 {
			return c, true
		}
	}
	if len(q) >= len(pt) {
		return 0, true
	}
	return restInf, true
}

func pruneValueForExplain(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	if tab.Partitioning == nil || len(tab.Partitioning.Columns) == 0 {
		return tab.Partitioning.Partitions
	}
	if len(tab.Partitioning.Columns) > 1 {
		return pruneMultiColumnValueForExplain(tab, where)
	}
	colOrd := tab.Partitioning.Columns[0]
	colName := tab.Columns[colOrd].Name
	vals, ok := extractEqualValuesForExplain(where, colName)
	if !ok || len(vals) == 0 {
		return tab.Partitioning.Partitions
	}
	lookup := make(map[string]catalog.Partition, len(tab.Partitioning.Partitions))
	for _, part := range tab.Partitioning.Partitions {
		for _, v := range part.Values {
			if len(v) == 1 {
				key, err := types.EncodeKey(v)
				if err != nil {
					return tab.Partitioning.Partitions
				}
				lookup[string(key)] = part
			}
		}
	}
	var out []catalog.Partition
	seen := make(map[uint32]struct{})
	for _, v := range vals {
		coerced, err := types.Coerce(v, tab.Columns[colOrd].Type)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		key, err := types.EncodeKey([]types.Value{coerced})
		if err != nil {
			return tab.Partitioning.Partitions
		}
		if part, ok := lookup[string(key)]; ok {
			if _, dup := seen[part.ID]; !dup {
				seen[part.ID] = struct{}{}
				out = append(out, part)
			}
		}
	}
	if len(out) == 0 {
		return []catalog.Partition{}
	}
	return out
}

// pruneMultiColumnValueForExplain prunes multi-column LIST partitions only when
// every partition column is pinned to a single equality value; the resulting
// tuple matches at most one partition's membership set. Any looser predicate
// keeps every partition.
func pruneMultiColumnValueForExplain(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	tuple := make([]types.Value, len(tab.Partitioning.Columns))
	for i, ord := range tab.Partitioning.Columns {
		vals, ok := extractEqualValuesForExplain(where, tab.Columns[ord].Name)
		if !ok || len(vals) != 1 {
			return tab.Partitioning.Partitions
		}
		coerced, err := types.Coerce(vals[0], tab.Columns[ord].Type)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		tuple[i] = coerced
	}
	key, err := types.EncodeKey(tuple)
	if err != nil {
		return tab.Partitioning.Partitions
	}
	for _, part := range tab.Partitioning.Partitions {
		for _, v := range part.Values {
			ek, err := types.EncodeKey(v)
			if err != nil {
				return tab.Partitioning.Partitions
			}
			if string(ek) == string(key) {
				return []catalog.Partition{part}
			}
		}
	}
	return []catalog.Partition{}
}

func pruneHashForExplain(tab *catalog.Table, where ast.Expr) []catalog.Partition {
	if tab == nil || tab.Partitioning == nil {
		return nil
	}
	if len(tab.Partitioning.Columns) == 0 || len(tab.Partitioning.Partitions) == 0 {
		return tab.Partitioning.Partitions
	}
	modulus := tab.Partitioning.Partitions[0].Modulus
	byRemainder := make(map[uint32]catalog.Partition, len(tab.Partitioning.Partitions))
	for _, part := range tab.Partitioning.Partitions {
		byRemainder[part.Remainder] = part
	}
	// Multi-column HASH prunes only when every partition column is pinned to a
	// single equality value; the tuple then hashes to exactly one partition.
	if len(tab.Partitioning.Columns) > 1 {
		tuple := make([]types.Value, len(tab.Partitioning.Columns))
		for i, ord := range tab.Partitioning.Columns {
			cvals, cok := extractEqualValuesForExplain(where, tab.Columns[ord].Name)
			if !cok || len(cvals) != 1 {
				return tab.Partitioning.Partitions
			}
			coerced, err := types.Coerce(cvals[0], tab.Columns[ord].Type)
			if err != nil {
				return tab.Partitioning.Partitions
			}
			tuple[i] = coerced
		}
		remainder, err := catalog.HashPartitionRemainder(tuple, modulus)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		if part, ok := byRemainder[remainder]; ok {
			return []catalog.Partition{part}
		}
		return tab.Partitioning.Partitions
	}
	colOrd := tab.Partitioning.Columns[0]
	vals, ok := extractEqualValuesForExplain(where, tab.Columns[colOrd].Name)
	if !ok || len(vals) == 0 {
		return tab.Partitioning.Partitions
	}
	out := make([]catalog.Partition, 0, len(vals))
	seen := make(map[uint32]struct{}, len(vals))
	for _, value := range vals {
		coerced, err := types.Coerce(value, tab.Columns[colOrd].Type)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		remainder, err := catalog.HashPartitionRemainder([]types.Value{coerced}, modulus)
		if err != nil {
			return tab.Partitioning.Partitions
		}
		part, exists := byRemainder[remainder]
		if !exists {
			return tab.Partitioning.Partitions
		}
		if _, exists := seen[part.ID]; !exists {
			seen[part.ID] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

func extractRangeConstraintsForExplain(expr ast.Expr, col string) (eq *types.Value, lower *types.Value, upper *types.Value, lowerInc bool, upperInc bool) {
	var eqs []*types.Value
	var lowers []*types.Value
	var uppers []*types.Value
	var lowersInc []bool
	var uppersInc []bool
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch x := e.(type) {
		case ast.Binary:
			if x.Op == "and" || x.Op == "AND" {
				walk(x.Left)
				walk(x.Right)
				return
			}
			if isColForPrune(x.Left, col) {
				if v := literalToValueForPrune(x.Right); v != nil {
					switch x.Op {
					case "=":
						eqs = append(eqs, v)
					case "<":
						uppers = append(uppers, v)
						uppersInc = append(uppersInc, false)
					case "<=":
						uppers = append(uppers, v)
						uppersInc = append(uppersInc, true)
					case ">":
						lowers = append(lowers, v)
						lowersInc = append(lowersInc, false)
					case ">=":
						lowers = append(lowers, v)
						lowersInc = append(lowersInc, true)
					}
				}
			} else if isColForPrune(x.Right, col) {
				if v := literalToValueForPrune(x.Left); v != nil {
					switch x.Op {
					case "=":
						eqs = append(eqs, v)
					case "<":
						lowers = append(lowers, v)
						lowersInc = append(lowersInc, false)
					case "<=":
						lowers = append(lowers, v)
						lowersInc = append(lowersInc, true)
					case ">":
						uppers = append(uppers, v)
						uppersInc = append(uppersInc, false)
					case ">=":
						uppers = append(uppers, v)
						uppersInc = append(uppersInc, true)
					}
				}
			}
		case ast.Between:
			if isColForPrune(x.Expr, col) {
				if lv := literalToValueForPrune(x.Low); lv != nil {
					lowers = append(lowers, lv)
					lowersInc = append(lowersInc, true)
				}
				if hv := literalToValueForPrune(x.High); hv != nil {
					uppers = append(uppers, hv)
					uppersInc = append(uppersInc, true)
				}
			}
		}
	}
	walk(expr)
	if len(eqs) > 0 {
		return eqs[0], nil, nil, false, false
	}
	if len(lowers) > 0 {
		best := lowers[0]
		inc := lowersInc[0]
		for i := 1; i < len(lowers); i++ {
			cmp, _ := compareValuesForExplain([]types.Value{*best}, []types.Value{*lowers[i]})
			if cmp < 0 {
				best = lowers[i]
				inc = lowersInc[i]
			} else if cmp == 0 && !inc && lowersInc[i] {
				inc = false
			}
		}
		lower = best
		lowerInc = inc
	}
	if len(uppers) > 0 {
		best := uppers[0]
		inc := uppersInc[0]
		for i := 1; i < len(uppers); i++ {
			cmp, _ := compareValuesForExplain([]types.Value{*best}, []types.Value{*uppers[i]})
			if cmp > 0 {
				best = uppers[i]
				inc = uppersInc[i]
			} else if cmp == 0 && !inc && uppersInc[i] {
				inc = false
			}
		}
		upper = best
		upperInc = inc
	}
	return nil, lower, upper, lowerInc, upperInc
}

func extractEqualValuesForExplain(expr ast.Expr, col string) ([]types.Value, bool) {
	x, ok := expr.(ast.Binary)
	if !ok {
		return nil, false
	}
	switch strings.ToLower(x.Op) {
	case "and":
		left, leftOK := extractEqualValuesForExplain(x.Left, col)
		right, rightOK := extractEqualValuesForExplain(x.Right, col)
		if leftOK && rightOK {
			return append(left, right...), true
		}
		if leftOK {
			return left, true
		}
		if rightOK {
			return right, true
		}
		return nil, false
	case "or":
		left, leftOK := extractEqualValuesForExplain(x.Left, col)
		right, rightOK := extractEqualValuesForExplain(x.Right, col)
		if !leftOK || !rightOK {
			return nil, false
		}
		return append(left, right...), true
	case "=":
		if isColForPrune(x.Left, col) {
			if value := literalToValueForPrune(x.Right); value != nil {
				return []types.Value{*value}, true
			}
		} else if isColForPrune(x.Right, col) {
			if value := literalToValueForPrune(x.Left); value != nil {
				return []types.Value{*value}, true
			}
		}
	}
	return nil, false
}

func isColForPrune(expr ast.Expr, name string) bool {
	switch x := expr.(type) {
	case ast.Ident:
		return strings.EqualFold(x.Name, name)
	case ast.Path:
		if len(x.Parts) == 1 {
			return strings.EqualFold(x.Parts[0], name)
		}
		if len(x.Parts) == 2 {
			return strings.EqualFold(x.Parts[1], name)
		}
	}
	return false
}

func literalToValueForPrune(expr ast.Expr) *types.Value {
	switch x := expr.(type) {
	case ast.Literal:
		v := x.Value
		cv := v.Clone()
		return &cv
	case ast.Unary:
		if x.Op == "-" {
			if lit, ok := x.Right.(ast.Literal); ok {
				v := lit.Value
				cv := v.Clone()
				return &cv
			}
		}
	}
	return nil
}

func compareValuesForExplain(a, b []types.Value) (int, error) {
	ak, err := types.EncodeKey(a)
	if err != nil {
		return 0, err
	}
	bk, err := types.EncodeKey(b)
	if err != nil {
		return 0, err
	}
	return bytes.Compare(ak, bk), nil
}
