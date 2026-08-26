package crypto

import (
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

func benchPage() []byte {
	p := page.New(2, format.PageTypeSlotted)
	_, _ = p.Insert([]byte("bench"))
	p.Finalize()
	return p.Bytes()
}

func BenchmarkPageEncrypt(b *testing.B) {
	dek, err := GenerateDEK(1)
	if err != nil {
		b.Fatal(err)
	}
	plain := benchPage()
	b.ReportAllocs()
	b.SetBytes(int64(len(plain)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := SealPage(dek, 2, uint64(i+1), plain); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWrapDEK(b *testing.B) {
	kek, err := GenerateDEK(1)
	if err != nil {
		b.Fatal(err)
	}
	dek, err := GenerateDEK(2)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := WrapDEK(kek, dek, DomainPage); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPageDecrypt(b *testing.B) {
	dek, err := GenerateDEK(1)
	if err != nil {
		b.Fatal(err)
	}
	keys, err := NewMemoryKeyProvider(dek)
	if err != nil {
		b.Fatal(err)
	}
	sealed, err := SealPage(dek, 2, 1, benchPage())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(sealed)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := OpenPage(keys, 2, sealed); err != nil {
			b.Fatal(err)
		}
	}
}
