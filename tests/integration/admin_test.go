package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/admin"
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/security"
)

// startManager runs an admin.Server in Operations mode against the given
// nextsqld address over a plaintext loopback connection (the protocol server
// from startTLSServer is TLS, so we hand it the self-signed CA PEM).
func startManager(t *testing.T, serverAddr string) string {
	t.Helper()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, lastClientCAPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := admin.New(admin.Config{
		Mode:          admin.ModeOperate,
		ServerAddr:    serverAddr,
		ServerTLSCA:   caPath,
		ServerTLSName: "localhost",
		MaxSessions:   4,
	}, admin.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("admin.New: %v", err)
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

func TestAdminOpsLoginOverviewLogout(t *testing.T) {
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
	if bbody["kind"] != "nextsql-admin-diagnostic-bundle" {
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

func TestAdminStudioWorkspaceOverNSQL(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	base := startManager(t, addr)
	c := mustClient(t)

	res, login := doJSON(t, c, "POST", base+"/api/v1/session", "", map[string]any{
		"user": "app", "password": "s3cret", "database": "studio_test",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login: want 200, got %d (%v)", res.StatusCode, login)
	}
	csrf, _ := login["csrf_token"].(string)
	if csrf == "" {
		t.Fatal("no csrf token")
	}

	// Studio is capability-aware before it runs editor SQL.
	res, bootstrap := doJSON(t, c, "GET", base+"/api/v1/studio/bootstrap", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap: want 200, got %d (%v)", res.StatusCode, bootstrap)
	}
	caps, _ := bootstrap["capabilities"].(map[string]any)
	if rows, _ := caps["rows"].([]any); len(rows) == 0 {
		t.Fatalf("bootstrap capabilities returned no rows: %v", bootstrap)
	}

	// The query endpoint is state-changing from the browser's perspective,
	// even for SELECT, so it always requires this session's CSRF token.
	res, _ = doJSON(t, c, "POST", base+"/api/v1/studio/query", "", map[string]any{
		"query_id": "create-without-csrf", "sql": "CREATE TABLE studio_items (id INT64 PRIMARY KEY, label STRING)",
	})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("query without CSRF: want 403, got %d", res.StatusCode)
	}

	queries := []struct {
		id  string
		sql string
	}{
		{"studio-create", "CREATE TABLE studio_items (id INT64 PRIMARY KEY, label STRING)"},
		{"studio-insert-1", "INSERT INTO studio_items (id, label) VALUES (2, 'two')"},
		{"studio-insert-2", "INSERT INTO studio_items (id, label) VALUES (1, 'one')"},
		{"studio-create-child", "CREATE TABLE studio_notes (id INT64 PRIMARY KEY, item_id INT64 NOT NULL REFERENCES studio_items (id) ON DELETE CASCADE)"},
	}
	for _, query := range queries {
		res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
			"query_id": query.id, "sql": query.sql,
		})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: want 200, got %d (%v)", query.id, res.StatusCode, body)
		}
	}

	res, result := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-select", "sql": "SELECT id, label FROM studio_items ORDER BY id",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("select: want 200, got %d (%v)", res.StatusCode, result)
	}
	if truncated, _ := result["truncated"].(bool); truncated {
		t.Fatalf("two-row result reported truncated: %v", result)
	}
	if got := toStringSlice(result["column_types"].([]any)); len(got) != 2 || got[0] != "INT64" || got[1] != "STRING" {
		t.Fatalf("column_types = %v, want [INT64 STRING]", got)
	}
	rows, _ := result["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("select rows = %v", rows)
	}
	first, _ := rows[0].([]any)
	if len(first) != 2 || first[0] != "1" || first[1] != "one" {
		t.Fatalf("first row = %v, want [1 one]", first)
	}

	// Positional bind parameters: $1 is a string coerced to the INT64 key, and
	// a nil param slot binds a typed SQL NULL (matching nothing).
	res, boundResult := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-param-select",
		"sql":      "SELECT label FROM studio_items WHERE id = $1",
		"params":   []any{map[string]any{"value": "2"}},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("param select: want 200, got %d (%v)", res.StatusCode, boundResult)
	}
	if boundRows, _ := boundResult["rows"].([]any); len(boundRows) != 1 {
		t.Fatalf("param select rows = %v, want one", boundResult["rows"])
	} else if row0, _ := boundRows[0].([]any); len(row0) != 1 || row0[0] != "two" {
		t.Fatalf("param select row = %v, want [two]", row0)
	}
	res, nullResult := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-param-null",
		"sql":      "SELECT label FROM studio_items WHERE id = $1",
		"params":   []any{map[string]any{"value": nil}},
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("null param select: want 200, got %d (%v)", res.StatusCode, nullResult)
	}
	if nullRows, _ := nullResult["rows"].([]any); len(nullRows) != 0 {
		t.Fatalf("null param select rows = %v, want none", nullRows)
	}
	res, tooMany := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-param-overflow",
		"sql":      "SELECT 1",
		"params":   make([]any, 33),
	})
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("overflow params: want 413, got %d (%v)", res.StatusCode, tooMany)
	}

	// system.table_ddl renders canonical CREATE statements over the wire; the
	// child table's FK clause survives the round trip.
	res, ddlResult := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-table-ddl",
		"sql":      "SELECT object_type, ddl FROM system.table_ddl WHERE table_name = 'studio_notes' AND object_type = 'TABLE'",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("table_ddl select: want 200, got %d (%v)", res.StatusCode, ddlResult)
	}
	ddlRows, _ := ddlResult["rows"].([]any)
	if len(ddlRows) != 1 {
		t.Fatalf("table_ddl rows = %v, want one", ddlResult["rows"])
	}
	if ddlRow, _ := ddlRows[0].([]any); len(ddlRow) != 2 ||
		!strings.Contains(ddlRow[1].(string), `REFERENCES "studio_items" ("id") ON DELETE CASCADE`) {
		t.Fatalf("studio_notes DDL missing FK clause: %v", ddlRows[0])
	}

	// The primary browser path streams the same typed result as bounded
	// NDJSON batches. Decode frames directly from the network response rather
	// than through doJSON, proving the HTTP contract is progressive and has
	// one terminal completion record instead of one materialized result body.
	streamPayload, err := json.Marshal(map[string]any{
		"query_id": "studio-stream-select", "sql": "SELECT id, label FROM studio_items ORDER BY id",
	})
	if err != nil {
		t.Fatal(err)
	}
	streamReq, err := http.NewRequestWithContext(context.Background(), "POST", base+"/api/v1/studio/query/stream", bytes.NewReader(streamPayload))
	if err != nil {
		t.Fatal(err)
	}
	streamReq.Header.Set("Content-Type", "application/json")
	streamReq.Header.Set("X-NSM-CSRF", csrf)
	streamRes, err := c.Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	defer streamRes.Body.Close()
	if streamRes.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(streamRes.Body)
		t.Fatalf("stream select: want 200, got %d (%s)", streamRes.StatusCode, raw)
	}
	if got := streamRes.Header.Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("stream content type = %q", got)
	}
	dec := json.NewDecoder(streamRes.Body)
	var frames []map[string]any
	for {
		var frame map[string]any
		if err := dec.Decode(&frame); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode stream frame: %v", err)
		}
		frames = append(frames, frame)
	}
	if len(frames) != 3 || frames[0]["type"] != "meta" || frames[1]["type"] != "rows" || frames[2]["type"] != "complete" {
		t.Fatalf("stream frame sequence = %v", frames)
	}
	streamRows, _ := frames[1]["rows"].([]any)
	if len(streamRows) != 2 || frames[2]["row_count"] != float64(2) {
		t.Fatalf("stream rows/completion = %v", frames)
	}

	// The initial table listing is bounded; table details are lazy and come
	// from the authorized system.columns/system.indexes views.
	res, bootstrap = doJSON(t, c, "GET", base+"/api/v1/studio/bootstrap", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap after create: %d (%v)", res.StatusCode, bootstrap)
	}
	tables, _ := bootstrap["tables"].(map[string]any)
	if !resultRowsContain(tables, "studio_items") {
		t.Fatalf("Studio table explorer did not reflect server catalog: %v", tables)
	}
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-analyze", "sql": "ANALYZE studio_items",
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("ANALYZE studio_items: want 200, got %d (%v)", res.StatusCode, body)
	}
	res, detail := doJSON(t, c, "GET", base+"/api/v1/studio/table?name=studio_items", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("table detail: want 200, got %d (%v)", res.StatusCode, detail)
	}
	columns, _ := detail["columns"].(map[string]any)
	if !resultRowsContain(columns, "label") {
		t.Fatalf("table detail did not include label column: %v", detail)
	}
	// Statistics come from the authorized system.table_stats/system.index_stats
	// views, populated by the ANALYZE above.
	tableStats, ok := detail["table_stats"].(map[string]any)
	if !ok {
		t.Fatalf("table detail did not include table_stats: %v", detail)
	}
	if !resultRowsContain(tableStats, "studio_items") {
		t.Fatalf("table_stats did not reflect ANALYZE: %v", tableStats)
	}
	if _, ok := detail["index_stats"].(map[string]any); !ok {
		t.Fatalf("table detail did not include index_stats: %v", detail)
	}

	// Foreign keys come from the authorized system.foreign_keys view. The
	// parent table has none; the child table's FK surfaces here.
	if fks, ok := detail["foreign_keys"].(map[string]any); !ok {
		t.Fatalf("table detail did not include foreign_keys: %v", detail)
	} else if rows, _ := fks["rows"].([]any); len(rows) != 0 {
		t.Fatalf("studio_items should have no foreign keys, got %v", rows)
	}
	// The parent table's inbound references surface via system.foreign_keys
	// filtered on ref_table.
	if refs, ok := detail["referencing_keys"].(map[string]any); !ok {
		t.Fatalf("table detail did not include referencing_keys: %v", detail)
	} else if !resultRowsContain(refs, "studio_notes") {
		t.Fatalf("studio_items referencing_keys did not include studio_notes: %v", refs)
	}
	res, childDetail := doJSON(t, c, "GET", base+"/api/v1/studio/table?name=studio_notes", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("child table detail: want 200, got %d (%v)", res.StatusCode, childDetail)
	}
	childFKs, ok := childDetail["foreign_keys"].(map[string]any)
	if !ok {
		t.Fatalf("child table detail did not include foreign_keys: %v", childDetail)
	}
	if !resultRowsContain(childFKs, "studio_items") {
		t.Fatalf("child foreign_keys did not reference studio_items: %v", childFKs)
	}
	if refs, _ := childDetail["referencing_keys"].(map[string]any); resultRowsContain(refs, "studio_notes") {
		t.Fatalf("studio_notes should have no inbound references: %v", refs)
	}
	// The table detail bundle carries canonical DDL from system.table_ddl.
	childDDL, ok := childDetail["ddl"].(map[string]any)
	if !ok {
		t.Fatalf("child table detail did not include ddl: %v", childDetail)
	}
	if ddlRows, _ := childDDL["rows"].([]any); len(ddlRows) == 0 {
		t.Fatalf("child ddl bundle was empty: %v", childDDL)
	} else {
		var joined string
		for _, raw := range ddlRows {
			for _, cell := range raw.([]any) {
				if s, ok := cell.(string); ok {
					joined += s + "\n"
				}
			}
		}
		if !strings.Contains(joined, `REFERENCES "studio_items" ("id") ON DELETE CASCADE`) {
			t.Fatalf("child ddl did not carry the FK clause: %s", joined)
		}
	}

	// Schema-relationship diagram: one authorized read over the whole
	// system.foreign_keys catalog, RBAC-filtered.
	res, graph := doJSON(t, c, "GET", base+"/api/v1/studio/schema-graph", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("schema-graph: want 200, got %d (%v)", res.StatusCode, graph)
	}
	if graph["truncated"] != false {
		t.Fatalf("schema-graph should not be truncated for a tiny schema: %v", graph["truncated"])
	}
	graphFKs, ok := graph["foreign_keys"].(map[string]any)
	if !ok {
		t.Fatalf("schema-graph did not include foreign_keys: %v", graph)
	}
	if !resultRowsContain(graphFKs, "studio_notes") || !resultRowsContain(graphFKs, "studio_items") {
		t.Fatalf("schema-graph foreign_keys did not include the studio_notes -> studio_items edge: %v", graphFKs)
	}

	// Workflows & CDC explorer: a read-only bundle over the authorized
	// workflow/trigger/schedule/task/change-stream catalog. Create a workflow
	// and both relationship kinds through the same Studio query path, then
	// confirm each native definition surfaces.
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-workflow",
		"sql":      "CREATE WORKFLOW studio_touch(n INT64) AS BEGIN UPDATE studio_items SET label = 'touched' WHERE id = $n; END",
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("CREATE WORKFLOW: want 200, got %d (%v)", res.StatusCode, body)
	}
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-trigger",
		"sql":      "CREATE TRIGGER studio_notes_touch AFTER INSERT ON studio_notes FOR EACH ROW RUN WORKFLOW studio_touch(NEW.item_id)",
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("CREATE TRIGGER: want 200, got %d (%v)", res.StatusCode, body)
	}
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-schedule",
		"sql":      "CREATE SCHEDULE studio_touch_hourly EVERY '1h' RUN WORKFLOW studio_touch(1)",
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("CREATE SCHEDULE: want 200, got %d (%v)", res.StatusCode, body)
	}
	res, workflows := doJSON(t, c, "GET", base+"/api/v1/studio/workflows", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("studio workflows: want 200, got %d (%v)", res.StatusCode, workflows)
	}
	wfRows, ok := workflows["workflows"].(map[string]any)
	if !ok || !resultRowsContain(wfRows, "studio_touch") {
		t.Fatalf("studio workflows did not include studio_touch: %v", workflows)
	}
	triggerRows, ok := workflows["triggers"].(map[string]any)
	if !ok || !resultRowsContain(triggerRows, "studio_notes_touch") || !resultRowsContain(triggerRows, "studio_notes") || !resultRowsContain(triggerRows, "studio_touch") {
		t.Fatalf("studio workflows did not include the trigger relationship: %v", workflows)
	}
	scheduleRows, ok := workflows["schedules"].(map[string]any)
	if !ok || !resultRowsContain(scheduleRows, "studio_touch_hourly") || !resultRowsContain(scheduleRows, "studio_touch") {
		t.Fatalf("studio workflows did not include the schedule relationship: %v", workflows)
	}
	if _, ok := workflows["tasks"].(map[string]any); !ok {
		t.Fatalf("studio workflows response missing tasks result: %v", workflows)
	}
	if _, ok := workflows["change_streams"].(map[string]any); !ok {
		t.Fatalf("studio workflows response missing change_streams result: %v", workflows)
	}

	// The table-detail bundle now also carries row triggers defined on the
	// selected table (system.triggers), for the inspector Dependencies panel.
	res, notesWithTrigger := doJSON(t, c, "GET", base+"/api/v1/studio/table?name=studio_notes", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("studio_notes detail after trigger: want 200, got %d (%v)", res.StatusCode, notesWithTrigger)
	}
	if trg, ok := notesWithTrigger["triggers"].(map[string]any); !ok {
		t.Fatalf("table detail did not include triggers: %v", notesWithTrigger)
	} else if !resultRowsContain(trg, "studio_notes_touch") || !resultRowsContain(trg, "studio_touch") {
		t.Fatalf("studio_notes triggers did not include the studio_notes_touch -> studio_touch row: %v", trg)
	}
	if res, items := doJSON(t, c, "GET", base+"/api/v1/studio/table?name=studio_items", "", nil); res.StatusCode == http.StatusOK {
		if trg, _ := items["triggers"].(map[string]any); resultRowsContain(trg, "studio_notes_touch") {
			t.Fatalf("studio_items should not carry studio_notes's trigger: %v", trg)
		}
	}

	// Schema migration history explorer: a read-only view of the reserved
	// nsql_schema_migrations table. It does not exist on a fresh database, so
	// the read degrades to present=false rather than an error.
	res, migr := doJSON(t, c, "GET", base+"/api/v1/studio/migrations", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("studio migrations (absent table): want 200, got %d (%v)", res.StatusCode, migr)
	}
	if present, _ := migr["present"].(bool); present {
		t.Fatalf("studio migrations should report present=false before the migration system runs: %v", migr)
	}
	// Create the reserved history table (only its exact DDL is accepted) and a
	// row through the same Studio query path, then confirm it surfaces.
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-history-ddl",
		"sql":      catalog.HistoryDDL,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("CREATE nsql_schema_migrations: want 200, got %d (%v)", res.StatusCode, body)
	}
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-history-row",
		"sql": "INSERT INTO nsql_schema_migrations (version, name, applied_at, checksum, execution_ms, dirty, direction) " +
			"VALUES ('0001', 'studio_init', NOW(), 'sum0001', 12, 0, 'up')",
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("INSERT migration row: want 200, got %d (%v)", res.StatusCode, body)
	}
	res, migr = doJSON(t, c, "GET", base+"/api/v1/studio/migrations", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("studio migrations (present table): want 200, got %d (%v)", res.StatusCode, migr)
	}
	if present, _ := migr["present"].(bool); !present {
		t.Fatalf("studio migrations should report present=true once the history table exists: %v", migr)
	}
	histRows, ok := migr["history"].(map[string]any)
	if !ok || !resultRowsContain(histRows, "studio_init") {
		t.Fatalf("studio migrations did not include the applied migration row: %v", migr)
	}

	// The editor's confirm-before-run classification never touches the
	// connection: it parses with the same grammar the executor itself binds.
	res, analysis := doJSON(t, c, "POST", base+"/api/v1/studio/query/analyze", csrf, map[string]any{
		"sql": "DELETE FROM studio_items",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("analyze DELETE without WHERE: want 200, got %d (%v)", res.StatusCode, analysis)
	}
	if destructive, _ := analysis["destructive"].(bool); !destructive {
		t.Fatalf("DELETE without WHERE not flagged destructive: %v", analysis)
	}
	reasons, _ := analysis["reasons"].([]any)
	if len(reasons) == 0 || !strings.Contains(fmt.Sprint(reasons[0]), "studio_items") {
		t.Fatalf("analyze reasons missing table name: %v", analysis)
	}

	res, analysis = doJSON(t, c, "POST", base+"/api/v1/studio/query/analyze", csrf, map[string]any{
		"sql": "DELETE FROM studio_items WHERE id = 1",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("analyze DELETE with WHERE: want 200, got %d (%v)", res.StatusCode, analysis)
	}
	if destructive, _ := analysis["destructive"].(bool); destructive {
		t.Fatalf("DELETE with WHERE flagged destructive: %v", analysis)
	}
	// analyze also classifies read vs write for the read-only safety mode.
	if write, _ := analysis["write"].(bool); !write {
		t.Fatalf("DELETE not classified as a write: %v", analysis)
	}
	res, readAnalysis := doJSON(t, c, "POST", base+"/api/v1/studio/query/analyze", csrf, map[string]any{
		"sql": "SELECT id FROM studio_items",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("analyze SELECT: want 200, got %d (%v)", res.StatusCode, readAnalysis)
	}
	if write, _ := readAnalysis["write"].(bool); write {
		t.Fatalf("SELECT classified as a write: %v", readAnalysis)
	}
	if rs, _ := readAnalysis["realm_scoped"].(bool); rs {
		t.Fatalf("SELECT flagged realm_scoped: %v", readAnalysis)
	}

	// analyze flags a realm-wide principal change so the editor's
	// confirm-before-run dialog can name the connected realm/database — the
	// "visible cross-database administration warning".
	res, realmAnalysis := doJSON(t, c, "POST", base+"/api/v1/studio/query/analyze", csrf, map[string]any{
		"sql": "CREATE USER studio_contractor IDENTIFIED BY 'pw'",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("analyze CREATE USER: want 200, got %d (%v)", res.StatusCode, realmAnalysis)
	}
	if rs, _ := realmAnalysis["realm_scoped"].(bool); !rs {
		t.Fatalf("CREATE USER not flagged realm_scoped: %v", realmAnalysis)
	}

	res, analysis = doJSON(t, c, "POST", base+"/api/v1/studio/query/analyze", csrf, map[string]any{
		"sql": "not valid sql (((",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("analyze syntax error: want 400, got %d (%v)", res.StatusCode, analysis)
	}

	// Exercise the browser cancellation endpoint against a query that is
	// deterministically waiting on a real transaction lock. BEGIN on the
	// Studio connection first also proves cancellation is scoped to that
	// authenticated session and leaves its NSQL connection reusable.
	res, begin := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-begin", "sql": "BEGIN",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Studio BEGIN: want 200, got %d (%v)", res.StatusCode, begin)
	}
	holder := openApp(t, addr, tlsCfg)
	if _, err := holder.Exec(context.Background(), `BEGIN`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = holder.Exec(context.Background(), `ROLLBACK`) }()
	if _, err := holder.Exec(context.Background(), `UPDATE studio_items SET label = 'held' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	observer := openApp(t, addr, tlsCfg)
	const waitingSQL = `UPDATE studio_items SET label = 'waiting' WHERE id = 1`
	payload, err := json.Marshal(map[string]any{"query_id": "studio-cancel-live", "sql": waitingSQL})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), "POST", base+"/api/v1/studio/query/stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-NSM-CSRF", csrf)
	type queryOutcome struct {
		status int
		body   string
		err    error
	}
	done := make(chan queryOutcome, 1)
	go func() {
		response, requestErr := c.Do(req)
		if requestErr != nil {
			done <- queryOutcome{err: requestErr}
			return
		}
		raw, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		done <- queryOutcome{status: response.StatusCode, body: string(raw), err: readErr}
	}()

	seen := false
	deadline := time.Now().Add(2 * time.Second)
	for !seen && time.Now().Before(deadline) {
		active, queryErr := observer.Exec(context.Background(), `SELECT sql FROM system.active_queries`)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		for _, row := range active.Rows {
			if len(row) == 1 && !row[0].Null && row[0].Str == waitingSQL {
				seen = true
				break
			}
		}
		if !seen {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !seen {
		t.Fatal("Studio lock-waiting query never appeared in system.active_queries")
	}
	res, canceled := doJSON(t, c, "POST", base+"/api/v1/studio/query/cancel", csrf, map[string]any{
		"query_id": "studio-cancel-live",
	})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel active query: want 202, got %d (%v)", res.StatusCode, canceled)
	}
	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("canceled query request: %v", outcome.err)
		}
		if outcome.status != http.StatusRequestTimeout {
			t.Fatalf("canceled query: want 408, got %d (%s)", outcome.status, outcome.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Studio cancellation did not release the lock-waiting request")
	}
	res, rolledBack := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "studio-rollback", "sql": "ROLLBACK",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Studio connection unusable after cancel: rollback got %d (%v)", res.StatusCode, rolledBack)
	}

	res, _ = doJSON(t, c, "POST", base+"/api/v1/studio/query/cancel", csrf, map[string]any{
		"query_id": "already-finished",
	})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("cancel finished query: want 404, got %d", res.StatusCode)
	}

	// Connection re-targeting. The bootstrap advertises which nextsqld the
	// session talks to.
	res, bootstrap = doJSON(t, c, "GET", base+"/api/v1/studio/bootstrap", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap before reconnect: %d (%v)", res.StatusCode, bootstrap)
	}
	if got, _ := bootstrap["server_addr"].(string); got != addr {
		t.Fatalf("bootstrap server_addr = %q, want %q", got, addr)
	}

	// A reconnect is state-changing and requires this session's CSRF token.
	res, _ = doJSON(t, c, "POST", base+"/api/v1/studio/reconnect", "", map[string]any{
		"database": "studio_test", "password": "s3cret",
	})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("reconnect without CSRF: want 403, got %d", res.StatusCode)
	}

	// An invalid target name is rejected before any network I/O.
	res, badName := doJSON(t, c, "POST", base+"/api/v1/studio/reconnect", csrf, map[string]any{
		"realm": "not a realm", "password": "s3cret",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("reconnect with a bad realm name: want 400, got %d (%v)", res.StatusCode, badName)
	}

	// A wrong password fails 401 and must leave the session on its working
	// connection — a follow-up query still succeeds.
	res, _ = doJSON(t, c, "POST", base+"/api/v1/studio/reconnect", csrf, map[string]any{
		"database": "studio_test", "password": "wrong-password",
	})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reconnect with a wrong password: want 401, got %d", res.StatusCode)
	}
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "post-failed-reconnect", "sql": "SELECT 1",
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("session unusable after a failed reconnect: SELECT 1 got %d (%v)", res.StatusCode, body)
	}

	// A valid reconnect swaps the connection and reports the now-active
	// target; whoami follows.
	res, reconnected := doJSON(t, c, "POST", base+"/api/v1/studio/reconnect", csrf, map[string]any{
		"database": "studio_test", "password": "s3cret",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("valid reconnect: want 200, got %d (%v)", res.StatusCode, reconnected)
	}
	if reconnected["user"] != "app" || reconnected["database"] != "studio_test" {
		t.Fatalf("reconnect response = %v, want user app / database studio_test", reconnected)
	}
	res, who := doJSON(t, c, "GET", base+"/api/v1/session", "", nil)
	if res.StatusCode != http.StatusOK || who["database"] != "studio_test" {
		t.Fatalf("whoami after reconnect = %d %v", res.StatusCode, who)
	}
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "post-reconnect", "sql": "SELECT id FROM studio_items ORDER BY id LIMIT 1",
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("query on the reconnected session: want 200, got %d (%v)", res.StatusCode, body)
	}

	// Read consistency: a live session-control change on the existing
	// connection, no reconnect. A fresh session (and a fresh reconnect) is
	// STRONG.
	res, bootstrap = doJSON(t, c, "GET", base+"/api/v1/studio/bootstrap", "", nil)
	if res.StatusCode != http.StatusOK || bootstrap["read_consistency"] != "strong" {
		t.Fatalf("bootstrap read_consistency = %v (status %d), want strong", bootstrap["read_consistency"], res.StatusCode)
	}

	res, _ = doJSON(t, c, "POST", base+"/api/v1/studio/read-consistency", "", map[string]any{"mode": "bounded"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("read-consistency without CSRF: want 403, got %d", res.StatusCode)
	}
	res, badMode := doJSON(t, c, "POST", base+"/api/v1/studio/read-consistency", csrf, map[string]any{"mode": "eventual"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("read-consistency with an unknown mode: want 400, got %d (%v)", res.StatusCode, badMode)
	}

	res, rc := doJSON(t, c, "POST", base+"/api/v1/studio/read-consistency", csrf, map[string]any{
		"mode": "bounded", "max_staleness_ms": 2000,
	})
	if res.StatusCode != http.StatusOK || rc["mode"] != "bounded" || rc["max_staleness_ms"] != float64(2000) {
		t.Fatalf("set bounded read consistency: %d %v", res.StatusCode, rc)
	}
	res, bootstrap = doJSON(t, c, "GET", base+"/api/v1/studio/bootstrap", "", nil)
	if res.StatusCode != http.StatusOK || bootstrap["read_consistency"] != "bounded" || bootstrap["max_staleness_ms"] != float64(2000) {
		t.Fatalf("bootstrap after bounded: read_consistency=%v max_staleness_ms=%v", bootstrap["read_consistency"], bootstrap["max_staleness_ms"])
	}
	if res, body := doJSON(t, c, "POST", base+"/api/v1/studio/query", csrf, map[string]any{
		"query_id": "bounded-read", "sql": "SELECT id FROM studio_items ORDER BY id LIMIT 1",
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("SELECT under bounded read consistency: want 200, got %d (%v)", res.StatusCode, body)
	}

	// A non-BOUNDED mode drops any staleness bound.
	res, rc = doJSON(t, c, "POST", base+"/api/v1/studio/read-consistency", csrf, map[string]any{
		"mode": "strong", "max_staleness_ms": 9999,
	})
	if res.StatusCode != http.StatusOK || rc["mode"] != "strong" || rc["max_staleness_ms"] != float64(0) {
		t.Fatalf("reset to strong: %d %v", res.StatusCode, rc)
	}
}

func TestAdminOpsServerUnreachable(t *testing.T) {
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
	srv, err := admin.New(admin.Config{
		Mode:           admin.ModeOperate,
		ServerAddr:     serverAddr,
		InsecureServer: true,
	}, admin.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("admin.New: %v", err)
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

// TestAdminStudioEnforcesRBAC proves the Studio design invariant that every
// Studio surface runs as the logged-in user over the real NSQL connection,
// so the server's own RBAC is the sole authority: a limited user cannot see,
// read, or create anything their grants do not allow, and admin-only
// system.* views return zero rows rather than data or an error. This closes
// the Studio MVP exit-gate line "RBAC/realm/database tests pass".
func TestAdminStudioEnforcesRBAC(t *testing.T) {
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "studio.acl"))
	if err != nil {
		t.Fatal(err)
	}
	// app: full cluster admin. viewer: connect + SELECT on public_items only.
	for _, g := range []struct {
		who   string
		priv  security.Privilege
		scope security.ScopeKind
		obj   string
	}{
		{"app", security.PrivAdmin, security.ScopeCluster, ""},
		{"viewer", security.PrivConnect, security.ScopeDatabase, ""},
		{"viewer", security.PrivSelect, security.ScopeTable, "public_items"},
	} {
		if err := acl.Grant(g.who, g.priv, g.scope, g.obj); err != nil {
			t.Fatalf("grant %s/%v: %v", g.who, g.priv, err)
		}
	}

	addr, _ := startTLSServer(t, func(srv *protocol.Server) {
		srv.ACL = acl
		if err := srv.Auth.Upsert("viewer", "look-only"); err != nil {
			t.Fatalf("create viewer: %v", err)
		}
	})
	base := startManager(t, addr)

	// As app (admin): create a table the viewer may see and one it may not,
	// plus a workflow the viewer must not see.
	cAdmin := mustClient(t)
	res, login := doJSON(t, cAdmin, "POST", base+"/api/v1/session", "", map[string]any{
		"user": "app", "password": "s3cret", "database": "rbac_test",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("admin login: want 200, got %d (%v)", res.StatusCode, login)
	}
	adminCSRF, _ := login["csrf_token"].(string)
	for _, stmt := range []string{
		"CREATE TABLE public_items (id INT64 PRIMARY KEY, label STRING)",
		"CREATE TABLE secret_ledger (id INT64 PRIMARY KEY, amount DECIMAL(10,2))",
		"CREATE WORKFLOW secret_flow(n INT64) AS BEGIN UPDATE public_items SET label = 'x' WHERE id = $n; END",
	} {
		if r, b := doJSON(t, cAdmin, "POST", base+"/api/v1/studio/query", adminCSRF, map[string]any{
			"query_id": "admin-setup", "sql": stmt,
		}); r.StatusCode != http.StatusOK {
			t.Fatalf("admin setup %q: want 200, got %d (%v)", stmt, r.StatusCode, b)
		}
	}

	// As viewer (limited).
	cViewer := mustClient(t)
	res, vLogin := doJSON(t, cViewer, "POST", base+"/api/v1/session", "", map[string]any{
		"user": "viewer", "password": "look-only", "database": "rbac_test",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("viewer login: want 200, got %d (%v)", res.StatusCode, vLogin)
	}
	viewerCSRF, _ := vLogin["csrf_token"].(string)

	// Bootstrap lists only the table the viewer can SELECT.
	res, boot := doJSON(t, cViewer, "GET", base+"/api/v1/studio/bootstrap", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("viewer bootstrap: want 200, got %d (%v)", res.StatusCode, boot)
	}
	bootTables, _ := boot["tables"].(map[string]any)
	if !resultRowsContain(bootTables, "public_items") {
		t.Fatalf("viewer bootstrap missing its own visible table: %v", bootTables)
	}
	if resultRowsContain(bootTables, "secret_ledger") {
		t.Fatalf("viewer bootstrap leaked a table it has no SELECT on: %v", bootTables)
	}

	// Table detail is 404 for the invisible table, 200 for the visible one.
	if r, b := doJSON(t, cViewer, "GET", base+"/api/v1/studio/table?name=secret_ledger", "", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("viewer table detail on invisible table: want 404, got %d (%v)", r.StatusCode, b)
	}
	if r, b := doJSON(t, cViewer, "GET", base+"/api/v1/studio/table?name=public_items", "", nil); r.StatusCode != http.StatusOK {
		t.Fatalf("viewer table detail on visible table: want 200, got %d (%v)", r.StatusCode, b)
	}

	// The editor path enforces the same boundary: a SELECT on the invisible
	// table fails, a CREATE without privilege fails, the granted SELECT works.
	if r, b := doJSON(t, cViewer, "POST", base+"/api/v1/studio/query", viewerCSRF, map[string]any{
		"query_id": "viewer-read-secret", "sql": "SELECT * FROM secret_ledger",
	}); r.StatusCode == http.StatusOK {
		t.Fatalf("viewer SELECT on unauthorized table succeeded: %v", b)
	}
	if r, b := doJSON(t, cViewer, "POST", base+"/api/v1/studio/query", viewerCSRF, map[string]any{
		"query_id": "viewer-ddl", "sql": "CREATE TABLE evil (id INT64 PRIMARY KEY)",
	}); r.StatusCode == http.StatusOK {
		t.Fatalf("viewer CREATE TABLE without privilege succeeded: %v", b)
	}
	if r, b := doJSON(t, cViewer, "POST", base+"/api/v1/studio/query", viewerCSRF, map[string]any{
		"query_id": "viewer-read-public", "sql": "SELECT id, label FROM public_items",
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("viewer SELECT on its own granted table: want 200, got %d (%v)", r.StatusCode, b)
	}

	// Admin-only system.* views return zero rows for the non-admin — not an
	// error, not data.
	if r, b := doJSON(t, cViewer, "POST", base+"/api/v1/studio/query", viewerCSRF, map[string]any{
		"query_id": "viewer-system-users", "sql": "SELECT * FROM system.users",
	}); r.StatusCode != http.StatusOK {
		t.Fatalf("viewer SELECT FROM system.users: want 200, got %d (%v)", r.StatusCode, b)
	} else if rows, _ := b["rows"].([]any); len(rows) != 0 {
		t.Fatalf("system.users leaked %d rows to a non-admin", len(rows))
	}

	// The Workflows & CDC explorer read is RBAC-filtered too: the viewer
	// sees none of the admin's workflows.
	res, wf := doJSON(t, cViewer, "GET", base+"/api/v1/studio/workflows", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("viewer studio workflows: want 200, got %d (%v)", res.StatusCode, wf)
	}
	if wfRows, _ := wf["workflows"].(map[string]any); resultRowsContain(wfRows, "secret_flow") {
		t.Fatalf("viewer saw a workflow it has no visibility on: %v", wf)
	}
}

func resultRowsContain(result map[string]any, needle string) bool {
	rows, _ := result["rows"].([]any)
	for _, raw := range rows {
		row, _ := raw.([]any)
		for _, cell := range row {
			if s, ok := cell.(string); ok && strings.EqualFold(s, needle) {
				return true
			}
		}
	}
	return false
}
