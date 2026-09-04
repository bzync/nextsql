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

// TestEnvelopeKeyStatus covers the system.key_versions read-model source
// (Manager Security view, M4 remainder): a locked envelope refuses rather
// than returning a stale/zero snapshot; a freshly created envelope reports
// version 1 for every key with nothing revoked/retired; a rotation bumps
// CurrentVersion/VersionCount; a Revoke of the now-superseded version drops
// VersionCount back down while moving it into RevokedCount — and nowhere in
// the returned struct is there room for key material to leak (the type
// itself has no such field, not just "happens to be redacted here").
func TestEnvelopeKeyStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	env, err := CreateEnvelope(path, testIdent(t), root)
	if err != nil {
		t.Fatal(err)
	}

	statuses, err := env.KeyStatus()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2+len(AllDomains) {
		t.Fatalf("KeyStatus row count = %d, want %d (kek+master+%d domains)", len(statuses), 2+len(AllDomains), len(AllDomains))
	}
	byName := make(map[string]KeyStatus, len(statuses))
	for _, st := range statuses {
		if _, dup := byName[st.Domain]; dup {
			t.Fatalf("duplicate key name %q in KeyStatus", st.Domain)
		}
		byName[st.Domain] = st
	}
	for _, want := range append([]string{"kek", "master"}, domainNames(t)...) {
		st, ok := byName[want]
		if !ok {
			t.Fatalf("KeyStatus missing %q", want)
		}
		if st.CurrentVersion != 1 || st.VersionCount != 1 || st.RevokedCount != 0 || st.RetiredCount != 0 {
			t.Fatalf("fresh envelope %q status = %+v, want version 1, count 1, nothing revoked/retired", want, st)
		}
	}

	// Rotate DomainPage, then revoke the superseded version 1.
	next, err := env.RotateDomain(DomainPage)
	if err != nil {
		t.Fatal(err)
	}
	if next != 2 {
		t.Fatalf("RotateDomain returned version %d, want 2", next)
	}
	statuses, err = env.KeyStatus()
	if err != nil {
		t.Fatal(err)
	}
	page := findKeyStatus(t, statuses, DomainName(DomainPage))
	if page.CurrentVersion != 2 || page.VersionCount != 2 || page.RevokedCount != 0 {
		t.Fatalf("post-rotate page status = %+v, want current 2, count 2, 0 revoked", page)
	}

	if err := env.Revoke(DomainPage, 1); err != nil {
		t.Fatal(err)
	}
	statuses, err = env.KeyStatus()
	if err != nil {
		t.Fatal(err)
	}
	page = findKeyStatus(t, statuses, DomainName(DomainPage))
	if page.CurrentVersion != 2 || page.VersionCount != 1 || page.RevokedCount != 1 {
		t.Fatalf("post-revoke page status = %+v, want current 2, count 1 (v1's DEK is gone), 1 revoked", page)
	}
	// Every other domain is untouched by DomainPage's rotation/revocation.
	wal := findKeyStatus(t, statuses, DomainName(DomainWAL))
	if wal.CurrentVersion != 1 || wal.VersionCount != 1 || wal.RevokedCount != 0 {
		t.Fatalf("unrelated wal status changed: %+v", wal)
	}

	_ = env.Close()
	locked, err := OpenLocked(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locked.KeyStatus(); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("locked envelope must refuse KeyStatus: %v", err)
	}
}

func domainNames(t *testing.T) []string {
	t.Helper()
	names := make([]string, len(AllDomains))
	for i, d := range AllDomains {
		names[i] = DomainName(d)
	}
	return names
}

func findKeyStatus(t *testing.T, statuses []KeyStatus, name string) KeyStatus {
	t.Helper()
	for _, st := range statuses {
		if st.Domain == name {
			return st
		}
	}
	t.Fatalf("KeyStatus missing %q: %+v", name, statuses)
	return KeyStatus{}
}
