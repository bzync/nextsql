package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/nerr"
)

const maxLoginBody = 4 << 10
const maxActionBody = 4 << 10

type loginRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	Realm    string `json:"realm"`
}

type loginResponse struct {
	User      string `json:"user"`
	Database  string `json:"database"`
	Realm     string `json:"realm"`
	CSRFToken string `json:"csrf_token"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxLoginBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.User = strings.TrimSpace(req.User)
	if req.User == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "user and password are required")
		return
	}

	base, err := s.cfg.driverConfig()
	if err != nil {
		// A misconfigured Manager, not a bad credential.
		writeError(w, http.StatusInternalServerError, "manager TLS configuration error")
		s.log.Error("manager driver config", "err", err.Error())
		return
	}
	base.User = req.User
	base.Password = req.Password
	base.Database = strings.TrimSpace(req.Database)
	base.Realm = strings.TrimSpace(req.Realm)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	conn, err := nextsql.OpenContext(ctx, base)
	if err != nil {
		status, msg := loginErrorStatus(err)
		writeError(w, status, msg)
		s.log.Info("manager login failed", "user", req.User, "status", status)
		return
	}

	sess, err := s.sessions.create(conn, req.User, base.Database, base.Realm)
	if err != nil {
		_ = conn.Close()
		if nerr.HasCode(err, nerr.Exhausted) {
			writeError(w, http.StatusServiceUnavailable, "too many active Manager sessions")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not start session")
		return
	}

	setSessionCookie(w, sess.id, s.tls)
	s.log.Info("manager login", "user", req.User, "sessions", s.sessions.len())
	writeJSON(w, http.StatusOK, loginResponse{
		User: sess.user, Database: sess.database, Realm: sess.realm, CSRFToken: sess.csrf,
	})
}

func (s *Server) handleWhoami(w http.ResponseWriter, _ *http.Request, sess *session) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          sess.user,
		"database":      sess.database,
		"realm":         sess.realm,
		"csrf_token":    sess.csrf,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request, sess *session) {
	s.sessions.remove(sess.id)
	clearSessionCookie(w, s.tls)
	w.WriteHeader(http.StatusNoContent)
}

// bundle is one JSON read-model: a set of named system.* result sets plus a
// timestamp and any per-query warnings. A query that a required table is
// missing (or the operator cannot read) becomes a warning rather than
// failing the whole view — except a "required" one, which returns an error.
type bundle struct {
	GeneratedAt string                `json:"generated_at"`
	Tables      map[string]resultJSON `json:"tables"`
	Warnings    []string              `json:"warnings,omitempty"`
}

type querySpec struct {
	key      string
	sql      string
	required bool
}

func runBundle(ctx context.Context, sess *session, specs []querySpec) (bundle, error) {
	b := bundle{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Tables:      make(map[string]resultJSON, len(specs)),
	}
	for _, spec := range specs {
		r, err := sess.query(ctx, spec.sql)
		if err != nil {
			if spec.required {
				return bundle{}, &bundleError{source: spec.key, err: err}
			}
			b.Warnings = append(b.Warnings, spec.key+" unavailable: "+userError(err))
			b.Tables[spec.key] = resultJSON{Columns: []string{}, Rows: [][]*string{}}
			continue
		}
		b.Tables[spec.key] = r
	}
	return b, nil
}

type bundleError struct {
	source string
	err    error
}

func (e *bundleError) Error() string { return e.source + ": " + e.err.Error() }
func (e *bundleError) Unwrap() error { return e.err }

// handleOverview is the M1 Overview read-model.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "storage", sql: "SELECT * FROM system.storage", required: true},
		{key: "replication", sql: "SELECT * FROM system.replication", required: true},
		{key: "capabilities", sql: "SELECT name, status, description, since_version FROM system.capabilities", required: true},
		{key: "sessions", sql: "SELECT session_id FROM system.sessions"},
		{key: "active_queries", sql: "SELECT query_id FROM system.active_queries"},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}

	out := map[string]any{
		"generated_at":   b.GeneratedAt,
		"storage":        b.Tables["storage"],
		"replication":    b.Tables["replication"],
		"capabilities":   b.Tables["capabilities"],
		"sessions":       len(b.Tables["sessions"].Rows),
		"active_queries": len(b.Tables["active_queries"].Rows),
		"clustered":      clusteredFrom(b.Tables["replication"]),
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDatabases is the M2 Databases & Storage read-model. system.databases
// and system.realms are empty on a non-hosted deployment — that is reported,
// not an error.
func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "storage", sql: "SELECT * FROM system.storage", required: true},
		{key: "databases", sql: "SELECT * FROM system.databases"},
		{key: "realms", sql: "SELECT * FROM system.realms"},
		{key: "tables", sql: "SELECT * FROM system.tables"},
		{key: "table_stats", sql: "SELECT * FROM system.table_stats"},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	out := map[string]any{
		"generated_at": b.GeneratedAt,
		"storage":      b.Tables["storage"],
		"databases":    b.Tables["databases"],
		"realms":       b.Tables["realms"],
		"tables":       b.Tables["tables"],
		"table_stats":  b.Tables["table_stats"],
		"hosted":       len(b.Tables["databases"].Rows) > 0,
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

// handleActivity is the M3 Connections & Activity read-model.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "sessions", sql: "SELECT * FROM system.sessions", required: true},
		{key: "active_queries", sql: "SELECT * FROM system.active_queries"},
		{key: "transactions", sql: "SELECT * FROM system.transactions"},
		{key: "locks", sql: "SELECT * FROM system.locks"},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	out := map[string]any{
		"generated_at":   b.GeneratedAt,
		"sessions":       b.Tables["sessions"],
		"active_queries": b.Tables["active_queries"],
		"transactions":   b.Tables["transactions"],
		"locks":          b.Tables["locks"],
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSecurity is the M4 Security read-model: users, roles, and grants
// from the durable auth.Store/security.ACL state via system.users/roles/
// grants, the live listener's redacted TLS status via system.tls, the
// attached envelope's redacted key rotation state via system.key_versions,
// and (this increment, closing M4's original scope) a bounded, chain-
// verified tail of the audit log via system.audit_verify/system.audit_log.
// All seven tables are already admin-only server-side (a non-admin sees
// zero rows, never an error — docs/system-catalog.md "Security
// administration tables"), so this handler adds no RBAC of its own; it is
// a thin read-model shape over official interfaces, same as M1-M3/M6/M7.
func (s *Server) handleSecurity(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "users", sql: "SELECT * FROM system.users", required: true},
		{key: "roles", sql: "SELECT * FROM system.roles"},
		{key: "grants", sql: "SELECT * FROM system.grants"},
		{key: "tls", sql: "SELECT * FROM system.tls"},
		{key: "key_versions", sql: "SELECT * FROM system.key_versions ORDER BY key_name"},
		{key: "audit_verify", sql: "SELECT * FROM system.audit_verify"},
		{key: "audit_log", sql: "SELECT * FROM system.audit_log ORDER BY seq DESC"},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	out := map[string]any{
		"generated_at": b.GeneratedAt,
		"users":        b.Tables["users"],
		"roles":        b.Tables["roles"],
		"grants":       b.Tables["grants"],
		"tls":          b.Tables["tls"],
		"key_versions": b.Tables["key_versions"],
		"audit_verify": b.Tables["audit_verify"],
		"audit_log":    b.Tables["audit_log"],
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

// handleConfig is the M8 Configuration read-model: the running process's
// redacted config.Config via system.config, admin-only server-side and zero
// rows (not an error) for embedded/CLI use with no process-level config.Config
// attached. The `file_value` / `restart_required` columns show a persisted-
// but-not-applied SET CONFIG write (or a startup flag that overrode the file).
// handleConfigAction is the write side.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "config", sql: "SELECT * FROM system.config ORDER BY name", required: true},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	out := map[string]any{
		"generated_at": b.GeneratedAt,
		"config":       b.Tables["config"],
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

type configActionRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Reset bool   `json:"reset"`
}

// configSettableKeys is config.SettableKeys() as a set — the allowlist a
// "key" from the request JSON must be in before it is interpolated into a
// SET CONFIG statement. Exact-match against the config package's own list
// (not a regex), the same "validate before interpolation" discipline M7's
// maintenance action uses; anything not in it could not name a real setting
// anyway.
var configSettableKeys = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range config.SettableKeys() {
		m[k] = true
	}
	return m
}()

// configActionSQL renders one M8 config-write request to a SET CONFIG
// statement. The key is allowlisted against config.SettableKeys(); the value
// is always emitted as a single-quoted string literal (with '' escaping) —
// the server's config.WithSetting re-parses it through config.Load, which
// does the per-key type coercion, so a quoted "4096" still lands as the
// integer buffer_pages. Reset renders `= DEFAULT`.
func configActionSQL(key, value string, reset bool) (string, error) {
	const op = "manager.configAction"
	key = strings.TrimSpace(key)
	if !configSettableKeys[key] {
		return "", nerr.New(nerr.InvalidArgument, op, "unknown or non-settable configuration key")
	}
	if reset {
		return "SET CONFIG " + key + " = DEFAULT", nil
	}
	if strings.ContainsAny(value, "\n\r") {
		return "", nerr.New(nerr.InvalidArgument, op, "value must not contain a newline")
	}
	if strings.TrimSpace(value) == "" {
		return "", nerr.New(nerr.InvalidArgument, op, "value is required (use reset to clear a setting)")
	}
	return "SET CONFIG " + key + " = '" + strings.ReplaceAll(value, "'", "''") + "'", nil
}

// handleConfigAction is the M8 Configuration write path: it issues a single
// SET CONFIG statement on the operator's own connection. SET CONFIG requires
// ADMIN ON CLUSTER server-side and persists to the node's nextsql.conf
// (never the Manager touching the file); the change is persist-only and
// takes effect on the next server restart. Returns the statement's
// acknowledgment row (key / file_value / running_value / restart_required).
func (s *Server) handleConfigAction(w http.ResponseWriter, r *http.Request, sess *session) {
	var req configActionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxActionBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sql, err := configActionSQL(req.Key, req.Value, req.Reset)
	if err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := sess.query(ctx, sql)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case nerr.HasCode(err, nerr.Unauthorized):
			status = http.StatusForbidden
		case nerr.HasCode(err, nerr.InvalidArgument):
			status = http.StatusBadRequest
		case nerr.HasCode(err, nerr.Unavailable):
			status = http.StatusConflict
		}
		writeError(w, status, userError(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleDiagnostics is the M9 Logs & Diagnostics read-model: the process-wide
// metrics registry via system.metrics and a bounded tail of the process's
// own structured log via system.server_log. Both are admin-only server-side
// and return zero rows (not an error) for embedded/CLI use with nothing
// attached, same "empty means not applicable" convention as system.config.
// The redacted diagnostic-bundle export is a sibling route,
// handleDiagnosticsBundle. A thin read-model shape over official interfaces,
// exactly like M1-M8; no RBAC of its own.
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "metrics", sql: "SELECT * FROM system.metrics", required: true},
		{key: "server_log", sql: "SELECT * FROM system.server_log ORDER BY seq DESC"},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	out := map[string]any{
		"generated_at": b.GeneratedAt,
		"metrics":      b.Tables["metrics"],
		"server_log":   b.Tables["server_log"],
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

// diagnosticsBundleSpecs is the fixed set of official read surfaces that go
// into the M9 diagnostic bundle — every one already admin-only and already
// redacted at its own source (system.config scrubs addresses; system.tls /
// system.key_versions never carry key material; system.audit_verify is
// status only, not log content). No table here can return tenant row data.
// system.server_log is included as-is: an operator assembling a diagnostic
// bundle already sees it in the Diagnostics view, and it is the single most
// useful artifact for an off-box investigation.
var diagnosticsBundleSpecs = []querySpec{
	{key: "metrics", sql: "SELECT * FROM system.metrics"},
	{key: "server_log", sql: "SELECT * FROM system.server_log ORDER BY seq DESC"},
	{key: "config", sql: "SELECT * FROM system.config ORDER BY name"},
	{key: "capabilities", sql: "SELECT * FROM system.capabilities"},
	{key: "storage", sql: "SELECT * FROM system.storage"},
	{key: "replication", sql: "SELECT * FROM system.replication"},
	{key: "replica_health", sql: "SELECT * FROM system.replica_health"},
	{key: "tls", sql: "SELECT * FROM system.tls"},
	{key: "key_versions", sql: "SELECT * FROM system.key_versions ORDER BY key_name"},
	{key: "audit_verify", sql: "SELECT * FROM system.audit_verify"},
}

// handleDiagnosticsBundle assembles the M9 redacted diagnostic-bundle export
// ("nextsql diagnose" shaped, but Manager-scoped: driver-only, so it is
// built entirely from official admin-only system.* read surfaces, never
// data-directory access). Returns one JSON document with an attachment
// Content-Disposition so the operator's browser saves it. Every constituent
// table is already redacted at its own source; this handler adds a top-level
// note rather than re-scrubbing. No RBAC of its own — a non-admin operator
// simply gets a bundle of empty tables.
func (s *Server) handleDiagnosticsBundle(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, diagnosticsBundleSpecs)
	if err != nil {
		writeBundleError(w, err)
		return
	}
	doc := map[string]any{
		"kind":         "nextsql-manager-diagnostic-bundle",
		"version":      1,
		"generated_at": b.GeneratedAt,
		"connection": map[string]any{
			"user":     sess.user,
			"database": sess.database,
			"realm":    sess.realm,
		},
		"note": "Assembled from admin-only system.* read surfaces only. " +
			"config/tls/key_versions are redacted at their source and carry " +
			"no key material or server addresses; server_log is verbatim " +
			"process log text and may contain listen/peer addresses. Contains " +
			"no tenant row data.",
		"tables": b.Tables,
	}
	if len(b.Warnings) > 0 {
		doc["warnings"] = b.Warnings
	}
	filename := "nextsql-diagnostics-" + time.Now().UTC().Format("20060102T150405Z") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc)
}

// handleBackups is the M5 Backups read-model: system.backups — the verified
// backups in the node's configured backup_dir, oldest first. Admin/BACKUP
// gated server-side; zero rows when no backup_dir is configured. The write
// side is handleBackupAction (BACKUP DATABASE / VERIFY BACKUP). Restore and
// PITR are not a Manager operation — you cannot restore a running server
// into itself — so the view surfaces the CLI command instead.
func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "backups", sql: "SELECT * FROM system.backups ORDER BY created_at DESC", required: true},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	out := map[string]any{
		"generated_at": b.GeneratedAt,
		"backups":      b.Tables["backups"],
		"restore_hint": "Restore is offline-only (a running server cannot restore into itself). " +
			"Stop nextsqld, then: nextsql restore --from <backup_dir>/<name> --data-dir <DIR> --key-file <KEY> " +
			"[--wal-archive DIR] [--until-lsn N | --until RFC3339]",
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

type backupActionRequest struct {
	Op   string `json:"op"`   // "create" | "verify"
	Name string `json:"name"` // required for "verify" — a backup subdirectory name only
}

// backupActionSQL renders one M5 backup request to its statement. "verify"
// interpolates the backup name as a quoted literal (with '' escaping); the
// server-side executor.VerifyBackup rejects a name with a path separator or
// "..", so this only has to guard the SQL string.
func backupActionSQL(op, name string) (string, error) {
	const p = "manager.backupAction"
	switch op {
	case "create":
		return "BACKUP DATABASE", nil
	case "verify":
		name = strings.TrimSpace(name)
		if name == "" {
			return "", nerr.New(nerr.InvalidArgument, p, "verify requires a backup name")
		}
		if strings.ContainsAny(name, "\n\r'\\/") || strings.Contains(name, "..") {
			return "", nerr.New(nerr.InvalidArgument, p, "invalid backup name")
		}
		return "VERIFY BACKUP '" + name + "'", nil
	default:
		return "", nerr.New(nerr.InvalidArgument, p, "unknown backup op")
	}
}

// handleBackupAction issues one M5 backup statement on the operator's own
// connection. BACKUP DATABASE / VERIFY BACKUP both require the BACKUP
// privilege (or cluster ADMIN) server-side; this adds no RBAC of its own.
// A create can take a while (it copies + seals + restore-tests every file),
// hence the longer timeout.
func (s *Server) handleBackupAction(w http.ResponseWriter, r *http.Request, sess *session) {
	var req backupActionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxActionBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sql, err := backupActionSQL(req.Op, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}

	timeout := 30 * time.Second
	if req.Op == "create" {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	res, err := sess.query(ctx, sql)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case nerr.HasCode(err, nerr.Unauthorized):
			status = http.StatusForbidden
		case nerr.HasCode(err, nerr.InvalidArgument), nerr.HasCode(err, nerr.NotFound):
			status = http.StatusBadRequest
		case nerr.HasCode(err, nerr.Unavailable):
			status = http.StatusConflict
		}
		writeError(w, status, userError(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleCluster is the M6 Cluster read-model: system.replication (Raft
// membership/leader/voters/applied LSN/maintenance-mode) and
// system.replica_health (per-node role/lag/health). Both are always-visible
// system.* tables — no admin gating needed for the read side (the actions
// below are the gated part).
func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "replication", sql: "SELECT * FROM system.replication", required: true},
		{key: "replica_health", sql: "SELECT * FROM system.replica_health", required: true},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	out := map[string]any{
		"generated_at":   b.GeneratedAt,
		"replication":    b.Tables["replication"],
		"replica_health": b.Tables["replica_health"],
		"clustered":      clusteredFrom(b.Tables["replication"]),
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

const maxDrainTimeoutMS = int64(24 * time.Hour / time.Millisecond)

type clusterActionRequest struct {
	Action    string `json:"action"`
	TimeoutMS int64  `json:"timeout_ms"`
}

// clusterActionSQL renders one of the M6 action requests to the exact SQL
// statement text docs/sql.md documents — never a private code path. Every
// one of these already requires ADMIN ON CLUSTER server-side
// (internal/executor/security.go's authorize), so the handler using this
// adds no RBAC of its own; the interpolated timeout is validated as a
// bounded non-negative integer first, so the string build is never
// attacker-controlled text.
func clusterActionSQL(action string, timeoutMS int64) (string, error) {
	const op = "manager.clusterAction"
	switch action {
	case "transfer_leader":
		return "CLUSTER TRANSFER LEADER", nil
	case "drain":
		if timeoutMS < 0 || timeoutMS > maxDrainTimeoutMS {
			return "", nerr.New(nerr.InvalidArgument, op, "timeout_ms must be between 0 and 86400000 (24h)")
		}
		if timeoutMS == 0 {
			return "CLUSTER DRAIN", nil
		}
		return fmt.Sprintf("CLUSTER DRAIN WITH (TIMEOUT_MS = %d)", timeoutMS), nil
	case "maintenance_enable":
		return "CLUSTER MAINTENANCE ENABLE", nil
	case "maintenance_disable":
		return "CLUSTER MAINTENANCE DISABLE", nil
	case "reconcile_confirm":
		return "CLUSTER RECONCILE CONFIRM", nil
	default:
		return "", nerr.New(nerr.InvalidArgument, op, "unknown cluster action")
	}
}

// handleClusterAction issues one M6 cluster admin statement on the
// operator's own connection and returns its single-row acknowledgment
// (e.g. {"columns":["result"],"rows":[["drain_initiated"]]}) — the same
// shape every other read-model table uses, so the frontend needs no special
// case to render it.
func (s *Server) handleClusterAction(w http.ResponseWriter, r *http.Request, sess *session) {
	var req clusterActionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxActionBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sql, err := clusterActionSQL(req.Action, req.TimeoutMS)
	if err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := sess.query(ctx, sql)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case nerr.HasCode(err, nerr.Unauthorized):
			status = http.StatusForbidden
		case nerr.HasCode(err, nerr.InvalidArgument), nerr.HasCode(err, nerr.Unavailable):
			status = http.StatusConflict
		}
		writeError(w, status, userError(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleMaintenance is the M7 Maintenance read-model: system.tables and
// system.indexes (both table-visibility filtered, same as M2's Databases
// view) plus system.table_stats/system.index_stats (row counts an operator
// uses to judge whether ANALYZE/REBUILD INDEX/MAINTAIN is warranted).
func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "tables", sql: "SELECT * FROM system.tables", required: true},
		{key: "indexes", sql: "SELECT * FROM system.indexes", required: true},
		{key: "table_stats", sql: "SELECT * FROM system.table_stats"},
		{key: "index_stats", sql: "SELECT * FROM system.index_stats"},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	out := map[string]any{
		"generated_at": b.GeneratedAt,
		"tables":       b.Tables["tables"],
		"indexes":      b.Tables["indexes"],
		"table_stats":  b.Tables["table_stats"],
		"index_stats":  b.Tables["index_stats"],
	}
	if len(b.Warnings) > 0 {
		out["warnings"] = b.Warnings
	}
	writeJSON(w, http.StatusOK, out)
}

// identPattern matches exactly what internal/sql/lexer accepts as a bare
// identifier (isIdentStart/isIdentPart) — no quoting exists in this SQL
// dialect, so this is the only gate between a JSON "target" string and text
// interpolated into a hand-built SQL statement. Anything that doesn't match
// this can't be a valid table/index name anyway (the server would reject it
// as a syntax error or NotFound), so rejecting it here first keeps the
// interpolation itself provably safe rather than relying on the server to
// catch a malformed statement after the fact.
var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func validIdent(s string) bool { return identPattern.MatchString(s) }

type maintenanceActionRequest struct {
	Op     string `json:"op"`     // "analyze" | "rebuild_index" | "maintain"
	Target string `json:"target"` // table name (analyze/maintain table scope) or index name (rebuild_index/maintain index scope); empty for analyze-whole-database or maintain-database-scope
	Scope  string `json:"scope"`  // "maintain" only: "database" | "table" | "index"
	Online bool   `json:"online"` // "rebuild_index" only: REBUILD INDEX ... ONLINE
}

// maintenanceActionSQL renders one M7 action request to the exact SQL
// statement text docs/sql.md documents — never a private code path. ANALYZE
// requires SELECT (table or database scope); REBUILD INDEX requires INDEX
// on the resolved table; MAINTAIN requires ADMIN ON CLUSTER — all already
// enforced server-side (internal/executor/security.go), so this handler
// adds no RBAC of its own.
func maintenanceActionSQL(op, target, scope string, online bool) (string, error) {
	const opName = "manager.maintenanceAction"
	switch op {
	case "analyze":
		if target == "" {
			return "ANALYZE", nil
		}
		if !validIdent(target) {
			return "", nerr.New(nerr.InvalidArgument, opName, "target must be a valid table name")
		}
		return "ANALYZE " + target, nil
	case "rebuild_index":
		if !validIdent(target) {
			return "", nerr.New(nerr.InvalidArgument, opName, "target must be a valid index name")
		}
		sql := "REBUILD INDEX " + target
		if online {
			sql += " ONLINE"
		}
		return sql, nil
	case "maintain":
		switch scope {
		case "database":
			return "MAINTAIN DATABASE", nil
		case "table":
			if !validIdent(target) {
				return "", nerr.New(nerr.InvalidArgument, opName, "target must be a valid table name")
			}
			return "MAINTAIN TABLE " + target, nil
		case "index":
			if !validIdent(target) {
				return "", nerr.New(nerr.InvalidArgument, opName, "target must be a valid index name")
			}
			return "MAINTAIN INDEX " + target, nil
		default:
			return "", nerr.New(nerr.InvalidArgument, opName, "scope must be database, table, or index")
		}
	default:
		return "", nerr.New(nerr.InvalidArgument, opName, "unknown maintenance op")
	}
}

// handleMaintenanceAction issues one M7 maintenance statement on the
// operator's own connection. The 30s context matches the Server's own
// http.Server.WriteTimeout ceiling (server.go) — a REBUILD INDEX or MAINTAIN
// pass expected to run longer than that (a very large table/index) needs
// `nextsql exec` today, not the Manager; ANALYZE/MAINTAIN's own 10,000-
// tombstone-per-statement cap keeps the common case well under it.
func (s *Server) handleMaintenanceAction(w http.ResponseWriter, r *http.Request, sess *session) {
	var req maintenanceActionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxActionBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sql, err := maintenanceActionSQL(req.Op, strings.TrimSpace(req.Target), req.Scope, req.Online)
	if err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := sess.query(ctx, sql)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case nerr.HasCode(err, nerr.Unauthorized):
			status = http.StatusForbidden
		case nerr.HasCode(err, nerr.NotFound):
			status = http.StatusNotFound
		case nerr.HasCode(err, nerr.InvalidArgument), nerr.HasCode(err, nerr.Unavailable):
			status = http.StatusConflict
		}
		writeError(w, status, userError(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// clusteredFrom reads the single system.replication row: a standalone node
// reports state "single"; anything else is a real Raft state.
func clusteredFrom(repl resultJSON) bool {
	if len(repl.Rows) == 1 && len(repl.Rows[0]) >= 2 && repl.Rows[0][1] != nil {
		return !strings.EqualFold(*repl.Rows[0][1], "single")
	}
	return false
}

// ---- helpers ----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeBundleError renders a failed required-query from a read-model bundle.
// An authorization failure is the operator's own RBAC, surfaced honestly.
func writeBundleError(w http.ResponseWriter, err error) {
	source := "system.*"
	var be *bundleError
	if errors.As(err, &be) {
		source = "system." + be.source
	}
	status := http.StatusBadGateway
	if nerr.HasCode(err, nerr.Unauthorized) {
		status = http.StatusForbidden
	}
	writeError(w, status, "querying "+source+": "+userError(err))
}

func userError(err error) string {
	var ne *nerr.Error
	if errors.As(err, &ne) {
		return ne.Error()
	}
	return err.Error()
}

func loginErrorStatus(err error) (int, string) {
	switch {
	case nerr.HasCode(err, nerr.Unauthorized):
		return http.StatusUnauthorized, "authentication failed"
	case nerr.HasCode(err, nerr.IO), nerr.HasCode(err, nerr.Unavailable):
		return http.StatusBadGateway, "cannot reach nextsqld"
	case nerr.HasCode(err, nerr.InvalidArgument):
		return http.StatusBadRequest, userError(err)
	default:
		return http.StatusBadGateway, "login failed: " + userError(err)
	}
}

func setSessionCookie(w http.ResponseWriter, id string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
