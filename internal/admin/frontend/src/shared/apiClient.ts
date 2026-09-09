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

export async function jsonRequest<T>(
  method: string,
  path: string,
  body: unknown,
  // Optional: a caller whose auth rides on an HttpOnly cookie (Setup mode)
  // has no header of its own to attach.
  extraHeaders: Record<string, string> = {},
): Promise<T> {
  const headers: Record<string, string> = { ...extraHeaders };
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
