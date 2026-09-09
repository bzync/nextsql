package allocator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

// Nothing here writes a bulk workload — these tests allocate and free page ids
// — so the production ~256 MiB allocation runway would be pure I/O cost per
// database created. The runway keeps its own coverage at the production
// default in internal/storage/file.
func TestMain(m *testing.M) {
	file.SetCapacityAhead(64) // 1 MiB
	os.Exit(m.Run())
}

func newManager(t testing.TB) *file.Manager {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	m, err := file.Create(filepath.Join(t.TempDir(), "nextsql.db"), id, keys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func idRecord(id format.PageID) []byte {
	rec := make([]byte, 8)
	encoding.PutU64(rec, 0, uint64(id))
	return rec
}

// writeFreeList plants a freelist metadata page holding recs verbatim, so a
// test can write a record the allocator would never produce, linked to next.
func writeFreeList(t testing.TB, m *file.Manager, id, next format.PageID, recs ...[]byte) {
	t.Helper()
	p := page.New(id, format.PageTypeFreeList)
	for _, rec := range recs {
		if _, err := p.Insert(rec); err != nil {
			t.Fatal(err)
		}
	}
	p.SetTxnMeta(format.TxnID(next))
	if err := m.WriteLogical(id, p.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// A freelist that survives Open is trusted for the life of the database: every
// id it names is handed straight back out by Alloc. Each of these is a state
// the allocator itself cannot produce, so reaching Open means the on-disk
// freelist was corrupted or tampered with, and Open must refuse it rather than
// hand out a page that is already in use.
func TestOpenRejectsCorruptFreeList(t *testing.T) {
	const next = format.PageID(64)

	cases := []struct {
		name string
		code nerr.Code
		want string
		// plant writes the freelist pages and returns the superblock's
		// freelist head and count.
		plant func(t *testing.T, m *file.Manager) (head format.PageID, count uint64)
	}{
		{
			name: "count without head",
			code: nerr.Corruption,
			want: "freelist count without head",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				return 0, 3
			},
		},
		{
			name: "head page is not a freelist page",
			code: nerr.Corruption,
			want: "freelist page has wrong type",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				p := page.New(5, format.PageTypeSlotted)
				if err := m.WriteLogical(5, p.Bytes()); err != nil {
					t.Fatal(err)
				}
				return 5, 0
			},
		},
		{
			name: "record is not a page id",
			code: nerr.Corruption,
			want: "freelist record has wrong size",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				writeFreeList(t, m, 5, 0, []byte{1, 2, 3, 4})
				return 5, 1
			},
		},
		{
			name: "superblock is a free page",
			code: nerr.InvalidArgument,
			want: "reserved for the superblock",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				writeFreeList(t, m, 5, 0, idRecord(0))
				return 5, 1
			},
		},
		{
			name: "same page free twice in one page",
			code: nerr.Corruption,
			want: "duplicate free page id",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				writeFreeList(t, m, 5, 0, idRecord(7), idRecord(7))
				return 5, 2
			},
		},
		{
			name: "same page free twice across pages",
			code: nerr.Corruption,
			want: "duplicate free page id",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				writeFreeList(t, m, 5, 6, idRecord(7))
				writeFreeList(t, m, 6, 0, idRecord(7))
				return 5, 2
			},
		},
		{
			name: "metadata chain cycles",
			code: nerr.Corruption,
			want: "freelist cycle",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				writeFreeList(t, m, 5, 6, idRecord(7))
				writeFreeList(t, m, 6, 5, idRecord(8))
				return 5, 2
			},
		},
		{
			name: "free page was never allocated",
			code: nerr.Corruption,
			want: "beyond the allocation high-water mark",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				writeFreeList(t, m, 5, 0, idRecord(next+1))
				return 5, 1
			},
		},
		{
			name: "chain lists its own metadata page as free",
			code: nerr.Corruption,
			want: "metadata page is listed as free",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				writeFreeList(t, m, 5, 0, idRecord(5))
				return 5, 1
			},
		},
		{
			name: "chain lists a later metadata page as free",
			code: nerr.Corruption,
			want: "metadata page is listed as free",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				// Page 6 is only discovered after the record naming it.
				writeFreeList(t, m, 5, 6, idRecord(6))
				writeFreeList(t, m, 6, 0, idRecord(7))
				return 5, 2
			},
		},
		{
			name: "count disagrees with the chain",
			code: nerr.Corruption,
			want: "freelist count mismatch",
			plant: func(t *testing.T, m *file.Manager) (format.PageID, uint64) {
				writeFreeList(t, m, 5, 0, idRecord(7))
				return 5, 2
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newManager(t)
			head, count := tc.plant(t, m)
			if err := m.SetAllocState(next, head, count); err != nil {
				t.Fatal(err)
			}
			a, err := Open(m)
			if err == nil {
				t.Fatalf("Open accepted a corrupt freelist: next=%d free=%v", a.Next(), a.State().Free)
			}
			if !nerr.HasCode(err, tc.code) {
				t.Fatalf("Open error = %v, want code %s", err, tc.code)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Open error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestAllocFreeSurvivesReopen(t *testing.T) {
	m := newManager(t)
	a, err := Open(m)
	if err != nil {
		t.Fatal(err)
	}

	var allocated []format.PageID
	for i := 0; i < 16; i++ {
		id, err := a.Alloc()
		if err != nil {
			t.Fatal(err)
		}
		allocated = append(allocated, id)
	}
	freed := map[format.PageID]bool{}
	for _, id := range allocated[:5] {
		if err := a.Free(id); err != nil {
			t.Fatal(err)
		}
		freed[id] = true
	}
	if got := a.FreeCount(); got != len(freed) {
		t.Fatalf("FreeCount = %d, want %d", got, len(freed))
	}
	next := a.Next()
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.FreeCount(); got != len(freed) {
		t.Fatalf("FreeCount after reopen = %d, want %d", got, len(freed))
	}
	// next advanced past the freelist metadata pages persist claimed, so it
	// only has to cover the pages that were handed out.
	if reopened.Next() < next {
		t.Fatalf("Next after reopen = %d, want at least %d", reopened.Next(), next)
	}
	// Every freed page must come back, and nothing else: a page that is live
	// must never be handed out a second time.
	got := map[format.PageID]bool{}
	for i := 0; i < len(freed); i++ {
		id, err := reopened.Alloc()
		if err != nil {
			t.Fatal(err)
		}
		if !freed[id] {
			t.Fatalf("Alloc returned %d, which was never freed (freed=%v)", id, freed)
		}
		if got[id] {
			t.Fatalf("Alloc returned %d twice", id)
		}
		got[id] = true
	}
	if len(got) != len(freed) {
		t.Fatalf("reused %d pages, want %d", len(got), len(freed))
	}
}

// A freelist longer than one metadata page has to chain, and the chain must
// stay linked once it has been allocated: silently shortening it would orphan
// the tail pages, which nothing would ever reclaim.
func TestFreeListSpansAndKeepsMetadataPages(t *testing.T) {
	const total = idsPerMetaPage*2 + 100

	m := newManager(t)
	a, err := Open(m)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]format.PageID, 0, total)
	for i := 0; i < total; i++ {
		id, err := a.Alloc()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if err := a.Free(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	meta := a.State().Metadata
	if len(meta) != 3 {
		t.Fatalf("metadata pages = %d, want 3 for %d free ids at %d per page", len(meta), total, idsPerMetaPage)
	}

	reopened, err := Open(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.FreeCount(); got != total {
		t.Fatalf("FreeCount after reopen = %d, want %d", got, total)
	}
	want := map[format.PageID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, id := range reopened.State().Free {
		if !want[id] {
			t.Fatalf("freelist contains %d, which was never freed", id)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("%d freed pages were lost across reopen", len(want))
	}

	// Draining most of the freelist leaves one page's worth of ids, but the
	// two pages that are no longer needed must stay in the chain.
	for i := 0; i < total-10; i++ {
		if _, err := reopened.Alloc(); err != nil {
			t.Fatal(err)
		}
	}
	if err := reopened.Flush(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.FreeCount(); got != 10 {
		t.Fatalf("FreeCount = %d, want 10", got)
	}
	if got := again.State().Metadata; len(got) != len(meta) {
		t.Fatalf("metadata pages = %d after draining the freelist, want the original %d kept linked", len(got), len(meta))
	}
}

func TestFreeRejectsPagesItMustNotReclaim(t *testing.T) {
	m := newManager(t)
	a, err := Open(m)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Free(id); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	meta := a.State().Metadata
	if len(meta) == 0 {
		t.Fatal("no freelist metadata page was allocated")
	}

	cases := []struct {
		name string
		id   format.PageID
		want string
	}{
		{"superblock", format.PageIDSuperblock, "reserved for the superblock"},
		{"never allocated", a.Next(), "never allocated"},
		{"already free", id, "already free"},
		{"allocator metadata", meta[0], "cannot free allocator metadata page"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := a.Free(tc.id)
			if err == nil {
				t.Fatalf("Free(%d) succeeded, want a rejection", tc.id)
			}
			if !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("Free(%d) error = %v, want code %s", tc.id, err, nerr.InvalidArgument)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Free(%d) error = %v, want it to mention %q", tc.id, err, tc.want)
			}
		})
	}
}

// The cap is a hosting isolation boundary: a database at its storage cap must
// stop growing the file, but must still be able to reuse what it already owns,
// or a full database could never be shrunk by deleting from it.
func TestAllocRespectsStorageCap(t *testing.T) {
	m := newManager(t)
	a, err := Open(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.CapPages(); got != 0 {
		t.Fatalf("CapPages = %d, want 0 (unlimited) by default", got)
	}
	capPages := uint64(a.Next()) + 4
	a.SetCapPages(capPages)
	if got := a.CapPages(); got != capPages {
		t.Fatalf("CapPages = %d, want %d", got, capPages)
	}

	var last format.PageID
	for i := 0; i < 4; i++ {
		last, err = a.Alloc()
		if err != nil {
			t.Fatalf("Alloc %d of 4 under the cap: %v", i+1, err)
		}
	}
	if _, err := a.Alloc(); err == nil {
		t.Fatal("Alloc grew the file past the storage cap")
	} else if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("Alloc past the cap error = %v, want code %s", err, nerr.Exhausted)
	}

	if err := a.Free(last); err != nil {
		t.Fatal(err)
	}
	reused, err := a.Alloc()
	if err != nil {
		t.Fatalf("Alloc from the freelist at the cap: %v", err)
	}
	if reused != last {
		t.Fatalf("Alloc = %d, want the freed page %d", reused, last)
	}

	a.SetCapPages(0)
	if _, err := a.Alloc(); err != nil {
		t.Fatalf("Alloc after clearing the cap: %v", err)
	}
}

func TestReloadRereadsPersistedState(t *testing.T) {
	m := newManager(t)
	a, err := Open(m)
	if err != nil {
		t.Fatal(err)
	}
	var ids []format.PageID
	for i := 0; i < 8; i++ {
		id, err := a.Alloc()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Freeing inside the loop above would only hand the same page straight
	// back to the next Alloc, leaving nothing persisted to reread.
	for _, id := range ids[:4] {
		if err := a.Free(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	before := a.State()
	if len(before.Metadata) == 0 {
		t.Fatal("no freelist metadata page was persisted")
	}

	if err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	after := a.State()
	if after.Next != before.Next || len(after.Free) != len(before.Free) || len(after.Metadata) != len(before.Metadata) {
		t.Fatalf("Reload changed persisted state: before=%+v after=%+v", before, after)
	}

	// Reload is how an allocator picks up a file that changed underneath it,
	// so it has to apply the same validation Open does rather than trusting
	// what is already in memory.
	p := page.New(before.Metadata[0], format.PageTypeSlotted)
	if err := m.WriteLogical(before.Metadata[0], p.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(); err == nil {
		t.Fatal("Reload accepted a corrupt freelist metadata page")
	} else if !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("Reload error = %v, want code %s", err, nerr.Corruption)
	}
}

func TestNilAllocatorIsInert(t *testing.T) {
	var a *Allocator
	a.SetCapPages(16)
	if got := a.CapPages(); got != 0 {
		t.Fatalf("CapPages on a nil allocator = %d, want 0", got)
	}
	if got := a.State(); got.Next != 0 || len(got.Free) != 0 || len(got.Metadata) != 0 {
		t.Fatalf("State on a nil allocator = %+v, want the zero value", got)
	}
	if err := a.Reload(); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("Reload on a nil allocator = %v, want code %s", err, nerr.InvalidArgument)
	}
	if err := a.Flush(); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("Flush on a nil allocator = %v, want code %s", err, nerr.InvalidArgument)
	}
}

// The freelist is a persistent decoder fed from disk, so it is reachable by
// anything that can corrupt or tamper with the data file. Whatever it is
// handed, Open must either reject it or produce an allocator that cannot hand
// the same page out twice.
func FuzzOpenFreeList(f *testing.F) {
	f.Add([]byte{}, uint64(0), uint64(0))
	f.Add(idRecord(7), uint64(1), uint64(0))
	f.Add(append(idRecord(7), idRecord(8)...), uint64(2), uint64(0))
	f.Add(append(idRecord(7), idRecord(7)...), uint64(2), uint64(0))
	f.Add(append(idRecord(0), idRecord(1)...), uint64(2), uint64(6))
	f.Add([]byte{1, 2, 3}, uint64(1), uint64(5))

	f.Fuzz(func(t *testing.T, records []byte, count, link uint64) {
		// Two metadata pages is enough to reach the chain walk; more only
		// makes each iteration slower.
		if len(records) > 4096 {
			records = records[:4096]
		}
		const next = format.PageID(4096)

		m := newManager(t)
		p := page.New(5, format.PageTypeFreeList)
		for off := 0; off < len(records); off += 8 {
			end := off + 8
			if end > len(records) {
				end = len(records)
			}
			if _, err := p.Insert(records[off:end]); err != nil {
				break // page full; what fits is enough to decode
			}
		}
		p.SetTxnMeta(format.TxnID(link % uint64(next)))
		if err := m.WriteLogical(5, p.Bytes()); err != nil {
			t.Fatal(err)
		}
		writeFreeList(t, m, 6, 0, idRecord(9))
		if err := m.SetAllocState(next, 5, count%64); err != nil {
			t.Fatal(err)
		}

		a, err := Open(m)
		if err != nil {
			return // rejecting a corrupt freelist is the correct outcome
		}
		st := a.State()
		if uint64(len(st.Free)) != uint64(a.FreeCount()) {
			t.Fatalf("FreeCount = %d, but the freelist holds %d ids", a.FreeCount(), len(st.Free))
		}
		seen := map[format.PageID]bool{}
		for _, id := range st.Free {
			if id == format.PageIDSuperblock {
				t.Fatalf("accepted freelist hands out the superblock: %v", st.Free)
			}
			if id >= st.Next {
				t.Fatalf("accepted freelist hands out %d, beyond the high-water mark %d", id, st.Next)
			}
			if seen[id] {
				t.Fatalf("accepted freelist hands out %d twice: %v", id, st.Free)
			}
			seen[id] = true
		}
		for _, id := range st.Metadata {
			if seen[id] {
				t.Fatalf("accepted freelist hands out its own metadata page %d", id)
			}
		}
	})
}
