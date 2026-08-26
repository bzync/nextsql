package undo

import (
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/row"
)

func FuzzDecodeRecord(f *testing.F) {
	f.Add(encodeRecord(Record{
		Txn:  7,
		Prev: 3,
		Kind: KindUpdate,
		Key:  []byte("key"),
		Old:  row.Version{Xmin: 2, Xmax: 7, Undo: 3, Payload: []byte("old")},
	}))
	f.Add([]byte{})
	f.Add([]byte("undo"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, err := decodeRecord(format.UndoID(1), raw)
		if err != nil && !nerr.HasCode(err, nerr.InvalidFormat) {
			t.Fatalf("uncontrolled error: %v", err)
		}
	})
}
