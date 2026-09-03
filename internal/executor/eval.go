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
		case "highlight", "snippet":
			return s.evalHighlight(x, tab, row)
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
			if v.Typ.Kind == types.KindInterval {
				if v.IntervalMonths == math.MinInt32 || v.IntervalDays == math.MinInt32 || v.Time == math.MinInt64 {
					return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL negation overflow")
				}
				return types.IntervalValue(-v.IntervalMonths, -v.IntervalDays, -v.Time), nil
			}
			if !isNumericKind(v.Typ.Kind) {
				return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unary minus requires a numeric type")
			}
			if types.IsFloat(v.Typ.Kind) {
				return types.FloatValue(v.Typ.Kind, -v.Flt), nil
			}
			if v.Typ.Kind != types.KindDecimal {
				c, err := types.Coerce(v, types.Type{Kind: types.KindDecimal})
				if err != nil {
					return types.Value{}, err
				}
				v = c
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
	stmt, err := s.guardLegacyTenancy(query)
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
	// CHAR/VARCHAR flow through the string builtins as plain STRING — CHAR's
	// trailing-space padding is not significant content
	// (docs/design-datatypes.md D4).
	for i, v := range args {
		switch v.Typ.Kind {
		case types.KindChar:
			args[i] = types.StringValue(strings.TrimRight(v.Str, " "))
		case types.KindVarchar:
			args[i] = types.StringValue(v.Str)
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
	canonical := name
	switch name {
	case "dot", "vector_dot":
		canonical = "inner_product"
	case "vector_dims", "dimensions":
		canonical = "vector_dim"
	case "norm":
		canonical = "vector_norm"
	case "normalize":
		canonical = "vector_normalize"
	case "vector_sub":
		canonical = "vector_subtract"
	case "manhattan":
		canonical = "l1"
	}
	switch canonical {
	case "cosine", "cosine_distance", "l2", "l1", "inner_product", "vector_dim", "vector_norm", "vector_normalize", "vector_add", "vector_subtract", "vector_scale":
	default:
		return types.Value{}, false, nil
	}
	want := 2
	if canonical == "vector_dim" || canonical == "vector_norm" || canonical == "vector_normalize" {
		want = 1
	}
	if len(args) != want {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalVecFn", name+" has invalid argument count")
	}
	vectorType := types.Type{Kind: types.KindVector, VecElem: types.VecF32}
	if len(args) > 0 && args[0].Typ.Kind == types.KindVector {
		vectorType = args[0].Typ
	}
	if args[0].Null || len(args) == 2 && args[1].Null {
		if canonical == "vector_normalize" || canonical == "vector_add" || canonical == "vector_subtract" || canonical == "vector_scale" {
			return types.Null(vectorType), true, nil
		}
		return types.Null(types.Type{Kind: types.KindDecimal}), true, nil
	}
	if args[0].Typ.Kind != types.KindVector {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalVecFn", name+" requires a VECTOR first argument")
	}
	if args[0].Typ.VecElem == types.VecSparse || len(args[0].SparseIdx) > 0 {
		return evalSparseVecFn(canonical, name, args, vectorType)
	}
	if err := nsvec.Check(args[0].Vec, 0); err != nil {
		return types.Value{}, true, err
	}
	switch canonical {
	case "vector_dim":
		return types.DecimalValue(types.DecimalFromInt64(int64(len(args[0].Vec))), types.Type{Kind: types.KindDecimal}), true, nil
	case "vector_norm":
		norm, err := nsvec.Norm(args[0].Vec)
		if err != nil {
			return types.Value{}, true, err
		}
		d, err := types.DecimalFromFloat(norm)
		return d, true, err
	case "vector_normalize":
		out, err := nsvec.Normalize(args[0].Vec)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.VectorValue(out, vectorType), true, nil
	case "vector_scale":
		if args[1].Typ.Kind != types.KindDecimal {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalVecFn", "VECTOR_SCALE requires a DECIMAL scale")
		}
		scalar, err := strconv.ParseFloat(args[1].Dec.String(), 64)
		if err != nil {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalVecFn", "invalid VECTOR_SCALE scale")
		}
		out, err := nsvec.Scale(args[0].Vec, scalar)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.VectorValue(out, vectorType), true, nil
	}
	if args[1].Typ.Kind != types.KindVector {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalVecFn", name+" requires VECTOR arguments")
	}
	if err := nsvec.Check(args[1].Vec, len(args[0].Vec)); err != nil {
		return types.Value{}, true, err
	}
	switch canonical {
	case "vector_add", "vector_subtract":
		var out []float32
		var err error
		if canonical == "vector_add" {
			out, err = nsvec.Add(args[0].Vec, args[1].Vec)
		} else {
			out, err = nsvec.Sub(args[0].Vec, args[1].Vec)
		}
		if err != nil {
			return types.Value{}, true, err
		}
		return types.VectorValue(out, vectorType), true, nil
	case "l1":
		value, err := nsvec.L1(args[0].Vec, args[1].Vec)
		if err != nil {
			return types.Value{}, true, err
		}
		d, err := types.DecimalFromFloat(value)
		return d, true, err
	case "cosine_distance":
		d, err := types.DecimalFromFloat(nsvec.Distance(nsvec.MetricCosine, args[0].Vec, args[1].Vec))
		return d, true, err
	default:
		metric := nsvec.MetricCosine
		if canonical == "l2" {
			metric = nsvec.MetricL2
		} else if canonical == "inner_product" {
			metric = nsvec.MetricIP
		}
		d, err := types.DecimalFromFloat(nsvec.Similarity(metric, args[0].Vec, args[1].Vec))
		return d, true, err
	}
}

func evalSparseVecFn(canonical, name string, args []types.Value, vectorType types.Type) (types.Value, bool, error) {
	dim := uint32(args[0].Typ.Precision)
	a, err := valueSparse(args[0], dim)
	if err != nil {
		return types.Value{}, true, err
	}
	switch canonical {
	case "vector_dim":
		d := int64(a.Dim)
		if d == 0 {
			d = int64(args[0].Typ.Precision)
		}
		return types.DecimalValue(types.DecimalFromInt64(d), types.Type{Kind: types.KindDecimal}), true, nil
	case "vector_norm":
		dec, err := types.DecimalFromFloat(nsvec.SparseNorm(a))
		return dec, true, err
	case "l1", "l2", "vector_add", "vector_subtract", "vector_normalize", "vector_scale":
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalSparseVecFn", name+" is not supported on SPARSEVECTOR")
	}
	if len(args) != 2 || args[1].Typ.Kind != types.KindVector {
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalSparseVecFn", name+" requires VECTOR arguments")
	}
	b, err := valueSparse(args[1], a.Dim)
	if err != nil {
		return types.Value{}, true, err
	}
	switch canonical {
	case "cosine_distance":
		d, err := types.DecimalFromFloat(nsvec.SparseDistance(nsvec.MetricCosine, a, b))
		return d, true, err
	case "cosine":
		d, err := types.DecimalFromFloat(nsvec.SparseSimilarity(nsvec.MetricCosine, a, b))
		return d, true, err
	case "inner_product":
		d, err := types.DecimalFromFloat(nsvec.SparseDot(a, b))
		return d, true, err
	default:
		return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.evalSparseVecFn", name+" is not supported on SPARSEVECTOR")
	}
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

// isNumericKind reports whether k participates in arithmetic (+ - * / and
// unary -): DECIMAL, or any fixed-width signed or unsigned integer kind
// (D2/D3, Datatype expansion track). Deliberately narrow — STRING/TEXT/BLOB/
// etc. stay rejected exactly as before D2/D3, so this only widens scope to
// the 8 int kinds.
func isNumericKind(k types.Kind) bool {
	return k == types.KindDecimal || types.IsInt(k) || types.IsUint(k) || types.IsFloat(k)
}

// isTemporalKind reports whether k is one of DATE/TIME/TIMESTAMP/TIMESTAMPTZ
// (D6, Datatype expansion track): the Kinds INTERVAL arithmetic can combine
// with, plus the "same Kind minus same Kind -> INTERVAL" rule.
func isTemporalKind(k types.Kind) bool {
	return k == types.KindDate || k == types.KindTime || k == types.KindTimestamp || k == types.KindTimestampTZ
}

const dayNanosConst = int64(86400_000_000_000)

// evalIntervalArith handles every +/- combination involving INTERVAL, or two
// operands of the same temporal Kind (D6, Datatype expansion track):
//
//	INTERVAL +/- INTERVAL        -> INTERVAL, field-wise, overflow-checked
//	<temporal> +/- INTERVAL      -> same <temporal> Kind, except DATE which
//	                                 always promotes to TIMESTAMP (a DATE has
//	                                 no time-of-day, so an interval carrying
//	                                 any time component doesn't fit back into
//	                                 DATE — matches Postgres's own rule)
//	INTERVAL + <temporal>        -> same as <temporal> + INTERVAL (commutative)
//	INTERVAL - <temporal>        -> rejected (subtracting a point in time
//	                                 from a duration is not meaningful)
//	<temporal> - <same temporal> -> INTERVAL: the exact elapsed duration,
//	                                 carried entirely in the nanosecond
//	                                 field (months/days always 0) — this
//	                                 increment does not attempt to break an
//	                                 arbitrary elapsed duration back into
//	                                 "N months, N days", which is inherently
//	                                 ambiguous without an anchor date
//
// Calendar-month addition clamps the day-of-month to the target month's
// last day (Jan 31 + 1 month = Feb 28/29), matching Postgres. TIME discards
// an interval's months/days components entirely (also matching Postgres)
// and wraps modulo 24h.
func evalIntervalArith(op string, l, r types.Value) (types.Value, error) {
	if op != "+" && op != "-" {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL only supports + and -")
	}
	lInterval := l.Typ.Kind == types.KindInterval
	rInterval := r.Typ.Kind == types.KindInterval
	switch {
	case lInterval && rInterval:
		return addIntervals(l, r, op)
	case lInterval && !rInterval:
		if op == "-" {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "cannot subtract a "+r.Typ.Kind.String()+" from an INTERVAL")
		}
		if !isTemporalKind(r.Typ.Kind) {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL arithmetic requires a DATE/TIME/TIMESTAMP/TIMESTAMPTZ operand")
		}
		return applyIntervalToTemporal(r, l, "+")
	case rInterval && !lInterval:
		if !isTemporalKind(l.Typ.Kind) {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL arithmetic requires a DATE/TIME/TIMESTAMP/TIMESTAMPTZ operand")
		}
		return applyIntervalToTemporal(l, r, op)
	default:
		if op != "-" {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "cannot add two "+l.Typ.Kind.String()+" values")
		}
		return subtractTemporal(l, r)
	}
}

func addIntervals(l, r types.Value, op string) (types.Value, error) {
	var months, days int64
	var nanos int64
	var err error
	if op == "+" {
		months = int64(l.IntervalMonths) + int64(r.IntervalMonths)
		days = int64(l.IntervalDays) + int64(r.IntervalDays)
		nanos, err = addInt64Checked(l.Time, r.Time)
	} else {
		months = int64(l.IntervalMonths) - int64(r.IntervalMonths)
		days = int64(l.IntervalDays) - int64(r.IntervalDays)
		nanos, err = subInt64Checked(l.Time, r.Time)
	}
	if err != nil {
		return types.Value{}, err
	}
	if months < math.MinInt32 || months > math.MaxInt32 {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL month component overflow")
	}
	if days < math.MinInt32 || days > math.MaxInt32 {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL day component overflow")
	}
	return types.IntervalValue(int32(months), int32(days), nanos), nil
}

// applyIntervalToTemporal computes temporal +/- interval. op == "-" negates
// the interval's 3 components first, then always adds.
func applyIntervalToTemporal(temporal, interval types.Value, op string) (types.Value, error) {
	months, days, nanos := interval.IntervalMonths, interval.IntervalDays, interval.Time
	if op == "-" {
		if months == math.MinInt32 || days == math.MinInt32 || nanos == math.MinInt64 {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL negation overflow")
		}
		months, days, nanos = -months, -days, -nanos
	}
	switch temporal.Typ.Kind {
	case types.KindTime:
		// months/days discarded (Postgres's own time+interval rule); wrap
		// modulo 24h, handling a negative result correctly (Go's % keeps
		// the dividend's sign).
		result := (temporal.Time + nanos) % dayNanosConst
		if result < 0 {
			result += dayNanosConst
		}
		return types.TimeOfDayValue(result), nil
	case types.KindDate:
		// DATE always promotes to TIMESTAMP (docs/design-datatypes.md D6).
		epochNanos, err := mulInt64Checked(int64(temporal.Int), dayNanosConst)
		if err != nil {
			return types.Value{}, err
		}
		result, err := addCalendar(epochNanos, months, days, nanos)
		if err != nil {
			return types.Value{}, err
		}
		return types.NaiveTimestampValue(result), nil
	case types.KindTimestamp:
		result, err := addCalendar(temporal.Time, months, days, nanos)
		if err != nil {
			return types.Value{}, err
		}
		return types.NaiveTimestampValue(result), nil
	case types.KindTimestampTZ:
		// Calendar (months/days) math operates on the UTC calendar fields
		// directly — this engine has no session-timezone concept, unlike
		// Postgres, whose TIMESTAMPTZ+INTERVAL uses the session's timezone
		// GUC for calendar-correct results (docs/design-datatypes.md D6).
		result, err := addCalendar(temporal.Time, months, days, nanos)
		if err != nil {
			return types.Value{}, err
		}
		return types.TimeValue(result), nil
	default:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL arithmetic requires a DATE/TIME/TIMESTAMP/TIMESTAMPTZ operand")
	}
}

// addCalendar adds months (with day-of-month clamped to the target month's
// last day), then days, then nanos, to an epoch-nanoseconds instant read as
// literal UTC civil fields — the same order Postgres applies.
func addCalendar(epochNanos int64, months, days int32, nanos int64) (int64, error) {
	if months != 0 {
		t := time.Unix(0, epochNanos).UTC()
		y, m, d := t.Date()
		hh, mm, ss := t.Clock()
		ns := t.Nanosecond()
		totalMonthIdx := int64(y)*12 + int64(m) - 1 + int64(months)
		newYear := totalMonthIdx / 12
		newMonthIdx := totalMonthIdx % 12
		if newMonthIdx < 0 {
			newMonthIdx += 12
			newYear--
		}
		newMonth := time.Month(newMonthIdx + 1)
		if lastDay := daysInMonth(int(newYear), newMonth); d > lastDay {
			d = lastDay
		}
		epochNanos = time.Date(int(newYear), newMonth, d, hh, mm, ss, ns, time.UTC).UnixNano()
	}
	dayPart, err := mulInt64Checked(int64(days), dayNanosConst)
	if err != nil {
		return 0, err
	}
	epochNanos, err = addInt64Checked(epochNanos, dayPart)
	if err != nil {
		return 0, err
	}
	return addInt64Checked(epochNanos, nanos)
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// subtractTemporal computes l - r for two operands of the same temporal
// Kind, as the exact elapsed duration expressed purely in INTERVAL's
// nanosecond field (months/days always 0 — see evalIntervalArith's doc).
func subtractTemporal(l, r types.Value) (types.Value, error) {
	switch l.Typ.Kind {
	case types.KindTime, types.KindTimestamp, types.KindTimestampTZ:
		nanos, err := subInt64Checked(l.Time, r.Time)
		if err != nil {
			return types.Value{}, err
		}
		return types.IntervalValue(0, 0, nanos), nil
	case types.KindDate:
		lNanos, err := mulInt64Checked(int64(l.Int), dayNanosConst)
		if err != nil {
			return types.Value{}, err
		}
		rNanos, err := mulInt64Checked(int64(r.Int), dayNanosConst)
		if err != nil {
			return types.Value{}, err
		}
		nanos, err := subInt64Checked(lNanos, rNanos)
		if err != nil {
			return types.Value{}, err
		}
		return types.IntervalValue(0, 0, nanos), nil
	default:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "cannot add two "+l.Typ.Kind.String()+" values")
	}
}

func addInt64Checked(a, b int64) (int64, error) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL arithmetic overflow")
	}
	return sum, nil
}

func subInt64Checked(a, b int64) (int64, error) {
	diff := a - b
	if (b < 0 && diff < a) || (b > 0 && diff > a) {
		return 0, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL arithmetic overflow")
	}
	return diff, nil
}

func mulInt64Checked(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	p := a * b
	if p/b != a {
		return 0, nerr.New(nerr.InvalidArgument, "executor.eval", "INTERVAL arithmetic overflow")
	}
	return p, nil
}

func evalArith(op string, l, r types.Value) (types.Value, error) {
	if l.Null || r.Null {
		return types.Null(types.Type{Kind: types.KindDecimal}), nil
	}
	// DATE/TIME/TIMESTAMP/TIMESTAMPTZ +/- INTERVAL, INTERVAL +/- INTERVAL,
	// and same-Kind-temporal subtraction (D6, Datatype expansion track) are
	// handled before the numeric-only gate below, since none of these Kinds
	// are numeric and Coerce keeps INTERVAL isolated from DECIMAL on purpose.
	if l.Typ.Kind == types.KindInterval || r.Typ.Kind == types.KindInterval || (isTemporalKind(l.Typ.Kind) && l.Typ.Kind == r.Typ.Kind) {
		return evalIntervalArith(op, l, r)
	}
	if !isNumericKind(l.Typ.Kind) || !isNumericKind(r.Typ.Kind) {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "arithmetic requires a numeric type")
	}
	// If either operand is an IEEE-754 float, the whole operation evaluates in
	// float64 and yields FLOAT64 — DECIMAL is exact and cannot represent an
	// arbitrary float result (docs/design-datatypes.md D8). Assigning the
	// result back into a FLOAT32 column re-rounds via Coerce.
	if types.IsFloat(l.Typ.Kind) || types.IsFloat(r.Typ.Kind) {
		return evalFloatArith(op, l, r)
	}
	// Binary arithmetic always evaluates in DECIMAL (arbitrary-precision)
	// space, even when an operand is a fixed-width int column — mirroring
	// the pre-D2 behavior where DECIMAL was the only arithmetic type, so the
	// operation itself can never overflow. An out-of-range result only
	// errors if the caller assigns/coerces it back into a fixed-width int
	// column (see docs/design-datatypes.md D2).
	if l.Typ.Kind != types.KindDecimal {
		c, err := types.Coerce(l, types.Type{Kind: types.KindDecimal})
		if err != nil {
			return types.Value{}, err
		}
		l = c
	}
	if r.Typ.Kind != types.KindDecimal {
		c, err := types.Coerce(r, types.Type{Kind: types.KindDecimal})
		if err != nil {
			return types.Value{}, err
		}
		r = c
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

// evalFloatArith computes op in float64 space. Non-float operands (int, uint,
// decimal, decimal text) are widened via Coerce to FLOAT64 first. Division by
// zero follows IEEE-754 (±Inf / NaN), not a SQL error.
func evalFloatArith(op string, l, r types.Value) (types.Value, error) {
	f64 := types.Type{Kind: types.KindFloat64}
	lc, err := types.Coerce(l, f64)
	if err != nil {
		return types.Value{}, err
	}
	rc, err := types.Coerce(r, f64)
	if err != nil {
		return types.Value{}, err
	}
	a, b := lc.Flt, rc.Flt
	var out float64
	switch op {
	case "+":
		out = a + b
	case "-":
		out = a - b
	case "*":
		out = a * b
	case "/":
		out = a / b
	}
	return types.Float64Value(out), nil
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
