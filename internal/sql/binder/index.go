package binder

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// maxIVFCentroidBytes is a conservative bound on one encoded IVF centroid: a
// B+Tree leaf record holds roughly half of the 16 KiB logical page.
const maxIVFCentroidBytes = 8000

// isIVFFamily reports whether m is an inverted-file vector index method (plain
// IVF or IVF-PQ), which share the coarse-quantiser build/search restrictions.
func isIVFFamily(m uint8) bool {
	return m == catalog.VecMethodIVF || m == catalog.VecMethodIVFPQ
}

func upperVecMethod(using string) string {
	if using == "ivfpq" {
		return "IVFPQ"
	}
	return "IVF"
}

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
	if s.Vector {
		switch s.Using {
		case "hnsw":
			idx.VecMethod = catalog.VecMethodHNSW
		case "sparse":
			idx.VecMethod = catalog.VecMethodSPARSE
			if s.IVFLists != 0 || s.IVFProbes != 0 || s.IVFSubspaces != 0 {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "USING SPARSE does not take IVF parameters")
			}
		case "ivf", "ivfpq":
			if s.Using == "ivfpq" {
				idx.VecMethod = catalog.VecMethodIVFPQ
			} else {
				idx.VecMethod = catalog.VecMethodIVF
			}
			if s.IVFLists <= 0 {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "USING "+upperVecMethod(s.Using)+" requires WITH (LISTS = n)")
			}
			if s.IVFLists > catalog.MaxVectorIndexLists {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "IVF LISTS exceeds the maximum")
			}
			if s.IVFProbes < 0 {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "IVF PROBES cannot be negative")
			}
			if s.IVFProbes > s.IVFLists {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "IVF PROBES cannot exceed LISTS")
			}
			idx.IVFLists = uint32(s.IVFLists)
			idx.IVFProbes = uint32(s.IVFProbes)
			if idx.VecMethod == catalog.VecMethodIVFPQ {
				if s.IVFSubspaces <= 0 {
					return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "USING IVFPQ requires WITH (SUBSPACES = M)")
				}
				if s.IVFSubspaces > catalog.MaxVectorIndexSubspaces {
					return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "IVFPQ SUBSPACES exceeds the maximum")
				}
				idx.IVFSubspaces = uint32(s.IVFSubspaces)
			} else if s.IVFSubspaces != 0 {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SUBSPACES requires USING IVFPQ")
			}
		default:
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR INDEX requires USING HNSW, IVF, IVFPQ, or SPARSE")
		}
	}
	if s.Analyzer != "" {
		if !s.Fulltext {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "ANALYZER requires a FULLTEXT INDEX")
		}
		a, err := fulltext.LookupAnalyzer(s.Analyzer)
		if err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "unknown full-text analyzer")
		}
		idx.FTAnalyzer = a.ID
		idx.FTVersion = a.Version
	}
	if s.VecQuant != "" {
		if !s.Vector {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "QUANTIZATION requires a VECTOR INDEX")
		}
		if idx.VecMethod != catalog.VecMethodHNSW {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "QUANTIZATION requires USING HNSW")
		}
		switch s.VecQuant {
		case "none", "f32":
			idx.VecQuant = 0
		case "f16":
			idx.VecQuant = types.VecF16
		case "i8":
			idx.VecQuant = types.VecI8
		default:
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "unknown HNSW QUANTIZATION (want F16, I8, or NONE)")
		}
	}
	special := s.Spatial || s.Fulltext || s.Vector
	if special && (len(s.Include) > 0 || s.Where != nil) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "INCLUDE and WHERE require a btree index")
	}
	if s.Spatial && len(keys) != 1 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SPATIAL INDEX requires one POINT column")
	}
	if s.Fulltext && (len(keys) == 0 || len(keys) > fulltext.MaxFields) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "FULLTEXT INDEX requires 1 to 8 STRING or TEXT columns")
	}
	if s.Vector && len(keys) != 1 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR INDEX requires one VECTOR column")
	}
	var pathKeys int
	hasExpr := false
	ftSeen := make(map[int]struct{})
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
			if _, dup := ftSeen[col]; dup {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate FULLTEXT INDEX column")
			}
			ftSeen[col] = struct{}{}
		}
		if s.Vector && tab.Columns[col].Type.Kind != types.KindVector {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR INDEX requires a VECTOR column")
		}
		if s.Vector && idx.VecQuant != 0 && tab.Columns[col].Type.VecElem == types.VecBit {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "QUANTIZATION is not supported on a BITVECTOR index")
		}
		if s.Vector && idx.VecQuant != 0 && tab.Columns[col].Type.VecElem == types.VecSparse {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "QUANTIZATION is not supported on a SPARSEVECTOR index")
		}
		if s.Vector && isIVFFamily(idx.VecMethod) && tab.Columns[col].Type.VecElem == types.VecBit {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "IVF does not support a BITVECTOR column; use USING HNSW")
		}
		if s.Vector && idx.VecMethod == catalog.VecMethodSPARSE && tab.Columns[col].Type.VecElem != types.VecSparse {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "USING SPARSE requires a SPARSEVECTOR column")
		}
		if s.Vector && idx.VecMethod != catalog.VecMethodSPARSE && tab.Columns[col].Type.VecElem == types.VecSparse {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SPARSEVECTOR requires CREATE VECTOR INDEX … USING SPARSE")
		}
		// One IVF centroid group is stored as a single B+Tree record, which
		// holds roughly half a logical page (~8 KiB). A single f32 centroid
		// ("NSIC" header + 4*dim bytes) past that ceiling will not fit even
		// alone, so reject it up front rather than failing mid-build with a
		// storage error.
		if s.Vector && isIVFFamily(idx.VecMethod) && 11+4*int(tab.Columns[col].Type.Precision) > maxIVFCentroidBytes {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "IVF is not supported at this vector dimension; use USING HNSW")
		}
		if s.Vector && idx.VecMethod == catalog.VecMethodIVFPQ {
			dim := int(tab.Columns[col].Type.Precision)
			if dim == 0 || dim%int(idx.IVFSubspaces) != 0 {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "IVFPQ SUBSPACES must divide the vector dimension")
			}
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
	if tab.Partitioning != nil && isIVFFamily(idx.VecMethod) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "IVF indexes on partitioned tables are not supported in this slice")
	}
	if tab.Partitioning != nil && idx.VecMethod == catalog.VecMethodSPARSE {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SPARSE indexes on partitioned tables are not supported in this slice")
	}
	if tab.Partitioning != nil && idx.Unique {
		// Cross-partition UNIQUE is enforced by probing every partition-local
		// root on every write (CREATE INDEX, INSERT, UPDATE, ATTACH PARTITION).
		// Partial, expression, and JSON-path UNIQUE indexes, and UNIQUE on
		// legacy TENANT tables, stay fail-closed on partitioned tables.
		if tab.Partitioning.Kind == catalog.PartitionLegacyTenant {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "secondary UNIQUE indexes on legacy TENANT tables are not supported")
		}
		if idx.Predicate != nil {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "partial UNIQUE indexes on partitioned tables are not supported in this slice")
		}
		if hasExpr {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "expression UNIQUE indexes on partitioned tables are not supported in this slice")
		}
		if pathKeys > 0 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "JSON-path UNIQUE indexes on partitioned tables are not supported in this slice")
		}
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
		case "highlight", "snippet":
			return types.Type{}, nerr.New(nerr.InvalidArgument, "sql.binder", "HIGHLIGHT/SNIPPET cannot be indexed")
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
