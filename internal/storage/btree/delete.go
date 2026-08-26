package btree

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

// Delete removes key and its clustered row.
func (t *Tree) Delete(key []byte) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Delete", "nil tree")
	}
	if err := checkKey(key); err != nil {
		return err
	}
	key = copyBytes(key)

	tx, err := t.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		return err
	}
	if err := tx.Delete(key); err != nil {
		if !wal.IsCrash(err) {
			_ = tx.Rollback()
		}
		return err
	}
	return tx.Commit()
}

func (t *Tree) deleteLocked(key []byte) error {
	t.clearHint()
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
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	slot, _, found, err := findLeafSlot(h.Page(), key)
	if err != nil {
		_ = release(h, false)
		return err
	}
	if !found {
		_ = release(h, false)
		return nerr.New(nerr.NotFound, "btree.Delete", "key not found")
	}
	// Header is slot 0. One remaining data slot means this delete empties the leaf.
	if h.Page().LiveSlots() == 2 && leafID != t.root {
		if err := release(h, false); err != nil {
			return err
		}
		return t.removeEmptyLeaf(path, hdr)
	}
	if err := h.Page().Delete(slot); err != nil {
		_ = release(h, false)
		return err
	}
	if leafID == t.root {
		return release(h, true)
	}
	views, err := collectLeafViews(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if !shouldMerge(views) {
		return release(h, true)
	}
	ents, err := collectLeaves(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if err := release(h, true); err != nil {
		return err
	}
	return t.maybeMergeLeaf(path, hdr, ents)
}

func payloadBytes(ents []leafEntry) int {
	n := 0
	for _, e := range ents {
		n += 4 + len(e.key) + len(e.value)
	}
	return n
}

func shouldMerge(ents []leafEntry) bool {
	return payloadBytes(ents) <= maxPayload/4
}

func parentHasChild(hdr nodeHeader, ents []internalEntry, id format.PageID) bool {
	if hdr.leftmost == id {
		return true
	}
	for _, e := range ents {
		if e.child == id {
			return true
		}
	}
	return false
}

func (t *Tree) maybeMergeLeaf(path []format.PageID, hdr nodeHeader, ents []leafEntry) error {
	leafID := path[len(path)-1]
	var (
		phdr  nodeHeader
		pents []internalEntry
	)
	if len(path) >= 2 {
		ph, err := t.pin(path[len(path)-2])
		if err != nil {
			return err
		}
		phdr, err = readHeader(ph.Page())
		if err != nil {
			_ = release(ph, false)
			return err
		}
		pents, err = collectInternals(ph.Page())
		if err != nil {
			_ = release(ph, false)
			return err
		}
		if err := release(ph, false); err != nil {
			return err
		}
	}
	for _, sibID := range []format.PageID{hdr.next, hdr.prev} {
		if sibID == 0 {
			continue
		}
		if len(path) >= 2 && !parentHasChild(phdr, pents, sibID) {
			// Next/prev may be in another parent. Merging across that
			// boundary moves keys into the wrong subtree.
			continue
		}
		sh, err := t.pin(sibID)
		if err != nil {
			return err
		}
		if sh.Page().Type() != format.PageTypeBTreeLeaf {
			_ = release(sh, false)
			return nerr.New(nerr.Corruption, "btree.maybeMergeLeaf", "sibling is not a leaf")
		}
		sibHdr, err := readHeader(sh.Page())
		if err != nil {
			_ = release(sh, false)
			return err
		}
		sibEnts, err := collectLeaves(sh.Page())
		if err != nil {
			_ = release(sh, false)
			return err
		}
		var combined []leafEntry
		if sibID == hdr.next {
			combined = append(append([]leafEntry(nil), ents...), sibEnts...)
		} else {
			combined = append(append([]leafEntry(nil), sibEnts...), ents...)
		}
		if !leafFits(combined) {
			if err := release(sh, false); err != nil {
				return err
			}
			continue
		}
		keepHdr := sibHdr
		if sibID == hdr.next {
			keepHdr.prev = hdr.prev
		} else {
			keepHdr.next = hdr.next
		}
		rebuilt, err := rebuildLeaf(sibID, keepHdr, combined)
		if err != nil {
			_ = release(sh, false)
			return err
		}
		if err := overwrite(sh.Page(), rebuilt); err != nil {
			_ = release(sh, false)
			return err
		}
		if err := release(sh, true); err != nil {
			return err
		}
		if err := t.eng.CrashAt(wal.PointDuringMerge); err != nil {
			return err
		}
		if sibID == hdr.next && hdr.prev != 0 {
			if err := t.setSiblingNext(hdr.prev, sibID); err != nil {
				return err
			}
		}
		if sibID == hdr.prev && hdr.next != 0 {
			if err := t.setSiblingPrev(hdr.next, sibID); err != nil {
				return err
			}
		}
		if sibID == hdr.next && len(combined) > 0 {
			if err := t.setChildSeparator(path[:len(path)-1], sibID, combined[0].key); err != nil {
				return err
			}
		}
		if err := t.removeChild(path[:len(path)-1], leafID); err != nil {
			return err
		}
		return t.eng.Drop(leafID)
	}
	return nil
}

func (t *Tree) removeEmptyLeaf(path []format.PageID, hdr nodeHeader) error {
	leafID := path[len(path)-1]
	if hdr.prev != 0 {
		if err := t.setSiblingNext(hdr.prev, hdr.next); err != nil {
			return err
		}
	}
	if hdr.next != 0 {
		if err := t.setSiblingPrev(hdr.next, hdr.prev); err != nil {
			return err
		}
	}
	if err := t.removeChild(path[:len(path)-1], leafID); err != nil {
		return err
	}
	if t.root == leafID {
		return nil
	}
	// Last child of the root was an empty internal. Collapse to this
	// leaf so the empty tree stays a height-1 leaf instead of a freed
	// internal (or a recycled page of another type).
	if t.root == 0 {
		t.root = leafID
		t.height = 1
		return t.persist()
	}
	return t.eng.Drop(leafID)
}

func (t *Tree) setChildSeparator(parentPath []format.PageID, child format.PageID, sep []byte) error {
	if len(parentPath) == 0 {
		return nil
	}
	parentID := parentPath[len(parentPath)-1]
	h, err := t.pin(parentID)
	if err != nil {
		return err
	}
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	ents, err := collectInternals(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if hdr.leftmost == child {
		if err := release(h, false); err != nil {
			return err
		}
		return t.setChildSeparator(parentPath[:len(parentPath)-1], parentID, sep)
	}
	found := false
	for i := range ents {
		if ents[i].child == child {
			ents[i].key = copyBytes(sep)
			found = true
			break
		}
	}
	if !found {
		_ = release(h, false)
		return nerr.New(nerr.Corruption, "btree.setChildSeparator", "parent does not reference child")
	}
	rebuilt, err := rebuildInternal(parentID, hdr, ents)
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

func (t *Tree) setSiblingNext(id, next format.PageID) error {
	h, err := t.pin(id)
	if err != nil {
		return err
	}
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	hdr.next = next
	if err := writeHeader(h.Page(), hdr); err != nil {
		_ = release(h, false)
		return err
	}
	return release(h, true)
}

func (t *Tree) removeChild(parentPath []format.PageID, child format.PageID) error {
	if len(parentPath) == 0 {
		return nerr.New(nerr.Internal, "btree.removeChild", "cannot remove child of missing parent")
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
	ents, err := collectInternals(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if hdr.leftmost == child && len(ents) == 0 {
		if err := release(h, false); err != nil {
			return err
		}
		if parentID == t.root {
			// Root's only remaining child is being removed. Drop the
			// empty root and let the original leaf become the new
			// empty-tree root. Promoting an empty internal here
			// (height >= 3) left a non-leaf at height 1, and the
			// caller then Drop'd that page — the next pin saw a
			// recycled page of the wrong type.
			if err := t.eng.Drop(parentID); err != nil {
				return err
			}
			t.root = 0
			t.height = 0
			return nil
		}
		if err := t.removeChild(parentPath[:len(parentPath)-1], parentID); err != nil {
			return err
		}
		return t.eng.Drop(parentID)
	}
	if hdr.leftmost == child {
		hdr.leftmost = ents[0].child
		ents = ents[1:]
	} else {
		found := false
		for i, e := range ents {
			if e.child == child {
				ents = append(ents[:i], ents[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			_ = release(h, false)
			return nerr.New(nerr.Corruption, "btree.removeChild", "parent does not reference child")
		}
	}

	// A single-child non-root internal must stay. Replacing it with that
	// child in the grandparent shortens one path and breaks descend.
	if len(ents) == 0 && parentID == t.root {
		only := hdr.leftmost
		if err := release(h, false); err != nil {
			return err
		}
		t.root = only
		t.height--
		if err := t.eng.Drop(parentID); err != nil {
			return err
		}
		return t.persist()
	}

	rebuilt, err := rebuildInternal(parentID, hdr, ents)
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

func (t *Tree) replaceChild(parentPath []format.PageID, old, neu format.PageID) error {
	if len(parentPath) == 0 {
		t.root = neu
		t.height--
		return t.persist()
	}
	parentID := parentPath[len(parentPath)-1]
	h, err := t.pin(parentID)
	if err != nil {
		return err
	}
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	ents, err := collectInternals(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	if hdr.leftmost == old {
		hdr.leftmost = neu
	} else {
		found := false
		for i := range ents {
			if ents[i].child == old {
				ents[i].child = neu
				found = true
				break
			}
		}
		if !found {
			_ = release(h, false)
			return nerr.New(nerr.Corruption, "btree.replaceChild", "parent does not reference child")
		}
	}
	rebuilt, err := rebuildInternal(parentID, hdr, ents)
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
