package security

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	ActionAuthSuccess    = "auth.success"
	ActionAuthFailure    = "auth.failure"
	ActionRoleCreate     = "role.create"
	ActionRoleDrop       = "role.drop"
	ActionGrant          = "grant"
	ActionRevoke         = "revoke"
	ActionUserCreate     = "user.create"
	ActionUserDrop       = "user.drop"
	ActionDDL            = "ddl"
	ActionBackup         = "backup"
	ActionRestore        = "restore"
	ActionExport         = "export"
	ActionImport         = "import"
	ActionKeyRotate      = "key.rotate"
	ActionKeyRevoke      = "key.revoke"
	ActionKeyRewrap      = "key.rewrap"
	ActionKeyShred       = "key.shred"
	ActionTokenMint      = "token.mint"
	ActionTokenRevoke    = "token.revoke"
	ActionTokenKeyRotate = "token.key.rotate"
	ActionMembership     = "cluster.membership"
	ActionSecuritySet    = "security.setting"
	ActionSessionKill    = "session.terminate"
	ActionWorkflowCreate = "workflow.create"
	ActionWorkflowAlter  = "workflow.alter"
	ActionWorkflowDrop   = "workflow.drop"
	ActionWorkflowRun    = "workflow.run"
	ActionTriggerCreate  = "trigger.create"
	ActionTriggerAlter   = "trigger.alter"
	ActionTriggerDrop    = "trigger.drop"
	ActionTriggerFire    = "trigger.fire"
	ActionScheduleCreate = "schedule.create"
	ActionScheduleAlter  = "schedule.alter"
	ActionScheduleDrop   = "schedule.drop"
	ActionTaskCancel     = "task.cancel"
	ActionCDCSubscribe   = "cdc.subscribe"
)

// Event is one audit record. Never put passwords, keys, tokens, or secrets in these fields.
type Event struct {
	Time           time.Time `json:"time"`
	Actor          string    `json:"actor"`
	Action         string    `json:"action"`
	Object         string    `json:"object,omitempty"`
	Outcome        string    `json:"outcome"`
	Remote         string    `json:"remote,omitempty"`
	IdentitySource string    `json:"identity_source,omitempty"`
}

// Log is an append-only JSON-lines audit file (mode 0600).
type Log struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

func OpenAudit(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.OpenAudit", "open", err)
	}
	return &Log{path: path, f: f}, nil
}

func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *Log) Record(ev Event) {
	if l == nil || l.f == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	ev.Actor = Redact(ev.Actor)
	ev.Action = Redact(ev.Action)
	ev.Object = Redact(ev.Object)
	ev.Outcome = Redact(ev.Outcome)
	ev.Remote = Redact(ev.Remote)
	ev.IdentitySource = Redact(ev.IdentitySource)
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.f.Write(append(line, '\n'))
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
