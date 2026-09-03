package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestAuditKeysetRotationOverlap(t *testing.T) {
	ks, err := CreateAuditKeyset(filepath.Join(t.TempDir(), "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	oldID := ks.List()[0].ID
	oldHash := [32]byte{1}
	oldSig, oldKeyID, err := ks.sign(oldHash[:])
	if err != nil {
		t.Fatal(err)
	}

	newID, err := ks.AddKey()
	if err != nil {
		t.Fatal(err)
	}
	newHash := [32]byte{2}
	newSig, newKeyID, err := ks.sign(newHash[:])
	if err != nil {
		t.Fatal(err)
	}
	if newKeyID != newID {
		t.Fatalf("current signer should be the new key: got %d want %d", newKeyID, newID)
	}

	// Both signatures verify during the overlap window.
	if err := ks.verify(oldKeyID, oldHash[:], oldSig); err != nil {
		t.Fatalf("old signature during overlap: %v", err)
	}
	if err := ks.verify(newKeyID, newHash[:], newSig); err != nil {
		t.Fatalf("new signature: %v", err)
	}

	if err := ks.Retire(oldID); err != nil {
		t.Fatal(err)
	}
	// A retired key cannot sign new lines...
	if _, _, err := ks.sign([]byte("anything")); err != nil {
		t.Fatalf("current key should still be able to sign: %v", err)
	}
	// ...but it still verifies signatures it made before retirement.
	if err := ks.verify(oldID, oldHash[:], oldSig); err != nil {
		t.Fatalf("retired key should still verify its old signatures: %v", err)
	}
}

func TestAuditKeysetReloadLastKnownGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.keys")
	ks, err := CreateAuditKeyset(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := [32]byte{9}
	sig, keyID, err := ks.sign(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ks.Reload(); err == nil {
		t.Fatal("reload of corrupt keyset returned nil error")
	}
	if err := ks.verify(keyID, hash[:], sig); err != nil {
		t.Fatalf("keyset not retained after failed reload: %v", err)
	}
}

func TestAuditSignerReloadRejectsVerifyOnlyReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.keys")
	ks, err := CreateAuditKeyset(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := [32]byte{7}
	if err := ks.WritePublic(path); err != nil {
		t.Fatal(err)
	}
	if err := ks.Reload(); err == nil {
		t.Fatal("signer accepted verify-only replacement")
	}
	if _, _, err := ks.sign(hash[:]); err != nil {
		t.Fatalf("last-known-good signer was not retained: %v", err)
	}
}

func TestAuditKeysetPublicOnlyCannotSign(t *testing.T) {
	ks, err := CreateAuditKeyset(filepath.Join(t.TempDir(), "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	pub := ks.PublicOnly()
	if _, _, err := pub.sign([]byte("x")); err == nil {
		t.Fatal("verify-only keyset signed a line")
	}
	hash := [32]byte{3}
	sig, keyID, err := ks.sign(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.verify(keyID, hash[:], sig); err != nil {
		t.Fatalf("verify-only keyset failed to verify: %v", err)
	}
}

func TestAuditKeysetCannotRetireLastKey(t *testing.T) {
	ks, err := CreateAuditKeyset(filepath.Join(t.TempDir(), "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	id := ks.List()[0].ID
	if err := ks.Retire(id); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
}

func TestAuditKeysetDecodeRejectsGarbage(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte("xxxx"),
		[]byte("NSAK"),
		append([]byte("NSAK"), 0, 99, 0, 0),
		append([]byte("BAD!"), 1, 0, 0, 0),
	}
	for _, c := range cases {
		if _, err := decodeAuditKeys(c); err == nil {
			t.Fatalf("accepted %q", c)
		}
	}
}

func TestOpenAuditKeysetBoundsAndRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	large := filepath.Join(dir, "large.keys")
	if err := os.WriteFile(large, make([]byte, maxAuditKeyBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAuditKeyset(large); err == nil {
		t.Fatal("oversized keyset accepted")
	}
	real := filepath.Join(dir, "real.keys")
	if _, err := CreateAuditKeyset(real); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.keys")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAuditKeyset(link); err == nil {
		t.Fatal("symlink keyset accepted")
	}
}

func FuzzDecodeAuditKeys(f *testing.F) {
	ks, err := CreateAuditKeyset(filepath.Join(f.TempDir(), "audit.keys"))
	if err != nil {
		f.Fatal(err)
	}
	if raw, err := os.ReadFile(ks.Path()); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSAK"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		keys, err := decodeAuditKeys(raw)
		if err != nil {
			if keys != nil {
				t.Fatal("error with non-nil keys")
			}
			return
		}
		if keys == nil {
			t.Fatal("nil keys without error")
		}
	})
}
