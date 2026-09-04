// Same-origin JSON client for the NextSQL Manager API (/api/v1). No external
// deps. CSRF token is kept in memory only and sent on state-changing calls.

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

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

let csrf: string | null = null;
export function setCsrf(token: string | null) {
  csrf = token;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (csrf) headers["X-NSM-CSRF"] = csrf;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }
  if (!res.ok) {
    let msg = res.statusText || `HTTP ${res.status}`;
    if (data && typeof data === "object" && "error" in data) {
      const e = (data as { error: unknown }).error;
      if (typeof e === "string" && e) msg = e;
    }
    throw new ApiError(msg, res.status);
  }
  return data as T;
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
};
