package admin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func discardLogger() Options {
	return Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// writeFakeNextSQL writes an executable script that answers `lifecycle
// detect --json` (and, for Setup-mode tests, `setup ...`) the way the real
// `nextsql` binary would, without needing a real installation on disk.
func writeFakeNextSQL(t *testing.T, detectStatus string) string {
	t.Helper()
	dir := t.TempDir()
	name := "nextsql"
	body := `#!/bin/sh
if [ "$1" = "lifecycle" ] && [ "$2" = "detect" ]; then
  printf '{"status":"` + detectStatus + `"}'
  exit 0
fi
if [ "$1" = "setup" ]; then
  printf '{"ok":true}'
  exit 0
fi
exit 1
`
	if runtime.GOOS == "windows" {
		t.Skip("fake nextsql script is a POSIX shell script")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigRejectsNonLoopbackListenWithoutTLS(t *testing.T) {
	bin := writeFakeNextSQL(t, "initialized")
	_, err := New(Config{Mode: ModeOperate, Listen: "0.0.0.0:7220", NextSQLBin: bin, ServerAddr: "127.0.0.1:7210", InsecureServer: true}, discardLogger())
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument, got %v", err)
	}
}

func TestConfigRequiresModeOrDetectableBin(t *testing.T) {
	_, err := New(Config{}, discardLogger())
	if err == nil {
		t.Fatal("expected an error when no mode is given and no nextsql binary can be resolved")
	}
}

func TestNewOperateModeExplicit(t *testing.T) {
	srv, err := New(Config{Mode: ModeOperate, Listen: "127.0.0.1:0", ServerAddr: "127.0.0.1:7210", InsecureServer: true}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if srv.Mode() != ModeOperate {
		t.Fatalf("Mode() = %q, want operate", srv.Mode())
	}
	if _, ok := srv.SetupURL(); ok {
		t.Fatal("SetupURL should not be available in Operations mode")
	}
	if srv.Done() != nil {
		t.Fatal("Done() should be nil (never closes) in Operations mode")
	}
}

func TestNewSetupModeExplicit(t *testing.T) {
	bin := writeFakeNextSQL(t, "none")
	srv, err := New(Config{Mode: ModeSetup, NextSQLBin: bin}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if srv.Mode() != ModeSetup {
		t.Fatalf("Mode() = %q, want setup", srv.Mode())
	}
	url, ok := srv.SetupURL()
	if !ok || !strings.Contains(url, "token=") {
		t.Fatalf("SetupURL() = (%q, %v), want a token-bearing URL", url, ok)
	}
	if srv.Done() == nil {
		t.Fatal("Done() should be non-nil in Setup mode")
	}
}

func TestDetectModeMapsStatus(t *testing.T) {
	cases := []struct {
		status string
		want   Mode
	}{
		{"none", ModeSetup},
		{"config-only", ModeSetup},
		{"initialized", ModeOperate},
		{"running", ModeOperate},
	}
	for _, c := range cases {
		bin := writeFakeNextSQL(t, c.status)
		srv, err := New(Config{Listen: "127.0.0.1:0", NextSQLBin: bin, ServerAddr: "127.0.0.1:7210", InsecureServer: true, DataDirHint: t.TempDir()}, discardLogger())
		if err != nil {
			t.Fatalf("status=%q: New: %v", c.status, err)
		}
		if srv.Mode() != c.want {
			t.Errorf("status=%q: Mode() = %q, want %q", c.status, srv.Mode(), c.want)
		}
		_ = srv.Close()
	}
}

func TestModeEndpointUnauthenticated(t *testing.T) {
	srv, err := New(Config{Mode: ModeOperate, Listen: "127.0.0.1:0", ServerAddr: "127.0.0.1:7210", InsecureServer: true}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/mode", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/mode: got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"mode":"operate"`) {
		t.Fatalf("GET /api/v1/mode body = %s", rec.Body.String())
	}
}

func TestShellServedUnauthenticatedInOperateMode(t *testing.T) {
	srv, err := New(Config{Mode: ModeOperate, Listen: "127.0.0.1:0", ServerAddr: "127.0.0.1:7210", InsecureServer: true}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: got %d", rec.Code)
	}
}

func TestShellRequiresTokenInSetupMode(t *testing.T) {
	bin := writeFakeNextSQL(t, "none")
	srv, err := New(Config{Mode: ModeSetup, NextSQLBin: bin}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET / without token: got %d, want 403", rec.Code)
	}

	url, _ := srv.SetupURL()
	path := strings.TrimPrefix(url, "http://"+srv.Addr().String())
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s (with token): got %d", path, rec.Code)
	}
}

func TestAssetsServed(t *testing.T) {
	srv, err := New(Config{Mode: ModeOperate, Listen: "127.0.0.1:0", ServerAddr: "127.0.0.1:7210", InsecureServer: true}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	for _, p := range []string{"/assets/app.js", "/assets/app.css"} {
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d", p, rec.Code)
		}
	}
}

func TestPWAAssetsServed(t *testing.T) {
	srv, err := New(Config{Mode: ModeOperate, Listen: "127.0.0.1:0", ServerAddr: "127.0.0.1:7210", InsecureServer: true}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	cases := []struct {
		path       string
		wantType   string
		wantCache  string
		cacheExact bool // false: substring match ("public, max-age=..."); true: exact match ("no-cache")
	}{
		{"/manifest.webmanifest", "application/manifest+json", "public, max-age=", false},
		{"/sw.js", "application/javascript", "no-cache", true},
		{"/favicon.ico", "image/x-icon", "public, max-age=", false},
		{"/apple-icon.png", "image/png", "public, max-age=", false},
		{"/icons/icon-192.png", "image/png", "public, max-age=", false},
		{"/icons/icon-512.png", "image/png", "public, max-age=", false},
		{"/icons/icon-512-maskable.png", "image/png", "public, max-age=", false},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d", c.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, c.wantType) {
			t.Fatalf("GET %s: Content-Type %q, want to contain %q", c.path, ct, c.wantType)
		}
		if cc := rec.Header().Get("Cache-Control"); (c.cacheExact && cc != c.wantCache) || (!c.cacheExact && !strings.Contains(cc, c.wantCache)) {
			t.Fatalf("GET %s: Cache-Control %q, want %q", c.path, cc, c.wantCache)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("GET %s: empty body", c.path)
		}
	}

	// The service worker must never be scoped under /assets/ — it needs to
	// be reachable at a top-level path so its default scope covers the
	// whole origin, not just /assets/*.
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/sw.js", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("GET /assets/sw.js: got 200, sw.js must only be served from the top-level path")
	}
}

func TestManifestReferencesRegisteredIcons(t *testing.T) {
	srv, err := New(Config{Mode: ModeOperate, Listen: "127.0.0.1:0", ServerAddr: "127.0.0.1:7210", InsecureServer: true}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/manifest.webmanifest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest: got %d", rec.Code)
	}
	var manifest struct {
		Name    string `json:"name"`
		Display string `json:"display"`
		Icons   []struct {
			Src string `json:"src"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if manifest.Name == "" || manifest.Display != "standalone" || len(manifest.Icons) == 0 {
		t.Fatalf("manifest missing expected fields: %+v", manifest)
	}
	for _, icon := range manifest.Icons {
		iconRec := httptest.NewRecorder()
		srv.mux.ServeHTTP(iconRec, httptest.NewRequest("GET", icon.Src, nil))
		if iconRec.Code != http.StatusOK {
			t.Fatalf("manifest icon %s: got %d", icon.Src, iconRec.Code)
		}
	}
}

func TestOpsRoutesDelegatedThroughTopMux(t *testing.T) {
	srv, err := New(Config{Mode: ModeOperate, Listen: "127.0.0.1:0", ServerAddr: "127.0.0.1:7210", InsecureServer: true}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/overview", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/v1/overview (no session): got %d, want 401", rec.Code)
	}
}

func TestSetupRoutesDelegatedThroughTopMux(t *testing.T) {
	bin := writeFakeNextSQL(t, "none")
	srv, err := New(Config{Mode: ModeSetup, NextSQLBin: bin}, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/hello", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /api/v1/hello (no token): got %d, want 403", rec.Code)
	}
}
