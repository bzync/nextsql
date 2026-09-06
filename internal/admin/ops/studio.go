package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/admin/studio"
	"github.com/bzync/nextsql/internal/nerr"
)

// maxStudioRequestBody caps every JSON Studio request body: the 1 MiB SQL
// ceiling, plus headroom for a fully-bound parameter set on the query routes,
// plus a small JSON framing margin.
const maxStudioRequestBody = studio.MaxSQLBytes + studio.MaxQueryParams*studio.MaxQueryParamBytes + 4096

// handleStudioBootstrap is the bounded, capability-aware first read for the
// development workspace. The table list is capped; columns and indexes are
// intentionally omitted until handleStudioTable is called for one selection.
func (s *Server) handleStudioBootstrap(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "capabilities", sql: "SELECT name, status, description, since_version FROM system.capabilities ORDER BY name", required: true},
		{key: "tables", sql: fmt.Sprintf("SELECT name, column_count, pk FROM system.tables ORDER BY name LIMIT %d", studio.MaxTables+1), required: true},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	tables := b.Tables["tables"]
	truncated := len(tables.Rows) > studio.MaxTables
	if truncated {
		tables.Rows = tables.Rows[:studio.MaxTables]
	}
	readMode, readMaxMS := sess.readConsistency()
	writeJSON(w, http.StatusOK, studio.Bootstrap{
		GeneratedAt:     b.GeneratedAt,
		ServerAddr:      s.cfg.ServerAddr,
		ReadConsistency: readMode,
		MaxStalenessMS:  readMaxMS,
		Capabilities:    studioResult(b.Tables["capabilities"]),
		Tables:          studioResult(tables),
		TablesTruncated: truncated,
		Warnings:        b.Warnings,
	})
}

// handleStudioReadConsistency changes how the current session's reads observe
// replicated state (STRONG / BOUNDED / STALE). This is a live session-control
// message on the existing connection — no reconnect, no credential — and it
// affects reads only; writes still go to the leader. nextsqld remains the
// authority over whether a routed read is actually served from a follower.
func (s *Server) handleStudioReadConsistency(w http.ResponseWriter, r *http.Request, sess *session) {
	var req studio.SetReadConsistencyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxLoginBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Mode = strings.TrimSpace(strings.ToLower(req.Mode))
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var mode nextsql.ReadConsistency
	switch req.Mode {
	case studio.ReadStrong:
		mode = nextsql.Strong
	case studio.ReadBounded:
		mode = nextsql.Bounded
	case studio.ReadStale:
		mode = nextsql.Stale
	}
	maxStaleness := time.Duration(req.MaxStalenessMS) * time.Millisecond
	if req.Mode != studio.ReadBounded {
		maxStaleness, req.MaxStalenessMS = 0, 0
	}
	if err := sess.setReadConsistency(mode, maxStaleness, req.Mode, req.MaxStalenessMS); err != nil {
		writeBundleError(w, err)
		return
	}
	sess.touch()
	writeJSON(w, http.StatusOK, studio.ReadConsistencyState{Mode: req.Mode, MaxStalenessMS: req.MaxStalenessMS})
}

// handleStudioReconnect re-targets the current session's connection to a
// different realm/database on the same nextsqld, as the same NSQL user. It
// opens a fresh authenticated connection first (NextSQL binds realm/database
// only at handshake), and swaps it in only on success — a failed switch
// (wrong password, unknown realm, suspended database) leaves the session on
// its existing connection. The password is used once and never stored.
func (s *Server) handleStudioReconnect(w http.ResponseWriter, r *http.Request, sess *session) {
	var req studio.ReconnectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxLoginBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Realm = strings.TrimSpace(req.Realm)
	req.Database = strings.TrimSpace(req.Database)
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	base, err := s.cfg.driverConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ops TLS configuration error")
		s.log.Error("studio reconnect driver config", "err", err.Error())
		return
	}
	base.User = sess.user
	base.Password = req.Password
	base.Database = req.Database
	base.Realm = req.Realm

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	conn, err := nextsql.OpenContext(ctx, base)
	if err != nil {
		status, msg := loginErrorStatus(err)
		writeError(w, status, msg)
		s.log.Info("studio reconnect failed", "user", sess.user, "status", status)
		return
	}
	if err := sess.reconnect(conn, req.Realm, req.Database); err != nil {
		_ = conn.Close()
		writeBundleError(w, err)
		return
	}
	sess.touch()
	s.log.Info("studio reconnect", "user", sess.user, "realm", req.Realm, "database", req.Database)
	writeJSON(w, http.StatusOK, studio.Connection{
		User: sess.user, Realm: req.Realm, Database: req.Database,
	})
}

// handleStudioTable lazy-loads the server-authorized metadata for one table.
// NextSQL identifiers are bare and bounded; validating the exact lexer shape
// before interpolation makes every generated SQL literal here inert.
func (s *Server) handleStudioTable(w http.ResponseWriter, r *http.Request, sess *session) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if !validIdent(name) {
		writeError(w, http.StatusBadRequest, "name must be a valid table identifier")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	quoted := "'" + name + "'"
	b, err := runBundle(ctx, sess, []querySpec{
		{key: "table", sql: "SELECT * FROM system.tables WHERE name = " + quoted, required: true},
		{key: "columns", sql: "SELECT * FROM system.columns WHERE table_name = " + quoted + " ORDER BY ordinal", required: true},
		{key: "indexes", sql: "SELECT * FROM system.indexes WHERE table_name = " + quoted + " ORDER BY index_name", required: true},
		{key: "foreign_keys", sql: "SELECT * FROM system.foreign_keys WHERE table_name = " + quoted + " ORDER BY constraint_name, ordinal"},
		{key: "referencing_keys", sql: "SELECT * FROM system.foreign_keys WHERE ref_table = " + quoted + " ORDER BY table_name, constraint_name, ordinal"},
		{key: "triggers", sql: "SELECT * FROM system.triggers WHERE table_name = " + quoted + " ORDER BY name"},
		{key: "table_stats", sql: "SELECT * FROM system.table_stats WHERE table_name = " + quoted},
		{key: "index_stats", sql: "SELECT * FROM system.index_stats WHERE table_name = " + quoted + " ORDER BY index_name"},
		{key: "ddl", sql: "SELECT object_type, object_name, ddl FROM system.table_ddl WHERE table_name = " + quoted + " ORDER BY object_type DESC, object_name"},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	if len(b.Tables["table"].Rows) == 0 {
		writeError(w, http.StatusNotFound, "table is not visible or does not exist")
		return
	}
	writeJSON(w, http.StatusOK, studio.TableDetail{
		GeneratedAt:     b.GeneratedAt,
		Name:            name,
		Table:           studioResult(b.Tables["table"]),
		Columns:         studioResult(b.Tables["columns"]),
		Indexes:         studioResult(b.Tables["indexes"]),
		ForeignKeys:     studioResult(b.Tables["foreign_keys"]),
		ReferencingKeys: studioResult(b.Tables["referencing_keys"]),
		Triggers:        studioResult(b.Tables["triggers"]),
		TableStats:      studioResult(b.Tables["table_stats"]),
		IndexStats:      studioResult(b.Tables["index_stats"]),
		DDL:             studioResult(b.Tables["ddl"]),
		Warnings:        b.Warnings,
	})
}

// handleStudioWorkflows is the read-only Workflows, triggers, schedules,
// tasks & change streams explorer bundle. It adds no new server capability:
// it runs the same authorized SELECTs against system.* that any editor
// statement could, through the logged-in operator's own NSQL connection, so
// the system catalog's RBAC filtering stays the sole authority for what is
// visible. Every list except workflows is legitimately empty, so those reads
// are not required. Every result is independently capped before it reaches
// the browser.
func (s *Server) handleStudioWorkflows(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "workflows", sql: fmt.Sprintf(
			"SELECT * FROM system.workflows ORDER BY name LIMIT %d",
			studio.WorkflowOverviewMaxRows+1), required: true},
		{key: "triggers", sql: fmt.Sprintf(
			"SELECT * FROM system.triggers ORDER BY name LIMIT %d",
			studio.WorkflowOverviewMaxRows+1)},
		{key: "schedules", sql: fmt.Sprintf(
			"SELECT * FROM system.schedules ORDER BY name LIMIT %d",
			studio.WorkflowOverviewMaxRows+1)},
		{key: "tasks", sql: fmt.Sprintf(
			"SELECT * FROM system.tasks ORDER BY id LIMIT %d",
			studio.WorkflowOverviewMaxRows+1)},
		{key: "change_streams", sql: fmt.Sprintf(
			"SELECT * FROM system.change_streams ORDER BY table_name, lsn LIMIT %d",
			studio.WorkflowOverviewMaxRows+1)},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, studio.WorkflowOverview{
		GeneratedAt:   b.GeneratedAt,
		Workflows:     cappedStudioResult(b.Tables["workflows"], studio.WorkflowOverviewMaxRows),
		Triggers:      cappedStudioResult(b.Tables["triggers"], studio.WorkflowOverviewMaxRows),
		Schedules:     cappedStudioResult(b.Tables["schedules"], studio.WorkflowOverviewMaxRows),
		Tasks:         cappedStudioResult(b.Tables["tasks"], studio.WorkflowOverviewMaxRows),
		ChangeStreams: cappedStudioResult(b.Tables["change_streams"], studio.WorkflowOverviewMaxRows),
		Warnings:      b.Warnings,
	})
}

// handleStudioSchemaGraph is the read-only schema-relationship bundle: every
// visible system.foreign_keys row, for the Studio schema diagram. It adds no
// new server capability — the same authorized SELECT any editor statement
// could run — so the system catalog's RBAC filtering (a row is visible only
// when the caller can SELECT its child table) stays the sole authority.
func (s *Server) handleStudioSchemaGraph(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "foreign_keys", sql: fmt.Sprintf(
			"SELECT * FROM system.foreign_keys ORDER BY ref_table, table_name, constraint_name, ordinal LIMIT %d",
			studio.SchemaGraphMaxRows+1), required: true},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	fks := b.Tables["foreign_keys"]
	truncated := len(fks.Rows) > studio.SchemaGraphMaxRows
	if truncated {
		fks.Rows = fks.Rows[:studio.SchemaGraphMaxRows]
	}
	writeJSON(w, http.StatusOK, studio.SchemaGraph{
		GeneratedAt: b.GeneratedAt,
		ForeignKeys: studioResult(fks),
		Truncated:   truncated,
		Warnings:    b.Warnings,
	})
}

// handleStudioMigrations is the read-only schema-migration history explorer. It
// adds no new server capability: it runs one authorized SELECT against the
// reserved `nsql_schema_migrations` table through the logged-in operator's own
// NSQL connection, so that table's own SELECT privilege stays the sole
// authority. The table is absent until the official migration system first runs
// on a database, so the read is not `required` — an absent or invisible table
// yields present=false with the reason in warnings, not an error. Authoring,
// validating and applying migrations stays with the `nextsql migrate` CLI,
// which needs the local migration files this protocol-only client never holds.
func (s *Server) handleStudioMigrations(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	b, err := runBundle(ctx, sess, []querySpec{
		{key: "history", sql: fmt.Sprintf(
			"SELECT version, name, applied_at, execution_ms, dirty, direction, checksum "+
				"FROM nsql_schema_migrations ORDER BY version LIMIT %d",
			studio.MigrationHistoryMaxRows+1)},
	})
	if err != nil {
		writeBundleError(w, err)
		return
	}
	hist := b.Tables["history"]
	truncated := len(hist.Rows) > studio.MigrationHistoryMaxRows
	if truncated {
		hist.Rows = hist.Rows[:studio.MigrationHistoryMaxRows]
	}
	writeJSON(w, http.StatusOK, studio.MigrationHistory{
		GeneratedAt: b.GeneratedAt,
		Present:     len(b.Warnings) == 0,
		History:     studioResult(hist),
		Truncated:   truncated,
		Warnings:    b.Warnings,
	})
}

// handleStudioAnalyze classifies one editor statement for a confirm-before-run
// warning without opening a driver connection or touching the current
// session's query slot. It requires the same authenticated session as every
// other Studio route but has no other side effect.
func (s *Server) handleStudioAnalyze(w http.ResponseWriter, r *http.Request, sess *session) {
	var req studio.AnalyzeRequest
	if err := decodeStudioJSON(w, r, &req, maxStudioRequestBody); err != nil {
		writeError(w, studioDecodeErrorStatus(err), userError(err))
		return
	}
	analysis, err := studio.Analyze(req.SQL)
	if err != nil {
		status := http.StatusBadRequest
		if nerr.HasCode(err, nerr.Exhausted) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, userError(err))
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}

// handleStudioQuery executes one editor statement on the logged-in user's
// official-driver connection. The session registers QueryID before I/O so
// the sibling cancellation endpoint can interrupt it while this request is
// blocked in the driver. Server-side parser/binder/RBAC remain authoritative.
func (s *Server) handleStudioQuery(w http.ResponseWriter, r *http.Request, sess *session) {
	var req studio.QueryRequest
	if err := decodeStudioJSON(w, r, &req, maxStudioRequestBody); err != nil {
		writeError(w, studioDecodeErrorStatus(err), userError(err))
		return
	}
	if err := studio.ValidateQueryID(req.QueryID); err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}
	if err := studio.ValidateSQL(req.SQL); err != nil {
		status := http.StatusBadRequest
		if nerr.HasCode(err, nerr.Exhausted) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, userError(err))
		return
	}
	if err := studio.ValidateParams(req.Params); err != nil {
		status := http.StatusBadRequest
		if nerr.HasCode(err, nerr.Exhausted) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, userError(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), studio.QueryTimeout)
	defer cancel()
	result, err := sess.studioQuery(ctx, req.QueryID, req.SQL, studio.ParamValues(req.Params))
	if err != nil {
		writeError(w, studioErrorStatus(err), userError(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleStudioQueryStream executes the same bounded query contract as
// handleStudioQuery, but sends metadata, row batches, and completion as
// newline-delimited JSON. No whole-result object is retained by Admin. A
// query error before metadata keeps the ordinary HTTP error status; an error
// after streaming starts is an explicit terminal error frame because the
// HTTP status has already been committed.
func (s *Server) handleStudioQueryStream(w http.ResponseWriter, r *http.Request, sess *session) {
	var req studio.QueryRequest
	if err := decodeStudioJSON(w, r, &req, maxStudioRequestBody); err != nil {
		writeError(w, studioDecodeErrorStatus(err), userError(err))
		return
	}
	if err := studio.ValidateQueryID(req.QueryID); err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}
	if err := studio.ValidateSQL(req.SQL); err != nil {
		status := http.StatusBadRequest
		if nerr.HasCode(err, nerr.Exhausted) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, userError(err))
		return
	}
	if err := studio.ValidateParams(req.Params); err != nil {
		status := http.StatusBadRequest
		if nerr.HasCode(err, nerr.Exhausted) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, userError(err))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), studio.QueryTimeout)
	defer cancel()

	startedAt := time.Now()
	started := false
	var writeErr error
	enc := json.NewEncoder(w)
	emit := func(frame studio.StreamFrame) error {
		if !started {
			w.Header().Set("Content-Type", studio.StreamContentType)
			w.WriteHeader(http.StatusOK)
			started = true
		}
		if err := enc.Encode(frame); err != nil {
			writeErr = err
			return err
		}
		flusher.Flush()
		return nil
	}

	err := sess.studioStreamQuery(ctx, req.QueryID, req.SQL, studio.ParamValues(req.Params), emit)
	if err == nil || writeErr != nil {
		return
	}
	if !started {
		writeError(w, studioErrorStatus(err), userError(err))
		return
	}
	_ = emit(studio.StreamFrame{
		Type:      "error",
		Error:     userError(err),
		ErrorCode: studioStreamErrorCode(err),
		ElapsedMS: time.Since(startedAt).Milliseconds(),
	})
}

// handleStudioSplit tokenizes an Execute Script buffer into individually
// runnable statements without opening a driver connection or touching the
// session's query slot — the same trust level as handleStudioAnalyze.
func (s *Server) handleStudioSplit(w http.ResponseWriter, r *http.Request, sess *session) {
	var req studio.AnalyzeRequest
	if err := decodeStudioJSON(w, r, &req, maxStudioRequestBody); err != nil {
		writeError(w, studioDecodeErrorStatus(err), userError(err))
		return
	}
	statements, err := studio.SplitScript(req.SQL)
	if err != nil {
		status := http.StatusBadRequest
		if nerr.HasCode(err, nerr.Exhausted) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, userError(err))
		return
	}
	writeJSON(w, http.StatusOK, studio.Script{Statements: statements})
}

func (s *Server) handleStudioCancel(w http.ResponseWriter, r *http.Request, sess *session) {
	var req studio.CancelRequest
	if err := decodeStudioJSON(w, r, &req, 4<<10); err != nil {
		writeError(w, studioDecodeErrorStatus(err), userError(err))
		return
	}
	if err := studio.ValidateQueryID(req.QueryID); err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}
	if !sess.cancelStudioQuery(req.QueryID) {
		writeError(w, http.StatusNotFound, "query is not active in this session")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"canceled": true, "query_id": req.QueryID})
}

func studioResult(in resultJSON) studio.ResultSet {
	return studio.ResultSet{
		Columns:     append([]string{}, in.Columns...),
		ColumnTypes: append([]string{}, in.columnTypes...),
		Rows:        in.Rows,
		Affected:    in.Affected,
	}
}

func cappedStudioResult(in resultJSON, limit int) studio.ResultSet {
	truncated := limit >= 0 && len(in.Rows) > limit
	if truncated {
		in.Rows = in.Rows[:limit]
	}
	out := studioResult(in)
	out.Truncated = truncated
	return out
}

func decodeStudioJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nerr.New(nerr.Exhausted, "ops.decodeStudioJSON",
				fmt.Sprintf("request body exceeds %d bytes", limit))
		}
		return errors.New("invalid JSON body")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func studioDecodeErrorStatus(err error) int {
	if nerr.HasCode(err, nerr.Exhausted) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func studioErrorStatus(err error) int {
	switch {
	case nerr.HasCode(err, nerr.Unauthorized), nerr.HasCode(err, nerr.Forbidden):
		return http.StatusForbidden
	case nerr.HasCode(err, nerr.InvalidArgument), nerr.HasCode(err, nerr.Syntax):
		return http.StatusBadRequest
	case nerr.HasCode(err, nerr.NotFound):
		return http.StatusNotFound
	case nerr.HasCode(err, nerr.Conflict), nerr.HasCode(err, nerr.Deadlock), nerr.HasCode(err, nerr.Serialization):
		return http.StatusConflict
	case nerr.HasCode(err, nerr.Exhausted):
		return http.StatusRequestEntityTooLarge
	case nerr.HasCode(err, nerr.Canceled), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout
	default:
		return http.StatusBadGateway
	}
}

func studioStreamErrorCode(err error) string {
	var ne *nerr.Error
	if errors.As(err, &ne) {
		return string(ne.Code)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return string(nerr.Canceled)
	}
	return string(nerr.Internal)
}
