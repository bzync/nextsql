package btree

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/row"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/undo"
	"github.com/bzync/nextsql/internal/wal"
)

// Txn is a user transaction with isolation, locks, and MVCC visibility.
type Txn struct {
	tree      *Tree
	wal       *storage.Txn
	h         *txn.Handle
	done      bool
	deleted   [][]byte
	snapR     format.PageID
	snapH     int
	snapLive  int64
	snapKnown bool
}

func (t *Tree) BeginTxn(iso txn.Isolation) (*Txn, error) {
	if t == nil {
		return nil, nerr.New(nerr.InvalidArgument, "btree.BeginTxn", "nil tree")
	}
	if iso == 0 {
		iso = txn.SnapshotIsolation
	}
	wt, err := t.eng.StartTxn()
	if err != nil {
		return nil, err
	}
	var h *txn.Handle
	if t.eng.TM != nil {
		h = t.eng.TM.Handle(wt.ID())
		if h != nil {
			h.Iso = iso
			t.eng.TM.Refresh(h)
		}
	}
	t.mu.RLock()
	snapR, snapH, snapL, snapK := t.root, t.height, t.liveRows, t.liveKnown
	t.mu.RUnlock()
	return &Txn{tree: t, wal: wt, h: h, snapR: snapR, snapH: snapH, snapLive: snapL, snapKnown: snapK}, nil
}

// BeginRead is a snapshot that does not write WAL records. Followers use
// this for SELECT so they do not diverge from the replicated LSN stream.
func (t *Tree) BeginRead(iso txn.Isolation) (*Txn, error) {
	if t == nil {
		return nil, nerr.New(nerr.InvalidArgument, "btree.BeginRead", "nil tree")
	}
	if iso == 0 {
		iso = txn.SnapshotIsolation
	}
	var h *txn.Handle
	if t.eng.TM != nil {
		h = t.eng.TM.BeginRead(iso)
	}
	t.mu.RLock()
	snapR, snapH, snapL, snapK := t.root, t.height, t.liveRows, t.liveKnown
	t.mu.RUnlock()
	return &Txn{tree: t, wal: nil, h: h, snapR: snapR, snapH: snapH, snapLive: snapL, snapKnown: snapK}, nil
}

// Attach binds this tree to an existing engine transaction (multi-tree SQL txn).
func (t *Tree) Attach(wal *storage.Txn, h *txn.Handle) *Txn {
	t.mu.RLock()
	snapR, snapH, snapL, snapK := t.root, t.height, t.liveRows, t.liveKnown
	t.mu.RUnlock()
	return &Txn{tree: t, wal: wal, h: h, snapR: snapR, snapH: snapH, snapLive: snapL, snapKnown: snapK}
}

func (tx *Txn) Storage() *storage.Txn {
	if tx == nil {
		return nil
	}
	return tx.wal
}

func (tx *Txn) Handle() *txn.Handle {
	if tx == nil {
		return nil
	}
	return tx.h
}

func (tx *Txn) Tree() *Tree {
	if tx == nil {
		return nil
	}
	return tx.tree
}

// PersistMeta writes root/height without committing the WAL transaction.
func (tx *Txn) PersistMeta() error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.PersistMeta", "transaction is not active")
	}
	if tx.wal == nil {
		return nil
	}
	tx.tree.mu.Lock()
	tx.tree.eng.Enter(tx.wal)
	err := tx.tree.persist()
	tx.tree.eng.Leave(tx.wal)
	tx.tree.mu.Unlock()
	return err
}

// RestoreSnap invalidates this tree's cached live-row count on rollback. It
// deliberately does NOT reset root/height back to Begin/Attach: a page split
// this transaction triggered is a physical structure change that took
// effect immediately and may already be relied on by another transaction's
// own routing (see the doc comment on storage.Engine.undoTxnLogical) — it is
// never reverted by rollback, so root/height must be left exactly as they
// are. liveKnown is invalidated rather than restored to the pre-txn
// snapshot because other, still-committing transactions may have changed
// the row count in the meantime too; it is a cache, so forcing a recount is
// simplest and always safe.
func (tx *Txn) RestoreSnap() {
	if tx == nil || tx.tree == nil {
		return
	}
	tx.tree.mu.Lock()
	tx.tree.liveKnown = false
	tx.tree.mu.Unlock()
}

// WasEmpty reports whether this tree had no root at all when this
// transaction attached to it (BeginTxn/Attach) — i.e. the tree is entirely
// new to this transaction, such as a fresh CREATE TABLE/INDEX heap or a
// standalone detached tree. Only in that case can every page the tree ends
// up owning be safely reclaimed if the transaction rolls back: nothing
// outside this transaction could ever have referenced a page of a tree that
// did not exist yet, so unlike an ordinary pre-existing tree there is no
// concurrent-writer or structural-sharing hazard to worry about.
func (tx *Txn) WasEmpty() bool {
	return tx != nil && tx.snapR == 0
}

func (tx *Txn) MarkDone() {
	if tx != nil {
		tx.done = true
	}
}

func (tx *Txn) PurgeDeleted() error {
	if tx == nil || tx.tree == nil || tx.wal == nil {
		return nil
	}
	// CommitTxn has already removed this writer from the transaction manager.
	// Any remaining live handle, including a read-only snapshot, may still need
	// the tombstone's UNDO chain to reconstruct the deleted row.
	if tx.tree.eng.TM != nil && tx.tree.eng.TM.LiveSnapshots() != 0 {
		return nil
	}
	// Physical removal happens after the logical delete transaction is durable.
	// Commit bounded groups so bulk SQL deletes do not turn one durable commit
	// into one additional fdatasync per row. A crash between groups is safe:
	// committed tombstones left behind are rediscovered by maintenance.
	const purgeBatchSize = 256
	for start := 0; start < len(tx.deleted); start += purgeBatchSize {
		if tx.tree.eng.TM != nil && tx.tree.eng.TM.LiveSnapshots() != 0 {
			return nil
		}
		end := start + purgeBatchSize
		if end > len(tx.deleted) {
			end = len(tx.deleted)
		}
		if err := tx.tree.purgeCommittedBatch(tx.deleted[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (tx *Txn) ID() (id uint64) {
	if tx == nil || tx.wal == nil {
		return 0
	}
	return uint64(tx.wal.ID())
}

func (tx *Txn) snap() txn.Snapshot {
	if tx.h != nil {
		return tx.h.Snap
	}
	return tx.tree.readSnapshot()
}

func (tx *Txn) refreshIfRC() {
	if tx.h != nil && tx.h.Iso == txn.ReadCommitted && tx.tree.eng.TM != nil {
		tx.tree.eng.TM.Refresh(tx.h)
	}
}

func (tx *Txn) soleWriter() bool {
	if tx == nil || tx.tree == nil || tx.tree.eng == nil || tx.tree.eng.TM == nil {
		return true
	}
	if tx.h != nil && tx.h.Iso >= txn.Serializable {
		return false
	}
	return tx.tree.eng.TM.LiveSnapshots() <= 1
}

func (tx *Txn) lockWrite(key []byte) error {
	if tx.tree.eng.TM == nil || tx.h == nil {
		return nil
	}
	if tx.h.Iso < txn.Serializable && tx.tree.eng.TM.ActiveCount() <= 1 && !tx.tree.eng.TM.OnlineBuildActive() {
		return nil
	}
	return tx.tree.eng.TM.LockKey(tx.h, key, txn.Exclusive, tx.tree.Name())
}

// LockExclusive always takes an exclusive key lock so concurrent UPSERT
// on the same unique key is serialized. Unlike Insert/Update, it does
// not skip locking when only one transaction is visible yet.
func (tx *Txn) LockExclusive(key []byte) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.LockExclusive", "transaction is not active")
	}
	if err := checkKey(key); err != nil {
		return err
	}
	if tx.tree.eng.TM == nil || tx.h == nil {
		return nil
	}
	return tx.tree.eng.TM.LockKey(tx.h, key, txn.Exclusive, tx.tree.Name())
}

func (tx *Txn) lockRead(key []byte) error {
	if tx.tree.eng.TM == nil || tx.h == nil || tx.h.Iso != txn.Serializable {
		return nil
	}
	return tx.tree.eng.TM.LockKey(tx.h, key, txn.Shared, tx.tree.Name())
}

func (tx *Txn) lockRange(start, end []byte) error {
	if tx.tree.eng.TM == nil || tx.h == nil || tx.h.Iso != txn.Serializable {
		return nil
	}
	return tx.tree.eng.TM.LockRange(tx.h, start, end, txn.Shared, tx.tree.Name())
}

func (tx *Txn) withWrite(fn func() error) error {
	tx.tree.mu.Lock()
	tx.tree.eng.Enter(tx.wal)
	err := fn()
	tx.tree.eng.Leave(tx.wal)
	tx.tree.mu.Unlock()
	return err
}

func (tx *Txn) Insert(key, value []byte) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.Insert", "transaction is not active")
	}
	if _, err := encodeLeaf(key, value); err != nil {
		return err
	}
	tx.refreshIfRC()
	return tx.insertAt(key, value, tx.snap())
}

// InsertAt is Insert using snap for visibility and write-write. It does
// not refresh RC, so FK cascade can maintain indexes of a row committed
// after BEGIN SNAPSHOT without mutating h.Snap.
func (tx *Txn) InsertAt(key, value []byte, snap txn.Snapshot) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.InsertAt", "transaction is not active")
	}
	if _, err := encodeLeaf(key, value); err != nil {
		return err
	}
	return tx.insertAt(key, value, snap)
}

func (tx *Txn) insertAt(key, value []byte, snap txn.Snapshot) error {
	key, value = copyBytes(key), copyBytes(value)
	if err := tx.lockWrite(key); err != nil {
		return err
	}
	return tx.withWrite(func() error {
		if err := tx.tree.eng.CrashAt(wal.PointDuringInsert); err != nil {
			return err
		}
		leafID, raw, found, err := tx.tree.leafRaw(key)
		if err != nil {
			return err
		}
		if found {
			ver, has, err := decodeVersion(raw)
			if err != nil {
				return err
			}
			_, vis, err := tx.tree.visiblePayload(raw, snap)
			if err != nil {
				return err
			}
			if vis {
				return nerr.New(nerr.AlreadyExists, "btree.Txn.Insert", "duplicate key")
			}
			if tx.h != nil && tx.h.Iso >= txn.SnapshotIsolation && has {
				if ver.Xmin != tx.wal.ID() && !snap.Sees(ver.Xmin, tx.tree.eng.TM.Status) &&
					tx.tree.eng.TM.Status(ver.Xmin) == txn.StatusCommitted {
					return nerr.New(nerr.Serialization, "btree.Txn.Insert", "write-write conflict")
				}
			}
			old := ver
			// When a real prior version occupies the slot (a tombstone, or a
			// row invisible to this snapshot), inserting over it is an UPDATE of
			// that slot, not a fresh insert: the prior version — and the undo
			// chain hanging off it back to the last committed value — must be
			// restorable on rollback. Logging KindInsert here would make undo
			// (and visiblePayload's chain walk) treat the key as never having
			// existed, silently dropping a committed value when this txn aborts
			// after a delete+reinsert of the same key (e.g. an UPDATE that does
			// not change an indexed column).
			undoKind := undo.KindInsert
			if has {
				undoKind = undo.KindUpdate
			} else {
				old = row.Version{Payload: raw}
			}
			uid, err := tx.tree.eng.LogUndo(tx.tree, undoKind, leafID, key, old)
			if err != nil {
				return err
			}
			neu := row.Encode(row.Version{Xmin: tx.wal.ID(), Undo: uid, Payload: value})
			if err := tx.tree.eng.LogLogical(wal.RecInsert, key, value); err != nil {
				return err
			}
			if err := tx.tree.updateLocked(key, neu); err != nil {
				return err
			}
			tx.tree.addLive(1)
			return nil
		}
		uid, err := tx.tree.eng.LogUndo(tx.tree, undo.KindInsert, leafID, key, row.Version{})
		if err != nil {
			return err
		}
		neu := row.Encode(row.Version{Xmin: tx.wal.ID(), Undo: uid, Payload: value})
		if err := tx.tree.eng.LogLogical(wal.RecInsert, key, value); err != nil {
			return err
		}
		if err := tx.tree.insertOnLeaf(leafID, key, neu); err != nil {
			return err
		}
		tx.tree.addLive(1)
		return nil
	})
}

// InsertBatch inserts keys in one tree lock. Sequential keys reuse a pinned leaf.
// Sole-writer bulk load skips per-row UNDO and logical WAL; commit still writes
// encrypted page images and fsyncs.
func (tx *Txn) InsertBatch(keys, values [][]byte) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.InsertBatch", "transaction is not active")
	}
	if len(keys) != len(values) {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.InsertBatch", "keys/values length")
	}
	if len(keys) == 0 {
		return nil
	}
	if !tx.soleWriter() {
		for _, k := range keys {
			if err := tx.lockWrite(k); err != nil {
				return err
			}
		}
	}
	return tx.withWrite(func() error {
		return tx.tree.insertBatchLocked(tx.wal.ID(), keys, values)
	})
}

// PatchVisible rewrites up to limit visible rows after `after` (exclusive).
func (tx *Txn) PatchVisible(after []byte, limit int, fn func(key, payload []byte) ([]byte, error)) (last []byte, n int, err error) {
	if tx == nil || tx.done {
		return nil, 0, nerr.New(nerr.InvalidArgument, "btree.Txn.PatchVisible", "transaction is not active")
	}
	if limit < 1 || fn == nil {
		return nil, 0, nerr.New(nerr.InvalidArgument, "btree.Txn.PatchVisible", "bad limit or fn")
	}
	err = tx.withWrite(func() error {
		var e error
		last, n, e = tx.tree.patchVisibleLocked(after, limit, tx.snap(), tx.wal.ID(), fn)
		return e
	})
	return last, n, err
}

func (tx *Txn) Update(key, value []byte) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.Update", "transaction is not active")
	}
	if _, err := encodeLeaf(key, value); err != nil {
		return err
	}
	tx.refreshIfRC()
	_, err := tx.updateAt(key, value, tx.snap())
	return err
}

// UpdateAt is Update using snap for visibility and write-write. It does
// not refresh RC, so FK cascade can rewrite a row committed after BEGIN
// SNAPSHOT without mutating h.Snap.
func (tx *Txn) UpdateAt(key, value []byte, snap txn.Snapshot) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.UpdateAt", "transaction is not active")
	}
	if _, err := encodeLeaf(key, value); err != nil {
		return err
	}
	_, err := tx.updateAt(key, value, snap)
	return err
}

// UpdateAtReturningOld is UpdateAt, additionally returning the payload
// actually found and overwritten — see UpdateReturningOld's doc comment.
func (tx *Txn) UpdateAtReturningOld(key, value []byte, snap txn.Snapshot) ([]byte, error) {
	if tx == nil || tx.done {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Txn.UpdateAtReturningOld", "transaction is not active")
	}
	if _, err := encodeLeaf(key, value); err != nil {
		return nil, err
	}
	return tx.updateAt(key, value, snap)
}

// UpdateReturningOld is Update, additionally returning the payload actually
// found and overwritten. Under ReadCommitted, refreshIfRC() takes a fresh
// snapshot for this specific write, which can be newer than whatever
// snapshot a caller used to read the row earlier in the same statement (e.g.
// a scan-then-write UPDATE/DELETE) — with no write-write conflict raised for
// RC, that means the write can silently overwrite a row a *different*,
// concurrently committed transaction already changed since the caller's own
// read. A caller that needs to know the row's true prior value at the exact
// moment of this write (e.g. secondary-index maintenance, which must delete
// the index entry that is actually there, not the one the caller expected)
// must use the value returned here instead of its own earlier read.
func (tx *Txn) UpdateReturningOld(key, value []byte) ([]byte, error) {
	if tx == nil || tx.done {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Txn.UpdateReturningOld", "transaction is not active")
	}
	if _, err := encodeLeaf(key, value); err != nil {
		return nil, err
	}
	tx.refreshIfRC()
	return tx.updateAt(key, value, tx.snap())
}

func (tx *Txn) updateAt(key, value []byte, snap txn.Snapshot) (oldPayload []byte, err error) {
	key, value = copyBytes(key), copyBytes(value)
	if err := tx.lockWrite(key); err != nil {
		return nil, err
	}
	err = tx.withWrite(func() error {
		if err := tx.tree.eng.CrashAt(wal.PointDuringUpdate); err != nil {
			return err
		}
		leafID, raw, found, err := tx.tree.leafRaw(key)
		if err != nil {
			return err
		}
		if !found {
			return nerr.New(nerr.NotFound, "btree.Txn.Update", "key not found")
		}
		payload, vis, err := tx.tree.visiblePayload(raw, snap)
		if err != nil {
			return err
		}
		if !vis {
			return nerr.New(nerr.NotFound, "btree.Txn.Update", "key not found")
		}
		ver, has, err := decodeVersion(raw)
		if err != nil {
			return err
		}
		if !has {
			ver = row.Version{Payload: payload}
		}
		if tx.h != nil && tx.h.Iso >= txn.SnapshotIsolation && has {
			if ver.Xmin != tx.wal.ID() && !snap.Sees(ver.Xmin, tx.tree.eng.TM.Status) &&
				tx.tree.eng.TM.Status(ver.Xmin) == txn.StatusCommitted {
				return nerr.New(nerr.Serialization, "btree.Txn.Update", "write-write conflict")
			}
		}
		uid, err := tx.tree.eng.LogUndo(tx.tree, undo.KindUpdate, leafID, key, ver)
		if err != nil {
			return err
		}
		neu := row.Encode(row.Version{Xmin: tx.wal.ID(), Undo: uid, Payload: value})
		if err := tx.tree.eng.LogLogical(wal.RecUpdate, key, value); err != nil {
			return err
		}
		if err := tx.tree.updateLocked(key, neu); err != nil {
			return err
		}
		oldPayload = payload
		return nil
	})
	return oldPayload, err
}

func (tx *Txn) Delete(key []byte) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.Delete", "transaction is not active")
	}
	if err := checkKey(key); err != nil {
		return err
	}
	tx.refreshIfRC()
	_, err := tx.deleteAt(key, tx.snap())
	return err
}

// DeleteAt is Delete using snap for visibility and write-write. It does
// not refresh RC; FK cascade uses it so a SNAPSHOT parent delete can
// remove a child committed after begin.
func (tx *Txn) DeleteAt(key []byte, snap txn.Snapshot) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.DeleteAt", "transaction is not active")
	}
	if err := checkKey(key); err != nil {
		return err
	}
	_, err := tx.deleteAt(key, snap)
	return err
}

// DeleteAtReturningOld is DeleteAt, additionally returning the payload
// actually found and removed — see UpdateReturningOld's doc comment.
func (tx *Txn) DeleteAtReturningOld(key []byte, snap txn.Snapshot) ([]byte, error) {
	if tx == nil || tx.done {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Txn.DeleteAtReturningOld", "transaction is not active")
	}
	if err := checkKey(key); err != nil {
		return nil, err
	}
	return tx.deleteAt(key, snap)
}

// DeleteReturningOld is Delete, additionally returning the payload actually
// found and removed — see UpdateReturningOld's doc comment for why a caller
// doing its own secondary-index maintenance needs this instead of a payload
// it read earlier in the same statement.
func (tx *Txn) DeleteReturningOld(key []byte) ([]byte, error) {
	if tx == nil || tx.done {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Txn.DeleteReturningOld", "transaction is not active")
	}
	if err := checkKey(key); err != nil {
		return nil, err
	}
	tx.refreshIfRC()
	return tx.deleteAt(key, tx.snap())
}

func (tx *Txn) deleteAt(key []byte, snap txn.Snapshot) (oldPayload []byte, err error) {
	key = copyBytes(key)
	if err := tx.lockWrite(key); err != nil {
		return nil, err
	}
	err = tx.withWrite(func() error {
		if err := tx.tree.eng.CrashAt(wal.PointDuringDelete); err != nil {
			return err
		}
		leafID, raw, found, err := tx.tree.leafRaw(key)
		if err != nil {
			return err
		}
		if !found {
			return nerr.New(nerr.NotFound, "btree.Txn.Delete", "key not found")
		}
		payload, vis, err := tx.tree.visiblePayload(raw, snap)
		if err != nil {
			return err
		}
		if !vis {
			return nerr.New(nerr.NotFound, "btree.Txn.Delete", "key not found")
		}
		ver, has, err := decodeVersion(raw)
		if err != nil {
			return err
		}
		if !has {
			ver = row.Version{Payload: payload}
		}
		if tx.h != nil && tx.h.Iso >= txn.SnapshotIsolation && has {
			if ver.Xmin != tx.wal.ID() && !snap.Sees(ver.Xmin, tx.tree.eng.TM.Status) &&
				tx.tree.eng.TM.Status(ver.Xmin) == txn.StatusCommitted {
				return nerr.New(nerr.Serialization, "btree.Txn.Delete", "write-write conflict")
			}
		}
		uid, err := tx.tree.eng.LogUndo(tx.tree, undo.KindDelete, leafID, key, ver)
		if err != nil {
			return err
		}
		tomb := row.Encode(row.Version{Xmin: ver.Xmin, Xmax: tx.wal.ID(), Undo: uid, Payload: ver.Payload})
		if ver.Xmin == 0 && !has {
			tomb = row.Encode(row.Version{Xmin: 0, Xmax: tx.wal.ID(), Undo: uid, Payload: payload})
		}
		if err := tx.tree.eng.LogLogical(wal.RecDelete, key, nil); err != nil {
			return err
		}
		tx.deleted = append(tx.deleted, copyBytes(key))
		if err := tx.tree.updateLocked(key, tomb); err != nil {
			return err
		}
		tx.tree.addLive(-1)
		oldPayload = payload
		return nil
	})
	return oldPayload, err
}

func (tx *Txn) Lookup(key []byte) ([]byte, error) {
	if tx == nil || tx.done {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Txn.Lookup", "transaction is not active")
	}
	if err := checkKey(key); err != nil {
		return nil, err
	}
	tx.refreshIfRC()
	if err := tx.lockRead(key); err != nil {
		return nil, err
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	_, raw, found, err := tx.tree.leafRaw(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nerr.New(nerr.NotFound, "btree.Txn.Lookup", "key not found")
	}
	payload, vis, err := tx.tree.visiblePayload(raw, tx.snap())
	if err != nil {
		return nil, err
	}
	if !vis {
		return nil, nerr.New(nerr.NotFound, "btree.Txn.Lookup", "key not found")
	}
	return payload, nil
}

// LookupAt is Lookup with a caller snapshot. It does not refresh RC or
// take read locks, so a probe can see later commits without mutating h.Snap.
func (tx *Txn) LookupAt(key []byte, snap txn.Snapshot) ([]byte, error) {
	if tx == nil || tx.done {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Txn.LookupAt", "transaction is not active")
	}
	if err := checkKey(key); err != nil {
		return nil, err
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	_, raw, found, err := tx.tree.leafRaw(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nerr.New(nerr.NotFound, "btree.Txn.LookupAt", "key not found")
	}
	payload, vis, err := tx.tree.visiblePayload(raw, snap)
	if err != nil {
		return nil, err
	}
	if !vis {
		return nil, nerr.New(nerr.NotFound, "btree.Txn.LookupAt", "key not found")
	}
	return payload, nil
}

func (tx *Txn) Range(start, end []byte, fn func(key, value []byte) error) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.Range", "transaction is not active")
	}
	if fn == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.Range", "nil callback")
	}
	tx.refreshIfRC()
	if err := tx.lockRange(start, end); err != nil {
		return err
	}
	return tx.tree.rangeVisible(start, end, tx.snap(), fn)
}

// RangeAt is Range with a caller snapshot. It does not refresh RC or take
// range locks; visibility uses snap, not h.Snap.
func (tx *Txn) RangeAt(start, end []byte, snap txn.Snapshot, fn func(key, value []byte) error) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.RangeAt", "transaction is not active")
	}
	if fn == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.RangeAt", "nil callback")
	}
	return tx.tree.rangeVisible(start, end, snap, fn)
}

// RangeVisible is Range without taking additional locks. The caller must
// already hold the covering range lock (or be a reader under snapshot).
func (tx *Txn) RangeVisible(start, end []byte, fn func(key, value []byte) error) error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.RangeVisible", "transaction is not active")
	}
	return tx.tree.rangeVisible(start, end, tx.snap(), fn)
}

// Count is Range that only returns the visible row count.
func (tx *Txn) Count(start, end []byte) (int64, error) {
	if tx == nil || tx.done {
		return 0, nerr.New(nerr.InvalidArgument, "btree.Txn.Count", "transaction is not active")
	}
	tx.refreshIfRC()
	if err := tx.lockRange(start, end); err != nil {
		return 0, err
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	return tx.tree.rangeCount(start, end, tx.snap(), false)
}

// CachedLive returns the process-maintained live key count when known.
func (tx *Txn) CachedLive() (int64, bool) {
	if tx == nil || tx.done || tx.tree == nil {
		return 0, false
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	if !tx.tree.liveKnown {
		return 0, false
	}
	return tx.tree.liveRows, true
}

// CountLive counts live slots with no MVCC walk. Safe when this
// transaction is the only snapshot and every row is committed.
func (tx *Txn) CountLive() (int64, error) {
	if tx == nil || tx.done {
		return 0, nerr.New(nerr.InvalidArgument, "btree.Txn.CountLive", "transaction is not active")
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	if tx.tree.liveKnown {
		return tx.tree.liveRows, nil
	}
	return tx.tree.countLiveLocked()
}

// RangeLive walks every live record without a visibility check.
func (tx *Txn) RangeLive(fn func(key, value []byte) error) error {
	if tx == nil || tx.done || fn == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.RangeLive", "bad args")
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	return tx.tree.rangeLiveLocked(fn)
}

// CountVisible is Count without taking additional locks.
func (tx *Txn) CountVisible(start, end []byte) (int64, error) {
	if tx == nil || tx.done {
		return 0, nerr.New(nerr.InvalidArgument, "btree.Txn.CountVisible", "transaction is not active")
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	return tx.tree.rangeCount(start, end, tx.snap(), false)
}

// CountLiveRange counts keys in [start, end) with no visibility walk.
func (tx *Txn) CountLiveRange(start, end []byte) (int64, error) {
	if tx == nil || tx.done {
		return 0, nerr.New(nerr.InvalidArgument, "btree.Txn.CountLiveRange", "transaction is not active")
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	return tx.tree.rangeCount(start, end, tx.snap(), true)
}

// RangeLiveRange walks [start, end) without a visibility check.
func (tx *Txn) RangeLiveRange(start, end []byte, fn func(key, value []byte) error) error {
	if tx == nil || tx.done || fn == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.RangeLiveRange", "bad args")
	}
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	return tx.tree.rangeVisibleSkip(start, end, tx.snap(), fn)
}

// SplitKeys returns interior keys for a parallel range scan under this snapshot.
func (tx *Txn) SplitKeys(n int) ([][]byte, error) {
	if tx == nil || tx.done {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Txn.SplitKeys", "transaction is not active")
	}
	if n <= 1 {
		return nil, nil
	}
	tx.refreshIfRC()
	tx.tree.mu.RLock()
	defer tx.tree.mu.RUnlock()
	return tx.tree.splitKeysLocked(n, tx.snap())
}

func (tx *Txn) Commit() error {
	if tx == nil || tx.done {
		return nerr.New(nerr.InvalidArgument, "btree.Txn.Commit", "transaction is not active")
	}
	tx.done = true
	tx.tree.mu.Lock()
	tx.tree.eng.Enter(tx.wal)
	err := tx.tree.persist()
	tx.tree.eng.Leave(tx.wal)
	tx.tree.mu.Unlock()
	if err != nil {
		if !wal.IsCrash(err) {
			_ = tx.tree.eng.RollbackTxn(tx.wal)
		}
		return err
	}
	if err := tx.tree.eng.CommitTxn(tx.wal); err != nil {
		return err
	}
	return tx.PurgeDeleted()
}

func (tx *Txn) Rollback() error {
	if tx == nil || tx.done {
		return nil
	}
	tx.done = true
	tx.RestoreSnap()
	// RollbackTxn itself now replays the durable undo chain through
	// UndoTarget.ApplyUndo (routed per-tree via LogUndo's recorded targets),
	// so no separate manual replay is needed here.
	return tx.tree.eng.RollbackTxn(tx.wal)
}
