package main

import (
	"bytes"
	"encoding/json"
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

// The JSON report must keep the keys operators already parse and additionally
// classify a torn tail, so "nextsqld repairs this on its next start" can be
// told apart from "do not start it until you know what happened".
func TestAuditVerifyCLIReportsATornTail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	l, err := security.OpenAudit(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		l.Record(security.Event{Actor: "app", Action: security.ActionAuthSuccess, Outcome: "success"})
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	decode := func(t *testing.T, path string) map[string]any {
		t.Helper()
		stdout := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = w
		verr := auditVerify([]string{"--file", path, "--json"})
		w.Close()
		os.Stdout = stdout
		var out bytes.Buffer
		if _, err := out.ReadFrom(r); err != nil {
			t.Fatal(err)
		}
		_ = verr
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("--json did not emit one JSON object: %v (%q)", err, out.String())
		}
		return got
	}

	// A clean file: every key an operator already parses is still present.
	clean := decode(t, logPath)
	for _, k := range []string{
		"file", "verified", "lines", "legacy", "chained", "signed",
		"signing_started", "signatures_checked", "first_bad_line", "problem",
	} {
		if _, ok := clean[k]; !ok {
			t.Fatalf("--json dropped the %q key", k)
		}
	}
	if clean["verified"] != true || clean["torn_tail"] != false {
		t.Fatalf("clean file reported %v", clean)
	}

	// A torn tail: unverified, as before, but now classified.
	tornPath := filepath.Join(dir, "torn.log")
	if err := os.WriteFile(tornPath, raw[:len(raw)-18], 0o600); err != nil {
		t.Fatal(err)
	}
	torn := decode(t, tornPath)
	if torn["verified"] != false {
		t.Fatal("a torn tail must still report verified=false")
	}
	if torn["problem"] != "malformed JSON line" {
		t.Fatalf("problem = %v, want the malformed-line message", torn["problem"])
	}
	if torn["torn_tail"] != true {
		t.Fatalf("torn tail was not classified: %v", torn)
	}

	// Real mid-chain damage must not be classified as repairable.
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	lines[1] = bytes.Replace(lines[1], []byte(`"success"`), []byte(`"failure"`), 1)
	badPath := filepath.Join(dir, "tampered-mid.log")
	if err := os.WriteFile(badPath, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := decode(t, badPath)
	if bad["verified"] != false || bad["torn_tail"] != false {
		t.Fatalf("mid-chain tampering must not be classified as a torn tail: %v", bad)
	}
}
