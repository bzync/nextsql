package page

import (
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
)

func FuzzParse(f *testing.F) {
	p := New(1, format.PageTypeSlotted)
	_, _ = p.Insert([]byte("seed-record"))
	p.Finalize()
	f.Add(p.Bytes())
	f.Add(make([]byte, format.LogicalPageSize))
	f.Add([]byte{0, 1, 2, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := Parse(data)
		if err != nil {
			return
		}
		if err := got.Validate(); err != nil {
			t.Fatalf("parse succeeded but validate failed: %v", err)
		}
		if len(got.Bytes()) != format.LogicalPageSize {
			t.Fatalf("parsed page size %d", len(got.Bytes()))
		}
		got.Compact()
		for i := 0; i < got.SlotCount(); i++ {
			_, _ = got.Get(uint16(i))
		}
	})
}
