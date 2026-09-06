// Same-origin JSON client for NextSQL Admin's Operations-mode API
// (/api/v1). CSRF token is kept in memory only and sent on state-changing
// calls.

import { ApiError, jsonRequest } from "../shared/apiClient";
export { ApiError };

export type ResultSet = {
  columns: string[];
  rows: (string | null)[][];
  // Set (non-zero) only for a statement that reports a count without any
  // columns of its own (ANALYZE / MAINTAIN / REBUILD INDEX's acknowledgment).
  affected?: number;
};

export type Whoami = {
  authenticated: boolean;
  user: string;
  database: string;
  realm: string;
  csrf_token: string;
};

export type LoginBody = {
  user: string;
  password: string;
  database?: string;
  realm?: string;
};

export type Overview = {
  generated_at: string;
  storage: ResultSet;
  replication: ResultSet;
  capabilities: ResultSet;
  sessions: number;
  active_queries: number;
  clustered: boolean;
  warnings?: string[];
};

export type Databases = {
  generated_at: string;
  storage: ResultSet;
  databases: ResultSet;
  realms: ResultSet;
  tables: ResultSet;
  table_stats: ResultSet;
  hosted: boolean;
  warnings?: string[];
};

export type Activity = {
  generated_at: string;
  sessions: ResultSet;
  active_queries: ResultSet;
  transactions: ResultSet;
  locks: ResultSet;
  warnings?: string[];
};

export type Security = {
  generated_at: string;
  users: ResultSet;
  roles: ResultSet;
  grants: ResultSet;
  tls: ResultSet;
  key_versions: ResultSet;
  audit_verify: ResultSet;
  audit_log: ResultSet;
  warnings?: string[];
};

export type Cluster = {
  generated_at: string;
  replication: ResultSet;
  replica_health: ResultSet;
  clustered: boolean;
  warnings?: string[];
};

export type ClusterAction =
  | "transfer_leader"
  | "drain"
  | "maintenance_enable"
  | "maintenance_disable"
  | "reconcile_confirm";

export type Maintenance = {
  generated_at: string;
  tables: ResultSet;
  indexes: ResultSet;
  table_stats: ResultSet;
  index_stats: ResultSet;
  warnings?: string[];
};

export type MaintenanceOp = "analyze" | "rebuild_index" | "maintain";
export type MaintainScope = "database" | "table" | "index";

export type MaintenanceActionRequest = {
  op: MaintenanceOp;
  target?: string;
  scope?: MaintainScope;
  online?: boolean;
};

export type Config = {
  generated_at: string;
  config: ResultSet;
  warnings?: string[];
};

export type ConfigActionRequest = {
  key: string;
  value?: string;
  reset?: boolean;
};

export type Diagnostics = {
  generated_at: string;
  metrics: ResultSet;
  server_log: ResultSet;
  warnings?: string[];
};

export type Backups = {
  generated_at: string;
  backups: ResultSet;
  restore_hint: string;
  warnings?: string[];
};

export type BackupActionRequest = { op: "create" | "verify"; name?: string };

export type StudioResultSet = ResultSet & {
  column_types: string[];
  truncated: boolean;
  elapsed_ms: number;
};

export type StudioReadConsistency = "strong" | "bounded" | "stale";

export type StudioBootstrap = {
  generated_at: string;
  server_addr: string;
  read_consistency: StudioReadConsistency;
  max_staleness_ms: number;
  capabilities: StudioResultSet;
  tables: StudioResultSet;
  tables_truncated: boolean;
  warnings?: string[];
};

// The session's read-consistency after a successful change. Also the shape of
// StudioBootstrap's read_consistency / max_staleness_ms fields.
export type StudioReadConsistencyState = {
  mode: StudioReadConsistency;
  max_staleness_ms: number;
};

// The current Studio session connection after a successful reconnect. The
// NSQL user never changes; realm and database are the now-active target.
export type StudioConnection = {
  user: string;
  realm: string;
  database: string;
};

// Body for POST /api/v1/studio/reconnect. The password is required (NextSQL
// binds realm/database at handshake only) and is never stored anywhere.
export type StudioReconnectBody = {
  realm: string;
  database: string;
  password: string;
};

export type StudioTableDetail = {
  generated_at: string;
  name: string;
  table: StudioResultSet;
  columns: StudioResultSet;
  indexes: StudioResultSet;
  foreign_keys: StudioResultSet;
  referencing_keys: StudioResultSet;
  triggers: StudioResultSet;
  table_stats: StudioResultSet;
  index_stats: StudioResultSet;
  ddl: StudioResultSet;
  warnings?: string[];
};

export type StudioWorkflowOverview = {
  generated_at: string;
  workflows: StudioResultSet;
  triggers: StudioResultSet;
  schedules: StudioResultSet;
  tasks: StudioResultSet;
  change_streams: StudioResultSet;
  warnings?: string[];
};

export type StudioSchemaGraph = {
  generated_at: string;
  foreign_keys: StudioResultSet;
  truncated: boolean;
  warnings?: string[];
};

// Read-only view of a database's nsql_schema_migrations table. present is
// false when the migration system has never run on this database (the table
// does not exist) or the caller cannot see it — the reason is in warnings.
export type StudioMigrationHistory = {
  generated_at: string;
  present: boolean;
  history: StudioResultSet;
  truncated: boolean;
  warnings?: string[];
};

// A positional bind value for a parameterized editor statement. value is null
// for a SQL NULL; otherwise a string the server coerces to the placeholder's
// expected type. Sent only for the single-statement Run path.
export type StudioQueryParam = { value: string | null };

export type StudioQueryRequest = { query_id: string; sql: string; params?: StudioQueryParam[] };

// A DDL/DML statement (no result set) reports its column metadata as JSON
// null, not an empty array — the streaming transport already coalesces this
// (see acceptLine's meta frame below); this bounded non-streaming path needs
// the same normalization so a null-safe StudioResultSet is the only shape
// any caller ever has to handle.
function normalizeStudioResult(result: StudioResultSet): StudioResultSet {
  return {
    ...result,
    columns: result.columns ?? [],
    column_types: result.column_types ?? [],
    rows: result.rows ?? [],
  };
}

export type StudioAnalysis = {
  kind: string;
  destructive: boolean;
  reasons?: string[];
  write: boolean;
  // True for CREATE/DROP USER and CREATE/DROP ROLE: NextSQL identities are
  // realm-scoped, so the statement affects every database in the connected
  // realm, not just the one this Studio session targets. Drives an extra
  // confirm-before-run line naming the realm.
  realm_scoped?: boolean;
};

export type StudioScript = {
  statements: string[];
};

export type StudioStreamMeta = {
  columns: string[];
  column_types: string[];
};

export type StudioStreamComplete = {
  affected: number;
  truncated: boolean;
  elapsed_ms: number;
  row_count: number;
};

export type StudioStreamHandlers = {
  onMeta: (meta: StudioStreamMeta) => void;
  onRows: (rows: (string | null)[][]) => void;
  onComplete: (complete: StudioStreamComplete) => void;
};

let csrf: string | null = null;
export function setCsrf(token: string | null) {
  csrf = token;
}

function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  return jsonRequest<T>(method, path, body, csrf ? { "X-NSM-CSRF": csrf } : {});
}

const MAX_STUDIO_STREAM_ROWS = 5_000;
const MAX_STUDIO_STREAM_BYTES = 8 << 20;
const MAX_STUDIO_STREAM_ROW_BYTES = 1 << 20;
// JSON control-character escaping can expand one byte to six bytes. The
// server's 1 MiB raw-row bound therefore gives this line parser a strict,
// still-finite worst-case frame ceiling.
const MAX_STUDIO_STREAM_LINE = MAX_STUDIO_STREAM_ROW_BYTES * 6 + (64 << 10);
const MAX_STUDIO_STREAM_COLUMNS = 1_024;
const utf8 = new TextEncoder();

function streamStrings(value: unknown, field: string): string[] {
  if (!Array.isArray(value) || value.some((item) => typeof item !== "string")) {
    throw new ApiError(`invalid Studio stream ${field}`, 502);
  }
  return value as string[];
}

function streamRows(value: unknown): (string | null)[][] {
  if (!Array.isArray(value)) throw new ApiError("invalid Studio stream rows", 502);
  const rows = value as unknown[];
  if (rows.length > 128) throw new ApiError("Studio stream row batch exceeds its bound", 502);
  return rows.map((candidate) => {
    if (!Array.isArray(candidate) || candidate.some((cell) => cell !== null && typeof cell !== "string")) {
      throw new ApiError("invalid Studio stream row", 502);
    }
    return candidate as (string | null)[];
  });
}

// Reads the bounded NDJSON query response incrementally. The browser repeats
// the server's row/byte ceilings so a compromised or version-skewed Admin
// cannot grow client memory without limit.
async function studioQueryStream(body: StudioQueryRequest, handlers: StudioStreamHandlers): Promise<StudioStreamComplete> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (csrf) headers["X-NSM-CSRF"] = csrf;
  const response = await fetch("/api/v1/studio/query/stream", {
    method: "POST",
    headers,
    body: JSON.stringify(body),
    credentials: "same-origin",
  });
  if (!response.ok) {
    const text = await response.text();
    let message = response.statusText || `HTTP ${response.status}`;
    try {
      const parsed = JSON.parse(text) as { error?: unknown };
      if (typeof parsed.error === "string" && parsed.error) message = parsed.error;
    } catch {
      if (text) message = text;
    }
    throw new ApiError(message, response.status);
  }
  if (!response.body) throw new ApiError("Studio stream response has no body", 502);

  const contentType = response.headers.get("Content-Type")?.split(";", 1)[0].trim().toLowerCase();
  if (contentType !== "application/x-ndjson") {
    await response.body.cancel();
    throw new ApiError("Studio stream response has an unexpected content type", 502);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffered = "";
  let sawMeta = false;
  let complete: StudioStreamComplete | null = null;
  let retainedRows = 0;
  let retainedBytes = 0;
  let columnCount = -1;

  const acceptLine = (line: string) => {
    if (!line) return;
    if (line.length > MAX_STUDIO_STREAM_LINE) throw new ApiError("Studio stream frame exceeds its bound", 502);
    let raw: unknown;
    try {
      raw = JSON.parse(line);
    } catch {
      throw new ApiError("Studio stream contains invalid JSON", 502);
    }
    if (!raw || typeof raw !== "object" || !("type" in raw)) {
      throw new ApiError("Studio stream contains an invalid frame", 502);
    }
    const frame = raw as Record<string, unknown>;
    if (frame.type === "meta") {
      if (sawMeta || complete) throw new ApiError("Studio stream metadata is out of order", 502);
      const columns = streamStrings(frame.columns ?? [], "columns");
      const columnTypes = streamStrings(frame.column_types ?? [], "column types");
      if (columns.length !== columnTypes.length) throw new ApiError("Studio stream column metadata is inconsistent", 502);
      if (columns.length > MAX_STUDIO_STREAM_COLUMNS) throw new ApiError("Studio stream column metadata exceeds its bound", 502);
      columnCount = columns.length;
      for (const value of [...columns, ...columnTypes]) retainedBytes += utf8.encode(value).byteLength;
      if (retainedBytes > MAX_STUDIO_STREAM_BYTES) throw new ApiError("Studio stream metadata exceeds the browser result bound", 502);
      sawMeta = true;
      handlers.onMeta({ columns, column_types: columnTypes });
      return;
    }
    if (frame.type === "rows") {
      if (!sawMeta || complete) throw new ApiError("Studio stream rows are out of order", 502);
      const rows = streamRows(frame.rows);
      retainedRows += rows.length;
      for (const row of rows) {
        if (row.length !== columnCount) throw new ApiError("Studio stream row width is inconsistent", 502);
        let rowBytes = 0;
        for (const cell of row) if (cell !== null) rowBytes += utf8.encode(cell).byteLength;
        if (rowBytes > MAX_STUDIO_STREAM_ROW_BYTES) throw new ApiError("Studio stream row exceeds the browser result bound", 502);
        retainedBytes += rowBytes;
      }
      if (retainedRows > MAX_STUDIO_STREAM_ROWS || retainedBytes > MAX_STUDIO_STREAM_BYTES) {
        throw new ApiError("Studio stream exceeds the browser result bound", 502);
      }
      handlers.onRows(rows);
      return;
    }
    if (frame.type === "complete") {
      if (!sawMeta || complete) throw new ApiError("Studio stream completion is out of order", 502);
      const rowCount = typeof frame.row_count === "number" && Number.isSafeInteger(frame.row_count) && frame.row_count >= 0
        ? frame.row_count
        : 0;
      if (rowCount !== retainedRows) throw new ApiError("Studio stream row count is inconsistent", 502);
      complete = {
        affected: typeof frame.affected === "number" && Number.isSafeInteger(frame.affected) && frame.affected >= 0 ? frame.affected : 0,
        truncated: frame.truncated === true,
        elapsed_ms: typeof frame.elapsed_ms === "number" && Number.isFinite(frame.elapsed_ms) && frame.elapsed_ms >= 0 ? frame.elapsed_ms : 0,
        row_count: rowCount,
      };
      handlers.onComplete(complete);
      return;
    }
    if (frame.type === "error") {
      const message = typeof frame.error === "string" && frame.error ? frame.error : "Studio query stream failed";
      const status = frame.error_code === "canceled" ? 408 : 502;
      throw new ApiError(message, status);
    }
    throw new ApiError("Studio stream contains an unknown frame type", 502);
  };

  try {
    while (true) {
      const { done, value } = await reader.read();
      buffered += decoder.decode(value, { stream: !done });
      if (buffered.length > MAX_STUDIO_STREAM_LINE && !buffered.includes("\n")) {
        throw new ApiError("Studio stream frame exceeds its bound", 502);
      }
      let newline = buffered.indexOf("\n");
      while (newline >= 0) {
        const line = buffered.slice(0, newline).trimEnd();
        buffered = buffered.slice(newline + 1);
        acceptLine(line);
        newline = buffered.indexOf("\n");
      }
      if (done) break;
    }
    if (buffered.trim()) acceptLine(buffered.trimEnd());
    if (!complete) throw new ApiError("Studio stream ended before completion", 502);
    return complete;
  } catch (error) {
    await reader.cancel().catch(() => undefined);
    throw error;
  }
}

export const api = {
  whoami: () => request<Whoami>("GET", "/api/v1/session"),
  login: (b: LoginBody) => request<Whoami>("POST", "/api/v1/session", b),
  logout: () => request<void>("DELETE", "/api/v1/session"),
  overview: () => request<Overview>("GET", "/api/v1/overview"),
  databases: () => request<Databases>("GET", "/api/v1/databases"),
  activity: () => request<Activity>("GET", "/api/v1/activity"),
  security: () => request<Security>("GET", "/api/v1/security"),
  cluster: () => request<Cluster>("GET", "/api/v1/cluster"),
  clusterAction: (action: ClusterAction, timeoutMs?: number) =>
    request<ResultSet>("POST", "/api/v1/cluster/action", { action, timeout_ms: timeoutMs ?? 0 }),
  maintenance: () => request<Maintenance>("GET", "/api/v1/maintenance"),
  maintenanceAction: (body: MaintenanceActionRequest) =>
    request<ResultSet>("POST", "/api/v1/maintenance/action", body),
  config: () => request<Config>("GET", "/api/v1/config"),
  configAction: (body: ConfigActionRequest) =>
    request<ResultSet>("POST", "/api/v1/config/action", body),
  diagnostics: () => request<Diagnostics>("GET", "/api/v1/diagnostics"),
  backups: () => request<Backups>("GET", "/api/v1/backups"),
  backupAction: (body: BackupActionRequest) =>
    request<ResultSet>("POST", "/api/v1/backups/action", body),
  studioBootstrap: () => request<StudioBootstrap>("GET", "/api/v1/studio/bootstrap"),
  studioTable: (name: string) =>
    request<StudioTableDetail>("GET", `/api/v1/studio/table?name=${encodeURIComponent(name)}`),
  studioWorkflows: () => request<StudioWorkflowOverview>("GET", "/api/v1/studio/workflows"),
  studioSchemaGraph: () => request<StudioSchemaGraph>("GET", "/api/v1/studio/schema-graph"),
  studioMigrations: () => request<StudioMigrationHistory>("GET", "/api/v1/studio/migrations"),
  studioQuery: (body: StudioQueryRequest) =>
    request<StudioResultSet>("POST", "/api/v1/studio/query", body).then(normalizeStudioResult),
  studioQueryStream,
  studioAnalyze: (sql: string) =>
    request<StudioAnalysis>("POST", "/api/v1/studio/query/analyze", { sql }),
  studioSplit: (sql: string) =>
    request<StudioScript>("POST", "/api/v1/studio/query/split", { sql }),
  studioCancel: (queryId: string) =>
    request<{ canceled: boolean; query_id: string }>("POST", "/api/v1/studio/query/cancel", { query_id: queryId }),
  studioReconnect: (body: StudioReconnectBody) =>
    request<StudioConnection>("POST", "/api/v1/studio/reconnect", body),
  studioSetReadConsistency: (body: StudioReadConsistencyState) =>
    request<StudioReadConsistencyState>("POST", "/api/v1/studio/read-consistency", body),
};
