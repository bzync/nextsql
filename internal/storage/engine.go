package storage

import (
	"fmt"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/storage/page"
	"os"
	"slices"
	"sync"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/recovery"
	"github.com/bzync/nextsql/internal/storage/allocator"
	"github.com/bzync/nextsql/internal/storage/buffer"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/integrity"
	"github.com/bzync/nextsql/internal/storage/row"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/undo"
	"github.com/bzync/nextsql/internal/wal"
)

// Engine is the storage facade: encrypted file + allocator + buffer pool + WAL + UNDO.
type Engine struct {
	File   *file.Manager
	Alloc  *allocator.Allocator
	Buffer *buffer.Pool
	WAL    *wal.Log
	Undo   *undo.Log
	TM     *txn.Manager

	mu sync.Mutex
	// exclusiveMu serializes maintenance write transactions (see
	// BeginExclusiveWrite). Lock order: a tree's mutex, then exclusiveMu,
	// then pageMu, then mu.
	exclusiveMu sync.Mutex
	// pageMu separates page mutation from commit-time physical snapshots.
	// Buffer-pool pins alias frame bytes, so pool.mu cannot protect a copy
	// from a writer that already holds a Handle.
	pageMu     sync.RWMutex
	txn        *Txn
	writers    map[format.TxnID]*Txn
	opTxn      *Txn
	pageWriter bool
	// Structure-modification logging; see LeaveOp. opDirty is every page
	// dirtied inside the current Enter..Leave operation, opSMO whether that
	// operation allocated or dropped a page (a split or merge), and
	// smoPending pages a structure modification left unlogged because its
	// WAL append failed.
	opDirty    []format.PageID
	opDirtySet map[format.PageID]struct{}
	opSMO      bool
	smoPending map[format.PageID]struct{}
	// unstamped counts, by first LSN, page-image batches that are appended to
	// the WAL but not yet stamped onto their buffer frames (stamping runs
	// outside e.mu). A checkpoint treats them as unflushed; see Checkpoint.
	unstamped map[format.LSN]int
	// pageLog holds page-delta bases; nil when page deltas are disabled.
	pageLog *pageLogCache
	crash   *wal.Injector
	closed  bool

	repl    Replicator
	replMu  sync.Mutex
	replLSN format.LSN

	// openNextLSN is WAL.NextLSN() as of the moment this Engine finished
	// opening (after any redo), never modified afterward. Checkpoint uses it
	// to detect "nothing has happened since this Engine was opened" and, in
	// that case, skip writing a new checkpoint record rather than
	// unconditionally consuming fresh WAL LSN numbers. See Checkpoint's
	// comment for why that distinction matters.
	openNextLSN format.LSN

	iso *integrity.Registry

	// budget/budgetFrames mirror what NewWithBudget reserved against
	// opt.Budget at open, released exactly once in Close.
	budget       *buffer.Budget
	budgetFrames int
}

// Replicator is the quorum-commit hook (Phase 15). Commit is not
// acknowledged until Replicate returns. A nil Replicator is single-node.
type Replicator interface {
	Replicate(recs []wal.Record) error
}

// ReplicationOrphanReporter is an optional capability of a Replicator: it
// lets commitAndReplicate report that a local commit could not be
// replicated to quorum, so the Replicator can protect linearizable
// ("STRONG") reads until an operator confirms the node's divergence has
// been checked/reconciled. Implemented by *replication.Cluster; a test
// double that doesn't implement it just isn't notified (the type
// assertion at the call site fails harmlessly).
type ReplicationOrphanReporter interface {
	ReportReplicationOrphan()
}

// NotProposedError is an optional capability a Replicate error can
// implement: NotProposed reports whether the entry is known to have never
// reached the Raft log at all (rejected before being proposed — e.g. this
// node was not the leader), as opposed to an ambiguous in-doubt outcome
// (proposed, but the quorum wait itself failed/timed out/lost leadership
// mid-flight). commitAndReplicate only discards a held, not-yet-durable
// local commit on the definite case; an error that doesn't implement this
// is always treated as ambiguous — the safe default, matching this
// package's pre-existing fail-open behavior. Implemented by
// *replication.Cluster's not-leader rejection; a test double that doesn't
// implement it just isn't recognized as definite (the type assertion at
// the call site fails harmlessly, same convention as
// ReplicationOrphanReporter above).
type NotProposedError interface {
	error
	NotProposed() bool
}

// Txn is a write transaction. WAL records, UNDO, and page dirtiness are
// tracked here; visibility and locking live in the txn manager.
type Txn struct {
	eng          *Engine
	id           format.TxnID
	prev         format.LSN
	first        format.LSN
	lastUndo     format.UndoID
	liveTargets  []UndoTarget
	dirty        map[format.PageID]*undoPage
	created      map[format.PageID]struct{}
	changes      []wal.Change
	changeBytes  int
	changeBroken bool
	done         bool
	// legacy marks a single-writer maintenance transaction (BeginWrite, or
	// OnDirty's implicit begin). Its commit keeps the engine mutex across the
	// durability wait; see commitAndReplicate.
	legacy bool
	// loggedPages are the pages whose state this transaction has logged,
	// so their delta bases can be settled when its commit is appended.
	loggedPages []format.PageID
	// durableWait is set while the transaction's commit record is appended
	// and its commit is waiting, outside e.mu, for the WAL to make it
	// durable. A rollback in that window is refused: the commit record may
	// already be on stable storage.
	durableWait bool
}

// UndoTarget reverses one logical undo record against the live, in-memory
// tree it originated from. Implemented by *btree.Tree (btree imports
// storage, so the interface lives here to avoid a cycle). LogUndo records
// the target alongside each durable undo.Record it appends, in the same
// order, so a rollback can replay every record through the exact tree that
// produced it — required once a single SQL transaction spans several trees
// (heap + indexes), where a raw undo.Record alone does not say which tree
// its Key belongs to.
type UndoTarget interface {
	ApplyUndo(txn *Txn, rec undo.Record) error
}

const (
	// Logical CDC staging is bounded independently of query result memory. A
	// large transaction is rejected before it can create an unbounded WAL
	// change batch; callers may commit smaller batches.
	maxTxnChanges     = 131072
	maxTxnChangeBytes = 16 << 20
)

func (t *Txn) ID() format.TxnID {
	if t == nil {
		return 0
	}
	return t.id
}

func (t *Txn) LastUndo() format.UndoID {
	if t == nil {
		return 0
	}
	return t.lastUndo
}

// StageChange attaches a logical SQL-row mutation to this storage
// transaction. Changes are copied, bounded, and written contiguously at
// commit so a CDC resume token at a prior COMMIT cannot skip a transaction
// that began earlier.
func (t *Txn) StageChange(change wal.Change) error {
	if t == nil || t.eng == nil {
		return nerr.New(nerr.InvalidArgument, "storage.Txn.StageChange", "nil transaction")
	}
	n, err := change.EncodedSize()
	if err != nil {
		t.eng.mu.Lock()
		t.changeBroken = true
		t.eng.mu.Unlock()
		return err
	}
	t.eng.mu.Lock()
	defer t.eng.mu.Unlock()
	if t.done {
		return nerr.New(nerr.InvalidArgument, "storage.Txn.StageChange", "transaction is not active")
	}
	if _, ok := t.eng.writers[t.id]; !ok {
		return nerr.New(nerr.InvalidArgument, "storage.Txn.StageChange", "transaction is not registered")
	}
	if len(t.changes) >= maxTxnChanges || t.changeBytes+n > maxTxnChangeBytes {
		t.changeBroken = true
		return nerr.New(nerr.Exhausted, "storage.Txn.StageChange", "transaction change stream limit exceeded")
	}
	t.changes = append(t.changes, wal.CloneChange(change))
	t.changeBytes += n
	return nil
}

type undoPage struct {
	created bool
}

func Create(path string, keys crypto.KeyProvider, bufferPages int) (*Engine, error) {
	id, err := format.NewIdentity()
	if err != nil {
		return nil, err
	}
	return CreateWithIdentity(path, id, keys, bufferPages)
}

// CreateWithIdentity uses a caller-chosen identity so a keystore can bind to the same UUIDs.
func CreateWithIdentity(path string, id format.Identity, keys crypto.KeyProvider, bufferPages int) (*Engine, error) {
	return open(path, keys, bufferPages, id, true, OpenOptions{})
}

// CreateWith creates a database with explicit options.
func CreateWith(path string, keys crypto.KeyProvider, bufferPages int, opt OpenOptions) (*Engine, error) {
	id, err := format.NewIdentity()
	if err != nil {
		return nil, err
	}
	return open(path, keys, bufferPages, id, true, opt)
}

func Open(path string, keys crypto.KeyProvider, bufferPages int) (*Engine, error) {
	return OpenWith(path, keys, bufferPages, OpenOptions{})
}

// OpenOptions tunes recovery. Zero values preserve Open() behavior.
type OpenOptions struct {
	UntilLSN format.LSN
	Archiver wal.Archiver
	// Budget, if non-nil, gates this Engine's buffer pool against a
	// process-wide shared frame ceiling (M2-3b-2) instead of allocating its
	// bufferPages frames unconditionally. Reserved once at open, released
	// once at Close.
	Budget *buffer.Budget
	// PageDeltas chooses whether commits may log page deltas instead of full
	// page images (see pagelog.go and docs/wal.md).
	PageDeltas PageDeltaMode
}

// PageDeltaMode selects page-delta logging.
type PageDeltaMode uint8

const (
	// PageDeltasAuto enables deltas for a database this binary creates and
	// leaves an existing database's log as it is: a log already at control
	// version 2 writes deltas, an older one keeps full images and stays
	// readable by the release that created it.
	PageDeltasAuto PageDeltaMode = iota
	// PageDeltasOn also moves an existing log to control version 2. It is
	// one-way: releases that predate page deltas can no longer open it.
	PageDeltasOn
	// PageDeltasOff never writes deltas. Reading them is always supported.
	PageDeltasOff
)

// ParsePageDeltaMode maps the wal_page_deltas configuration value; the empty
// string is auto.
func ParsePageDeltaMode(s string) (PageDeltaMode, error) {
	switch s {
	case "", "auto":
		return PageDeltasAuto, nil
	case "on":
		return PageDeltasOn, nil
	case "off":
		return PageDeltasOff, nil
	}
	return 0, nerr.New(nerr.InvalidArgument, "storage.ParsePageDeltaMode", "wal_page_deltas must be auto, on, or off")
}

// OpenWith opens an existing database and optionally stops redo at UntilLSN.
func OpenWith(path string, keys crypto.KeyProvider, bufferPages int, opt OpenOptions) (*Engine, error) {
	return open(path, keys, bufferPages, format.Identity{}, false, opt)
}

func open(path string, keys crypto.KeyProvider, bufferPages int, id format.Identity, create bool, opt OpenOptions) (eng *Engine, err error) {
	if bufferPages < 1 {
		return nil, nerr.New(nerr.InvalidArgument, "storage.open", "buffer_pages must be >= 1")
	}
	wdir := wal.DirFor(path)
	udir := undo.DirFor(path)
	// Record which sidecar directories predate this call, so a failed create
	// only removes what it made (see the cleanup below).
	_, walAbsent := os.Stat(wdir)
	_, undoAbsent := os.Stat(udir)

	var fm *file.Manager
	if create {
		fm, err = file.Create(path, id, keys)
	} else {
		fm, err = file.Open(path, keys)
	}
	if err != nil {
		return nil, err
	}
	if create {
		// file.Create made the data file exclusively, so a later failure here
		// leaves a file no database was ever built on. Every subsequent
		// create at this path would then fail with AlreadyExists — a full
		// disk or a failing device would permanently poison the path — so
		// unwind exactly what this call created. Pre-existing sidecar
		// directories are left alone: they are not ours to delete.
		defer func() {
			if eng != nil {
				return
			}
			_ = os.Remove(path)
			_ = os.Remove(integrity.PathFor(path))
			if walAbsent != nil {
				_ = os.RemoveAll(wdir)
			}
			if undoAbsent != nil {
				_ = os.RemoveAll(udir)
			}
		}()
	}
	ident := fm.Identity()
	if env, ok := keys.(*crypto.Envelope); ok {
		if env.Identity() != ident {
			_ = fm.Close()
			return nil, nerr.New(nerr.Corruption, "storage.open", "keystore identity does not match data file")
		}
		if env.NonceHigh() > fm.Superblock().NextNonceGeneration {
			if err := fm.AdvanceNonceTo(env.NonceHigh()); err != nil {
				_ = fm.Close()
				return nil, err
			}
		}
		if err := env.NoteNonceHigh(fm.Superblock().NextNonceGeneration); err != nil {
			_ = fm.Close()
			return nil, err
		}
	}
	var lg *wal.Log
	walOpt := wal.Options{Archiver: opt.Archiver}
	if create {
		createOpt := walOpt
		createOpt.PageDeltas = opt.PageDeltas != PageDeltasOff
		lg, err = wal.Create(wdir, keys, ident, createOpt)
	} else if _, statErr := os.Stat(wdir); os.IsNotExist(statErr) {
		lg, err = wal.Create(wdir, keys, ident, walOpt)
	} else {
		lg, err = wal.Open(wdir, keys, ident, walOpt)
		if err != nil {
			_ = fm.Close()
			return nil, err
		}
		if err := recovery.RedoUntil(fm, lg, opt.UntilLSN); err != nil {
			_ = lg.Close()
			_ = fm.Close()
			return nil, err
		}
		if opt.UntilLSN != 0 {
			if err := lg.ClipTo(opt.UntilLSN); err != nil {
				_ = lg.Close()
				_ = fm.Close()
				return nil, err
			}
		}
	}
	if err != nil {
		_ = fm.Close()
		return nil, err
	}
	var ul *undo.Log
	if create {
		ul, err = undo.Create(udir, keys, ident)
	} else if _, statErr := os.Stat(udir); os.IsNotExist(statErr) {
		ul, err = undo.Create(udir, keys, ident)
	} else {
		ul, err = undo.Open(udir, keys, ident)
	}
	if err != nil {
		_ = lg.Close()
		_ = fm.Close()
		return nil, err
	}
	// No WAL byte that can carry a row version may reach disk before the
	// undo record that reverses it (undo.Log.Sync).
	lg.SetBeforeWrite(ul.Sync)
	if !create {
		open, aborted, uerr := recovery.NotCommittedUntil(lg, opt.UntilLSN)
		if uerr != nil {
			_ = ul.Close()
			_ = lg.Close()
			_ = fm.Close()
			return nil, uerr
		}
		// Undo is applied to aborted transactions too: see
		// recovery.NotCommittedUntil. It only reverses versions still carrying
		// the transaction's id, so versions written after a completed rollback
		// are untouched.
		if err := undo.Apply(fm, ul, append(append([]format.TxnID(nil), open...), aborted...)); err != nil {
			_ = ul.Close()
			_ = lg.Close()
			_ = fm.Close()
			return nil, err
		}
	}
	alloc, err := allocator.Open(fm)
	if err != nil {
		_ = ul.Close()
		_ = lg.Close()
		_ = fm.Close()
		return nil, err
	}
	pool, err := buffer.NewWithBudget(fm, bufferPages, opt.Budget)
	if err != nil {
		_ = ul.Close()
		_ = lg.Close()
		_ = fm.Close()
		return nil, err
	}
	iso, err := integrity.OpenOrCreate(integrity.PathFor(path))
	if err != nil {
		_ = ul.Close()
		_ = lg.Close()
		_ = fm.Close()
		return nil, err
	}
	if !create && opt.PageDeltas == PageDeltasOn {
		if err := lg.EnablePageDeltas(); err != nil {
			_ = ul.Close()
			_ = lg.Close()
			_ = fm.Close()
			return nil, err
		}
	}
	e := &Engine{
		File:         fm,
		Alloc:        alloc,
		Buffer:       pool,
		WAL:          lg,
		Undo:         ul,
		TM:           txn.NewManager(lg.NextTxn()),
		writers:      make(map[format.TxnID]*Txn),
		iso:          iso,
		budget:       opt.Budget,
		budgetFrames: bufferPages,
		openNextLSN:  lg.NextLSN(),
	}
	if lg.PageDeltas() && opt.PageDeltas != PageDeltasOff {
		// Half the buffer pool's frames: the bases worth keeping are the pages
		// being written, which are also the pages kept in memory.
		e.pageLog = newPageLogCache(bufferPages / 2)
	}
	if !create {
		open, aborted, uerr := recovery.NotCommittedUntil(lg, opt.UntilLSN)
		if uerr == nil {
			e.TM.Recover(lg.NextTxn(), nil, append(open, aborted...))
		}
		e.recheckIsolated()
	}
	pool.SetHooks(e)
	return e, nil
}

func (e *Engine) Path() string {
	if e == nil || e.File == nil {
		return ""
	}
	return e.File.Path()
}

func (e *Engine) Keys() crypto.KeyProvider {
	if e == nil || e.File == nil {
		return nil
	}
	return e.File.Keys()
}

func (e *Engine) Identity() format.Identity {
	return e.File.Identity()
}

// Reencrypt writes every allocated page with the current DEK version.
func (e *Engine) Reencrypt() error {
	if err := e.Buffer.FlushAll(); err != nil {
		return err
	}
	return e.File.Reencrypt()
}

// SetArchiver installs the PITR hook on the live WAL.
func (e *Engine) SetArchiver(a wal.Archiver) {
	if e == nil || e.WAL == nil {
		return
	}
	e.WAL.SetArchiver(a)
}

// SetReplicator installs the quorum-commit hook. Pass nil to disable.
func (e *Engine) SetReplicator(r Replicator) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.repl = r
	e.mu.Unlock()
}

func (e *Engine) SetCrash(p wal.Point) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crash == nil {
		e.crash = wal.NewInjector()
	}
	e.crash.Arm(p)
	if e.WAL != nil {
		e.WAL.SetCrash(e.crash)
	}
}

func (e *Engine) CrashAt(p wal.Point) error {
	e.mu.Lock()
	inj := e.crash
	e.mu.Unlock()
	if inj == nil {
		return nil
	}
	return inj.Hit(p)
}

func (e *Engine) NewSlotted() (*buffer.Handle, error) {
	return e.NewPage(format.PageTypeSlotted)
}

func (e *Engine) NewPage(typ format.PageType) (*buffer.Handle, error) {
	if !typ.Known() || typ == format.PageTypeSuperblock || typ == format.PageTypeInvalid {
		return nil, nerr.New(nerr.InvalidArgument, "storage.NewPage", "invalid page type")
	}
	e.mu.Lock()
	if e.pageWriter {
		e.opSMO = true
	}
	e.mu.Unlock()
	id, err := e.Alloc.Alloc()
	if err != nil {
		return nil, err
	}
	return e.Buffer.InstallNew(id, typ)
}

func (e *Engine) PrimaryTree() (format.PageID, uint16) {
	return e.File.PrimaryTree()
}

// SetStorageCapBytes limits how far the data file may grow. capBytes == 0
// disables the cap. Growth (new page allocation, i.e. INSERT / row-splitting
// UPDATE) past the cap fails with nerr.Exhausted; DELETE, rollback, and
// in-place UPDATE keep working because they reuse freed pages. The cap covers
// the main data file only, not WAL or UNDO. It is set out of band from the
// hosting registry and is not persisted in the database.
func (e *Engine) SetStorageCapBytes(capBytes uint64) {
	if e == nil || e.Alloc == nil {
		return
	}
	var pages uint64
	if capBytes > 0 {
		pages = capBytes / uint64(format.PhysicalPageSize)
		if pages == 0 {
			pages = 1
		}
	}
	e.Alloc.SetCapPages(pages)
}

// StorageCapBytes reports the current data-file growth cap in bytes (0 = none).
func (e *Engine) StorageCapBytes() uint64 {
	if e == nil || e.Alloc == nil {
		return 0
	}
	return e.Alloc.CapPages() * uint64(format.PhysicalPageSize)
}

func (e *Engine) SetPrimaryTree(root format.PageID, height uint16) error {
	return e.NoteTree(root, height)
}

func (e *Engine) Drop(id format.PageID) error {
	e.mu.Lock()
	if e.pageWriter {
		e.opSMO = true
	}
	if e.txn != nil {
		delete(e.txn.dirty, id)
		e.txn.created[id] = struct{}{}
	}
	e.mu.Unlock()
	if err := e.Buffer.Drop(id); err != nil {
		return err
	}
	return e.Alloc.Free(id)
}

// ReclaimPages durably returns detached, unreachable pages to the allocator.
// Callers must exclude the primary tree and serialize this with transactions.
func (e *Engine) ReclaimPages(ids []format.PageID) error {
	if e == nil || e.Alloc == nil || e.Buffer == nil || e.File == nil {
		return nerr.New(nerr.InvalidArgument, "storage.ReclaimPages", "nil engine")
	}
	e.mu.Lock()
	active := len(e.writers) != 0 || e.opTxn != nil || e.txn != nil
	e.mu.Unlock()
	if active || (e.TM != nil && e.TM.LiveSnapshots() != 0) {
		return nerr.New(nerr.Unavailable, "storage.ReclaimPages", "transactions are active")
	}
	seen := make(map[format.PageID]struct{}, len(ids))
	for _, id := range ids {
		if err := id.UserData(); err != nil {
			return err
		}
		if _, dup := seen[id]; dup {
			return nerr.New(nerr.InvalidArgument, "storage.ReclaimPages", "duplicate page id")
		}
		seen[id] = struct{}{}
	}
	for _, id := range ids {
		if err := e.Buffer.Drop(id); err != nil {
			return err
		}
		if err := e.Alloc.Free(id); err != nil {
			return err
		}
	}
	if err := e.Alloc.Flush(); err != nil {
		return err
	}
	return e.File.Sync()
}

func (e *Engine) Pin(id format.PageID) (*buffer.Handle, error) {
	h, err := e.Buffer.Pin(id)
	if err == nil {
		return h, nil
	}
	if !integrity.IsFailure(err) {
		return nil, err
	}
	return e.recoverPin(id, err)
}

// Isolated returns the current quarantine set. Page contents are never included.
func (e *Engine) Isolated() []integrity.Isolated {
	if e == nil || e.iso == nil {
		return nil
	}
	return e.iso.List()
}

func (e *Engine) recoverPin(id format.PageID, cause error) (*buffer.Handle, error) {
	if e.iso != nil {
		_ = e.iso.Isolate(id, integrity.ReasonOf(cause))
	}
	metrics.Default().AddIsolated()
	if e.File == nil || e.WAL == nil {
		return nil, nerr.Wrap(nerr.Corruption, "storage.Pin", "page isolated; no recoverable image", cause)
	}
	body, err := recovery.RepairPage(e.File, e.WAL, id)
	if err != nil {
		return nil, nerr.Wrap(nerr.Corruption, "storage.Pin", "page isolated; no recoverable image", cause)
	}
	if err := e.Buffer.Replace(id, body); err != nil {
		return nil, err
	}
	if e.iso != nil {
		_ = e.iso.Clear(id)
	}
	metrics.Default().AddRepaired()
	return e.Buffer.Pin(id)
}

func (e *Engine) recheckIsolated() {
	if e == nil || e.iso == nil {
		return
	}
	for _, rec := range e.iso.List() {
		if _, err := e.File.ReadLogical(rec.PageID); err == nil {
			_ = e.iso.Clear(rec.PageID)
			continue
		}
		if _, err := recovery.RepairPage(e.File, e.WAL, rec.PageID); err == nil {
			_ = e.iso.Clear(rec.PageID)
			metrics.Default().AddRepaired()
		}
	}
}

func (e *Engine) Sync() error {
	if e.Alloc != nil {
		if err := e.Alloc.Flush(); err != nil {
			return err
		}
	}
	if e.WAL != nil {
		if err := e.WAL.Flush(e.WAL.NextLSN() - 1); err != nil {
			return err
		}
	}
	if err := e.Buffer.FlushAll(); err != nil {
		return err
	}
	return e.File.Sync()
}

func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	active := e.txn
	e.mu.Unlock()
	if active != nil {
		_ = e.Rollback()
	}
	e.budget.Release(e.budgetFrames)
	var first error
	if e.Alloc != nil {
		if err := e.Alloc.Flush(); err != nil && first == nil {
			first = err
		}
	}
	if err := e.Checkpoint(); err != nil && first == nil {
		first = err
	}
	if e.WAL != nil {
		if err := e.WAL.Close(); err != nil && first == nil {
			first = err
		}
	}
	if e.Undo != nil {
		if err := e.Undo.Close(); err != nil && first == nil {
			first = err
		}
	}
	if e.File != nil {
		if err := e.File.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Kill simulates process death: drop unsynced WAL and close without flushing.
func (e *Engine) Kill() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.closed = true
	e.txn = nil
	e.mu.Unlock()
	if e.WAL != nil {
		e.WAL.CrashClose()
	}
	if e.Undo != nil {
		e.Undo.CrashClose()
	}
	if e.File != nil {
		e.File.CrashClose()
	}
}

// BeginExclusiveWrite starts a maintenance write transaction and returns its
// handle, holding the engine's exclusive-writer lock until EndExclusiveWrite
// or AbandonExclusiveWrite.
//
// Maintenance writers (tombstone purge, tree creation) used to call
// BeginWrite and then Commit, both of which act on the single engine-global
// e.txn slot. Two of them running at once -- a heap purge and an index purge
// after concurrent commits -- overwrote each other's slot: the first Commit
// committed the second writer's transaction, and the first writer's own
// transaction stayed in e.writers forever with pages AllowFlush would never
// release. The explicit handle and the lock remove both halves of that.
func (e *Engine) BeginExclusiveWrite() (*Txn, error) {
	e.exclusiveMu.Lock()
	e.mu.Lock()
	t, err := e.beginLocked(true)
	e.mu.Unlock()
	if err != nil {
		e.exclusiveMu.Unlock()
		return nil, err
	}
	return t, nil
}

// EndExclusiveWrite commits (or rolls back) t and releases the exclusive
// writer lock.
func (e *Engine) EndExclusiveWrite(t *Txn, commit bool) error {
	defer e.exclusiveMu.Unlock()
	if commit {
		return e.CommitTxn(t)
	}
	return e.RollbackTxn(t)
}

// AbandonExclusiveWrite releases the exclusive writer lock without resolving
// t. It exists only for an injected crash, after which the engine is dead.
func (e *Engine) AbandonExclusiveWrite() {
	e.exclusiveMu.Unlock()
}

func (e *Engine) BeginWrite() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.beginLocked(true)
	return err
}

func (e *Engine) StartTxn() (*Txn, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.beginLocked(false)
}

func (e *Engine) Enter(t *Txn) {
	e.pageMu.Lock()
	e.mu.Lock()
	e.opTxn = t
	e.pageWriter = true
	e.opDirty = e.opDirty[:0]
	if e.opDirtySet == nil {
		e.opDirtySet = make(map[format.PageID]struct{})
	} else {
		clear(e.opDirtySet)
	}
	e.opSMO = false
	e.mu.Unlock()
}

// LeaveOp ends a page-writing operation begun with Enter, first making any
// structure modification it performed durable-by-order.
//
// A B+tree split or merge takes effect immediately: other transactions route
// through the new page and write into it, and rollback never reverts it. Its
// redo, though, used to be only the page images the splitting transaction
// logs when it commits. If that transaction never committed, a committed
// transaction could make durable an image that points to, or lives in, a page
// whose own image was never logged -- after a crash, recovery found the page
// unreadable and the committed rows that had moved into it were gone
// (tests/crash TestConcurrentCommitsSurvivePowerLoss reproduced it about one
// round in three, with or without group commit).
//
// So a successful operation that allocated or dropped a page logs every page
// it dirtied as its own small committed system transaction (Begin, page
// images, AllocState, Commit), the standard nested-top-action treatment. It
// does not fsync: WAL order puts it ahead of any commit that can depend on it,
// so the flush that makes such a commit durable makes this durable first. The
// images may hold another transaction's uncommitted row versions; recovery's
// undo and MVCC visibility treat those exactly as they treat any shared page.
// A failed operation logs nothing -- its half-done structure must not become
// redo -- and a failed append leaves the pages pending: AllowFlush refuses
// them and the next commit retries them.
func (e *Engine) LeaveOp(t *Txn, opErr error) error {
	err := opErr
	if err == nil {
		err = e.logStructure(true, true)
	}
	e.Leave(t)
	return err
}

// logStructure logs pending structure modifications and, when ownOp is set,
// the one the calling operation just performed. ownOp must only be set by the
// goroutine that called Enter for the current operation: another goroutine
// reading the operation state would log a split that is still half done.
// pageWriteHeld reports whether the caller holds pageMu for writing;
// otherwise it is taken for reading while page images are copied. Called
// without e.mu.
func (e *Engine) logStructure(ownOp, pageWriteHeld bool) error {
	e.mu.Lock()
	includeOp := ownOp && e.opSMO
	if ownOp {
		e.opSMO = false
	}
	if e.WAL == nil || (!includeOp && len(e.smoPending) == 0) {
		e.mu.Unlock()
		return nil
	}
	seen := make(map[format.PageID]struct{}, len(e.opDirty)+len(e.smoPending))
	var ids []format.PageID
	if includeOp {
		for _, id := range e.opDirty {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	for id := range e.smoPending {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	e.mu.Unlock()
	slices.Sort(ids)

	// As in flushDirtyImages, pageMu stays read-held until the images are
	// appended, so no page changes between its copy and its LSN.
	if !pageWriteHeld {
		e.pageMu.RLock()
		defer e.pageMu.RUnlock()
	}
	kept := ids[:0]
	images := make([][]byte, 0, len(ids))
	for _, id := range ids {
		data, ok := e.Buffer.CopyPageInto(id, nil)
		if !ok {
			continue // dropped by the operation itself
		}
		kept = append(kept, id)
		images = append(images, data)
	}
	ids = kept

	e.mu.Lock()
	markPending := func() {
		if e.smoPending == nil {
			e.smoPending = make(map[format.PageID]struct{})
		}
		for _, id := range ids {
			e.smoPending[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		e.mu.Unlock()
		return nil
	}
	sys := e.WAL.AllocTxn()
	begin, err := e.WAL.Append(wal.BeginRec(sys))
	if err != nil {
		markPending()
		e.mu.Unlock()
		return err
	}
	lsns, last, first, err := e.appendPageStatesLocked(sys, begin, ids, images, false)
	if err != nil {
		markPending()
		e.mu.Unlock()
		return err
	}
	next, head, count := e.File.AllocState()
	allocLSN, err := e.WAL.Append(wal.AllocState(sys, last, next, head, count))
	if err != nil {
		markPending()
		e.mu.Unlock()
		return err
	}
	if _, err := e.WAL.Append(wal.CommitRec(sys, allocLSN)); err != nil {
		markPending()
		for _, id := range ids {
			e.pageLog.forget(id) // its record will never be replayed
		}
		e.mu.Unlock()
		return err
	}
	e.pageLog.settle(sys, ids)
	for _, id := range ids {
		delete(e.smoPending, id)
	}
	e.noteUnstampedLocked(first)
	e.mu.Unlock()
	e.stampImages(ids, lsns, first)
	return nil
}

// noteUnstampedLocked records that a batch starting at lsn is appended but not
// yet stamped. Called with e.mu held, in the same critical section as the
// append.
func (e *Engine) noteUnstampedLocked(lsn format.LSN) {
	if e.unstamped == nil {
		e.unstamped = make(map[format.LSN]int)
	}
	e.unstamped[lsn]++
}

// stampImages stamps each page's frame with its image LSN and then retires
// the batch's unstamped entry. Called without e.mu.
func (e *Engine) stampImages(ids []format.PageID, lsns []format.LSN, first format.LSN) {
	for i, id := range ids {
		e.Buffer.StampLSN(id, lsns[i])
	}
	if first == 0 {
		return
	}
	e.mu.Lock()
	if n := e.unstamped[first]; n <= 1 {
		delete(e.unstamped, first)
	} else {
		e.unstamped[first] = n - 1
	}
	e.mu.Unlock()
}

func (e *Engine) minUnstampedLocked() format.LSN {
	var min format.LSN
	for lsn := range e.unstamped {
		if min == 0 || lsn < min {
			min = lsn
		}
	}
	return min
}

func (e *Engine) Leave(t *Txn) {
	e.mu.Lock()
	if e.opTxn == t {
		e.opTxn = nil
	}
	e.pageWriter = false
	e.mu.Unlock()
	e.pageMu.Unlock()
}

func (e *Engine) beginLocked(legacy bool) (*Txn, error) {
	id := e.WAL.AllocTxn()
	lsn, err := e.WAL.Append(wal.BeginRec(id))
	if err != nil {
		return nil, err
	}
	t := &Txn{
		eng:     e,
		id:      id,
		prev:    lsn,
		first:   lsn,
		dirty:   make(map[format.PageID]*undoPage),
		created: make(map[format.PageID]struct{}),
		legacy:  legacy,
	}
	if e.writers == nil {
		e.writers = make(map[format.TxnID]*Txn)
	}
	e.writers[id] = t
	if e.TM != nil {
		e.TM.Attach(id, txn.SnapshotIsolation)
	}
	if legacy || e.txn == nil {
		e.txn = t
	}
	// opTxn identifies whichever transaction currently holds pageMu and is
	// actively mutating pages — Enter/Leave are its only correct setter and
	// clearer, since every hook that reads opTxn as attribution (LogUndo,
	// OnDirty, OnInstall, ...) assumes the assignment is synchronized with
	// pageMu, not merely with e.mu. Setting it here unconditionally, for a
	// brand new transaction that has not called Enter yet, raced a
	// different, concurrently-still-entered transaction: beginLocked only
	// takes e.mu (briefly), never pageMu, so a StartTxn() for txn B could
	// run to completion — including this assignment — while txn A was still
	// inside its own Enter..Leave section with pageMu held, silently
	// overwriting opTxn out from under A. Every subsequent hook call A made
	// before its own Leave (e.g. LogUndo for a later step of the same
	// statement) then misattributed A's own work to B: undo records ended
	// up chained onto B's lastUndo/liveTargets instead of A's, so neither
	// A's nor B's rollback correctly reversed the transaction that actually
	// produced them. Confirmed live via a 3-writer concurrent UPDATE/INSERT/
	// DELETE stress test: LogUndo's txnID for an index write repeatedly
	// diverged from the *btree.Txn that issued it. legacy callers (a
	// maintenance operation via BeginWrite, or OnDirty's implicit auto-begin
	// when no transaction is active at all) are unaffected: by construction
	// nothing else can be concurrently entered when they run, so keep
	// setting opTxn immediately for them; only the normal StartTxn (SQL
	// transaction, legacy=false) path — the one genuinely used
	// concurrently — drops the premature assignment and relies solely on
	// this transaction's own later Enter() call.
	if legacy {
		e.opTxn = t
	}
	return t, nil
}

func (e *Engine) Commit() error {
	e.mu.Lock()
	t := e.txn
	e.mu.Unlock()
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "storage.Commit", "no active transaction")
	}
	return e.commitAndReplicate(t)
}

func (e *Engine) CommitTxn(t *Txn) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "storage.CommitTxn", "nil transaction")
	}
	return e.commitAndReplicate(t)
}

func (e *Engine) commitAndReplicate(t *Txn) error {
	e.mu.Lock()
	needRepl := e.repl != nil
	e.mu.Unlock()
	if !needRepl {
		return e.commitUnreplicated(t)
	}
	e.replMu.Lock()
	defer e.replMu.Unlock()
	e.mu.Lock()
	recs, lsn, preReplLSN, err := e.prepareCommitLocked(t, false, true)
	repl := e.repl
	e.mu.Unlock()
	if err != nil {
		return err
	}

	rerr := repl.Replicate(recs)
	if rerr == nil {
		e.mu.Lock()
		ferr := e.finishCommitOK(t, lsn)
		e.mu.Unlock()
		return ferr
	}
	if np, ok := rerr.(NotProposedError); ok && np.NotProposed() {
		// The entry never reached Raft at all (this node was not the
		// leader when Replicate was called) — the held CommitRec is still
		// unflushed and t's writes are neither visible nor lock-released,
		// so it's safe to discard: no acknowledged write is lost, and now
		// no local orphan is left behind either.
		if ferr := e.finishCommitDiscarded(t, preReplLSN); ferr != nil {
			return ferr
		}
		return rerr
	}
	// Ambiguous/in-doubt failure (Replicate was actually proposed to Raft
	// but the quorum wait itself failed — see isRetryableApplyErr):
	// discarding here would be worse than today's known orphan, because
	// the WAL's LSN counter has already moved past this record, so if the
	// entry *did* reach quorum, a later replay of it via ApplyReplicated
	// would be silently skipped as already-seen (LSN < nextLSN) rather
	// than applied — a permanent, undetectable divergence. So this case
	// keeps this package's original fail-open behavior byte for byte:
	// commit, surface an observable orphan count, and tell the Replicator
	// so it can bar this node from serving STRONG reads until an operator
	// confirms the divergence is checked/reconciled
	// (CLUSTER RECONCILE CONFIRM). See TODO.md's Phase 27 exit gate,
	// "Local commit precedes replication acknowledgment", for the full
	// writeup of why this residual case can't be closed by this fix.
	e.mu.Lock()
	ferr := e.finishCommitOK(t, lsn)
	e.mu.Unlock()
	if ferr != nil {
		return ferr
	}
	metrics.Default().AddReplicationOrphan()
	if reporter, ok := repl.(ReplicationOrphanReporter); ok {
		reporter.ReportReplicationOrphan()
	}
	return rerr
}

// takeReplLocked returns every WAL record since the last call (tracked by
// e.replLSN), read back from disk — so it only sees what's already been
// flushed. Called by prepareCommitLocked's hold branch after flushing
// everything up to (but not including) the held CommitRec.
func (e *Engine) takeReplLocked() ([]wal.Record, error) {
	if e.WAL == nil {
		return nil, nil
	}
	start := e.replLSN + 1
	if start == 0 {
		start = 1
	}
	recs, last, err := e.WAL.ScanFrom(start)
	if err != nil {
		return nil, err
	}
	if last != 0 {
		e.replLSN = last
	}
	return recs, nil
}

func (e *Engine) commitLocked(txn *Txn, pageWriteHeld bool) error {
	_, _, _, err := e.prepareCommitLocked(txn, pageWriteHeld, false)
	return err
}

// appendCommitBodyLocked runs a commit up to, but not including, its commit
// record: the change-stream check, the undo and dirty-page flushes, and the
// AllocState and Change records. Every commit path shares it, so their
// ordering cannot drift apart. Called with e.mu held.
func (e *Engine) appendCommitBodyLocked(txn *Txn, pageWriteHeld bool) error {
	if txn.changeBroken {
		return nerr.New(nerr.Conflict, "storage.CommitTxn", "transaction change stream is incomplete; rollback required")
	}
	if len(e.smoPending) > 0 {
		// A structure modification this commit may depend on is not logged
		// yet (its append failed). Log it first, or refuse to commit.
		e.mu.Unlock()
		err := e.logStructure(false, pageWriteHeld)
		e.mu.Lock()
		if err != nil {
			return err
		}
	}
	if e.Undo != nil {
		if err := e.Undo.Flush(); err != nil {
			return err
		}
	}
	if err := e.flushDirtyImages(txn, pageWriteHeld); err != nil {
		return err
	}
	// Alloc.Flush is deliberately NOT called here: unlike WAL records
	// (gated by AppendHeld/ReleaseHold) and buffer-pool pages (gated by
	// AllowFlush refusing eviction while txn is still in e.writers),
	// Allocator.Flush persists directly to the data file's
	// superblock/freelist pages with no durability gate of its own and no
	// undo log — Alloc.Reload() only re-reads whatever is already on disk,
	// it cannot revert a persist that already happened. Calling it before
	// a hold resolves would make finishCommitDiscarded's Alloc.Reload()
	// silently fail to undo the discarded transaction's page allocations
	// (it would just reload the same, already-persisted state back). It
	// runs instead in finishCommitOK, once the transaction is known to be
	// actually committing (both here for the non-replicated path, where
	// this is the very next call, and after a hold resolves) — see
	// AllocState's own record below, which reads Allocator's in-memory
	// mirror (SetAllocStateMem) and is therefore already correct
	// regardless of when the physical persist happens.
	if err := e.hitLocked(wal.PointBeforeCommitRecord); err != nil {
		return err
	}
	next, head, count := e.File.AllocState()
	allocLSN, err := e.WAL.Append(wal.AllocState(txn.id, txn.prev, next, head, count))
	if err != nil {
		return err
	}
	txn.prev = allocLSN
	for _, change := range txn.changes {
		rec, err := wal.ChangeRec(txn.id, txn.prev, change)
		if err != nil {
			return err
		}
		changeLSN, err := e.WAL.Append(rec)
		if err != nil {
			return err
		}
		txn.prev = changeLSN
	}
	return nil
}

// commitUnreplicated commits t on a node with no replicator.
//
// The durability wait -- the WAL fsync -- runs without e.mu. Every commit used
// to hold the engine-wide mutex across its own fsync, so no other transaction
// could even append its commit record until it finished: N concurrent commits
// paid N fsyncs and throughput stayed flat from 1 to 64 connections. With e.mu
// released, commits that arrive during one fsync append their records and are
// made durable together by the next (wal.Log.Flush).
//
// The ordering that makes a commit correct is unchanged: every record,
// including the commit record, is appended under e.mu; the transaction stays
// in e.writers -- unacknowledged, invisible to other snapshots, still holding
// its locks, and still counted as active by a checkpoint -- until the commit
// record is durable; only then does it become visible (TM.Commit), under e.mu.
// A crash before durability recovers it as uncommitted, and after, as
// committed, exactly as before.
//
// A legacy maintenance transaction keeps the original fully-locked commit:
// those callers are single-writer by construction and rely on nothing else
// interleaving with them.
func (e *Engine) commitUnreplicated(t *Txn) error {
	e.mu.Lock()
	if t.legacy {
		defer e.mu.Unlock()
		return e.commitLocked(t, false)
	}
	lsn, err := e.prepareDeferredCommitLocked(t)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	t.durableWait = true
	e.mu.Unlock()

	ferr := e.WAL.Flush(lsn)

	e.mu.Lock()
	defer e.mu.Unlock()
	t.durableWait = false
	if ferr != nil {
		return ferr
	}
	e.completeCommitLocked(t)
	return nil
}

// prepareCommitLocked runs a transaction's commit through appending its
// CommitRec. Called with e.mu held.
//
// If hold is false (the e.repl == nil path, and every commit before this
// fix existed), the CommitRec is appended and flushed immediately and the
// transaction is fully finished before returning — byte for byte the
// original commitLocked behavior; recs and preReplLSN are always zero
// (nothing needs them).
//
// If hold is true, the CommitRec is appended via WAL.AppendHeld and left
// unresolved: t's writes are not yet durable, visible, or lock-released.
// recs is every WAL record appended since the last replicated LSN — not
// just this transaction's own — because other, unrelated transactions'
// records can interleave into the WAL between replication rounds (e.g.
// flushDirtyImages briefly releases e.mu mid-commit) and a follower's
// InstallRecords requires a gap-free LSN sequence; omitting any of them
// here would make a later, correctly-quorum-committed batch silently
// unappliable on a follower. Building recs this way needs
// e.takeReplLocked's disk-reading scan (the held CommitRec's own bytes are
// the one thing it can't see — appended onto the end manually below), so
// this transaction's own AllocState/Change/page-image records are flushed
// right here too, ahead of the point they'd ordinarily be flushed at, to
// make that scan see them. preReplLSN is the scan's watermark from just
// before it ran; if this batch is later discarded (finishCommitDiscarded),
// none of it — including the swept-up unrelated records, which stay
// durable — ever reached Raft, so the watermark must roll back to
// preReplLSN or those records would never be offered for replication
// again. The caller must replicate recs and then call finishCommitOK or
// finishCommitDiscarded to resolve it.
func (e *Engine) prepareCommitLocked(txn *Txn, pageWriteHeld, hold bool) (recs []wal.Record, lsn, preReplLSN format.LSN, err error) {
	if err := e.appendCommitBodyLocked(txn, pageWriteHeld); err != nil {
		return nil, 0, 0, err
	}
	commitRec := wal.CommitRec(txn.id, txn.prev)
	if hold {
		commitLSN, err := e.WAL.AppendHeld(commitRec)
		if err != nil {
			return nil, 0, 0, err
		}
		txn.prev = commitLSN
		commitRec.LSN = commitLSN
		if err := e.hitLocked(wal.PointAfterCommitRecordHeld); err != nil {
			return nil, 0, 0, err
		}
		if err := e.WAL.Flush(commitLSN - 1); err != nil {
			return nil, 0, 0, err
		}
		// preReplLSN is the watermark from before this scan: if the batch
		// this transaction's CommitRec ends up part of is discarded
		// (finishCommitDiscarded), nothing in it — including the other,
		// unrelated records takeReplLocked just swept up — ever reached
		// Raft, so the watermark must roll back to here too, or those
		// records would never be offered for replication again.
		preReplLSN = e.replLSN
		recs, err = e.takeReplLocked()
		if err != nil {
			return nil, 0, 0, err
		}
		recs = append(recs, commitRec)
		return recs, commitLSN, preReplLSN, nil
	}
	commitLSN, err := e.WAL.Append(commitRec)
	if err != nil {
		return nil, 0, 0, err
	}
	txn.prev = commitLSN
	e.pageLog.settle(txn.id, txn.loggedPages)
	if err := e.finishCommitOK(txn, commitLSN); err != nil {
		return nil, 0, 0, err
	}
	return nil, commitLSN, 0, nil
}

// prepareDeferredCommitLocked appends txn's commit and runs everything
// finishCommitOK does before the durability wait, returning the LSN the caller
// must make durable before calling completeCommitLocked. Called with e.mu held.
func (e *Engine) prepareDeferredCommitLocked(txn *Txn) (format.LSN, error) {
	if err := e.appendCommitBodyLocked(txn, false); err != nil {
		return 0, err
	}
	commitLSN, err := e.WAL.Append(wal.CommitRec(txn.id, txn.prev))
	if err != nil {
		return 0, err
	}
	txn.prev = commitLSN
	e.pageLog.settle(txn.id, txn.loggedPages)
	if err := e.Alloc.Flush(); err != nil {
		return 0, err
	}
	if commitLSN > e.replLSN {
		e.replLSN = commitLSN
	}
	if err := e.hitLocked(wal.PointAfterCommitRecordBeforeSync); err != nil {
		return 0, err
	}
	return commitLSN, nil
}

// finishCommitOK makes a transaction durable, visible, and unlocked: it
// releases any WAL hold (a no-op if prepareCommitLocked was called with
// hold == false, since nothing is held), flushes through lsn, and runs
// today's original commitLocked tail. Called with e.mu held.
func (e *Engine) finishCommitOK(txn *Txn, lsn format.LSN) error {
	if err := e.Alloc.Flush(); err != nil {
		return err
	}
	if err := e.WAL.ReleaseHold(true); err != nil {
		return err
	}
	// A held commit record becomes replayable only once released.
	e.pageLog.settle(txn.id, txn.loggedPages)
	// The held CommitRec itself was never seen by takeReplLocked's scan
	// (it wasn't durable yet), so the watermark sits one short; advance it
	// past this record now that it's known to be staying, so a later
	// takeReplLocked call doesn't needlessly re-scan and resend it (a
	// follower would just no-op the duplicate, but there's no reason to
	// pay for it). A no-op for the non-replicated path (lsn's value there
	// is never read back by anything replication-related).
	if lsn > e.replLSN {
		e.replLSN = lsn
	}
	if err := e.hitLocked(wal.PointAfterCommitRecordBeforeSync); err != nil {
		return err
	}
	if err := e.WAL.Flush(lsn); err != nil {
		return err
	}
	e.completeCommitLocked(txn)
	return nil
}

// completeCommitLocked makes a durable commit visible and releases the
// transaction. Called with e.mu held, only after txn's commit record is
// durable.
func (e *Engine) completeCommitLocked(txn *Txn) {
	txn.done = true
	delete(e.writers, txn.id)
	if e.txn == txn {
		e.txn = nil
	}
	if e.opTxn == txn {
		e.opTxn = nil
	}
	if e.TM != nil {
		e.TM.Commit(txn.id)
	}
	if e.Undo != nil && (e.TM == nil || e.TM.LiveSnapshots() == 0) {
		e.Undo.ForgetTxn(txn.id)
	}
}

// finishCommitDiscarded undoes a held commit whose replication is known to
// have never reached Raft: it splices the held CommitRec back out (it
// never touches disk) and then runs the same buffer-pool/allocator undo
// and WAL-abort sequence RollbackTxn uses for an ordinary ROLLBACK,
// including running the buffer/allocator undo without e.mu held — Buffer
// callbacks (Pin → OnPin) re-enter e.mu, so holding it here would
// deadlock, exactly the reason RollbackTxn already drops it first.
// preReplLSN is prepareCommitLocked's pre-scan watermark: since nothing in
// this batch reached Raft, it must be restored so the other, unrelated
// records takeReplLocked swept up alongside the discarded CommitRec — they
// stayed durable, this only undoes txn's own effects — are offered for
// replication again by a later commit, instead of being silently skipped
// forever because the watermark had already passed them.
func (e *Engine) finishCommitDiscarded(txn *Txn, preReplLSN format.LSN) error {
	if err := e.WAL.ReleaseHold(false); err != nil {
		return err
	}
	if err := e.hitLocked(wal.PointAfterHoldReleaseDiscardBeforeAbortAppend); err != nil {
		return err
	}
	e.mu.Lock()
	if e.txn == txn {
		e.txn = nil
	}
	if e.opTxn == txn {
		e.opTxn = nil
	}
	e.replLSN = preReplLSN
	e.mu.Unlock()

	// Best-effort: see the doc comment on undoTxnLogical for why a failure
	// here does not abort the discard itself.
	_ = e.undoTxnLogical(txn)
	e.retireRolledBack(txn)

	if _, err := e.WAL.Append(wal.AbortRec(txn.id, txn.prev)); err != nil {
		if e.TM != nil {
			e.TM.Abort(txn.id)
		}
		return err
	}
	if e.TM != nil {
		e.TM.Abort(txn.id)
	}
	return e.Alloc.Reload()
}

func (e *Engine) Rollback() error {
	e.mu.Lock()
	t := e.txn
	e.mu.Unlock()
	return e.RollbackTxn(t)
}

// retireRolledBack ends a rolled-back transaction's hold on its pages.
//
// Rollback changes pages -- the transaction's own edits and the undo that
// reverses them -- without logging them, so each such page's content no longer
// matches the state its LSN names. That was harmless with full page images, but
// a delta encoded against that logged state would be applied to different
// bytes. So the transaction stays in e.writers, keeping its pages unflushable,
// until undo has finished; then every delta base for those pages is discarded
// (their next logged state is a full image) and only then are the pages
// released for flushing.
func (e *Engine) retireRolledBack(txn *Txn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id := range txn.dirty {
		e.pageLog.forget(id)
	}
	for _, id := range txn.loggedPages {
		e.pageLog.forget(id)
	}
	delete(e.writers, txn.id)
}

func (e *Engine) RollbackTxn(txn *Txn) error {
	if err := e.CrashAt(wal.PointBeforeRollback); err != nil {
		return err
	}
	if txn == nil {
		return nil
	}
	e.mu.Lock()
	if txn.durableWait {
		e.mu.Unlock()
		return nerr.New(nerr.Conflict, "storage.RollbackTxn", "transaction is committing; its commit record may already be durable")
	}
	if e.txn == txn {
		e.txn = nil
	}
	if e.opTxn == txn {
		e.opTxn = nil
	}
	e.mu.Unlock()

	// Best-effort: see the doc comment on undoTxnLogical for why a failure
	// here does not abort the rollback itself.
	_ = e.undoTxnLogical(txn)
	e.retireRolledBack(txn)

	if _, err := e.WAL.Append(wal.AbortRec(txn.id, txn.prev)); err != nil {
		if e.TM != nil {
			e.TM.Abort(txn.id)
		}
		return err
	}
	if e.TM != nil {
		e.TM.Abort(txn.id)
	}
	_ = e.Alloc.Reload()
	return e.CrashAt(wal.PointAfterRollback)
}

// undoTxnLogical reverses txn's row-level changes by replaying its durable
// undo chain (newest -> oldest) through the exact tree each record came
// from (txn.liveTargets, appended by LogUndo in the same order the chain
// itself was built). Each reversal runs through the tree's ordinary
// key-based mutation path (see btree.Tree.ApplyUndo / applyUndoRec), which
// re-descends from the tree's live root — so it finds a key's current
// location even if a concurrent split relocated it after this record was
// logged, unlike a raw page-image restore.
//
// Deliberately does NOT touch page-level structure (splits, new sibling
// pages, separator insertions, root promotion): those are physical facts
// that took effect immediately when they happened, are potentially visible
// to and built upon by other transactions the moment they occur, and are
// never reverted by a rollback — the standard "structure modifications are
// physiological, not logical" design used by every mainstream B+Tree engine
// (e.g. ARIES nested top actions). The previous implementation
// (undoTxnBuffers, removed) restored whole dirty pages to a pre-transaction
// image and freed pages the transaction had created; both were unsound
// under concurrency: a dirty leaf page is shared row storage, so restoring
// its pre-image silently discarded any other transaction's row committed to
// that page in the meantime (the corruption undoTxnLogical replaces), and a
// "created" page from a leaf split holds rows *relocated* from the
// pre-existing page by the split, not just this transaction's own new row,
// so freeing it destroyed committed data outright.
//
// Failure here is intentionally best-effort and never aborts the rollback:
// this only fixes up the *live* buffer pool for transactions still running
// in this process. If the process crashes before txn's WAL abort record
// becomes durable, crash recovery's own undo.Apply independently redoes the
// identical reversal from the durable undo log against the on-disk file,
// so nothing durable depends on this call succeeding.
func (e *Engine) undoTxnLogical(txn *Txn) error {
	if e.Undo == nil || len(txn.liveTargets) == 0 {
		return nil
	}
	recs := e.Undo.Chain(txn.lastUndo) // newest -> oldest
	n := len(txn.liveTargets)
	if len(recs) != n {
		return nerr.New(nerr.Internal, "storage.undoTxnLogical", "undo chain length does not match recorded targets")
	}
	for i, rec := range recs {
		target := txn.liveTargets[n-1-i] // liveTargets is oldest -> newest; align with recs
		if target == nil {
			continue
		}
		if err := target.ApplyUndo(txn, rec); err != nil {
			return err
		}
	}
	return nil
}

// Savepoint is a position inside an open write transaction: how much undo the
// transaction had logged and how many CDC changes it had staged. It is a
// value, not a handle, so holding one costs nothing and a session can keep a
// bounded stack of them.
type Savepoint struct {
	undoLen     int
	changeLen   int
	changeBytes int
}

// Savepoint captures the transaction's current position.
func (t *Txn) Savepoint() Savepoint {
	if t == nil {
		return Savepoint{}
	}
	t.eng.mu.Lock()
	defer t.eng.mu.Unlock()
	return Savepoint{undoLen: len(t.liveTargets), changeLen: len(t.changes), changeBytes: t.changeBytes}
}

// RollbackToSavepoint reverses everything the transaction did after sp,
// leaving the transaction open with its locks still held — the SQL rule for
// ROLLBACK TO SAVEPOINT.
//
// Reversal replays the undo records logged after sp, newest first, through the
// same per-tree targets a full rollback uses, so a key is found even if a
// concurrent split moved it. Nothing extra is logged: redo is page-image based
// and a commit writes each dirty page's final image, which already reflects
// the reversal. The undo chain deliberately keeps the reversed records —
// applying one twice is idempotent (re-deleting a row already gone, or
// restoring a version already restored), so a later full rollback, or a second
// rollback to the same savepoint, stays correct.
//
// Staged CDC changes are truncated to the savepoint: a row that was rolled
// back must not be published as a change event.
func (e *Engine) RollbackToSavepoint(txn *Txn, sp Savepoint) error {
	if txn == nil {
		return nerr.New(nerr.InvalidArgument, "storage.RollbackToSavepoint", "no active transaction")
	}
	e.mu.Lock()
	if txn.done {
		e.mu.Unlock()
		return nerr.New(nerr.InvalidArgument, "storage.RollbackToSavepoint", "transaction is not active")
	}
	n := len(txn.liveTargets)
	if sp.undoLen > n || sp.changeLen > len(txn.changes) {
		e.mu.Unlock()
		return nerr.New(nerr.InvalidArgument, "storage.RollbackToSavepoint", "savepoint is not part of this transaction")
	}
	targets := append([]UndoTarget(nil), txn.liveTargets...)
	e.mu.Unlock()

	if e.Undo != nil && n > sp.undoLen {
		recs := e.Undo.Chain(txn.lastUndo) // newest -> oldest
		if len(recs) != n {
			return nerr.New(nerr.Internal, "storage.RollbackToSavepoint", "undo chain length does not match recorded targets")
		}
		// recs[i] corresponds to targets[n-1-i]; everything with an index at
		// or past sp.undoLen was written after the savepoint.
		for i, rec := range recs {
			pos := n - 1 - i
			if pos < sp.undoLen {
				break
			}
			target := targets[pos]
			if target == nil {
				continue
			}
			if err := target.ApplyUndo(txn, rec); err != nil {
				return err
			}
		}
	}

	e.mu.Lock()
	if sp.changeLen <= len(txn.changes) {
		txn.changes = txn.changes[:sp.changeLen]
		txn.changeBytes = sp.changeBytes
	}
	e.mu.Unlock()
	return nil
}

func (e *Engine) currentWriter() *Txn {
	if e.opTxn != nil {
		return e.opTxn
	}
	return e.txn
}

func (e *Engine) NoteTree(root format.PageID, height uint16) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.currentWriter()
	if t == nil {
		return e.File.SetPrimaryTree(root, height)
	}
	lsn, err := e.WAL.Append(wal.TreeMeta(t.id, t.prev, root, height))
	if err != nil {
		return err
	}
	t.prev = lsn
	return e.File.SetPrimaryTreeMem(root, height)
}

func (e *Engine) LogLogical(typ wal.RecType, key, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.currentWriter()
	if t == nil {
		return nil
	}
	var rec wal.Record
	switch typ {
	case wal.RecInsert:
		rec = wal.LogicalInsert(t.id, t.prev, key, value)
	case wal.RecDelete:
		rec = wal.LogicalDelete(t.id, t.prev, key)
	case wal.RecUpdate:
		rec = wal.LogicalUpdate(t.id, t.prev, key, value)
	default:
		return nerr.New(nerr.InvalidArgument, "storage.LogLogical", "not a logical record type")
	}
	lsn, err := e.WAL.Append(rec)
	if err != nil {
		return err
	}
	t.prev = lsn
	return nil
}

func (e *Engine) ActiveTxn() format.TxnID {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.txn == nil {
		return 0
	}
	return e.txn.id
}

func (e *Engine) Checkpoint() error {
	if e.WAL == nil {
		return e.Buffer.FlushAll()
	}
	if err := e.CrashAt(wal.PointBeforeCheckpoint); err != nil {
		return err
	}
	if err := e.CrashAt(wal.PointDuringPageFlush); err != nil {
		return err
	}
	// The redo boundary is fuzzy: recovery must replay every logged change
	// not yet in the data file, and a checkpoint cannot flush everything --
	// a page a running transaction has dirtied stays in memory (no-steal),
	// together with any committed image of it that predates that write. The
	// boundary used to be the log's end after the flush, which skipped such
	// images: after a crash an acknowledged commit was gone
	// (tests/crash TestStorageCheckpointKeepsCommittedKeyOnDirtiedPage).
	//
	// It is the smallest of: the log's end captured before flushing
	// (anything appended later is at or after it); the first LSN of any
	// image batch appended but not yet stamped onto its frames; and the
	// oldest unflushed change of any frame. The samples are taken in that
	// order so none can be missed: a batch stamped after its unstamped entry
	// is sampled is by then visible to MinRecLSN or already flushed. The
	// pool mutex is never taken under e.mu (AllowFlush takes e.mu under the
	// pool mutex).
	e.mu.Lock()
	start := e.WAL.NextLSN()
	e.mu.Unlock()
	if err := e.Buffer.FlushAll(); err != nil {
		return err
	}
	if err := e.File.Sync(); err != nil {
		return err
	}
	e.mu.Lock()
	unstampedMin := e.minUnstampedLocked()
	// Nor may it pass the Begin record of a transaction still running.
	// Recovery finds uncommitted transactions by scanning for Begin records
	// from the boundary, and an id it never sees defaults to committed. A
	// committed transaction's page image can carry a running transaction's
	// row versions (the page is shared), so a boundary past that Begin let a
	// transaction that never committed come back after a crash as committed
	// -- half of a transfer, with no undo (tests/crash
	// TestRollbacksWithPageDeltasSurvivePowerLoss reproduced it with full
	// images alone).
	var activeFirst format.LSN
	for _, w := range e.writers {
		if w != nil && !w.done && w.first != 0 && (activeFirst == 0 || w.first < activeFirst) {
			activeFirst = w.first
		}
	}
	txnID := format.TxnID(0)
	prev := format.LSN(0)
	if e.txn != nil {
		txnID = e.txn.id
		prev = e.txn.prev
	}
	e.mu.Unlock()
	redoFloor := start
	if unstampedMin != 0 && unstampedMin < redoFloor {
		redoFloor = unstampedMin
	}
	if activeFirst != 0 && activeFirst < redoFloor {
		redoFloor = activeFirst
	}
	if rec := e.Buffer.MinRecLSN(); rec != 0 && rec < redoFloor {
		redoFloor = rec
	}

	// Nothing has happened on this Engine instance since it was opened (no
	// transaction touched it, and — since a checkpoint call is the only
	// other thing that appends WAL records outside a transaction — nothing
	// else advanced NextLSN either): writing a fresh checkpoint record here
	// would be redundant, and worse, would consume WAL LSN numbers purely as
	// local housekeeping. That matters beyond wasted space: a caller that
	// opens a data file only to produce a consistent snapshot for backup
	// (internal/backup.Create/Restore, which do not otherwise touch this
	// engine at all) must not perturb its LSN numbering — on a replica,
	// that numbering is also what ApplyReplicated uses to know how far the
	// replicated stream has already been caught up to, and an LSN advance
	// with no corresponding applied data would make it silently skip real,
	// not-yet-applied writes replayed into it later (found via an
	// independent audit, TODO.md log #95). A normal Close() or any session
	// that actually wrote something always has txnID != 0 at some point or
	// has already advanced NextLSN past openNextLSN, so this only ever
	// short-circuits a checkpoint that would have had nothing to record.
	if txnID == 0 && e.WAL.NextLSN() == e.openNextLSN {
		return nil
	}

	e.mu.Lock()
	e.pageLog.raiseFloor(redoFloor)
	e.mu.Unlock()
	root, height := e.File.PrimaryTree()
	next, head, count := e.File.AllocState()
	redo := redoFloor
	body := wal.CheckpointBody{
		RedoLSN:    redo,
		DurableLSN: e.WAL.DurableLSN(),
		NextPageID: next,
		FreeHead:   head,
		FreeCount:  count,
		Root:       root,
		Height:     height,
	}
	if txnID == 0 {
		txnID = e.WAL.AllocTxn()
		lsn, err := e.WAL.Append(wal.BeginRec(txnID))
		if err != nil {
			return err
		}
		prev = lsn
	}
	// Allocator freelist pages are written outside the buffer pool. Include
	// their current logical images in the checkpoint transaction so a backup
	// plus archived WAL can reconstruct a checkpoint whose FreeHead points at
	// metadata created after the base backup.
	allocState := e.Alloc.State()
	if len(allocState.Metadata) > 0 {
		images := make([][]byte, len(allocState.Metadata))
		for i, id := range allocState.Metadata {
			image, err := e.File.ReadLogical(id)
			if err != nil {
				return err
			}
			images[i] = image
		}
		_, last, err := e.WAL.AppendPageImages(txnID, prev, allocState.Metadata, images)
		if err != nil {
			return err
		}
		prev = last
	}
	lsn, err := e.WAL.Append(wal.CheckpointRec(txnID, prev, body))
	if err != nil {
		return err
	}
	if e.ActiveTxn() == 0 {
		if _, err := e.WAL.Append(wal.CommitRec(txnID, lsn)); err != nil {
			return err
		}
	}
	if err := e.WAL.Flush(e.WAL.NextLSN() - 1); err != nil {
		return err
	}
	if err := e.WAL.InstallCheckpoint(lsn, redo); err != nil {
		return err
	}
	if err := e.File.SetCheckpoint(lsn, redo); err != nil {
		return err
	}
	return e.WAL.Recycle()
}

func (e *Engine) hitLocked(p wal.Point) error {
	if e.crash == nil {
		return nil
	}
	return e.crash.Hit(p)
}

// OnPin is the buffer pool's per-pin hook. It does nothing.
//
// It used to copy the whole 16 KiB page into the pinning transaction's
// before-image map on first pin, under the engine mutex, while the buffer
// pool held its own mutex -- for every pin by any goroutine whenever some
// transaction was current, including pure reads attributed to an unrelated
// writer. Nothing has read those before-images since rollback moved to the
// durable logical undo chain, so the copy was pure cost: measured as the
// largest serialization point for concurrent scans, together with the pool
// mutex it was taken under.
func (e *Engine) OnPin(id format.PageID, data []byte) {}

func (e *Engine) OnInstall(id format.PageID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.opTxn
	if t == nil {
		t = e.txn
	}
	if t == nil {
		return
	}
	t.created[id] = struct{}{}
}

func (e *Engine) OnDirty(id format.PageID, data []byte) (format.LSN, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.WAL == nil {
		return 0, nil
	}
	t := e.opTxn
	if t == nil {
		t = e.txn
	}
	if t != nil && t.durableWait {
		// Its page images are already appended and it is only waiting for
		// them to become durable; a page attributed to it now would never be
		// logged. Treat the write as unowned.
		t = nil
	}
	auto := false
	if t == nil {
		var err error
		t, err = e.beginLocked(true)
		if err != nil {
			return 0, err
		}
		auto = true
	}
	if _, ok := t.dirty[id]; !ok {
		_, created := t.created[id]
		t.dirty[id] = &undoPage{created: created}
	}
	if e.pageWriter {
		if _, ok := e.opDirtySet[id]; !ok {
			e.opDirtySet[id] = struct{}{}
			e.opDirty = append(e.opDirty, id)
		}
	}
	// Redo is physical page images. One image per dirty page is enough:
	// recovery applies the last committed image. Logging every pin/release
	// would write a full page per row and blow the WAL on bulk load.
	if auto {
		if err := e.commitLocked(t, e.pageWriter); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

// flushDirtyImages writes the final committed image of each dirty page.
// Called with e.mu held. Buffer operations drop the engine lock so they
// cannot deadlock with Pin → OnPin (pool.mu then e.mu).
// appendPageStatesLocked logs the state of each page -- a RecPageDelta
// against its cached base where one is safe (see pagelog.go), otherwise a full
// image -- and records the logged states as the pages' new bases, unsettled
// until settle. images[i] must be a private copy of page ids[i]; each is
// stamped with its record's LSN and retained as the base. It returns each
// page's LSN, the last LSN appended and the first. Called with e.mu held.
func (e *Engine) appendPageStatesLocked(txnID format.TxnID, prev format.LSN, ids []format.PageID, images [][]byte, settled bool) (lsns []format.LSN, last, first format.LSN, err error) {
	// An image carries every row version on its page, including other
	// transactions' uncommitted ones, and only their undo records hold the
	// values those versions replaced. Every such record was appended before
	// its page was changed (btree logs undo first), so before the image was
	// copied; write them out before the image enters the log. The commit
	// path's earlier Undo.Flush runs before the copy, leaving a window for
	// another transaction's write, and a split's images had no flush at all:
	// after a crash redo installed the version and undo could not reverse it
	// (tests/crash TestPageImageDoesNotOutrunTheUndoItCarries).
	if e.Undo != nil {
		if err := e.Undo.Flush(); err != nil {
			return nil, 0, 0, err
		}
	}
	lsns = make([]format.LSN, len(ids))
	var fullIDs []format.PageID
	var fullImages [][]byte
	var fullAt []int
	for i, id := range ids {
		if b := e.pageLog.base(id, images[i]); b != nil {
			if body, ok := wal.EncodePageDelta(b.lsn, b.content, images[i], maxPageDeltaBody); ok {
				lsn, aerr := e.WAL.Append(wal.PageDeltaRec(txnID, prev, id, body))
				if aerr != nil {
					return nil, 0, 0, aerr
				}
				encoding.PutU64(images[i], wal.PageLSNOffset, uint64(lsn))
				lsns[i], prev = lsn, lsn
				if first == 0 {
					first = lsn
				}
				continue
			}
		}
		fullIDs = append(fullIDs, id)
		fullImages = append(fullImages, images[i])
		fullAt = append(fullAt, i)
	}
	if len(fullIDs) > 0 {
		got, lastFull, aerr := e.WAL.AppendPageImages(txnID, prev, fullIDs, fullImages)
		if aerr != nil {
			return nil, 0, 0, aerr
		}
		for k, i := range fullAt {
			lsns[i] = got[k]
		}
		if first == 0 {
			first = got[0]
		}
		prev = lastFull
	}
	for i, id := range ids {
		e.pageLog.put(id, lsns[i], txnID, settled, images[i])
	}
	return lsns, prev, first, nil
}

func (e *Engine) flushDirtyImages(txn *Txn, pageWriteHeld bool) error {
	if e.WAL == nil || txn == nil || len(txn.dirty) == 0 {
		return nil
	}
	ids := make([]format.PageID, 0, len(txn.dirty))
	for id := range txn.dirty {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	e.mu.Unlock()
	// pageMu stays read-held from the copy until the images are appended.
	// Released in between, another transaction could change a page, copy
	// it and append its newer image first; this older copy then landed at
	// the higher LSN, and recovery, replaying in LSN order, ended on it --
	// committed rows lost (tests/crash TestConcurrentCommitsSurvivePowerLoss,
	// which a slower WAL flush made fail about one round in five).
	// Lock order is pageMu, then mu.
	if !pageWriteHeld {
		e.pageMu.RLock()
	}
	images := make([][]byte, len(ids))
	for i, id := range ids {
		data, ok := e.Buffer.CopyPageInto(id, images[i])
		if !ok {
			if !pageWriteHeld {
				e.pageMu.RUnlock()
			}
			e.mu.Lock()
			return nerr.New(nerr.Internal, "storage.commit", "dirty page missing from buffer")
		}
		images[i] = data
	}
	e.mu.Lock()
	lsns, last, first, err := e.appendPageStatesLocked(txn.id, txn.prev, ids, images, false)
	if !pageWriteHeld {
		e.pageMu.RUnlock()
	}
	if err != nil {
		return err
	}
	txn.prev = last
	txn.loggedPages = append(txn.loggedPages, ids...)
	e.noteUnstampedLocked(first)
	e.mu.Unlock()
	e.stampImages(ids, lsns, first)
	e.mu.Lock()
	return nil
}

func (e *Engine) AllowFlush(id format.PageID, lsn format.LSN) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if lsn == 0 {
		return false
	}
	if _, pending := e.smoPending[id]; pending {
		return false // a structure modification's image is not logged yet
	}
	for _, t := range e.writers {
		if t != nil && !t.done {
			if _, ok := t.dirty[id]; ok {
				return false
			}
		}
	}
	if e.WAL == nil {
		return true
	}
	return lsn <= e.WAL.DurableLSN()
}

func (e *Engine) LogUndo(target UndoTarget, kind undo.Kind, pageID format.PageID, key []byte, old row.Version) (format.UndoID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.opTxn
	if t == nil {
		t = e.txn
	}
	if t == nil || e.Undo == nil {
		return 0, nil
	}
	rec := undo.Record{
		Txn:    t.id,
		Prev:   t.lastUndo,
		Kind:   kind,
		PageID: pageID,
		Key:    append([]byte(nil), key...),
		Old:    old,
	}
	id, err := e.Undo.Append(rec)
	if err != nil {
		return 0, err
	}
	t.lastUndo = id
	t.liveTargets = append(t.liveTargets, target)
	// Fresh inserts are undone from the UNDO file / in-memory chain.
	// A second WAL RecUndo doubles AEAD work and is not required for redo.
	if kind == undo.KindInsert {
		return id, nil
	}
	lsn, err := e.WAL.Append(wal.UndoRec(t.id, t.prev, id, uint8(kind), pageID, key))
	if err != nil {
		return 0, err
	}
	t.prev = lsn
	return id, nil
}

// ApplyReplicated installs quorum-committed WAL records on a replica.
// Already-present LSNs (the originating leader) are a no-op.
func (e *Engine) ApplyReplicated(recs []wal.Record) error {
	if e == nil {
		return nerr.New(nerr.InvalidArgument, "storage.ApplyReplicated", "nil engine")
	}
	if len(recs) == 0 {
		return nil
	}
	last := recs[len(recs)-1].LSN
	if last < e.WAL.NextLSN() {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.WAL.InstallRecords(recs); err != nil {
		return err
	}
	committed := make(map[format.TxnID]struct{})
	var maxTxn format.TxnID
	for _, r := range recs {
		if r.TxnID > maxTxn {
			maxTxn = r.TxnID
		}
		if r.Type == wal.RecCommit {
			committed[r.TxnID] = struct{}{}
		}
	}
	var (
		root, height        = e.File.PrimaryTree()
		next, head, count   = e.File.AllocState()
		haveTree, haveAlloc bool
	)
	for _, r := range recs {
		if r.Type == wal.RecCheckpoint {
			body, err := wal.DecodeCheckpoint(r.Body)
			if err != nil {
				return err
			}
			if body.Root != 0 || body.Height != 0 {
				root, height = body.Root, body.Height
				haveTree = true
			}
			if body.NextPageID != 0 {
				next, head, count = body.NextPageID, body.FreeHead, body.FreeCount
				haveAlloc = true
			}
			continue
		}
		if _, ok := committed[r.TxnID]; !ok {
			continue
		}
		switch r.Type {
		case wal.RecPageImage:
			if r.PageID == 0 || len(r.Body) != format.LogicalPageSize {
				return nerr.New(nerr.Corruption, "storage.ApplyReplicated", "invalid page image")
			}
			if err := e.Buffer.Replace(r.PageID, r.Body); err != nil {
				return err
			}
		case wal.RecPageDelta:
			if err := e.applyReplicatedDelta(r); err != nil {
				return err
			}
		case wal.RecTreeMeta:
			nr, nh, err := wal.DecodeTreeMeta(r.Body)
			if err != nil {
				return err
			}
			root, height = nr, nh
			haveTree = true
		case wal.RecAllocState:
			n, h, c, err := wal.DecodeAllocState(r.Body)
			if err != nil {
				return err
			}
			next, head, count = n, h, c
			haveAlloc = true
		}
	}
	if haveTree {
		if err := e.File.SetPrimaryTree(root, height); err != nil {
			return err
		}
	}
	if haveAlloc {
		if err := e.File.SetAllocState(next, head, count); err != nil {
			return err
		}
		if err := e.Alloc.Reload(); err != nil {
			return err
		}
	}
	e.WAL.AdvanceAfterRecovery(last+1, maxTxn+1)
	if e.TM != nil {
		ids := make([]format.TxnID, 0, len(committed))
		for id := range committed {
			ids = append(ids, id)
		}
		e.TM.Recover(e.WAL.NextTxn(), ids, nil)
	}
	if last > e.replLSN {
		e.replLSN = last
	}
	return nil
}

// applyReplicatedDelta applies a RecPageDelta on a replica, with the same rule
// recovery uses: a page already at or past the delta is left alone, and
// otherwise it must be exactly the delta's base. The replica's current copy is
// the buffer frame when the page is cached (it may be newer than the file),
// else the data file.
func (e *Engine) applyReplicatedDelta(r wal.Record) error {
	if r.PageID == 0 {
		return nerr.New(nerr.Corruption, "storage.ApplyReplicated", "page delta missing page id")
	}
	d, err := wal.DecodePageDelta(r.Body)
	if err != nil {
		return nerr.Wrap(nerr.Corruption, "storage.ApplyReplicated", "undecodable page delta", err)
	}
	cur, ok := e.Buffer.CopyPageInto(r.PageID, nil)
	if !ok {
		cur, err = e.File.ReadLogical(r.PageID)
		if err != nil {
			return nerr.Wrap(nerr.Corruption, "storage.ApplyReplicated", fmt.Sprintf("page %d is unreadable for the delta at LSN %d", r.PageID, r.LSN), err)
		}
	}
	if page.LSNOf(cur) >= r.LSN {
		return nil
	}
	if err := wal.ApplyPageDelta(cur, d, r.LSN); err != nil {
		return nerr.Wrap(nerr.Corruption, "storage.ApplyReplicated", fmt.Sprintf(
			"page %d is at LSN %d; the delta at LSN %d needs base LSN %d", r.PageID, page.LSNOf(cur), r.LSN, d.BaseLSN), err)
	}
	return e.Buffer.Replace(r.PageID, cur)
}
