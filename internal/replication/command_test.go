package replication

import (
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

func testDEK(t *testing.T) (*crypto.DEK, crypto.KeyProvider) {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return dek, keys
}

func TestCommandRoundTrip(t *testing.T) {
	dek, keys := testDEK(t)
	recs := []wal.Record{
		{Type: wal.RecBegin, LSN: 1, TxnID: 1},
		{Type: wal.RecCommit, LSN: 2, TxnID: 1, PrevLSN: 1, Body: []byte("ok")},
	}
	data, err := EncodeCommand(dek, recs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCommand(keys, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].LSN != 2 || string(got[1].Body) != "ok" {
		t.Fatalf("%+v", got)
	}
}

func TestCommandWrongKey(t *testing.T) {
	dek, _ := testDEK(t)
	_, other := testDEK(t)
	data, err := EncodeCommand(dek, []wal.Record{{Type: wal.RecBegin, LSN: 1, TxnID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCommand(other, data); !nerr.HasCode(err, nerr.Crypto) && !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestCommandTamper(t *testing.T) {
	dek, keys := testDEK(t)
	data, err := EncodeCommand(dek, []wal.Record{{Type: wal.RecBegin, LSN: 1, TxnID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF
	if _, err := DecodeCommand(keys, data); err == nil {
		t.Fatal("tamper must fail")
	}
}

func TestCommandRejectsEmpty(t *testing.T) {
	dek, _ := testDEK(t)
	if _, err := EncodeCommand(dek, nil); err == nil {
		t.Fatal("empty batch")
	}
}

func TestCommandRejectsBadMagic(t *testing.T) {
	_, keys := testDEK(t)
	buf := make([]byte, 40)
	if _, err := DecodeCommand(keys, buf); err == nil {
		t.Fatal("bad magic")
	}
	_ = format.LSN(0)
}

// sealBatch seals a batch at an arbitrary version, bypassing the version
// EncodeCommand would choose, to check that DecodeCommand refuses a mismatch.
func sealBatch(t *testing.T, dek *crypto.DEK, ver uint16, recs []wal.Record) []byte {
	t.Helper()
	plain := marshalBatch(ver, recs)
	aad := make([]byte, 6)
	encoding.PutU32(aad, 0, Magic)
	encoding.PutU16(aad, 4, ver)
	nonce, ct, err := crypto.SealBytesRandom(dek, aad, plain)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 28+len(ct))
	encoding.PutU32(out, 0, Magic)
	encoding.PutU16(out, 4, ver)
	encoding.PutU16(out, 6, uint16(dek.Suite))
	encoding.PutU32(out, 8, uint32(dek.Version))
	copy(out[12:24], nonce)
	encoding.PutU32(out, 24, uint32(len(ct)))
	copy(out[28:], ct)
	return out
}

// A batch carrying a page delta is version 2, so a follower on a release that
// predates deltas rejects it instead of logging the record and skipping it on
// apply. Every other batch stays version 1.
func TestCommandVersionFollowsPageDeltas(t *testing.T) {
	dek, keys := testDEK(t)
	plain := []wal.Record{
		{Type: wal.RecBegin, LSN: 1, TxnID: 1},
		{Type: wal.RecPageImage, LSN: 2, TxnID: 1, PrevLSN: 1, PageID: 9, Body: make([]byte, format.LogicalPageSize)},
		{Type: wal.RecCommit, LSN: 3, TxnID: 1, PrevLSN: 2},
	}
	withDelta := []wal.Record{
		{Type: wal.RecBegin, LSN: 4, TxnID: 2},
		{Type: wal.RecPageDelta, LSN: 5, TxnID: 2, PrevLSN: 4, PageID: 9, Body: []byte("delta body")},
		{Type: wal.RecCommit, LSN: 6, TxnID: 2, PrevLSN: 5},
	}
	for _, tc := range []struct {
		name string
		recs []wal.Record
		want uint16
	}{{"full images", plain, CurrentVersion}, {"page delta", withDelta, VersionPageDeltas}} {
		data, err := EncodeCommand(dek, tc.recs)
		if err != nil {
			t.Fatal(err)
		}
		if v := encoding.U16(data, 4); v != tc.want {
			t.Fatalf("%s: batch version %d, want %d", tc.name, v, tc.want)
		}
		got, err := DecodeCommand(keys, data)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != len(tc.recs) || got[1].Type != tc.recs[1].Type || string(got[1].Body) != string(tc.recs[1].Body) {
			t.Fatalf("%s: %+v", tc.name, got)
		}
	}
	if _, err := DecodeCommand(keys, sealBatch(t, dek, CurrentVersion, withDelta)); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("a page delta in a version-1 batch decoded: %v", err)
	}
	if _, err := DecodeCommand(keys, sealBatch(t, dek, VersionPageDeltas, plain)); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("a version-2 batch without a page delta decoded: %v", err)
	}
	if _, err := DecodeCommand(keys, sealBatch(t, dek, 3, plain)); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("a version-3 batch decoded: %v", err)
	}
}
