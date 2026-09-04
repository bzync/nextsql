package security

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/bzync/nextsql/internal/nerr"
)

// maxAuditLineBytes bounds both verification memory and the event size emitted
// by Log.Record. Audit files may grow without bound on disk, but verification
// streams them one line at a time and never allocates from an unchecked line.
const maxAuditLineBytes = 1 << 20

// VerifyReport summarizes one pass of VerifyFile over an audit log.
type VerifyReport struct {
	Lines             int
	Legacy            int // contiguous prefix with no hash-chain fields
	Chained           int // lines with hash-chain fields
	Signed            int // chained lines that also carry a signature
	SigningStarted    bool
	SignaturesChecked bool

	// Verified is true only if every chained line's sequence, prev_hash,
	// hash, and signed-segment transition are internally consistent and,
	// when SignaturesChecked, every signature in the signed segment verifies.
	// FirstBadLine/Problem describe the first failure (1-indexed non-blank
	// line number), zero/empty if none.
	Verified     bool
	FirstBadLine int
	Problem      string
}

type auditVerifyState struct {
	nextSeq         uint64
	lastHash        [32]byte
	signingRequired bool
}

// VerifyFile streams an audit log written by Log.Record. It checks the SHA-256
// hash chain unconditionally and, when verifiers is non-nil, every signature
// from the signed transition record onward. It detects edits, reordering, and
// insertion/removal inside the retained chain. A local file alone cannot prove
// that its final suffix was not removed; compare the final signed hash with an
// independently retained/WORM checkpoint when that threat is in scope.
//
// A fully legacy file reports Verified with Chained == 0: it is syntactically
// readable, but the report makes clear that no tamper-evident records exist.
func VerifyFile(path string, verifiers *AuditKeyset) (VerifyReport, error) {
	report, _, err := verifyAuditPath(path, verifiers, 0)
	return report, err
}

// TailReport is a VerifyReport plus the most recent (up to maxEvents) parsed
// records from the same pass over the file — oldest first. It intentionally
// includes records regardless of whether they individually failed
// verification: hiding entries near a detected tampering point would defeat
// the point of a viewer meant to help an operator investigate one. Check
// VerifyReport.Verified/FirstBadLine/Problem for the chain's overall status.
type TailReport struct {
	VerifyReport
	Events []Event
}

// TailEvents verifies the whole file exactly like VerifyFile — streamed one
// line at a time, so cost scales with file size on disk/CPU, never with
// memory — and additionally retains the last maxEvents parsed records in a
// bounded ring buffer. maxEvents <= 0 retains none (Events is nil); the
// records themselves can be arbitrarily large individually (Log.Record caps
// one line at maxAuditLineBytes, 1 MiB) but the ring never holds more than
// maxEvents of them regardless of how many lines the file actually has.
func TailEvents(path string, maxEvents int, verifiers *AuditKeyset) (TailReport, error) {
	report, _, events, err := verifyAuditPathTail(path, verifiers, maxEvents)
	return TailReport{VerifyReport: report, Events: events}, err
}

func verifyAuditPath(path string, verifiers *AuditKeyset, maxEvents int) (VerifyReport, auditVerifyState, error) {
	report, state, _, err := verifyAuditPathTail(path, verifiers, maxEvents)
	return report, state, err
}

func verifyAuditPathTail(path string, verifiers *AuditKeyset, maxEvents int) (VerifyReport, auditVerifyState, []Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return VerifyReport{}, auditVerifyState{}, nil, err
	}
	defer f.Close()

	report, state, events, err := verifyAuditScanner(f, verifiers, maxEvents)
	if err != nil {
		return report, state, events, err
	}
	return report, state, events, nil
}

// eventRing is a fixed-capacity, oldest-evicted-first buffer: O(1) amortized
// per push regardless of how many lines are pushed in total, so tailing a
// huge audit file costs no more memory than maxEvents records however many
// lines it actually has.
type eventRing struct {
	buf  []Event
	next int
	seen int
}

func newEventRing(capacity int) *eventRing {
	if capacity <= 0 {
		return nil
	}
	return &eventRing{buf: make([]Event, 0, capacity)}
}

func (r *eventRing) push(ev Event) {
	if r == nil {
		return
	}
	if len(r.buf) < cap(r.buf) {
		r.buf = append(r.buf, ev)
	} else {
		r.buf[r.next] = ev
		r.next = (r.next + 1) % cap(r.buf)
	}
	r.seen++
}

// ordered returns the retained events oldest-first.
func (r *eventRing) ordered() []Event {
	if r == nil || len(r.buf) == 0 {
		return nil
	}
	if r.seen <= len(r.buf) {
		return append([]Event(nil), r.buf...)
	}
	out := make([]Event, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	out = append(out, r.buf[:r.next]...)
	return out
}

func verifyAuditScanner(f *os.File, verifiers *AuditKeyset, maxEvents int) (VerifyReport, auditVerifyState, []Event, error) {
	var report VerifyReport
	report.SignaturesChecked = verifiers != nil
	state := auditVerifyState{nextSeq: 1}
	tail := newEventRing(maxEvents)

	var prevHash [32]byte
	var expectSeq uint64 = 1
	lineNo := 0
	chainStarted := false
	signingRequired := false
	fail := func(n int, format string, args ...any) {
		if report.FirstBadLine == 0 {
			report.FirstBadLine = n
			report.Problem = fmt.Sprintf(format, args...)
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), maxAuditLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		lineNo++
		report.Lines++

		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			fail(lineNo, "malformed JSON line")
			continue
		}
		// Retained regardless of what the checks below find: an operator
		// investigating a detected tampering point needs to see the
		// suspect record, not have it silently hidden from the tail.
		tail.push(ev)
		legacy := ev.ChainVersion == 0 && ev.Seq == 0 && ev.PrevHash == "" && ev.Hash == "" &&
			ev.Sig == "" && ev.KeyID == 0
		if legacy {
			report.Legacy++
			if chainStarted {
				fail(lineNo, "legacy/unsigned record appears after the hash chain began")
			}
			continue
		}
		chainStarted = true
		report.Chained++

		if ev.ChainVersion != auditChainVersion {
			fail(lineNo, "unsupported audit chain version %d", ev.ChainVersion)
			continue
		}
		if ev.Seq == 0 || ev.Hash == "" || ev.PrevHash == "" {
			fail(lineNo, "incomplete hash-chain fields")
			continue
		}
		if ev.Seq != expectSeq {
			fail(lineNo, "sequence gap: expected %d, got %d (a retained line may be missing)", expectSeq, ev.Seq)
		}
		gotPrevHash, err := hex.DecodeString(ev.PrevHash)
		if err != nil || len(gotPrevHash) != len(prevHash) {
			fail(lineNo, "invalid prev_hash encoding")
			continue
		}
		if !bytes.Equal(gotPrevHash, prevHash[:]) {
			fail(lineNo, "prev_hash does not match the previous chained line")
		}

		canonical, err := json.Marshal(canonicalizeForHash(ev))
		if err != nil {
			fail(lineNo, "could not re-encode line for hashing")
			continue
		}
		wantHash := auditChainHash(prevHash, ev.Seq, canonical)
		gotHash, err := hex.DecodeString(ev.Hash)
		if err != nil || len(gotHash) != len(wantHash) || !bytes.Equal(gotHash, wantHash[:]) {
			fail(lineNo, "hash mismatch (line was modified after being written)")
		}

		hasSig := ev.Sig != "" || ev.KeyID != 0
		if (ev.Sig == "") != (ev.KeyID == 0) {
			fail(lineNo, "signature and key_id must be present together")
			hasSig = false
		}
		if ev.Action == ActionAuditSigningEnabled {
			signingRequired = true
			if !hasSig {
				fail(lineNo, "audit signing transition is not signed")
			}
		} else if hasSig && !signingRequired {
			fail(lineNo, "signed record appears before the audit signing transition")
		}
		if signingRequired && !hasSig {
			fail(lineNo, "unsigned record appears after audit signing was enabled")
		}
		if hasSig {
			report.Signed++
			sig, err := base64.StdEncoding.DecodeString(ev.Sig)
			if err != nil || len(sig) != ed25519.SignatureSize {
				fail(lineNo, "invalid signature encoding")
			} else if verifiers != nil {
				if err := verifiers.verify(ev.KeyID, wantHash[:], sig); err != nil {
					fail(lineNo, "signature verification failed for key id %d", ev.KeyID)
				}
			}
		}

		prevHash = wantHash
		expectSeq = ev.Seq + 1
	}
	if err := scanner.Err(); err != nil {
		return report, state, nil, nerr.Wrap(nerr.InvalidFormat, "security.VerifyFile", "audit line exceeds limit or could not be read", err)
	}
	if verifiers != nil && !signingRequired {
		badLine := lineNo
		if badLine == 0 {
			badLine = 1
		}
		fail(badLine, "no signed audit transition is present")
	}

	report.Verified = report.FirstBadLine == 0
	report.SigningStarted = signingRequired
	state.nextSeq = expectSeq
	state.lastHash = prevHash
	state.signingRequired = signingRequired
	return report, state, tail.ordered(), nil
}

// canonicalizeForHash clears every field Record computes so re-marshaling
// reproduces the exact bytes that were hashed at write time.
func canonicalizeForHash(ev Event) Event {
	ev.Seq, ev.PrevHash, ev.Hash, ev.Sig, ev.KeyID = 0, "", "", "", 0
	return ev
}
