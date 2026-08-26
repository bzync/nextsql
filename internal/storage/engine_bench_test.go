package storage

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/storage/format"
)

func benchEngine(b *testing.B, pages int) *Engine {
	b.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		b.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		b.Fatal(err)
	}
	e, err := Create(filepath.Join(b.TempDir(), "nextsql.db"), keys, pages)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = e.Close() })
	return e
}

func BenchmarkPageWrite(b *testing.B) {
	e := benchEngine(b, 8)
	h, err := e.NewSlotted()
	if err != nil {
		b.Fatal(err)
	}
	if _, err := h.Page().Insert([]byte("write-bench")); err != nil {
		b.Fatal(err)
	}
	raw := h.Page().CloneBytes()
	id := h.ID()
	if err := h.Release(true); err != nil {
		b.Fatal(err)
	}
	if err := e.Sync(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(format.PhysicalPageSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.File.WriteLogical(id, raw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPageRead(b *testing.B) {
	e := benchEngine(b, 8)
	h, err := e.NewSlotted()
	if err != nil {
		b.Fatal(err)
	}
	if _, err := h.Page().Insert([]byte("read-bench")); err != nil {
		b.Fatal(err)
	}
	id := h.ID()
	if err := h.Release(true); err != nil {
		b.Fatal(err)
	}
	if err := e.Sync(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(format.PhysicalPageSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.File.ReadLogical(id); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferHit(b *testing.B) {
	e := benchEngine(b, 4)
	h, err := e.NewSlotted()
	if err != nil {
		b.Fatal(err)
	}
	id := h.ID()
	if err := h.Release(true); err != nil {
		b.Fatal(err)
	}
	h, err = e.Pin(id)
	if err != nil {
		b.Fatal(err)
	}
	if err := h.Release(false); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h, err := e.Pin(id)
		if err != nil {
			b.Fatal(err)
		}
		if err := h.Release(false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferMiss(b *testing.B) {
	e := benchEngine(b, 1)
	var ids [2]format.PageID
	for i := 0; i < 2; i++ {
		h, err := e.NewSlotted()
		if err != nil && i == 0 {
			b.Fatal(err)
		}
		if err != nil {
			// pool size 1: release first page before allocating second
			break
		}
		ids[i] = h.ID()
		if _, err := h.Page().Insert([]byte("x")); err != nil {
			b.Fatal(err)
		}
		if err := h.Release(true); err != nil {
			b.Fatal(err)
		}
	}
	h, err := e.NewSlotted()
	if err != nil {
		b.Fatal(err)
	}
	ids[1] = h.ID()
	if err := h.Release(true); err != nil {
		b.Fatal(err)
	}
	if err := e.Sync(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%2]
		h, err := e.Pin(id)
		if err != nil {
			b.Fatal(err)
		}
		if err := h.Release(false); err != nil {
			b.Fatal(err)
		}
	}
}
