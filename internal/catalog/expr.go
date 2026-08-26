package catalog

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	exprNil byte = iota
	exprLiteral
	exprIdent
	exprPath
	exprUnary
	exprBinary
	exprBetween
	exprIsNull
	exprCall
	exprCase
	exprParam
)

func appendExpr(buf []byte, e ast.Expr) ([]byte, error) {
	if e == nil {
		return append(buf, exprNil), nil
	}
	switch x := e.(type) {
	case ast.Literal:
		buf = append(buf, exprLiteral)
		buf = appendType(buf, x.Value.Typ)
		raw, err := types.EncodeRow([]types.Value{x.Value})
		if err != nil {
			return nil, err
		}
		if len(raw) > int(^uint16(0)) {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.appendExpr", "encoded literal exceeds size limit")
		}
		return appendBytes(buf, raw), nil
	case ast.Ident:
		buf = append(buf, exprIdent)
		return appendString(buf, x.Name), nil
	case ast.Param:
		buf = append(buf, exprParam)
		return appendString(buf, x.Name), nil
	case ast.Path:
		buf = append(buf, exprPath)
		buf = appendU16(buf, uint16(len(x.Parts)))
		for _, p := range x.Parts {
			buf = appendString(buf, p)
		}
		return buf, nil
	case ast.Unary:
		buf = append(buf, exprUnary)
		buf = appendString(buf, x.Op)
		return appendExpr(buf, x.Right)
	case ast.Binary:
		buf = append(buf, exprBinary)
		buf = appendString(buf, x.Op)
		var err error
		buf, err = appendExpr(buf, x.Left)
		if err != nil {
			return nil, err
		}
		return appendExpr(buf, x.Right)
	case ast.Between:
		buf = append(buf, exprBetween)
		if x.Not {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
		var err error
		buf, err = appendExpr(buf, x.Expr)
		if err != nil {
			return nil, err
		}
		buf, err = appendExpr(buf, x.Low)
		if err != nil {
			return nil, err
		}
		return appendExpr(buf, x.High)
	case ast.IsNull:
		buf = append(buf, exprIsNull)
		if x.Not {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
		return appendExpr(buf, x.Expr)
	case ast.Call:
		buf = append(buf, exprCall)
		buf = appendString(buf, x.Name)
		if x.Star {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
		buf = appendU16(buf, uint16(len(x.Args)))
		for _, a := range x.Args {
			var err error
			buf, err = appendExpr(buf, a)
			if err != nil {
				return nil, err
			}
		}
		return buf, nil
	case ast.Case:
		buf = append(buf, exprCase)
		if x.Operand != nil {
			buf = append(buf, 1)
			var err error
			buf, err = appendExpr(buf, x.Operand)
			if err != nil {
				return nil, err
			}
		} else {
			buf = append(buf, 0)
		}
		buf = appendU16(buf, uint16(len(x.Whens)))
		for _, arm := range x.Whens {
			var err error
			buf, err = appendExpr(buf, arm.When)
			if err != nil {
				return nil, err
			}
			buf, err = appendExpr(buf, arm.Then)
			if err != nil {
				return nil, err
			}
		}
		if x.Else != nil {
			buf = append(buf, 1)
			return appendExpr(buf, x.Else)
		}
		return append(buf, 0), nil
	default:
		return nil, nerr.New(nerr.InvalidArgument, "catalog.appendExpr", "unsupported index expression")
	}
}

func takeExpr(raw []byte, off int) (ast.Expr, int, error) {
	if off >= len(raw) {
		return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeExpr", "truncated expression")
	}
	tag := raw[off]
	off++
	switch tag {
	case exprNil:
		return nil, off, nil
	case exprLiteral:
		typ, off, err := takeType(raw, off)
		if err != nil {
			return nil, 0, err
		}
		body, off, err := takeBytes(raw, off)
		if err != nil {
			return nil, 0, err
		}
		vals, err := types.DecodeRow(body, []types.Type{typ})
		if err != nil {
			return nil, 0, err
		}
		return ast.Literal{Value: vals[0]}, off, nil
	case exprIdent:
		name, off, err := takeString(raw, off)
		if err != nil {
			return nil, 0, err
		}
		return ast.Ident{Name: name}, off, nil
	case exprParam:
		name, off, err := takeString(raw, off)
		if err != nil {
			return nil, 0, err
		}
		return ast.Param{Name: name}, off, nil
	case exprPath:
		n, off, err := takeU16(raw, off)
		if err != nil {
			return nil, 0, err
		}
		parts := make([]string, 0, n)
		for i := 0; i < int(n); i++ {
			var part string
			part, off, err = takeString(raw, off)
			if err != nil {
				return nil, 0, err
			}
			parts = append(parts, part)
		}
		return ast.Path{Parts: parts}, off, nil
	case exprUnary:
		op, off, err := takeString(raw, off)
		if err != nil {
			return nil, 0, err
		}
		right, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		return ast.Unary{Op: op, Right: right}, off, nil
	case exprBinary:
		op, off, err := takeString(raw, off)
		if err != nil {
			return nil, 0, err
		}
		left, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		right, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		return ast.Binary{Op: op, Left: left, Right: right}, off, nil
	case exprBetween:
		if off >= len(raw) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeExpr", "truncated between")
		}
		not := raw[off] != 0
		off++
		ex, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		low, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		high, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		return ast.Between{Expr: ex, Low: low, High: high, Not: not}, off, nil
	case exprIsNull:
		if off >= len(raw) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeExpr", "truncated is null")
		}
		not := raw[off] != 0
		off++
		ex, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		return ast.IsNull{Expr: ex, Not: not}, off, nil
	case exprCall:
		name, off, err := takeString(raw, off)
		if err != nil {
			return nil, 0, err
		}
		if off >= len(raw) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeExpr", "truncated call")
		}
		star := raw[off] != 0
		off++
		n, off, err := takeU16(raw, off)
		if err != nil {
			return nil, 0, err
		}
		args := make([]ast.Expr, 0, n)
		for i := 0; i < int(n); i++ {
			var a ast.Expr
			a, off, err = takeExpr(raw, off)
			if err != nil {
				return nil, 0, err
			}
			args = append(args, a)
		}
		return ast.Call{Name: name, Args: args, Star: star}, off, nil
	case exprCase:
		if off >= len(raw) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeExpr", "truncated case")
		}
		var operand ast.Expr
		var err error
		if raw[off] != 0 {
			off++
			operand, off, err = takeExpr(raw, off)
			if err != nil {
				return nil, 0, err
			}
		} else {
			off++
		}
		n, off, err := takeU16(raw, off)
		if err != nil {
			return nil, 0, err
		}
		whens := make([]ast.CaseWhen, 0, n)
		for i := 0; i < int(n); i++ {
			var when, then ast.Expr
			when, off, err = takeExpr(raw, off)
			if err != nil {
				return nil, 0, err
			}
			then, off, err = takeExpr(raw, off)
			if err != nil {
				return nil, 0, err
			}
			whens = append(whens, ast.CaseWhen{When: when, Then: then})
		}
		if off >= len(raw) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeExpr", "truncated case else")
		}
		var els ast.Expr
		if raw[off] != 0 {
			off++
			els, off, err = takeExpr(raw, off)
			if err != nil {
				return nil, 0, err
			}
		} else {
			off++
		}
		return ast.Case{Operand: operand, Whens: whens, Else: els}, off, nil
	default:
		return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeExpr", "unknown expression tag")
	}
}

func appendType(buf []byte, t types.Type) []byte {
	buf = append(buf, byte(t.Kind), t.VecElem)
	buf = appendU16(buf, t.Precision)
	buf = appendU16(buf, t.Scale)
	return buf
}

func takeType(raw []byte, off int) (types.Type, int, error) {
	if off+2 > len(raw) {
		return types.Type{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeType", "truncated type")
	}
	t := types.Type{Kind: types.Kind(raw[off]), VecElem: raw[off+1]}
	off += 2
	var err error
	t.Precision, off, err = takeU16(raw, off)
	if err != nil {
		return types.Type{}, 0, err
	}
	t.Scale, off, err = takeU16(raw, off)
	if err != nil {
		return types.Type{}, 0, err
	}
	return t, off, nil
}

func FormatExpr(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch x := e.(type) {
	case ast.Literal:
		return x.Value.String()
	case ast.Ident:
		return x.Name
	case ast.Path:
		s := ""
		for i, p := range x.Parts {
			if i > 0 {
				s += "."
			}
			s += p
		}
		return s
	case ast.Unary:
		return x.Op + " " + FormatExpr(x.Right)
	case ast.Binary:
		return "(" + FormatExpr(x.Left) + " " + x.Op + " " + FormatExpr(x.Right) + ")"
	case ast.Between:
		s := FormatExpr(x.Expr) + " BETWEEN " + FormatExpr(x.Low) + " AND " + FormatExpr(x.High)
		if x.Not {
			return "NOT " + s
		}
		return s
	case ast.IsNull:
		if x.Not {
			return FormatExpr(x.Expr) + " IS NOT NULL"
		}
		return FormatExpr(x.Expr) + " IS NULL"
	case ast.Call:
		s := x.Name + "("
		for i, a := range x.Args {
			if i > 0 {
				s += ", "
			}
			s += FormatExpr(a)
		}
		if x.Star {
			s += "*"
		}
		return s + ")"
	case ast.Case:
		s := "CASE"
		if x.Operand != nil {
			s += " " + FormatExpr(x.Operand)
		}
		for _, arm := range x.Whens {
			s += " WHEN " + FormatExpr(arm.When) + " THEN " + FormatExpr(arm.Then)
		}
		if x.Else != nil {
			s += " ELSE " + FormatExpr(x.Else)
		}
		return s + " END"
	default:
		return "?"
	}
}

func ExprEqual(a, b ast.Expr) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch x := a.(type) {
	case ast.Literal:
		y, ok := b.(ast.Literal)
		if !ok {
			return false
		}
		c, err := x.Value.Cmp(y.Value)
		return err == nil && c == 0
	case ast.Ident:
		y, ok := b.(ast.Ident)
		return ok && x.Name == y.Name
	case ast.Path:
		y, ok := b.(ast.Path)
		if !ok || len(x.Parts) != len(y.Parts) {
			return false
		}
		for i := range x.Parts {
			if x.Parts[i] != y.Parts[i] {
				return false
			}
		}
		return true
	case ast.Unary:
		y, ok := b.(ast.Unary)
		return ok && x.Op == y.Op && ExprEqual(x.Right, y.Right)
	case ast.Binary:
		y, ok := b.(ast.Binary)
		return ok && x.Op == y.Op && ExprEqual(x.Left, y.Left) && ExprEqual(x.Right, y.Right)
	case ast.Between:
		y, ok := b.(ast.Between)
		return ok && x.Not == y.Not && ExprEqual(x.Expr, y.Expr) && ExprEqual(x.Low, y.Low) && ExprEqual(x.High, y.High)
	case ast.IsNull:
		y, ok := b.(ast.IsNull)
		return ok && x.Not == y.Not && ExprEqual(x.Expr, y.Expr)
	case ast.Call:
		y, ok := b.(ast.Call)
		if !ok || x.Name != y.Name || x.Star != y.Star || len(x.Args) != len(y.Args) {
			return false
		}
		for i := range x.Args {
			if !ExprEqual(x.Args[i], y.Args[i]) {
				return false
			}
		}
		return true
	case ast.Case:
		y, ok := b.(ast.Case)
		if !ok || len(x.Whens) != len(y.Whens) || !ExprEqual(x.Operand, y.Operand) || !ExprEqual(x.Else, y.Else) {
			return false
		}
		for i := range x.Whens {
			if !ExprEqual(x.Whens[i].When, y.Whens[i].When) || !ExprEqual(x.Whens[i].Then, y.Whens[i].Then) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func ExprUsesIdent(e ast.Expr, name string) bool {
	if e == nil || name == "" {
		return false
	}
	switch x := e.(type) {
	case ast.Ident:
		return x.Name == name
	case ast.Path:
		return len(x.Parts) > 0 && x.Parts[0] == name
	case ast.Unary:
		return ExprUsesIdent(x.Right, name)
	case ast.Binary:
		return ExprUsesIdent(x.Left, name) || ExprUsesIdent(x.Right, name)
	case ast.Between:
		return ExprUsesIdent(x.Expr, name) || ExprUsesIdent(x.Low, name) || ExprUsesIdent(x.High, name)
	case ast.IsNull:
		return ExprUsesIdent(x.Expr, name)
	case ast.Call:
		for _, a := range x.Args {
			if ExprUsesIdent(a, name) {
				return true
			}
		}
	case ast.Case:
		if ExprUsesIdent(x.Operand, name) || ExprUsesIdent(x.Else, name) {
			return true
		}
		for _, arm := range x.Whens {
			if ExprUsesIdent(arm.When, name) || ExprUsesIdent(arm.Then, name) {
				return true
			}
		}
	}
	return false
}

func RewriteIdent(e ast.Expr, old, neu string) ast.Expr {
	if e == nil || old == "" || old == neu {
		return e
	}
	switch x := e.(type) {
	case ast.Ident:
		if x.Name == old {
			return ast.Ident{Name: neu}
		}
		return x
	case ast.Path:
		if len(x.Parts) > 0 && x.Parts[0] == old {
			parts := append([]string(nil), x.Parts...)
			parts[0] = neu
			return ast.Path{Parts: parts}
		}
		return x
	case ast.Unary:
		return ast.Unary{Op: x.Op, Right: RewriteIdent(x.Right, old, neu)}
	case ast.Binary:
		return ast.Binary{Op: x.Op, Left: RewriteIdent(x.Left, old, neu), Right: RewriteIdent(x.Right, old, neu)}
	case ast.Between:
		return ast.Between{Expr: RewriteIdent(x.Expr, old, neu), Low: RewriteIdent(x.Low, old, neu), High: RewriteIdent(x.High, old, neu), Not: x.Not}
	case ast.IsNull:
		return ast.IsNull{Expr: RewriteIdent(x.Expr, old, neu), Not: x.Not}
	case ast.Call:
		args := make([]ast.Expr, len(x.Args))
		for i, a := range x.Args {
			args[i] = RewriteIdent(a, old, neu)
		}
		return ast.Call{Name: x.Name, Args: args, Star: x.Star}
	case ast.Case:
		whens := make([]ast.CaseWhen, len(x.Whens))
		for i, arm := range x.Whens {
			whens[i] = ast.CaseWhen{When: RewriteIdent(arm.When, old, neu), Then: RewriteIdent(arm.Then, old, neu)}
		}
		return ast.Case{Operand: RewriteIdent(x.Operand, old, neu), Whens: whens, Else: RewriteIdent(x.Else, old, neu)}
	default:
		return e
	}
}

func ExprVolatile(e ast.Expr) bool {
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
			if ExprVolatile(a) {
				return true
			}
		}
	case ast.Unary:
		return ExprVolatile(x.Right)
	case ast.Binary:
		return ExprVolatile(x.Left) || ExprVolatile(x.Right)
	case ast.Between:
		return ExprVolatile(x.Expr) || ExprVolatile(x.Low) || ExprVolatile(x.High)
	case ast.IsNull:
		return ExprVolatile(x.Expr)
	case ast.Case:
		if ExprVolatile(x.Operand) || ExprVolatile(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if ExprVolatile(arm.When) || ExprVolatile(arm.Then) {
				return true
			}
		}
	case ast.Window, ast.ScalarSubquery, ast.InSubquery, ast.ExistsSubquery, ast.Param:
		return true
	}
	return false
}

func ExprHasSubquery(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.ScalarSubquery, ast.InSubquery, ast.ExistsSubquery:
		return true
	case ast.Unary:
		return ExprHasSubquery(x.Right)
	case ast.Binary:
		return ExprHasSubquery(x.Left) || ExprHasSubquery(x.Right)
	case ast.Between:
		return ExprHasSubquery(x.Expr) || ExprHasSubquery(x.Low) || ExprHasSubquery(x.High)
	case ast.IsNull:
		return ExprHasSubquery(x.Expr)
	case ast.Call:
		for _, a := range x.Args {
			if ExprHasSubquery(a) {
				return true
			}
		}
	case ast.Case:
		if ExprHasSubquery(x.Operand) || ExprHasSubquery(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if ExprHasSubquery(arm.When) || ExprHasSubquery(arm.Then) {
				return true
			}
		}
	}
	return false
}
