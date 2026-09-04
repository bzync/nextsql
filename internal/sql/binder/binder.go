package binder

import (
	"reflect"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/txn"
)

type Lookup func(name string) (*catalog.Table, bool)

type Bound interface{ bound() }

type (
	CreateTable struct {
		Table *catalog.Table
	}
	CreateDatabase struct {
		Name        string
		IfNotExists bool
	}
	ShowTasks struct {
		After string
		Limit int
	}
	CancelTask struct {
		ID string
	}
	Subscribe struct {
		Table     *catalog.Table
		Operation string
		After     uint64
	}
	DropTable struct {
		Name     string
		Table    *catalog.Table // nil when IF EXISTS and the table is absent
		IfExists bool
	}
	DropIndex struct {
		Name     string
		Table    *catalog.Table
		Index    catalog.Index
		IfExists bool
	}
	RebuildIndex struct {
		Table  *catalog.Table
		Index  catalog.Index
		Online bool
	}
	AlterTable struct {
		Table          *catalog.Table
		Result         *catalog.Table
		Transfer       *catalog.Table // source for ATTACH, new standalone table for DETACH
		OldName        string
		NewName        string
		Kind           ast.AlterCmd
		DroppedIndexes []catalog.Index
	}
	CreateIndex struct {
		Table *catalog.Table
		Index catalog.Index
	}
	Insert struct {
		Table     *catalog.Table
		Columns   []int
		Rows      [][]ast.Expr
		Returning Returning
	}
	Upsert struct {
		Table      *catalog.Table
		Columns    []int
		Rows       [][]ast.Expr
		UniqueCols []int
		UniquePK   bool
		UniqueIdx  string
		Sets       []Set
		DefaultSet bool
		Returning  Returning
	}
	// Returning is a bound DML output list. Empty Exprs means no RETURNING.
	Returning struct {
		Cols  []int
		Exprs []ast.Expr
		Names []string
		Eval  *catalog.Table
	}
	BoundJoin struct {
		Table *catalog.Table // qualified right side
		On    ast.Expr
		Kind  ast.JoinKind
		Input Bound // CTE/derived right input; nil means catalog table scan
	}
	// BoundSubjoin is a decorrelated EXISTS/IN predicate as a semi- or anti-join.
	BoundSubjoin struct {
		Kind   ast.JoinKind
		Right  Bound
		Pred   ast.Expr
		Schema *catalog.Table // concatenated left||right names for the join predicate
	}
	Select struct {
		Table          *catalog.Table
		Input          Bound
		Joins          []BoundJoin
		Subjoins       []BoundSubjoin
		Schema         *catalog.Table // single table or joined schema
		Star           bool
		Distinct       bool
		OutCols        []int // source column ordinals; -1 means expression
		OutExprs       []ast.Expr
		OutNames       []string
		Where          ast.Expr
		Groups         []ast.Expr
		Aggs           []Agg
		HasAgg         bool
		Having         ast.Expr
		SearchCols     []int
		SearchWeights  []float64
		SearchQuery    ast.Expr
		FacetCols      []int
		FacetNames     []string
		NearestCol     int
		NearestQuery   ast.Expr
		NearestMetric  string
		Nearest2Col    int
		Nearest2Query  ast.Expr
		Nearest2Metric string
		Order          []OrderKey
		Hidden         int // trailing ORDER BY columns stripped after sort
		Windows        []BoundWindow
		AggExprs       []ast.Expr
		AggNames       []string
		AggSchema      *catalog.Table
		Limit          *int64
		Offset         *int64
	}
	SetOperation struct {
		Left, Right Bound
		Op          string
		All         bool
		Names       []string
	}
	With struct {
		CTEs  []CTE
		Query Bound
	}
	CTE struct {
		Name        string
		ID          uint64
		Query       Bound
		QueryAST    ast.Stmt
		Names       []string
		Schema      *catalog.Table
		Recursive   bool
		Distinct    bool
		Materialize ast.CTEMaterialize
		Refs        int
		Anchor      Bound
		RecursiveQ  Bound
	}
	CTERef struct {
		Name   string
		ID     uint64
		Schema *catalog.Table
	}
	OrderKey struct {
		Col  int
		Desc bool
	}
	Agg struct {
		Fun  string
		Arg  ast.Expr
		Col  int
		Star bool
	}
	BoundWindow struct {
		Fun       string
		Args      []ast.Expr
		Star      bool
		Partition []ast.Expr
		Order     []ast.OrderItem
		Frame     ast.Frame
		Result    string
		OutType   types.Type
	}
	Update struct {
		Table     *catalog.Table
		Sets      []Set
		Where     ast.Expr
		Limit     int64
		Returning Returning
	}
	Set struct {
		Col  int
		Expr ast.Expr
	}
	Delete struct {
		Table     *catalog.Table
		Where     ast.Expr
		Limit     int64
		Returning Returning
	}
	Begin struct {
		Iso txn.Isolation
	}
	Commit   struct{}
	Rollback struct{}
	Explain  struct {
		Analyze bool
		Stmt    Bound
	}
	Analyze struct {
		Table *catalog.Table // nil means every table
	}
	Maintain struct {
		Table *catalog.Table // nil means the database
		Index string
	}
)

func (CreateTable) bound()    {}
func (CreateDatabase) bound() {}
func (ShowTasks) bound()      {}
func (CancelTask) bound()     {}
func (Subscribe) bound()      {}
func (DropTable) bound()      {}
func (DropIndex) bound()      {}
func (RebuildIndex) bound()   {}
func (AlterTable) bound()     {}
func (CreateIndex) bound()    {}
func (Insert) bound()         {}
func (Upsert) bound()         {}
func (Select) bound()         {}
func (SetOperation) bound()   {}
func (With) bound()           {}
func (CTERef) bound()         {}
func (Update) bound()         {}
func (Delete) bound()         {}
func (Begin) bound()          {}
func (Commit) bound()         {}
func (Rollback) bound()       {}
func (Explain) bound()        {}
func (Analyze) bound()        {}
func (Maintain) bound()       {}

func Bind(stmt ast.Stmt, lookup Lookup, nextID uint32) (Bound, error) {
	return bind(stmt, lookup, nextID, nil)
}

func bind(stmt ast.Stmt, lookup Lookup, nextID uint32, ctes map[string]*CTE) (Bound, error) {
	switch s := stmt.(type) {
	case ast.With:
		return bindWith(s, lookup, nextID, ctes)
	case ast.SetOperation:
		left, err := bind(s.Left, lookup, nextID, ctes)
		if err != nil {
			return nil, err
		}
		right, err := bind(s.Right, lookup, nextID, ctes)
		if err != nil {
			return nil, err
		}
		ln, lok := boundNames(left)
		rn, rok := boundNames(right)
		if !lok || !rok {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "set-operation inputs must be queries")
		}
		if len(ln) != len(rn) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "set-operation column count mismatch")
		}
		if boundHasClientEncryptedOutput(left) || boundHasClientEncryptedOutput(right) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "set operations cannot include ENCRYPTED CLIENT columns")
		}
		return SetOperation{Left: left, Right: right, Op: s.Op, All: s.All, Names: ln}, nil
	case ast.CreateTable:
		t, err := catalog.TableFromAST(nextID, s)
		if err != nil {
			return nil, err
		}
		if _, ok := lookup(t.Name); ok {
			return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "table already exists")
		}
		if s.Partition != nil {
			if err := attachPartitioning(t, s.Partition); err != nil {
				return nil, err
			}
		}
		if err := catalog.ValidateForeignKeys(t, lookup); err != nil {
			return nil, err
		}
		if t.Partitioning != nil {
			if err := catalog.ValidatePartitioning(t); err != nil {
				return nil, err
			}
			pk := make(map[int]struct{}, len(t.PK))
			for _, ord := range t.PK {
				pk[ord] = struct{}{}
			}
			for _, ord := range t.Partitioning.Columns {
				if _, ok := pk[ord]; !ok {
					return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "primary key must include every partition column")
				}
			}
			// Cross-partition FKs are not yet supported transactionally.
			if len(t.ForeignKeys) > 0 {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "partitioned tables cannot have foreign keys in this slice")
			}
			// Partition key must be NOT NULL or PRIMARY? Enforce NOT NULL for RANGE/TENANT.
			// Tenant partitioning must be on tenant_id.
		}
		return CreateTable{Table: t}, nil
	case ast.CreateDatabase:
		if s.Name == "" {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "empty database name")
		}
		if catalog.ReservedName(s.Name) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "database name prefix nsql_ is reserved")
		}
		return CreateDatabase{Name: s.Name, IfNotExists: s.IfNotExists}, nil
	case ast.Subscribe:
		tab, err := mustTable(lookup, s.Table)
		if err != nil {
			return nil, err
		}
		return Subscribe{Table: tab, Operation: s.Operation, After: s.After}, nil
	case ast.DropTable:
		if catalog.ReservedName(s.Name) && !catalog.IsHistoryTable(s.Name) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "table name prefix nsql_ is reserved")
		}
		tab, err := mustTable(lookup, s.Name)
		if err != nil {
			if s.IfExists && nerr.HasCode(err, nerr.NotFound) {
				return DropTable{Name: s.Name, IfExists: true}, nil
			}
			return nil, err
		}
		return DropTable{Name: s.Name, Table: tab, IfExists: s.IfExists}, nil
	case ast.DropIndex:
		if s.Table == "" {
			if s.IfExists {
				return DropIndex{Name: s.Name, IfExists: true}, nil
			}
			return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown index")
		}
		tab, err := mustTable(lookup, s.Table)
		if err != nil {
			return nil, err
		}
		for _, idx := range tab.Indexes {
			if idx.Name == s.Name {
				return DropIndex{Name: s.Name, Table: tab, Index: idx, IfExists: s.IfExists}, nil
			}
		}
		if s.IfExists {
			return DropIndex{Name: s.Name, IfExists: true}, nil
		}
		return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown index")
	case ast.RebuildIndex:
		if s.Table == "" {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown index")
		}
		tab, err := mustTable(lookup, s.Table)
		if err != nil {
			return nil, err
		}
		for _, idx := range tab.Indexes {
			if idx.Name == s.Name {
				return RebuildIndex{Table: tab, Index: idx, Online: s.Online}, nil
			}
		}
		return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown index")
	case ast.AlterTable:
		return bindAlter(s, lookup, nextID)
	case ast.CreateIndex:
		return bindCreateIndex(s, lookup)
	case ast.Insert:
		for _, row := range s.Rows {
			for _, ex := range row {
				if err := rejectClientEncryptedSubqueryExpr(ex, lookup, ctes); err != nil {
					return nil, err
				}
			}
		}
		for _, item := range s.Returning {
			if err := rejectClientEncryptedSubqueryExpr(item.Expr, lookup, ctes); err != nil {
				return nil, err
			}
		}
		tab, cols, err := bindInsertRows(s.Table, s.Columns, s.Rows, lookup, "INSERT")
		if err != nil {
			return nil, err
		}
		ret, err := bindReturning(s.ReturningStar, s.Returning, tab, nil)
		if err != nil {
			return nil, err
		}
		return Insert{Table: tab, Columns: cols, Rows: s.Rows, Returning: ret}, nil
	case ast.Upsert:
		return bindUpsert(s, lookup)
	case ast.Select:
		return bindSelect(s, lookup, ctes)
	case ast.Update:
		if err := rejectClientEncryptedSubqueryExpr(s.Where, lookup, ctes); err != nil {
			return nil, err
		}
		for _, set := range s.Sets {
			if err := rejectClientEncryptedSubqueryExpr(set.Expr, lookup, ctes); err != nil {
				return nil, err
			}
		}
		for _, item := range s.Returning {
			if err := rejectClientEncryptedSubqueryExpr(item.Expr, lookup, ctes); err != nil {
				return nil, err
			}
		}
		tab, err := mustTable(lookup, s.Table)
		if err != nil {
			return nil, err
		}
		if len(s.Sets) == 0 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "UPDATE has no assignments")
		}
		var sets []Set
		seen := make(map[int]struct{}, len(s.Sets))
		for _, a := range s.Sets {
			i, ok := tab.ColIndex(a.Name)
			if !ok {
				return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown update column")
			}
			if _, dup := seen[i]; dup {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate update column")
			}
			seen[i] = struct{}{}
			if err := checkClientEncryptedAssignment(a.Expr, tab, tab.Columns[i]); err != nil {
				return nil, err
			}
			if err := checkExpr(a.Expr, tab, tab.Columns[i].Type, false); err != nil {
				return nil, err
			}
			if err := rejectSearchHL(a.Expr); err != nil {
				return nil, err
			}
			sets = append(sets, Set{Col: i, Expr: a.Expr})
		}
		if err := checkExpr(s.Where, tab, types.Bool(), true); err != nil {
			return nil, err
		}
		if err := rejectClientEncryptedExpr(s.Where, tab, "WHERE"); err != nil {
			return nil, err
		}
		if err := rejectSearchHL(s.Where); err != nil {
			return nil, err
		}
		ret, err := bindReturning(s.ReturningStar, s.Returning, tab, nil)
		if err != nil {
			return nil, err
		}
		return Update{Table: tab, Sets: sets, Where: s.Where, Limit: s.Limit, Returning: ret}, nil
	case ast.Delete:
		if err := rejectClientEncryptedSubqueryExpr(s.Where, lookup, ctes); err != nil {
			return nil, err
		}
		for _, item := range s.Returning {
			if err := rejectClientEncryptedSubqueryExpr(item.Expr, lookup, ctes); err != nil {
				return nil, err
			}
		}
		tab, err := mustTable(lookup, s.Table)
		if err != nil {
			return nil, err
		}
		if err := checkExpr(s.Where, tab, types.Bool(), true); err != nil {
			return nil, err
		}
		if err := rejectClientEncryptedExpr(s.Where, tab, "WHERE"); err != nil {
			return nil, err
		}
		if err := rejectSearchHL(s.Where); err != nil {
			return nil, err
		}
		ret, err := bindReturning(s.ReturningStar, s.Returning, tab, nil)
		if err != nil {
			return nil, err
		}
		return Delete{Table: tab, Where: s.Where, Limit: s.Limit, Returning: ret}, nil
	case ast.Begin:
		iso := txn.SnapshotIsolation
		switch s.Isolation {
		case "", "snapshot":
			iso = txn.SnapshotIsolation
		case "read committed":
			iso = txn.ReadCommitted
		case "serializable":
			iso = txn.Serializable
		default:
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "unknown isolation level")
		}
		return Begin{Iso: iso}, nil
	case ast.Commit:
		return Commit{}, nil
	case ast.Rollback:
		return Rollback{}, nil
	case ast.Explain:
		inner, err := bind(s.Stmt, lookup, nextID, ctes)
		if err != nil {
			return nil, err
		}
		return Explain{Analyze: s.Analyze, Stmt: inner}, nil
	case ast.Analyze:
		if s.Table == "" {
			return Analyze{}, nil
		}
		tab, err := mustTable(lookup, s.Table)
		if err != nil {
			return nil, err
		}
		return Analyze{Table: tab}, nil
	case ast.Maintain:
		if s.Table == "" {
			return Maintain{Index: s.Index}, nil
		}
		tab, err := mustTable(lookup, s.Table)
		if err != nil {
			return nil, err
		}
		return Maintain{Table: tab, Index: s.Index}, nil
	default:
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "unsupported statement")
	}
}

func boundNames(b Bound) ([]string, bool) {
	switch x := b.(type) {
	case Select:
		n := len(x.OutNames) - x.Hidden
		if n < 0 {
			n = 0
		}
		return append([]string(nil), x.OutNames[:n]...), true
	case SetOperation:
		return append([]string(nil), x.Names...), true
	case With:
		return boundNames(x.Query)
	case CTERef:
		if x.Schema == nil {
			return nil, false
		}
		names := make([]string, len(x.Schema.Columns))
		for i, c := range x.Schema.Columns {
			names[i] = c.Name
		}
		return names, true
	default:
		return nil, false
	}
}

func mustTable(lookup Lookup, name string) (*catalog.Table, error) {
	if lookup == nil {
		return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown table")
	}
	t, ok := lookup(name)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown table")
	}
	return t, nil
}

func checkExpr(e ast.Expr, tab *catalog.Table, hint types.Type, allowNil bool) error {
	if e == nil {
		if allowNil {
			return nil
		}
		return nerr.New(nerr.InvalidArgument, "sql.binder", "missing expression")
	}
	switch x := e.(type) {
	case ast.ScalarSubquery:
		if scalarQueryColumns(x.Query) != 1 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "scalar subquery must return exactly one column")
		}
		return nil
	case ast.InSubquery:
		if scalarQueryColumns(x.Query) != 1 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "IN subquery must return exactly one column")
		}
		return checkExpr(x.Expr, tab, types.Type{}, false)
	case ast.ExistsSubquery:
		return nil
	case ast.Literal, ast.VectorLit:
		return nil
	case ast.Param:
		if x.Name == "" {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "empty parameter name")
		}
		return nil
	case ast.Ident:
		if _, ok := tab.ColIndex(x.Name); !ok {
			return nerr.New(nerr.NotFound, "sql.binder", "unknown column")
		}
		return nil
	case ast.Path:
		if len(x.Parts) == 2 {
			qual := x.Parts[0] + "." + x.Parts[1]
			if _, ok := tab.ColIndex(qual); ok {
				return nil
			}
			if _, ok := tab.ColIndex(x.Parts[1]); ok {
				return nil
			}
		}
		if len(x.Parts) < 2 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "invalid path")
		}
		i, ok := tab.ColIndex(x.Parts[0])
		if !ok {
			return nerr.New(nerr.NotFound, "sql.binder", "unknown column")
		}
		switch tab.Columns[i].Type.Kind {
		case types.KindJSON:
			return nil
		case types.KindStruct:
			// col.field.field ... — every trailing part must resolve to a
			// STRUCT field name, descending into nested STRUCTs.
			ct := tab.Columns[i].Type
			for _, part := range x.Parts[1:] {
				fi := ct.StructFieldIndex(part)
				if fi < 0 {
					return nerr.New(nerr.NotFound, "sql.binder", "unknown STRUCT field "+part)
				}
				ct = ct.Fields[fi].Type
			}
			return nil
		default:
			return nerr.New(nerr.InvalidArgument, "sql.binder", "path extract requires a JSON or STRUCT column")
		}
	case ast.ArrayCtor:
		for _, el := range x.Elems {
			if err := checkExpr(el, tab, types.Type{}, false); err != nil {
				return err
			}
		}
		return nil
	case ast.StructCtor:
		if len(x.Names) == 0 || len(x.Names) != len(x.Elems) {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "malformed STRUCT constructor")
		}
		seen := map[string]struct{}{}
		for i, nm := range x.Names {
			if _, dup := seen[nm]; dup {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate STRUCT field name "+nm)
			}
			seen[nm] = struct{}{}
			if err := checkExpr(x.Elems[i], tab, types.Type{}, false); err != nil {
				return err
			}
		}
		return nil
	case ast.MapCtor:
		if len(x.Keys) != len(x.Vals) {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "malformed MAP constructor")
		}
		for i := range x.Keys {
			if err := checkExpr(x.Keys[i], tab, types.Type{}, false); err != nil {
				return err
			}
			if err := checkExpr(x.Vals[i], tab, types.Type{}, false); err != nil {
				return err
			}
		}
		return nil
	case ast.FieldAccess:
		return checkExpr(x.Base, tab, types.Type{}, false)
	case ast.Call:
		switch x.Name {
		case "uuid", "now", "ai":
			if len(x.Args) != 0 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes no arguments")
			}
			return nil
		case "row_number", "rank", "dense_rank", "lag", "lead", "first_value", "last_value":
			return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" requires OVER")
		case "count", "sum", "avg", "min", "max":
			if x.Star {
				if x.Name != "count" {
					return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" does not accept *")
				}
				return nil
			}
			if len(x.Args) != 1 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes one argument")
			}
			return checkExpr(x.Args[0], tab, types.Type{}, false)
		case "lower", "upper", "length", "trim", "ltrim", "rtrim":
			if x.Star || len(x.Args) != 1 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes one argument")
			}
			return checkExpr(x.Args[0], tab, types.Type{}, false)
		case "highlight":
			if x.Star || (len(x.Args) != 1 && len(x.Args) != 3) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "highlight takes one or three arguments")
			}
		case "snippet":
			if x.Star || (len(x.Args) != 1 && len(x.Args) != 2 && len(x.Args) != 4) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "snippet takes one, two, or four arguments")
			}
		case "substring":
			if x.Star || (len(x.Args) != 2 && len(x.Args) != 3) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "substring takes two or three arguments")
			}
		case "replace", "starts_with", "ends_with", "contains":
			if x.Star || len(x.Args) != 3 && x.Name == "replace" || len(x.Args) != 2 && x.Name != "replace" {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" has invalid argument count")
			}
		case "concat":
			if x.Star || len(x.Args) < 1 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "concat requires at least one argument")
			}
		case "coalesce", "greatest", "least":
			if x.Star || len(x.Args) < 1 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" requires at least one argument")
			}
		case "nullif":
			if x.Star || len(x.Args) != 2 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "nullif takes two arguments")
			}
		case "abs", "ceil", "floor":
			if x.Star || len(x.Args) != 1 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes one argument")
			}
		case "round":
			if x.Star || (len(x.Args) != 1 && len(x.Args) != 2) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "round takes one or two arguments")
			}
		case "mod":
			if x.Star || len(x.Args) != 2 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "mod takes two arguments")
			}
		case "sqrt":
			if x.Star || len(x.Args) != 1 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "sqrt takes one argument")
			}
		case "power":
			if x.Star || len(x.Args) != 2 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "power takes two arguments")
			}
		case "json_get":
			if x.Star || len(x.Args) != 2 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "json_get takes two arguments")
			}
		case "json_array_length":
			if x.Star || (len(x.Args) != 1 && len(x.Args) != 2) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "json_array_length takes one or two arguments")
			}
		case "json_type":
			if x.Star || (len(x.Args) != 1 && len(x.Args) != 2) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "json_type takes one or two arguments")
			}
		case "json_set":
			if x.Star || len(x.Args) != 3 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "json_set takes three arguments")
			}
		case "extract", "date_trunc":
			if x.Star || len(x.Args) != 2 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes two arguments")
			}
		case "date_add", "date_diff":
			if x.Star || len(x.Args) != 3 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes three arguments")
			}
		case "json_remove", "json_contains":
			if x.Star || len(x.Args) != 2 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes two arguments")
			}
		case "cosine", "cosine_distance", "l2", "l1", "manhattan", "inner_product", "dot", "vector_dot", "vector_add", "vector_sub", "vector_subtract", "vector_scale":
			if len(x.Args) != 2 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes two arguments")
			}
		case "vector_dim", "vector_dims", "dimensions", "vector_norm", "norm", "vector_normalize", "normalize":
			if len(x.Args) != 1 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes one argument")
			}
		case "cardinality", "array_length", "map_size", "map_keys", "map_values":
			if x.Star || len(x.Args) != 1 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes one argument")
			}
		case "element_at", "array_contains", "map_contains_key":
			if x.Star || len(x.Args) != 2 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" takes two arguments")
			}
		default:
			if types.IsGeoFunc(x.Name) {
				for _, a := range x.Args {
					if err := checkExpr(a, tab, types.Type{}, false); err != nil {
						return err
					}
				}
				return checkGeoCall(x)
			}
			if types.IsSpatialFunc(x.Name) {
				for _, a := range x.Args {
					if err := checkExpr(a, tab, types.Type{}, false); err != nil {
						return err
					}
				}
				return nil
			}
			return nerr.New(nerr.InvalidArgument, "sql.binder", "unknown function")
		}
		for _, a := range x.Args {
			if err := checkExpr(a, tab, types.Type{}, false); err != nil {
				return err
			}
		}
		return nil
	case ast.Window:
		return checkWindowExpr(x, tab)
	case ast.Case:
		if len(x.Whens) == 0 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "CASE requires WHEN")
		}
		if x.Operand != nil {
			if err := checkExpr(x.Operand, tab, types.Type{}, false); err != nil {
				return err
			}
		}
		for _, arm := range x.Whens {
			if err := checkExpr(arm.When, tab, types.Type{}, false); err != nil {
				return err
			}
			if err := checkExpr(arm.Then, tab, types.Type{}, false); err != nil {
				return err
			}
		}
		return checkExpr(x.Else, tab, types.Type{}, true)
	case ast.Unary:
		return checkExpr(x.Right, tab, hint, false)
	case ast.Binary:
		if err := checkExpr(x.Left, tab, types.Type{}, false); err != nil {
			return err
		}
		return checkExpr(x.Right, tab, types.Type{}, false)
	case ast.Between:
		if err := checkExpr(x.Expr, tab, types.Type{}, false); err != nil {
			return err
		}
		if err := checkExpr(x.Low, tab, types.Type{}, false); err != nil {
			return err
		}
		return checkExpr(x.High, tab, types.Type{}, false)
	case ast.IsNull:
		return checkExpr(x.Expr, tab, types.Type{}, false)
	default:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "unsupported expression")
	}
}

func scalarQueryColumns(stmt ast.Stmt) int {
	switch q := stmt.(type) {
	case ast.Select:
		if q.Star {
			return -1
		}
		return len(q.List)
	case ast.SetOperation:
		return scalarQueryColumns(q.Left)
	default:
		return -1
	}
}

func checkGeoCall(x ast.Call) error {
	n := 0
	switch types.CanonGeoName(x.Name) {
	case "point":
		n = 2
	case "box":
		n = 4
	case "lon", "lat", "linelength", "linestring", "polygon", "area", "perimeter", "centroid", "envelope", "geometrytype", "npoints", "nrings":
		n = 1
	case "distance", "distance_spheroid", "within", "covers", "intersects", "disjoint":
		n = 2
	case "dwithin":
		n = 3
	default:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "unknown function")
	}
	if len(x.Args) != n {
		return nerr.New(nerr.InvalidArgument, "sql.binder", x.Name+" argument count")
	}
	return nil
}

func bindSelect(s ast.Select, lookup Lookup, ctes map[string]*CTE) (Bound, error) {
	if err := rejectClientEncryptedSubqueriesInSelect(s, lookup, ctes); err != nil {
		return nil, err
	}
	var left *catalog.Table
	var input Bound
	if s.FromQuery != nil {
		if len(s.Joins) > 0 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "joins from a derived table are not yet supported")
		}
		var err error
		input, err = bind(s.FromQuery, lookup, 0, ctes)
		if err != nil {
			return nil, err
		}
		names, ok := boundNames(input)
		if !ok || len(names) == 0 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "derived table must expose columns")
		}
		left = &catalog.Table{Name: s.Alias, Columns: make([]catalog.Column, len(names))}
		for i, name := range names {
			left.Columns[i] = catalog.Column{Name: name, Type: types.Type{Kind: types.KindInvalid}}
		}
		fillCTETypes(left, input)
	} else if c := ctes[s.Table]; c != nil {
		c.Refs++
		input = CTERef{Name: c.Name, ID: c.ID, Schema: c.Schema}
		left = c.Schema.Clone()
		if left == nil {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "CTE is missing a schema")
		}
		left.Name = s.Table
	} else {
		var err error
		left, err = mustTable(lookup, s.Table)
		if err != nil {
			return nil, err
		}
	}
	schema := qualifyTable(left, aliasOr(s.Alias, s.Table), len(s.Joins) > 0)

	if 1+len(s.Joins) > security.MaxJoinTables {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "join complexity exceeds limit")
	}
	var joins []BoundJoin
	for _, js := range s.Joins {
		var rt *catalog.Table
		var jInput Bound
		if c := ctes[js.Table]; c != nil {
			c.Refs++
			jInput = CTERef{Name: c.Name, ID: c.ID, Schema: c.Schema}
			rt = c.Schema.Clone()
			if rt == nil {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "CTE is missing a schema")
			}
			rt.Name = js.Table
		} else {
			var err error
			rt, err = mustTable(lookup, js.Table)
			if err != nil {
				return nil, err
			}
		}
		right := qualifyTable(rt, aliasOr(js.Alias, js.Table), true)
		if js.Kind == ast.JoinLeft || js.Kind == ast.JoinFull {
			// Null-extended side is nullable even when the base column is NOT NULL.
			for i := range right.Columns {
				right.Columns[i].NotNull = false
			}
		}
		if js.Kind == ast.JoinRight || js.Kind == ast.JoinFull {
			for i := range schema.Columns {
				schema.Columns[i].NotNull = false
			}
		}
		schema = mergeTables(schema, right)
		joinOn := rewriteQual(js.On, schema)
		if containsWindow(joinOn) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "window functions are not allowed in JOIN")
		}
		if err := checkExpr(joinOn, schema, types.Bool(), true); err != nil {
			return nil, err
		}
		if err := rejectClientEncryptedExpr(joinOn, schema, "JOIN"); err != nil {
			return nil, err
		}
		if err := rejectSearchHL(joinOn); err != nil {
			return nil, err
		}
		joins = append(joins, BoundJoin{Table: right, On: joinOn, Kind: js.Kind, Input: jInput})
	}
	if (len(s.SearchCols) > 0 || s.NearestCol != "" || s.Nearest2Col != "") && len(s.Joins) > 0 {
		for _, js := range s.Joins {
			if js.Kind == ast.JoinLeft || js.Kind == ast.JoinRight || js.Kind == ast.JoinFull {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH/NEAREST does not support outer JOIN")
			}
		}
	}
	where := rewriteQual(s.Where, schema)
	if containsWindow(where) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "window functions are not allowed in WHERE")
	}
	if err := checkExpr(where, schema, types.Bool(), true); err != nil {
		return nil, err
	}
	if err := rejectClientEncryptedExpr(where, schema, "WHERE"); err != nil {
		return nil, err
	}
	if err := rejectSearchHL(where); err != nil {
		return nil, err
	}
	where, subjoins, err := extractSubjoins(where, schema, aliasOr(s.Alias, s.Table), lookup, ctes)
	if err != nil {
		return nil, err
	}
	out := Select{
		Table:          left,
		Input:          input,
		Joins:          joins,
		Subjoins:       subjoins,
		Schema:         schema,
		Star:           s.Star,
		Distinct:       s.Distinct,
		Where:          where,
		SearchQuery:    s.SearchQuery,
		NearestCol:     -1,
		NearestQuery:   s.NearestQuery,
		NearestMetric:  s.NearestMetric,
		Nearest2Col:    -1,
		Nearest2Query:  s.Nearest2Query,
		Nearest2Metric: s.Nearest2Metric,
		Limit:          s.Limit,
		Offset:         s.Offset,
	}
	if len(s.SearchCols) > 0 {
		if len(s.SearchCols) > fulltext.MaxFields {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH exceeds the maximum number of fields")
		}
		if s.SearchQuery == nil {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH is missing a query")
		}
		switch q := s.SearchQuery.(type) {
		case ast.Param:
		case ast.Literal:
			k := q.Value.Typ.Kind
			if k != types.KindString && k != types.KindText {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH query must be a string or parameter")
			}
		default:
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH query must be a string or parameter")
		}
		if err := checkExpr(s.SearchQuery, schema, types.String(), false); err != nil {
			return nil, err
		}
		seen := make(map[int]struct{}, len(s.SearchCols))
		for _, name := range s.SearchCols {
			ord, ok := left.ColIndex(name)
			if !ok {
				if colOnJoined(name, s.Joins, lookup) {
					return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH column must belong to the FROM table")
				}
				return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown SEARCH column")
			}
			if _, dup := seen[ord]; dup {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate SEARCH column")
			}
			k := left.Columns[ord].Type.Kind
			if left.Columns[ord].ClientEncrypted() {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH cannot use an ENCRYPTED CLIENT column")
			}
			if k != types.KindString && k != types.KindText {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH requires a STRING or TEXT column")
			}
			seen[ord] = struct{}{}
			out.SearchCols = append(out.SearchCols, ord)
		}
		if len(s.SearchWeights) > 0 {
			if len(s.SearchWeights) != len(s.SearchCols) {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SEARCH field weights must match the column list")
			}
			weights := make([]float64, len(s.SearchWeights))
			for i, w := range s.SearchWeights {
				if err := fulltext.CheckFieldWeight(w); err != nil {
					return nil, err
				}
				weights[i] = w
			}
			if !fulltext.UniformWeights(weights) {
				out.SearchWeights = weights
			}
		}
	}
	if s.NearestCol != "" {
		ord, err := bindNearestCol(s.NearestCol, s.NearestQuery, s.NearestMetric, left, schema, s.Joins, lookup)
		if err != nil {
			return nil, err
		}
		out.NearestCol = ord
	}
	if s.Nearest2Col != "" {
		if s.NearestCol == "" {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "a second NEAREST requires a first NEAREST")
		}
		ord, err := bindNearestCol(s.Nearest2Col, s.Nearest2Query, s.Nearest2Metric, left, schema, s.Joins, lookup)
		if err != nil {
			return nil, err
		}
		out.Nearest2Col = ord
		if out.NearestCol == out.Nearest2Col {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "NEAREST columns must be distinct")
		}
		a := left.Columns[out.NearestCol].Type.VecElem
		b := left.Columns[out.Nearest2Col].Type.VecElem
		aSparse, bSparse := a == types.VecSparse, b == types.VecSparse
		if aSparse == bSparse || a == types.VecBit || b == types.VecBit {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "a second NEAREST requires one VECTOR column and one SPARSEVECTOR column")
		}
	}
	if len(s.FacetCols) > 0 {
		if err := bindFacet(&out, s, left); err != nil {
			return nil, err
		}
		return out, nil
	}
	if s.Star {
		if len(s.Group) > 0 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SELECT * with GROUP BY is not supported")
		}
		if s.Distinct && tableHasClientEncrypted(schema) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SELECT DISTINCT cannot include ENCRYPTED CLIENT columns")
		}
		for i, c := range schema.Columns {
			out.OutCols = append(out.OutCols, i)
			out.OutExprs = append(out.OutExprs, ast.Ident{Name: c.Name})
			out.OutNames = append(out.OutNames, c.Name)
		}
		if err := bindOrder(&out, s, schema); err != nil {
			return nil, err
		}
		if err := bindWindows(&out, schema); err != nil {
			return nil, err
		}
		return out, nil
	}
	if len(s.List) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "empty select list")
	}
	for i := range s.Group {
		s.Group[i] = rewriteQual(s.Group[i], schema)
		if containsWindow(s.Group[i]) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "window functions are not allowed in GROUP BY")
		}
		if err := checkExpr(s.Group[i], schema, types.Type{}, false); err != nil {
			return nil, err
		}
		if err := rejectClientEncryptedExpr(s.Group[i], schema, "GROUP BY"); err != nil {
			return nil, err
		}
		if err := rejectSearchHL(s.Group[i]); err != nil {
			return nil, err
		}
		out.Groups = append(out.Groups, s.Group[i])
	}
	var hasAgg, hasBare bool
	for _, item := range s.List {
		ex := rewriteQual(item.Expr, schema)
		if err := checkExpr(ex, schema, types.Type{}, false); err != nil {
			return nil, err
		}
		if exprUsesClientEncrypted(ex, schema) && !bareClientEncryptedColumn(ex, schema) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "ENCRYPTED CLIENT select item must be a bare column")
		}
		if containsSearchHL(ex) && len(out.SearchCols) == 0 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "HIGHLIGHT/SNIPPET requires SEARCH")
		}
		name := item.Alias
		ord := -1
		if id, ok := ex.(ast.Ident); ok {
			if i, found := schema.ColIndex(id.Name); found {
				ord = i
				if name == "" {
					name = schema.Columns[i].Name
				}
			}
		}
		if _, isWin := ex.(ast.Window); isWin {
			if name == "" {
				if w := ex.(ast.Window); w.Fn.Name != "" {
					name = w.Fn.Name
				}
			}
		} else if call, ok := ex.(ast.Call); ok && isAgg(call.Name) {
			hasAgg = true
			col := -1
			var arg ast.Expr
			if !call.Star && len(call.Args) == 1 {
				arg = call.Args[0]
				if id, ok := arg.(ast.Ident); ok {
					if i, found := schema.ColIndex(id.Name); found {
						col = i
					}
				}
			}
			if name == "" {
				name = call.Name
			}
			out.Aggs = append(out.Aggs, Agg{Fun: call.Name, Arg: arg, Col: col, Star: call.Star})
		} else {
			if containsGroupingAgg(ex) {
				hasAgg = true
			} else if !containsWindow(ex) {
				hasBare = true
				if len(s.Group) > 0 && !grouped(ex, s.Group) {
					return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "select item must be an aggregate or GROUP BY expression")
				}
			}
		}
		if name == "" {
			name = "?"
		}
		out.OutCols = append(out.OutCols, ord)
		out.OutExprs = append(out.OutExprs, ex)
		out.OutNames = append(out.OutNames, name)
	}
	if s.Distinct {
		for _, ex := range out.OutExprs {
			if exprUsesClientEncrypted(ex, schema) {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "SELECT DISTINCT cannot include ENCRYPTED CLIENT columns")
			}
		}
	}
	if hasAgg && hasBare && len(s.Group) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "aggregate mixed with columns requires GROUP BY")
	}
	out.HasAgg = hasAgg || len(s.Group) > 0
	if out.HasAgg && len(out.Aggs) == 0 && len(s.Group) > 0 {
		// GROUP BY without aggregates is a distinct-style grouping
	}
	if s.Having != nil {
		if containsWindow(s.Having) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "window functions are not allowed in HAVING")
		}
		if err := rejectSearchHL(s.Having); err != nil {
			return nil, err
		}
		if !out.HasAgg {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "HAVING requires GROUP BY or an aggregate")
		}
		having, err := bindHaving(s.Having, out)
		if err != nil {
			return nil, err
		}
		out.Having = having
	}
	if err := bindOrder(&out, s, schema); err != nil {
		return nil, err
	}
	if err := bindWindows(&out, schema); err != nil {
		return nil, err
	}
	return out, nil
}

func bindHaving(e ast.Expr, out Select) (ast.Expr, error) {
	var rewrite func(ast.Expr) ast.Expr
	rewrite = func(e ast.Expr) ast.Expr {
		for i, selected := range out.OutExprs {
			if reflect.DeepEqual(e, selected) {
				return ast.Ident{Name: out.OutNames[i]}
			}
		}
		switch x := e.(type) {
		case ast.Ident:
			for _, name := range out.OutNames {
				if x.Name == name {
					return x
				}
			}
			return x
		case ast.Unary:
			return ast.Unary{Op: x.Op, Right: rewrite(x.Right)}
		case ast.Binary:
			return ast.Binary{Op: x.Op, Left: rewrite(x.Left), Right: rewrite(x.Right)}
		case ast.Between:
			return ast.Between{Expr: rewrite(x.Expr), Low: rewrite(x.Low), High: rewrite(x.High), Not: x.Not}
		case ast.IsNull:
			return ast.IsNull{Expr: rewrite(x.Expr), Not: x.Not}
		case ast.Case:
			whens := make([]ast.CaseWhen, len(x.Whens))
			for i, arm := range x.Whens {
				whens[i] = ast.CaseWhen{When: rewrite(arm.When), Then: rewrite(arm.Then)}
			}
			return ast.Case{Operand: rewrite(x.Operand), Whens: whens, Else: rewrite(x.Else)}
		default:
			return e
		}
	}
	having := rewrite(e)
	tab := &catalog.Table{Name: "having", Columns: make([]catalog.Column, len(out.OutNames))}
	for i, name := range out.OutNames {
		tab.Columns[i] = catalog.Column{Name: name, Type: types.Type{Kind: types.KindDecimal}}
	}
	if err := checkExpr(having, tab, types.Bool(), false); err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, "sql.binder", "HAVING may reference selected aggregates, grouped outputs, or their aliases", err)
	}
	return having, nil
}

func bindOrder(out *Select, s ast.Select, schema *catalog.Table) error {
	if len(s.Order) == 0 {
		return nil
	}
	hiddenStart := len(out.OutNames)
	for _, item := range s.Order {
		ex := rewriteQual(item.Expr, schema)
		if err := rejectClientEncryptedExpr(ex, schema, "ORDER BY"); err != nil {
			return err
		}
		if containsSearchHL(ex) && len(out.SearchCols) == 0 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "HIGHLIGHT/SNIPPET requires SEARCH")
		}
		if n, ok := orderOrdinal(ex); ok {
			if n < 1 || n > len(out.OutNames) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "ORDER BY position out of range")
			}
			if n-1 < len(out.OutExprs) && exprUsesClientEncrypted(out.OutExprs[n-1], schema) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "ORDER BY cannot use an ENCRYPTED CLIENT column")
			}
			out.Order = append(out.Order, OrderKey{Col: n - 1, Desc: item.Desc})
			continue
		}
		if id, ok := ex.(ast.Ident); ok {
			found := -1
			for i, name := range out.OutNames {
				if name == id.Name {
					if found >= 0 {
						return nerr.New(nerr.InvalidArgument, "sql.binder", "ORDER BY column is ambiguous")
					}
					found = i
				}
			}
			if found >= 0 {
				if found < len(out.OutExprs) && exprUsesClientEncrypted(out.OutExprs[found], schema) {
					return nerr.New(nerr.InvalidArgument, "sql.binder", "ORDER BY cannot use an ENCRYPTED CLIENT column")
				}
				out.Order = append(out.Order, OrderKey{Col: found, Desc: item.Desc})
				continue
			}
		}
		if containsWindow(ex) {
			if err := checkExpr(ex, schema, types.Type{}, false); err != nil {
				return err
			}
		} else if out.HasAgg {
			if !grouped(ex, out.Groups) {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "ORDER BY must be an output column, ordinal, or GROUP BY expression")
			}
		} else if err := checkExpr(ex, schema, types.Type{}, false); err != nil {
			return err
		}
		matched := -1
		ek := exprKey(ex)
		if ek != "" {
			for i, oe := range out.OutExprs {
				if exprKey(oe) == ek {
					matched = i
					break
				}
			}
		}
		if matched >= 0 {
			out.Order = append(out.Order, OrderKey{Col: matched, Desc: item.Desc})
			continue
		}
		if out.Distinct {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "ORDER BY expression must appear in SELECT DISTINCT output")
		}
		if out.HasAgg && !containsWindow(ex) {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "ORDER BY must be an output column, ordinal, or GROUP BY expression")
		}
		name := "?"
		ord := -1
		if id, ok := ex.(ast.Ident); ok {
			name = id.Name
			if i, found := schema.ColIndex(id.Name); found {
				ord = i
			}
		}
		out.OutCols = append(out.OutCols, ord)
		out.OutExprs = append(out.OutExprs, ex)
		out.OutNames = append(out.OutNames, name)
		out.Order = append(out.Order, OrderKey{Col: len(out.OutNames) - 1, Desc: item.Desc})
	}
	out.Hidden = len(out.OutNames) - hiddenStart
	return nil
}

func orderOrdinal(e ast.Expr) (int, bool) {
	lit, ok := e.(ast.Literal)
	if !ok || lit.Value.Null || lit.Value.Typ.Kind != types.KindDecimal || lit.Value.Dec.Coef == nil {
		return 0, false
	}
	if lit.Value.Dec.Scale != 0 || !lit.Value.Dec.Coef.IsInt64() {
		return 0, false
	}
	n := lit.Value.Dec.Coef.Int64()
	if n < 1 || n > 1<<20 {
		return 0, false
	}
	return int(n), true
}

func isAgg(name string) bool {
	switch name {
	case "count", "sum", "avg", "min", "max":
		return true
	}
	return false
}

func grouped(e ast.Expr, groups []ast.Expr) bool {
	es := exprKey(e)
	for _, g := range groups {
		if exprKey(g) == es {
			return true
		}
	}
	if id, ok := e.(ast.Ident); ok {
		for _, g := range groups {
			if gid, ok := g.(ast.Ident); ok && gid.Name == id.Name {
				return true
			}
		}
	}
	return false
}

func exprKey(e ast.Expr) string {
	switch x := e.(type) {
	case ast.Ident:
		return "i:" + x.Name
	case ast.Literal:
		return "l:" + x.Value.String()
	case ast.Path:
		s := "p:"
		for i, p := range x.Parts {
			if i > 0 {
				s += "."
			}
			s += p
		}
		return s
	default:
		return ""
	}
}

func bindFacet(out *Select, s ast.Select, left *catalog.Table) error {
	if len(out.SearchCols) == 0 {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET requires SEARCH")
	}
	if !s.Star {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET requires SELECT *")
	}
	if s.Distinct {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET does not support DISTINCT")
	}
	if len(s.Group) > 0 || s.Having != nil {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET does not support GROUP BY or HAVING")
	}
	if len(s.Joins) > 0 {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET does not support JOIN")
	}
	if s.NearestCol != "" || s.Nearest2Col != "" {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET does not support NEAREST")
	}
	if len(s.Order) > 0 {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET does not support ORDER BY")
	}
	if s.Offset != nil && *s.Offset > 0 {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET does not support OFFSET")
	}
	if len(s.FacetCols) > fulltext.MaxFields {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET exceeds the maximum number of columns")
	}
	seen := make(map[int]struct{}, len(s.FacetCols))
	for _, name := range s.FacetCols {
		ord, ok := left.ColIndex(name)
		if !ok {
			return nerr.New(nerr.NotFound, "sql.binder", "unknown FACET column")
		}
		if _, dup := seen[ord]; dup {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate FACET column")
		}
		if left.Columns[ord].ClientEncrypted() {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET cannot use an ENCRYPTED CLIENT column")
		}
		if !facetable(left.Columns[ord].Type.Kind) {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "FACET requires a STRING, TEXT, DECIMAL, BOOL, UUID, or TIMESTAMPTZ column")
		}
		seen[ord] = struct{}{}
		out.FacetCols = append(out.FacetCols, ord)
		out.FacetNames = append(out.FacetNames, left.Columns[ord].Name)
	}
	out.Star = false
	out.OutCols = []int{-1, -1, -1}
	out.OutExprs = []ast.Expr{ast.Ident{Name: "facet"}, ast.Ident{Name: "value"}, ast.Ident{Name: "count"}}
	out.OutNames = []string{"facet", "value", "count"}
	return nil
}

func facetable(k types.Kind) bool {
	switch k {
	case types.KindString, types.KindText, types.KindChar, types.KindVarchar, types.KindDecimal, types.KindBool, types.KindUUID, types.KindTimestampTZ,
		types.KindInt8, types.KindInt16, types.KindInt32, types.KindInt64,
		types.KindUint8, types.KindUint16, types.KindUint32, types.KindUint64,
		types.KindDate, types.KindTime, types.KindTimestamp, types.KindFloat32, types.KindFloat64, types.KindEnum, types.KindInterval:
		return true
	default:
		return false
	}
}

func bindNearestCol(colName string, query ast.Expr, metric string, left, schema *catalog.Table, joins []ast.JoinSpec, lookup Lookup) (int, error) {
	ord, ok := left.ColIndex(colName)
	if !ok {
		if colOnJoined(colName, joins, lookup) {
			return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "NEAREST column must belong to the FROM table")
		}
		return -1, nerr.New(nerr.NotFound, "sql.binder", "unknown NEAREST column")
	}
	if left.Columns[ord].Type.Kind != types.KindVector {
		return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "NEAREST requires a VECTOR column")
	}
	if query == nil {
		return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "NEAREST is missing a query")
	}
	switch q := query.(type) {
	case ast.Param, ast.VectorLit:
	case ast.Literal:
		if q.Value.Typ.Kind != types.KindVector {
			return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "NEAREST query must be a vector or parameter")
		}
	default:
		if err := checkExpr(query, schema, types.Type{}, false); err != nil {
			return -1, err
		}
	}
	if metric != "" {
		isBit := left.Columns[ord].Type.VecElem == types.VecBit
		isSparse := left.Columns[ord].Type.VecElem == types.VecSparse
		switch metric {
		case "cosine", "inner_product":
			if isBit {
				return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "BITVECTOR NEAREST requires USING HAMMING")
			}
		case "l2":
			if isBit {
				return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "BITVECTOR NEAREST requires USING HAMMING")
			}
			if isSparse {
				return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "SPARSEVECTOR NEAREST does not support USING L2")
			}
		case "hamming":
			if !isBit {
				return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "HAMMING distance requires a BITVECTOR column")
			}
		default:
			return -1, nerr.New(nerr.InvalidArgument, "sql.binder", "unknown NEAREST metric")
		}
	}
	return ord, nil
}

func colOnJoined(name string, joins []ast.JoinSpec, lookup Lookup) bool {
	if lookup == nil || name == "" {
		return false
	}
	for _, js := range joins {
		t, ok := lookup(js.Table)
		if !ok {
			continue
		}
		if _, ok := t.ColIndex(name); ok {
			return true
		}
	}
	return false
}

func aliasOr(alias, name string) string {
	if alias != "" {
		return alias
	}
	return name
}

func qualifyTable(t *catalog.Table, alias string, prefix bool) *catalog.Table {
	c := t.Clone()
	if !prefix {
		return c
	}
	for i := range c.Columns {
		c.Columns[i].Name = alias + "." + t.Columns[i].Name
	}
	return c
}

func mergeTables(left, right *catalog.Table) *catalog.Table {
	out := &catalog.Table{Name: left.Name + "+" + right.Name}
	out.Columns = append(out.Columns, left.Columns...)
	out.Columns = append(out.Columns, right.Columns...)
	out.PK = append([]int(nil), left.PK...)
	return out
}

func rewriteQual(e ast.Expr, schema *catalog.Table) ast.Expr {
	if e == nil || schema == nil {
		return e
	}
	switch x := e.(type) {
	case ast.Ident:
		if _, ok := schema.ColIndex(x.Name); ok {
			return x
		}
		var match string
		for _, c := range schema.Columns {
			if len(c.Name) > len(x.Name)+1 && c.Name[len(c.Name)-len(x.Name)-1:] == "."+x.Name {
				if match != "" {
					return x
				}
				match = c.Name
			}
		}
		if match != "" {
			return ast.Ident{Name: match}
		}
		return x
	case ast.Path:
		if len(x.Parts) >= 2 {
			qual := x.Parts[0] + "." + x.Parts[1]
			if _, ok := schema.ColIndex(qual); ok {
				if len(x.Parts) == 2 {
					return ast.Ident{Name: qual}
				}
				return ast.Path{Parts: append([]string{qual}, x.Parts[2:]...)}
			}
			if len(x.Parts) == 2 {
				if _, ok := schema.ColIndex(x.Parts[1]); ok {
					return ast.Ident{Name: x.Parts[1]}
				}
			}
		}
		return x
	case ast.Unary:
		return ast.Unary{Op: x.Op, Right: rewriteQual(x.Right, schema)}
	case ast.Binary:
		return ast.Binary{Op: x.Op, Left: rewriteQual(x.Left, schema), Right: rewriteQual(x.Right, schema)}
	case ast.InSubquery:
		return ast.InSubquery{Expr: rewriteQual(x.Expr, schema), Query: x.Query, Not: x.Not, ID: x.ID}
	case ast.Between:
		return ast.Between{Expr: rewriteQual(x.Expr, schema), Low: rewriteQual(x.Low, schema), High: rewriteQual(x.High, schema), Not: x.Not}
	case ast.IsNull:
		return ast.IsNull{Expr: rewriteQual(x.Expr, schema), Not: x.Not}
	case ast.Call:
		args := make([]ast.Expr, len(x.Args))
		for i := range x.Args {
			args[i] = rewriteQual(x.Args[i], schema)
		}
		if x.Name == "json_get" && len(args) == 2 {
			id, idOK := args[0].(ast.Ident)
			path, pathOK := args[1].(ast.Literal)
			if idOK && pathOK && !path.Value.Null && (path.Value.Typ.Kind == types.KindString || path.Value.Typ.Kind == types.KindText) {
				parts := constantJSONPath(path.Value.Str)
				if len(parts) > 0 {
					return ast.Path{Parts: append([]string{id.Name}, parts...)}
				}
			}
		}
		return ast.Call{Name: x.Name, Args: args, Star: x.Star}
	case ast.Window:
		fn := rewriteQual(x.Fn, schema)
		call, ok := fn.(ast.Call)
		if !ok {
			call = x.Fn
			args := make([]ast.Expr, len(x.Fn.Args))
			for i := range x.Fn.Args {
				args[i] = rewriteQual(x.Fn.Args[i], schema)
			}
			call.Args = args
		}
		parts := make([]ast.Expr, len(x.Partition))
		for i := range x.Partition {
			parts[i] = rewriteQual(x.Partition[i], schema)
		}
		order := make([]ast.OrderItem, len(x.Order))
		for i := range x.Order {
			order[i] = ast.OrderItem{Expr: rewriteQual(x.Order[i].Expr, schema), Desc: x.Order[i].Desc}
		}
		var frame *ast.Frame
		if x.Frame != nil {
			f := *x.Frame
			f.Start.Offset = rewriteQual(x.Frame.Start.Offset, schema)
			f.End.Offset = rewriteQual(x.Frame.End.Offset, schema)
			frame = &f
		}
		return ast.Window{Fn: call, Partition: parts, Order: order, Frame: frame}
	case ast.Case:
		whens := make([]ast.CaseWhen, len(x.Whens))
		for i, arm := range x.Whens {
			whens[i] = ast.CaseWhen{When: rewriteQual(arm.When, schema), Then: rewriteQual(arm.Then, schema)}
		}
		return ast.Case{Operand: rewriteQual(x.Operand, schema), Whens: whens, Else: rewriteQual(x.Else, schema)}
	default:
		return e
	}
}

func constantJSONPath(path string) []string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	for _, part := range parts {
		if part == "" {
			return nil
		}
	}
	return parts
}

func bindAlter(s ast.AlterTable, lookup Lookup, nextID uint32) (Bound, error) {
	tab, err := mustTable(lookup, s.Table)
	if err != nil {
		return nil, err
	}
	if catalog.ReservedName(tab.Name) && !catalog.IsHistoryTable(tab.Name) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "table name prefix nsql_ is reserved")
	}
	neu := tab.Clone()
	var transfer *catalog.Table
	var dropped []catalog.Index
	oldName, newName := tab.Name, tab.Name
	switch cmd := s.Cmd.(type) {
	case ast.AlterAddColumn:
		if cmd.Column.Primary {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "ALTER TABLE ADD COLUMN cannot add a PRIMARY KEY")
		}
		if _, ok := neu.ColIndex(cmd.Column.Name); ok {
			return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "column already exists")
		}
		col, err := catalog.ColumnFromAST(cmd.Column)
		if err != nil {
			return nil, err
		}
		if col.Type.Kind == types.KindVector && col.NotNull && col.Default.Kind == catalog.DefNone {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "NOT NULL VECTOR column requires a default")
		}
		neu.Columns = append(neu.Columns, col)
		if cmd.Column.References != nil {
			if err := catalog.AddForeignKey(neu, *cmd.Column.References); err != nil {
				return nil, err
			}
			if err := catalog.ValidateForeignKeys(neu, lookup); err != nil {
				return nil, err
			}
		}
	case ast.AlterDropColumn:
		idx, ok := neu.ColIndex(cmd.Name)
		if !ok {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown column")
		}
		if len(neu.Columns) <= 1 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "cannot drop the last column")
		}
		for _, pk := range neu.PK {
			if pk == idx {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "cannot drop a PRIMARY KEY column")
			}
		}
		for _, fk := range neu.ForeignKeys {
			for _, c := range fk.Columns {
				if c == idx {
					return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "column is referenced by a foreign key")
				}
			}
		}
		var keepIdx []catalog.Index
		for _, ix := range neu.Indexes {
			if ix.UsesColumn(idx, neu) {
				dropped = append(dropped, ix)
				continue
			}
			keepIdx = append(keepIdx, remapIndexOrds(ix, idx))
		}
		neu.Indexes = keepIdx
		neu.PK = remapOrds(neu.PK, idx)
		for i := range neu.ForeignKeys {
			neu.ForeignKeys[i].Columns = remapOrds(neu.ForeignKeys[i].Columns, idx)
		}
		neu.Columns = append(neu.Columns[:idx], neu.Columns[idx+1:]...)
	case ast.AlterRenameColumn:
		idx, ok := neu.ColIndex(cmd.Old)
		if !ok {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown column")
		}
		if neu.Columns[idx].ClientEncrypted() && cmd.New != cmd.Old {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "rename of an ENCRYPTED CLIENT column requires client-side decrypt/re-encrypt migration")
		}
		if cmd.New == "" {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "empty column name")
		}
		if cmd.New != cmd.Old {
			if _, exists := neu.ColIndex(cmd.New); exists {
				return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "column already exists")
			}
		}
		if cmd.New != cmd.Old {
			for i, ix := range neu.Indexes {
				neu.Indexes[i] = ix.RenameColumn(cmd.Old, cmd.New)
			}
		}
		neu.Columns[idx].Name = cmd.New
	case ast.AlterRenameTable:
		if tableHasClientEncrypted(neu) && cmd.New != tab.Name {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "rename of a table with ENCRYPTED CLIENT columns requires client-side decrypt/re-encrypt migration")
		}
		if catalog.ReservedName(cmd.New) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "table name prefix nsql_ is reserved")
		}
		if cmd.New != tab.Name {
			if _, ok := lookup(cmd.New); ok {
				return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "table already exists")
			}
		}
		neu.Name = cmd.New
		newName = cmd.New
	case ast.AlterAddConstraint:
		if err := catalog.AddForeignKey(neu, cmd.FK); err != nil {
			return nil, err
		}
		if err := catalog.ValidateForeignKeys(neu, lookup); err != nil {
			return nil, err
		}
	case ast.AlterDropConstraint:
		found := -1
		for i, fk := range neu.ForeignKeys {
			if fk.Name == cmd.Name {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown constraint")
		}
		neu.ForeignKeys = append(neu.ForeignKeys[:found], neu.ForeignKeys[found+1:]...)
	case ast.AlterSetCDCImages:
		switch cmd.Mode {
		case "KEYS":
			neu.CDCImages = catalog.CDCImagesKeys
		case "FULL":
			neu.CDCImages = catalog.CDCImagesFull
		default:
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "invalid CDC image mode")
		}
	case ast.AlterAddPartition:
		if err := addPartitionDescriptor(neu, cmd.Partition); err != nil {
			return nil, err
		}
	case ast.AlterDropPartition:
		if err := dropPartitionDescriptor(neu, cmd.Name); err != nil {
			return nil, err
		}
	case ast.AlterAttachPartition:
		if tableHasClientEncrypted(neu) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "ATTACH PARTITION with ENCRYPTED CLIENT columns requires client-side decrypt/re-encrypt migration")
		}
		source, err := mustTable(lookup, cmd.Partition.Name)
		if err != nil {
			return nil, err
		}
		if source.Name == tab.Name {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "cannot attach a table to itself")
		}
		if err := validateAttachTable(neu, source); err != nil {
			return nil, err
		}
		if err := addPartitionDescriptor(neu, cmd.Partition); err != nil {
			return nil, err
		}
		part := &neu.Partitioning.Partitions[len(neu.Partitioning.Partitions)-1]
		part.HeapMeta = source.HeapMeta
		part.VecMeta = source.VecMeta
		for i := range part.Indexes {
			part.Indexes[i].Meta = source.Indexes[i].Meta
		}
		if err := catalog.ValidatePartitioning(neu); err != nil {
			return nil, err
		}
		transfer = source
	case ast.AlterDetachPartition:
		if tableHasClientEncrypted(neu) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "DETACH PARTITION with ENCRYPTED CLIENT columns requires client-side decrypt/re-encrypt migration")
		}
		if _, exists := lookup(cmd.Name); exists {
			return nil, nerr.New(nerr.AlreadyExists, "sql.binder", "detached table already exists")
		}
		if catalog.ReservedName(cmd.Name) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "table name prefix nsql_ is reserved")
		}
		part, err := partitionByName(tab, cmd.Name)
		if err != nil {
			return nil, err
		}
		if err := dropPartitionDescriptor(neu, cmd.Name); err != nil {
			return nil, err
		}
		transfer, err = detachedPartitionTable(tab, part, nextID)
		if err != nil {
			return nil, err
		}
	default:
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "unsupported ALTER TABLE command")
	}
	return AlterTable{
		Table:          tab,
		Result:         neu,
		Transfer:       transfer,
		OldName:        oldName,
		NewName:        newName,
		Kind:           s.Cmd,
		DroppedIndexes: dropped,
	}, nil
}

func validateAttachTable(parent, source *catalog.Table) error {
	if parent == nil || parent.Partitioning == nil || source == nil {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "invalid ATTACH PARTITION table")
	}
	if source.Partitioning != nil {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "attached table must be unpartitioned")
	}
	if len(source.ForeignKeys) != 0 {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "attached table must not have foreign keys")
	}
	if !reflect.DeepEqual(parent.Columns, source.Columns) || !reflect.DeepEqual(parent.PK, source.PK) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "attached table schema does not match parent")
	}
	if len(parent.Indexes) != len(source.Indexes) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "attached table indexes do not match parent")
	}
	for i := range parent.Indexes {
		want := parent.Indexes[i]
		got := source.Indexes[i]
		want.Meta = 0
		got.Meta = 0
		if !reflect.DeepEqual(want, got) {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "attached table indexes do not match parent")
		}
	}
	return nil
}

func partitionByName(tab *catalog.Table, name string) (catalog.Partition, error) {
	if tab == nil || tab.Partitioning == nil {
		return catalog.Partition{}, nerr.New(nerr.InvalidArgument, "sql.binder", "table is not partitioned")
	}
	for _, part := range tab.Partitioning.Partitions {
		if part.Name == name {
			return part, nil
		}
	}
	return catalog.Partition{}, nerr.New(nerr.NotFound, "sql.binder", "unknown partition")
}

func detachedPartitionTable(parent *catalog.Table, part catalog.Partition, id uint32) (*catalog.Table, error) {
	if id == 0 || parent == nil {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "invalid detached table identity")
	}
	out := parent.Clone()
	out.ID = id
	out.Name = part.Name
	out.HeapMeta = part.HeapMeta
	out.VecMeta = part.VecMeta
	out.Partitioning = nil
	out.ForeignKeys = nil
	if len(out.Indexes) != len(part.Indexes) {
		return nil, nerr.New(nerr.Corruption, "sql.binder", "partition-local index metadata mismatch")
	}
	for i := range out.Indexes {
		if out.Indexes[i].Name != part.Indexes[i].Name || part.Indexes[i].Meta == 0 {
			return nil, nerr.New(nerr.Corruption, "sql.binder", "partition-local index metadata mismatch")
		}
		out.Indexes[i].Meta = part.Indexes[i].Meta
	}
	return out, nil
}

func addPartitionDescriptor(tab *catalog.Table, def ast.PartitionDef) error {
	if tab == nil || tab.Partitioning == nil {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "table is not partitioned")
	}
	p := tab.Partitioning
	if len(p.Partitions) >= catalog.MaxPartitions {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "partition count")
	}
	if def.Name == "" || len(def.Name) > catalog.MaxPartitionNameLength {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "partition name")
	}
	var maxID uint32
	for _, part := range p.Partitions {
		if part.Name == def.Name {
			return nerr.New(nerr.AlreadyExists, "sql.binder", "partition already exists")
		}
		if part.ID > maxID {
			maxID = part.ID
		}
	}
	nextID := p.NextID
	if nextID == 0 {
		nextID = maxID + 1
	}
	if maxID == ^uint32(0) || nextID == 0 || nextID <= maxID || nextID == ^uint32(0) {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "partition identity exhausted")
	}
	part := catalog.Partition{ID: nextID, Name: def.Name, HeapMeta: 1}
	p.NextID = nextID + 1
	if tab.HasVector() {
		part.VecMeta = 1
	}
	for _, idx := range tab.Indexes {
		part.Indexes = append(part.Indexes, catalog.PartitionIndex{Name: idx.Name, Meta: 1})
	}
	want := make([]types.Type, len(p.Columns))
	for i, ord := range p.Columns {
		want[i] = tab.Columns[ord].Type
	}
	switch p.Kind {
	case catalog.PartitionRange:
		if def.Rule != "RANGE" {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "RANGE table requires VALUES LESS THAN")
		}
		last := p.Partitions[len(p.Partitions)-1]
		if len(last.Values) != 2 || last.Values[1] == nil {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "cannot append after MAXVALUE partition")
		}
		lower := make([]types.Value, len(last.Values[1]))
		for i := range last.Values[1] {
			lower[i] = last.Values[1][i].Clone()
		}
		var upper []types.Value
		if upperExprs := rangeUpperExprs(def); upperExprs != nil {
			value, err := evalPartitionTuple(upperExprs, want)
			if err != nil {
				return err
			}
			upper = value
		}
		part.Values = [][]types.Value{lower, upper}
		part.LowerInclusive = true
	case catalog.PartitionList:
		tuples := listValueTuples(def)
		if def.Rule != "VALUE" || len(tuples) == 0 {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "LIST table requires VALUES IN")
		}
		for _, exprs := range tuples {
			value, err := evalPartitionTuple(exprs, want)
			if err != nil {
				return err
			}
			part.Values = append(part.Values, value)
		}
	case catalog.PartitionLegacyTenant:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "legacy TENANT partitions cannot be extended; migrate to an isolated hosted database")
	case catalog.PartitionHash:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "HASH partition membership changes require redistribution")
	default:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "unknown partition kind")
	}
	p.Partitions = append(p.Partitions, part)
	return catalog.ValidatePartitioning(tab)
}

func dropPartitionDescriptor(tab *catalog.Table, name string) error {
	if tab == nil || tab.Partitioning == nil {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "table is not partitioned")
	}
	if tab.Partitioning.Kind == catalog.PartitionHash {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "HASH partition membership changes require redistribution")
	}
	if len(tab.Partitioning.Partitions) <= 1 {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "cannot drop the final partition")
	}
	pos := -1
	for i, part := range tab.Partitioning.Partitions {
		if part.Name == name {
			pos = i
			break
		}
	}
	if pos < 0 {
		return nerr.New(nerr.NotFound, "sql.binder", "unknown partition")
	}
	tab.Partitioning.Partitions = append(tab.Partitioning.Partitions[:pos], tab.Partitioning.Partitions[pos+1:]...)
	return catalog.ValidatePartitioning(tab)
}

func attachPartitioning(t *catalog.Table, spec *ast.PartitionSpec) error {
	if spec == nil {
		return nil
	}
	if len(spec.Columns) == 0 || len(spec.Columns) > catalog.MaxPartitionColumns {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "partition column count")
	}
	colOrds := make([]int, len(spec.Columns))
	seen := make(map[int]struct{})
	for i, name := range spec.Columns {
		ord, ok := t.ColIndex(name)
		if !ok {
			return nerr.New(nerr.NotFound, "sql.binder", "partition column missing")
		}
		if _, dup := seen[ord]; dup {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate partition column")
		}
		seen[ord] = struct{}{}
		if t.Columns[ord].ClientEncrypted() {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "ENCRYPTED CLIENT column cannot be a partition key")
		}
		if t.Columns[ord].Type.Kind == types.KindVector {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "VECTOR partition key")
		}
		colOrds[i] = ord
	}
	typesForCols := make([]types.Type, len(colOrds))
	for i, ord := range colOrds {
		typesForCols[i] = t.Columns[ord].Type
	}
	var kind catalog.PartitionKind
	switch spec.Kind {
	case "RANGE":
		kind = catalog.PartitionRange
	case "HASH":
		kind = catalog.PartitionHash
	case "LIST":
		kind = catalog.PartitionList
	case "TENANT":
		return nerr.New(nerr.InvalidArgument, "sql.binder", "PARTITION BY TENANT was removed; use an isolated hosted database")
	default:
		return nerr.New(nerr.InvalidArgument, "sql.binder", "unknown partition kind")
	}
	// Multi-column HASH keys route on the SHA-256 digest of the canonical typed
	// tuple (see catalog.HashPartitionRemainder). Multi-column RANGE keys compare
	// lexicographically ordered bound tuples (VALUES LESS THAN (a, b, ...)) and
	// multi-column LIST keys match membership tuples (VALUES IN ((a, b), ...)).
	if len(spec.Partitions) == 0 || len(spec.Partitions) > catalog.MaxPartitions {
		return nerr.New(nerr.InvalidArgument, "sql.binder", "partition count")
	}
	seenNames := make(map[string]struct{}, len(spec.Partitions))
	seenIDs := make(map[uint32]struct{})
	var partitions []catalog.Partition
	for idx, pd := range spec.Partitions {
		if pd.Name == "" || len(pd.Name) > catalog.MaxPartitionNameLength {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "partition name")
		}
		if _, dup := seenNames[pd.Name]; dup {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate partition name")
		}
		seenNames[pd.Name] = struct{}{}
		pid := uint32(idx + 1)
		if _, dup := seenIDs[pid]; dup {
			return nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate partition identity")
		}
		seenIDs[pid] = struct{}{}
		part := catalog.Partition{ID: pid, Name: pd.Name, HeapMeta: 1, VecMeta: 0}
		if t.HasVector() {
			part.VecMeta = 1
		}
		switch kind {
		case catalog.PartitionRange:
			// Build RANGE bounds: each partition has an upper bound (VALUES LESS
			// THAN), and its lower bound is the previous partition's upper bound.
			// A nil upper means MAXVALUE (unbounded). Single-column keys carry a
			// one-element tuple; multi-column keys carry one literal per column.
			upperExprs := rangeUpperExprs(pd)
			var upper []types.Value
			if upperExprs != nil {
				v, err := evalPartitionTuple(upperExprs, typesForCols)
				if err != nil {
					return err
				}
				upper = v
			}
			var lower []types.Value
			if idx > 0 {
				prevExprs := rangeUpperExprs(spec.Partitions[idx-1])
				if prevExprs == nil {
					// previous partition was MAXVALUE; nothing can follow it.
					return nerr.New(nerr.InvalidArgument, "sql.binder", "RANGE partition after MAXVALUE")
				}
				lv, err := evalPartitionTuple(prevExprs, typesForCols)
				if err != nil {
					return err
				}
				lower = lv
			}
			// RANGE: (-inf, upper) for first partition, [prevUpper, upper) for rest. Upper exclusive.
			if lower != nil {
				part.LowerInclusive = true
			} else {
				part.LowerInclusive = false
			}
			part.UpperInclusive = false
			// store as two-value slice: [lower, upper] where nil is unbounded
			part.Values = [][]types.Value{lower, upper}
		case catalog.PartitionHash:
			part.Modulus = pd.Modulus
			part.Remainder = pd.Remainder
		case catalog.PartitionList:
			tuples := listValueTuples(pd)
			if len(tuples) == 0 {
				return nerr.New(nerr.InvalidArgument, "sql.binder", "LIST partition requires at least one value")
			}
			for _, exprs := range tuples {
				value, err := evalPartitionTuple(exprs, typesForCols)
				if err != nil {
					return err
				}
				part.Values = append(part.Values, value)
			}
		case catalog.PartitionLegacyTenant:
			return nerr.New(nerr.InvalidArgument, "sql.binder", "legacy TENANT partitions cannot be created")
		}
		partitions = append(partitions, part)
	}
	t.Partitioning = &catalog.Partitioning{Kind: kind, NextID: uint32(len(partitions) + 1), Columns: colOrds, Partitions: partitions}
	return nil
}

// evalPartitionExpr evaluates a single-column partition bound/value literal.
func evalPartitionExpr(expr ast.Expr, want []types.Type) ([]types.Value, error) {
	return evalPartitionTuple([]ast.Expr{expr}, want)
}

// evalPartitionTuple evaluates a partition bound/value tuple against the ordered
// partition-key column types. Single-column keys pass a one-element tuple;
// multi-column RANGE/LIST keys pass one literal per partition column.
func evalPartitionTuple(exprs []ast.Expr, want []types.Type) ([]types.Value, error) {
	if len(exprs) != len(want) {
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "partition tuple arity")
	}
	out := make([]types.Value, len(exprs))
	for i, expr := range exprs {
		val, err := literalValue(expr, want[i])
		if err != nil {
			return nil, err
		}
		if val.Null {
			return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "NULL partition value")
		}
		if !val.Typ.Equals(want[i]) {
			cv, cerr := types.Coerce(val, want[i])
			if cerr != nil {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "partition value type")
			}
			val = cv
		}
		out[i] = val
	}
	return out, nil
}

// rangeUpperExprs returns the ordered RANGE upper-bound expressions for a
// partition definition, or nil for MAXVALUE (unbounded upper).
func rangeUpperExprs(def ast.PartitionDef) []ast.Expr {
	if len(def.LessThanTuple) > 0 {
		return def.LessThanTuple
	}
	if def.LessThan != nil {
		return []ast.Expr{def.LessThan}
	}
	return nil
}

// listValueTuples returns the ordered LIST membership tuples for a partition
// definition, normalizing single-column VALUES IN (...) into one-element tuples.
func listValueTuples(def ast.PartitionDef) [][]ast.Expr {
	if len(def.ValueTuples) > 0 {
		return def.ValueTuples
	}
	out := make([][]ast.Expr, 0, len(def.Values))
	for _, expr := range def.Values {
		out = append(out, []ast.Expr{expr})
	}
	return out
}

func literalValue(expr ast.Expr, hint types.Type) (types.Value, error) {
	switch x := expr.(type) {
	case ast.Literal:
		return types.Coerce(x.Value, hint)
	case ast.Unary:
		if x.Op == "-" {
			inner, ok := x.Right.(ast.Literal)
			if !ok {
				return types.Value{}, nerr.New(nerr.InvalidArgument, "sql.binder", "partition value must be a literal")
			}
			if inner.Value.Typ.Kind != types.KindDecimal {
				return types.Value{}, nerr.New(nerr.InvalidArgument, "sql.binder", "invalid partition literal")
			}
			neg := inner.Value.Dec.Negate()
			v := types.DecimalValue(neg, hint)
			if hint.Kind != types.KindDecimal {
				cv, err := types.Coerce(v, hint)
				if err != nil {
					return types.Value{}, err
				}
				return cv, nil
			}
			return v, nil
		}
		return types.Value{}, nerr.New(nerr.InvalidArgument, "sql.binder", "invalid partition literal")
	case ast.Ident:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "sql.binder", "partition value must be a literal")
	default:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "sql.binder", "partition value must be a literal")
	}
}

func remapOrds(ords []int, dropped int) []int {
	out := make([]int, 0, len(ords))
	for _, o := range ords {
		if o == dropped {
			continue
		}
		if o > dropped {
			out = append(out, o-1)
		} else {
			out = append(out, o)
		}
	}
	return out
}

func remapIndexOrds(idx catalog.Index, dropped int) catalog.Index {
	idx.Columns = remapOrds(idx.Columns, dropped)
	idx.Include = remapOrds(idx.Include, dropped)
	return idx
}
