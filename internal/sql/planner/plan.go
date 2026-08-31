package planner

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/txn"
)

// Logical is a plan node. Phase 6 rewrites and physical choice stay in this type set.
type Logical interface{ logical() }

type (
	CreateTable struct {
		Table *catalog.Table
	}
	CreateWorkflow struct {
		Workflow    *catalog.Workflow
		IfNotExists bool
		Existing    bool
	}
	RunWorkflow struct {
		Workflow *catalog.Workflow
		Args     []ast.Expr
	}
	AlterWorkflow struct {
		Workflow *catalog.Workflow
		Result   *catalog.Workflow
	}
	DropWorkflow struct {
		Workflow *catalog.Workflow
		IfExists bool
	}
	CreateTrigger struct {
		Trigger     *catalog.Trigger
		IfNotExists bool
		Existing    bool
	}
	AlterTrigger struct {
		Trigger *catalog.Trigger
		Result  *catalog.Trigger
	}
	DropTrigger struct {
		Trigger  *catalog.Trigger
		IfExists bool
	}
	CreateSchedule struct {
		Schedule    *catalog.Schedule
		IfNotExists bool
		Existing    bool
	}
	AlterSchedule struct {
		Schedule *catalog.Schedule
		Result   *catalog.Schedule
	}
	DropSchedule struct {
		Schedule *catalog.Schedule
		IfExists bool
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
	CreateDatabase struct {
		Name        string
		IfNotExists bool
	}
	DropTable struct {
		Name     string
		Table    *catalog.Table
		IfExists bool
	}
	DropIndex struct {
		Name     string
		Table    *catalog.Table
		Index    catalog.Index
		IfExists bool
	}
	RebuildIndex struct {
		Table *catalog.Table
		Index catalog.Index
	}
	AlterTable struct {
		Table          *catalog.Table
		Result         *catalog.Table
		Transfer       *catalog.Table
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
		Returning binder.Returning
	}
	Upsert struct {
		Table      *catalog.Table
		Columns    []int
		Rows       [][]ast.Expr
		UniqueCols []int
		UniquePK   bool
		UniqueIdx  string
		Sets       []binder.Set
		DefaultSet bool
		Returning  binder.Returning
	}
	SetOperation struct {
		Left, Right Logical
		Op          string
		All         bool
		Names       []string
	}
	Scan struct {
		Table  *catalog.Table
		Needed []int // nil means every column
	}
	SeqScan struct {
		Table      *catalog.Table
		Needed     []int
		Segments   []SegmentSpan // empty means the whole heap
		Partitions []uint32      // nil means all; non-nil is the pruned stable-ID set
	}
	IndexScan struct {
		Table      *catalog.Table
		IndexName  string
		PK         bool
		Unique     bool
		Spatial    bool
		Columns    []int
		Low        []types.Value
		High       []types.Value
		LowIncl    bool
		HighIncl   bool
		GeoStart   []byte
		GeoEnd     []byte
		Residual   ast.Expr
		Needed     []int
		IndexOnly  bool
		Partitions []uint32 // nil means all; non-nil is the pruned stable-ID set
	}
	Search struct {
		Input     Logical
		Table     *catalog.Table
		IndexName string
		Columns   []int
		Weights   []float64 // parallel to Columns; nil means all 1
		Query     ast.Expr
		Residual  ast.Expr
		Needed    []int
	}
	// Facet counts independent histograms of Columns over SEARCH matches.
	// Output is (facet STRING, value STRING, count DECIMAL). Limit is
	// per-facet top-N (-1 = all values up to MaxFacetValues).
	Facet struct {
		Input   Logical
		Table   *catalog.Table
		Columns []int
		Names   []string // parallel to Columns
		Limit   int64
	}
	Nearest struct {
		Input      Logical
		Table      *catalog.Table
		IndexName  string
		Column     int
		Query      ast.Expr
		Metric     string
		Residual   ast.Expr
		Needed     []int
		K          int64
		Partitions []uint32 // nil means all; non-nil is the pruned stable-ID set
	}
	// Candidates is hybrid candidate generation (HNSW, flat ANN, or full-text).
	Candidates struct {
		Input      Logical
		Table      *catalog.Table
		Kind       string // "hnsw", "flat", "fulltext"
		IndexName  string
		Column     int
		Query      ast.Expr
		Metric     string
		Residual   ast.Expr
		Needed     []int
		K          int64
		Partitions []uint32 // nil means all; non-nil is the pruned stable-ID set
	}
	// Rerank fuses BM25 and vector ranks over a candidate set and keeps top K.
	Rerank struct {
		Input         Logical
		Extra         []Logical // additional retrievers; unioned with Input before scoring
		Table         *catalog.Table
		SearchCols    []int
		SearchWeights []float64
		SearchQuery   ast.Expr
		NearestCol    int
		NearestQuery  ast.Expr
		Metric        string
		SparseCol     int // -1 if absent
		SparseQuery   ast.Expr
		SparseMetric  string
		K             int64
		Method        string // "bm25+vector", "vector+sparse", "bm25+vector+sparse"
		Strategy      string // "filter-ann", "ann-filter", "search-ann", "fusion"
	}
	SegmentSpan struct {
		ID        int
		Low, High []types.Value
		LowIncl   bool
		HighIncl  bool
	}
	Filter struct {
		Input Logical
		Pred  ast.Expr
	}
	Project struct {
		Input         Logical
		Cols          []int
		Exprs         []ast.Expr
		Names         []string
		Distinct      bool
		DistinctIndex string
	}
	Limit struct {
		Input  Logical
		N      int64 // -1 = no LIMIT (OFFSET only)
		Offset int64
	}
	SortKey struct {
		Col  int
		Desc bool
	}
	Sort struct {
		Input           Logical
		Keys            []SortKey
		Hidden          int
		OrderedDistinct bool
		TopN            int64
	}
	Join struct {
		Left, Right Logical
		Pred        ast.Expr
		Kind        ast.JoinKind
		Cross       bool
		Method      string // "hash", "merge"
		LeftKeys    []int
		RightKeys   []int
		Schema      *catalog.Table
	}
	Aggregate struct {
		Input    Logical
		Groups   []int
		Specs    []AggSpec
		Exprs    []ast.Expr
		Names    []string
		Schema   *catalog.Table
		Distinct bool
		Having   ast.Expr
	}
	AggSpec struct {
		Fun  string
		Col  int
		Star bool
	}
	Window struct {
		Input  Logical
		Specs  []WindowSpec
		Schema *catalog.Table
	}
	WindowSpec struct {
		Fun       string
		Args      []ast.Expr
		Star      bool
		Partition []ast.Expr
		Order     []ast.OrderItem
		Frame     ast.Frame
		Result    string
		OutType   types.Type
	}
	Empty struct {
		Names []string
	}
	Update struct {
		Input     Logical
		Table     *catalog.Table
		Sets      []binder.Set
		Limit     int64
		Returning binder.Returning
	}
	Delete struct {
		Input     Logical
		Table     *catalog.Table
		Limit     int64
		Returning binder.Returning
	}
	Begin struct {
		Iso txn.Isolation
	}
	Commit   struct{}
	Rollback struct{}
	Explain  struct {
		Input   Logical
		Analyze bool
	}
	Analyze struct {
		Table *catalog.Table
	}
	Maintain struct {
		Table *catalog.Table
		Index string
	}
	With struct {
		CTEs  []CTE
		Query Logical
	}
	CTE struct {
		Name        string
		ID          uint64
		Input       Logical
		Anchor      Logical
		Recursive   Logical
		Materialize bool
		ForceInline bool
		RecursiveOn bool
		Distinct    bool
		Names       []string
		Schema      *catalog.Table
		EstRows     int64
	}
	CTEScan struct {
		Name    string
		ID      uint64
		Names   []string
		Schema  *catalog.Table
		EstRows int64
	}
)

func (CreateTable) logical()    {}
func (CreateWorkflow) logical() {}
func (RunWorkflow) logical()    {}
func (AlterWorkflow) logical()  {}
func (DropWorkflow) logical()   {}
func (CreateTrigger) logical()  {}
func (AlterTrigger) logical()   {}
func (DropTrigger) logical()    {}
func (CreateSchedule) logical() {}
func (AlterSchedule) logical()  {}
func (DropSchedule) logical()   {}
func (ShowTasks) logical()      {}
func (CancelTask) logical()     {}
func (Subscribe) logical()      {}
func (CreateDatabase) logical() {}
func (DropTable) logical()      {}
func (DropIndex) logical()      {}
func (RebuildIndex) logical()   {}
func (AlterTable) logical()     {}
func (CreateIndex) logical()    {}
func (Insert) logical()         {}
func (Upsert) logical()         {}
func (SetOperation) logical()   {}
func (Scan) logical()           {}
func (SeqScan) logical()        {}
func (IndexScan) logical()      {}
func (Search) logical()         {}
func (Facet) logical()          {}
func (Nearest) logical()        {}
func (Candidates) logical()     {}
func (Rerank) logical()         {}
func (Filter) logical()         {}
func (Project) logical()        {}
func (Limit) logical()          {}
func (Sort) logical()           {}
func (Join) logical()           {}
func (Aggregate) logical()      {}
func (Window) logical()         {}
func (Empty) logical()          {}
func (Update) logical()         {}
func (Delete) logical()         {}
func (Begin) logical()          {}
func (Commit) logical()         {}
func (Rollback) logical()       {}
func (Explain) logical()        {}
func (Analyze) logical()        {}
func (Maintain) logical()       {}
func (With) logical()           {}
func (CTEScan) logical()        {}

func applySubjoins(p Logical, joins []binder.BoundSubjoin) (Logical, error) {
	for _, j := range joins {
		right, err := Plan(j.Right)
		if err != nil {
			return nil, err
		}
		p = Join{Left: p, Right: right, Pred: j.Pred, Kind: j.Kind, Schema: j.Schema}
	}
	return p, nil
}

// Fetch is N+Offset input rows a Limit must consume, or -1 if unbounded.
func (l Limit) Fetch() int64 {
	if l.N < 0 {
		return -1
	}
	if l.Offset <= 0 {
		return l.N
	}
	n := l.N + l.Offset
	if n < l.N {
		return -1
	}
	return n
}

func MergeLimit(outer, inner Limit) Limit {
	skip := inner.Offset + outer.Offset
	n := outer.N
	if inner.N >= 0 {
		remain := inner.N - outer.Offset
		if remain < 0 {
			remain = 0
		}
		if n < 0 || remain < n {
			n = remain
		}
	}
	return Limit{Input: inner.Input, N: n, Offset: skip}
}

func Plan(b binder.Bound) (Logical, error) {
	switch s := b.(type) {
	case binder.With:
		ctes := make([]CTE, 0, len(s.CTEs))
		for _, c := range s.CTEs {
			pc := CTE{
				Name:        c.Name,
				ID:          c.ID,
				Materialize: c.Materialize == ast.CTEAlways || c.Recursive,
				ForceInline: c.Materialize == ast.CTENever && !c.Recursive,
				RecursiveOn: c.Recursive,
				Distinct:    c.Distinct,
				Names:       append([]string(nil), c.Names...),
				Schema:      c.Schema,
			}
			if c.Recursive {
				anchor, err := Plan(c.Anchor)
				if err != nil {
					return nil, err
				}
				rec, err := Plan(c.RecursiveQ)
				if err != nil {
					return nil, err
				}
				pc.Anchor = renameCTE(anchor, c.Names)
				pc.Recursive = renameCTE(rec, c.Names)
			} else {
				in, err := Plan(c.Query)
				if err != nil {
					return nil, err
				}
				pc.Input = renameCTE(in, c.Names)
			}
			ctes = append(ctes, pc)
		}
		query, err := Plan(s.Query)
		if err != nil {
			return nil, err
		}
		return With{CTEs: ctes, Query: query}, nil
	case binder.CTERef:
		names := make([]string, 0)
		if s.Schema != nil {
			names = make([]string, len(s.Schema.Columns))
			for i, c := range s.Schema.Columns {
				names[i] = c.Name
			}
		}
		return CTEScan{Name: s.Name, ID: s.ID, Names: names, Schema: s.Schema}, nil
	case binder.SetOperation:
		left, err := Plan(s.Left)
		if err != nil {
			return nil, err
		}
		right, err := Plan(s.Right)
		if err != nil {
			return nil, err
		}
		return SetOperation{Left: left, Right: right, Op: s.Op, All: s.All, Names: append([]string(nil), s.Names...)}, nil
	case binder.CreateTable:
		return CreateTable{Table: s.Table}, nil
	case binder.CreateWorkflow:
		return CreateWorkflow{Workflow: s.Workflow, IfNotExists: s.IfNotExists, Existing: s.Existing}, nil
	case binder.RunWorkflow:
		return RunWorkflow{Workflow: s.Workflow, Args: append([]ast.Expr(nil), s.Args...)}, nil
	case binder.AlterWorkflow:
		return AlterWorkflow{Workflow: s.Workflow, Result: s.Result}, nil
	case binder.DropWorkflow:
		return DropWorkflow{Workflow: s.Workflow, IfExists: s.IfExists}, nil
	case binder.CreateTrigger:
		return CreateTrigger{Trigger: s.Trigger, IfNotExists: s.IfNotExists, Existing: s.Existing}, nil
	case binder.AlterTrigger:
		return AlterTrigger{Trigger: s.Trigger, Result: s.Result}, nil
	case binder.DropTrigger:
		return DropTrigger{Trigger: s.Trigger, IfExists: s.IfExists}, nil
	case binder.CreateSchedule:
		return CreateSchedule{Schedule: s.Schedule, IfNotExists: s.IfNotExists, Existing: s.Existing}, nil
	case binder.AlterSchedule:
		return AlterSchedule{Schedule: s.Schedule, Result: s.Result}, nil
	case binder.DropSchedule:
		return DropSchedule{Schedule: s.Schedule, IfExists: s.IfExists}, nil
	case binder.ShowTasks:
		return ShowTasks{After: s.After, Limit: s.Limit}, nil
	case binder.CancelTask:
		return CancelTask{ID: s.ID}, nil
	case binder.Subscribe:
		return Subscribe{Table: s.Table, Operation: s.Operation, After: s.After}, nil
	case binder.CreateDatabase:
		return CreateDatabase{Name: s.Name, IfNotExists: s.IfNotExists}, nil
	case binder.DropTable:
		return DropTable{Name: s.Name, Table: s.Table, IfExists: s.IfExists}, nil
	case binder.DropIndex:
		return DropIndex{Name: s.Name, Table: s.Table, Index: s.Index, IfExists: s.IfExists}, nil
	case binder.RebuildIndex:
		return RebuildIndex{Table: s.Table, Index: s.Index}, nil
	case binder.AlterTable:
		return AlterTable{
			Table:          s.Table,
			Result:         s.Result,
			Transfer:       s.Transfer,
			OldName:        s.OldName,
			NewName:        s.NewName,
			Kind:           s.Kind,
			DroppedIndexes: s.DroppedIndexes,
		}, nil
	case binder.CreateIndex:
		return CreateIndex{Table: s.Table, Index: s.Index}, nil
	case binder.Insert:
		return Insert{Table: s.Table, Columns: s.Columns, Rows: s.Rows, Returning: s.Returning}, nil
	case binder.Upsert:
		return Upsert{
			Table:      s.Table,
			Columns:    s.Columns,
			Rows:       s.Rows,
			UniqueCols: s.UniqueCols,
			UniquePK:   s.UniquePK,
			UniqueIdx:  s.UniqueIdx,
			Sets:       s.Sets,
			DefaultSet: s.DefaultSet,
			Returning:  s.Returning,
		}, nil
	case binder.Select:
		schema := s.Schema
		if schema == nil {
			schema = s.Table
		}
		hasRank := (s.SearchQuery != nil && len(s.SearchCols) > 0) || (s.NearestQuery != nil && s.NearestCol >= 0) || (s.Nearest2Query != nil && s.Nearest2Col >= 0)
		rank := s.Table
		if hasRank && len(s.Joins) > 0 {
			rank = qualifyRankTable(s.Table, schema)
		}
		var p Logical
		if s.Input != nil {
			var err error
			p, err = Plan(s.Input)
			if err != nil {
				return nil, err
			}
		} else {
			p = Scan{Table: rank}
		}
		if s.Where != nil && len(s.Joins) == 0 {
			p = Filter{Input: p, Pred: s.Where}
		}
		if len(s.Joins) == 0 {
			var err error
			p, err = applySubjoins(p, s.Subjoins)
			if err != nil {
				return nil, err
			}
		}
		if s.SearchQuery != nil && len(s.SearchCols) > 0 {
			p = Search{Input: p, Table: rank, Columns: append([]int(nil), s.SearchCols...), Weights: append([]float64(nil), s.SearchWeights...), Query: s.SearchQuery}
		}
		if len(s.FacetCols) > 0 {
			lim := int64(-1)
			if s.Limit != nil {
				lim = *s.Limit
			}
			p = Facet{
				Input:   p,
				Table:   rank,
				Columns: append([]int(nil), s.FacetCols...),
				Names:   append([]string(nil), s.FacetNames...),
				Limit:   lim,
			}
			return p, nil
		}
		if s.NearestQuery != nil && s.NearestCol >= 0 {
			p = Nearest{Input: p, Table: rank, Column: s.NearestCol, Query: s.NearestQuery, Metric: s.NearestMetric}
		}
		if s.Nearest2Query != nil && s.Nearest2Col >= 0 {
			p = Nearest{Input: p, Table: rank, Column: s.Nearest2Col, Query: s.Nearest2Query, Metric: s.Nearest2Metric}
		}
		if n := len(s.Joins); n > 0 {
			ncol := len(s.Table.Columns)
			for i, j := range s.Joins {
				ncol += len(j.Table.Columns)
				js := schema
				if i < n-1 {
					js = schemaPrefix(schema, ncol)
				}
				var right Logical
				if j.Input != nil {
					var err error
					right, err = Plan(j.Input)
					if err != nil {
						return nil, err
					}
				} else {
					right = Scan{Table: j.Table}
				}
				cross := j.Kind == ast.JoinCross || (j.On == nil && j.Kind != ast.JoinLeft && j.Kind != ast.JoinRight && j.Kind != ast.JoinFull)
				p = Join{Left: p, Right: right, Pred: j.On, Kind: j.Kind, Cross: cross, Schema: js}
			}
		}
		if s.Where != nil && len(s.Joins) > 0 {
			p = Filter{Input: p, Pred: s.Where}
		}
		if len(s.Joins) > 0 {
			var err error
			p, err = applySubjoins(p, s.Subjoins)
			if err != nil {
				return nil, err
			}
		}
		distinctIndex, indexDistinct := uniqueDistinctKey(s)
		orderedDistinct := s.Distinct && !indexDistinct && orderCoversOutput(s.Order, len(s.OutNames))
		if s.HasAgg {
			var groups []int
			for _, g := range s.Groups {
				if id, ok := g.(ast.Ident); ok {
					if i, found := schema.ColIndex(id.Name); found {
						groups = append(groups, i)
					}
				}
			}
			specs := make([]AggSpec, len(s.Aggs))
			for i, a := range s.Aggs {
				specs[i] = AggSpec{Fun: a.Fun, Col: a.Col, Star: a.Star}
			}
			aggExprs, aggNames, aggSchema := s.OutExprs, s.OutNames, schema
			if len(s.Windows) > 0 && len(s.AggExprs) > 0 {
				aggExprs, aggNames, aggSchema = s.AggExprs, s.AggNames, s.AggSchema
			}
			distinct := s.Distinct && !orderedDistinct && len(s.Windows) == 0
			p = Aggregate{Input: p, Groups: groups, Specs: specs, Exprs: aggExprs, Names: aggNames, Schema: aggSchema, Distinct: distinct, Having: s.Having}
		} else if len(s.Windows) == 0 {
			p = Project{Input: p, Cols: s.OutCols, Exprs: s.OutExprs, Names: s.OutNames, Distinct: s.Distinct && !orderedDistinct && !indexDistinct, DistinctIndex: distinctIndex}
		}
		if len(s.Windows) > 0 {
			winSchema := windowOutputSchema(p, s.Windows)
			specs := make([]WindowSpec, len(s.Windows))
			for i, w := range s.Windows {
				specs[i] = WindowSpec{Fun: w.Fun, Args: w.Args, Star: w.Star, Partition: w.Partition, Order: w.Order, Frame: w.Frame, Result: w.Result, OutType: w.OutType}
			}
			p = Window{Input: p, Specs: specs, Schema: winSchema}
			p = Project{Input: p, Cols: s.OutCols, Exprs: s.OutExprs, Names: s.OutNames, Distinct: s.Distinct && !orderedDistinct && !indexDistinct, DistinctIndex: distinctIndex}
		}
		if len(s.Order) > 0 {
			keys := make([]SortKey, len(s.Order))
			for i, k := range s.Order {
				keys[i] = SortKey{Col: k.Col, Desc: k.Desc}
			}
			p = Sort{Input: p, Keys: keys, Hidden: s.Hidden, OrderedDistinct: orderedDistinct}
		}
		if s.Limit != nil || (s.Offset != nil && *s.Offset > 0) {
			n := int64(-1)
			if s.Limit != nil {
				n = *s.Limit
			}
			off := int64(0)
			if s.Offset != nil {
				off = *s.Offset
			}
			p = Limit{Input: p, N: n, Offset: off}
		}
		return p, nil
	case binder.Update:
		var p Logical = Scan{Table: s.Table}
		if s.Where != nil {
			p = Filter{Input: p, Pred: s.Where}
		}
		return Update{Input: p, Table: s.Table, Sets: s.Sets, Limit: s.Limit, Returning: s.Returning}, nil
	case binder.Delete:
		var p Logical = Scan{Table: s.Table}
		if s.Where != nil {
			p = Filter{Input: p, Pred: s.Where}
		}
		return Delete{Input: p, Table: s.Table, Limit: s.Limit, Returning: s.Returning}, nil
	case binder.Begin:
		return Begin{Iso: s.Iso}, nil
	case binder.Commit:
		return Commit{}, nil
	case binder.Rollback:
		return Rollback{}, nil
	case binder.Explain:
		inner, err := Plan(s.Stmt)
		if err != nil {
			return nil, err
		}
		return Explain{Input: inner, Analyze: s.Analyze}, nil
	case binder.Analyze:
		return Analyze{Table: s.Table}, nil
	case binder.Maintain:
		return Maintain{Table: s.Table, Index: s.Index}, nil
	default:
		return nil, nil
	}
}

func renameCTE(in Logical, names []string) Logical {
	if in == nil || len(names) == 0 {
		return in
	}
	current := outputNames(in)
	if len(current) != len(names) {
		return in
	}
	same := true
	for i := range names {
		if current[i] != names[i] {
			same = false
			break
		}
	}
	if same {
		return in
	}
	exprs := make([]ast.Expr, len(names))
	cols := make([]int, len(names))
	for i, src := range current {
		cols[i] = i
		exprs[i] = ast.Ident{Name: src}
	}
	return Project{Input: in, Cols: cols, Exprs: exprs, Names: append([]string(nil), names...)}
}

func outputNames(p Logical) []string {
	switch n := p.(type) {
	case Project:
		return append([]string(nil), n.Names...)
	case Aggregate:
		return append([]string(nil), n.Names...)
	case Window:
		if n.Schema != nil {
			out := make([]string, len(n.Schema.Columns))
			for i, c := range n.Schema.Columns {
				out[i] = c.Name
			}
			return out
		}
		return outputNames(n.Input)
	case SetOperation:
		return append([]string(nil), n.Names...)
	case CTEScan:
		return append([]string(nil), n.Names...)
	case Empty:
		return append([]string(nil), n.Names...)
	case Filter:
		return outputNames(n.Input)
	case Limit:
		return outputNames(n.Input)
	case Sort:
		names := outputNames(n.Input)
		if n.Hidden > 0 && n.Hidden <= len(names) {
			return names[:len(names)-n.Hidden]
		}
		return names
	case Scan:
		if n.Table == nil {
			return nil
		}
		out := make([]string, len(n.Table.Columns))
		for i, c := range n.Table.Columns {
			out[i] = c.Name
		}
		return out
	default:
		return nil
	}
}

func windowOutputSchema(input Logical, wins []binder.BoundWindow) *catalog.Table {
	base := outputSchema(input)
	out := &catalog.Table{Name: "window"}
	if base != nil {
		out.Name = base.Name
		out.Columns = append(out.Columns, base.Columns...)
	}
	for _, w := range wins {
		out.Columns = append(out.Columns, catalog.Column{Name: w.Result, Type: w.OutType})
	}
	return out
}

func outputSchema(p Logical) *catalog.Table {
	switch n := p.(type) {
	case Window:
		return n.Schema
	case Aggregate:
		if n.Schema != nil {
			return n.Schema
		}
		return outputSchema(n.Input)
	case Project:
		tab := &catalog.Table{Name: "project", Columns: make([]catalog.Column, len(n.Names))}
		in := outputSchema(n.Input)
		for i, name := range n.Names {
			col := catalog.Column{Name: name}
			if in != nil && i < len(n.Cols) && n.Cols[i] >= 0 && n.Cols[i] < len(in.Columns) {
				src := in.Columns[n.Cols[i]]
				col.Type = src.Type
				col.NotNull = src.NotNull
			}
			tab.Columns[i] = col
		}
		return tab
	case Scan:
		return n.Table
	case SeqScan:
		return n.Table
	case IndexScan:
		return n.Table
	case Filter:
		return outputSchema(n.Input)
	case Sort:
		return outputSchema(n.Input)
	case Limit:
		return outputSchema(n.Input)
	case Search:
		if n.Table != nil {
			return n.Table
		}
		return outputSchema(n.Input)
	case Nearest:
		if n.Table != nil {
			return n.Table
		}
		return outputSchema(n.Input)
	case Join:
		if n.Schema != nil {
			return n.Schema
		}
		if t := outputSchema(n.Left); t != nil {
			return t
		}
		return outputSchema(n.Right)
	case CTEScan:
		return n.Schema
	default:
		return nil
	}
}

func orderCoversOutput(keys []binder.OrderKey, outputs int) bool {
	if outputs == 0 || len(keys) < outputs {
		return false
	}
	seen := make([]bool, outputs)
	for _, key := range keys {
		if key.Col >= 0 && key.Col < outputs {
			seen[key.Col] = true
		}
	}
	for _, ok := range seen {
		if !ok {
			return false
		}
	}
	return true
}

func uniqueDistinctKey(s binder.Select) (string, bool) {
	if !s.Distinct || s.HasAgg || len(s.Joins) != 0 || s.Table == nil {
		return "", false
	}
	hasAll := func(cols []int) bool {
		for _, keyCol := range cols {
			found := false
			for _, outCol := range s.OutCols {
				if outCol == keyCol {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return len(cols) > 0
	}
	if hasAll(s.Table.PK) {
		return "PRIMARY", true
	}
	for _, idx := range s.Table.Indexes {
		if !idx.Unique || len(idx.Path) != 0 || idx.HasExpr() || idx.Predicate != nil || !hasAll(idx.Columns) {
			continue
		}
		notNull := true
		for _, col := range idx.Columns {
			if col < 0 || col >= len(s.Table.Columns) || !s.Table.Columns[col].NotNull {
				notNull = false
				break
			}
		}
		if notNull {
			return idx.Name, true
		}
	}
	return "", false
}

func qualifyRankTable(from, schema *catalog.Table) *catalog.Table {
	if from == nil {
		return nil
	}
	c := from.Clone()
	if schema == nil {
		return c
	}
	n := len(from.Columns)
	if n > len(schema.Columns) {
		n = len(schema.Columns)
	}
	for i := 0; i < n; i++ {
		c.Columns[i].Name = schema.Columns[i].Name
	}
	return c
}

func schemaPrefix(full *catalog.Table, n int) *catalog.Table {
	if full == nil {
		return nil
	}
	if n > len(full.Columns) {
		n = len(full.Columns)
	}
	out := &catalog.Table{Name: full.Name}
	out.Columns = append([]catalog.Column(nil), full.Columns[:n]...)
	if len(full.PK) > 0 {
		out.PK = append([]int(nil), full.PK...)
	}
	return out
}
