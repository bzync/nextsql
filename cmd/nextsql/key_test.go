package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
)

// newKeyTestDeployment initializes a real deployment and returns its data
// directory plus the two root key files init created.
func newKeyTestDeployment(t *testing.T) (dataDir, keyFile, instanceKeyFile string) {
	t.Helper()
	dataDir = t.TempDir()
	secrets := t.TempDir()
	keyFile = filepath.Join(secrets, "database.key")
	instanceKeyFile = filepath.Join(secrets, "instance.key")
	if err := initDB([]string{
		"--data-dir", dataDir,
		"--key-file", keyFile,
		"--instance-key-file", instanceKeyFile,
		"--database", "keytest", // a keystore only exists once a database does
		"--buffer-pages", "8",
	}); err != nil {
		t.Fatal(err)
	}
	return dataDir, keyFile, instanceKeyFile
}

func TestKeyCommandRejectsUnknownSubcommandAndKeystore(t *testing.T) {
	if err := keyCmd(nil); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("keyCmd() = %v, want InvalidArgument", err)
	}
	if err := keyCmd([]string{"frobnicate"}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("unknown subcommand = %v, want InvalidArgument", err)
	}
	dataDir, keyFile, _ := newKeyTestDeployment(t)
	err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile,
		"--recovery-key-out", filepath.Join(t.TempDir(), "r.key"),
		"--keystore", "nonsense",
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("unknown --keystore = %v, want InvalidArgument", err)
	}
}

func TestKeyAddRecoveryThenRecoverRestoresAccess(t *testing.T) {
	dataDir, keyFile, _ := newKeyTestDeployment(t)
	secrets := t.TempDir()
	recPath := filepath.Join(secrets, "recovery.key")

	if err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile, "--recovery-key-out", recPath,
	}); err != nil {
		t.Fatalf("add-recovery: %v", err)
	}
	if err := keyVerifyRecoveryCmd([]string{
		"--data-dir", dataDir, "--recovery-key", recPath,
	}); err != nil {
		t.Fatalf("verify-recovery: %v", err)
	}

	// Lose the root key entirely; only the recovery key remains.
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(secrets, "new-root.key")
	if err := keyRecoverCmd([]string{
		"--data-dir", dataDir, "--recovery-key", recPath,
		"--key-file-out", newRoot, "--confirm",
	}); err != nil {
		t.Fatalf("recover: %v", err)
	}

	root, err := crypto.ReadKeyFile(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	env, err := crypto.OpenEnvelope(crypto.KeystorePath(filepath.Join(dataDir, "nextsql.db")), root)
	if err != nil {
		t.Fatalf("recovered root does not open the keystore: %v", err)
	}
	defer env.Close()
	// The recovery key seals the KEK, which recovery did not change, so it
	// must keep working — the operator should not silently lose their backup
	// by using it once.
	if err := env.VerifyRecoveryKey(mustReadRecovery(t, recPath)); err != nil {
		t.Fatalf("recovery key stopped working after recover: %v", err)
	}
}

func TestKeyRecoverRefusesWithoutConfirm(t *testing.T) {
	dataDir, keyFile, _ := newKeyTestDeployment(t)
	secrets := t.TempDir()
	recPath := filepath.Join(secrets, "recovery.key")
	if err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile, "--recovery-key-out", recPath,
	}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(secrets, "new-root.key")
	if err := keyRecoverCmd([]string{
		"--data-dir", dataDir, "--recovery-key", recPath, "--key-file-out", out,
	}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("recover without --confirm = %v, want InvalidArgument", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("refused recover still wrote a root key file")
	}
}

func TestKeyRemoveRecoveryRequiresConfirm(t *testing.T) {
	dataDir, keyFile, _ := newKeyTestDeployment(t)
	recPath := filepath.Join(t.TempDir(), "recovery.key")
	if err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile, "--recovery-key-out", recPath,
	}); err != nil {
		t.Fatal(err)
	}
	if err := keyRemoveRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile,
	}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("remove without --confirm = %v, want InvalidArgument", err)
	}
	if err := keyRemoveRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile, "--confirm",
	}); err != nil {
		t.Fatalf("remove-recovery: %v", err)
	}
	if err := keyVerifyRecoveryCmd([]string{
		"--data-dir", dataDir, "--recovery-key", recPath,
	}); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("verify after removal = %v, want NotFound", err)
	}
}

func TestKeyAddRecoveryRefusesToSupersedeWithoutReplace(t *testing.T) {
	dataDir, keyFile, _ := newKeyTestDeployment(t)
	secrets := t.TempDir()
	first := filepath.Join(secrets, "one.key")
	if err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile, "--recovery-key-out", first,
	}); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(secrets, "two.key")
	if err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile, "--recovery-key-out", second,
	}); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("second add without --replace = %v, want AlreadyExists", err)
	}
	if _, err := os.Stat(second); err == nil {
		t.Fatal("refused add still wrote a recovery key file")
	}
	// The original must be untouched by the refusal.
	if err := keyVerifyRecoveryCmd([]string{
		"--data-dir", dataDir, "--recovery-key", first,
	}); err != nil {
		t.Fatalf("original recovery key broken by a refused add: %v", err)
	}
	// With --replace the new key works and the old one stops working.
	if err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile, "--recovery-key-out", second, "--replace",
	}); err != nil {
		t.Fatalf("add --replace: %v", err)
	}
	if err := keyVerifyRecoveryCmd([]string{
		"--data-dir", dataDir, "--recovery-key", second,
	}); err != nil {
		t.Fatalf("replacement key does not unlock: %v", err)
	}
	if err := keyVerifyRecoveryCmd([]string{
		"--data-dir", dataDir, "--recovery-key", first,
	}); err == nil {
		t.Fatal("superseded recovery key still unlocks the keystore")
	}
}

// The deployment registry is sealed under its own root key, so it needs its
// own recovery key; --keystore instance must address it and nothing else.
func TestKeyRecoveryCoversTheDeploymentRegistryKeystore(t *testing.T) {
	dataDir, keyFile, instanceKeyFile := newKeyTestDeployment(t)
	secrets := t.TempDir()
	instRec := filepath.Join(secrets, "instance-recovery.key")
	if err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--keystore", "instance",
		"--key-file", instanceKeyFile, "--recovery-key-out", instRec,
	}); err != nil {
		t.Fatalf("add-recovery --keystore instance: %v", err)
	}
	// It must unlock the registry keystore...
	if err := keyVerifyRecoveryCmd([]string{
		"--data-dir", dataDir, "--keystore", "instance", "--recovery-key", instRec,
	}); err != nil {
		t.Fatalf("verify --keystore instance: %v", err)
	}
	// ...and must not be mistaken for the database's, which has none.
	if err := keyVerifyRecoveryCmd([]string{
		"--data-dir", dataDir, "--recovery-key", instRec,
	}); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("instance recovery key against the database keystore = %v, want NotFound", err)
	}
	// The database root key must be unaffected by registry recovery work.
	if _, err := crypto.ReadKeyFile(keyFile); err != nil {
		t.Fatalf("database root key disturbed: %v", err)
	}
}

func TestKeyStatusReportsBothKeystoresWithoutAnyKey(t *testing.T) {
	dataDir, keyFile, _ := newKeyTestDeployment(t)
	// status must work holding no key at all.
	if err := keyStatusCmd([]string{"--data-dir", dataDir}); err != nil {
		t.Fatalf("key status: %v", err)
	}
	if err := keyStatusCmd([]string{"--data-dir", dataDir, "--json"}); err != nil {
		t.Fatalf("key status --json: %v", err)
	}
	if err := keyStatusCmd(nil); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("key status without --data-dir = %v, want InvalidArgument", err)
	}
	_ = keyFile
}

// A recovery key file must not be usable as a root key file or vice versa;
// the CLI should say which file it actually got.
func TestKeyCommandsRejectSwappedKeyFiles(t *testing.T) {
	dataDir, keyFile, _ := newKeyTestDeployment(t)
	recPath := filepath.Join(t.TempDir(), "recovery.key")
	if err := keyAddRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile, "--recovery-key-out", recPath,
	}); err != nil {
		t.Fatal(err)
	}
	if err := keyVerifyRecoveryCmd([]string{
		"--data-dir", dataDir, "--recovery-key", keyFile,
	}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("root key passed as --recovery-key = %v, want InvalidArgument", err)
	}
	err := keyRemoveRecoveryCmd([]string{
		"--data-dir", dataDir, "--key-file", recPath, "--confirm",
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("recovery key passed as --key-file = %v, want InvalidArgument", err)
	}
}

func mustReadRecovery(t *testing.T, path string) *crypto.DEK {
	t.Helper()
	d, err := crypto.ReadRecoveryKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
