package crypto

import (
	"bytes"
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

func testDEK(t *testing.T, ver format.KeyVersion) *DEK {
	t.Helper()
	d, err := GenerateDEK(ver)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func logicalPage(t *testing.T, id format.PageID) []byte {
	t.Helper()
	p := page.New(id, format.PageTypeSlotted)
	if _, err := p.Insert([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	p.Finalize()
	return p.CloneBytes()
}

func TestSealOpen(t *testing.T) {
	dek := testDEK(t, 1)
	keys, err := NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	id := format.PageID(3)
	plain := logicalPage(t, id)
	sealed, err := SealPage(dek, id, 1, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != format.PhysicalPageSize {
		t.Fatalf("physical size %d", len(sealed))
	}
	got, err := OpenPage(keys, id, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("plaintext mismatch")
	}
}

func TestOpenPageInto(t *testing.T) {
	dek := testDEK(t, 1)
	keys, err := NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	id := format.PageID(4)
	plain := logicalPage(t, id)
	sealed, err := SealPage(dek, id, 2, plain)
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, format.LogicalPageSize)
	if err := OpenPageInto(keys, id, sealed, dst); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst, plain) {
		t.Fatal("OpenPageInto plaintext mismatch")
	}
	if err := OpenPageInto(keys, id, sealed, dst[:8]); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("short dst: %v", err)
	}
}

func TestSealPageInto(t *testing.T) {
	dek := testDEK(t, 1)
	keys, err := NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	id := format.PageID(5)
	plain := logicalPage(t, id)
	buf := make([]byte, 0, format.PhysicalPageSize)
	sealed, err := SealPageInto(dek, id, 3, plain, buf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenPage(keys, id, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("SealPageInto plaintext mismatch")
	}
}

func TestWrongKey(t *testing.T) {
	dek1 := testDEK(t, 1)
	dek2 := testDEK(t, 1)
	keys2, _ := NewMemoryKeyProvider(dek2)
	id := format.PageID(2)
	sealed, err := SealPage(dek1, id, 7, logicalPage(t, id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPage(keys2, id, sealed); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("expected crypto failure, got %v", err)
	}
}

func TestAuthTagModification(t *testing.T) {
	dek := testDEK(t, 1)
	keys, _ := NewMemoryKeyProvider(dek)
	id := format.PageID(4)
	sealed, err := SealPage(dek, id, 2, logicalPage(t, id))
	if err != nil {
		t.Fatal(err)
	}
	tag := format.EnvelopeHeaderSize + format.LogicalPageSize
	sealed[tag] ^= 0xFF
	if _, err := OpenPage(keys, id, sealed); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("expected auth failure, got %v", err)
	}
}

func TestTruncatedPhysicalPage(t *testing.T) {
	dek := testDEK(t, 1)
	keys, _ := NewMemoryKeyProvider(dek)
	id := format.PageID(5)
	sealed, err := SealPage(dek, id, 3, logicalPage(t, id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPage(keys, id, sealed[:100]); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("expected truncated, got %v", err)
	}
}

func TestInvalidPageID(t *testing.T) {
	dek := testDEK(t, 1)
	if _, err := SealPage(dek, 0, 1, make([]byte, format.LogicalPageSize)); err == nil {
		t.Fatal("page 0 must be rejected")
	}
	id := format.PageID(6)
	sealed, err := SealPage(dek, id, 4, logicalPage(t, id))
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := NewMemoryKeyProvider(dek)
	if _, err := OpenPage(keys, 7, sealed); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("expected page id mismatch, got %v", err)
	}
}

func TestInvalidEnvelopeVersion(t *testing.T) {
	dek := testDEK(t, 1)
	keys, _ := NewMemoryKeyProvider(dek)
	id := format.PageID(8)
	sealed, err := SealPage(dek, id, 5, logicalPage(t, id))
	if err != nil {
		t.Fatal(err)
	}
	encoding.PutU16(sealed, 0, 99)
	if _, err := OpenPage(keys, id, sealed); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("expected version error, got %v", err)
	}
}

func TestCiphertextTamper(t *testing.T) {
	dek := testDEK(t, 1)
	keys, _ := NewMemoryKeyProvider(dek)
	id := format.PageID(9)
	sealed, err := SealPage(dek, id, 6, logicalPage(t, id))
	if err != nil {
		t.Fatal(err)
	}
	sealed[format.EnvelopeHeaderSize+20] ^= 0x01
	if _, err := OpenPage(keys, id, sealed); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}

func TestUnknownKeyVersion(t *testing.T) {
	dek := testDEK(t, 1)
	keys, _ := NewMemoryKeyProvider(dek)
	other := testDEK(t, 9)
	id := format.PageID(10)
	sealed, err := SealPage(other, id, 1, logicalPage(t, id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPage(keys, id, sealed); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("expected unknown key version, got %v", err)
	}
}

func TestKeyFileRoundTrip(t *testing.T) {
	path := t.TempDir() + "/master.key"
	dek, err := CreateKeyFile(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !dek.Equal(got) {
		t.Fatal("key file mismatch")
	}
	if _, err := CreateKeyFile(path, 3); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("expected exists, got %v", err)
	}
}
