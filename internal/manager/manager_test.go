package manager

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{
		Listen:         "127.0.0.1:0",
		ServerAddr:     "127.0.0.1:7210",
		InsecureServer: true,
	}, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConfigRejectsNonLoopbackWithoutTLS(t *testing.T) {
	_, err := New(Config{Listen: "0.0.0.0:7220", ServerAddr: "127.0.0.1:7210", InsecureServer: true},
		Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument, got %v", err)
	}
}

func TestConfigRejectsInsecureRemoteServer(t *testing.T) {
	_, err := New(Config{Listen: "127.0.0.1:0", ServerAddr: "db.example:7210", InsecureServer: true},
		Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument for --insecure remote, got %v", err)
	}
}

func TestConfigRequiresCAOrInsecure(t *testing.T) {
	_, err := New(Config{Listen: "127.0.0.1:0", ServerAddr: "127.0.0.1:7210"},
		Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument without --tls-ca/--insecure, got %v", err)
	}
}

func TestHealthzNoAuth(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("healthz: %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("missing CSP header")
	}
}

func TestShellAndAssetsServed(t *testing.T) {
	s := testServer(t)
	h := s.withBaseMiddleware(s.mux)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "NextSQL Manager") {
		t.Fatalf("shell: %d", rec.Code)
	}

	for _, p := range []string{"/assets/app.js", "/assets/app.css"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 {
			t.Fatalf("asset %s: %d", p, rec.Code)
		}
	}
}

func TestReadModelsRequireSession(t *testing.T) {
	s := testServer(t)
	for _, p := range []string{"/api/v1/overview", "/api/v1/databases", "/api/v1/activity", "/api/v1/security", "/api/v1/cluster", "/api/v1/maintenance", "/api/v1/config", "/api/v1/diagnostics", "/api/v1/diagnostics/bundle", "/api/v1/backups"} {
		rec := httptest.NewRecorder()
		s.withBaseMiddleware(s.mux).ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: want 401, got %d", p, rec.Code)
		}
	}
}

func TestLogoutRequiresCSRF(t *testing.T) {
	s := testServer(t)
	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// DELETE with the cookie but no CSRF header → 403.
	req := httptest.NewRequest("DELETE", "/api/v1/session", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	rec := httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 without CSRF, got %d", rec.Code)
	}

	// With the CSRF header → 204 and the session is gone.
	req = httptest.NewRequest("DELETE", "/api/v1/session", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	req.Header.Set(csrfHeader, sess.csrf)
	rec = httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204 with CSRF, got %d", rec.Code)
	}
	if s.sessions.get(sess.id) != nil {
		t.Fatal("session survived logout")
	}
}

func TestWhoamiWorksWithCookieOnly(t *testing.T) {
	s := testServer(t)
	sess, _ := s.sessions.create(nil, "op", "maindb", "acme")

	req := httptest.NewRequest("GET", "/api/v1/session", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	rec := httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("whoami: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"user":"op"`) || !strings.Contains(body, `"database":"maindb"`) ||
		!strings.Contains(body, sess.csrf) {
		t.Fatalf("whoami body: %s", body)
	}
}

func TestSessionStoreBounded(t *testing.T) {
	st := newSessionStore(2, time.Minute, time.Hour)
	defer st.close()
	if _, err := st.create(nil, "a", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.create(nil, "b", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.create(nil, "c", "", ""); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("want exhausted on the 3rd session, got %v", err)
	}
}

func TestSessionStoreExpiry(t *testing.T) {
	st := newSessionStore(4, time.Millisecond, time.Hour)
	defer st.close()
	sess, _ := st.create(nil, "a", "", "")
	time.Sleep(3 * time.Millisecond)
	if st.get(sess.id) != nil {
		t.Fatal("expired session still returned")
	}
	if st.len() != 0 {
		t.Fatalf("expired session not evicted: len=%d", st.len())
	}
}

func TestClusterActionSQL(t *testing.T) {
	cases := []struct {
		action    string
		timeoutMS int64
		want      string
		wantErr   bool
	}{
		{action: "transfer_leader", want: "CLUSTER TRANSFER LEADER"},
		{action: "drain", want: "CLUSTER DRAIN"},
		{action: "drain", timeoutMS: 5000, want: "CLUSTER DRAIN WITH (TIMEOUT_MS = 5000)"},
		{action: "drain", timeoutMS: -1, wantErr: true},
		{action: "drain", timeoutMS: maxDrainTimeoutMS + 1, wantErr: true},
		{action: "maintenance_enable", want: "CLUSTER MAINTENANCE ENABLE"},
		{action: "maintenance_disable", want: "CLUSTER MAINTENANCE DISABLE"},
		{action: "reconcile_confirm", want: "CLUSTER RECONCILE CONFIRM"},
		{action: "drop_everything", wantErr: true},
		{action: "", wantErr: true},
	}
	for _, c := range cases {
		got, err := clusterActionSQL(c.action, c.timeoutMS)
		if c.wantErr {
			if !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Errorf("action=%q timeout=%d: want invalid_argument, got %v", c.action, c.timeoutMS, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("action=%q timeout=%d: got (%q, %v), want %q", c.action, c.timeoutMS, got, err, c.want)
		}
	}
}

func TestClusterActionRequiresAuthAndCSRF(t *testing.T) {
	s := testServer(t)

	// No session cookie at all → 401, same as every other API route.
	rec := httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cluster/action", strings.NewReader(`{"action":"drain"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d", rec.Code)
	}

	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Authenticated but no CSRF header → 403 before the request body is
	// even inspected (an unknown action would otherwise also be a 400).
	req := httptest.NewRequest("POST", "/api/v1/cluster/action", strings.NewReader(`{"action":"drain"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	rec = httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: want 403, got %d", rec.Code)
	}

	// CSRF present but an unrecognized action → 400, never reaching the
	// (here nil) connection.
	req = httptest.NewRequest("POST", "/api/v1/cluster/action", strings.NewReader(`{"action":"not_a_real_action"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	req.Header.Set(csrfHeader, sess.csrf)
	rec = httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown action: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestMaintenanceActionSQL(t *testing.T) {
	cases := []struct {
		name    string
		op      string
		target  string
		scope   string
		online  bool
		want    string
		wantErr bool
	}{
		{name: "analyze whole db", op: "analyze", want: "ANALYZE"},
		{name: "analyze table", op: "analyze", target: "orders", want: "ANALYZE orders"},
		{name: "analyze bad target", op: "analyze", target: "orders; DROP TABLE users", wantErr: true},
		{name: "rebuild index", op: "rebuild_index", target: "idx_orders_customer", want: "REBUILD INDEX idx_orders_customer"},
		{name: "rebuild index online", op: "rebuild_index", target: "idx_orders_customer", online: true, want: "REBUILD INDEX idx_orders_customer ONLINE"},
		{name: "rebuild index empty target", op: "rebuild_index", wantErr: true},
		{name: "rebuild index injection attempt", op: "rebuild_index", target: "x ONLINE; DROP TABLE t --", wantErr: true},
		{name: "maintain database", op: "maintain", scope: "database", want: "MAINTAIN DATABASE"},
		{name: "maintain table", op: "maintain", scope: "table", target: "orders", want: "MAINTAIN TABLE orders"},
		{name: "maintain index", op: "maintain", scope: "index", target: "idx_orders_customer", want: "MAINTAIN INDEX idx_orders_customer"},
		{name: "maintain missing scope", op: "maintain", target: "orders", wantErr: true},
		{name: "maintain bad scope", op: "maintain", scope: "everything", target: "orders", wantErr: true},
		{name: "maintain table missing target", op: "maintain", scope: "table", wantErr: true},
		{name: "unknown op", op: "drop_database", wantErr: true},
		{name: "empty op", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := maintenanceActionSQL(c.op, c.target, c.scope, c.online)
			if c.wantErr {
				if !nerr.HasCode(err, nerr.InvalidArgument) {
					t.Fatalf("want invalid_argument, got (%q, %v)", got, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got (%q, %v), want %q", got, err, c.want)
			}
		})
	}
}

func TestMaintenanceActionRequiresAuthAndCSRF(t *testing.T) {
	s := testServer(t)

	rec := httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/maintenance/action", strings.NewReader(`{"op":"analyze"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d", rec.Code)
	}

	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/v1/maintenance/action", strings.NewReader(`{"op":"analyze"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	rec = httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: want 403, got %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "/api/v1/maintenance/action", strings.NewReader(`{"op":"rebuild_index","target":""}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	req.Header.Set(csrfHeader, sess.csrf)
	rec = httptest.NewRecorder()
	s.withBaseMiddleware(s.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty rebuild target: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestConfigActionSQL(t *testing.T) {
	cases := []struct {
		key     string
		value   string
		reset   bool
		want    string
		wantErr bool
	}{
		{key: "buffer_pages", value: "4096", want: "SET CONFIG buffer_pages = '4096'"},
		{key: "log_level", value: "debug", want: "SET CONFIG log_level = 'debug'"},
		{key: "buffer_pages", reset: true, want: "SET CONFIG buffer_pages = DEFAULT"},
		{key: "listen_addr", value: "10.0.0.1:7210", want: "SET CONFIG listen_addr = '10.0.0.1:7210'"},
		{key: "log_level", value: "it's", want: "SET CONFIG log_level = 'it''s'"}, // quote-escaped
		{key: "not_a_real_key", value: "x", wantErr: true},
		{key: "buffer_pages", value: "", wantErr: true},                // empty, no reset
		{key: "log_level", value: "a\nb", wantErr: true},               // newline
		{key: "", value: "x", wantErr: true},                           // empty key
		{key: "buffer_pages; DROP TABLE t", value: "1", wantErr: true}, // not a settable key
	}
	for _, c := range cases {
		got, err := configActionSQL(c.key, c.value, c.reset)
		if c.wantErr {
			if !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("%q=%q: want invalid_argument, got (%q, %v)", c.key, c.value, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("%q=%q: got (%q, %v), want %q", c.key, c.value, got, err, c.want)
		}
	}
}

func TestBackupActionSQL(t *testing.T) {
	cases := []struct {
		op, name, want string
		wantErr        bool
	}{
		{op: "create", want: "BACKUP DATABASE"},
		{op: "verify", name: "backup-20260101T000000Z", want: "VERIFY BACKUP 'backup-20260101T000000Z'"},
		{op: "verify", name: "", wantErr: true},
		{op: "verify", name: "../etc", wantErr: true},
		{op: "verify", name: "a/b", wantErr: true},
		{op: "verify", name: "a'b", wantErr: true},
		{op: "verify", name: "a\nb", wantErr: true},
		{op: "restore", wantErr: true},
		{op: "", wantErr: true},
	}
	for _, c := range cases {
		got, err := backupActionSQL(c.op, c.name)
		if c.wantErr {
			if !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("%s/%q: want invalid_argument, got (%q, %v)", c.op, c.name, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("%s/%q: got (%q, %v), want %q", c.op, c.name, got, err, c.want)
		}
	}
}

func TestCSRFConstantTimeCompare(t *testing.T) {
	s := &session{csrf: "abc123"}
	if s.checkCSRF("") || s.checkCSRF("wrong") || s.checkCSRF("abc124") {
		t.Fatal("bad token accepted")
	}
	if !s.checkCSRF("abc123") {
		t.Fatal("correct token rejected")
	}
}
