package buffer

import (
	"sync"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

type Stats struct {
	Hits   uint64
	Misses uint64
}

// Hooks let the WAL observe dirty pages without importing the WAL package.
type Hooks interface {
	OnPin(id format.PageID, data []byte)
	OnInstall(id format.PageID)
	OnDirty(id format.PageID, data []byte) (format.LSN, error)
	AllowFlush(id format.PageID, lsn format.LSN) bool
}

type frame struct {
	occupied bool
	loading  bool
	id       format.PageID
	data     []byte
	pins     int
	dirty    bool
	ref      bool
}

// Handle is a pinned logical page. Release must be called exactly once.
type Handle struct {
	pool     *Pool
	frame    *frame
	page     *page.Page
	released bool
}

func (h *Handle) Page() *page.Page {
	if h == nil {
		return nil
	}
	return h.page
}

func (h *Handle) ID() format.PageID {
	return h.frame.id
}

func (h *Handle) Release(dirty bool) error {
	if h == nil || h.released {
		return nerr.New(nerr.InvalidArgument, "buffer.Handle.Release", "handle already released")
	}
	if dirty && h.pool.hooks != nil {
		lsn, err := h.pool.hooks.OnDirty(h.frame.id, h.page.Bytes())
		if err != nil {
			h.released = true
			_ = h.pool.release(h.frame, false)
			return err
		}
		if lsn != 0 {
			h.page.SetLSN(lsn)
		}
	}
	h.released = true
	return h.pool.release(h.frame, dirty)
}

type Pool struct {
	mu       sync.Mutex
	loadWait *sync.Cond
	file     *file.Manager
	hooks    Hooks
	frames   []*frame
	index    map[format.PageID]*frame
	clock    int
	hits     uint64
	misses   uint64
}

func (p *Pool) SetHooks(h Hooks) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hooks = h
}

func New(f *file.Manager, n int) (*Pool, error) {
	if f == nil {
		return nil, nerr.New(nerr.InvalidArgument, "buffer.New", "nil file manager")
	}
	if n < 1 {
		return nil, nerr.New(nerr.InvalidArgument, "buffer.New", "buffer pool must have at least one frame")
	}
	p := &Pool{
		file:   f,
		frames: make([]*frame, n),
		index:  make(map[format.PageID]*frame, n),
	}
	p.loadWait = sync.NewCond(&p.mu)
	for i := range p.frames {
		p.frames[i] = &frame{data: make([]byte, format.LogicalPageSize)}
	}
	return p, nil
}

func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{Hits: p.hits, Misses: p.misses}
}

func (p *Pool) Pin(id format.PageID) (*Handle, error) {
	if err := id.UserData(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	for {
		if fr, ok := p.index[id]; ok {
			if fr.loading {
				p.loadWait.Wait()
				continue
			}
			fr.pins++
			fr.ref = true
			p.hits++
			pg, err := page.WrapCached(fr.data)
			if err != nil {
				fr.pins--
				p.mu.Unlock()
				return nil, err
			}
			if p.hooks != nil {
				p.hooks.OnPin(id, fr.data)
			}
			p.mu.Unlock()
			return &Handle{pool: p, frame: fr, page: pg}, nil
		}
		p.misses++
		fr, err := p.evict()
		if err != nil {
			p.mu.Unlock()
			return nil, err
		}
		fr.occupied = true
		fr.loading = true
		fr.id = id
		fr.pins = 1
		fr.dirty = false
		fr.ref = true
		p.index[id] = fr
		p.mu.Unlock()

		err = p.file.ReadLogicalInto(id, fr.data)

		p.mu.Lock()
		if err != nil {
			p.abortLoad(fr)
			p.mu.Unlock()
			return nil, err
		}
		// ReadLogicalInto already authenticated the envelope and page id.
		pg, err := page.WrapCached(fr.data)
		if err != nil {
			p.abortLoad(fr)
			p.mu.Unlock()
			return nil, err
		}
		fr.loading = false
		p.loadWait.Broadcast()
		if p.hooks != nil {
			p.hooks.OnPin(id, fr.data)
		}
		p.mu.Unlock()
		return &Handle{pool: p, frame: fr, page: pg}, nil
	}
}

func (p *Pool) abortLoad(fr *frame) {
	delete(p.index, fr.id)
	fr.occupied = false
	fr.loading = false
	fr.id = 0
	fr.pins = 0
	fr.dirty = false
	fr.ref = false
	p.loadWait.Broadcast()
}

// Install inserts a newly allocated page that is not yet on disk.
func (p *Pool) Install(pg *page.Page) (*Handle, error) {
	if pg == nil {
		return nil, nerr.New(nerr.InvalidArgument, "buffer.Install", "nil page")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := pg.ID()
	if err := id.UserData(); err != nil {
		return nil, err
	}
	if _, exists := p.index[id]; exists {
		return nil, nerr.New(nerr.AlreadyExists, "buffer.Install", "page already cached")
	}
	fr, err := p.evict()
	if err != nil {
		return nil, err
	}
	copy(fr.data, pg.Bytes())
	wrapped, err := page.Wrap(fr.data)
	if err != nil {
		return nil, err
	}
	fr.occupied = true
	fr.id = id
	fr.pins = 1
	fr.dirty = true
	fr.ref = true
	p.index[id] = fr
	if p.hooks != nil {
		p.hooks.OnInstall(id)
	}
	return &Handle{pool: p, frame: fr, page: wrapped}, nil
}

// InstallNew allocates a frame and initializes an empty page in place.
func (p *Pool) InstallNew(id format.PageID, typ format.PageType) (*Handle, error) {
	if err := id.UserData(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.index[id]; exists {
		return nil, nerr.New(nerr.AlreadyExists, "buffer.InstallNew", "page already cached")
	}
	fr, err := p.evict()
	if err != nil {
		return nil, err
	}
	pg, err := page.NewIn(fr.data, id, typ)
	if err != nil {
		return nil, err
	}
	fr.occupied = true
	fr.id = id
	fr.pins = 1
	fr.dirty = true
	fr.ref = true
	p.index[id] = fr
	if p.hooks != nil {
		p.hooks.OnInstall(id)
	}
	return &Handle{pool: p, frame: fr, page: pg}, nil
}

// Drop evicts id without flushing. Used when the page has been freed.
func (p *Pool) Drop(id format.PageID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	fr, ok := p.index[id]
	if !ok {
		return nil
	}
	if fr.pins > 0 {
		return nerr.New(nerr.Internal, "buffer.Drop", "cannot drop a pinned page")
	}
	delete(p.index, fr.id)
	fr.occupied = false
	fr.id = 0
	fr.pins = 0
	fr.dirty = false
	fr.ref = false
	return nil
}

func (p *Pool) Restore(id format.PageID, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		fr, ok := p.index[id]
		if !ok {
			return nil
		}
		if !fr.loading {
			break
		}
		p.loadWait.Wait()
	}
	fr, ok := p.index[id]
	if !ok {
		return nil
	}
	if len(data) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidArgument, "buffer.Restore", "logical page has wrong size")
	}
	copy(fr.data, data)
	fr.dirty = false
	return nil
}

// CopyPage returns a snapshot of a cached logical page. The slice is
// detached from the frame. ok is false if the page is not in the pool.
func (p *Pool) CopyPage(id format.PageID) (data []byte, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fr, exists := p.index[id]
	if !exists || !fr.occupied || fr.loading {
		return nil, false
	}
	out := make([]byte, len(fr.data))
	copy(out, fr.data)
	return out, true
}

// CopyPageInto copies a cached logical page into dst, growing dst if needed.
func (p *Pool) CopyPageInto(id format.PageID, dst []byte) (data []byte, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fr, exists := p.index[id]
	if !exists || !fr.occupied || fr.loading {
		return nil, false
	}
	if cap(dst) < len(fr.data) {
		dst = make([]byte, len(fr.data))
	} else {
		dst = dst[:len(fr.data)]
	}
	copy(dst, fr.data)
	return dst, true
}

// StampLSN writes lsn into a cached page, if present. No-op when evicted.
func (p *Pool) StampLSN(id format.PageID, lsn format.LSN) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if fr, ok := p.index[id]; ok && fr.occupied && !fr.loading {
		page.StampLSN(fr.data, lsn)
	}
}

// Replace writes a replica page image to disk and updates a cached copy.
// It does not invoke dirty hooks (those belong to the originating writer).
func (p *Pool) Replace(id format.PageID, data []byte) error {
	if err := id.UserData(); err != nil {
		return err
	}
	if len(data) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidArgument, "buffer.Replace", "logical page has wrong size")
	}
	p.mu.Lock()
	for {
		fr, ok := p.index[id]
		if !ok || !fr.loading {
			break
		}
		p.loadWait.Wait()
	}
	if fr, ok := p.index[id]; ok {
		copy(fr.data, data)
		fr.dirty = false
	}
	p.mu.Unlock()
	return p.file.WriteLogical(id, data)
}

func (p *Pool) Flush(id format.PageID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	fr, ok := p.index[id]
	if !ok || !fr.dirty {
		return nil
	}
	return p.writeFrame(fr)
}

func (p *Pool) FlushAll() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, fr := range p.frames {
		if fr.occupied && fr.dirty {
			if !p.canFlush(fr) {
				continue
			}
			if err := p.writeFrame(fr); err != nil {
				return err
			}
		}
	}
	return p.file.Sync()
}

func (p *Pool) release(fr *frame, dirty bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if fr.pins <= 0 {
		return nerr.New(nerr.Internal, "buffer.release", "pin count underflow")
	}
	if dirty {
		fr.dirty = true
	}
	fr.pins--
	return nil
}

func (p *Pool) evict() (*frame, error) {
	n := len(p.frames)
	for i := 0; i < n*2; i++ {
		fr := p.frames[p.clock]
		p.clock = (p.clock + 1) % n
		if !fr.occupied {
			return fr, nil
		}
		if fr.pins > 0 || fr.loading {
			continue
		}
		if fr.ref {
			fr.ref = false
			continue
		}
		if fr.dirty && !p.canFlush(fr) {
			continue
		}
		if err := p.drop(fr); err != nil {
			return nil, err
		}
		return fr, nil
	}
	// A no-steal transaction may have released the page handles while its
	// dirty images are still WAL-undurable. Those frames are not technically
	// pinned, but they are equally ineligible for eviction. Keep the error
	// explicit so callers do not misdiagnose a capacity/no-steal exhaustion as
	// a leaked pin.
	return nil, nerr.New(nerr.Exhausted, "buffer.evict", "no evictable frames: pages are pinned or WAL-undurable dirty")
}

func (p *Pool) drop(fr *frame) error {
	if !fr.occupied {
		return nil
	}
	if fr.dirty {
		if err := p.writeFrame(fr); err != nil {
			return err
		}
	}
	delete(p.index, fr.id)
	fr.occupied = false
	fr.loading = false
	fr.id = 0
	fr.pins = 0
	fr.dirty = false
	fr.ref = false
	return nil
}

func (p *Pool) canFlush(fr *frame) bool {
	if p.hooks == nil {
		return true
	}
	return p.hooks.AllowFlush(fr.id, page.LSNOf(fr.data))
}

func (p *Pool) writeFrame(fr *frame) error {
	if !p.canFlush(fr) {
		return nerr.New(nerr.Internal, "buffer.writeFrame", "refusing to flush a page whose WAL is not durable")
	}
	if err := p.file.WriteLogical(fr.id, fr.data); err != nil {
		return err
	}
	fr.dirty = false
	return nil
}
