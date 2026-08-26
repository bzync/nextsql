package btree

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/buffer"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/row"
	"github.com/bzync/nextsql/internal/txn"
)

// Lookup returns a copy of the row stored under key.
func (t *Tree) Lookup(key []byte) ([]byte, error) {
	if t == nil {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Lookup", "nil tree")
	}
	if err := checkKey(key); err != nil {
		return nil, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	path, err := t.descend(key)
	if err != nil {
		return nil, err
	}
	h, err := t.pin(path[len(path)-1])
	if err != nil {
		return nil, err
	}
	if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
		_ = release(h, false)
		return nil, err
	}
	_, raw, found, err := findLeafSlot(h.Page(), key)
	if err != nil {
		_ = release(h, false)
		return nil, err
	}
	if !found {
		_ = release(h, false)
		return nil, nerr.New(nerr.NotFound, "btree.Lookup", "key not found")
	}
	raw = copyBytes(raw)
	if err := release(h, false); err != nil {
		return nil, err
	}
	payload, vis, err := t.visiblePayload(raw, t.readSnapshot())
	if err != nil {
		return nil, err
	}
	if !vis {
		return nil, nerr.New(nerr.NotFound, "btree.Lookup", "key not found")
	}
	return payload, nil
}

// Range visits keys in [start, end) in order. A nil start is the first key.
// A nil end is unbounded. fn must not retain the key or value slices.
func (t *Tree) Range(start, end []byte, fn func(key, value []byte) error) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Range", "nil tree")
	}
	if fn == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Range", "nil callback")
	}
	if start != nil {
		if err := checkKey(start); err != nil {
			return err
		}
		start = copyBytes(start)
	}
	if end != nil {
		if err := checkKey(end); err != nil {
			return err
		}
		end = copyBytes(end)
	}
	if start != nil && end != nil && compare(start, end) >= 0 {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var id format.PageID
	var err error
	if start == nil {
		id, err = t.leftmostLeaf()
	} else {
		var path []format.PageID
		path, err = t.descend(start)
		if err == nil {
			id = path[len(path)-1]
		}
	}
	if err != nil {
		return err
	}

	for id != 0 {
		h, err := t.pin(id)
		if err != nil {
			return err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return err
		}
		stop, err := t.visitLeaf(h, start, end, t.readSnapshot(), fn, false)
		if err != nil {
			_ = release(h, false)
			return err
		}
		hdr, herr := readHeader(h.Page())
		if herr != nil {
			_ = release(h, false)
			return herr
		}
		if err := release(h, false); err != nil {
			return err
		}
		if stop {
			return nil
		}
		id = hdr.next
	}
	return nil
}

func (t *Tree) rangeVisible(start, end []byte, snap txn.Snapshot, fn func(key, value []byte) error) error {
	if start != nil {
		if err := checkKey(start); err != nil {
			return err
		}
		start = copyBytes(start)
	}
	if end != nil {
		if err := checkKey(end); err != nil {
			return err
		}
		end = copyBytes(end)
	}
	if start != nil && end != nil && compare(start, end) >= 0 {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var id format.PageID
	var err error
	if start == nil {
		id, err = t.leftmostLeaf()
	} else {
		var path []format.PageID
		path, err = t.descend(start)
		if err == nil {
			id = path[len(path)-1]
		}
	}
	if err != nil {
		return err
	}

	for id != 0 {
		h, err := t.pin(id)
		if err != nil {
			return err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return err
		}
		stop, err := t.visitLeaf(h, start, end, snap, fn, false)
		if err != nil {
			_ = release(h, false)
			return err
		}
		hdr, herr := readHeader(h.Page())
		if herr != nil {
			_ = release(h, false)
			return herr
		}
		if err := release(h, false); err != nil {
			return err
		}
		if stop {
			return nil
		}
		id = hdr.next
	}
	return nil
}

// visitLeaf walks one pinned leaf. Entries alias the page; fn must not
// retain them. stop is true when the remaining right siblings are >= end.
func (t *Tree) rangeVisibleSkip(start, end []byte, snap txn.Snapshot, fn func(key, value []byte) error) error {
	return t.rangeVisibleVis(start, end, snap, fn, true)
}

func (t *Tree) rangeVisibleVis(start, end []byte, snap txn.Snapshot, fn func(key, value []byte) error, skipVis bool) error {
	if start != nil {
		if err := checkKey(start); err != nil {
			return err
		}
		start = copyBytes(start)
	}
	if end != nil {
		if err := checkKey(end); err != nil {
			return err
		}
		end = copyBytes(end)
	}
	if start != nil && end != nil && compare(start, end) >= 0 {
		return nil
	}
	var id format.PageID
	var err error
	if start == nil {
		id, err = t.leftmostLeaf()
	} else {
		var path []format.PageID
		path, err = t.descend(start)
		if err == nil {
			id = path[len(path)-1]
		}
	}
	if err != nil {
		return err
	}
	for id != 0 {
		h, err := t.pin(id)
		if err != nil {
			return err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return err
		}
		stop, err := t.visitLeaf(h, start, end, snap, fn, skipVis)
		if err != nil {
			_ = release(h, false)
			return err
		}
		hdr, herr := readHeader(h.Page())
		if herr != nil {
			_ = release(h, false)
			return herr
		}
		if err := release(h, false); err != nil {
			return err
		}
		if stop {
			return nil
		}
		id = hdr.next
	}
	return nil
}

func (t *Tree) visitLeaf(h *buffer.Handle, start, end []byte, snap txn.Snapshot, fn func(key, value []byte) error, skipVis bool) (stop bool, err error) {
	ents, err := collectLeafViews(h.Page())
	if err != nil {
		return false, err
	}
	for _, e := range ents {
		if start != nil && compare(e.key, start) < 0 {
			continue
		}
		if end != nil && compare(e.key, end) >= 0 {
			return true, nil
		}
		var payload []byte
		if skipVis {
			ver, has, err := row.Inspect(e.value)
			if err != nil {
				return false, err
			}
			if has {
				payload = append([]byte(nil), ver.Payload...)
			} else {
				payload = append([]byte(nil), e.value...)
			}
		} else {
			var vis bool
			payload, vis, err = t.visiblePayload(e.value, snap)
			if err != nil {
				return false, err
			}
			if !vis {
				continue
			}
		}
		if err := fn(copyBytes(e.key), payload); err != nil {
			return false, err
		}
	}
	return false, nil
}

// SplitKeys returns up to n-1 interior keys that partition visible rows
// into at most n ranges [nil, k0), [k0, k1), ..., [k_last, nil).
func (t *Tree) SplitKeys(n int) ([][]byte, error) {
	if t == nil || n <= 1 {
		return nil, nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.splitKeysLocked(n, t.readSnapshot())
}

func (t *Tree) splitKeysLocked(n int, _ txn.Snapshot) ([][]byte, error) {
	if n <= 1 || t.height < 2 {
		return nil, nil
	}
	keys, err := t.gatherSeparators(t.root, n-1)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	return pickEvenKeys(keys, n-1), nil
}

func (t *Tree) gatherSeparators(id format.PageID, minKeys int) ([][]byte, error) {
	keys, children, err := t.internalKeys(id)
	if err != nil || len(keys) == 0 {
		return keys, err
	}
	if len(keys) >= minKeys || t.height < 3 {
		return keys, nil
	}
	var all [][]byte
	for _, child := range children {
		part, err := t.separatorKeys(child)
		if err != nil {
			return nil, err
		}
		all = append(all, part...)
	}
	if len(all) > 0 {
		return all, nil
	}
	return keys, nil
}

func (t *Tree) separatorKeys(id format.PageID) ([][]byte, error) {
	keys, _, err := t.internalKeys(id)
	return keys, err
}

func (t *Tree) internalKeys(id format.PageID) ([][]byte, []format.PageID, error) {
	h, err := t.pin(id)
	if err != nil {
		return nil, nil, err
	}
	if h.Page().Type() != format.PageTypeBTreeInternal {
		_ = release(h, false)
		return nil, nil, nil
	}
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return nil, nil, err
	}
	ents, err := collectInternals(h.Page())
	if err != nil {
		_ = release(h, false)
		return nil, nil, err
	}
	if err := release(h, false); err != nil {
		return nil, nil, err
	}
	keys := make([][]byte, len(ents))
	children := make([]format.PageID, 0, len(ents)+1)
	if hdr.leftmost != 0 {
		children = append(children, hdr.leftmost)
	}
	for i, e := range ents {
		keys[i] = e.key
		children = append(children, e.child)
	}
	return keys, children, nil
}

func pickEvenKeys(keys [][]byte, want int) [][]byte {
	if want <= 0 || len(keys) == 0 {
		return nil
	}
	if want >= len(keys) {
		return keys
	}
	out := make([][]byte, 0, want)
	step := float64(len(keys)+1) / float64(want+1)
	for i := 1; i <= want; i++ {
		idx := int(step*float64(i)+0.5) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(keys) {
			idx = len(keys) - 1
		}
		if len(out) > 0 && compare(out[len(out)-1], keys[idx]) >= 0 {
			continue
		}
		out = append(out, keys[idx])
	}
	return out
}

// Count returns the number of visible keys in [start, end).
func (t *Tree) countLiveLocked() (int64, error) {
	id, err := t.leftmostLeaf()
	if err != nil {
		return 0, err
	}
	var n int64
	for id != 0 {
		h, err := t.pin(id)
		if err != nil {
			return 0, err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return 0, err
		}
		hdr, err := readHeader(h.Page())
		if err != nil {
			_ = release(h, false)
			return 0, err
		}
		live := h.Page().LiveSlots() - 1
		if live > 0 {
			n += int64(live)
		}
		next := hdr.next
		if err := release(h, false); err != nil {
			return 0, err
		}
		id = next
	}
	return n, nil
}

func (t *Tree) rangeLiveLocked(fn func(key, value []byte) error) error {
	id, err := t.leftmostLeaf()
	if err != nil {
		return err
	}
	for id != 0 {
		h, err := t.pin(id)
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
		err = forEachSlot(h.Page(), func(_ uint16, rec []byte) error {
			k, v, err := decodeLeaf(rec)
			if err != nil {
				return err
			}
			ver, has, err := row.Inspect(v)
			if err != nil {
				return err
			}
			payload := v
			if has {
				payload = ver.Payload
			}
			return fn(k, payload)
		})
		next := hdr.next
		if rerr := release(h, false); rerr != nil && err == nil {
			err = rerr
		}
		if err != nil {
			return err
		}
		id = next
	}
	return nil
}

func (t *Tree) Count(start, end []byte) (int64, error) {
	if t == nil {
		return 0, nerr.New(nerr.InvalidArgument, "btree.Count", "nil tree")
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rangeCount(start, end, t.readSnapshot(), false)
}

func (t *Tree) rangeCount(start, end []byte, snap txn.Snapshot, skipVis bool) (int64, error) {
	if start != nil {
		if err := checkKey(start); err != nil {
			return 0, err
		}
		start = copyBytes(start)
	}
	if end != nil {
		if err := checkKey(end); err != nil {
			return 0, err
		}
		end = copyBytes(end)
	}
	if start != nil && end != nil && compare(start, end) >= 0 {
		return 0, nil
	}

	var id format.PageID
	var err error
	if start == nil {
		id, err = t.leftmostLeaf()
	} else {
		var path []format.PageID
		path, err = t.descend(start)
		if err == nil {
			id = path[len(path)-1]
		}
	}
	if err != nil {
		return 0, err
	}

	var n int64
	for id != 0 {
		h, err := t.pin(id)
		if err != nil {
			return 0, err
		}
		if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return 0, err
		}
		hdr, err := readHeader(h.Page())
		if err != nil {
			_ = release(h, false)
			return 0, err
		}
		var pastEnd bool
		err = forEachSlot(h.Page(), func(_ uint16, rec []byte) error {
			k, v, err := decodeLeaf(rec)
			if err != nil {
				return err
			}
			if start != nil && compare(k, start) < 0 {
				return nil
			}
			if end != nil && compare(k, end) >= 0 {
				pastEnd = true
				return nil
			}
			if !skipVis {
				vis, err := t.rowVisible(v, snap)
				if err != nil {
					return err
				}
				if !vis {
					return nil
				}
			}
			n++
			return nil
		})
		next := hdr.next
		if rerr := release(h, false); rerr != nil && err == nil {
			err = rerr
		}
		if err != nil {
			return 0, err
		}
		if pastEnd {
			return n, nil
		}
		id = next
	}
	return n, nil
}
