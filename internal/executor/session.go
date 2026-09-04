package executor

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/executor/vector"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/txn"
)

// Param is a typed query parameter. Name is optional; "$1" binds to index 0.
type Param struct {
	Name  string
	Value types.Value
}

// Result is a local-execution result. Exec materializes under the query
// budget; NextBatch streams remaining batches when Query is used.
type Result struct {
	Columns  []string
	Rows     [][]types.Value
	Affected int64
	Cached   bool // true when Rows came from the bounded query-result cache
	// IdempotentReplay is true when ExecIdempotent returned a previously
	// committed mutation result instead of executing the mutation again.
	IdempotentReplay bool
	next             func() (*vector.Batch, error)
	close            func()
}

// NextBatch returns the next result batch, or nil at end of stream.
func (r *Result) NextBatch() (*vector.Batch, error) {
	if r == nil || r.next == nil {
		return nil, nil
	}
	b, err := r.next()
	if err != nil || b == nil {
		r.Close()
	}
	return b, err
}

// Close releases resources held by a streaming result. It is idempotent.
func (r *Result) Close() {
	if r == nil || r.close == nil {
		return
	}
	close := r.close
	r.close = nil
	close()
}

// Session is one SQL client. Transactions are session-scoped.
type Session struct {
	db                   *DB
	x                    *xact
	overlay              map[string]*catalog.Table
	workflowOverlay      map[string]*catalog.Workflow
	triggerOverlay       map[string]*catalog.Trigger
	scheduleOverlay      map[string]*catalog.Schedule
	resourceGroupOverlay map[string]*catalog.ResourceGroup
	pending              *pending
	trace                *optimizer.Node
	limits               scheduler.Limits
	qbudget              *scheduler.Budget
	// txnTimeout bounds an open transaction's total wall-clock lifetime; 0
	// (the default) is unbounded. See Session.SetTxnTimeout.
	txnTimeout time.Duration
	// resourceGroup is this session's assigned workload-governance class
	// (SET RESOURCE GROUP name / RESET RESOURCE GROUP), empty meaning none.
	// Same-goroutine-use-only, like execSQL above — not published for
	// cross-session introspection (system.sessions has no resource_group
	// column yet).
	resourceGroup        string
	ftHL                 *ftHighlightState
	params               []Param
	subqueryResults      map[uint64]*Result
	cteRows              map[uint64][][]types.Value
	cteNames             map[string]struct{}
	automaticMaintenance map[string]uint64

	user      string
	authRoles []string
	acl       *security.ACL
	audit     *security.Log
	users     *auth.Store
	registry  *security.Registry
	remote    string
	execSQL   string // current statement text; reserved history DDL matching
	// hostingRegistry backs system.realms/system.databases (M2-4a). Nil on
	// a legacy/non-hosted deployment — those views then return zero rows,
	// never an error.
	hostingRegistry *hosting.Registry
	// realmID scopes every auth.Store/security.ACL check this session makes
	// (M2-4b-1); the zero value hosting.ID{} means deployment-wide, which is
	// every session that never had SetRealmID called on it (every
	// non-hosted deployment, unchanged from before this field existed).
	realmID hosting.ID

	dirtyHNSW       bool
	pendingHNSW     map[string]*lockedMem
	dirtyIVF        bool
	pendingIVF      map[string]*lockedIVF
	fkProbes        int
	fkDepth         int
	fkTouched       int
	fkVisited       map[string]struct{}
	workflowDepth   int
	workflowVisited map[string]struct{}
	triggerDepth    int
	triggerBroken   bool
	// fkMaxTouched is 0 to use security.MaxFKTouchedRows (tests may lower it).
	fkMaxTouched int
	// fkBroken is set on a cascade cap hit so COMMIT cannot persist a
	// partial cascade from an explicit transaction.
	fkBroken bool
	// conflictWrite uses a latest-committed snapshot for UPSERT updates of
	// a unique key occupied after this transaction's begin snapshot.
	conflictWrite bool
	txnGuard      bool
	// readConsistency selects how non-mutating statements observe replicated
	// state. The zero value is ReadStrong.
	readConsistency ReadConsistency
	// readStaleness bounds a BOUNDED read. Zero selects DefaultMaxStaleness.
	readStaleness time.Duration

	// id/connectedAt are set once by DB.RegisterSession, at network-connect
	// time; a session never registered (embedded/CLI/test use) keeps id 0
	// and is invisible to system.sessions.
	id          uint64
	connectedAt time.Time

	// queryMu guards the currently-executing-statement snapshot read cross-
	// goroutine by system.active_queries/system.sessions. s.execSQL above
	// stays unsynchronized (same-goroutine use only, in exec.go).
	queryMu    sync.Mutex
	queryID    uint64
	queryText  string
	queryStart time.Time

	// txnMu guards the open-transaction snapshot read cross-goroutine by
	// system.transactions. s.x itself is never read/written outside this
	// session's own goroutine.
	txnMu       sync.Mutex
	txnActive   bool
	txnID       format.TxnID
	txnIso      txn.Isolation
	txnReadOnly bool
	txnStart    time.Time
}

// nextQueryID assigns process-wide unique ids to executing statements, for
// system.active_queries.
var nextQueryID atomic.Uint64

// beginQuery publishes the currently-executing statement for cross-session
// introspection (system.active_queries). Call at the start of execAdmitted.
func (s *Session) beginQuery(sql string) {
	id := nextQueryID.Add(1)
	s.queryMu.Lock()
	s.queryID = id
	s.queryText = sql
	s.queryStart = time.Now()
	s.queryMu.Unlock()
}

// endQuery clears the published currently-executing statement. Call in the
// same defer that resets s.execSQL in execAdmitted.
func (s *Session) endQuery() {
	s.queryMu.Lock()
	s.queryID = 0
	s.queryText = ""
	s.queryMu.Unlock()
}

// CurrentQuery reports the SQL text and start time of the statement this
// session is presently executing, if any. Safe to call from another
// goroutine.
func (s *Session) CurrentQuery() (id uint64, sql string, start time.Time, running bool) {
	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	return s.queryID, s.queryText, s.queryStart, s.queryText != ""
}

// setTxnActive publishes an open transaction for cross-session introspection
// (system.transactions). Call right after s.x is assigned.
func (s *Session) setTxnActive(h *txn.Handle, readOnly bool) {
	if h == nil {
		return
	}
	s.txnMu.Lock()
	s.txnID = h.ID
	s.txnIso = h.Iso
	s.txnReadOnly = readOnly
	s.txnActive = true
	s.txnStart = time.Now()
	s.txnMu.Unlock()
}

// clearTxnActive clears the published open-transaction snapshot. Call
// wherever s.x is reset to nil (commit, rollback/abort).
func (s *Session) clearTxnActive() {
	s.txnMu.Lock()
	s.txnActive = false
	s.txnMu.Unlock()
}

// TxnSnapshot reports this session's currently open transaction, if any.
// Safe to call from another goroutine.
func (s *Session) TxnSnapshot() (id format.TxnID, iso txn.Isolation, readOnly bool, start time.Time, active bool) {
	s.txnMu.Lock()
	defer s.txnMu.Unlock()
	return s.txnID, s.txnIso, s.txnReadOnly, s.txnStart, s.txnActive
}

// SessionID is the id assigned by DB.RegisterSession (0 if never
// registered).
func (s *Session) SessionID() uint64 { return s.id }

// ConnectedAt is when DB.RegisterSession registered this session (zero if
// never registered).
func (s *Session) ConnectedAt() time.Time { return s.connectedAt }

// Remote is the client address set by SetRemote, if any.
func (s *Session) Remote() string { return s.remote }

// SetReadConsistency selects how this session's reads observe replicated
// state. STRONG (the default) serves every read on the leader behind a
// verified Raft read barrier. BOUNDED serves any node — leader or a follower
// still within MaxStaleness of the leader — from local applied state, and
// rejects a node that has fallen further behind. STALE serves the local
// node's applied state with no freshness bound.
func (s *Session) SetReadConsistency(mode ReadConsistency) error {
	switch mode {
	case ReadStrong, ReadBounded, ReadStale:
		s.readConsistency = mode
		return nil
	default:
		return nerr.New(nerr.InvalidArgument, "executor.SetReadConsistency", "unknown read consistency mode")
	}
}

// ReadConsistency reports the session read-consistency mode.
func (s *Session) ReadConsistency() ReadConsistency { return s.readConsistency }

// SetMaxStaleness sets the freshness bound for BOUNDED reads. Zero (the
// default) selects DefaultMaxStaleness. A negative value is clamped to zero.
// It has no effect in STRONG or STALE mode.
func (s *Session) SetMaxStaleness(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.readStaleness = d
}

// MaxStaleness reports the configured BOUNDED-read freshness bound (0 = default).
func (s *Session) MaxStaleness() time.Duration { return s.readStaleness }

func (s *Session) boundedStaleness() time.Duration {
	if s.readStaleness > 0 {
		return s.readStaleness
	}
	return DefaultMaxStaleness
}

func (s *Session) SetIdentity(user string) { s.user = user }

// SetAuthRoles narrows this session's effective privileges to those reachable
// through the named roles, as carried by a short-lived credential's role
// scope. An empty slice imposes no narrowing.
func (s *Session) SetAuthRoles(roles []string) { s.authRoles = roles }

// authAllowed is the single authorization check for this session. It applies
// the credential's role scope when one is set.
func (s *Session) authAllowed(user string, priv security.Privilege, scope security.ScopeKind, object string) bool {
	if s.acl == nil {
		return true
	}
	return s.acl.AllowedScopedInRealm(s.realmID, user, s.authRoles, priv, scope, object)
}
func (s *Session) User() string                           { return s.user }
func (s *Session) SetACL(a *security.ACL)                 { s.acl = a }
func (s *Session) SetAudit(l *security.Log)               { s.audit = l }
func (s *Session) SetAuth(st *auth.Store)                 { s.users = st }
func (s *Session) SetRegistry(r *security.Registry)       { s.registry = r }
func (s *Session) SetHostingRegistry(r *hosting.Registry) { s.hostingRegistry = r }
func (s *Session) SetRealmID(id hosting.ID)               { s.realmID = id }
func (s *Session) SetRemote(addr string)                  { s.remote = addr }

type pending struct {
	heaps              map[string]*btree.Tree
	idxs               map[string]*btree.Tree
	vecs               map[string]*btree.Tree
	partHeaps          map[string]*btree.Tree
	partVecs           map[string]*btree.Tree
	partIdxs           map[string]*btree.Tree
	stats              map[string]*catalog.TableStats
	statsChanges       map[string]uint64
	maintenanceChanges map[string]uint64
	dropped            map[string]struct{}
	renames            map[string]string
	reclaims           []format.PageID
	partitionDrops     []string
	indexDrops         []indexMapDrop
	taskCancels        []string
}

type indexMapDrop struct {
	key       string
	tree      *btree.Tree
	partition bool
}

func newPending() *pending {
	return &pending{
		heaps:              make(map[string]*btree.Tree),
		idxs:               make(map[string]*btree.Tree),
		vecs:               make(map[string]*btree.Tree),
		partHeaps:          make(map[string]*btree.Tree),
		partVecs:           make(map[string]*btree.Tree),
		partIdxs:           make(map[string]*btree.Tree),
		stats:              make(map[string]*catalog.TableStats),
		statsChanges:       make(map[string]uint64),
		maintenanceChanges: make(map[string]uint64),
		dropped:            make(map[string]struct{}),
		renames:            make(map[string]string),
	}
}

type xact struct {
	owner    *btree.Txn
	mu       sync.Mutex
	parts    map[*btree.Tree]*btree.Txn
	iso      txn.Isolation
	readOnly bool
}

func newXact(owner *btree.Txn, iso txn.Isolation) *xact {
	return &xact{
		owner:    owner,
		iso:      iso,
		readOnly: owner != nil && owner.Storage() == nil,
		parts:    map[*btree.Tree]*btree.Txn{owner.Tree(): owner},
	}
}

func (x *xact) use(tr *btree.Tree) *btree.Txn {
	x.mu.Lock()
	defer x.mu.Unlock()
	if p, ok := x.parts[tr]; ok {
		return p
	}
	p := tr.Attach(x.owner.Storage(), x.owner.Handle())
	x.parts[tr] = p
	return p
}

func (x *xact) commit() error {
	if x.readOnly {
		for _, p := range x.parts {
			p.MarkDone()
		}
		if h := x.owner.Handle(); h != nil && h.ReadOnly && x.owner.Tree() != nil && x.owner.Tree().Engine() != nil && x.owner.Tree().Engine().TM != nil {
			x.owner.Tree().Engine().TM.EndRead(h.ID)
		}
		return nil
	}
	for _, p := range x.parts {
		if err := p.PersistMeta(); err != nil {
			_ = x.rollback()
			return err
		}
	}
	if err := x.owner.Tree().Engine().CommitTxn(x.owner.Storage()); err != nil {
		return err
	}
	for _, p := range x.parts {
		p.MarkDone()
		if err := p.PurgeDeleted(); err != nil {
			return err
		}
	}
	return nil
}

func (x *xact) rollback() error {
	for _, p := range x.parts {
		p.MarkDone()
		p.RestoreSnap()
	}
	if x.readOnly {
		if h := x.owner.Handle(); h != nil && h.ReadOnly && x.owner.Tree() != nil && x.owner.Tree().Engine() != nil && x.owner.Tree().Engine().TM != nil {
			x.owner.Tree().Engine().TM.EndRead(h.ID)
		}
		return nil
	}
	return x.owner.Tree().Engine().RollbackTxn(x.owner.Storage())
}

func (s *Session) lookup(name string) (*catalog.Table, bool) {
	if s.overlay != nil {
		if t, ok := s.overlay[name]; ok {
			if t == nil {
				return nil, false
			}
			return t.Clone(), true
		}
	}
	return s.db.Cat.Get(name)
}

// inboundFKs walks overlay ∪ catalog through lookup so an uncommitted
// CREATE TABLE child is visible to parent DELETE in the same txn.
func (s *Session) inboundFKs(parent *catalog.Table) []inboundFK {
	if s == nil || parent == nil {
		return nil
	}
	names := make(map[string]struct{})
	if s.overlay != nil {
		for name, t := range s.overlay {
			if t == nil {
				continue
			}
			names[name] = struct{}{}
		}
	}
	if s.db != nil && s.db.Cat != nil {
		for _, t := range s.db.Cat.List() {
			names[t.Name] = struct{}{}
		}
	}
	var out []inboundFK
	for name := range names {
		t, ok := s.lookup(name)
		if !ok {
			continue
		}
		for _, fk := range t.ForeignKeys {
			if !fkReferences(fk, parent) {
				continue
			}
			out = append(out, inboundFK{child: t, fk: fk})
		}
	}
	return out
}

func (s *Session) InTxn() bool { return s.x != nil }

func (s *Session) SetLimits(l scheduler.Limits) { s.limits = l }

// SetTxnTimeout bounds how long a transaction opened by this session may
// stay open (BEGIN, or the first statement of an implicit autocommit
// transaction, to COMMIT/ROLLBACK) before the next statement dispatched
// inside it force-aborts and fails Exhausted. d <= 0 disables the bound.
func (s *Session) SetTxnTimeout(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.txnTimeout = d
}

func (s *Session) budget() *scheduler.Budget {
	if s.qbudget == nil {
		s.qbudget = scheduler.NewBudget(nil, s.limitsOrDefault())
	}
	return s.qbudget
}

func (s *Session) limitsOrDefault() scheduler.Limits {
	lim := s.limits
	if lim.Workers == 0 && lim.Memory == 0 {
		lim = scheduler.DefaultLimits()
	}
	if s.resourceGroup != "" && s.db != nil {
		if g, ok := s.db.resourceGroup(s.resourceGroup); ok {
			// 0 on the group means unset/unbounded: leave the session/server
			// value in place rather than overriding it with zero. Workers is
			// still clamped to [1, MaxWorkers] by Limits.normalized() inside
			// scheduler.NewBudget, so a group can never request more workers
			// than the process-wide ceiling allows.
			if g.Workers > 0 {
				lim.Workers = int(g.Workers)
			}
			if g.MemoryBytes > 0 {
				lim.Memory = g.MemoryBytes
			}
		}
	}
	return lim
}

// ResourceGroup returns the session's currently assigned resource group
// name, or "" if none (SET RESOURCE GROUP / RESET RESOURCE GROUP).
func (s *Session) ResourceGroup() string { return s.resourceGroup }

func (s *Session) workers() int {
	return s.budget().Workers()
}

func (s *Session) pool() *scheduler.Pool {
	if s.db != nil && s.db.sched != nil {
		return s.db.sched
	}
	return scheduler.DefaultPool
}

func (s *Session) Exec(sql string) (*Result, error) {
	return s.ExecContext(context.Background(), sql, nil)
}

// ExecContext runs one statement with a parent context and typed parameters.
func (s *Session) ExecContext(ctx context.Context, sql string, params []Param) (*Result, error) {
	if s != nil && s.db != nil && s.db.admit != nil {
		priority := int32(0)
		if s.resourceGroup != "" {
			if g, ok := s.db.resourceGroup(s.resourceGroup); ok {
				priority = g.Priority
			}
		}
		rel, err := s.db.admit.AcquireWithPriority(ctx, priority)
		if err != nil {
			if s.db.metrics != nil {
				s.db.metrics.AddRejected()
			}
			return nil, err
		}
		defer rel()
		if s.db.metrics != nil {
			s.db.metrics.AddAdmitted()
		}
	}
	// A resource group's MaxConcurrency, when set, is a second, strictly
	// additional gate layered on top of the process-wide db.admit above —
	// never a substitute for it. A session with no group assigned, or one
	// assigned to an unbounded (MaxConcurrency == 0) group, sees no change
	// here (resourceGroupGate returns nil, and scheduler.Admission.Acquire on
	// a nil receiver is a documented no-op). This is what makes "resource
	// groups cannot bypass global safety limits" true by construction rather
	// than by convention.
	if s != nil && s.db != nil && s.resourceGroup != "" {
		if gate := s.db.resourceGroupGate(s.resourceGroup); gate != nil {
			rel, err := gate.Acquire(ctx)
			if err != nil {
				if s.db.metrics != nil {
					s.db.metrics.AddRejected()
				}
				return nil, err
			}
			defer rel()
		}
	}
	start := time.Now()
	res, err := s.execAdmitted(ctx, sql, params)
	if s != nil && len(s.automaticMaintenance) > 0 {
		changes := s.automaticMaintenance
		s.automaticMaintenance = nil
		s.runAutomaticMaintenance(changes)
	}
	if s != nil && s.db != nil {
		s.db.drainCommittedReclaims()
	}
	if s != nil && s.db != nil && s.db.metrics != nil {
		s.db.metrics.ObserveQuery(time.Since(start), err)
		if err == nil && res != nil {
			s.db.metrics.AddRows(int64(len(res.Rows)))
		}
	}
	return res, err
}

func (s *Session) execAdmitted(ctx context.Context, sql string, params []Param) (*Result, error) {
	s.params = params
	s.execSQL = sql
	s.beginQuery(sql)
	s.subqueryResults = make(map[uint64]*Result)
	s.cteRows = make(map[uint64][][]types.Value)
	defer func() {
		s.params = nil
		s.execSQL = ""
		s.endQuery()
		s.subqueryResults = nil
		s.cteRows = nil
	}()
	stmt, err := parser.Parse(sql)
	if err != nil {
		return nil, err
	}
	if s.x != nil && s.txnTimeout > 0 {
		if _, _, _, start, active := s.TxnSnapshot(); active && time.Since(start) > s.txnTimeout {
			_ = s.abort()
			return nil, nerr.New(nerr.Exhausted, "executor.Exec", "transaction timeout exceeded")
		}
	}
	if st, ok := stmt.(ast.Maintain); ok {
		if st.Index != "" {
			st, err = s.resolveMaintainIndex(st)
			if err != nil {
				return nil, err
			}
		}
		if err := s.authorize(st); err != nil {
			s.auditRecord(security.ActionDDL, st.Table, err)
			return nil, err
		}
		if s.InTxn() {
			return nil, nerr.New(nerr.InvalidArgument, "executor.Maintain", "MAINTAIN cannot run inside a transaction")
		}
		if err := s.requireLeader(true); err != nil {
			return nil, err
		}
		const limit = 10_000
		var n int
		var err error
		if st.Index != "" {
			n, err = s.db.CleanupIndexDeadVersions(st.Table, st.Index, limit)
		} else if st.Table == "" {
			n, err = s.db.CleanupDeadVersions(limit)
		} else {
			n, err = s.db.CleanupTableDeadVersions(st.Table, limit)
		}
		s.auditRecord(security.ActionDDL, st.Table, err)
		return &Result{Affected: int64(n)}, err
	}
	if _, ok := stmt.(ast.TransferLeader); ok {
		if err := s.authorize(stmt); err != nil {
			s.auditRecord(security.ActionLeaderTransfer, "", err)
			return nil, err
		}
		if s.InTxn() {
			return nil, nerr.New(nerr.InvalidArgument, "executor.TransferLeader", "CLUSTER TRANSFER LEADER cannot run inside a transaction")
		}
		if err := s.requireLeader(true); err != nil {
			return nil, err
		}
		err := s.db.TransferLeadership()
		s.auditRecord(security.ActionLeaderTransfer, "", err)
		if err != nil {
			return nil, err
		}
		return &Result{Columns: []string{"result"}, Rows: [][]types.Value{{types.TextValue("transfer_initiated")}}}, nil
	}
	if st, ok := stmt.(ast.ClusterDrain); ok {
		if err := s.authorize(stmt); err != nil {
			s.auditRecord(security.ActionClusterDrain, "", err)
			return nil, err
		}
		if s.InTxn() {
			return nil, nerr.New(nerr.InvalidArgument, "executor.ClusterDrain", "CLUSTER DRAIN cannot run inside a transaction")
		}
		// No requireLeader gate: draining is purely local to the node this
		// connection reached, unlike TransferLeader/Maintain/CreateDatabase
		// which must go through the replicated write path. A follower is
		// exactly as drainable as a leader — that is the common case for
		// planned per-node maintenance in a cluster.
		err := s.db.Drain(time.Duration(st.TimeoutMS) * time.Millisecond)
		s.auditRecord(security.ActionClusterDrain, "", err)
		if err != nil {
			return nil, err
		}
		return &Result{Columns: []string{"result"}, Rows: [][]types.Value{{types.TextValue("drain_initiated")}}}, nil
	}
	if st, ok := stmt.(ast.ClusterMaintenance); ok {
		if err := s.authorize(stmt); err != nil {
			s.auditRecord(security.ActionClusterMaintenance, "", err)
			return nil, err
		}
		if s.InTxn() {
			return nil, nerr.New(nerr.InvalidArgument, "executor.ClusterMaintenance", "CLUSTER MAINTENANCE cannot run inside a transaction")
		}
		// No requireLeader gate: like ClusterDrain, this is purely local to
		// the node this connection reached.
		if st.Enable {
			s.db.EnableMaintenanceMode()
		} else {
			s.db.DisableMaintenanceMode()
		}
		s.auditRecord(security.ActionClusterMaintenance, "", nil)
		result := "maintenance_disabled"
		if st.Enable {
			result = "maintenance_enabled"
		}
		return &Result{Columns: []string{"result"}, Rows: [][]types.Value{{types.TextValue(result)}}}, nil
	}
	if _, ok := stmt.(ast.ClusterReconcileConfirm); ok {
		if err := s.authorize(stmt); err != nil {
			s.auditRecord(security.ActionClusterReconcile, "", err)
			return nil, err
		}
		if s.InTxn() {
			return nil, nerr.New(nerr.InvalidArgument, "executor.ClusterReconcileConfirm", "CLUSTER RECONCILE CONFIRM cannot run inside a transaction")
		}
		// No requireLeader gate: like ClusterMaintenance/ClusterDrain, this
		// is purely local to the node this connection reached — the
		// replication-suspect flag it clears is itself node-local.
		err := s.db.ConfirmReplicationReconciled()
		s.auditRecord(security.ActionClusterReconcile, "", err)
		if err != nil {
			return nil, err
		}
		return &Result{Columns: []string{"result"}, Rows: [][]types.Value{{types.TextValue("replication_reconciled")}}}, nil
	}
	if st, ok := stmt.(ast.SetResourceGroup); ok {
		if err := s.authorize(stmt); err != nil {
			s.auditRecord(security.ActionResourceGroupAssign, st.Name, err)
			return nil, err
		}
		if _, ok := s.lookupResourceGroup(st.Name); !ok {
			err := nerr.New(nerr.NotFound, "executor.SetResourceGroup", "resource group not found")
			s.auditRecord(security.ActionResourceGroupAssign, st.Name, err)
			return nil, err
		}
		s.resourceGroup = st.Name
		s.qbudget = nil
		s.auditRecord(security.ActionResourceGroupAssign, st.Name, nil)
		return &Result{}, nil
	}
	if _, ok := stmt.(ast.ResetResourceGroup); ok {
		if err := s.authorize(stmt); err != nil {
			s.auditRecord(security.ActionResourceGroupAssign, "", err)
			return nil, err
		}
		s.resourceGroup = ""
		s.qbudget = nil
		s.auditRecord(security.ActionResourceGroupAssign, "", nil)
		return &Result{}, nil
	}
	if st, ok := stmt.(ast.SetConfig); ok {
		if err := s.authorize(st); err != nil {
			s.auditRecord(security.ActionConfigSet, st.Key, err)
			return nil, err
		}
		if s.InTxn() {
			return nil, nerr.New(nerr.InvalidArgument, "executor.SetConfig", "SET CONFIG cannot run inside a transaction")
		}
		// Node-local, like CLUSTER DRAIN/MAINTENANCE: each node has its own
		// nextsql.conf, so this is not routed through the replicated write
		// path and needs no requireLeader gate.
		res, err := s.db.WriteConfigSetting(st.Key, st.Value, st.Reset)
		s.auditRecord(security.ActionConfigSet, st.Key, err)
		if err != nil {
			return nil, err
		}
		restart := "no"
		if res.RestartRequired {
			restart = "yes"
		}
		return &Result{
			Columns: []string{"key", "file_value", "running_value", "restart_required"},
			Rows: [][]types.Value{{
				types.TextValue(res.Key),
				types.TextValue(res.FileValue),
				types.TextValue(res.RunningValue),
				types.TextValue(restart),
			}},
		}, nil
	}
	if _, ok := stmt.(ast.BackupDatabase); ok {
		if err := s.authorize(stmt); err != nil {
			s.auditRecord(security.ActionBackup, "", err)
			return nil, err
		}
		if s.InTxn() {
			return nil, nerr.New(nerr.InvalidArgument, "executor.BackupDatabase", "BACKUP DATABASE cannot run inside a transaction")
		}
		// Node-local: writes to this node's own backup_dir, not replicated.
		res, err := s.db.CreateBackup()
		s.auditRecord(security.ActionBackup, res.Name, err)
		if err != nil {
			return nil, err
		}
		return &Result{
			Columns: []string{"name", "checkpoint_lsn", "durable_lsn", "members"},
			Rows: [][]types.Value{{
				types.TextValue(res.Name),
				sysDec(int64(res.CheckpointLSN), 20),
				sysDec(int64(res.DurableLSN), 20),
				sysDec(int64(res.Members), 10),
			}},
		}, nil
	}
	if st, ok := stmt.(ast.VerifyBackup); ok {
		if err := s.authorize(st); err != nil {
			s.auditRecord(security.ActionBackupVerify, st.Name, err)
			return nil, err
		}
		res, err := s.db.VerifyBackup(st.Name)
		s.auditRecord(security.ActionBackupVerify, st.Name, err)
		if err != nil {
			return nil, err
		}
		ok := "no"
		if res.OK {
			ok = "yes"
		}
		return &Result{
			Columns: []string{"name", "verified", "problem"},
			Rows: [][]types.Value{{
				types.TextValue(res.Name),
				types.TextValue(ok),
				types.TextValue(res.Problem),
			}},
		}, nil
	}
	if s != nil && s.db != nil && !s.txnGuard {
		s.acquireTxnGuard()
		defer func() {
			// Autocommit and non-transaction statements release here if they
			// did not already release in commit/abort. BEGIN transfers this
			// same guard to the explicit transaction; never take a nested
			// RLock while a replicated apply writer may be queued.
			if s.x == nil {
				s.releaseTxnGuard()
			}
		}()
	}
	if isSecurityStmt(stmt) {
		if err := s.requireLeader(true); err != nil {
			return nil, err
		}
		return s.execSecurity(stmt)
	}
	if st, ok := stmt.(ast.CreateDatabase); ok {
		if err := s.authorize(st); err != nil {
			s.auditRecord(security.ActionDDL, st.Name, err)
			return nil, err
		}
		if s.InTxn() {
			return nil, nerr.New(nerr.InvalidArgument, "executor.CreateDatabase", "CREATE DATABASE cannot run inside a transaction")
		}
		if err := s.requireLeader(true); err != nil {
			return nil, err
		}
		return s.execCreateDatabase(planner.CreateDatabase{Name: st.Name, IfNotExists: st.IfNotExists})
	}
	stmt, err = s.guardLegacyTenancy(stmt)
	if err != nil {
		return nil, err
	}
	if drop, ok := stmt.(ast.DropIndex); ok {
		stmt, err = s.resolveDropIndex(drop)
		if err != nil {
			return nil, err
		}
	}
	if rebuild, ok := stmt.(ast.RebuildIndex); ok {
		stmt, err = s.resolveRebuildIndex(rebuild)
		if err != nil {
			return nil, err
		}
		if resolved, ok := stmt.(ast.RebuildIndex); ok && resolved.Online {
			if err := s.authorize(resolved); err != nil {
				s.auditRecord(security.ActionDDL, resolved.Table, err)
				return nil, err
			}
			if s.InTxn() {
				return nil, nerr.New(nerr.InvalidArgument, "executor.RebuildIndex", "REBUILD INDEX ... ONLINE cannot run inside a transaction")
			}
			if err := s.requireNotMaintenance(true); err != nil {
				return nil, err
			}
			if err := s.requireLeader(true); err != nil {
				return nil, err
			}
			res, rerr := s.rebuildIndexOnline(ctx, resolved.Table, resolved.Name)
			s.auditRecord(security.ActionDDL, resolved.Table, rerr)
			return res, rerr
		}
	}
	if isReadStmt(stmt) {
		if err := s.requireReadConsistency(); err != nil {
			return nil, err
		}
	}
	if sel, ok := s.isNoFromSelect(stmt); ok {
		// No table/index/catalog involved; bypass the normal binder entirely,
		// the same architectural precedent as the system-select bypass below.
		res, err := s.execNoFromSelect(sel)
		s.auditRecord(workflowAuditAction(stmt), sqlObject(stmt), err)
		return res, err
	}
	if sel, ok := s.isSystemSelect(stmt); ok {
		// System catalog is authoritative and tenant-aware; bypass normal binder.
		if s.InTxn() {
			// Allow system reads inside transaction as read-only.
		}
		res, err := s.execSystemSelect(sel)
		s.auditRecord(workflowAuditAction(stmt), sqlObject(stmt), err)
		return res, err
	}
	if err := s.authorize(stmt); err != nil {
		s.auditRecord(workflowAuditAction(stmt), sqlObject(stmt), err)
		return nil, err
	}
	owner := strings.TrimSpace(s.user)
	if owner == "" && s.acl == nil {
		owner = "local"
	}
	nextID := s.db.Cat.PeekNext()
	switch st := stmt.(type) {
	case ast.CreateTable, ast.CreateWorkflow, ast.CreateTrigger, ast.CreateSchedule, ast.CreateResourceGroup:
		// Reserve a catalog identity atomically. Gaps after failed DDL are
		// harmless; duplicate stable identities under concurrent DDL are not.
		nextID = s.db.Cat.NextID()
	case ast.AlterTable:
		if _, ok := st.Cmd.(ast.AlterDetachPartition); ok {
			// DETACH publishes a new standalone catalog object and therefore
			// consumes the same non-reusing identity stream as CREATE TABLE.
			nextID = s.db.Cat.NextID()
		}
	}
	bound, err := binder.BindAutomation(stmt, s.lookup, s.lookupWorkflow, s.listWorkflows, s.lookupTrigger, s.listTriggers, s.lookupSchedule, s.listSchedules, s.lookupResourceGroup, nextID, owner)
	if err != nil {
		return nil, err
	}
	plan, err := planner.Plan(bound)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nerr.New(nerr.Internal, "executor.Exec", "empty plan")
	}
	out, err := optimizer.Optimize(optimizer.Request{
		Plan:     plan,
		SQL:      sql,
		CacheKey: s.planCacheKey(sql),
		Stats:    s.lookupStats,
		Gen:      s.statsGen(),
		Cache:    s.optCache(),
	})
	if err != nil {
		return nil, err
	}
	cacheableResult := !s.InTxn() && resultCacheable(stmt) && s.db != nil && s.db.resCache != nil
	var cacheKey [32]byte
	var cacheVersion resultVersion
	if cacheableResult {
		cacheKey, err = resultCacheKey(sql, s.user, params)
		if err != nil {
			return nil, err
		}
		cacheVersion = s.db.resultVersion()
		if cacheVersion.lsn == 0 {
			cacheableResult = false
		} else if cached, ok := s.db.resCache.get(cacheKey, cacheVersion); ok {
			return cached, nil
		}
	}
	s.qbudget = scheduler.NewBudget(ctx, s.limitsOrDefault())
	defer func() {
		s.qbudget.Close()
		s.qbudget = nil
	}()
	if err := s.requireNotMaintenance(isMutating(out.Plan)); err != nil {
		return nil, err
	}
	if err := s.requireLeader(isMutating(out.Plan)); err != nil {
		return nil, err
	}
	res, runErr := s.run(ctx, out.Plan, out.Trace, sql)
	if runErr == nil && cacheableResult && s.db.resultVersion() == cacheVersion {
		s.db.resCache.put(cacheKey, cacheVersion, res)
	}
	switch stmt.(type) {
	case ast.CreateWorkflow, ast.RunWorkflow, ast.AlterWorkflow, ast.DropWorkflow, ast.CreateTrigger, ast.AlterTrigger, ast.DropTrigger, ast.CreateSchedule, ast.AlterSchedule, ast.DropSchedule, ast.CancelTask:
		s.auditRecord(workflowAuditAction(stmt), sqlObject(stmt), runErr)
	case ast.DropIndex, ast.RebuildIndex:
		s.auditRecord(security.ActionDDL, sqlObject(stmt), runErr)
	case ast.Subscribe:
		s.auditRecord(security.ActionCDCSubscribe, sqlObject(stmt), runErr)
	}
	return res, runErr
}

// requireNotMaintenance rejects a mutating statement while this node is in
// maintenance mode (CLUSTER MAINTENANCE ENABLE) or has tripped its disk
// watermark (see DB.SetDiskWatermarkTripped). Uses the same isMutating
// classification as requireLeader, so BEGIN is blocked along with DML/DDL —
// a read-only transaction cannot be distinguished from a write one before it
// opens; run autocommit SELECTs to inspect state during maintenance.
func (s *Session) requireNotMaintenance(write bool) error {
	if !write || s == nil || s.db == nil {
		return nil
	}
	if s.db.InMaintenanceMode() {
		return nerr.New(nerr.Unavailable, "executor.MaintenanceMode", "server is in maintenance mode; writes are rejected until CLUSTER MAINTENANCE DISABLE")
	}
	if s.db.DiskWatermarkTripped() {
		return nerr.New(nerr.Unavailable, "executor.DiskWatermark", "server is low on disk space; writes are rejected until free space recovers")
	}
	return nil
}

func (s *Session) requireLeader(write bool) error {
	if !write || s == nil || s.db == nil || s.db.gate == nil {
		return nil
	}
	return s.db.gate.AllowWrite()
}

// requireReadConsistency enforces the session read-consistency mode for a
// non-mutating statement. A single-node deployment has no gate and is
// unaffected.
//
//   - STRONG: served on the leader behind a verified Raft read barrier, so it
//     observes every previously acknowledged write. A follower — or a leader
//     that can no longer reach a quorum — is rejected with leader-routing
//     guidance rather than served stale.
//   - BOUNDED: served from this node's locally applied state, but only while
//     the node is the leader or a follower still within MaxStaleness of the
//     leader. A node that has fallen further behind is rejected so the caller
//     routes elsewhere.
//   - STALE: served from local applied state with no freshness check.
func (s *Session) requireReadConsistency() error {
	if s == nil || s.db == nil || s.db.gate == nil {
		return nil
	}
	switch s.readConsistency {
	case ReadStale:
		return nil
	case ReadBounded:
		fg, ok := s.db.gate.(FollowerReadGate)
		if !ok {
			return nil
		}
		return fg.FollowerReadHealthy(s.boundedStaleness())
	default:
		rg, ok := s.db.gate.(ReadGate)
		if !ok {
			return nil
		}
		return rg.StrongReadBarrier()
	}
}

// isReadStmt reports whether a statement only reads user data and is therefore
// subject to the session read-consistency barrier.
func isReadStmt(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case ast.Select, ast.SetOperation, ast.With:
		return true
	default:
		return false
	}
}

func isMutating(plan planner.Logical) bool {
	switch plan.(type) {
	case planner.CreateTable, planner.CreateDatabase, planner.CreateWorkflow, planner.AlterWorkflow, planner.DropWorkflow, planner.RunWorkflow, planner.CreateTrigger, planner.AlterTrigger, planner.DropTrigger, planner.CreateSchedule, planner.AlterSchedule, planner.DropSchedule, planner.CreateResourceGroup, planner.AlterResourceGroup, planner.DropResourceGroup, planner.CancelTask, planner.DropTable, planner.DropIndex, planner.RebuildIndex, planner.AlterTable, planner.CreateIndex, planner.Insert, planner.Upsert, planner.Update, planner.Delete, planner.Begin, planner.Commit, planner.Rollback:
		return true
	default:
		return false
	}
}

func (s *Session) lookupParam(name string) (types.Value, error) {
	if s == nil {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unbound parameter")
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 1 {
		if n <= len(s.params) {
			return s.params[n-1].Value.Clone(), nil
		}
	}
	for _, p := range s.params {
		if p.Name != "" && p.Name == name {
			return p.Value.Clone(), nil
		}
	}
	return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unbound parameter")
}

// Query runs SQL and returns a result that can stream batches. Rows are
// still filled for statements that fit the memory budget.
func (s *Session) Query(sql string) (*Result, error) {
	return s.QueryContext(context.Background(), sql, nil)
}

func (s *Session) QueryContext(ctx context.Context, sql string, params []Param) (*Result, error) {
	res, err := s.ExecContext(ctx, sql, params)
	if err != nil {
		return nil, err
	}
	if res != nil && len(res.Rows) > 0 {
		i := 0
		sz := s.limitsOrDefault().BatchSize
		if sz < 1 {
			sz = scheduler.DefaultBatch
		}
		cols := make([]types.Type, 0)
		if len(res.Rows[0]) > 0 {
			cols = make([]types.Type, len(res.Rows[0]))
			for j, v := range res.Rows[0] {
				cols[j] = v.Typ
			}
		}
		res.next = func() (*vector.Batch, error) {
			if i >= len(res.Rows) {
				return nil, nil
			}
			b := vector.New(cols, sz)
			for i < len(res.Rows) && b.AppendRow(res.Rows[i]) {
				i++
			}
			return b, nil
		}
	}
	return res, nil
}

// Stream runs SQL and delivers SELECT rows in batches without requiring
// the caller to hold the full result. DML still executes to completion.
func (s *Session) Stream(sql string, fn func(*vector.Batch) error) error {
	if fn == nil {
		return nerr.New(nerr.InvalidArgument, "executor.Stream", "nil callback")
	}
	res, err := s.Query(sql)
	if err != nil {
		return err
	}
	defer res.Close()
	if res.next == nil {
		return nil
	}
	for {
		b, err := res.NextBatch()
		if err != nil {
			return err
		}
		if b == nil {
			return nil
		}
		if err := fn(b); err != nil {
			return err
		}
	}
}

func (s *Session) lookupStats(name string) (*catalog.TableStats, bool) {
	if s.pending != nil && s.pending.stats != nil {
		if st, ok := s.pending.stats[name]; ok {
			return st.Clone(), true
		}
	}
	return s.db.Cat.Stats(name)
}

func (s *Session) lookupWorkflow(name string) (*catalog.Workflow, bool) {
	if s.workflowOverlay != nil {
		if w, ok := s.workflowOverlay[name]; ok {
			if w == nil {
				return nil, false
			}
			return w.Clone(), true
		}
	}
	return s.db.workflow(name)
}

func (s *Session) listWorkflows() []*catalog.Workflow {
	byName := make(map[string]*catalog.Workflow)
	for _, w := range s.db.workflowList() {
		byName[w.Name] = w
	}
	for name, w := range s.workflowOverlay {
		if w == nil {
			delete(byName, name)
			continue
		}
		if clone := w.Clone(); clone != nil {
			byName[name] = clone
		}
	}
	out := make([]*catalog.Workflow, 0, len(byName))
	for _, w := range byName {
		out = append(out, w)
	}
	return out
}

func (s *Session) lookupTrigger(name string) (*catalog.Trigger, bool) {
	if s.triggerOverlay != nil {
		if trigger, ok := s.triggerOverlay[name]; ok {
			if trigger == nil {
				return nil, false
			}
			return trigger.Clone(), true
		}
	}
	return s.db.trigger(name)
}

func (s *Session) listTriggers() []*catalog.Trigger {
	byName := make(map[string]*catalog.Trigger)
	for _, trigger := range s.db.triggerList() {
		byName[trigger.Name] = trigger
	}
	for name, trigger := range s.triggerOverlay {
		if trigger == nil {
			delete(byName, name)
			continue
		}
		if clone := trigger.Clone(); clone != nil {
			byName[name] = clone
		}
	}
	out := make([]*catalog.Trigger, 0, len(byName))
	for _, trigger := range byName {
		out = append(out, trigger)
	}
	return out
}

func (s *Session) lookupSchedule(name string) (*catalog.Schedule, bool) {
	if s.scheduleOverlay != nil {
		if schedule, ok := s.scheduleOverlay[name]; ok {
			if schedule == nil {
				return nil, false
			}
			return schedule.Clone(), true
		}
	}
	return s.db.schedule(name)
}

func (s *Session) listSchedules() []*catalog.Schedule {
	byName := make(map[string]*catalog.Schedule)
	for _, schedule := range s.db.scheduleList() {
		byName[schedule.Name] = schedule
	}
	for name, schedule := range s.scheduleOverlay {
		if schedule == nil {
			delete(byName, name)
			continue
		}
		if clone := schedule.Clone(); clone != nil {
			byName[name] = clone
		}
	}
	out := make([]*catalog.Schedule, 0, len(byName))
	for _, schedule := range byName {
		out = append(out, schedule)
	}
	return out
}

func (s *Session) lookupResourceGroup(name string) (*catalog.ResourceGroup, bool) {
	if s.resourceGroupOverlay != nil {
		if group, ok := s.resourceGroupOverlay[name]; ok {
			if group == nil {
				return nil, false
			}
			return group.Clone(), true
		}
	}
	return s.db.resourceGroup(name)
}

func (s *Session) listResourceGroups() []*catalog.ResourceGroup {
	byName := make(map[string]*catalog.ResourceGroup)
	for _, group := range s.db.resourceGroupList() {
		byName[group.Name] = group
	}
	for name, group := range s.resourceGroupOverlay {
		if group == nil {
			delete(byName, name)
			continue
		}
		if clone := group.Clone(); clone != nil {
			byName[name] = clone
		}
	}
	out := make([]*catalog.ResourceGroup, 0, len(byName))
	for _, group := range byName {
		out = append(out, group)
	}
	return out
}

func (s *Session) statsGen() uint64 {
	g := s.db.Cat.Generation()
	if s.pending != nil && len(s.pending.stats) > 0 {
		return g + 1
	}
	return g
}

func (s *Session) optCache() *optimizer.Cache {
	if s.pending != nil && len(s.pending.stats) > 0 {
		return nil
	}
	return s.db.optCache
}

func (s *Session) run(ctx context.Context, plan planner.Logical, trace *optimizer.Node, sql string) (*Result, error) {
	switch p := plan.(type) {
	case planner.Begin:
		return s.begin(p.Iso)
	case planner.Commit:
		return s.commit()
	case planner.Rollback:
		return s.rollback()
	case planner.Subscribe:
		if s.x != nil {
			return nil, nerr.New(nerr.InvalidArgument, "executor.Subscribe", "SUBSCRIBE cannot run inside a transaction")
		}
		return s.execSubscribe(ctx, p)
	}

	if ex, ok := plan.(planner.Explain); ok {
		return s.execExplain(ex, trace, sql)
	}

	auto := false
	if s.x == nil {
		var err error
		if isMutating(plan) {
			err = s.start(txn.SnapshotIsolation)
		} else {
			err = s.startRead(txn.SnapshotIsolation)
		}
		if err != nil {
			return nil, err
		}
		auto = true
	}
	prev := s.trace
	s.trace = trace
	s.resetFKStmt()
	s.triggerBroken = false
	res, err := s.execPlan(plan)
	if err != nil {
		if _, workflow := plan.(planner.RunWorkflow); workflow {
			_ = s.abort()
			return nil, err
		}
	}
	broken := s.fkBroken
	triggerBroken := s.triggerBroken
	s.triggerBroken = false
	s.resetFKStmt()
	s.trace = prev
	if broken {
		_ = s.abort()
		return nil, err
	}
	if triggerBroken {
		_ = s.abort()
		return nil, err
	}
	if auto {
		if err != nil {
			_ = s.abort()
			return nil, err
		}
		if _, cerr := s.commit(); cerr != nil {
			return nil, cerr
		}
	}
	if err == nil && trace != nil && s.db.feedback != nil && sql != "" {
		s.db.feedback.Record(sql, trace)
	}
	return res, err
}

func (s *Session) startRead(iso txn.Isolation) error {
	s.acquireTxnGuard()
	owner, err := s.db.CatTree.BeginRead(iso)
	if err != nil {
		s.releaseTxnGuard()
		return err
	}
	s.x = newXact(owner, iso)
	s.setTxnActive(owner.Handle(), s.x.readOnly)
	if s.overlay == nil {
		s.overlay = make(map[string]*catalog.Table)
	}
	if s.workflowOverlay == nil {
		s.workflowOverlay = make(map[string]*catalog.Workflow)
	}
	if s.triggerOverlay == nil {
		s.triggerOverlay = make(map[string]*catalog.Trigger)
	}
	if s.scheduleOverlay == nil {
		s.scheduleOverlay = make(map[string]*catalog.Schedule)
	}
	if s.resourceGroupOverlay == nil {
		s.resourceGroupOverlay = make(map[string]*catalog.ResourceGroup)
	}
	s.pending = newPending()
	return nil
}

func (s *Session) start(iso txn.Isolation) error {
	s.acquireTxnGuard()
	owner, err := s.db.CatTree.BeginTxn(iso)
	if err != nil {
		s.releaseTxnGuard()
		return err
	}
	s.x = newXact(owner, iso)
	s.setTxnActive(owner.Handle(), s.x.readOnly)
	if s.overlay == nil {
		s.overlay = make(map[string]*catalog.Table)
	}
	if s.workflowOverlay == nil {
		s.workflowOverlay = make(map[string]*catalog.Workflow)
	}
	if s.triggerOverlay == nil {
		s.triggerOverlay = make(map[string]*catalog.Trigger)
	}
	if s.scheduleOverlay == nil {
		s.scheduleOverlay = make(map[string]*catalog.Schedule)
	}
	if s.resourceGroupOverlay == nil {
		s.resourceGroupOverlay = make(map[string]*catalog.ResourceGroup)
	}
	s.pending = newPending()
	return nil
}

func (s *Session) acquireTxnGuard() {
	if s != nil && s.db != nil && !s.txnGuard {
		s.db.applyMu.RLock()
		s.txnGuard = true
	}
}

func (s *Session) releaseTxnGuard() {
	if s != nil && s.db != nil && s.txnGuard {
		s.txnGuard = false
		s.db.applyMu.RUnlock()
	}
}

func (s *Session) begin(iso txn.Isolation) (*Result, error) {
	if s.x != nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.begin", "transaction already active")
	}
	if err := s.start(iso); err != nil {
		return nil, err
	}
	return &Result{}, nil
}

func (s *Session) commit() (*Result, error) {
	if s != nil && s.fkBroken {
		_ = s.abort()
		return nil, nerr.New(nerr.Exhausted, "executor.commit", "foreign key cascade exceeded limit")
	}
	if s.x == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.commit", "no active transaction")
	}
	if err := s.x.commit(); err != nil {
		s.x = nil
		s.clearTxnActive()
		s.overlay = nil
		s.workflowOverlay = nil
		s.triggerOverlay = nil
		s.scheduleOverlay = nil
		s.resourceGroupOverlay = nil
		s.pending = nil
		s.dirtyHNSW = false
		s.pendingHNSW = nil
		s.dirtyIVF = false
		s.pendingIVF = nil
		s.releaseTxnGuard()
		return nil, err
	}
	if s.pending != nil {
		for old, neu := range s.pending.renames {
			s.db.renameTableMaps(old, neu)
		}
	}
	for name, t := range s.overlay {
		if t == nil {
			if s.pending != nil {
				if _, renamed := s.pending.renames[name]; renamed {
					s.db.Cat.Remove(name)
					continue
				}
			}
			s.db.Cat.Remove(name)
			s.db.dropTableMaps(name)
			continue
		}
		s.db.Cat.Put(t)
		s.db.dropMissingIndexes(t)
	}
	for name, w := range s.workflowOverlay {
		if w == nil {
			s.db.removeWorkflow(name)
			continue
		}
		s.db.putWorkflow(w)
	}
	for name, trigger := range s.triggerOverlay {
		if trigger == nil {
			s.db.removeTrigger(name)
			continue
		}
		s.db.putTrigger(trigger)
	}
	for name, schedule := range s.scheduleOverlay {
		if schedule == nil {
			s.db.removeSchedule(name)
			continue
		}
		s.db.putSchedule(schedule)
	}
	for name, group := range s.resourceGroupOverlay {
		if group == nil {
			s.db.removeResourceGroup(name)
			continue
		}
		s.db.putResourceGroup(group)
	}
	if s.pending != nil {
		for name, tr := range s.pending.heaps {
			if t, dropped := s.overlay[name]; dropped && t == nil {
				continue
			}
			s.db.putHeap(name, tr)
		}
		for name, tr := range s.pending.vecs {
			if t, dropped := s.overlay[name]; dropped && t == nil {
				continue
			}
			s.db.putVec(name, tr)
		}
		for name, tr := range s.pending.idxs {
			table := name
			if i := strings.IndexByte(name, '/'); i >= 0 {
				table = name[:i]
			}
			if t, dropped := s.overlay[table]; dropped && t == nil {
				continue
			}
			s.db.mu.Lock()
			s.db.idxs[name] = tr
			s.db.mu.Unlock()
		}
		for key, tr := range s.pending.partHeaps {
			table := key
			if i := strings.IndexByte(key, '#'); i >= 0 {
				table = key[:i]
			}
			if t, dropped := s.overlay[table]; dropped && t == nil {
				continue
			}
			s.db.mu.Lock()
			s.db.partHeaps[key] = tr
			s.db.mu.Unlock()
		}
		for key, tr := range s.pending.partVecs {
			table := key
			if i := strings.IndexByte(key, '#'); i >= 0 {
				table = key[:i]
			}
			if t, dropped := s.overlay[table]; dropped && t == nil {
				continue
			}
			s.db.mu.Lock()
			s.db.partVecs[key] = tr
			s.db.mu.Unlock()
		}
		for key, tr := range s.pending.partIdxs {
			table := key
			if i := strings.IndexByte(key, '#'); i >= 0 {
				table = key[:i]
			}
			if i := strings.IndexByte(table, '/'); i >= 0 {
				table = table[:i]
			}
			// key is table#pid/index; extract table part before '#'
			if idx := strings.IndexByte(key, '#'); idx >= 0 {
				table = key[:idx]
			}
			if t, dropped := s.overlay[table]; dropped && t == nil {
				continue
			}
			s.db.mu.Lock()
			s.db.partIdxs[key] = tr
			s.db.mu.Unlock()
		}
		for name, st := range s.pending.stats {
			if t, dropped := s.overlay[name]; dropped && t == nil {
				continue
			}
			s.db.Cat.SetStats(st)
		}
	}
	s.installPendingHNSW()
	var reclaims []format.PageID
	var partitionDrops []string
	var indexDrops []indexMapDrop
	maintenanceChanges := make(map[string]uint64)
	var taskCancels []string
	if s.pending != nil {
		reclaims = append(reclaims, s.pending.reclaims...)
		partitionDrops = append(partitionDrops, s.pending.partitionDrops...)
		indexDrops = append(indexDrops, s.pending.indexDrops...)
		for name, changed := range s.pending.maintenanceChanges {
			maintenanceChanges[name] = changed
		}
		taskCancels = append(taskCancels, s.pending.taskCancels...)
	}
	s.x = nil
	s.clearTxnActive()
	s.overlay = nil
	s.workflowOverlay = nil
	s.triggerOverlay = nil
	s.scheduleOverlay = nil
	s.resourceGroupOverlay = nil
	s.pending = nil
	s.dirtyHNSW = false
	s.pendingHNSW = nil
	s.dirtyIVF = false
	s.pendingIVF = nil
	if s.db != nil && s.db.metrics != nil {
		s.db.metrics.AddCommit()
	}
	s.releaseTxnGuard()
	if len(reclaims) > 0 || len(partitionDrops) > 0 || len(indexDrops) > 0 {
		s.db.queueCommittedReclaims(reclaims, partitionDrops, indexDrops)
	}
	if len(maintenanceChanges) > 0 {
		if s.automaticMaintenance == nil {
			s.automaticMaintenance = make(map[string]uint64)
		}
		for name, changed := range maintenanceChanges {
			s.automaticMaintenance[name] += changed
		}
	}
	for _, id := range taskCancels {
		s.db.signalTaskCancellation(id)
	}
	return &Result{}, nil
}

func (s *Session) rollback() (*Result, error) {
	if s.x == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.rollback", "no active transaction")
	}
	err := s.abort()
	if s.db != nil && s.db.metrics != nil {
		s.db.metrics.AddRollback()
	}
	return &Result{}, err
}

// Abort force-rolls-back this session's open transaction, if any, releasing
// its locks and resources without a client-issued ROLLBACK. It is a no-op
// when no transaction is open. Used by the protocol server when a connection
// is torn down (idle-in-transaction timeout, a forced drain close, or a bare
// disconnect) while a transaction is still open, so the transaction's locks
// are never left held by a session nothing will ever resume.
func (s *Session) Abort() error {
	if s == nil || s.x == nil {
		return nil
	}
	return s.abort()
}

func (s *Session) abort() error {
	var err error
	if s.x != nil {
		err = s.x.rollback()
		s.reclaimEmptyTreesOnRollback()
	}
	s.fkBroken = false
	s.conflictWrite = false
	s.x = nil
	s.clearTxnActive()
	s.overlay = nil
	s.workflowOverlay = nil
	s.triggerOverlay = nil
	s.scheduleOverlay = nil
	s.resourceGroupOverlay = nil
	s.pending = nil
	s.dirtyHNSW = false
	s.pendingHNSW = nil
	s.dirtyIVF = false
	s.pendingIVF = nil
	s.releaseTxnGuard()
	return err
}

// reclaimEmptyTreesOnRollback frees every page of a tree this transaction
// created from nothing (a fresh CREATE TABLE/CREATE INDEX heap, WasEmpty()
// true — see its doc comment) once the transaction has rolled back. Nothing
// outside this transaction could ever have referenced such a tree — its
// catalog row was never committed — so unlike an ordinary pre-existing
// tree there is no concurrent-writer or structural-sharing hazard, and the
// whole tree is safe to discard wholesale via the tree's own OwnedPages
// walk, exactly like abortOnlineBuild already does for an abandoned online
// rebuild's shadow tree. Left unfreed on any error (OwnedPages fails closed
// on an unexpected shape, or the tree still has no root at all because
// nothing was ever inserted) rather than risk reclaiming the wrong pages;
// worst case is a harmless leaked page, not corruption.
func (s *Session) reclaimEmptyTreesOnRollback() {
	if s == nil || s.x == nil || s.db == nil {
		return
	}
	for tree, part := range s.x.parts {
		if tree == nil || !part.WasEmpty() {
			continue
		}
		if pages, perr := tree.OwnedPages(); perr == nil && len(pages) > 0 {
			s.db.queueCommittedReclaims(pages, nil, nil)
		}
	}
}

func (s *Session) heapOf(t *catalog.Table) (*btree.Tree, error) {
	if s.pending != nil {
		if tr, ok := s.pending.heaps[t.Name]; ok {
			return tr, nil
		}
	}
	return s.db.heap(t.Name)
}

func (s *Session) indexOf(t *catalog.Table, idx catalog.Index) (*btree.Tree, error) {
	k := idxKey(t.Name, idx.Name)
	if s.pending != nil {
		if tr, ok := s.pending.idxs[k]; ok {
			return tr, nil
		}
	}
	return s.db.index(t.Name, idx.Name)
}
