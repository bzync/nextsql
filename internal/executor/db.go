package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/maintenance"
	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

// DB is a local NextSQL database: storage engine + catalog + table trees.
type DB struct {
	path        string
	keys        crypto.KeyProvider
	bufferPages int
	// databaseName is the logical served name used by system.storage and
	// SHOW DATABASES. It is deliberately separate from path: filesystem
	// layout is not SQL-visible metadata. Empty means "default".
	databaseName atomic.Pointer[string]

	Eng     *storage.Engine
	Cat     *catalog.Store
	CatTree *btree.Tree

	mu        sync.RWMutex
	heaps     map[string]*btree.Tree
	idxs      map[string]*btree.Tree
	vecs      map[string]*btree.Tree
	partHeaps map[string]*btree.Tree // key: partitionHeapKey(table, partitionID)
	partVecs  map[string]*btree.Tree // key: partitionHeapKey(table, partitionID)
	partIdxs  map[string]*btree.Tree // key: partitionIndexKey(table, partitionID, indexName)
	workflows map[string]*catalog.Workflow
	triggers  map[string]*catalog.Trigger
	schedules map[string]*catalog.Schedule
	resGroups map[string]*catalog.ResourceGroup
	// resGroupGates caches a lazily-built, process-wide scheduler.Admission
	// per resource group with a non-zero MaxConcurrency, keyed by group
	// name. It is a pure cache: dropped (not migrated) on any change to the
	// backing group, guarded by mu like resGroups itself. A group with
	// MaxConcurrency == 0 never gets an entry (0 means unbounded for
	// resource groups, unlike scheduler.NewAdmission's own <1-means-default
	// convention, so the two must never be confused).
	resGroupGates map[string]*scheduler.Admission
	hnswMu        sync.RWMutex
	hnswGen       uint64
	hnsw          map[string]*lockedMem
	ivf           map[string]*lockedIVF // process-local committed IVF copies, same gen/lock as hnsw
	optCache      *optimizer.Cache
	resCache      *resultCache
	feedback      *optimizer.Feedback
	sched         *scheduler.Pool
	metrics       *metrics.Registry
	admit         *scheduler.Admission
	maint         *maintenance.Manager
	// drainFn is set by the embedding server (nextsqld) to receive CLUSTER
	// DRAIN requests issued over SQL. Nil in embedded/CLI use, where there
	// is no listening protocol.Server to drain.
	drainFn func(timeout time.Duration)

	// maintenanceMode gates every mutating statement with Unavailable while
	// set, node-local like drainFn (no Raft replication — see
	// CLUSTER MAINTENANCE's doc comment in internal/sql/ast).
	maintenanceMode atomic.Bool

	// diskWatermarkTripped gates every mutating statement with Unavailable
	// while set, same enforcement point as maintenanceMode but a distinct
	// flag: it is driven automatically by cmd/nextsqld's disk-watermark
	// monitor (see config.Config.DiskWatermarkThresholds), not by an
	// operator's CLUSTER MAINTENANCE ENABLE, so the two must not be
	// conflated — clearing one must never clear the other.
	diskWatermarkTripped atomic.Bool

	applyMu        sync.RWMutex
	gate           WriteGate
	rebuildMu      sync.RWMutex
	rebuilds       map[string]*rebuildProgress
	onlineMu       sync.RWMutex
	onlineBuilds   map[string]*onlineBuild // key: idxKey(table, index)
	reclaimMu      sync.Mutex
	reclaimErr     error
	reclaimPending []format.PageID
	reclaimPartMap []string
	reclaimIdxMap  []indexMapDrop
	taskCancelMu   sync.Mutex
	taskCancels    map[string]context.CancelFunc
	idempotencyMu  sync.Mutex
	walRetention   atomic.Uint64

	// sessMu/sessions/nextSessID back system.sessions, system.active_queries,
	// and system.transactions: a process-local, node-local registry of live
	// Session objects. Entries are ephemeral (not persisted, not replicated).
	sessMu     sync.RWMutex
	sessions   map[uint64]*Session
	nextSessID atomic.Uint64

	// cdcMu/cdcSubs/nextCDCID back system.change_streams: a process-local
	// registry of open CDC subscriptions, keyed by an opaque id assigned at
	// Subscribe time.
	cdcMu     sync.RWMutex
	cdcSubs   map[uint64]*cdcSubInfo
	nextCDCID atomic.Uint64
}

// SetDatabaseName publishes the logical served database name for
// introspection. It never exposes or derives the storage path.
func (db *DB) SetDatabaseName(name string) {
	if db == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	db.databaseName.Store(&name)
}

// DatabaseName returns the logical served database name. Embedded/single-user
// databases that have no configured routing name use the native default.
func (db *DB) DatabaseName() string {
	if db == nil {
		return "default"
	}
	if name := db.databaseName.Load(); name != nil && *name != "" {
		return *name
	}
	return "default"
}

// cdcSubInfo is one open CDC subscription's introspectable state. lsn is
// updated via atomic.Uint64 from the subscribing session's own goroutine
// (Subscription.Token/Lag are not safe to call cross-goroutine), then read
// from any session evaluating system.change_streams.
type cdcSubInfo struct {
	table string
	lsn   atomic.Uint64
}

// RegisterSession adds s to the live-session registry consulted by
// system.sessions / system.active_queries / system.transactions, and
// assigns it a stable per-process session id. The caller that owns the
// session's lifecycle (the protocol server, for a network connection) must
// call UnregisterSession when the session ends.
func (db *DB) RegisterSession(s *Session) uint64 {
	if db == nil || s == nil {
		return 0
	}
	id := db.nextSessID.Add(1)
	s.id = id
	s.connectedAt = time.Now()
	db.sessMu.Lock()
	if db.sessions == nil {
		db.sessions = make(map[uint64]*Session)
	}
	db.sessions[id] = s
	db.sessMu.Unlock()
	return id
}

// UnregisterSession removes a session from the live-session registry.
func (db *DB) UnregisterSession(id uint64) {
	if db == nil || id == 0 {
		return
	}
	db.sessMu.Lock()
	delete(db.sessions, id)
	db.sessMu.Unlock()
}

// LiveSessions returns a snapshot of currently registered sessions.
func (db *DB) LiveSessions() []*Session {
	if db == nil {
		return nil
	}
	db.sessMu.RLock()
	defer db.sessMu.RUnlock()
	out := make([]*Session, 0, len(db.sessions))
	for _, s := range db.sessions {
		out = append(out, s)
	}
	return out
}

// registerCDCSubscription adds an open CDC subscription to the registry
// consulted by system.change_streams, seeded with its starting LSN.
func (db *DB) registerCDCSubscription(table string, startLSN uint64) uint64 {
	if db == nil {
		return 0
	}
	id := db.nextCDCID.Add(1)
	info := &cdcSubInfo{table: table}
	info.lsn.Store(startLSN)
	db.cdcMu.Lock()
	if db.cdcSubs == nil {
		db.cdcSubs = make(map[uint64]*cdcSubInfo)
	}
	db.cdcSubs[id] = info
	db.cdcMu.Unlock()
	return id
}

// unregisterCDCSubscription removes a closed subscription from the registry.
func (db *DB) unregisterCDCSubscription(id uint64) {
	if db == nil || id == 0 {
		return
	}
	db.cdcMu.Lock()
	delete(db.cdcSubs, id)
	db.cdcMu.Unlock()
}

// updateCDCSubscriptionLSN publishes a subscription's latest observed commit
// LSN. Must only be called from the subscribing session's own goroutine.
func (db *DB) updateCDCSubscriptionLSN(id uint64, lsn uint64) {
	if db == nil || id == 0 {
		return
	}
	db.cdcMu.RLock()
	info := db.cdcSubs[id]
	db.cdcMu.RUnlock()
	if info != nil {
		info.lsn.Store(lsn)
	}
}

// CDCSubscriptionInfo is a snapshot of one open CDC subscription.
type CDCSubscriptionInfo struct {
	Table string
	LSN   uint64
}

// CDCSubscriptions returns a snapshot of currently open CDC subscriptions.
func (db *DB) CDCSubscriptions() []CDCSubscriptionInfo {
	if db == nil {
		return nil
	}
	db.cdcMu.RLock()
	defer db.cdcMu.RUnlock()
	out := make([]CDCSubscriptionInfo, 0, len(db.cdcSubs))
	for _, info := range db.cdcSubs {
		out = append(out, CDCSubscriptionInfo{Table: info.table, LSN: info.lsn.Load()})
	}
	return out
}

// LockSnapshot returns a snapshot of every key/range lock currently held in
// this database's storage engine, for system.locks. table_name is only as
// good as the tags threaded through btree.Tree.SetName at the executor's
// tree-resolver call sites (db.heap/db.index/db.vecStore and the partition
// equivalents); a lock acquired through an untagged path reports "".
func (db *DB) LockSnapshot() []txn.LockInfo {
	if db == nil || db.Eng == nil || db.Eng.TM == nil || db.Eng.TM.Locks == nil {
		return nil
	}
	return db.Eng.TM.Locks.Snapshot()
}

// SetLockWaitTimeout bounds how long a contended, non-deadlocking key/range
// lock wait blocks before failing Exhausted. d <= 0 (the default) blocks
// indefinitely, matching pre-P27 behavior.
func (db *DB) SetLockWaitTimeout(d time.Duration) {
	if db == nil || db.Eng == nil || db.Eng.TM == nil || db.Eng.TM.Locks == nil {
		return
	}
	db.Eng.TM.Locks.SetWaitTimeout(d)
}

type IndexRebuildProgress struct {
	Table, Index string
	Phase        string
	Rows         int64
	Entries      int64
	Started      time.Time
}

// SetStorageCapBytes limits how far this database's data file may grow;
// capBytes == 0 disables the cap. Once at the cap, statements that need a new
// page (INSERT, a row-splitting UPDATE, index growth) fail with nerr.Exhausted;
// DELETE, ROLLBACK, and in-place UPDATE keep working. The cap covers the data
// file only (not WAL or UNDO) and is not persisted; a hosting deployment sets
// it from the realm/database StorageCapBytes at open time.
func (db *DB) SetStorageCapBytes(capBytes uint64) {
	if db == nil {
		return
	}
	db.Eng.SetStorageCapBytes(capBytes)
}

// StorageCapBytes reports the current data-file growth cap (0 = none).
func (db *DB) StorageCapBytes() uint64 {
	if db == nil {
		return 0
	}
	return db.Eng.StorageCapBytes()
}

// ResultCacheStats returns process-local query-result cache diagnostics.
func (db *DB) ResultCacheStats() ResultCacheStats {
	if db == nil {
		return ResultCacheStats{}
	}
	return db.resCache.stats()
}

func (db *DB) resultVersion() resultVersion {
	if db == nil || db.Eng == nil || db.Eng.WAL == nil || db.Cat == nil {
		return resultVersion{}
	}
	return resultVersion{lsn: db.Eng.WAL.DurableLSN(), cat: db.Cat.Generation()}
}

// CleanupDeadVersions physically removes at most limit committed MVCC
// tombstones across catalog, heap, and index trees. It serializes with SQL
// transactions and returns partial progress with the first error.
func (db *DB) CleanupDeadVersions(limit int) (int, error) {
	if db == nil || db.Eng == nil || limit < 1 {
		return 0, nerr.New(nerr.InvalidArgument, "executor.CleanupDeadVersions", "database and positive limit are required")
	}
	started := time.Now()
	n, err := db.maint.RunBudgeted("dead_versions", "database", func(budget *maintenance.Budget) (int, error) {
		return db.cleanupDeadVersions(limit, budget)
	})
	if db.metrics != nil {
		db.metrics.ObserveMaintenance(int64(n), time.Since(started), err)
	}
	return n, err
}

func (db *DB) cleanupDeadVersions(limit int, budget *maintenance.Budget) (int, error) {
	if db.gate != nil {
		if err := db.gate.AllowWrite(); err != nil {
			return 0, err
		}
	}
	db.applyMu.Lock()
	defer db.applyMu.Unlock()
	if db.Eng.TM != nil && db.Eng.TM.LiveSnapshots() != 0 {
		return 0, nerr.New(nerr.Unavailable, "executor.CleanupDeadVersions", "transactions are active")
	}

	db.mu.RLock()
	trees := make([]*btree.Tree, 0, 1+len(db.heaps)+len(db.idxs)+len(db.vecs)+len(db.partHeaps)+len(db.partVecs)+len(db.partIdxs))
	trees = append(trees, db.CatTree)
	seen := map[*btree.Tree]struct{}{db.CatTree: {}}
	for _, group := range []map[string]*btree.Tree{db.heaps, db.idxs, db.vecs, db.partHeaps, db.partVecs, db.partIdxs} {
		for _, tr := range group {
			if tr != nil {
				if _, ok := seen[tr]; !ok {
					seen[tr] = struct{}{}
					trees = append(trees, tr)
				}
			}
		}
	}
	db.mu.RUnlock()

	n, err := cleanupDeadTrees(trees, limit, budget)
	if err != nil {
		return n, err
	}
	if db.Eng.Undo != nil {
		if err := db.Eng.Undo.VacuumBudgeted(budget); err != nil {
			return n, err
		}
	}
	if horizon := format.LSN(db.walRetention.Load()); horizon != 0 && db.Eng.WAL != nil {
		if _, err := db.Eng.WAL.PruneArchivedBefore(horizon, budget); err != nil {
			return n, err
		}
	}
	return n, nil
}

// CleanupTableDeadVersions is CleanupDeadVersions scoped to one table's heap,
// vector store, and secondary indexes.
func (db *DB) CleanupTableDeadVersions(name string, limit int) (int, error) {
	if db == nil || db.Eng == nil || limit < 1 {
		return 0, nerr.New(nerr.InvalidArgument, "executor.CleanupTableDeadVersions", "database and positive limit are required")
	}
	started := time.Now()
	n, err := db.maint.RunBudgeted("dead_versions", name, func(budget *maintenance.Budget) (int, error) {
		return db.cleanupTableDeadVersions(name, limit, budget)
	})
	if db.metrics != nil {
		db.metrics.ObserveMaintenance(int64(n), time.Since(started), err)
	}
	return n, err
}

func (db *DB) cleanupTableDeadVersions(name string, limit int, budget *maintenance.Budget) (int, error) {
	if db.gate != nil {
		if err := db.gate.AllowWrite(); err != nil {
			return 0, err
		}
	}
	db.applyMu.Lock()
	defer db.applyMu.Unlock()
	tab, ok := db.Cat.Get(name)
	if !ok {
		return 0, nerr.New(nerr.NotFound, "executor.CleanupTableDeadVersions", "unknown table")
	}
	trees, err := db.tableMaintenanceTrees(tab)
	if err != nil {
		return 0, err
	}
	return cleanupDeadTrees(trees, limit, budget)
}

// tableMaintenanceTrees resolves every physical tree owned by tab while the
// caller holds applyMu exclusively. Catalog metadata is authoritative: a
// missing registered tree fails closed instead of silently leaving part of a
// partitioned table unmaintained.
func (db *DB) tableMaintenanceTrees(tab *catalog.Table) ([]*btree.Tree, error) {
	if tab == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CleanupTableDeadVersions", "nil table")
	}
	db.mu.RLock()
	defer db.mu.RUnlock()

	trees := make([]*btree.Tree, 0, 2+len(tab.Indexes))
	appendRequired := func(tr *btree.Tree, object string) error {
		if tr == nil {
			return nerr.New(nerr.Corruption, "executor.CleanupTableDeadVersions", object+" is not open")
		}
		trees = append(trees, tr)
		return nil
	}
	if err := appendRequired(db.heaps[tab.Name], "table heap"); err != nil {
		return nil, err
	}
	if tab.VecMeta != 0 {
		if err := appendRequired(db.vecs[tab.Name], "table vector store"); err != nil {
			return nil, err
		}
	}
	if tab.Partitioning == nil {
		for _, idx := range tab.Indexes {
			if err := appendRequired(db.idxs[idxKey(tab.Name, idx.Name)], "index "+idx.Name); err != nil {
				return nil, err
			}
		}
		return trees, nil
	}
	for _, part := range tab.Partitioning.Partitions {
		base := partitionHeapKey(tab.Name, part.ID)
		if err := appendRequired(db.partHeaps[base], "partition "+part.Name+" heap"); err != nil {
			return nil, err
		}
		if part.VecMeta != 0 {
			if err := appendRequired(db.partVecs[base], "partition "+part.Name+" vector store"); err != nil {
				return nil, err
			}
		}
		for _, idx := range part.Indexes {
			key := partitionIndexKey(tab.Name, part.ID, idx.Name)
			if err := appendRequired(db.partIdxs[key], "partition "+part.Name+" index "+idx.Name); err != nil {
				return nil, err
			}
		}
	}
	return trees, nil
}

// CleanupIndexDeadVersions scopes physical cleanup to one resolved index.
func (db *DB) CleanupIndexDeadVersions(table, index string, limit int) (int, error) {
	if db == nil || db.Eng == nil || limit < 1 {
		return 0, nerr.New(nerr.InvalidArgument, "executor.CleanupIndexDeadVersions", "database and positive limit are required")
	}
	started := time.Now()
	scope := table + "." + index
	n, err := db.maint.RunBudgeted("dead_versions", scope, func(budget *maintenance.Budget) (int, error) {
		if db.gate != nil {
			if err := db.gate.AllowWrite(); err != nil {
				return 0, err
			}
		}
		db.applyMu.Lock()
		defer db.applyMu.Unlock()
		tab, ok := db.Cat.Get(table)
		if !ok {
			return 0, nerr.New(nerr.NotFound, "executor.CleanupIndexDeadVersions", "unknown table")
		}
		trees, err := db.indexMaintenanceTrees(tab, index)
		if err != nil {
			return 0, err
		}
		return cleanupDeadTrees(trees, limit, budget)
	})
	if db.metrics != nil {
		db.metrics.ObserveMaintenance(int64(n), time.Since(started), err)
	}
	return n, err
}

func (db *DB) indexMaintenanceTrees(tab *catalog.Table, index string) ([]*btree.Tree, error) {
	if tab == nil || index == "" {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CleanupIndexDeadVersions", "table and index are required")
	}
	found := false
	for _, idx := range tab.Indexes {
		if idx.Name == index {
			found = true
			break
		}
	}
	if !found {
		return nil, nerr.New(nerr.NotFound, "executor.CleanupIndexDeadVersions", "unknown index")
	}

	db.mu.RLock()
	defer db.mu.RUnlock()
	if tab.Partitioning == nil {
		tr := db.idxs[idxKey(tab.Name, index)]
		if tr == nil {
			return nil, nerr.New(nerr.Corruption, "executor.CleanupIndexDeadVersions", "index is not open")
		}
		return []*btree.Tree{tr}, nil
	}
	trees := make([]*btree.Tree, 0, len(tab.Partitioning.Partitions))
	for _, part := range tab.Partitioning.Partitions {
		key := partitionIndexKey(tab.Name, part.ID, index)
		tr := db.partIdxs[key]
		if tr == nil {
			return nil, nerr.New(nerr.Corruption, "executor.CleanupIndexDeadVersions", "partition "+part.Name+" index is not open")
		}
		trees = append(trees, tr)
	}
	return trees, nil
}

func (db *DB) MaintenanceStatus() maintenance.Status { return db.maint.Status() }

// cluster returns the attached Raft cluster, or nil for a single-node
// deployment.
func (db *DB) cluster() *replication.Cluster {
	if db == nil {
		return nil
	}
	c, _ := db.gate.(*replication.Cluster)
	return c
}

// ClusterStatus returns the live Raft status when a cluster is attached.
func (db *DB) ClusterStatus() (replication.Status, bool) {
	c := db.cluster()
	if c == nil {
		return replication.Status{}, false
	}
	return c.Status(), true
}

// ClusterHealth returns this node's replication health snapshot when a cluster
// is attached.
func (db *DB) ClusterHealth() (replication.ReplicaHealth, bool) {
	c := db.cluster()
	if c == nil {
		return replication.ReplicaHealth{}, false
	}
	return c.ReplicaHealth(), true
}

// ConfirmReplicationReconciled clears this node's local replication-suspect
// flag — set automatically when a local commit couldn't reach quorum (see
// storage.Engine's ReplicationOrphanReporter) and, while set, blocks this
// node from serving STRONG reads. Run only after an operator has verified
// or repaired this node's divergence (CLUSTER RECONCILE CONFIRM). Returns
// Unavailable when no cluster is attached (single-node deployment, where
// the flag can never be set in the first place).
func (db *DB) ConfirmReplicationReconciled() error {
	c := db.cluster()
	if c == nil {
		return nerr.New(nerr.Unavailable, "executor.ConfirmReplicationReconciled", "no cluster attached")
	}
	c.ClearReplicationSuspect()
	return nil
}

// TransferLeadership asks this Raft cluster's current leader to step down in
// favor of another voter. Returns Unavailable when no cluster is attached
// (single-node deployment).
func (db *DB) TransferLeadership() error {
	c := db.cluster()
	if c == nil {
		return nerr.New(nerr.Unavailable, "executor.TransferLeadership", "no cluster attached")
	}
	return c.TransferLeadership()
}

// SetDrainFunc registers the callback CLUSTER DRAIN invokes: normally
// protocol.Server.Drain, wired by nextsqld at startup. Nil (the default,
// used by embedded/CLI callers with no listening server) makes Drain fail
// Unavailable.
func (db *DB) SetDrainFunc(fn func(timeout time.Duration)) {
	if db == nil {
		return
	}
	db.drainFn = fn
}

// Drain asks the attached server, if any, to begin a graceful drain —
// stop accepting new connections, close idle ones immediately, wait up to
// timeout for busy ones, then force-close whatever remains. It launches the
// drain in its own goroutine and returns immediately: the calling
// connection is itself "busy" running this very call, so waiting here for
// it to become idle (as Drain's own idle-polling loop would) would never
// complete until the timeout forced it closed.
func (db *DB) Drain(timeout time.Duration) error {
	if db == nil || db.drainFn == nil {
		return nerr.New(nerr.Unavailable, "executor.Drain", "no server attached")
	}
	fn := db.drainFn
	go fn(timeout)
	return nil
}

// EnableMaintenanceMode makes every subsequent mutating statement on this
// node fail Unavailable until DisableMaintenanceMode is called. Node-local,
// like Drain: not Raft-replicated, so a leader failover does not carry it to
// the new leader. Distinct from PauseMaintenance/ResumeMaintenance below,
// which pause the background dead-version cleanup (MAINTAIN) scheduler, not
// client query traffic.
func (db *DB) EnableMaintenanceMode() {
	if db == nil {
		return
	}
	db.maintenanceMode.Store(true)
}

// DisableMaintenanceMode reverses EnableMaintenanceMode.
func (db *DB) DisableMaintenanceMode() {
	if db == nil {
		return
	}
	db.maintenanceMode.Store(false)
}

// InMaintenanceMode reports the current node-local maintenance-mode state.
func (db *DB) InMaintenanceMode() bool {
	if db == nil {
		return false
	}
	return db.maintenanceMode.Load()
}

// SetDiskWatermarkTripped is called by cmd/nextsqld's disk-watermark monitor
// to flip the node into (or out of) the automatic write-reject state once
// free disk space crosses the configured reject/warn thresholds (with
// hysteresis applied by the caller). Node-local, like EnableMaintenanceMode,
// and intentionally independent of it: an operator's own CLUSTER MAINTENANCE
// ENABLE/DISABLE must not be able to mask a real disk-space emergency, and
// clearing an operator maintenance window must not silently un-reject writes
// on a node that is still critically low on disk.
func (db *DB) SetDiskWatermarkTripped(tripped bool) {
	if db == nil {
		return
	}
	db.diskWatermarkTripped.Store(tripped)
}

// DiskWatermarkTripped reports the current node-local disk-watermark state.
func (db *DB) DiskWatermarkTripped() bool {
	if db == nil {
		return false
	}
	return db.diskWatermarkTripped.Load()
}

func (db *DB) PauseMaintenance()                               { db.maint.Pause() }
func (db *DB) ResumeMaintenance()                              { db.maint.Resume() }
func (db *DB) SetMaintenanceLimits(l maintenance.Limits) error { return db.maint.SetLimits(l) }

// SetWALRetentionHorizon configures the oldest LSN that local WAL pruning may
// remove history before. Zero disables pruning (the fail-closed default).
func (db *DB) SetWALRetentionHorizon(lsn format.LSN) { db.walRetention.Store(uint64(lsn)) }

func cleanupDeadTrees(trees []*btree.Tree, limit int, budget *maintenance.Budget) (int, error) {
	total := 0
	for _, tr := range trees {
		if err := budget.Check(); err != nil {
			return total, err
		}
		n, err := tr.PurgeDeadBudgeted(limit-total, budget)
		total += n
		if err != nil || total >= limit {
			return total, err
		}
	}
	return total, nil
}

type rebuildProgress struct {
	table, index string
	started      time.Time
	phase        atomic.Value
	rows         atomic.Int64
	entries      atomic.Int64
}

// WriteGate rejects writes when this process is not the Raft leader.
type WriteGate interface {
	AllowWrite() error
}

// ReadGate rejects a strongly consistent read when this process cannot prove
// it is still the Raft leader. A nil gate (single-node deployment) permits the
// read. The gate installed by AttachCluster satisfies both interfaces.
type ReadGate interface {
	StrongReadBarrier() error
}

// FollowerReadGate reports whether this node may serve a bounded-staleness
// ("BOUNDED") read now, given the caller's freshness bound. The leader always
// passes; a follower passes only while it still sees a leader and was contacted
// within the bound. A rejected node returns an unavailable error so the caller
// routes elsewhere. The gate installed by AttachCluster satisfies it.
type FollowerReadGate interface {
	FollowerReadHealthy(maxStaleness time.Duration) error
}

// DefaultMaxStaleness is the freshness bound a BOUNDED read uses when the
// session sets no explicit MAX STALENESS.
const DefaultMaxStaleness = replication.HealthyContactWindow

// ReadConsistency selects how a read observes replicated state. See the
// replication package for the full contract. STRONG (the zero value) is the
// default; STALE serves a follower's locally applied state without a barrier.
type ReadConsistency = replication.ReadConsistency

const (
	ReadStrong  = replication.ReadStrong
	ReadBounded = replication.ReadBounded
	ReadStale   = replication.ReadStale
)

func Create(path string, keys crypto.KeyProvider, bufferPages int) (*DB, error) {
	e, err := storage.Create(path, keys, bufferPages)
	if err != nil {
		return nil, err
	}
	db, err := newDB(e)
	if err != nil {
		return nil, err
	}
	db.path = path
	db.keys = keys
	db.bufferPages = bufferPages
	return db, nil
}

func CreateWithIdentity(path string, id format.Identity, keys crypto.KeyProvider, bufferPages int) (*DB, error) {
	e, err := storage.CreateWithIdentity(path, id, keys, bufferPages)
	if err != nil {
		return nil, err
	}
	db, err := newDB(e)
	if err != nil {
		return nil, err
	}
	db.path = path
	db.keys = keys
	db.bufferPages = bufferPages
	return db, nil
}

func newDB(e *storage.Engine) (*DB, error) {
	tr, err := btree.Create(e)
	if err != nil {
		_ = e.Close()
		return nil, err
	}
	return &DB{
		path:         e.Path(),
		keys:         e.Keys(),
		Eng:          e,
		Cat:          catalog.New(),
		CatTree:      tr,
		heaps:        make(map[string]*btree.Tree),
		partHeaps:    make(map[string]*btree.Tree),
		partVecs:     make(map[string]*btree.Tree),
		partIdxs:     make(map[string]*btree.Tree),
		idxs:         make(map[string]*btree.Tree),
		vecs:         make(map[string]*btree.Tree),
		workflows:    make(map[string]*catalog.Workflow),
		triggers:     make(map[string]*catalog.Trigger),
		schedules:    make(map[string]*catalog.Schedule),
		resGroups:    make(map[string]*catalog.ResourceGroup),
		hnsw:         make(map[string]*lockedMem),
		optCache:     optimizer.NewCache(),
		resCache:     newResultCache(),
		feedback:     optimizer.NewFeedback(),
		sched:        scheduler.DefaultPool,
		metrics:      metrics.New(),
		admit:        scheduler.DefaultAdmission(),
		maint:        maintenance.New(),
		rebuilds:     make(map[string]*rebuildProgress),
		onlineBuilds: make(map[string]*onlineBuild),
		taskCancels:  make(map[string]context.CancelFunc),
	}, nil
}

func (db *DB) registerTaskCancel(id string, cancel context.CancelFunc) {
	if db == nil || id == "" || cancel == nil {
		return
	}
	db.taskCancelMu.Lock()
	if db.taskCancels == nil {
		db.taskCancels = make(map[string]context.CancelFunc)
	}
	db.taskCancels[id] = cancel
	db.taskCancelMu.Unlock()
}

func (db *DB) unregisterTaskCancel(id string) {
	if db == nil {
		return
	}
	db.taskCancelMu.Lock()
	delete(db.taskCancels, id)
	db.taskCancelMu.Unlock()
}

func (db *DB) signalTaskCancellation(id string) {
	if db == nil {
		return
	}
	db.taskCancelMu.Lock()
	cancel := db.taskCancels[id]
	db.taskCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (db *DB) IndexRebuildProgress() []IndexRebuildProgress {
	if db == nil {
		return nil
	}
	db.rebuildMu.RLock()
	defer db.rebuildMu.RUnlock()
	out := make([]IndexRebuildProgress, 0, len(db.rebuilds))
	for _, p := range db.rebuilds {
		phase, _ := p.phase.Load().(string)
		out = append(out, IndexRebuildProgress{Table: p.table, Index: p.index, Phase: phase, Rows: p.rows.Load(), Entries: p.entries.Load(), Started: p.started})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Table+"/"+out[i].Index < out[j].Table+"/"+out[j].Index })
	return out
}

func (db *DB) beginIndexRebuild(table, index string) *rebuildProgress {
	p := &rebuildProgress{table: table, index: index, started: time.Now().UTC()}
	p.phase.Store("building")
	key := idxKey(table, index)
	db.rebuildMu.Lock()
	db.rebuilds[key] = p
	db.rebuildMu.Unlock()
	return p
}

func (db *DB) finishIndexRebuild(p *rebuildProgress) {
	if db == nil || p == nil {
		return
	}
	db.rebuildMu.Lock()
	delete(db.rebuilds, idxKey(p.table, p.index))
	db.rebuildMu.Unlock()
}

func (db *DB) queueCommittedReclaims(ids []format.PageID, partitionMaps []string, indexMaps []indexMapDrop) {
	if db == nil || db.Eng == nil || (len(ids) == 0 && len(partitionMaps) == 0 && len(indexMaps) == 0) {
		return
	}
	db.reclaimMu.Lock()
	db.reclaimPending = append(db.reclaimPending, ids...)
	db.reclaimPartMap = append(db.reclaimPartMap, partitionMaps...)
	db.reclaimIdxMap = append(db.reclaimIdxMap, indexMaps...)
	db.reclaimMu.Unlock()
}

func (db *DB) drainCommittedReclaims() {
	if db == nil || db.Eng == nil {
		return
	}
	db.reclaimMu.Lock()
	ids := append([]format.PageID(nil), db.reclaimPending...)
	partitionMaps := append([]string(nil), db.reclaimPartMap...)
	indexMaps := append([]indexMapDrop(nil), db.reclaimIdxMap...)
	db.reclaimPending = nil
	db.reclaimPartMap = nil
	db.reclaimIdxMap = nil
	db.reclaimMu.Unlock()
	if len(ids) == 0 && len(partitionMaps) == 0 && len(indexMaps) == 0 {
		if db.hasSwappedOnlineBuilds() {
			db.applyMu.Lock()
			db.disarmSwappedOnlineBuilds()
			db.applyMu.Unlock()
		}
		return
	}
	db.applyMu.Lock()
	db.disarmSwappedOnlineBuilds()
	err := db.writeReclaimIntent(ids)
	if err == nil {
		err = db.Eng.CrashAt(wal.PointDuringPageReclaim)
	}
	if err == nil {
		err = db.Eng.ReclaimPages(ids)
	}
	if err == nil {
		err = db.Eng.CrashAt(wal.PointAfterPageReclaimBeforeIntentClear)
	}
	if err == nil {
		err = db.clearReclaimIntent()
	}
	if err == nil && len(partitionMaps) > 0 {
		db.dropPartitionMaps(partitionMaps)
	}
	if err == nil && len(indexMaps) > 0 {
		db.dropReclaimedIndexMaps(indexMaps)
	}
	db.applyMu.Unlock()
	db.reclaimMu.Lock()
	db.reclaimErr = err
	if err != nil {
		db.reclaimPending = append(ids, db.reclaimPending...)
		db.reclaimPartMap = append(partitionMaps, db.reclaimPartMap...)
		db.reclaimIdxMap = append(indexMaps, db.reclaimIdxMap...)
	}
	db.reclaimMu.Unlock()
}

func (db *DB) LastReclaimError() error {
	if db == nil {
		return nil
	}
	db.reclaimMu.Lock()
	defer db.reclaimMu.Unlock()
	return db.reclaimErr
}

// OrphanPages reports allocated data-file pages that are neither reachable
// from a catalog-owned tree nor owned by the allocator. It is read-only and
// serializes with transactions and Raft application to obtain one exact view.
func (db *DB) OrphanPages() ([]format.PageID, error) {
	if db == nil || db.Eng == nil || db.Eng.Alloc == nil || db.CatTree == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.OrphanPages", "database is not open")
	}
	db.applyMu.Lock()
	defer db.applyMu.Unlock()

	db.mu.RLock()
	trees := make([]*btree.Tree, 0, 1+len(db.heaps)+len(db.vecs)+len(db.idxs)+len(db.partHeaps)+len(db.partVecs)+len(db.partIdxs))
	trees = append(trees, db.CatTree)
	for _, tr := range db.heaps {
		trees = append(trees, tr)
	}
	for _, tr := range db.vecs {
		trees = append(trees, tr)
	}
	for _, tr := range db.idxs {
		trees = append(trees, tr)
	}
	for _, tr := range db.partHeaps {
		trees = append(trees, tr)
	}
	for _, tr := range db.partVecs {
		trees = append(trees, tr)
	}
	for _, tr := range db.partIdxs {
		trees = append(trees, tr)
	}
	db.mu.RUnlock()

	reachable := make(map[format.PageID]struct{})
	for _, tr := range trees {
		pages, err := tr.OwnedPages()
		if err != nil {
			return nil, err
		}
		for _, id := range pages {
			reachable[id] = struct{}{}
		}
	}
	state := db.Eng.Alloc.State()
	for _, id := range state.Free {
		reachable[id] = struct{}{}
	}
	for _, id := range state.Metadata {
		reachable[id] = struct{}{}
	}
	orphans := make([]format.PageID, 0)
	for id := format.FirstAllocPageID; id < state.Next; id++ {
		if _, ok := reachable[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	return orphans, nil
}

func (p *rebuildProgress) add(rows, entries int64) {
	if p != nil {
		p.rows.Add(rows)
		p.entries.Add(entries)
	}
}

func Open(path string, keys crypto.KeyProvider, bufferPages int) (*DB, error) {
	return OpenWith(path, keys, bufferPages, storage.OpenOptions{})
}

// OpenWith opens an existing database with recovery options (PITR until-LSN).
func OpenWith(path string, keys crypto.KeyProvider, bufferPages int, opt storage.OpenOptions) (*DB, error) {
	e, err := storage.OpenWith(path, keys, bufferPages, opt)
	if err != nil {
		return nil, err
	}
	tr, err := btree.Open(e)
	if err != nil && nerr.HasCode(err, nerr.NotFound) {
		tr, err = btree.Create(e)
	}
	if err != nil {
		_ = e.Close()
		return nil, err
	}
	db := &DB{
		path:         path,
		keys:         keys,
		bufferPages:  bufferPages,
		Eng:          e,
		Cat:          catalog.New(),
		CatTree:      tr,
		heaps:        make(map[string]*btree.Tree),
		partHeaps:    make(map[string]*btree.Tree),
		partVecs:     make(map[string]*btree.Tree),
		partIdxs:     make(map[string]*btree.Tree),
		idxs:         make(map[string]*btree.Tree),
		vecs:         make(map[string]*btree.Tree),
		workflows:    make(map[string]*catalog.Workflow),
		triggers:     make(map[string]*catalog.Trigger),
		schedules:    make(map[string]*catalog.Schedule),
		resGroups:    make(map[string]*catalog.ResourceGroup),
		hnsw:         make(map[string]*lockedMem),
		optCache:     optimizer.NewCache(),
		resCache:     newResultCache(),
		feedback:     optimizer.NewFeedback(),
		sched:        scheduler.DefaultPool,
		metrics:      metrics.New(),
		admit:        scheduler.DefaultAdmission(),
		maint:        maintenance.New(),
		rebuilds:     make(map[string]*rebuildProgress),
		onlineBuilds: make(map[string]*onlineBuild),
	}
	if err := db.reloadCatalog(); err != nil {
		_ = e.Close()
		return nil, err
	}
	if err := db.replayReclaimIntent(); err != nil {
		_ = e.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	if db == nil || db.Eng == nil {
		return nil
	}
	return db.Eng.Close()
}

func (db *DB) Session() *Session {
	return &Session{db: db, limits: scheduler.DefaultLimits()}
}

func (db *DB) Metrics() *metrics.Registry {
	if db == nil {
		return nil
	}
	return db.metrics
}

func (db *DB) SetMetrics(m *metrics.Registry) {
	if db != nil {
		db.metrics = m
	}
}

func (db *DB) Admission() *scheduler.Admission {
	if db == nil {
		return nil
	}
	return db.admit
}

func (db *DB) SetAdmission(a *scheduler.Admission) {
	if db != nil {
		db.admit = a
	}
}

func (db *DB) SetGate(g WriteGate) {
	if db != nil {
		db.gate = g
	}
}

// AttachCluster installs leadership checks and quorum commit.
func (db *DB) AttachCluster(c *replication.Cluster) {
	if db == nil {
		return
	}
	db.gate = c
	if db.Eng != nil {
		db.Eng.SetReplicator(c)
	}
}

// ApplyRecords is the Raft FSM hook on a replica.
func (db *DB) ApplyRecords(recs []wal.Record) error {
	if db == nil || db.Eng == nil {
		return nerr.New(nerr.InvalidArgument, "executor.ApplyRecords", "nil database")
	}
	db.applyMu.Lock()
	defer db.applyMu.Unlock()
	if err := db.Eng.ApplyReplicated(recs); err != nil {
		return err
	}
	tr, err := btree.Open(db.Eng)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil
		}
		return err
	}
	db.CatTree = tr
	db.dropAllHNSW()
	return db.reloadCatalog()
}

func (db *DB) reloadCatalog() error {
	var tables []*catalog.Table
	start := catalog.TableKey("")
	end := []byte{catalog.KeyTable + 1}
	err := db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) == 0 || k[0] != catalog.KeyTable {
			return nil
		}
		t, err := catalog.DecodeTable(v)
		if err != nil {
			return err
		}
		tables = append(tables, t)
		return nil
	})
	if err != nil {
		return err
	}
	idempotencyCount := 0
	start, end = catalog.IdempotencyBounds()
	err = db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) != 33 || k[0] != catalog.KeyIdempotency {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "invalid idempotency catalog key")
		}
		if _, err := catalog.DecodeIdempotency(v); err != nil {
			return err
		}
		idempotencyCount++
		if idempotencyCount > catalog.MaxIdempotencyRecords {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "idempotency record capacity exceeded")
		}
		return nil
	})
	if err != nil {
		return err
	}
	schedules := make(map[string]*catalog.Schedule)
	start = catalog.ScheduleKey("")
	end = []byte{catalog.KeySchedule + 1}
	err = db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) == 0 || k[0] != catalog.KeySchedule {
			return nil
		}
		schedule, err := catalog.DecodeSchedule(v)
		if err != nil {
			return err
		}
		if string(k[1:]) != schedule.Name {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "schedule catalog key/name mismatch")
		}
		if _, exists := schedules[schedule.Name]; exists {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "duplicate schedule")
		}
		schedules[schedule.Name] = schedule
		return nil
	})
	if err != nil {
		return err
	}
	schedulesByID := make(map[uint32]*catalog.Schedule, len(schedules))
	for _, schedule := range schedules {
		if _, exists := schedulesByID[schedule.ID]; exists {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "duplicate schedule identity")
		}
		schedulesByID[schedule.ID] = schedule
	}
	indexedSchedules := make(map[uint32]struct{}, len(schedules))
	start = catalog.ScheduleDueKey(0, 1)
	end = []byte{catalog.KeyScheduleDue + 1}
	err = db.CatTree.Range(start, end, func(k, v []byte) error {
		nextNS, id, err := catalog.ParseScheduleDueKey(k)
		if err != nil {
			return err
		}
		schedule, ok := schedulesByID[id]
		if !ok || !schedule.Enabled || schedule.NextFireNS != nextNS || schedule.Name != string(v) {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "schedule due index mismatch")
		}
		if _, exists := indexedSchedules[id]; exists {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "duplicate schedule due index")
		}
		indexedSchedules[id] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		_, indexed := indexedSchedules[schedule.ID]
		if indexed != schedule.Enabled {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "schedule due index missing or unexpected")
		}
	}
	workflows := make(map[string]*catalog.Workflow)
	start = catalog.WorkflowKey("")
	end = []byte{catalog.KeyWorkflow + 1}
	err = db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) == 0 || k[0] != catalog.KeyWorkflow {
			return nil
		}
		w, err := catalog.DecodeWorkflow(v)
		if err != nil {
			return err
		}
		if string(k[1:]) != w.Name {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "workflow catalog key/name mismatch")
		}
		if _, exists := workflows[w.Name]; exists {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "duplicate workflow")
		}
		workflows[w.Name] = w
		return nil
	})
	if err != nil {
		return err
	}
	triggers := make(map[string]*catalog.Trigger)
	start = catalog.TriggerKey("")
	end = []byte{catalog.KeyTrigger + 1}
	err = db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) == 0 || k[0] != catalog.KeyTrigger {
			return nil
		}
		trigger, err := catalog.DecodeTrigger(v)
		if err != nil {
			return err
		}
		if string(k[1:]) != trigger.Name {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "trigger catalog key/name mismatch")
		}
		if _, exists := triggers[trigger.Name]; exists {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "duplicate trigger")
		}
		triggers[trigger.Name] = trigger
		return nil
	})
	if err != nil {
		return err
	}
	resGroups := make(map[string]*catalog.ResourceGroup)
	start = catalog.ResourceGroupKey("")
	end = []byte{catalog.KeyResourceGroup + 1}
	err = db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) == 0 || k[0] != catalog.KeyResourceGroup {
			return nil
		}
		g, err := catalog.DecodeResourceGroup(v)
		if err != nil {
			return err
		}
		if string(k[1:]) != g.Name {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "resource group catalog key/name mismatch")
		}
		if _, exists := resGroups[g.Name]; exists {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "duplicate resource group")
		}
		resGroups[g.Name] = g
		return nil
	})
	if err != nil {
		return err
	}
	tablesByID := make(map[uint32]*catalog.Table, len(tables))
	for _, table := range tables {
		tablesByID[table.ID] = table
	}
	for _, workflow := range workflows {
		for _, dep := range workflow.Dependencies {
			switch dep.Kind {
			case catalog.WorkflowDependencyTable:
				table, ok := tablesByID[dep.ID]
				if !ok || table.Name != dep.Name {
					return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "workflow table dependency mismatch")
				}
			case catalog.WorkflowDependencyWorkflow:
				target, ok := workflows[dep.Name]
				if !ok || target.ID != dep.ID || target.ID == workflow.ID {
					return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "workflow dependency mismatch")
				}
			default:
				return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "unknown workflow dependency kind")
			}
		}
	}
	for _, trigger := range triggers {
		table, tableOK := tablesByID[trigger.TableID]
		workflow, workflowOK := workflows[trigger.Workflow]
		if !tableOK || table.Name != trigger.Table || !workflowOK || workflow.ID != trigger.WorkflowID {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "trigger dependency mismatch")
		}
	}
	for _, schedule := range schedules {
		workflow, ok := workflows[schedule.Workflow]
		if !ok || workflow.ID != schedule.WorkflowID {
			return nerr.New(nerr.InvalidFormat, "executor.reloadCatalog", "schedule dependency mismatch")
		}
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.heaps = make(map[string]*btree.Tree, len(tables))
	db.idxs = make(map[string]*btree.Tree)
	db.vecs = make(map[string]*btree.Tree)
	db.partHeaps = make(map[string]*btree.Tree)
	db.partVecs = make(map[string]*btree.Tree)
	db.partIdxs = make(map[string]*btree.Tree)
	db.workflows = workflows
	db.triggers = triggers
	db.schedules = schedules
	db.resGroups = resGroups
	db.resGroupGates = nil
	for _, w := range workflows {
		db.Cat.SetNextID(w.ID + 1)
	}
	for _, trigger := range triggers {
		db.Cat.SetNextID(trigger.ID + 1)
	}
	for _, schedule := range schedules {
		db.Cat.SetNextID(schedule.ID + 1)
	}
	for _, t := range tables {
		heap, err := btree.OpenDetached(db.Eng, t.HeapMeta)
		if err != nil {
			return err
		}
		db.heaps[t.Name] = heap
		if t.VecMeta != 0 {
			vs, err := btree.OpenDetached(db.Eng, t.VecMeta)
			if err != nil {
				return err
			}
			db.vecs[t.Name] = vs
		}
		for _, idx := range t.Indexes {
			if t.Partitioning != nil {
				// Partitioned logical indexes have no global physical root.
				continue
			}
			ix, err := btree.OpenDetached(db.Eng, idx.Meta)
			if err != nil {
				return err
			}
			db.idxs[idxKey(t.Name, idx.Name)] = ix
		}
		if t.Partitioning != nil {
			for _, part := range t.Partitioning.Partitions {
				h, err := btree.OpenDetached(db.Eng, part.HeapMeta)
				if err != nil {
					return err
				}
				db.partHeaps[partitionHeapKey(t.Name, part.ID)] = h
				if part.VecMeta != 0 {
					vs, err := btree.OpenDetached(db.Eng, part.VecMeta)
					if err != nil {
						return err
					}
					db.partVecs[partitionHeapKey(t.Name, part.ID)] = vs
				}
				for _, pi := range part.Indexes {
					ix, err := btree.OpenDetached(db.Eng, pi.Meta)
					if err != nil {
						return err
					}
					db.partIdxs[partitionIndexKey(t.Name, part.ID, pi.Name)] = ix
				}
			}
		}
	}
	db.Cat.Replace(tables)
	return db.reloadStats()
}

func (db *DB) workflow(name string) (*catalog.Workflow, bool) {
	if db == nil {
		return nil, false
	}
	db.mu.RLock()
	w, ok := db.workflows[name]
	db.mu.RUnlock()
	if !ok || w == nil {
		return nil, false
	}
	return w.Clone(), true
}

func (db *DB) workflowList() []*catalog.Workflow {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make([]*catalog.Workflow, 0, len(db.workflows))
	for _, w := range db.workflows {
		if clone := w.Clone(); clone != nil {
			out = append(out, clone)
		}
	}
	return out
}

func (db *DB) trigger(name string) (*catalog.Trigger, bool) {
	if db == nil {
		return nil, false
	}
	db.mu.RLock()
	trigger, ok := db.triggers[name]
	db.mu.RUnlock()
	if !ok || trigger == nil {
		return nil, false
	}
	return trigger.Clone(), true
}

func (db *DB) triggerList() []*catalog.Trigger {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make([]*catalog.Trigger, 0, len(db.triggers))
	for _, trigger := range db.triggers {
		if clone := trigger.Clone(); clone != nil {
			out = append(out, clone)
		}
	}
	return out
}

func (db *DB) putTrigger(trigger *catalog.Trigger) {
	if db == nil || trigger == nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.triggers == nil {
		db.triggers = make(map[string]*catalog.Trigger)
	}
	db.triggers[trigger.Name] = trigger
}

func (db *DB) removeTrigger(name string) {
	if db == nil {
		return
	}
	db.mu.Lock()
	delete(db.triggers, name)
	db.mu.Unlock()
}

func (db *DB) schedule(name string) (*catalog.Schedule, bool) {
	if db == nil {
		return nil, false
	}
	db.mu.RLock()
	schedule, ok := db.schedules[name]
	db.mu.RUnlock()
	if !ok || schedule == nil {
		return nil, false
	}
	return schedule.Clone(), true
}

func (db *DB) scheduleList() []*catalog.Schedule {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make([]*catalog.Schedule, 0, len(db.schedules))
	for _, schedule := range db.schedules {
		if clone := schedule.Clone(); clone != nil {
			out = append(out, clone)
		}
	}
	return out
}

func (db *DB) putSchedule(schedule *catalog.Schedule) {
	if db == nil || schedule == nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.schedules == nil {
		db.schedules = make(map[string]*catalog.Schedule)
	}
	db.schedules[schedule.Name] = schedule
}

func (db *DB) removeSchedule(name string) {
	if db == nil {
		return
	}
	db.mu.Lock()
	delete(db.schedules, name)
	db.mu.Unlock()
}

func (db *DB) resourceGroup(name string) (*catalog.ResourceGroup, bool) {
	if db == nil {
		return nil, false
	}
	db.mu.RLock()
	group, ok := db.resGroups[name]
	db.mu.RUnlock()
	if !ok || group == nil {
		return nil, false
	}
	return group.Clone(), true
}

func (db *DB) resourceGroupList() []*catalog.ResourceGroup {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make([]*catalog.ResourceGroup, 0, len(db.resGroups))
	for _, group := range db.resGroups {
		if clone := group.Clone(); clone != nil {
			out = append(out, clone)
		}
	}
	return out
}

func (db *DB) putResourceGroup(group *catalog.ResourceGroup) {
	if db == nil || group == nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.resGroups == nil {
		db.resGroups = make(map[string]*catalog.ResourceGroup)
	}
	db.resGroups[group.Name] = group
	delete(db.resGroupGates, group.Name)
}

func (db *DB) removeResourceGroup(name string) {
	if db == nil {
		return
	}
	db.mu.Lock()
	delete(db.resGroups, name)
	delete(db.resGroupGates, name)
	db.mu.Unlock()
}

// resourceGroupGate returns the process-wide concurrency gate for a named
// resource group, or nil if the group does not exist or has no
// MaxConcurrency bound (0 = unbounded, no gate needed). The gate is built
// once and cached; ALTER RESOURCE GROUP drops the cache entry via
// putResourceGroup so the next query under that group picks up the new
// bound (starting its in-flight count fresh at zero).
func (db *DB) resourceGroupGate(name string) *scheduler.Admission {
	if db == nil || name == "" {
		return nil
	}
	db.mu.RLock()
	g, ok := db.resGroups[name]
	gate, cached := db.resGroupGates[name]
	db.mu.RUnlock()
	if !ok || g == nil || g.MaxConcurrency <= 0 {
		return nil
	}
	if cached && gate != nil {
		return gate
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	g, ok = db.resGroups[name]
	if !ok || g == nil || g.MaxConcurrency <= 0 {
		delete(db.resGroupGates, name)
		return nil
	}
	if existing, ok := db.resGroupGates[name]; ok && existing != nil {
		return existing
	}
	gate = scheduler.NewAdmission(scheduler.AdmissionConfig{MaxInflight: int(g.MaxConcurrency)})
	if db.resGroupGates == nil {
		db.resGroupGates = make(map[string]*scheduler.Admission)
	}
	db.resGroupGates[name] = gate
	return gate
}

func (db *DB) putWorkflow(w *catalog.Workflow) {
	if db == nil || w == nil {
		return
	}
	db.mu.Lock()
	if db.workflows == nil {
		db.workflows = make(map[string]*catalog.Workflow)
	}
	// The transaction overlay owns w and is cleared immediately after commit;
	// committed descriptors are immutable. Readers receive a deep clone.
	db.workflows[w.Name] = w
	db.mu.Unlock()
}

func (db *DB) removeWorkflow(name string) {
	if db == nil {
		return
	}
	db.mu.Lock()
	delete(db.workflows, name)
	db.mu.Unlock()
}

func (db *DB) reloadStats() error {
	type tableSnapshot struct {
		stats  *catalog.TableStats
		digest [32]byte
	}
	byTableID := make(map[uint32]tableSnapshot)
	start := catalog.StatsKey("")
	end := []byte{catalog.KeyStats + 1}
	if err := db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) == 0 || k[0] != catalog.KeyStats {
			return nil
		}
		st, err := catalog.DecodeStats(v)
		if err != nil {
			return err
		}
		if st.TableID == 0 {
			return nerr.New(nerr.Corruption, "executor.reloadStats", "zero statistics table identity")
		}
		if _, exists := byTableID[st.TableID]; exists {
			return nerr.New(nerr.Corruption, "executor.reloadStats", "duplicate statistics table identity")
		}
		byTableID[st.TableID] = tableSnapshot{stats: st, digest: sha256.Sum256(v)}
		return nil
	}); err != nil {
		return err
	}
	start = []byte{catalog.KeyPartitionStats}
	end = []byte{catalog.KeyPartitionStats + 1}
	if err := db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) != 9 || k[0] != catalog.KeyPartitionStats {
			return nerr.New(nerr.Corruption, "executor.reloadStats", "invalid partition statistics key")
		}
		tableID, snapshot, part, err := catalog.DecodePartitionStats(v)
		if err != nil {
			return err
		}
		if !bytes.Equal(k, catalog.PartitionStatsKey(tableID, part.ID)) {
			return nerr.New(nerr.Corruption, "executor.reloadStats", "partition statistics identity mismatch")
		}
		table, exists := byTableID[tableID]
		if !exists || table.stats == nil {
			return nerr.New(nerr.Corruption, "executor.reloadStats", "orphan partition statistics")
		}
		if snapshot != table.digest {
			// An older writer may have refreshed NSST without knowing NSPS.
			// Treat the side record as stale and retain conservative globals.
			return nil
		}
		st := table.stats
		found := false
		for i := range st.Partitions {
			if st.Partitions[i].ID != part.ID {
				continue
			}
			if found {
				return nerr.New(nerr.Corruption, "executor.reloadStats", "duplicate partition statistics identity")
			}
			st.Partitions[i] = part
			found = true
		}
		if !found {
			return nerr.New(nerr.Corruption, "executor.reloadStats", "partition statistics missing from table snapshot")
		}
		return nil
	}); err != nil {
		return err
	}
	for _, table := range byTableID {
		db.Cat.SetStats(table.stats)
	}
	return nil
}

func (db *DB) heap(name string) (*btree.Tree, error) {
	db.mu.RLock()
	tr := db.heaps[name]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.heap", "table heap not open")
	}
	tr.SetName(name)
	return tr, nil
}

func (db *DB) index(table, name string) (*btree.Tree, error) {
	db.mu.RLock()
	tr := db.idxs[idxKey(table, name)]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.index", "index not open")
	}
	// Tag the index tree with its owning table (not the index name) so
	// system.locks reports the same table_name as the heap.
	tr.SetName(table)
	return tr, nil
}

func (db *DB) vecStore(name string) (*btree.Tree, error) {
	db.mu.RLock()
	tr := db.vecs[name]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.vecStore", "vector store not open")
	}
	tr.SetName(name)
	return tr, nil
}

func (db *DB) putHeap(name string, tr *btree.Tree) {
	db.mu.Lock()
	db.heaps[name] = tr
	db.mu.Unlock()
}

func (db *DB) putVec(name string, tr *btree.Tree) {
	db.mu.Lock()
	db.vecs[name] = tr
	db.mu.Unlock()
}

func (db *DB) dropTableMaps(name string) {
	db.mu.Lock()
	delete(db.heaps, name)
	delete(db.vecs, name)
	prefix := name + "/"
	var dropHNSW []string
	for k := range db.idxs {
		if strings.HasPrefix(k, prefix) {
			delete(db.idxs, k)
			dropHNSW = append(dropHNSW, k)
		}
	}
	// Partition heaps/vecs: keys like name#pid and name#pid/idx
	prefixHash := name + "#"
	for k := range db.partHeaps {
		if strings.HasPrefix(k, prefixHash) {
			delete(db.partHeaps, k)
		}
	}
	for k := range db.partVecs {
		if strings.HasPrefix(k, prefixHash) {
			delete(db.partVecs, k)
		}
	}
	for k := range db.partIdxs {
		if strings.HasPrefix(k, prefixHash) {
			delete(db.partIdxs, k)
			dropHNSW = append(dropHNSW, k)
		}
	}
	db.mu.Unlock()
	for _, k := range dropHNSW {
		db.dropHNSW(k)
	}
}

// dropPartitionMaps runs only while reclamation holds applyMu exclusively, so
// no snapshot can still resolve the removed stable partition identity.
func (db *DB) dropPartitionMaps(keys []string) {
	db.mu.Lock()
	var dropHNSW []string
	for _, base := range keys {
		delete(db.partHeaps, base)
		delete(db.partVecs, base)
		prefix := base + "/"
		for key := range db.partIdxs {
			if strings.HasPrefix(key, prefix) {
				delete(db.partIdxs, key)
				dropHNSW = append(dropHNSW, key)
			}
		}
	}
	db.mu.Unlock()
	for _, k := range dropHNSW {
		db.dropHNSW(k)
	}
}

func (db *DB) dropReclaimedIndexMaps(drops []indexMapDrop) {
	db.mu.Lock()
	var dropHNSW []string
	for _, drop := range drops {
		if drop.partition {
			if db.partIdxs[drop.key] == drop.tree {
				delete(db.partIdxs, drop.key)
				dropHNSW = append(dropHNSW, drop.key)
			}
			continue
		}
		if db.idxs[drop.key] == drop.tree {
			delete(db.idxs, drop.key)
			dropHNSW = append(dropHNSW, drop.key)
		}
	}
	db.mu.Unlock()
	for _, k := range dropHNSW {
		db.dropHNSW(k)
	}
}

func (db *DB) renameTableMaps(old, neu string) {
	if old == neu {
		return
	}
	db.mu.Lock()
	if tr, ok := db.heaps[old]; ok {
		db.heaps[neu] = tr
		delete(db.heaps, old)
	}
	if tr, ok := db.vecs[old]; ok {
		db.vecs[neu] = tr
		delete(db.vecs, old)
	}
	prefix := old + "/"
	moved := make(map[string]*btree.Tree)
	var dropHNSW []string
	for k, tr := range db.idxs {
		if strings.HasPrefix(k, prefix) {
			moved[neu+"/"+k[len(prefix):]] = tr
			delete(db.idxs, k)
			dropHNSW = append(dropHNSW, k)
		}
	}
	for k, tr := range moved {
		db.idxs[k] = tr
	}
	// Partition maps: rename prefix old# to neu#
	oldHash := old + "#"
	neuHash := neu + "#"
	movedPh := make(map[string]*btree.Tree)
	for k, tr := range db.partHeaps {
		if strings.HasPrefix(k, oldHash) {
			movedPh[neuHash+k[len(oldHash):]] = tr
			delete(db.partHeaps, k)
		}
	}
	for k, tr := range movedPh {
		db.partHeaps[k] = tr
	}
	movedPv := make(map[string]*btree.Tree)
	for k, tr := range db.partVecs {
		if strings.HasPrefix(k, oldHash) {
			movedPv[neuHash+k[len(oldHash):]] = tr
			delete(db.partVecs, k)
		}
	}
	for k, tr := range movedPv {
		db.partVecs[k] = tr
	}
	movedPi := make(map[string]*btree.Tree)
	for k, tr := range db.partIdxs {
		if strings.HasPrefix(k, oldHash) {
			movedPi[neuHash+k[len(oldHash):]] = tr
			delete(db.partIdxs, k)
			dropHNSW = append(dropHNSW, k)
		}
	}
	for k, tr := range movedPi {
		db.partIdxs[k] = tr
	}
	db.mu.Unlock()
	for _, k := range dropHNSW {
		db.dropHNSW(k)
	}
}

func (db *DB) dropMissingIndexes(t *catalog.Table) {
	if t == nil {
		return
	}
	keep := make(map[string]struct{}, len(t.Indexes))
	for _, idx := range t.Indexes {
		keep[idxKey(t.Name, idx.Name)] = struct{}{}
	}
	db.mu.Lock()
	prefix := t.Name + "/"
	var drop []string
	for k := range db.idxs {
		if strings.HasPrefix(k, prefix) {
			if _, ok := keep[k]; !ok {
				delete(db.idxs, k)
				drop = append(drop, k)
			}
		}
	}
	db.mu.Unlock()
	for _, k := range drop {
		db.dropHNSW(k)
	}
}

func idxKey(table, name string) string { return table + "/" + name }
