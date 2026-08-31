package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditNeverWritesSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.audit")
	l, err := OpenAudit(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Event{
		Actor:   "app",
		Action:  ActionAuthFailure,
		Object:  "password=hunter2",
		Outcome: "failure",
	})
	l.Record(Event{Actor: "app", Action: ActionKeyRotate, Object: "page:v2", Outcome: "success", IdentitySource: "mtls+native"})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if strings.Contains(s, "hunter2") || strings.Contains(s, "password=") {
		t.Fatalf("secret leaked: %s", s)
	}
	if !strings.Contains(s, "[redacted]") || !strings.Contains(s, ActionKeyRotate) {
		t.Fatalf("audit content: %s", s)
	}
	if !strings.Contains(s, `"identity_source":"mtls+native"`) {
		t.Fatalf("identity source missing: %s", s)
	}
}

func TestRedact(t *testing.T) {
	if Redact("token=abc") != "[redacted]" || Redact("products") != "products" {
		t.Fatal("redact policy")
	}
}

func TestRegistryTerminate(t *testing.T) {
	r := NewRegistry()
	n := 0
	unreg := r.Register("app", func() { n++ })
	if r.Terminate("app") != 1 || n != 1 {
		t.Fatalf("terminate n=%d", n)
	}
	unreg()
	if r.Terminate("app") != 0 {
		t.Fatal("already gone")
	}
	r.Register("a", func() { n++ })
	r.Register("b", func() { n++ })
	if r.TerminateAll() != 2 {
		t.Fatal("terminate all")
	}
}
