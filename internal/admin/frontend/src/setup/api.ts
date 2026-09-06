// Same-origin JSON client for NextSQL Admin's Setup-mode API (/api/v1). Auth
// is the single-run installer token — read once from the cookie the server
// sets on first load (see internal/admin/setup) and sent on every call via
// X-Installer-Token, exactly like the cookie itself would be, so a call
// still works even if the cookie somehow isn't attached.

import { ApiError, jsonRequest } from "../shared/apiClient";
export { ApiError };

export type Defaults = {
  dataDir: string;
  keyFile: string;
  configOut: string;
  elevated: boolean;
  os: string;
};

export type Hello = {
  nextsql_version: string;
  phase: number;
  defaults: Defaults;
};

export type ServiceStatus = {
  supported: boolean;
  scope: string;
  unitFound: boolean;
  configPath: string;
  enabled: boolean;
  active: boolean;
};

// Params mirrors internal/installgui.Params field-for-field (json tags must
// match exactly — the server decodes with DisallowUnknownFields, so sending
// any extra key is a 400, not a silently-ignored one).
export type Params = {
  dataDir: string;
  keyFile: string;
  configOut: string;
  preset: "" | "conservative" | "balanced" | "high-performance" | "custom";
  profile: "" | "developer" | "production";
  bufferPages: number;
  adminUser: string;
  adminPassword: string;
  realm: string;
  database: string;
  skipInit: boolean;
  listenAddr: string;
  tlsCert: string;
  tlsKey: string;
  enableService: boolean;
};

export function defaultParams(): Params {
  return {
    dataDir: "",
    keyFile: "",
    configOut: "",
    preset: "balanced",
    profile: "production",
    bufferPages: 0,
    adminUser: "",
    adminPassword: "",
    realm: "default",
    database: "default",
    skipInit: false,
    listenAddr: "",
    tlsCert: "",
    tlsKey: "",
    enableService: false,
  };
}

// PlanResult is `nextsql setup [--dry-run] --json`'s own output, forwarded
// verbatim by the server as runResult.result. Field set matches
// cmd/nextsql/setup.go's setupResult.
export type PlanResult = {
  nextsql_version: string;
  phase: number;
  hardware?: {
    goos: string;
    goarch: string;
    num_cpu: number;
    gomaxprocs: number;
    ram_bytes: number;
    measured_path: string;
    disk_total_bytes: number;
    disk_free_bytes: number;
    filesystem: string;
  };
  recommendation?: {
    preset: string;
    buffer_pages: number;
    buffer_bytes: number;
    ram_fraction: number;
    rationale: string;
  };
  config_path: string;
  config_written: boolean;
  listen_addr: string;
  tls: boolean;
  data_dir: string;
  key_file: string;
  key_file_exists: boolean;
  instance_key_file: string;
  instance_key_exists: boolean;
  admin_user?: string;
  profile?: string;
  initialized: boolean;
  init_output?: string;
  health?: { ok: boolean; format_compatible: boolean; tables: number; durable_lsn: number };
  warnings?: string[];
  dry_run: boolean;
  plan?: string;
};

export type ServiceOutcome = {
  enabled: boolean;
  active: boolean;
  error?: string;
};

// BrowseEntry/BrowseResult mirror internal/installgui/browse.go's
// browseEntry/browseResult — the wizard's path-picker fields (data
// directory, key file, TLS cert/key) list the server's own filesystem
// through this, since a browser file input can never hand JS a real
// absolute path.
export type BrowseEntry = {
  name: string;
  path: string;
  isDir: boolean;
};

export type BrowseResult = {
  dir: string;
  parent?: string;
  entries: BrowseEntry[];
};

export type RunResult = {
  ok: boolean;
  result?: PlanResult;
  error?: string;
  service?: ServiceOutcome;
};

function tokenFromCookie(): string {
  const m = document.cookie.match(/(?:^|; )nsi_token=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  return jsonRequest<T>(method, path, body, { "X-Installer-Token": tokenFromCookie() });
}

export const api = {
  hello: () => request<Hello>("GET", "/api/v1/hello"),
  service: () => request<ServiceStatus>("GET", "/api/v1/service"),
  plan: (p: Params) => request<RunResult>("POST", "/api/v1/plan", p),
  install: (p: Params) => request<RunResult>("POST", "/api/v1/install", p),
  finish: () => request<{ ok: boolean }>("POST", "/api/v1/finish", {}),
  browse: (dir?: string) =>
    request<BrowseResult>("GET", "/api/v1/browse" + (dir ? "?dir=" + encodeURIComponent(dir) : "")),
};
