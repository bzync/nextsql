package ops

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/admin/studio"
	"github.com/bzync/nextsql/internal/nerr"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Config{
		ServerAddr:     "127.0.0.1:7210",
		InsecureServer: true,
	}, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCappedStudioResult(t *testing.T) {
	a, b, c := "a", "b", "c"
	in := resultJSON{
		Columns:     []string{"name"},
		columnTypes: []string{"STRING"},
		Rows:        [][]*string{{&a}, {&b}, {&c}},
	}
	got := cappedStudioResult(in, 2)
	if !got.Truncated || len(got.Rows) != 2 || *got.Rows[1][0] != "b" {
		t.Fatalf("capped result = %+v", got)
	}
	if full := cappedStudioResult(in, 3); full.Truncated || len(full.Rows) != 3 {
		t.Fatalf("exact-size result should not be truncated: %+v", full)
	}
}

// Listen/TLS validation (the shared process listener address) now lives in
// the parent internal/admin package's Config, not here — see
// internal/admin/config_test.go.

func TestConfigRejectsInsecureRemoteServer(t *testing.T) {
	_, err := New(Config{ServerAddr: "db.example:7210", InsecureServer: true},
		Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument for --insecure remote, got %v", err)
	}
}

func TestConfigRequiresCAOrInsecure(t *testing.T) {
	_, err := New(Config{ServerAddr: "127.0.0.1:7210"},
		Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument without --tls-ca/--insecure, got %v", err)
	}
}

// Shell/asset serving (GET /, GET /assets/*) now lives in the parent
// internal/admin package, not here — see internal/admin/server_test.go.

func TestReadModelsRequireSession(t *testing.T) {
	s := testServer(t)
	for _, p := range []string{"/api/v1/overview", "/api/v1/databases", "/api/v1/activity", "/api/v1/security", "/api/v1/cluster", "/api/v1/maintenance", "/api/v1/config", "/api/v1/diagnostics", "/api/v1/diagnostics/bundle", "/api/v1/backups", "/api/v1/studio/bootstrap", "/api/v1/studio/table?name=t", "/api/v1/studio/workflows", "/api/v1/studio/schema-graph", "/api/v1/studio/migrations"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: want 401, got %d", p, rec.Code)
		}
	}
}

func TestStudioQueryRequiresAuthAndCSRF(t *testing.T) {
	s := testServer(t)
	body := `{"query_id":"q-1","sql":"SELECT 1"}`

	for _, path := range []string{"/api/v1/studio/query", "/api/v1/studio/query/stream"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s no session: want 401, got %d", path, rec.Code)
		}
	}

	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/studio/query", "/api/v1/studio/query/stream"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s no CSRF: want 403, got %d", path, rec.Code)
		}
	}

	// With CSRF, input validation runs before the deliberately nil test
	// connection is touched.
	req := httptest.NewRequest("POST", "/api/v1/studio/query", strings.NewReader(`{"query_id":"bad/id","sql":"SELECT 1"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	req.Header.Set(csrfHeader, sess.csrf)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid query id: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestStudioQueryRejectsMalformedAndOversizedBodies(t *testing.T) {
	s := testServer(t)
	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{name: "unknown field", body: `{"query_id":"q-1","sql":"SELECT 1","unexpected":true}`, want: http.StatusBadRequest},
		{name: "second object", body: `{"query_id":"q-1","sql":"SELECT 1"}{}`, want: http.StatusBadRequest},
		{name: "SQL limit", body: `{"query_id":"q-1","sql":"` + strings.Repeat("x", studio.MaxSQLBytes+1) + `"}`, want: http.StatusRequestEntityTooLarge},
		{name: "body limit", body: `{"query_id":"q-1","sql":"` + strings.Repeat("x", studio.MaxSQLBytes+2048) + `"}`, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/studio/query", strings.NewReader(tc.body))
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
			req.Header.Set(csrfHeader, sess.csrf)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("want %d, got %d (%s)", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestStudioAnalyzeRequiresAuthAndCSRF(t *testing.T) {
	s := testServer(t)
	body := `{"sql":"DELETE FROM accounts"}`

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/studio/query/analyze", strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d", rec.Code)
	}

	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/studio/query/analyze", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: want 403, got %d", rec.Code)
	}
}

func TestStudioAnalyzeClassifiesStatements(t *testing.T) {
	s := testServer(t)
	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/studio/query/analyze", strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
		req.Header.Set(csrfHeader, sess.csrf)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"sql":"DELETE FROM accounts"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("destructive DELETE: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"destructive":true`) || !strings.Contains(rec.Body.String(), "accounts") {
		t.Fatalf("destructive DELETE response missing destructive/reason: %s", rec.Body.String())
	}

	rec = post(`{"sql":"SELECT 1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("safe SELECT: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"destructive":true`) {
		t.Fatalf("safe SELECT flagged destructive: %s", rec.Body.String())
	}

	rec = post(`{"sql":"SELECT FROM WHERE"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("syntax error: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = post(`{"sql":"SELECT 1","unexpected":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = post(`{"sql":"` + strings.Repeat("x", studio.MaxSQLBytes+1) + `"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized SQL: want 413, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestStudioSplitRequiresAuthAndCSRF(t *testing.T) {
	s := testServer(t)
	body := `{"sql":"SELECT 1; SELECT 2;"}`

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/studio/query/split", strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d", rec.Code)
	}

	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/studio/query/split", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: want 403, got %d", rec.Code)
	}
}

func TestStudioSplitTokenizesScript(t *testing.T) {
	s := testServer(t)
	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/studio/query/split", strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
		req.Header.Set(csrfHeader, sess.csrf)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}

	rec := post(`{"sql":"SELECT 1; -- a comment\nDELETE FROM accounts;"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"SELECT 1"`) || !strings.Contains(rec.Body.String(), "DELETE FROM accounts") {
		t.Fatalf("unexpected split response: %s", rec.Body.String())
	}

	rec = post(`{"sql":"-- only a comment"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("comment-only script: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = post(`{"sql":"SELECT 1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("single statement: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = post(`{"sql":"` + strings.Repeat("x", studio.MaxSQLBytes+1) + `"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized script: want 413, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = post(`{"sql":"SELECT 1","unexpected":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestStudioTableRejectsInterpolationInput(t *testing.T) {
	s := testServer(t)
	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/studio/table?name=users%27%3BDELETE+FROM+users", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("injection-shaped table: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestStudioCancellationIsSessionScoped(t *testing.T) {
	sess := &session{}
	ctx, cancel := context.WithCancel(context.Background())
	if err := sess.beginStudioQuery("query-1", cancel); err != nil {
		t.Fatal(err)
	}
	if sess.cancelStudioQuery("other-query") {
		t.Fatal("a mismatched query id was canceled")
	}
	select {
	case <-ctx.Done():
		t.Fatal("mismatched query id canceled the context")
	default:
	}
	if !sess.cancelStudioQuery("query-1") {
		t.Fatal("matching query id was not canceled")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error = %v, want canceled", ctx.Err())
	}
	sess.finishStudioQuery("query-1")
	if sess.cancelStudioQuery("query-1") {
		t.Fatal("finished query remained cancellable")
	}
}

func TestSessionReconnectSwapsTargetAndGuardsBusy(t *testing.T) {
	sess := &session{realm: "old-realm", database: "old-db"}

	if err := sess.reconnect(nil, "new-realm", "new-db"); err != nil {
		t.Fatalf("reconnect on an idle session: %v", err)
	}
	if realm, database := sess.target(); realm != "new-realm" || database != "new-db" {
		t.Fatalf("target after reconnect = (%q, %q), want (new-realm, new-db)", realm, database)
	}

	// A reconnect must fail closed while the connection lock is held (a query
	// is in flight), leaving the target untouched.
	sess.mu.Lock()
	err := sess.reconnect(nil, "raced-realm", "raced-db")
	sess.mu.Unlock()
	if !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("reconnect while busy: want conflict, got %v", err)
	}
	if realm, database := sess.target(); realm != "new-realm" || database != "new-db" {
		t.Fatalf("target after a refused reconnect changed to (%q, %q)", realm, database)
	}
}

func TestSessionSetReadConsistencyGuards(t *testing.T) {
	sess := &session{}
	if mode, ms := sess.readConsistency(); mode != studio.ReadStrong || ms != 0 {
		t.Fatalf("default read consistency = (%q, %d), want (strong, 0)", mode, ms)
	}

	// A busy connection (a query in flight) blocks the change.
	sess.mu.Lock()
	err := sess.setReadConsistency(nextsql.Bounded, time.Second, studio.ReadBounded, 1000)
	sess.mu.Unlock()
	if !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("setReadConsistency while busy: want conflict, got %v", err)
	}

	// With no connection the wire call cannot happen; the recorded mode is
	// left at its default rather than optimistically advanced.
	if err := sess.setReadConsistency(nextsql.Bounded, time.Second, studio.ReadBounded, 1000); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("setReadConsistency with no connection: want unavailable, got %v", err)
	}
	if mode, _ := sess.readConsistency(); mode != studio.ReadStrong {
		t.Fatalf("read consistency advanced despite a failed change: %q", mode)
	}
}

func TestStatusWriterPreservesStreamingFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &statusWriter{ResponseWriter: recorder, status: http.StatusOK}
	w.Header().Set("Content-Type", studio.StreamContentType)
	w.Flush()
	if !recorder.Flushed {
		t.Fatal("request logging wrapper swallowed http.Flusher")
	}
	if recorder.Code != http.StatusOK || w.status != http.StatusOK {
		t.Fatalf("flush status recorder=%d wrapper=%d", recorder.Code, w.status)
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
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 without CSRF, got %d", rec.Code)
	}

	// With the CSRF header → 204 and the session is gone.
	req = httptest.NewRequest("DELETE", "/api/v1/session", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	req.Header.Set(csrfHeader, sess.csrf)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
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
	s.Handler().ServeHTTP(rec, req)
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
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cluster/action", strings.NewReader(`{"action":"drain"}`)))
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
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: want 403, got %d", rec.Code)
	}

	// CSRF present but an unrecognized action → 400, never reaching the
	// (here nil) connection.
	req = httptest.NewRequest("POST", "/api/v1/cluster/action", strings.NewReader(`{"action":"not_a_real_action"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	req.Header.Set(csrfHeader, sess.csrf)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
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
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/maintenance/action", strings.NewReader(`{"op":"analyze"}`)))
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
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: want 403, got %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "/api/v1/maintenance/action", strings.NewReader(`{"op":"rebuild_index","target":""}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	req.Header.Set(csrfHeader, sess.csrf)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
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
