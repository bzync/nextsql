package optimizer

import (
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
)

func decideCTEs(p planner.Logical, stats StatsFunc) planner.Logical {
	if p == nil {
		return nil
	}
	switch n := p.(type) {
	case planner.With:
		for i := range n.CTEs {
			if n.CTEs[i].RecursiveOn {
				n.CTEs[i].Anchor = decideCTEs(n.CTEs[i].Anchor, stats)
				n.CTEs[i].Recursive = decideCTEs(n.CTEs[i].Recursive, stats)
			} else {
				n.CTEs[i].Input = decideCTEs(n.CTEs[i].Input, stats)
			}
		}
		n.Query = decideCTEs(n.Query, stats)
		kept := make([]planner.CTE, 0, len(n.CTEs))
		query := n.Query
		for i := range n.CTEs {
			cte := n.CTEs[i]
			later := planner.With{CTEs: n.CTEs[i+1:], Query: query}
			refs := countCTEScans(later, cte.ID)
			materialize := shouldMaterialize(cte, refs, stats)
			cte.Materialize = materialize
			if materialize || cte.RecursiveOn {
				query = stampCTEScanRows(query, cte.ID, estimateCTERows(cte, stats))
				for j := i + 1; j < len(n.CTEs); j++ {
					if n.CTEs[j].RecursiveOn {
						n.CTEs[j].Anchor = stampCTEScanRows(n.CTEs[j].Anchor, cte.ID, estimateCTERows(cte, stats))
						n.CTEs[j].Recursive = stampCTEScanRows(n.CTEs[j].Recursive, cte.ID, estimateCTERows(cte, stats))
					} else {
						n.CTEs[j].Input = stampCTEScanRows(n.CTEs[j].Input, cte.ID, estimateCTERows(cte, stats))
					}
				}
				kept = append(kept, cte)
				continue
			}
			body := clonePlan(cte.Input)
			query = replaceCTEScan(query, cte.ID, body)
			for j := i + 1; j < len(n.CTEs); j++ {
				if n.CTEs[j].RecursiveOn {
					n.CTEs[j].Anchor = replaceCTEScan(n.CTEs[j].Anchor, cte.ID, clonePlan(body))
					n.CTEs[j].Recursive = replaceCTEScan(n.CTEs[j].Recursive, cte.ID, clonePlan(body))
				} else {
					n.CTEs[j].Input = replaceCTEScan(n.CTEs[j].Input, cte.ID, clonePlan(body))
				}
			}
		}
		if len(kept) == 0 {
			return query
		}
		n.CTEs = kept
		n.Query = query
		return n
	case planner.SetOperation:
		n.Left = decideCTEs(n.Left, stats)
		n.Right = decideCTEs(n.Right, stats)
		return n
	case planner.Filter:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Project:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Limit:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Sort:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Window:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Join:
		n.Left = decideCTEs(n.Left, stats)
		n.Right = decideCTEs(n.Right, stats)
		return n
	case planner.Aggregate:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Search:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Nearest:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Candidates:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Rerank:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Update:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Delete:
		n.Input = decideCTEs(n.Input, stats)
		return n
	case planner.Explain:
		n.Input = decideCTEs(n.Input, stats)
		return n
	default:
		return p
	}
}

func shouldMaterialize(cte planner.CTE, refs int, stats StatsFunc) bool {
	if cte.RecursiveOn {
		return true
	}
	if cte.Materialize {
		return true
	}
	body := cte.Input
	if volatilePlan(body) {
		return true
	}
	if cte.ForceInline {
		return false
	}
	if refs <= 1 {
		return false
	}
	if cheapCTE(body) {
		return false
	}
	_, node := choose(body, stats)
	if node != nil && node.EstCost > 2_000 && refs > 1 {
		return true
	}
	return refs > 1
}

func cheapCTE(p planner.Logical) bool {
	switch n := p.(type) {
	case planner.Scan, planner.SeqScan, planner.IndexScan, planner.CTEScan, planner.Empty:
		return true
	case planner.Filter:
		return cheapCTE(n.Input)
	case planner.Project:
		return !n.Distinct && n.DistinctIndex == "" && cheapCTE(n.Input)
	case planner.Limit:
		return cheapCTE(n.Input)
	default:
		return false
	}
}

func estimateCTERows(cte planner.CTE, stats StatsFunc) int64 {
	body := cte.Input
	if cte.RecursiveOn {
		body = cte.Anchor
	}
	_, node := choose(body, stats)
	if node == nil || node.EstRows <= 0 {
		return defaultRows
	}
	return node.EstRows
}

func countCTEScans(p planner.Logical, id uint64) int {
	if p == nil {
		return 0
	}
	switch n := p.(type) {
	case planner.CTEScan:
		if n.ID == id {
			return 1
		}
		return 0
	case planner.With:
		nrefs := countCTEScans(n.Query, id)
		for _, cte := range n.CTEs {
			if cte.RecursiveOn {
				nrefs += countCTEScans(cte.Anchor, id) + countCTEScans(cte.Recursive, id)
			} else {
				nrefs += countCTEScans(cte.Input, id)
			}
		}
		return nrefs
	case planner.SetOperation:
		return countCTEScans(n.Left, id) + countCTEScans(n.Right, id)
	case planner.Join:
		return countCTEScans(n.Left, id) + countCTEScans(n.Right, id)
	case planner.Filter:
		return countCTEScans(n.Input, id)
	case planner.Project:
		return countCTEScans(n.Input, id)
	case planner.Limit:
		return countCTEScans(n.Input, id)
	case planner.Sort:
		return countCTEScans(n.Input, id)
	case planner.Window:
		return countCTEScans(n.Input, id)
	case planner.Aggregate:
		return countCTEScans(n.Input, id)
	case planner.Search:
		return countCTEScans(n.Input, id)
	case planner.Nearest:
		return countCTEScans(n.Input, id)
	case planner.Candidates:
		return countCTEScans(n.Input, id)
	case planner.Rerank:
		return countCTEScans(n.Input, id)
	case planner.Update:
		return countCTEScans(n.Input, id)
	case planner.Delete:
		return countCTEScans(n.Input, id)
	default:
		return 0
	}
}

func replaceCTEScan(p planner.Logical, id uint64, repl planner.Logical) planner.Logical {
	if p == nil {
		return nil
	}
	switch n := p.(type) {
	case planner.CTEScan:
		if n.ID == id {
			return clonePlan(repl)
		}
		return n
	case planner.With:
		n.Query = replaceCTEScan(n.Query, id, repl)
		for i := range n.CTEs {
			if n.CTEs[i].RecursiveOn {
				n.CTEs[i].Anchor = replaceCTEScan(n.CTEs[i].Anchor, id, repl)
				n.CTEs[i].Recursive = replaceCTEScan(n.CTEs[i].Recursive, id, repl)
			} else {
				n.CTEs[i].Input = replaceCTEScan(n.CTEs[i].Input, id, repl)
			}
		}
		return n
	case planner.SetOperation:
		n.Left = replaceCTEScan(n.Left, id, repl)
		n.Right = replaceCTEScan(n.Right, id, repl)
		return n
	case planner.Join:
		n.Left = replaceCTEScan(n.Left, id, repl)
		n.Right = replaceCTEScan(n.Right, id, repl)
		return n
	case planner.Filter:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Project:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Limit:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Sort:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Window:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Aggregate:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Search:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Nearest:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Candidates:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Rerank:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Update:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	case planner.Delete:
		n.Input = replaceCTEScan(n.Input, id, repl)
		return n
	default:
		return p
	}
}

func stampCTEScanRows(p planner.Logical, id uint64, rows int64) planner.Logical {
	if p == nil {
		return nil
	}
	switch n := p.(type) {
	case planner.CTEScan:
		if n.ID == id {
			n.EstRows = rows
		}
		return n
	case planner.With:
		n.Query = stampCTEScanRows(n.Query, id, rows)
		return n
	case planner.SetOperation:
		n.Left = stampCTEScanRows(n.Left, id, rows)
		n.Right = stampCTEScanRows(n.Right, id, rows)
		return n
	case planner.Join:
		n.Left = stampCTEScanRows(n.Left, id, rows)
		n.Right = stampCTEScanRows(n.Right, id, rows)
		return n
	case planner.Filter:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Project:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Limit:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Sort:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Window:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Aggregate:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Search:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Nearest:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Candidates:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	case planner.Rerank:
		n.Input = stampCTEScanRows(n.Input, id, rows)
		return n
	default:
		return p
	}
}

func clonePlan(p planner.Logical) planner.Logical {
	if p == nil {
		return nil
	}
	switch n := p.(type) {
	case planner.SetOperation:
		n.Left = clonePlan(n.Left)
		n.Right = clonePlan(n.Right)
		return n
	case planner.Join:
		n.Left = clonePlan(n.Left)
		n.Right = clonePlan(n.Right)
		return n
	case planner.Filter:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Project:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Limit:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Sort:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Window:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Aggregate:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Search:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Nearest:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Candidates:
		n.Input = clonePlan(n.Input)
		return n
	case planner.Rerank:
		n.Input = clonePlan(n.Input)
		return n
	case planner.With:
		n.Query = clonePlan(n.Query)
		ctes := make([]planner.CTE, len(n.CTEs))
		copy(ctes, n.CTEs)
		for i := range ctes {
			if ctes[i].RecursiveOn {
				ctes[i].Anchor = clonePlan(ctes[i].Anchor)
				ctes[i].Recursive = clonePlan(ctes[i].Recursive)
			} else {
				ctes[i].Input = clonePlan(ctes[i].Input)
			}
		}
		n.CTEs = ctes
		return n
	default:
		return p
	}
}

func volatilePlan(p planner.Logical) bool {
	if p == nil {
		return false
	}
	switch n := p.(type) {
	case planner.Project:
		for _, e := range n.Exprs {
			if volatileExpr(e) {
				return true
			}
		}
		return volatilePlan(n.Input)
	case planner.Filter:
		return volatileExpr(n.Pred) || volatilePlan(n.Input)
	case planner.Aggregate:
		for _, e := range n.Exprs {
			if volatileExpr(e) {
				return true
			}
		}
		return volatilePlan(n.Input)
	case planner.Join:
		return volatileExpr(n.Pred) || volatilePlan(n.Left) || volatilePlan(n.Right)
	case planner.SetOperation:
		return volatilePlan(n.Left) || volatilePlan(n.Right)
	case planner.Limit:
		return volatilePlan(n.Input)
	case planner.Sort:
		return volatilePlan(n.Input)
	case planner.Window:
		for _, sp := range n.Specs {
			for _, e := range sp.Args {
				if volatileExpr(e) {
					return true
				}
			}
			for _, e := range sp.Partition {
				if volatileExpr(e) {
					return true
				}
			}
			for _, o := range sp.Order {
				if volatileExpr(o.Expr) {
					return true
				}
			}
		}
		return volatilePlan(n.Input)
	case planner.With:
		if volatilePlan(n.Query) {
			return true
		}
		for _, cte := range n.CTEs {
			if cte.RecursiveOn {
				if volatilePlan(cte.Anchor) || volatilePlan(cte.Recursive) {
					return true
				}
			} else if volatilePlan(cte.Input) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func volatileExpr(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.Call:
		switch x.Name {
		case "uuid", "now", "ai":
			return true
		}
		for _, a := range x.Args {
			if volatileExpr(a) {
				return true
			}
		}
		return false
	case ast.Window:
		if volatileExpr(x.Fn) {
			return true
		}
		for _, p := range x.Partition {
			if volatileExpr(p) {
				return true
			}
		}
		for _, o := range x.Order {
			if volatileExpr(o.Expr) {
				return true
			}
		}
		return false
	case ast.Unary:
		return volatileExpr(x.Right)
	case ast.Binary:
		return volatileExpr(x.Left) || volatileExpr(x.Right)
	case ast.Between:
		return volatileExpr(x.Expr) || volatileExpr(x.Low) || volatileExpr(x.High)
	case ast.Case:
		if volatileExpr(x.Operand) || volatileExpr(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if volatileExpr(arm.When) || volatileExpr(arm.Then) {
				return true
			}
		}
		return false
	case ast.ScalarSubquery:
		return true
	default:
		return false
	}
}
