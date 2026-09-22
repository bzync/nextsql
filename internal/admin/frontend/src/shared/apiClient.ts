// Shared same-origin JSON client for the NextSQL Admin API (/api/v1). No
// external deps. Setup mode (single-run token) and Operations mode
// (operator session + CSRF) each supply their own auth header via
// `extraHeaders` — this only handles the request/response plumbing both
// already did identically.

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export function responseMediaType(res: Response): string {
  return res.headers.get("Content-Type")?.split(";", 1)[0].trim().toLowerCase() ?? "";
}

// misdirectedResponseError is the message for an /api/v1 response that is
// not the media type Admin asked for. A reverse proxy commonly answers with
// another application's HTML shell; the body is not an error string and must
// not be retained or shown.
export function misdirectedResponseError(res: Response, expected: string): ApiError {
  const contentType = responseMediaType(res);
  const received = contentType || "a response without Content-Type";
  const status = res.ok ? 502 : res.status;
  return new ApiError(
    `NextSQL Admin expected ${expected} from its API, but received ${received}. ` +
      "The request reached another web app; open nextsql-admin's URL or route /api/v1/* to nextsql-admin.",
    status,
  );
}

async function discardBody(res: Response): Promise<void> {
  if (res.body) await res.body.cancel().catch(() => undefined);
}

// readAPIError reads a failed /api/v1 response. JSON `{"error":"..."}` is
// preserved, including its HTTP status. Any other media type, and JSON that
// does not parse, is reported without echoing the body.
export async function readAPIError(res: Response): Promise<ApiError> {
  if (responseMediaType(res) !== "application/json") {
    await discardBody(res);
    return misdirectedResponseError(res, "JSON");
  }
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      return new ApiError("NextSQL Admin API returned invalid JSON", res.ok ? 502 : res.status);
    }
  }
  let msg = res.statusText || `HTTP ${res.status}`;
  if (data && typeof data === "object" && "error" in data) {
    const e = (data as { error: unknown }).error;
    if (typeof e === "string" && e) msg = e;
  }
  return new ApiError(msg, res.status);
}

export async function jsonRequest<T>(
  method: string,
  path: string,
  body: unknown,
  // Optional: a caller whose auth rides on an HttpOnly cookie (Setup mode)
  // has no header of its own to attach.
  extraHeaders: Record<string, string> = {},
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json", ...extraHeaders };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });

  // Every /api/v1 response with a body is JSON. Check that contract before
  // reading the body: a reverse-proxy miss commonly returns an HTML app shell,
  // and rendering that whole document as an error both hides the real routing
  // problem and needlessly retains an attacker-controlled response in memory.
  // 204/205 are the intentional body-less exceptions (logout/forget).
  if (res.status === 204 || res.status === 205) {
    if (!res.ok) throw new ApiError(res.statusText || `HTTP ${res.status}`, res.status);
    return null as T;
  }
  if (responseMediaType(res) !== "application/json") {
    if (res.body) await res.body.cancel().catch(() => undefined);
    throw misdirectedResponseError(res, "JSON");
  }

  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      const status = res.ok ? 502 : res.status;
      throw new ApiError("NextSQL Admin API returned invalid JSON", status);
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
