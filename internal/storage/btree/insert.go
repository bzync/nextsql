package btree

import (
	"errors"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

var errPatchDone = errors.New("btree: patch limit")

// Insert stores a unique key and its clustered row value.
func (t *Tree) Insert(key, value []byte) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Insert", "nil tree")
	}
	if _, err := encodeLeaf(key, value); err != nil {
		return err
	}
	key = copyBytes(key)
	value = copyBytes(value)

	tx, err := t.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		return err
	}
	if err := tx.Insert(key, value); err != nil {
		if !wal.IsCrash(err) {
			_ = tx.Rollback()
		}
		return err
	}
	return tx.Commit()
}

func (t *Tree) insertLocked(key, value []byte) error {
	return t.insertAt(key, value, true)
}

func (t *Tree) insertLockedAbsent(key, value []byte) error {
	return t.insertAt(key, value, false)
}

func (t *Tree) noteRight(id format.PageID, key []byte) {
	if t.hintRight == 0 || t.hintRight == id || len(t.hintMax) == 0 || compare(key, t.hintMax) > 0 {
		t.hintRight = id
		t.hintMax = append(t.hintMax[:0], key...)
	}
}

func (t *Tree) clearHint() {
	t.hintRight = 0
	t.hintMax = nil
	t.hintParent = 0
}

func (t *Tree) leafRawHint(key []byte) (format.PageID, []byte, bool, bool, error) {
	if t.hintRight == 0 || len(t.hintMax) == 0 || compare(key, t.hintMax) <= 0 {
		return 0, nil, false, false, nil
	}
	h, err := t.pin(t.hintRight)
	if err != nil {
		t.clearHint()
		return 0, nil, false, false, nil
	}
	if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
		_ = release(h, false)
		t.clearHint()
		return 0, nil, false, false, nil
	}
	hdr, err := readHeader(h.Page())
	if err != nil || hdr.next != 0 {
		_ = release(h, false)
		t.clearHint()
		return 0, nil, false, false, nil
	}
	_, val, found, err := findLeafSlot(h.Page(), key)
	if err != nil {
		_ = release(h, false)
		return 0, nil, false, false, nil
	}
	if found {
		out := copyBytes(val)
		if err := release(h, false); err != nil {
			return 0, nil, false, true, err
		}
		return t.hintRight, out, true, true, nil
	}
	if err := release(h, false); err != nil {
		return 0, nil, false, true, err
	}
	return t.hintRight, nil, false, true, nil
}

// insertOnLeaf inserts into a known leaf. On PageFull it descends and splits.
func (t *Tree) insertOnLeaf(leafID format.PageID, key, value []byte) error {
	h, err := t.pin(leafID)
	if err != nil {
		return t.insertAt(key, value, false)
	}
	if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
		_ = release(h, false)
		t.clearHint()
		return t.insertAt(key, value, false)
	}
	rec, err := encodeLeaf(key, value)
	if err != nil {
		_ = release(h, false)
		return err
	}
	if _, err := h.Page().Insert(rec); err == nil {
		t.noteRight(leafID, key)
		return release(h, true)
	} else if !nerr.HasCode(err, nerr.PageFull) {
		_ = release(h, false)
		return err
	}
	if err := release(h, false); err != nil {
		return err
	}
	t.clearHint()
	return t.insertAt(key, value, false)
}

func (t *Tree) insertAt(key, value []byte, checkDup bool) error {
	path, err := t.descend(key)
	if err != nil {
		return err
	}
	leafID := path[len(path)-1]
	h, err := t.pin(leafID)
	if err != nil {
		return err
	}
	if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
		_ = release(h, false)
		return err
	}
	if checkDup {
		if _, _, found, err := findLeafSlot(h.Page(), key); err != nil {
			_ = release(h, false)
			return err
		} else if found {
			_ = release(h, false)
			return nerr.New(nerr.AlreadyExists, "btree.Insert", "duplicate key")
		}
	}
	rec, err := encodeLeaf(key, value)
	if err != nil {
		_ = release(h, false)
		return err
	}
	if _, err := h.Page().Insert(rec); err == nil {
		t.noteRight(leafID, key)
		return release(h, true)
	} else if !nerr.HasCode(err, nerr.PageFull) {
		_ = release(h, false)
		return err
	}
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if t.hintRight == leafID && len(t.hintMax) > 0 && compare(t.hintMax, key) < 0 {
		return t.splitLeafAppend(h, leafID, hdr, copyBytes(t.hintMax), key, rec)
	}
	max, err := maxLeafKey(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if max != nil && compare(max, key) < 0 {
		return t.splitLeafAppend(h, leafID, hdr, copyBytes(max), key, rec)
	}
	ents, err := collectLeaves(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if err := release(h, false); err != nil {
		return err
	}
	if len(ents) == 0 {
		return nerr.New(nerr.InvalidArgument, "btree.Insert", "record exceeds page capacity")
	}
	return t.splitLeafAndInsert(path, hdr, ents, leafEntry{key: key, value: value})
}

func (t *Tree) descend(key []byte) ([]format.PageID, error) {
	path := make([]format.PageID, 0, t.height)
	id := t.root
	for level := t.height; level > 1; level-- {
		path = append(path, id)
		h, err := t.pin(id)
		if err != nil {
			return nil, err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeInternal); err != nil {
			_ = release(h, false)
			return nil, err
		}
		hdr, err := readHeader(h.Page())
		if err != nil {
			_ = release(h, false)
			return nil, err
		}
		ents, err := collectInternals(h.Page())
		if err != nil {
			_ = release(h, false)
			return nil, err
		}
		child := childForKey(hdr, ents, key)
		if err := release(h, false); err != nil {
			return nil, err
		}
		if child == 0 {
			return nil, nerr.New(nerr.Corruption, "btree.descend", "internal node has no child")
		}
		id = child
	}
	path = append(path, id)
	return path, nil
}

func (t *Tree) splitLeafAndInsert(path []format.PageID, hdr nodeHeader, ents []leafEntry, neu leafEntry) error {
	t.clearHint()
	all := append(append([]leafEntry(nil), ents...), neu)
	sortLeaves(all)
	if len(all) < 2 {
		return nerr.New(nerr.InvalidArgument, "btree.splitLeaf", "cannot split a single record")
	}
	mid, err := splitLeafIndex(all)
	if err != nil {
		return err
	}
	leftEnts := all[:mid]
	rightEnts := all[mid:]
	sep := copyBytes(rightEnts[0].key)

	leftID := path[len(path)-1]
	rightH, err := t.eng.NewPage(format.PageTypeBTreeLeaf)
	if err != nil {
		return err
	}
	rightID := rightH.ID()
	rightHdr := nodeHeader{prev: leftID, next: hdr.next}
	leftHdr := nodeHeader{prev: hdr.prev, next: rightID}

	leftPage, err := rebuildLeaf(leftID, leftHdr, leftEnts)
	if err != nil {
		_ = release(rightH, false)
		return err
	}
	rightPage, err := rebuildLeaf(rightID, rightHdr, rightEnts)
	if err != nil {
		_ = release(rightH, false)
		return err
	}
	if err := overwrite(rightH.Page(), rightPage); err != nil {
		_ = release(rightH, false)
		return err
	}
	if err := release(rightH, true); err != nil {
		return err
	}
	if err := t.eng.CrashAt(wal.PointDuringSplit); err != nil {
		return err
	}

	leftH, err := t.pin(leftID)
	if err != nil {
		return err
	}
	if err := overwrite(leftH.Page(), leftPage); err != nil {
		_ = release(leftH, false)
		return err
	}
	if err := release(leftH, true); err != nil {
		return err
	}

	if hdr.next != 0 {
		if err := t.setSiblingPrev(hdr.next, rightID); err != nil {
			return err
		}
	}
	return t.insertSeparator(path[:len(path)-1], sep, rightID)
}

func splitLeafIndex(ents []leafEntry) (int, error) {
	target := len(ents) / 2
	best := -1
	bestDelta := len(ents)
	for mid := 1; mid < len(ents); mid++ {
		if !leafFits(ents[:mid]) || !leafFits(ents[mid:]) {
			continue
		}
		delta := mid - target
		if delta < 0 {
			delta = -delta
		}
		if best < 0 || delta < bestDelta {
			best = mid
			bestDelta = delta
		}
	}
	if best < 0 {
		return 0, nerr.New(nerr.InvalidArgument, "btree.splitLeaf", "records cannot be split across two pages")
	}
	return best, nil
}

func (t *Tree) setSiblingPrev(id, prev format.PageID) error {
	h, err := t.pin(id)
	if err != nil {
		return err
	}
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	hdr.prev = prev
	if err := writeHeader(h.Page(), hdr); err != nil {
		_ = release(h, false)
		return err
	}
	return release(h, true)
}

func (t *Tree) insertSeparator(parentPath []format.PageID, sep []byte, right format.PageID) error {
	if len(parentPath) == 0 {
		return t.newRoot(t.root, sep, right)
	}
	parentID := parentPath[len(parentPath)-1]
	h, err := t.pin(parentID)
	if err != nil {
		return err
	}
	if err := expectType(h.Page(), format.PageTypeBTreeInternal); err != nil {
		_ = release(h, false)
		return err
	}
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	rec, err := encodeInternal(sep, right)
	if err != nil {
		_ = release(h, false)
		return err
	}
	max, err := maxInternalKey(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if max == nil || compare(max, sep) < 0 {
		if _, err := h.Page().Append(rec); err == nil {
			return release(h, true)
		} else if !nerr.HasCode(err, nerr.PageFull) {
			_ = release(h, false)
			return err
		}
		// Full: use a balanced internal split so the tree does not become a
		// right-skinny spine (that serializes later scans).
	}
	ents, err := collectInternals(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	next := append(append([]internalEntry(nil), ents...), internalEntry{key: copyBytes(sep), child: right})
	sortInternals(next)
	if internalFits(hdr, next) {
		rebuilt, err := rebuildInternal(parentID, hdr, next)
		if err != nil {
			_ = release(h, false)
			return err
		}
		if err := overwrite(h.Page(), rebuilt); err != nil {
			_ = release(h, false)
			return err
		}
		return release(h, true)
	}
	_ = release(h, false)
	return t.splitInternalAndInsert(parentPath, hdr, ents, internalEntry{key: copyBytes(sep), child: right})
}

func (t *Tree) newRoot(left format.PageID, sep []byte, right format.PageID) error {
	h, err := t.eng.NewPage(format.PageTypeBTreeInternal)
	if err != nil {
		return err
	}
	hdr := nodeHeader{leftmost: left}
	ents := []internalEntry{{key: copyBytes(sep), child: right}}
	rebuilt, err := rebuildInternal(h.ID(), hdr, ents)
	if err != nil {
		_ = release(h, false)
		return err
	}
	if err := overwrite(h.Page(), rebuilt); err != nil {
		_ = release(h, false)
		return err
	}
	t.root = h.ID()
	t.height++
	if err := release(h, true); err != nil {
		return err
	}
	return t.persist()
}

func (t *Tree) splitInternalAndInsert(path []format.PageID, hdr nodeHeader, ents []internalEntry, neu internalEntry) error {
	all := append(append([]internalEntry(nil), ents...), neu)
	sortInternals(all)
	if len(all) < 1 {
		return nerr.New(nerr.Internal, "btree.splitInternal", "empty internal node")
	}
	// children: [leftmost, all[0].child, all[1].child, ...]
	// keys:     [all[0].key, all[1].key, ...]
	mid := len(all) / 2
	if mid >= len(all) {
		mid = len(all) - 1
	}
	promote := copyBytes(all[mid].key)

	leftEnts := all[:mid]
	rightLeftmost := all[mid].child
	rightEnts := all[mid+1:]

	leftID := path[len(path)-1]
	rightH, err := t.eng.NewPage(format.PageTypeBTreeInternal)
	if err != nil {
		return err
	}
	rightID := rightH.ID()
	leftHdr := nodeHeader{leftmost: hdr.leftmost}
	rightHdr := nodeHeader{leftmost: rightLeftmost}

	leftPage, err := rebuildInternal(leftID, leftHdr, leftEnts)
	if err != nil {
		_ = release(rightH, false)
		return err
	}
	rightPage, err := rebuildInternal(rightID, rightHdr, rightEnts)
	if err != nil {
		_ = release(rightH, false)
		return err
	}
	if err := overwrite(rightH.Page(), rightPage); err != nil {
		_ = release(rightH, false)
		return err
	}
	if err := release(rightH, true); err != nil {
		return err
	}
	leftH, err := t.pin(leftID)
	if err != nil {
		return err
	}
	if err := overwrite(leftH.Page(), leftPage); err != nil {
		_ = release(leftH, false)
		return err
	}
	if err := release(leftH, true); err != nil {
		return err
	}
	return t.insertSeparator(path[:len(path)-1], promote, rightID)
}

func (t *Tree) leftmostLeaf() (format.PageID, error) {
	id := t.root
	for {
		h, err := t.pin(id)
		if err != nil {
			return 0, err
		}
		typ := h.Page().Type()
		if typ == format.PageTypeBTreeLeaf {
			if err := release(h, false); err != nil {
				return 0, err
			}
			return id, nil
		}
		if typ != format.PageTypeBTreeInternal {
			_ = release(h, false)
			return 0, nerr.New(nerr.Corruption, "btree.leftmostLeaf", "unexpected page type")
		}
		hdr, err := readHeader(h.Page())
		if err != nil {
			_ = release(h, false)
			return 0, err
		}
		if err := release(h, false); err != nil {
			return 0, err
		}
		if hdr.leftmost == 0 {
			return 0, nerr.New(nerr.Corruption, "btree.leftmostLeaf", "internal node missing leftmost child")
		}
		id = hdr.leftmost
	}
}
