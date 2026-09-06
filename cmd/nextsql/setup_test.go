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
func smallSetupArgs(dataDir, keyFile string, extra ...string) []string {
	base := []string{
		"--data-dir", dataDir,
		"--key-file", keyFile,
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
