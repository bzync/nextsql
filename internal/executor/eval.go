package executor

import (
	stdjson "encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/catalog"
	nsjson "github.com/bzync/nextsql/internal/json"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	nsvec "github.com/bzync/nextsql/internal/vector"
)

func (s *Session) eval(e ast.Expr, tab *catalog.Table, row []types.Value) (types.Value, error) {
	if e == nil {
		return types.Null(types.NullType()), nil
	}
	switch x := e.(type) {
	case ast.ScalarSubquery:
		return s.evalScalarSubquery(x, tab, row)
	case ast.InSubquery:
		return s.evalInSubquery(x, tab, row)
	case ast.ExistsSubquery:
		result, err := s.evalSubqueryAnyColumns(x.ID, x.Query, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		return types.BoolValue(len(result.Rows) > 0), nil
	case ast.Literal:
		return x.Value, nil
	case ast.VectorLit:
		return types.VectorValue(x.Elems, types.Type{Kind: types.KindVector, Precision: uint16(len(x.Elems)), VecElem: types.VecF32}), nil
	case ast.Ident:
		if tab == nil {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "column reference is not valid in this context")
		}
		i, ok := tab.ColIndex(x.Name)
		if !ok {
			return types.Value{}, nerr.New(nerr.NotFound, "executor.eval", "unknown column")
		}
		return row[i], nil
	case ast.Window:
		return types.Value{}, nerr.New(nerr.Internal, "executor.eval", "window function must be planned as a Window operator")
	case ast.Call:
		switch x.Name {
		case "uuid":
			return types.NewUUID()
		case "now":
			return types.Now(), nil
		case "ai":
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "AI() is only valid as a column default")
		case "coalesce":
			for _, arg := range x.Args {
				v, err := s.eval(arg, tab, row)
				if err != nil {
					return types.Value{}, err
				}
				if !v.Null {
					return v, nil
				}
			}
			return types.Null(types.NullType()), nil
		default:
			args := make([]types.Value, len(x.Args))
			for i, a := range x.Args {
				v, err := s.eval(a, tab, row)
				if err != nil {
					return types.Value{}, err
				}
				args[i] = v
			}
			if v, ok, err := evalStringFn(x.Name, args); err != nil || ok {
				return v, err
			}
			if v, ok, err := evalValueFn(x.Name, args); err != nil || ok {
				return v, err
			}
			if v, ok, err := evalNumericFn(x.Name, args); err != nil || ok {
				return v, err
			}
			if v, ok, err := evalJSONFn(x.Name, args); err != nil || ok {
				return v, err
			}
			if v, ok, err := evalDateFn(x.Name, args); err != nil || ok {
				return v, err
			}
			v, ok, err := types.EvalGeo(x.Name, args)
			if err != nil {
				return types.Value{}, err
			}
			if ok {
				return v, nil
			}
			if dv, ok, err := evalVecFn(x.Name, args); err != nil || ok {
				return dv, err
			}
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unknown function")
		}
	case ast.Case:
		return s.evalCase(x, tab, row)
	case ast.Unary:
		v, err := s.eval(x.Right, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		switch x.Op {
		case "-":
			if v.Null {
				return v, nil
			}
			if v.Typ.Kind != types.KindDecimal {
				return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unary minus requires DECIMAL")
			}
			v.Dec = v.Dec.Negate()
			return v, nil
		case "NOT":
			if v.Null {
				return types.Null(types.Bool()), nil
			}
			if v.Typ.Kind != types.KindBool {
				return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "NOT requires boolean")
			}
			return types.BoolValue(!v.Bool), nil
		default:
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unknown unary operator")
		}
	case ast.Binary:
		if x.Op == "AND" || x.Op == "OR" {
			return s.evalLogic(x, tab, row)
		}
		l, err := s.eval(x.Left, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		r, err := s.eval(x.Right, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		if x.Op == "+" || x.Op == "-" || x.Op == "*" || x.Op == "/" {
			return evalArith(x.Op, l, r)
		}
		if l.Null || r.Null {
			return types.Null(types.Bool()), nil
		}
		if l.Typ.Kind != r.Typ.Kind {
			r, err = types.Coerce(r, l.Typ)
			if err != nil {
				l2, err2 := types.Coerce(l, r.Typ)
				if err2 != nil {
					return types.Value{}, err
				}
				l = l2
			}
		}
		cmp, err := l.Cmp(r)
		if err != nil {
			return types.Value{}, err
		}
		switch x.Op {
		case "=":
			return types.BoolValue(cmp == 0), nil
		case "<>":
			return types.BoolValue(cmp != 0), nil
		case "<":
			return types.BoolValue(cmp < 0), nil
		case ">":
			return types.BoolValue(cmp > 0), nil
		case "<=":
			return types.BoolValue(cmp <= 0), nil
		case ">=":
			return types.BoolValue(cmp >= 0), nil
		default:
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unknown operator")
		}
	case ast.Between:
		v, err := s.eval(x.Expr, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		lo, err := s.eval(x.Low, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		hi, err := s.eval(x.High, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		if v.Null || lo.Null || hi.Null {
			return types.Null(types.Bool()), nil
		}
		if lo.Typ.Kind != v.Typ.Kind {
			if c, err := types.Coerce(lo, v.Typ); err == nil {
				lo = c
			}
		}
		if hi.Typ.Kind != v.Typ.Kind {
			if c, err := types.Coerce(hi, v.Typ); err == nil {
				hi = c
			}
		}
		c1, err := v.Cmp(lo)
		if err != nil {
			return types.Value{}, err
		}
		c2, err := v.Cmp(hi)
		if err != nil {
			return types.Value{}, err
		}
		in := c1 >= 0 && c2 <= 0
		if x.Not {
			in = !in
		}
		return types.BoolValue(in), nil
	case ast.IsNull:
		v, err := s.eval(x.Expr, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		is := v.Null
		if x.Not {
			is = !is
		}
		return types.BoolValue(is), nil
	case ast.Param:
		return s.lookupParam(x.Name)
	case ast.Path:
		if len(x.Parts) < 2 {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "invalid path")
		}
		i, ok := tab.ColIndex(x.Parts[0])
		if !ok {
			return types.Value{}, nerr.New(nerr.NotFound, "executor.eval", "unknown column")
		}
		v := row[i]
		if v.Null {
			return types.Null(types.JSON()), nil
		}
		if v.Typ.Kind != types.KindJSON {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "path extract requires a JSON column")
		}
		return types.ExtractJSON(v.JSON, x.Parts[1:])
	default:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unsupported expression")
	}
}

func (s *Session) evalScalarSubquery(x ast.ScalarSubquery, tab *catalog.Table, row []types.Value) (types.Value, error) {
	result, err := s.evalSubquery(x.ID, x.Query, tab, row)
	if err != nil {
		return types.Value{}, err
	}
	if len(result.Rows) == 0 {
		return types.Null(types.NullType()), nil
	}
	if len(result.Rows) > 1 {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.scalarSubquery", "scalar subquery returned more than one row")
	}
	return result.Rows[0][0], nil
}

func (s *Session) evalSubquery(id uint64, query ast.Stmt, outer *catalog.Table, row []types.Value) (*Result, error) {
	result, err := s.evalSubqueryAnyColumns(id, query, outer, row)
	if err != nil {
		return nil, err
	}
	if len(result.Columns) != 1 {
		return nil, nerr.New(nerr.InvalidArgument, "executor.subquery", "subquery must return exactly one column")
	}
	return result, nil
}

func (s *Session) evalSubqueryAnyColumns(id uint64, query ast.Stmt, outer *catalog.Table, row []types.Value) (*Result, error) {
	query, correlated := s.correlateSubquery(query, outer, row)
	if !correlated {
		if result := s.subqueryResults[id]; result != nil {
			return result, nil
		}
	}
	stmt, err := s.applyTenant(query)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(stmt); err != nil {
		return nil, err
	}
	bound, err := binder.Bind(stmt, s.lookup, s.db.Cat.PeekNext())
	if err != nil {
		return nil, err
	}
	plan, err := planner.Plan(bound)
	if err != nil {
		return nil, err
	}
	out, err := optimizer.Optimize(optimizer.Request{Plan: plan, Stats: s.lookupStats})
	if err != nil {
		return nil, err
	}
	result, err := s.execPlan(out.Plan)
	if err != nil {
		return nil, err
	}
	if !correlated {
		s.subqueryResults[id] = result
	}
	return result, nil
}

func (s *Session) evalInSubquery(x ast.InSubquery, tab *catalog.Table, row []types.Value) (types.Value, error) {
	left, err := s.eval(x.Expr, tab, row)
	if err != nil {
		return types.Value{}, err
	}
	result, err := s.evalSubquery(x.ID, x.Query, tab, row)
	if err != nil {
		return types.Value{}, err
	}
	unknown := false
	for _, candidate := range result.Rows {
		right := candidate[0]
		if left.Null || right.Null {
			unknown = true
			continue
		}
		cmpRight := right
		if left.Typ.Kind != right.Typ.Kind {
			cmpRight, err = types.Coerce(right, left.Typ)
			if err != nil {
				return types.Value{}, nerr.Wrap(nerr.InvalidArgument, "executor.inSubquery", "IN subquery types are incompatible", err)
			}
		}
		cmp, err := left.Cmp(cmpRight)
		if err != nil {
			return types.Value{}, err
		}
		if cmp == 0 {
			return types.BoolValue(!x.Not), nil
		}
	}
	if unknown {
		return types.Null(types.Bool()), nil
	}
	return types.BoolValue(x.Not), nil
}

func evalDateFn(name string, args []types.Value) (types.Value, bool, error) {
	switch name {
	case "extract", "date_trunc", "date_add", "date_diff":
	default:
		return types.Value{}, false, nil
	}
	for _, v := range args {
		if v.Null {
			if name == "date_trunc" || name == "date_add" {
				return types.Null(types.TimestampTZ()), true, nil
			}
			return types.Null(types.Type{Kind: types.KindDecimal}), true, nil
		}
	}
	unitArg := 0
	if name == "date_add" || name == "date_diff" {
		unitArg = 2
	}
	if args[unitArg].Typ.Kind != types.KindString && args[unitArg].Typ.Kind != types.KindText {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", name+" unit requires STRING")
	}
	unit := strings.ToLower(strings.TrimSpace(args[unitArg].Str))
	if !validDateUnit(unit) {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "unsupported date/time unit")
	}
	switch name {
	case "extract":
		if args[1].Typ.Kind != types.KindTimestampTZ {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "extract requires TIMESTAMPTZ")
		}
		t := time.Unix(0, args[1].Time).UTC()
		var n int64
		switch unit {
		case "year":
			n = int64(t.Year())
		case "month":
			n = int64(t.Month())
		case "day":
			n = int64(t.Day())
		case "hour":
			n = int64(t.Hour())
		case "minute":
			n = int64(t.Minute())
		case "second":
			n = int64(t.Second())
		}
		return decimalIntValue(n), true, nil
	case "date_trunc":
		if args[1].Typ.Kind != types.KindTimestampTZ {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "date_trunc requires TIMESTAMPTZ")
		}
		t := truncateUTC(time.Unix(0, args[1].Time).UTC(), unit)
		return types.TimeValue(t.UnixNano()), true, nil
	case "date_add":
		if args[0].Typ.Kind != types.KindTimestampTZ {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "date_add first argument requires TIMESTAMPTZ")
		}
		n, err := decimalIndex(args[1], "date_add amount")
		if err != nil {
			return types.Value{}, true, err
		}
		t := time.Unix(0, args[0].Time).UTC()
		switch unit {
		case "year":
			t = t.AddDate(n, 0, 0)
		case "month":
			t = t.AddDate(0, n, 0)
		case "day":
			t = t.AddDate(0, 0, n)
		case "hour":
			t = t.Add(time.Duration(n) * time.Hour)
		case "minute":
			t = t.Add(time.Duration(n) * time.Minute)
		case "second":
			t = t.Add(time.Duration(n) * time.Second)
		}
		return types.TimeValue(t.UnixNano()), true, nil
	case "date_diff":
		if args[0].Typ.Kind != types.KindTimestampTZ || args[1].Typ.Kind != types.KindTimestampTZ {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "date_diff requires two TIMESTAMPTZ values")
		}
		start, end := time.Unix(0, args[0].Time).UTC(), time.Unix(0, args[1].Time).UTC()
		var n int64
		switch unit {
		case "year":
			n = int64(end.Year() - start.Year())
		case "month":
			n = int64((end.Year()-start.Year())*12 + int(end.Month()-start.Month()))
		case "day":
			n = int64(end.Sub(start) / (24 * time.Hour))
		case "hour":
			n = int64(end.Sub(start) / time.Hour)
		case "minute":
			n = int64(end.Sub(start) / time.Minute)
		case "second":
			n = int64(end.Sub(start) / time.Second)
		}
		return decimalIntValue(n), true, nil
	}
	return types.Value{}, true, nerr.New(nerr.Internal, "executor.eval", "unhandled date function")
}

func validDateUnit(unit string) bool {
	switch unit {
	case "year", "month", "day", "hour", "minute", "second":
		return true
	default:
		return false
	}
}

func truncateUTC(t time.Time, unit string) time.Time {
	y, m, d := t.Date()
	h, min, sec := t.Clock()
	switch unit {
	case "year":
		m, d, h, min, sec = time.January, 1, 0, 0, 0
	case "month":
		d, h, min, sec = 1, 0, 0, 0
	case "day":
		h, min, sec = 0, 0, 0
	case "hour":
		min, sec = 0, 0
	case "minute":
		sec = 0
	}
	return time.Date(y, m, d, h, min, sec, 0, time.UTC)
}

func decimalIntValue(n int64) types.Value {
	return types.DecimalValue(types.DecimalFromInt64(n), types.Type{Kind: types.KindDecimal})
}

func evalJSONFn(name string, args []types.Value) (types.Value, bool, error) {
	switch name {
	case "json_get", "json_array_length", "json_type", "json_set", "json_remove", "json_contains":
	default:
		return types.Value{}, false, nil
	}
	if len(args) == 0 || args[0].Null {
		return types.Null(types.NullType()), true, nil
	}
	if args[0].Typ.Kind != types.KindJSON {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", name+" requires JSON")
	}
	if name == "json_contains" {
		if len(args) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "json_contains takes two arguments")
		}
		target, err := jsonArgumentDoc(args[1], true)
		if err != nil {
			return types.Value{}, true, err
		}
		ok, err := nsjson.Contains(args[0].JSON, target)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.BoolValue(ok), true, nil
	}
	var path []string
	if len(args) > 1 {
		if args[1].Null {
			return types.Null(types.NullType()), true, nil
		}
		if args[1].Typ.Kind != types.KindString && args[1].Typ.Kind != types.KindText {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", name+" path requires STRING")
		}
		path = jsonPath(args[1].Str)
	}
	switch name {
	case "json_set":
		if len(args) != 3 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "json_set takes three arguments")
		}
		replacement, err := jsonArgumentDoc(args[2], false)
		if err != nil {
			return types.Value{}, true, err
		}
		doc, err := nsjson.Set(args[0].JSON, path, replacement)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.JSONValue(doc), true, nil
	case "json_remove":
		if len(args) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "json_remove takes two arguments")
		}
		doc, err := nsjson.Remove(args[0].JSON, path)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.JSONValue(doc), true, nil
	case "json_get":
		if len(args) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "json_get takes two arguments")
		}
		v, err := types.ExtractJSON(args[0].JSON, path)
		return v, true, err
	case "json_array_length":
		n, found, err := nsjson.ArrayLength(args[0].JSON, path)
		if err != nil {
			return types.Value{}, true, err
		}
		if !found {
			return types.Null(types.Type{Kind: types.KindDecimal}), true, nil
		}
		return types.DecimalValue(types.DecimalFromInt64(int64(n)), types.Type{Kind: types.KindDecimal}), true, nil
	case "json_type":
		r, err := nsjson.Extract(args[0].JSON, path)
		if err != nil {
			return types.Value{}, true, err
		}
		var typ string
		switch r.Kind {
		case nsjson.KindMissing:
			return types.Null(types.String()), true, nil
		case nsjson.KindNull:
			typ = "null"
		case nsjson.KindBool:
			typ = "boolean"
		case nsjson.KindInt, nsjson.KindNumber:
			typ = "number"
		case nsjson.KindString:
			typ = "string"
		case nsjson.KindArray:
			typ = "array"
		case nsjson.KindObject:
			typ = "object"
		}
		return types.StringValue(typ), true, nil
	}
	return types.Value{}, true, nerr.New(nerr.Internal, "executor.eval", "unhandled JSON function")
}

func jsonArgumentDoc(v types.Value, parseString bool) ([]byte, error) {
	if v.Null {
		return nsjson.FromText([]byte("null"))
	}
	if v.Typ.Kind == types.KindJSON {
		return append([]byte(nil), v.JSON...), nil
	}
	var text []byte
	switch v.Typ.Kind {
	case types.KindString, types.KindText:
		if parseString {
			text = []byte(v.Str)
		} else {
			text, _ = stdjson.Marshal(v.Str)
		}
	case types.KindDecimal:
		text = []byte(v.Dec.String())
	case types.KindBool:
		text = []byte(strconv.FormatBool(v.Bool))
	default:
		return nil, nerr.New(nerr.InvalidArgument, "executor.eval", "value cannot be represented as JSON")
	}
	return nsjson.FromText(text)
}

func jsonPath(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return nil
	}
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return nil
	}
	return strings.Split(path, ".")
}

func evalNumericFn(name string, args []types.Value) (types.Value, bool, error) {
	switch name {
	case "abs", "round", "ceil", "floor", "mod", "power", "sqrt":
	default:
		return types.Value{}, false, nil
	}
	for _, v := range args {
		if v.Null {
			return types.Null(types.Type{Kind: types.KindDecimal}), true, nil
		}
		if v.Typ.Kind != types.KindDecimal {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", name+" requires DECIMAL")
		}
	}
	switch name {
	case "abs":
		if len(args) != 1 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "abs takes one argument")
		}
		v := args[0]
		if v.Dec.Coef != nil && v.Dec.Coef.Sign() < 0 {
			v.Dec.Coef = new(big.Int).Abs(v.Dec.Coef)
		}
		return v, true, nil
	case "ceil", "floor":
		if len(args) != 1 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", name+" takes one argument")
		}
		d, err := decimalFloorCeil(args[0].Dec, name == "ceil")
		return types.DecimalValue(d, types.Type{Kind: types.KindDecimal}), true, err
	case "round":
		if len(args) != 1 && len(args) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "round takes one or two arguments")
		}
		scale := 0
		if len(args) == 2 {
			var err error
			scale, err = decimalIndex(args[1], "round scale")
			if err != nil || scale < 0 {
				if err == nil {
					err = nerr.New(nerr.InvalidArgument, "executor.eval", "round scale must not be negative")
				}
				return types.Value{}, true, err
			}
		}
		d := decimalRound(args[0].Dec, scale)
		return types.DecimalValue(d, types.Type{Kind: types.KindDecimal, Scale: uint16(scale)}), true, nil
	case "mod":
		if len(args) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "mod takes two arguments")
		}
		if args[1].Dec.IsZero() {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "modulo by zero")
		}
		d := decimalMod(args[0].Dec, args[1].Dec)
		return types.DecimalValue(d, types.Type{Kind: types.KindDecimal, Scale: uint16(d.Scale)}), true, nil
	case "sqrt":
		if len(args) != 1 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "sqrt takes one argument")
		}
		if args[0].Dec.Coef != nil && args[0].Dec.Coef.Sign() < 0 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "sqrt requires a non-negative value")
		}
		x, err := strconv.ParseFloat(args[0].Dec.String(), 64)
		if err != nil {
			return types.Value{}, true, nerr.Wrap(nerr.InvalidArgument, "executor.eval", "sqrt input", err)
		}
		v, err := types.DecimalFromFloat(math.Sqrt(x))
		return v, true, err
	case "power":
		if len(args) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "power takes two arguments")
		}
		base, err := strconv.ParseFloat(args[0].Dec.String(), 64)
		if err != nil {
			return types.Value{}, true, nerr.Wrap(nerr.InvalidArgument, "executor.eval", "power base", err)
		}
		exponent, err := strconv.ParseFloat(args[1].Dec.String(), 64)
		if err != nil {
			return types.Value{}, true, nerr.Wrap(nerr.InvalidArgument, "executor.eval", "power exponent", err)
		}
		result := math.Pow(base, exponent)
		if math.IsNaN(result) || math.IsInf(result, 0) {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "power result is not finite")
		}
		v, err := types.DecimalFromFloat(result)
		return v, true, err
	}
	return types.Value{}, true, nerr.New(nerr.Internal, "executor.eval", "unhandled numeric function")
}

func decimalPow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func decimalFloorCeil(d types.Decimal, ceil bool) (types.Decimal, error) {
	if d.Scale == 0 {
		return d.Clone(), nil
	}
	coef := new(big.Int)
	if d.Coef != nil {
		coef.Set(d.Coef)
	}
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(coef, decimalPow10(d.Scale), rem)
	if rem.Sign() != 0 && (ceil && coef.Sign() > 0 || !ceil && coef.Sign() < 0) {
		if ceil {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	return types.Decimal{Coef: q, Scale: 0}, nil
}

func decimalRound(d types.Decimal, scale int) types.Decimal {
	if d.Scale <= scale {
		out := d.Clone()
		if out.Coef == nil {
			out.Coef = new(big.Int)
		}
		out.Coef.Mul(out.Coef, decimalPow10(scale-out.Scale))
		out.Scale = scale
		return out
	}
	coef := new(big.Int)
	if d.Coef != nil {
		coef.Set(d.Coef)
	}
	div := decimalPow10(d.Scale - scale)
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(coef, div, rem)
	twice := new(big.Int).Lsh(new(big.Int).Abs(rem), 1)
	if twice.Cmp(div) >= 0 {
		if coef.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return types.Decimal{Coef: q, Scale: scale}
}

func decimalMod(a, b types.Decimal) types.Decimal {
	scale := a.Scale
	if b.Scale > scale {
		scale = b.Scale
	}
	ac, bc := new(big.Int), new(big.Int)
	if a.Coef != nil {
		ac.Set(a.Coef)
	}
	if b.Coef != nil {
		bc.Set(b.Coef)
	}
	ac.Mul(ac, decimalPow10(scale-a.Scale))
	bc.Mul(bc, decimalPow10(scale-b.Scale))
	return types.Decimal{Coef: new(big.Int).Rem(ac, bc), Scale: scale}
}

func evalValueFn(name string, args []types.Value) (types.Value, bool, error) {
	switch name {
	case "nullif":
		if len(args) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "nullif takes two arguments")
		}
		left, right := args[0], args[1]
		if left.Null || right.Null {
			return left, true, nil
		}
		if left.Typ.Kind != right.Typ.Kind {
			var err error
			right, err = types.Coerce(right, left.Typ)
			if err != nil {
				return types.Value{}, true, err
			}
		}
		cmp, err := left.Cmp(right)
		if err != nil {
			return types.Value{}, true, err
		}
		if cmp == 0 {
			return types.Null(left.Typ), true, nil
		}
		return left, true, nil
	case "greatest", "least":
		if len(args) == 0 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", name+" requires arguments")
		}
		best := args[0]
		if best.Null {
			return types.Null(best.Typ), true, nil
		}
		for _, candidate := range args[1:] {
			if candidate.Null {
				return types.Null(best.Typ), true, nil
			}
			if candidate.Typ.Kind != best.Typ.Kind {
				var err error
				candidate, err = types.Coerce(candidate, best.Typ)
				if err != nil {
					return types.Value{}, true, err
				}
			}
			cmp, err := candidate.Cmp(best)
			if err != nil {
				return types.Value{}, true, err
			}
			if name == "greatest" && cmp > 0 || name == "least" && cmp < 0 {
				best = candidate
			}
		}
		return best, true, nil
	default:
		return types.Value{}, false, nil
	}
}

func evalStringFn(name string, args []types.Value) (types.Value, bool, error) {
	switch name {
	case "lower", "upper", "length", "substring", "trim", "ltrim", "rtrim", "replace", "concat", "starts_with", "ends_with", "contains":
	default:
		return types.Value{}, false, nil
	}
	for _, v := range args {
		if v.Null {
			if name == "length" {
				return types.Null(types.Type{Kind: types.KindDecimal}), true, nil
			}
			if name == "starts_with" || name == "ends_with" || name == "contains" {
				return types.Null(types.Bool()), true, nil
			}
			return types.Null(types.String()), true, nil
		}
	}
	requireString := func(v types.Value) error {
		if v.Typ.Kind != types.KindString && v.Typ.Kind != types.KindText {
			return nerr.New(nerr.InvalidArgument, "executor.eval", name+" requires STRING or TEXT")
		}
		return nil
	}
	if len(args) == 0 {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", name+" requires arguments")
	}
	if err := requireString(args[0]); err != nil {
		return types.Value{}, true, err
	}
	v := args[0]
	if v.Null {
		if name == "length" {
			return types.Null(types.Type{Kind: types.KindDecimal}), true, nil
		}
		return types.Null(v.Typ), true, nil
	}
	switch name {
	case "lower":
		v.Str = strings.ToLower(v.Str)
		return v, true, nil
	case "upper":
		v.Str = strings.ToUpper(v.Str)
		return v, true, nil
	case "length":
		d, err := types.ParseDecimal(strconv.Itoa(utf8.RuneCountInString(v.Str)))
		if err != nil {
			return types.Value{}, true, err
		}
		return types.DecimalValue(d, types.Type{Kind: types.KindDecimal}), true, nil
	case "trim", "ltrim", "rtrim":
		switch name {
		case "trim":
			v.Str = strings.TrimSpace(v.Str)
		case "ltrim":
			v.Str = strings.TrimLeftFunc(v.Str, unicode.IsSpace)
		case "rtrim":
			v.Str = strings.TrimRightFunc(v.Str, unicode.IsSpace)
		}
		return v, true, nil
	case "substring":
		if len(args) != 2 && len(args) != 3 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "substring takes two or three arguments")
		}
		start, err := decimalIndex(args[1], "substring start")
		if err != nil || start < 1 {
			if err == nil {
				err = nerr.New(nerr.InvalidArgument, "executor.eval", "substring start must be at least 1")
			}
			return types.Value{}, true, err
		}
		runes := []rune(v.Str)
		begin := start - 1
		if begin > len(runes) {
			begin = len(runes)
		}
		end := len(runes)
		if len(args) == 3 {
			n, err := decimalIndex(args[2], "substring length")
			if err != nil || n < 0 {
				if err == nil {
					err = nerr.New(nerr.InvalidArgument, "executor.eval", "substring length must not be negative")
				}
				return types.Value{}, true, err
			}
			if begin+n < end {
				end = begin + n
			}
		}
		v.Str = string(runes[begin:end])
		return v, true, nil
	case "concat":
		var b strings.Builder
		kind := types.KindString
		for _, arg := range args {
			if err := requireString(arg); err != nil {
				return types.Value{}, true, err
			}
			if arg.Typ.Kind == types.KindText {
				kind = types.KindText
			}
			b.WriteString(arg.Str)
		}
		if kind == types.KindText {
			return types.TextValue(b.String()), true, nil
		}
		return types.StringValue(b.String()), true, nil
	case "replace":
		if len(args) != 3 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", "replace takes three arguments")
		}
		if err := requireString(args[1]); err != nil {
			return types.Value{}, true, err
		}
		if err := requireString(args[2]); err != nil {
			return types.Value{}, true, err
		}
		v.Str = strings.ReplaceAll(v.Str, args[1].Str, args[2].Str)
		return v, true, nil
	case "starts_with", "ends_with", "contains":
		if len(args) != 2 {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.eval", name+" takes two arguments")
		}
		if err := requireString(args[1]); err != nil {
			return types.Value{}, true, err
		}
		var matched bool
		switch name {
		case "starts_with":
			matched = strings.HasPrefix(v.Str, args[1].Str)
		case "ends_with":
			matched = strings.HasSuffix(v.Str, args[1].Str)
		case "contains":
			matched = strings.Contains(v.Str, args[1].Str)
		}
		return types.BoolValue(matched), true, nil
	}
	return types.Value{}, true, nerr.New(nerr.Internal, "executor.eval", "unhandled string function")
}

func decimalIndex(v types.Value, what string) (int, error) {
	if v.Typ.Kind != types.KindDecimal || v.Dec.Scale != 0 || v.Dec.Coef == nil || !v.Dec.Coef.IsInt64() {
		return 0, nerr.New(nerr.InvalidArgument, "executor.eval", what+" must be an integer DECIMAL")
	}
	n := v.Dec.Coef.Int64()
	if int64(int(n)) != n {
		return 0, nerr.New(nerr.InvalidArgument, "executor.eval", what+" is out of range")
	}
	return int(n), nil
}

func (s *Session) evalCase(x ast.Case, tab *catalog.Table, row []types.Value) (types.Value, error) {
	var operand types.Value
	var err error
	if x.Operand != nil {
		operand, err = s.eval(x.Operand, tab, row)
		if err != nil {
			return types.Value{}, err
		}
	}
	for _, arm := range x.Whens {
		when, err := s.eval(arm.When, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		matched := false
		if x.Operand == nil {
			matched = !when.Null && when.Typ.Kind == types.KindBool && when.Bool
			if !when.Null && when.Typ.Kind != types.KindBool {
				return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "CASE WHEN requires boolean")
			}
		} else if !operand.Null && !when.Null {
			other := when
			if operand.Typ.Kind != other.Typ.Kind {
				other, err = types.Coerce(other, operand.Typ)
				if err != nil {
					return types.Value{}, err
				}
			}
			cmp, err := operand.Cmp(other)
			if err != nil {
				return types.Value{}, err
			}
			matched = cmp == 0
		}
		if matched {
			return s.eval(arm.Then, tab, row)
		}
	}
	if x.Else == nil {
		return types.Null(types.NullType()), nil
	}
	return s.eval(x.Else, tab, row)
}

func evalVecFn(name string, args []types.Value) (types.Value, bool, error) {
	var metric nsvec.Metric
	switch name {
	case "cosine":
		metric = nsvec.MetricCosine
	case "l2":
		metric = nsvec.MetricL2
	case "inner_product":
		metric = nsvec.MetricIP
	default:
		return types.Value{}, false, nil
	}
	if len(args) != 2 {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalVecFn", name+" takes two arguments")
	}
	if args[0].Null || args[1].Null {
		return types.Null(types.Type{Kind: types.KindDecimal}), true, nil
	}
	if args[0].Typ.Kind != types.KindVector || args[1].Typ.Kind != types.KindVector {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalVecFn", name+" requires VECTOR arguments")
	}
	if err := nsvec.Check(args[0].Vec, 0); err != nil {
		return types.Value{}, true, err
	}
	if err := nsvec.Check(args[1].Vec, len(args[0].Vec)); err != nil {
		return types.Value{}, true, err
	}
	d, err := types.DecimalFromFloat(nsvec.Similarity(metric, args[0].Vec, args[1].Vec))
	return d, true, err
}

func (s *Session) evalLogic(x ast.Binary, tab *catalog.Table, row []types.Value) (types.Value, error) {
	l, err := s.eval(x.Left, tab, row)
	if err != nil {
		return types.Value{}, err
	}
	if x.Op == "AND" && !l.Null && l.Typ.Kind == types.KindBool && !l.Bool {
		return types.BoolValue(false), nil
	}
	if x.Op == "OR" && !l.Null && l.Typ.Kind == types.KindBool && l.Bool {
		return types.BoolValue(true), nil
	}
	r, err := s.eval(x.Right, tab, row)
	if err != nil {
		return types.Value{}, err
	}
	if l.Null || r.Null {
		if x.Op == "AND" {
			if !l.Null && l.Typ.Kind == types.KindBool && !l.Bool {
				return types.BoolValue(false), nil
			}
			if !r.Null && r.Typ.Kind == types.KindBool && !r.Bool {
				return types.BoolValue(false), nil
			}
		}
		if x.Op == "OR" {
			if !l.Null && l.Typ.Kind == types.KindBool && l.Bool {
				return types.BoolValue(true), nil
			}
			if !r.Null && r.Typ.Kind == types.KindBool && r.Bool {
				return types.BoolValue(true), nil
			}
		}
		return types.Null(types.Bool()), nil
	}
	if l.Typ.Kind != types.KindBool || r.Typ.Kind != types.KindBool {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "AND/OR require boolean")
	}
	if x.Op == "AND" {
		return types.BoolValue(l.Bool && r.Bool), nil
	}
	return types.BoolValue(l.Bool || r.Bool), nil
}

func evalArith(op string, l, r types.Value) (types.Value, error) {
	if l.Null || r.Null {
		return types.Null(types.Type{Kind: types.KindDecimal}), nil
	}
	if l.Typ.Kind != types.KindDecimal || r.Typ.Kind != types.KindDecimal {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "arithmetic requires DECIMAL")
	}
	var (
		d   types.Decimal
		err error
	)
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
			return types.Value{}, err
		}
	}
	return types.DecimalValue(d, types.Type{Kind: types.KindDecimal, Scale: uint16(d.Scale)}), nil
}

func (s *Session) match(pred ast.Expr, tab *catalog.Table, row []types.Value) (bool, error) {
	if pred == nil {
		return true, nil
	}
	v, err := s.eval(pred, tab, row)
	if err != nil {
		return false, err
	}
	return !v.Null && v.Typ.Kind == types.KindBool && v.Bool, nil
}
