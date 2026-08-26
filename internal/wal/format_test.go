package wal

import (
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func testWALDEK(t *testing.T) *crypto.DEK {
	t.Helper()
	d, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEncodeDecodePhysical(t *testing.T) {
	dek := testWALDEK(t)
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{
		Type:    RecInsert,
		TxnID:   3,
		PrevLSN: 8,
		PageID:  0,
		Body:    encodeInsertBody([]byte("k"), []byte("v")),
	}
	payload := encodePayload(rec)
	phys, err := encodePhysical(dek, 9, 1, payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePhysical(keys, phys[:HeaderSize], phys[HeaderSize:])
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != RecInsert || got.LSN != 9 || got.TxnID != 3 {
		t.Fatalf("decoded %+v", got)
	}
	if string(got.Body) != string(rec.Body) {
		t.Fatalf("body %q", got.Body)
	}
}

func TestPhysicalWrongKey(t *testing.T) {
	dek := testWALDEK(t)
	other := testWALDEK(t)
	keys, _ := crypto.NewMemoryKeyProvider(other)
	phys, err := encodePhysical(dek, 1, 1, encodePayload(Record{Type: RecCommit, TxnID: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePhysical(keys, phys[:HeaderSize], phys[HeaderSize:]); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestPhysicalTamper(t *testing.T) {
	dek := testWALDEK(t)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	phys, err := encodePhysical(dek, 2, 1, encodePayload(Record{Type: RecBegin, TxnID: 1}))
	if err != nil {
		t.Fatal(err)
	}
	phys[HeaderSize+1] ^= 0x0f
	if _, err := decodePhysical(keys, phys[:HeaderSize], phys[HeaderSize:]); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("tamper: %v", err)
	}
}

func TestHeaderTruncated(t *testing.T) {
	if _, err := parseHeader([]byte("nope")); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("got %v", err)
	}
}

func TestTreeMetaAllocRoundTrip(t *testing.T) {
	body := encodeTreeMeta(4, 2)
	root, h, err := decodeTreeMeta(body)
	if err != nil || root != 4 || h != 2 {
		t.Fatalf("tree %d %d %v", root, h, err)
	}
	b2 := encodeAllocState(9, 3, 2)
	n, hd, c, err := decodeAllocState(b2)
	if err != nil || n != 9 || hd != 3 || c != 2 {
		t.Fatalf("alloc %v", err)
	}
	_ = format.PageID(0)
}
