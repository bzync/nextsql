package executor

import (
	"context"
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
	"github.com/bzync/nextsql/internal/wal"
)

// DB is a local NextSQL database: storage engine + catalog + table trees.
type DB struct {
	path        string
	keys        crypto.KeyProvider
	bufferPages int

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
	hnswMu    sync.RWMutex
	hnswGen   uint64
	hnsw      map[string]*lockedMem
	optCache  *optimizer.Cache
	feedback  *optimizer.Feedback
	sched     *scheduler.Pool
	metrics   *metrics.Registry
	admit     *scheduler.Admission
	maint     *maintenance.Manager

	applyMu        sync.RWMutex
	gate           WriteGate
	rebuildMu      sync.RWMutex
	rebuilds       map[string]*rebuildProgress
	reclaimMu      sync.Mutex
	reclaimErr     error
	reclaimPending []format.PageID
	taskCancelMu   sync.Mutex
	taskCancels    map[string]context.CancelFunc
	walRetention   atomic.Uint64
}

type IndexRebuildProgress struct {
	Table, Index string
	Phase        string
	Rows         int64
	Entries      int64
	Started      time.Time
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
	db.mu.RLock()
	trees := []*btree.Tree{db.heaps[tab.Name]}
	if tr := db.vecs[tab.Name]; tr != nil {
		trees = append(trees, tr)
	}
	for _, idx := range tab.Indexes {
		if tr := db.idxs[idxKey(tab.Name, idx.Name)]; tr != nil {
			trees = append(trees, tr)
		}
	}
	db.mu.RUnlock()
	if trees[0] == nil {
		return 0, nerr.New(nerr.NotFound, "executor.CleanupTableDeadVersions", "table heap not open")
	}
	return cleanupDeadTrees(trees, limit, budget)
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
		tr, err := db.index(table, index)
		if err != nil {
			return 0, err
		}
		return cleanupDeadTrees([]*btree.Tree{tr}, limit, budget)
	})
	if db.metrics != nil {
		db.metrics.ObserveMaintenance(int64(n), time.Since(started), err)
	}
	return n, err
}

func (db *DB) MaintenanceStatus() maintenance.Status           { return db.maint.Status() }
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
		path:        e.Path(),
		keys:        e.Keys(),
		Eng:         e,
		Cat:         catalog.New(),
		CatTree:     tr,
		heaps:       make(map[string]*btree.Tree),
	partHeaps:   make(map[string]*btree.Tree),
	partVecs:    make(map[string]*btree.Tree),
	partIdxs:    make(map[string]*btree.Tree),
		idxs:        make(map[string]*btree.Tree),
		vecs:        make(map[string]*btree.Tree),
		workflows:   make(map[string]*catalog.Workflow),
		triggers:    make(map[string]*catalog.Trigger),
		schedules:   make(map[string]*catalog.Schedule),
		hnsw:        make(map[string]*lockedMem),
		optCache:    optimizer.NewCache(),
		feedback:    optimizer.NewFeedback(),
		sched:       scheduler.DefaultPool,
		metrics:     metrics.New(),
		admit:       scheduler.DefaultAdmission(),
		maint:       maintenance.New(),
		rebuilds:    make(map[string]*rebuildProgress),
		taskCancels: make(map[string]context.CancelFunc),
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

func (db *DB) queueCommittedReclaims(ids []format.PageID) {
	if db == nil || db.Eng == nil || len(ids) == 0 {
		return
	}
	db.reclaimMu.Lock()
	db.reclaimPending = append(db.reclaimPending, ids...)
	db.reclaimMu.Unlock()
}

func (db *DB) drainCommittedReclaims() {
	if db == nil || db.Eng == nil {
		return
	}
	db.reclaimMu.Lock()
	ids := append([]format.PageID(nil), db.reclaimPending...)
	db.reclaimPending = nil
	db.reclaimMu.Unlock()
	if len(ids) == 0 {
		return
	}
	db.applyMu.Lock()
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
	db.applyMu.Unlock()
	db.reclaimMu.Lock()
	db.reclaimErr = err
	if err != nil {
		db.reclaimPending = append(ids, db.reclaimPending...)
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
		path:        path,
		keys:        keys,
		bufferPages: bufferPages,
		Eng:         e,
		Cat:         catalog.New(),
		CatTree:     tr,
		heaps:       make(map[string]*btree.Tree),
	partHeaps:   make(map[string]*btree.Tree),
	partVecs:    make(map[string]*btree.Tree),
	partIdxs:    make(map[string]*btree.Tree),
		idxs:        make(map[string]*btree.Tree),
		vecs:        make(map[string]*btree.Tree),
		workflows:   make(map[string]*catalog.Workflow),
		triggers:    make(map[string]*catalog.Trigger),
		schedules:   make(map[string]*catalog.Schedule),
		hnsw:        make(map[string]*lockedMem),
		optCache:    optimizer.NewCache(),
		feedback:    optimizer.NewFeedback(),
		sched:       scheduler.DefaultPool,
		metrics:     metrics.New(),
		admit:       scheduler.DefaultAdmission(),
		maint:       maintenance.New(),
		rebuilds:    make(map[string]*rebuildProgress),
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
	start := catalog.StatsKey("")
	end := []byte{catalog.KeyStats + 1}
	return db.CatTree.Range(start, end, func(k, v []byte) error {
		if len(k) == 0 || k[0] != catalog.KeyStats {
			return nil
		}
		st, err := catalog.DecodeStats(v)
		if err != nil {
			return err
		}
		db.Cat.SetStats(st)
		return nil
	})
}

func (db *DB) heap(name string) (*btree.Tree, error) {
	db.mu.RLock()
	tr := db.heaps[name]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.heap", "table heap not open")
	}
	return tr, nil
}

func (db *DB) index(table, name string) (*btree.Tree, error) {
	db.mu.RLock()
	tr := db.idxs[idxKey(table, name)]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.index", "index not open")
	}
	return tr, nil
}

func (db *DB) vecStore(name string) (*btree.Tree, error) {
	db.mu.RLock()
	tr := db.vecs[name]
	db.mu.RUnlock()
	if tr == nil {
		return nil, nerr.New(nerr.NotFound, "executor.vecStore", "vector store not open")
	}
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

func (db *DB) putIndex(table, name string, tr *btree.Tree) {
	db.mu.Lock()
	db.idxs[idxKey(table, name)] = tr
	db.mu.Unlock()
}

func (db *DB) dropHeap(name string) {
	db.mu.Lock()
	delete(db.heaps, name)
	db.mu.Unlock()
}

func (db *DB) dropIndex(table, name string) {
	db.mu.Lock()
	delete(db.idxs, idxKey(table, name))
	db.mu.Unlock()
	db.dropHNSW(idxKey(table, name))
}

func (db *DB) dropVec(name string) {
	db.mu.Lock()
	delete(db.vecs, name)
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
