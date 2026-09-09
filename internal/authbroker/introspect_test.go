package authbroker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/authbroker"
)

func TestReadClientSecretFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "client.secret")
	if err := os.WriteFile(secretPath, []byte("super-secret-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	secret, err := authbroker.ReadClientSecretFile(secretPath)
	if err != nil {
		t.Fatalf("ReadClientSecretFile failed: %v", err)
	}
	if secret != "super-secret-123" {
		t.Fatalf("secret = %q, want super-secret-123", secret)
	}

	// Permissive mode check on non-windows
	if runtime.GOOS != "windows" {
		badPermPath := filepath.Join(dir, "bad-perm.secret")
		if err := os.WriteFile(badPermPath, []byte("secret"), 0o666); err != nil {
			t.Fatal(err)
		}
		if _, err := authbroker.ReadClientSecretFile(badPermPath); err == nil {
			t.Fatal("expected error on mode 0666 file, got nil")
		}
	}

	// Missing path
	if _, err := authbroker.ReadClientSecretFile(filepath.Join(dir, "missing.secret")); err == nil {
		t.Fatal("expected error on missing file, got nil")
	}

	// Over-sized file
	bigPath := filepath.Join(dir, "big.secret")
	if err := os.WriteFile(bigPath, make([]byte, 65<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authbroker.ReadClientSecretFile(bigPath); err == nil {
		t.Fatal("expected error on oversized secret file, got nil")
	}
}

func TestIntrospectorHappyPathAndCache(t *testing.T) {
	now := time.Now().UTC()
	var hitCount int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != "my-client" || p != "my-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "token=opaque-test-token") {
			http.Error(w, "bad token", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active":    true,
			"sub":       "user-42",
			"iss":       "https://idp.example/realm",
			"client_id": "my-client",
			"exp":       now.Add(time.Hour).Unix(),
			"email":     "alice@corp.example",
		})
	}))
	defer srv.Close()

	// Use test server URL (https check: in test constructor we allow HTTP if simulated, or convert)
	// Wait, introspect checks https:// prefix, so let's use an httptest.NewTLSServer
	tlsSrv := httptest.NewTLSServer(srv.Config.Handler)
	defer tlsSrv.Close()

	intro, err := authbroker.NewIntrospector(authbroker.IntrospectorConfig{
		Endpoint:     tlsSrv.URL,
		ClientID:     "my-client",
		ClientSecret: "my-secret",
		Issuer:       "https://idp.example/realm",
		HTTPClient:   tlsSrv.Client(),
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewIntrospector: %v", err)
	}

	tok, err := intro.Introspect(context.Background(), "opaque-test-token")
	if err != nil {
		t.Fatalf("Introspect failed: %v", err)
	}
	if tok.Subject != "user-42" {
		t.Errorf("subject = %q, want user-42", tok.Subject)
	}
	if tok.Claims["email"] != "alice@corp.example" {
		t.Errorf("email = %v, want alice@corp.example", tok.Claims["email"])
	}

	// Second call should hit cache
	tok2, err := intro.Introspect(context.Background(), "opaque-test-token")
	if err != nil {
		t.Fatalf("second Introspect failed: %v", err)
	}
	if tok2.Subject != tok.Subject {
		t.Errorf("mismatch cached token: %v vs %v", tok2, tok)
	}
	if got := atomic.LoadInt32(&hitCount); got != 1 {
		t.Fatalf("expected 1 server hit due to cache, got %d", got)
	}
}

func TestIntrospectorRejections(t *testing.T) {
	now := time.Now().UTC()

	cases := []struct {
		name     string
		response map[string]any
		status   int
	}{
		{
			name:     "inactive",
			response: map[string]any{"active": false, "sub": "u1", "exp": now.Add(time.Hour).Unix()},
			status:   http.StatusOK,
		},
		{
			name:     "missing-sub",
			response: map[string]any{"active": true, "exp": now.Add(time.Hour).Unix()},
			status:   http.StatusOK,
		},
		{
			name:     "expired",
			response: map[string]any{"active": true, "sub": "u1", "exp": now.Add(-time.Hour).Unix()},
			status:   http.StatusOK,
		},
		{
			name:     "issuer-mismatch",
			response: map[string]any{"active": true, "sub": "u1", "exp": now.Add(time.Hour).Unix(), "iss": "https://attacker.example"},
			status:   http.StatusOK,
		},
		{
			name:     "client-id-mismatch",
			response: map[string]any{"active": true, "sub": "u1", "exp": now.Add(time.Hour).Unix(), "client_id": "attacker-client"},
			status:   http.StatusOK,
		},
		{
			name:   "server-error",
			status: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.response != nil {
					_ = json.NewEncoder(w).Encode(tc.response)
				}
			}))
			defer tlsSrv.Close()

			intro, err := authbroker.NewIntrospector(authbroker.IntrospectorConfig{
				Endpoint:   tlsSrv.URL,
				ClientID:   "my-client",
				Issuer:     "https://idp.example/realm",
				HTTPClient: tlsSrv.Client(),
				Now:        func() time.Time { return now },
			})
			if err != nil {
				t.Fatalf("NewIntrospector: %v", err)
			}

			if _, err := intro.Introspect(context.Background(), "opaque-token"); err == nil {
				t.Fatalf("expected error for case %s, got nil", tc.name)
			}
		})
	}
}

func TestExchangeWithOpaqueTokenIntrospectionEndToEnd(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()

	secretFile := filepath.Join(dir, "client.secret")
	if err := os.WriteFile(secretFile, []byte("test-client-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != clientID || p != "test-client-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"active":         true,
			"sub":            "test-sub",
			"iss":            issuer,
			"client_id":      clientID,
			"exp":            now.Add(2 * time.Hour).Unix(),
			"email":          "alice@corp.example",
			"email_verified": "true",
			"groups":         []any{"db-admins"},
		})
	}))
	defer tlsSrv.Close()

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

	cfgContent := fmt.Sprintf(`
listen = 127.0.0.1:8645
identity_policy = %s
issuing_keyset = %s
deployment_audience = %s

[idp "test-idp"]
issuer = %s
client_id = %s
introspection_endpoint = %s
client_secret_file = %s
`, policyPath, keysetPath, audience, issuer, clientID, tlsSrv.URL, secretFile)

	confPath := filepath.Join(dir, "broker.conf")
	if err := os.WriteFile(confPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := authbroker.LoadConfig(confPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	broker, err := authbroker.New(cfg, authbroker.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return now },
		HTTPClient: tlsSrv.Client(),
	})
	if err != nil {
		t.Fatalf("authbroker.New: %v", err)
	}

	handler := broker.Handler()
	rec := httptest.NewRecorder()
	reqBody := `{"idp":"test-idp","access_token":"opaque-token-12345"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/exchange", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/exchange status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Credential string   `json:"credential"`
		Principal  string   `json:"principal"`
		Roles      []string `json:"roles"`
		TokenID    string   `json:"token_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Principal != "alice" {
		t.Errorf("principal = %q, want alice", resp.Principal)
	}
	if len(resp.Roles) == 0 || resp.Roles[0] != "app_admin" {
		t.Errorf("roles = %v, want [app_admin reporting_ro]", resp.Roles)
	}

	// Verify minted credential using the public keyset
	verifier := auth.NewTokenVerifier(pubKS, nil, audience)
	claims, err := verifier.Verify(resp.Credential)
	if err != nil {
		t.Fatalf("verify minted credential: %v", err)
	}
	if claims.Principal != "alice" {
		t.Errorf("verified claims principal = %q, want alice", claims.Principal)
	}
}
