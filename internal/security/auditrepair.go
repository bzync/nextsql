package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

// Repair kinds reported by TornTailRepair.Kind.
const (
	// RepairTruncated means an unparseable final record was moved to the
	// quarantine file and removed from the chain.
	RepairTruncated = "truncated"
	// RepairTerminated means the final record was complete and verified but
	// carried no newline, so only the missing terminator was appended.
	// Nothing was dropped.
	RepairTerminated = "terminated"
)

// TornTailRepair describes one applied repair. DroppedSHA256 is over the
// exact bytes moved to Quarantine, so an auditor can confirm the quarantine
// file is what the chain says was removed.
type TornTailRepair struct {
	Kind          string
	Offset        int64
	DroppedBytes  int64
	DroppedSHA256 string
	Quarantine    string
	RetainedLines int
}

// Summary is the compact, redaction-safe description recorded as the audit
// event's object. Event has no free-form field and adding one would change
// the bytes older verifiers re-marshal when recomputing a hash, so the
// detail is packed into the one field that already carries an identifier.
func (r *TornTailRepair) Summary() string {
	if r == nil {
		return ""
	}
	if r.Kind == RepairTerminated {
		return fmt.Sprintf("kind=%s offset=%d retained_lines=%d", r.Kind, r.Offset, r.RetainedLines)
	}
	return fmt.Sprintf("kind=%s offset=%d dropped_bytes=%d dropped_sha256=%s retained_lines=%d quarantine=%s",
		r.Kind, r.Offset, r.DroppedBytes, r.DroppedSHA256, r.RetainedLines, filepath.Base(r.Quarantine))
}

// RepairAuditTornTail repairs the one kind of audit-chain damage an
// interrupted write can produce, and only that kind.
//
// A record is appended with a single write followed by an fsync, and
// Log.Record advances its in-memory chain head only after that fsync
// returns. A record whose bytes are torn on disk is therefore a record whose
// fsync never completed: it was never acknowledged to the caller that
// requested it, and nothing before it is affected. Dropping it loses no
// committed history, which is the same argument wal.Log and undo.Log already
// use to truncate their own torn tails rather than refuse to open.
//
// Two shapes qualify, both confined to the final line:
//
//   - the final line does not parse and every line before it verifies. Its
//     bytes are copied to a mode-0600 quarantine sibling, fsynced, and only
//     then removed from the chain. Nothing is destroyed.
//   - the whole file including the final line verifies, but the final line
//     has no newline. The record is intact and chain-valid; only its
//     terminator is missing, so the terminator is appended and nothing is
//     dropped. Left alone, the next append would concatenate onto it.
//
// Anything else — a failure on an earlier line, a sequence gap, a prev_hash
// or hash mismatch, a bad signature — returns (nil, nil) without touching
// the file, leaving OpenAudit to fail closed with the authoritative error.
// A non-nil error means the repair itself could not be carried out; the file
// is never left with bytes dropped that were not first preserved.
func RepairAuditTornTail(path string) (*TornTailRepair, error) {
	if err := auditFileSafe(path); err != nil {
		return nil, err
	}
	report, _, err := verifyAuditPath(path, nil, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	switch {
	case report.TornTail:
		return truncateAuditTornTail(path, report)
	case report.Verified && report.TailUnterminated:
		return terminateAuditTail(path, report)
	default:
		return nil, nil
	}
}

// auditFileSafe repeats OpenAudit's ownership checks. Repair writes to the
// audit file, so it must refuse a symlink, a non-regular file, or one
// readable by group or others for the same reasons OpenAudit does, and must
// refuse them before reading rather than after.
func auditFileSafe(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "stat", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nerr.New(nerr.InvalidArgument, "security.RepairAuditTornTail", "audit path must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nerr.New(nerr.Forbidden, "security.RepairAuditTornTail", "audit file must not be accessible by group or others")
	}
	return nil
}

func truncateAuditTornTail(path string, report VerifyReport) (*TornTailRepair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "open", err)
	}
	// The scanner already refused a final line longer than its cap, so the
	// tail is bounded; read one byte past the cap anyway so a tail that
	// somehow exceeds it is rejected rather than truncated silently.
	if _, err := f.Seek(report.TornTailOffset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "seek to torn tail", err)
	}
	dropped, err := io.ReadAll(io.LimitReader(f, maxAuditLineBytes+1))
	_ = f.Close()
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "read torn tail", err)
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	if len(dropped) > maxAuditLineBytes {
		return nil, nerr.New(nerr.InvalidFormat, "security.RepairAuditTornTail", "torn tail exceeds the audit line limit")
	}
	sum := sha256.Sum256(dropped)
	quarantine, err := writeAuditQuarantine(path, dropped)
	if err != nil {
		return nil, err
	}
	// Only now that the dropped bytes are durable somewhere else.
	if err := os.Truncate(path, report.TornTailOffset); err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "truncate torn tail", err)
	}
	if err := syncAuditFile(path); err != nil {
		return nil, err
	}
	return &TornTailRepair{
		Kind:          RepairTruncated,
		Offset:        report.TornTailOffset,
		DroppedBytes:  int64(len(dropped)),
		DroppedSHA256: hex.EncodeToString(sum[:]),
		Quarantine:    quarantine,
		RetainedLines: report.Lines - 1,
	}, nil
}

func terminateAuditTail(path string, report VerifyReport) (*TornTailRepair, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "open to terminate tail", err)
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		_ = f.Close()
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "size before terminating tail", err)
	}
	if _, err := diskio.Write(f, []byte{'\n'}); err != nil {
		_ = f.Close()
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "append missing terminator", err)
	}
	if err := diskio.Sync(f); err != nil {
		_ = f.Close()
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "sync terminator", err)
	}
	if err := f.Close(); err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "close after terminating tail", err)
	}
	return &TornTailRepair{
		Kind:          RepairTerminated,
		Offset:        size,
		RetainedLines: report.Lines,
	}, nil
}

// writeAuditQuarantine preserves the dropped bytes beside the audit file
// under a mode-0600 name that cannot collide with an existing one, and makes
// both the file and its directory entry durable before returning. A caller
// that truncates only after this returns can never destroy bytes it has not
// already preserved.
func writeAuditQuarantine(path string, dropped []byte) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path) + ".torn-" + time.Now().UTC().Format("20060102T150405Z")
	name := filepath.Join(dir, base)
	var f *os.File
	var err error
	for attempt := 0; ; attempt++ {
		f, err = os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			break
		}
		if !os.IsExist(err) || attempt >= 64 {
			return "", nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "create quarantine file", err)
		}
		name = filepath.Join(dir, fmt.Sprintf("%s.%d", base, attempt+1))
	}
	if _, err := diskio.Write(f, dropped); err != nil {
		_ = f.Close()
		return "", nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "write quarantine file", err)
	}
	if err := diskio.Sync(f); err != nil {
		_ = f.Close()
		return "", nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "sync quarantine file", err)
	}
	if err := f.Close(); err != nil {
		return "", nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "close quarantine file", err)
	}
	if err := diskio.SyncDir(dir); err != nil {
		return "", err
	}
	return name, nil
}

func syncAuditFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "reopen to sync", err)
	}
	if err := diskio.Sync(f); err != nil {
		_ = f.Close()
		return nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "sync repaired audit file", err)
	}
	if err := f.Close(); err != nil {
		return nerr.Wrap(nerr.IO, "security.RepairAuditTornTail", "close after sync", err)
	}
	return diskio.SyncDir(filepath.Dir(path))
}
