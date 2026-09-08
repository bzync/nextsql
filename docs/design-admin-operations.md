# NextSQL Admin — Operations mode design (formerly "NextSQL Manager")

> **2026-09-05 (log #142): NextSQL Installer, NextSQL Manager, and NextSQL Studio were
> merged into one product, NextSQL Admin — see `docs/design-admin.md` for the umbrella
> architecture (one binary `nextsql-admin`, mode detection, the unified frontend).** This
> document is the still-authoritative implementation history and design record for what
> is now **Operations mode**: `nextsql-manager`/`internal/manager` became
> `nextsql-admin`/`internal/admin/ops`; behavior, routes, and every M1-M9 decision below
> are unchanged, only the binary/package names moved. Historical text below still says
> "Manager"/"the Manager" — read that as "Operations mode".

> Status: **MVP COMPLETE (2026-09-04).** All nine slices landed: **M1
> (serving backbone + Overview)**, **M2 (Databases & Storage)**, **M3
> (Connections & Activity)**, **M6 (Cluster)**, **M7 (Maintenance)** (logs
> #119, #120, #123, #124); **M4 (Security)** COMPLETE (log #128);
> users/roles/grants (log #122), `system.tls` (log #125), `system.
> key_versions` (log #126), and `system.audit_verify`/`system.audit_log`
> (log #128) — see the M4 entry below for the full landing history. **M8
> (Configuration) is COMPLETE** (2026-09-04): the viewer (`system.config`,
> log #127) plus the validated safe editor — a new `SET CONFIG` statement +
> `POST /api/v1/config/action` + an inline row editor, log #132. **M9 (Logs
> & Diagnostics) is COMPLETE** (2026-09-04): the metrics panel (`system.
> metrics`, log #129), the server-log tail (`system.server_log` over a new
> bounded `internal/logging.Ring`, log #130), and the redacted
> diagnostic-bundle export (`GET /api/v1/diagnostics/bundle`, log #131) —
> see the M9 entry below. **M5 (Backups) is COMPLETE** (2026-09-04, log
> #133): new `backup.CreateFromEngine` hot-backup path + `BACKUP DATABASE` /
> `VERIFY BACKUP` statements + `system.backups` + a Backups view; restore/PITR
> stays offline-CLI (a running server cannot restore into itself). Every M-slice
> is landed — the Manager MVP exit gate is closed.

NextSQL Manager is the official operational administration UI (Phase 28),
separate from the Installer and from Studio. It is the GUI equivalent of the
`nextsql` operational subcommands (`status`, `cluster`, `backup`, `diagnose`,
`audit`, `hosting`, …) plus the `system.*` schema.

`PROJECT.md` §47 is the authority for intended scope; `TODO.md` Phase 28
"NextSQL Manager MVP" for status.

## 1. Architecture decision — local web app served by Go

Chosen 2026-09-04 via `AskUserQuestion`, over Wails (Go + system webview),
Fyne (pure-Go toolkit), and a CLI/JSON-only Manager.

**The Manager is a single Go binary (`nextsql-manager`) that serves a static
web UI plus a JSON API over a loopback HTTP listener. The operator opens it in
their normal browser.**

Rationale against the alternatives, measured against `PROJECT.md`'s stated
criteria (security, performance, accessibility, size, offline support):

| Criterion | Local web app | Wails | Fyne | Electron |
|---|---|---|---|---|
| Attack surface | smallest — no bundled runtime, no native IPC bridge, browser sandbox | OS webview + Go↔JS bridge | native, but custom widget code | bundled Chromium + Node, largest |
| Binary size | one static Go binary (+ embedded assets) | Go binary + webview deps | one static Go binary | ~100 MB+ |
| Accessibility | the user's real browser — mature AT support, zoom, high-contrast, reduced-motion for free | webview-dependent, uneven across platforms | immature AT story | Chromium (good) but heavy |
| Offline | fully — assets embedded, no CDN | yes | yes | yes |
| Cross-platform packaging | trivial — no per-OS app bundle, no webview runtime to ship | per-OS webview quirks (WebView2 / WebKitGTK / WKWebView) | one binary, but platform look-and-feel drift | per-OS installers, code signing pain |
| "Never runs as root" (`PROJECT.md` §47) | clean — unprivileged HTTP process; OS-only tasks go through a separate minimal privileged helper | same | same | same |
| Single-language with the engine | Go backend; a bundled JS/TS frontend | yes + a JS build toolchain | yes | no |

`PROJECT.md` explicitly says *"Do not choose Electron automatically; justify
framework"* — Electron is rejected on size and attack surface. Wails is a
reasonable second choice but adds a Node frontend build step and a per-OS
webview dependency for a marginal "native window" gain that a browser tab does
not meaningfully lack for an admin tool. Fyne cannot deliver the dense tables
/ audit views / dashboards the Manager needs with acceptable accessibility.

### Frontend stack — React + `@bzync/rui`

M1 shipped a hand-written vanilla-JS shell. Starting with M2/M3 (2026-09-04,
`TODO.md` log #121) the frontend was rebuilt on **React + `@bzync/rui`** — the
same component library `docs/web` (the product site) uses — bundled with
esbuild into a single `app.js`/`app.css` pair that is embedded and served
exactly as before; the served *output* is still static files, so nothing in
§2–§4 below changes. Rationale for the switch (decided via `AskUserQuestion`,
choosing "adopt now" over "adopt at M4" or "stay vanilla"): M4–M9 need real
forms, dialogs, tabs, and data tables (security/backup/cluster wizards,
confirmation flows) where a component library pays for itself immediately,
and reusing `@bzync/rui` keeps the Manager visually and behaviorally
consistent with the rest of Bzync's products rather than diverging into a
one-off hand-rolled design system.

- **Source**: `internal/manager/frontend/` — a small standalone npm package
  (`package.json`, `tsconfig.json`, `src/`), *not* part of the Go module and
  not part of any Go build. `npm ci && npm run build` there runs
  `build.mjs` (esbuild), which bundles `src/main.tsx` into
  `internal/manager/web/app.js` + `internal/manager/web/app.css` and copies
  `index.html` alongside them.
- **Generated output is committed**: `internal/manager/web/` (the
  `//go:embed` target) is checked in like any other generated-but-tracked
  asset, so `go build ./...` needs no Node toolchain. Re-run the frontend
  build and commit the result after any `frontend/src/` change.
- **No server-side rendering, no framework routing** — the whole UI is one
  client-side bundle; navigation between Manager sections is in-memory tab
  state, not URL routes (keeps the Go server a pure static-file + JSON API
  server with no path-matching for frontend routes).
- **Structure**: `src/api.ts` (typed fetch client, mirrors the Go
  `resultJSON`/bundle shapes 1:1), `src/useReadModel.ts` (a small hook:
  fetch a read-model, expose `{data,error,loading,reload}`, and treat a 401
  as "session expired" rather than a view-level error), `src/ResultTable.tsx`
  (renders a `{columns,rows}` result set with `@bzync/rui`'s `Table`),
  `src/views/*.tsx` (one file per read-model view), `src/Login.tsx` +
  `src/Shell.tsx` + `src/App.tsx` (auth state machine + top-level layout).
- **CSP** stays script-src 'self' (the bundle is the only script, no
  inline/eval); `style-src` allows `'unsafe-inline'` because `@bzync/rui`
  sets some styles at runtime (its animation/theme system), which does not
  permit script execution.

### Shared visual/accessibility baseline with Installer

As of 2026-09-05 (P28 log #141), Manager deliberately adopts the GUI
Installer's presentation rather than retaining its earlier generic AppShell:
canonical NextSQL wordmark, flat backdrop, self-hosted fonts, branded 200px
rail, elevated main card, spacing, responsive navigation, skip link, and an
explicit persisted system/light/dark selector are shared conventions. The
Manager canvas remains wider for operational tables, but it is recognizably
the same product shell. Its tab orientation now follows the responsive
layout and is exposed through `aria-orientation` so arrow-key behavior and
assistive-technology semantics agree.

`internal/manager/frontend/test-accessibility.mjs` drives the real generated
bundle in headless Chrome: login, authenticated Overview, Arrow-key navigation
to Databases, all theme choices, and emulated increased-contrast/reduced-motion
styles. Axe's WCAG 2.2 A/AA-tagged rules are green on those states. The shared
CDP harness lives at `internal/webtest/cdp.mjs`; this is a repeatable semantic
audit, not a claim of manual certification in every screen reader/browser pair.

### Consequences

- The Manager is **stateless across restarts** except for in-memory sessions
  (see §3). Killing and restarting it logs everyone out; it holds nothing
  durable.
- The Manager has **no data-directory access and no key access**. It never
  imports `internal/storage`, `internal/wal`, `internal/catalog`,
  `internal/crypto`, or `internal/recovery`. Enforced by the package's import
  set and a test.

## 2. How the Manager talks to the server

Every data operation goes through the **official Go driver
(`drivers/go`)** against a running `nextsqld` — the same NSQL wire protocol
any application uses. The Manager:

- connects to `nextsqld` at a configured address (`--server-addr`), with the
  configured TLS material (`--tls-ca`, optional mTLS `--tls-client-cert` /
  `--tls-client-key`), or `--insecure` on loopback only — identical rules to
  `nextsql` server-mode commands (`internal/cli.ServerConfig`);
- performs each request **as the logged-in operator's own NSQL user**, so
  server-side RBAC, realm/database isolation, audit, and redaction apply unchanged.
  The Manager has no ambient authority and no service account;
- reads operational state from the `system.*` schema and the `SHOW` aliases;
- issues management actions as ordinary SQL / driver calls (`CLUSTER …`,
  `BACKUP DATABASE`, `VERIFY BACKUP …`, `ANALYZE`, `REBUILD INDEX …`,
  `MAINTAIN …`, and `SET CONFIG …`) — never a private code path. User/role
  mutation, cancel-another-session, restore, and PITR are not Manager MVP
  actions.

The Manager process itself starts with **no credentials**. It learns them
only when an operator logs in through the browser, and forgets them when the
session ends.

## 3. Session and authentication model

1. **Login** — `POST /api/v1/session` with `{user, password, database?,
   realm?}`. The Manager opens a driver connection to `nextsqld` with those
   credentials. Success ⇒ a new session; failure ⇒ the driver's error is
   surfaced verbatim (401 for auth failure, 502 for unreachable server).
2. **Session record** (in memory only): a 256-bit random id, the operator's
   user / database / realm, the live `*nextsql.Conn`, `createdAt`,
   `lastSeen`, a per-session mutex (a `Conn` is not safe for concurrent
   queries), and a random CSRF token.
3. **Cookie** — `nsm_session=<id>`, `HttpOnly`, `SameSite=Strict`, `Path=/`,
   `Secure` whenever the Manager listener is TLS. Never contains anything but
   the opaque id.
4. **CSRF** — every state-changing `/api/v1/*` call (POST/PUT/PATCH/DELETE)
   must carry `X-NSM-CSRF: <token>` matching the session. Safe methods
   (GET/HEAD) rely on the `SameSite=Strict` cookie alone — which also lets the
   SPA recover the token after a page reload via `GET /api/v1/session`. The
   token is returned in the login/whoami response body, never in a cookie.
5. **Expiry** — idle timeout (default 15 min since `lastSeen`) and absolute
   timeout (default 12 h since `createdAt`). A single bounded sweeper
   goroutine evicts expired sessions once a minute and closes their
   connections. A session with a request **in flight** is never idle
   (`beginRequest`/`endRequest` around the `authed` handler; the idle clock
   restarts when the request finishes): an action can outlive the idle
   timeout — `BACKUP DATABASE` is allowed 30 minutes — while the view keeps
   its button disabled and the SPA sends nothing else, and evicting the
   session mid-action logged the operator out the moment the backup returned.
   The absolute lifetime is *not* held off this way: it is a security bound,
   not an activity measure.
6. **Bounded** — at most `--max-sessions` (default 16) concurrent sessions;
   login past that returns 503. No unbounded goroutines, maps, or
   connections.
7. **Logout** — `DELETE /api/v1/session` closes the connection and drops the
   record.

Passwords are never logged, never written to disk, and never stored beyond
the live driver connection they opened. The request logger records only
method, path, status, duration, and the authenticated user (once known).

## 4. HTTP surface

```
GET    /                     the embedded SPA shell
GET    /assets/*             embedded static assets (long-cache, immutable)
GET    /healthz              liveness — no auth, no data, just "ok"
POST   /api/v1/session       login       → {user, database, realm, csrf_token}
GET    /api/v1/session       whoami      → {authenticated, user, database, realm}
DELETE /api/v1/session       logout
GET    /api/v1/overview      Overview read-model (slice M1)
GET    /api/v1/databases     Databases & Storage read-model (slice M2)
GET    /api/v1/activity      Connections & Activity read-model (slice M3)
GET    /api/v1/security      Users/roles/grants/TLS/keys/audit read-model (M4)
POST   /api/v1/security/action  RBAC administration (M4 write half)
```

Later slices add `/api/v1/security/*`, `/backups`, `/cluster`,
`/maintenance/*`, `/settings`, `/logs` (see §6).

All API responses are JSON. Each read-model is a bundle: named `system.*`
result sets plus a `generated_at` and any per-query `warnings`. A
non-required query that fails (a table absent, or the operator's RBAC
forbids it) becomes a warning and an empty result rather than failing the
whole view; a required one returns an error. Result sets are rendered
generically as `{"columns": [...], "rows": [[...]]}` with every cell a
string (or JSON `null`) — the same rendering the CLI uses — so the UI never
needs type-specific decoding and the Manager never reinterprets server
values.

Security headers on every response: `Content-Security-Policy: default-src
'self'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, `Cache-Control: no-store` on API responses.

## 5. Configuration

Flags on `nextsql-manager` (all overridable by a key=value `--config` file
later; MVP is flags only):

| Flag | Default | Meaning |
|---|---|---|
| `--listen` | `127.0.0.1:7220` | Manager HTTP listener. A non-loopback address requires `--tls-cert` / `--tls-key`. |
| `--tls-cert` / `--tls-key` | — | TLS for the Manager listener itself. |
| `--server-addr` | `127.0.0.1:7210` | `nextsqld` address. |
| `--tls-ca` | — | PEM CA / server cert for the `nextsqld` connection. |
| `--tls-server-name` | address host | TLS SNI / verification name for `nextsqld`. |
| `--tls-client-cert` / `--tls-client-key` | — | mTLS to `nextsqld`. |
| `--insecure` | false | Allow a plaintext `nextsqld` connection (loopback only). |
| `--max-sessions` | 16 | Concurrent operator sessions. |
| `--idle-timeout` | 15m | Session idle expiry (a request in flight is not idle). |
| `--session-lifetime` | 12h | Session absolute expiry. |
| `--log-level` | info | `debug`/`info`/`warn`/`error`. |
| `--grant-port-80` | false | Linux only: run `setcap cap_net_bind_service=+ep` on this installed binary so a later `--listen 127.0.0.1:80` doesn't need root, then exit. Opt-in — the default listener is unaffected and needs no such grant. Typically run once via `sudo`, since only root can add file capabilities. |

## 6. MVP decomposition

Each slice is one navigation section, is independently shippable, and reuses
only official interfaces. Order is roughly by operator value.

- **M1 — serving backbone + Overview** *(landed, log #119)*: the
  `nextsql-manager` binary, the loopback HTTP server, embedded shell, the
  session/auth/CSRF layer, and `GET /api/v1/overview` (server storage,
  replication/HA state, live session and active-query counts, the capability
  registry).
- **M2 — Databases & Storage** *(landed, log #120)*: `GET /api/v1/databases`
  — `system.storage`, `system.databases` / `system.realms` (a `hosted` flag;
  empty on a single-database deployment, reported not errored),
  `system.tables`, `system.table_stats`.
  **View reshaped 2026-09-08 (log #228)**: the flat Tables / Databases /
  Realms tabs became one **catalog tree** — realm rows expand to their
  databases, database rows expand to their tables (nested `aria-expanded`
  disclosure rows, not an ARIA tree). Databases join realms by `realm_id`;
  a database whose realm is not visible in `system.realms` is grouped under
  its own `realm_name` rather than dropped, and a deployment with neither
  table populated still gets one row — the connected database — so the table
  list keeps a home. `system.tables` is the **connected** database's catalog
  (one Ops connection binds one realm+database at handshake), so only the
  connected database expands to tables; any other says why instead of
  showing an empty or borrowed list. The read-model is unchanged.
- **M3 — Connections & Activity** *(landed, log #120)*: `GET /api/v1/activity`
  — `system.sessions`, `system.active_queries`, `system.transactions`,
  `system.locks`. **Correction from the original plan**: NextSQL has no
  server-side "cancel another session's query / kill session" surface —
  cancellation is client-driven (context cancel on your own connection) or
  credential-driven (`nextsql token revoke`). The Manager therefore only
  *observes* activity here; terminating a session belongs with M4 (Security,
  via credential revocation) if it lands at all.
- **M4 — Security**: `system.users` / `system.roles` / `system.grants`; key
  and TLS status (no key material); the audit viewer over
  `nextsql audit` / the audit chain.
  **Users/roles/grants landed 2026-09-04 (log #122)**:
  `GET /api/v1/security` (a `runBundle` read-model exactly like M1–M3) plus a
  Security view. Those three tables are already admin-only server-side —
  see `docs/system-catalog.md` "Security administration tables" — so the
  handler adds no RBAC of its own.
  **RBAC administration landed 2026-09-08 (log #223)**: `POST
  /api/v1/security/action` completes M4's write half. Until it, RBAC could
  only be *read* here — there was no counterpart to the Cluster / Maintenance
  / Config / Backups action routes — so creating a principal or changing a
  grant meant hand-writing SQL in Studio. The route takes a **structured**
  request (`op` plus named fields), never raw SQL, and renders the documented
  statement server-side from a closed set of ops
  (`create_user`, `drop_user`, `create_role`, `drop_role`, `grant_role`,
  `revoke_role`, `grant`, `revoke`), the closed privilege list, and the
  closed scope list. Every interpolated name must first pass `validIdent`, so
  the interpolation is provably safe rather than relying on the server to
  reject a malformed statement afterwards; a password is escaped as a SQL
  string literal (`''` doubling, the lexer's only escape) and never logged or
  echoed in an error. The statement runs on the operator's own authenticated
  connection, so `internal/executor/security.go` remains the sole authority
  on who may administer RBAC — the route can grant nothing the signed-in
  operator could not grant by typing the same statement, and the engine's
  refusal surfaces as 403. The Security view gains the matching UI
  (`internal/admin/frontend/src/ops/SecurityAdmin.tsx`): create user (with
  password confirmation), create role, drop user/role behind an explicit
  confirmation, role membership, and a privilege grant/revoke form whose
  privilege and scope choices mirror the Studio builder. The confirmation
  message lives in the parent view rather than the toolbar because applying a
  change reloads the read model and `ViewFrame` swaps its children for a
  spinner, which would otherwise unmount the message before it could be read.
  `alter` is absent from the privilege list on purpose: it lexes as a reserved
  keyword outside the GRANT privilege-list grammar, so `GRANT ALTER ON …`
  does not parse.
  **TLS status landed 2026-09-04 (log #125)**: a new `system.tls` table
  (`internal/system/schema.go`) backed by `executor.DB.TLSStatus()`, a
  settable-callback field on `*DB` (`SetTLSStatusSource`, mirroring the
  pre-existing `SetDrainFunc` pattern for "state the embedding
  `protocol.Server` owns, exposed read-only through the executor") wired
  by `cmd/nextsqld/main.go` to `security.ServerTLSReloader.Status()` right
  after the reloader is constructed. `Status()` is new on
  `ServerTLSReloader` itself: it returns the active leaf certificate's
  redacted identity/validity plus the mTLS/CRL posture — subject, issuer,
  not-before/not-after, DNS SANs, `mtls_required`, `client_ca_configured`,
  `client_crl_configured` — reading the same `current *tls.Config` snapshot
  `GetConfigForClient` already serves handshakes from, so a `Reload`
  rotation is visible to `system.tls` on the very next call, same
  atomicity guarantee the handshake path already has. No private key
  material and no network address is ever in the struct (there is nothing
  to redact — the fields simply don't exist on it), matching the
  `system.replication.leader_addr` "never expose addresses over SQL"
  convention. Admin-only like `system.users`/`roles`/`grants`; always
  exactly one row for an admin (`enabled=false` with the rest blank when
  no TLS listener is attached — embedded/CLI use or a loopback plaintext
  deployment), never an error, same "always one descriptive row" shape as
  `system.replication` reporting `state='single'`. Wired only onto the
  legacy/non-hosted `db` variable in `cmd/nextsqld/main.go` (the same
  scope `SetDrainFunc` already has) — under M2 multi-database hosting mode
  (`db == nil`, only `dbMgr`-opened per-realm databases exist), a hosted
  session's `system.tls` reports "not attached" even though the process's
  listener is in fact TLS-configured, since TLS is a process/listener-level
  fact but this wiring is per-`*DB`. Documented as a known gap here rather
  than solved in this increment — solving it needs either wiring every
  `dbMgr`-opened database too or promoting `SetTLSStatusSource` to a
  process-level (not per-DB) attachment point, both bigger changes than
  this scoped read-surface increment warrants. Security view gained a
  `TLSStatusCard` (labeled fact sheet, not a raw `ResultTable` — a single
  descriptive row reads better as labeled fields, same reasoning as
  Cluster's clustered/standalone `Badge`) with an expiry-urgency badge
  (`error` under 14 days, `warning` under 30, `success` otherwise).
  **Key-rotation status landed 2026-09-04 (log #126)**: a new
  `system.key_versions` table (`internal/system/schema.go`) backed by
  `executor.DB.KeyStatus()`, a settable-callback field on `*DB`
  (`SetKeyStatusSource`, the exact same pattern as `SetTLSStatusSource`)
  wired by `cmd/nextsqld/main.go` right alongside `SetDrainFunc` to the
  already-in-scope `env *crypto.Envelope` variable's new `KeyStatus()`
  method. `KeyStatus()` is new on `crypto.Envelope`: it returns one row per
  key the envelope manages — `kek`, `master`, and each `crypto.AllDomains`
  data domain (`page`/`wal`/`undo`/`backup`/`vector`/`fulltext`/`temp`/
  `replication`) — with the current version and retained/revoked/retired
  counts, reading `ring.keys`/`ring.flags` directly under the envelope's
  own lock. No key material is in the returned struct (there is no field
  for it, same as `security.TLSStatus`). Admin-only like `system.tls`/
  `users`/`roles`/`grants`, but unlike `system.tls` this table is
  **list-shaped**, not a single-status-fact table: it returns **zero rows**
  (not a placeholder row) when no persistent envelope is attached —
  embedded/CLI use with a bare `crypto.KeyProvider`, or a legacy deployment
  with no `.keys` keystore file — the same "empty means not applicable"
  convention `system.databases`/`system.realms` already use rather than
  `system.tls`/`system.replication`'s "always one descriptive row" shape,
  since a list with nothing to list is already a correct empty answer. A
  real, pre-existing wrinkle found while writing this (not introduced by
  it): `Revoke` deletes a version's DEK from `ring.keys` immediately but
  leaves its `ring.flags` entry (with `flagRevoked` set) behind until a
  later `Retire` — so `version_count` drops right away on `Revoke` while
  `revoked_count` only drops on `Retire`, and separately, `Retire`'s
  current implementation deletes a version's flags entry outright rather
  than ever setting `flagRetired` first, so `retired_count` is always `0`
  today (the field/column still exists so the table's shape doesn't need
  to change if that ever does — documented in `crypto.KeyStatus`'s own doc
  comment, not silently left for a future reader to rediscover). Security
  view gained a "Key rotation" `ResultTable` section (a raw table this
  time, not a labeled fact sheet like `TLSStatusCard` — this table is
  naturally tabular, several keys × few columns, unlike `system.tls`'s one
  row of many columns). Same hosting-mode wiring-scope caveat as
  `system.tls` (wired only onto the legacy/non-hosted `db`/`env`) applies
  here identically, for the identical reason.
  **The audit-chain viewer landed 2026-09-04 (log #128), closing M4's
  originally scoped surface.** Two new tables, both admin-only: `system.
  audit_verify` (a single status fact, same "always exactly one row" shape
  as `system.tls`) and `system.audit_log` (a bounded, list-shaped tail of
  the most recent entries, same "zero rows for not-attached" shape as
  `system.key_versions`/`system.config`). Both are sourced from a new
  `security.TailEvents(path, maxEvents, verifiers) (TailReport, error)`,
  which extends the existing `security.VerifyFile` streaming scanner (the
  same one `nextsql audit verify` already uses) rather than duplicating its
  chain-walking logic: `VerifyFile` itself is now a thin wrapper calling
  the same internal scanner with `maxEvents=0`. `TailReport` embeds the
  unchanged `VerifyReport` plus `Events []Event`, populated by a small
  fixed-capacity ring buffer (`eventRing`, O(1) amortized push regardless
  of how many lines the file has) so memory cost is bounded by `maxEvents`
  — 200, `executor.systemAuditLogTailCap` — never by file size, even though
  the chain-verification pass itself is inherently sequential and still
  costs I/O/CPU proportional to the whole file. New `executor.DB.
  SetAuditSource`/`AuditTail(maxEvents)`, the same settable-callback shape
  as the other three (`SetTLSStatusSource`/`SetKeyStatusSource`/
  `SetConfigSource`) but with one deliberate difference: it is called fresh
  on **every** query rather than reading in-memory state, since the whole
  point of an audit viewer is to reflect what is actually durable on disk
  right now, not a cached snapshot. Wired by `cmd/nextsqld/main.go`
  alongside the audit log's own open/signing-keyset setup, reusing the
  already-resolved `audit`/`auditSigningKeys` locals; a file-open/read
  failure (e.g. the file removed out from under a running server) is
  folded into the same `TailReport` shape via its `Problem` field — real
  operational information, not silently degraded to "not attached", the
  distinction a `system.audit_verify` reader can tell apart by checking
  whether `problem` is non-empty despite `lines=0`. **Deliberately includes
  a record in the tail even when the chain is reported broken** — an
  operator investigating a detected tampering point needs to see the
  suspect entry, not have it hidden; documented on `security.TailEvents`
  itself, not just assumed obvious. `remote` is **not** redacted in
  `system.audit_log`, unlike `system.config`'s address fields: it is a
  client connection address, not server/cluster topology, and
  `system.sessions` already exposes the identical kind of value
  unredacted — checked against that existing precedent rather than applying
  the address-redaction rule reflexively to every field that looks
  network-shaped. Folded into the existing `GET /api/v1/security` bundle
  (not a new route — this is still M4's Security slice) with two new
  entries; Security view gained an `AuditVerifyCard` fact sheet (same
  reasoning as `TLSStatusCard`) plus an "Audit log" `ResultTable`. Same
  hosting-mode wiring-scope caveat as `system.tls`/`system.key_versions`
  applies here identically (wired only onto the legacy/non-hosted `db`),
  for the identical reason.
- **M5 — Backups**: list / verify status / create; restore & PITR wizard
  wrapping `nextsql backup` / `restore`.
  **Checked and found blocked 2026-09-04** (same shape as M4's TLS/key/audit
  gap): `nextsql backup`/`restore`/`verify` (`cmd/nextsql/main.go`) all take
  `--data-dir`/`--key-file` and operate on the filesystem directly — no
  `BACKUP` SQL statement and no `system.backups` table.
  **Closed 2026-09-04 (log #133), completing the Manager MVP**, under the
  "must be all status is landed" directive. The blocker had a real technical
  core, not just a missing surface: `backup.Create` does its own
  `storage.Open` → checkpoint → close → copy, and WAL open **truncates a
  torn tail** — so a second engine open inside a running nextsqld (or
  `nextsql backup` against a live server under load) can truncate a WAL
  segment the live engine is mid-write on. **What landed**: new
  `backup.CreateFromEngine(LiveEngine, dataDir, dest, keys, opt)` — factors
  `Create`'s post-checkpoint body into a shared `writeBackup` and takes the
  server's **already-open** engine (checkpoint it, read `CheckpointLSN`/
  `RedoLSN`/`DurableLSN`, copy the file set); no second open, no second
  recovery. The copy is a fuzzy snapshot restore reconciles by replaying WAL
  from `RedoLSN` (the standard hot-backup model), and the restore-test still
  gates publication — `backup.TestCreateFromEngineWhileDatabaseOpen` proves
  a backup taken from a live DB restores cleanly and excludes a write made
  after it was taken. New config key `backup_dir`; new `BACKUP DATABASE` /
  `VERIFY BACKUP 'name'` statements (`BACKUP` matched as the existing
  privilege keyword, `VERIFY` as a bare identifier), gated on the existing
  `BACKUP` privilege or cluster `ADMIN`, node-local, non-transactional.
  New `system.backups` table (`backup.ListBackups` over `backup_dir`).
  `executor.DB` gets thin `SetBackupOps`/`CreateBackup`/`ListBackups`/
  `VerifyBackup` callbacks — the `internal/backup` calls live in
  `cmd/nextsqld/backup.go`, not the executor package, because
  `internal/backup`'s own tests import `internal/executor` (an import cycle
  otherwise). Manager: `GET /api/v1/backups` (`system.backups` + an offline
  `restore_hint` string), `POST /api/v1/backups/action` (`create` /
  `verify`; the verify name is allowlisted for path separators / `..` / `'`
  both at the handler and in `executor`), and a Backups view with a
  "Back up now" button, a per-row Verify action, and the exact
  `nextsql restore` command for offline restore/PITR. **Restore and PITR
  stay CLI-only** — a running server cannot restore into itself, the same
  inherent limit as M6's `DRAIN` (which exits the process). `PROJECT.md` §47
  updated for the new SQL surface.
  **`backup_dir` not configured (refinement)**: `GET /api/v1/backups` also
  returns `backup_state` — `configured` / `unset` / `unknown`, derived from
  whether `system.config` lists a `backup_dir` row (`unknown` when the
  operator holds `BACKUP` but not cluster `ADMIN` and so cannot read
  `system.config`). When it is `unset` the view disables "Back up now" and
  explains that `backup_dir` must be set (Configuration view / `nextsql.conf`)
  and `nextsqld` restarted; a `create`/`verify` that still reaches a node with
  no `backup_dir` (the `unknown` case) has its raw `nerr.Unavailable` wire
  string rewritten to the same remediation text rather than shown verbatim.
  **Known limitation**: `BACKUP DATABASE` is the first runtime caller of
  `Engine.Checkpoint()` on a live (not-closing) engine — the engine's own
  doc comment already anticipates "a caller that … produce[s] a consistent
  snapshot for backup", and the checkpoint serializes against the commit
  path via `e.mu` — but a backup taken under sustained heavy concurrent
  write load is not yet stress-tested. The restore-test gate still rejects a
  copy that did not come out consistent; a concurrent-load backup stress
  test is a good follow-on.
  **In-flight state (refinement, log #228)**: both actions run a full
  restore test server-side and can take minutes (log #227 measured ~100s on
  a 269 MB database). The confirm dialog therefore closes on confirm and the
  originating control — "Back up now…", or that one row's **Verify** —
  carries the spinner, stays disabled, and keeps both for the whole call, so
  the row identifies *which* backup is being verified; every other backup
  action is disabled meanwhile, so one operator cannot start a second
  restore test by accident. RUI's `Button` hides its label
  (`visibility:hidden`) behind the spinner while `loading`, which would
  leave the control with no accessible name, so each loading button is given
  an explicit `aria-label` prefixed with its visible text.
- **M6 — Cluster landed 2026-09-04 (log #123)**: `GET /api/v1/cluster`
  (`system.replication` + `system.replica_health`, both already
  always-visible) and `POST /api/v1/cluster/action` issuing the exact
  documented `CLUSTER TRANSFER LEADER` / `DRAIN [WITH (TIMEOUT_MS = n)]` /
  `MAINTENANCE ENABLE|DISABLE` / `RECONCILE CONFIRM` statements — all
  already gated on `ADMIN ON CLUSTER` server-side, confirmed against a
  running cluster's leader/follower/quorum/lag display. **Verified live,
  not assumed**: `CLUSTER DRAIN` closes the listener and then the entire
  `nextsqld` **process exits** (`protocol.Server.Drain` → `s.Close()`) —
  not just the issuing connection, and on a standalone node not just "this
  node," the *only* node. It does not restart itself; nothing in the
  Manager's scope can bring it back (that needs the still-unbuilt
  privileged helper — "Deliberately out of MVP scope" below). The Cluster
  view's drain confirmation dialog says this explicitly rather than reusing
  softer "graceful drain" language that would undersell what happens.
- **M7 — Maintenance landed 2026-09-04 (log #124)**: `GET /api/v1/maintenance`
  (`system.tables`/`system.indexes`/`system.table_stats`/`system.index_stats`,
  all table-visibility filtered like M2) and `POST /api/v1/maintenance/action`
  issuing `ANALYZE [table]` / `REBUILD INDEX name [ONLINE]` /
  `MAINTAIN DATABASE|TABLE name|INDEX name` — already gated respectively on
  `SELECT`, `INDEX` (on the resolved table), and `ADMIN ON CLUSTER`
  server-side. Unlike M6's fixed action set, a table/index **name** is
  attacker(-authenticated-user)-controlled JSON text interpolated into a
  hand-built SQL string — this SQL dialect has no quoted-identifier syntax
  (checked `internal/sql/lexer` first: `isIdentStart`/`isIdentPart` is the
  entire grammar), so a bare `^[A-Za-z_][A-Za-z0-9_]{0,127}$` regex gate
  before interpolation is both necessary and sufficient — anything it
  rejects couldn't have parsed as a bare identifier anyway. Live-verified
  against a real table/index (`ANALYZE` → `affected:1` and `system.
  table_stats.row_count` updated in the very next read; `REBUILD INDEX` and
  `MAINTAIN TABLE` on real objects; a nonexistent index → 404; an
  injection-shaped target (`"x; DROP TABLE t --"`) → 400 before reaching the
  server at all). **A latent bug found and fixed along the way, not
  triggered yet but real**: `ANALYZE`/`MAINTAIN`/`REBUILD INDEX` report only
  an affected count with zero result columns; Go's nil-slice JSON encoding
  rendered that as `"columns":null`, while the frontend's `ResultSet.
  columns` is typed as a plain (non-nullable) array — `ResultTable` would
  throw on `.length` the first time any view fed such a result through it.
  Nothing does yet (the Cluster/Maintenance views only read `.affected`/
  `.rows[0]` today), but it was one line to source the null at the
  `session.query` boundary itself (`resultJSON.Columns` now initializes
  `[]string{}`, not an `append` onto a nil slice), plus a defensive
  null-check added to `ResultTable` itself.
- **M8 — Configuration**: viewer over the running config; a validated safe
  editor with restart-required indicators (writes go through a config
  endpoint, not by editing files under the server).
  **The viewer landed 2026-09-04 (log #127)**: `GET /api/v1/config` +
  a Configuration view, over a new `system.config` table
  (`internal/system/schema.go` + `executor.systemConfigRows`) backed by
  `executor.DB.ConfigEntries()`, a settable-callback field on `*DB`
  (`SetConfigSource`, the same pattern as `SetTLSStatusSource`/
  `SetKeyStatusSource`) wired by `cmd/nextsqld/main.go` to a new
  `config.Config.SafeEntries()` method. `SafeEntries` deliberately reuses
  `Config.Marshal`'s own byte output (parsed back on `=`) rather than
  re-enumerating every field a second time, so the two can never disagree
  about which keys exist — every key `SafeEntries` can emit is one
  `Marshal` (and therefore `Load`) also knows about, checked directly by
  `TestSafeEntries`. Every network-address-shaped value (`listen_addr`,
  `raft_bind`, `raft_join`, `auth_broker_listen`) is replaced with
  `[redacted]`, same "never expose a network address over SQL" convention
  `system.replication.leader_addr` already established; nothing else is
  redacted, since `Config` itself never holds key material or passwords —
  only file *paths* to them (its own doc comment guarantees this) — so
  every other value is already safe to show an admin, the same trust tier
  `system.users`/`system.tls`/`system.key_versions` already use. Admin-only;
  list-shaped like `system.key_versions` (zero rows, not a placeholder row,
  for embedded/CLI use with no process-level `config.Config` attached).
  **A small naming wrinkle found while writing the schema, not assumed**:
  the natural column name `key` is a reserved word in this dialect's own
  grammar (`PRIMARY KEY`/`FOREIGN KEY`), and there is no quoted-identifier
  syntax to escape it with (confirmed by checking `internal/sql/lexer`
  directly, same check the M7 identifier-validation increment made) — so
  `ORDER BY key` could never parse; the column is named `name` instead.
  Same hosting-mode wiring-scope caveat as `system.tls`/`system.key_versions`
  does **not** apply to the viewer: `cfg` is a single process-wide value
  regardless of how many databases `dbMgr` opens, so `system.config`
  reflects the true running configuration in every deployment shape.
  **The validated safe editor landed 2026-09-04 (log #132), completing M8**:
  a new `SET CONFIG key = value` SQL statement (`docs/sql.md`) — cluster
  `ADMIN`, node-local like `CLUSTER DRAIN`, cannot run in a transaction —
  persists one setting to the node's on-disk `nextsql.conf` through the
  server (`config` is matched as a bare identifier, **not** a keyword, so
  nothing that already names a table/column `config`, `system.config`
  included, is affected). `config` package: `WithSetting` (splices one line
  into `Marshal`'s own output and re-parses through `Load`, so it can never
  disagree about which keys exist or how a value is formatted — the same
  trick `SafeEntries` uses), `SettableKeys`, `DiffState` (running vs on-disk,
  with `restart_required` computed from unredacted values so it stays correct
  for the address keys), and `WriteFile` (atomic tmp+rename, provenance
  header). `executor.DB.SetConfigWriter`/`WriteConfigSetting` +
  `ConfigWriteResult`; nextsqld wires the writer **only when started from a
  config file** (`--config`), otherwise `SET CONFIG` fails `Unavailable`.
  `system.config` gained `file_value` / `restart_required` columns. **Every
  write is persist-only** — nothing is hot-reloaded, so `restart_required`
  is `yes` until `nextsqld` restarts; the Manager cannot restart the server
  (that needs the still-unbuilt privileged helper). Manager:
  `POST /api/v1/config/action` renders `SET CONFIG key = 'value'` (key
  allowlisted against `config.SettableKeys()` before interpolation, same
  discipline as M7's maintenance action; value always a quoted literal with
  `''` escaping, since `WithSetting`→`Load` does the per-key type coercion)
  and the Configuration view gained an inline per-row editor with a
  ConfirmDialog and a "reset to default" action. **Known characteristic**:
  the file is rewritten in canonical form — comments are not preserved, and
  settings `Default()` populates are written explicitly. Audited as
  `config.set`.
- **M9 — Logs & Diagnostics**: server logs / metrics view; a redacted
  diagnostic-bundle export (`nextsql diagnose` shaped).
  **The metrics panel landed 2026-09-04 (log #129)**: `GET /api/v1/diagnostics`
  (a `runBundle` read-model like M1–M8) + a Diagnostics view, over a new
  `system.metrics` table (`internal/system/schema.go` +
  `executor.systemMetricsRows`) backed by `executor.DB.MetricsSnapshot()`,
  a settable-callback field on `*DB` (`SetMetricsSource`, the same pattern
  as `SetConfigSource`/`SetTLSStatusSource`). One real wiring decision it
  forced: the executor's per-`DB` registry (`db.Metrics()`) accumulates
  query/txn/rows/fk/maintenance/cdc counters, while the encryption-seal,
  disk, replication, and page-repair counters live in the process-global
  `metrics.Default()` — neither alone is a complete process view. `nextsqld`
  now routes both into one registry: `db.SetMetrics(metrics.Default())` at
  startup (safe — no connection accepted yet, so the fresh `metrics.New()`
  from `executor.Open` has recorded nothing to lose) plus
  `db.SetMetricsSource(func() *metrics.Registry { return metrics.Default() })`,
  so a single `Snapshot` is internally coherent (`QPS`/`TPS`/`EncryptPct`
  all computed against one `born` time). Admin-only, list-shaped
  (`category`/`name`/`value`/`unit`); zero rows for embedded/CLI use with
  no process registry attached — same "empty means not applicable"
  convention as `system.config`/`system.key_versions`. Same hosting-mode
  wiring-scope caveat as `system.tls`/`system.config` (wired only onto the
  legacy/non-hosted `db`), for the identical reason.
  **The server-log tail landed 2026-09-04 (log #130)**: new
  `internal/logging.Ring` (a fixed-capacity, oldest-evicted-first buffer,
  `DefaultRingCapacity` 500) + a `ringHandler` that mirrors every record
  into it before delegating to the unchanged stderr JSON handler;
  `logging.NewWithRing(level, w) (*slog.Logger, *Ring)` is what `nextsqld`
  now calls instead of `logging.New` (the Manager and auth-broker keep
  `New` — they have no `system.*` surface). New `executor.DB.
  SetServerLogSource`/`ServerLogTail(max)` (re-read per query, like
  `SetAuditSource`) backing a new admin-only, list-shaped `system.server_log`
  table (`seq, event_time, level, message, attributes`; capped at 200/query
  by `executor.systemServerLogTailCap`; zero rows when no ring is attached).
  **A deliberate redaction difference from `system.config`**: `system.
  server_log` does *not* scrub network addresses — a log message is freeform
  text and an admin diagnosing a connectivity fault needs the listen address
  / the unreachable peer; the same "privileged reader + operational value"
  reasoning that keeps `system.audit_log.remote` unredacted, and the table
  never holds anything the process didn't already print to its own stderr.
  It is an in-memory *diagnostic* tail, not a durable log store — the real
  log is still stderr / the service journal. Folded into the same
  `GET /api/v1/diagnostics` bundle (`{metrics, server_log}`); the Diagnostics
  view gained a level-badged log table above the metrics panel. Same
  legacy/non-hosted `db` wiring-scope caveat.
  **The diagnostic-bundle export landed 2026-09-04 (log #131), completing M9**:
  `GET /api/v1/diagnostics/bundle` (`handleDiagnosticsBundle`) returns one
  indented JSON document with an `attachment` `Content-Disposition`
  (`nextsql-diagnostics-<UTC>.json`) so the operator's browser saves it —
  a plain authenticated GET, the session cookie rides along same-origin, no
  CSRF (it is not state-changing). "`nextsql diagnose` shaped" but
  **Manager-scoped**: the driver-only Manager has no data-directory access,
  so the bundle is assembled *entirely* from official admin-only `system.*`
  read surfaces — `metrics`, `server_log`, `config`, `capabilities`,
  `storage`, `replication`, `replica_health`, `tls`, `key_versions`,
  `audit_verify` (chain *status* only, never log content). Every constituent
  is already redacted at its own source (`config` scrubs addresses; `tls`/
  `key_versions` never carry key material) — the handler adds a top-level
  `note` field spelling out what is and isn't in the document rather than
  re-scrubbing; `server_log` is the one part carrying verbatim process log
  text (possibly listen/peer addresses), same as the live view. Contains no
  tenant row data. No RBAC of its own: a non-admin operator gets a bundle of
  empty tables. The Diagnostics view gained a "Download diagnostic bundle"
  button. **M9 is complete** (metrics #129, server-log tail #130, bundle
  export #131).

### Deliberately out of MVP scope

- Start/stop/restart of the `nextsqld` **process** and OS service control —
  needs the separate minimal privileged helper (`PROJECT.md` §47 "Minimal
  privileged helper for OS-only tasks"); the Manager MVP only *observes* and
  drives in-server actions.
- Cluster **creation** wizard (bootstrapping new nodes) — depends on the
  privileged helper and installer integration.
- Any multi-user Manager account system — the Manager has no accounts of its
  own; identity is always the NSQL user.
- Localization and theming beyond light/dark/system.

## 7. Exit gate (Phase 28 "Manager exit gate")

- [x] Manager can perform its MVP operations through official interfaces
- [x] RBAC enforced server-side (the Manager never has ambient authority)
- [x] No raw encryption key exposure
- [x] Cluster / backup / security status reflects server truth (never a
      value derived only from local UI state)
