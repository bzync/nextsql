package binder

import (
	"fmt"
	"reflect"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func bindWindows(out *Select, schema *catalog.Table) error {
	if out == nil {
		return nil
	}
	n := 0
	for i, ex := range out.OutExprs {
		rewritten, wins, err := extractWindows(ex, out, schema, &n)
		if err != nil {
			return err
		}
		out.OutExprs[i] = rewritten
		out.Windows = append(out.Windows, wins...)
	}
	if len(out.Windows) == 0 {
		return nil
	}
	if out.HasAgg {
		if err := bindWindowsAfterAgg(out, schema); err != nil {
			return err
		}
	}
	return nil
}

func extractWindows(e ast.Expr, out *Select, schema *catalog.Table, n *int) (ast.Expr, []BoundWindow, error) {
	if e == nil {
		return nil, nil, nil
	}
	switch x := e.(type) {
	case ast.Window:
		if containsWindow(x.Fn) {
			return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", "nested window functions are not supported")
		}
		for _, p := range x.Partition {
			if containsWindow(p) {
				return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", "nested window functions are not supported")
			}
		}
		for _, o := range x.Order {
			if containsWindow(o.Expr) {
				return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", "nested window functions are not supported")
			}
		}
		bw, err := bindOneWindow(x, schema, *n)
		if err != nil {
			return nil, nil, err
		}
		*n++
		return ast.Ident{Name: bw.Result}, []BoundWindow{bw}, nil
	case ast.Unary:
		r, wins, err := extractWindows(x.Right, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		x.Right = r
		return x, wins, nil
	case ast.Binary:
		l, lw, err := extractWindows(x.Left, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		r, rw, err := extractWindows(x.Right, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		x.Left, x.Right = l, r
		return x, append(lw, rw...), nil
	case ast.Between:
		v, vw, err := extractWindows(x.Expr, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		lo, low, err := extractWindows(x.Low, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		hi, hiw, err := extractWindows(x.High, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		x.Expr, x.Low, x.High = v, lo, hi
		return x, append(append(vw, low...), hiw...), nil
	case ast.IsNull:
		inner, wins, err := extractWindows(x.Expr, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		x.Expr = inner
		return x, wins, nil
	case ast.Call:
		var wins []BoundWindow
		args := make([]ast.Expr, len(x.Args))
		for i, a := range x.Args {
			r, w, err := extractWindows(a, out, schema, n)
			if err != nil {
				return nil, nil, err
			}
			args[i] = r
			wins = append(wins, w...)
		}
		x.Args = args
		return x, wins, nil
	case ast.Case:
		var wins []BoundWindow
		op, w, err := extractWindows(x.Operand, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		wins = append(wins, w...)
		x.Operand = op
		for i, arm := range x.Whens {
			when, ww, err := extractWindows(arm.When, out, schema, n)
			if err != nil {
				return nil, nil, err
			}
			then, tw, err := extractWindows(arm.Then, out, schema, n)
			if err != nil {
				return nil, nil, err
			}
			x.Whens[i] = ast.CaseWhen{When: when, Then: then}
			wins = append(wins, ww...)
			wins = append(wins, tw...)
		}
		el, ew, err := extractWindows(x.Else, out, schema, n)
		if err != nil {
			return nil, nil, err
		}
		x.Else = el
		return x, append(wins, ew...), nil
	default:
		return e, nil, nil
	}
}

func bindOneWindow(w ast.Window, schema *catalog.Table, n int) (BoundWindow, error) {
	if err := checkWindowExpr(w, schema); err != nil {
		return BoundWindow{}, err
	}
	frame, err := bindFrame(w)
	if err != nil {
		return BoundWindow{}, err
	}
	outType, err := windowResultType(w, schema)
	if err != nil {
		return BoundWindow{}, err
	}
	return BoundWindow{
		Fun:       w.Fn.Name,
		Args:      append([]ast.Expr(nil), w.Fn.Args...),
		Star:      w.Fn.Star,
		Partition: append([]ast.Expr(nil), w.Partition...),
		Order:     append([]ast.OrderItem(nil), w.Order...),
		Frame:     frame,
		Result:    fmt.Sprintf("?w%d", n),
		OutType:   outType,
	}, nil
}

func checkWindowExpr(w ast.Window, tab *catalog.Table) error {
	if containsWindow(w.Fn) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "nested window functions are not supported")
	}
	if err := checkWindowFn(w.Fn); err != nil {
		return err
	}
	if w.Fn.Star {
		if err := checkExpr(ast.Call{Name: w.Fn.Name, Star: true}, tab, types.Type{}, false); err != nil && w.Fn.Name != "count" {
			return err
		}
	} else {
		for _, a := range w.Fn.Args {
			if containsWindow(a) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "nested window functions are not supported")
			}
			if err := checkExpr(a, tab, types.Type{}, false); err != nil {
				return err
			}
		}
	}
	for _, p := range w.Partition {
		if containsWindow(p) {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "nested window functions are not supported")
		}
		if err := checkExpr(p, tab, types.Type{}, false); err != nil {
			return err
		}
	}
	for _, o := range w.Order {
		if containsWindow(o.Expr) {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "nested window functions are not supported")
		}
		if err := checkExpr(o.Expr, tab, types.Type{}, false); err != nil {
			return err
		}
	}
	if w.Frame != nil {
		if err := checkExpr(w.Frame.Start.Offset, tab, types.Type{}, true); err != nil {
			return err
		}
		if err := checkExpr(w.Frame.End.Offset, tab, types.Type{}, true); err != nil {
			return err
		}
	}
	return nil
}

func checkWindowFn(fn ast.Call) error {
	switch fn.Name {
	case "row_number", "rank", "dense_rank":
		if fn.Star || len(fn.Args) != 0 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", fn.Name+" takes no arguments")
		}
		return nil
	case "lag", "lead":
		if fn.Star || len(fn.Args) < 1 || len(fn.Args) > 3 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", fn.Name+" takes one to three arguments")
		}
		if len(fn.Args) >= 2 {
			if _, err := nonNegIntLiteral(fn.Args[1]); err != nil {
				return nerr.New(nerr.InvalidArgument, "sql.binder", fn.Name+" offset must be a non-negative integer literal")
			}
		}
		return nil
	case "first_value", "last_value":
		if fn.Star || len(fn.Args) != 1 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", fn.Name+" takes one argument")
		}
		return nil
	case "count":
		if fn.Star {
			if len(fn.Args) != 0 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "count does not accept arguments with *")
			}
			return nil
		}
		if len(fn.Args) != 1 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "count takes one argument")
		}
		return nil
	case "sum", "avg", "min", "max":
		if fn.Star || len(fn.Args) != 1 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", fn.Name+" takes one argument")
		}
		return nil
	default:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "function does not support OVER")
	}
}

func bindFrame(w ast.Window) (ast.Frame, error) {
	if w.Frame == nil {
		if len(w.Order) == 0 {
			return ast.Frame{
				Mode:  ast.FrameRange,
				Start: ast.FrameBound{Kind: ast.BoundUnboundedPreceding},
				End:   ast.FrameBound{Kind: ast.BoundUnboundedFollowing},
			}, nil
		}
		return ast.Frame{
			Mode:  ast.FrameRange,
			Start: ast.FrameBound{Kind: ast.BoundUnboundedPreceding},
			End:   ast.FrameBound{Kind: ast.BoundCurrentRow},
		}, nil
	}
	fr := *w.Frame
	if err := checkFrameBound(fr.Start); err != nil {
		return ast.Frame{}, err
	}
	if err := checkFrameBound(fr.End); err != nil {
		return ast.Frame{}, err
	}
	if fr.Start.Kind == ast.BoundUnboundedFollowing {
		return ast.Frame{}, nerr.New(nerr.InvalidArgument, "sql.binder", "frame start cannot be UNBOUNDED FOLLOWING")
	}
	if fr.End.Kind == ast.BoundUnboundedPreceding {
		return ast.Frame{}, nerr.New(nerr.InvalidArgument, "sql.binder", "frame end cannot be UNBOUNDED PRECEDING")
	}
	if fr.Mode == ast.FrameRange && (fr.Start.Kind == ast.BoundPreceding || fr.Start.Kind == ast.BoundFollowing || fr.End.Kind == ast.BoundPreceding || fr.End.Kind == ast.BoundFollowing) {
		return ast.Frame{}, nerr.New(nerr.InvalidArgument, "sql.binder", "RANGE offset frames are not supported")
	}
	if frameKindOrder(fr.Start.Kind) > frameKindOrder(fr.End.Kind) {
		return ast.Frame{}, nerr.New(nerr.InvalidArgument, "sql.binder", "frame start follows frame end")
	}
	if fr.Start.Kind == ast.BoundPreceding && fr.End.Kind == ast.BoundPreceding {
		s, _ := nonNegIntLiteral(fr.Start.Offset)
		e, _ := nonNegIntLiteral(fr.End.Offset)
		if s < e {
			return ast.Frame{}, nerr.New(nerr.InvalidArgument, "sql.binder", "frame start follows frame end")
		}
	}
	if fr.Start.Kind == ast.BoundFollowing && fr.End.Kind == ast.BoundFollowing {
		s, _ := nonNegIntLiteral(fr.Start.Offset)
		e, _ := nonNegIntLiteral(fr.End.Offset)
		if s > e {
			return ast.Frame{}, nerr.New(nerr.InvalidArgument, "sql.binder", "frame start follows frame end")
		}
	}
	return fr, nil
}

func checkFrameBound(b ast.FrameBound) error {
	switch b.Kind {
	case ast.BoundPreceding, ast.BoundFollowing:
		if _, err := nonNegIntLiteral(b.Offset); err != nil {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "frame offset must be a non-negative integer literal")
		}
	}
	return nil
}

func frameKindOrder(k ast.BoundKind) int {
	switch k {
	case ast.BoundUnboundedPreceding:
		return 0
	case ast.BoundPreceding:
		return 1
	case ast.BoundCurrentRow:
		return 2
	case ast.BoundFollowing:
		return 3
	case ast.BoundUnboundedFollowing:
		return 4
	default:
		return 2
	}
}

func nonNegIntLiteral(e ast.Expr) (int64, error) {
	lit, ok := e.(ast.Literal)
	if !ok || lit.Value.Null || lit.Value.Typ.Kind != types.KindDecimal || lit.Value.Dec.Coef == nil {
		return 0, nerr.New(nerr.InvalidArgument, "sql.binder", "expected a non-negative integer")
	}
	if lit.Value.Dec.Scale != 0 || !lit.Value.Dec.Coef.IsInt64() {
		return 0, nerr.New(nerr.InvalidArgument, "sql.binder", "expected a non-negative integer")
	}
	n := lit.Value.Dec.Coef.Int64()
	if n < 0 {
		return 0, nerr.New(nerr.InvalidArgument, "sql.binder", "expected a non-negative integer")
	}
	return n, nil
}

func windowResultType(w ast.Window, schema *catalog.Table) (types.Type, error) {
	switch w.Fn.Name {
	case "row_number", "rank", "dense_rank", "count":
		return types.Type{Kind: types.KindDecimal}, nil
	case "sum", "avg":
		return types.Type{Kind: types.KindDecimal}, nil
	case "min", "max", "first_value", "last_value", "lag", "lead":
		if len(w.Fn.Args) == 0 {
			return types.Type{}, nerr.New(nerr.InvalidArgument, "sql.binder", w.Fn.Name+" takes one argument")
		}
		return exprType(w.Fn.Args[0], schema), nil
	default:
		return types.Type{Kind: types.KindDecimal}, nil
	}
}

func exprType(e ast.Expr, schema *catalog.Table) types.Type {
	switch x := e.(type) {
	case ast.Ident:
		if schema != nil {
			if i, ok := schema.ColIndex(x.Name); ok {
				return schema.Columns[i].Type
			}
		}
	case ast.Literal:
		return x.Value.Typ
	}
	return types.Type{}
}

func containsWindow(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.Window:
		return true
	case ast.Unary:
		return containsWindow(x.Right)
	case ast.Binary:
		return containsWindow(x.Left) || containsWindow(x.Right)
	case ast.Between:
		return containsWindow(x.Expr) || containsWindow(x.Low) || containsWindow(x.High)
	case ast.IsNull:
		return containsWindow(x.Expr)
	case ast.Call:
		for _, a := range x.Args {
			if containsWindow(a) {
				return true
			}
		}
	case ast.Case:
		if containsWindow(x.Operand) || containsWindow(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if containsWindow(arm.When) || containsWindow(arm.Then) {
				return true
			}
		}
	}
	return false
}

func containsGroupingAgg(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.Window:
		return false
	case ast.Call:
		if isAgg(x.Name) {
			return true
		}
		for _, a := range x.Args {
			if containsGroupingAgg(a) {
				return true
			}
		}
	case ast.Unary:
		return containsGroupingAgg(x.Right)
	case ast.Binary:
		return containsGroupingAgg(x.Left) || containsGroupingAgg(x.Right)
	case ast.Between:
		return containsGroupingAgg(x.Expr) || containsGroupingAgg(x.Low) || containsGroupingAgg(x.High)
	case ast.IsNull:
		return containsGroupingAgg(x.Expr)
	case ast.Case:
		if containsGroupingAgg(x.Operand) || containsGroupingAgg(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if containsGroupingAgg(arm.When) || containsGroupingAgg(arm.Then) {
				return true
			}
		}
	}
	return false
}

func bindWindowsAfterAgg(out *Select, input *catalog.Table) error {
	inter := &interSchema{}
	for _, g := range out.Groups {
		name := "?"
		if id, ok := g.(ast.Ident); ok {
			name = id.Name
		}
		inter.add(g, name)
	}
	for i, a := range out.Aggs {
		name := a.Fun
		if i < len(out.OutNames) {
			for j, ex := range out.OutExprs {
				if call, ok := ex.(ast.Call); ok && isAgg(call.Name) && call.Name == a.Fun && call.Star == a.Star {
					name = out.OutNames[j]
					break
				}
			}
		}
		call := ast.Call{Name: a.Fun, Args: nil, Star: a.Star}
		if a.Arg != nil {
			call.Args = []ast.Expr{a.Arg}
		}
		inter.add(call, name)
	}
	for i := range out.Windows {
		if err := collectWindowAggs(&out.Windows[i], out, inter); err != nil {
			return err
		}
	}
	aggTab := inter.table()
	for i := range out.Windows {
		if err := rewriteWindowAgainst(out, &out.Windows[i], aggTab, inter); err != nil {
			return err
		}
	}
	for i, ex := range out.OutExprs {
		out.OutExprs[i] = rewriteAggRefs(ex, inter)
	}
	out.AggExprs = append([]ast.Expr(nil), inter.exprs...)
	out.AggNames = append([]string(nil), inter.names...)
	out.AggSchema = aggTab
	_ = input
	return nil
}

type interSchema struct {
	exprs []ast.Expr
	names []string
}

func (s *interSchema) add(e ast.Expr, name string) string {
	if e == nil {
		return ""
	}
	for i, ex := range s.exprs {
		if exprEqual(ex, e) {
			return s.names[i]
		}
	}
	if name == "" {
		name = "?"
	}
	base := name
	dup := 0
	for {
		taken := false
		for _, n := range s.names {
			if n == name {
				taken = true
				break
			}
		}
		if !taken {
			break
		}
		dup++
		name = fmt.Sprintf("%s_%d", base, dup)
	}
	s.exprs = append(s.exprs, e)
	s.names = append(s.names, name)
	return name
}

func (s *interSchema) table() *catalog.Table {
	tab := &catalog.Table{Name: "window_input", Columns: make([]catalog.Column, len(s.names))}
	for i, name := range s.names {
		typ := types.Type{Kind: types.KindDecimal}
		if id, ok := s.exprs[i].(ast.Ident); ok {
			typ = exprType(id, nil)
		}
		tab.Columns[i] = catalog.Column{Name: name, Type: typ}
	}
	return tab
}

func collectWindowAggs(w *BoundWindow, out *Select, inter *interSchema) error {
	rewrite := func(e ast.Expr) (ast.Expr, error) {
		if e == nil {
			return nil, nil
		}
		if containsGroupingAgg(e) {
			if call, ok := e.(ast.Call); ok && isAgg(call.Name) {
				name := inter.add(call, call.Name)
				if !aggListed(out, call) {
					col := -1
					var arg ast.Expr
					if !call.Star && len(call.Args) == 1 {
						arg = call.Args[0]
						if id, ok := arg.(ast.Ident); ok {
							if i, found := out.Schema.ColIndex(id.Name); found {
								col = i
							}
						}
					}
					out.Aggs = append(out.Aggs, Agg{Fun: call.Name, Arg: arg, Col: col, Star: call.Star})
				}
				return ast.Ident{Name: name}, nil
			}
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "window PARTITION/ORDER BY must be a grouped expression or selected aggregate")
		}
		if grouped(e, out.Groups) {
			name := inter.add(e, identName(e))
			return ast.Ident{Name: name}, nil
		}
		if id, ok := e.(ast.Ident); ok {
			for i, n := range out.OutNames {
				if n == id.Name {
					return rewriteAggRefs(out.OutExprs[i], inter), nil
				}
			}
		}
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "window PARTITION/ORDER BY must be a grouped expression or selected aggregate")
	}
	var err error
	for i, p := range w.Partition {
		w.Partition[i], err = rewrite(p)
		if err != nil {
			return err
		}
	}
	for i, o := range w.Order {
		w.Order[i].Expr, err = rewrite(o.Expr)
		if err != nil {
			return err
		}
	}
	for i, a := range w.Args {
		w.Args[i], err = rewrite(a)
		if err != nil {
			return err
		}
	}
	return nil
}

func rewriteWindowAgainst(out *Select, w *BoundWindow, tab *catalog.Table, inter *interSchema) error {
	_ = out
	_ = tab
	_ = inter
	return nil
}

func rewriteAggRefs(e ast.Expr, inter *interSchema) ast.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case ast.Call:
		if isAgg(x.Name) {
			for i, ex := range inter.exprs {
				if exprEqual(ex, x) {
					return ast.Ident{Name: inter.names[i]}
				}
			}
		}
		args := make([]ast.Expr, len(x.Args))
		for i := range x.Args {
			args[i] = rewriteAggRefs(x.Args[i], inter)
		}
		x.Args = args
		return x
	case ast.Ident:
		for i, ex := range inter.exprs {
			if exprEqual(ex, x) || identName(ex) == x.Name {
				return ast.Ident{Name: inter.names[i]}
			}
		}
		return x
	case ast.Unary:
		x.Right = rewriteAggRefs(x.Right, inter)
		return x
	case ast.Binary:
		x.Left = rewriteAggRefs(x.Left, inter)
		x.Right = rewriteAggRefs(x.Right, inter)
		return x
	case ast.Between:
		x.Expr = rewriteAggRefs(x.Expr, inter)
		x.Low = rewriteAggRefs(x.Low, inter)
		x.High = rewriteAggRefs(x.High, inter)
		return x
	case ast.IsNull:
		x.Expr = rewriteAggRefs(x.Expr, inter)
		return x
	case ast.Case:
		x.Operand = rewriteAggRefs(x.Operand, inter)
		x.Else = rewriteAggRefs(x.Else, inter)
		for i := range x.Whens {
			x.Whens[i].When = rewriteAggRefs(x.Whens[i].When, inter)
			x.Whens[i].Then = rewriteAggRefs(x.Whens[i].Then, inter)
		}
		return x
	default:
		return e
	}
}

func aggListed(out *Select, call ast.Call) bool {
	for _, a := range out.Aggs {
		if a.Fun == call.Name && a.Star == call.Star {
			if call.Star || (len(call.Args) == 1 && exprEqual(a.Arg, call.Args[0])) || (a.Arg == nil && len(call.Args) == 0) {
				return true
			}
		}
	}
	return false
}

func identName(e ast.Expr) string {
	if id, ok := e.(ast.Ident); ok {
		return id.Name
	}
	return "?"
}

func exprEqual(a, b ast.Expr) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if ka, kb := exprKey(a), exprKey(b); ka != "" && ka == kb {
		return true
	}
	return reflect.DeepEqual(a, b)
}
