package system

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/version"
)

// Version is the system schema version for machine consumers.
// Deterministic and monotonic; bump only with column changes.
//
// v3 (Phase 28, Manager MVP): system.config gained file_value /
// restart_required (M8); new tables system.metrics (M9), system.server_log
// (M9), system.backups (M5); the M4 security tables (system.tls,
// system.key_versions, system.audit_verify, system.audit_log) and M8's
// system.config were added under v2 without a bump — v3 covers all of it.
const SchemaVersion = 3

// SchemaName is the virtual schema name.
const SchemaName = "system"

var tables = map[string]*catalog.Table{}

func init() {
	register("capabilities", []catalog.Column{
		{Name: "name", Type: types.String()},
		{Name: "status", Type: types.String()},
		{Name: "description", Type: types.String()},
		{Name: "since_version", Type: types.String()},
	})
	register("tables", []catalog.Column{
		{Name: "name", Type: types.String()},
		{Name: "id", Type: dec(10, 0)},
		{Name: "column_count", Type: dec(10, 0)},
		{Name: "pk", Type: types.String()},
		{Name: "legacy_tenant_column", Type: types.String()},
	})
	register("columns", []catalog.Column{
		{Name: "table_name", Type: types.String()},
		{Name: "column_name", Type: types.String()},
		{Name: "ordinal", Type: dec(10, 0)},
		{Name: "type", Type: types.String()},
		{Name: "not_null", Type: types.Bool()},
		{Name: "is_primary", Type: types.Bool()},
		{Name: "default_value", Type: types.String()},
	})
	register("indexes", []catalog.Column{
		{Name: "table_name", Type: types.String()},
		{Name: "index_name", Type: types.String()},
		{Name: "kind", Type: types.String()},
		{Name: "is_unique", Type: types.Bool()},
		{Name: "columns", Type: types.String()},
		{Name: "include_columns", Type: types.String()},
		{Name: "predicate", Type: types.String()},
		{Name: "status", Type: types.String()},
	})
	register("storage", []catalog.Column{
		{Name: "database", Type: types.String()},
		{Name: "engine", Type: types.String()},
		{Name: "page_size", Type: dec(10, 0)},
		{Name: "page_count", Type: dec(20, 0)},
		{Name: "file_size", Type: dec(20, 0)},
		{Name: "wal_lsn", Type: dec(20, 0)},
		{Name: "encryption", Type: types.String()},
	})
	register("replication", []catalog.Column{
		{Name: "node_id", Type: types.String()},
		{Name: "state", Type: types.String()},
		{Name: "leader_id", Type: types.String()},
		{Name: "leader_addr", Type: types.String()},
		{Name: "voters", Type: dec(10, 0)},
		{Name: "applied_lsn", Type: dec(20, 0)},
		{Name: "has_leader", Type: types.Bool()},
		{Name: "maintenance_mode", Type: types.Bool()},
	})
	// realms and databases (M2-4a) expose the hosted deployment registry
	// (internal/hosting.Registry.Manifest) read-only. Admin-only, like
	// system.resource_groups: deployment structure across realms is not
	// tenant-visible data. Empty on a legacy/non-hosted deployment (no
	// registry attached) or for a non-admin caller, never an error.
	register("realms", []catalog.Column{
		{Name: "realm_id", Type: types.String()},
		{Name: "name", Type: types.String()},
		{Name: "state", Type: types.String()},
		{Name: "database_count", Type: dec(10, 0)},
		{Name: "storage_cap_bytes", Type: dec(20, 0)},
		{Name: "realm_root_delegated", Type: types.Bool()},
	})
	register("databases", []catalog.Column{
		{Name: "realm_id", Type: types.String()},
		{Name: "realm_name", Type: types.String()},
		{Name: "database_id", Type: types.String()},
		{Name: "name", Type: types.String()},
		{Name: "state", Type: types.String()},
		{Name: "layout", Type: types.String()},
		{Name: "storage_cap_bytes", Type: dec(20, 0)},
	})
	register("replica_health", []catalog.Column{
		{Name: "node_id", Type: types.String()},
		{Name: "role", Type: types.String()},
		{Name: "has_leader", Type: types.Bool()},
		{Name: "applied_lsn", Type: dec(20, 0)},
		{Name: "commit_index", Type: dec(20, 0)},
		{Name: "applied_index", Type: dec(20, 0)},
		{Name: "apply_backlog", Type: dec(20, 0)},
		{Name: "last_contact_ms", Type: dec(20, 0)},
		{Name: "healthy", Type: types.Bool()},
		{Name: "replication_suspect", Type: types.Bool()},
	})
	// tls (M4 remainder) exposes the live listener's redacted TLS status —
	// leaf certificate identity/validity and the mTLS/CRL posture, never
	// key material and never a network address (same convention as
	// system.replication.leader_addr). Admin-only, like system.users/
	// roles/grants: a non-admin gets zero rows. enabled=false with the
	// other columns empty on a loopback plaintext deployment or embedded/
	// CLI use, never an error — same "always one descriptive row" shape as
	// system.replication reporting "standalone".
	register("tls", []catalog.Column{
		{Name: "enabled", Type: types.Bool()},
		{Name: "subject", Type: types.String()},
		{Name: "issuer", Type: types.String()},
		{Name: "not_before", Type: types.TimestampTZ()},
		{Name: "not_after", Type: types.TimestampTZ()},
		{Name: "days_until_expiry", Type: dec(10, 0)},
		{Name: "dns_names", Type: types.String()},
		{Name: "mtls_required", Type: types.Bool()},
		{Name: "client_ca_configured", Type: types.Bool()},
		{Name: "client_crl_configured", Type: types.Bool()},
	})
	// key_versions (M4 remainder) exposes the attached crypto.Envelope's
	// per-key rotation state — current version and retained/revoked/retired
	// counts — never key material. Admin-only, like system.tls/users/roles/
	// grants. Zero rows (never an error) when no persistent envelope is
	// attached: embedded/CLI use with a bare crypto.KeyProvider, or a
	// legacy deployment with no .keys keystore file — same "empty means not
	// applicable" convention as system.databases/realms on a non-hosted
	// deployment, chosen over system.tls's "always one row" shape because
	// this table is a list of keys, not a single status fact.
	register("key_versions", []catalog.Column{
		{Name: "key_name", Type: types.String()},
		{Name: "current_version", Type: dec(10, 0)},
		{Name: "version_count", Type: dec(10, 0)},
		{Name: "revoked_count", Type: dec(10, 0)},
		{Name: "retired_count", Type: dec(10, 0)},
	})
	// config (M8 Configuration viewer, read-only) exposes the running
	// process's config.Config, redacted (config.Config.SafeEntries) — every
	// network-address-shaped value replaced with "[redacted]" (same
	// convention as system.replication.leader_addr); nothing in Config ever
	// holds key material to begin with. Admin-only, like system.tls/
	// key_versions/users/roles/grants. Zero rows (list-shaped, never an
	// error) when no process-level config.Config is attached — embedded/CLI
	// use, same convention as system.key_versions with no envelope
	// attached. Only settings that differ from Config's own Default() (or
	// that Default() itself sets) appear — a field left at its zero value
	// is omitted entirely, not shown as "0"/""; see config.Config.Marshal's
	// own doc comment, which SafeEntries reuses verbatim. Column is named
	// "name", not "key": KEY is a reserved word in this dialect's grammar
	// (PRIMARY KEY/FOREIGN KEY) and there is no quoted-identifier syntax to
	// escape it with, so "SELECT key FROM ..."/"ORDER BY key" could never
	// parse.
	register("config", []catalog.Column{
		{Name: "name", Type: types.String()},
		{Name: "value", Type: types.String()},
		// file_value is the setting's value in the node's on-disk
		// nextsql.conf; restart_required ("yes"/"no") is set when it
		// differs from the running "value" — either because SET CONFIG
		// persisted a change not yet applied, or because a startup flag
		// overrode the file. Every SET CONFIG write is persist-only today.
		{Name: "file_value", Type: types.String()},
		{Name: "restart_required", Type: types.String()},
	})
	// audit_verify and audit_log (M4's last piece) close the Manager
	// Security view's audit-chain viewer, over security.TailEvents — a
	// bounded, chain-verified re-read of the live audit file, on every
	// query (unlike the in-memory tls/key_versions/config sources above,
	// there is nothing to cache: the whole point is what's actually
	// durable on disk right now). Both are admin-only.
	//
	// audit_verify is a single status fact, same "always exactly one row"
	// shape as system.tls: verified=false with zero counts when no audit
	// log is attached (embedded/CLI use), or when the file could not even
	// be read (in that case problem carries the read error, not a chain
	// finding — checked at the executor layer, not distinguishable from a
	// column here).
	register("audit_verify", []catalog.Column{
		{Name: "lines", Type: dec(20, 0)},
		{Name: "legacy_count", Type: dec(20, 0)},
		{Name: "chained_count", Type: dec(20, 0)},
		{Name: "signed_count", Type: dec(20, 0)},
		{Name: "signing_started", Type: types.Bool()},
		{Name: "signatures_checked", Type: types.Bool()},
		{Name: "verified", Type: types.Bool()},
		{Name: "first_bad_line", Type: dec(20, 0)},
		{Name: "problem", Type: types.String()},
	})
	// audit_log is list-shaped, same "zero rows means not applicable"
	// convention as system.key_versions/system.config: the most recent
	// entries (bounded server-side — see executor.systemAuditLogRows),
	// oldest first. Deliberately includes a record even when audit_verify
	// reports the chain broken: an operator investigating a detected
	// problem needs to see the suspect entry, not have it silently hidden
	// (security.TailEvents's own doc comment). Every field here already
	// went through security.Redact/prepareAuditEvent at write time —
	// "Never put passwords, keys, tokens, or secrets" is Event's own
	// contract, not something this table adds; the object field can still
	// legitimately name things like a table or index (DDL), a grantee
	// (grant/revoke), or a database (create/drop) — never a secret value.
	// remote is a client connection address, not server/cluster topology,
	// so the "never expose a network address over SQL" convention
	// (system.replication.leader_addr, system.config's listen_addr/
	// raft_bind/raft_join) does not apply to it — confirmed against
	// existing precedent, not just reasoned from scratch: system.sessions
	// already exposes the identical kind of value in its own "remote"
	// column, unredacted, and source-IP forensics is core audit-log value.
	// Two columns needed renaming off their most natural names, both
	// reserved words in this dialect's own grammar with no quoted-
	// identifier escape, both caught the same way — by actually trying the
	// query against a live built binary before shipping the schema, not by
	// inspecting the lexer's keyword table and assuming it was exhaustive
	// from memory (the same reserved-word pitfall system.config's "name",
	// not "key", column already hit once): "action" (FOREIGN KEY's
	// ON DELETE/UPDATE {CASCADE|RESTRICT|NO ACTION} clause) became
	// "action_name"; "time" (the TIME data type keyword) became
	// "event_time".
	register("audit_log", []catalog.Column{
		{Name: "seq", Type: dec(20, 0)},
		{Name: "event_time", Type: types.TimestampTZ()},
		{Name: "actor", Type: types.String()},
		{Name: "action_name", Type: types.String()},
		{Name: "object", Type: types.String()},
		{Name: "outcome", Type: types.String()},
		{Name: "remote", Type: types.String()},
		{Name: "identity_source", Type: types.String()},
		{Name: "signed", Type: types.Bool()},
	})
	// raft is an alias for replication
	if t, ok := tables["system.replication"]; ok {
		cp := t.Clone()
		cp.Name = "system.raft"
		tables["system.raft"] = cp
	}
	// Live node-local tables: rows come from executor.Session.systemRows
	// (system.go), backed by process-local registries on executor.DB. See
	// docs/system-catalog.md.
	register("active_queries", []catalog.Column{
		{Name: "query_id", Type: types.String()},
		{Name: "user", Type: types.String()},
		{Name: "sql", Type: types.String()},
		{Name: "state", Type: types.String()},
	})
	register("sessions", []catalog.Column{
		{Name: "session_id", Type: types.String()},
		{Name: "user", Type: types.String()},
		{Name: "remote", Type: types.String()},
		{Name: "state", Type: types.String()},
	})
	register("transactions", []catalog.Column{
		{Name: "txn_id", Type: types.String()},
		{Name: "user", Type: types.String()},
		{Name: "isolation", Type: types.String()},
		{Name: "state", Type: types.String()},
	})
	register("locks", []catalog.Column{
		{Name: "lock_id", Type: types.String()},
		{Name: "table_name", Type: types.String()},
		{Name: "mode", Type: types.String()},
		{Name: "granted", Type: types.Bool()},
	})
	register("table_stats", []catalog.Column{
		{Name: "table_name", Type: types.String()},
		{Name: "row_count", Type: dec(20, 0)},
		{Name: "updated_at", Type: types.String()},
	})
	register("index_stats", []catalog.Column{
		{Name: "table_name", Type: types.String()},
		{Name: "index_name", Type: types.String()},
		{Name: "row_count", Type: dec(20, 0)},
	})
	register("workflows", []catalog.Column{
		{Name: "name", Type: types.String()},
		{Name: "owner", Type: types.String()},
		{Name: "param_count", Type: dec(10, 0)},
		{Name: "statement_count", Type: dec(10, 0)},
	})
	register("tasks", []catalog.Column{
		{Name: "id", Type: types.String()},
		{Name: "schedule", Type: types.String()},
		{Name: "workflow", Type: types.String()},
		{Name: "state", Type: types.String()},
		{Name: "attempts", Type: dec(10, 0)},
	})
	register("change_streams", []catalog.Column{
		{Name: "table_name", Type: types.String()},
		{Name: "lsn", Type: dec(20, 0)},
		{Name: "state", Type: types.String()},
	})
	register("partitions", []catalog.Column{
		{Name: "table_name", Type: types.String()},
		{Name: "partition_name", Type: types.String()},
		{Name: "kind", Type: types.String()},
		{Name: "ordinal", Type: dec(10, 0)},
	})
	register("users", []catalog.Column{
		{Name: "name", Type: types.String()},
		{Name: "password_algo", Type: types.String()},
	})
	register("roles", []catalog.Column{
		{Name: "role", Type: types.String()},
		{Name: "members", Type: types.String()},
	})
	register("grants", []catalog.Column{
		{Name: "grantee", Type: types.String()},
		{Name: "privilege", Type: types.String()},
		{Name: "scope", Type: types.String()},
		{Name: "object", Type: types.String()},
	})
	register("resource_groups", []catalog.Column{
		{Name: "name", Type: types.String()},
		{Name: "owner", Type: types.String()},
		{Name: "max_concurrency", Type: dec(10, 0)},
		{Name: "memory_bytes", Type: dec(20, 0)},
		{Name: "workers", Type: dec(10, 0)},
		{Name: "priority", Type: dec(10, 0)},
	})
	// quotas (M3) is the advisory surfacing of the hosting storage caps
	// (docs/design-multidatabase-dbaas.md §10.1). One row per realm and per
	// database from the deployment registry manifest. Admin-only and empty on
	// a legacy/non-hosted deployment, exactly like system.realms/databases.
	// used_bytes / pct_of_cap / over_cap are populated only for the row that
	// matches the session's own connected database (usage_known = true); every
	// other row reports the caps only. Never an error, never a hard limit —
	// the authoritative signal is still the write-path rejection.
	register("quotas", []catalog.Column{
		{Name: "scope", Type: types.String()},
		{Name: "realm_name", Type: types.String()},
		{Name: "database_name", Type: types.String()},
		{Name: "state", Type: types.String()},
		{Name: "cap_bytes", Type: dec(20, 0)},
		{Name: "effective_cap_bytes", Type: dec(20, 0)},
		{Name: "usage_known", Type: types.Bool()},
		{Name: "used_bytes", Type: dec(20, 0)},
		{Name: "pct_of_cap", Type: dec(5, 0)},
		{Name: "over_cap", Type: types.Bool()},
	})
	// metrics (M9) is the process-wide metrics registry (internal/metrics.
	// Snapshot) surfaced read-only for the Manager's Logs & Diagnostics view.
	// Admin-only, list-shaped: zero rows (not a placeholder row) when no
	// process-level registry is attached — embedded/CLI use — same "empty
	// means not applicable" convention as system.config/system.key_versions.
	// Every field is rendered to a decimal string in "value" with a "unit"
	// hint ("count"/"bytes"/"nanoseconds"/"ratio_pct"/"per_second") rather
	// than a wide typed row per counter, the same name/value shape
	// system.config already uses for a heterogeneous key space. "category"
	// groups related counters for display; it is not "group" (a reserved
	// word in this dialect's grammar — GROUP BY — with no quoted-identifier
	// syntax to escape it, the same pitfall system.config's "name" not
	// "key" and system.audit_log's "action_name"/"event_time" already hit).
	// Nothing here is redacted: the registry holds only counters and process
	// resource stats, never addresses, key material, or tenant data.
	register("metrics", []catalog.Column{
		{Name: "category", Type: types.String()},
		{Name: "name", Type: types.String()},
		{Name: "value", Type: types.String()},
		{Name: "unit", Type: types.String()},
	})
	// server_log (M9) is a bounded, in-memory tail of the running process's
	// own structured log (`internal/logging.Ring` — the newest
	// `logging.DefaultRingCapacity` records, capped again per query at
	// `executor.systemServerLogTailCap`), sourced from whatever `nextsqld`
	// wired via `executor.DB.SetServerLogSource`. Admin-only, list-shaped:
	// zero rows when no ring is attached (embedded/CLI use, a bare
	// `logging.New` logger) — same "empty means not applicable" convention
	// as system.metrics/system.config. Memory cost is fixed regardless of
	// how long the process runs; this is a diagnostic tail, not a durable
	// log store (the real log is still stderr/the service journal).
	// `event_time` (not `time` — the `TIME` data-type keyword, the same
	// pitfall system.audit_log's column already hit); every field is exactly
	// what was written to stderr — the "never log keys/passwords/tokens"
	// contract is the logger's callers' responsibility (`logging.New`'s own
	// doc comment), not something this table re-checks. Unlike system.config,
	// this table does **not** redact network addresses: a log message is
	// freeform text (a listen address, a peer that went unreachable, a
	// client that desynced) and an admin diagnosing a connectivity problem
	// needs to see it — the same "privileged reader + real operational
	// value" reasoning that keeps system.audit_log's `remote` unredacted.
	// It never holds anything the process didn't already print to its own
	// stderr / service journal.
	register("server_log", []catalog.Column{
		{Name: "seq", Type: dec(20, 0)},
		{Name: "event_time", Type: types.TimestampTZ()},
		{Name: "level", Type: types.String()},
		{Name: "message", Type: types.String()},
		{Name: "attributes", Type: types.String()},
	})
	// backups (M5) lists the verified backups in the node's configured
	// backup directory (config key `backup_dir`), oldest first — one row per
	// immediate subdirectory of `backup_dir` with a valid backup header
	// (`backup.ListBackups`). Admin/BACKUP-privilege gated. Zero rows when no
	// `backup_dir` is configured (embedded/CLI use, or the key unset) —
	// same "empty means not applicable" convention as `system.config`. The
	// write/verify side is the `BACKUP DATABASE` / `VERIFY BACKUP 'name'`
	// statements (`docs/sql.md`); a driver-only client never names a
	// filesystem path — `name` here is the subdirectory name only.
	register("backups", []catalog.Column{
		{Name: "name", Type: types.String()},
		{Name: "created_at", Type: types.TimestampTZ()},
		{Name: "database_id", Type: types.String()},
		{Name: "checkpoint_lsn", Type: dec(20, 0)},
		{Name: "durable_lsn", Type: dec(20, 0)},
	})
}

func dec(p, s uint16) types.Type { t, _ := types.DecimalType(p, s); return t }

func register(name string, cols []catalog.Column) {
	full := SchemaName + "." + name
	t := &catalog.Table{
		Name:    full,
		Columns: cols,
	}
	tables[full] = t
}

// IsSystemTable reports whether name is a virtual system table (case-folded).
func IsSystemTable(name string) bool {
	_, ok := tables[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// TableFor returns the schema for a system table.
func TableFor(name string) (*catalog.Table, bool) {
	t, ok := tables[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, false
	}
	return t.Clone(), true
}

// List returns all system table names sorted.
func List() []string {
	out := make([]string, 0, len(tables))
	for k := range tables {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Capabilities returns deterministic capability rows sorted by name.
// Columns: name, status, description, since_version
func Capabilities() [][]types.Value {
	// status values: supported, experimental, unsupported, deprecated
	rows := [][]types.Value{
		rowCap("backup", "supported", "encrypted backup and restore", "0.1.0"),
		rowCap("btree", "supported", "clustered B+Tree with MVCC", "0.1.0"),
		rowCap("cdc", "supported", "change data capture streams", "0.1.0"),
		rowCap("covering_indexes", "supported", "INCLUDE covering indexes", "0.1.0"),
		rowCap("distinct", "supported", "SELECT DISTINCT", "0.1.0"),
		rowCap("encryption", "supported", "AES-256-GCM envelope", "0.1.0"),
		rowCap("field_encryption_client", "experimental", "server-opaque randomized ENCRYPTED CLIENT fields; Go, Node.js/TypeScript, Bun, Deno, and PHP helpers", version.String),
		rowCap("expression_indexes", "supported", "expression indexes", "0.1.0"),
		rowCap("foreign_keys", "supported", "FOREIGN KEY constraints", "0.1.0"),
		rowCap("fulltext", "supported", "full-text SEARCH with simple and language analyzers, prefix/fuzzy/typo matching, HIGHLIGHT/SNIPPET, multi-field indexes, per-field WEIGHT, and FACET histograms", "0.1.0"),
		rowCap("geo", "supported", "rich POINT/BOX/LINESTRING/POLYGON operations and spatial index", "0.1.0"),
		rowCap("hybrid_search", "supported", "hybrid SEARCH+NEAREST and dense+sparse+BM25 fusion", "0.1.0"),
		rowCap("hnsw", "supported", "HNSW vector index", "0.1.0"),
		rowCap("json", "supported", "JSON binary and path indexes", "0.1.0"),
		rowCap("locks", "supported", "row-level locking and deadlock detection", "0.1.0"),
		rowCap("maintenance", "supported", "MAINTAIN DATABASE/TABLE/INDEX", "0.1.0"),
		rowCap("mvcc", "supported", "snapshot isolation", "0.1.0"),
		rowCap("partial_indexes", "supported", "partial indexes with predicate", "0.1.0"),
		rowCap("pitr", "supported", "point-in-time recovery from WAL", "0.1.0"),
		rowCap("raft", "supported", "HA replication via Raft", "0.1.0"),
		rowCap("returning", "supported", "INSERT/UPDATE/DELETE RETURNING", "0.1.0"),
		rowCap("result_cache", "supported", "bounded WAL-invalidated SELECT result cache", "0.1.0"),
		rowCap("idempotency", "supported", "durable scoped mutation replay fence", "0.1.0"),
		rowCap("schedules", "supported", "SCHEDULE every/at/cron", "0.1.0"),
		rowCap("set_operations", "supported", "UNION/INTERSECT/EXCEPT", "0.1.0"),
		rowCap("subqueries", "supported", "scalar, IN, EXISTS subqueries", "0.1.0"),
		rowCap("system_catalog", "supported", fmt.Sprintf("virtual system schema v%d", SchemaVersion), version.String),
		rowCap(fmt.Sprintf("system_schema_v%d", SchemaVersion), "supported", fmt.Sprintf("stable system table column contract v%d", SchemaVersion), version.String),
		rowCap("system_show_aliases", "supported", "SHOW aliases backed by canonical system views", version.String),
		rowCap("tasks", "supported", "durable TASK execution", "0.1.0"),
		rowCap("hosting_isolation", "experimental", "realm/database registry foundation; row tenancy removed", version.String),
		rowCap("transactions", "supported", "BEGIN/COMMIT/ROLLBACK", "0.1.0"),
		rowCap("triggers", "supported", "TRIGGER RUN WORKFLOW", "0.1.0"),
		rowCap("upsert", "supported", "UPSERT with RETURNING", "0.1.0"),
		rowCap("vector", "supported", "VECTOR<F32,N> / VECTOR<F16,N> / VECTOR<I8,N> / BITVECTOR<N>, bounded algebra, and NEAREST", "0.1.0"),
		rowCap("vector_ivf", "supported", "CREATE VECTOR INDEX ... USING IVF WITH (LISTS=n[,PROBES=m])", version.String),
		rowCap("vector_ivfpq", "supported", "CREATE VECTOR INDEX ... USING IVFPQ WITH (LISTS=n,SUBSPACES=m[,PROBES=p])", version.String),
		rowCap("vector_sparse", "supported", "SPARSEVECTOR<N> inverted-index sparse retrieval and dense+sparse+BM25 fusion", version.String),
		rowCap("quantized_vector_index", "supported", "CREATE VECTOR INDEX ... WITH (QUANTIZATION = 'F16'|'I8') on HNSW with full-precision re-rank", version.String),
		rowCap("window_functions", "supported", "ROW_NUMBER/RANK/LAG etc", "0.1.0"),
		rowCap("workflows", "supported", "CREATE/RUN WORKFLOW", "0.1.0"),
		rowCap("cte", "supported", "WITH and WITH RECURSIVE", "0.1.0"),
		rowCap("partitions_range", "supported", "RANGE partitioning", "0.1.0"),
		rowCap("partitions_hash", "supported", "HASH partitioning", "0.1.0"),
		rowCap("partitions_list", "supported", "LIST partitioning", "0.1.0"),
		rowCap("follower_reads", "experimental", "STRONG, BOUNDED, and STALE routing in the server and official drivers; replica health in system.replica_health", version.String),
		rowCap("rebuild_index_online", "supported", "REBUILD INDEX ONLINE — non-partitioned B+Tree/UNIQUE/JSON-path/spatial indexes; vector/full-text/partitioned indexes still use the blocking REBUILD INDEX", version.String),
		rowCap("mtls", "supported", "mutual TLS service identity; SIGHUP trust bundle/CRL rotation forces reauthentication", version.String),
		rowCap("token_credentials", "supported", "signed short-lived NSSC1 credentials with rotatable keysets and revocation (nextsql token)", version.String),
		rowCap("oidc_broker", "supported", "external IdP (OIDC) token-exchange broker minting NSSC1 credentials; nextsql login (Authorization Code/PKCE, client-credentials)", version.String),
		rowCap("audit_chain", "supported", "tamper-evident NSAC hash-chain audit log with optional NSAK Ed25519 signing and verification (nextsql audit)", version.String),
		rowCap("storage_caps", "supported", "hosting realm/database storage caps enforced on the write path", version.String),
		rowCap("quotas_view", "supported", "advisory system.quotas surfacing of hosting storage caps with connected-database usage/percent/over-cap", version.String),
		rowCap("resource_groups", "experimental", "CREATE/ALTER/DROP RESOURCE GROUP workload-governance descriptors (system.resource_groups); durable and RBAC-gated, not yet wired to query admission/scheduling", version.String),
	}
	// Ensure deterministic order already sorted by name; sort to guarantee.
	sort.Slice(rows, func(i, j int) bool {
		return rows[i][0].Str < rows[j][0].Str
	})
	return rows
}

func rowCap(name, status, desc, since string) []types.Value {
	return []types.Value{
		types.StringValue(name),
		types.StringValue(status),
		types.StringValue(desc),
		types.StringValue(since),
	}
}
