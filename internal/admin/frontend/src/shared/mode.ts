// The one thing every top-level render needs before anything else: which
// lifecycle phase this install is in. Setup and Operations are sequential,
// never concurrent, so the server picks one mode at process startup
// (nextsql lifecycle detect) and this is the single unauthenticated call the
// UI makes to find out which sub-app to mount.

import { jsonRequest } from "./apiClient";

export type Mode = "setup" | "operate";

export type ModeResponse = { mode: Mode };

export async function fetchMode(): Promise<ModeResponse> {
  const data = await jsonRequest<ModeResponse>("GET", "/api/v1/mode", undefined, {});
  if (data.mode !== "setup" && data.mode !== "operate") {
    throw new Error("unexpected /api/v1/mode response");
  }
  return data;
}
