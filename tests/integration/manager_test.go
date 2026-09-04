package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/manager"
)

// startManager runs a manager.Server against the given nextsqld address over a
// plaintext loopback connection (the protocol server from startTLSServer is
// TLS, so we hand the Manager the self-signed CA PEM).
func startManager(t *testing.T, serverAddr string) string {
	t.Helper()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, lastClientCAPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := manager.New(manager.Config{
		Listen:        "127.0.0.1:0",
		ServerAddr:    serverAddr,
		ServerTLSCA:   caPath,
		ServerTLSName: "localhost",
		MaxSessions:   4,
	}, manager.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return "http://" + srv.Addr().String()
}

func mustClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// 30s (above the Manager's own 15s per-handler context timeouts) so a
	// slow-but-working request under heavy concurrent disk load in the full
	// integration suite still completes; a genuinely stuck handler fails via
	// the server's own timeout, and connection-refused (TestManagerServer-
	// Unreachable) is immediate regardless.
	return &http.Client{Jar: jar, Timeout: 30 * time.Second}
}

func doJSON(t *testing.T, c *http.Client, method, url, csrf string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-NSM-CSRF", csrf)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return res, out
}

func TestManagerLoginOverviewLogout(t *testing.T) {
	addr, _ := startTLSServer(t)
	base := startManager(t, addr)
	c := mustClient(t)

	// Bad password → 401.
	res, _ := doJSON(t, c, "POST", base+"/api/v1/session", "", map[string]any{
		"user": "app", "password": "wrong",
	})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login: want 401, got %d", res.StatusCode)
	}

	// Good login.
	res, body := doJSON(t, c, "POST", base+"/api/v1/session", "", map[string]any{
		"user": "app", "password": "s3cret", "database": "production",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login: want 200, got %d (%v)", res.StatusCode, body)
	}
	csrf, _ := body["csrf_token"].(string)
	if csrf == "" {
		t.Fatal("no csrf_token in login response")
	}

	// Overview reflects server truth from system.*.
	res, ov := doJSON(t, c, "GET", base+"/api/v1/overview", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("overview: want 200, got %d (%v)", res.StatusCode, ov)
	}
	storage, ok := ov["storage"].(map[string]any)
	if !ok {
		t.Fatalf("overview.storage missing: %v", ov)
	}
	cols, _ := storage["columns"].([]any)
	if len(cols) == 0 {
		t.Fatalf("system.storage returned no columns: %v", storage)
	}
	joined := toStringSlice(cols)
	if !contains(joined, "engine") || !contains(joined, "page_size") {
		t.Fatalf("system.storage columns unexpected: %v", joined)
	}
	caps, _ := ov["capabilities"].(map[string]any)
	if crows, _ := caps["rows"].([]any); len(crows) == 0 {
		t.Fatal("system.capabilities returned no rows")
	}
	if cl, _ := ov["clustered"].(bool); cl {
		t.Fatal("standalone node reported as clustered")
	}

	// M2 — Databases & Storage.
	res, db := doJSON(t, c, "GET", base+"/api/v1/databases", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("databases: want 200, got %d (%v)", res.StatusCode, db)
	}
	if _, ok := db["storage"].(map[string]any); !ok {
		t.Fatalf("databases.storage missing: %v", db)
	}
	dtables, _ := db["tables"].(map[string]any)
	if trows, _ := dtables["rows"].([]any); trows == nil {
		t.Fatalf("databases.tables missing rows key: %v", dtables)
	}
	if hosted, _ := db["hosted"].(bool); hosted {
		t.Fatal("single-db test deployment reported as hosted")
	}

	// M3 — Connections & Activity. The Manager's own connection is a session.
	res, act := doJSON(t, c, "GET", base+"/api/v1/activity", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("activity: want 200, got %d (%v)", res.StatusCode, act)
	}
	asess, _ := act["sessions"].(map[string]any)
	if srows, _ := asess["rows"].([]any); len(srows) == 0 {
		t.Fatalf("activity.sessions returned no rows: %v", asess)
	}
	for _, k := range []string{"active_queries", "transactions", "locks"} {
		if _, ok := act[k].(map[string]any); !ok {
			t.Fatalf("activity.%s missing: %v", k, act)
		}
	}

	// M4 (partial) — Security: users/roles/grants. No security.ACL is wired
	// in this test server, so the session is treated as admin (matching
	// every other system.* admin table) and system.users returns the real
	// "app" user; roles/grants are naturally empty (no ACL to read from).
	res, sec := doJSON(t, c, "GET", base+"/api/v1/security", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("security: want 200, got %d (%v)", res.StatusCode, sec)
	}
	susers, _ := sec["users"].(map[string]any)
	if urows, _ := susers["rows"].([]any); len(urows) == 0 {
		t.Fatalf("security.users returned no rows: %v", susers)
	}
	for _, k := range []string{"roles", "grants"} {
		if _, ok := sec[k].(map[string]any); !ok {
			t.Fatalf("security.%s missing: %v", k, sec)
		}
	}
	// system.tls (M4 continuation): this test server has no TLS listener
	// attached (SetTLSStatusSource never wired), so it must report exactly
	// one row with enabled=false rather than omit the table or error.
	stls, _ := sec["tls"].(map[string]any)
	trows, _ := stls["rows"].([]any)
	if len(trows) != 1 {
		t.Fatalf("security.tls: want exactly 1 row (no listener attached), got %v", stls)
	}
	if trow, _ := trows[0].([]any); len(trow) == 0 || trow[0] != "FALSE" {
		t.Fatalf("security.tls[0].enabled = %v, want \"FALSE\"", stls)
	}
	// system.key_versions (M4 continuation): this test server's DB was
	// opened with a bare crypto.KeyProvider (crypto.LoadProvider in
	// startTLSServer), not a crypto.Envelope, so SetKeyStatusSource is
	// never wired — a list-shaped table reports "not applicable" as zero
	// rows, not a placeholder row (unlike system.tls's single status row).
	skv, _ := sec["key_versions"].(map[string]any)
	if krows, _ := skv["rows"].([]any); len(krows) != 0 {
		t.Fatalf("security.key_versions: want 0 rows (no envelope attached), got %v", skv)
	}
	// system.audit_verify/system.audit_log (M4 continuation, closing its
	// originally scoped surface): this test server's DB was never wired via
	// DB.SetAuditSource (no nextsqld/cfg in this harness — see the
	// system.config assertion above for the same reasoning), so
	// audit_verify must report exactly one "not attached" row (lines=0,
	// verified=FALSE) and audit_log zero rows, same conventions as
	// system.tls and system.key_versions respectively.
	sav, _ := sec["audit_verify"].(map[string]any)
	avrows, _ := sav["rows"].([]any)
	if len(avrows) != 1 {
		t.Fatalf("security.audit_verify: want exactly 1 row (no audit source attached), got %v", sav)
	}
	if avrow, _ := avrows[0].([]any); len(avrow) == 0 || avrow[0] != "0" {
		t.Fatalf("security.audit_verify[0].lines = %v, want \"0\"", sav)
	}
	sal, _ := sec["audit_log"].(map[string]any)
	if alrows, _ := sal["rows"].([]any); len(alrows) != 0 {
		t.Fatalf("security.audit_log: want 0 rows (no audit source attached), got %v", sal)
	}

	// M6 — Cluster: system.replication + system.replica_health, both
	// always-visible (no admin gating needed for the read side).
	res, cl := doJSON(t, c, "GET", base+"/api/v1/cluster", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cluster: want 200, got %d (%v)", res.StatusCode, cl)
	}
	crepl, _ := cl["replication"].(map[string]any)
	if rrows, _ := crepl["rows"].([]any); len(rrows) != 1 {
		t.Fatalf("cluster.replication: want exactly 1 row (standalone), got %v", crepl)
	}
	if _, ok := cl["replica_health"].(map[string]any); !ok {
		t.Fatalf("cluster.replica_health missing: %v", cl)
	}
	if clustered, _ := cl["clustered"].(bool); clustered {
		t.Fatal("standalone test deployment reported as clustered")
	}

	// A cluster action without CSRF is refused, same as any other mutation.
	res, _ = doJSON(t, c, "POST", base+"/api/v1/cluster/action", "", map[string]any{"action": "maintenance_enable"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cluster action without CSRF: want 403, got %d", res.StatusCode)
	}

	// With CSRF, a real MAINTENANCE ENABLE/DISABLE round trip against the
	// live server — proves the action handler actually executes the exact
	// documented SQL over the operator's own connection, not a stub.
	res, act2 := doJSON(t, c, "POST", base+"/api/v1/cluster/action", csrf, map[string]any{"action": "maintenance_enable"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("maintenance_enable: want 200, got %d (%v)", res.StatusCode, act2)
	}
	if rows, _ := act2["rows"].([]any); len(rows) != 1 {
		t.Fatalf("maintenance_enable: want a 1-row acknowledgment, got %v", act2)
	} else if row, _ := rows[0].([]any); len(row) != 1 || row[0] != "maintenance_enabled" {
		t.Fatalf("maintenance_enable: want [[\"maintenance_enabled\"]], got %v", rows)
	}
	res, act3 := doJSON(t, c, "POST", base+"/api/v1/cluster/action", csrf, map[string]any{"action": "maintenance_disable"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("maintenance_disable: want 200, got %d (%v)", res.StatusCode, act3)
	}

	// An unrecognized action is a 400, never reaching the server.
	res, _ = doJSON(t, c, "POST", base+"/api/v1/cluster/action", csrf, map[string]any{"action": "delete_everything"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown cluster action: want 400, got %d", res.StatusCode)
	}

	// M7 — Maintenance: system.tables/indexes/table_stats/index_stats, plus
	// ANALYZE / MAINTAIN / REBUILD INDEX.
	res, mt := doJSON(t, c, "GET", base+"/api/v1/maintenance", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("maintenance: want 200, got %d (%v)", res.StatusCode, mt)
	}
	for _, k := range []string{"tables", "indexes", "table_stats", "index_stats"} {
		if _, ok := mt[k].(map[string]any); !ok {
			t.Fatalf("maintenance.%s missing: %v", k, mt)
		}
	}

	// A maintenance action without CSRF is refused.
	res, _ = doJSON(t, c, "POST", base+"/api/v1/maintenance/action", "", map[string]any{"op": "analyze"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("maintenance action without CSRF: want 403, got %d", res.StatusCode)
	}

	// A real ANALYZE (whole database) round trip against the live server —
	// no user tables exist in this test deployment, so it reports affected:0
	// rather than erroring; that itself proves the statement executed.
	res, an := doJSON(t, c, "POST", base+"/api/v1/maintenance/action", csrf, map[string]any{"op": "analyze"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("analyze: want 200, got %d (%v)", res.StatusCode, an)
	}
	if _, ok := an["affected"]; !ok {
		// affected:0 is omitted by omitempty — absence here is the expected
		// shape for an empty test deployment, not a failure; just confirm
		// there are also no unexpected columns/rows.
		if rows, _ := an["rows"].([]any); len(rows) != 0 {
			t.Fatalf("analyze: want 0 rows for an empty deployment, got %v", an)
		}
	}

	// A real MAINTAIN DATABASE round trip — standalone nodes have no Raft
	// gate, so requireLeader is a no-op and this always succeeds locally.
	res, mn := doJSON(t, c, "POST", base+"/api/v1/maintenance/action", csrf, map[string]any{"op": "maintain", "scope": "database"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("maintain database: want 200, got %d (%v)", res.StatusCode, mn)
	}

	// A REBUILD INDEX on a nonexistent index surfaces as 404, not a crash.
	res, _ = doJSON(t, c, "POST", base+"/api/v1/maintenance/action", csrf, map[string]any{"op": "rebuild_index", "target": "no_such_index"})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("rebuild nonexistent index: want 404, got %d", res.StatusCode)
	}

	// An injection-shaped target is rejected as 400 before reaching the
	// server at all.
	res, _ = doJSON(t, c, "POST", base+"/api/v1/maintenance/action", csrf, map[string]any{
		"op": "rebuild_index", "target": "x; DROP TABLE t --",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("injection-shaped target: want 400, got %d", res.StatusCode)
	}

	// M8 — Configuration (read-only): system.config. This test server's DB
	// (startTLSServer) was never wired via DB.SetConfigSource (there is no
	// nextsqld/cfg in this harness, only a bare protocol.NewServer(db,
	// users)), so it must report "not attached" as zero rows, not an error.
	res, cfgResp := doJSON(t, c, "GET", base+"/api/v1/config", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("config: want 200, got %d (%v)", res.StatusCode, cfgResp)
	}
	scfg, _ := cfgResp["config"].(map[string]any)
	if crows, _ := scfg["rows"].([]any); len(crows) != 0 {
		t.Fatalf("config: want 0 rows (no config.Config attached), got %v", scfg)
	}

	// M8 write path: SET CONFIG. This harness's server has no config file, so
	// a well-formed request is accepted by the Manager (200 from
	// configActionSQL) but the server rejects it Unavailable → 409.
	cares, _ := doJSON(t, c, "POST", base+"/api/v1/config/action", csrf,
		map[string]any{"key": "buffer_pages", "value": "4096"})
	if cares.StatusCode != http.StatusConflict {
		t.Fatalf("config action with no server config file: want 409, got %d", cares.StatusCode)
	}
	// A bad key is rejected by the Manager before it ever reaches the server.
	cbad, _ := doJSON(t, c, "POST", base+"/api/v1/config/action", csrf,
		map[string]any{"key": "not_a_key", "value": "x"})
	if cbad.StatusCode != http.StatusBadRequest {
		t.Fatalf("config action bad key: want 400, got %d", cbad.StatusCode)
	}

	// M9 — Diagnostics: system.metrics. Same harness limitation as
	// system.config above — this DB was never wired via DB.SetMetricsSource,
	// so system.metrics must report "not attached" as zero rows, not error.
	res, diagResp := doJSON(t, c, "GET", base+"/api/v1/diagnostics", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("diagnostics: want 200, got %d (%v)", res.StatusCode, diagResp)
	}
	smetrics, _ := diagResp["metrics"].(map[string]any)
	if mrows, _ := smetrics["rows"].([]any); len(mrows) != 0 {
		t.Fatalf("diagnostics: want 0 metric rows (no registry attached), got %v", smetrics)
	}
	slog, _ := diagResp["server_log"].(map[string]any)
	if lrows, _ := slog["rows"].([]any); len(lrows) != 0 {
		t.Fatalf("diagnostics: want 0 server_log rows (no log ring attached), got %v", slog)
	}

	// M9 — diagnostic bundle: one JSON attachment assembled from admin-only
	// system.* surfaces. Every constituent table is empty in this harness
	// (nothing wired), but the document shape and headers must be right.
	bres, bbody := doJSON(t, c, "GET", base+"/api/v1/diagnostics/bundle", "", nil)
	if bres.StatusCode != http.StatusOK {
		t.Fatalf("diagnostics bundle: want 200, got %d", bres.StatusCode)
	}
	if cd := bres.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("diagnostics bundle: Content-Disposition = %q, want attachment", cd)
	}
	if bbody["kind"] != "nextsql-manager-diagnostic-bundle" {
		t.Fatalf("diagnostics bundle: kind = %v", bbody["kind"])
	}
	btabs, _ := bbody["tables"].(map[string]any)
	for _, k := range []string{"metrics", "server_log", "config", "capabilities", "replication", "tls", "audit_verify"} {
		if _, ok := btabs[k]; !ok {
			t.Fatalf("diagnostics bundle: tables missing %q (%v)", k, btabs)
		}
	}

	// M5 — Backups: system.backups. This harness's server has no backup_dir,
	// so system.backups is empty (not an error) and BACKUP DATABASE fails
	// Unavailable → 409.
	bkres, bkbody := doJSON(t, c, "GET", base+"/api/v1/backups", "", nil)
	if bkres.StatusCode != http.StatusOK {
		t.Fatalf("backups: want 200, got %d", bkres.StatusCode)
	}
	sbk, _ := bkbody["backups"].(map[string]any)
	if brows, _ := sbk["rows"].([]any); len(brows) != 0 {
		t.Fatalf("backups: want 0 rows (no backup_dir), got %v", sbk)
	}
	if _, ok := bkbody["restore_hint"].(string); !ok {
		t.Fatalf("backups: missing restore_hint")
	}
	bcreate, _ := doJSON(t, c, "POST", base+"/api/v1/backups/action", csrf, map[string]any{"op": "create"})
	if bcreate.StatusCode != http.StatusConflict {
		t.Fatalf("backup create with no backup_dir: want 409, got %d", bcreate.StatusCode)
	}
	bbad, _ := doJSON(t, c, "POST", base+"/api/v1/backups/action", csrf, map[string]any{"op": "verify", "name": "../x"})
	if bbad.StatusCode != http.StatusBadRequest {
		t.Fatalf("backup verify bad name: want 400, got %d", bbad.StatusCode)
	}

	// A mutating call without CSRF is refused.
	res, _ = doJSON(t, c, "DELETE", base+"/api/v1/session", "", nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF: want 403, got %d", res.StatusCode)
	}

	// Logout with CSRF; the session is then gone.
	res, _ = doJSON(t, c, "DELETE", base+"/api/v1/session", csrf, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: want 204, got %d", res.StatusCode)
	}
	for _, p := range []string{"/api/v1/overview", "/api/v1/databases", "/api/v1/activity", "/api/v1/security", "/api/v1/cluster", "/api/v1/maintenance", "/api/v1/config", "/api/v1/diagnostics", "/api/v1/backups"} {
		res, _ = doJSON(t, c, "GET", base+p, "", nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s after logout: want 401, got %d", p, res.StatusCode)
		}
	}
}

func TestManagerServerUnreachable(t *testing.T) {
	// Point the Manager at a dead address; login should be a clean 502.
	base := startManagerInsecure(t, "127.0.0.1:1")
	c := mustClient(t)
	res, body := doJSON(t, c, "POST", base+"/api/v1/session", "", map[string]any{
		"user": "app", "password": "s3cret",
	})
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("unreachable server: want 502, got %d (%v)", res.StatusCode, body)
	}
}

func startManagerInsecure(t *testing.T, serverAddr string) string {
	t.Helper()
	srv, err := manager.New(manager.Config{
		Listen:         "127.0.0.1:0",
		ServerAddr:     serverAddr,
		InsecureServer: true,
	}, manager.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return "http://" + srv.Addr().String()
}

func toStringSlice(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle || strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}
