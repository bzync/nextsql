package btree

import (
	"github.com/bzync/nextsql/internal/maintenance"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/row"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/undo"
)

func (t *Tree) readSnapshot() txn.Snapshot {
	if t.eng.TM == nil {
		return txn.Snapshot{}
	}
	return t.eng.TM.Capture(0)
}

func (t *Tree) visiblePayload(raw []byte, snap txn.Snapshot) ([]byte, bool, error) {
	ver, has, err := row.Inspect(raw)
	if err != nil {
		return nil, false, err
	}
	st := t.eng.TM.Status
	if !has {
		return append([]byte(nil), raw...), true, nil
	}
	for {
		if snap.Sees(ver.Xmin, st) {
			if ver.Xmax == 0 || !snap.Sees(ver.Xmax, st) {
				return append([]byte(nil), ver.Payload...), true, nil
			}
			return nil, false, nil
		}
		if ver.Undo == 0 || t.eng.Undo == nil {
			return nil, false, nil
		}
		rec, err := t.eng.Undo.Get(ver.Undo)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if rec.Kind == undo.KindInsert {
			return nil, false, nil
		}
		ver = rec.Old
	}
}

func (t *Tree) rowVisible(raw []byte, snap txn.Snapshot) (bool, error) {
	ver, has, err := row.Inspect(raw)
	if err != nil {
		return false, err
	}
	if !has {
		return true, nil
	}
	st := t.eng.TM.Status
	for {
		if snap.Sees(ver.Xmin, st) {
			return ver.Xmax == 0 || !snap.Sees(ver.Xmax, st), nil
		}
		if ver.Undo == 0 || t.eng.Undo == nil {
			return false, nil
		}
		rec, err := t.eng.Undo.Get(ver.Undo)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return false, nil
			}
			return false, err
		}
		if rec.Kind == undo.KindInsert {
			return false, nil
		}
		ver = rec.Old
	}
}

func (t *Tree) leafRaw(key []byte) (format.PageID, []byte, bool, error) {
	if id, raw, found, ok, err := t.leafRawHint(key); ok {
		return id, raw, found, err
	}
	path, err := t.descend(key)
	if err != nil {
		return 0, nil, false, err
	}
	leafID := path[len(path)-1]
	h, err := t.pin(leafID)
	if err != nil {
		return 0, nil, false, err
	}
	if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
		_ = release(h, false)
		return 0, nil, false, err
	}
	_, val, found, err := findLeafSlot(h.Page(), key)
	if err != nil {
		_ = release(h, false)
		return 0, nil, false, err
	}
	if !found {
		if err := release(h, false); err != nil {
			return 0, nil, false, err
		}
		return leafID, nil, false, nil
	}
	out := copyBytes(val)
	if err := release(h, false); err != nil {
		return 0, nil, false, err
	}
	return leafID, out, true, nil
}

func decodeVersion(raw []byte) (row.Version, bool, error) {
	return row.Decode(raw)
}

func (t *Tree) purgeCommitted(key []byte) error {
	return t.purgeCommittedBatch([][]byte{key})
}

func (t *Tree) purgeCommittedBatch(keys [][]byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(keys) == 0 {
		return nil
	}
	return t.apply(func() error {
		for _, key := range keys {
			_, raw, found, err := t.leafRaw(key)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			ver, has, err := row.Decode(raw)
			if err != nil {
				return err
			}
			if !has || ver.Xmax == 0 || (t.eng.TM != nil && t.eng.TM.Status(ver.Xmax) != txn.StatusCommitted) {
				continue
			}
			if err := t.deleteLocked(key); err != nil && !nerr.HasCode(err, nerr.NotFound) {
				return err
			}
		}
		return nil
	})
}

// PurgeDead removes up to limit committed tombstones. The caller must prevent
// new transactions from starting for the duration (the executor uses its
// database apply barrier). It fails closed while any snapshot is live.
// Tombstones are discovered from durable tree contents, so skipped work is
// naturally recovered after restart without an in-memory queue.
func (t *Tree) PurgeDead(limit int) (int, error) {
	return t.PurgeDeadBudgeted(limit, nil)
}

func (t *Tree) PurgeDeadBudgeted(limit int, budget *maintenance.Budget) (int, error) {
	if t == nil || t.eng == nil || limit < 1 {
		return 0, nerr.New(nerr.InvalidArgument, "btree.PurgeDead", "tree and positive limit are required")
	}
	if t.eng.TM != nil && t.eng.TM.LiveSnapshots() != 0 {
		return 0, nerr.New(nerr.Unavailable, "btree.PurgeDead", "transactions are active")
	}

	keys := make([][]byte, 0, limit)
	deadTxns := make(map[format.TxnID]struct{})
	t.mu.RLock()
	err := t.collectDeadLocked(limit, &keys, deadTxns, budget)
	t.mu.RUnlock()
	var bufferedMemory int64
	for _, key := range keys {
		bufferedMemory += int64(len(key) + 24)
	}
	defer budget.ReleaseMemory(bufferedMemory)
	if err != nil {
		return 0, err
	}
	for i, key := range keys {
		if err := budget.Check(); err != nil {
			return i, err
		}
		// Reserve path plus sibling/parent merge work before mutation.
		if err := budget.ConsumeIO(int64(2*t.height + 4)); err != nil {
			return i, err
		}
		if t.eng.TM != nil && t.eng.TM.LiveSnapshots() != 0 {
			return i, nerr.New(nerr.Unavailable, "btree.PurgeDead", "transaction started during maintenance")
		}
		if err := t.purgeCommitted(key); err != nil {
			return i, err
		}
	}
	if t.eng.Undo != nil {
		for id := range deadTxns {
			t.eng.Undo.ForgetTxn(id)
		}
	}
	return len(keys), nil
}

func (t *Tree) collectDeadLocked(limit int, keys *[][]byte, deadTxns map[format.TxnID]struct{}, budget *maintenance.Budget) error {
	id, err := t.leftmostLeaf()
	if err != nil {
		return err
	}
	for id != 0 && len(*keys) < limit {
		if err := budget.Check(); err != nil {
			return err
		}
		if err := budget.ConsumeIO(1); err != nil {
			return err
		}
		h, err := t.pin(id)
		if err != nil {
			return err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return err
		}
		hdr, err := readHeader(h.Page())
		if err == nil {
			err = forEachSlot(h.Page(), func(_ uint16, rec []byte) error {
				if len(*keys) >= limit {
					return nil
				}
				key, raw, err := decodeLeaf(rec)
				if err != nil {
					return err
				}
				ver, has, err := row.Decode(raw)
				if err != nil {
					return err
				}
				if has && ver.Xmax != 0 && (t.eng.TM == nil || t.eng.TM.Status(ver.Xmax) == txn.StatusCommitted) {
					if err := budget.ReserveMemory(int64(len(key) + 24)); err != nil {
						return err
					}
					*keys = append(*keys, copyBytes(key))
					deadTxns[ver.Xmax] = struct{}{}
				}
				return nil
			})
		}
		next := hdr.next
		if rerr := release(h, false); err == nil {
			err = rerr
		}
		if err != nil {
			return err
		}
		id = next
	}
	return nil
}

func (t *Tree) applyUndoRec(rec undo.Record) error {
	switch rec.Kind {
	case undo.KindInsert:
		if err := t.deleteLocked(rec.Key); err != nil && !nerr.HasCode(err, nerr.NotFound) {
			return err
		}
		return nil
	case undo.KindUpdate, undo.KindDelete:
		raw := row.Encode(rec.Old)
		if err := t.updateLocked(rec.Key, raw); err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return t.insertLocked(rec.Key, raw)
			}
			return err
		}
		return nil
	default:
		return nil
	}
}

// ApplyUndo implements storage.UndoTarget: it reverses one durable undo
// record against this tree's live, in-memory state, taking the same
// tree-mutex + engine page-mutation section every ordinary DML mutation
// takes (see Txn.withWrite). rec.Key is looked up by a fresh top-down
// descend from the tree's current root (applyUndoRec -> {delete,update,
// insert}Locked), so this is correct even if a concurrent split moved the
// key to a different physical page since rec was logged; rec.PageID itself
// is not consulted here (only crash recovery's undo.Apply, which runs
// before any buffer pool exists, needs it). liveKnown is invalidated rather
// than adjusted precisely: it is a row-count cache, not a correctness
// value, and forcing a recount is simplest and always safe.
func (t *Tree) ApplyUndo(stx *storage.Txn, rec undo.Record) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Tree.ApplyUndo", "nil tree")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.eng.Enter(stx)
	err := t.applyUndoRec(rec)
	t.eng.Leave(stx)
	if err == nil {
		t.liveKnown = false
	}
	return err
}
