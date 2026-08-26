package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/nerr"
)

func TestExecSQLTextPositional(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	c := fs.String("c", "", "")
	if err := fs.Parse([]string{"SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	got, err := execSQLText(fs, *c)
	if err != nil || got != "SELECT 1" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestExecSQLTextDashCWins(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	c := fs.String("c", "", "")
	if err := fs.Parse([]string{"-c", "SELECT 2"}); err != nil {
		t.Fatal(err)
	}
	got, err := execSQLText(fs, *c)
	if err != nil || got != "SELECT 2" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestExecSQLTextDashCWinsOverPositional(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	c := fs.String("c", "", "")
	if err := fs.Parse([]string{"-c", "SELECT 1", "SELECT 2"}); err != nil {
		t.Fatal(err)
	}
	got, err := execSQLText(fs, *c)
	if err != nil || got != "SELECT 1" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestExecSQLRequiresSQL(t *testing.T) {
	t.Setenv("NEXTSQL_USER", "app")
	t.Setenv("NEXTSQL_PASSWORD", "secret")
	t.Setenv("NEXTSQL_INSECURE", "true")
	err := execSQL([]string{"--no-env"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "SQL is required") {
		t.Fatalf("%v", err)
	}
}

func TestExecSQLRejectsDataDirFlag(t *testing.T) {
	err := execSQL([]string{"--no-env", "--data-dir", "/tmp"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "local command") {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "SQL is required") {
		t.Fatalf("mode error should precede SQL-required: %v", err)
	}
}

func TestExecSQLRejectsURLAddress(t *testing.T) {
	err := execSQL([]string{
		"--no-env",
		"--addr", "nextsql://127.0.0.1:7210",
		"--user", "app",
		"--insecure",
		"-c", "SELECT 1",
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("%v", err)
	}
}

func TestStatusRejectsAddrAndLocal(t *testing.T) {
	err := statusDB([]string{"--no-env", "--local", "--addr", "127.0.0.1:7210"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "--local or --addr") {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitUsage {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestStatusLocalMissingDataDir(t *testing.T) {
	t.Setenv("NEXTSQL_DATA_DIR", "")
	t.Setenv("NEXTSQL_KEY_FILE", "")
	err := statusDB([]string{"--no-env", "--local"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "--data-dir and --key-file") {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitLocal {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestStatusServerRejectsDataDir(t *testing.T) {
	err := statusDB([]string{"--no-env", "--data-dir", "/tmp"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "status --local") {
		t.Fatalf("%v", err)
	}
}

func TestStatusServerRequiresUser(t *testing.T) {
	t.Setenv("NEXTSQL_USER", "")
	t.Setenv("NEXTSQL_PASSWORD", "")
	t.Setenv("NEXTSQL_PASSWORD_FILE", "")
	err := statusDB([]string{"--no-env", "--insecure"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitUsage {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestLocalCommandsMissingDataDirExit7(t *testing.T) {
	cases := []struct {
		name string
		fn   func() error
	}{
		{"init", func() error { return initDB(nil) }},
		{"diagnose", func() error { return diagnoseDB(nil) }},
		{"cluster", func() error { return clusterCmd([]string{"status"}) }},
		{"backup", func() error { return backupDB([]string{"--out", "/tmp/out"}) }},
		{"verify", func() error { return verifyBackup([]string{"--from", "/tmp/from"}) }},
	}
	for _, tc := range cases {
		err := tc.fn()
		if cli.Code(err) != cli.ExitLocal {
			t.Errorf("%s: Code(%v)=%d want %d", tc.name, err, cli.Code(err), cli.ExitLocal)
		}
	}
}

func TestUnknownCommandExitUsage(t *testing.T) {
	err := run([]string{"not-a-command"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitUsage {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestExecSQLRequiresPassword(t *testing.T) {
	t.Setenv("NEXTSQL_PASSWORD", "")
	t.Setenv("NEXTSQL_PASSWORD_FILE", "")
	err := execSQL([]string{"--no-env", "--user", "app", "--insecure", "-c", "SELECT 1"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestMigrateRequiresSubcommand(t *testing.T) {
	err := migrateCmd(nil)
	if !nerr.HasCode(err, nerr.InvalidArgument) || !strings.Contains(err.Error(), "expected status") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateRejectsUnknown(t *testing.T) {
	err := migrateCmd([]string{"bogus", "--no-env"})
	if !nerr.HasCode(err, nerr.InvalidArgument) || !strings.Contains(err.Error(), "unknown migrate command") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateDownRequiresUser(t *testing.T) {
	err := migrateDown([]string{"--no-env"})
	if err == nil || !nerr.HasCode(err, nerr.InvalidArgument) || !strings.Contains(err.Error(), "user is required") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateForceRequiresConfirm(t *testing.T) {
	err := migrateForce([]string{"--no-env", "20260818120000"})
	if !nerr.HasCode(err, nerr.InvalidArgument) || err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateRepairRequiresConfirm(t *testing.T) {
	err := migrateRepair([]string{"--no-env"})
	if !nerr.HasCode(err, nerr.InvalidArgument) || err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateValidateOK(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "migrate", "testdata", "ok")
	if err := migrateValidate([]string{"--no-env", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateValidateRejects(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "migrate", "testdata", "invalid", "begin")
	err := migrateValidate([]string{"--no-env", "--dir", dir})
	if err == nil || !strings.Contains(err.Error(), "BEGIN/COMMIT/ROLLBACK") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateValidateRejectsDataDir(t *testing.T) {
	err := migrateValidate([]string{"--no-env", "--data-dir", "/tmp"})
	if !nerr.HasCode(err, nerr.InvalidArgument) || err == nil || !strings.Contains(err.Error(), "local command") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateCreateAndValidate(t *testing.T) {
	dir := t.TempDir()
	if err := migrateCreate([]string{"--no-env", "--dir", dir, "add_orders"}); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("files %d", len(ents))
	}
	if err := migrateValidate([]string{"--no-env", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateCreateRequiresName(t *testing.T) {
	err := migrateCreate([]string{"--no-env", "--dir", t.TempDir()})
	if !nerr.HasCode(err, nerr.InvalidArgument) || err == nil || !strings.Contains(err.Error(), "NAME") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateDirFromEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20260818120000_t.up.sql"), []byte("ANALYZE;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXTSQL_MIGRATIONS_DIR", dir)
	if err := migrateValidate([]string{"--no-env"}); err != nil {
		t.Fatal(err)
	}
}
