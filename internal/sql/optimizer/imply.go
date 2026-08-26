package optimizer

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func impliesPredicate(query, partial ast.Expr, tab *catalog.Table) bool {
	if partial == nil {
		return true
	}
	partial = foldExpr(partial)
	if predIsTrue(partial) {
		return true
	}
	if query == nil {
		return false
	}
	query = foldExpr(query)
	if predIsFalse(query) {
		return true
	}
	if catalog.ExprEqual(query, partial) {
		return true
	}
	return impliesExpr(conjuncts(query), partial, tab)
}

func impliesExpr(query []ast.Expr, need ast.Expr, tab *catalog.Table) bool {
	if need == nil {
		return true
	}
	if bin, ok := need.(ast.Binary); ok && (bin.Op == "AND" || bin.Op == "and") {
		return impliesExpr(query, bin.Left, tab) && impliesExpr(query, bin.Right, tab)
	}
	if bin, ok := need.(ast.Binary); ok && (bin.Op == "OR" || bin.Op == "or") {
		return impliesExpr(query, bin.Left, tab) || impliesExpr(query, bin.Right, tab)
	}
	for _, q := range query {
		if conjunctImplies(q, need, tab) {
			return true
		}
	}
	qr := extractRanges(andAll(query), tab)
	nr := extractRanges(need, tab)
	if len(nr) > 0 && rangeMapImplies(qr, nr) {
		return true
	}
	return false
}

func conjunctImplies(query, need ast.Expr, tab *catalog.Table) bool {
	if catalog.ExprEqual(query, need) {
		return true
	}
	if bin, ok := query.(ast.Binary); ok && (bin.Op == "AND" || bin.Op == "and") {
		return conjunctImplies(bin.Left, need, tab) || conjunctImplies(bin.Right, need, tab)
	}
	if isn, ok := need.(ast.IsNull); ok && isn.Not {
		if _, _, _, ok := sargableIdentCmp(query, tab); ok {
			return catalog.ExprEqual(cmpSide(query), isn.Expr)
		}
	}
	qr := extractRanges(query, tab)
	nr := extractRanges(need, tab)
	if len(nr) > 0 && rangeMapImplies(qr, nr) {
		return true
	}
	return exprRangeImplies(query, need)
}

func sargableIdentCmp(e ast.Expr, tab *catalog.Table) (int, types.Value, string, bool) {
	b, ok := e.(ast.Binary)
	if !ok {
		return 0, types.Value{}, "", false
	}
	return sargable(b, tab)
}

func cmpSide(e ast.Expr) ast.Expr {
	b, ok := e.(ast.Binary)
	if !ok {
		return nil
	}
	if _, ok := b.Right.(ast.Literal); ok {
		return b.Left
	}
	if _, ok := b.Left.(ast.Literal); ok {
		return b.Right
	}
	return nil
}

func rangeMapImplies(query, need map[int]colRange) bool {
	if len(need) == 0 {
		return false
	}
	for ord, nr := range need {
		qr, ok := query[ord]
		if !ok || !rangeImplies(qr, nr) {
			return false
		}
	}
	return true
}

func rangeImplies(query, need colRange) bool {
	if need.eq && need.low != nil {
		if query.eq && query.low != nil {
			c, err := query.low.Cmp(*need.low)
			return err == nil && c == 0
		}
		return false
	}
	if need.isNull {
		return query.isNull
	}
	if query.isNull {
		return false
	}
	if !need.unboundedLow {
		if query.unboundedLow || query.low == nil {
			return false
		}
		c, err := query.low.Cmp(*need.low)
		if err != nil {
			return false
		}
		if c < 0 {
			return false
		}
		if c == 0 && !query.lowIncl && need.lowIncl {
			return false
		}
	}
	if !need.unboundedHigh {
		if query.unboundedHigh || query.high == nil {
			return false
		}
		c, err := query.high.Cmp(*need.high)
		if err != nil {
			return false
		}
		if c > 0 {
			return false
		}
		if c == 0 && !query.highIncl && need.highIncl {
			return false
		}
	}
	return true
}

func exprRangeImplies(query, need ast.Expr) bool {
	qop, qe, qv, qok := exprCmp(query)
	nop, ne, nv, nok := exprCmp(need)
	if !qok || !nok || !catalog.ExprEqual(qe, ne) {
		return false
	}
	if nop == "=" {
		return qop == "=" && valuesEqual(qv, nv)
	}
	if qop == "=" {
		return valueSatisfies(qv, nop, nv)
	}
	switch nop {
	case ">":
		return boundGE(qop, qv, nv, false)
	case ">=":
		return boundGE(qop, qv, nv, true)
	case "<":
		return boundLE(qop, qv, nv, false)
	case "<=":
		return boundLE(qop, qv, nv, true)
	}
	return false
}

func exprCmp(e ast.Expr) (op string, expr ast.Expr, val types.Value, ok bool) {
	b, ok := e.(ast.Binary)
	if !ok {
		return "", nil, types.Value{}, false
	}
	switch b.Op {
	case "=", "<", "<=", ">", ">=":
	default:
		return "", nil, types.Value{}, false
	}
	if lit, isLit := b.Right.(ast.Literal); isLit {
		return b.Op, b.Left, lit.Value, true
	}
	if lit, isLit := b.Left.(ast.Literal); isLit {
		op = flipOp(b.Op)
		if op == "" && b.Op == "=" {
			op = "="
		}
		return op, b.Right, lit.Value, op != ""
	}
	return "", nil, types.Value{}, false
}

func valuesEqual(a, b types.Value) bool {
	c, err := a.Cmp(b)
	return err == nil && c == 0
}

func valueSatisfies(v types.Value, op string, bound types.Value) bool {
	c, err := v.Cmp(bound)
	if err != nil {
		return false
	}
	switch op {
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case "=":
		return c == 0
	}
	return false
}

func boundGE(qop string, qv, nv types.Value, incl bool) bool {
	c, err := qv.Cmp(nv)
	if err != nil {
		return false
	}
	switch qop {
	case ">":
		return c > 0 || (c == 0 && !incl)
	case ">=":
		return c > 0 || (c == 0 && incl)
	case "=":
		return c > 0 || (c == 0 && incl)
	}
	return false
}

func boundLE(qop string, qv, nv types.Value, incl bool) bool {
	c, err := qv.Cmp(nv)
	if err != nil {
		return false
	}
	switch qop {
	case "<":
		return c < 0 || (c == 0 && !incl)
	case "<=":
		return c < 0 || (c == 0 && incl)
	case "=":
		return c < 0 || (c == 0 && incl)
	}
	return false
}

func predColumns(e ast.Expr, tab *catalog.Table) []int {
	if e == nil || tab == nil {
		return nil
	}
	seen := map[int]struct{}{}
	var walk func(ast.Expr)
	walk = func(ex ast.Expr) {
		if ex == nil {
			return
		}
		switch x := ex.(type) {
		case ast.Ident:
			if i, ok := tab.ColIndex(x.Name); ok {
				seen[i] = struct{}{}
			}
		case ast.Path:
			if len(x.Parts) > 0 {
				if i, ok := tab.ColIndex(x.Parts[0]); ok {
					seen[i] = struct{}{}
				}
			}
		case ast.Unary:
			walk(x.Right)
		case ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case ast.Between:
			walk(x.Expr)
			walk(x.Low)
			walk(x.High)
		case ast.IsNull:
			walk(x.Expr)
		case ast.Call:
			for _, a := range x.Args {
				walk(a)
			}
		case ast.Case:
			walk(x.Operand)
			walk(x.Else)
			for _, arm := range x.Whens {
				walk(arm.When)
				walk(arm.Then)
			}
		}
	}
	walk(e)
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	return out
}

func unionNeeded(a, b []int) []int {
	if len(b) == 0 {
		return a
	}
	seen := map[int]struct{}{}
	var out []int
	for _, v := range a {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range b {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
