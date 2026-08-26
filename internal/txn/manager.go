package txn

import (
	"sync"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// Handle is one live transaction.
type Handle struct {
	ID       format.TxnID
	Iso      Isolation
	Snap     Snapshot
	ReadOnly bool
}

// Manager allocates transaction ids, snapshots, and the lock table.
type Manager struct {
	mu       sync.Mutex
	next     format.TxnID
	nextRead uint64
	active   map[format.TxnID]*Handle
	readers  map[format.TxnID]*Handle
	status   map[format.TxnID]Status
	Locks    *LockManager
	cv       *sync.Cond
}

func NewManager(next format.TxnID) *Manager {
	if next == 0 {
		next = 1
	}
	m := &Manager{
		next:     next,
		nextRead: 1,
		active:   make(map[format.TxnID]*Handle),
		readers:  make(map[format.TxnID]*Handle),
		status:   make(map[format.TxnID]Status),
		Locks:    NewLockManager(),
	}
	m.cv = sync.NewCond(&m.mu)
	return m
}

func (m *Manager) Next() format.TxnID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.next
}

func (m *Manager) Recover(next format.TxnID, committed, aborted []format.TxnID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if next > m.next {
		m.next = next
	}
	for _, id := range committed {
		m.status[id] = StatusCommitted
	}
	for _, id := range aborted {
		m.status[id] = StatusAborted
	}
}

// Attach registers an already-allocated WAL transaction id.
func (m *Manager) Attach(id format.TxnID, iso Isolation) *Handle {
	m.mu.Lock()
	defer m.mu.Unlock()
	if iso == 0 {
		iso = SnapshotIsolation
	}
	if id >= m.next {
		m.next = id + 1
	}
	h := &Handle{ID: id, Iso: iso}
	h.Snap = m.captureLocked(id)
	m.active[id] = h
	m.status[id] = StatusInProgress
	return h
}

func (m *Manager) Handle(id format.TxnID) *Handle {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[id]
}

// ActiveCount is the number of in-progress write transactions.
func (m *Manager) ActiveCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

// BeginRead registers a snapshot so writers do not drop UNDO it may need.
func (m *Manager) BeginRead(iso Isolation) *Handle {
	if m == nil {
		return &Handle{Iso: iso, ReadOnly: true}
	}
	if iso == 0 {
		iso = SnapshotIsolation
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Reader handles are registry tokens, not transaction ids. Keep them in a
	// disjoint namespace so a WAL writer id can never compare equal to Snap.Tid.
	id := format.TxnID(uint64(1)<<63 | m.nextRead)
	m.nextRead++
	h := &Handle{ID: id, Iso: iso, ReadOnly: true}
	h.Snap = m.captureLocked(0)
	if m.readers == nil {
		m.readers = make(map[format.TxnID]*Handle)
	}
	m.readers[id] = h
	return h
}

// EndRead unregisters a snapshot from BeginRead.
func (m *Manager) EndRead(id format.TxnID) {
	if m == nil || id == 0 {
		return
	}
	m.mu.Lock()
	delete(m.readers, id)
	m.mu.Unlock()
}

// LiveSnapshots is writers plus registered readers.
func (m *Manager) LiveSnapshots() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active) + len(m.readers)
}

func (m *Manager) Status(id format.TxnID) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(id)
}

func (m *Manager) statusLocked(id format.TxnID) Status {
	if id == 0 {
		return StatusCommitted
	}
	if s, ok := m.status[id]; ok {
		return s
	}
	if _, ok := m.active[id]; ok {
		return StatusInProgress
	}
	return StatusCommitted
}

func (m *Manager) Capture(id format.TxnID) Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.captureLocked(id)
}

func (m *Manager) captureLocked(id format.TxnID) Snapshot {
	active := make(map[format.TxnID]struct{}, len(m.active))
	for xid := range m.active {
		if xid != id {
			active[xid] = struct{}{}
		}
	}
	return Snapshot{Tid: id, Xmax: m.next, Active: active}
}

func (m *Manager) Refresh(h *Handle) {
	if h == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h.Snap = m.captureLocked(h.ID)
}

func (m *Manager) Commit(id format.TxnID) {
	m.mu.Lock()
	delete(m.active, id)
	m.status[id] = StatusCommitted
	m.cv.Broadcast()
	m.mu.Unlock()
	m.Locks.ReleaseAll(id)
}

func (m *Manager) Abort(id format.TxnID) {
	m.mu.Lock()
	delete(m.active, id)
	m.status[id] = StatusAborted
	m.cv.Broadcast()
	m.mu.Unlock()
	m.Locks.ReleaseAll(id)
}

func (m *Manager) HasForeign(self format.TxnID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.active {
		if id != self {
			return true
		}
	}
	return false
}

func (m *Manager) WaitDone(id format.TxnID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		if m.statusLocked(id) != StatusInProgress {
			return
		}
		m.cv.Wait()
	}
}

func (m *Manager) StatusFn() func(format.TxnID) Status {
	return m.Status
}

// LockKey takes a key lock for h. btree.Txn.lockWrite skips this under
// SNAPSHOT / RC when ActiveCount()<=1; callers that must conflict on a
// key they do not write (foreign keys) have to call LockKey themselves.
func (m *Manager) LockKey(h *Handle, key []byte, mode Mode) error {
	if h == nil {
		return nerr.New(nerr.InvalidArgument, "txn.LockKey", "nil handle")
	}
	return m.Locks.Acquire(h.ID, key, mode)
}

func (m *Manager) LockRange(h *Handle, start, end []byte, mode Mode) error {
	if h == nil {
		return nerr.New(nerr.InvalidArgument, "txn.LockRange", "nil handle")
	}
	return m.Locks.AcquireRange(h.ID, start, end, mode)
}
