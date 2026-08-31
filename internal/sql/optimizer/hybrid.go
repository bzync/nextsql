package optimizer

import (
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

type hybAlt struct {
	plan planner.Logical
	node *Node
	cost int64
	rank int // 0=filter-ann, 1=search-ann, 2=ann-filter
	name string
}

func chooseHybrid(nr planner.Nearest, stats StatsFunc, underJoin bool) (planner.Logical, *Node) {
	search, extra, pred, needed, tab, ok := peelHybrid(nr)
	if !ok || tab == nil {
		return chooseNearest(nr, stats)
	}
	if pred != nil {
		pred = foldExpr(pred)
		if predIsFalse(pred) {
			return planner.Empty{Names: namesOf(nr)}, &Node{Op: "Empty", EstRows: 0}
		}
		if predIsTrue(pred) {
			pred = nil
		}
	}
	if extra != nil {
		return chooseFusion(nr, *extra, search, pred, needed, tab, stats, underJoin)
	}
	st := lookupStats(stats, tab)
	rows := tableRows(st)
	k := nr.K
	if k <= 0 && !underJoin {
		k = 10
		if rows > 0 && int64(rows) < k {
			k = int64(rows)
		}
	}
	dim := vecDim(tab, nr.Column, st)
	vIdx := vectorIndex(tab, nr.Column)
	ftIdx := fulltextIndex(tab, search.Columns)

	// A residual predicate that constrains the partition key narrows hybrid
	// candidate generation to the surviving partition-local vector graphs and
	// heaps, exactly like a bare NEAREST. partitionIDs is nil when nothing is
	// pruned; partitionSuffix is the EXPLAIN annotation for the Candidates node.
	partitionIDs, partitionSuffix := partitionAccessDetail(tab, pred)

	// Rerank scores every candidate on both the full-text and the vector
	// column, so candidate generation must project both even when the outer
	// query only needs the primary key (which would otherwise take a
	// covering-key shortcut that never reads the search column).
	needed = hybridNeeded(needed, append(append([]int(nil), search.Columns...), nr.Column)...)

	var alts []hybAlt
	if alt, ok := hybridFilterANN(tab, pred, needed, nr, search, st, k, dim, partitionIDs, partitionSuffix); ok {
		alts = append(alts, alt)
	}
	if alt, ok := hybridSearchANN(tab, pred, needed, nr, search, ftIdx, st, k, dim, partitionIDs, partitionSuffix); ok {
		alts = append(alts, alt)
	}
	if vIdx != nil {
		if alt, ok := hybridANNFilter(tab, pred, needed, nr, search, vIdx, st, rows, k, dim, partitionIDs, partitionSuffix); ok {
			alts = append(alts, alt)
		}
	}
	if len(alts) == 0 {
		return chooseNearest(nr, stats)
	}
	best := alts[0]
	for _, a := range alts[1:] {
		if a.cost < best.cost || (a.cost == best.cost && (a.rank < best.rank || (a.rank == best.rank && a.name < best.name))) {
			best = a
		}
	}
	return best.plan, best.node
}

// hybridNeeded adds the columns Rerank must read (full-text, vector) to a
// projection list. A nil list already means "every column" and is left alone.
func hybridNeeded(needed []int, cols ...int) []int {
	if needed == nil {
		return nil
	}
	out := append([]int(nil), needed...)
	for _, c := range cols {
		if c < 0 {
			continue
		}
		seen := false
		for _, e := range out {
			if e == c {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, c)
		}
	}
	return out
}

func peelHybrid(nr planner.Nearest) (planner.Search, *planner.Nearest, ast.Expr, []int, *catalog.Table, bool) {
	in := nr.Input
	pred := nr.Residual
	needed := nr.Needed
	tab := nr.Table
	var extra *planner.Nearest
	if inner, ok := in.(planner.Nearest); ok {
		cp := inner
		extra = &cp
		in = inner.Input
		if inner.Residual != nil {
			if pred != nil {
				pred = andAll([]ast.Expr{pred, inner.Residual})
			} else {
				pred = inner.Residual
			}
		}
		if tab == nil {
			tab = inner.Table
		}
		if needed == nil {
			needed = inner.Needed
		}
	}
	search, hasSearch := in.(planner.Search)
	access := in
	if hasSearch {
		if search.Residual != nil {
			if pred != nil {
				pred = andAll([]ast.Expr{pred, search.Residual})
			} else {
				pred = search.Residual
			}
		}
		if tab == nil {
			tab = search.Table
		}
		access = search.Input
	}
	if t, p, nd, ok := peelAccess(access); ok {
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
	return search, extra, pred, needed, tab, hasSearch || extra != nil
}

func hybridFilterANN(tab *catalog.Table, pred ast.Expr, needed []int, nr planner.Nearest, search planner.Search, st *catalog.TableStats, k int64, dim int, partitionIDs []uint32, partitionSuffix string) (hybAlt, bool) {
	var access planner.Logical = planner.Scan{Table: tab, Needed: needed}
	if pred != nil {
		access = planner.Filter{Input: access, Pred: pred}
	}
	in, kid := chooseAccess(access, statsFn(st, tab))
	accessRows, accessCost := int64(0), int64(0)
	if kid != nil {
		accessRows, accessCost = kid.EstRows, kid.EstCost
	}
	out := accessRows
	if k > 0 && out > k {
		out = k
	}
	cost := accessCost + flatANNCost(accessRows, dim) + rerankCost(accessRows)
	cands := planner.Candidates{
		Input:      in,
		Table:      tab,
		Kind:       "flat",
		Column:     nr.Column,
		Query:      nr.Query,
		Metric:     nr.Metric,
		Needed:     needed,
		K:          k,
		Partitions: partitionIDs,
	}
	plan := wrapRerank(cands, tab, search, nr, k, "filter-ann")
	node := &Node{
		Op:      "Rerank",
		Detail:  "bm25+vector filter-ann",
		EstRows: out,
		EstCost: cost,
		Kids: []*Node{{
			Op:      "Candidates",
			Detail:  tableName(tab) + " flat" + partitionSuffix,
			EstRows: accessRows,
			EstCost: accessCost + flatANNCost(accessRows, dim),
			Kids:    kids(kid),
		}},
	}
	return hybAlt{plan: plan, node: node, cost: cost, rank: 0, name: "filter-ann"}, true
}

func hybridSearchANN(tab *catalog.Table, pred ast.Expr, needed []int, nr planner.Nearest, search planner.Search, ft *catalog.Index, st *catalog.TableStats, k int64, dim int, partitionIDs []uint32, partitionSuffix string) (hybAlt, bool) {
	var inSrc planner.Logical = search.Input
	if inSrc == nil {
		inSrc = planner.Scan{Table: tab, Needed: needed}
		if pred != nil {
			inSrc = planner.Filter{Input: inSrc, Pred: pred}
		}
	}
	sp := planner.Search{
		Input:   inSrc,
		Table:   tab,
		Columns: append([]int(nil), search.Columns...),
		Weights: append([]float64(nil), search.Weights...),
		Query:   search.Query,
		Needed:  needed,
	}
	if ft != nil {
		sp.IndexName = ft.Name
	}
	in, kid := chooseSearch(sp, statsFn(st, tab))
	ftRows, ftCost := int64(0), int64(0)
	if kid != nil {
		ftRows, ftCost = kid.EstRows, kid.EstCost
	}
	out := ftRows
	if k > 0 && out > k {
		out = k
	}
	cost := ftCost + flatANNCost(ftRows, dim) + rerankCost(ftRows)
	cands := planner.Candidates{
		Input:      in,
		Table:      tab,
		Kind:       "flat",
		Column:     nr.Column,
		Query:      nr.Query,
		Metric:     nr.Metric,
		Needed:     needed,
		K:          k,
		Partitions: partitionIDs,
	}
	plan := wrapRerank(cands, tab, search, nr, k, "search-ann")
	ftName := "seq"
	idxName := ""
	if ft != nil {
		ftName = ft.Name + " fulltext"
		idxName = ft.Name
	}
	node := &Node{
		Op:      "Rerank",
		Detail:  "bm25+vector search-ann",
		EstRows: out,
		EstCost: cost,
		Kids: []*Node{{
			Op:      "Candidates",
			Detail:  tableName(tab) + " flat after " + ftName + partitionSuffix,
			EstRows: ftRows,
			EstCost: ftCost + flatANNCost(ftRows, dim),
			Index:   idxName,
			Kids:    kids(kid),
		}},
	}
	return hybAlt{plan: plan, node: node, cost: cost, rank: 1, name: "search-ann"}, true
}

func hybridANNFilter(tab *catalog.Table, pred ast.Expr, needed []int, nr planner.Nearest, search planner.Search, vIdx *catalog.Index, st *catalog.TableStats, rows uint64, k int64, dim int, partitionIDs []uint32, partitionSuffix string) (hybAlt, bool) {
	rs := predSel(pred, tab, st)
	over := overfetch(k, rs, rows)
	hnsw := hnswCost(rows, over, dim)
	after := applySel(uint64(over), rs)
	if after < 1 && over > 0 && rs > 0 {
		after = 1
	}
	cost := hnsw + over*cpuPred + rerankCost(after)
	out := after
	if k > 0 && out > k {
		out = k
	}
	cands := planner.Candidates{
		Table:      tab,
		Kind:       "hnsw",
		IndexName:  vIdx.Name,
		Column:     nr.Column,
		Query:      nr.Query,
		Metric:     nr.Metric,
		Residual:   pred,
		Needed:     needed,
		K:          over,
		Partitions: partitionIDs,
	}
	plan := wrapRerank(cands, tab, search, nr, k, "ann-filter")
	detail := tableName(tab) + " " + vIdx.Name + " hnsw"
	if pred != nil {
		detail += " residual=" + formatExpr(pred)
	}
	detail += partitionSuffix
	node := &Node{
		Op:      "Rerank",
		Detail:  "bm25+vector ann-filter",
		EstRows: out,
		EstCost: cost,
		Index:   vIdx.Name,
		Kids: []*Node{{
			Op:      "Candidates",
			Detail:  detail,
			EstRows: after,
			EstCost: hnsw + over*cpuPred,
			Index:   vIdx.Name,
		}},
	}
	return hybAlt{plan: plan, node: node, cost: cost, rank: 2, name: "ann-filter"}, true
}

func wrapRerank(cands planner.Candidates, tab *catalog.Table, search planner.Search, nr planner.Nearest, k int64, strategy string) planner.Logical {
	return planner.Rerank{
		Input:         cands,
		Table:         tab,
		SearchCols:    append([]int(nil), search.Columns...),
		SearchWeights: append([]float64(nil), search.Weights...),
		SearchQuery:   search.Query,
		NearestCol:    nr.Column,
		NearestQuery:  nr.Query,
		Metric:        nr.Metric,
		SparseCol:     -1,
		K:             k,
		Method:        "bm25+vector",
		Strategy:      strategy,
	}
}

func mapRerankInputs(n planner.Rerank, fn func(planner.Logical) planner.Logical) planner.Rerank {
	n.Input = fn(n.Input)
	if len(n.Extra) == 0 {
		return n
	}
	extra := make([]planner.Logical, len(n.Extra))
	for i, e := range n.Extra {
		extra[i] = fn(e)
	}
	n.Extra = extra
	return n
}

func colIsSparse(tab *catalog.Table, col int) bool {
	return tab != nil && col >= 0 && col < len(tab.Columns) && tab.Columns[col].Type.VecElem == types.VecSparse
}

func splitDenseSparse(a, b planner.Nearest, tab *catalog.Table) (dense, sparse planner.Nearest) {
	if colIsSparse(tab, a.Column) {
		return b, a
	}
	return a, b
}

func candKind(idx *catalog.Index) string {
	if idx != nil && idx.VecMethod == catalog.VecMethodSPARSE {
		return "sparse"
	}
	return "hnsw"
}

func chooseFusion(outer, inner planner.Nearest, search planner.Search, pred ast.Expr, needed []int, tab *catalog.Table, stats StatsFunc, underJoin bool) (planner.Logical, *Node) {
	st := lookupStats(stats, tab)
	rows := tableRows(st)
	k := outer.K
	if k <= 0 && inner.K > 0 {
		k = inner.K
	}
	if k <= 0 && !underJoin {
		k = 10
		if rows > 0 && int64(rows) < k {
			k = int64(rows)
		}
	}
	dense, sparse := splitDenseSparse(outer, inner, tab)
	partitionIDs, partitionSuffix := partitionAccessDetail(tab, pred)
	rs := predSel(pred, tab, st)
	over := overfetch(k, rs, rows)
	dIdx := vectorIndex(tab, dense.Column)
	sIdx := vectorIndex(tab, sparse.Column)
	hasSearch := search.Query != nil && len(search.Columns) > 0
	if hasSearch {
		needed = hybridNeeded(needed, search.Columns...)
	}
	needed = hybridNeeded(needed, dense.Column, sparse.Column)
	ftIdx := fulltextIndex(tab, search.Columns)

	var (
		extras    []planner.Logical
		traceKids []*Node
		cost      int64
		union     int64
	)
	add := func(p planner.Logical, node *Node, est, c int64) {
		extras = append(extras, p)
		if node != nil {
			traceKids = append(traceKids, node)
		}
		cost = satAdd(cost, c)
		union = satAdd(union, est)
	}

	denseDim := vecDim(tab, dense.Column, st)
	if dIdx != nil {
		c, out := idxCost(rows, sel(100_000), rs, false, 0)
		if out > over {
			out = over
		}
		cands := planner.Candidates{
			Table:      tab,
			Kind:       candKind(dIdx),
			IndexName:  dIdx.Name,
			Column:     dense.Column,
			Query:      dense.Query,
			Metric:     dense.Metric,
			Residual:   pred,
			Needed:     needed,
			K:          over,
			Partitions: partitionIDs,
		}
		detail := tableName(tab) + " " + dIdx.Name + " " + candKind(dIdx) + partitionSuffix
		add(cands, &Node{Op: "Candidates", Detail: detail, EstRows: out, EstCost: c, Index: dIdx.Name}, out, c)
	} else {
		var access planner.Logical = planner.Scan{Table: tab, Needed: needed}
		if pred != nil {
			access = planner.Filter{Input: access, Pred: pred}
		}
		in, kid := chooseAccess(access, statsFn(st, tab))
		accessRows, accessCost := int64(0), int64(0)
		if kid != nil {
			accessRows, accessCost = kid.EstRows, kid.EstCost
		}
		c := accessCost + flatANNCost(accessRows, denseDim)
		out := accessRows
		if over > 0 && out > over {
			out = over
		}
		cands := planner.Candidates{
			Input:      in,
			Table:      tab,
			Kind:       "flat",
			Column:     dense.Column,
			Query:      dense.Query,
			Metric:     dense.Metric,
			Needed:     needed,
			K:          over,
			Partitions: partitionIDs,
		}
		add(cands, &Node{Op: "Candidates", Detail: tableName(tab) + " flat" + partitionSuffix, EstRows: out, EstCost: c, Kids: kids(kid)}, out, c)
	}

	sparseDim := vecDim(tab, sparse.Column, st)
	if sIdx != nil {
		c, out := idxCost(rows, sel(100_000), rs, false, 0)
		if out > over {
			out = over
		}
		cands := planner.Candidates{
			Table:      tab,
			Kind:       "sparse",
			IndexName:  sIdx.Name,
			Column:     sparse.Column,
			Query:      sparse.Query,
			Metric:     sparse.Metric,
			Residual:   pred,
			Needed:     needed,
			K:          over,
			Partitions: partitionIDs,
		}
		detail := tableName(tab) + " " + sIdx.Name + " sparse" + partitionSuffix
		add(cands, &Node{Op: "Candidates", Detail: detail, EstRows: out, EstCost: c, Index: sIdx.Name}, out, c)
	} else {
		var access planner.Logical = planner.Scan{Table: tab, Needed: needed}
		if pred != nil {
			access = planner.Filter{Input: access, Pred: pred}
		}
		in, kid := chooseAccess(access, statsFn(st, tab))
		accessRows, accessCost := int64(0), int64(0)
		if kid != nil {
			accessRows, accessCost = kid.EstRows, kid.EstCost
		}
		c := accessCost + flatANNCost(accessRows, sparseDim)
		out := accessRows
		if over > 0 && out > over {
			out = over
		}
		cands := planner.Candidates{
			Input:      in,
			Table:      tab,
			Kind:       "flat",
			Column:     sparse.Column,
			Query:      sparse.Query,
			Metric:     sparse.Metric,
			Needed:     needed,
			K:          over,
			Partitions: partitionIDs,
		}
		add(cands, &Node{Op: "Candidates", Detail: tableName(tab) + " flat" + partitionSuffix, EstRows: out, EstCost: c, Kids: kids(kid)}, out, c)
	}

	if hasSearch {
		var inSrc planner.Logical = search.Input
		if inSrc == nil {
			inSrc = planner.Scan{Table: tab, Needed: needed}
			if pred != nil {
				inSrc = planner.Filter{Input: inSrc, Pred: pred}
			}
		}
		sp := planner.Search{
			Input:    inSrc,
			Table:    tab,
			Columns:  append([]int(nil), search.Columns...),
			Weights:  append([]float64(nil), search.Weights...),
			Query:    search.Query,
			Needed:   needed,
			Residual: pred,
		}
		if ftIdx != nil {
			sp.IndexName = ftIdx.Name
		}
		in, kid := chooseSearch(sp, statsFn(st, tab))
		ftRows, ftCost := int64(0), int64(0)
		if kid != nil {
			ftRows, ftCost = kid.EstRows, kid.EstCost
		}
		out := ftRows
		if over > 0 && out > over {
			out = over
		}
		var src planner.Logical = in
		if over > 0 {
			src = planner.Limit{Input: in, N: over}
		}
		add(src, kid, out, ftCost)
	}

	if len(extras) == 0 {
		return chooseNearest(outer, stats)
	}
	if union > int64(rows) && rows > 0 {
		union = int64(rows)
	}
	cost = satAdd(cost, rerankCost(union))
	out := union
	if k > 0 && out > k {
		out = k
	}
	method := "vector+sparse"
	if hasSearch {
		method = "bm25+vector+sparse"
	}
	plan := planner.Rerank{
		Input:         extras[0],
		Extra:         extras[1:],
		Table:         tab,
		SearchCols:    append([]int(nil), search.Columns...),
		SearchWeights: append([]float64(nil), search.Weights...),
		SearchQuery:   search.Query,
		NearestCol:    dense.Column,
		NearestQuery:  dense.Query,
		Metric:        dense.Metric,
		SparseCol:     sparse.Column,
		SparseQuery:   sparse.Query,
		SparseMetric:  sparse.Metric,
		K:             k,
		Method:        method,
		Strategy:      "fusion",
	}
	return plan, &Node{Op: "Rerank", Detail: method + " fusion", EstRows: out, EstCost: cost, Kids: traceKids}
}

func chooseCandidates(n planner.Candidates, stats StatsFunc) (planner.Logical, *Node) {
	if n.Input != nil {
		in, kid := choose(n.Input, stats)
		n.Input = in
		rows, cost := int64(0), int64(0)
		if kid != nil {
			rows, cost = kid.EstRows, kid.EstCost
		}
		return n, &Node{Op: "Candidates", Detail: candDetail(n), EstRows: rows, EstCost: cost, Index: n.IndexName, Kids: kids(kid)}
	}
	st := lookupStats(stats, n.Table)
	rows := tableRows(st)
	out := int64(rows)
	if n.K > 0 && out > n.K {
		out = n.K
	}
	cost := hnswCost(rows, n.K, vecDim(n.Table, n.Column, st))
	if n.Residual != nil {
		out = applySel(uint64(out), predSel(n.Residual, n.Table, st))
	}
	return n, &Node{Op: "Candidates", Detail: candDetail(n), EstRows: out, EstCost: cost, Index: n.IndexName}
}

func candDetail(n planner.Candidates) string {
	s := tableName(n.Table) + " " + n.Kind
	if n.IndexName != "" {
		s = tableName(n.Table) + " " + n.IndexName + " " + n.Kind
	}
	if n.Metric != "" && (n.Kind == "hnsw" || n.Kind == "flat") {
		s += " " + n.Metric
	}
	if n.Residual != nil {
		s += " residual=" + formatExpr(n.Residual)
	}
	if n.Partitions != nil && n.Table != nil && n.Table.Partitioning != nil {
		names := make([]string, 0, len(n.Partitions))
		for _, part := range n.Table.Partitioning.Partitions {
			for _, id := range n.Partitions {
				if part.ID == id {
					names = append(names, part.Name)
				}
			}
		}
		s += " partitions=[" + strings.Join(names, ",") + "]"
	}
	return s
}

func rerankDetail(n planner.Rerank) string {
	s := n.Method
	if s == "" {
		s = "bm25+vector"
	}
	if n.Strategy != "" {
		s += " " + n.Strategy
	}
	return s
}

func statsFn(st *catalog.TableStats, tab *catalog.Table) StatsFunc {
	if st == nil || tab == nil {
		return nil
	}
	return func(name string) (*catalog.TableStats, bool) {
		if name == tab.Name || name == st.Table {
			return st, true
		}
		return nil, false
	}
}

func vectorIndex(tab *catalog.Table, col int) *catalog.Index {
	if tab == nil {
		return nil
	}
	var best *catalog.Index
	for i := range tab.Indexes {
		ix := tab.Indexes[i]
		if !ix.Vector || len(ix.Columns) != 1 || ix.Columns[0] != col {
			continue
		}
		if best == nil || ix.Name < best.Name {
			cp := ix
			best = &cp
		}
	}
	return best
}

func fulltextIndex(tab *catalog.Table, cols []int) *catalog.Index {
	if tab == nil || len(cols) == 0 {
		return nil
	}
	var best *catalog.Index
	for i := range tab.Indexes {
		ix := tab.Indexes[i]
		if !ix.Fulltext || !catalog.IntsEqual(ix.Columns, cols) {
			continue
		}
		if best == nil || ix.Name < best.Name {
			cp := ix
			best = &cp
		}
	}
	return best
}
