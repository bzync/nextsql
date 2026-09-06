# NextSQL Admin — design

> Status: **In progress (2026-09-05).** Setup and Operations are unified; Studio
> M1 is implemented in the same product shell (see `docs/design-admin-studio.md`).

`PROJECT.md` §46-§67 is the authority for intended scope (Setup mode / Operations mode /
Studio mode); `TODO.md` Phase 28 (Setup + Operations) and Phase 29 (Studio) for status.

## 1. Why one product

NextSQL previously specified three separate GUI surfaces — Installer, Manager, Studio —
each with its own binary, its own "separate from" boundary statement, and (for Installer
and Manager) its own frontend package. In practice the two that were built had already
converged: same React + `@bzync/rui` stack, same dependency versions, same visual
language (Manager explicitly adopted the Installer's shell, log #141), and both already
labeled themselves "NextSQL Admin" in UI copy before the docs ever used that name. Studio
was still 100% unbuilt. Splitting one coherent admin experience across three binaries
bought no real isolation — none of the three needed a different process boundary from the
others for security reasons (all three are equally "no engine imports, no key access,
official-interfaces-only" clients of `nextsqld`) — while costing three build pipelines,
three sets of hand-duplicated chrome (`Brand`, `ThemeSelect`, API client base), and no
shared navigation between what are, for an operator or developer, facets of the same job.

**Decision: one binary (`nextsql-admin`), one package (`internal/admin`), one frontend
bundle, three modes.** Each mode keeps the connection/auth model its job actually needs
(below) — the merge is architectural and organizational, not a forced identical session
model across fundamentally different use cases.

## 2. The three modes

| Mode | Formerly | Package | Design doc | Status |
|---|---|---|---|---|
| Setup | NextSQL Installer | `internal/admin/setup` | `docs/design-admin-setup.md` | mostly complete (Linux) |
| Operations | NextSQL Manager | `internal/admin/ops` | `docs/design-admin-operations.md` | MVP complete |
| Studio | NextSQL Studio | `internal/admin/studio` | `docs/design-admin-studio.md` | P29 M1–M3 implemented; MVP open |

Setup and Operations are **sequential lifecycle phases of the same install, never
concurrent** — a database is either not yet initialized (Setup) or already running
(Operations/Studio) — so `nextsql-admin` picks exactly one mode at process start rather
than serving both at once.

### Mode detection

At startup, `nextsql-admin` shells out to the existing `nextsql lifecycle detect --json`
(no new CLI surface) to decide:

- **No initialized installation found → Setup mode.** Behaves exactly as the former
  Installer: single-operator ephemeral 256-bit token auth (URL query on first load →
  cookie → header on every subsequent call), ephemeral `--listen` port by default,
  auto-opens the system browser (`internal/browseropen`, unchanged), drives `nextsql
  setup` as a subprocess. See `docs/design-admin-setup.md`.
- **Initialized installation found → Operations mode.** Behaves exactly as the former
  Manager: operator logs in with real NSQL credentials, session cookie + CSRF, fixed
  `--listen` default (`127.0.0.1:7220`), no browser auto-open. The Studio nav entry now
  hosts P29's first authenticated query workspace on that same session. See
  `docs/design-admin-operations.md` and `docs/design-admin-studio.md`.
- `--mode setup|operate` overrides detection, for packaging/automation/tests.

If detection is ambiguous or the `nextsql` binary can't be found/run, `nextsql-admin`
fails closed with a clear error rather than guessing which mode to start in.

### Why Studio isn't a fourth session model

Studio mode's eventual connection manager (PROJECT.md §50) is a materially different
shape from Operations mode's single implicit session — saved profiles across
environments (dev/test/staging/production), potentially many servers, secure
OS-keychain credential storage. That is a genuine difference in what the mode connects
to, not a reason to make Studio a separate product: it is a natural superset of
Operations mode's session store (one live connection per browser session today), which
the first Phase 29 increment deliberately reuses. The full saved-profile manager remains
open work and will extend this foundation to hold multiple named connections rather than
replace the native-driver/session boundary.

## 3. Backend composition

One `Server`/mux picks the mode at startup and serves that mode's full route table under
it — `setup`'s token middleware for Setup-mode routes, `ops`'s session+CSRF middleware for
Operations-mode routes. An unauthenticated `GET /api/v1/mode` returns `{"mode":"setup"}`
or `{"mode":"operate"}` — the one endpoint that exists in both modes, and the first call
the frontend makes to decide which sub-app to mount.

Both `setup` and `ops` keep their own compiler-enforced engine-import ban (an
`imports_test.go` in each package forbidding `internal/storage`/`wal`/`undo`/`recovery`/
`catalog`/`crypto`/`txn`/…) — merging the binary does not relax that boundary.

## 4. Frontend composition

One npm package, `internal/admin/frontend/`, replaces the two that existed
(`internal/manager/frontend/`, `internal/installgui/frontend/`) — same dependency
versions both already shared (`@bzync/rui`, React, esbuild). `src/shared/` holds what was
previously hand-duplicated between the two apps (`Brand`, `ThemeSelect`, API client base).
`src/setup/` and `src/ops/` hold the moved wizard/views; `src/studio/` now contains P29
M1–M3's explorer/editor/streaming-result/result-inspector workspace, wired into the Operations-mode shell's nav
alongside the existing nine views. The top-level `App.tsx` calls `GET /api/v1/mode` first
and mounts the corresponding sub-app.

This merge also introduces real URL routing, which neither prior app had (Manager's
navigation was in-memory tab state only) — needed so Studio's eventual multi-tab,
deep-linkable SQL editor has somewhere to grow into.

Build output goes to `internal/admin/web/`, committed, embedded via `//go:embed`, so
`go build ./...` still needs no Node toolchain — unchanged from both prior apps'
convention.

## 5. PWA integration (2026-09-05, log #145)

NextSQL Admin is installable: a web app manifest, a service worker, and the icon set
needed for "Add to Home Screen"/desktop install prompts. This applies uniformly to
whichever mode is active — Setup, Operations, or (once built) Studio — since the manifest
and service worker are registered once from the shared shell (`src/main.tsx`), not
per-mode.

**What the service worker does and does not cache**, precisely, because getting this
wrong would be a real correctness/security issue in a tool that manages live database
credentials and sessions:

- Precaches the static JS/CSS bundle (`/assets/app.js`, `/assets/app.css`) at install.
  Everything else, including the shell HTML at `/`, is stale-while-revalidate: served from
  cache immediately when present, always refetched in the background.
- **Never** intercepts `/api/*` — every read/write either mode issues (sessions, queries,
  actions) must always reach the real server, exactly as if no service worker were
  installed. This is the one rule the whole feature is designed around.
- **Never** caches a URL carrying Setup mode's one-time `?token=` query parameter —
  caching that response would let a later, unrelated visitor be answered from a stranger's
  already-consumed token page instead of the real server.
- The cache name is a hash of the built bundle (`build.mjs`, computed at build time), so
  a new release gets a fresh cache automatically and the worker's own `activate` handler
  deletes every older one.

**Why the PWA static files (`manifest.webmanifest`, `sw.js`, icons, favicon) live in a
directory embedded separately from the rest of the frontend build** (`internal/admin/
webpwa/`, a sibling of `internal/admin/web/`, not a subdirectory of it): the existing
`/assets/` route exposes the whole of `web/` wholesale, and `sw.js` in particular must be
reachable *only* at the top-level `/sw.js` path it is actually registered from — a service
worker's default scope is the directory it is served from, so anything reachable under
`/assets/` as well would (harmlessly, since the content is identical either way, but
sloppily) blur that boundary. A first attempt nested these files under `web/pwa/` instead
of a true sibling directory, which turned out to still be reachable via `/assets/pwa/...`
since `/assets/` walks its tree recursively — caught by a test
(`TestPWAAssetsServed`) before it shipped, not after.

## 6. Studio M1–M3

The first Studio slices reuse the authenticated Operations connection, query the
authorized native `system` schema for capabilities and lazy table metadata, and execute
one bounded editor statement at a time through the official Go driver. M2 carries result
metadata and bounded row batches over NDJSON and virtualizes the visible result rows in
the browser. M3 adds bounded local copy/export and type-aware JSON/vector/fixed-geo/
TIMESTAMPTZ cell inspection without a new server route. Their additive
same-origin HTTP routes, cancellation semantics, limits, test matrix, and intentionally
open MVP scope are specified in `docs/design-admin-studio.md`.

## 7. What did not change

- `internal/setup` (the CLI backbone: `nextsql setup`, `nextsql lifecycle {detect,
  preflight,backup-config,upgrade,repair,uninstall}`) — unaffected; Setup mode still
  drives it as a subprocess exactly as the former Installer did.
- `internal/browseropen` — unaffected.
- Every HTTP route, request/response shape, and SQL statement either mode issues —
  unchanged from the former Installer/Manager. This was a restructuring of process/package
  boundaries and frontend delivery, not a behavior change.
- The server-side RBAC/tenant/encryption/audit model — Admin (all modes) remains a pure,
  credential-less, official-interfaces-only client of `nextsqld`, per `PROJECT.md` §73's
  cross-mode UX/safety contract.
