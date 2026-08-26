package btree

import (
	"fmt"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// Check walks the tree and fails closed on a broken invariant.
func (t *Tree) Check() error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Check", "nil tree")
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.checkLocked()
}

func (t *Tree) checkLocked() error {
	if t.height < 1 {
		return nerr.New(nerr.Corruption, "btree.Check", "invalid height")
	}
	root, height := t.eng.PrimaryTree()
	if root != t.root || int(height) != t.height {
		return nerr.New(nerr.Corruption, "btree.Check", "in-memory root does not match superblock")
	}
	seen := map[format.PageID]struct{}{}
	minKey, maxKey, leaves, err := t.walk(t.root, t.height, nil, nil, seen)
	if err != nil {
		return err
	}
	_, _, _ = minKey, maxKey, leaves
	return t.checkLeafChain(leaves)
}

func (t *Tree) walk(id format.PageID, height int, lo, hi []byte, seen map[format.PageID]struct{}) (minKey, maxKey []byte, leaves []format.PageID, err error) {
	if _, dup := seen[id]; dup {
		return nil, nil, nil, nerr.New(nerr.Corruption, "btree.Check", "page cycle")
	}
	seen[id] = struct{}{}
	h, err := t.pin(id)
	if err != nil {
		return nil, nil, nil, err
	}
	p := h.Page()
	if height == 1 {
		if err := expectType(p, format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return nil, nil, nil, err
		}
		ents, err := collectLeaves(p)
		if err != nil {
			_ = release(h, false)
			return nil, nil, nil, err
		}
		if err := release(h, false); err != nil {
			return nil, nil, nil, err
		}
		if id != t.root && len(ents) == 0 {
			return nil, nil, nil, nerr.New(nerr.Corruption, "btree.Check", "empty non-root leaf")
		}
		for i := 1; i < len(ents); i++ {
			if compare(ents[i-1].key, ents[i].key) >= 0 {
				return nil, nil, nil, nerr.New(nerr.Corruption, "btree.Check", "leaf keys are not strictly increasing")
			}
		}
		if len(ents) == 0 {
			return nil, nil, []format.PageID{id}, nil
		}
		if lo != nil && compare(ents[0].key, lo) < 0 {
			return nil, nil, nil, nerr.New(nerr.Corruption, "btree.Check", "leaf key is below separator")
		}
		if hi != nil && compare(ents[len(ents)-1].key, hi) >= 0 {
			return nil, nil, nil, nerr.New(nerr.Corruption, "btree.Check", "leaf key is not below high separator")
		}
		return ents[0].key, ents[len(ents)-1].key, []format.PageID{id}, nil
	}

	if err := expectType(p, format.PageTypeBTreeInternal); err != nil {
		_ = release(h, false)
		return nil, nil, nil, err
	}
	hdr, err := readHeader(p)
	if err != nil {
		_ = release(h, false)
		return nil, nil, nil, err
	}
	ents, err := collectInternals(p)
	if err != nil {
		_ = release(h, false)
		return nil, nil, nil, err
	}
	if err := release(h, false); err != nil {
		return nil, nil, nil, err
	}
	if hdr.leftmost == 0 {
		return nil, nil, nil, nerr.New(nerr.Corruption, "btree.Check", "internal node missing leftmost child")
	}
	// A non-root internal may have only leftmost (no separators) after
	// deletes. Collapsing it into the grandparent would unbalance the tree.
	for i := 1; i < len(ents); i++ {
		if compare(ents[i-1].key, ents[i].key) >= 0 {
			return nil, nil, nil, nerr.New(nerr.Corruption, "btree.Check", "internal separators are not strictly increasing")
		}
	}

	children := make([]format.PageID, 0, len(ents)+1)
	lowers := make([][]byte, 0, len(ents)+1)
	uppers := make([][]byte, 0, len(ents)+1)
	children = append(children, hdr.leftmost)
	lowers = append(lowers, lo)
	if len(ents) == 0 {
		uppers = append(uppers, hi)
	} else {
		uppers = append(uppers, ents[0].key)
		for i, e := range ents {
			children = append(children, e.child)
			lowers = append(lowers, e.key)
			if i+1 < len(ents) {
				uppers = append(uppers, ents[i+1].key)
			} else {
				uppers = append(uppers, hi)
			}
		}
	}

	var allLeaves []format.PageID
	for i, child := range children {
		cmin, cmax, cleaves, err := t.walk(child, height-1, lowers[i], uppers[i], seen)
		if err != nil {
			return nil, nil, nil, err
		}
		allLeaves = append(allLeaves, cleaves...)
		if cmin == nil {
			continue
		}
		if minKey == nil || compare(cmin, minKey) < 0 {
			minKey = cmin
		}
		if maxKey == nil || compare(cmax, maxKey) > 0 {
			maxKey = cmax
		}
	}
	return minKey, maxKey, allLeaves, nil
}

func (t *Tree) checkLeafChain(leaves []format.PageID) error {
	if len(leaves) == 0 {
		return nerr.New(nerr.Corruption, "btree.Check", "tree has no leaves")
	}
	var prev format.PageID
	for i, id := range leaves {
		h, err := t.pin(id)
		if err != nil {
			return err
		}
		hdr, err := readHeader(h.Page())
		if err != nil {
			_ = release(h, false)
			return err
		}
		if err := release(h, false); err != nil {
			return err
		}
		if hdr.prev != prev {
			return nerr.New(nerr.Corruption, "btree.Check", fmt.Sprintf("leaf %d prev pointer got %d want %d", id, hdr.prev, prev))
		}
		var wantNext format.PageID
		if i+1 < len(leaves) {
			wantNext = leaves[i+1]
		}
		if hdr.next != wantNext {
			return nerr.New(nerr.Corruption, "btree.Check", fmt.Sprintf("leaf %d next pointer", id))
		}
		prev = id
	}
	return nil
}
