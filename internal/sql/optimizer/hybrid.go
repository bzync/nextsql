package optimizer

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
)

type hybAlt struct {
	plan planner.Logical
	node *Node
	cost int64
	rank int // 0=filter-ann, 1=search-ann, 2=ann-filter
	name string
}

func chooseHybrid(nr planner.Nearest, stats StatsFunc, underJoin bool) (planner.Logical, *Node) {
	search, pred, needed, tab, ok := peelHybrid(nr)
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
	ftIdx := fulltextIndex(tab, search.Column)

	var alts []hybAlt
	if alt, ok := hybridFilterANN(tab, pred, needed, nr, search, st, k, dim); ok {
		alts = append(alts, alt)
	}
	if alt, ok := hybridSearchANN(tab, pred, needed, nr, search, ftIdx, st, k, dim); ok {
		alts = append(alts, alt)
	}
	if vIdx != nil {
		if alt, ok := hybridANNFilter(tab, pred, needed, nr, search, vIdx, st, rows, k, dim); ok {
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

func peelHybrid(nr planner.Nearest) (planner.Search, ast.Expr, []int, *catalog.Table, bool) {
	search, ok := nr.Input.(planner.Search)
	if !ok {
		return planner.Search{}, nil, nil, nil, false
	}
	pred := nr.Residual
	if search.Residual != nil {
		if pred != nil {
			pred = andAll([]ast.Expr{pred, search.Residual})
		} else {
			pred = search.Residual
		}
	}
	tab := nr.Table
	if tab == nil {
		tab = search.Table
	}
	needed := nr.Needed
	if t, p, nd, ok := peelAccess(search.Input); ok {
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
	return search, pred, needed, tab, true
}

func hybridFilterANN(tab *catalog.Table, pred ast.Expr, needed []int, nr planner.Nearest, search planner.Search, st *catalog.TableStats, k int64, dim int) (hybAlt, bool) {
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
		Input:  in,
		Table:  tab,
		Kind:   "flat",
		Column: nr.Column,
		Query:  nr.Query,
		Metric: nr.Metric,
		Needed: needed,
		K:      k,
	}
	plan := wrapRerank(cands, tab, search, nr, k, "filter-ann")
	node := &Node{
		Op:      "Rerank",
		Detail:  "bm25+vector filter-ann",
		EstRows: out,
		EstCost: cost,
		Kids: []*Node{{
			Op:      "Candidates",
			Detail:  tableName(tab) + " flat",
			EstRows: accessRows,
			EstCost: accessCost + flatANNCost(accessRows, dim),
			Kids:    kids(kid),
		}},
	}
	return hybAlt{plan: plan, node: node, cost: cost, rank: 0, name: "filter-ann"}, true
}

func hybridSearchANN(tab *catalog.Table, pred ast.Expr, needed []int, nr planner.Nearest, search planner.Search, ft *catalog.Index, st *catalog.TableStats, k int64, dim int) (hybAlt, bool) {
	var inSrc planner.Logical = search.Input
	if inSrc == nil {
		inSrc = planner.Scan{Table: tab, Needed: needed}
		if pred != nil {
			inSrc = planner.Filter{Input: inSrc, Pred: pred}
		}
	}
	sp := planner.Search{
		Input:  inSrc,
		Table:  tab,
		Column: search.Column,
		Query:  search.Query,
		Needed: needed,
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
		Input:  in,
		Table:  tab,
		Kind:   "flat",
		Column: nr.Column,
		Query:  nr.Query,
		Metric: nr.Metric,
		Needed: needed,
		K:      k,
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
			Detail:  tableName(tab) + " flat after " + ftName,
			EstRows: ftRows,
			EstCost: ftCost + flatANNCost(ftRows, dim),
			Index:   idxName,
			Kids:    kids(kid),
		}},
	}
	return hybAlt{plan: plan, node: node, cost: cost, rank: 1, name: "search-ann"}, true
}

func hybridANNFilter(tab *catalog.Table, pred ast.Expr, needed []int, nr planner.Nearest, search planner.Search, vIdx *catalog.Index, st *catalog.TableStats, rows uint64, k int64, dim int) (hybAlt, bool) {
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
		Table:     tab,
		Kind:      "hnsw",
		IndexName: vIdx.Name,
		Column:    nr.Column,
		Query:     nr.Query,
		Metric:    nr.Metric,
		Residual:  pred,
		Needed:    needed,
		K:         over,
	}
	plan := wrapRerank(cands, tab, search, nr, k, "ann-filter")
	detail := tableName(tab) + " " + vIdx.Name + " hnsw"
	if pred != nil {
		detail += " residual=" + formatExpr(pred)
	}
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
		Input:        cands,
		Table:        tab,
		SearchCol:    search.Column,
		SearchQuery:  search.Query,
		NearestCol:   nr.Column,
		NearestQuery: nr.Query,
		Metric:       nr.Metric,
		K:            k,
		Method:       "bm25+vector",
		Strategy:     strategy,
	}
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

func fulltextIndex(tab *catalog.Table, col int) *catalog.Index {
	if tab == nil {
		return nil
	}
	var best *catalog.Index
	for i := range tab.Indexes {
		ix := tab.Indexes[i]
		if !ix.Fulltext || len(ix.Columns) != 1 || ix.Columns[0] != col {
			continue
		}
		if best == nil || ix.Name < best.Name {
			cp := ix
			best = &cp
		}
	}
	return best
}
