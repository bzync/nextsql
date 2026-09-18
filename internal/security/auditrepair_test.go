package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAuditChain lays down n chained records and returns the file's bytes.
func writeAuditChain(t testing.TB, path string, n int) []byte {
	t.Helper()
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := l.RecordChecked(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// tearFinalRecord drops the last cut bytes of the file, leaving the final
// record partially written exactly as an interrupted append would.
func tearFinalRecord(t *testing.T, path string, raw []byte, cut int) {
	t.Helper()
	if cut >= len(raw) {
		t.Fatalf("cut %d exceeds file size %d", cut, len(raw))
	}
	if err := os.WriteFile(path, raw[:len(raw)-cut], 0o600); err != nil {
		t.Fatal(err)
	}
}

func lastLineStart(raw []byte) int64 {
	trimmed := bytes.TrimRight(raw, "\n")
	return int64(bytes.LastIndexByte(trimmed, '\n') + 1)
}

func TestRepairAuditTornTailTruncatesPartialFinalRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	raw := writeAuditChain(t, path, 5)
	start := lastLineStart(raw)
	// Keep 20 bytes of the final record so the tail is a partial line, not
	// an empty one.
	torn := raw[:start+20]
	dropped := torn[start:]
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	// Precondition: this is exactly the failure the field report saw.
	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified || report.Problem != "malformed JSON line" || report.FirstBadLine != 5 {
		t.Fatalf("expected a malformed final line at 5: %+v", report)
	}
	if !report.TornTail || report.TornTailOffset != start {
		t.Fatalf("torn tail not classified: TornTail=%v offset=%d want %d", report.TornTail, report.TornTailOffset, start)
	}
	if _, err := OpenAudit(path); err == nil {
		t.Fatal("OpenAudit must still refuse a torn chain on its own")
	}

	repair, err := RepairAuditTornTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if repair == nil {
		t.Fatal("torn tail was not repaired")
	}
	if repair.Kind != RepairTruncated || repair.Offset != start {
		t.Fatalf("unexpected repair: %+v", repair)
	}
	if repair.DroppedBytes != int64(len(dropped)) || repair.RetainedLines != 4 {
		t.Fatalf("unexpected repair accounting: %+v (dropped %d bytes)", repair, len(dropped))
	}
	sum := sha256.Sum256(dropped)
	if repair.DroppedSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("DroppedSHA256 does not cover the dropped bytes: %+v", repair)
	}

	// Nothing is destroyed: the quarantine file holds the exact bytes.
	quar, err := os.ReadFile(repair.Quarantine)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(quar, dropped) {
		t.Fatalf("quarantine bytes differ: got %q want %q", quar, dropped)
	}
	qinfo, err := os.Stat(repair.Quarantine)
	if err != nil {
		t.Fatal(err)
	}
	if qinfo.Mode().Perm() != 0o600 {
		t.Fatalf("quarantine file mode = %v, want 0600", qinfo.Mode().Perm())
	}

	// The retained prefix is byte-identical to what it was before the tear.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, raw[:start]) {
		t.Fatal("repair did not leave the verified prefix untouched")
	}

	// And the daemon can now start and keep appending to the same chain.
	verified, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Verified || verified.Chained != 4 {
		t.Fatalf("repaired file does not verify: %+v", verified)
	}
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatalf("OpenAudit after repair: %v", err)
	}
	if err := l.RecordChecked(Event{Actor: "app", Action: ActionAuthFailure, Outcome: "failure"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := TailEvents(path, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Verified || resumed.Chained != 5 {
		t.Fatalf("chain did not resume after repair: %+v", resumed.VerifyReport)
	}
	if len(resumed.Events) != 1 || resumed.Events[0].Seq != 5 {
		t.Fatalf("resumed record took the wrong sequence: %+v", resumed.Events)
	}
}

func TestRepairAuditTornTailTerminatesUnterminatedFinalRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	raw := writeAuditChain(t, path, 3)
	// Every byte of the final record reached disk except its terminator.
	tearFinalRecord(t, path, raw, 1)

	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || !report.TailUnterminated || report.TornTail {
		t.Fatalf("expected a verified but unterminated tail: %+v", report)
	}
	if _, err := OpenAudit(path); err == nil {
		t.Fatal("OpenAudit must refuse an unterminated final line")
	}

	repair, err := RepairAuditTornTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if repair == nil || repair.Kind != RepairTerminated {
		t.Fatalf("unexpected repair: %+v", repair)
	}
	if repair.DroppedBytes != 0 || repair.Quarantine != "" {
		t.Fatalf("terminating a complete record must drop nothing: %+v", repair)
	}
	if repair.RetainedLines != 3 {
		t.Fatalf("RetainedLines = %d, want 3", repair.RetainedLines)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, raw) {
		t.Fatal("terminator repair must restore the file byte-for-byte")
	}
	if _, err := OpenAudit(path); err != nil {
		t.Fatalf("OpenAudit after terminator repair: %v", err)
	}
}

// A torn tail that is the only record leaves an empty file, and the chain
// restarts cleanly from genesis rather than refusing.
func TestRepairAuditTornTailHandlesSoleRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	raw := writeAuditChain(t, path, 1)
	if err := os.WriteFile(path, raw[:12], 0o600); err != nil {
		t.Fatal(err)
	}
	repair, err := RepairAuditTornTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if repair == nil || repair.Kind != RepairTruncated || repair.Offset != 0 || repair.RetainedLines != 0 {
		t.Fatalf("unexpected repair: %+v", repair)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("file size = %d, want 0", info.Size())
	}
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.RecordChecked(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	tr, err := TailEvents(path, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Verified || len(tr.Events) != 1 || tr.Events[0].Seq != 1 {
		t.Fatalf("chain did not restart from genesis: %+v", tr)
	}
}

// Everything below is damage a partial write cannot produce. Repair must
// decline and leave the file byte-identical, so OpenAudit still fails closed.
func TestRepairAuditTornTailDeclinesNonTornDamage(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(t *testing.T, path string, raw []byte)
	}{
		{"mid-chain malformed line", func(t *testing.T, path string, raw []byte) {
			lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
			lines[2] = []byte(`{"time":"broken`)
			write(t, path, bytes.Join(lines, []byte("\n")))
		}},
		{"final line edited but well-formed", func(t *testing.T, path string, raw []byte) {
			lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
			last := len(lines) - 1
			lines[last] = bytes.Replace(lines[last], []byte(`"success"`), []byte(`"failure"`), 1)
			write(t, path, bytes.Join(lines, []byte("\n")))
		}},
		{"mid-chain line removed", func(t *testing.T, path string, raw []byte) {
			lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
			write(t, path, bytes.Join(append(lines[:2:2], lines[3:]...), []byte("\n")))
		}},
		{"torn tail plus earlier tampering", func(t *testing.T, path string, raw []byte) {
			lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
			lines[1] = bytes.Replace(lines[1], []byte(`"success"`), []byte(`"failure"`), 1)
			joined := bytes.Join(lines, []byte("\n"))
			write(t, path, joined[:len(joined)-10])
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.log")
			raw := writeAuditChain(t, path, 5)
			tc.corrupt(t, path, raw)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			repair, err := RepairAuditTornTail(path)
			if err != nil {
				t.Fatal(err)
			}
			if repair != nil {
				t.Fatalf("repair must decline damage a partial write cannot cause: %+v", repair)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("declined repair must not modify the file")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("declined repair must not leave a quarantine file: %v", entries)
			}
			if _, err := OpenAudit(path); err == nil {
				t.Fatal("OpenAudit must still fail closed on this damage")
			}
		})
	}
}

// Removing whole trailing records *including* the last newline is
// byte-for-byte indistinguishable from an append whose terminator never
// reached disk: both leave a fully verified chain with no final newline. The
// engine already documents that a local file cannot prove its suffix was not
// removed (docs/security.md, threat boundary), so repair resolves the
// ambiguity the only way that is safe — it appends the missing terminator
// and drops nothing. Suffix removal is neither concealed nor worsened: the
// retained records are untouched, and detecting it still requires the
// external WORM checkpoint the docs call for.
func TestRepairAuditTornTailTerminatorIsIndistinguishableFromSuffixRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	raw := writeAuditChain(t, path, 5)
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	kept := bytes.Join(lines[:3], []byte("\n"))
	write(t, path, kept)

	repair, err := RepairAuditTornTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if repair == nil || repair.Kind != RepairTerminated {
		t.Fatalf("unexpected repair: %+v", repair)
	}
	if repair.DroppedBytes != 0 || repair.Quarantine != "" {
		t.Fatalf("repair must drop nothing here: %+v", repair)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, append(kept, '\n')) {
		t.Fatal("repair must add only the terminator, and never restore or remove a record")
	}
	report, err := VerifyFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || report.Chained != 3 {
		t.Fatalf("retained records must be untouched: %+v", report)
	}

	// The same removal that keeps its final newline verifies outright, so
	// repair has nothing to do and must not touch the file. OpenAudit has
	// always accepted this — undetectable suffix removal is a documented
	// limit of a local chain, not something repair introduces.
	whole := filepath.Join(t.TempDir(), "audit.log")
	rawWhole := writeAuditChain(t, whole, 5)
	wholeLines := bytes.Split(bytes.TrimRight(rawWhole, "\n"), []byte("\n"))
	keptWhole := append(bytes.Join(wholeLines[:3], []byte("\n")), '\n')
	write(t, whole, keptWhole)

	repair, err = RepairAuditTornTail(whole)
	if err != nil {
		t.Fatal(err)
	}
	if repair != nil {
		t.Fatalf("a suffix-removed file that verifies needs no repair: %+v", repair)
	}
	afterWhole, err := os.ReadFile(whole)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterWhole, keptWhole) {
		t.Fatal("repair must not modify a file that verifies")
	}
	if _, err := OpenAudit(whole); err != nil {
		t.Fatalf("OpenAudit has always accepted a verified suffix-removed file: %v", err)
	}
}

func write(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRepairAuditTornTailIsNoOpOnCleanAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.log")
	repair, err := RepairAuditTornTail(missing)
	if err != nil || repair != nil {
		t.Fatalf("missing file: repair=%+v err=%v", repair, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("repair must not create the audit file")
	}

	path := filepath.Join(dir, "audit.log")
	raw := writeAuditChain(t, path, 4)
	repair, err = RepairAuditTornTail(path)
	if err != nil || repair != nil {
		t.Fatalf("clean file: repair=%+v err=%v", repair, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, after) {
		t.Fatal("repair modified a clean file")
	}
}

func TestRepairAuditTornTailRefusesUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	loose := filepath.Join(dir, "loose.log")
	writeAuditChain(t, loose, 2)
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RepairAuditTornTail(loose); err == nil {
		t.Fatal("repair must refuse a group/other-readable audit file")
	}

	target := filepath.Join(dir, "target.log")
	writeAuditChain(t, target, 2)
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := RepairAuditTornTail(link); err == nil {
		t.Fatal("repair must refuse a symlinked audit path")
	}
}

// A signed chain repairs the same way, and the record appended afterwards is
// signed — the repair does not let an unsigned record back into the signed
// segment.
func TestRepairAuditTornTailOnSignedChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	ks, err := CreateAuditKeyset(filepath.Join(dir, "keys.nsak"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetSigningKeys(ks); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := l.RecordChecked(Event{Actor: "app", Action: ActionAuthSuccess, Outcome: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	start := lastLineStart(raw)
	if err := os.WriteFile(path, raw[:start+30], 0o600); err != nil {
		t.Fatal(err)
	}

	repair, err := RepairAuditTornTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if repair == nil || repair.Kind != RepairTruncated {
		t.Fatalf("signed chain torn tail not repaired: %+v", repair)
	}
	report, err := VerifyFile(path, ks)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verified || !report.SigningStarted {
		t.Fatalf("repaired signed chain does not verify: %+v", report)
	}

	reopened, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.SigningRequired() {
		t.Fatal("repair lost the signed-transition requirement")
	}
	if err := reopened.RecordChecked(Event{Actor: "app", Action: ActionAuthFailure, Outcome: "failure"}); err == nil {
		t.Fatal("an unsigned record must still be refused after repair")
	}
	if err := reopened.SetSigningKeys(ks); err != nil {
		t.Fatal(err)
	}
	if err := reopened.RecordChecked(Event{Actor: "app", Action: ActionAuthFailure, Outcome: "failure"}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	final, err := VerifyFile(path, ks)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Verified {
		t.Fatalf("chain does not verify after repair and resume: %+v", final)
	}
}

func TestTornTailRepairSummaryIsSelfDescribing(t *testing.T) {
	r := &TornTailRepair{
		Kind: RepairTruncated, Offset: 41022983, DroppedBytes: 87,
		DroppedSHA256: strings.Repeat("ab", 32),
		Quarantine:    "/var/lib/nextsql/nextsql.audit.torn-20260918T080512Z",
		RetainedLines: 92150,
	}
	got := r.Summary()
	for _, want := range []string{
		"kind=truncated", "offset=41022983", "dropped_bytes=87",
		"dropped_sha256=" + strings.Repeat("ab", 32),
		"retained_lines=92150", "quarantine=nextsql.audit.torn-20260918T080512Z",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Summary() = %q, missing %q", got, want)
		}
	}
	// The summary lands in Event.Object, which passes through Redact.
	if Redact(got) != got {
		t.Fatalf("summary must survive redaction: %q", Redact(got))
	}
}

// The field report's file had a NUL-filled tail, the shape a filesystem with
// delayed allocation leaves when the size reaches disk ahead of the bytes.
// NUL is not whitespace, so it reaches the JSON decoder as a malformed line
// rather than being trimmed away as blank.
func TestRepairAuditTornTailHandlesNulFilledTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	raw := writeAuditChain(t, path, 6)
	start := lastLineStart(raw)
	torn := append(append([]byte{}, raw[:start]...), bytes.Repeat([]byte{0}, 64)...)
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	repair, err := RepairAuditTornTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if repair == nil || repair.Kind != RepairTruncated || repair.DroppedBytes != 64 {
		t.Fatalf("NUL tail not repaired as a torn tail: %+v", repair)
	}
	quar, err := os.ReadFile(repair.Quarantine)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(quar, bytes.Repeat([]byte{0}, 64)) {
		t.Fatalf("quarantine did not preserve the NUL tail: %q", quar)
	}
	if _, err := OpenAudit(path); err != nil {
		t.Fatalf("OpenAudit after NUL-tail repair: %v", err)
	}
}

// FuzzAuditTornTailRepair drives the classifier with arbitrary file content,
// because the decision to modify the audit file is made entirely from
// untrusted bytes. The invariants it holds are the ones the repair's safety
// argument rests on: a declined repair changes nothing, a truncation removes
// only a suffix and preserves every byte of it, and whatever survives a
// repair verifies.
func FuzzAuditTornTailRepair(f *testing.F) {
	clean := writeAuditChain(f, filepath.Join(f.TempDir(), "seed.log"), 3)
	f.Add(clean)
	f.Add(clean[:len(clean)-1])
	f.Add(clean[:len(clean)-20])
	f.Add(append(append([]byte{}, clean...), 0, 0, 0))
	f.Add([]byte{})
	f.Add([]byte("\n\n\n"))
	f.Add([]byte(`{"time":"2026-09-18T08:05:12Z"`))
	f.Add([]byte("not json at all"))

	f.Fuzz(func(t *testing.T, content []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "audit.log")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		repair, err := RepairAuditTornTail(path)
		if err != nil {
			// An I/O or over-limit refusal is allowed, but it must not have
			// left the file changed.
			after, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("failed repair modified the file: %q -> %q", before, after)
			}
			return
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if repair == nil {
			if !bytes.Equal(before, after) {
				t.Fatalf("declined repair modified the file: %q -> %q", before, after)
			}
			return
		}

		switch repair.Kind {
		case RepairTerminated:
			if !bytes.Equal(after, append(append([]byte{}, before...), '\n')) {
				t.Fatalf("terminator repair changed more than the terminator: %q -> %q", before, after)
			}
		case RepairTruncated:
			if !bytes.HasPrefix(before, after) {
				t.Fatalf("truncation is not a suffix removal: %q -> %q", before, after)
			}
			if int64(len(after)) != repair.Offset {
				t.Fatalf("truncated to %d, reported offset %d", len(after), repair.Offset)
			}
			quar, qerr := os.ReadFile(repair.Quarantine)
			if qerr != nil {
				t.Fatal(qerr)
			}
			if !bytes.Equal(quar, before[len(after):]) {
				t.Fatalf("quarantine does not hold the removed suffix: %q vs %q", quar, before[len(after):])
			}
			sum := sha256.Sum256(quar)
			if repair.DroppedSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatal("DroppedSHA256 does not cover the quarantined bytes")
			}
		default:
			t.Fatalf("unknown repair kind %q", repair.Kind)
		}

		// Whatever survived must verify, and a second pass must find
		// nothing left to do.
		report, err := VerifyFile(path, nil)
		if err != nil {
			t.Fatalf("repaired file could not be verified: %v", err)
		}
		if !report.Verified {
			t.Fatalf("repaired file does not verify: %+v (content %q)", report, after)
		}
		again, err := RepairAuditTornTail(path)
		if err != nil {
			t.Fatal(err)
		}
		if again != nil {
			t.Fatalf("repair is not idempotent: %+v", again)
		}
		if _, err := OpenAudit(path); err != nil {
			t.Fatalf("OpenAudit refused a repaired file: %v", err)
		}
	})
}
