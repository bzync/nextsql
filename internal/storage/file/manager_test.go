package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/storage/format"
)

func testKeys(t *testing.T) crypto.KeyProvider {
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

// The runway is claimed with fallocate in mode 0, so it reserves real blocks
// rather than making the file sparse: a brand-new database occupies its live
// pages plus ~256 MiB from the moment it is created. That fixed per-database
// cost is the whole reason prealloc_ahead_pages is configurable, so it is
// asserted here — at the production default — rather than inferred. Suites
// that only need a database, not a runway, shrink it in their own TestMain.
func TestEnsureCapacityReservesRealBlocksAtDefaultRunway(t *testing.T) {
	if got := capacityAhead.Load(); got != DefaultCapacityAhead {
		t.Fatalf("runway = %d, want the production default %d; this package must not shrink it", got, DefaultCapacityAhead)
	}
	path := filepath.Join(t.TempDir(), "nextsql.db")
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Create(path, id, testKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	next, _, _ := m.AllocState()
	if err := m.EnsureCapacity(next); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := format.PhysicalOffset(next + format.PageID(DefaultCapacityAhead))
	if st.Size() < want {
		t.Fatalf("file size %d, want at least the reserved runway %d", st.Size(), want)
	}
	// Sparse would report a size but occupy no blocks, so check the reservation
	// itself where the platform exposes it.
	if got, ok := allocatedBytes(st); ok && got < want/2 {
		t.Fatalf("allocated %d bytes of blocks for a %d byte runway; fallocate did not reserve real blocks", got, want)
	}
}

func TestSetCapacityAheadIgnoresNonPositive(t *testing.T) {
	prev := capacityAhead.Load()
	defer capacityAhead.Store(prev)
	SetCapacityAhead(0)
	if capacityAhead.Load() != prev {
		t.Fatalf("a non-positive runway must be ignored, got %d", capacityAhead.Load())
	}
	SetCapacityAhead(-1)
	if capacityAhead.Load() != prev {
		t.Fatalf("a negative runway must be ignored, got %d", capacityAhead.Load())
	}
	SetCapacityAhead(64)
	if capacityAhead.Load() != 64 {
		t.Fatalf("runway = %d, want 64", capacityAhead.Load())
	}
}
