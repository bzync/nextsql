import { useCallback, useEffect, useState } from "react";
import { ApiError } from "./api";

type State<T> = {
  data: T | null;
  error: string | null;
  loading: boolean;
  reload: () => void;
};

// useReadModel fetches a Manager read-model, exposes {data,error,loading} and a
// reload(). A 401 calls onUnauthorized (the session expired) instead of
// surfacing as an error.
export function useReadModel<T>(fetcher: () => Promise<T>, onUnauthorized: () => void): State<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [nonce, setNonce] = useState(0);

  const reload = useCallback(() => setNonce((n) => n + 1), []);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    fetcher()
      .then((d) => {
        if (!alive) return;
        setData(d);
        setError(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        if (e instanceof ApiError && e.status === 401) {
          onUnauthorized();
          return;
        }
        setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nonce, onUnauthorized]);

  return { data, error, loading, reload };
}
