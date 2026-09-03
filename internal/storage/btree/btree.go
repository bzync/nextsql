package btree

import (
	"sync"
	"sync/atomic"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/buffer"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

// Tree is a clustered B+Tree. Leaves hold row representations (key + value).
// The superblock primary tree has meta == 0. User tables and secondary
// indexes are detached trees whose root/height live on a slotted meta page.
type Tree struct {
	eng    *storage.Engine
	mu     sync.RWMutex
	root   format.PageID
	height int
	meta   format.PageID

	// hintRight/hintMax speed sequential appends (bulk load). Cleared on split/merge.
	hintRight  format.PageID
	hintMax    []byte
	hintParent format.PageID

	// liveRows is the number of live keys in this process-created tree.
	// liveKnown is false after Open of an existing tree until a full count.
	liveRows  int64
	liveKnown bool

	// name is an optional introspection label (typically a table name),
	// set by SetName and read by lock acquisition to tag held locks for
	// system.locks. Deliberately its own atomic, not guarded by mu: it is
	// set once (idempotently) by executor resolver code and read on every
	// lock acquisition, which must not risk nesting under mu.
	name atomic.Pointer[string]
}

// SetName labels this tree for introspection (system.locks) — typically a
// table name. Idempotent: the first non-empty name wins, so later calls
// (e.g. a cache re-resolving the same tree) are no-ops. Safe to call
// concurrently. A tree with no name reports "" from Name.
func (t *Tree) SetName(name string) {
	if t == nil || name == "" {
		return
	}
	t.name.CompareAndSwap(nil, &name)
}

// Name returns the label set by SetName, or "" if none was set.
func (t *Tree) Name() string {
	if t == nil {
		return ""
	}
	p := t.name.Load()
	if p == nil {
		return ""
	}
	return *p
}

// Create allocates an empty clustered tree and records it in the superblock.
func Create(eng *storage.Engine) (*Tree, error) {
	if eng == nil {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Create", "nil engine")
	}
	if root, _ := eng.PrimaryTree(); root != 0 {
		return nil, nerr.New(nerr.AlreadyExists, "btree.Create", "primary tree already exists")
	}
	if err := eng.BeginWrite(); err != nil {
		return nil, err
	}
	h, err := eng.NewPage(format.PageTypeBTreeLeaf)
	if err != nil {
		_ = eng.Rollback()
		return nil, err
	}
	if err := initNode(h.Page(), nodeHeader{}); err != nil {
		_ = h.Release(false)
		_ = eng.Rollback()
		return nil, err
	}
	root := h.ID()
	if err := h.Release(true); err != nil {
		_ = eng.Rollback()
		return nil, err
	}
	if err := eng.NoteTree(root, 1); err != nil {
		_ = eng.Rollback()
		return nil, err
	}
	if err := eng.Commit(); err != nil {
		return nil, err
	}
	return &Tree{eng: eng, root: root, height: 1, liveKnown: true}, nil
}

// CreateDetached allocates an empty tree that is not the superblock primary.
// Caller must have an active write transaction so pages roll back with it.
func CreateDetached(eng *storage.Engine) (*Tree, error) {
	if eng == nil {
		return nil, nerr.New(nerr.InvalidArgument, "btree.CreateDetached", "nil engine")
	}
	h, err := eng.NewPage(format.PageTypeBTreeLeaf)
	if err != nil {
		return nil, err
	}
	if err := initNode(h.Page(), nodeHeader{}); err != nil {
		_ = h.Release(false)
		return nil, err
	}
	root := h.ID()
	if err := h.Release(true); err != nil {
		return nil, err
	}
	mh, err := eng.NewPage(format.PageTypeSlotted)
	if err != nil {
		return nil, err
	}
	if _, err := mh.Page().Insert(encodeTreeMeta(root, 1)); err != nil {
		_ = mh.Release(false)
		return nil, err
	}
	meta := mh.ID()
	if err := mh.Release(true); err != nil {
		return nil, err
	}
	return &Tree{eng: eng, root: root, height: 1, meta: meta, liveKnown: true}, nil
}

// OpenDetached loads a non-primary tree from its meta page.
func OpenDetached(eng *storage.Engine, meta format.PageID) (*Tree, error) {
	if eng == nil {
		return nil, nerr.New(nerr.InvalidArgument, "btree.OpenDetached", "nil engine")
	}
	if meta == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "btree.OpenDetached", "meta page is required")
	}
	root, height, err := readTreeMeta(eng, meta)
	if err != nil {
		return nil, err
	}
	if root == 0 || height == 0 {
		return nil, nerr.New(nerr.Corruption, "btree.OpenDetached", "empty tree meta")
	}
	h, err := eng.Pin(root)
	if err != nil {
		return nil, err
	}
	typ := h.Page().Type()
	if err := h.Release(false); err != nil {
		return nil, err
	}
	if height == 1 && typ != format.PageTypeBTreeLeaf {
		return nil, nerr.New(nerr.Corruption, "btree.OpenDetached", "height-1 root is not a leaf")
	}
	if height > 1 && typ != format.PageTypeBTreeInternal {
		return nil, nerr.New(nerr.Corruption, "btree.OpenDetached", "internal root has wrong page type")
	}
	return &Tree{eng: eng, root: root, height: int(height), meta: meta}, nil
}

// Open loads the primary clustered tree from the superblock.
func Open(eng *storage.Engine) (*Tree, error) {
	if eng == nil {
		return nil, nerr.New(nerr.InvalidArgument, "btree.Open", "nil engine")
	}
	root, height := eng.PrimaryTree()
	if root == 0 || height == 0 {
		return nil, nerr.New(nerr.NotFound, "btree.Open", "no primary tree")
	}
	h, err := eng.Pin(root)
	if err != nil {
		return nil, err
	}
	typ := h.Page().Type()
	if err := h.Release(false); err != nil {
		return nil, err
	}
	if height == 1 && typ != format.PageTypeBTreeLeaf {
		return nil, nerr.New(nerr.Corruption, "btree.Open", "height-1 root is not a leaf")
	}
	if height > 1 && typ != format.PageTypeBTreeInternal {
		return nil, nerr.New(nerr.Corruption, "btree.Open", "internal root has wrong page type")
	}
	return &Tree{eng: eng, root: root, height: int(height)}, nil
}

func (t *Tree) Root() format.PageID {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.root
}

func (t *Tree) Height() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.height
}

func (t *Tree) Meta() format.PageID {
	if t == nil {
		return 0
	}
	return t.meta
}

func (t *Tree) Engine() *storage.Engine {
	if t == nil {
		return nil
	}
	return t.eng
}

func (t *Tree) persist() error {
	if t.meta == 0 {
		return t.eng.NoteTree(t.root, uint16(t.height))
	}
	return t.writeMeta()
}

func (t *Tree) addLive(n int64) {
	if t != nil && t.liveKnown {
		t.liveRows += n
	}
}

func (t *Tree) apply(fn func() error) error {
	snapR, snapH, snapL, snapK := t.root, t.height, t.liveRows, t.liveKnown
	if err := t.eng.BeginWrite(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if !wal.IsCrash(err) {
			_ = t.eng.Rollback()
		}
		t.root, t.height, t.liveRows, t.liveKnown = snapR, snapH, snapL, snapK
		return err
	}
	if err := t.persist(); err != nil {
		if !wal.IsCrash(err) {
			_ = t.eng.Rollback()
		}
		t.root, t.height, t.liveRows, t.liveKnown = snapR, snapH, snapL, snapK
		return err
	}
	if err := t.eng.Commit(); err != nil {
		t.root, t.height, t.liveRows, t.liveKnown = snapR, snapH, snapL, snapK
		return err
	}
	return nil
}

// WriteTxn is an explicit multi-operation write transaction (snapshot isolation).
type WriteTxn struct {
	inner *Txn
}

func (t *Tree) Begin() (*WriteTxn, error) {
	tx, err := t.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		return nil, err
	}
	return &WriteTxn{inner: tx}, nil
}

func (tx *WriteTxn) Insert(key, value []byte) error {
	if tx == nil || tx.inner == nil {
		return nerr.New(nerr.InvalidArgument, "btree.WriteTxn.Insert", "nil transaction")
	}
	return tx.inner.Insert(key, value)
}

func (tx *WriteTxn) Delete(key []byte) error {
	if tx == nil || tx.inner == nil {
		return nerr.New(nerr.InvalidArgument, "btree.WriteTxn.Delete", "nil transaction")
	}
	return tx.inner.Delete(key)
}

func (tx *WriteTxn) Update(key, value []byte) error {
	if tx == nil || tx.inner == nil {
		return nerr.New(nerr.InvalidArgument, "btree.WriteTxn.Update", "nil transaction")
	}
	return tx.inner.Update(key, value)
}

func (tx *WriteTxn) Commit() error {
	if tx == nil || tx.inner == nil {
		return nerr.New(nerr.InvalidArgument, "btree.WriteTxn.Commit", "nil transaction")
	}
	return tx.inner.Commit()
}

func (tx *WriteTxn) Rollback() error {
	if tx == nil || tx.inner == nil {
		return nil
	}
	return tx.inner.Rollback()
}

func (t *Tree) pin(id format.PageID) (*buffer.Handle, error) {
	return t.eng.Pin(id)
}

func release(h *buffer.Handle, dirty bool) error {
	if h == nil {
		return nil
	}
	return h.Release(dirty)
}

func expectType(p *page.Page, want format.PageType) error {
	if p.Type() != want {
		return nerr.New(nerr.Corruption, "btree.expectType", "unexpected page type")
	}
	return nil
}
