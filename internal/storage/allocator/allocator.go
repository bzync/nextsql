package allocator

import (
	"sync"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

const idsPerMetaPage = 1024

// Allocator assigns page IDs. State is persisted in the superblock and freelist pages.
type Allocator struct {
	mu    sync.Mutex
	file  *file.Manager
	next  format.PageID
	free  []format.PageID
	set   map[format.PageID]struct{}
	meta  []format.PageID
	dirty bool
}

// State is a stable allocator snapshot for diagnostics. Returned slices do
// not alias allocator memory.
type State struct {
	Next     format.PageID
	Free     []format.PageID
	Metadata []format.PageID
}

func Open(f *file.Manager) (*Allocator, error) {
	a := &Allocator{file: f}
	if err := a.reloadLocked(); err != nil {
		return nil, err
	}
	return a, nil
}

// Reload rereads allocator state from the superblock and freelist pages.
func (a *Allocator) Reload() error {
	if a == nil {
		return nerr.New(nerr.InvalidArgument, "allocator.Reload", "nil allocator")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reloadLocked()
}

func (a *Allocator) reloadLocked() error {
	next, head, count := a.file.AllocState()
	a.next = next
	a.free = a.free[:0]
	a.meta = a.meta[:0]
	a.set = make(map[format.PageID]struct{})
	if head == 0 {
		if count != 0 {
			return nerr.New(nerr.Corruption, "allocator.Open", "freelist count without head")
		}
		return nil
	}
	seen := map[format.PageID]struct{}{}
	cur := head
	for cur != 0 {
		if _, ok := seen[cur]; ok {
			return nerr.New(nerr.Corruption, "allocator.Open", "freelist cycle")
		}
		seen[cur] = struct{}{}
		a.meta = append(a.meta, cur)
		raw, err := a.file.ReadLogical(cur)
		if err != nil {
			return err
		}
		p, err := page.ParseID(raw, cur)
		if err != nil {
			return err
		}
		if p.Type() != format.PageTypeFreeList {
			return nerr.New(nerr.Corruption, "allocator.Open", "freelist page has wrong type")
		}
		for i := 0; i < p.SlotCount(); i++ {
			rec, err := p.Get(uint16(i))
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					continue
				}
				return err
			}
			if len(rec) != 8 {
				return nerr.New(nerr.Corruption, "allocator.Open", "freelist record has wrong size")
			}
			id := format.PageID(encoding.U64(rec, 0))
			if err := id.UserData(); err != nil {
				return err
			}
			if _, dup := a.set[id]; dup {
				return nerr.New(nerr.Corruption, "allocator.Open", "duplicate free page id")
			}
			a.free = append(a.free, id)
			a.set[id] = struct{}{}
		}
		cur = format.PageID(p.TxnMeta())
	}
	if uint64(len(a.free)) != count {
		return nerr.New(nerr.Corruption, "allocator.Open", "freelist count mismatch")
	}
	a.dirty = false
	return nil
}

// Flush writes in-memory alloc state to the superblock and freelist pages.
func (a *Allocator) Flush() error {
	if a == nil {
		return nerr.New(nerr.InvalidArgument, "allocator.Flush", "nil allocator")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.dirty {
		return nil
	}
	if err := a.persist(); err != nil {
		return err
	}
	a.dirty = false
	return nil
}

func (a *Allocator) Alloc() (format.PageID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var id format.PageID
	if n := len(a.free); n > 0 {
		id = a.free[n-1]
		a.free = a.free[:n-1]
		delete(a.set, id)
	} else {
		id = a.next
		if err := id.UserData(); err != nil {
			return 0, err
		}
		a.next++
		if id == format.FirstAllocPageID || uint64(id)%1024 == 0 {
			_ = a.file.EnsureCapacity(a.next)
		}
	}
	a.dirty = true
	a.file.SetAllocStateMem(a.next, a.freeHeadLocked(), uint64(len(a.free)))
	return id, nil
}

func (a *Allocator) Free(id format.PageID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := id.UserData(); err != nil {
		return err
	}
	if id >= a.next {
		return nerr.New(nerr.InvalidArgument, "allocator.Free", "page id was never allocated")
	}
	if _, ok := a.set[id]; ok {
		return nerr.New(nerr.InvalidArgument, "allocator.Free", "page id is already free")
	}
	for _, m := range a.meta {
		if m == id {
			return nerr.New(nerr.InvalidArgument, "allocator.Free", "cannot free allocator metadata page")
		}
	}
	a.free = append(a.free, id)
	a.set[id] = struct{}{}
	a.dirty = true
	a.file.SetAllocStateMem(a.next, a.freeHeadLocked(), uint64(len(a.free)))
	return nil
}

func (a *Allocator) freeHeadLocked() format.PageID {
	if len(a.free) == 0 || len(a.meta) == 0 {
		return 0
	}
	return a.meta[0]
}

func (a *Allocator) Next() format.PageID {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.next
}

func (a *Allocator) FreeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.free)
}

func (a *Allocator) State() State {
	if a == nil {
		return State{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return State{
		Next:     a.next,
		Free:     append([]format.PageID(nil), a.free...),
		Metadata: append([]format.PageID(nil), a.meta...),
	}
}

type snapshot struct {
	next format.PageID
	free []format.PageID
	set  map[format.PageID]struct{}
	meta []format.PageID
}

func (a *Allocator) snapshot() snapshot {
	s := snapshot{
		next: a.next,
		free: append([]format.PageID(nil), a.free...),
		set:  make(map[format.PageID]struct{}, len(a.set)),
		meta: append([]format.PageID(nil), a.meta...),
	}
	for id := range a.set {
		s.set[id] = struct{}{}
	}
	return s
}

func (a *Allocator) restore(s snapshot) {
	a.next = s.next
	a.free = s.free
	a.set = s.set
	a.meta = s.meta
}

func (a *Allocator) persist() error {
	need := 0
	if len(a.free) > 0 {
		need = (len(a.free) + idsPerMetaPage - 1) / idsPerMetaPage
	}
	if need == 0 && len(a.meta) > 0 {
		need = 1
	}
	// Once allocated, keep every freelist metadata page linked. Silently
	// shortening the chain would itself orphan the unused metadata tail.
	if need < len(a.meta) {
		need = len(a.meta)
	}
	for len(a.meta) < need {
		id := a.next
		a.next++
		a.meta = append(a.meta, id)
	}
	var head format.PageID
	if need > 0 {
		head = a.meta[0]
	}
	offset := 0
	for i := 0; i < need; i++ {
		pid := a.meta[i]
		p := page.New(pid, format.PageTypeFreeList)
		end := offset + idsPerMetaPage
		if end > len(a.free) {
			end = len(a.free)
		}
		for _, id := range a.free[offset:end] {
			var rec [8]byte
			encoding.PutU64(rec[:], 0, uint64(id))
			if _, err := p.Insert(rec[:]); err != nil {
				return err
			}
		}
		var next format.PageID
		if i+1 < need {
			next = a.meta[i+1]
		}
		p.SetTxnMeta(format.TxnID(next))
		if err := a.file.WriteLogical(pid, p.Bytes()); err != nil {
			return err
		}
		offset = end
	}
	return a.file.SetAllocState(a.next, head, uint64(len(a.free)))
}
