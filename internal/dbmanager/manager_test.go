package dbmanager

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
)

func fakeRealmDB(name string, id byte) (hosting.Realm, hosting.Database) {
	var dbID hosting.ID
	dbID[0] = id
	return hosting.Realm{Name: "r"}, hosting.Database{ID: dbID, Name: name}
}

func fakeLookup(records map[string]hosting.Database, realm hosting.Realm) LookupFunc {
	return func(realmName, databaseName string) (hosting.Realm, hosting.Database, error) {
		db, ok := records[databaseName]
		if !ok {
			return hosting.Realm{}, hosting.Database{}, nerr.New(nerr.NotFound, "test", "unknown database")
		}
		return realm, db, nil
	}
}

// simpleOpener always succeeds, returning a fresh *executor.DB whose own
// Close is used as cleanup (fine to call: DB.Close on a fake with no Eng is
// a safe no-op).
func simpleOpener(calls *atomic.Int32) Opener {
	return func(hosting.Realm, hosting.Database) (*executor.DB, func() error, error) {
		calls.Add(1)
		db := &executor.DB{}
		return db, db.Close, nil
	}
}

// TestAcquireCachedNoReopen proves an already-open (still referenced) key
// is returned without a second Opener call. Since M2-3b-1, a key with no
// outstanding reference is evicted (see TestReleaseDecrementsAndEvictsAtZero) —
// this test holds the reference across every Acquire to isolate the
// caching behavior from eviction.
func TestAcquireCachedNoReopen(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	mgr := New(4, fakeLookup(records, realm), simpleOpener(&calls))

	var releases []func()
	for i := 0; i < 3; i++ {
		if _, release, err := mgr.Acquire("r", "db1"); err != nil {
			t.Fatal(err)
		} else {
			releases = append(releases, release)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("opener called %d times, want 1", calls.Load())
	}
	for _, release := range releases {
		release()
	}
}

// TestAcquireConcurrentSingleFlight proves concurrent Acquire calls for the
// same not-yet-open key share exactly one Opener call.
func TestAcquireConcurrentSingleFlight(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	unblock := make(chan struct{})
	mgr := New(4, fakeLookup(records, realm), func(hosting.Realm, hosting.Database) (*executor.DB, func() error, error) {
		calls.Add(1)
		<-unblock
		db := &executor.DB{}
		return db, db.Close, nil
	})

	const n = 20
	var wg sync.WaitGroup
	results := make([]*executor.DB, n)
	errs := make([]error, n)
	releases := make([]func(), n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], releases[i], errs[i] = mgr.Acquire("r", "db1")
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(unblock)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("opener called %d times, want 1", calls.Load())
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d got a different handle than goroutine 0", i)
		}
	}
	for _, release := range releases {
		release()
	}
}

// TestAcquireDistinctLimitRejected proves the bound counts distinct open
// databases, not calls: the (limit+1)th distinct key is rejected while
// re-Acquiring any of the first limit keys keeps succeeding.
func TestAcquireDistinctLimitRejected(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	_, db2 := fakeRealmDB("db2", 2)
	_, db3 := fakeRealmDB("db3", 3)
	records := map[string]hosting.Database{"db1": db1, "db2": db2, "db3": db3}
	var calls atomic.Int32
	mgr := New(2, fakeLookup(records, realm), simpleOpener(&calls))

	if _, release, err := mgr.Acquire("r", "db1"); err != nil {
		t.Fatal(err)
	} else {
		defer release()
	}
	if _, release, err := mgr.Acquire("r", "db2"); err != nil {
		t.Fatal(err)
	} else {
		defer release()
	}
	if _, _, err := mgr.Acquire("r", "db3"); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("3rd distinct database: want Exhausted, got %v", err)
	}
	// Re-Acquiring an already-open key still succeeds under the same limit.
	if _, release, err := mgr.Acquire("r", "db1"); err != nil {
		t.Fatalf("re-acquire of open db1: %v", err)
	} else {
		release()
	}
	if _, release, err := mgr.Acquire("r", "db2"); err != nil {
		t.Fatalf("re-acquire of open db2: %v", err)
	} else {
		release()
	}
}

// TestAcquireFailedOpenDoesNotPoisonKey proves a failed open lets a later
// Acquire for the same key retry (once past the quarantine backoff
// window — see TestQuarantineRejectsThenRecoversAfterBackoff for the
// quarantine state transitions themselves), rather than caching the error
// forever.
func TestAcquireFailedOpenDoesNotPoisonKey(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var attempt atomic.Int32
	mgr := New(4, fakeLookup(records, realm), func(hosting.Realm, hosting.Database) (*executor.DB, func() error, error) {
		if attempt.Add(1) == 1 {
			return nil, nil, nerr.New(nerr.IO, "test", "boom")
		}
		db := &executor.DB{}
		return db, db.Close, nil
	})
	var now time.Time
	mgr.now = func() time.Time { return now }
	now = time.Now()

	if _, _, err := mgr.Acquire("r", "db1"); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("first attempt: want IO error, got %v", err)
	}
	now = now.Add(time.Hour) // past the quarantine backoff window
	db, release, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	defer release()
	if db == nil {
		t.Fatal("second attempt: nil db")
	}
	if attempt.Load() != 2 {
		t.Fatalf("opener called %d times, want 2", attempt.Load())
	}
}

// TestPreloadThenAcquireIsCacheHit proves Preload registers a slot that
// Acquire then serves without calling Opener.
func TestPreloadThenAcquireIsCacheHit(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	mgr := New(1, fakeLookup(records, realm), simpleOpener(&calls))

	preloaded := &executor.DB{}
	if err := mgr.Preload(realm, db1, preloaded); err != nil {
		t.Fatal(err)
	}
	got, release, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got != preloaded {
		t.Fatal("Acquire after Preload did not return the preloaded handle")
	}
	if calls.Load() != 0 {
		t.Fatalf("opener called %d times, want 0", calls.Load())
	}
	// The preloaded entry counts toward the limit.
	_, db2 := fakeRealmDB("db2", 2)
	if err := mgr.Preload(realm, db2, &executor.DB{}); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("second Preload over limit=1: want Exhausted, got %v", err)
	}
}

// TestSnapshotEmptyWhenNothingOpen proves Snapshot never opens anything —
// with nothing Acquired/Preloaded, it returns an empty slice, not an error
// or a freshly-opened entry.
func TestSnapshotEmptyWhenNothingOpen(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	mgr := New(4, fakeLookup(records, realm), simpleOpener(&calls))

	if got := mgr.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot with nothing open = %+v, want empty", got)
	}
	if calls.Load() != 0 {
		t.Fatalf("opener called %d times, want 0", calls.Load())
	}
}

// TestSnapshotHoldsRefUntilReleased proves Snapshot's ref-holding contract:
// a database it returns cannot be evicted (by a concurrent release of the
// caller's own connection-held ref) until Snapshot's own handle is released
// too — the exact property M2-3b-3b's CentralScheduler depends on to
// safely claim/dispatch/submit work for a database without racing eviction.
func TestSnapshotHoldsRefUntilReleased(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	mgr := New(4, fakeLookup(records, realm), simpleOpener(&calls))

	_, connRelease, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	handles := mgr.Snapshot()
	if len(handles) != 1 {
		t.Fatalf("Snapshot() = %+v, want 1 entry", handles)
	}
	if handles[0].DB == nil {
		t.Fatal("Snapshot entry has a nil DB")
	}

	// The connection's own ref releases first; Snapshot's own ref (refs
	// still 1) must keep the entry alive.
	connRelease()
	if got := mgr.OpenCount(); got != 1 {
		t.Fatalf("OpenCount after connection release, Snapshot ref still held = %d, want 1", got)
	}

	handles[0].Release()
	if got := mgr.OpenCount(); got != 0 {
		t.Fatalf("OpenCount after Snapshot ref also released = %d, want 0", got)
	}
}

// TestReleaseDecrementsAndEvictsAtZero proves a released, non-pinned entry
// with no other holders is actually evicted: a follow-up Acquire for the
// same key must re-invoke the opener, not hit the (now-closed) cache.
func TestReleaseDecrementsAndEvictsAtZero(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	mgr := New(4, fakeLookup(records, realm), simpleOpener(&calls))

	_, release, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	if got := mgr.OpenCount(); got != 1 {
		t.Fatalf("OpenCount after acquire = %d, want 1", got)
	}
	release()
	if got := mgr.OpenCount(); got != 0 {
		t.Fatalf("OpenCount after release = %d, want 0 (not evicted)", got)
	}

	if _, release2, err := mgr.Acquire("r", "db1"); err != nil {
		t.Fatal(err)
	} else {
		release2()
	}
	if calls.Load() != 2 {
		t.Fatalf("opener called %d times after re-acquire, want 2 (fresh open, not a stale cache hit)", calls.Load())
	}
}

// TestMultipleAcquireOneReleaseKeepsOpen proves refcounting, not
// first-release-wins: with two outstanding Acquires, one release must not
// evict the entry.
func TestMultipleAcquireOneReleaseKeepsOpen(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	mgr := New(4, fakeLookup(records, realm), simpleOpener(&calls))

	db, release1, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	_, release2, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	release1()
	if got := mgr.OpenCount(); got != 1 {
		t.Fatalf("OpenCount after one of two releases = %d, want 1 (still held)", got)
	}
	got, release3, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	release3()
	if got != db {
		t.Fatal("Acquire while still open returned a different handle than the original")
	}
	if calls.Load() != 1 {
		t.Fatalf("opener called %d times, want 1 (no reopen while still referenced)", calls.Load())
	}
	release2()
	if got := mgr.OpenCount(); got != 0 {
		t.Fatalf("OpenCount after final release = %d, want 0", got)
	}
}

// TestReleaseIsIdempotent proves calling the same returned release() twice
// only decrements once, under -race alongside a concurrent Acquire for the
// same key, to catch a double-eviction/negative-refcount bug.
func TestReleaseIsIdempotent(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	mgr := New(4, fakeLookup(records, realm), simpleOpener(&calls))

	_, release, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	_, release2, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); release() }()
	go func() { defer wg.Done(); release() }() // duplicate call: must be a no-op
	wg.Wait()

	if got := mgr.OpenCount(); got != 1 {
		t.Fatalf("OpenCount after one real + one duplicate release = %d, want 1", got)
	}
	release2()
	if got := mgr.OpenCount(); got != 0 {
		t.Fatalf("OpenCount after final release = %d, want 0", got)
	}
}

// TestPinnedPreloadNeverEvicted proves a Preloaded (pinned) entry survives
// its external refcount reaching zero.
func TestPinnedPreloadNeverEvicted(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var calls atomic.Int32
	mgr := New(4, fakeLookup(records, realm), simpleOpener(&calls))

	preloaded := &executor.DB{}
	if err := mgr.Preload(realm, db1, preloaded); err != nil {
		t.Fatal(err)
	}
	got, release, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	release() // refs back to 0, but pinned — must not evict
	if got := mgr.OpenCount(); got != 1 {
		t.Fatalf("OpenCount after release of a pinned entry = %d, want 1 (still open)", got)
	}
	got2, release2, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	release2()
	if got2 != got || got2 != preloaded {
		t.Fatal("Acquire after releasing a pinned entry did not return the same preloaded handle")
	}
	if calls.Load() != 0 {
		t.Fatalf("opener called %d times, want 0 (pinned entry never reopens)", calls.Load())
	}
}

// TestCleanupCalledOnEviction proves cleanup is invoked exactly once, only
// once the refcount actually reaches zero — never on an intermediate
// release while another reference is still outstanding.
func TestCleanupCalledOnEviction(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var cleanupCalls atomic.Int32
	mgr := New(4, fakeLookup(records, realm), func(hosting.Realm, hosting.Database) (*executor.DB, func() error, error) {
		return &executor.DB{}, func() error {
			cleanupCalls.Add(1)
			return nil
		}, nil
	})

	_, release1, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	_, release2, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatal(err)
	}
	release1()
	if cleanupCalls.Load() != 0 {
		t.Fatalf("cleanup called after first of two releases, want 0")
	}
	release2()
	if cleanupCalls.Load() != 1 {
		t.Fatalf("cleanup called %d times after final release, want 1", cleanupCalls.Load())
	}
}

// TestQuarantineRejectsThenRecoversAfterBackoff proves a repeatedly failing
// open is quarantined (rejected immediately, without retrying the opener)
// until the backoff window passes, then retried and cleared on success.
func TestQuarantineRejectsThenRecoversAfterBackoff(t *testing.T) {
	realm, db1 := fakeRealmDB("db1", 1)
	records := map[string]hosting.Database{"db1": db1}
	var attempts atomic.Int32
	mgr := New(4, fakeLookup(records, realm), func(hosting.Realm, hosting.Database) (*executor.DB, func() error, error) {
		n := attempts.Add(1)
		if n <= 2 {
			return nil, nil, nerr.New(nerr.IO, "test", "boom")
		}
		db := &executor.DB{}
		return db, db.Close, nil
	})

	var now time.Time
	mgr.now = func() time.Time { return now }
	now = time.Now()

	if _, _, err := mgr.Acquire("r", "db1"); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("first attempt: want IO, got %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}

	// Immediately retrying while still within the backoff window must be
	// rejected Unavailable without calling the opener again.
	if _, _, err := mgr.Acquire("r", "db1"); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("quarantined retry: want Unavailable, got %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts after quarantined retry = %d, want 1 (opener not called)", attempts.Load())
	}

	// Fast-forward past the first backoff window: the opener retries (and
	// fails again, extending the quarantine).
	now = now.Add(time.Hour)
	if _, _, err := mgr.Acquire("r", "db1"); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("second attempt after backoff: want IO, got %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}

	// Fast-forward past the second backoff window: the opener succeeds and
	// quarantine clears.
	now = now.Add(time.Hour)
	db, release, err := mgr.Acquire("r", "db1")
	if err != nil {
		t.Fatalf("third attempt: %v", err)
	}
	defer release()
	if db == nil {
		t.Fatal("third attempt: nil db")
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}
