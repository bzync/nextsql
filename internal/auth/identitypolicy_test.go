package auth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func sampleDoc() PolicyDoc {
	return PolicyDoc{
		GroupClaim: "groups",
		SubjectRules: []SubjectRule{
			{
				ID:     "corp-email",
				Issuer: "https://corp.okta.com/oauth2/abc",
				Match: []MatchCond{
					{Claim: "email", Op: OpHasSuffix, Value: "@corp.example"},
					{Claim: "email_verified", Op: OpEquals, Value: "true"},
				},
				Principal: Principal{
					Kind:  PrincipalClaim,
					Value: "email",
					Transforms: []Transform{
						{Op: TransformBefore, A: "@"},
						{Op: TransformLower},
					},
				},
			},
			{
				ID:        "corp-sub",
				Issuer:    "https://corp.okta.com/oauth2/abc",
				Match:     []MatchCond{{Claim: "sub", Op: OpEquals, Value: "svc-ci"}},
				Principal: Principal{Kind: PrincipalLiteral, Value: "ci-runner"},
			},
		},
		GroupMappings: []GroupMapping{
			{Group: "db-readers", Roles: []string{"reporting_ro"}},
			{Group: "db-admins", Roles: []string{"app_admin", "reporting_ro"}},
			{Group: `team-(.+)`, IsRegex: true, Roles: []string{"team_${1}_rw"}},
		},
		DefaultRoles: []string{"base_connect"},
	}
}

func mustPolicy(t *testing.T, doc PolicyDoc) *IdentityPolicy {
	t.Helper()
	p, err := LoadIdentityPolicy(doc)
	if err != nil {
		t.Fatalf("LoadIdentityPolicy: %v", err)
	}
	return p
}

func TestIdentityPolicyRoundTrip(t *testing.T) {
	doc := sampleDoc()
	raw, err := encodeIdentityPolicy(doc)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decodeIdentityPolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cloneDoc(doc), cloneDoc(back)) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", doc, back)
	}
	raw2, err := encodeIdentityPolicy(back)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw, raw2) {
		t.Fatal("re-encode not byte-identical")
	}
}

func TestIdentityPolicySubjectRuleFirstMatchWins(t *testing.T) {
	p := mustPolicy(t, sampleDoc())
	iss := "https://corp.okta.com/oauth2/abc"

	r, err := p.Map(iss, map[string]any{
		"email": "Alice@corp.example", "email_verified": true, "groups": []any{"db-readers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.RuleID != "corp-email" || r.Principal != "alice" {
		t.Fatalf("unexpected: %+v", r)
	}

	r, err = p.Map(iss, map[string]any{"sub": "svc-ci", "groups": []any{"db-admins"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.RuleID != "corp-sub" || r.Principal != "ci-runner" {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestIdentityPolicyNoRuleMatchesDenies(t *testing.T) {
	p := mustPolicy(t, sampleDoc())
	_, err := p.Map("https://evil.example", map[string]any{"email": "x@corp.example", "email_verified": true})
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want Forbidden, got %v", err)
	}
	// right issuer, condition fails
	_, err = p.Map("https://corp.okta.com/oauth2/abc", map[string]any{
		"email": "x@corp.example", "email_verified": false,
	})
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want Forbidden, got %v", err)
	}
}

func TestIdentityPolicyPrincipalTransformFailsClosed(t *testing.T) {
	p := mustPolicy(t, sampleDoc())
	// email has no "@" so TransformBefore cannot apply.
	_, err := p.Map("https://corp.okta.com/oauth2/abc", map[string]any{
		"email": "not-an-email", "email_verified": true, "groups": []any{"db-readers"},
	})
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want Forbidden, got %v", err)
	}
}

func TestIdentityPolicyPrincipalCharsetRejected(t *testing.T) {
	doc := PolicyDoc{
		SubjectRules: []SubjectRule{{
			ID:        "r",
			Issuer:    "iss",
			Match:     []MatchCond{{Claim: "sub", Op: OpEquals, Value: "x"}},
			Principal: Principal{Kind: PrincipalClaim, Value: "name"},
		}},
		DefaultRoles: []string{"base"},
	}
	p := mustPolicy(t, doc)
	_, err := p.Map("iss", map[string]any{"sub": "x", "name": "a b/c"})
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want Forbidden for bad charset, got %v", err)
	}
	r, err := p.Map("iss", map[string]any{"sub": "x", "name": "Good.Name-1"})
	if err != nil || r.Principal != "good.name-1" {
		t.Fatalf("unexpected: %+v %v", r, err)
	}
}

func TestIdentityPolicyGroupRegexCapture(t *testing.T) {
	p := mustPolicy(t, sampleDoc())
	r, err := p.Map("https://corp.okta.com/oauth2/abc", map[string]any{
		"email": "bob@corp.example", "email_verified": true,
		"groups": []any{"team-payments", "unrelated"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Roles, []string{"team_payments_rw"}) {
		t.Fatalf("roles = %v", r.Roles)
	}
}

func TestIdentityPolicyGroupClaimAbsentDenies(t *testing.T) {
	p := mustPolicy(t, sampleDoc())
	_, err := p.Map("https://corp.okta.com/oauth2/abc", map[string]any{
		"email": "bob@corp.example", "email_verified": true,
	})
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want Forbidden, got %v", err)
	}
}

func TestIdentityPolicyDefaultRolesWhenNoGroupMapped(t *testing.T) {
	p := mustPolicy(t, sampleDoc())
	r, err := p.Map("https://corp.okta.com/oauth2/abc", map[string]any{
		"email": "bob@corp.example", "email_verified": true,
		"groups": []any{"nothing-maps-here"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Roles, []string{"base_connect"}) {
		t.Fatalf("roles = %v", r.Roles)
	}
}

func TestIdentityPolicyNoGroupClaimConfigured(t *testing.T) {
	doc := sampleDoc()
	doc.GroupClaim = ""
	p := mustPolicy(t, doc)
	r, err := p.Map("https://corp.okta.com/oauth2/abc", map[string]any{
		"email": "bob@corp.example", "email_verified": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Roles, []string{"base_connect"}) {
		t.Fatalf("roles = %v", r.Roles)
	}

	doc.DefaultRoles = nil
	p = mustPolicy(t, doc)
	if _, err := p.Map("https://corp.okta.com/oauth2/abc", map[string]any{
		"email": "bob@corp.example", "email_verified": true,
	}); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want Forbidden when no roles at all, got %v", err)
	}
}

func TestIdentityPolicyAuthorizeRBACIntersection(t *testing.T) {
	p := mustPolicy(t, sampleDoc())
	claims := map[string]any{
		"email": "carol@corp.example", "email_verified": true,
		"groups": []any{"db-admins"}, // maps to app_admin + reporting_ro
	}

	// principal holds only reporting_ro -> app_admin is dropped (no escalation)
	r, err := p.Authorize("https://corp.okta.com/oauth2/abc", claims, []string{"reporting_ro", "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Roles, []string{"reporting_ro"}) {
		t.Fatalf("roles = %v", r.Roles)
	}

	// principal holds none of the mapped roles -> deny
	_, err = p.Authorize("https://corp.okta.com/oauth2/abc", claims, []string{"something_else"})
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want Forbidden, got %v", err)
	}
}

func TestIntersectRoles(t *testing.T) {
	got := IntersectRoles([]string{"A", "b", "b", "c"}, []string{"B", "c", "d"})
	if !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("got %v", got)
	}
	if len(IntersectRoles([]string{"x"}, nil)) != 0 {
		t.Fatal("want empty")
	}
}

func TestIdentityPolicyRoleCapDenies(t *testing.T) {
	doc := PolicyDoc{
		GroupClaim: "groups",
		SubjectRules: []SubjectRule{{
			ID:        "r",
			Issuer:    "iss",
			Match:     []MatchCond{{Claim: "sub", Op: OpEquals, Value: "x"}},
			Principal: Principal{Kind: PrincipalLiteral, Value: "user"},
		}},
		GroupMappings: []GroupMapping{
			{Group: `g-(.+)`, IsRegex: true, Roles: []string{"role_${1}"}},
		},
	}
	p := mustPolicy(t, doc)
	groups := make([]any, 0, 20)
	for i := 0; i < 20; i++ {
		groups = append(groups, "g-"+string(rune('a'+i)))
	}
	if _, err := p.Map("iss", map[string]any{"sub": "x", "groups": groups}); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want Forbidden for >16 roles, got %v", err)
	}
}

func TestIdentityPolicyReloadLastKnownGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.nsip")
	if err := WriteIdentityPolicy(path, sampleDoc()); err != nil {
		t.Fatal(err)
	}
	p, err := OpenIdentityPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	before := p.Summary()

	if err := os.WriteFile(path, []byte("NSIPgarbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Reload(); err == nil {
		t.Fatal("expected reload error")
	}
	if !reflect.DeepEqual(p.Summary(), before) {
		t.Fatal("policy changed after a failed reload")
	}
	// still evaluates
	if _, err := p.Map("https://corp.okta.com/oauth2/abc", map[string]any{
		"email": "d@corp.example", "email_verified": true, "groups": []any{"db-readers"},
	}); err != nil {
		t.Fatalf("policy broken after failed reload: %v", err)
	}
}

func TestIdentityPolicyRejectsBadRegex(t *testing.T) {
	doc := sampleDoc()
	doc.GroupMappings = append(doc.GroupMappings, GroupMapping{Group: "team-(", IsRegex: true, Roles: []string{"x"}})
	if _, err := LoadIdentityPolicy(doc); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestIdentityPolicyRejectsRoleTemplateOutOfRange(t *testing.T) {
	doc := sampleDoc()
	doc.GroupMappings = []GroupMapping{{Group: `team-(.+)`, IsRegex: true, Roles: []string{"r_${2}"}}}
	if _, err := LoadIdentityPolicy(doc); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func FuzzDecodeIdentityPolicy(f *testing.F) {
	if raw, err := encodeIdentityPolicy(sampleDoc()); err == nil {
		f.Add(raw)
	}
	minimal := PolicyDoc{
		SubjectRules: []SubjectRule{{
			ID:        "r",
			Issuer:    "iss",
			Match:     []MatchCond{{Claim: "sub", Op: OpEquals, Value: "x"}},
			Principal: Principal{Kind: PrincipalLiteral, Value: "user"},
		}},
		DefaultRoles: []string{"base"},
	}
	if raw, err := encodeIdentityPolicy(minimal); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSIP"))
	f.Add([]byte("NSIP\x01\x00"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		doc, err := decodeIdentityPolicy(raw)
		if err != nil {
			return
		}
		// A decoded policy must compile and re-encode without loss.
		if _, _, err := compilePolicy(doc); err != nil {
			t.Fatalf("decoded policy does not compile: %v", err)
		}
		raw2, err := encodeIdentityPolicy(doc)
		if err != nil {
			t.Fatalf("decoded policy does not re-encode: %v", err)
		}
		doc2, err := decodeIdentityPolicy(raw2)
		if err != nil {
			t.Fatalf("re-encoded policy does not decode: %v", err)
		}
		if !reflect.DeepEqual(cloneDoc(doc), cloneDoc(doc2)) {
			t.Fatal("decode/encode/decode not stable")
		}
	})
}

func FuzzMapClaims(f *testing.F) {
	p := mustPolicyF(f, sampleDoc())
	f.Add([]byte(`{"email":"a@corp.example","email_verified":true,"groups":["db-admins"]}`))
	f.Add([]byte(`{"sub":"svc-ci"}`))
	f.Add([]byte(`{"groups":["team-x"]}`))
	f.Add([]byte(`{"email":123,"groups":{}}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var claims map[string]any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&claims); err != nil {
			return
		}
		r, err := p.Map("https://corp.okta.com/oauth2/abc", claims)
		if err != nil {
			return
		}
		if !validLogin(r.Principal) {
			t.Fatalf("Map returned an invalid principal %q", r.Principal)
		}
		if len(r.Roles) == 0 || len(r.Roles) > maxTokenRoles {
			t.Fatalf("Map returned %d roles", len(r.Roles))
		}
		eff, err := p.Authorize("https://corp.okta.com/oauth2/abc", claims, r.Roles)
		if err != nil {
			t.Fatalf("Authorize denied a self-consistent role set: %v", err)
		}
		if len(eff.Roles) != len(r.Roles) {
			t.Fatalf("Authorize dropped roles from an identical held set: %v vs %v", eff.Roles, r.Roles)
		}
	})
}

func mustPolicyF(f *testing.F, doc PolicyDoc) *IdentityPolicy {
	f.Helper()
	p, err := LoadIdentityPolicy(doc)
	if err != nil {
		f.Fatalf("LoadIdentityPolicy: %v", err)
	}
	return p
}
