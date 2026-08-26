package wal

import (
	"bytes"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestChangeRecordRoundTrip(t *testing.T) {
	want := Change{
		Operation: ChangeUpdate,
		TableID:   42,
		Table:     "orders",
		Tenant:    "tenant-b",
		OldTenant: "tenant-a",
		Key:       []byte("new-key"),
		OldKey:    []byte("old-key"),
		Before:    []byte("before-row"),
		After:     []byte("after-row"),
	}
	rec, err := ChangeRec(7, 11, want)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Type != RecChange || rec.TxnID != 7 || rec.PrevLSN != 11 {
		t.Fatalf("record metadata: %+v", rec)
	}
	got, err := DecodeChange(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Operation != want.Operation || got.TableID != want.TableID || got.Table != want.Table ||
		got.Tenant != want.Tenant || got.OldTenant != want.OldTenant ||
		!bytes.Equal(got.Key, want.Key) || !bytes.Equal(got.OldKey, want.OldKey) ||
		!bytes.Equal(got.Before, want.Before) || !bytes.Equal(got.After, want.After) {
		t.Fatalf("round trip: got=%+v want=%+v", got, want)
	}
}

func TestChangeRecordRejectsMalformedAndInvalid(t *testing.T) {
	if _, err := ChangeRec(1, 1, Change{Operation: ChangeInsert, TableID: 1, Table: "t"}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("missing key: %v", err)
	}
	if _, err := ChangeRec(1, 1, Change{Operation: ChangeUpdate, TableID: 1, Table: "t", Key: []byte("k"), Before: make([]byte, 9000), After: make([]byte, 9000)}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("oversized images: %v", err)
	}
	rec, err := ChangeRec(1, 1, Change{Operation: ChangeInsert, TableID: 1, Table: "t", Key: []byte("k")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(rec.Body); i++ {
		if _, err := DecodeChange(rec.Body[:i]); err == nil {
			t.Fatalf("truncation %d accepted", i)
		}
	}
	bad := append([]byte(nil), rec.Body...)
	bad[7] = 1
	if _, err := DecodeChange(bad); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("flags: %v", err)
	}
}

func FuzzDecodeChange(f *testing.F) {
	rec, err := ChangeRec(1, 1, Change{Operation: ChangeDelete, TableID: 1, Table: "t", Tenant: "x", Key: []byte("k")})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(rec.Body)
	f.Add([]byte("NSCD"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = DecodeChange(raw)
	})
}
