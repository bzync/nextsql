package page

import (
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
)

func BenchmarkEncodeDecode(b *testing.B) {
	p := New(1, format.PageTypeSlotted)
	_, _ = p.Insert([]byte("benchmark-record"))
	p.Finalize()
	raw := p.Bytes()
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := Parse(raw)
		if err != nil {
			b.Fatal(err)
		}
		got.Finalize()
	}
}

func BenchmarkSlottedInsert(b *testing.B) {
	rec := []byte("insert-bench")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := New(1, format.PageTypeSlotted)
		if _, err := p.Insert(rec); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSlottedLookup(b *testing.B) {
	p := New(1, format.PageTypeSlotted)
	slot, err := p.Insert([]byte("lookup-bench"))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Get(slot); err != nil {
			b.Fatal(err)
		}
	}
}
