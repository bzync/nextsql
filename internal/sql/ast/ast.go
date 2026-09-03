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
		Name string
		Rule string // RANGE, HASH, or VALUE; used by ALTER TABLE lifecycle DDL
		// LessThan holds the single-column RANGE upper bound; nil means MAXVALUE.
		// LessThanTuple holds a multi-column RANGE upper bound (VALUES LESS THAN
		// (a, b, ...)); exactly one of the two is set for a bounded partition.
		LessThan      Expr
		LessThanTuple []Expr
		// Values holds single-column LIST/TENANT membership (VALUES IN (a, b, ...)).
		// ValueTuples holds multi-column LIST membership (VALUES IN ((a, b), ...)).
		Values      []Expr
		ValueTuples [][]Expr
		Modulus     uint32 // HASH
		Remainder   uint32 // HASH
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
	// CreateResourceGroup declares a named workload-governance descriptor.
	// Zero-valued options mean "unbounded" / "unset", the same convention
	// used by protocol.Limits.MaxSessionsPerUser and hosting storage caps.
	CreateResourceGroup struct {
		Name           string
		MaxConcurrency int
		MemoryBytes    int64
		Workers        int
		Priority       int
		IfNotExists    bool
	}
	// AlterResourceGroup replaces only the options whose Has* flag is set;
	// omitted options keep the group's current stored value.
	AlterResourceGroup struct {
		Name              string
		MaxConcurrency    int
		HasMaxConcurrency bool
		MemoryBytes       int64
		HasMemoryBytes    bool
		Workers           int
		HasWorkers        bool
		Priority          int
		HasPriority       bool
	}
	DropResourceGroup struct {
		Name     string
		IfExists bool
	}
	// SetResourceGroup assigns the issuing session's workload governance
	// class for every statement it runs until RESET or the session ends.
	SetResourceGroup struct {
		Name string
	}
	// ResetResourceGroup clears a session's resource group assignment,
	// returning it to unbounded/process-default scheduling.
	ResetResourceGroup struct{}
	ShowTasks          struct {
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
	// TransferLeader asks this Raft cluster's current leader to step down in
	// favor of another voter (CLUSTER TRANSFER LEADER). A no-op on a
	// single-node deployment's caller sees Unavailable instead.
	TransferLeader struct{}
	// ClusterDrain asks the server this connection reached to begin a
	// graceful drain of itself (CLUSTER DRAIN [WITH (TIMEOUT_MS = n)]) —
	// stop accepting new connections, close idle ones immediately, wait up
	// to the timeout for busy ones, then force-close whatever remains.
	// Purely local to the node the client connected to; unlike
	// TransferLeader it needs no Raft cluster and works the same on a
	// single-node deployment. TimeoutMS 0 means "use the server's
	// configured shutdown_drain_ms".
	ClusterDrain struct {
		TimeoutMS int64
	}
	// ClusterMaintenance asks the server this connection reached to enter or
	// leave maintenance mode (CLUSTER MAINTENANCE ENABLE|DISABLE). While
	// enabled, the node rejects every mutating statement (DML, DDL, and
	// BEGIN — the same classification requireLeader already uses) with
	// Unavailable; reads keep working so operators can still inspect state.
	// Purely local to the node the client connected to, like ClusterDrain:
	// it is not Raft-replicated, so a leader failover during maintenance
	// does not carry the flag to the new leader.
	ClusterMaintenance struct {
		Enable bool
	}
	// ClusterReconcileConfirm asks the server this connection reached to
	// clear its local replication-suspect flag (CLUSTER RECONCILE CONFIRM).
	// That flag is set automatically when a local commit could not be
	// replicated to quorum (see storage.Engine's ReplicationOrphanReporter)
	// and, while set, blocks this node from serving STRONG reads — an
	// operator must run this only after verifying/repairing the node's
	// divergence. The CONFIRM keyword is mandatory, not optional, so the
	// statement can't be fat-fingered as a bare CLUSTER RECONCILE. Purely
	// local to the node this connection reached, like ClusterMaintenance:
	// not Raft-replicated.
	ClusterReconcileConfirm struct{}
	DropTable               struct {
		Name     string
		IfExists bool
	}
	DropIndex struct {
		Name     string
		Table    string // resolved before binding; SQL syntax names only the index
		IfExists bool
	}
	RebuildIndex struct {
		Name   string
		Table  string // resolved before binding; SQL syntax names only the index
		Online bool   // REBUILD INDEX ... ONLINE: build without blocking concurrent writes
	}
	AlterTable struct {
		Table string
		Cmd   AlterCmd
	}
	CreateIndex struct {
		Name         string
		Table        string
		Unique       bool
		Spatial      bool
		Fulltext     bool
		Vector       bool
		Using        string     // "hnsw", "ivf", "ivfpq", or "sparse" for VECTOR INDEX
		VecQuant     string     // HNSW traversal quantisation: "", "none", "f16", "i8"
		IVFLists     int        // USING IVF/IVFPQ WITH (LISTS = n); 0 when not IVF
		IVFProbes    int        // USING IVF/IVFPQ WITH (..., PROBES = m); 0 = build default
		IVFSubspaces int        // USING IVFPQ WITH (..., SUBSPACES = M); 0 when not IVFPQ
		Analyzer     string     // FULLTEXT WITH (ANALYZER = 'simple' | 'english' | 'french' | 'german' | 'spanish'); empty = simple
		Cols         []string   // first identifier of each key (compat)
		Keys         [][]string // full key; one part is a column, more is a JSON path
		Exprs        []Expr     // parallel to Keys; non-nil replaces that key with an expression
		Include      []string   // INCLUDE columns stored in the leaf payload
		Where        Expr       // partial-index predicate
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
		List           []SelectItem
		Star           bool
		Distinct       bool
		Table          string
		Alias          string
		FromQuery      Stmt
		Joins          []JoinSpec
		Where          Expr
		Group          []Expr
		Having         Expr
		SearchCols     []string
		SearchWeights  []float64 // parallel to SearchCols; nil means all 1
		SearchQuery    Expr
		FacetCols      []string // FACET col [, col …]; independent histograms over SEARCH matches
		NearestCol     string
		NearestQuery   Expr
		NearestMetric  string // "", "cosine", "l2", "inner_product"
		Nearest2Col    string
		Nearest2Query  Expr
		Nearest2Metric string
		Order          []OrderItem
		Limit          *int64
		Offset         *int64
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
	ScheduleCron
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

func (CreateTable) stmt()             {}
func (CreateDatabase) stmt()          {}
func (CreateWorkflow) stmt()          {}
func (RunWorkflow) stmt()             {}
func (AlterWorkflow) stmt()           {}
func (DropWorkflow) stmt()            {}
func (CreateTrigger) stmt()           {}
func (AlterTrigger) stmt()            {}
func (DropTrigger) stmt()             {}
func (CreateSchedule) stmt()          {}
func (AlterSchedule) stmt()           {}
func (DropSchedule) stmt()            {}
func (CreateResourceGroup) stmt()     {}
func (AlterResourceGroup) stmt()      {}
func (DropResourceGroup) stmt()       {}
func (SetResourceGroup) stmt()        {}
func (ResetResourceGroup) stmt()      {}
func (ShowTasks) stmt()               {}
func (CancelTask) stmt()              {}
func (Subscribe) stmt()               {}
func (TransferLeader) stmt()          {}
func (ClusterDrain) stmt()            {}
func (ClusterMaintenance) stmt()      {}
func (ClusterReconcileConfirm) stmt() {}
func (CreateIndex) stmt()             {}
func (DropTable) stmt()               {}
func (DropIndex) stmt()               {}
func (RebuildIndex) stmt()            {}
func (AlterTable) stmt()              {}
func (Insert) stmt()                  {}
func (Upsert) stmt()                  {}
func (Select) stmt()                  {}
func (SetOperation) stmt()            {}
func (With) stmt()                    {}
func (Update) stmt()                  {}
func (Delete) stmt()                  {}
func (Begin) stmt()                   {}
func (Commit) stmt()                  {}
func (Rollback) stmt()                {}
func (Explain) stmt()                 {}
func (Analyze) stmt()                 {}
func (Maintain) stmt()                {}
func (CreateUser) stmt()              {}
func (DropUser) stmt()                {}
func (CreateRole) stmt()              {}
func (DropRole) stmt()                {}
func (Grant) stmt()                   {}
func (Revoke) stmt()                  {}

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
	AlterAddPartition struct {
		Partition PartitionDef
	}
	AlterDropPartition struct {
		Name string
	}
	AlterAttachPartition struct {
		Partition PartitionDef
	}
	AlterDetachPartition struct {
		Name string
	}
)

func (AlterAddColumn) alterCmd()       {}
func (AlterDropColumn) alterCmd()      {}
func (AlterRenameColumn) alterCmd()    {}
func (AlterRenameTable) alterCmd()     {}
func (AlterAddConstraint) alterCmd()   {}
func (AlterDropConstraint) alterCmd()  {}
func (AlterSetCDCImages) alterCmd()    {}
func (AlterAddPartition) alterCmd()    {}
func (AlterDropPartition) alterCmd()   {}
func (AlterAttachPartition) alterCmd() {}
func (AlterDetachPartition) alterCmd() {}

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
	Name string
	Type types.Type
	// EncryptedClient stores only randomized client ciphertext on the server.
	// Type remains the logical plaintext type in the AST.
	EncryptedClient bool
	NotNull         bool
	Primary         bool
	Default         Expr
	References      *ForeignKeyDef
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
