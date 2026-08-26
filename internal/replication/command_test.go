package replication

import (
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
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
