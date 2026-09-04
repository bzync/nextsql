package security

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestAuditChainVerifiesCleanLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || report.Lines != 5 || report.Chained != 5 || report.Legacy != 0 || report.FirstBadLine != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAuditChainResumesAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l1, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	l1.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	l1.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	if err := l1.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	l2.Record(Event{Actor: "app", Action: ActionAuthFailure, Outcome: "failure"})
	if err := l2.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || report.Chained != 3 {
		t.Fatalf("chain did not resume across reopen: %+v", report)
	}
}

// TestTailEvents covers the system.audit_log/system.audit_verify read-model
// source (Manager Security view, the last M4 piece): the ring buffer keeps
// only the last maxEvents records in oldest-first order regardless of how
// many lines the file actually has, maxEvents<=0 retains nothing, and a
// tampered record still comes back in the tail (an operator investigating a
// detected problem needs to see the suspect entry, not have it hidden) even
// though VerifyReport correctly reports the chain broken.
func TestTailEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Object: strconv.Itoa(i), Outcome: "success"})
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// maxEvents <= 0 retains nothing.
	tr, err := TailEvents(path, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Events != nil {
		t.Fatalf("maxEvents=0 must retain no events, got %d", len(tr.Events))
	}
	if !tr.Verified || tr.Chained != 7 {
		t.Fatalf("report wrong with maxEvents=0: %+v", tr.VerifyReport)
	}

	// A cap smaller than the record count keeps only the newest, oldest-first.
	tr, err = TailEvents(path, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Events) != 3 {
		t.Fatalf("want 3 tailed events, got %d", len(tr.Events))
	}
	for i, want := range []string{"4", "5", "6"} {
		if tr.Events[i].Object != want {
			t.Fatalf("tail[%d].Object = %q, want %q (events = %+v)", i, tr.Events[i].Object, want, tr.Events)
		}
	}

	// A cap larger than the record count returns everything, still oldest-first.
	tr, err = TailEvents(path, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Events) != 7 || tr.Events[0].Object != "0" || tr.Events[6].Object != "6" {
		t.Fatalf("uncapped tail wrong: %+v", tr.Events)
	}

	// A tampered record still appears in the tail; the report flags it.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	lines[5] = bytes.Replace(lines[5], []byte(`"5"`), []byte(`"tampered"`), 1)
	if err := os.WriteFile(path, bytes.Join(lines, []byte("\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, err = TailEvents(path, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Verified {
		t.Fatal("tampered file must not report Verified")
	}
	if tr.FirstBadLine != 6 {
		t.Fatalf("FirstBadLine = %d, want 6", tr.FirstBadLine)
	}
	if len(tr.Events) != 3 || tr.Events[1].Object != "tampered" {
		t.Fatalf("tampered record must still be visible in the tail: %+v", tr.Events)
	}
}

func TestAuditChainDetectsTamperedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	l.Record(Event{Actor: "app", Action: ActionAuthFailure, Outcome: "failure"})
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	// Flip the outcome on the second line without touching its recorded hash.
	lines[1] = bytes.Replace(lines[1], []byte(`"failure"`), []byte(`"success"`), 1)
	if err := os.WriteFile(path, bytes.Join(lines, []byte("\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified || report.FirstBadLine != 2 {
		t.Fatalf("tampering not detected at line 2: %+v", report)
	}
}

func TestAuditChainDetectsDeletedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
	// Drop the third line: seq/prev_hash chain now skips it.
	kept := append(append([][]byte{}, lines[:2]...), lines[3])
	if err := os.WriteFile(path, bytes.Join(kept, []byte("\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified {
		t.Fatalf("deleted line not detected: %+v", report)
	}
}

func TestAuditChainDetectsReorderedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	lines[0], lines[1] = lines[1], lines[0]
	if err := os.WriteFile(path, bytes.Join(lines, []byte("\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified {
		t.Fatalf("reordering not detected: %+v", report)
	}
}

func TestAuditLegacyFileVerifiesWithoutChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	legacy := `{"time":"2026-01-01T00:00:00Z","actor":"app","action":"auth.success","outcome":"success"}` + "\n" +
		`{"time":"2026-01-01T00:00:01Z","actor":"app","action":"auth.failure","outcome":"failure"}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || report.Legacy != 2 || report.Chained != 0 {
		t.Fatalf("legacy file should verify with no chain claims: %+v", report)
	}
}

func TestAuditSigningRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ks, err := CreateAuditKeyset(filepath.Join(dir, "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetSigningKeys(ks); err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	l.Record(Event{Actor: "app", Action: ActionAuthFailure, Outcome: "failure"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(path, ks)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || report.Signed != 3 || report.Chained != 3 || !report.SignaturesChecked {
		t.Fatalf("signed chain should verify: %+v", report)
	}

	// Without the keyset, the chain still verifies (tamper-evident is
	// unconditional) but signatures are simply not checked.
	unchecked, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !unchecked.Verified || unchecked.SignaturesChecked {
		t.Fatalf("unsigned-verifier pass should still pass the chain: %+v", unchecked)
	}
}

func TestAuditVerifierKeysetRequiresSignedTransition(t *testing.T) {
	dir := t.TempDir()
	ks, err := CreateAuditKeyset(filepath.Join(dir, "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := VerifyFile(path, ks.PublicOnly())
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified || report.SigningStarted {
		t.Fatalf("keyed verification accepted an unsigned-only log: %+v", report)
	}
}

func TestAuditSigningDetectsTamperedSignature(t *testing.T) {
	dir := t.TempDir()
	ks, err := CreateAuditKeyset(filepath.Join(dir, "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetSigningKeys(ks); err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Flip the first base64 character inside the "sig" field's value.
	marker := []byte(`"sig":"`)
	idx := bytes.Index(raw, marker)
	if idx < 0 {
		t.Fatal("test setup: signature field not found")
	}
	valueStart := idx + len(marker)
	tampered := append([]byte(nil), raw...)
	if tampered[valueStart] == 'A' {
		tampered[valueStart] = 'B'
	} else {
		tampered[valueStart] = 'A'
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(path, ks)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified {
		t.Fatalf("tampered signature not detected: %+v", report)
	}
}

func TestAuditSigningRetiredKeyStillVerifiesOldSignatures(t *testing.T) {
	dir := t.TempDir()
	ks, err := CreateAuditKeyset(filepath.Join(dir, "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	oldID := ks.List()[0].ID
	path := filepath.Join(dir, "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetSigningKeys(ks); err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})

	if _, err := ks.AddKey(); err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	if err := ks.Retire(oldID); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := VerifyFile(path, ks)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || report.Signed != 3 {
		t.Fatalf("signature by a since-retired key should still verify: %+v", report)
	}
}

func TestOpenAuditRejectsTamperedExistingChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"success"`), []byte(`"failure"`), 1)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAudit(path); err == nil {
		t.Fatal("tampered audit chain reopened for append")
	}
}

func TestAuditSigningTransitionCannotLoseSignature(t *testing.T) {
	dir := t.TempDir()
	ks, err := CreateAuditKeyset(filepath.Join(dir, "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetSigningKeys(ks); err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
	var transition Event
	if err := json.Unmarshal(lines[0], &transition); err != nil {
		t.Fatal(err)
	}
	transition.Sig = ""
	transition.KeyID = 0
	lines[0], err = json.Marshal(transition)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := VerifyFile(path, ks)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified || report.FirstBadLine != 1 {
		t.Fatalf("stripped transition signature was accepted: %+v", report)
	}
}

func TestSignedAuditCannotResumeUnsigned(t *testing.T) {
	dir := t.TempDir()
	ks, err := CreateAuditKeyset(filepath.Join(dir, "audit.keys"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetSigningKeys(ks); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RecordChecked(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"}); err == nil {
		t.Fatal("signed chain accepted an unsigned append")
	}
	if err := reopened.SetSigningKeys(ks.PublicOnly()); err == nil {
		t.Fatal("verify-only keyset attached as a signer")
	}
}

func TestAuditRejectsLegacyLineAfterChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"actor":"app","action":"auth.success","outcome":"success"}` + "\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified || report.FirstBadLine != 2 {
		t.Fatalf("legacy insertion was accepted: %+v", report)
	}
}

func TestOpenAuditRejectsIncompleteOrPermissiveFile(t *testing.T) {
	dir := t.TempDir()
	incomplete := filepath.Join(dir, "incomplete.log")
	if err := os.WriteFile(incomplete, []byte(`{"actor":"app"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAudit(incomplete); err == nil {
		t.Fatal("incomplete final line accepted for append")
	}
	permissive := filepath.Join(dir, "permissive.log")
	if err := os.WriteFile(permissive, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAudit(permissive); err == nil {
		t.Fatal("group/other-readable audit file accepted")
	}
}

func TestAuditVerificationBoundsLineLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	line := bytes.Repeat([]byte{'x'}, maxAuditLineBytes+1)
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFile(path, nil); err == nil {
		t.Fatal("oversized audit line did not fail closed")
	}
}
