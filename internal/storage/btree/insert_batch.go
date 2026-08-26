package btree

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/buffer"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
	"github.com/bzync/nextsql/internal/storage/row"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

func (t *Tree) insertBatchLocked(xmin format.TxnID, keys, values [][]byte) error {
	var (
		h      *buffer.Handle
		leafID format.PageID
		dirty  bool
		verBuf []byte
		recBuf []byte
	)
	flush := func() error {
		if h == nil {
			return nil
		}
		err := release(h, dirty)
		h = nil
		dirty = false
		return err
	}
	defer func() { _ = flush() }()

	put := func(key, rec []byte) error {
		if h != nil {
			if _, err := h.Page().Append(rec); err == nil {
				t.noteRight(leafID, key)
				dirty = true
				return nil
			} else if !nerr.HasCode(err, nerr.PageFull) {
				return err
			}
			if err := t.splitPinnedAndInsert(h, leafID, key, rec); err != nil {
				h = nil
				dirty = false
				return err
			}
			h = nil
			dirty = false
			if t.hintRight != 0 {
				nh, err := t.pin(t.hintRight)
				if err != nil {
					return nil
				}
				h = nh
				leafID = t.hintRight
			}
			return nil
		}
		id, _, found, err := t.leafRaw(key)
		if err != nil {
			return err
		}
		if found {
			return nerr.New(nerr.AlreadyExists, "btree.InsertBatch", "duplicate key")
		}
		leafID = id
		h, err = t.pin(leafID)
		if err != nil {
			return err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
			_ = flush()
			return err
		}
		if _, err := h.Page().Append(rec); err == nil {
			t.noteRight(leafID, key)
			dirty = true
			return nil
		} else if !nerr.HasCode(err, nerr.PageFull) {
			return err
		}
		if err := t.splitPinnedAndInsert(h, leafID, key, rec); err != nil {
			h = nil
			dirty = false
			return err
		}
		h = nil
		dirty = false
		if t.hintRight != 0 {
			nh, err := t.pin(t.hintRight)
			if err == nil {
				h = nh
				leafID = t.hintRight
			}
		}
		return nil
	}

	var added int64
	for i, key := range keys {
		verBuf = row.EncodeInto(verBuf, row.Version{Xmin: xmin, Payload: values[i]})
		var err error
		recBuf, err = encodeLeafInto(recBuf, key, verBuf)
		if err != nil {
			t.addLive(added)
			return err
		}
		if err := put(key, recBuf); err != nil {
			t.addLive(added)
			return err
		}
		added++
	}
	t.addLive(added)
	return flush()
}

func (t *Tree) splitPinnedAndInsert(h *buffer.Handle, leafID format.PageID, key, rec []byte) error {
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
	k, v, err := decodeLeaf(rec)
	if err != nil {
		return err
	}
	path, err := t.descend(key)
	if err != nil {
		return err
	}
	if path[len(path)-1] != leafID {
		return t.insertAt(copyBytes(k), copyBytes(v), false)
	}
	return t.splitLeafAndInsert(path, hdr, ents, leafEntry{key: copyBytes(k), value: copyBytes(v)})
}

// splitLeafAppend is the sequential bulk-load split: the new key is greater
// than every key on the full leaf, so the left page stays as-is except its
// next pointer. Only the new record goes on the right page.
func (t *Tree) splitLeafAppend(h *buffer.Handle, leafID format.PageID, hdr nodeHeader, leftMax, key, rec []byte) error {
	t.hintRight = 0
	t.hintMax = nil
	rightH, err := t.eng.NewPage(format.PageTypeBTreeLeaf)
	if err != nil {
		_ = release(h, false)
		return err
	}
	rightID := rightH.ID()
	if err := initNode(rightH.Page(), nodeHeader{prev: leafID, next: hdr.next}); err != nil {
		_ = release(rightH, false)
		_ = release(h, false)
		return err
	}
	if _, err := rightH.Page().Append(rec); err != nil {
		_ = release(rightH, false)
		_ = release(h, false)
		return err
	}
	if err := release(rightH, true); err != nil {
		_ = release(h, false)
		return err
	}
	oldNext := hdr.next
	hdr.next = rightID
	if err := writeHeader(h.Page(), hdr); err != nil {
		_ = release(h, false)
		return err
	}
	if err := t.eng.CrashAt(wal.PointDuringSplit); err != nil {
		_ = release(h, true)
		return err
	}
	if err := release(h, true); err != nil {
		return err
	}
	if oldNext != 0 {
		if err := t.setSiblingPrev(oldNext, rightID); err != nil {
			return err
		}
	}
	t.noteRight(rightID, key)
	return t.attachRight(leftMax, key, rightID)
}

// attachRight inserts the separator for a new rightmost leaf. Sequential
// bulk load reuses the cached parent so it does not descend from the root.
func (t *Tree) attachRight(leftMax, sep []byte, rightID format.PageID) error {
	if t.hintParent != 0 {
		ok, err := t.tryAppendSeparator(t.hintParent, sep, rightID)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		t.hintParent = 0
	}
	path, err := t.descend(leftMax)
	if err != nil {
		return err
	}
	var parentPath []format.PageID
	if len(path) > 1 {
		parentPath = path[:len(path)-1]
	}
	if err := t.insertSeparator(parentPath, copyBytes(sep), rightID); err != nil {
		t.hintParent = 0
		return err
	}
	if len(parentPath) == 0 {
		t.hintParent = t.root
		return nil
	}
	p2, err := t.descend(sep)
	if err != nil || len(p2) < 2 {
		return nil
	}
	t.hintParent = p2[len(p2)-2]
	return nil
}

func (t *Tree) tryAppendSeparator(parentID format.PageID, sep []byte, right format.PageID) (bool, error) {
	h, err := t.pin(parentID)
	if err != nil {
		return false, nil
	}
	if err := expectType(h.Page(), format.PageTypeBTreeInternal); err != nil {
		_ = release(h, false)
		return false, nil
	}
	rec, err := encodeInternal(sep, right)
	if err != nil {
		_ = release(h, false)
		return false, err
	}
	max, err := lastInternalKey(h.Page())
	if err != nil {
		_ = release(h, false)
		return false, err
	}
	if max != nil && compare(max, sep) >= 0 {
		_ = release(h, false)
		return false, nil
	}
	if _, err := h.Page().Append(rec); err != nil {
		_ = release(h, false)
		if nerr.HasCode(err, nerr.PageFull) {
			return false, nil
		}
		return false, err
	}
	return true, release(h, true)
}

func lastInternalKey(p *page.Page) ([]byte, error) {
	for i := p.SlotCount() - 1; i >= 1; i-- {
		rec, err := p.GetView(uint16(i))
		if err != nil {
			continue
		}
		k, _, err := decodeInternal(rec)
		return k, err
	}
	return nil, nil
}

func (t *Tree) patchVisibleLocked(after []byte, limit int, snap txn.Snapshot, xmin format.TxnID, fn func(key, payload []byte) ([]byte, error)) ([]byte, int, error) {
	var (
		id  format.PageID
		err error
	)
	if after == nil {
		id, err = t.leftmostLeaf()
	} else {
		path, e := t.descend(after)
		err = e
		if err == nil {
			id = path[len(path)-1]
		}
	}
	if err != nil {
		return nil, 0, err
	}
	n := 0
	var last, lastScratch []byte
	for id != 0 && n < limit {
		h, err := t.pin(id)
		if err != nil {
			return last, n, err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return last, n, err
		}
		hdr, err := readHeader(h.Page())
		if err != nil {
			_ = release(h, false)
			return last, n, err
		}
		dirty := false
		var verBuf, recBuf []byte
		walkErr := forEachSlot(h.Page(), func(slot uint16, rec []byte) error {
			if n >= limit {
				return errPatchDone
			}
			k, v, err := decodeLeaf(rec)
			if err != nil {
				return err
			}
			if after != nil && compare(k, after) <= 0 {
				return nil
			}
			payload, vis, err := t.visiblePayload(v, snap)
			if err != nil {
				return err
			}
			if !vis {
				return nil
			}
			neu, err := fn(k, payload)
			if err != nil {
				return err
			}
			if neu == nil {
				return nil
			}
			verBuf = row.EncodeInto(verBuf, row.Version{Xmin: xmin, Payload: neu})
			recBuf, err = encodeLeafInto(recBuf, k, verBuf)
			if err != nil {
				return err
			}
			if err := h.Page().Update(slot, recBuf); err != nil {
				return err
			}
			dirty = true
			lastScratch = append(lastScratch[:0], k...)
			n++
			return nil
		})
		next := hdr.next
		if len(lastScratch) > 0 {
			last = append(last[:0], lastScratch...)
		}
		if err := release(h, dirty); err != nil {
			return last, n, err
		}
		if walkErr != nil && !errorsIsPatchDone(walkErr) {
			return last, n, walkErr
		}
		if n >= limit {
			break
		}
		id = next
	}
	return last, n, nil
}

func errorsIsPatchDone(err error) bool {
	return err == errPatchDone
}
