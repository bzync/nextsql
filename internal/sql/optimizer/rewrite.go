package optimizer

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
)

func rewrite(p planner.Logical) planner.Logical {
	prev := ""
	for i := 0; i < 8; i++ {
		p = rewriteOnce(p)
		key := formatPlan(p)
		if key == prev {
			return p
		}
		prev = key
	}
	return p
}

func rewriteOnce(p planner.Logical) planner.Logical {
	if p == nil {
		return nil
	}
	switch n := p.(type) {
	case planner.With:
		for i := range n.CTEs {
			if n.CTEs[i].RecursiveOn {
				n.CTEs[i].Anchor = rewriteOnce(n.CTEs[i].Anchor)
				n.CTEs[i].Recursive = rewriteOnce(n.CTEs[i].Recursive)
			} else {
				n.CTEs[i].Input = rewriteOnce(n.CTEs[i].Input)
			}
		}
		n.Query = rewriteOnce(n.Query)
		return n
	case planner.CTEScan:
		return n
	case planner.SetOperation:
		n.Left = rewriteOnce(n.Left)
		n.Right = rewriteOnce(n.Right)
		return n
	case planner.Filter:
		return rewriteFilter(n)
	case planner.Project:
		return rewriteProject(n)
	case planner.Limit:
		return rewriteLimit(n)
	case planner.Sort:
		in := rewriteOnce(n.Input)
		if e, ok := in.(planner.Empty); ok {
			return e
		}
		n.Input = in
		return n
	case planner.Search:
		in := rewriteOnce(n.Input)
		if e, ok := in.(planner.Empty); ok {
			return e
		}
		n.Input = in
		return n
	case planner.Facet:
		in := rewriteOnce(n.Input)
		if n.Limit == 0 {
			return planner.Empty{Names: []string{"facet", "value", "count"}}
		}
		if e, ok := in.(planner.Empty); ok {
			e.Names = []string{"facet", "value", "count"}
			return e
		}
		n.Input = in
		return n
	case planner.Nearest:
		in := rewriteOnce(n.Input)
		if e, ok := in.(planner.Empty); ok {
			return e
		}
		n.Input = in
		return n
	case planner.Candidates:
		in := rewriteOnce(n.Input)
		if e, ok := in.(planner.Empty); ok {
			return e
		}
		n.Input = in
		return n
	case planner.Rerank:
		n = mapRerankInputs(n, rewriteOnce)
		if _, ok := n.Input.(planner.Empty); ok {
			kept := n.Extra[:0]
			for _, e := range n.Extra {
				if _, empty := e.(planner.Empty); !empty && e != nil {
					kept = append(kept, e)
				}
			}
			if len(kept) == 0 {
				return planner.Empty{Names: namesOf(n)}
			}
			n.Input = kept[0]
			n.Extra = kept[1:]
		} else if len(n.Extra) > 0 {
			kept := n.Extra[:0]
			for _, e := range n.Extra {
				if _, empty := e.(planner.Empty); !empty && e != nil {
					kept = append(kept, e)
				}
			}
			n.Extra = kept
		}
		return n
	case planner.Join:
		return rewriteJoin(n)
	case planner.Aggregate:
		return planner.Aggregate{
			Input:    rewriteOnce(n.Input),
			Groups:   n.Groups,
			Specs:    n.Specs,
			Exprs:    n.Exprs,
			Names:    n.Names,
			Schema:   n.Schema,
			Distinct: n.Distinct,
			Having:   n.Having,
		}
	case planner.Window:
		in := rewriteOnce(n.Input)
		if e, ok := in.(planner.Empty); ok {
			return e
		}
		n.Input = in
		return n
	case planner.Update:
		return planner.Update{Input: rewriteOnce(n.Input), Table: n.Table, Sets: n.Sets, Limit: n.Limit, Returning: n.Returning}
	case planner.Delete:
		return planner.Delete{Input: rewriteOnce(n.Input), Table: n.Table, Limit: n.Limit, Returning: n.Returning}
	case planner.Scan:
		return n
	default:
		return p
	}
}

func rewriteFilter(n planner.Filter) planner.Logical {
	pred := foldExpr(n.Pred)
	in := rewriteOnce(n.Input)
	if predIsFalse(pred) {
		return planner.Empty{Names: namesOf(in)}
	}
	if predIsTrue(pred) {
		return in
	}
	if e, ok := in.(planner.Empty); ok {
		return e
	}
	if pr, ok := in.(planner.Project); ok && !projectUsesAlias(pred, pr) {
		return planner.Project{
			Input:         planner.Filter{Input: pr.Input, Pred: pred},
			Cols:          pr.Cols,
			Exprs:         pr.Exprs,
			Names:         pr.Names,
			Distinct:      pr.Distinct,
			DistinctIndex: pr.DistinctIndex,
		}
	}
	if j, ok := in.(planner.Join); ok {
		return pushFilterJoin(pred, j)
	}
	if s, ok := in.(planner.Search); ok {
		s.Input = planner.Filter{Input: s.Input, Pred: pred}
		return rewriteOnce(s)
	}
	if nr, ok := in.(planner.Nearest); ok {
		nr.Input = planner.Filter{Input: nr.Input, Pred: pred}
		return rewriteOnce(nr)
	}
	if rr, ok := in.(planner.Rerank); ok {
		rr.Input = planner.Filter{Input: rr.Input, Pred: pred}
		return rewriteOnce(rr)
	}
	if f, ok := in.(planner.Filter); ok {
		return planner.Filter{Input: f.Input, Pred: foldExpr(andAll([]ast.Expr{f.Pred, pred}))}
	}
	if lim, ok := in.(planner.Limit); ok {
		// Filter above Limit is not pushed (would change semantics).
		return planner.Filter{Input: lim, Pred: pred}
	}
	return planner.Filter{Input: in, Pred: pred}
}

func rewriteProject(n planner.Project) planner.Logical {
	exprs := make([]ast.Expr, len(n.Exprs))
	for i, e := range n.Exprs {
		exprs[i] = foldExpr(e)
	}
	in := rewriteOnce(n.Input)
	if e, ok := in.(planner.Empty); ok {
		e.Names = append([]string(nil), n.Names...)
		return e
	}
	return planner.Project{Input: pruneInput(in, usedByProject(n)), Cols: n.Cols, Exprs: exprs, Names: n.Names, Distinct: n.Distinct, DistinctIndex: n.DistinctIndex}
}

func rewriteLimit(n planner.Limit) planner.Logical {
	in := rewriteOnce(n.Input)
	if n.N == 0 {
		return planner.Empty{Names: namesOf(in)}
	}
	if e, ok := in.(planner.Empty); ok {
		return e
	}
	if inner, ok := in.(planner.Limit); ok {
		n = planner.MergeLimit(n, inner)
		in = n.Input
		if n.N == 0 {
			return planner.Empty{Names: namesOf(in)}
		}
	}
	if s, ok := in.(planner.Sort); ok {
		if fetch := n.Fetch(); fetch > 0 && !s.OrderedDistinct {
			s.TopN = fetch
			in = s
		}
		return planner.Limit{Input: in, N: n.N, Offset: n.Offset}
	}
	if pr, ok := in.(planner.Project); ok && !pr.Distinct {
		return planner.Project{
			Input:         planner.Limit{Input: pr.Input, N: n.N, Offset: n.Offset},
			Cols:          pr.Cols,
			Exprs:         pr.Exprs,
			Names:         pr.Names,
			DistinctIndex: pr.DistinctIndex,
		}
	}
	if nr, ok := in.(planner.Nearest); ok {
		if k := n.Fetch(); k > 0 {
			nr.K = k
		}
		return planner.Limit{Input: nr, N: n.N, Offset: n.Offset}
	}
	if rr, ok := in.(planner.Rerank); ok {
		if k := n.Fetch(); k > 0 {
			rr.K = k
		}
		return planner.Limit{Input: rr, N: n.N, Offset: n.Offset}
	}
	// Do not copy LIMIT onto Nearest/Rerank.K through a join: an inner join
	// can drop top neighbors, and K would under-fetch the next rows that match.
	return planner.Limit{Input: in, N: n.N, Offset: n.Offset}
}

func rewriteJoin(n planner.Join) planner.Logical {
	left := rewriteOnce(n.Left)
	right := rewriteOnce(n.Right)
	pred := foldExpr(n.Pred)
	if n.Kind == ast.JoinRight {
		return rewriteOnce(rewriteRightToLeft(left, right, pred, n))
	}
	_, leftEmpty := left.(planner.Empty)
	_, rightEmpty := right.(planner.Empty)
	full := n.Kind == ast.JoinFull
	leftOuter := n.Kind == ast.JoinLeft
	semi := n.Kind == ast.JoinSemi
	anti := n.Kind == ast.JoinAnti
	outer := leftOuter || full
	if leftEmpty && rightEmpty {
		return planner.Empty{Names: namesOfJoin(n, left, right)}
	}
	if leftEmpty && !full {
		return planner.Empty{Names: namesOf(left)}
	}
	if rightEmpty && anti {
		return left
	}
	if rightEmpty && (semi || !outer) {
		if semi {
			return planner.Empty{Names: namesOf(left)}
		}
		return planner.Empty{Names: namesOf(right)}
	}
	if predIsFalse(pred) && anti {
		return left
	}
	if predIsFalse(pred) && !outer {
		if semi {
			return planner.Empty{Names: namesOf(left)}
		}
		return planner.Empty{}
	}
	if predIsTrue(pred) {
		if anti {
			return planner.Empty{Names: namesOf(left)}
		}
		pred = nil
	}
	// Do not turn LEFT/FULL/SEMI/ANTI into CROSS when ON folds to TRUE/nil.
	cross := !outer && !semi && !anti && pred == nil
	kind := n.Kind
	if cross && kind == ast.JoinInner {
		kind = ast.JoinCross
	}
	if leftOuter {
		cross = false
		kind = ast.JoinLeft
	}
	if full {
		cross = false
		kind = ast.JoinFull
	}
	if semi || anti {
		cross = false
	}
	return planner.Join{Left: left, Right: right, Pred: pred, Kind: kind, Cross: cross, Schema: n.Schema, Method: n.Method, LeftKeys: n.LeftKeys, RightKeys: n.RightKeys}
}

func rewriteRightToLeft(left, right planner.Logical, pred ast.Expr, n planner.Join) planner.Logical {
	ln := colCount(left)
	rn := colCount(right)
	swapped := swapJoinSchema(n.Schema, left, right)
	j := planner.Join{
		Left:      right,
		Right:     left,
		Pred:      pred,
		Kind:      ast.JoinLeft,
		Cross:     false,
		Schema:    swapped,
		Method:    n.Method,
		LeftKeys:  n.RightKeys,
		RightKeys: n.LeftKeys,
	}
	cols := make([]int, ln+rn)
	exprs := make([]ast.Expr, ln+rn)
	names := make([]string, ln+rn)
	orig := joinColNames(n.Schema, left, right)
	for i := 0; i < ln; i++ {
		cols[i] = rn + i
		names[i] = orig[i]
		exprs[i] = ast.Ident{Name: names[i]}
	}
	for i := 0; i < rn; i++ {
		cols[ln+i] = i
		names[ln+i] = orig[ln+i]
		exprs[ln+i] = ast.Ident{Name: names[ln+i]}
	}
	return planner.Project{Input: j, Cols: cols, Exprs: exprs, Names: names}
}

func colCount(p planner.Logical) int {
	if t := tableOf(p); t != nil {
		return len(t.Columns)
	}
	return len(namesOf(p))
}

func joinColNames(schema *catalog.Table, left, right planner.Logical) []string {
	ln, rn := colCount(left), colCount(right)
	if schema != nil && len(schema.Columns) == ln+rn && ln+rn > 0 {
		out := make([]string, len(schema.Columns))
		for i, c := range schema.Columns {
			out[i] = c.Name
		}
		return out
	}
	return append(namesOf(left), namesOf(right)...)
}

func swapJoinSchema(schema *catalog.Table, left, right planner.Logical) *catalog.Table {
	ln := colCount(left)
	rn := colCount(right)
	if schema != nil && len(schema.Columns) == ln+rn && ln+rn > 0 {
		out := schema.Clone()
		cols := make([]catalog.Column, ln+rn)
		copy(cols[:rn], schema.Columns[ln:])
		copy(cols[rn:], schema.Columns[:ln])
		out.Columns = cols
		return out
	}
	lt, rt := tableOf(left), tableOf(right)
	if lt == nil && rt == nil {
		return schema
	}
	out := &catalog.Table{}
	if rt != nil {
		out.Name = rt.Name
		out.Columns = append(out.Columns, rt.Columns...)
	}
	if lt != nil {
		if out.Name != "" {
			out.Name += "+" + lt.Name
		} else {
			out.Name = lt.Name
		}
		out.Columns = append(out.Columns, lt.Columns...)
	}
	return out
}

func namesOfJoin(n planner.Join, left, right planner.Logical) []string {
	if n.Schema != nil {
		out := make([]string, len(n.Schema.Columns))
		for i, c := range n.Schema.Columns {
			out[i] = c.Name
		}
		return out
	}
	return append(namesOf(left), namesOf(right)...)
}

func pushFilterJoin(pred ast.Expr, j planner.Join) planner.Logical {
	full := j.Kind == ast.JoinFull
	leftOuter := j.Kind == ast.JoinLeft
	outer := leftOuter || full
	var leftC, rightC, mid, above []ast.Expr
	for _, c := range conjuncts(pred) {
		names := identNames(c)
		lOnly, rOnly := true, true
		for _, name := range names {
			if !hasCol(j.Left, name) {
				lOnly = false
			}
			if !hasCol(j.Right, name) {
				rOnly = false
			}
		}
		switch {
		case lOnly && !rOnly:
			if full {
				// FULL: do not push either side's WHERE into the inputs.
				above = append(above, c)
			} else {
				leftC = append(leftC, c)
			}
		case rOnly && !lOnly:
			if outer {
				// WHERE on the null-extended side must stay above the join.
				above = append(above, c)
			} else {
				rightC = append(rightC, c)
			}
		default:
			if outer {
				above = append(above, c)
			} else {
				mid = append(mid, c)
			}
		}
	}
	left, right := j.Left, j.Right
	if len(leftC) > 0 {
		left = planner.Filter{Input: left, Pred: andAll(leftC)}
	}
	if len(rightC) > 0 {
		right = planner.Filter{Input: right, Pred: andAll(rightC)}
	}
	jp := andAll(append(conjuncts(j.Pred), mid...))
	cross := !outer && jp == nil
	kind := j.Kind
	if outer {
		cross = false
	}
	out := rewriteOnce(planner.Join{Left: left, Right: right, Pred: jp, Kind: kind, Cross: cross, Schema: j.Schema, Method: j.Method, LeftKeys: j.LeftKeys, RightKeys: j.RightKeys})
	if len(above) > 0 {
		return planner.Filter{Input: out, Pred: andAll(above)}
	}
	return out
}

func usedByProject(n planner.Project) []int {
	tab := tableOf(n.Input)
	var need []int
	for _, e := range n.Exprs {
		need = unionOrds(need, colOrds(e, tab))
	}
	if f, ok := n.Input.(planner.Filter); ok {
		need = unionOrds(need, colOrds(f.Pred, tab))
	}
	if s, ok := n.Input.(planner.Search); ok {
		need = unionOrds(need, s.Columns)
		need = unionOrds(need, colOrds(s.Residual, tab))
	}
	if f, ok := n.Input.(planner.Facet); ok {
		need = unionOrds(need, f.Columns)
	}
	if nr, ok := n.Input.(planner.Nearest); ok {
		need = unionOrds(need, []int{nr.Column})
		need = unionOrds(need, colOrds(nr.Residual, tab))
	}
	if c, ok := n.Input.(planner.Candidates); ok {
		need = unionOrds(need, []int{c.Column})
		need = unionOrds(need, colOrds(c.Residual, tab))
	}
	if r, ok := n.Input.(planner.Rerank); ok {
		need = unionOrds(need, r.SearchCols)
		need = unionOrds(need, []int{r.NearestCol, r.SparseCol})
	}
	return need
}

func pruneInput(p planner.Logical, need []int) planner.Logical {
	switch n := p.(type) {
	case planner.Scan:
		n.Needed = need
		return n
	case planner.Filter:
		n.Input = pruneInput(n.Input, unionOrds(need, colOrds(n.Pred, tableOf(n.Input))))
		return n
	case planner.Search:
		n.Needed = unionOrds(need, n.Columns)
		n.Input = pruneInput(n.Input, n.Needed)
		return n
	case planner.Facet:
		n.Input = pruneInput(n.Input, n.Columns)
		return n
	case planner.Nearest:
		n.Needed = unionOrds(need, []int{n.Column})
		n.Input = pruneInput(n.Input, n.Needed)
		return n
	case planner.Candidates:
		n.Needed = unionOrds(need, []int{n.Column})
		n.Input = pruneInput(n.Input, n.Needed)
		return n
	case planner.Rerank:
		need = unionOrds(need, n.SearchCols)
		need = unionOrds(need, []int{n.NearestCol, n.SparseCol})
		return mapRerankInputs(n, func(p planner.Logical) planner.Logical { return pruneInput(p, need) })
	case planner.Limit:
		n.Input = pruneInput(n.Input, need)
		return n
	case planner.Sort:
		extra := need
		for _, k := range n.Keys {
			extra = unionOrds(extra, []int{k.Col})
		}
		n.Input = pruneInput(n.Input, extra)
		return n
	default:
		return p
	}
}

func windowDetail(n planner.Window) string {
	s := ""
	for i, sp := range n.Specs {
		if i > 0 {
			s += ", "
		}
		s += sp.Fun
	}
	return s
}

func sortDetail(n planner.Sort) string {
	s := ""
	for i, k := range n.Keys {
		if i > 0 {
			s += ", "
		}
		s += itoa(k.Col)
		if k.Desc {
			s += " DESC"
		}
	}
	return s
}

func formatPlan(p planner.Logical) string {
	if p == nil {
		return "<nil>"
	}
	switch n := p.(type) {
	case planner.Scan:
		s := "Scan " + tableName(n.Table)
		if len(n.Needed) > 0 {
			s += " needed=" + ords(n.Needed)
		}
		return s
	case planner.With:
		s := "With"
		for _, cte := range n.CTEs {
			label := "Inline " + cte.Name
			if cte.RecursiveOn {
				label = "RecursiveCTE " + cte.Name
			} else if cte.Materialize {
				label = "Materialize " + cte.Name
			}
			body := cte.Input
			if cte.RecursiveOn {
				s += "\n" + indent(label+"\n"+indent(formatPlan(cte.Anchor))+"\n"+indent(formatPlan(cte.Recursive)))
				continue
			}
			s += "\n" + indent(label+"\n"+indent(formatPlan(body)))
		}
		return s + "\n" + indent(formatPlan(n.Query))
	case planner.CTEScan:
		return "CTEScan " + n.Name
	case planner.SetOperation:
		kind := "UnionAll"
		if n.Op == "intersect" {
			kind = "Intersect"
		} else if n.Op == "except" {
			kind = "Except"
		} else if !n.All {
			kind = "Union"
		}
		return kind + "\n" + indent(formatPlan(n.Left)) + "\n" + indent(formatPlan(n.Right))
	case planner.SeqScan:
		s := "SeqScan " + tableName(n.Table)
		if len(n.Needed) > 0 {
			s += " needed=" + ords(n.Needed)
		}
		if len(n.Segments) > 0 {
			s += " segs="
			for i, g := range n.Segments {
				if i > 0 {
					s += ","
				}
				s += itoa(g.ID)
			}
		}
		return s
	case planner.Search:
		s := "Search " + tableName(n.Table)
		if n.IndexName != "" {
			s += " " + n.IndexName + " fulltext"
		} else {
			s += " seq"
		}
		s += formatSearchWeights(n.Weights)
		if n.Residual != nil {
			s += " residual=" + formatExpr(n.Residual)
		}
		if n.Input != nil {
			return s + "\n" + indent(formatPlan(n.Input))
		}
		return s
	case planner.Facet:
		s := "Facet"
		for i, name := range n.Names {
			if i == 0 {
				s += " "
			} else {
				s += ","
			}
			s += name
		}
		if n.Limit >= 0 {
			s += " limit=" + itoa64(n.Limit)
		}
		return s + "\n" + indent(formatPlan(n.Input))
	case planner.Nearest:
		s := "Nearest " + tableName(n.Table)
		if n.IndexName != "" {
			method := "hnsw"
			if idx := indexByName(n.Table, n.IndexName); idx != nil {
				switch idx.VecMethod {
				case catalog.VecMethodIVF:
					method = "ivf"
				case catalog.VecMethodIVFPQ:
					method = "ivfpq"
				case catalog.VecMethodSPARSE:
					method = "sparse"
				}
			}
			s += " " + n.IndexName + " " + method
		} else {
			s += " flat"
		}
		if n.Metric != "" {
			s += " " + n.Metric
		}
		if n.Residual != nil {
			s += " residual=" + formatExpr(n.Residual)
		}
		if n.Input != nil {
			return s + "\n" + indent(formatPlan(n.Input))
		}
		return s
	case planner.Candidates:
		s := "Candidates " + candDetail(n)
		if n.Input != nil {
			return s + "\n" + indent(formatPlan(n.Input))
		}
		return s
	case planner.Rerank:
		s := "Rerank " + rerankDetail(n) + "\n" + indent(formatPlan(n.Input))
		for _, e := range n.Extra {
			s += "\n" + indent(formatPlan(e))
		}
		return s
	case planner.IndexScan:
		s := "IndexScan " + tableName(n.Table)
		if n.PK {
			s += " pk"
		} else {
			s += " " + n.IndexName
		}
		if n.Spatial {
			s += " spatial"
		}
		if idx := indexByName(n.Table, n.IndexName); idx != nil && len(idx.Path) > 0 {
			s += " json"
		}
		if n.IndexOnly {
			s += " covering"
		}
		if n.Residual != nil {
			s += " residual=" + formatExpr(n.Residual)
		}
		return s
	case planner.Filter:
		return "Filter " + formatExpr(n.Pred) + "\n" + indent(formatPlan(n.Input))
	case planner.Project:
		s := "Project"
		if n.DistinctIndex != "" {
			s = "IndexDistinct " + n.DistinctIndex + "\n" + s
		}
		if n.Distinct {
			s = "HashDistinct\n" + s
		}
		for i, name := range n.Names {
			if i == 0 {
				s += " "
			} else {
				s += ", "
			}
			s += name
		}
		return s + "\n" + indent(formatPlan(n.Input))
	case planner.Limit:
		s := "Limit " + itoa64(n.N)
		if n.N < 0 {
			s = "Limit ALL"
		}
		if n.Offset > 0 {
			s += " OFFSET " + itoa64(n.Offset)
		}
		return s + "\n" + indent(formatPlan(n.Input))
	case planner.Sort:
		s := "Sort " + sortDetail(n) + "\n" + indent(formatPlan(n.Input))
		if n.TopN > 0 {
			s = "TopNSort " + itoa64(n.TopN) + " " + sortDetail(n) + "\n" + indent(formatPlan(n.Input))
		}
		if n.OrderedDistinct {
			return "OrderedDistinct\n" + s
		}
		return s
	case planner.DropTable:
		return "DropTable"
	case planner.DropIndex:
		return "DropIndex"
	case planner.RebuildIndex:
		return "RebuildIndex"
	case planner.AlterTable:
		return "AlterTable"
	case planner.CreateDatabase:
		return "CreateDatabase"
	case planner.Join:
		kind := "Join"
		if n.Kind == ast.JoinSemi {
			kind = "HashSemiJoin"
		} else if n.Kind == ast.JoinAnti {
			kind = "HashAntiJoin"
		} else if n.Kind == ast.JoinLeft {
			kind = "LeftJoin"
		} else if n.Kind == ast.JoinFull {
			kind = "FullJoin"
		} else if n.Cross || n.Kind == ast.JoinCross {
			kind = "CrossJoin"
		}
		if n.Kind != ast.JoinLeft && n.Kind != ast.JoinFull && n.Kind != ast.JoinSemi && n.Kind != ast.JoinAnti {
			if n.Method == "hash" {
				kind = "HashJoin"
			}
			if n.Method == "merge" {
				kind = "MergeJoin"
			}
		}
		s := kind
		if n.Pred != nil {
			s += " " + formatExpr(n.Pred)
		}
		return s + "\n" + indent(formatPlan(n.Left)) + "\n" + indent(formatPlan(n.Right))
	case planner.Window:
		s := "Window"
		for i, sp := range n.Specs {
			if i > 0 {
				s += ", "
			} else {
				s += " "
			}
			s += sp.Fun
		}
		return s + "\n" + indent(formatPlan(n.Input))
	case planner.Aggregate:
		s := "Aggregate"
		if n.Distinct {
			s = "HashDistinct\n" + s
		}
		for i, name := range n.Names {
			if i == 0 {
				s += " "
			} else {
				s += ", "
			}
			s += name
		}
		s = s + "\n" + indent(formatPlan(n.Input))
		if n.Having != nil {
			return "Having " + formatExpr(n.Having) + "\n" + indent(s)
		}
		return s
	case planner.Empty:
		return "Empty"
	case planner.Update:
		return "Update " + tableName(n.Table) + "\n" + indent(formatPlan(n.Input))
	case planner.Delete:
		return "Delete " + tableName(n.Table) + "\n" + indent(formatPlan(n.Input))
	case planner.Explain:
		s := "Explain"
		if n.Analyze {
			s += " Analyze"
		}
		return s + "\n" + indent(formatPlan(n.Input))
	case planner.Analyze:
		return "Analyze"
	case planner.CreateTable:
		return "CreateTable"
	case planner.CreateIndex:
		return "CreateIndex"
	case planner.Insert:
		return "Insert"
	case planner.Upsert:
		return "Upsert"
	default:
		return "Plan"
	}
}

func tableName(t *catalog.Table) string {
	if t == nil {
		return "?"
	}
	return t.Name
}

func indexByName(t *catalog.Table, name string) *catalog.Index {
	if t == nil || name == "" {
		return nil
	}
	for i := range t.Indexes {
		if t.Indexes[i].Name == name {
			return &t.Indexes[i]
		}
	}
	return nil
}

func indent(s string) string {
	if s == "" {
		return "  "
	}
	out := "  "
	for i := 0; i < len(s); i++ {
		out += s[i : i+1]
		if s[i] == '\n' {
			out += "  "
		}
	}
	return out
}

func ords(xs []int) string {
	s := ""
	for i, n := range xs {
		if i > 0 {
			s += ","
		}
		s += itoa(n)
	}
	return s
}

func itoa(n int) string     { return itoa64(int64(n)) }
func itoa64(n int64) string { return formatInt(n) }

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
