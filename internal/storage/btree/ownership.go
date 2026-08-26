package btree

import (
	"sort"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// OwnedPages returns every page owned by this tree in ascending order. For a
// detached tree the metadata page is included. The walk validates page types,
// height, child IDs, and cycles so reclamation fails closed.
func (t *Tree) OwnedPages() ([]format.PageID, error) {
	if t == nil {
		return nil, nerr.New(nerr.InvalidArgument, "btree.OwnedPages", "nil tree")
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.root == 0 || t.height < 1 {
		return nil, nerr.New(nerr.Corruption, "btree.OwnedPages", "invalid tree metadata")
	}
	seen := make(map[format.PageID]struct{})
	if t.meta != 0 {
		seen[t.meta] = struct{}{}
	}
	if err := t.collectOwned(t.root, t.height, seen); err != nil {
		return nil, err
	}
	out := make([]format.PageID, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (t *Tree) collectOwned(id format.PageID, height int, seen map[format.PageID]struct{}) error {
	if err := id.UserData(); err != nil {
		return nerr.Wrap(nerr.Corruption, "btree.OwnedPages", "invalid child page", err)
	}
	if _, exists := seen[id]; exists {
		return nerr.New(nerr.Corruption, "btree.OwnedPages", "page cycle or duplicate child")
	}
	seen[id] = struct{}{}
	h, err := t.pin(id)
	if err != nil {
		return err
	}
	p := h.Page()
	if height == 1 {
		if err := expectType(p, format.PageTypeBTreeLeaf); err != nil {
			_ = release(h, false)
			return err
		}
		return release(h, false)
	}
	if err := expectType(p, format.PageTypeBTreeInternal); err != nil {
		_ = release(h, false)
		return err
	}
	hdr, err := readHeader(p)
	if err != nil {
		_ = release(h, false)
		return err
	}
	ents, err := collectInternals(p)
	if err != nil {
		_ = release(h, false)
		return err
	}
	if err := release(h, false); err != nil {
		return err
	}
	if hdr.leftmost == 0 {
		return nerr.New(nerr.Corruption, "btree.OwnedPages", "internal node missing leftmost child")
	}
	if err := t.collectOwned(hdr.leftmost, height-1, seen); err != nil {
		return err
	}
	for _, ent := range ents {
		if err := t.collectOwned(ent.child, height-1, seen); err != nil {
			return err
		}
	}
	return nil
}
