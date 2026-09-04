package security

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	ActionAuthSuccess         = "auth.success"
	ActionAuthFailure         = "auth.failure"
	ActionRoleCreate          = "role.create"
	ActionRoleDrop            = "role.drop"
	ActionGrant               = "grant"
	ActionRevoke              = "revoke"
	ActionUserCreate          = "user.create"
	ActionUserDrop            = "user.drop"
	ActionDDL                 = "ddl"
	ActionBackup              = "backup"
	ActionBackupPrune         = "backup.prune"
	ActionRealmCreate         = "realm.create"
	ActionDatabaseCreate      = "database.create"
	ActionDatabaseSuspend     = "database.suspend"
	ActionDatabaseResume      = "database.resume"
	ActionDatabaseDrop        = "database.drop"
	ActionRestore             = "restore"
	ActionExport              = "export"
	ActionImport              = "import"
	ActionKeyRotate           = "key.rotate"
	ActionKeyRevoke           = "key.revoke"
	ActionKeyRewrap           = "key.rewrap"
	ActionKeyShred            = "key.shred"
	ActionTokenMint           = "token.mint"
	ActionTokenRevoke         = "token.revoke"
	ActionTokenKeyRotate      = "token.key.rotate"
	ActionMembership          = "cluster.membership"
	ActionSecuritySet         = "security.setting"
	ActionSessionKill         = "session.terminate"
	ActionWorkflowCreate      = "workflow.create"
	ActionWorkflowAlter       = "workflow.alter"
	ActionWorkflowDrop        = "workflow.drop"
	ActionWorkflowRun         = "workflow.run"
	ActionTriggerCreate       = "trigger.create"
	ActionTriggerAlter        = "trigger.alter"
	ActionTriggerDrop         = "trigger.drop"
	ActionTriggerFire         = "trigger.fire"
	ActionScheduleCreate      = "schedule.create"
	ActionScheduleAlter       = "schedule.alter"
	ActionScheduleDrop        = "schedule.drop"
	ActionResourceGroupCreate = "resource_group.create"
	ActionResourceGroupAlter  = "resource_group.alter"
	ActionResourceGroupDrop   = "resource_group.drop"
	ActionResourceGroupAssign = "resource_group.assign"
	ActionTaskCancel          = "task.cancel"
	ActionCDCSubscribe        = "cdc.subscribe"
	ActionLeaderTransfer      = "cluster.leader_transfer"
	ActionClusterDrain        = "cluster.drain"
	ActionClusterMaintenance  = "cluster.maintenance"
	ActionClusterReconcile    = "cluster.reconcile_confirm"
	ActionConfigSet           = "config.set"
	ActionBackupVerify        = "backup.verify"
	// ActionAuditSigningEnabled is the signed transition record after which
	// every chained line must carry a valid signature when verified with an
	// audit public keyset. Its presence prevents stripping the first
	// signature from silently moving the start of the signed segment.
	ActionAuditSigningEnabled = "audit.signing.enabled"

	auditChainVersion = 1
	auditChainDomain  = "NSAC\x01"
)

// Event is one audit record. Never put passwords, keys, tokens, or secrets
// in these fields. ChainVersion/Seq/PrevHash/Hash/Sig/KeyID are always
// computed by Record and must not be set by callers — any caller-supplied
// value is discarded before hashing.
type Event struct {
	Time           time.Time `json:"time"`
	Actor          string    `json:"actor"`
	Action         string    `json:"action"`
	Object         string    `json:"object,omitempty"`
	Outcome        string    `json:"outcome"`
	Remote         string    `json:"remote,omitempty"`
	IdentitySource string    `json:"identity_source,omitempty"`

	// Seq and PrevHash/Hash form a tamper-evident hash chain, present on
	// every line written by a hash-chain-aware Log (every Log opened by
	// this build). A line without these fields is a pre-chain legacy
	// entry. Sig/KeyID are present only when the Log has a signing
	// AuditKeyset attached (SetSigningKeys) — the chain is tamper-evident
	// unconditionally; signing adds non-repudiation on top.
	ChainVersion uint8  `json:"chain_version,omitempty"`
	Seq          uint64 `json:"seq,omitempty"`
	PrevHash     string `json:"prev_hash,omitempty"`
	Hash         string `json:"hash,omitempty"`
	Sig          string `json:"sig,omitempty"`
	KeyID        uint32 `json:"key_id,omitempty"`
}

// Log is an append-only JSON-lines audit file (mode 0600). Every line
// written by this build carries a SHA-256 hash chain (see Event), so accidental
// edits, reordering, and removal/insertion inside the retained chain are
// detectable by VerifyFile. An attacker able to rewrite the whole unsigned
// file can recompute that chain; an AuditKeyset prevents such rewriting by
// signing each new chain hash. Even a signed local file cannot prove that an
// attacker did not remove its final suffix, so deployments with that threat
// must retain the last signed hash in an external append-only/WORM system.
type Log struct {
	mu              sync.Mutex
	path            string
	f               *os.File
	nextSeq         uint64
	lastHash        [32]byte
	signer          *AuditKeyset
	signingRequired bool
	failed          error
}

func OpenAudit(path string) (*Log, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, nerr.New(nerr.InvalidArgument, "security.OpenAudit", "audit path must be a regular non-symlink file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, nerr.New(nerr.Forbidden, "security.OpenAudit", "audit file must not be accessible by group or others")
		}
	} else if !os.IsNotExist(err) {
		return nil, nerr.Wrap(nerr.IO, "security.OpenAudit", "stat", err)
	}
	nextSeq, lastHash, signingRequired, err := auditChainResumeState(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.OpenAudit", "open", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nerr.Wrap(nerr.IO, "security.OpenAudit", "stat opened file", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		_ = f.Close()
		return nil, nerr.New(nerr.Forbidden, "security.OpenAudit", "audit file must be a mode-0600 regular file")
	}
	if info.Size() > 0 {
		last := make([]byte, 1)
		rf, rerr := os.Open(path)
		if rerr == nil {
			_, rerr = rf.Seek(-1, io.SeekEnd)
			if rerr == nil {
				_, rerr = io.ReadFull(rf, last)
			}
			_ = rf.Close()
		}
		if rerr != nil || last[0] != '\n' {
			_ = f.Close()
			return nil, nerr.New(nerr.InvalidFormat, "security.OpenAudit", "audit file has an incomplete final line")
		}
	}
	return &Log{
		path: path, f: f, nextSeq: nextSeq, lastHash: lastHash,
		signingRequired: signingRequired,
	}, nil
}

// auditChainResumeState scans any existing lines to find where the hash
// chain left off, so appending across a process restart continues the same
// chain rather than silently starting a second, disconnected one. Lines
// before the first chained line (pre-upgrade legacy entries, if any) do not
// participate; a file with no chained lines yet resumes at seq 1, prevHash
// all-zero (a fresh genesis).
func auditChainResumeState(path string) (uint64, [32]byte, bool, error) {
	report, state, err := verifyAuditPath(path, nil, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, [32]byte{}, false, nil
		}
		return 0, [32]byte{}, false, err
	}
	if !report.Verified {
		return 0, [32]byte{}, false, nerr.New(
			nerr.InvalidFormat, "security.OpenAudit",
			fmt.Sprintf("existing audit chain failed at line %d: %s", report.FirstBadLine, report.Problem),
		)
	}
	return state.nextSeq, state.lastHash, state.signingRequired, nil
}

func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// SigningRequired reports whether the retained chain has crossed the signed
// transition and therefore must never accept another unsigned record.
func (l *Log) SigningRequired() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.signingRequired
}

// SetSigningKeys attaches an Ed25519 signer and durably appends the signed
// transition record after which unsigned chained lines are invalid. A nil or
// verify-only keyset is rejected; signing cannot be silently detached once
// enabled.
func (l *Log) SetSigningKeys(ks *AuditKeyset) error {
	if l == nil || ks == nil {
		return nerr.New(nerr.InvalidArgument, "security.Log.SetSigningKeys", "a signing keyset is required")
	}
	if err := ks.ValidateSigner(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nerr.New(nerr.Unavailable, "security.Log.SetSigningKeys", "audit log is closed")
	}
	previous := l.signer
	l.signer = ks
	ev, canonical, err := prepareAuditEvent(Event{
		Actor: "system", Action: ActionAuditSigningEnabled,
		Object: "ed25519", Outcome: "success",
	})
	if err != nil {
		l.signer = previous
		return err
	}
	if err := l.recordPreparedLocked(ev, canonical); err != nil {
		l.signer = previous
		return err
	}
	l.signingRequired = true
	return nil
}

func (l *Log) Record(ev Event) {
	_ = l.RecordChecked(ev)
}

// RecordChecked appends one audit event and reports signing/write/sync
// failures to callers that require an explicit audit durability boundary.
// Record remains as the compatibility wrapper used by existing call sites.
func (l *Log) RecordChecked(ev Event) error {
	if l == nil {
		return nerr.New(nerr.InvalidArgument, "security.Log.Record", "nil audit log")
	}
	ev, canonical, err := prepareAuditEvent(ev)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recordPreparedLocked(ev, canonical)
}

func prepareAuditEvent(ev Event) (Event, []byte, error) {
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	ev.Actor = Redact(ev.Actor)
	ev.Action = Redact(ev.Action)
	ev.Object = Redact(ev.Object)
	ev.Outcome = Redact(ev.Outcome)
	ev.Remote = Redact(ev.Remote)
	ev.IdentitySource = auditIdentitySource(ev.IdentitySource)
	ev.ChainVersion, ev.Seq, ev.PrevHash, ev.Hash, ev.Sig, ev.KeyID = auditChainVersion, 0, "", "", "", 0
	canonical, err := json.Marshal(ev)
	if err != nil {
		return Event{}, nil, nerr.Wrap(nerr.InvalidArgument, "security.Log.Record", "encode event", err)
	}
	if len(canonical) > maxAuditLineBytes {
		return Event{}, nil, nerr.New(nerr.InvalidArgument, "security.Log.Record", "audit event exceeds line limit")
	}
	return ev, canonical, nil
}

func (l *Log) recordPreparedLocked(ev Event, canonical []byte) error {
	if l.f == nil {
		if l.failed != nil {
			return l.failed
		}
		return nerr.New(nerr.Unavailable, "security.Log.Record", "audit log is closed")
	}
	if l.signingRequired && l.signer == nil {
		return nerr.New(nerr.Unavailable, "security.Log.Record", "audit signing is required by the existing chain")
	}
	seq := l.nextSeq
	if seq == 0 || seq == ^uint64(0) {
		return nerr.New(nerr.Exhausted, "security.Log.Record", "audit sequence exhausted")
	}
	prevHash := l.lastHash
	hash := auditChainHash(prevHash, seq, canonical)
	ev.Seq = seq
	ev.PrevHash = hex.EncodeToString(prevHash[:])
	ev.Hash = hex.EncodeToString(hash[:])
	if l.signer != nil {
		sig, keyID, err := l.signer.sign(hash[:])
		if err != nil {
			return err
		}
		ev.Sig = base64.StdEncoding.EncodeToString(sig)
		ev.KeyID = keyID
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return nerr.Wrap(nerr.InvalidArgument, "security.Log.Record", "encode chained event", err)
	}
	if len(line) > maxAuditLineBytes {
		return nerr.New(nerr.InvalidArgument, "security.Log.Record", "audit event exceeds line limit")
	}
	record := append(line, '\n')
	if n, err := l.f.Write(record); err != nil || n != len(record) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return l.failLocked(nerr.Wrap(nerr.IO, "security.Log.Record", "append", err))
	}
	if err := l.f.Sync(); err != nil {
		return l.failLocked(nerr.Wrap(nerr.IO, "security.Log.Record", "sync", err))
	}
	l.nextSeq = seq + 1
	l.lastHash = hash
	return nil
}

func (l *Log) failLocked(err error) error {
	l.failed = err
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	return err
}

// auditChainHash binds a line to its position (seq), its predecessor
// (prevHash), and its own content (canonical, the line's JSON with every
// chain/signature field cleared) so any edit, reorder, or drop of a
// committed line changes a hash a verifier can recompute independently.
func auditChainHash(prevHash [32]byte, seq uint64, canonical []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(auditChainDomain))
	h.Write(prevHash[:])
	var seqBuf [8]byte
	binary.LittleEndian.PutUint64(seqBuf[:], seq)
	h.Write(seqBuf[:])
	h.Write(canonical)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// auditIdentitySource preserves only the closed set emitted by authentication
// code. This avoids redacting the legitimate word "token" while ensuring an
// unexpected or secret-shaped value still passes through the general redactor.
func auditIdentitySource(source string) string {
	switch source {
	case "native", "mtls", "mtls+native", "token", "mtls+token", "oidc", "mtls+oidc":
		return source
	default:
		return Redact(source)
	}
}

func (l *Log) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.f.Close()
	l.f = nil
	return err
}

// Redact replaces secret-like tokens so audit lines never contain credentials.
func Redact(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	for _, bad := range []string{"password", "passwd", "secret", "token", "apikey", "api_key", "private_key", "dek=", "kek="} {
		if strings.Contains(lower, bad) {
			return "[redacted]"
		}
	}
	return s
}

func Outcome(err error) string {
	if err == nil {
		return "success"
	}
	return "failure"
}
