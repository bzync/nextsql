package integration

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/security"
)

type slcHarness struct {
	addr      string
	issuer    *auth.TokenKeyset
	acl       *security.ACL
	rev       *auth.TokenRevocations
	auditPath string
}

func startSLCServer(t *testing.T, audience string) slcHarness {
	return startSLCServerWithIdentityHints(t, audience, nil)
}

func startSLCServerWithIdentityHints(t *testing.T, audience string, hints map[uint32]string) slcHarness {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")
	if _, err := crypto.CreateKeyFile(keyPath, 1); err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.LoadProvider(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Create(filepath.Join(dir, "nextsql.db"), keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	users, err := auth.Create(filepath.Join(dir, "nextsql.users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("app", "s3cret"); err != nil {
		t.Fatal(err)
	}

	acl, err := security.CreateACL(filepath.Join(dir, "nextsql.acl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivSelect, security.ScopeTable, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivCreate, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivInsert, security.ScopeTable, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.CreateRole("readonly"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("readonly", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("readonly", security.PrivSelect, security.ScopeTable, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.GrantRole("readonly", "app"); err != nil {
		t.Fatal(err)
	}

	issuer, err := auth.CreateTokenKeyset(filepath.Join(dir, "keyset"))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := auth.CreateTokenRevocations(filepath.Join(dir, "rev"))
	if err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(dir, "nextsql.audit")
	audit, err := security.OpenAudit(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	srv := protocol.NewServer(db, users)
	srv.ACL = acl
	srv.Audit = audit
	srv.Registry = security.NewRegistry()
	srv.Tokens = auth.NewTokenVerifier(issuer.PublicOnly(), rev, audience)
	srv.TokenIdentitySourceHints = hints

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = srv.Close() })
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe(ctx, "127.0.0.1:0") }()
	deadline := time.Now().Add(2 * time.Second)
	for srv.Addr() == nil && time.Now().Before(deadline) {
		select {
		case err := <-serveErr:
			t.Fatalf("server failed to start: %v", err)
		case <-time.After(5 * time.Millisecond):
		}
	}
	if srv.Addr() == nil {
		t.Fatal("server did not start")
	}
	addr := srv.Addr().String()
	seed, err := nextsql.Open(nextsql.Config{Address: addr, User: "app", Password: "s3cret", InsecureNoTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(context.Background(), `CREATE TABLE demo (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()
	return slcHarness{addr: addr, issuer: issuer, acl: acl, rev: rev, auditPath: auditPath}
}

func slcConn(t *testing.T, addr, user, secret string) (*nextsql.Conn, error) {
	return nextsql.Open(nextsql.Config{
		Address:       addr,
		User:          user,
		Password:      secret,
		InsecureNoTLS: true,
	})
}

func TestShortLivedCredentialAuth(t *testing.T) {
	h := startSLCServer(t, "prod")
	tok, _, _, err := h.issuer.Mint(auth.TokenMintRequest{Principal: "app", Audience: "prod", TTL: 10 * time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := slcConn(t, h.addr, "app", tok)
	if err != nil {
		t.Fatalf("token auth rejected: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec(context.Background(), `CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
}

func TestShortLivedCredentialOIDCAuditSourceIsKeyDerivedAndSecretFree(t *testing.T) {
	h := startSLCServerWithIdentityHints(t, "prod", map[uint32]string{1: "oidc"})
	tok, id, _, err := h.issuer.Mint(auth.TokenMintRequest{Principal: "app", Audience: "prod", TTL: 10 * time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := slcConn(t, h.addr, "app", tok)
	if err != nil {
		t.Fatalf("OIDC-key credential rejected: %v", err)
	}
	_ = conn.Close()

	var body []byte
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		body, err = os.ReadFile(h.auditPath)
		if err == nil && strings.Contains(string(body), `"identity_source":"oidc"`) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `"identity_source":"oidc"`) {
		t.Fatalf("OIDC identity source missing: %s", text)
	}
	if strings.Contains(text, tok) || strings.Contains(text, hex.EncodeToString(id[:])) {
		t.Fatalf("credential or token id leaked to server audit: %s", text)
	}
}

func TestForgedOIDCKeyIDCannotUpgradeAuditSource(t *testing.T) {
	h := startSLCServerWithIdentityHints(t, "prod", map[uint32]string{1: "oidc"})
	other, err := auth.CreateTokenKeyset(filepath.Join(t.TempDir(), "other.nstk"))
	if err != nil {
		t.Fatal(err)
	}
	// Both keysets begin at id 1. The presented id is hinted as OIDC, but the
	// credential is signed by a different key and therefore must remain a
	// generic token failure in the server audit.
	forged, _, _, err := other.Mint(auth.TokenMintRequest{Principal: "app", Audience: "prod", TTL: time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := slcConn(t, h.addr, "app", forged); err == nil {
		t.Fatal("credential signed by an untrusted key was accepted")
	}

	var body []byte
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		body, err = os.ReadFile(h.auditPath)
		text := string(body)
		if err == nil && strings.Contains(text, `"action":"auth.failure"`) && strings.Contains(text, `"identity_source":"token"`) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `"action":"auth.failure"`) || !strings.Contains(text, `"identity_source":"token"`) {
		t.Fatalf("generic token failure audit missing: %s", text)
	}
	if strings.Contains(text, `"identity_source":"oidc"`) || strings.Contains(text, forged) {
		t.Fatalf("forged credential upgraded its audit source or leaked: %s", text)
	}
}

func TestShortLivedCredentialWrongPrincipal(t *testing.T) {
	h := startSLCServer(t, "")
	tok, _, _, _ := h.issuer.Mint(auth.TokenMintRequest{Principal: "someone-else", TTL: time.Minute}, time.Now())
	if _, err := slcConn(t, h.addr, "app", tok); err == nil {
		t.Fatal("credential for a different principal was accepted")
	}
}

func TestShortLivedCredentialAudienceMismatch(t *testing.T) {
	h := startSLCServer(t, "prod")
	tok, _, _, _ := h.issuer.Mint(auth.TokenMintRequest{Principal: "app", Audience: "staging", TTL: time.Minute}, time.Now())
	if _, err := slcConn(t, h.addr, "app", tok); err == nil {
		t.Fatal("credential with the wrong audience was accepted")
	}
}

func TestShortLivedCredentialRevoked(t *testing.T) {
	h := startSLCServer(t, "")
	tok, id, exp, _ := h.issuer.Mint(auth.TokenMintRequest{Principal: "app", TTL: 10 * time.Minute}, time.Now())
	c1, err := slcConn(t, h.addr, "app", tok)
	if err != nil {
		t.Fatalf("pre-revocation auth failed: %v", err)
	}
	_ = c1.Close()
	// The verifier holds the same in-memory revocation set, so the new
	// entry takes effect immediately.
	if err := h.rev.Revoke(id, exp); err != nil {
		t.Fatal(err)
	}
	if _, err := slcConn(t, h.addr, "app", tok); err == nil {
		t.Fatal("revoked credential still authenticates")
	}
}

func TestShortLivedCredentialRoleScope(t *testing.T) {
	h := startSLCServer(t, "")
	tok, _, _, err := h.issuer.Mint(auth.TokenMintRequest{Principal: "app", Roles: []string{"readonly"}, TTL: 10 * time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := slcConn(t, h.addr, "app", tok)
	if err != nil {
		t.Fatalf("role-scoped credential rejected: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec(context.Background(), `SELECT id FROM demo`); err != nil {
		t.Fatalf("scoped SELECT denied: %v", err)
	}
	_, err = conn.Exec(context.Background(), `CREATE TABLE blocked (id STRING PRIMARY KEY)`)
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("role scope did not block CREATE: %v", err)
	}
}

func TestShortLivedCredentialExpiryClosesSession(t *testing.T) {
	h := startSLCServer(t, "")
	tok, _, _, err := h.issuer.Mint(auth.TokenMintRequest{Principal: "app", TTL: 2 * time.Second}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := slcConn(t, h.addr, "app", tok)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(4 * time.Second)
	if _, err := conn.Exec(context.Background(), `SELECT id FROM demo`); err == nil {
		t.Fatal("session survived credential expiry")
	}
}
