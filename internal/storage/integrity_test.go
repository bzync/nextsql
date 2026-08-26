package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

func TestCorruptPageRecoversFromWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	h, err := e.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	id := h.ID()
	slot, err := h.Page().Insert([]byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Release(true); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	flipPhysical(t, path, id)

	e, err = Open(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	h, err = e.Pin(id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.Page().Get(slot)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Release(false); err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("recovered %q", got)
	}
	if n := len(e.Isolated()); n != 0 {
		t.Fatalf("repaired page still isolated: %+v", e.Isolated())
	}
}

func TestCorruptPageWithoutWALStaysIsolated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	e, err := Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.Alloc.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	pg := page.New(id, format.PageTypeSlotted)
	if _, err := pg.Insert([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := e.File.WriteLogical(id, pg.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := e.File.Sync(); err != nil {
		t.Fatal(err)
	}
	flipPhysical(t, path, id)

	if _, err := e.Pin(id); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("expected isolated corruption, got %v", err)
	}
	list := e.Isolated()
	if len(list) != 1 || list[0].PageID != id {
		t.Fatalf("isolated %+v", list)
	}
	if _, err := e.Pin(id); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("second pin must stay failed: %v", err)
	}

	good, err := e.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := good.Page().Insert([]byte("other")); err != nil {
		t.Fatal(err)
	}
	if err := good.Release(true); err != nil {
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
	list = e.Isolated()
	if len(list) != 1 || list[0].PageID != id {
		t.Fatalf("isolation must survive restart: %+v", list)
	}
	if _, err := e.Pin(id); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("reopen pin: %v", err)
	}
}

func flipPhysical(t *testing.T, path string, id format.PageID) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	off := format.PhysicalOffset(id) + int64(format.EnvelopeHeaderSize) + 64
	var b [1]byte
	if _, err := f.ReadAt(b[:], off); err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if _, err := f.WriteAt(b[:], off); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}
