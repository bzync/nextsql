package setup

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T, fakeBinBody string) *Server {
	t.Helper()
	bin := writeFakeNextSQL(t, fakeBinBody)
	s, err := New(Config{NextSQLBin: bin, RunTimeout: 5 * time.Second}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newTestHTTPServer(t *testing.T, fakeBinBody string) (*Server, *httptest.Server) {
	t.Helper()
	s := newTestServer(t, fakeBinBody)
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, hs
}

func TestServerRequiresToken(t *testing.T) {
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	base := hs.URL

	resp, err := http.Get(base + "/api/v1/hello")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /api/v1/hello without token: got %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong token also refused.
	req, _ := http.NewRequest("GET", base+"/api/v1/hello", nil)
	req.Header.Set(tokenHeader, "wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET with wrong token: got %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// Correct token via header succeeds.
	req, _ = http.NewRequest("GET", base+"/api/v1/hello", nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET with correct token: got %d, body %s", resp.StatusCode, body)
	}
}

// TestAuthenticateShellRequestSetsTokenCookie exercises the shell-auth helper
// the parent admin package calls before serving the shared shell HTML — it no
// longer owns an HTTP route of its own (the shell lives in internal/admin).
func TestAuthenticateShellRequestSetsTokenCookie(t *testing.T) {
	s := newTestServer(t, `printf '{"ok":true}'`)

	req := httptest.NewRequest("GET", "/?token="+s.Token(), nil)
	rec := httptest.NewRecorder()
	if !s.AuthenticateShellRequest(rec, req) {
		t.Fatal("expected a valid ?token= load to authenticate")
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == tokenCookie && tokenEqual(c.Value, s.Token()) {
			found = true
		}
	}
	if !found {
		t.Error("expected the token cookie to be set after a valid ?token= load")
	}

	// No token at all: refused.
	req = httptest.NewRequest("GET", "/", nil)
	rec = httptest.NewRecorder()
	if s.AuthenticateShellRequest(rec, req) {
		t.Error("expected a request with no token to be refused")
	}
}

func TestServerSecurityHeaders(t *testing.T) {
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/hello", nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing security header %s", h)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "font-src 'self' data:") {
		t.Errorf("CSP does not allow bundled data: fonts: %q", csp)
	}
}

func TestServerHello(t *testing.T) {
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/hello", nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		NextSQLVersion string `json:"nextsql_version"`
		Defaults       struct {
			DataDir string `json:"dataDir"`
		} `json:"defaults"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.NextSQLVersion == "" {
		t.Error("expected a non-empty nextsql_version")
	}
	if body.Defaults.DataDir == "" {
		t.Error("expected a non-empty default data dir")
	}
}

func TestServerLifecycleDetect(t *testing.T) {
	s, hs := newTestHTTPServer(t, `printf '{"status":"healthy","summary":"ready","config_present":true,"data_file_present":true,"keystore_present":true,"headers_compatible":true}'`)
	req, _ := http.NewRequest("POST", hs.URL+"/api/v1/lifecycle/detect", bytes.NewBufferString(`{"dataDir":"/data","config":"/etc/nextsql.conf"}`))
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("detect: got %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		OK     bool `json:"ok"`
		Result struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Result.Status != "healthy" || got.Result.Summary != "ready" {
		t.Fatalf("unexpected lifecycle detect result: %+v", got)
	}
}

func TestServerPlanAndInstall(t *testing.T) {
	s, hs := newTestHTTPServer(t, `
dry=0
for a in "$@"; do [ "$a" = "--dry-run" ] && dry=1; done
printf '{"ok":true,"dry_run":%s}' "$( [ $dry = 1 ] && echo true || echo false )"
`)
	body := bytes.NewBufferString(`{"dataDir":"/d","keyFile":"/k","preset":"balanced"}`)
	req, _ := http.NewRequest("POST", hs.URL+"/api/v1/plan", body)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var plan runResult
	if err := json.NewDecoder(resp.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if !plan.OK || !bytes.Contains(plan.Result, []byte(`"dry_run":true`)) {
		t.Fatalf("plan should be a dry run: %+v", plan)
	}

	body = bytes.NewBufferString(`{"dataDir":"/d","keyFile":"/k","preset":"balanced"}`)
	req, _ = http.NewRequest("POST", hs.URL+"/api/v1/install", body)
	req.Header.Set(tokenHeader, s.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var install runResult
	if err := json.NewDecoder(resp.Body).Decode(&install); err != nil {
		t.Fatal(err)
	}
	if !install.OK || !bytes.Contains(install.Result, []byte(`"dry_run":false`)) {
		t.Fatalf("install should not be a dry run: %+v", install)
	}
}

func TestServerServiceEndpoint(t *testing.T) {
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/service", nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/service: got %d, body %s", resp.StatusCode, body)
	}
	var st ServiceStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	// Read-only endpoint requires a request token like every other API
	// route (already exercised by TestServerRequiresToken's pattern); the
	// only thing worth asserting on the body here is that decoding
	// succeeded into the real shape — the actual field values are
	// host-dependent (DetectService's own tests cover the logic).
}

// TestServerInstallSkipsServiceWithoutUnit exercises the full
// EnableService:true path through the real HTTP API. It never mutates real
// systemd state: on any host with no "nextsql" unit registered (true for a
// test sandbox), DetectService correctly reports UnitFound=false and
// maybeEnableService refuses with a clear, non-fatal reason — this is the
// expected outcome for the vast majority of environments and must never
// turn a successful database install into a reported failure.
func TestServerInstallSkipsServiceWithoutUnit(t *testing.T) {
	s, hs := newTestHTTPServer(t, `
printf '{"ok":true,"dry_run":false,"initialized":true,"config_path":"/d/nextsql.conf","health":{"ok":true}}'
`)
	body := bytes.NewBufferString(`{"dataDir":"/d","keyFile":"/k","preset":"balanced","enableService":true}`)
	req, _ := http.NewRequest("POST", hs.URL+"/api/v1/install", body)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var install runResult
	if err := json.NewDecoder(resp.Body).Decode(&install); err != nil {
		t.Fatal(err)
	}
	if !install.OK {
		t.Fatalf("the database install itself must still succeed regardless of the service outcome: %+v", install)
	}
	if install.Service == nil {
		t.Fatal("expected a non-nil service outcome when enableService was requested")
	}
	if install.Service.Enabled {
		t.Errorf("did not expect the service to be enabled with no unit installed on this host: %+v", install.Service)
	}
	if install.Service.Error == "" {
		t.Error("expected a non-empty explanation for why the service was not enabled")
	}
}

// TestServerInstallOmitsServiceWhenNotRequested confirms the field is
// entirely absent (not just false/empty) when the operator never checked
// "start at boot" — no systemctl subprocess should even run.
func TestServerInstallOmitsServiceWhenNotRequested(t *testing.T) {
	s, hs := newTestHTTPServer(t, `
printf '{"ok":true,"dry_run":false,"initialized":true,"config_path":"/d/nextsql.conf","health":{"ok":true}}'
`)
	body := bytes.NewBufferString(`{"dataDir":"/d","keyFile":"/k","preset":"balanced"}`)
	req, _ := http.NewRequest("POST", hs.URL+"/api/v1/install", body)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if bytes.Contains(raw, []byte(`"service"`)) {
		t.Errorf("expected no service field when enableService was not requested, got: %s", raw)
	}
}

func TestServerRejectsInvalidParams(t *testing.T) {
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	body := bytes.NewBufferString(`{"dataDir":"","keyFile":""}`)
	req, _ := http.NewRequest("POST", hs.URL+"/api/v1/plan", body)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res runResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Error == "" {
		t.Fatalf("expected a validation error, got %+v", res)
	}
}

func TestServerFinishClosesDone(t *testing.T) {
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	select {
	case <-s.Done():
		t.Fatal("Done() closed before /api/v1/finish was called")
	default:
	}

	req, _ := http.NewRequest("POST", hs.URL+"/api/v1/finish", nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("finish: got %d", resp.StatusCode)
	}

	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() was not closed after /api/v1/finish")
	}

	// A second call must not panic (double-close protection).
	req, _ = http.NewRequest("POST", hs.URL+"/api/v1/finish", nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestServerURLQueryIncludesToken(t *testing.T) {
	s := newTestServer(t, `printf '{"ok":true}'`)
	if !strings.Contains(s.URLQuery(), s.Token()) {
		t.Errorf("URLQuery() = %q, expected it to contain the token", s.URLQuery())
	}
}

// TestTokenCookieIsHttpOnlyAndAuthenticatesAPI pins the two halves of the
// installer token's cookie contract. The token is the single credential that
// authorises a first install on this machine, so the cookie carrying it must
// not be script-readable; and because the bundled JS therefore cannot echo it
// into X-Installer-Token, the cookie alone has to authenticate an /api/v1
// call. Losing either half silently breaks the wizard or widens what an
// injected script can steal.
func TestTokenCookieIsHttpOnlyAndAuthenticatesAPI(t *testing.T) {
	s := newTestServer(t, `printf '{"ok":true}'`)

	req := httptest.NewRequest("GET", "/?token="+s.Token(), nil)
	rec := httptest.NewRecorder()
	if !s.AuthenticateShellRequest(rec, req) {
		t.Fatal("expected a valid ?token= load to authenticate")
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == tokenCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no token cookie was set")
	}
	if !cookie.HttpOnly {
		t.Error("token cookie is script-readable; an injected script could read the install token")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("token cookie SameSite = %v, want Strict", cookie.SameSite)
	}

	// The cookie by itself must authorise the API, with no token header.
	apiReq := httptest.NewRequest("GET", "/api/v1/hello", nil)
	apiReq.AddCookie(cookie)
	apiRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("cookie-only /api/v1/hello = %d, want 200 (body %q)", apiRec.Code, apiRec.Body.String())
	}
}
