package ops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/admin/profile"
	"github.com/bzync/nextsql/internal/nerr"
)

// memStore is an in-memory credential.Store.
type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Get(k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (s *memStore) Set(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}

func (s *memStore) Delete(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}

func (s *memStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// fakeDialer records every sign-in and accepts only the passwords in ok,
// keyed by "address/user".
type fakeDialer struct {
	mu    sync.Mutex
	calls []nextsql.Config
	ok    map[string]string
}

func (d *fakeDialer) open(_ context.Context, cfg nextsql.Config) (*nextsql.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, cfg)
	if pw, ok := d.ok[cfg.Address+"/"+cfg.User]; ok && pw == cfg.Password {
		return nil, nil
	}
	return nil, nerr.New(nerr.Unauthorized, "fake", "authentication failed")
}

func (d *fakeDialer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

func (d *fakeDialer) last() nextsql.Config {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[len(d.calls)-1]
}

func profileServer(t *testing.T) (*Server, *fakeDialer, *memStore) {
	t.Helper()
	store := &memStore{m: map[string]string{}}
	s, err := New(Config{
		ServerAddr:        "127.0.0.1:7210",
		InsecureServer:    true,
		ServerName:        "Local",
		ServerEnvironment: "development",
		Profiles: []profile.Profile{{
			ID: "staging", Name: "Staging", Environment: "staging",
			Address: "127.0.0.1:7311", Insecure: true, User: "stage_op", Database: "stagedb",
		}},
		CredentialStore: store,
	}, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	d := &fakeDialer{ok: map[string]string{
		"127.0.0.1:7210/alice":    "alice-pw",
		"127.0.0.1:7210/mallory":  "mallory-pw",
		"127.0.0.1:7311/stage_op": "stage-pw",
	}}
	s.open = d.open
	return s, d, store
}

type apiResult struct {
	code   int
	body   map[string]any
	cookie string
}

func call(t *testing.T, s *Server, method, path, cookie, csrf, body string) apiResult {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	if csrf != "" {
		req.Header.Set(csrfHeader, csrf)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	out := apiResult{code: rec.Code}
	_ = json.Unmarshal(rec.Body.Bytes(), &out.body)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			out.cookie = c.Value
		}
	}
	return out
}

func login(t *testing.T, s *Server, user, password, prof string) (cookie, csrf string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"user": user, "password": password, "profile": prof})
	r := call(t, s, "POST", "/api/v1/session", "", "", string(body))
	if r.code != http.StatusOK || r.cookie == "" {
		t.Fatalf("login %s@%s: %d %v", user, prof, r.code, r.body)
	}
	return r.cookie, r.body["csrf_token"].(string)
}

func profileID(t *testing.T, r apiResult) string {
	t.Helper()
	p, ok := r.body["profile"].(map[string]any)
	if !ok {
		t.Fatalf("response has no profile: %v", r.body)
	}
	return p["id"].(string)
}

func TestProfilesListIsPreAuthAndNamesNoTarget(t *testing.T) {
	s, _, _ := profileServer(t)
	req := httptest.NewRequest("GET", "/api/v1/profiles", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-auth profiles: %d", rec.Code)
	}
	raw := rec.Body.String()
	for _, want := range []string{`"default":"default"`, `"id":"staging"`, `"name":"Local"`, `"environment":"staging"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("profiles list missing %s: %s", want, raw)
		}
	}
	for _, leak := range []string{"127.0.0.1", "7311", "stage_op", "stagedb"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("pre-auth profiles list leaks %q: %s", leak, raw)
		}
	}

	// The authenticated list shows where each profile points.
	cookie, _ := login(t, s, "alice", "alice-pw", "")
	r := call(t, s, "GET", "/api/v1/session/profiles", cookie, "", "")
	if r.code != http.StatusOK || r.body["current"] != "default" {
		t.Fatalf("session profiles: %d %v", r.code, r.body)
	}
	if !strings.Contains(mustJSON(t, r.body), `"address":"127.0.0.1:7311"`) {
		t.Fatalf("session profiles should carry addresses: %v", r.body)
	}
	if call(t, s, "GET", "/api/v1/session/profiles", "", "", "").code != http.StatusUnauthorized {
		t.Fatal("session profiles served without a session")
	}
}

func TestLoginTargetsTheChosenProfile(t *testing.T) {
	s, d, _ := profileServer(t)
	if r := call(t, s, "POST", "/api/v1/session", "", "", `{"user":"x","password":"y","profile":"nope"}`); r.code != http.StatusBadRequest {
		t.Fatalf("unknown profile: want 400, got %d", r.code)
	}
	if d.count() != 0 {
		t.Fatal("an unknown profile was dialed")
	}

	cookie, _ := login(t, s, "stage_op", "stage-pw", "staging")
	got := d.last()
	if got.Address != "127.0.0.1:7311" || got.Database != "stagedb" || !got.InsecureNoTLS {
		t.Fatalf("staging sign-in dialed %+v", got)
	}
	r := call(t, s, "GET", "/api/v1/session", cookie, "", "")
	if profileID(t, r) != "staging" || r.body["database"] != "stagedb" {
		t.Fatalf("whoami after staging sign-in: %v", r.body)
	}
	c := call(t, s, "GET", "/api/v1/connection", cookie, "", "")
	if c.body["server_addr"] != "127.0.0.1:7311" {
		t.Fatalf("connection probe names %v, want the staging address", c.body["server_addr"])
	}
}

func TestSwitchRequiresSessionAndCSRF(t *testing.T) {
	s, d, _ := profileServer(t)
	body := `{"profile":"staging","user":"stage_op","password":"stage-pw"}`
	if r := call(t, s, "POST", "/api/v1/session/switch", "", "", body); r.code != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d", r.code)
	}
	cookie, _ := login(t, s, "alice", "alice-pw", "")
	if r := call(t, s, "POST", "/api/v1/session/switch", cookie, "", body); r.code != http.StatusForbidden {
		t.Fatalf("no CSRF: want 403, got %d", r.code)
	}
	if d.count() != 1 {
		t.Fatalf("a refused switch dialed nextsqld (%d calls)", d.count())
	}
}

func TestSwitchIssuesANewSessionAndRetiresTheOld(t *testing.T) {
	s, d, _ := profileServer(t)
	oldCookie, oldCSRF := login(t, s, "alice", "alice-pw", "")

	r := call(t, s, "POST", "/api/v1/session/switch", oldCookie, oldCSRF,
		`{"profile":"staging","user":"stage_op","password":"stage-pw"}`)
	if r.code != http.StatusOK {
		t.Fatalf("switch: %d %v", r.code, r.body)
	}
	if r.cookie == "" || r.cookie == oldCookie {
		t.Fatal("switch did not issue a new session cookie")
	}
	if r.body["csrf_token"] == oldCSRF || r.body["csrf_token"] == "" {
		t.Fatal("switch did not rotate the CSRF token")
	}
	if profileID(t, r) != "staging" || r.body["user"] != "stage_op" {
		t.Fatalf("switch response: %v", r.body)
	}
	if got := d.last(); got.Address != "127.0.0.1:7311" || got.User != "stage_op" {
		t.Fatalf("switch dialed %+v", got)
	}
	if call(t, s, "GET", "/api/v1/session", oldCookie, "", "").code != http.StatusUnauthorized {
		t.Fatal("the old session survived a switch")
	}
	if w := call(t, s, "GET", "/api/v1/session", r.cookie, "", ""); profileID(t, w) != "staging" {
		t.Fatalf("new session whoami: %v", w.body)
	}
	if s.sessions.len() != 1 {
		t.Fatalf("%d sessions live after a switch, want 1", s.sessions.len())
	}
}

func TestFailedSwitchKeepsTheCurrentSession(t *testing.T) {
	s, _, _ := profileServer(t)
	cookie, csrf := login(t, s, "alice", "alice-pw", "")
	r := call(t, s, "POST", "/api/v1/session/switch", cookie, csrf,
		`{"profile":"staging","user":"stage_op","password":"wrong"}`)
	if r.code != http.StatusUnauthorized || r.cookie != "" {
		t.Fatalf("bad password: want 401 and no new cookie, got %d cookie=%q", r.code, r.cookie)
	}
	if w := call(t, s, "GET", "/api/v1/session", cookie, "", ""); w.code != http.StatusOK || profileID(t, w) != "default" {
		t.Fatalf("session after a failed switch: %d %v", w.code, w.body)
	}

	for name, body := range map[string]string{
		"unknown profile":         `{"profile":"nope","user":"u","password":"p"}`,
		"no user":                 `{"profile":"staging","password":"p"}`,
		"no password":             `{"profile":"staging","user":"u"}`,
		"password and saved":      `{"profile":"staging","user":"u","password":"p","use_saved_password":true}`,
		"save without a password": `{"profile":"staging","user":"u","use_saved_password":true,"save_password":true}`,
		"control char user":       `{"profile":"staging","user":"a\nb","password":"p"}`,
	} {
		if r := call(t, s, "POST", "/api/v1/session/switch", cookie, csrf, body); r.code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %v", name, r.code, r.body)
		}
	}
}

func TestSwitchRefusedWhileAStudioQueryRuns(t *testing.T) {
	s, d, _ := profileServer(t)
	cookie, csrf := login(t, s, "alice", "alice-pw", "")
	sess := s.sessions.get(cookie)
	if err := sess.beginStudioQuery("q-1", func() {}); err != nil {
		t.Fatal(err)
	}
	calls := d.count()
	r := call(t, s, "POST", "/api/v1/session/switch", cookie, csrf,
		`{"profile":"staging","user":"stage_op","password":"stage-pw"}`)
	if r.code != http.StatusConflict {
		t.Fatalf("switch during a query: want 409, got %d", r.code)
	}
	if d.count() != calls {
		t.Fatal("a refused switch dialed nextsqld")
	}
	sess.finishStudioQuery("q-1")
}

// A saved password is a delegation from the principal that saved it. Another
// operator signed in to the same Admin must not be able to spend it.
func TestSavedPasswordIsBoundToThePrincipalThatSavedIt(t *testing.T) {
	s, d, store := profileServer(t)

	// alice switches to staging, saving the password.
	cookie, csrf := login(t, s, "alice", "alice-pw", "")
	r := call(t, s, "POST", "/api/v1/session/switch", cookie, csrf,
		`{"profile":"staging","user":"stage_op","password":"stage-pw","save_password":true}`)
	if r.code != http.StatusOK || r.body["credential_saved"] != true {
		t.Fatalf("switch with save: %d %v", r.code, r.body)
	}
	if store.len() != 1 {
		t.Fatalf("store holds %d entries, want 1", store.len())
	}

	// mallory, signed in to the same server, cannot use it.
	mCookie, mCSRF := login(t, s, "mallory", "mallory-pw", "")
	calls := d.count()
	r = call(t, s, "POST", "/api/v1/session/switch", mCookie, mCSRF,
		`{"profile":"staging","user":"stage_op","use_saved_password":true}`)
	if r.code != http.StatusUnauthorized {
		t.Fatalf("another principal spent a saved password: %d %v", r.code, r.body)
	}
	if d.count() != calls {
		t.Fatal("a foreign principal's saved-password attempt reached nextsqld")
	}

	// alice, signed in again, can.
	cookie, csrf = login(t, s, "alice", "alice-pw", "")
	r = call(t, s, "POST", "/api/v1/session/switch", cookie, csrf,
		`{"profile":"staging","user":"stage_op","use_saved_password":true}`)
	if r.code != http.StatusOK || profileID(t, r) != "staging" {
		t.Fatalf("saving principal could not use its saved password: %d %v", r.code, r.body)
	}
	if got := d.last(); got.Password != "stage-pw" {
		t.Fatal("the saved password was not the one sent")
	}
}

func TestRejectedSavedPasswordIsForgotten(t *testing.T) {
	s, d, store := profileServer(t)
	cookie, csrf := login(t, s, "alice", "alice-pw", "")
	if r := call(t, s, "POST", "/api/v1/session/switch", cookie, csrf,
		`{"profile":"staging","user":"stage_op","password":"stage-pw","save_password":true}`); r.code != http.StatusOK {
		t.Fatalf("switch with save: %d", r.code)
	}

	// The staging password changes server-side.
	d.mu.Lock()
	d.ok["127.0.0.1:7311/stage_op"] = "rotated"
	d.mu.Unlock()

	cookie, csrf = login(t, s, "alice", "alice-pw", "")
	r := call(t, s, "POST", "/api/v1/session/switch", cookie, csrf,
		`{"profile":"staging","user":"stage_op","use_saved_password":true}`)
	if r.code != http.StatusUnauthorized || !strings.Contains(r.body["error"].(string), "forgotten") {
		t.Fatalf("stale saved password: %d %v", r.code, r.body)
	}
	if store.len() != 0 {
		t.Fatal("a rejected saved password was kept")
	}
}

func TestForgetCredentialOnlyReachesTheCallersOwnDelegation(t *testing.T) {
	s, _, store := profileServer(t)
	cookie, csrf := login(t, s, "alice", "alice-pw", "")
	if r := call(t, s, "POST", "/api/v1/session/switch", cookie, csrf,
		`{"profile":"staging","user":"stage_op","password":"stage-pw","save_password":true}`); r.code != http.StatusOK {
		t.Fatalf("switch with save: %d", r.code)
	}

	mCookie, mCSRF := login(t, s, "mallory", "mallory-pw", "")
	if r := call(t, s, "POST", "/api/v1/session/credential/forget", mCookie, mCSRF,
		`{"profile":"staging","user":"stage_op"}`); r.code != http.StatusNoContent {
		t.Fatalf("forget: want 204, got %d", r.code)
	}
	if store.len() != 1 {
		t.Fatal("one principal deleted another's saved password")
	}

	cookie, csrf = login(t, s, "alice", "alice-pw", "")
	if r := call(t, s, "POST", "/api/v1/session/credential/forget", cookie, "", `{"profile":"staging","user":"stage_op"}`); r.code != http.StatusForbidden {
		t.Fatalf("forget without CSRF: want 403, got %d", r.code)
	}
	if r := call(t, s, "POST", "/api/v1/session/credential/forget", cookie, csrf,
		`{"profile":"staging","user":"stage_op"}`); r.code != http.StatusNoContent {
		t.Fatalf("forget own: want 204, got %d", r.code)
	}
	if store.len() != 0 {
		t.Fatal("the saving principal could not forget its own saved password")
	}
}

func TestConfigRejectsBadProfiles(t *testing.T) {
	base := Config{ServerAddr: "127.0.0.1:7210", InsecureServer: true}
	cases := map[string]Config{
		"reserved id": withProfiles(base, profile.Profile{ID: "default", Address: "127.0.0.1:1", Insecure: true}),
		"duplicate id": withProfiles(base,
			profile.Profile{ID: "a", Address: "127.0.0.1:1", Insecure: true},
			profile.Profile{ID: "a", Address: "127.0.0.1:2", Insecure: true}),
		"remote plaintext":        withProfiles(base, profile.Profile{ID: "a", Address: "db.example:7210", Insecure: true}),
		"unreadable CA":           withProfiles(base, profile.Profile{ID: "a", Address: "db.example:7210", TLSCA: "/nonexistent/ca.pem"}),
		"bad server environment":  {ServerAddr: "127.0.0.1:7210", InsecureServer: true, ServerEnvironment: "prod"},
		"default address no port": {ServerAddr: "127.0.0.1", InsecureServer: true},
	}
	for name, cfg := range cases {
		if s, err := New(cfg, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}); err == nil {
			_ = s.Close()
			t.Errorf("%s: New accepted the config", name)
		}
	}
}

func withProfiles(c Config, ps ...profile.Profile) Config {
	c.Profiles = ps
	return c
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
