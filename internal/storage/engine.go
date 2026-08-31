package storage

import (
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
	// pageMu separates page mutation from commit-time physical snapshots.
	// Buffer-pool pins alias frame bytes, so pool.mu cannot protect a copy
	// from a writer that already holds a Handle.
	pageMu     sync.RWMutex
	txn        *Txn
	writers    map[format.TxnID]*Txn
	opTxn      *Txn
	pageWriter bool
	crash      *wal.Injector
	closed     bool

	repl    Replicator
	replMu  sync.Mutex
	replLSN format.LSN

	iso *integrity.Registry
}

// Replicator is the quorum-commit hook (Phase 15). Commit is not
// acknowledged until Replicate returns. A nil Replicator is single-node.
type Replicator interface {
	Replicate(recs []wal.Record) error
}

// Txn is a write transaction. WAL records, UNDO, and page dirtiness are
// tracked here; visibility and locking live in the txn manager.
type Txn struct {
	eng          *Engine
	id           format.TxnID
	prev         format.LSN
	first        format.LSN
	lastUndo     format.UndoID
	dirty        map[format.PageID]*undoPage
	snap         map[format.PageID][]byte
	created      map[format.PageID]struct{}
	changes      []wal.Change
	changeBytes  int
	changeBroken bool
	done         bool
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
	before  []byte
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

func Open(path string, keys crypto.KeyProvider, bufferPages int) (*Engine, error) {
	return OpenWith(path, keys, bufferPages, OpenOptions{})
}

// OpenOptions tunes recovery. Zero values preserve Open() behavior.
type OpenOptions struct {
	UntilLSN format.LSN
	Archiver wal.Archiver
}

// OpenWith opens an existing database and optionally stops redo at UntilLSN.
func OpenWith(path string, keys crypto.KeyProvider, bufferPages int, opt OpenOptions) (*Engine, error) {
	return open(path, keys, bufferPages, format.Identity{}, false, opt)
}

func open(path string, keys crypto.KeyProvider, bufferPages int, id format.Identity, create bool, opt OpenOptions) (*Engine, error) {
	if bufferPages < 1 {
		return nil, nerr.New(nerr.InvalidArgument, "storage.open", "buffer_pages must be >= 1")
	}
	var (
		fm  *file.Manager
		err error
	)
	if create {
		fm, err = file.Create(path, id, keys)
	} else {
		fm, err = file.Open(path, keys)
	}
	if err != nil {
		return nil, err
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
	wdir := wal.DirFor(path)
	udir := undo.DirFor(path)
	var lg *wal.Log
	walOpt := wal.Options{Archiver: opt.Archiver}
	if create {
		lg, err = wal.Create(wdir, keys, ident, walOpt)
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
	if !create {
		uncommitted, uerr := recovery.UncommittedUntil(lg, opt.UntilLSN)
		if uerr != nil {
			_ = ul.Close()
			_ = lg.Close()
			_ = fm.Close()
			return nil, uerr
		}
		if err := undo.Apply(fm, ul, uncommitted); err != nil {
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
	pool, err := buffer.New(fm, bufferPages)
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
	e := &Engine{
		File:    fm,
		Alloc:   alloc,
		Buffer:  pool,
		WAL:     lg,
		Undo:    ul,
		TM:      txn.NewManager(lg.NextTxn()),
		writers: make(map[format.TxnID]*Txn),
		iso:     iso,
	}
	if !create {
		uncommitted, uerr := recovery.UncommittedUntil(lg, opt.UntilLSN)
		if uerr == nil {
			e.TM.Recover(lg.NextTxn(), nil, uncommitted)
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
	if e.txn != nil {
		delete(e.txn.dirty, id)
		delete(e.txn.snap, id)
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
	e.mu.Unlock()
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
		snap:    make(map[format.PageID][]byte),
		created: make(map[format.PageID]struct{}),
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
	e.opTxn = t
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
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.commitLocked(t, false)
	}
	e.replMu.Lock()
	defer e.replMu.Unlock()
	e.mu.Lock()
	err := e.commitLocked(t, false)
	recs, rerr := e.takeReplLocked()
	repl := e.repl
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if rerr != nil {
		return rerr
	}
	if repl != nil && len(recs) > 0 {
		return repl.Replicate(recs)
	}
	return nil
}

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
	if txn.changeBroken {
		return nerr.New(nerr.Conflict, "storage.CommitTxn", "transaction change stream is incomplete; rollback required")
	}
	if e.Undo != nil {
		if err := e.Undo.Flush(); err != nil {
			return err
		}
	}
	if err := e.flushDirtyImages(txn, pageWriteHeld); err != nil {
		return err
	}
	if err := e.Alloc.Flush(); err != nil {
		return err
	}
	if err := e.hitLocked(wal.PointBeforeCommitRecord); err != nil {
		return err
	}
	next, head, count := e.File.AllocState()
	lsn, err := e.WAL.Append(wal.AllocState(txn.id, txn.prev, next, head, count))
	if err != nil {
		return err
	}
	txn.prev = lsn
	for _, change := range txn.changes {
		rec, err := wal.ChangeRec(txn.id, txn.prev, change)
		if err != nil {
			return err
		}
		lsn, err = e.WAL.Append(rec)
		if err != nil {
			return err
		}
		txn.prev = lsn
	}
	lsn, err = e.WAL.Append(wal.CommitRec(txn.id, txn.prev))
	if err != nil {
		return err
	}
	txn.prev = lsn
	if err := e.hitLocked(wal.PointAfterCommitRecordBeforeSync); err != nil {
		return err
	}
	if err := e.WAL.Flush(lsn); err != nil {
		return err
	}
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
	return nil
}

func (e *Engine) Rollback() error {
	e.mu.Lock()
	t := e.txn
	e.mu.Unlock()
	return e.RollbackTxn(t)
}

func (e *Engine) RollbackTxn(txn *Txn) error {
	if err := e.CrashAt(wal.PointBeforeRollback); err != nil {
		return err
	}
	if txn == nil {
		return nil
	}
	e.mu.Lock()
	if e.txn == txn {
		e.txn = nil
	}
	if e.opTxn == txn {
		e.opTxn = nil
	}
	delete(e.writers, txn.id)
	others := make([]*Txn, 0, len(e.writers))
	for _, o := range e.writers {
		others = append(others, o)
	}
	e.mu.Unlock()

	for id, u := range txn.dirty {
		shared := false
		for _, o := range others {
			if _, ok := o.dirty[id]; ok {
				shared = true
				break
			}
		}
		if shared {
			continue
		}
		if u.created {
			_ = e.Buffer.Drop(id)
			_ = e.Alloc.Free(id)
			continue
		}
		if u.before != nil {
			_ = e.Buffer.Restore(id, u.before)
		}
	}
	for id := range txn.created {
		if _, ok := txn.dirty[id]; ok {
			continue
		}
		shared := false
		for _, o := range others {
			if _, ok := o.dirty[id]; ok {
				shared = true
				break
			}
			if _, ok := o.created[id]; ok {
				shared = true
				break
			}
		}
		if shared {
			continue
		}
		_ = e.Buffer.Drop(id)
		_ = e.Alloc.Free(id)
	}
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
	if err := e.Buffer.FlushAll(); err != nil {
		return err
	}
	if err := e.File.Sync(); err != nil {
		return err
	}
	e.mu.Lock()
	txnID := format.TxnID(0)
	prev := format.LSN(0)
	if e.txn != nil {
		txnID = e.txn.id
		prev = e.txn.prev
	}
	e.mu.Unlock()

	root, height := e.File.PrimaryTree()
	next, head, count := e.File.AllocState()
	redo := e.WAL.NextLSN()
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

func (e *Engine) OnPin(id format.PageID, data []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.opTxn
	if t == nil {
		t = e.txn
	}
	if t == nil {
		return
	}
	if _, ok := t.snap[id]; ok {
		return
	}
	if _, ok := t.dirty[id]; ok {
		return
	}
	if _, ok := t.created[id]; ok {
		return
	}
	t.snap[id] = append([]byte(nil), data...)
}

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
		t.dirty[id] = &undoPage{before: t.snap[id], created: created}
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
	if !pageWriteHeld {
		e.pageMu.RUnlock()
	}
	e.mu.Lock()
	lsns, last, err := e.WAL.AppendPageImages(txn.id, txn.prev, ids, images)
	if err != nil {
		return err
	}
	txn.prev = last
	e.mu.Unlock()
	for i, id := range ids {
		e.Buffer.StampLSN(id, lsns[i])
	}
	e.mu.Lock()
	return nil
}

func (e *Engine) AllowFlush(id format.PageID, lsn format.LSN) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if lsn == 0 {
		return false
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

func (e *Engine) LogUndo(kind undo.Kind, pageID format.PageID, key []byte, old row.Version) (format.UndoID, error) {
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
