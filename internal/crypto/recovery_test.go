package crypto

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
)

// newRecoveryEnv creates a root-unlocked envelope with a recovery key already
// configured, returning the keystore path, the root, and the recovery key.
func newRecoveryEnv(t *testing.T) (string, *Envelope, *DEK, *DEK) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	env, err := CreateEnvelope(path, testIdent(t), root)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := GenerateDEK(env.NextRecoveryKeyVersion())
	if err != nil {
		t.Fatal(err)
	}
	if err := env.SetRecoveryKey(rec); err != nil {
		t.Fatal(err)
	}
	return path, env, root, rec
}

func onDiskKeystoreVersion(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 6 {
		t.Fatalf("keystore too short: %d bytes", len(raw))
	}
	return int(encoding.U16(raw, 4))
}

// A database that never opts in must stay byte-compatible with releases that
// only understand v1, or configuring recovery for one database would strand
// every other one on rollback.
func TestKeystoreStaysV1WithoutRecoveryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	env, err := CreateEnvelope(path, testIdent(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if got := onDiskKeystoreVersion(t, path); got != keystoreV1 {
		t.Fatalf("fresh keystore version = %d, want %d", got, keystoreV1)
	}
	if env.HasRecoveryKey() {
		t.Fatal("fresh envelope reports a recovery key")
	}
	// Rewriting for an unrelated reason must not drift the version either.
	if _, err := env.RotateDomain(DomainPage); err != nil {
		t.Fatalf("RotateDomain: %v", err)
	}
	if got := onDiskKeystoreVersion(t, path); got != keystoreV1 {
		t.Fatalf("after rotate, keystore version = %d, want %d", got, keystoreV1)
	}
}

func TestSetRecoveryKeyMovesKeystoreToV2AndBack(t *testing.T) {
	path, env, _, rec := newRecoveryEnv(t)
	if got := onDiskKeystoreVersion(t, path); got != keystoreV2 {
		t.Fatalf("with recovery key, version = %d, want %d", got, keystoreV2)
	}
	if !env.HasRecoveryKey() {
		t.Fatal("HasRecoveryKey false after SetRecoveryKey")
	}
	if got := env.RecoveryKeyVersion(); got != rec.Version {
		t.Fatalf("RecoveryKeyVersion = %d, want %d", got, rec.Version)
	}
	if err := env.RemoveRecoveryKey(); err != nil {
		t.Fatalf("RemoveRecoveryKey: %v", err)
	}
	if got := onDiskKeystoreVersion(t, path); got != keystoreV1 {
		t.Fatalf("after removal, version = %d, want %d", got, keystoreV1)
	}
	if env.HasRecoveryKey() {
		t.Fatal("HasRecoveryKey true after removal")
	}
	// The removed key must no longer open anything.
	if _, err := OpenEnvelopeWithRecovery(path, rec); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("removed recovery key unlock error = %v, want NotFound", err)
	}
}

func TestRecoveryKeyUnlocksWithoutRoot(t *testing.T) {
	path, env, root, rec := newRecoveryEnv(t)
	pageKey, err := env.Current()
	if err != nil {
		t.Fatal(err)
	}
	// keyBytes aliases the DEK's array, and Lock zeroes it — copy first.
	want := append([]byte(nil), pageKey.keyBytes()...)
	env.Lock()

	re, err := OpenEnvelopeWithRecovery(path, rec)
	if err != nil {
		t.Fatalf("OpenEnvelopeWithRecovery: %v", err)
	}
	if !re.Unlocked() {
		t.Fatal("recovery-unlocked envelope reports locked")
	}
	if re.HasRoot() {
		t.Fatal("recovery unlock left a root loaded")
	}
	got, err := re.Current()
	if err != nil {
		t.Fatalf("Current after recovery unlock: %v", err)
	}
	if string(got.keyBytes()) != string(want) {
		t.Fatal("recovery unlock produced a different page DEK")
	}
	// The root must still work; a recovery key is an addition, not a swap.
	if _, err := OpenEnvelope(path, root); err != nil {
		t.Fatalf("root unlock after recovery use: %v", err)
	}
}

// A recovery-unlocked envelope may read and write data, but must not rewrite
// key material it cannot re-seal under a root it does not hold.
func TestRecoveryUnlockedEnvelopeCannotRewriteKeysUntilRootAdopted(t *testing.T) {
	path, env, _, rec := newRecoveryEnv(t)
	env.Lock()
	re, err := OpenEnvelopeWithRecovery(path, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := re.RotateDomain(DomainPage); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("RotateDomain on rootless envelope = %v, want Unauthorized", err)
	}
	if err := re.RemoveRecoveryKey(); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("RemoveRecoveryKey on rootless envelope = %v, want Unauthorized", err)
	}
	if err := re.SetRecoveryKey(testRoot(t)); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("SetRecoveryKey on rootless envelope = %v, want Unauthorized", err)
	}

	// Adopting a fresh root is the documented way out, and it must not
	// disturb the recovery path that got us here.
	newRoot := testRoot(t)
	if err := re.RotateRoot(newRoot); err != nil {
		t.Fatalf("RotateRoot on rootless envelope: %v", err)
	}
	if !re.HasRoot() {
		t.Fatal("RotateRoot did not install a root")
	}
	if _, err := re.RotateDomain(DomainPage); err != nil {
		t.Fatalf("RotateDomain after adopting a root: %v", err)
	}
	re.Lock()
	if _, err := OpenEnvelope(path, newRoot); err != nil {
		t.Fatalf("new root does not unlock: %v", err)
	}
	if _, err := OpenEnvelopeWithRecovery(path, rec); err != nil {
		t.Fatalf("recovery key stopped working after root adoption: %v", err)
	}
}

// The recovery wrap seals the KEK, so a plain KEK rotation would invalidate it.
// That must fail closed rather than silently discarding the operator's backup.
func TestRotateKEKFailsClosedWithRecoveryKeyConfigured(t *testing.T) {
	path, env, root, rec := newRecoveryEnv(t)
	if err := env.RotateKEK(); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("RotateKEK with recovery configured = %v, want InvalidArgument", err)
	}
	// Nothing may have changed on disk.
	env.Lock()
	if _, err := OpenEnvelope(path, root); err != nil {
		t.Fatalf("root unlock after refused rotate: %v", err)
	}
	if _, err := OpenEnvelopeWithRecovery(path, rec); err != nil {
		t.Fatalf("recovery unlock after refused rotate: %v", err)
	}
}

func TestRotateKEKWithRecoveryKeepsBothUnlockPaths(t *testing.T) {
	path, env, root, rec := newRecoveryEnv(t)
	before := env.RecoveryKeyVersion()
	if err := env.RotateKEKWithRecovery(rec); err != nil {
		t.Fatalf("RotateKEKWithRecovery: %v", err)
	}
	if got := env.RecoveryKeyVersion(); got != before {
		t.Fatalf("recovery key version changed on KEK rotation: %d -> %d", before, got)
	}
	env.Lock()
	if _, err := OpenEnvelope(path, root); err != nil {
		t.Fatalf("root unlock after KEK rotation: %v", err)
	}
	re, err := OpenEnvelopeWithRecovery(path, rec)
	if err != nil {
		t.Fatalf("recovery unlock after KEK rotation: %v", err)
	}
	if _, err := re.Current(); err != nil {
		t.Fatalf("Current after KEK rotation via recovery: %v", err)
	}
}

func TestRotateKEKWithRecoveryRejectsForeignKey(t *testing.T) {
	_, env, _, _ := newRecoveryEnv(t)
	other, err := GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.RotateKEKWithRecovery(other); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("RotateKEKWithRecovery with a foreign key = %v, want Crypto", err)
	}
}

// Rotations that do not touch the KEK must leave the recovery path alone.
func TestRecoveryKeySurvivesRootAndMasterRotation(t *testing.T) {
	path, env, _, rec := newRecoveryEnv(t)
	newRoot := testRoot(t)
	if err := env.RotateRoot(newRoot); err != nil {
		t.Fatalf("RotateRoot: %v", err)
	}
	if err := env.RotateMaster(); err != nil {
		t.Fatalf("RotateMaster: %v", err)
	}
	if _, err := env.RotateDomain(DomainPage); err != nil {
		t.Fatalf("RotateDomain: %v", err)
	}
	env.Lock()
	re, err := OpenEnvelopeWithRecovery(path, rec)
	if err != nil {
		t.Fatalf("recovery unlock after rotations: %v", err)
	}
	if _, err := re.Current(); err != nil {
		t.Fatalf("Current after rotations: %v", err)
	}
}

func TestSetRecoveryKeyRejectsTheRootKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	root := testRoot(t)
	env, err := CreateEnvelope(path, testIdent(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.SetRecoveryKey(root); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("SetRecoveryKey(root) = %v, want InvalidArgument", err)
	}
	if env.HasRecoveryKey() {
		t.Fatal("refused SetRecoveryKey still configured one")
	}
	if got := onDiskKeystoreVersion(t, path); got != keystoreV1 {
		t.Fatalf("refused SetRecoveryKey wrote v%d", got)
	}
}

func TestVerifyRecoveryKey(t *testing.T) {
	_, env, _, rec := newRecoveryEnv(t)
	if err := env.VerifyRecoveryKey(rec); err != nil {
		t.Fatalf("VerifyRecoveryKey with the real key: %v", err)
	}
	other, err := GenerateDEK(rec.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.VerifyRecoveryKey(other); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("VerifyRecoveryKey with a foreign key = %v, want Crypto", err)
	}
	if err := env.VerifyRecoveryKey(nil); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("VerifyRecoveryKey(nil) = %v, want InvalidArgument", err)
	}
}

func TestVerifyRecoveryKeyWhenNoneConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.keys")
	env, err := CreateEnvelope(path, testIdent(t), testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.VerifyRecoveryKey(testRoot(t)); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("VerifyRecoveryKey with none configured = %v, want NotFound", err)
	}
}

func TestShreddedKeystoreRefusesRecoveryUnlock(t *testing.T) {
	path, env, _, rec := newRecoveryEnv(t)
	if err := env.Shred(ShredPhrase); err != nil {
		t.Fatalf("Shred: %v", err)
	}
	if _, err := OpenEnvelopeWithRecovery(path, rec); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("recovery unlock of a shredded keystore = %v, want Crypto", err)
	}
}

func TestKeyStatusReportsRecoveryKeyPresenceOnly(t *testing.T) {
	_, env, _, rec := newRecoveryEnv(t)
	st, err := env.KeyStatus()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range st {
		if s.Domain == "recovery" {
			found = true
			if s.CurrentVersion != rec.Version {
				t.Fatalf("recovery status version = %d, want %d", s.CurrentVersion, rec.Version)
			}
		}
	}
	if !found {
		t.Fatal("KeyStatus omits the configured recovery key")
	}
	if err := env.RemoveRecoveryKey(); err != nil {
		t.Fatal(err)
	}
	st, err = env.KeyStatus()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if s.Domain == "recovery" {
			t.Fatal("KeyStatus reports a recovery key after removal")
		}
	}
}

func TestRecoveryKeyFileIsNotInterchangeableWithARootKeyFile(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "root.key")
	recPath := filepath.Join(dir, "recovery.key")
	if _, err := CreateKeyFile(rootPath, 1); err != nil {
		t.Fatal(err)
	}
	rec, err := CreateRecoveryKeyFile(recPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKeyFile(recPath); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("ReadKeyFile on a recovery key = %v, want InvalidArgument", err)
	}
	if _, err := ReadRecoveryKeyFile(rootPath); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("ReadRecoveryKeyFile on a root key = %v, want InvalidArgument", err)
	}
	back, err := ReadRecoveryKeyFile(recPath)
	if err != nil {
		t.Fatalf("ReadRecoveryKeyFile round trip: %v", err)
	}
	if !back.Equal(rec) {
		t.Fatal("recovery key file did not round trip")
	}
	if _, err := CreateRecoveryKeyFile(recPath, 2); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("CreateRecoveryKeyFile over an existing file = %v, want AlreadyExists", err)
	}
	fi, err := os.Stat(recPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("recovery key file mode = %v, want 0600", fi.Mode().Perm())
	}
}

// A v2 keystore is never written without a recovery wrap; one that claims v2
// and carries none was truncated or edited, and must not silently downgrade
// the operator to a single unlock path.
func TestDecodeRejectsV2KeystoreWithoutRecoveryWrap(t *testing.T) {
	ks := keystore{
		WrappedKEK:      []byte("kek"),
		WrappedMaster:   []byte("master"),
		RecoveryVersion: 1,
		WrappedRecovery: []byte("recovery"),
	}
	raw, err := encodeKeystore(ks)
	if err != nil {
		t.Fatal(err)
	}
	if got := int(encoding.U16(raw, 4)); got != keystoreV2 {
		t.Fatalf("encoded version = %d, want %d", got, keystoreV2)
	}
	// Zero the recovery wrap length in place and repair the checksum, so it is
	// the empty-wrap rule that rejects the file and not the checksum — without
	// the repair this test would pass even if that rule were deleted.
	off := 58 + len(ks.WrappedKEK) + 2 + len(ks.WrappedMaster) + 4
	encoding.PutU16(raw, off, 0)
	// Dropping the wrap shortens the file; rebuild it so the trailing
	// checksum covers exactly the bytes that remain.
	trimmed := make([]byte, 0, len(raw)-len(ks.WrappedRecovery))
	trimmed = append(trimmed, raw[:off+2]...)
	trimmed = append(trimmed, raw[off+2+len(ks.WrappedRecovery):]...)
	checksum.Write(trimmed, len(trimmed)-4)
	if err := checksum.Verify(trimmed, len(trimmed)-4); err != nil {
		t.Fatalf("test fixture checksum not repaired: %v", err)
	}
	if _, err := decodeKeystore(trimmed); err == nil {
		t.Fatal("decoded a v2 keystore with an empty recovery wrap")
	}
}

func TestKeystoreV2RoundTrip(t *testing.T) {
	ks := keystore{
		KEKVersion:      3,
		MasterVersion:   4,
		NonceHigh:       99,
		WrappedKEK:      []byte("kek-wrap"),
		WrappedMaster:   []byte("master-wrap"),
		RecoveryVersion: 7,
		WrappedRecovery: []byte("recovery-wrap"),
	}
	raw, err := encodeKeystore(ks)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeKeystore(raw)
	if err != nil {
		t.Fatal(err)
	}
	if back.RecoveryVersion != ks.RecoveryVersion {
		t.Fatalf("RecoveryVersion = %d, want %d", back.RecoveryVersion, ks.RecoveryVersion)
	}
	if string(back.WrappedRecovery) != string(ks.WrappedRecovery) {
		t.Fatalf("WrappedRecovery = %q, want %q", back.WrappedRecovery, ks.WrappedRecovery)
	}
	if back.KEKVersion != ks.KEKVersion || back.NonceHigh != ks.NonceHigh {
		t.Fatal("v2 round trip lost a v1 field")
	}
}
