// Friendly, deep-linkable hash router for NextSQL Admin:
// Reads/writes window.location.hash as "#!/<section>?<params>".
// Supports structured deep linking (e.g. "#!/studio?table=articles",
// "#!/databases?tab=tables", "#!/activity?tab=locks", "#!/security?tab=users")
// while maintaining complete backward compatibility with simple routes
// and in-page anchor skip links (e.g. "#admin-main").

import { useCallback, useEffect, useMemo, useState } from "react";

const ROUTE_PREFIX = "#!/";
const ALT_ROUTE_PREFIX = "#/";

export type ParsedRoute = {
  section: string;
  subpath?: string;
  params: Record<string, string>;
  full: string;
};

export function parseRoute(raw: string): ParsedRoute {
  let cleaned = raw.trim();
  if (cleaned.startsWith(ROUTE_PREFIX)) {
    cleaned = cleaned.slice(ROUTE_PREFIX.length);
  } else if (cleaned.startsWith(ALT_ROUTE_PREFIX)) {
    cleaned = cleaned.slice(ALT_ROUTE_PREFIX.length);
  } else if (cleaned.startsWith("/")) {
    cleaned = cleaned.slice(1);
  }

  const [pathPart, queryPart] = cleaned.split("?", 2);
  const pathSegments = (pathPart || "").split("/").filter(Boolean);
  const section = pathSegments[0] || "";
  const subpath = pathSegments.slice(1).join("/");

  const params: Record<string, string> = {};
  if (queryPart) {
    const searchParams = new URLSearchParams(queryPart);
    searchParams.forEach((val, key) => {
      params[key] = val;
    });
  }

  return {
    section,
    subpath: subpath || undefined,
    params,
    full: cleaned,
  };
}

export function buildRoute(section: string, params?: Record<string, string | undefined>): string {
  if (!params) return section;
  const searchParams = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") {
      searchParams.set(k, v);
    }
  }
  const qs = searchParams.toString();
  return qs ? `${section}?${qs}` : section;
}

export function routeFromHash(): string | null {
  const h = window.location.hash;
  if (h.startsWith(ROUTE_PREFIX)) {
    return h.slice(ROUTE_PREFIX.length);
  }
  if (h.startsWith(ALT_ROUTE_PREFIX)) {
    return h.slice(ALT_ROUTE_PREFIX.length);
  }
  return null;
}

export type HashRouteResult = [string, (next: string) => void] & {
  route: string;
  section: string;
  subpath?: string;
  params: Record<string, string>;
  navigate: (next: string) => void;
  setParam: (key: string, value: string | undefined) => void;
};

export function useHashRoute(defaultRoute: string): HashRouteResult {
  const [route, setRoute] = useState(() => routeFromHash() ?? defaultRoute);

  useEffect(() => {
    const onHashChange = () => {
      const next = routeFromHash();
      if (next !== null) setRoute(next);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const navigate = useCallback((next: string) => {
    setRoute(next);
    if (routeFromHash() !== next) {
      window.location.hash = `${ROUTE_PREFIX}${next}`;
    }
  }, []);

  const parsed = useMemo(() => parseRoute(route), [route]);

  const setParam = useCallback((key: string, value: string | undefined) => {
    const current = parseRoute(routeFromHash() ?? defaultRoute);
    const nextParams = { ...current.params };
    if (value === undefined || value === "") {
      delete nextParams[key];
    } else {
      nextParams[key] = value;
    }
    const nextRoute = buildRoute(current.section || defaultRoute, nextParams);
    navigate(nextRoute);
  }, [defaultRoute, navigate]);

  const tuple = [route, navigate] as [string, (next: string) => void];

  return Object.assign(tuple, {
    route,
    section: parsed.section || defaultRoute,
    subpath: parsed.subpath,
    params: parsed.params,
    navigate,
    setParam,
  });
}
