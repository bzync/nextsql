// Minimal hash-based router: reads/writes window.location.hash as
// "#!/<route>". Neither of the two products this replaces (Manager,
// Installer) had any URL routing at all — Manager's section switch was
// plain useState. This is deliberately small: it only makes the
// Operations shell's top-level sections URL-addressable and
// back/forward-navigable, which is what Studio's eventual deep-linkable
// multi-tab editor will need to build on. It does not attempt to model
// Studio's own future internal routing.
//
// The "!/" prefix (not just "/") is deliberate: both the Setup wizard and
// the Operations shell also use a plain `href="#some-id"` skip link to jump
// focus to their main landmark (e.g. "#admin-main"). Reusing bare "#/..."
// for routes would make clicking that skip link look like a route change.
// Prefixing routes lets in-page anchors and app routes share the one hash
// namespace without colliding — a hashchange to something outside our
// prefix (like a skip-link jump) is simply ignored here, and the browser's
// native scroll/focus behavior for it is unaffected either way.

import { useCallback, useEffect, useState } from "react";

const ROUTE_PREFIX = "#!/";

function routeFromHash(): string | null {
  const h = window.location.hash;
  return h.startsWith(ROUTE_PREFIX) ? h.slice(ROUTE_PREFIX.length) : null;
}

export function useHashRoute(defaultRoute: string): [string, (next: string) => void] {
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
    if (routeFromHash() !== next) window.location.hash = `${ROUTE_PREFIX}${next}`;
  }, []);

  return [route, navigate];
}
