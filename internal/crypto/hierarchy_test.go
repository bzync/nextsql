package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

func testRoot(t *testing.T) *DEK {
	t.Helper()
	d, err := GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func testIdent(t *testing.T) format.Identity {
	t.Helper()
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEnvelopeCreateOpenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.keys")
	root := testRoot(t)
	id := testIdent(t)
	env, err := CreateEnvelope(path, id, root)
	if err != nil {
		t.Fatal(err)
	}
	pageDEK, err := env.Current()
	if err != nil {
		t.Fatal(err)
	}
	walDEK, err := env.Provider(DomainWAL).Current()
	if err != nil {
		t.Fatal(err)
	}
	if pageDEK.Equal(walDEK) {
		t.Fatal("page and WAL DEKs must be distinct")
	}
	master, err := env.Master()
	if err != nil {
		t.Fatal(err)
	}
	if master.Equal(pageDEK) || master.Equal(walDEK) {
		t.Fatal("master must not equal a domain DEK")
	}
	pg := page.New(3, format.PageTypeSlotted)
	if _, err := pg.Insert([]byte("roundtrip")); err != nil {
		t.Fatal(err)
	}
	pg.Finalize()
	sealed, err := SealPage(pageDEK, 3, 1, pg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	_ = env.Close()

	re, err := OpenEnvelope(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPage(re, 3, sealed); err != nil {
		t.Fatalf("page DEK mismatch after reopen: %v", err)
	}
	if re.Identity() != id {
		t.Fatal("identity mismatch")
	}
}

func TestEnvelopeWrongRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	if _, err := CreateEnvelope(path, testIdent(t), root); err != nil {
		t.Fatal(err)
	}
	other := testRoot(t)
	if _, err := OpenEnvelope(path, other); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong root: %v", err)
	}
}

func TestEnvelopeStolenKeystoreUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	if _, err := CreateEnvelope(path, testIdent(t), testRoot(t)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decodeKeystore(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.WrappedKEK) == 0 || bytes.Contains(body, []byte("NSKY")) {
		t.Fatal("keystore must hold wrapped keys only")
	}
	locked, err := OpenLocked(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locked.Current(); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("locked envelope must not yield keys: %v", err)
	}
}

func TestEnvelopeRotateAndDecryptOld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	env, err := CreateEnvelope(path, testIdent(t), root)
	if err != nil {
		t.Fatal(err)
	}
	old, err := env.Current()
	if err != nil {
		t.Fatal(err)
	}
	pg := page.New(3, format.PageTypeSlotted)
	if _, err := pg.Insert([]byte("v1")); err != nil {
		t.Fatal(err)
	}
	pg.Finalize()
	sealed, err := SealPage(old, 3, 1, pg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	ver, err := env.RotateDomain(DomainPage)
	if err != nil {
		t.Fatal(err)
	}
	if ver != 2 {
		t.Fatalf("version %d", ver)
	}
	cur, err := env.Current()
	if err != nil {
		t.Fatal(err)
	}
	if cur.Version != 2 || cur.Equal(old) {
		t.Fatal("current must be the new version")
	}
	if _, err := OpenPage(env, 3, sealed); err != nil {
		t.Fatalf("old ciphertext must still open: %v", err)
	}
	if err := env.Revoke(DomainPage, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPage(env, 3, sealed); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("revoked version must fail: %v", err)
	}
}

func TestEnvelopeRewrapAndRootRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	env, err := CreateEnvelope(path, testIdent(t), root)
	if err != nil {
		t.Fatal(err)
	}
	pageDEK, err := env.Current()
	if err != nil {
		t.Fatal(err)
	}
	pg := page.New(4, format.PageTypeSlotted)
	if _, err := pg.Insert([]byte("keep")); err != nil {
		t.Fatal(err)
	}
	pg.Finalize()
	sealed, err := SealPage(pageDEK, 4, 1, pg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := env.RotateKEK(); err != nil {
		t.Fatal(err)
	}
	if err := env.RotateMaster(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPage(env, 4, sealed); err != nil {
		t.Fatalf("wrapped-key rotation must keep domain DEKs: %v", err)
	}
	newRoot := testRoot(t)
	if err := env.RotateRoot(newRoot); err != nil {
		t.Fatal(err)
	}
	_ = env.Close()
	if _, err := OpenEnvelope(path, root); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("old root after rotate: %v", err)
	}
	re, err := OpenEnvelope(path, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPage(re, 4, sealed); err != nil {
		t.Fatalf("root rotation must keep domain DEKs: %v", err)
	}
}

func TestEnvelopeShred(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	env, err := CreateEnvelope(path, testIdent(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Shred("please"); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("weak confirm: %v", err)
	}
	fired := false
	env.OnRevoke(func(RevokeEvent) { fired = true })
	if err := env.Shred(ShredPhrase); err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("shred must notify listeners")
	}
	if _, err := env.Current(); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("shredded envelope: %v", err)
	}
	if _, err := OpenEnvelope(path, root); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("shredded keystore still opened: %v", err)
	}
}

func TestEnvelopeClientUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	if _, err := CreateEnvelope(path, testIdent(t), root); err != nil {
		t.Fatal(err)
	}
	locked, err := OpenLocked(path)
	if err != nil {
		t.Fatal(err)
	}
	if locked.Unlocked() {
		t.Fatal("expected locked")
	}
	if err := locked.Unlock(root); err != nil {
		t.Fatal(err)
	}
	if _, err := locked.Current(); err != nil {
		t.Fatal(err)
	}
	if err := locked.VerifyRoot(testRoot(t)); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong verify: %v", err)
	}
}

func TestWrapParentUsesMaster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	env, err := CreateEnvelope(path, testIdent(t), testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := WrapParent(env)
	if err != nil {
		t.Fatal(err)
	}
	master, err := env.Master()
	if err != nil {
		t.Fatal(err)
	}
	if !parent.Equal(master) {
		t.Fatal("WrapParent must return the master")
	}
	flat := testRoot(t)
	keys, err := NewMemoryKeyProvider(flat)
	if err != nil {
		t.Fatal(err)
	}
	got, err := WrapParent(keys)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(flat) {
		t.Fatal("flat provider wraps under Current")
	}
}

func TestUnlockMaterialRoundTrip(t *testing.T) {
	root := testRoot(t)
	raw, err := EncodeUnlockMaterial(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseUnlockMaterial(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(root) {
		t.Fatal("unlock material mismatch")
	}
	if _, err := ParseUnlockMaterial([]byte{1, 2, 3}); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("short payload: %v", err)
	}
}

func TestCannotRevokeCurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	env, err := CreateEnvelope(path, testIdent(t), testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Revoke(DomainPage, 1); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("revoke current: %v", err)
	}
}
