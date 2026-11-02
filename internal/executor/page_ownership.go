package executor

import (
	"sort"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/allocator"
	"github.com/bzync/nextsql/internal/storage/format"
)

// PageConflict is one page with an ownership claim it must not have.
type PageConflict struct {
	Page   format.PageID `json:"page"`
	Owners []string      `json:"owners"`
}

// BrokenTree is a tree whose ownership walk or structure check failed.
type BrokenTree struct {
	Owner string `json:"owner"`
	Error string `json:"error"`
}

// PageOwnershipReport compares every page's tree owners with the allocator.
// It holds page IDs and object names only, never page contents.
type PageOwnershipReport struct {
	AllocatedPages uint64 `json:"allocated_pages"`
	OwnedPages     uint64 `json:"owned_pages"`
	FreePages      uint64 `json:"free_pages"`
	MetadataPages  uint64 `json:"metadata_pages"`
	// FreeAndOwned pages are live but on the freelist: a later allocation
	// would replace them with an empty page.
	FreeAndOwned []PageConflict `json:"free_and_owned"`
	// MetadataAndOwned pages hold both freelist records and tree data.
	MetadataAndOwned []PageConflict `json:"metadata_and_owned"`
	// SharedPages are claimed by more than one tree.
	SharedPages []PageConflict `json:"shared_pages"`
	// OutOfRange pages are linked into a tree at or above the high-water mark.
	OutOfRange  []PageConflict `json:"out_of_range"`
	BrokenTrees []BrokenTree   `json:"broken_trees"`
	// Orphans are allocated pages nothing refers to: wasted space, not damage.
	// It is nil when a broken tree makes reachability unknown.
	Orphans []format.PageID `json:"orphans"`
}

// Clean reports whether no page has a conflicting owner and every tree walked.
func (r *PageOwnershipReport) Clean() bool {
	return len(r.FreeAndOwned) == 0 && len(r.MetadataAndOwned) == 0 && len(r.SharedPages) == 0 &&
		len(r.OutOfRange) == 0 && len(r.BrokenTrees) == 0
}

type pageOwner interface {
	OwnedPages() ([]format.PageID, error)
	Check() error
}

type labeledTree struct {
	label string
	tree  pageOwner
}

// ownedTrees lists the catalog tree and every catalog-owned detached tree,
// in a stable order.
func (db *DB) ownedTrees() []labeledTree {
	db.mu.RLock()
	defer db.mu.RUnlock()
	trees := make([]labeledTree, 0, 1+len(db.heaps)+len(db.vecs)+len(db.idxs)+len(db.partHeaps)+len(db.partVecs)+len(db.partIdxs))
	trees = append(trees, labeledTree{"catalog", db.CatTree})
	for name, tr := range db.heaps {
		trees = append(trees, labeledTree{"heap " + name, tr})
	}
	for name, tr := range db.vecs {
		trees = append(trees, labeledTree{"vector " + name, tr})
	}
	for name, tr := range db.idxs {
		trees = append(trees, labeledTree{"index " + name, tr})
	}
	for name, tr := range db.partHeaps {
		trees = append(trees, labeledTree{"partition heap " + name, tr})
	}
	for name, tr := range db.partVecs {
		trees = append(trees, labeledTree{"partition vector " + name, tr})
	}
	for name, tr := range db.partIdxs {
		trees = append(trees, labeledTree{"partition index " + name, tr})
	}
	sort.Slice(trees, func(i, j int) bool { return trees[i].label < trees[j].label })
	return trees
}

// CheckPageOwnership reports pages whose owners conflict, the damage a
// reissued page leaves behind. It frees and repairs nothing. A running writer
// can move pages between the walks, so a verdict is only exact on a quiet
// database; `nextsql check-pages` runs it offline for that reason.
func (db *DB) CheckPageOwnership() (*PageOwnershipReport, error) {
	if db == nil || db.Eng == nil || db.Eng.Alloc == nil || db.CatTree == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CheckPageOwnership", "database is not open")
	}
	db.applyMu.Lock()
	defer db.applyMu.Unlock()
	return analyzePageOwnership(db.ownedTrees(), db.Eng.Alloc.State()), nil
}

func analyzePageOwnership(trees []labeledTree, state allocator.State) *PageOwnershipReport {
	rep := &PageOwnershipReport{
		FreePages:     uint64(len(state.Free)),
		MetadataPages: uint64(len(state.Metadata)),
	}
	if state.Next > format.FirstAllocPageID {
		rep.AllocatedPages = uint64(state.Next - format.FirstAllocPageID)
	}
	// owners[id] is the tree index + 1 of the page's first owner. The high-water
	// mark comes from the authenticated superblock and bounds this slice.
	owners := make([]int32, state.Next)
	shared := make(map[format.PageID][]string)
	outOfRange := make(map[format.PageID][]string)
	for i, tr := range trees {
		pages, err := tr.tree.OwnedPages()
		if err != nil {
			rep.BrokenTrees = append(rep.BrokenTrees, BrokenTree{Owner: tr.label, Error: err.Error()})
			continue
		}
		// The ownership walk follows parent links only. Scans also follow leaf
		// sibling links, which a reissued page can break on its own.
		if err := tr.tree.Check(); err != nil {
			rep.BrokenTrees = append(rep.BrokenTrees, BrokenTree{Owner: tr.label, Error: err.Error()})
		}
		for _, id := range pages {
			switch {
			case id >= state.Next:
				outOfRange[id] = append(outOfRange[id], tr.label)
			case owners[id] == 0:
				owners[id] = int32(i + 1)
				rep.OwnedPages++
			case shared[id] == nil:
				shared[id] = []string{trees[owners[id]-1].label, tr.label}
			default:
				shared[id] = append(shared[id], tr.label)
			}
		}
	}
	ownersOf := func(id format.PageID) []string {
		if names, ok := shared[id]; ok {
			return names
		}
		return []string{trees[owners[id]-1].label}
	}
	claimed := func(ids []format.PageID) []PageConflict {
		out := make([]PageConflict, 0)
		for _, id := range ids {
			if id < state.Next && owners[id] != 0 {
				out = append(out, PageConflict{Page: id, Owners: ownersOf(id)})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Page < out[j].Page })
		return out
	}
	rep.FreeAndOwned = claimed(state.Free)
	rep.MetadataAndOwned = claimed(state.Metadata)
	rep.SharedPages = conflictsOf(shared)
	rep.OutOfRange = conflictsOf(outOfRange)
	if len(rep.BrokenTrees) == 0 {
		rep.Orphans = unreferencedPages(owners, state)
	}
	return rep
}

func conflictsOf(pages map[format.PageID][]string) []PageConflict {
	out := make([]PageConflict, 0, len(pages))
	for id, names := range pages {
		out = append(out, PageConflict{Page: id, Owners: names})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Page < out[j].Page })
	return out
}

func unreferencedPages(owners []int32, state allocator.State) []format.PageID {
	allocatorOwned := make(map[format.PageID]struct{}, len(state.Free)+len(state.Metadata))
	for _, id := range state.Free {
		allocatorOwned[id] = struct{}{}
	}
	for _, id := range state.Metadata {
		allocatorOwned[id] = struct{}{}
	}
	out := make([]format.PageID, 0)
	for id := format.FirstAllocPageID; id < state.Next; id++ {
		if owners[id] != 0 {
			continue
		}
		if _, ok := allocatorOwned[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}
