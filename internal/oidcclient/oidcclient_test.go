package oidcclient_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/authbroker"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/oidc/oidctest"
	"github.com/bzync/nextsql/internal/oidcclient"

	"log/slog"
	"io"
)

const (
	clientID = "nextsql-oidc-client"
	audience = "prod-eu"
)

// fakeIdP is an httptest OpenID provider: discovery + authorize + token + jwks.
type fakeIdP struct {
	t      *testing.T
	idp    *oidctest.IdP
	srv    *httptest.Server
	now    time.Time
	mu     sync.Mutex
	codes  map[string]codeState // authorization code -> state
	nextID int
}

type codeState struct {
	nonce     string
	challenge string
	subject   string
	email     string
	groups    []string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	f := &fakeIdP{t: t, idp: oidctest.NewRSA(t, "https://placeholder"), now: time.Now(), codes: map[string]codeState{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 f.srv.URL,
			"authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint":         f.srv.URL + "/token",
			"jwks_uri":               f.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.idp.JWKS())
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != clientID || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad authorize request", http.StatusBadRequest)
			return
		}
		redir := q.Get("redirect_uri")
		f.mu.Lock()
		f.nextID++
		code := "code-" + strconv.Itoa(f.nextID)
		f.codes[code] = codeState{
			nonce: q.Get("nonce"), challenge: q.Get("code_challenge"),
			subject: "sub-alice", email: "alice@corp.example", groups: []string{"db-readers"},
		}
		f.mu.Unlock()
		u, _ := url.Parse(redir)
		rq := u.Query()
		rq.Set("code", code)
		rq.Set("state", q.Get("state"))
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			f.mu.Lock()
			cs, ok := f.codes[r.Form.Get("code")]
			delete(f.codes, r.Form.Get("code"))
			f.mu.Unlock()
			if !ok {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != cs.challenge {
				http.Error(w, `{"error":"invalid_grant","error_description":"pkce mismatch"}`, http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{
				"id_token":      f.signID(cs.subject, cs.email, cs.nonce, cs.groups),
				"access_token":  "at-1",
				"refresh_token": "rt-1",
				"expires_in":    3600,
				"token_type":    "Bearer",
			})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "rt-1" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{
				"id_token":     f.signID("sub-alice", "alice@corp.example", "", []string{"db-readers"}),
				"access_token": "at-2",
				"expires_in":   3600,
				"token_type":   "Bearer",
			})
		default:
			http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
		}
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	f.idp.Issuer = f.srv.URL
	return f
}

func (f *fakeIdP) signID(sub, email, nonce string, groups []string) string {
	c := f.idp.StandardClaims(clientID, sub, nonce, f.now, time.Hour)
	c["email"] = email
	c["email_verified"] = true
	c["groups"] = anySlice(groups)
	f.mu.Lock()
	f.nextID++
	c["jti"] = "jti-" + strconv.Itoa(f.nextID)
	f.mu.Unlock()
	return f.idp.Sign(f.t, c)
}

// startBroker wires a real authbroker in front of the fake IdP and returns its
// URL plus the verify-only keyset for the credentials it mints.
func (f *fakeIdP) startBroker(t *testing.T) (string, *auth.TokenKeyset) {
	t.Helper()
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "idp.nsip")
	if err := auth.WriteIdentityPolicy(policyPath, testPolicy(f.srv.URL)); err != nil {
		t.Fatal(err)
	}
	ksPath := filepath.Join(dir, "issuing.nstk")
	issuerKS, err := auth.CreateTokenKeyset(ksPath)
	if err != nil {
		t.Fatal(err)
	}

	cfg := authbroker.Config{
		Listen:             "127.0.0.1:0",
		IdentityPolicy:     policyPath,
		IssuingKeyset:      ksPath,
		DeploymentAudience: audience,
		CredentialTTL:      time.Hour,
		LogLevel:           "error",
		Profiles: []authbroker.IdPProfile{{
			Name: "corp", Issuer: f.srv.URL, ClientID: clientID, JWKSURI: f.srv.URL + "/jwks",
		}},
	}
	b, err := authbroker.New(cfg, authbroker.Options{
		Fetcher: f.idp.Fetcher(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	bsrv := httptest.NewServer(b.Handler())
	t.Cleanup(bsrv.Close)
	return bsrv.URL, issuerKS.PublicOnly()
}

func testPolicy(issuer string) auth.PolicyDoc {
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
		},
	}
}

func (f *fakeIdP) profile(brokerURL string) oidcclient.IdPProfile {
	return oidcclient.IdPProfile{
		Name: "corp", Issuer: f.srv.URL, ClientID: clientID,
		BrokerURL: brokerURL, Scopes: []string{"openid", "profile", "email", "groups"},
	}
}

// fakeBrowser drives the authorize redirect with the given http client.
func fakeBrowser(hc *http.Client) oidcclient.BrowserOpener {
	return func(u string) error {
		go func() {
			resp, err := hc.Get(u)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestLoginEndToEnd(t *testing.T) {
	f := newFakeIdP(t)
	brokerURL, pubKS := f.startBroker(t)
	hc := f.srv.Client()

	res, ts, err := oidcclient.Login(context.Background(), oidcclient.LoginOptions{
		Profile: f.profile(brokerURL),
		HTTP:    hc,
		Browser: fakeBrowser(hc),
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.Principal != "alice" {
		t.Fatalf("principal = %q, want alice", res.Principal)
	}
	if len(res.Roles) != 1 || res.Roles[0] != "reporting_ro" {
		t.Fatalf("roles = %v, want [reporting_ro]", res.Roles)
	}
	if ts.RefreshToken != "rt-1" {
		t.Fatalf("refresh token = %q", ts.RefreshToken)
	}

	verifier := auth.NewTokenVerifier(pubKS, nil, audience)
	verifier.SetClock(func() time.Time { return f.now })
	claims, err := verifier.Verify(res.Credential)
	if err != nil {
		t.Fatalf("verify minted credential: %v", err)
	}
	if claims.Principal != "alice" || len(claims.Roles) != 1 || claims.Roles[0] != "reporting_ro" {
		t.Fatalf("credential claims = %+v", claims)
	}
	if claims.Audience != audience {
		t.Fatalf("audience = %q", claims.Audience)
	}
}

func TestEnsureFreshSilentRefresh(t *testing.T) {
	f := newFakeIdP(t)
	brokerURL, pubKS := f.startBroker(t)
	hc := f.srv.Client()
	store := &oidcclient.Store{Dir: t.TempDir()}

	// A credential that expired an hour ago but still has a usable refresh token.
	stale := &oidcclient.Credential{
		Version: 1, IdP: "corp", Host: "db.internal:7423",
		Issuer: f.srv.URL, ClientID: clientID, BrokerURL: brokerURL,
		Principal: "alice", Roles: []string{"reporting_ro"},
		Credential: "NSSC1.expired", ExpiresAt: f.now.Add(-time.Hour),
		ObtainedAt: f.now.Add(-2 * time.Hour), RefreshToken: "rt-1",
	}
	if err := store.Save(stale); err != nil {
		t.Fatal(err)
	}

	got, err := oidcclient.EnsureFresh(context.Background(), oidcclient.RenewOptions{
		Profile: f.profile(brokerURL), Store: store, Host: "db.internal:7423",
		HTTP: hc, Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("ensure fresh: %v", err)
	}
	if !got.ExpiresAt.After(f.now) {
		t.Fatalf("renewed credential still expired: %s", got.ExpiresAt)
	}
	if got.Credential == "NSSC1.expired" {
		t.Fatal("credential was not refreshed")
	}
	verifier := auth.NewTokenVerifier(pubKS, nil, audience)
	verifier.SetClock(func() time.Time { return f.now })
	if _, err := verifier.Verify(got.Credential); err != nil {
		t.Fatalf("refreshed credential does not verify: %v", err)
	}
	// It must be persisted.
	reloaded, err := store.Load("corp", "db.internal:7423")
	if err != nil || reloaded.Credential != got.Credential {
		t.Fatalf("refreshed credential not saved: %v", err)
	}
}

func TestEnsureFreshNoRefreshTokenFailsClosed(t *testing.T) {
	f := newFakeIdP(t)
	brokerURL, _ := f.startBroker(t)
	store := &oidcclient.Store{Dir: t.TempDir()}
	if err := store.Save(&oidcclient.Credential{
		Version: 1, IdP: "corp", Host: "h:1", Issuer: f.srv.URL, ClientID: clientID,
		BrokerURL: brokerURL, Principal: "alice", Credential: "NSSC1.x",
		ExpiresAt: f.now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := oidcclient.EnsureFresh(context.Background(), oidcclient.RenewOptions{
		Profile: f.profile(brokerURL), Store: store, Host: "h:1",
		HTTP: f.srv.Client(), Now: func() time.Time { return f.now },
	})
	if !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("err = %v, want unauthorized", err)
	}
}

func TestDiscoverRejectsIssuerMismatch(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 "https://evil.example",
			"authorization_endpoint": "https://x/authorize",
			"token_endpoint":         "https://x/token",
		})
	}))
	defer srv.Close()
	_, err := oidcclient.Discover(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected issuer mismatch to be rejected")
	}
}

func TestNewPKCE(t *testing.T) {
	p, err := oidcclient.NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != p.Challenge {
		t.Fatal("challenge is not S256(verifier)")
	}
	p2, _ := oidcclient.NewPKCE()
	if p2.Verifier == p.Verifier {
		t.Fatal("PKCE verifier is not random")
	}
}

func TestStoreRoundTripAndPerms(t *testing.T) {
	store := &oidcclient.Store{Dir: t.TempDir()}
	c := &oidcclient.Credential{
		Version: 1, IdP: "corp", Host: "127.0.0.1:7423", Issuer: "https://i", ClientID: "c",
		BrokerURL: "https://b", Principal: "alice", Roles: []string{"reporting_ro"},
		Credential: "NSSC1.abc", ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path("corp", "127.0.0.1:7423"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode = %v, want 0600", info.Mode().Perm())
	}
	got, err := store.Load("corp", "127.0.0.1:7423")
	if err != nil || got.Principal != "alice" || got.Credential != "NSSC1.abc" {
		t.Fatalf("round trip failed: %+v %v", got, err)
	}
	if err := store.Delete("corp", "127.0.0.1:7423"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("corp", "127.0.0.1:7423"); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}
}

func TestLoadClientConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
# nextsql client config
[idp.corp]
issuer = "https://corp.okta.com/oauth2/abc"
client_id = "0oaABC"
broker_url = "https://auth.db.internal"
scopes = ["openid", "profile", "groups"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cc, err := oidcclient.LoadClientConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p, ok := cc.Profile("corp")
	if !ok || p.ClientID != "0oaABC" || len(p.Scopes) != 3 || p.BrokerURL != "https://auth.db.internal" {
		t.Fatalf("profile = %+v", p)
	}

	if err := os.WriteFile(path, []byte("[idp.corp]\nbogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := oidcclient.LoadClientConfig(path); err == nil {
		t.Fatal("expected unknown key to be rejected")
	}
}

func TestExchangeAtBrokerPropagatesDenial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"token exchange denied"}`))
	}))
	defer srv.Close()
	_, err := oidcclient.ExchangeAtBroker(context.Background(), srv.Client(), srv.URL, "corp", "tok", "n", "", "")
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
