package credential

import (
	"strings"
	"testing"
)

func TestDelegationKeyIsOpaqueAndBoundToBothPrincipals(t *testing.T) {
	origin := Principal{Address: "127.0.0.1:7210", ServerName: "localhost", User: "alice"}
	target := Target{Principal: Principal{Address: "db.example:7210", ServerName: "db.internal", User: "alice_prod"}, Database: "main"}
	a := DelegationKey(origin, target)

	trimmed := DelegationKey(
		Principal{Address: " 127.0.0.1:7210 ", ServerName: "localhost", User: " alice "},
		Target{Principal: Principal{Address: "db.example:7210 ", ServerName: "db.internal", User: "alice_prod"}, Database: " main"},
	)
	if a != trimmed {
		t.Fatal("surrounding whitespace should not change the key")
	}
	if len(a) != len("delegation-")+64 {
		t.Fatalf("key length %d", len(a))
	}
	for _, leak := range []string{"127.0.0.1", "alice", "db.example", "db.internal", "main"} {
		if strings.Contains(a, leak) {
			t.Fatalf("key %q leaks %q", a, leak)
		}
	}

	// Each component on its own must change the key.
	variants := map[string]string{
		"origin user":        DelegationKey(Principal{Address: origin.Address, ServerName: origin.ServerName, User: "mallory"}, target),
		"origin address":     DelegationKey(Principal{Address: "127.0.0.1:7211", ServerName: origin.ServerName, User: origin.User}, target),
		"target address":     DelegationKey(origin, Target{Principal: Principal{Address: "evil.example:7210", ServerName: "db.internal", User: "alice_prod"}, Database: "main"}),
		"target server name": DelegationKey(origin, Target{Principal: Principal{Address: "db.example:7210", ServerName: "other", User: "alice_prod"}, Database: "main"}),
		"target user":        DelegationKey(origin, Target{Principal: Principal{Address: "db.example:7210", ServerName: "db.internal", User: "root"}, Database: "main"}),
		"target database":    DelegationKey(origin, Target{Principal: Principal{Address: "db.example:7210", ServerName: "db.internal", User: "alice_prod"}, Database: "other"}),
	}
	for name, k := range variants {
		if k == a {
			t.Fatalf("changing the %s did not change the key", name)
		}
	}

	// Length prefixing: shifting bytes between adjacent fields must not
	// collide ("ab"+"c" vs "a"+"bc").
	x := DelegationKey(Principal{Address: "ab", ServerName: "c"}, Target{})
	y := DelegationKey(Principal{Address: "a", ServerName: "bc"}, Target{})
	if x == y {
		t.Fatal("adjacent fields collide")
	}
}
