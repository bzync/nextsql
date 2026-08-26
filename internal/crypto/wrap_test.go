package crypto

import (
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestWrapUnwrapDEK(t *testing.T) {
	kek := testDEK(t, 1)
	dek, err := GenerateDEK(2)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := WrapDEK(kek, dek, DomainWAL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapDEK(kek, blob, DomainWAL)
	if err != nil {
		t.Fatal(err)
	}
	if !dek.Equal(got) {
		t.Fatal("unwrapped DEK does not match")
	}
}

func TestUnwrapWrongKEK(t *testing.T) {
	kek := testDEK(t, 1)
	other := testDEK(t, 1)
	dek := testDEK(t, 2)
	blob, err := WrapDEK(kek, dek, DomainWAL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapDEK(other, blob, DomainWAL); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("expected crypto error, got %v", err)
	}
}

func TestUnwrapDomainMismatch(t *testing.T) {
	kek := testDEK(t, 1)
	dek := testDEK(t, 2)
	blob, err := WrapDEK(kek, dek, DomainWAL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapDEK(kek, blob, 'P'); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("expected domain mismatch, got %v", err)
	}
	undoBlob, err := WrapDEK(kek, dek, DomainUNDO)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapDEK(kek, undoBlob, DomainWAL); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("undo/wal domain mismatch: %v", err)
	}
}

func TestSealBytesRoundTrip(t *testing.T) {
	dek := testDEK(t, 1)
	plain := []byte("wal-payload")
	aad := []byte("aad")
	nonce, ct, err := SealBytes(dek, 7, aad, plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenBytes(dek, nonce, aad, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("got %q", got)
	}
	ct[0] ^= 0xff
	if _, err := OpenBytes(dek, nonce, aad, ct); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("tamper: %v", err)
	}
}
