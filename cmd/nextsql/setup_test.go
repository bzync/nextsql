package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
)

// smallSetupArgs runs setup with an explicit tiny buffer pool so the test
// initializes a small database regardless of host RAM.
// smallSetupArgs installs a real, servable deployment: `--database` is what
// makes one exist at all (without it `nextsql setup` initializes the
// deployment only), and every caller of this helper wants a database.
func smallSetupArgs(dataDir, keyFile string, extra ...string) []string {
	base := []string{
		"--data-dir", dataDir,
		"--key-file", keyFile,
		"--database", "setuptest",
		"--preset", "custom",
		"--buffer-pages", "8",
	}
	return append(base, extra...)
}

func TestSetupEndToEnd(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("s3cret-passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := setupCmd(smallSetupArgs(dataDir, keyFile, "--user", "app", "--password-file", pw))
	if err != nil {
		t.Fatalf("setupCmd: %v", err)
	}

	// Database initialized.
	if _, err := os.Stat(filepath.Join(dataDir, config.DataFileName)); err != nil {
		t.Fatalf("nextsql.db not created: %v", err)
	}
	// Root and instance key files created off the data volume.
	for _, p := range []string{keyFile, keyFile + ".instance"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("key file %s not created: %v", p, err)
		}
	}
	// Generated config reloads and is valid.
	confPath := filepath.Join(dataDir, "nextsql.conf")
	cfg, err := config.Load(confPath)
	if err != nil {
		t.Fatalf("generated config does not reload: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("generated config invalid: %v", err)
	}
	if cfg.BufferPages != 8 || cfg.DataDir != dataDir || cfg.KeyFile != keyFile {
		t.Fatalf("generated config has unexpected values: %+v", cfg)
	}
	if cfg.ListenAddr != config.DefaultListenAddr {
		t.Errorf("expected loopback default listen, got %q", cfg.ListenAddr)
	}
	// No secret material in the config file.
	body, _ := os.ReadFile(confPath)
	if strings.Contains(string(body), "s3cret-passphrase") {
		t.Fatal("password leaked into the config file")
	}
}

func TestSetupDryRunMutatesNothing(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")

	if err := setupCmd(smallSetupArgs(dataDir, keyFile, "--dry-run")); err != nil {
		t.Fatalf("dry-run setupCmd: %v", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Error("dry-run created the data directory")
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Error("dry-run created the key file")
	}
}

func TestSetupSkipInitRegeneratesConfigOnly(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")

	if err := setupCmd(smallSetupArgs(dataDir, keyFile, "--skip-init")); err != nil {
		t.Fatalf("setupCmd --skip-init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, config.DataFileName)); !os.IsNotExist(err) {
		t.Error("--skip-init initialized a database")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "nextsql.conf")); err != nil {
		t.Errorf("--skip-init did not write the config: %v", err)
	}
	// A second --skip-init run against an identical config is allowed
	// without --force.
	if err := setupCmd(smallSetupArgs(dataDir, keyFile, "--skip-init")); err != nil {
		t.Fatalf("idempotent --skip-init rerun: %v", err)
	}
}

func TestSetupRefusesToClobberDifferentConfig(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")

	if err := setupCmd(smallSetupArgs(dataDir, keyFile, "--skip-init")); err != nil {
		t.Fatal(err)
	}
	// Re-run with a different buffer size and no --force.
	err := setupCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile,
		"--preset", "custom", "--buffer-pages", "16", "--skip-init",
	})
	if !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("expected AlreadyExists without --force, got %v", err)
	}
	// With --force it succeeds.
	if err := setupCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile,
		"--preset", "custom", "--buffer-pages", "16", "--skip-init", "--force",
	}); err != nil {
		t.Fatalf("--force rerun: %v", err)
	}
	cfg, err := config.Load(filepath.Join(dataDir, "nextsql.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BufferPages != 16 {
		t.Errorf("--force did not rewrite the config: buffer_pages = %d", cfg.BufferPages)
	}
}

func TestSetupRefusesSecondInit(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")

	if err := setupCmd(smallSetupArgs(dataDir, keyFile)); err != nil {
		t.Fatal(err)
	}
	if err := setupCmd(smallSetupArgs(dataDir, keyFile, "--force")); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("second full setup should refuse to re-init, got %v", err)
	}
}

func TestSetupMissingKeyFileExit7(t *testing.T) {
	err := setupCmd([]string{"--data-dir", t.TempDir()})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit code = %d, want %d (ExitLocal); err = %v", got, cli.ExitLocal, err)
	}
}

func TestSetupRemoteWithoutTLSExitValidation(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	err := setupCmd(smallSetupArgs(dataDir, keyFile, "--listen", "0.0.0.0:7210", "--skip-init"))
	if got := cli.Code(err); got != cli.ExitValidation {
		t.Fatalf("exit code = %d, want %d (ExitValidation); err = %v", got, cli.ExitValidation, err)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "nextsql.conf")); statErr == nil {
		t.Error("a rejected plan still wrote a config file")
	}
}

func TestSetupUserWithoutPasswordRejected(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	err := setupCmd(smallSetupArgs(dataDir, keyFile, "--user", "app"))
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

// emptyPasswordFile makes setup's init step fail *after* the database and
// keys have been created (bootstrapDeploymentUser rejects a blank password).
func emptyPasswordFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(p, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSetupRollsBackAPartialInstall(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	pw := emptyPasswordFile(t)

	err := setupCmd(smallSetupArgs(dataDir, keyFile, "--user", "app", "--password-file", pw))
	if err == nil {
		t.Fatal("expected setup to fail on the blank password")
	}
	// Everything setup created must be gone.
	for _, p := range []string{
		filepath.Join(dataDir, config.DataFileName),
		filepath.Join(dataDir, config.DataFileName) + ".keys",
		keyFile, keyFile + ".instance",
		filepath.Join(dataDir, "nextsql.conf"),
	} {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			t.Errorf("rollback left %s behind (%v)", p, statErr)
		}
	}
	// The data dir it created should be gone too (it came out empty).
	if _, statErr := os.Stat(dataDir); !os.IsNotExist(statErr) {
		t.Errorf("rollback left the created data dir behind")
	}
}

func TestSetupRollbackPreservesPreexistingKeyAndDataDir(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	pw := emptyPasswordFile(t)

	// Operator supplies their own root key and an already-created data dir
	// (with an unrelated file in it).
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dataDir, "operator-notes.txt")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := crypto.CreateKeyFile(keyFile, 1)
	if err != nil {
		t.Fatal(err)
	}
	k.Zero()

	err = setupCmd(smallSetupArgs(dataDir, keyFile, "--user", "app", "--password-file", pw))
	if err == nil {
		t.Fatal("expected setup to fail")
	}
	if _, statErr := os.Stat(keyFile); statErr != nil {
		t.Errorf("rollback destroyed the operator's pre-existing key: %v", statErr)
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Errorf("rollback destroyed the operator's pre-existing data dir / file: %v", statErr)
	}
	// But the database it created is cleaned up.
	if _, statErr := os.Stat(filepath.Join(dataDir, config.DataFileName)); !os.IsNotExist(statErr) {
		t.Errorf("rollback left the created database behind")
	}
}

// TestSetupKeyFileExistsDisclosure covers the generate-vs-import advisory
// (`TODO.md` Phase 28 "Installer UX" — makes `nextsql init`'s already-true
// "existing key is imported, missing key is generated" behavior explicit in
// --json output rather than leaving it implicit). Dry-run only: it must
// never mutate anything to answer the question.
func TestSetupKeyFileExistsDisclosure(t *testing.T) {
	parseResult := func(t *testing.T, args []string) setupResult {
		t.Helper()
		out, err := captureStdout(func() error { return setupCmd(args) })
		if err != nil {
			t.Fatalf("setupCmd: %v", err)
		}
		var r setupResult
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("invalid JSON output: %v\n%s", err, out)
		}
		return r
	}

	t.Run("fresh path reports generate", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "data")
		keyFile := filepath.Join(t.TempDir(), "root.key")

		r := parseResult(t, smallSetupArgs(dataDir, keyFile, "--dry-run", "--json"))
		if r.KeyFileExists {
			t.Error("expected key_file_exists=false for a path with no file")
		}
		if r.InstanceKeyExists {
			t.Error("expected instance_key_exists=false for a path with no file")
		}
		if !containsSubstring(r.Warnings, "a new root unlock key will be generated") {
			t.Errorf("expected a generate advisory, got warnings: %v", r.Warnings)
		}
		if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
			t.Error("dry-run must not create the key file it reports on")
		}
	})

	t.Run("pre-existing key reports import", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "data")
		keyFile := filepath.Join(t.TempDir(), "root.key")
		k, err := crypto.CreateKeyFile(keyFile, 1)
		if err != nil {
			t.Fatal(err)
		}
		k.Zero()

		r := parseResult(t, smallSetupArgs(dataDir, keyFile, "--dry-run", "--json"))
		if !r.KeyFileExists {
			t.Error("expected key_file_exists=true for a pre-existing key file")
		}
		if !containsSubstring(r.Warnings, "will be imported and reused, not regenerated or overwritten") {
			t.Errorf("expected an import advisory, got warnings: %v", r.Warnings)
		}
	})
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func TestSetupProductionProfileWritesLiveDefaults(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("s3cret-passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := setupCmd(smallSetupArgs(dataDir, keyFile,
		"--profile", "production",
		"--user", "app", "--password-file", pw))
	if err != nil {
		t.Fatalf("setupCmd --profile production: %v", err)
	}
	cfg, err := config.Load(filepath.Join(dataDir, "nextsql.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeploymentProfile != config.ProfileProduction {
		t.Fatalf("deployment_profile = %q", cfg.DeploymentProfile)
	}
	if cfg.DiskWatermarkCheckMS != config.ProductionDiskWatermarkCheckMS {
		t.Errorf("disk_watermark_check_ms = %d", cfg.DiskWatermarkCheckMS)
	}
	if cfg.StatementTimeoutMS != config.ProductionStatementTimeoutMS {
		t.Errorf("statement_timeout_ms = %d", cfg.StatementTimeoutMS)
	}
	if err := cfg.CheckProduction(); err != nil {
		t.Fatal(err)
	}
}

func TestSetupProductionRequiresAdmin(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	err := setupCmd(smallSetupArgs(dataDir, keyFile, "--profile", "production"))
	if got := cli.Code(err); got != cli.ExitValidation {
		t.Fatalf("exit code = %d, want %d; err = %v", got, cli.ExitValidation, err)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "nextsql.conf")); statErr == nil {
		t.Error("a rejected production install still wrote a config file")
	}
}

func TestSetupProductionSkipInitRejected(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	err := setupCmd(smallSetupArgs(dataDir, keyFile, "--profile", "production", "--skip-init"))
	if got := cli.Code(err); got != cli.ExitValidation {
		t.Fatalf("exit code = %d, want %d; err = %v", got, cli.ExitValidation, err)
	}
}

func TestSetupProductionDryRunWithoutAdmin(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	out, err := captureStdout(func() error {
		return setupCmd(smallSetupArgs(dataDir, keyFile, "--profile", "production", "--dry-run", "--json"))
	})
	if err != nil {
		t.Fatalf("dry-run production without admin should succeed: %v", err)
	}
	if strings.Contains(out, `"profile": "production"`) == false && strings.Contains(out, `"profile":"production"`) == false {
		// indented JSON from encoder
		if !strings.Contains(out, "production") {
			t.Fatalf("expected production profile in JSON, got %s", out)
		}
	}
	if !strings.Contains(out, "requires --user") {
		t.Fatalf("expected an admin-required warning, got %s", out)
	}
	if _, statErr := os.Stat(dataDir); !os.IsNotExist(statErr) {
		t.Error("dry-run created the data directory")
	}
}

func TestSetupKeepFailedLeavesPartialInstall(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	pw := emptyPasswordFile(t)

	err := setupCmd(smallSetupArgs(dataDir, keyFile, "--user", "app", "--password-file", pw, "--keep-failed"))
	if err == nil {
		t.Fatal("expected setup to fail")
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, config.DataFileName)); statErr != nil {
		t.Errorf("--keep-failed should have left the partial database in place: %v", statErr)
	}
}

// --- recovery-key export during a first install (`TODO.md` Phase 28 M2
// "Recovery-key export/verification UX", the CLI half). The capability
// itself is covered by key_test.go; these tests are about setup producing a
// deployment that is recoverable, and refusing rather than half-doing it.

// setupRecoveryResult runs setup with --json and decodes the result.
func setupRecoveryResult(t *testing.T, args []string) setupResult {
	t.Helper()
	out, err := captureStdout(func() error { return setupCmd(append([]string{"--json"}, args...)) })
	if err != nil {
		t.Fatalf("setupCmd: %v (output %s)", err, out)
	}
	var r setupResult
	if decErr := json.Unmarshal([]byte(out), &r); decErr != nil {
		t.Fatalf("decode setup --json: %v (output %s)", decErr, out)
	}
	return r
}

// TestSetupExportsAndVerifiesRecoveryKeys is the whole point of the flag: an
// install that asked for recovery keys must come out with a second, working
// unlock path for *both* of the deployment's keystores, and the exported
// files must actually open them from disk.
func TestSetupExportsAndVerifiesRecoveryKeys(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyDir := t.TempDir()
	keyFile := filepath.Join(keyDir, "root.key")
	recOut := filepath.Join(keyDir, "root.recovery.key")
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("s3cret-passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := setupRecoveryResult(t, smallSetupArgs(dataDir, keyFile,
		"--user", "app", "--password-file", pw, "--recovery-key-out", recOut))
	if !r.RecoveryKeysCreated {
		t.Fatalf("recovery_keys_created is false: %+v", r)
	}
	if r.RecoveryKeyFile != recOut {
		t.Errorf("recovery_key_file = %q, want %q", r.RecoveryKeyFile, recOut)
	}
	wantInstance := recOut + ".instance"
	if r.InstanceRecoveryKeyFile != wantInstance {
		t.Errorf("instance_recovery_key_file = %q, want %q", r.InstanceRecoveryKeyFile, wantInstance)
	}

	// Both exported files exist, are mode 0600, and are recovery keys — not
	// root keys under a different name.
	for _, p := range []string{recOut, wantInstance} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("exported recovery key %s missing: %v", p, err)
		}
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode is %v, want 0600", p, perm)
		}
		if _, err := crypto.ReadRecoveryKeyFile(p); err != nil {
			t.Errorf("%s is not a readable recovery key file: %v", p, err)
		}
		if _, err := crypto.ReadKeyFile(p); err == nil {
			t.Errorf("%s was accepted as a root unlock key file; the two must be distinguishable", p)
		}
	}

	// The real assertion: with both root keys deleted, each recovery key
	// still opens its own keystore. This is the disaster the feature exists
	// for, run against what setup actually wrote.
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyFile + ".instance"); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct{ recFile, keystore string }{
		{recOut, keystoreDatabase},
		{wantInstance, keystoreInstance},
	} {
		ksPath, err := keystorePathFor(dataDir, pair.keystore)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := crypto.ReadRecoveryKeyFile(pair.recFile)
		if err != nil {
			t.Fatalf("read %s: %v", pair.recFile, err)
		}
		env, err := crypto.OpenEnvelopeWithRecovery(ksPath, rec)
		if err != nil {
			t.Fatalf("%s does not unlock the %s keystore with its root key gone: %v",
				pair.recFile, pair.keystore, err)
		}
		if got := env.KeystoreFormatVersion(); got != 2 {
			t.Errorf("%s keystore format version = %d, want 2 (a recovery key was configured)", pair.keystore, got)
		}
		_ = env.Close()
	}
}

// TestSetupWithoutRecoveryKeyStaysAtKeystoreV1 protects the opt-in property:
// an install that did not ask for recovery keys must write exactly what a
// release predating the format wrote, so it stays readable on rollback.
func TestSetupWithoutRecoveryKeyStaysAtKeystoreV1(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("s3cret-passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := setupRecoveryResult(t, smallSetupArgs(dataDir, keyFile, "--user", "app", "--password-file", pw))
	if r.RecoveryKeysCreated || r.RecoveryKeyFile != "" || r.InstanceRecoveryKeyFile != "" {
		t.Fatalf("an install that asked for no recovery key reported one: %+v", r)
	}
	for _, which := range []string{keystoreDatabase, keystoreInstance} {
		ksPath, err := keystorePathFor(dataDir, which)
		if err != nil {
			t.Fatal(err)
		}
		env, err := crypto.OpenLocked(ksPath)
		if err != nil {
			t.Fatal(err)
		}
		if env.HasRecoveryKey() {
			t.Errorf("%s keystore has a recovery key configured without being asked", which)
		}
		if got := env.KeystoreFormatVersion(); got != 1 {
			t.Errorf("%s keystore format version = %d, want 1 for a non-opted-in install", which, got)
		}
		_ = env.Close()
	}
	// And the operator is told, rather than left to discover it.
	if !hasWarningContaining(r.Warnings, "the root unlock key will be the only way to open this deployment") {
		t.Errorf("no advisory that the deployment has a single unlock path: %v", r.Warnings)
	}
}

// TestSetupDryRunDisclosesRecoveryKeysWithoutWriting keeps --dry-run
// authoritative for the GUI: it must report the same paths a real run would
// use and still create nothing.
func TestSetupDryRunDisclosesRecoveryKeysWithoutWriting(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyDir := t.TempDir()
	keyFile := filepath.Join(keyDir, "root.key")
	recOut := filepath.Join(keyDir, "root.recovery.key")

	r := setupRecoveryResult(t, smallSetupArgs(dataDir, keyFile, "--dry-run", "--recovery-key-out", recOut))
	if r.RecoveryKeyFile != recOut || r.InstanceRecoveryKeyFile != recOut+".instance" {
		t.Errorf("dry run did not disclose the recovery key paths: %+v", r)
	}
	if r.RecoveryKeysCreated {
		t.Error("dry run claims recovery keys were created")
	}
	if !hasWarningContaining(r.Warnings, "a recovery key for the database keystore will be generated") {
		t.Errorf("dry run did not advise what it would write: %v", r.Warnings)
	}
	for _, p := range []string{recOut, recOut + ".instance", keyFile, dataDir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("dry run created %s", p)
		}
	}
}

// TestSetupRefusesRecoveryKeyOutThatExists — setup never overwrites key
// material, and it must refuse before it builds a database, not after.
func TestSetupRefusesRecoveryKeyOutThatExists(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyDir := t.TempDir()
	keyFile := filepath.Join(keyDir, "root.key")
	recOut := filepath.Join(keyDir, "root.recovery.key")
	if err := os.WriteFile(recOut, []byte("someone else's key"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := setupCmd(smallSetupArgs(dataDir, keyFile, "--recovery-key-out", recOut))
	if !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("expected already_exists, got %v", err)
	}
	if body, readErr := os.ReadFile(recOut); readErr != nil || string(body) != "someone else's key" {
		t.Fatalf("the pre-existing file was modified (%v, %q)", readErr, body)
	}
	if _, statErr := os.Stat(dataDir); !os.IsNotExist(statErr) {
		t.Error("setup built a data directory before refusing")
	}
}

// TestSetupRefusesRecoveryKeyPathCollisions — a "recovery" key written over
// the root key it backs up, or two keystores sharing one recovery file, is
// not a second unlock path.
func TestSetupRefusesRecoveryKeyPathCollisions(t *testing.T) {
	keyDir := t.TempDir()
	keyFile := filepath.Join(keyDir, "root.key")
	cases := []struct {
		name  string
		extra []string
	}{
		{"recovery over root", []string{"--recovery-key-out", keyFile}},
		{"recovery over instance root", []string{"--recovery-key-out", keyFile + ".instance"}},
		{"both keystores share one file", []string{
			"--recovery-key-out", filepath.Join(keyDir, "rec.key"),
			"--instance-recovery-key-out", filepath.Join(keyDir, "rec.key"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "data")
			err := setupCmd(smallSetupArgs(dataDir, keyFile, append([]string{"--dry-run"}, tc.extra...)...))
			if err == nil {
				t.Fatal("expected setup to refuse the colliding paths")
			}
			if code := cli.Code(err); code != cli.ExitValidation {
				t.Fatalf("exit code %d, want %d (validation): %v", code, cli.ExitValidation, err)
			}
		})
	}
}

// TestSetupRecoveryKeyRejectedWithSkipInit — --skip-init creates no keystore,
// so there is nothing to seal a recovery key into.
func TestSetupRecoveryKeyRejectedWithSkipInit(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyDir := t.TempDir()
	err := setupCmd(smallSetupArgs(dataDir, filepath.Join(keyDir, "root.key"),
		"--skip-init", "--recovery-key-out", filepath.Join(keyDir, "rec.key")))
	if code := cli.Code(err); code != cli.ExitValidation {
		t.Fatalf("exit code %d, want %d (validation): %v", code, cli.ExitValidation, err)
	}
}

// TestSetupInstanceRecoveryKeyOutAloneRejected — a deployment recovered
// halfway is not recovered: both keystores or neither.
func TestSetupInstanceRecoveryKeyOutAloneRejected(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyDir := t.TempDir()
	err := setupCmd(smallSetupArgs(dataDir, filepath.Join(keyDir, "root.key"),
		"--dry-run", "--instance-recovery-key-out", filepath.Join(keyDir, "rec.key.instance")))
	if code := cli.Code(err); code != cli.ExitValidation {
		t.Fatalf("exit code %d, want %d (validation): %v", code, cli.ExitValidation, err)
	}
}

// TestSetupRollbackRemovesExportedRecoveryKeys — a failed install must not
// leave key files behind that unlock a database it just deleted.
func TestSetupRollbackRemovesExportedRecoveryKeys(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyDir := t.TempDir()
	keyFile := filepath.Join(keyDir, "root.key")
	recOut := filepath.Join(keyDir, "root.recovery.key")
	pw := emptyPasswordFile(t)

	err := setupCmd(smallSetupArgs(dataDir, keyFile,
		"--user", "app", "--password-file", pw, "--recovery-key-out", recOut))
	if err == nil {
		t.Fatal("expected setup to fail on the blank password")
	}
	for _, p := range []string{recOut, recOut + ".instance", keyFile, keyFile + ".instance"} {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			t.Errorf("rollback left key material at %s (%v)", p, statErr)
		}
	}
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// `nextsql setup` follows the same rule as `nextsql init`: no --database, no
// database. The two things that then have nothing to act on say so instead of
// half-working — a production install (which could not serve) and a recovery
// key (which would have no keystore to seal).
func TestSetupWithoutDatabaseInitializesTheDeploymentOnly(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	passwordFile := writeTempFile(t, "adminpassword\n")
	args := []string{
		"--data-dir", dataDir,
		"--key-file", keyFile,
		"--preset", "custom", "--buffer-pages", "8",
		"--user", "admin", "--password-file", passwordFile,
	}
	if err := setupCmd(args); err != nil {
		t.Fatalf("deployment-only setup: %v", err)
	}
	for _, name := range []string{config.DataFileName, "nextsql.instance"} {
		if _, err := os.Stat(filepath.Join(dataDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s must not exist without a database: %v", name, err)
		}
	}
	// The configuration is still written: it is what the later
	// `nextsql init --database NAME` and the server both read.
	if _, err := os.Stat(filepath.Join(dataDir, "nextsql.conf")); err != nil {
		t.Fatalf("config: %v", err)
	}
}

func TestSetupProductionRequiresADatabase(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	passwordFile := writeTempFile(t, "adminpassword\n")
	err := setupCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile,
		"--preset", "custom", "--buffer-pages", "8",
		"--profile", "production",
		"--user", "admin", "--password-file", passwordFile,
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("production without a database: %v", err)
	}
	if _, statErr := os.Stat(dataDir); !os.IsNotExist(statErr) {
		t.Fatalf("a refused setup must write nothing: %v", statErr)
	}
}

func TestSetupRecoveryKeyRequiresADatabase(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	keyFile := filepath.Join(t.TempDir(), "root.key")
	recovery := filepath.Join(t.TempDir(), "recovery.key")
	err := setupCmd([]string{
		"--data-dir", dataDir, "--key-file", keyFile,
		"--preset", "custom", "--buffer-pages", "8",
		"--recovery-key-out", recovery,
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("recovery key without a database: %v", err)
	}
	if _, statErr := os.Stat(recovery); !os.IsNotExist(statErr) {
		t.Fatal("a refused setup must not write key material")
	}
}
