// NextSQL Admin service worker: caches the static app shell (the JS/CSS
// bundle) for installability and offline load, and nothing else. It must
// never cache anything under /api/* — that is live session, auth, and query
// data, not app-shell content, and must always reach the real server exactly
// as if no service worker were installed. It also never caches a Setup-mode
// token URL (?token=...): that token is single-use, and caching its response
// would let a later, unrelated visitor be answered from a stranger's
// already-consumed token page instead of the real server.
//
// CACHE_NAME is rewritten at build time (build.mjs) to a hash of the built
// bundle, so a new release always gets a fresh cache and the activate
// handler below drops every older one.
const CACHE_NAME = "nextsql-admin-shell-__BUILD_ID__";
const PRECACHE = ["/assets/app.js", "/assets/app.css"];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE_NAME)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;

  const url = new URL(req.url);
  if (url.pathname.startsWith("/api/")) return;
  if (url.searchParams.has("token")) return;

  // Stale-while-revalidate: answer from cache immediately when present (so
  // the installed app opens offline), but always refetch in the background
  // to keep the cache current for next time.
  event.respondWith(
    caches.match(req).then((cached) => {
      const network = fetch(req)
        .then((res) => {
          if (res.ok) caches.open(CACHE_NAME).then((c) => c.put(req, res.clone()));
          return res;
        })
        .catch(() => cached);
      return cached || network;
    }),
  );
});
