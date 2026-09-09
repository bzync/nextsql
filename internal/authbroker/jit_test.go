package authbroker_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/authbroker"
	"github.com/bzync/nextsql/internal/oidc/oidctest"
)

func TestJITProvisioningDisabledByDefault(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()

	idp := oidctest.NewRSA(t, issuer)

	policyPath := filepath.Join(dir, "test.policy")
	if err := auth.WriteIdentityPolicy(policyPath, testPolicy()); err != nil {
		t.Fatal(err)
	}

	keysetPath := filepath.Join(dir, "issuing.nstk")
	if _, err := auth.CreateTokenKeyset(keysetPath); err != nil {
		t.Fatal(err)
	}

	cfg := authbroker.Config{
		Listen:             "127.0.0.1:0",
		IdentityPolicy:     policyPath,
		IssuingKeyset:      keysetPath,
		DeploymentAudience: audience,
		CredentialTTL:      time.Hour,
		LogLevel:           "error",
		JITProvisioning:    false, // explicitly false
		Profiles: []authbroker.IdPProfile{{
			Name:        "test-idp",
			Issuer:      issuer,
			ClientID:    clientID,
			JWKSURI:     idp.JWKSURI(),
			AllowedAlgs: []string{"RS256"},
		}},
	}

	provisionCalled := false
	broker, err := authbroker.New(cfg, authbroker.Options{
		Fetcher: idp.Fetcher(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return now },
		RoleMembership: func(realm, principal string) ([]string, error) {
			// User does not exist, has no roles
			return nil, nil
		},
		UserProvisioner: func(realm, principal string, roles []string) error {
			provisionCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("authbroker.New: %v", err)
	}

	c := idp.StandardClaims(clientID, "user-1", "nonce-1", now, time.Hour)
	c["email"] = "alice@corp.example"
	c["email_verified"] = "true"
	c["groups"] = []any{"db-admins"}
	tok := idp.Sign(t, c)

	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	raw, _ := json.Marshal(map[string]any{"idp": "test-idp", "id_token": tok, "nonce": "nonce-1"})
	resp, err := http.Post(srv.URL+"/v1/exchange", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 403 Forbidden when JIT is disabled, got %d (body: %s)", resp.StatusCode, body)
	}
	if provisionCalled {
		t.Fatal("UserProvisioner was called when JIT was disabled")
	}
}

func TestJITProvisioningHappyPathAndBoundary(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()

	idp := oidctest.NewRSA(t, issuer)

	policyPath := filepath.Join(dir, "test.policy")
	if err := auth.WriteIdentityPolicy(policyPath, testPolicy()); err != nil {
		t.Fatal(err)
	}

	keysetPath := filepath.Join(dir, "issuing.nstk")
	ks, err := auth.CreateTokenKeyset(keysetPath)
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(dir, "verify.nstk")
	if err := ks.WritePublic(pubPath); err != nil {
		t.Fatal(err)
	}
	pubKS, err := auth.OpenTokenKeyset(pubPath)
	if err != nil {
		t.Fatal(err)
	}

	cfg := authbroker.Config{
		Listen:              "127.0.0.1:0",
		IdentityPolicy:      policyPath,
		IssuingKeyset:       keysetPath,
		DeploymentAudience:  audience,
		CredentialTTL:       time.Hour,
		LogLevel:            "error",
		JITProvisioning:     true,
		AllowedRoleBoundary: []string{"reporting_ro"}, // boundary only permits reporting_ro, app_admin stripped!
		MaxPrincipals:       100,
		Profiles: []authbroker.IdPProfile{{
			Name:        "test-idp",
			Issuer:      issuer,
			ClientID:    clientID,
			JWKSURI:     idp.JWKSURI(),
			AllowedAlgs: []string{"RS256"},
		}},
	}

	var provisionedPrincipal string
	var provisionedRoles []string

	broker, err := authbroker.New(cfg, authbroker.Options{
		Fetcher: idp.Fetcher(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return now },
		RoleMembership: func(realm, principal string) ([]string, error) {
			// User does not exist initially
			if principal == provisionedPrincipal {
				return provisionedRoles, nil
			}
			return nil, nil
		},
		UserProvisioner: func(realm, principal string, roles []string) error {
			provisionedPrincipal = principal
			provisionedRoles = roles
			return nil
		},
	})
	if err != nil {
		t.Fatalf("authbroker.New: %v", err)
	}

	c := idp.StandardClaims(clientID, "user-jit-1", "nonce-2", now, time.Hour)
	c["email"] = "bob@corp.example"
	c["email_verified"] = "true"
	c["groups"] = []any{"db-admins"}
	tok := idp.Sign(t, c)

	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	raw, _ := json.Marshal(map[string]any{"idp": "test-idp", "id_token": tok, "nonce": "nonce-2"})
	resp, err := http.Post(srv.URL+"/v1/exchange", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 200 OK for JIT exchange, got %d (body: %s)", resp.StatusCode, body)
	}

	if provisionedPrincipal != "bob" {
		t.Errorf("provisionedPrincipal = %q, want bob", provisionedPrincipal)
	}
	// Verify boundary narrowed roles to reporting_ro only
	if len(provisionedRoles) != 1 || provisionedRoles[0] != "reporting_ro" {
		t.Fatalf("provisionedRoles = %v, want [reporting_ro]", provisionedRoles)
	}

	var out struct {
		Credential string   `json:"credential"`
		Principal  string   `json:"principal"`
		Roles      []string `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Principal != "bob" {
		t.Errorf("response principal = %q, want bob", out.Principal)
	}
	if len(out.Roles) != 1 || out.Roles[0] != "reporting_ro" {
		t.Errorf("response roles = %v, want [reporting_ro]", out.Roles)
	}

	// Verify minted credential
	verifier := auth.NewTokenVerifier(pubKS, nil, audience)
	claims, err := verifier.Verify(out.Credential)
	if err != nil {
		t.Fatalf("verify credential: %v", err)
	}
	if claims.Principal != "bob" {
		t.Errorf("claims.Principal = %q, want bob", claims.Principal)
	}
}

func TestJITProvisioningEscalationSafeguard(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()

	idp := oidctest.NewRSA(t, issuer)

	// Policy that maps to an admin role
	adminPolicy := auth.PolicyDoc{
		SubjectRules: []auth.SubjectRule{{
			ID:     "corp-email",
			Issuer: issuer,
			Match: []auth.MatchCond{
				{Claim: "email", Op: auth.OpHasSuffix, Value: "@corp.example"},
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
		DefaultRoles: []string{"admin"}, // attempts to map to admin!
	}

	policyPath := filepath.Join(dir, "admin.policy")
	if err := auth.WriteIdentityPolicy(policyPath, adminPolicy); err != nil {
		t.Fatal(err)
	}

	keysetPath := filepath.Join(dir, "issuing.nstk")
	if _, err := auth.CreateTokenKeyset(keysetPath); err != nil {
		t.Fatal(err)
	}

	// JIT is enabled, but AllowedRoleBoundary does NOT include admin (only reader)
	cfg := authbroker.Config{
		Listen:              "127.0.0.1:0",
		IdentityPolicy:      policyPath,
		IssuingKeyset:       keysetPath,
		DeploymentAudience:  audience,
		CredentialTTL:       time.Hour,
		LogLevel:            "error",
		JITProvisioning:     true,
		AllowedRoleBoundary: []string{"reader"},
		MaxPrincipals:       100,
		Profiles: []authbroker.IdPProfile{{
			Name:        "test-idp",
			Issuer:      issuer,
			ClientID:    clientID,
			JWKSURI:     idp.JWKSURI(),
			AllowedAlgs: []string{"RS256"},
		}},
	}

	broker, err := authbroker.New(cfg, authbroker.Options{
		Fetcher: idp.Fetcher(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return now },
		RoleMembership: func(realm, principal string) ([]string, error) {
			return nil, nil
		},
		UserProvisioner: func(realm, principal string, roles []string) error {
			t.Fatal("UserProvisioner should not be called when roles are rejected by boundary")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("authbroker.New: %v", err)
	}

	c := idp.StandardClaims(clientID, "attacker-1", "nonce-3", now, time.Hour)
	c["email"] = "charlie@corp.example"
	tok := idp.Sign(t, c)

	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	raw, _ := json.Marshal(map[string]any{"idp": "test-idp", "id_token": tok, "nonce": "nonce-3"})
	resp, err := http.Post(srv.URL+"/v1/exchange", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 403 Forbidden for escalation attempt, got %d (body: %s)", resp.StatusCode, body)
	}
}

func TestJITProvisioningProvisionerError(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()

	idp := oidctest.NewRSA(t, issuer)

	policyPath := filepath.Join(dir, "test.policy")
	if err := auth.WriteIdentityPolicy(policyPath, testPolicy()); err != nil {
		t.Fatal(err)
	}

	keysetPath := filepath.Join(dir, "issuing.nstk")
	if _, err := auth.CreateTokenKeyset(keysetPath); err != nil {
		t.Fatal(err)
	}

	cfg := authbroker.Config{
		Listen:              "127.0.0.1:0",
		IdentityPolicy:      policyPath,
		IssuingKeyset:       keysetPath,
		DeploymentAudience:  audience,
		CredentialTTL:       time.Hour,
		LogLevel:            "error",
		JITProvisioning:     true,
		AllowedRoleBoundary: []string{"reporting_ro"},
		MaxPrincipals:       100,
		Profiles: []authbroker.IdPProfile{{
			Name:        "test-idp",
			Issuer:      issuer,
			ClientID:    clientID,
			JWKSURI:     idp.JWKSURI(),
			AllowedAlgs: []string{"RS256"},
		}},
	}

	broker, err := authbroker.New(cfg, authbroker.Options{
		Fetcher: idp.Fetcher(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return now },
		RoleMembership: func(realm, principal string) ([]string, error) {
			return nil, nil
		},
		UserProvisioner: func(realm, principal string, roles []string) error {
			return errors.New("storage disk full / limit exceeded")
		},
	})
	if err != nil {
		t.Fatalf("authbroker.New: %v", err)
	}

	c := idp.StandardClaims(clientID, "user-err-1", "nonce-4", now, time.Hour)
	c["email"] = "dave@corp.example"
	c["email_verified"] = "true"
	c["groups"] = []any{"db-readers"}
	tok := idp.Sign(t, c)

	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	raw, _ := json.Marshal(map[string]any{"idp": "test-idp", "id_token": tok, "nonce": "nonce-4"})
	resp, err := http.Post(srv.URL+"/v1/exchange", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 500 when provisioner errors, got %d (body: %s)", resp.StatusCode, body)
	}
}
