package storage

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/buffer"
	"github.com/bzync/nextsql/internal/storage/format"
)

func testKeys(t *testing.T) *crypto.MemoryKeyProvider {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestAllocationAndReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	id1, err := e.Alloc.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := e.Alloc.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 || id1 == format.PageIDSuperblock {
		t.Fatalf("bad ids %d %d", id1, id2)
	}
	if err := e.Alloc.Free(id1); err != nil {
		t.Fatal(err)
	}
	if err := e.Alloc.Free(id1); err == nil {
		t.Fatal("double free must fail")
	}
	if err := e.Alloc.Free(0); err == nil {
		t.Fatal("free of superblock must fail")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e, err = Open(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	reused, err := e.Alloc.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if reused != id1 {
		t.Fatalf("expected reuse of %d, got %d", id1, reused)
	}
	if e.Alloc.Next() <= id2 {
		t.Fatalf("high-water %d should be beyond %d", e.Alloc.Next(), id2)
	}
}

func TestStorageCapBlocksGrowthAllowsReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	e, err := Create(path, testKeys(t), 8)
	if err != nil {
		t.Fatal(err)
	}
	base := e.Alloc.Next()
	e.SetStorageCapBytes(uint64(base+3) * uint64(format.PhysicalPageSize))
	if got := e.StorageCapBytes(); got != uint64(base+3)*uint64(format.PhysicalPageSize) {
		t.Fatalf("StorageCapBytes=%d", got)
	}

	var grown []format.PageID
	for i := 0; i < 3; i++ {
		id, err := e.Alloc.Alloc()
		if err != nil {
			t.Fatalf("growth %d within cap: %v", i, err)
		}
		grown = append(grown, id)
	}
	if _, err := e.Alloc.Alloc(); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("growth past cap: %v", err)
	}

	// Freeing pages and reusing them still works at the cap.
	if err := e.Alloc.Free(grown[0]); err != nil {
		t.Fatal(err)
	}
	if err := e.Alloc.Free(grown[1]); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := e.Alloc.Alloc(); err != nil {
			t.Fatalf("reuse %d at cap: %v", i, err)
		}
	}
	if _, err := e.Alloc.Alloc(); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("growth past cap after reuse: %v", err)
	}

	// Lifting the cap re-enables growth.
	e.SetStorageCapBytes(0)
	if _, err := e.Alloc.Alloc(); err != nil {
		t.Fatalf("growth after cap lifted: %v", err)
	}
}

// TestBufferBudgetGatesConcurrentOpens proves M2-3b-2's shared frame budget
// gates a *second* Engine's Pool construction, not just one Engine's own
// growth (that's the pre-existing per-Engine StorageCapBytes, a different
// mechanism entirely): two databases with bufferPages=4 each against a
// shared budget capped at 4 total frames can never both be open at once.
func TestBufferBudgetGatesConcurrentOpens(t *testing.T) {
	path1 := filepath.Join(t.TempDir(), "nextsql.db")
	path2 := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)

	for _, p := range []string{path1, path2} {
		e, err := Create(p, keys, 4)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}

	budget := buffer.NewBudget(4)
	eng1, err := OpenWith(path1, keys, 4, OpenOptions{Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	if got := budget.Used(); got != 4 {
		t.Fatalf("budget.Used() = %d, want 4", got)
	}
	if _, err := OpenWith(path2, keys, 4, OpenOptions{Budget: budget}); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("second open past budget: got %v, want Exhausted", err)
	}

	if err := eng1.Close(); err != nil {
		t.Fatal(err)
	}
	if got := budget.Used(); got != 0 {
		t.Fatalf("budget.Used() after close = %d, want 0", got)
	}
	eng2, err := OpenWith(path2, keys, 4, OpenOptions{Budget: budget})
	if err != nil {
		t.Fatalf("open after release: %v", err)
	}
	if err := eng2.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestBufferBudgetNilUnbounded proves a nil Budget (the default for every
// pre-M2-3b-2 caller, including Create/CreateWithIdentity which never set
// OpenOptions.Budget at all) never gates anything.
func TestBufferBudgetNilUnbounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	e, err := Create(path, testKeys(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
}

func TestFreelistMetadataTailRemainsReachable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]format.PageID, 2050)
	for i := range ids {
		ids[i], err = e.Alloc.Alloc()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		if err := e.Alloc.Free(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Alloc.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := len(e.Alloc.State().Metadata); got != 3 {
		t.Fatalf("metadata pages=%d want=3", got)
	}
	for i := 0; i < 1500; i++ {
		if _, err := e.Alloc.Alloc(); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Alloc.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err = Open(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	state := e.Alloc.State()
	if len(state.Metadata) != 3 || len(state.Free) != 550 {
		t.Fatalf("metadata=%d free=%d", len(state.Metadata), len(state.Free))
	}
}

func TestPersistenceAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := Create(path, keys, 4)
	if err != nil {
		t.Fatal(err)
	}
	h, err := e.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	id := h.ID()
	slot, err := h.Page().Insert([]byte("survives-restart"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Release(true); err != nil {
		t.Fatal(err)
	}
	ident := e.Identity()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e, err = Open(path, keys, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.Identity().DatabaseString() != ident.DatabaseString() {
		t.Fatal("database identity changed")
	}
	h, err = e.Pin(id)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release(false)
	got, err := h.Page().Get(slot)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "survives-restart" {
		t.Fatalf("got %q", got)
	}
}

func TestBufferEviction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := Create(path, keys, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	var ids []format.PageID
	for i := 0; i < 3; i++ {
		h, err := e.NewSlotted()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Page().Insert([]byte(fmt.Sprintf("rec-%d", i))); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, h.ID())
		if err := h.Release(true); err != nil {
			t.Fatal(err)
		}
	}
	h, err := e.Pin(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release(false)
	got, err := h.Page().Get(0)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "rec-0" {
		t.Fatalf("evicted page %q", got)
	}
	st := e.Buffer.Stats()
	if st.Misses == 0 {
		t.Fatal("expected at least one buffer miss after eviction")
	}
}

func TestBufferHitAndPinnedExhaustion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	e, err := Create(path, testKeys(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	h1, err := e.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.NewSlotted(); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("expected exhausted pool, got %v", err)
	}
	id := h1.ID()
	if err := h1.Release(true); err != nil {
		t.Fatal(err)
	}
	h2, err := e.Pin(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h2.Release(false); err != nil {
		t.Fatal(err)
	}
	st := e.Buffer.Stats()
	if st.Hits == 0 {
		t.Fatal("expected a buffer hit on re-pin")
	}
}

func TestConcurrentPageAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	e, err := Create(path, testKeys(t), 16)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	const pages = 8
	ids := make([]format.PageID, pages)
	for i := 0; i < pages; i++ {
		h, err := e.NewSlotted()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Page().Insert([]byte("init")); err != nil {
			t.Fatal(err)
		}
		ids[i] = h.ID()
		if err := h.Release(true); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Sync(); err != nil {
		t.Fatal(err)
	}

	locks := make([]sync.Mutex, pages)
	var wg sync.WaitGroup
	errCh := make(chan error, pages*2)
	for i := 0; i < pages; i++ {
		wg.Add(2)
		id := ids[i]
		idx := i
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				h, err := e.Pin(id)
				if err != nil {
					errCh <- err
					return
				}
				locks[idx].Lock()
				got, err := h.Page().Get(0)
				locks[idx].Unlock()
				_ = h.Release(false)
				if err != nil {
					errCh <- err
					return
				}
				if !bytes.Equal(got, []byte("init")) && !bytes.HasPrefix(got, []byte("w")) {
					errCh <- fmt.Errorf("unexpected record %q", got)
					return
				}
			}
		}()
		go func(owner int) {
			defer wg.Done()
			h, err := e.Pin(id)
			if err != nil {
				errCh <- err
				return
			}
			locks[idx].Lock()
			err = h.Page().Update(0, []byte(fmt.Sprintf("w%d", owner)))
			locks[idx].Unlock()
			if err != nil {
				_ = h.Release(false)
				errCh <- err
				return
			}
			errCh <- h.Release(true)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentPinMiss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	e, err := Create(path, testKeys(t), 8)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	const pages = 24
	ids := make([]format.PageID, pages)
	for i := 0; i < pages; i++ {
		h, err := e.NewSlotted()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Page().Insert([]byte(fmt.Sprintf("p%d", i))); err != nil {
			t.Fatal(err)
		}
		ids[i] = h.ID()
		if err := h.Release(true); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Sync(); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, pages)
	sem := make(chan struct{}, 4)
	for i := 0; i < pages; i++ {
		wg.Add(1)
		go func(id format.PageID, want string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			h, err := e.Pin(id)
			if err != nil {
				errCh <- err
				return
			}
			got, err := h.Page().Get(0)
			_ = h.Release(false)
			if err != nil {
				errCh <- err
				return
			}
			if string(got) != want {
				errCh <- fmt.Errorf("page %d: %q", id, got)
			}
		}(ids[i], fmt.Sprintf("p%d", i))
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWrongKeyCannotOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := Create(path, keys, 4)
	if err != nil {
		t.Fatal(err)
	}
	h, err := e.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Page().Insert([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := h.Release(true); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	other := testKeys(t)
	if _, err := Open(path, other, 4); err == nil {
		t.Fatal("wrong key must not open the file")
	}
}

func TestDropFreedPageCanBeReused(t *testing.T) {
	e, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	h, err := e.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	id := h.ID()
	if _, err := h.Page().Insert([]byte("gone")); err != nil {
		t.Fatal(err)
	}
	if err := h.Release(true); err != nil {
		t.Fatal(err)
	}
	if err := e.Drop(id); err != nil {
		t.Fatal(err)
	}
	h, err = e.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release(true)
	if h.ID() != id {
		t.Fatalf("expected reused id %d, got %d", id, h.ID())
	}
	if h.Page().LiveSlots() != 0 {
		t.Fatal("reused page must be empty")
	}
}

func TestCreateExistingFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := Create(path, keys, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(path, keys, 2); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("expected exists, got %v", err)
	}
}
