package executor

import (
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// cmpCoerced compares a against b, coercing b into a's type and retrying once
// if the direct comparison fails on a type mismatch (e.g. a MAP<DECIMAL,...>
// key looked up with an INT literal, or an ARRAY<DECIMAL> probed with an INT
// needle). ELEMENT_AT, ARRAY_CONTAINS and MAP_CONTAINS_KEY all resolve
// key/value equality through this one helper so they can't silently disagree
// on whether a key/value "matches". err is the original (pre-coercion)
// comparison error when neither the direct nor the coerced comparison
// succeeds — callers that want to treat a genuine type mismatch as "no match"
// rather than a hard error can simply ignore a non-nil err, same as before.
func cmpCoerced(a, b types.Value) (cmp int, err error) {
	cmp, err = a.Cmp(b)
	if err == nil {
		return cmp, nil
	}
	origErr := err
	cb, cerr := types.Coerce(b, a.Typ)
	if cerr != nil {
		return 0, origErr
	}
	cmp, err = a.Cmp(cb)
	if err != nil {
		return 0, origErr
	}
	return cmp, nil
}

// unifyElemType picks a single element/value type for an untyped collection
// constructor. Identical Kinds keep that Kind; a mix of exact-numeric or float
// Kinds widens to DECIMAL; a mix of string-family Kinds widens to TEXT;
// anything else is a type error. The constructed value is coerced again
// against the destination column on INSERT/UPDATE, so this only has to be
// good enough for a bare `SELECT ARRAY(...)`.
func unifyElemType(vals []types.Value) (types.Type, error) {
	var cand types.Type
	have := false
	for _, v := range vals {
		if v.Null {
			continue
		}
		if !have {
			cand, have = v.Typ, true
			continue
		}
		if cand.Equals(v.Typ) {
			continue
		}
		switch {
		case numericish(cand.Kind) && numericish(v.Typ.Kind):
			cand = types.Type{Kind: types.KindDecimal}
		case stringFamily(cand.Kind) && stringFamily(v.Typ.Kind):
			cand = types.Text()
		default:
			return types.Type{}, nerr.New(nerr.InvalidArgument, "executor.collection", "collection elements have incompatible types")
		}
	}
	if !have {
		return types.Type{}, nerr.New(nerr.InvalidArgument, "executor.collection", "cannot infer the element type of an all-NULL or empty collection; supply a typed column context")
	}
	return cand, nil
}

func numericish(k types.Kind) bool {
	return k == types.KindDecimal || types.IsInt(k) || types.IsUint(k) || types.IsFloat(k)
}

func stringFamily(k types.Kind) bool {
	return k == types.KindString || k == types.KindText || k == types.KindChar || k == types.KindVarchar
}

func (s *Session) evalArrayCtor(x ast.ArrayCtor, tab *catalog.Table, row []types.Value) (types.Value, error) {
	elems := make([]types.Value, len(x.Elems))
	for i, e := range x.Elems {
		v, err := s.eval(e, tab, row)
		if err != nil {
			return types.Value{}, err
		}
		elems[i] = v
	}
	et, err := unifyElemType(elems)
	if err != nil {
		return types.Value{}, err
	}
	at, err := types.ArrayType(et)
	if err != nil {
		return types.Value{}, err
	}
	return types.Coerce(types.ArrayValue(at, elems), at)
}

func (s *Session) evalStructCtor(x ast.StructCtor, tab *catalog.Table, row []types.Value) (types.Value, error) {
	if len(x.Names) != len(x.Elems) || len(x.Names) == 0 {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.collection", "malformed STRUCT constructor")
	}
	fields := make([]types.Field, len(x.Names))
	vals := make([]types.Value, len(x.Elems))
	for i := range x.Elems {
		v, err := s.eval(x.Elems[i], tab, row)
		if err != nil {
			return types.Value{}, err
		}
		ft := v.Typ
		if v.Null {
			// A bare NULL field defaults to a nullable TEXT slot; an
			// INSERT/UPDATE re-coerces against the real declared field type.
			ft = types.Text()
		}
		fields[i] = types.Field{Name: x.Names[i], Type: ft}
		vals[i] = v
	}
	st, err := types.StructType(fields)
	if err != nil {
		return types.Value{}, err
	}
	return types.Coerce(types.StructValue(st, vals), st)
}

func (s *Session) evalMapCtor(x ast.MapCtor, tab *catalog.Table, row []types.Value) (types.Value, error) {
	if len(x.Keys) != len(x.Vals) {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.collection", "malformed MAP constructor")
	}
	keys := make([]types.Value, len(x.Keys))
	vals := make([]types.Value, len(x.Vals))
	for i := range x.Keys {
		kv, err := s.eval(x.Keys[i], tab, row)
		if err != nil {
			return types.Value{}, err
		}
		if kv.Null {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.collection", "MAP key cannot be NULL")
		}
		vv, err := s.eval(x.Vals[i], tab, row)
		if err != nil {
			return types.Value{}, err
		}
		keys[i], vals[i] = kv, vv
	}
	kt, err := unifyElemType(keys)
	if err != nil {
		return types.Value{}, err
	}
	vt, err := unifyElemType(vals)
	if err != nil {
		return types.Value{}, err
	}
	mt, err := types.MapType(kt, vt)
	if err != nil {
		return types.Value{}, err
	}
	return types.Coerce(types.MapValue(mt, keys, vals), mt)
}

// structField reads a named field from a STRUCT value (NULL struct yields a
// NULL of the field's declared type).
func structField(v types.Value, name string) (types.Value, error) {
	if v.Typ.Kind != types.KindStruct {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.collection", "field access requires a STRUCT value")
	}
	idx := v.Typ.StructFieldIndex(name)
	if idx < 0 {
		return types.Value{}, nerr.New(nerr.NotFound, "executor.collection", "unknown STRUCT field "+name)
	}
	if v.Null {
		return types.Null(v.Typ.Fields[idx].Type), nil
	}
	if idx >= len(v.Coll) {
		return types.Value{}, nerr.New(nerr.Internal, "executor.collection", "STRUCT value is missing a field")
	}
	return v.Coll[idx], nil
}

// evalCollectionFn implements the collection accessor / helper functions.
func evalCollectionFn(name string, args []types.Value) (types.Value, bool, error) {
	switch strings.ToLower(name) {
	case "cardinality", "array_length", "map_size":
		if len(args) != 1 {
			return types.Value{}, true, arityErr(name)
		}
		a := args[0]
		if a.Null {
			return types.Null(types.Int64()), true, nil
		}
		switch a.Typ.Kind {
		case types.KindArray, types.KindStruct, types.KindMap:
			return types.IntValue(types.KindInt64, int64(len(a.Coll))), true, nil
		default:
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.collection", name+" requires a collection")
		}
	case "element_at":
		if len(args) != 2 {
			return types.Value{}, true, arityErr(name)
		}
		coll, key := args[0], args[1]
		if coll.Null {
			return types.Null(types.NullType()), true, nil
		}
		switch coll.Typ.Kind {
		case types.KindArray:
			if key.Null {
				return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.collection", "element_at index cannot be NULL")
			}
			idx, err := types.Coerce(key, types.Int64())
			if err != nil {
				return types.Value{}, true, err
			}
			// 1-based, like Spark/Postgres.
			n := idx.Int
			if n < 1 || n > int64(len(coll.Coll)) {
				return types.Null(elemType(coll.Typ)), true, nil
			}
			return coll.Coll[n-1], true, nil
		case types.KindMap:
			for i := range coll.CollKeys {
				c, err := cmpCoerced(coll.CollKeys[i], key)
				if err != nil {
					return types.Value{}, true, err
				}
				if c == 0 {
					return coll.Coll[i], true, nil
				}
			}
			return types.Null(elemType(coll.Typ)), true, nil
		default:
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.collection", "element_at requires an ARRAY or MAP")
		}
	case "array_contains":
		if len(args) != 2 {
			return types.Value{}, true, arityErr(name)
		}
		arr, want := args[0], args[1]
		if arr.Null {
			return types.Null(types.Bool()), true, nil
		}
		if arr.Typ.Kind != types.KindArray {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.collection", "array_contains requires an ARRAY")
		}
		for _, e := range arr.Coll {
			if e.Null || want.Null {
				if e.Null && want.Null {
					return types.BoolValue(true), true, nil
				}
				continue
			}
			if c, err := cmpCoerced(e, want); err == nil && c == 0 {
				return types.BoolValue(true), true, nil
			}
		}
		return types.BoolValue(false), true, nil
	case "map_contains_key":
		if len(args) != 2 {
			return types.Value{}, true, arityErr(name)
		}
		m, k := args[0], args[1]
		if m.Null {
			return types.Null(types.Bool()), true, nil
		}
		if m.Typ.Kind != types.KindMap {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.collection", "map_contains_key requires a MAP")
		}
		for i := range m.CollKeys {
			if c, err := cmpCoerced(m.CollKeys[i], k); err == nil && c == 0 {
				return types.BoolValue(true), true, nil
			}
		}
		return types.BoolValue(false), true, nil
	case "map_keys", "map_values":
		if len(args) != 1 {
			return types.Value{}, true, arityErr(name)
		}
		m := args[0]
		if m.Null {
			return types.Null(types.NullType()), true, nil
		}
		if m.Typ.Kind != types.KindMap {
			return types.Value{}, true, nerr.New(nerr.InvalidArgument, "executor.collection", name+" requires a MAP")
		}
		src := m.CollKeys
		var elemT types.Type
		if len(m.Typ.Key) == 1 {
			elemT = m.Typ.Key[0]
		}
		if strings.ToLower(name) == "map_values" {
			src = m.Coll
			if len(m.Typ.Elem) == 1 {
				elemT = m.Typ.Elem[0]
			}
		}
		at, err := types.ArrayType(elemT)
		if err != nil {
			return types.Value{}, true, err
		}
		return types.ArrayValue(at, src), true, nil
	default:
		return types.Value{}, false, nil
	}
}

func elemType(t types.Type) types.Type {
	if len(t.Elem) == 1 {
		return t.Elem[0]
	}
	return types.NullType()
}

func arityErr(name string) error {
	return nerr.New(nerr.InvalidArgument, "executor.collection", name+": wrong number of arguments")
}
