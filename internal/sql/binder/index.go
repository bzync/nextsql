package binder

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func bindCreateIndex(s ast.CreateIndex, lookup Lookup) (Bound, error) {
	tab, err := mustTable(lookup, s.Table)
	if err != nil {
		return nil, err
	}
	for _, idx := range tab.Indexes {
		if idx.Name == s.Name {
			return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "index already exists")
		}
	}
	idx := catalog.Index{Name: s.Name, Unique: s.Unique, Spatial: s.Spatial, Fulltext: s.Fulltext, Vector: s.Vector}
	keys := s.Keys
	if len(keys) == 0 {
		for _, name := range s.Cols {
			keys = append(keys, []string{name})
		}
	}
	if len(keys) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "index has no columns")
	}
	if len(s.Exprs) > 0 && len(s.Exprs) != len(keys) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "index expression count")
	}
	if s.Spatial && s.Unique {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SPATIAL INDEX cannot be UNIQUE")
	}
	if s.Fulltext && s.Unique {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "FULLTEXT INDEX cannot be UNIQUE")
	}
	if s.Fulltext && s.Spatial {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "index cannot be both SPATIAL and FULLTEXT")
	}
	if s.Vector && (s.Unique || s.Spatial || s.Fulltext) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR INDEX cannot be UNIQUE, SPATIAL, or FULLTEXT")
	}
	if s.Vector && s.Using != "hnsw" {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR INDEX requires USING HNSW")
	}
	special := s.Spatial || s.Fulltext || s.Vector
	if special && (len(s.Include) > 0 || s.Where != nil) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "INCLUDE and WHERE require a btree index")
	}
	if s.Spatial && len(keys) != 1 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SPATIAL INDEX requires one POINT column")
	}
	if s.Fulltext && len(keys) != 1 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "FULLTEXT INDEX requires one STRING or TEXT column")
	}
	if s.Vector && len(keys) != 1 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR INDEX requires one VECTOR column")
	}
	var pathKeys int
	hasExpr := false
	for i, parts := range keys {
		var expr ast.Expr
		if i < len(s.Exprs) {
			expr = s.Exprs[i]
		}
		if expr != nil {
			if special {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "expression keys require a btree index")
			}
			if err := checkIndexExpr(expr, tab); err != nil {
				return nil, err
			}
			ord, ok := firstIndexColumn(expr, tab)
			if !ok {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "index expression must reference a column")
			}
			if idx.Exprs == nil {
				idx.Exprs = make([]ast.Expr, len(keys))
				idx.ExprTypes = make([]types.Type, len(keys))
			}
			idx.Exprs[i] = expr
			typ, err := indexExprType(expr, tab)
			if err != nil {
				return nil, err
			}
			idx.ExprTypes[i] = typ
			idx.Columns = append(idx.Columns, ord)
			hasExpr = true
			continue
		}
		if len(parts) == 0 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "empty index key")
		}
		col, ok := tab.ColIndex(parts[0])
		if !ok {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown index column")
		}
		if s.Spatial && tab.Columns[col].Type.Kind != types.KindPoint {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SPATIAL INDEX requires a POINT column")
		}
		if s.Fulltext {
			k := tab.Columns[col].Type.Kind
			if k != types.KindString && k != types.KindText {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "FULLTEXT INDEX requires a STRING or TEXT column")
			}
		}
		if s.Vector && tab.Columns[col].Type.Kind != types.KindVector {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR INDEX requires a VECTOR column")
		}
		if !s.Vector && tab.Columns[col].Type.Kind == types.KindVector {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR column requires CREATE VECTOR INDEX")
		}
		if len(parts) > 1 {
			if s.Spatial {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SPATIAL INDEX cannot use a JSON path")
			}
			if s.Fulltext {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "FULLTEXT INDEX cannot use a JSON path")
			}
			if s.Vector {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR INDEX cannot use a JSON path")
			}
			if tab.Columns[col].Type.Kind != types.KindJSON {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "JSON path index requires a JSON column")
			}
			if len(idx.Path) > 0 {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "path index requires one key")
			}
			idx.Path = append([]string(nil), parts[1:]...)
			pathKeys++
		}
		idx.Columns = append(idx.Columns, col)
	}
	if pathKeys > 0 && len(keys) != 1 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "path index requires one key")
	}
	if pathKeys > 0 && hasExpr {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "JSON path index cannot mix expression keys")
	}
	if !hasExpr {
		idx.Exprs = nil
		idx.ExprTypes = nil
	}
	if err := bindIndexInclude(&idx, tab, s.Include); err != nil {
		return nil, err
	}
	if s.Where != nil {
		if err := checkIndexExpr(s.Where, tab); err != nil {
			return nil, err
		}
		if err := checkExpr(s.Where, tab, types.Bool(), false); err != nil {
			return nil, err
		}
		idx.Predicate = s.Where
	}
	return CreateIndex{Table: tab, Index: idx}, nil
}

func bindIndexInclude(idx *catalog.Index, tab *catalog.Table, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if len(names) > catalog.MaxIncludeColumns {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "too many INCLUDE columns")
	}
	seen := make(map[int]struct{}, len(names))
	keyCols := make(map[int]struct{}, len(idx.Columns))
	for i, ord := range idx.Columns {
		if idx.KeyIsExpr(i) {
			continue
		}
		if i == 0 && len(idx.Path) > 0 {
			continue
		}
		keyCols[ord] = struct{}{}
	}
	for _, name := range names {
		ord, ok := tab.ColIndex(name)
		if !ok {
			return nerr.New(nerr.NotFound, "sql.binder", "unknown INCLUDE column")
		}
		if _, dup := seen[ord]; dup {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate INCLUDE column")
		}
		if _, key := keyCols[ord]; key {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "INCLUDE column is already an index key")
		}
		switch tab.Columns[ord].Type.Kind {
		case types.KindVector:
			return nerr.New(nerr.InvalidArgument, "sql.binder", "INCLUDE cannot store a VECTOR column")
		}
		seen[ord] = struct{}{}
		idx.Include = append(idx.Include, ord)
	}
	return nil
}

func checkIndexExpr(e ast.Expr, tab *catalog.Table) error {
	if e == nil {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "missing index expression")
	}
	if catalog.ExprVolatile(e) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "index expression cannot be volatile")
	}
	if catalog.ExprHasSubquery(e) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "index expression cannot contain a subquery")
	}
	if containsWindow(e) || containsGroupingAgg(e) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "index expression cannot contain aggregates or windows")
	}
	if containsParam(e) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "index expression cannot contain parameters")
	}
	if containsIndexForbiddenCall(e) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "index expression cannot use a mutating or geo function")
	}
	return checkExpr(e, tab, types.Type{}, false)
}

func containsIndexForbiddenCall(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.Call:
		switch x.Name {
		case "json_set", "json_remove", "cosine", "l2", "inner_product":
			return true
		}
		if types.IsGeoFunc(x.Name) {
			return true
		}
		for _, a := range x.Args {
			if containsIndexForbiddenCall(a) {
				return true
			}
		}
	case ast.Unary:
		return containsIndexForbiddenCall(x.Right)
	case ast.Binary:
		return containsIndexForbiddenCall(x.Left) || containsIndexForbiddenCall(x.Right)
	case ast.Between:
		return containsIndexForbiddenCall(x.Expr) || containsIndexForbiddenCall(x.Low) || containsIndexForbiddenCall(x.High)
	case ast.IsNull:
		return containsIndexForbiddenCall(x.Expr)
	case ast.Case:
		if containsIndexForbiddenCall(x.Operand) || containsIndexForbiddenCall(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if containsIndexForbiddenCall(arm.When) || containsIndexForbiddenCall(arm.Then) {
				return true
			}
		}
	}
	return false
}

func containsParam(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.Param:
		return true
	case ast.Unary:
		return containsParam(x.Right)
	case ast.Binary:
		return containsParam(x.Left) || containsParam(x.Right)
	case ast.Between:
		return containsParam(x.Expr) || containsParam(x.Low) || containsParam(x.High)
	case ast.IsNull:
		return containsParam(x.Expr)
	case ast.Call:
		for _, a := range x.Args {
			if containsParam(a) {
				return true
			}
		}
	case ast.Case:
		if containsParam(x.Operand) || containsParam(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if containsParam(arm.When) || containsParam(arm.Then) {
				return true
			}
		}
	}
	return false
}

func firstIndexColumn(e ast.Expr, tab *catalog.Table) (int, bool) {
	if e == nil || tab == nil {
		return -1, false
	}
	switch x := e.(type) {
	case ast.Ident:
		return tab.ColIndex(x.Name)
	case ast.Path:
		if len(x.Parts) == 0 {
			return -1, false
		}
		return tab.ColIndex(x.Parts[0])
	case ast.Unary:
		return firstIndexColumn(x.Right, tab)
	case ast.Binary:
		if ord, ok := firstIndexColumn(x.Left, tab); ok {
			return ord, true
		}
		return firstIndexColumn(x.Right, tab)
	case ast.Between:
		if ord, ok := firstIndexColumn(x.Expr, tab); ok {
			return ord, true
		}
		if ord, ok := firstIndexColumn(x.Low, tab); ok {
			return ord, true
		}
		return firstIndexColumn(x.High, tab)
	case ast.IsNull:
		return firstIndexColumn(x.Expr, tab)
	case ast.Call:
		for _, a := range x.Args {
			if ord, ok := firstIndexColumn(a, tab); ok {
				return ord, true
			}
		}
	case ast.Case:
		if ord, ok := firstIndexColumn(x.Operand, tab); ok {
			return ord, true
		}
		for _, arm := range x.Whens {
			if ord, ok := firstIndexColumn(arm.When, tab); ok {
				return ord, true
			}
			if ord, ok := firstIndexColumn(arm.Then, tab); ok {
				return ord, true
			}
		}
		return firstIndexColumn(x.Else, tab)
	}
	return -1, false
}

func indexExprType(e ast.Expr, tab *catalog.Table) (types.Type, error) {
	switch x := e.(type) {
	case ast.Ident:
		i, ok := tab.ColIndex(x.Name)
		if !ok {
			return types.Type{}, nerr.New(nerr.NotFound, "sql.binder", "unknown column")
		}
		return tab.Columns[i].Type, nil
	case ast.Path:
		return types.JSON(), nil
	case ast.Literal:
		return x.Value.Typ, nil
	case ast.Call:
		switch x.Name {
		case "lower", "upper", "trim", "ltrim", "rtrim", "replace", "substring", "concat":
			out := types.String()
			for _, a := range x.Args {
				t, err := indexExprType(a, tab)
				if err != nil {
					return types.Type{}, err
				}
				if t.Kind == types.KindText {
					out = types.Text()
				}
			}
			return out, nil
		case "length", "abs", "ceil", "floor", "round", "mod", "sqrt", "power", "extract", "date_diff", "json_array_length":
			d, err := types.DecimalType(38, 8)
			if err != nil {
				return types.Type{}, err
			}
			return d, nil
		case "starts_with", "ends_with", "contains", "json_contains":
			return types.Bool(), nil
		case "coalesce", "nullif", "greatest", "least":
			if len(x.Args) == 0 {
				return types.Type{}, nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" requires an argument")
			}
			return indexExprType(x.Args[0], tab)
		case "date_trunc", "date_add":
			return types.TimestampTZ(), nil
		case "json_get", "json_set", "json_remove":
			return types.JSON(), nil
		case "json_type":
			return types.String(), nil
		default:
			if types.IsGeoFunc(x.Name) {
				return types.Type{}, nerr.New(nerr.InvalidArgument, "sql.binder", "geo functions cannot be indexed")
			}
			return types.Type{}, nerr.New(nerr.InvalidArgument, "sql.binder", "unsupported index function")
		}
	case ast.Unary:
		return indexExprType(x.Right, tab)
	case ast.Binary:
		switch x.Op {
		case "=", "<>", "!=", "<", "<=", ">", ">=", "AND", "OR":
			return types.Bool(), nil
		default:
			d, err := types.DecimalType(38, 8)
			if err != nil {
				return types.Type{}, err
			}
			return d, nil
		}
	case ast.Between, ast.IsNull:
		return types.Bool(), nil
	case ast.Case:
		for _, arm := range x.Whens {
			if t, err := indexExprType(arm.Then, tab); err == nil && t.Kind != 0 {
				return t, nil
			}
		}
		if x.Else != nil {
			return indexExprType(x.Else, tab)
		}
		return types.Type{}, nerr.New(nerr.InvalidArgument, "sql.binder", "CASE index expression has no type")
	default:
		return types.Type{}, nerr.New(nerr.InvalidArgument, "sql.binder", "unsupported index expression")
	}
}
