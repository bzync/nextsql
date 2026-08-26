package integrity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestIsolatePersistReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db.isolated")
	r, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Isolate(3, ReasonCrypto); err != nil {
		t.Fatal(err)
	}
	if err := r.Isolate(7, ReasonChecksum); err != nil {
		t.Fatal(err)
	}
	if !r.Contains(3) || !r.Contains(7) {
		t.Fatal("expected isolated pages")
	}
	if err := r.Clear(3); err != nil {
		t.Fatal(err)
	}
	if r.Contains(3) || !r.Contains(7) {
		t.Fatal("clear should drop only page 3")
	}

	r2, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	got := r2.List()
	if len(got) != 1 || got[0].PageID != 7 || got[0].Reason != ReasonChecksum {
		t.Fatalf("reload %+v", got)
	}
}

func TestDamagedSidecarStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db.isolated")
	if err := os.WriteFile(path, []byte("not-a-registry"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := OpenOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 0 {
		t.Fatalf("damaged sidecar must not invent pages: %+v", r.List())
	}
}

func TestRefuseSuperblock(t *testing.T) {
	r, err := OpenOrCreate(filepath.Join(t.TempDir(), "iso"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Isolate(0, ReasonChecksum); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("superblock: %v", err)
	}
}

func TestDecodeRejectsBadInput(t *testing.T) {
	if _, err := decodeRegistry(nil); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("nil: %v", err)
	}
	if _, err := decodeRegistry([]byte("NSQIx")); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("short: %v", err)
	}
}

func TestIsFailure(t *testing.T) {
	if !IsFailure(nerr.New(nerr.Crypto, "t", "tag")) {
		t.Fatal("crypto")
	}
	if !IsFailure(nerr.New(nerr.Corruption, "t", "crc")) {
		t.Fatal("corruption")
	}
	if IsFailure(nerr.New(nerr.IO, "t", "disk")) {
		t.Fatal("io is not an integrity failure")
	}
	if ReasonOf(nerr.New(nerr.Crypto, "t", "x")) != ReasonCrypto {
		t.Fatal("reason")
	}
}
