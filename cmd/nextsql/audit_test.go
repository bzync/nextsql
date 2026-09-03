package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func TestAuditRequiresSubcommand(t *testing.T) {
	err := auditCmd(nil)
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestAuditRejectsUnknown(t *testing.T) {
	err := auditCmd([]string{"bogus"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestAuditKeygenRotateRetireListExportPublicCLI(t *testing.T) {
	dir := t.TempDir()
	keyset := filepath.Join(dir, "audit.keys")
	if err := auditKeygen([]string{"--keyset", keyset}); err != nil {
		t.Fatal(err)
	}
	if err := auditKeygen([]string{"--keyset", keyset}); err == nil {
		t.Fatal("expected error creating over an existing keyset")
	}
	if err := auditRotate([]string{"--keyset", keyset}); err != nil {
		t.Fatal(err)
	}
	ks, err := security.OpenAuditKeyset(keyset)
	if err != nil {
		t.Fatal(err)
	}
	list := ks.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 keys after rotate, got %d", len(list))
	}
	if err := auditListKeys([]string{"--keyset", keyset}); err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(dir, "audit.pub")
	if err := auditExportPublic([]string{"--keyset", keyset, "--out", pub}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pub); err != nil {
		t.Fatal(err)
	}
	oldest := list[0].ID
	if err := auditRetire([]string{"--keyset", keyset, "--key-id", itoa(oldest)}); err != nil {
		t.Fatal(err)
	}
}

func TestAuditVerifyCLI(t *testing.T) {
	dir := t.TempDir()
	keyset := filepath.Join(dir, "audit.keys")
	ks, err := security.CreateAuditKeyset(keyset)
	if err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(dir, "audit.pub")
	if err := ks.WritePublic(pub); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "audit.log")
	l, err := security.OpenAudit(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetSigningKeys(ks); err != nil {
		t.Fatal(err)
	}
	l.Record(security.Event{Actor: "app", Action: security.ActionAuthSuccess, Outcome: "success"})
	l.Record(security.Event{Actor: "app", Action: security.ActionAuthFailure, Outcome: "failure"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	if err := auditVerify([]string{"--file", logPath, "--pubkey", pub}); err != nil {
		t.Fatalf("verify with pubkey should pass: %v", err)
	}
	if err := auditVerify([]string{"--file", logPath}); err != nil {
		t.Fatalf("verify without a key should still pass the chain: %v", err)
	}
	if err := auditVerify([]string{"--file", logPath, "--keyset", keyset, "--pubkey", pub}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("mutually exclusive flags should be rejected: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), raw...)
	for i := range tampered {
		if tampered[i] == 'f' { // corrupt the first "failure" byte
			tampered[i] = 'x'
			break
		}
	}
	tamperedPath := filepath.Join(dir, "tampered.log")
	if err := os.WriteFile(tamperedPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := auditVerify([]string{"--file", tamperedPath, "--pubkey", pub}); err == nil {
		t.Fatal("expected tampered log to fail verification")
	}
}

func TestAuditVerifyLegacyFileCLI(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "legacy.log")
	legacy := `{"time":"2026-01-01T00:00:00Z","actor":"app","action":"auth.success","outcome":"success"}` + "\n"
	if err := os.WriteFile(logPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := auditVerify([]string{"--file", logPath}); err != nil {
		t.Fatalf("legacy file should verify without error: %v", err)
	}
}

func itoa(id uint32) string {
	return strconv.FormatUint(uint64(id), 10)
}
