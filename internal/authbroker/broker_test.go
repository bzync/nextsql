package authbroker_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/authbroker"
	"github.com/bzync/nextsql/internal/oidc/oidctest"
)

const (
	issuer   = "https://idp.example/realm"
	clientID = "nextsql-oidc-client"
	audience = "prod-eu"
)

func testPolicy() auth.PolicyDoc {
	return auth.PolicyDoc{
		SubjectRules: []auth.SubjectRule{{
			ID:     "corp-email",
			Issuer: issuer,
			Match: []auth.MatchCond{
				{Claim: "email", Op: auth.OpHasSuffix, Value: "@corp.example"},
				{Claim: "email_verified", Op: auth.OpEquals, Value: "true"},
			},
			Principal: auth.Principal{
				Kind:  auth.PrincipalClaim,
				Value: "email",
				Transforms: []auth.Transform{
					{Op: auth.TransformBefore, A: "@"},
					{Op: auth.TransformLower},
				},
			},
		}},
		GroupClaim: "groups",
		GroupMappings: []auth.GroupMapping{
			{Group: "db-readers", Roles: []string{"reporting_ro"}},
			{Group: "db-admins", Roles: []string{"app_admin", "reporting_ro"}},
		},
	}
}

type brokerFixture struct {
	broker  *authbroker.Broker
	srv     *httptest.Server
	idp     *oidctest.IdP
	fetcher *oidctest.Fetcher
	pubKS   *auth.TokenKeyset
	now     time.Time
}

func newFixture(t *testing.T, opts authbroker.Options) *brokerFixture {
	t.Helper()
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "idp.nsip")
	if err := auth.WriteIdentityPolicy(policyPath, testPolicy()); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	ksPath := filepath.Join(dir, "issuing.nstk")
	issuerKS, err := auth.CreateTokenKeyset(ksPath)
	if err != nil {
		t.Fatalf("create keyset: %v", err)
	}
	pubPath := filepath.Join(dir, "verify.nstk")
	if err := issuerKS.WritePublic(pubPath); err != nil {
		t.Fatalf("write public keyset: %v", err)
	}
	pubKS, err := auth.OpenTokenKeyset(pubPath)
	if err != nil {
		t.Fatalf("open public keyset: %v", err)
	}

	idp := oidctest.NewRSA(t, issuer)
	fetcher := idp.Fetcher()

	now := time.Now()
	if opts.Now == nil {
		opts.Now = func() time.Time { return now }
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	opts.Fetcher = fetcher

	cfg := authbroker.Config{
		Listen:             "127.0.0.1:0",
		IdentityPolicy:     policyPath,
		IssuingKeyset:      ksPath,
		DeploymentAudience: audience,
		CredentialTTL:      time.Hour,
		LogLevel:           "error",
		Profiles: []authbroker.IdPProfile{{
			Name:     "corp",
			Issuer:   issuer,
			ClientID: clientID,
			JWKSURI:  idp.JWKSURI(),
		}},
	}
	b, err := authbroker.New(cfg, opts)
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	srv := httptest.NewServer(b.Handler())
	t.Cleanup(srv.Close)
	return &brokerFixture{broker: b, srv: srv, idp: idp, fetcher: fetcher, pubKS: pubKS, now: now}
}

func (f *brokerFixture) claims(email, nonce string, groups []string) map[string]any {
	c := f.idp.StandardClaims(clientID, "sub-"+email, nonce, f.now, time.Hour)
	c["email"] = email
	c["email_verified"] = true
	c["groups"] = anySlice(groups)
	return c
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func (f *brokerFixture) exchange(t *testing.T, body map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(f.srv.URL+"/v1/exchange", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	dec := json.NewDecoder(resp.Body)
	_ = dec.Decode(&out)
	return resp, out
}

func TestExchangeHappyPathMintsVerifiableCredential(t *testing.T) {
	f := newFixture(t, authbroker.Options{})
	tok := f.idp.Sign(t, f.claims("alice@corp.example", "n1", []string{"db-readers"}))

	resp, out := f.exchange(t, map[string]any{"idp": "corp", "id_token": tok, "nonce": "n1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, out)
	}
	if out["principal"] != "alice" {
		t.Fatalf("principal = %v, want alice", out["principal"])
	}
	cred, _ := out["credential"].(string)
	if cred == "" {
		t.Fatal("no credential in response")
	}

	// The minted credential verifies against a real verify-only TokenVerifier
	// built from the issuing keyset's public half, with the deployment audience.
	verifier := auth.NewTokenVerifier(f.pubKS, nil, audience)
	verifier.SetClock(func() time.Time { return f.now })
	claims, err := verifier.Verify(cred)
	if err != nil {
		t.Fatalf("verify minted credential: %v", err)
	}
	if claims.Principal != "alice" {
		t.Fatalf("credential principal = %q", claims.Principal)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "reporting_ro" {
		t.Fatalf("credential roles = %v, want [reporting_ro]", claims.Roles)
	}
	if claims.Audience != audience {
		t.Fatalf("credential audience = %q", claims.Audience)
	}
	if !claims.ExpiresAt.After(f.now) || claims.ExpiresAt.After(f.now.Add(2*time.Hour)) {
		t.Fatalf("credential expiry out of range: %s", claims.ExpiresAt)
	}
}

func TestExchangeRBACIntersection(t *testing.T) {
	// Membership feed that grants alice reporting_ro but not app_admin.
	members := func(principal string) ([]string, error) {
		if principal == "alice" {
			return []string{"reporting_ro"}, nil
		}
		return nil, nil
	}
	f := newFixture(t, authbroker.Options{RoleMembership: members})

	// db-admins maps to {app_admin, reporting_ro}; only reporting_ro survives.
	tok := f.idp.Sign(t, f.claims("alice@corp.example", "n", []string{"db-admins"}))
	resp, out := f.exchange(t, map[string]any{"idp": "corp", "id_token": tok, "nonce": "n"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	roles, _ := out["roles"].([]any)
	if len(roles) != 1 || roles[0] != "reporting_ro" {
		t.Fatalf("effective roles = %v, want [reporting_ro]", roles)
	}

	// bob holds none of the mapped roles -> deny.
	tok2 := f.idp.Sign(t, f.claims("bob@corp.example", "n2", []string{"db-admins"}))
	resp2, _ := f.exchange(t, map[string]any{"idp": "corp", "id_token": tok2, "nonce": "n2"})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a principal with no matching membership, got %d", resp2.StatusCode)
	}
}

func TestExchangeRejections(t *testing.T) {
	f := newFixture(t, authbroker.Options{})
	// Each case uses a distinct subject so a token that passes signature
	// verification does not collide in the replay cache with another case.
	good := func(tag string, groups ...string) map[string]any {
		return f.claims(tag+"@corp.example", tag, groups)
	}

	cases := []struct {
		name string
		body func() map[string]any
		want int
	}{
		{"unknown idp", func() map[string]any {
			return map[string]any{"idp": "nope", "id_token": f.idp.Sign(t, good("a", "db-readers")), "nonce": "a"}
		}, http.StatusBadRequest},
		{"alg none", func() map[string]any {
			return map[string]any{"idp": "corp", "nonce": "b",
				"id_token": f.idp.SignWith(t, "none", f.idp.Kid, good("b", "db-readers"))}
		}, http.StatusForbidden},
		{"wrong audience", func() map[string]any {
			c := good("c", "db-readers")
			c["aud"] = "someone-else"
			return map[string]any{"idp": "corp", "id_token": f.idp.Sign(t, c), "nonce": "c"}
		}, http.StatusForbidden},
		{"bad nonce", func() map[string]any {
			return map[string]any{"idp": "corp", "id_token": f.idp.Sign(t, good("d", "db-readers")), "nonce": "expected"}
		}, http.StatusForbidden},
		{"unmapped subject", func() map[string]any {
			c := f.claims("dave@other.example", "e", []string{"db-readers"})
			return map[string]any{"idp": "corp", "id_token": f.idp.Sign(t, c), "nonce": "e"}
		}, http.StatusForbidden},
		{"unmapped groups only", func() map[string]any {
			return map[string]any{"idp": "corp", "id_token": f.idp.Sign(t, good("g", "random-group")), "nonce": "g"}
		}, http.StatusForbidden},
		{"missing group claim", func() map[string]any {
			c := good("h")
			delete(c, "groups")
			return map[string]any{"idp": "corp", "id_token": f.idp.Sign(t, c), "nonce": "h"}
		}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := f.exchange(t, tc.body())
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d (%v)", resp.StatusCode, tc.want, out)
			}
			if _, leaked := out["credential"]; leaked {
				t.Fatal("a rejected exchange must not return a credential")
			}
		})
	}
}

func TestExchangeReplayRejected(t *testing.T) {
	f := newFixture(t, authbroker.Options{})
	tok := f.idp.Sign(t, f.claims("erin@corp.example", "n", []string{"db-readers"}))
	body := map[string]any{"idp": "corp", "id_token": tok, "nonce": "n"}

	if resp, _ := f.exchange(t, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("first exchange status %d", resp.StatusCode)
	}
	resp, _ := f.exchange(t, body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("replayed exchange status = %d, want 403", resp.StatusCode)
	}
}

func TestExchangeJWKSOutageFailsClosed(t *testing.T) {
	f := newFixture(t, authbroker.Options{})
	f.fetcher.Fail = true // JWKS never fetched, hard TTL exceeded from t=0

	tok := f.idp.Sign(t, f.claims("frank@corp.example", "n", []string{"db-readers"}))
	resp, _ := f.exchange(t, map[string]any{"idp": "corp", "id_token": tok, "nonce": "n"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the IdP key set is unavailable", resp.StatusCode)
	}
}

func TestExchangeCredentialTTLBoundedByIdPExpiry(t *testing.T) {
	f := newFixture(t, authbroker.Options{})
	// ID token that expires in 10 minutes; configured credential TTL is 1h.
	c := f.claims("gita@corp.example", "n", []string{"db-readers"})
	c["exp"] = f.now.Add(10 * time.Minute).Unix()
	tok := f.idp.Sign(t, c)

	_, out := f.exchange(t, map[string]any{"idp": "corp", "id_token": tok, "nonce": "n"})
	cred, _ := out["credential"].(string)
	if cred == "" {
		t.Fatalf("no credential: %v", out)
	}
	verifier := auth.NewTokenVerifier(f.pubKS, nil, audience)
	verifier.SetClock(func() time.Time { return f.now })
	claims, err := verifier.Verify(cred)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.ExpiresAt.After(f.now.Add(11 * time.Minute)) {
		t.Fatalf("credential expiry %s exceeds the IdP token expiry", claims.ExpiresAt)
	}
}

func TestExchangeReloadKeepsLastKnownGoodOnBadPolicy(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "idp.nsip")
	if err := auth.WriteIdentityPolicy(policyPath, testPolicy()); err != nil {
		t.Fatal(err)
	}
	ksPath := filepath.Join(dir, "issuing.nstk")
	if _, err := auth.CreateTokenKeyset(ksPath); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cfg := authbroker.Config{
		Listen: "127.0.0.1:0", IdentityPolicy: policyPath, IssuingKeyset: ksPath,
		DeploymentAudience: audience, CredentialTTL: time.Hour, LogLevel: "error",
		Profiles: []authbroker.IdPProfile{{Name: "corp", Issuer: issuer, ClientID: clientID, JWKSURI: issuer + "/jwks"}},
	}
	idp := oidctest.NewRSA(t, issuer)
	b, err := authbroker.New(cfg, authbroker.Options{
		Fetcher: idp.Fetcher(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the policy file, then reload: it must fail and keep serving.
	if err := os.WriteFile(policyPath, []byte("not a policy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Reload(); err == nil {
		t.Fatal("expected reload to fail on a corrupt policy")
	}

	srv := httptest.NewServer(b.Handler())
	defer srv.Close()
	c := idp.StandardClaims(clientID, "sub-h", "n", now, time.Hour)
	c["email"] = "helen@corp.example"
	c["email_verified"] = true
	c["groups"] = []any{"db-readers"}
	tok := idp.Sign(t, c)
	raw, _ := json.Marshal(map[string]any{"idp": "corp", "id_token": tok, "nonce": "n"})
	resp, err := http.Post(srv.URL+"/v1/exchange", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("last-known-good policy should still mint: status %d, %s", resp.StatusCode, body)
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broker.conf")
	content := `
# broker
listen = 127.0.0.1:8645
identity_policy = /etc/nextsql/idp.nsip
issuing_keyset = /etc/nextsql/issuing.nstk
deployment_audience = prod-eu
oidc_credential_ttl = 30m

[idp "corp"]
issuer = https://corp.okta.com/oauth2/abc
client_id = 0oaABC
allowed_algs = RS256, ES256
group_claim = groups
jwks_soft_ttl = 1h
jwks_hard_ttl = 24h
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := authbroker.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CredentialTTL != 30*time.Minute {
		t.Fatalf("ttl = %s", cfg.CredentialTTL)
	}
	p, ok := cfg.Profile("corp")
	if !ok || p.ClientID != "0oaABC" || len(p.AllowedAlgs) != 2 {
		t.Fatalf("profile = %+v", p)
	}

	// Unknown key is rejected.
	if err := os.WriteFile(path, []byte("bogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authbroker.LoadConfig(path); err == nil {
		t.Fatal("expected unknown key to be rejected")
	}
}
