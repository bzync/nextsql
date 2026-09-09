package credential

import "testing"

func TestKeyIsStableOpaqueAndTargetScoped(t *testing.T) {
	a := Key("db.example:7210", "alice", "acme", "main")
	if a != Key(" db.example:7210 ", " alice ", " acme ", " main ") {
		t.Fatal("trimmed target should retain its credential key")
	}
	if a == Key("db.example:7210", "alice", "acme", "other") {
		t.Fatal("database must scope credential key")
	}
	if len(a) != len("target-")+64 {
		t.Fatalf("key length %d", len(a))
	}
	if containsAny(a, "db.example", "alice", "acme", "main") {
		t.Fatalf("credential key leaks target data: %q", a)
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if p != "" && len(p) <= len(s) {
			for i := 0; i+len(p) <= len(s); i++ {
				if s[i:i+len(p)] == p {
					return true
				}
			}
		}
	}
	return false
}
