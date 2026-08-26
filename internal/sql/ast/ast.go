package ast

import "github.com/bzync/nextsql/internal/sql/types"

// Stmt is a parsed statement.
type Stmt interface{ stmt() }

type (
	CreateTable struct {
		Name      string
		Columns   []ColumnDef
		PK        []string
		FKs       []ForeignKeyDef
		Partition *PartitionSpec
	}
	PartitionSpec struct {
		Kind       string // RANGE, HASH, LIST, or TENANT
		Columns    []string
		Partitions []PartitionDef
	}
	PartitionDef struct {
		Name      string
		LessThan  Expr   // RANGE: nil means MAXVALUE
		Values    []Expr // TENANT/LIST: IN values
		Modulus   uint32 // HASH
		Remainder uint32 // HASH
	}
	CreateDatabase struct {
		Name        string
		IfNotExists bool
	}
	WorkflowParam struct {
		Name string
		Type types.Type
	}
	CreateWorkflow struct {
		Name        string
		Params      []WorkflowParam
		Body        []Stmt
		IfNotExists bool
	}
	RunWorkflow struct {
		Name string
		Args []Expr
	}
	AlterWorkflow struct {
		Name    string
		NewName string
	}
	DropWorkflow struct {
		Name     string
		IfExists bool
	}
	CreateTrigger struct {
		Name        string
		Timing      TriggerTiming
		Event       TriggerEvent
		Table       string
		Workflow    string
		Args        []Expr
		IfNotExists bool
	}
	AlterTrigger struct {
		Name    string
		NewName string
	}
	DropTrigger struct {
		Name     string
		IfExists bool
	}
	CreateSchedule struct {
		Name        string
		Kind        ScheduleKind
		Spec        string
		Workflow    string
		Args        []Expr
		IfNotExists bool
	}
	AlterSchedule struct {
		Name    string
		NewName string
	}
	DropSchedule struct {
		Name     string
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
		Table     string
		Operation string
		After     uint64
	}
	DropTable struct {
		Name     string
		IfExists bool
	}
	DropIndex struct {
		Name     string
		Table    string // resolved before binding; SQL syntax names only the index
		IfExists bool
	}
	RebuildIndex struct {
		Name  string
		Table string // resolved before binding; SQL syntax names only the index
	}
	AlterTable struct {
		Table string
		Cmd   AlterCmd
	}
	CreateIndex struct {
		Name     string
		Table    string
		Unique   bool
		Spatial  bool
		Fulltext bool
		Vector   bool
		Using    string     // "hnsw" for VECTOR INDEX
		Cols     []string   // first identifier of each key (compat)
		Keys     [][]string // full key; one part is a column, more is a JSON path
		Exprs    []Expr     // parallel to Keys; non-nil replaces that key with an expression
		Include  []string   // INCLUDE columns stored in the leaf payload
		Where    Expr       // partial-index predicate
	}
	Insert struct {
		Table         string
		Columns       []string
		Rows          [][]Expr
		Returning     []SelectItem
		ReturningStar bool
	}
	// Upsert is INSERT-or-UPDATE on a PRIMARY KEY or UNIQUE btree index.
	Upsert struct {
		Table         string
		Columns       []string
		Rows          [][]Expr
		OnUnique      []string // empty means infer PK or the sole covered UNIQUE index
		Sets          []Assignment
		Returning     []SelectItem
		ReturningStar bool
	}
	Select struct {
		List              []SelectItem
		Star              bool
		Distinct          bool
		Table             string
		Alias             string
		FromQuery         Stmt
		Joins             []JoinSpec
		Where             Expr
		Group             []Expr
		Having            Expr
		SearchCol         string
		SearchQuery       Expr
		NearestCol        string
		NearestQuery      Expr
		NearestMetric     string // "", "cosine", "l2", "inner_product"
		Order             []OrderItem
		Limit             *int64
		Offset            *int64
		TenantScanFilters []TenantScanFilter
	}
	SetOperation struct {
		Left, Right Stmt
		Op          string // union, intersect, except
		All         bool
	}
	// With is a CTE list wrapping a query.
	With struct {
		Recursive bool
		CTEs      []CTEDef
		Query     Stmt
	}
	// CTEMaterialize is an optional materialization hint.
	CTEMaterialize uint8
	CTEDef         struct {
		Name        string
		Columns     []string
		Query       Stmt
		Materialize CTEMaterialize
	}
	// OrderItem is one ORDER BY key.
	OrderItem struct {
		Expr Expr
		Desc bool
	}
	// JoinKind is the SQL join type.
	JoinKind      uint8
	TriggerTiming uint8
	TriggerEvent  uint8
	ScheduleKind  uint8

	JoinSpec struct {
		Table string
		Alias string
		On    Expr
		Kind  JoinKind
		Cross bool // true when Kind==JoinCross (JOIN without ON, or CROSS JOIN)
	}

	// TenantScanFilter is a pre-join tenant predicate for a FULL OUTER input Scan.
	// Never placed in ON or post-join WHERE (those leak or drop null-extended rows).
	TenantScanFilter struct {
		Table string
		Alias string
		Pred  Expr
	}
	SelectItem struct {
		Expr  Expr
		Alias string
	}
	Update struct {
		Table         string
		Sets          []Assignment
		Where         Expr
		Limit         int64 // 0 = no limit
		Returning     []SelectItem
		ReturningStar bool
	}
	Assignment struct {
		Name string
		Expr Expr
	}
	Delete struct {
		Table         string
		Where         Expr
		Limit         int64 // 0 = no limit
		Returning     []SelectItem
		ReturningStar bool
	}
	Begin struct {
		Isolation string // empty, "read committed", "snapshot", "serializable"
	}
	Commit   struct{}
	Rollback struct{}
	Explain  struct {
		Analyze bool
		Stmt    Stmt
	}
	Analyze struct {
		Table string // empty means every table
	}
	Maintain struct {
		Table string // resolved owner for index scope; empty for database
		Index string // non-empty means index scope
	}
	CreateUser struct {
		Name     string
		Password string
	}
	DropUser struct {
		Name string
	}
	CreateRole struct {
		Name string
	}
	DropRole struct {
		Name string
	}
	Grant struct {
		Role       string
		Privileges []string
		All        bool
		Scope      string
		Object     string
		Grantee    string
	}
	Revoke struct {
		Role       string
		Privileges []string
		All        bool
		Scope      string
		Object     string
		Grantee    string
	}
	// SetTenant binds or clears the session tenant. A nil Value is RESET TENANT.
	SetTenant struct {
		Value Expr
	}
)

const (
	JoinInner JoinKind = iota
	JoinLeft
	JoinRight
	JoinFull
	JoinCross
	JoinSemi
	JoinAnti
)

const (
	TriggerBefore TriggerTiming = iota + 1
	TriggerAfter
)

const (
	TriggerInsert TriggerEvent = iota + 1
	TriggerUpdate
	TriggerDelete
)

const (
	ScheduleEvery ScheduleKind = iota + 1
	ScheduleAt
)

const (
	CTEAuto CTEMaterialize = iota
	CTEAlways
	CTENever
)

const (
	FrameRows FrameMode = iota
	FrameRange
)

const (
	BoundUnboundedPreceding BoundKind = iota
	BoundPreceding
	BoundCurrentRow
	BoundFollowing
	BoundUnboundedFollowing
)

func (CreateTable) stmt()    {}
func (CreateDatabase) stmt() {}
func (CreateWorkflow) stmt() {}
func (RunWorkflow) stmt()    {}
func (AlterWorkflow) stmt()  {}
func (DropWorkflow) stmt()   {}
func (CreateTrigger) stmt()  {}
func (AlterTrigger) stmt()   {}
func (DropTrigger) stmt()    {}
func (CreateSchedule) stmt() {}
func (AlterSchedule) stmt()  {}
func (DropSchedule) stmt()   {}
func (ShowTasks) stmt()      {}
func (CancelTask) stmt()     {}
func (Subscribe) stmt()      {}
func (CreateIndex) stmt()    {}
func (DropTable) stmt()      {}
func (DropIndex) stmt()      {}
func (RebuildIndex) stmt()   {}
func (AlterTable) stmt()     {}
func (Insert) stmt()         {}
func (Upsert) stmt()         {}
func (Select) stmt()         {}
func (SetOperation) stmt()   {}
func (With) stmt()           {}
func (Update) stmt()         {}
func (Delete) stmt()         {}
func (Begin) stmt()          {}
func (Commit) stmt()         {}
func (Rollback) stmt()       {}
func (Explain) stmt()        {}
func (Analyze) stmt()        {}
func (Maintain) stmt()       {}
func (CreateUser) stmt()     {}
func (DropUser) stmt()       {}
func (CreateRole) stmt()     {}
func (DropRole) stmt()       {}
func (Grant) stmt()          {}
func (Revoke) stmt()         {}
func (SetTenant) stmt()      {}

// AlterCmd is one ALTER TABLE action.
type AlterCmd interface{ alterCmd() }

type (
	AlterAddColumn struct {
		Column ColumnDef
	}
	AlterDropColumn struct {
		Name string
	}
	AlterRenameColumn struct {
		Old, New string
	}
	AlterRenameTable struct {
		New string
	}
	AlterAddConstraint struct {
		FK ForeignKeyDef
	}
	AlterDropConstraint struct {
		Name string
	}
	AlterSetCDCImages struct {
		Mode string
	}
)

func (AlterAddColumn) alterCmd()      {}
func (AlterDropColumn) alterCmd()     {}
func (AlterRenameColumn) alterCmd()   {}
func (AlterRenameTable) alterCmd()    {}
func (AlterAddConstraint) alterCmd()  {}
func (AlterDropConstraint) alterCmd() {}
func (AlterSetCDCImages) alterCmd()   {}

// FKAction is a referential action. NO ACTION is stored as FKRestrict.
type FKAction uint8

const (
	FKRestrict FKAction = iota
	FKCascade
	FKSetNull
	FKSetDefault
)

// ForeignKeyDef is a table or column REFERENCES clause.
type ForeignKeyDef struct {
	Name     string
	Columns  []string
	RefTable string
	RefCols  []string
	OnDelete FKAction
	OnUpdate FKAction
}

type ColumnDef struct {
	Name       string
	Type       types.Type
	NotNull    bool
	Primary    bool
	Default    Expr
	References *ForeignKeyDef
}

// Expr is a parsed expression.
type Expr interface{ expr() }

type (
	Literal struct {
		Value types.Value
	}
	Ident struct {
		Name string
	}
	Path struct {
		Parts []string
	}
	Param struct {
		Name string
	}
	Unary struct {
		Op    string
		Right Expr
	}
	Binary struct {
		Op          string
		Left, Right Expr
	}
	Between struct {
		Expr, Low, High Expr
		Not             bool
	}
	IsNull struct {
		Expr Expr
		Not  bool
	}
	Call struct {
		Name string
		Args []Expr
		Star bool // COUNT(*)
	}
	// Window is fn(...) OVER (partition / order / frame).
	Window struct {
		Fn        Call
		Partition []Expr
		Order     []OrderItem
		Frame     *Frame // nil means the binder default
	}
	FrameMode uint8
	BoundKind uint8
	Frame     struct {
		Mode  FrameMode
		Start FrameBound
		End   FrameBound
	}
	FrameBound struct {
		Kind   BoundKind
		Offset Expr // N PRECEDING / FOLLOWING
	}
	CaseWhen struct {
		When Expr
		Then Expr
	}
	Case struct {
		Operand Expr // nil for searched CASE
		Whens   []CaseWhen
		Else    Expr // nil means NULL
	}
	ScalarSubquery struct {
		Query Stmt
		ID    uint64
	}
	InSubquery struct {
		Expr  Expr
		Query Stmt
		Not   bool
		ID    uint64
	}
	ExistsSubquery struct {
		Query Stmt
		ID    uint64
	}
	VectorLit struct {
		Elems []float32
	}
)

func (Literal) expr()        {}
func (Ident) expr()          {}
func (Path) expr()           {}
func (Param) expr()          {}
func (Unary) expr()          {}
func (Binary) expr()         {}
func (Between) expr()        {}
func (IsNull) expr()         {}
func (Call) expr()           {}
func (Window) expr()         {}
func (Case) expr()           {}
func (ScalarSubquery) expr() {}
func (InSubquery) expr()     {}
func (ExistsSubquery) expr() {}
func (VectorLit) expr()      {}
