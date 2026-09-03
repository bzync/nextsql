// Package dbmanager provides a bounded, keyed registry of open database
// handles for the Multi-database hosting cross-cutting track. M2-3a: a
// small fixed open-database limit, single-flight open (concurrent requests
// for the same not-yet-open database never duplicate the open work).
// M2-3b-1: reference counting (a database closes once nothing holds it,
// except the Preloaded primary, which is pinned and never evicted) and
// quarantine/backoff on a repeatedly failing open. Still out of scope
// (M2-3b-2/3): a cross-database memory budget, centralizing background
// pools.
package dbmanager

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
)

// DefaultLimit is used when New is given limit < 1.
const DefaultLimit = 8

// Quarantine backoff after a failed open: exponential, overflow-safe-capped
// shift (same shape as internal/executor/task.go's retryDelayNS, a
// different, catalog.Task-free implementation for this domain).
const (
	quarantineBaseDelay = 200 * time.Millisecond
	quarantineMaxDelay  = 30 * time.Second
	quarantineMaxShift  = 20 // 200ms<<20 ≈ 210s, well short of overflow; the cap below still bites first
)

// Opener physically opens (never creates) the database a Registry record
// describes, wiring whatever per-database lifecycle (task runtime,
// monitors) the caller needs, and returns a cleanup closure that closes
// everything it wired — including db itself — in the correct order.
// Called outside Manager's lock. An opener with nothing extra to close can
// simply return db.Close as cleanup.
type Opener func(realm hosting.Realm, database hosting.Database) (db *executor.DB, cleanup func() error, err error)

// LookupFunc resolves a realm/database name pair to their registry
// records. Implemented by *hosting.Registry.Lookup.
type LookupFunc func(realmName, databaseName string) (hosting.Realm, hosting.Database, error)

// entry is one open database. pinned entries (the Preloaded primary) are
// never evicted: Opener only ever handles LayoutManaged databases and
// would refuse to reopen the primary's LayoutLegacyDefault, so evicting it
// would make it permanently unreachable via Acquire again.
type entry struct {
	db      *executor.DB
	cleanup func() error
	refs    int
	pinned  bool
}

type quarantineEntry struct {
	failures int
	retryAt  time.Time
}

// Manager is a bounded, keyed map of open *executor.DB handles, keyed by
// the database's durable registry ID (not its name, which can change).
type Manager struct {
	limit  int
	lookup LookupFunc
	opener Opener
	now    func() time.Time // defaults to time.Now; overridable by tests in this package

	mu         sync.Mutex
	open       map[hosting.ID]*entry
	inflight   map[hosting.ID]chan struct{}
	quarantine map[hosting.ID]*quarantineEntry
}

// New builds a Manager. limit bounds the number of distinct databases open
// at once. limit < 1 uses DefaultLimit.
func New(limit int, lookup LookupFunc, opener Opener) *Manager {
	if limit < 1 {
		limit = DefaultLimit
	}
	return &Manager{
		limit:      limit,
		lookup:     lookup,
		opener:     opener,
		now:        time.Now,
		open:       make(map[hosting.ID]*entry),
		inflight:   make(map[hosting.ID]chan struct{}),
		quarantine: make(map[hosting.ID]*quarantineEntry),
	}
}

// Preload registers an already-open database (typically the primary,
// opened by the caller's own startup path) as one of Manager's entries,
// pinned so it is never evicted, consuming one slot, without going through
// Opener. Returns Exhausted if the limit is already reached by prior
// Preload/Acquire calls.
func (m *Manager) Preload(realm hosting.Realm, database hosting.Database, db *executor.DB) error {
	if m == nil {
		return nerr.New(nerr.InvalidArgument, "dbmanager.Preload", "nil manager")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.open[database.ID]; ok {
		return nil
	}
	if len(m.open) >= m.limit {
		return nerr.New(nerr.Exhausted, "dbmanager.Preload", "open database limit reached")
	}
	m.open[database.ID] = &entry{db: db, cleanup: db.Close, pinned: true}
	return nil
}

// Acquire returns the open handle for the named realm/database, opening it
// on demand (single-flight: concurrent callers for the same not-yet-open
// database share one Opener call) if it isn't already open, and a release
// func the caller must invoke exactly once when done with the handle.
// Returns Exhausted if opening it would exceed the configured limit, or
// Unavailable if the database is quarantined after repeated failed opens.
func (m *Manager) Acquire(realmName, databaseName string) (*executor.DB, func(), error) {
	noop := func() {}
	if m == nil {
		return nil, noop, nerr.New(nerr.InvalidArgument, "dbmanager.Acquire", "nil manager")
	}
	realm, database, err := m.lookup(realmName, databaseName)
	if err != nil {
		return nil, noop, err
	}
	for {
		m.mu.Lock()
		if e, ok := m.open[database.ID]; ok {
			e.refs++
			m.mu.Unlock()
			return e.db, m.releaseFunc(database.ID), nil
		}
		if ch, ok := m.inflight[database.ID]; ok {
			m.mu.Unlock()
			<-ch
			continue
		}
		if q, ok := m.quarantine[database.ID]; ok && m.now().Before(q.retryAt) {
			m.mu.Unlock()
			return nil, noop, nerr.New(nerr.Unavailable, "dbmanager.Acquire", "database open is quarantined after repeated failures, retry later")
		}
		if len(m.open) >= m.limit {
			m.mu.Unlock()
			return nil, noop, nerr.New(nerr.Exhausted, "dbmanager.Acquire", "open database limit reached")
		}
		ch := make(chan struct{})
		m.inflight[database.ID] = ch
		m.mu.Unlock()

		db, cleanup, openErr := m.opener(realm, database)

		m.mu.Lock()
		if openErr == nil {
			m.open[database.ID] = &entry{db: db, cleanup: cleanup, refs: 1}
			delete(m.quarantine, database.ID)
		} else {
			q := m.quarantine[database.ID]
			if q == nil {
				q = &quarantineEntry{}
			}
			q.failures++
			q.retryAt = m.now().Add(backoff(q.failures))
			m.quarantine[database.ID] = q
		}
		delete(m.inflight, database.ID)
		close(ch)
		m.mu.Unlock()

		if openErr != nil {
			return nil, noop, openErr
		}
		return db, m.releaseFunc(database.ID), nil
	}
}

// releaseFunc returns an idempotent closure that decrements id's refcount
// and, if it reaches zero on a non-pinned entry, evicts it.
func (m *Manager) releaseFunc(id hosting.ID) func() {
	var once atomic.Bool
	return func() {
		if once.Swap(true) {
			return
		}
		m.release(id)
	}
}

func (m *Manager) release(id hosting.ID) {
	m.mu.Lock()
	e, ok := m.open[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	e.refs--
	evict := !e.pinned && e.refs <= 0
	if evict {
		// Removed before unlocking: a concurrent Acquire for the same id
		// then sees a clean miss and opens a fresh handle through Opener,
		// never this one mid-close.
		delete(m.open, id)
	}
	m.mu.Unlock()
	if evict {
		_ = e.cleanup() // outside the lock: I/O must never happen under mu
	}
}

// DBHandle pairs one currently-open database with the Release func its
// snapshot-time ref must eventually get exactly once — the same
// Acquire/release refcounting mechanism as a live connection uses (see
// Snapshot's own doc comment for why that reuse matters).
type DBHandle struct {
	ID      hosting.ID
	DB      *executor.DB
	Release func()
}

// Snapshot returns a ref-held handle for every currently open database,
// opening nothing new — a centralized scheduler's per-tick "what's open
// right now" query (M2-3b-3b) needs a view that can't race M2-3b-1
// eviction: holding a ref for as long as the caller still has work
// in flight against that database is exactly the guarantee Acquire/release
// already provide for a live connection, reused here instead of inventing
// a second concurrency primitive. The caller must call each entry's
// Release exactly once, and must not do so until every task claimed from
// that database on this snapshot has actually finished executing, not
// merely been submitted — releasing early would let this database evict
// out from under a still-in-flight job.
func (m *Manager) Snapshot() []DBHandle {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DBHandle, 0, len(m.open))
	for id, e := range m.open {
		e.refs++
		out = append(out, DBHandle{ID: id, DB: e.db, Release: m.releaseFunc(id)})
	}
	return out
}

// OpenCount reports how many databases are currently open (for tests and
// observability).
func (m *Manager) OpenCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.open)
}

// Close closes every open database. Safe to call once at process shutdown.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var first error
	for id, e := range m.open {
		if err := e.cleanup(); err != nil && first == nil {
			first = err
		}
		delete(m.open, id)
	}
	return first
}

func backoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	shift := failures - 1
	if shift > quarantineMaxShift {
		shift = quarantineMaxShift
	}
	d := quarantineBaseDelay << shift
	if d <= 0 || d > quarantineMaxDelay {
		return quarantineMaxDelay
	}
	return d
}
