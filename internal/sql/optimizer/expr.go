package optimizer

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

func foldExpr(e ast.Expr) ast.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case ast.Case:
		whens := make([]ast.CaseWhen, len(x.Whens))
		for i, arm := range x.Whens {
			whens[i] = ast.CaseWhen{When: foldExpr(arm.When), Then: foldExpr(arm.Then)}
		}
		return ast.Case{Operand: foldExpr(x.Operand), Whens: whens, Else: foldExpr(x.Else)}
	case ast.Unary:
		r := foldExpr(x.Right)
		if lit, ok := r.(ast.Literal); ok {
			if x.Op == "NOT" {
				if lit.Value.Null {
					return ast.Literal{Value: types.Null(types.Bool())}
				}
				if lit.Value.Typ.Kind == types.KindBool {
					return ast.Literal{Value: types.BoolValue(!lit.Value.Bool)}
				}
			}
			if x.Op == "-" && !lit.Value.Null && lit.Value.Typ.Kind == types.KindDecimal {
				v := lit.Value.Clone()
				v.Dec = v.Dec.Negate()
				return ast.Literal{Value: v}
			}
		}
		return ast.Unary{Op: x.Op, Right: r}
	case ast.Binary:
		l, r := foldExpr(x.Left), foldExpr(x.Right)
		if x.Op == "AND" || x.Op == "OR" {
			return foldLogic(x.Op, l, r)
		}
		ll, lok := l.(ast.Literal)
		rl, rok := r.(ast.Literal)
		if !lok || !rok {
			return ast.Binary{Op: x.Op, Left: l, Right: r}
		}
		if x.Op == "+" || x.Op == "-" || x.Op == "*" || x.Op == "/" {
			if v, ok := foldArith(x.Op, ll.Value, rl.Value); ok {
				return ast.Literal{Value: v}
			}
			return ast.Binary{Op: x.Op, Left: l, Right: r}
		}
		if ll.Value.Null || rl.Value.Null {
			return ast.Literal{Value: types.Null(types.Bool())}
		}
		lv, rv := ll.Value, rl.Value
		if lv.Typ.Kind != rv.Typ.Kind {
			if c, err := types.Coerce(rv, lv.Typ); err == nil {
				rv = c
			} else if c, err := types.Coerce(lv, rv.Typ); err == nil {
				lv = c
			} else {
				return ast.Binary{Op: x.Op, Left: l, Right: r}
			}
		}
		cmp, err := lv.Cmp(rv)
		if err != nil {
			return ast.Binary{Op: x.Op, Left: l, Right: r}
		}
		var b bool
		switch x.Op {
		case "=":
			b = cmp == 0
		case "<>":
			b = cmp != 0
		case "<":
			b = cmp < 0
		case ">":
			b = cmp > 0
		case "<=":
			b = cmp <= 0
		case ">=":
			b = cmp >= 0
		default:
			return ast.Binary{Op: x.Op, Left: l, Right: r}
		}
		return ast.Literal{Value: types.BoolValue(b)}
	case ast.Between:
		v, lo, hi := foldExpr(x.Expr), foldExpr(x.Low), foldExpr(x.High)
		vl, ok1 := v.(ast.Literal)
		ll, ok2 := lo.(ast.Literal)
		hl, ok3 := hi.(ast.Literal)
		if ok1 && ok2 && ok3 && !vl.Value.Null && !ll.Value.Null && !hl.Value.Null {
			c1, err1 := vl.Value.Cmp(ll.Value)
			c2, err2 := vl.Value.Cmp(hl.Value)
			if err1 == nil && err2 == nil {
				in := c1 >= 0 && c2 <= 0
				if x.Not {
					in = !in
				}
				return ast.Literal{Value: types.BoolValue(in)}
			}
		}
		return ast.Between{Expr: v, Low: lo, High: hi, Not: x.Not}
	case ast.IsNull:
		inner := foldExpr(x.Expr)
		if lit, ok := inner.(ast.Literal); ok {
			is := lit.Value.Null
			if x.Not {
				is = !is
			}
			return ast.Literal{Value: types.BoolValue(is)}
		}
		return ast.IsNull{Expr: inner, Not: x.Not}
	case ast.Call:
		args := make([]ast.Expr, len(x.Args))
		lits := make([]types.Value, len(x.Args))
		all := types.IsGeoFunc(x.Name) && len(x.Args) > 0
		for i := range x.Args {
			args[i] = foldExpr(x.Args[i])
			lit, ok := args[i].(ast.Literal)
			if !ok {
				all = false
				continue
			}
			lits[i] = lit.Value
		}
		if all {
			if v, ok, err := types.EvalGeo(x.Name, lits); err == nil && ok {
				return ast.Literal{Value: v}
			}
		}
		return ast.Call{Name: x.Name, Args: args}
	case ast.Window:
		fn := foldExpr(x.Fn)
		call, _ := fn.(ast.Call)
		if call.Name == "" {
			call = x.Fn
		}
		parts := make([]ast.Expr, len(x.Partition))
		for i := range x.Partition {
			parts[i] = foldExpr(x.Partition[i])
		}
		order := make([]ast.OrderItem, len(x.Order))
		for i := range x.Order {
			order[i] = ast.OrderItem{Expr: foldExpr(x.Order[i].Expr), Desc: x.Order[i].Desc}
		}
		var frame *ast.Frame
		if x.Frame != nil {
			f := *x.Frame
			f.Start.Offset = foldExpr(x.Frame.Start.Offset)
			f.End.Offset = foldExpr(x.Frame.End.Offset)
			frame = &f
		}
		return ast.Window{Fn: call, Partition: parts, Order: order, Frame: frame}
	default:
		return e
	}
}

func foldLogic(op string, l, r ast.Expr) ast.Expr {
	lb, lok := boolLit(l)
	rb, rok := boolLit(r)
	if op == "AND" {
		if lok && !lb.Null && !lb.Bool {
			return ast.Literal{Value: types.BoolValue(false)}
		}
		if rok && !rb.Null && !rb.Bool {
			return ast.Literal{Value: types.BoolValue(false)}
		}
		if lok && !lb.Null && lb.Bool {
			return r
		}
		if rok && !rb.Null && rb.Bool {
			return l
		}
		if lok && lb.Null && rok && rb.Null {
			return ast.Literal{Value: types.Null(types.Bool())}
		}
	}
	if op == "OR" {
		if lok && !lb.Null && lb.Bool {
			return ast.Literal{Value: types.BoolValue(true)}
		}
		if rok && !rb.Null && rb.Bool {
			return ast.Literal{Value: types.BoolValue(true)}
		}
		if lok && !lb.Null && !lb.Bool {
			return r
		}
		if rok && !rb.Null && !rb.Bool {
			return l
		}
		if lok && lb.Null && rok && rb.Null {
			return ast.Literal{Value: types.Null(types.Bool())}
		}
	}
	return ast.Binary{Op: op, Left: l, Right: r}
}

func boolLit(e ast.Expr) (types.Value, bool) {
	lit, ok := e.(ast.Literal)
	if !ok {
		return types.Value{}, false
	}
	if lit.Value.Null {
		return lit.Value, true
	}
	if lit.Value.Typ.Kind != types.KindBool {
		return types.Value{}, false
	}
	return lit.Value, true
}

func foldArith(op string, l, r types.Value) (types.Value, bool) {
	if l.Null || r.Null {
		return types.Null(types.Type{Kind: types.KindDecimal}), true
	}
	if l.Typ.Kind != types.KindDecimal || r.Typ.Kind != types.KindDecimal {
		return types.Value{}, false
	}
	var d types.Decimal
	var err error
	switch op {
	case "+":
		d = types.AddDec(l.Dec, r.Dec)
	case "-":
		d = types.SubDec(l.Dec, r.Dec)
	case "*":
		d = types.MulDec(l.Dec, r.Dec)
	case "/":
		d, err = types.QuoDec(l.Dec, r.Dec)
		if err != nil {
			return types.Value{}, false
		}
	default:
		return types.Value{}, false
	}
	return types.DecimalValue(d, types.Type{Kind: types.KindDecimal, Scale: uint16(d.Scale)}), true
}

func conjuncts(e ast.Expr) []ast.Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(ast.Binary); ok && b.Op == "AND" {
		return append(conjuncts(b.Left), conjuncts(b.Right)...)
	}
	return []ast.Expr{e}
}

func andAll(cs []ast.Expr) ast.Expr {
	var out ast.Expr
	for _, c := range cs {
		if c == nil {
			continue
		}
		if out == nil {
			out = c
			continue
		}
		out = ast.Binary{Op: "AND", Left: out, Right: c}
	}
	return out
}

func identNames(e ast.Expr) []string {
	var out []string
	walkIdents(e, func(n string) { out = append(out, n) })
	return out
}

func mapIdents(e ast.Expr, fn func(string) string) ast.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case ast.Ident:
		return ast.Ident{Name: fn(x.Name)}
	case ast.Unary:
		return ast.Unary{Op: x.Op, Right: mapIdents(x.Right, fn)}
	case ast.Binary:
		return ast.Binary{Op: x.Op, Left: mapIdents(x.Left, fn), Right: mapIdents(x.Right, fn)}
	case ast.Between:
		return ast.Between{Expr: mapIdents(x.Expr, fn), Low: mapIdents(x.Low, fn), High: mapIdents(x.High, fn), Not: x.Not}
	case ast.IsNull:
		return ast.IsNull{Expr: mapIdents(x.Expr, fn), Not: x.Not}
	case ast.Call:
		args := make([]ast.Expr, len(x.Args))
		for i, a := range x.Args {
			args[i] = mapIdents(a, fn)
		}
		return ast.Call{Name: x.Name, Args: args}
	case ast.Case:
		whens := make([]ast.CaseWhen, len(x.Whens))
		for i, arm := range x.Whens {
			whens[i] = ast.CaseWhen{When: mapIdents(arm.When, fn), Then: mapIdents(arm.Then, fn)}
		}
		return ast.Case{Operand: mapIdents(x.Operand, fn), Whens: whens, Else: mapIdents(x.Else, fn)}
	case ast.Path:
		if len(x.Parts) == 0 {
			return x
		}
		parts := append([]string(nil), x.Parts...)
		parts[0] = fn(parts[0])
		return ast.Path{Parts: parts}
	case ast.ArrayCtor:
		els := make([]ast.Expr, len(x.Elems))
		for i, el := range x.Elems {
			els[i] = mapIdents(el, fn)
		}
		return ast.ArrayCtor{Elems: els}
	case ast.StructCtor:
		els := make([]ast.Expr, len(x.Elems))
		for i, el := range x.Elems {
			els[i] = mapIdents(el, fn)
		}
		return ast.StructCtor{Names: append([]string(nil), x.Names...), Elems: els}
	case ast.MapCtor:
		ks := make([]ast.Expr, len(x.Keys))
		vs := make([]ast.Expr, len(x.Vals))
		for i := range x.Keys {
			ks[i] = mapIdents(x.Keys[i], fn)
			vs[i] = mapIdents(x.Vals[i], fn)
		}
		return ast.MapCtor{Keys: ks, Vals: vs}
	case ast.FieldAccess:
		return ast.FieldAccess{Base: mapIdents(x.Base, fn), Field: x.Field}
	default:
		return e
	}
}

func adaptPredToRel(pred ast.Expr, rel planner.Logical) ast.Expr {
	tab := tableOf(rel)
	if tab == nil || pred == nil {
		return pred
	}
	return mapIdents(pred, func(name string) string {
		return resolveRelIdent(tab, name)
	})
}

func resolveRelIdent(tab *catalog.Table, name string) string {
	if tab == nil || name == "" {
		return name
	}
	if _, ok := tab.ColIndex(name); ok {
		return name
	}
	suf := name
	if i := lastDot(name); i >= 0 {
		suf = name[i+1:]
		if _, ok := tab.ColIndex(suf); ok {
			return suf
		}
	}
	var match string
	dotSuf := "." + suf
	for _, c := range tab.Columns {
		if len(c.Name) > len(dotSuf) && c.Name[len(c.Name)-len(dotSuf):] == dotSuf {
			if match != "" {
				return name
			}
			match = c.Name
		}
	}
	if match != "" {
		return match
	}
	return name
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

func walkIdents(e ast.Expr, fn func(string)) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case ast.Ident:
		fn(x.Name)
	case ast.Unary:
		walkIdents(x.Right, fn)
	case ast.Binary:
		walkIdents(x.Left, fn)
		walkIdents(x.Right, fn)
	case ast.Between:
		walkIdents(x.Expr, fn)
		walkIdents(x.Low, fn)
		walkIdents(x.High, fn)
	case ast.IsNull:
		walkIdents(x.Expr, fn)
	case ast.Call:
		for _, a := range x.Args {
			walkIdents(a, fn)
		}
	case ast.Window:
		walkIdents(x.Fn, fn)
		for _, p := range x.Partition {
			walkIdents(p, fn)
		}
		for _, o := range x.Order {
			walkIdents(o.Expr, fn)
		}
		if x.Frame != nil {
			walkIdents(x.Frame.Start.Offset, fn)
			walkIdents(x.Frame.End.Offset, fn)
		}
	case ast.Case:
		walkIdents(x.Operand, fn)
		for _, arm := range x.Whens {
			walkIdents(arm.When, fn)
			walkIdents(arm.Then, fn)
		}
		walkIdents(x.Else, fn)
	case ast.Path:
		if len(x.Parts) > 0 {
			fn(x.Parts[0])
		}
	case ast.ArrayCtor:
		for _, el := range x.Elems {
			walkIdents(el, fn)
		}
	case ast.StructCtor:
		for _, el := range x.Elems {
			walkIdents(el, fn)
		}
	case ast.MapCtor:
		for i := range x.Keys {
			walkIdents(x.Keys[i], fn)
			walkIdents(x.Vals[i], fn)
		}
	case ast.FieldAccess:
		walkIdents(x.Base, fn)
	}
}

func colOrds(e ast.Expr, tab *catalog.Table) []int {
	if tab == nil || e == nil {
		return nil
	}
	seen := map[int]struct{}{}
	var out []int
	walkIdents(e, func(n string) {
		if i, ok := tab.ColIndex(n); ok {
			if _, dup := seen[i]; !dup {
				seen[i] = struct{}{}
				out = append(out, i)
			}
		}
	})
	return out
}

func unionOrds(a, b []int) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, xs := range [][]int{a, b} {
		for _, i := range xs {
			if _, ok := seen[i]; ok {
				continue
			}
			seen[i] = struct{}{}
			out = append(out, i)
		}
	}
	return out
}

func hasCol(p planner.Logical, name string) bool {
	tab := tableOf(p)
	if tab == nil {
		return false
	}
	_, ok := tab.ColIndex(name)
	return ok
}

func tableOf(p planner.Logical) *catalog.Table {
	switch n := p.(type) {
	case planner.With:
		return tableOf(n.Query)
	case planner.CTEScan:
		return n.Schema
	case planner.Scan:
		return n.Table
	case planner.SeqScan:
		return n.Table
	case planner.IndexScan:
		return n.Table
	case planner.Search:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Facet:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Nearest:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Candidates:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Rerank:
		if n.Table != nil {
			return n.Table
		}
		return tableOf(n.Input)
	case planner.Filter:
		return tableOf(n.Input)
	case planner.Project:
		return tableOf(n.Input)
	case planner.Limit:
		return tableOf(n.Input)
	case planner.Sort:
		return tableOf(n.Input)
	case planner.Update:
		return n.Table
	case planner.Delete:
		return n.Table
	case planner.Join:
		if n.Kind == ast.JoinSemi || n.Kind == ast.JoinAnti {
			return tableOf(n.Left)
		}
		if n.Schema != nil {
			return n.Schema
		}
		if t := tableOf(n.Left); t != nil {
			return t
		}
		return tableOf(n.Right)
	case planner.Aggregate:
		if n.Schema != nil {
			return n.Schema
		}
		return tableOf(n.Input)
	case planner.Window:
		if n.Schema != nil {
			return n.Schema
		}
		return tableOf(n.Input)
	default:
		return nil
	}
}

func namesOf(p planner.Logical) []string {
	switch n := p.(type) {
	case planner.With:
		return namesOf(n.Query)
	case planner.CTEScan:
		return append([]string(nil), n.Names...)
	case planner.SetOperation:
		return append([]string(nil), n.Names...)
	case planner.Project:
		return append([]string(nil), n.Names...)
	case planner.Aggregate:
		return append([]string(nil), n.Names...)
	case planner.Window:
		if n.Schema != nil {
			out := make([]string, len(n.Schema.Columns))
			for i, c := range n.Schema.Columns {
				out[i] = c.Name
			}
			return out
		}
		return namesOf(n.Input)
	case planner.Empty:
		return append([]string(nil), n.Names...)
	case planner.Filter:
		return namesOf(n.Input)
	case planner.Limit:
		return namesOf(n.Input)
	case planner.Sort:
		names := namesOf(n.Input)
		if n.Hidden > 0 && n.Hidden <= len(names) {
			return names[:len(names)-n.Hidden]
		}
		return names
	case planner.Join:
		if n.Kind == ast.JoinSemi || n.Kind == ast.JoinAnti {
			return namesOf(n.Left)
		}
		return nil
	case planner.Scan:
		if n.Table == nil {
			return nil
		}
		out := make([]string, len(n.Table.Columns))
		for i, c := range n.Table.Columns {
			out[i] = c.Name
		}
		return out
	case planner.SeqScan:
		if n.Table == nil {
			return nil
		}
		out := make([]string, len(n.Table.Columns))
		for i, c := range n.Table.Columns {
			out[i] = c.Name
		}
		return out
	case planner.IndexScan:
		if n.Table == nil {
			return nil
		}
		out := make([]string, len(n.Table.Columns))
		for i, c := range n.Table.Columns {
			out[i] = c.Name
		}
		return out
	case planner.Search:
		if n.Table != nil {
			out := make([]string, len(n.Table.Columns))
			for i, c := range n.Table.Columns {
				out[i] = c.Name
			}
			return out
		}
		return namesOf(n.Input)
	case planner.Facet:
		return []string{"facet", "value", "count"}
	case planner.Nearest:
		if n.Table != nil {
			out := make([]string, len(n.Table.Columns))
			for i, c := range n.Table.Columns {
				out[i] = c.Name
			}
			return out
		}
		return namesOf(n.Input)
	case planner.Candidates:
		if n.Table != nil {
			out := make([]string, len(n.Table.Columns))
			for i, c := range n.Table.Columns {
				out[i] = c.Name
			}
			return out
		}
		return namesOf(n.Input)
	case planner.Rerank:
		if n.Table != nil {
			out := make([]string, len(n.Table.Columns))
			for i, c := range n.Table.Columns {
				out[i] = c.Name
			}
			return out
		}
		return namesOf(n.Input)
	default:
		return nil
	}
}

func predIsFalse(e ast.Expr) bool {
	lit, ok := e.(ast.Literal)
	if !ok {
		return false
	}
	if lit.Value.Null {
		return true
	}
	return lit.Value.Typ.Kind == types.KindBool && !lit.Value.Bool
}

func predIsTrue(e ast.Expr) bool {
	lit, ok := e.(ast.Literal)
	if !ok || lit.Value.Null {
		return false
	}
	return lit.Value.Typ.Kind == types.KindBool && lit.Value.Bool
}

func projectUsesAlias(pred ast.Expr, pr planner.Project) bool {
	tab := tableOf(pr.Input)
	for _, name := range identNames(pred) {
		if tab != nil {
			if _, ok := tab.ColIndex(name); ok {
				continue
			}
		}
		for _, n := range pr.Names {
			if n == name {
				return true
			}
		}
	}
	return false
}

func formatExpr(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch x := e.(type) {
	case ast.Literal:
		return x.Value.String()
	case ast.Ident:
		return x.Name
	case ast.Binary:
		return "(" + formatExpr(x.Left) + " " + x.Op + " " + formatExpr(x.Right) + ")"
	case ast.Unary:
		return x.Op + " " + formatExpr(x.Right)
	case ast.Between:
		s := formatExpr(x.Expr) + " BETWEEN " + formatExpr(x.Low) + " AND " + formatExpr(x.High)
		if x.Not {
			return "NOT " + s
		}
		return s
	case ast.IsNull:
		if x.Not {
			return formatExpr(x.Expr) + " IS NOT NULL"
		}
		return formatExpr(x.Expr) + " IS NULL"
	case ast.Case:
		s := "CASE"
		if x.Operand != nil {
			s += " " + formatExpr(x.Operand)
		}
		for _, arm := range x.Whens {
			s += " WHEN " + formatExpr(arm.When) + " THEN " + formatExpr(arm.Then)
		}
		if x.Else != nil {
			s += " ELSE " + formatExpr(x.Else)
		}
		return s + " END"
	case ast.Call:
		s := x.Name + "("
		for i, a := range x.Args {
			if i > 0 {
				s += ", "
			}
			s += formatExpr(a)
		}
		return s + ")"
	case ast.Window:
		return formatExpr(x.Fn) + " OVER"
	case ast.Path:
		s := ""
		for i, p := range x.Parts {
			if i > 0 {
				s += "."
			}
			s += p
		}
		return s
	case ast.ExistsSubquery:
		return "EXISTS"
	case ast.InSubquery:
		if x.Not {
			return formatExpr(x.Expr) + " NOT IN"
		}
		return formatExpr(x.Expr) + " IN"
	case ast.ScalarSubquery:
		return "SUBQUERY"
	default:
		return "?"
	}
}
