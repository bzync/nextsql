package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/authbroker"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/oidc/oidctest"
	"github.com/bzync/nextsql/internal/security"
)

func TestEmbeddedAuthBrokerUsesLiveNativeMembership(t *testing.T) {
	const (
		issuer   = "https://idp.example/embedded"
		clientID = "embedded-client"
		audience = "embedded-deployment"
	)
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "identity.nsip")
	policy := auth.PolicyDoc{
		SubjectRules: []auth.SubjectRule{{
			ID: "alice", Issuer: issuer,
			Match:     []auth.MatchCond{{Claim: "sub", Op: auth.OpHasPrefix, Value: "subject-"}},
			Principal: auth.Principal{Kind: auth.PrincipalLiteral, Value: "alice"},
		}},
		GroupClaim:    "groups",
		GroupMappings: []auth.GroupMapping{{Group: "readers", Roles: []string{"reporting_ro"}}},
	}
	if err := auth.WriteIdentityPolicy(policyPath, policy); err != nil {
		t.Fatal(err)
	}
	issuingPath := filepath.Join(dir, "issuing.nstk")
	issuing, err := auth.CreateTokenKeyset(issuingPath)
	if err != nil {
		t.Fatal(err)
	}
	verifyPath := filepath.Join(dir, "verify.nstk")
	if err := issuing.WritePublic(verifyPath); err != nil {
		t.Fatal(err)
	}
	idp := oidctest.NewRSA(t, issuer)
	brokerConfigPath := filepath.Join(dir, config.AuthBrokerFileName)
	brokerConfig := "listen = 127.0.0.1:0\n" +
		"identity_policy = " + policyPath + "\n" +
		"issuing_keyset = " + issuingPath + "\n" +
		"deployment_audience = " + audience + "\n" +
		"oidc_credential_ttl = 1h\n\n" +
		"[idp \"corp\"]\n" +
		"issuer = " + issuer + "\n" +
		"client_id = " + clientID + "\n" +
		"jwks_uri = " + idp.JWKSURI() + "\n"
	if err := os.WriteFile(brokerConfigPath, []byte(brokerConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	users, err := auth.Create(filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("alice", "password"); err != nil {
		t.Fatal(err)
	}
	acl, err := security.CreateACL(filepath.Join(dir, "acl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.CreateRole("reporting_ro"); err != nil {
		t.Fatal(err)
	}
	if err := acl.GrantRole("reporting_ro", "alice"); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dir
	cfg.TokenKeyset = verifyPath
	cfg.TokenAudience = audience
	cfg.AuthBrokerListen = "127.0.0.1:0"
	now := time.Now()
	broker, server, err := startEmbeddedAuthBroker(cfg, users, acl, nil, authbroker.Options{
		Fetcher: idp.Fetcher(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_ = broker
	errc := make(chan error, 1)
	go func() { errc <- server.Serve() }()

	exchange := func(subject, nonce string) (*http.Response, map[string]any) {
		t.Helper()
		claims := idp.StandardClaims(clientID, subject, nonce, now, time.Hour)
		claims["groups"] = []any{"readers"}
		raw, _ := json.Marshal(map[string]any{
			"idp": "corp", "id_token": idp.Sign(t, claims), "nonce": nonce,
		})
		resp, postErr := http.Post("http://"+server.Addr().String()+"/v1/exchange", "application/json", bytes.NewReader(raw))
		if postErr != nil {
			t.Fatal(postErr)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	resp, out := exchange("subject-alice", "n1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, out)
	}
	credential, _ := out["credential"].(string)
	verifyKeys, err := auth.OpenTokenKeyset(verifyPath)
	if err != nil {
		t.Fatal(err)
	}
	verifier := auth.NewTokenVerifier(verifyKeys, nil, audience)
	verifier.SetClock(func() time.Time { return now })
	claims, err := verifier.Verify(credential)
	if err != nil || len(claims.Roles) != 1 || claims.Roles[0] != "reporting_ro" {
		t.Fatalf("minted credential: claims=%+v err=%v", claims, err)
	}

	if err := acl.RevokeRole("reporting_ro", "alice"); err != nil {
		t.Fatal(err)
	}
	resp, out = exchange("subject-alice-2", "n2")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked live membership status = %d, body = %v", resp.StatusCode, out)
	}
	if _, leaked := out["credential"]; leaked {
		t.Fatal("denied exchange leaked a credential")
	}
	if err := acl.GrantRole("reporting_ro", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := users.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	resp, out = exchange("subject-alice-3", "n3")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing native user status = %d, body = %v", resp.StatusCode, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedAuthBrokerRejectsUnverifiableIssuingKey(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "identity.nsip")
	if err := auth.WriteIdentityPolicy(policyPath, auth.PolicyDoc{
		SubjectRules: []auth.SubjectRule{{
			ID: "rule", Issuer: "https://idp.example", Match: []auth.MatchCond{{Claim: "sub", Op: auth.OpEquals, Value: "x"}},
			Principal: auth.Principal{Kind: auth.PrincipalLiteral, Value: "alice"},
		}},
		DefaultRoles: []string{"reader"},
	}); err != nil {
		t.Fatal(err)
	}
	issuingPath := filepath.Join(dir, "issuing.nstk")
	if _, err := auth.CreateTokenKeyset(issuingPath); err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(dir, "other.nstk")
	other, err := auth.CreateTokenKeyset(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	verifyPath := filepath.Join(dir, "verify.nstk")
	if err := other.WritePublic(verifyPath); err != nil {
		t.Fatal(err)
	}
	brokerConfig := "listen = 127.0.0.1:0\nidentity_policy = " + policyPath + "\nissuing_keyset = " + issuingPath + "\n" +
		"[idp \"corp\"]\nissuer = https://idp.example\nclient_id = client\njwks_uri = https://idp.example/jwks\n"
	if err := os.WriteFile(filepath.Join(dir, config.AuthBrokerFileName), []byte(brokerConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	acl, err := security.CreateACL(filepath.Join(dir, "acl"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir, cfg.TokenKeyset, cfg.AuthBrokerListen = dir, verifyPath, "127.0.0.1:0"
	if _, _, err := startEmbeddedAuthBroker(cfg, users, acl, nil, authbroker.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}); err == nil {
		t.Fatal("embedded broker accepted an issuing key absent from token_verify_keyset")
	}
}
