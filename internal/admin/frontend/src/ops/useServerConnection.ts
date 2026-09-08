import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, api, type ServerConnection } from "./api";

const POLL_MS = 5_000;

// Live nextsqld reachability for this admin session. The session cookie can
// still be valid after the driver socket dies; Ops views and Studio share
// this probe so neither surface claims Connected when the server is gone.
export function useServerConnection(onUnauthorized: () => void): {
  status: ServerConnection | null;
  checking: boolean;
  refresh: () => void;
} {
  const [status, setStatus] = useState<ServerConnection | null>(null);
  const [checking, setChecking] = useState(true);
  const [nonce, setNonce] = useState(0);
  const onUnauthorizedRef = useRef(onUnauthorized);
  onUnauthorizedRef.current = onUnauthorized;

  const refresh = useCallback(() => setNonce((n) => n + 1), []);

  useEffect(() => {
    let stopped = false;
    let timer = 0;

    const probe = async () => {
      try {
        const next = await api.connection();
        if (stopped) return;
        setStatus(next);
      } catch (error: unknown) {
        if (stopped) return;
        if (error instanceof ApiError && error.status === 401) {
          onUnauthorizedRef.current();
          return;
        }
        setStatus((prev) => ({
          connected: false,
          server_addr: prev?.server_addr ?? "",
          user: prev?.user ?? "",
          database: prev?.database ?? "",
          realm: prev?.realm ?? "",
          error: error instanceof Error ? error.message : String(error),
        }));
      } finally {
        if (!stopped) setChecking(false);
      }
    };

    const loop = async () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      await probe();
      if (!stopped) timer = window.setTimeout(loop, POLL_MS);
    };

    const onVisibility = () => {
      window.clearTimeout(timer);
      if (document.visibilityState === "visible") void loop();
    };

    document.addEventListener("visibilitychange", onVisibility);
    void loop();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [nonce]);

  return { status, checking, refresh };
}
