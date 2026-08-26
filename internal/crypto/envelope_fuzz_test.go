package crypto

import (
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

func FuzzOpenPage(f *testing.F) {
	dek, err := GenerateDEK(1)
	if err != nil {
		f.Fatal(err)
	}
	keys, err := NewMemoryKeyProvider(dek)
	if err != nil {
		f.Fatal(err)
	}
	p := page.New(2, format.PageTypeSlotted)
	_, _ = p.Insert([]byte("fuzz"))
	p.Finalize()
	sealed, err := SealPage(dek, 2, 1, p.Bytes())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(sealed)
	f.Add(make([]byte, format.PhysicalPageSize))
	f.Add([]byte{1, 2, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		plain, err := OpenPage(keys, 2, data)
		if err != nil {
			return
		}
		if len(plain) != format.LogicalPageSize {
			t.Fatalf("decrypted size %d", len(plain))
		}
	})
}
