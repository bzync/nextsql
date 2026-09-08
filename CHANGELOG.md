# Changelog

All notable changes to **NextSQL** are documented in this file.

NextSQL's first tagged release is `0.0.1`; development continues toward `0.1.0`.

This changelog follows the project source-of-truth model:

```text
PROJECT.md = intended finished product
TODO.md    = implementation status, sequencing, dependencies, and phase gates
SKILLS.md  = engineering/agent contract
AGENTS.md  = repository agent instructions
USAGE.md   = current user/operator manual
README.md  = project overview
CHANGELOG.md = notable shipped/verified changes
```

A roadmap item is not recorded as completed here until its implementation, tests, documentation, and applicable exit gate are complete.

---

## [Unreleased]

### Fixed — NextSQL Admin: a long backup logged the operator out (2026-09-08)

- `Back up now` is allowed 30 minutes server-side (it copies, seals and restore-tests every file), but an Operations session went idle after 15 minutes and the idle clock was stamped only when a request *started*. Since the Backups view keeps its button disabled for the whole call and auto-refresh is off by default, the browser sent nothing in between: the sweeper evicted the session mid-backup and the operator was bounced to the login screen the moment their backup returned.
- A session with a request in flight is no longer counted as idle, and the idle clock now restarts when the request finishes rather than when it began — so the idle window starts when the operator could next act. The absolute `--session-lifetime` is unchanged and still applies: it is a security bound, not an activity measure.

### Changed — NextSQL Admin: Databases is a realm → database → tables tree, and backup actions hold their in-flight state (2026-09-08)

- **Data › Databases** no longer splits the catalog across flat Tables, Databases and Realms tabs. Realm rows expand to the databases in that realm, and a database row expands to its tables — one tree that matches how the catalog is actually nested. Realm and database rows keep everything the old tables showed (state, layout, storage cap, delegated realm root, database count) as row badges. A non-multi-realm deployment simply has no realm level, and a deployment with no registry at all still gets one row — the connected database — so the table list never disappears with the tab.
- Only the **connected** database expands to a table list: `system.tables` is the catalog of the database this session is connected to, and one connection binds one realm and one database. Expanding any other database says so and names the sign-in that would reach it, instead of showing an empty or borrowed list.
- **Backups**: `Back up now` and a row's `Verify` both run a full restore test on the server, which can take minutes on a large database. The confirmation dialog now closes on confirm and the button that started the action carries a spinner, stays disabled, and holds both until the call finishes — on the row, so it is clear *which* backup is being verified. Other backup actions are disabled while one runs, so a second restore test cannot be started by accident. A button showing a spinner also keeps an accessible name, which it previously lost.

### Added — `prealloc_ahead_pages`: the per-database preallocation runway is configurable (2026-09-08)

- A brand-new, **empty** database occupied ~257 MiB. That is not overhead per row: the allocator preallocates a fixed 16384-page (~256 MiB) runway ahead of the highest allocated page via `fallocate` in mode 0, which reserves real blocks rather than making the file sparse. The cost is paid **once per database**, so a host running many small databases pays it many times over — 10 databases is 2.56 GB before a single row.
- `prealloc_ahead_pages` now sets the runway, via the config file / `SET CONFIG` for `nextsqld`, or `--prealloc-ahead-pages` on the `nextsql` commands that create a database (`init`, `realm create`, `database create`). **The default is unchanged at 16384 pages**, so no existing deployment changes behaviour; `prealloc_ahead_pages = 64` yields a 1.04 MiB empty database instead of 257 MiB. Values below 1 are refused rather than silently disabling preallocation.
- The `EnsureCapacity` docstring claimed "a slack of 1024 pages" while the constant was 16384 — 16× apart. Corrected, and the per-database footprint is now documented in `docs/storage-format.md`.

### Fixed — hot backups of a live database failed as "corruption" (2026-09-08)

- `BACKUP DATABASE` against an actively-written database failed with `corruption: source size changed during copy`, leaving no backup. Nothing was actually corrupt: `backup.sealFile` records the source size once at the start, then read the file **to EOF** and required the bytes copied to equal that recorded size — so any write that extended the file mid-copy made the copy longer than the header said and the backup was rejected. The busier the database, the more reliably its backups failed, which is exactly when they matter.
- A backup member is defined as the file's first `st.Size()` bytes — the consistent prefix as of the moment the backup started — with WAL replay from the checkpoint LSN covering everything written afterwards. The read is now bounded to that snapshot, so growth during the copy is ignored. The size check is kept, and still catches the genuine failure it was meant to catch: a source that *shrank* mid-copy.
- `TestCreateFromEngineUnderConcurrentWrites` existed but reported only "~1 concurrent writes during it" — a single write that never extended the file — which is why this survived. Verified against a live 269 MB database under continuous writes: the backup now completes, is restore-tested, and is recorded in `system.backups`.

### Fixed — NextSQL Admin offered a role as its own recipient (2026-09-08)

- The **Role membership** form listed every user *and* role as a recipient, including the role being granted — so `testrole` appeared under "Grant to" while `testrole` was the selected role. Granting a role to itself records a meaningless self-membership: the engine's `GrantRole` only checks that the role exists, and its fixpoint expansion tolerates the cycle rather than rejecting it, so nothing would have complained.
- The selected role is now excluded from its own recipient list (a role may still be granted to a *different* role — nested roles are expanded transitively), and the server refuses `role == grantee` outright, case-insensitively, so a hand-crafted request cannot create one either.

### Added — NextSQL Admin: RBAC forms are pick-lists, and backup_dir is settable from Backups (2026-09-08)

- The **Grant / revoke privilege** form is now dropdowns end to end. Table, column, resource-group, role, and grantee are chosen from what the server reports the operator may actually see — `system.tables`, `system.columns`, and `system.resource_groups` join the Security read model as non-required, RBAC-filtered reads — instead of being typed from memory. Choosing a table narrows the column list to that table's columns. Only SCHEMA and FUNCTION stay typed, because no catalog view enumerates them, and the field says so.
- Every picker **explains itself when there is nothing to choose** rather than showing a dead dropdown: an empty grantee list says no users or roles are visible and that one must exist to receive a grant; an empty table list says so; and so on.
- **backup_dir** can now be set from the Backups view. The old copy told the operator to edit `nextsql.conf` and restart, without saying how, and hid the real precondition — `SET CONFIG` only persists when `nextsqld` was started from a configuration file. The view now offers a directory field writing through the existing config route, and when the node was started from flags it says exactly that and names the fix (`--config /path/to/nextsql.conf`) instead of surfacing the engine's raw "nothing to persist" error.

### Fixed — NextSQL Admin: wide tables were clipped, tab strips had no scroll affordance (2026-09-08)

- A result table wider than its container was **unreachable**: RUI's `Table` wraps itself in a rounded card with `overflow: hidden`, which sat between the table and the labeled scroll container. The card clipped the overflow and hid it from the scroller above, whose `scrollWidth` therefore never grew — measured as a 1087px audit-log table inside a 792px scroller reporting `scrollWidth` 792 and refusing to scroll. The inner element now sizes to its content and the card no longer clips, so the same table reports `scrollWidth` 1088 and scrolls.
- A tab strip with more triggers than fit scrolls, but RUI hides its scrollbar, leaving no affordance and — on a mouse with no horizontal wheel — no way to reach the tabs past the edge. Operations tab strips now show a slim, draggable scrollbar.
- The top-bar search button label is now just "Search…".

### Fixed — NextSQL Admin: tab icons and labels misaligned (2026-09-08)

- In a tab strip with enough triggers to fill the row (Diagnostics' nine metric categories: Throughput, Latency, Encryption, Storage, Replication, Maintenance, CDC, Runtime, Constraints), each trigger's contents **wrapped onto two lines** — icon on one, label and count on the next. `align="center"` only centres items *within* a flex line, so the icon ended up 11.8px above its own label. Measured in a real browser; the trigger box was 59.8px tall instead of 36px.
- The icon must never separate from the label it names, so the `Inline` inside a `TabsTrigger` no longer wraps (24 occurrences across Overview, Activity, Databases, Cluster, Maintenance, Security, and Diagnostics — all the same latent defect, visible only once a strip runs out of width). The strip itself was already horizontally scrollable, so a long row still reaches every tab.
- Also corrected a stale expectation in the browser accessibility suite: the Studio result grid's selection status now reads "1 of 250 selected" rather than "1 selected", which had been left failing.

### Added — NextSQL Admin: RBAC administration in Operations mode (2026-09-08)

- The Security view could only **read** users, roles, and grants: there was no write counterpart to the Cluster / Maintenance / Config / Backups action routes, so creating a principal or changing a grant meant hand-writing SQL in the Studio editor.
- New `POST /api/v1/security/action` plus matching UI: **create user** (with password confirmation), **create role**, **drop user** / **drop role** behind an explicit confirmation, **role membership** (grant/revoke a role to a principal), and a **privilege grant/revoke** form covering every documented privilege and scope, including `COLUMN table.column` and `RESOURCE GROUP`.
- Requests are **structured, never raw SQL**. The server renders the documented statement from a closed set of ops, privileges, and scopes; every interpolated name must pass the plain-identifier check first, and a password is escaped as a SQL string literal and never logged or echoed in an error.
- Statements run on the **operator's own authenticated connection**, so server-side RBAC remains the only authority — the route can grant nothing the signed-in operator could not grant by typing the same statement, and the engine's refusal surfaces as `403`. Session + CSRF are required, like every other mutating route.

### Fixed — ungrouped aggregate over empty input returned no row (2026-09-08)

- `SELECT COUNT(*) FROM t WHERE <matches nothing>` returned **zero rows** instead of one row containing `0`; `SUM` / `AVG` / `MIN` / `MAX` likewise returned no row instead of one `NULL` row. Drivers surface "no rows in result set" for that, so a routine existence/size check either errored or silently read as "missing" rather than "none".
- An aggregate with no `GROUP BY` is defined over one implicit group covering the whole input, so it now always reports exactly one row. Fixed centrally in `aggregate.Hash.Finish()` (every hash/stream/parallel/partition-wise path merges into one table before finishing) plus the `planner.Empty` short-circuit in `execSelect`, which returned early for a constant-false filter without running the pending aggregate.
- A natively empty table (`SELECT COUNT(*) FROM empty`) was already correct, which is why this only showed up with a filter. `GROUP BY` still correctly returns zero rows on empty input.
- Covered by `aggregate.TestUngroupedEmptyInputEmitsOneRow` / `TestGroupedEmptyInputEmitsNoRows` and the executor-level `TestUngroupedAggregateOverEmptyInput` (both the runtime-empty and constant-false paths).

### Fixed — `CREATE DATABASE` created unreachable orphans on registry-backed deployments (2026-09-08)

- On a deployment with a hosting registry (every `nextsql init` deployment), the SQL statement created a bare sibling database file with no realm, no registry record, and no routing entry. Nothing could ever connect to it — `Hello` resolves names through the registry, so the new name returned `not_found: unknown database` — while the file still consumed a full database's worth of disk, and it never appeared in `SHOW DATABASES`, `system.databases`, or the Admin Databases view.
- It now fails closed with `invalid_argument` naming the supported path (`nextsql database create`), and leaves no file behind. `IF NOT EXISTS` does not bypass the refusal. Embedded deployments with no registry keep the sibling-file behavior.
- Covered by `executor.TestCreateDatabaseFailsClosedOnRegistryBackedDeployment`; documented in `docs/sql.md`.

### Fixed — NextSQL Admin login showed hardcoded test placeholders (2026-09-08)

- The Operations-mode sign-in form shipped `(test_db)` and `(test_realm)` as the Database and Realm placeholder text — leftover local test values presented to every operator as if they were the server's defaults. Both placeholders are removed; the existing "Optional" hints stay.

### Added — NextSQL Admin Studio editable data grid, staged-change review & transactional commit (2026-09-08)

- Studio query results now support **in-place cell editing**, **row deletion staging**, **row insertion staging**, a **staged-change review modal**, and **atomic transactional commit/discard** over the authenticated session connection (`BEGIN; ... COMMIT;`).
- Single-table queries without joins or unions over user catalog tables are detected as editable (`detectEditableTable`) when all required primary key columns are present (`isResultEditable`).
- Active cells can be edited via modal (`EditCellModal`, double-click or Edit cell button) with type badge, NULL toggle, and modified diff indicators; rows can be staged for deletion (`stageRowDelete`) or inserted (`stageRowInsert` via `AddRowModal`).
- Staged changes are clearly highlighted in the result grid (`nss-cell-modified`, `nss-row-deleted`, `nss-row-inserted`) and summarized in a prominent banner (`.nss-staged-bar`) with a **Review & Commit…** review modal (`StagedChangesReviewModal`) that enables per-item removal, discard-all, and transactional SQL script preview with copy and open-in-editor actions.
- Committing runs the statements inside an explicit `BEGIN; ... COMMIT;` transaction on the session connection with automatic rollback on failure, and refreshes the query results on success.

### Added — NextSQL Admin Studio live parser diagnostics (2026-09-07)

- The Studio SQL editor now shows **where a statement fails to parse** as you
  type: a debounced strip under the editor with one entry per broken
  statement ("Line L, column C: <reason>.") and a **Go to error** button that
  jumps the caret to the offending token. It is advisory — nextsqld still
  parses, binds, and authorizes on Run — and covers **grammar** errors only;
  an unknown table or column is still reported by the server when the
  statement executes (the Admin client has no catalog of its own).
- New parser-only endpoint `POST /api/v1/studio/query/diagnostics` (no driver
  connection, no query slot, authed + CSRF like `/query/analyze` and
  `/query/split`). It splits the buffer with the same
  `';'`-in-string/comment-safe lexer pass Execute Script uses and reports
  each statement's first parse error mapped back to a whole-buffer line and
  column.
- `internal/sql/parser` gains `ParseDiag` (`Parse` is now a wrapper over it):
  on any failure it returns the byte offset of the token the parser stopped
  on, without changing the ~100 individual parser error sites.
- RUI's `CodeEditor` has no text-overlay primitive, so this is a strip rather
  than an inline squiggle — the same constraint that blocks NextSQL-native
  syntax highlighting. Binder ("unknown column"/"unknown table") diagnostics
  with a source position remain open.

### Added — NextSQL Admin Studio vector dataset import (2026-09-07)

- Studio gains an **Import vector dataset…** action (Data toolbar menu +
  command palette) for loading a development embedding dataset. It maps one
  document field of embeddings onto a table's `VECTOR` / `BITVECTOR` /
  `SPARSEVECTOR` column and any other fields onto its scalar columns, then
  builds `INSERT` statements into the active editor tab **for review** — it
  never executes and adds no server route (it reuses `GET /api/v1/studio/table`
  for the target table's authorized `system.columns`).
- Each embedding cell (a JSON array, a bracketed `[1, 2, 3]`, a parenthesized
  `(1, 2, 3)`, or a bare comma list) is validated against the column's
  declared dimensions and, for a `BITVECTOR`, its 0/1 domain — a wrong-length
  or non-finite vector is a named `Row N` error, never silently padded. The
  emitted literal is the exact native parenthesized form
  (`INSERT INTO docs (sig) VALUES ((1, 0, …));`); a `SPARSEVECTOR` column
  takes the same dense form and the server coerces the zeros away.
- Bounded: the shared 8 MiB input / 1 MiB output ceilings plus a
  1,000-row cap and a 262,144 rows×dimensions ceiling that fails with an
  actionable message. Scalar cells reuse the CSV/JSON importer's per-kind
  validation and 100-row batching. Pure `buildVectorImportSQL` /
  `autoVectorImportField` (`resultTools.ts`), unit-tested.

### Added — NextSQL Admin Studio SQL formatter (2026-09-07)

- The Studio query editor gains a **Format** button (also Shift+Alt+F, also a
  command-palette entry) that reflows the active tab's SQL for readability:
  normalized whitespace, uppercased keywords, a line break before each
  top-level clause keyword / `JOIN` / statement-level `AND`/`OR`, and
  one-per-line `SELECT`/`SET`/`VALUES` lists. It is a self-contained,
  comment-preserving client-side formatter (`formatSQL` in `resultTools.ts`) —
  not built on the server lexer, which discards comments and folds case.
- It never runs anything and adds no server route. A strict safety net
  re-tokenizes the formatted text and returns the buffer **unchanged** unless
  the significant-token stream is byte-for-byte identical (keywords aside) —
  as it also does for an unterminated string/comment/quoted identifier or a
  buffer over 1 MiB. Parenthesized groups (column-definition lists, `VALUES`
  tuples, subqueries) are kept on one line by design.

### Fixed — NextSQL Admin Studio connection status (2026-09-07)

- Studio no longer shows a static **Connected** badge. Ops and Studio now
  share a live probe of the session's nextsqld driver connection
  (`GET /api/v1/connection`). If nextsqld is down, both surfaces show
  unreachable/Disconnected (the admin session cookie stays valid so editor
  buffers are not thrown away). Run is disabled until the probe succeeds
  again. Polls every 5 s while the tab is visible.

### Changed — NextSQL Admin Studio More menu (2026-09-07)

- Studio's query editor keeps Run, Run script, Cancel, Suggest, Find, Saved,
  and History on the toolbar. Explorers and builders (full-text, vector,
  hybrid, geo, grants, users, audit, transactions, workflows, migrations,
  schema diagram, schema designer, generate/import data, parameterized DML)
  now open from a **More** control with a three-dot icon, grouped by Search /
  Security / Operations / Schema / Data. Capability-gated items stay in the
  menu and disable when the server does not report support. Command palette
  entries are unchanged.

### Changed — NextSQL Admin console visual language (2026-09-07)

- Operations, Setup, and Studio now share a denser operator-console chrome:
  grouped sidebar with a stroke icon on every nav item (collapses to an
  icon-only rail), identity chip, compact topbar, section panels around
  catalog tables, and a two-pane sign-in layout on wide screens. Layouts
  stack or scroll at the existing 899px / 760px / 799px breakpoints (phone
  through desktop) without a horizontal page overflow. Presentational only
  — routes, RBAC, and API behavior are unchanged.

### Changed — NextSQL Admin Setup shows actionable errors (2026-09-07)

- When `nextsql setup` (or the form validator) rejects a plan or install, the
  Setup wizard now leads with a plain-language headline and next step written
  for a first-run operator instead of the raw `nextsql invalid_argument: …`
  text. The verbatim engine message is always kept one "Technical details"
  click away (expanded automatically when the input is unrecognized), so no
  information is lost. Covers the recognized failure classes: a non-loopback
  listen address without TLS, an existing config or initialized database, a
  production profile missing an administrator / unlock key / resource-policy
  setting, an unlock key left on the data volume, unwritable target paths, a
  full disk, a failed post-install health check, and a missing `nextsql`
  binary. Purely presentational — the engine/CLI stays the one authority on
  what failed. See `docs/design-admin-setup.md`.

### Changed — container first-start initialization (2026-09-07)

- The container image now performs first-start initialization through
  `nextsql setup` (the same non-interactive backbone the OS installers use)
  instead of a bare `nextsql init`. It sizes the buffer pool from a resource
  preset, applies a deployment profile, writes `nextsql.conf` into the data
  volume, and `nextsqld` loads that file (`--config`) on every subsequent
  start. New environment variables: `NEXTSQL_PROFILE` (`developer` default /
  `production`), `NEXTSQL_PRESET`, and `NEXTSQL_CONFIG_FILE`.
  `NEXTSQL_PROFILE=production` writes `deployment_profile=production` plus the
  production operational defaults and runs the fail-closed production preflight
  on every start — a production container without a bootstrap administrator,
  with the unlock key on the data volume, or with a non-loopback listen and no
  TLS 1.3 does not start. See `docs/docker.md`.

### Removed

- **The Deno driver (`drivers/deno`) is removed.** The official driver set is
  now Go, Node.js, Bun, PHP, Python, and Ruby (six). The wire protocol is
  unchanged. `drivers/js` (shared codec + types) is now used only by the Node
  (types) and Bun (runtime + types) drivers; the Deno-only `caCerts` TLS
  option is gone from the shared type surface.

### Changed — drivers published to language registries (2026-09-07)

- Official drivers are now published under MIT to their language registries and
  versioned independently of the engine (`0.1.0`):
  `@bzync/nextsql` (npm), `bzync/nextsql` (Composer / Packagist),
  `bzync-nextsql` (PyPI), `bzync-nextsql` (RubyGems), and
  `github.com/bzync/nextsql/drivers/go` (Go modules). The Bun client stays
  repository-distributed. New tag-triggered publish workflows:
  `gem-publish-ruby-driver.yml`, `pypi-publish-python-driver.yml`,
  `packagist-split-php-driver.yml` (git-subtree split to a `bzync/nextsql-php`
  mirror that Packagist watches).
- One-shot driver release: pushing a single `drivers-v0.1.0` tag (or running
  the `Release drivers` workflow with a version) verifies every driver manifest
  is at that version and then publishes npm + PyPI + RubyGems + Packagist in
  parallel. The four per-driver workflows are now `workflow_call` reusable and
  still fire on their own `<lang>-v*` tags for a single-driver release.
- `drivers/node/nextsql.d.ts` and `drivers/bun/nextsql.d.ts` are now
  self-contained copies of the shared `drivers/js/types.d.ts` (with a drift
  guard test) instead of a `from '../js/types'` re-export that would not
  resolve for an installed npm consumer.
- The repository `LICENSE` is MIT and now applies uniformly: `drivers/php`'s
  `composer.json` no longer says `proprietary`, and the README no longer says
  "the engine is proprietary".

### Security

- Bump `golang.org/x/crypto` 0.55.0 → 0.56.0 (GO-2026-6354, GO-2026-6355:
  DoS in `golang.org/x/crypto/ssh` channel handling). NextSQL does not use the
  `ssh` package — `govulncheck` reports no reachable call — but the module
  version is compiled into the release binaries and image scanners (Trivy,
  `vuln-type: library`) flag it as a fixable HIGH. Requires `go` directive
  1.25.0 → 1.26.0 (x/crypto 0.56.0's minimum).

### Changed — release-tag-only container image publishing (2026-09-07)

- The `bzynchub/nextsql` Docker Hub repository is set to "All tags are
  immutable", and `.github/workflows/docker-publish-image.yml` now publishes
  an image **only from a `v*.*.*` release tag push**, producing exactly one
  write-once `0.x.y` tag. It emits no moving tags (`latest` disabled; `edge`,
  `{{major}}.{{minor}}`, `{{major}}` removed) and **no per-commit `sha-<short>`
  tags** — master pushes and pull requests build and Trivy-scan the image for
  CI signal but never push it. Re-cutting a release means bumping the version.
  The stale `:latest`, `:0.0`, `:edge`, and `sha-*` tags on Docker Hub are
  left over from the earlier scheme and can be deleted; the workflow does not
  touch them.

## [0.0.1] — 2026-09-07

First tagged release. Cut to exercise the release/publish pipeline; the engine
is still pre-1.0 and under active development (see `TODO.md`). The container
image publish workflow tagged this `bzynchub/nextsql:0.0.1` (plus moving
`:0.0` / `:latest` / `:edge` tags later retired — see `[Unreleased]`).

### Changed — container image runtime is scratch (2026-09-07)

- The published `bzynchub/nextsql` image no longer ships Debian bookworm
  userland (`apt`, `glibc`, `openssl`, `pam`, `util-linux`, …). `nextsql`
  and `nextsqld` are already static (`CGO_ENABLED=0`); Docker Hub Scout
  was scoring unused OS packages (3 Critical / 13 High on
  `sha-0a1cc8e` / `edge`). Runtime is now `FROM scratch` plus the CA
  bundle, uid 10001, and a small Go PID-1 (`cmd/nextsql-entrypoint`,
  `internal/dockerentry`) that preserves the Compose HA seed/join-wait
  behaviour of the former `docker/entrypoint.sh`. There is no shell in
  the image; `docker exec … nextsql` still works.

### Added — Docker Hub image publishing (2026-09-07)

- `.github/workflows/docker-publish-image.yml` builds the `Dockerfile` and
  pushes multi-arch (`linux/amd64`, `linux/arm64`) images to
  `docker.io/bzynchub/nextsql`: a semver tag from a `v*.*.*` release tag and
  an `sha-<short>` on every build. (The initial version of this workflow also
  pushed moving `edge` / `{{major}}.{{minor}}` / `latest` tags; retired the
  same day — see `[Unreleased]`.) Pull requests touching the image build it
  without pushing. Requires the `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN`
  repository secrets.
- `Dockerfile` build stage now cross-compiles from `$BUILDPLATFORM` using
  `GOOS`/`GOARCH` instead of emulating the Go toolchain under QEMU for the
  non-native target, so the arm64 build no longer runs the compiler emulated.

### Added — live-production profile and fail-closed preflight (2026-09-06)

- `nextsql setup --profile production|developer` writes
  `deployment_profile=` plus, for production, disk-watermark, replica-lag,
  drain, statement/idle/lock timeout, and connection-limit defaults.
- Production setup refuses `--skip-init`, an unlock key on the data
  volume, and a mutating install without `--user`/`--password-file`.
  Dry-run still reports the missing administrator as a warning. A
  tmpfs/ramfs data volume is a production warning, not a hard refusal.
- `nextsqld --production` (and any config with
  `deployment_profile=production`) re-runs the same preflight at start and
  refuses to serve if it fails.
- Setup-mode GUI defaults to Production, requires an administrator, and
  disables skip-init on that profile.
- `system.capabilities`: `follower_reads` and `resource_groups` are
  `supported` (they were already production-gated; the registry was stale).
  `field_encryption_client` and `hosting_isolation` stay `experimental`.
  See `TODO.md` log #208.

### Changed — former P30 NextSQL Intelligence / built-in RAG removed from the product (2026-09-06)

- NextSQL Intelligence, RAG Playground, and `CREATE RETRIEVER` are **not in
  the product**. They are not deferred. Studio is a native IDE without an
  AI assistant. Full-text, vector, and hybrid search remain ordinary SQL.
  See `TODO.md` log #207 and `PROJECT.md` §56.

### Added — realm/database rename (Multi-database hosting M3-2, 2026-09-06)

- `nextsql realm rename --realm OLD --to NEW --confirm` and
  `nextsql database rename --realm R --database OLD --to NEW --confirm`
  change a managed realm or database's logical name without moving files or
  changing its stable ID. Colliding names fail closed; deleting/tombstoned
  databases cannot be renamed. Offline exclusive-lock CLI, same shape as
  suspend/drop. See `TODO.md` log #206.

### Added — NextSQL Admin Studio: table / index designer (P29, 2026-09-06)

- Studio can now design a `CREATE TABLE` or `CREATE INDEX` from a form
  (**Design schema…**, also command-palette **Design table…** /
  **Design index…**) and load the native DDL into the editor for review. It
  never executes the statement. Types come from a closed NextSQL kind list;
  a primary key is required; `nsql_` table names are rejected; index kind is
  restricted to what the authorized columns can actually support (B+Tree,
  UNIQUE, FULLTEXT, VECTOR, SPATIAL). Live preview updates as the form
  changes. See `TODO.md` log #205.

### Added — NextSQL Admin Studio: vector-aware completion (P29, 2026-09-06)

- The SQL editor's IntelliSense now completes `NEAREST` vector-column names
  and `USING` metrics from authorized `VECTOR` / `BITVECTOR` / `SPARSEVECTOR`
  columns on referenced tables (the same catalog fetch already used for
  table/column and JSON-path completion). Metrics are restricted to the
  column kind — a dense `VECTOR` column is never offered `HAMMING`. The
  `TO (...)` literal is not a completion slot: NextSQL exposes no
  per-element vector catalog. See `TODO.md` log #204.

### Added — NextSQL Admin Studio: layout persistence without credentials (P29, 2026-09-06)

- Studio now remembers explorer/inspector visibility and pane widths, plus the
  last selected table name, in per-connection browser storage. No SQL, result,
  query history, parameter, or credential is stored in that document. Accessible
  column splitters resize the panes; Hide/Show controls and a command-palette
  **Reset layout** restore the defaults. A stored table is re-opened only if it
  still appears in the authorized catalog. See `TODO.md` log #203.

### Verified — NextSQL Admin high-DPI browser rendering (P29, 2026-09-06)

- The real-Chrome accessibility suite now exercises Setup, Operations, and the
  authenticated Studio workspace at device-pixel ratio 2. It verifies each
  compact responsive layout, rejects document-level horizontal overflow and
  undersized raster sources, confirms bundled scalable fonts are ready, and
  reruns axe WCAG 2.2 AA before restoring default device metrics. This closes
  Studio's browser/CSS high-DPI checklist item; Windows/macOS package execution
  remains separately unverified. See `TODO.md` log #202.

### Added — NextSQL Admin Studio: parameterized INSERT / UPDATE / DELETE generation (P29, 2026-09-06)

- A new **Parameterized DML…** control in the Studio SQL workspace builds a
  positional-parameter (`$1..$N`) `INSERT`, `UPDATE`, or `DELETE` statement
  *template* for the chosen table from its authorized column metadata and loads
  it into the active editor tab **for review** — it never executes anything and
  adds no server route (it reuses `GET /api/v1/studio/table`). The emitted
  `$1..$N` placeholders are bound in the editor's existing Parameters panel
  before the operator runs the statement; `UPDATE`/`DELETE` require at least one
  WHERE key column so the statement targets specific rows, and every identifier
  is quoted. The Studio query-tab strip now scrolls horizontally instead of
  wrapping to multiple rows. See `TODO.md` log #201.

### Added — NextSQL Admin Studio: CSV / JSON import for development (P29, 2026-09-06)

- A new **Import data…** control in the Studio SQL workspace parses a pasted or
  loaded CSV / semicolon / TSV / JSON-array / NDJSON document, auto-maps its
  fields to the target table's authorized columns (with per-field overrides),
  and builds a bounded `INSERT` script into the active editor tab **for
  review** — it never executes anything and adds no server route (it reuses
  `GET /api/v1/studio/table`). Integer and boolean cells are validated against
  the target column type with named row errors rather than quoted guesses;
  every identifier and string value is quoted; input and output are bounded
  (≤8 MiB in, ≤2,000 rows, 100-row `INSERT` batches, ≤1 MiB SQL).
  `VECTOR` / geo / collection / `BLOB` columns are not offered as targets.
  See `TODO.md` log #200.

### Added — NextSQL Admin Studio: table-inspector Dependencies panel (P29, 2026-09-06)

- The Studio table inspector now shows a **Dependencies** section — the inbound
  "Referenced by" foreign-key grid together with the row triggers defined on the
  table, read from `system.triggers` (added as a non-required query to the
  existing `GET /api/v1/studio/table` bundle). The lazy schema tree gains a
  matching per-table **Triggers** sub-branch. No new route or capability;
  `system.triggers` already restricts each row to callers who can `SELECT` the
  table the trigger fires on. See `TODO.md` log #199.

### Added — NextSQL Admin Studio: development data generator (P29, 2026-09-06)

- A **Generate data…** control in Studio builds `INSERT` statements of synthetic
  rows from a table's authorized column metadata and loads them into the editor
  for review — it never executes anything and adds no server route. Generation is
  bounded (at most 1,000 rows, batched into 100-row statements, 512 KiB cap) and
  deterministic: the same seed always produces identical SQL. Per-column fill
  strategies cover the scalar types (`INT8..64`/`UINT8..64`, `DECIMAL`, `STRING`/
  `TEXT`, `UUID`, `BOOL`, `TIMESTAMPTZ`, `JSON`); `VECTOR`/geo/collection/`BLOB`
  columns are marked "not generatable" and left out, and a NOT-NULL column with
  no default that would be left out is a blocking error. See `TODO.md` log #198.

### Added — NextSQL Admin Studio: visible cross-database administration warning (P29, 2026-09-06)

- Studio's parse-only pre-run analysis now marks `CREATE`/`DROP USER` and
  `CREATE`/`DROP ROLE` as realm-scoped. Before execution, the confirmation names
  the connected realm/database and explains that the principal change affects
  every database in that realm; canceling runs nothing. Consolidated script
  confirmation applies the same rule, and destructive realm DDL retains both
  its destructive and cross-database reasons. Server-side RBAC remains the
  authority. See `TODO.md` log #197.

### Added — NextSQL Admin Studio: schema migration history explorer (P29, 2026-09-06)

- A read-only **Migrations…** explorer (toolbar + command palette) shows the
  database's `nsql_schema_migrations` history — version, name, applied-at,
  execution time, dirty flag, direction — through the logged-in operator's own
  connection (`GET /api/v1/studio/migrations`). A bounded summary reports the
  applied count, current version, and any dirty migration state, pointing at
  `nextsql migrate repair`. When the migration system has never run on the
  database (or the role cannot read the reserved table) the panel says so.
- Read-only by design: authoring, validating, dry-running, applying and
  reverting migrations stay with the `nextsql migrate` CLI, which needs the
  local migration files this protocol-only client never holds. First slice of
  the Developer-operations migration workspace. See `TODO.md` log #196.

### Fixed — NextSQL Admin Studio: result status line for writes (P29, 2026-09-06)

- The one-line result status under the editor now reports `N rows affected` (or
  `Statement completed`) for a column-less write or DDL instead of a misleading
  `0 rows`, and `N rows · M columns · T ms` for a read. See `TODO.md` log #195.

### Added — NextSQL Admin Studio: global command palette (P29, 2026-09-06)

- **Ctrl/Cmd+K** (or a **Commands** button) opens a searchable palette over
  Studio's own actions — run/cancel a query, open any explorer, switch
  connection, saved queries, and more. It only reaches existing actions; no new
  behavior. See `TODO.md` log #194.

### Added — NextSQL Admin Studio: recent-connections quick-switch (P29, 2026-09-06)

- The Switch-connection modal now lists the realm/database pairs recently
  switched to on the current server as quick-fill buttons (stored per host +
  user in the browser, bounded at 10, never a credential — the password is
  still required). See `TODO.md` log #193.

### Added — NextSQL Admin Studio: per-session read-consistency mode (P29, 2026-09-06)

- The Studio toolbar now has a read-consistency selector — **Strong** (default),
  **Bounded** (with a staleness bound in seconds), or **Stale** — applied live to
  the session's connection via `POST /api/v1/studio/read-consistency` (no
  reconnect; affects reads only, writes still go to the leader). A warning badge
  shows whenever the mode is not Strong so a stale result is never presented as
  authoritative. A realm/database switch resets the mode to Strong. See `TODO.md`
  log #192.

### Added — NextSQL Admin Studio: switch realm/database (P29, 2026-09-06)

- The Studio toolbar now shows the `nextsqld` address the session targets and a
  **Switch connection…** control that re-targets the session's connection to a
  different realm and/or database on the same server, without signing out.
- `POST /api/v1/studio/reconnect` opens a fresh authenticated connection (the
  password is supplied each time and never stored) and swaps it in atomically:
  a switch cannot race an in-flight query (`409`), and a failed open (wrong
  password → `401`, unknown realm, suspended database) leaves the existing
  connection untouched. First real connection-manager capability; named
  multi-host profiles and credential storage remain open. See `TODO.md` log #191.

### Added — trigger/schedule catalog and Studio workflow diagram (P29, 2026-09-06)

- Added read-only `system.triggers` and `system.schedules` definition views.
  Trigger rows follow visibility of their firing table; schedule rows follow
  visibility of their invoked workflow. Their shapes, native state values,
  lifecycle, and separated RBAC gates are pinned by executor tests.
- The Studio Workflows & CDC explorer now independently caps all five of its
  catalog/activity results at 500 rows and adds trigger/schedule tables plus a
  deterministic accessible relationship view:
  `TABLE → TRIGGER → WORKFLOW` and `SCHEDULE → WORKFLOW`. A labelled inline SVG
  is drawn only for a small complete graph; an always-visible bounded text
  alternative remains for keyboard/screen-reader and large-graph use. See
  `TODO.md` log #190.

### Added — NextSQL Admin Studio unified Constraints panel (P29, 2026-09-06)

- The Studio table inspector now combines the selected table's already
  authorized primary-key, UNIQUE-index, NOT-NULL, and foreign-key metadata into
  one bounded **Constraints** grid. Composite keys retain catalog ordinal order;
  UNIQUE remains explicitly labelled as an index with its predicate/include/
  status; and a duplicate `PRIMARY` backing-index row is suppressed. No new
  server route or catalog surface is involved. See `TODO.md` log #189.

### Added — NextSQL Admin Studio table DDL view (P29, 2026-09-06)

- The Studio table inspector gains a **DDL** section: the canonical
  `CREATE TABLE` and `CREATE INDEX` statements for the selected table (from
  the new `system.table_ddl` view, added to the table-detail bundle), shown
  as a copyable script with an **Open in editor** action. Degrades to no
  panel against a server without the view. See `TODO.md` log #188.

### Added — `system.table_ddl` canonical-DDL catalog view (P29, 2026-09-06)

- New read-only system view `system.table_ddl` (`table_name`, `object_type`,
  `object_name`, `ddl`): one row per visible table and one per index, where
  `ddl` is the canonical `CREATE` statement — the same rendering used by
  encrypted-backup SQL export, correct for foreign-key ordinals, expression
  and JSON-path indexes, vector index method/quantization, full-text
  analyzer, ENUM/CHAR/VARCHAR types, and `DEFAULT` literals. Filtered by
  `SELECT` on the table, exactly like `system.columns`. `SchemaVersion` is
  unchanged (a new view, not a column change).
- Internal: the DDL renderer moved from `internal/xport` to a new
  dependency-free `internal/catalog/ddl` package so the query executor can
  reuse it; backup/restore/tenant-migration behavior is unchanged. See
  `TODO.md` log #187.

### Added — NextSQL Admin Studio prepared parameters (P29, 2026-09-06)

- A Studio editor buffer that references positional placeholders (`$1..$N`)
  now shows a **Parameters** panel: one value field per distinct placeholder,
  each with a NULL toggle. The bind values are per-tab and ephemeral — never
  written to browser storage, never sent anywhere except with an explicit Run.
- Run sends the values as a bounded positional array (`params`) on
  `POST /api/v1/studio/query` / `.../stream`; at most 32 parameters, 64 KiB per
  value. Each value is bound as a string and coerced to the placeholder's
  expected type by nextsqld's binder (server-side RBAC and validation stay
  authoritative); a toggled slot binds a typed SQL NULL. Single-statement Run
  only. Closes the Studio MVP exit-gate "Prepared parameters" line. See
  `TODO.md` log #186.

### Added — NextSQL Admin Studio saved queries with tag folders (P29, 2026-09-06)

- A **Saved** panel in the Studio editor keeps named, tag-grouped SQL
  snippets, mirrored to `localStorage` per connection (text only, never sent
  anywhere). A "folder" is a tag — the panel filters by tag and by free text
  over name and SQL.
- Each saved query loads into the active tab without running, can be updated
  with the current buffer, renamed, or deleted. Bounded: 200 entries, 200,000
  chars of SQL each, 10 tags each. See `TODO.md` log #184.
- The panel also exports the whole set to a JSON file — a stable,
  order-independent document (`format: "nextsql-studio-saved-queries-v1"`,
  entries sorted by name, two-space indent) that diffs cleanly when checked
  into a repo — and imports one back, merging by entry id: new entries are
  added, an existing entry is replaced only by a strictly newer copy, and an
  older copy never clobbers a local edit. See `TODO.md` log #185.

### Added — NextSQL Admin Studio crash recovery for unsaved editors (P29, 2026-09-06)

- Each Studio editor tab's title and SQL text (only — not results, errors, or
  history) is mirrored to `localStorage` under a per-connection key and
  restored on load, so a browser crash, accidental close, or reload does not
  lose unsaved work. Bounded: at most 8 tabs, 200,000 chars per buffer,
  malformed data ignored, every storage access best-effort.
- A status notice on restore offers **Start fresh** to discard the recovered
  tabs. This is a deliberate, narrow exception to Studio's "query history
  never touches disk" rule — only the working buffer is persisted, never the
  run log. See `TODO.md` log #183.

### Added — NextSQL Admin Studio global object search (P29, 2026-09-06)

- A **Search objects…** finder in the Studio database explorer — a keyboard-
  driven modal over table names (from the initial catalog read) and workflow
  names (one authorized `system.workflows` read). Matching ranks exact,
  prefix, substring, then in-order subsequence, and never surfaces a
  non-subsequence guess.
- Selecting a table opens it in the inspector; selecting a workflow opens the
  read-only Workflows explorer. Columns and indexes are not searched (that
  would need a per-table fetch) — the finder says so. No new route or server
  surface. See `TODO.md` log #182.

### Added — NextSQL Admin Studio schema-relationship diagram (P29, 2026-09-06)

- A **Schema diagram…** explorer in the Studio toolbar shows an entity-
  relationship view built from the actual foreign keys. `GET
  /api/v1/studio/schema-graph` runs one authorized, RBAC-filtered
  `system.foreign_keys` read (capped at 4,000 rows); the browser collapses it
  to one edge per constraint, lays the tables out in dependency layers (cycles
  broken deterministically), and renders an inline `<svg role="img">` with a
  grouped **Relationships** list as its text alternative.
- Schemas with more than 40 related tables or 80 foreign keys, or a truncated
  read, show the relationship list alone. No graph/layout library is added —
  the layout math is pure and unit-tested. See `TODO.md` log #181.

### Added — NextSQL Admin Studio per-table foreign-key inspection, inbound + outbound (P29, 2026-09-06)

- The Studio database explorer now shows a table's foreign keys. The lazy
  `GET /api/v1/studio/table` bundle reads `system.foreign_keys` filtered by
  `table_name` (not required; empty for a table with none), carrying the same
  visibility filter as the rest of the bundle.
- The table inspector renders a **Foreign keys** section (raw
  `system.foreign_keys` rows, or "This table has no foreign keys."); the
  schema tree gains a per-table **Foreign keys** sub-branch — one leaf per
  constraint, `(child cols) → ref_table (ref cols)` with a non-`RESTRICT`
  `ON DELETE` action shown inline.
- The inspector's Foreign keys section also shows a **Referenced by** grid —
  FK constraints on other visible tables that point at the selected one, from
  a second `system.foreign_keys` read filtered on `ref_table` (only inbound
  references from child tables the caller can already see are listed). The
  schema-tree sub-branch stays outbound-only.
- An ER diagram from actual FKs remains open. Primary-key columns are already
  tagged `PK` in the Columns view. No new route or engine change.
  See `TODO.md` logs #179–#180.

### Added — `system.foreign_keys` catalog view (P29, 2026-09-06)

- New read-only virtual view `system.foreign_keys`
  (`table_name, constraint_name, ordinal, column_name, ref_table, ref_column,
  on_delete, on_update`): one row per referencing column, ordered by child
  table then constraint then 1-based `ordinal`. `on_delete`/`on_update` are
  `RESTRICT` / `CASCADE` / `SET NULL` / `SET DEFAULT`. Filtered by `SELECT`
  on the referencing (child) table — the same table-visibility rule as
  `system.columns` / `system.indexes`.
- Unblocks the Studio database explorer's foreign-key sub-nodes, a
  table-overview FK panel, and an ER-diagram-from-actual-FKs view. NextSQL
  has no `CHECK` constraint; PK / `UNIQUE` / `NOT NULL` are already exposed
  by `system.tables` / `system.indexes` / `system.columns`, so no separate
  `system.constraints` view is added.
- No persistent-format, protocol, or catalog-encoding change; `SchemaVersion`
  is unchanged (a new view, not a column change). See `TODO.md` log #178.

### Added — NextSQL Admin Studio lazy-loaded schema tree (P29, 2026-09-06)

- The Studio database explorer is now a lazy object tree instead of a flat
  table list. A **Tables** branch lists every authorized table (from the
  bootstrap read); expanding a table node fetches its existing
  `GET /api/v1/studio/table` bundle once and shows lazy **Columns**
  (`name · type`, primary-key columns tagged) and **Indexes** sub-branches.
  Selecting a table name still opens the full inspector.
- A read-only **Workflows** branch lazily runs the existing
  `GET /api/v1/studio/workflows` bundle on first open and lists each
  visible workflow (name, owner) — no run/cancel/edit action.
- No new route or server surface; every read goes through the operator's
  authorized official-driver session, so the system catalog's RBAC
  filtering stays the sole authority. Primary/foreign-key + constraint
  sub-nodes, a DDL view, and an ER diagram still need a server catalog
  surface that does not exist yet.
- Added real-browser expand-Columns / expand-Workflows / one-authorized-read
  assertions under axe WCAG 2.2 AA. See `TODO.md` log #177.

### Added — NextSQL Admin Studio production environment tag + read-only safety mode (P29, 2026-09-06)

- The operator can tag the current Studio connection's environment
  (development / test / staging / production) — a per-viewer browser
  preference keyed by realm/database/user, never sent anywhere and not a
  credential.
- A `production` tag shows a standing banner and turns on **read-only
  mode** (toggleable for the session). While read-only mode is on, every
  write statement — single or inside a script — asks for confirmation
  before running, individually overridable with "Run anyway".
- `POST /api/v1/studio/query/analyze` now also returns a `write` flag,
  classifying read vs write at the AST level with the same list nextsqld's
  own `executor.isMutating` uses. Advisory only — server-side RBAC remains
  the real protection.
- Closes the "Highly visible production indicator", "Production safety
  mode", and "Optional read-only production session default"
  connection-manager checklist lines. Added write-classification unit
  tests, an analyze-`write` integration assertion, and a real-browser
  tag/banner/confirm/toggle flow under axe WCAG 2.2 AA. See `TODO.md` log
  #176.

### Added — NextSQL Admin Studio RBAC-enforcement test (P29, 2026-09-06)

- Added `TestAdminStudioEnforcesRBAC`: with a real `security.ACL` and a
  limited user, it proves a Studio session cannot list, open, read, or
  `CREATE` anything the user's grants disallow — through
  `/studio/bootstrap`, `/studio/table`, `/studio/query`, and
  `/studio/workflows` — and that admin-only `system.*` views return zero
  rows rather than data or an error.
- Closes the Studio MVP exit-gate line "RBAC/realm/database tests pass".
  Test-only; no production behavior change. See `TODO.md` log #175.

### Added — NextSQL Admin Studio indexed-JSON-path completion (P29, 2026-09-06)

- The editor's catalog-aware IntelliSense now completes native JSON paths:
  when the caret is inside a dotted path (`metadata.tags…`), the suggestion
  panel switches from table/column names to the JSON paths a
  FROM/JOIN-referenced table is actually **indexed** on.
- Those paths come from the `system.indexes` rows the IntelliSense fetch
  already returns — the only JSON structure NextSQL exposes any metadata
  for — so completion never offers an inferred or free-typed path. No new
  server route or request; pure frontend logic plus editor wiring.
- Vector-aware completion and inline parser/binder diagnostics remain open:
  the latter was assessed this round and needs source positions threaded
  through ~100 parser error sites and the binder's name-resolution errors,
  a core-decoder change rather than a frontend slice.
- Added path-extraction / range-detection / prefix-ranking unit tests and a
  real-browser flow (dotted-path mode switch, offered path, accept) under
  axe WCAG 2.2 AA. See `TODO.md` log #174.

### Added — NextSQL Admin Studio Workflows, tasks & change-streams explorer (P29, 2026-09-06)

- Added a read-only Studio explorer (toolbar button "Workflows & CDC…")
  over the authorized `system.workflows` / `system.tasks` /
  `system.change_streams` catalog, served by one small dedicated bundle
  route `GET /api/v1/studio/workflows` built the same way as
  `GET /api/v1/studio/table`.
- Lists workflows, their durable scheduled tasks (with a client-side
  per-workflow filter), and the open CDC `SUBSCRIBE` consumers on the node
  with each subscription's `lsn` resume cursor; refetches on every open and
  explicit Refresh because task and subscription state is live.
- Read-only by design, matching the Transaction console and Audit viewer:
  workflow bodies and trigger/schedule definitions are authored through
  the editor's own `CREATE`/`ALTER` statements, there is no
  cancel/retry-task control, and a CDC subscription is paused/resumed only
  by its own consuming client (not Studio). A full trigger/schedule
  relationship graph and a canonical `CREATE TABLE` DDL view both remain
  blocked on server catalog surfaces that do not exist yet
  (`system.triggers`/`system.schedules`; canonical DDL emission).
- Added a live `CREATE WORKFLOW` → `system.workflows` integration
  assertion plus a `change_streams` result-presence check, a no-session
  401 check for the new route, and a real-browser flow (open/refetch/
  client-side filter/stream-lsn render/no-mutation-control) under axe
  WCAG 2.2 AA. See `TODO.md` logs #172–#173.

### Added — NextSQL Admin Studio table/index statistics inspection (P29, 2026-09-06)

- The Studio database explorer's lazy table detail now includes a
  **Statistics** section: the table's `system.table_stats` row (row count,
  `updated_at`) and its `system.index_stats` rows (per-index row count),
  or an explicit "run ANALYZE" note when neither is populated.
- Implemented by extending the existing `GET /api/v1/studio/table` bundle
  with two more authorized reads — no new route, no engine change. Both
  reads are non-required and share `system.tables`' exact table-visibility
  filter, so a user sees statistics only for tables they can already see.
- The section labels these as `ANALYZE`-written estimates, not a live
  `COUNT(*)`; Studio issues no `ANALYZE` itself.
- Added a live ANALYZE→`system.table_stats`/`system.index_stats`
  integration assertion and a real-browser section-render check with axe
  WCAG 2.2 AA. See `TODO.md` log #171.

### Added — NextSQL Admin Studio deterministic misspelled table-name suggestions (P29, 2026-09-06)

- Added a live, non-interrupting notice when the SQL editor's buffer
  references a bare (unqualified) FROM/JOIN table name that matches no real
  table but is a small Levenshtein edit distance from exactly one real
  catalog table name — with a one-click fix that rewrites every occurrence
  of just that name.
- Deliberately scoped to table names only: a schema-qualified target (e.g.
  `system.capabilities`), a truncated catalog read, or a tie between two
  equally-close real table names is never flagged or "corrected" by a
  guess. Column/alias/function-name misspelling suggestions are out of
  scope — a plain identifier scan cannot safely tell those apart from a
  bare table/column reference without real parser AST access.
- Investigated and ruled out reacting to a live server error instead:
  NextSQL's "unknown table"/"unknown column" errors never include the
  offending identifier's own name in their text today, so this is a
  purely client-side check against the already-loaded catalog, with no
  server or wire change.
- Added Levenshtein-distance/detection/tie/bound/apply unit tests and a
  real-browser flow proving the notice appears, fixes, disappears, and
  never false-positives on a real or schema-qualified name, plus axe
  WCAG 2.2 AA. See `TODO.md` log #170.

### Added — NextSQL Admin Studio catalog-aware IntelliSense (P29, 2026-09-05)

- Added a bounded, keyboard-operable table/column-name suggestion list
  (Ctrl+Space or a "Suggest" toolbar button next to Find). Table names come
  free from the already-loaded catalog; column names for a FROM/JOIN-
  referenced table are fetched lazily, only while the panel is open, through
  the existing per-table catalog route every native explorer already uses,
  capped at a 16-table cache.
- Deliberately excluded NextSQL keyword completion: the only ground truth for
  the keyword set is the lexer's own unexported table, and duplicating it in
  the frontend would silently drift.
- Reused the existing Popover/dialog primitive instead of a caret-anchored
  popup (`@bzync/rui`'s editor has no overlay primitive), with the ARIA 1.2
  combobox-with-listbox-popup role applied to the editor's underlying
  textarea so focus never leaves it while suggesting.
- Added pure extraction/ranking/word-range/cache-eviction unit tests and a
  real-browser flow covering mouse and keyboard-only acceptance, cache reuse
  across a close/reopen cycle, and axe WCAG 2.2 AA. See `TODO.md` log #169.

### Added — NextSQL Admin Studio query profiler breakdown (P29, 2026-09-05)

- Added an ANALYZE-only Profile view capped at 512 operators. It preserves the
  server's structural order and raw time/CPU/memory/disk/cache/spill/workers/
  index values while adding estimate-error factors and reported maxima for
  root/child time, memory, spill, and workers.
- Made the metric boundary explicit after auditing the executor: operator
  timings can include child work, CPU can mirror elapsed time, and root
  resources can be query-level. Studio never sums operators, invents
  percentages, or treats an unattributed zero disk/cache counter as proof of
  no I/O. Corrected the stale metric note in `docs/optimizer.md` accordingly.
- Added pure duration/profile/bound tests and a real-browser Profile flow that
  verifies server timing, a 100× estimate miss, reported memory, the visible
  non-additive warning, labeled table, and axe WCAG 2.2 A/AA. See `TODO.md`
  log #168.

### Added — NextSQL Admin Studio plan comparison (P29, 2026-09-05)

- Added bounded per-tab EXPLAIN/EXPLAIN ANALYZE baselines and comparison. A
  baseline is deep-copied, capped at 512 operators, retained across reruns and
  tab switches, isolated from other tabs, and never persisted or sent to the
  server.
- Added deterministic structural-path comparison with explicit
  same/metric/operator/added/removed states. The UI preserves the server's raw
  estimate and resource values, never guesses semantic node identity across a
  reshaped tree, and labels values as measured only when their snapshot came
  from EXPLAIN ANALYZE.
- Added Pin/Replace, Compare, and Clear actions plus an accessible summary and
  result table. Pure tests cover copy isolation, alignment, classification,
  and the 512-node bound; the real-browser regression covers replace/compare,
  analyzed measurement scope, tab isolation/persistence, clearing, and axe
  WCAG 2.2 A/AA. See `TODO.md` log #167.

### Added — NextSQL Admin Studio Audit viewer (P29, 2026-09-05)

- Added a read-only Studio Audit viewer reusing Operations Security's existing
  admin-only `GET /api/v1/security` read of `system.audit_verify` and
  `system.audit_log`; no new route, privilege path, or engine interface was
  introduced. It refreshes on every open/manual Refresh, surfaces partial
  bundle warnings, and shares a one-in-flight guard with Users/Roles and the
  GRANT/REVOKE suggestion loader.
- Extracted the existing Operations `AuditVerifyCard` into a shared component,
  keeping chain/signature counts, failure state, first-bad-line diagnostics,
  and problem text identical in both modes. The recent tail remains capped at
  200 server-side, retains suspect records after verification failure, and
  exposes no delete/clear/repair action or polling loop.
- Extended the real-browser regression from a verified chain through a
  simulated tamper detected on Refresh, asserting the failure diagnosis and
  suspect record remain visible; fresh open/refresh/reopen reads and axe WCAG
  2.2 A/AA also pass. See `TODO.md` log #166.

### Added — NextSQL Admin Studio Transaction console + Lock explorer (P29, 2026-09-05)

- Added a read-only "Transactions & locks…" Developer operations panel that
  reuses Operations mode's existing authorized `GET /api/v1/activity` bundle
  over `system.sessions`, `system.active_queries`, `system.transactions`, and
  `system.locks`; no new server route, privilege, or engine interface was
  introduced. The panel refetches on every open and manual Refresh, limits
  reads to one in flight, and surfaces partial-read warnings.
- Deliberately provides no cross-session kill/terminate/rollback action:
  NextSQL has no authoritative server operation for it, so the UI states that
  boundary instead of implying unsupported behavior.
- Fixed keyboard access to wide shared catalog tables after the new realistic
  active-query fixture exposed RUI's inner horizontal scroller to axe's
  `scrollable-region-focusable` rule. `ResultTable` now uses a labeled,
  focusable outer scroll owner with distinct Activity-table labels in both
  Operations and Studio. The real-browser test verifies rendered live rows,
  no fabricated kill control, Refresh and reopen refetches, and axe WCAG 2.2
  A/AA. See `TODO.md` log #165.

### Added — NextSQL Admin Studio Users & roles privilege explorer (P29, 2026-09-05)

- Added a read-only Users & roles privilege explorer (Developer operations
  scope) reachable next to the GRANT/REVOKE builder, reusing the same
  admin-only `GET /api/v1/security` read Operations mode's Security view and
  the builder's grantee suggestions already use — no new server route. A
  non-admin connection sees empty sections, never an error, matching that
  existing convention.
- Each `system.grants` row's "Revoke…" action reverses its exact
  grantee/privilege/scope/object back into a `GrantBuilderState` via a new
  `grantStateFromRow` and opens the existing GRANT/REVOKE builder prefilled
  (a new optional `initial` prop) — it never executes anything itself; only
  the builder's own "Insert into editor" touches the active tab.
- Confirmed directly against `internal/security/rbac.go` and
  `internal/executor/security.go` before writing the reverse mapping:
  `system.grants` never carries a role-membership row (that lives in
  `system.roles.members` via a separate `ACL.GrantRole` path), and `ALL
  PRIVILEGES` persists as the single privilege `"admin"`, not a synthetic
  flag — so it round-trips as `REVOKE ADMIN ON ...`, a real statement, rather
  than a reconstructed flag that was never actually stored. No new server
  route, optimizer hint, privilege path, or persistent/wire/catalog format
  was introduced. See `TODO.md` log #164.

### Added — NextSQL Admin Studio Geo Explorer (P29, 2026-09-05)

- Added a capability-gated native Geo Explorer — the fifth and final
  originally scoped Studio native explorer — driven by the connected
  session's authorized `system.columns`/`system.indexes` metadata,
  restricted to a table's `POINT` column(s) (a spatial index and `WITHIN`'s
  point-vs-region argument order both require exactly a `POINT`). A fixed
  equirectangular click-to-draw world canvas is a convenience only — it is
  `aria-hidden`, since every coordinate it can set is also reachable through
  ordinary keyboard-accessible Longitude/Latitude/Radius fields and
  Add/Remove-vertex controls.
- Two query modes generate exact native SQL checked directly against
  `internal/sql/types/geo.go`: point+radius builds `WHERE DWITHIN(col,
  POINT(lon, lat), meters)`; polygon builds `WHERE WITHIN(col,
  POLYGON('(...)'))` over a ring auto-closed by repeating its first vertex.
  A valid shape reuses the existing `CellInspector` `GeoView` renderer,
  unchanged, for a zoomed confirmation preview.
- Reusing `GeoView` on a realistically wide literal surfaced a real
  pre-existing accessibility defect shared by all four of `CellInspector`'s
  raw-value `CodeBlock` call sites (a missing focusable scroll wrapper,
  failing axe's `scrollable-region-focusable` check on wide content) — fixed
  at the source for all four, not worked around locally. No new server
  route, optimizer hint, privilege path, or persistent/wire/catalog format
  was introduced. See `TODO.md` log #163.

### Added — NextSQL Admin Studio Hybrid Explorer (P29, 2026-09-05)

- Added a capability-gated native Hybrid Explorer (requires both `fulltext`
  and `vector` server support) that composes an optional single-condition
  structured filter (restricted to columns and operators that coerce
  cleanly — NextSQL has no `LIKE`/regex operator) with the same catalog-driven
  full-text and vector builders the standalone Full-text and Vector
  Explorers already use, into one native `[WHERE ...] SEARCH ... NEAREST
  ... LIMIT n` statement.
- Added an "Explain" action that runs `EXPLAIN ANALYZE` of the exact
  generated SQL through the existing bounded Studio stream; the
  already-implemented graphical EXPLAIN/EXPLAIN ANALYZE tree renders it
  automatically, with candidate counts coming from that tree's own per-node
  row figures. `Run search` labels its ordinal result rank "Hybrid rank",
  stating that NextSQL's reciprocal-rank-fusion score is not exposed as a
  single number. No new server route, optimizer hint, privilege path, or
  persistent/wire/catalog format was introduced. See `TODO.md` log #162.

### Added — NextSQL Admin Studio Vector Explorer (P29, 2026-09-05)

- Added a capability-gated native Vector Explorer driven by the connected
  session's authorized `system.columns`/`system.indexes` metadata. Operators
  can choose a table and `VECTOR`/`BITVECTOR`/`SPARSEVECTOR` column, a
  metric restricted to exactly what that column kind supports (verified
  against a real server's own rejection behavior), paste a query vector in
  several accepted shapes, and set a 1–100 Top-K.
- A live "Vector inspector" validates the pasted vector's dimension count
  against the column's declared size and its value domain for a `BITVECTOR`
  column before a query can be built. Generated `NEAREST` SQL safely quotes
  identifiers and can be copied, inserted without execution, or explicitly
  run through Studio's existing bounded NSQL stream. Confirmed there is no
  per-query HNSW/IVF/IVFPQ search-time tuning setting anywhere in NSQL, so
  the explorer states this rather than offering one. No new server route,
  optimizer hint, privilege path, or persistent/wire/catalog format was
  introduced. See `TODO.md` log #161.

### Added — NextSQL Admin Studio Full-text Explorer (P29, 2026-09-05)

- Added a capability-gated native Full-text Explorer driven by the connected
  session's authorized `system.tables`, `system.columns`, and
  `system.indexes` metadata. Operators can choose a table and valid
  full-text index (locking its ordered STRING/TEXT fields) or the documented
  sequential fallback, enter bounded native phrase/prefix/fuzzy/typo syntax,
  select rows/HIGHLIGHT/SNIPPET output, and set a 1–100 result limit.
- Generated SEARCH SQL safely quotes identifiers and string literals and can
  be copied, inserted without execution, or explicitly run through Studio's
  existing bounded NSQL stream. Results show truthful ordinal BM25 rank while
  stating that numeric scores are not exposed; match markers remain literal
  text rather than interpreted HTML. Ambiguous catalog field boundaries and
  non-valid indexes fail closed. No new server route, optimizer hint,
  privilege path, or persistent/wire/catalog format was introduced. See
  `TODO.md` log #160.

### Added — NextSQL Admin Studio JSON Explorer (P29, 2026-09-05)

- The bounded JSON result-cell inspector is now a dedicated native explorer:
  select any object/array/scalar node, inspect or copy its exact NextSQL path,
  switch between tree and unchanged raw JSON, and insert a safely quoted
  `SELECT <path> FROM <table> LIMIT 100` into the active editor without
  executing it. Numeric array positions use the parser behavior fixed in
  `TODO.md` log #158.
- When the source table is selected, path status comes from its authorized
  live `system.indexes` metadata and names valid matches. Special-key paths
  whose segment boundaries the current dotted catalog text cannot prove are
  marked ambiguous instead of guessed. Pure helper/parser coverage and the
  real-Chrome axe workflow are green. No new server route, privilege path, or
  persistent/wire/catalog format was introduced. See `TODO.md` log #159.

### Fixed — JSON array-index path syntax never actually parsed (P9, 2026-09-05)

- `docs/json.md`'s own documented example — a numeric path segment indexing
  a JSON array, e.g. `metadata.tags.0` — has never parsed since Phase 9
  shipped: the lexer fuses a dot immediately followed by a digit into one
  leading-dot float-literal token (the same rule that lexes `.5` as `0.5`),
  so the path never saw the token it needed to continue and always failed
  with a syntax error. `SELECT`, `WHERE`, and `CREATE INDEX` were all
  affected; the only working spelling was the undocumented workaround of
  quoting the numeric segment (`tags."0"`).
- Fixed narrowly in `internal/sql/parser` (no lexer, binder, AST, or
  executor change): both places that build a JSON path from raw tokens now
  also recognize the fused leading-dot-digit token as a path segment,
  alongside the existing plain-dot case. Ordinary leading-dot float literals
  elsewhere in a statement are unaffected. Verified live against a real
  server (`SELECT`/`WHERE`/`CREATE INDEX`+`EXPLAIN IndexScan`/`SHOW INDEXES`
  all now correct) and covered with new parser, fuzz, and executor tests
  that didn't exist for this shape before. See `TODO.md` log #158.

### Added — NextSQL Admin Studio find/replace (P29, 2026-09-05)

- The Studio query editor gained a "Find" panel (also opened with Ctrl/Cmd+F
  while the editor has focus): Find, Match case, Previous/Next, Replace, and
  Replace all, operating only on the active tab's own SQL text.
- Matching is a literal substring search (never a regex) with wrap-around
  navigation. A match is shown by moving the editor's real text selection to
  it rather than a separate highlight overlay, which is why this was
  achievable where syntax highlighting and SQL formatting are not (both
  blocked on missing editor primitives — see `TODO.md` logs #154/#155).
  This closes every SQL-editor checklist slice except catalog-aware
  IntelliSense, source-position diagnostics, and saved workspace artifacts.
  See `TODO.md` log #157.

### Added — NextSQL Admin Studio GRANT/REVOKE builder (P29, 2026-09-05)

- Studio's query editor gained a "Grant / Revoke…" builder: a form that
  generates a `GRANT` or `REVOKE` statement (role membership, or a privilege
  list/`ALL PRIVILEGES` on any scope the grammar supports — cluster,
  database, schema, table, column, function, resource group, backup,
  replication, administration) and inserts it into the active editor tab for
  review. It never executes or bypasses the existing confirm-before-run
  check or server-side RBAC, and adds no new server route — grantee/role
  suggestions reuse Operations mode's existing Security-view read.
- Every generated identifier is safely double-quoted regardless of content.
  Found and documented a real, pre-existing SQL-grammar gap while grounding
  the generator against the live parser: `GRANT ALTER ...` cannot parse
  today even though the `ALTER` privilege exists at the engine level,
  because it lexes as a reserved keyword the GRANT/REVOKE parser doesn't
  accept — the builder excludes it rather than generating unparseable SQL.
  See `TODO.md` log #156.

### Added — NextSQL Admin Studio Execute Script (P29, 2026-09-05)

- The Studio SQL editor can now run a whole buffer of multiple statements as
  a script (`Run script`), tokenized server-side with a real lexer pass
  (never a naive `;` split) via the new `POST /api/v1/studio/query/split`.
- If any statement in the script is destructive (the same UPDATE/DELETE-
  without-WHERE and destructive-DDL classifier used for a single statement),
  one consolidated confirmation shows the statement count and reasons before
  anything runs, instead of interrupting the script partway through.
- Statements run one at a time, stopping at the first failed or canceled
  one; each statement's own status and result are listed and selectable, and
  every statement is recorded into query history the same as a single run.
- Fixed a real bug found via live verification: a DDL/DML statement's result
  reports `null` column metadata (no result set), which crashed the result
  grid the first time a script ran a non-`SELECT` statement through the
  reused non-streaming query path. The streaming path already handled this;
  the fix normalizes it at the API client boundary so every caller is
  null-safe. Covered by a regression test that reproduces the exact crash
  when the fix is reverted. See `TODO.md` log #155.

### Added — NextSQL Admin Studio multi-tab editing (P29, 2026-09-05)

- The Studio query editor now supports up to 8 independent tabs, each with
  its own SQL buffer and last result/error, switchable without losing
  either tab's content.
- The server allows only one active Studio query per session, so running a
  query in one tab shows every other tab a disabled "Busy…" Run action and
  a notice naming which tab is running, rather than ever attempting (and
  failing) a second concurrent query. A tab currently running cannot be
  closed; closing any other tab falls back to an adjacent one.
- Verified with a real-Chrome test that runs a deliberately delayed query in
  one tab, confirms a second tab shows the busy state and can't be closed,
  confirms both tabs' buffers/results stay independent across switches, and
  confirms tab creation stops at the 8-tab bound — plus axe audits, which
  caught and fixed a real touch-target-size violation on the tab close
  buttons. See `TODO.md` log #154.

### Added — NextSQL Admin Studio graphical EXPLAIN / EXPLAIN ANALYZE tree (P29, 2026-09-05)

- `EXPLAIN`/`EXPLAIN ANALYZE` results now render as a nested plan tree by
  default (with a toggle back to the ordinary grid) instead of only a flat
  table, reconstructed client-side from the existing result columns — no
  server or protocol change.
- Every operator shows its estimated rows; `EXPLAIN ANALYZE` additionally
  shows actual rows and CPU/memory/disk/cache/spill/workers/index. A plain
  `EXPLAIN` shows an explicit "Estimates only" notice instead — measured
  fields are never shown unless the statement actually measured them.
- An operator whose actual row count is ≥10x off its estimate is flagged as
  an estimation error (≥3x as a warning).
- Verified with parser unit tests built from real output captured against a
  live `nextsqld`, and a real-Chrome test that runs an `EXPLAIN ANALYZE`,
  confirms the estimation-error highlight and toggles between Plan and Table
  views, plus an axe audit (which caught and fixed a real ARIA violation on
  the view toggle). See `TODO.md` log #153.

### Added — NextSQL Admin Studio query history with privacy controls (P29, 2026-09-05)

- Every statement run through the editor is now recorded in a "History"
  popover (success with row count/elapsed, cancellation, or error). A
  canceled confirm-before-run dialog records nothing, since no query ran.
  Clicking an entry reloads its SQL into the editor without re-running it —
  a past destructive statement still passes the confirm-before-run check
  again on its own merits.
- The list is an in-memory, per-session record only: never written to
  browser storage or sent anywhere, cleared on reload/navigation, with an
  explicit "Clear" action and a 50-entry/2 MiB bound regardless.
- Closes the "Query history with privacy controls" SQL-editor checklist
  item. Verified with a real-Chrome flow that records four successful runs
  (confirming a canceled confirmation added nothing), reloads a past entry
  without triggering a new query, and clears the list. See `TODO.md` log
  #152.

### Added — NextSQL Admin Studio execute selection (P29, 2026-09-05)

- The editor's Run action (button or Ctrl/Cmd+Enter) now runs a non-empty
  text selection instead of the whole buffer, letting a user draft several
  candidate statements in one editor and run exactly one. The confirm-before-
  run destructive-statement check runs against the same selected substring,
  and the button relabels itself "Run selection" while one is active.
- `selectionStart`/`selectionEnd` are read directly from the editor's
  `<textarea>` at run time (not from reactive state), so the exact
  highlighted text is what gets analyzed and executed even though clicking
  Run moves focus away from the editor first.
- Closes the "Execute selection" SQL-editor checklist item. Verified with a
  real-Chrome interaction that highlights one statement inside a two-line
  buffer and confirms (via a fixture-captured request body) that only the
  highlighted text — not the full buffer — is analyzed and executed. See
  `TODO.md` log #151.

### Added — NextSQL Admin Studio confirm-before-run destructive-statement warning (P29, 2026-09-05)

- Before running an editor statement, the browser now calls a new
  `POST /api/v1/studio/query/analyze` route, which parses the SQL with the
  same `internal/sql/parser` package `nextsqld`'s own executor binds (no
  reimplemented heuristic, no driver connection) and classifies it. A
  statement is flagged when it is `UPDATE`/`DELETE` with no `WHERE` clause,
  `DROP TABLE`/`INDEX`/`USER`/`ROLE`/`WORKFLOW`/`TRIGGER`/`SCHEDULE`/`RESOURCE
  GROUP`, or `ALTER TABLE ... DROP COLUMN`/`DROP PARTITION`.
- A flagged statement shows a confirmation naming the reason (e.g. the table
  it would empty or drop) before the query ever runs; canceling executes
  nothing. A parse failure or other analyze error is not treated as a block —
  the statement still runs and the real executor stays the sole authority, as
  before this check existed.
- This closes the "Warn UPDATE/DELETE without WHERE using parsed AST" and
  "Warn destructive DDL" Connection-manager checklist items without requiring
  the larger multi-environment connection-manager scope (profiles, OS
  credential storage, TLS/mTLS fields, production mode), which remains open.
  Verified with unit tests, an HTTP-level integration test, a live-NSQL
  integration test against a real running server, and a real-Chrome
  interaction + axe audit of the confirm/cancel/run-anyway flow. See
  `TODO.md` log #150.

### Added — NextSQL Admin Studio general GEOMETRY/GEOGRAPHY multi-shape preview (P29, 2026-09-05)

- Extended the M3 native cell inspector's geo preview from the four fixed
  native shapes to the general `GEOMETRY`/`GEOGRAPHY` family's `MULTIPOINT`,
  `MULTILINESTRING`, `MULTIPOLYGON`, and `GEOMETRYCOLLECTION` values. The
  frontend parser mirrors the server's own WKT grammar exactly, recursively
  flattening a `GEOMETRYCOLLECTION`'s members (including nested collections)
  to the same 8-level depth the server enforces, under an independent
  point/part budget.
- `GEOMETRY` is planar and `GEOGRAPHY` is geodetic in an arbitrary per-column
  SRID — neither is necessarily WGS84 degrees like the four fixed shapes are
  — so the new general-shape path never applies the fixed path's longitude/
  latitude range check, and the inspector's "Coordinate order" fact now reads
  the declared column type instead of always claiming longitude/latitude.
- Added parser unit coverage for all four new shapes (both `MULTIPOINT`
  spellings, a nested-collection flattening case, unclosed-ring/unrecognized-
  keyword rejection, and empty-collection rejection), replacing the prior
  test that asserted the old raw-WKT-only fallback. `npm run typecheck`,
  `npm run test:studio`, and `npm run test:a11y` all pass; this is a
  browser-only change with no server route, persistent format, or protocol
  impact. See `TODO.md` log #149.

### Added — NextSQL Admin Studio M3 result tools and native inspectors (P29, 2026-09-05)

- Added active-cell and selected/all-loaded row copy plus local CSV/JSON
  export to the bounded virtualized query grid. CSV/TSV neutralizes formula
  prefixes and writes SQL NULL as `\\N`; the versioned JSON envelope preserves
  ordered/duplicate columns, types, strings, and NULL. Encoded output is capped
  independently at 64 MiB and is never sent to server storage or the PWA cache.
- Added type-aware inspectors for bounded JSON tree/raw views, dense/bit/sparse
  vectors, fixed `POINT`/`BOX`/`LINESTRING`/`POLYGON` coordinate previews, and
  canonical/UTC/browser-local/epoch `TIMESTAMPTZ` values. General spatial
  multi-shapes deliberately remain raw-WKT-only and stay open in the checklist.
- Added deterministic serializer/parser/bound tests and real-Chrome interactions
  for selection plus each inspector. The modal and full workspace pass axe WCAG
  2.2 A/AA; the audit found and fixed a dark-theme JSON-string contrast miss.
- A follow-up adversarial re-verification pass found and fixed three more real
  gaps: `serializeResult` now validates column-type/column and row-width
  consistency instead of trusting a malformed result; the export filename
  prefix uses locale-independent `toLowerCase()` instead of
  `toLocaleLowerCase()` (which folds differently under e.g. the Turkish
  locale); and the geo parser now fails closed on a `BOX` without exactly two
  corners, a `LINESTRING` under two points, or an unclosed/under-four-point
  `POLYGON` ring instead of rendering a wrong preview. See `TODO.md` log #148.

### Added — NextSQL Admin Studio M2 streaming and virtualized results (P29, 2026-09-05)

- Added a session+CSRF-protected `/api/v1/studio/query/stream` endpoint that
  sends ordered NDJSON metadata, bounded row batches, and a terminal completion
  or error frame through the official Go driver. Server limits remain 5,000
  rows, 8 MiB, and 25 seconds, with new 128-row/256-KiB batch targets and a
  1-MiB single-row ceiling; truncation cancels and drains the NSQL stream.
- The browser now parses and validates the stream incrementally, independently
  re-enforces frame/order/row/byte/column limits, reports progressive row
  counts, and mounts only the visible fixed-height result window plus bounded
  overscan. Typed headers, NULL rendering, cancellation, and connection reuse
  are preserved.
- Added unit, authenticated HTTP, live NSQL, race, and real-Chrome coverage for
  frame ordering, limits, failures, lock-wait cancellation, a 250-row bounded
  DOM, and axe WCAG 2.2 A/AA. P29 remains in progress; this closes only the
  streaming/virtualized result-grid checklist slice.

### Added — NextSQL Admin Studio M1 authenticated query workspace (P29, 2026-09-05)

- Replaced the Studio placeholder with a responsive explorer/editor/inspector
  workspace in the shared Admin shell. It discovers the connected server's
  capabilities, lists only authorized `system.tables`, lazy-loads table/
  column/index metadata, and executes one NextSQL statement through the
  logged-in operator's official-driver NSQL connection.
- Added session+CSRF-protected `/api/v1/studio/{bootstrap,table,query,
  query/cancel}` endpoints. SQL is capped at 1 MiB/25 seconds; catalog
  bootstrap at 1,000 tables; results at 5,000 rows or 8 MiB, with a separate
  1,000-row browser DOM cap. Excess results are explicitly marked truncated
  after the stream is canceled and drained.
- Added typed/NULL-aware result rendering, Ctrl/Cmd+Enter execution, explicit
  session-scoped cancellation, connection/database/realm context, and table
  overview/column/index inspection. The current states pass the real-Chrome
  axe WCAG 2.2 A/AA audit in the shared light/dark/system shell.
- Added `docs/design-admin-studio.md`, including the designed vs implemented
  vs tested vs production-gated audit. P29 remains in progress: the saved
  connection manager, IntelliSense/prepared parameters, general spatial/native
  explorers, EXPLAIN, migration, and production-safety surfaces are still open.

### Fixed — query cancellation before first response and during lock waits (2026-09-05)

- The Go driver now arms context cancellation before waiting for the initial
  `RowDesc`/`CommandComplete`; previously a query blocked before that frame
  could not be canceled.
- Transaction key/range waits now observe the executing query-budget context
  and remove canceled waiters from both the lock queue and wait-for graph.
  Deterministic TLS/NSQL and Admin HTTP integrations hold a real row lock,
  observe the waiting query via `system.active_queries`, cancel it, and prove
  the same connection remains reusable.

### Added — NextSQL Admin is now a fully integrated PWA (2026-09-05)

- Web app manifest (`manifest.webmanifest`), service worker (`sw.js`), and
  the icon set needed for install prompts/"Add to Home Screen", shared by
  every mode (Setup, Operations, Studio placeholder) since they're
  registered once from the top-level shell.
- The service worker precaches only the static JS/CSS bundle and applies
  stale-while-revalidate to everything else — but **never** intercepts
  `/api/*` (live session/auth/query data must always reach the real
  server) and **never** caches a Setup-mode one-time `?token=` URL. Cache
  name is a hash of the built bundle, so a new release always gets a fresh
  cache and the old one is dropped automatically.
- New Go routes (`GET /manifest.webmanifest`, `/sw.js`, `/favicon.ico`,
  `/apple-icon.png`, `/icons/*`), each reachable only at its top-level path
  — served from `internal/admin/webpwa/`, an embed sibling to
  `internal/admin/web/` rather than a subdirectory of it, specifically so
  none of them are also reachable under `/assets/` (which exposes `web/`
  wholesale). CSP gained explicit `worker-src 'self'; manifest-src 'self'`.
- Live-verified in real headless Chrome: the service worker registers,
  activates, and controls the page after reload; the app shell is cached;
  no `/api/*` URL ever appears in the cache.

### Verified — offline installation and no mandatory telemetry (P28, 2026-09-05)

- Confirmed no outbound network call exists anywhere in the setup/install/
  lifecycle code paths or packaging scripts (repo-wide grep for `net/http`
  clients, `curl`/`wget`, `http(s)://` literals), then proved it live: a
  full install → provision → server → real query cycle, and separately
  `nextsql-admin`'s Setup-mode wizard serving its first page, both inside a
  `docker run --network none` container with no network stack at all.

### Added — GPG-signed release/checksum pipeline (P28, 2026-09-05)

- `--gpg-key ID` (or `NEXTSQL_RELEASE_GPG_KEY`) on both
  `scripts/build-{linux,windows}-installer.sh` detached-signs the
  `SHA256SUMS.*` file via a new shared `sign_checksums` helper
  (`packaging/lib.sh`). Opt-in (no signing key exists in this repo/CI yet);
  once a key is given, a missing `gpg` or a signing failure aborts the
  build rather than silently shipping unsigned artifacts.

### Verified — silent/unattended install and upgrade/repair through the packaged installer artifacts (P28, 2026-09-05)

- `.tar.gz`/`.run`: zero-prompt install (isolated `HOME`/XDG env, `--no-gui`,
  no TTY) → `nextsql setup --json` → real server → real query →
  `uninstall.sh`, end to end.
- `.deb`: `DEBIAN_FRONTEND=noninteractive apt-get install`/`remove`/`purge` in
  a disposable container; confirmed `purge` still preserves
  `/var/lib/nextsql` and the root key by design.
- `nextsql lifecycle upgrade`/`repair`, using the actual packaged `.tar.gz`
  binaries rather than freshly built ones: both `--dry-run` and real runs,
  each followed by a live post-operation query against the same data.
- Closes Phase 28's "Silent install tested," "Upgrade tested," and "Repair
  tested" exit-gate items; the remaining items are Windows/macOS-hardware or
  code-signing-tooling blocked, plus M2's separately-scoped recovery-key gap.

### Fixed — packaging: `.rpm` build (two spec bugs) and a build-script error-handling gap (P28, 2026-09-05)

- `packaging/linux/nextsql.spec.in`: `%doc`/`%license` used relative paths,
  which RPM only auto-copies from a `%prep`/`%build`-populated build
  directory — this spec has neither (it copies pre-built binaries straight
  into `%{buildroot}`), so the copy always failed. Fixed to use the same
  absolute-buildroot-path form the rest of `%files` already uses. Also
  restored `nextsql-admin`, `USAGE.md.gz`, and `VERSION` to `%files` — files
  the shared staging tree installs but the spec never declared, caught by
  RPM's "installed but unpackaged files" check (which `.deb` has no
  equivalent of, so this was invisible there). `.rpm` is now built and
  live-installed end to end for the first time.
- `scripts/build-linux-installer.sh`: removed a `build_rpm "$arch" || true`
  that suspended `set -e` for that call's *entire* execution tree (a bash
  semantic: `-e` is ignored while a command's exit status is being tested by
  `||`/`&&`/`if`), meaning a real `go build` failure during RPM staging would
  have been silently swallowed and misreported as the benign "rpmbuild not
  installed" skip case rather than failing the build loudly. `build_rpm`
  already tolerates its two legitimate skip cases internally, so the
  call-site guard was redundant and harmful. Added `shopt -s inherit_errexit`
  to this script, `build-windows-installer.sh`, and `packaging/lib.sh` as
  defense in depth.

### Changed — NextSQL Installer + NextSQL Manager + NextSQL Studio merged into one product, NextSQL Admin (P28/P29, 2026-09-05)

- **One product, one binary.** `nextsql-manager` and `nextsql-install` are gone;
  `cmd/nextsql-admin` replaces both, with a new `internal/admin` package holding
  three modes: `setup` (formerly `internal/installgui`), `ops` (formerly
  `internal/manager`), and `studio` (new, placeholder only — Phase 29 is still
  unbuilt). Behavior of Setup and Operations modes is unchanged from the
  former Installer/Manager; this is a process/package/frontend restructuring,
  not a feature change.
- **Mode selection at startup**, not two simultaneous UIs: `nextsql-admin`
  shells out to `nextsql lifecycle detect --json` to decide Setup mode
  (no initialized install found — token loopback auth, ephemeral port,
  auto-open browser) vs. Operations mode (installed — session+CSRF login,
  fixed `127.0.0.1:7220` default). `--mode setup|operate` overrides detection.
  New unauthenticated `GET /api/v1/mode`.
- **One frontend.** New `internal/admin/frontend/` (React + `@bzync/rui`,
  same versions both prior apps used) replaces the two separate npm packages;
  shared `Brand`/`ThemeSelect`/API-client base deduplicated; a new hash-based
  router makes the Operations shell's tabs — including a new placeholder
  Studio tab ("Coming in Phase 29") — URL-addressable for the first time.
  Build output committed to `internal/admin/web/`.
- Two real bugs found and fixed during the merge: `@bzync/rui`'s `Select` is
  button/listbox-based (not a native `<select>`), which the accessibility
  test suite's theme-toggle interaction now matches; and a WCAG AA contrast
  failure in the Wordmark's accent color against the Operations sidebar's
  dark background (Setup mode's rail didn't have this problem).
- Docs restructured: new `docs/design-admin.md` (umbrella design), with
  `docs/design-manager.md` → `docs/design-admin-operations.md` and
  `docs/design-installer-gui.md` → `docs/design-admin-setup.md` (renamed,
  full implementation history preserved). `PROJECT.md`, `TODO.md`,
  `ARCHITECTURE.md`, and every cross-referencing doc updated.
- Verified: `go build ./...` / `go vet ./...` clean; `internal/admin/...`
  and `tests/integration -run TestAdmin` green under `-race`; frontend
  typecheck/build/axe a11y (WCAG 2.2 AA) green; live end-to-end against real
  binaries in both Setup and Operations modes.

### Changed — Installer M5 accessibility complete; Manager aligned to Installer UI/UX (P28, 2026-09-05)

- The Installer now exposes semantic ordered progress, current-step state,
  skip navigation, stable heading focus and live announcements across every
  step/install-result transition, correctly associated custom-combobox and
  form errors, a named resource-preset group, and keyboard-complete path
  suggestions. A password now requires matching confirmation before Continue;
  blank confirmation previously left it enabled.
- Installer and Manager now share the Installer's visual language: canonical
  wordmark, backdrop, self-hosted fonts, elevated main surface, 200px branded
  rail, spacing/responsive behavior, and an explicit persisted System/Light/
  Dark selector. Manager retains a wider table canvas and exposes responsive
  tab orientation correctly to assistive technology.
- Added bounded, dependency-light real-Chrome audits for both generated
  frontends (`npm run test:a11y`) over a shared CDP harness. They cover the
  full installer keyboard flow and Manager login/authenticated navigation,
  run axe's WCAG 2.2 A/AA-tagged rules, and verify emulated increased-contrast
  and reduced-motion behavior. The pass found and corrected two RUI 0.0.9
  fixed-color contrast misses at the application boundary.
- Fixed the Installer CSP to permit its build's inlined `data:` font assets
  (matching Manager's already-correct policy); the browser audit now runs
  under that production policy and asserts the Inter face actually loads.

### Added — GUI installer frontend rebuilt on React + @bzync/rui; M4 packaging integration for Linux (P28, 2026-09-05)

- New `internal/installgui/frontend/` — the wizard is now React + `@bzync/rui`
  (same stack, same dependency versions, same build pattern as NextSQL
  Manager), replacing M1's hand-written vanilla-JS version. Output committed
  to `internal/installgui/web/`; `go build ./...` still needs no Node
  toolchain.
- `scripts/build-linux-installer.sh` now bundles `nextsql-install` into
  every Linux artifact. `install.sh` (tarball/`.run`) auto-launches it as
  the default interactive entry point on a fresh, TTY-driven `--user`
  install (`--no-gui` opts out); `--system`/root installs and the `.deb`
  `postinst` only mention it as an alternative, never auto-launch it, since
  it would otherwise create the database as root instead of the
  unprivileged `nextsql` service account. `uninstall.sh` removes the binary
  too.
- Fixed: the "start at boot" checkbox's config-path check compared against
  the wrong default (`nextsql setup`'s own no-`--config-out` default, inside
  the data directory) instead of the packaged installers' actual `/etc` (or
  per-user `$XDG_CONFIG_HOME`) split — meaning it had been silently,
  permanently unusable for every packaged install since service registration
  landed. `internal/installgui.Params`/`Defaults` gained a `configOut` field
  wired to `nextsql setup --config-out`, prefilled from the same
  installer-convention split `dataDir`/`keyFile` already use.
- Fixed: the Administrator step's Continue button could disable itself (a
  too-short password, or a username without a password) with no visible
  explanation beyond a small muted hint — now always shows an explicit error
  banner naming the exact reason.
- Fixed: the page's dark/light theme background only painted as far as the
  wizard card's own height, leaving the browser's default white below it on
  a viewport taller than the card.

### Added — GUI installer M6 complete: service registration / start at boot (P28, 2026-09-04)

- The Resources step gained a "Start NextSQL automatically at boot"
  checkbox. It only ever enables an **already-installed, already-matching**
  systemd unit — it never authors/writes a unit file itself, that stays the
  packaged (`.deb`/`.tar.gz`/`.run`) installer's job. Disabled with a clear,
  specific reason whenever there is no matching unit to enable.
- New read-only `GET /api/v1/service` (detection) and a best-effort,
  non-fatal `systemctl enable --now nextsql` step folded into
  `POST /api/v1/install` when requested — its outcome never affects whether
  the database install itself succeeded.
- Fixed a real bug found during live verification: `systemctl enable --now`
  reports success once a start is *issued*, even if the process exits
  immediately after. The response now distinguishes "enabled to start at
  boot" from "actually running now" instead of conflating the two.

### Added — GUI installer M3 complete: remote listen address + TLS (P28, 2026-09-04)

- The Resources step gained an "Advanced: configure a remote listen
  address" section — a listen-address field plus TLS certificate/private-key
  path fields (paths only; content is never uploaded, same contract as the
  root key file).
- `nextsql setup`'s existing non-loopback-requires-TLS check
  (`ErrInsecureRemote`) is the one authoritative validator, reached the same
  way every other GUI mutation reaches its CLI validation; the UI adds only
  an advisory client-side hint.
- Closes M3 (skip-init and custom buffer pages already landed in M1) and the
  "TLS certificate assistant and validation" / "Component selection"
  `TODO.md` lines.

### Added — GUI installer M2 partial: generate-vs-import root-key disclosure (P28, 2026-09-04)

- `nextsql setup` / `nextsql setup --dry-run --json` now report whether
  `--key-file` (and the resolved `--instance-key-file`) already exist on
  disk before anything is written, via new `key_file_exists` /
  `instance_key_exists` fields plus a matching advisory in `warnings` — the
  pre-existing "an existing key is imported and reused, a missing one is
  generated" behavior was already correct, just silent.
- The GUI installer's Location and Review steps show this explicitly (a
  banner refreshed on every "Check", repeated on Review right before
  Install is confirmed) — closes the "Secure encryption setup wizard" and
  "Generate/import root unlock key" gaps in `docs/design-installer-gui.md`
  M2.

### Added — GUI installer M1 continuation: Advanced mode + graceful finish (P28, 2026-09-04)

- The Resources step gained an "Advanced" checkbox for `nextsql setup
  --skip-init` (write the configuration only, initialize the database
  later) — closes the "Standard vs Advanced installation" and "Component
  selection" gap for M1's scope.
- New `POST /api/v1/finish` + `Server.Done()`: the completion screen's
  "Finish" button now stops `nextsql-install` itself, so a GUI-only
  operator never has to switch to a terminal and press Ctrl+C.

### Added — GUI installer M1: architecture + serving backbone (P28, 2026-09-04)

- New `cmd/nextsql-install` binary + `internal/installgui` package: a
  loopback HTTP service serving an embedded first-run setup wizard (Welcome
  → Location → Resources → Administrator → Summary → Install → Completion),
  opened automatically in the operator's browser. Chosen architecture (via
  `AskUserQuestion`, see `docs/design-installer-gui.md`): the same "local
  web app served by Go" pattern as NextSQL Manager, but the installer never
  links the storage engine at all — every effect happens by driving the
  already-tested `nextsql setup` CLI as a subprocess (`--dry-run --json` for
  live preview, `--json` to commit). Enforced by an import-boundary test
  mirroring Manager's own.
- Single-run token authentication (Jupyter-style `?token=`), no login
  screen — there is nothing to authenticate against before the first
  database exists.
- The administrator password never touches argv or a log line: it is
  written to a mode-0600 temp file for the one subprocess call that needs
  it and deleted immediately after.
- New `internal/browseropen` package, extracted from `internal/oidcclient`'s
  pre-existing `DefaultBrowserOpener` (now a thin wrapper over it) — the
  installer and the OIDC login flow share one cross-platform "open a
  browser" implementation.
- Not yet done (tracked in `docs/design-installer-gui.md` M2–M5):
  recovery-key export, generate-vs-import key choice, a TLS certificate
  assistant, bundling `nextsql-install` into the OS packaging artifacts, and
  an accessibility/theming pass.

### Fixed — stale phase status in `AGENTS.md` / `.codex/AGENTS.md` (2026-09-04)

- Both repository-agent instruction files still claimed P0–P15/P26 complete
  and P16/P27–P30 open or planned; `TODO.md`'s own log already showed
  P0–P27 complete and the NextSQL Manager MVP done. Updated both to the true
  current release gate (P28, the GUI installer) so Codex/Grok-class agents
  reading `AGENTS.md` don't act on stale status.

### Verified / Fixed — Linux installer platform testing (P28, 2026-09-04)

- Live end-to-end verification of the three Linux packaging artifacts
  `scripts/build-linux-installer.sh` produces, on amd64: `.tar.gz`
  (`install.sh --user` → `nextsql init` → real `nextsqld` → `nextsql exec`
  → `uninstall.sh`, confirming binaries are removed and data/keys are left
  in place), the self-extracting `.run` (same flow via `nextsql version`),
  and the `.deb` (`dpkg-deb --info`/`--contents` against the `fakeroot`-built
  package). A live system-wide `dpkg -i` was not performed (would mutate a
  shared host's `/etc`, `/var/lib`, and systemd state). `.rpm` and the
  Windows installers remain untested end to end — `rpmbuild`/Wine are not
  available in this environment.
- Fixed a stale `dist/` output-path reference left over from an earlier
  rename: `scripts/build-linux-installer.sh`, `scripts/build-windows-installer.sh`,
  and `packaging/lib.sh`'s `usage_common` all documented `dist/` as the
  default output directory while the actual default is `installers/` in
  every script; `packaging/README.md` had the same stale path. All four
  now say `installers/`.
- Added `/installers/` to `.gitignore` — the default build output directory
  had no ignore entry, so a local build run showed as untracked (multi-MB
  binary) files in `git status`.

### Added — NextSQL Manager MVP slice M5: Backups, completing the Manager MVP (P28, 2026-09-04)

- New `BACKUP DATABASE` and `VERIFY BACKUP 'name'` SQL statements: create a
  verified encrypted backup of the connected node into its configured
  `backup_dir` (config key), and integrity-check an existing one. Both
  require the `BACKUP` privilege or cluster `ADMIN`, are node-local, and
  fail `Unavailable` if `backup_dir` is unset. `BACKUP DATABASE` cannot run
  inside a transaction.
- New `backup.CreateFromEngine`: a hot-backup path that reuses the server's
  already-open engine (checkpoint, then copy the file set while the server
  keeps serving; restore reconciles the fuzzy copy by replaying WAL). No
  second engine open and no second recovery pass, so neither can truncate a
  WAL tail the other is mid-write on.
- New `system.backups` virtual table: the verified backups in `backup_dir`,
  oldest first — `name, created_at, database_id, checkpoint_lsn,
  durable_lsn`. `BACKUP`-privilege gated; zero rows when `backup_dir` is
  unset.
- New config key `backup_dir`.
- Manager: `GET /api/v1/backups`, `POST /api/v1/backups/action`
  (create / verify), and a Backups view with a "Back up now" button and a
  per-row Verify action.
- **Restore and PITR stay offline-only** — a running server cannot restore
  into itself. The Backups view prints the exact `nextsql restore` command.
- **The NextSQL Manager MVP is complete**: all nine slices M1–M9, and the
  Phase 28 Manager exit gate is closed.

### Changed — system schema version 2 → 3 (P28, 2026-09-04)

- `internal/system.SchemaVersion` is now 3; `system.capabilities` advertises
  `system_schema_v3`. Driven by `system.config` gaining `file_value` /
  `restart_required` columns (Manager M8). Also covers the tables added
  during the Manager MVP: `system.tls`, `system.key_versions`,
  `system.audit_verify`, `system.audit_log`, `system.config`,
  `system.metrics`, `system.server_log`, `system.backups`.

### Added — NextSQL Manager MVP slice M8: Configuration — validated safe editor, completing M8 (P28, 2026-09-04)

- New `SET CONFIG key = value` SQL statement: persists one server setting to
  the node's on-disk `nextsql.conf` through the server (the client never
  names or touches a path). `value` is a string / number / `TRUE`/`FALSE` /
  `DEFAULT` (reset to the built-in default). Requires cluster `ADMIN`,
  node-local, cannot run in a transaction, audited `config.set`.
- **Persist-only**: nothing is hot-reloaded, so a change takes effect on the
  next `nextsqld` restart. `system.config` gained `file_value` and
  `restart_required` columns showing the pending difference (also surfacing
  a startup flag that overrode the file).
- The server must have been started from a config file (`nextsqld --config`)
  or `SET CONFIG` fails `Unavailable` — there is nothing to persist to.
- `config` is matched as a bare identifier, not a keyword, so
  `SELECT … FROM system.config` and anything else named `config` is
  unaffected.
- New `config.WithSetting` / `DiffState` / `SettableKeys` / `WriteFile`;
  new `executor.DB.SetConfigWriter` / `WriteConfigSetting`.
- Manager: `POST /api/v1/config/action` and an inline per-row editor in the
  Configuration view (key allowlisted against `config.SettableKeys()`; each
  changed row shows a "restart required" badge).
- The file is rewritten in canonical `key=value` form — comments are not
  preserved, and settings the built-in default populates are written
  explicitly.
- **M8 (Configuration) is now complete**: viewer plus validated editor.

### Added — NextSQL Manager MVP slice M9: Logs & Diagnostics — diagnostic-bundle export, completing M9 (P28, 2026-09-04)

- New `GET /api/v1/diagnostics/bundle`: one indented JSON document,
  downloaded as an attachment (`nextsql-diagnostics-<UTC>.json`).
- Assembled entirely from admin-only `system.*` read surfaces —
  `metrics`, `server_log`, `config`, `capabilities`, `storage`,
  `replication`, `replica_health`, `tls`, `key_versions`, and the
  audit-chain *status* (not the audit log content). Driver-only: no
  data-directory access, unlike `nextsql diagnose`.
- Every constituent is already redacted at its source (config scrubs
  addresses; tls/key_versions never carry key material); a top-level
  `note` states what is and isn't included. No tenant row data.
- The Diagnostics view gained a "Download diagnostic bundle" button.
- **M9 (Logs & Diagnostics) is now complete**: metrics panel, server-log
  tail, and diagnostic-bundle export.

### Added — NextSQL Manager MVP slice M9: Logs & Diagnostics — server-log tail (P28, 2026-09-04)

- New `system.server_log` virtual table: a bounded, in-memory tail of the
  running process's structured log — `seq, event_time, level, message,
  attributes`, newest 500 records retained, capped at 200 per query.
  Admin-only; zero rows when no log ring is attached (embedded/CLI use).
- New `internal/logging.Ring` + `logging.NewWithRing` — a fixed-capacity
  ring that mirrors every log record before the unchanged stderr JSON
  handler writes it. `nextsqld` now uses it; the Manager and auth-broker
  keep the plain `logging.New`.
- New `executor.DB.SetServerLogSource`/`ServerLogTail`, re-read on every
  query so the tail is always current.
- Folded into `GET /api/v1/diagnostics` (`{metrics, server_log}`); the
  Diagnostics view gained a level-badged log table.
- Addresses in log messages are **not** redacted (unlike `system.config`):
  a log line is freeform text and an admin diagnosing a connectivity fault
  needs it — same reasoning as `system.audit_log.remote`. This is an
  in-memory diagnostic tail, not a durable log store (the real log is
  still stderr / the service journal).
- Still to land under M9: the redacted diagnostic-bundle export.

### Added — NextSQL Manager MVP slice M9: Logs & Diagnostics — metrics panel (P28, 2026-09-04)

- New `system.metrics` virtual table: one row per counter/gauge in the
  process-wide metrics registry (`internal/metrics.Snapshot`) —
  `category, name, value, unit`, grouped into throughput / latency /
  encryption / storage / replication / constraints / maintenance / cdc /
  runtime. Admin-only; list-shaped, zero rows for embedded/CLI use with no
  process registry attached. Nothing redacted — the registry holds only
  counters and process resource stats.
- New `executor.DB.SetMetricsSource`/`MetricsSnapshot()`, the same
  settable-callback shape as the TLS/key-rotation/config sources.
- `nextsqld` now routes the connected database's own query/txn counters
  into `metrics.Default()` (via `SetMetrics`) — the same registry the
  crypto/storage/replication hooks already write to — so a single snapshot
  is internally coherent (`queries_per_second`/`commits_per_second`/
  `crypto_time_pct` all share one uptime base). This also fixes
  `nextsql status`'s `queries`/`commits`/`errors` counters, which were
  always zero for a real `nextsqld`.
- New `GET /api/v1/diagnostics` read-model and a Diagnostics view/tab;
  values are humanized by unit (bytes → KiB/MiB, nanoseconds → ms, etc.)
  with the raw value kept alongside.
- Reserved-word note: the grouping column is `category`, not `group`
  (`GROUP BY` — no quoted-identifier escape), the same pitfall
  `system.config`'s `name`-not-`key` already hit.
- Still to land under M9: the server-log tail and the redacted
  diagnostic-bundle export. See `docs/design-manager.md` M9.

### Added — NextSQL Manager MVP slice M4: audit-chain viewer, closing M4 (P28, 2026-09-04)

- New `system.audit_verify` virtual table: chain-integrity status (lines,
  legacy/chained/signed counts, whether signing is enabled and checked,
  overall verified/not, and the first bad line + reason if not). Admin-only;
  always exactly one row, `verified=false` with the rest blank when no
  audit log is attached.
- New `system.audit_log` virtual table: the most recent audit records
  (bounded to 200 per query regardless of file size on disk), admin-only,
  zero rows when not attached. Includes a record even when the chain is
  reported broken — an operator investigating a problem needs to see it,
  not have it hidden. `remote` (a client address) is not redacted, matching
  `system.sessions`'s existing convention.
- New `security.TailEvents`, extending the existing `security.VerifyFile`
  streaming scanner with a bounded ring buffer. New `executor.DB.
  SetAuditSource`/`AuditTail()`, wired by `nextsqld` at startup — unlike
  the TLS/key-rotation/config sources, this re-reads the file from disk on
  every query, since the point is to reflect what is durable right now.
- `GET /api/v1/security` extended with two new tables; the Security view
  gained an `AuditVerifyCard` and an "Audit log" table.
- Live-verified to detect an externally tampered file on the very next
  query with no server restart, and to still surface the suspect record.
- Two reserved-word column-naming fixes found live: `action` → `action_name`,
  `time` → `event_time` (same pitfall `system.config`'s `key` → `name` hit).
- Closes the Manager Security view's (M4) originally scoped surface:
  users/roles/grants, TLS status, key-rotation status, and the audit
  viewer are all now landed. See `docs/design-manager.md` M4.

### Added — NextSQL Manager MVP slice M8: Configuration viewer (P28, 2026-09-04)

- New `system.config` virtual table: one row per non-default setting in the
  running process's `config.Config`. Admin-only; list-shaped, so it returns
  zero rows (not a placeholder) for embedded/CLI use with no process-level
  config attached.
- Every network-address-shaped value (`listen_addr`, `raft_bind`,
  `raft_join`, `auth_broker_listen`) is redacted to `[redacted]`; nothing
  else is, since `Config` never holds key material or passwords, only file
  paths to them.
- New `config.Config.SafeEntries()` sources it, reusing `Marshal`'s own
  byte output rather than re-enumerating every field a second time. New
  `executor.DB.SetConfigSource`/`ConfigEntries()`, wired by `nextsqld` at
  startup — the same settable-callback pattern as `SetTLSStatusSource`/
  `SetKeyStatusSource`.
- New `GET /api/v1/config` + a Configuration view/tab.
- Read-only: a validated safe editor with restart-required indicators is a
  separate, not-yet-built increment. See `docs/design-manager.md` M8.

### Added — NextSQL Manager MVP slice M4 continuation: key rotation status (P28, 2026-09-04)

- New `system.key_versions` virtual table: one row per key the attached
  `crypto.Envelope` manages (`kek`, `master`, and each data domain — page,
  WAL, UNDO, backup, vector, full-text, temp, replication) with its current
  version and retained/revoked/retired counts. Never carries key material.
  Admin-only; unlike `system.tls`'s "always one row," this table is
  list-shaped and returns zero rows (not a placeholder) when no persistent
  envelope is attached.
- New `crypto.Envelope.KeyStatus()` sources it. New `executor.DB.
  SetKeyStatusSource`/`KeyStatus()`, wired by `nextsqld` at startup — the
  same settable-callback pattern as `SetTLSStatusSource`/`SetDrainFunc`.
- `GET /api/v1/security` extended with a `key_versions` table; the Security
  view gained a "Key rotation" table.
- A real pre-existing wrinkle found and documented (not introduced): a
  revoked version's count drops immediately but its revoked-flag lingers
  until a later retire; retire's current implementation never actually sets
  a "retired" flag, so the retired count is always 0 today.
- Known scoping gap, same as the TLS status entry below: wired only onto
  the legacy/non-hosted database handle. See `docs/design-manager.md` M4.

### Added — NextSQL Manager MVP slice M4 continuation: TLS status (P28, 2026-09-04)

- New `system.tls` virtual table: the live listener's redacted TLS status —
  certificate subject/issuer/validity/DNS SANs plus mTLS/CRL posture. Never
  carries private key material or a network address. Admin-only; always
  exactly one row for an admin (`enabled=false` with the rest blank when no
  TLS listener is attached), zero rows for a non-admin.
- New `security.ServerTLSReloader.Status()` sources it from the same live
  snapshot the TLS handshake path already serves, so a `Reload` rotation is
  reflected on the very next call. New `executor.DB.SetTLSStatusSource`/
  `TLSStatus()`, wired by `nextsqld` at startup — the same settable-callback
  pattern as the pre-existing `SetDrainFunc`.
- `GET /api/v1/security` extended with a `tls` table; the Security view
  gained a `TLSStatusCard` (labeled fact sheet with an expiry-urgency badge)
  instead of a raw table for this single descriptive row.
- Known scoping gap, documented rather than silently absorbed: wired only
  onto the legacy/non-hosted database handle (same scope `SetDrainFunc`
  already has) — under multi-database hosting mode a hosted session's
  `system.tls` does not yet reflect the process listener's real TLS state.
  See `docs/design-manager.md` M4.

### Added — NextSQL Manager MVP slice M7: Maintenance (P28, 2026-09-04)

- `GET /api/v1/maintenance` + a Maintenance view — `system.tables`,
  `system.indexes`, `system.table_stats`, `system.index_stats`.
- `POST /api/v1/maintenance/action` — issues `ANALYZE [table]` /
  `REBUILD INDEX name [ONLINE]` / `MAINTAIN DATABASE|TABLE name|INDEX name`,
  each already gated server-side (`SELECT`, `INDEX`, `ADMIN ON CLUSTER`
  respectively). The view requires a confirmation dialog before any of them.
- A table/index name is untrusted text interpolated into hand-built SQL (no
  quoted-identifier syntax exists in this dialect) — validated against the
  lexer's own bare-identifier grammar before use.
- Found and fixed a latent JSON-contract bug: `ANALYZE`/`MAINTAIN`/
  `REBUILD INDEX` report only an affected count with zero columns, and Go's
  nil-slice encoding rendered that as `"columns":null` against the
  frontend's non-nullable `ResultSet.columns` type. Not yet triggered by any
  existing view, but fixed at the source (`session.query`) plus a defensive
  check in `ResultTable`.

### Added — NextSQL Manager MVP slice M6: Cluster (P28, 2026-09-04)

- `GET /api/v1/cluster` + a Cluster view — `system.replication` and
  `system.replica_health` (both already always-visible, no admin gating
  needed for the read side).
- `POST /api/v1/cluster/action` — issues the exact documented
  `CLUSTER TRANSFER LEADER` / `DRAIN [WITH (TIMEOUT_MS = n)]` /
  `MAINTENANCE ENABLE|DISABLE` / `RECONCILE CONFIRM` statement for one of
  five actions, each already gated on `ADMIN ON CLUSTER` server-side. The
  Cluster view requires an explicit confirmation dialog before firing any of
  them.
- Verified live: `CLUSTER DRAIN` stops the listener and the entire
  `nextsqld` process then exits — not just the Manager's own session. The
  drain confirmation copy says this explicitly rather than implying a
  lighter-weight disconnect.
- M5 (Backups) was investigated first and found blocked: `nextsql
  backup`/`restore`/`verify` operate on the data directory directly, with no
  `BACKUP` SQL statement or `system.backups` table to wrap yet.

### Added — NextSQL Manager MVP slice M4 partial: Security viewer (P28, 2026-09-04)

- `GET /api/v1/security` + a Security view — `system.users`, `system.roles`,
  `system.grants` (all already admin-only server-side: a non-admin sees
  empty tables, never an error). Uses the same `runBundle` read-model
  helper as M1–M3.
- Scope note: M4's remaining pieces (TLS/certificate status, encryption-key
  rotation status, and an audit-chain viewer) did **not** land — nothing in
  `system.*` exposes that state today, and the only existing reader
  (`nextsql audit`) uses direct data-directory file access the Manager is
  architecturally forbidden from using. See `docs/design-manager.md` §6 M4.

### Changed — NextSQL Manager frontend rebuilt on React + `@bzync/rui` (P28, 2026-09-04)

- Replaced the M1 hand-written vanilla-JS shell with a React frontend using
  `@bzync/rui` — the same component library `docs/web` (the product site)
  uses. Server-observable behavior (the HTTP/JSON API, session/CSRF model,
  and embedding mechanism) is unchanged; this is a frontend-source swap only.
- New `internal/manager/frontend/` — a standalone npm package (not part of
  the Go module) whose `npm run build` (esbuild) bundles into
  `internal/manager/web/`, which is committed so `go build ./...` still needs
  no Node toolchain.
- CSP tightened from `default-src 'self'` to explicit per-directive rules
  (`script-src 'self'`; `style-src` allows `'unsafe-inline'` for the
  component library's runtime styles, which cannot execute code).

### Added — NextSQL Manager MVP slices M2 (Databases & Storage) + M3 (Connections & Activity) (P28, 2026-09-04)

- `GET /api/v1/databases` — `system.storage`, `system.databases` /
  `system.realms` (a `hosted` flag; empty-and-reported, not an error, on a
  single-database deployment), `system.tables`, `system.table_stats`.
- `GET /api/v1/activity` — `system.sessions`, `system.active_queries`,
  `system.transactions`, `system.locks`.
- Both share a new read-model bundle helper with Overview: a named set of
  `system.*` queries, where a non-required query's failure becomes a warning
  and an empty result instead of failing the whole view.
- Scope correction: the planned "query cancellation" line assumed a
  server-side cancel-another-session surface that does not exist in NextSQL
  (cancellation is client- or credential-driven, e.g. `nextsql token
  revoke`) — M3 is observe-only; see `docs/design-manager.md` §6.

### Added — NextSQL Manager MVP slice M1: serving backbone + Overview (P28, 2026-09-04)

- New `nextsql-manager` binary — the NextSQL Manager, a loopback HTTP service
  that serves an embedded operational-administration web UI plus a JSON API.
  It is a pure client of a running `nextsqld`: every operation runs as the
  logged-in operator's own NSQL user (server-side RBAC applies), it holds no
  credentials of its own, and it has no data-directory or key access.
- UI-framework decision recorded in the new `docs/design-manager.md`: a local
  web app served by Go, chosen over Electron / Wails / Fyne, with the M1–M9
  slice decomposition.
- M1 landed: the loopback HTTP server (a non-loopback `--listen` requires
  `--tls-cert`/`--tls-key`), an embedded HTML/CSS/vanilla-JS shell, an
  operator-credential session layer (`SameSite=Strict` cookie + per-session
  CSRF token on state-changing calls; bounded, self-expiring), and
  `GET /api/v1/overview` (server storage / replication state, live session
  and active-query counts, the capability registry — all from `system.*`).
- New `internal/manager` package; it imports none of the storage-engine
  internals (enforced by a test).

### Added — rolling-cluster upgrade integration for `nextsql lifecycle upgrade` (P28, 2026-09-04)

- `nextsql lifecycle upgrade` now detects Raft cluster membership offline (a
  `raft/` state directory, the key-free `nextsql.cluster.json` status file, or
  `node_id` + `raft_bind` in the config) and refuses to mutate a clustered
  node in place until `--cluster-node` acknowledges the node has been drained
  and, if it was the leader, that leadership was transferred. Without the flag
  the run stops at `blocked` (exit 6) with nothing mutated and prints the
  ordered per-node rolling procedure; `--dry-run` and `nextsql lifecycle
  detect` show the same procedure without the gate.
- `--json` output gains `cluster` (detection) and `rolling_upgrade`
  (`proceed` / `blocking` / `warnings` / ordered `steps`) objects so an OS
  installer or the NextSQL Manager can drive the sequence.
- New pure decision logic `internal/setup.PlanRollingUpgrade`
  (`ClusterUpgradeInput` → `ClusterUpgradeGuidance`). No catalog, storage
  format, wire protocol, or Raft change.
- `docs/install.md` and `docs/ops.md` ("Rolling upgrade") updated.

### Added — FROM-less `SELECT` (2026-09-04)

- `SELECT <expr-list>` with no `FROM` (e.g. `SELECT 1`, `SELECT NOW()`,
  `SELECT 1 + 1 AS n`) is now supported, evaluated once against no row/table
  context — the same bypass-the-binder precedent as `system.*` virtual
  tables. `SELECT *` still requires `FROM`. `WHERE`/`ORDER BY`/`LIMIT`/
  `OFFSET` apply to the single synthetic row; `GROUP BY`/`HAVING`/`SEARCH`/
  `NEAREST`/`FACET` are rejected at parse time (they need a table or index).
  A bare column reference fails closed rather than silently resolving to
  nothing. Fixes `CLAUDE.md`'s own documented quickstart
  (`nextsql exec ... -c "SELECT 1"`), which could not previously run.
- No catalog/persistent-format/NSQL-wire/Raft change.

### Added — Spatial types: `GEOMETRY` / `GEOGRAPHY` (Spatial track, 2026-09-04)

- New column types `GEOMETRY(subtype, srid)` and `GEOGRAPHY(subtype, srid)`
  — general OGC geometry (`Point`/`LineString`/`Polygon`/`MultiPoint`/
  `MultiLineString`/`MultiPolygon`/`GeometryCollection`) alongside (not a
  replacement for) the existing fixed `POINT`/`BOX`/`LINESTRING`/`POLYGON`
  WGS84 types, which are unchanged. `GEOMETRY` is planar/Cartesian;
  `GEOGRAPHY` is geodetic/great-circle (default SRID 4326). SRID and
  subtype are declared per column; a value coerces implicitly to/from the
  matching fixed shape.
- SRID registry `{0, 4326, 3857}`; `ST_Transform` covers `4326 ↔ 3857`.
- Constructors `ST_GeomFromText`/`ST_GeogFromText`/`ST_GeomFromEWKT`/
  `ST_Point`/`ST_GeomFromGeoJSON`; accessors `ST_X`/`ST_Y`/`ST_SRID`/
  `ST_SetSRID`/`ST_GeometryType`/`ST_NPoints`/`ST_NumGeometries`/
  `ST_GeometryN`/`ST_ExteriorRing`/`ST_InteriorRingN`/`ST_NumInteriorRings`/
  `ST_PointN`/`ST_StartPoint`/`ST_EndPoint`/`ST_Boundary`/`ST_Dimension`/
  `ST_IsEmpty`/`ST_AsText`/`ST_AsEWKT`/`ST_AsBinary`/`ST_AsGeoJSON`;
  measurement `ST_Distance`/`ST_Length`/`ST_Perimeter`/`ST_Area`/
  `ST_Centroid`/`ST_Envelope`; predicates `ST_DWithin`/`ST_Intersects`/
  `ST_Disjoint`/`ST_Contains`/`ST_Within`/`ST_Covers`/`ST_CoveredBy`/
  `ST_Crosses`/`ST_Overlaps`/`ST_Touches`/`ST_Equals`; derived geometry
  `ST_ConvexHull`/`ST_Simplify`/`ST_Segmentize`/`ST_Reverse`; overlay
  `ST_Buffer`/`ST_Intersection`/`ST_Union`/`ST_Difference`/
  `ST_SymDifference` (bounded — exact for convex/disjoint/containment
  cases, errors rather than guessing for the general overlapping
  non-convex case; see `docs/design-spatial.md` §8).
- `CREATE SPATIAL INDEX` now accepts a `GEOMETRY`/`GEOGRAPHY` column
  (bbox-centre Z-order key); `ST_Intersects`/`ST_Contains`/`ST_Within`/
  `ST_Covers`/`ST_CoveredBy`/`ST_DWithin` against a constant are sargable.
- On disk: EWKB with a `u32` length prefix. No `NSCT` catalog or NSQL
  protocol version bump — SRID/subtype ride in the existing per-column
  type metadata.
- All 7 official drivers decode `GEOMETRY`/`GEOGRAPHY` result values to a
  GeoJSON-shaped object; a WKT/EWKT string or an explicit
  `{kind:'geometry'|'geography', wkt, srid}` wrapper works as a parameter.
- See `docs/design-spatial.md`, `docs/geo.md`, `docs/sql.md`.

### Fixed — Spatial: out-of-range SRID arguments were silently truncated (2026-09-04)

- `ST_Point`'s SRID argument, `ST_SetSRID`, `ST_Transform`'s target SRID,
  `ST_GeomFromGeoJSON`'s SRID argument, and the EWKT `SRID=<n>;...` text
  prefix all narrowed a caller-supplied SRID to `u16` with a bare Go
  conversion, which wraps silently instead of erroring — e.g.
  `ST_Point(x, y, 99999)` produced a geometry tagged SRID 34463 with no
  error, rather than failing the way `GEOMETRY(subtype, 99999)` in a
  `CREATE TABLE` already correctly did. All six sites now validate the
  range and error `"SRID out of range"`, matching the DDL form.

### Added — database suspend/resume enforcement (Multi-database hosting M3-1, 2026-09-04)

- `nextsql database suspend --realm NAME --database NAME --confirm` /
  `nextsql database resume` (`docs/design-multidatabase-dbaas.md` §11.2,
  §16 "M3"). Offline, exclusive-data-dir-lock CLI, the same shape as
  `hosting set-realm-cap`/`set-database-cap`.
- `hosting.Registry.Lookup` — the sole call `dbmanager.Manager.Acquire` (and
  therefore every new client connection) resolves a realm/database through —
  now fails closed for a non-Active database or realm instead of silently
  opening it: `StateSuspended` → `Unavailable "database suspended"`,
  `StateProvisioning`/`StateFailed` → `Unavailable`, `StateDeleting`/
  `StateTombstoned` → `NotFound`. Previously `SetDatabaseState` could mark a
  database Suspended durably while every connection kept working exactly as
  before — suspend recorded intent without ever blocking access.
- New `security.ActionDatabaseSuspend`/`ActionDatabaseResume` audit actions.
- A state change is applied on `nextsqld`'s next restart, the same
  already-documented shape as a live storage-cap edit.

### Added — database drop/tombstone physical reclamation (Multi-database hosting M3-3, 2026-09-04)

- `nextsql database drop --realm NAME --database NAME --confirm`
  (`docs/design-multidatabase-dbaas.md` §11.2, §16 "M3"). Offline,
  exclusive-data-dir-lock CLI, the same shape as `database suspend`/
  `resume`. Transitions the database to `StateDeleting`, removes its whole
  on-disk managed directory (db file plus `.keys`/`.wal`/`.undo`/
  `.isolated` sidecars), then transitions to `StateTombstoned`. Idempotent
  and crash-resumable at every step.
- Scoped to realm-managed (`LayoutManaged`) databases; refuses the
  deployment's default realm/database (no per-ID directory to safely
  reclaim for the legacy-default layout, and every tool assumes it exists).
- New `security.ActionDatabaseDrop` audit action.
- `StateDeleting`/`StateTombstoned` and `hosting.Registry.Lookup`'s
  fail-closed handling of both already existed (M3-1); this closes the
  remaining gap — nothing previously reclaimed a tombstoned database's
  files.

### Added — `system.quotas` advisory view (Multi-database hosting M3, 2026-09-04)

- New read-only `system.quotas` virtual table surfacing the hosting storage
  caps (`docs/design-multidatabase-dbaas.md` §10.1). One row per realm and per
  database from the deployment registry manifest, with `cap_bytes` and
  `effective_cap_bytes` (`EffectiveStorageCapBytes` of the realm and database
  caps). Admin-only and empty on a legacy/non-hosted deployment, the same
  convention as `system.realms` / `system.databases`.
- `used_bytes`, `pct_of_cap`, and `over_cap` (gated by `usage_known`) are
  populated only for the row matching the session's own connected
  realm+database — the data-file logical high-water, the quantity the write
  path enforces the cap against. The view never errors and never bounds
  anything; the authoritative over-cap signal is still the write rejection.
- `system.capabilities` gains the `quotas_view` row. Existing
  `system.realms` / `system.databases` are now also documented in
  `docs/system-catalog.md`.

### Added — Collection types: `STRUCT` / `ARRAY` / `MAP` (Collections track, 2026-09-04)

- New column types `STRUCT<name T, …>`, `ARRAY<T>`, and `MAP<K,V>`, nestable
  in one another to depth 8. `T` / field / value types may be any storable
  type including another collection; `MAP` keys must be an orderable scalar.
- Constructors `STRUCT(expr AS name, …)`, `ARRAY(e1, e2, …)`,
  `MAP(k1, v1, k2, v2, …)`. STRUCT field access `col.field[.field…]`.
  Functions `ELEMENT_AT` (1-based for arrays), `CARDINALITY` /
  `ARRAY_LENGTH` / `MAP_SIZE`, `ARRAY_CONTAINS`, `MAP_CONTAINS_KEY`,
  `MAP_KEYS`, `MAP_VALUES`.
- All three are orderable (lexicographic tuple order, NULL members first) —
  usable as `PRIMARY KEY`, `ORDER BY`, and index columns. `MIN`/`MAX` work;
  `MAP` entries are stored in canonical key order so equal maps compare and
  encode identically, and duplicate `MAP` keys are rejected.
- Not `ENCRYPTED CLIENT`-eligible and not foreign-key-eligible.
- On disk: a self-describing nested `NSRW` sub-encoding (`u32` body length
  for O(1) skip at any depth). Catalog descriptor `NSCT` v11 → **v12**
  (per-column recursive type descriptor; older descriptors still decode).
  No NSQL wire-protocol version bump — the recursive type descriptor rides
  after the existing fixed value header.
- All 7 official drivers updated (recursive type-descriptor codec, a
  collection param path, `RowDesc` parsing).
- See `docs/design-collections.md`, `docs/sql.md`, `docs/storage-format.md`.

### Added — `nextsql setup` transactional rollback of a failed install (P28, 2026-09-04)

- `nextsql setup` now undoes a partial install on failure — a failed
  `nextsql init` or post-install health check. It records whether each path
  it might create already existed *before* the run and, on failure, removes
  only the ones it actually created (database + sidecars, deployment
  registry, generated `nextsql.conf`, generated key files), newest first. A
  pre-existing operator-supplied key or a data directory that already held
  files is never removed; the data directory itself is removed only if it
  comes out empty. New `--keep-failed` flag leaves the partial install in
  place for inspection.
- New pure-logic `internal/setup`: `InstallRollback` (`Observe` /
  `Preexisting` / `Track` / `Plan` / `Empty` — reverse-order removal list
  with the never-delete-preexisting guard).
- Closes the P28 "Transactional rollback of safe installer changes" and
  "Never delete existing user data/keys on failed install" checklist items
  for the CLI/automation path.
- See `TODO.md` log #110.

### Added — `nextsql lifecycle repair`, the installation repair runner (P28, 2026-09-04)

- New `nextsql lifecycle repair --data-dir DIR --key-file FILE [--config FILE]
  [--preset P] [--buffer-pages N] [--listen HOST:PORT [--tls-cert --tls-key]]
  [--log-level L] [--force-config] [--fix-perms] [--dry-run] [--json]`.
  Reconciles a damaged install without touching the database or unlock keys:
  regenerates a missing or unparseable `nextsql.conf` with secure defaults
  (an unparseable one is backed up first; a parseable one is left alone
  unless `--force-config`), reports permission drift on the config (`0640`)
  and key files (`0600`) and tightens it with `--fix-perms` (never loosens),
  then opens the encrypted store once (running WAL recovery) to confirm
  health. Outcomes `repaired` / `healthy` / `dry-run` / `blocked` (2/7) /
  `failed` (5). Refuses to run while a server holds the deployment lock; is
  not `setup` (will not initialize a database).
- New pure-logic `internal/setup`: `PlanConfigRepair` (config action from
  observed state + `--force-config`), `RepairPlan` (ordered `RepairStep`s
  each flagged `Mutates`), `ConfigState`, `RepairConfigAction`,
  `RepairOutcome`.
- `docs/install.md` gains the `nextsql lifecycle repair` reference; "still to
  come" now records the whole `lifecycle` backbone as complete.
- See `TODO.md` log #108.

### Added — `nextsql lifecycle uninstall`, the installation removal runner (P28, 2026-09-04)

- New `nextsql lifecycle uninstall --data-dir DIR [--config FILE] [--key-file
  FILE] [--instance-key-file FILE] [--purge-data] [--purge-keys] [--confirm]
  [--json]`. Preserves the encrypted database and the external unlock keys by
  default — a plain run removes only `nextsql.conf` and its `.bak-*`
  siblings. `--purge-data` also removes the primary database + sidecars
  (keystore/WAL/UNDO/isolated registry), the auth/ACL/audit files, the
  deployment-registry database, and the deployment lock. `--purge-keys` (which
  requires `--purge-data`, and resolvable key paths) also removes the root
  and instance key files. Every run is a dry run until `--confirm`
  (`outcome: planned`, exit 0). Refuses to run while a server holds the
  deployment lock (`blocked`, exit 2) or with inconsistent purge flags
  (exit 6) — nothing is deleted in either case. Outcomes `planned` /
  `removed` / `blocked` / `partial` (exit 5).
- New pure-logic `internal/setup`: `PlanUninstall` (classifies every known
  artifact into remove / preserve for the requested purge flags, and reports
  the flag-dependency / running-server refusals as blocking reasons rather
  than silently downgrading), `UninstallCategory` (`safe`/`data`/`keys`),
  `UninstallOutcome`.
- `docs/install.md` extended with the `nextsql lifecycle uninstall`
  reference; "still to come" narrowed to the `repair` runner + GUI + Manager.
- See `TODO.md` log #107.

### Added — `nextsql lifecycle upgrade`, the mutating in-place upgrade runner (P28, 2026-09-04)

- New `nextsql lifecycle upgrade --data-dir DIR --key-file FILE [--config FILE]
  [--buffer-pages N] [--dry-run] [--json]` — the mutating half of the
  lifecycle backbone. Holds the deployment lock for the whole operation
  (server running → `server-running`, exit 2), preflights the on-disk formats
  (non-`ready` → blocked, nothing mutated), takes the verified config backup,
  then opens the encrypted store once with this binary — running WAL recovery
  and confirming the catalog decodes under the new format code — and
  re-verifies the headers. Outcomes: `applied` (0) / `dry-run` (0) /
  `blocked` (2/6/7) / `failed-verify` (5, any config backup already written
  is retained for rollback). Never deletes anything; does not swap the binary
  or restart a service. Idempotent.
- New pure-logic `internal/setup`: `UpgradePlan` (ordered `UpgradeStep`s,
  each flagged `Mutates` — one source for the dry-run output and the eventual
  GUI's staged progress) and `UpgradeOutcome`.
- `lifecycle preflight`'s header-assessment and `backup-config`'s verified
  copy are refactored into shared helpers (`assessInPlaceUpgrade`,
  `backupConfigFile`) that `upgrade` reuses.
- `docs/install.md` extended with the `nextsql lifecycle upgrade` reference;
  "still to come" narrowed to `repair` / `uninstall` / rolling-cluster /
  rollback / GUI / Manager.
- See `TODO.md` log #106.

### Added — `nextsql lifecycle`, the installer lifecycle backbone (P28, 2026-09-04)

- New `nextsql lifecycle` command group, the non-interactive backbone for
  the Installer's detect / upgrade / repair / uninstall half:
  - `detect --data-dir DIR [--config FILE]` — non-destructive discovery of an
    existing installation: config presence + parse + resolved paths, whether
    the database is initialized, on-disk header compatibility, format
    database id, and whether a NextSQL process holds the deployment lock,
    reduced to one status (`none` / `config-only` / `initialized` /
    `running`).
  - `preflight --data-dir DIR` — upgrade preflight: checks the keyless
    superblock / WAL-control / UNDO-control / envelope header versions
    against this binary's `internal/upgrade/compat` catalog and returns a
    verdict (`ready` → exit 0; `not-initialized` → 7; `server-running` → 2;
    `blocked-too-new` / `blocked-too-old` / `blocked-damaged` → 6), each with
    the concrete fix.
  - `backup-config --config FILE [--out DIR]` — copies the live config to a
    timestamped `nextsql.conf.bak-<UTC>` sibling (mode 0640) and confirms the
    copy reloads to an identical config before reporting success.
- All three take `--json` and use the shared `nextsql` exit-code scheme.
- New pure-logic `internal/setup` decisions: `ClassifyInstall` and
  `AssessUpgrade` (verdict precedence: running server → uninitialized →
  damaged header → too-new → too-old → ready).
- `docs/install.md` extended with the `nextsql lifecycle` reference.
- See `TODO.md` log #105.

### Added — `nextsql setup`, the installer automation backbone (P28, 2026-09-04)

- New `nextsql setup` command: one non-interactive step that detects the
  host's CPU/RAM/disk/filesystem, sizes the buffer pool from a resource
  preset (`conservative` / `balanced` / `high-performance` / `custom` —
  10 / 25 / 50 % of physical RAM), writes a validated `nextsql.conf` with
  secure defaults, initializes the database through the same path as
  `nextsql init`, and verifies the result.
- Secure by default: loopback-only listener; a non-loopback `--listen` is
  rejected (exit 6) unless `--tls-cert` and `--tls-key` are both given; the
  generated config contains no key, password, or token material.
- Automation-friendly: `--json` single-object output, `--dry-run`,
  `--skip-init`, `--config-in` / `--config-out`, and the standard `nextsql`
  exit-code scheme.
- New packages `internal/sysinfo` (cross-platform capacity snapshot) and
  `internal/setup` (preset sizing + plan validation); new
  `config.Config.Marshal` renders a config back to the `key=value` format
  `config.Load` reads and round-trips with it.
- New reference: `docs/install.md`.
- Also bumped the stale `version.Phase` constant (15 → 27).
- See `TODO.md` log #104.

### Fixed — catalog format v11 bump was only half-applied (2026-09-04)

- The `NSCT` catalog descriptor moved to v11 (per-column `ENUM` label list)
  but the `internal/upgrade` compatibility-window tests, the newest
  `DecodeTable` version guard, and the on-disk-format docs were never
  updated to match — the compatibility tests shipped asserting v10 and
  failing.
- `internal/catalog/encode.go`: added the missing `tableVersionV11`
  constant and gated the v11 ENUM-label decode block on it instead of the
  bare "current version" constant, which would have silently stopped
  decoding a v11 catalog's ENUM labels the moment a v12 is introduced.
- `internal/upgrade/compat` tests corrected to v11, with a v12-rejection
  guard added for the next bump. `docs/storage-format.md` (+ `sql.md`,
  `ops.md`, `protocol.md`, `vector.md`, `security.md`) updated to describe
  v11. See `TODO.md` log #102.

### Fixed — aggregation over-counted deleted rows after concurrent DELETE churn (2026-09-04)

- `SELECT COUNT(*)` and `SELECT COUNT(*) ... GROUP BY` over a partitioned
  table could report more rows than actually exist, when the query runs
  as the sole open snapshot shortly after sustained concurrent
  INSERT/UPDATE/DELETE traffic. A plain row scan (`SELECT ...`) and
  single-row index lookups returned the correct data throughout; only the
  aggregate counts were wrong. No data was lost or corrupted on disk.
- Root cause: the heap-aggregation fast path (taken only when the reader
  is the sole snapshot) skips the per-row MVCC visibility check for speed,
  but the underlying btree walks (`RangeLive` / `RangeLiveRange` /
  `CountLiveRange`) iterated every physically-present slot — including
  committed tombstones that the deleting transaction could not reclaim at
  commit time because a concurrent snapshot still needed them. A deleted
  row that had not yet been VACUUMed was counted as live. Normal
  single-writer deletes purge their own tombstone immediately, which is
  why this only surfaced under concurrency.
- Fix: those three fast-path walks now skip records carrying a committed
  delete marker, consistent with the rest of the engine's "live row"
  accounting (`page.LiveSlots()`, `CountLive()`). The raw physical walk is
  still available as `btree.Txn.RangePhysical` for maintenance/VACUUM
  code. No on-disk format, wire-protocol, catalog, or isolation-level
  change; the non-fast-path (already-correct) MVCC scan is untouched.
- `TestPartitionCrossPartitionUniqueSustainedConcurrentWrites`
  (`internal/executor/partition_test.go`) now asserts aggregate counts
  equal the heap row count; 5/5 under `-race`. Regression swept:
  `internal/executor` (incl. the deferred-tombstone `TestMaintain*` /
  `TestCleanupDeadVersions*` suites), `internal/storage/...`,
  `internal/recovery`, `internal/wal`, `tests/integration`. See `TODO.md`
  log #101.

### Fixed — silent data loss repairing a replica from backup + `AddVoter` (2026-09-03)

- Writing the previously-missing regression test for the documented
  "wiped replica restored from `nextsql backup`/`restore`, rejoined with
  `AddVoter`" repair procedure (`docs/ha.md` "Replica repair and rolling
  maintenance") surfaced a real bug: a repaired replica could permanently,
  silently lose any write that happened between the backup and the
  rejoin, with no error and no detectable divergence signal — it reported
  itself fully caught up.
- Root cause: `internal/backup`'s `Create`/`Restore` each open the data
  file directly and run their own checkpoint, which durably consumed
  several WAL LSN numbers as local housekeeping unrelated to replication
  (measured: one checkpoint alone advanced a small table's `NextLSN()` by
  9). `internal/storage/engine.go`'s `ApplyReplicated` treats that same,
  now-inflated counter as "how far into the replicated stream have I
  gotten" (`if last < e.WAL.NextLSN() { return nil }`), so a legitimate
  not-yet-applied write whose leader-assigned LSN now fell below the
  locally-inflated counter was silently treated as already-applied and
  never replayed.
- This was a second, more easily reached trigger for a class of risk
  `internal/storage/engine.go`'s own `prepareCommitLocked` already
  documents for a narrower case (an ambiguous replication-failure race,
  see the "Fixed a general transaction-attribution race" entry below's
  neighbor in `TODO.md` log #79).
- Considered a dedicated, durably-persisted "replicated progress" watermark
  (an on-disk superblock format change) but found a narrower, non-invasive
  fix instead: `Engine.Checkpoint()` — the only two callers are `Close()`
  and `backup.Create` — unconditionally wrote a fresh checkpoint record
  even when nothing had happened since the engine was opened. New
  `Engine.openNextLSN` field (`WAL.NextLSN()` captured once at open) lets
  `Checkpoint()` skip entirely when there's no in-progress transaction and
  nothing has been appended since — `backup.Create`'s checkpoint of an
  already-cleanly-closed file is now a true no-op, so it no longer
  perturbs the file's LSN numbering. No on-disk format change.
- `TestHAReplicaRepairFromBackupAddVoter` (`tests/ha/ha_test.go`) —
  20/20 clean under `-race`, previously reliably failing. Regression
  swept: full `tests/ha`, `internal/storage/...` (incl. `btree`),
  `internal/backup`, `internal/recovery`, `internal/wal`,
  `internal/executor/...`, all under `-race`, all green. See `TODO.md`
  log #95 for the full investigation and fix writeup.

### Fixed — REBUILD INDEX ... ONLINE orphaned index entry under concurrent UPDATE (2026-09-03)

- An independent, skeptical production-readiness re-audit of Phase 0–27 (run
  at explicit user request, rather than trusting the existing checkmarks)
  reproduced a real, intermittent (~5% of runs) data-integrity race in
  `REBUILD INDEX name ONLINE`: an `UPDATE` executing after the rebuild's
  catalog swap could silently fail to remove the row's old index entry from
  the newly-swapped-in tree, leaving it as a permanent orphan alongside the
  correct new entry. Root cause: the ordinary (non-mirror) index-maintenance
  path deleted via the transaction's own snapshot captured at `BEGIN`, which
  cannot see an entry the online rebuild's backfill committed after that
  snapshot was taken; the delete's "not found" result was — correctly, in
  the ordinary case — tolerated as a no-op, so it silently vanished instead
  of erroring.
- Fixed in `internal/executor/exec.go`/`fk.go`: while an online rebuild is
  registered for an index (armed or swapped-but-not-yet-disarmed), ordinary
  index writes now use a freshly captured snapshot, extending the same
  protection `mirrorOnlineIndex` already had to the post-swap path.
  Verified clean across 60 `-race` runs and 300 non-`-race` runs of the
  regression (previously ~5% failure), plus the full `internal/executor`
  and `tests/integration` suites. See `TODO.md` log #93 for the full
  root-cause writeup.

### Datatype expansion — D3 fixed-width unsigned integers (2026-09-03)

- New first-class scalar column types `UINT8`/`UINT16`/`UINT32`/`UINT64`:
  exact unsigned integers (1/2/4/8 bytes). Index keys use plain unsigned
  big-endian bytes — no sign-bit flip needed, unlike `INT8..64`. Narrowing
  and assigning a negative value both error rather than wrapping. `+ - * /`
  and unary `-` promote to `DECIMAL`, same as `INT8..64`; `SUM`/`AVG` reuse
  the same DECIMAL-promotion accumulator, `MIN`/`MAX` stay in the column's
  own uint kind. Ordinary FK-eligible scalars. `ENCRYPTED CLIENT` supported.
  `INT8..64` and `UINT8..64` are directly coercible into each other
  (range/sign checked either way) — treated as one exact-integer group
  rather than isolated families. Catalog wire tags are plain appended enum
  values — no `NSCT` version bump. Updated all 7 official drivers (Go
  needed no code change; JS/Bun/Deno via the shared `drivers/js` core; Node;
  PHP; Python; Ruby), each exposing every width with its own round-trip
  test. PHP's `UINT64` decodes as a decimal digit string once a value
  reaches or exceeds `PHP_INT_MAX` (mirroring how `DECIMAL` is already
  represented in that driver), since PHP's native `int` has no unsigned
  64-bit counterpart. Also fixed, while implementing this increment, a
  pre-existing gap unrelated to D3 itself: the Node driver's
  `ENCRYPTED CLIENT` (NSCE1) implementation had never picked up D2's
  `INT8..64` support at all — it now supports the full
  `INT8..64`/`UINT8..64` set, same as every other driver. See
  `docs/design-datatypes.md` D3 and `docs/sql.md`.

### Datatype expansion — D1 `BLOB` type (2026-09-03)

- New first-class scalar column type `BLOB`: variable-length raw bytes,
  `u32`-length-prefixed on disk (same shape as `STRING`/`TEXT`, no UTF-8
  validation), with its own `X'<hex>'` literal syntax (`X''` for empty).
  Orders byte-lexicographically, so `BLOB` is usable as a `PRIMARY KEY` or
  `ORDER BY`/`GROUP BY` column. Deliberately isolated from `STRING`/`TEXT`:
  coercion either way requires hex text, never an implicit byte-for-byte
  reinterpretation. `ENCRYPTED CLIENT` is supported (the existing opaque
  ciphertext path is fully generic over scalar encode/decode, so this
  needed no new crypto code). Catalog wire tag is a plain appended enum
  value — no `NSCT` version bump. Updated all 7 official drivers
  (Go needed no code change — it shares `internal/sql/types`/`internal/protocol`
  directly; JS/Bun/Deno via the shared `drivers/js` core; Node; PHP; Python;
  Ruby), each exposing `BLOB` as its native byte-string type (`[]byte`,
  `Uint8Array`/`Buffer`, PHP/Ruby byte-safe `String`, Python `bytes`) with
  its own round-trip test. See `docs/design-datatypes.md` D1 and
  `docs/sql.md`/`docs/client-encryption.md`.

### Datatype expansion — D2 fixed-width signed integers (2026-09-03)

- New first-class scalar column types `INT8`/`INT16`/`INT32`/`INT64`: exact
  two's-complement signed integers (1/2/4/8 bytes). Index keys (clustered
  `PRIMARY KEY` and secondary `ORDER BY`/`GROUP BY`) flip the sign bit
  before storing big-endian unsigned bytes, so they sort numerically —
  naive two's-complement byte order would otherwise sort every negative
  value after every positive one. Narrowing — including a literal or an
  arithmetic result that doesn't fit — errors rather than wrapping.
  `+ - * /` and unary `-` always promote both operands to `DECIMAL`
  (arbitrary precision, matching the pre-D2 behavior where `DECIMAL` was
  the only arithmetic type), so the operation itself can never overflow;
  only assigning/coercing the result back into a fixed-width column
  re-checks range. `SUM`/`AVG` inherit the same DECIMAL-promotion
  accumulator DECIMAL columns already used; `MIN`/`MAX` stay in the
  column's own int kind. Ordinary FK-eligible scalars (unlike `BLOB`/
  `VECTOR`/`JSON`). `ENCRYPTED CLIENT` is supported. Catalog wire tags are
  plain appended enum values — no `NSCT` version bump. Updated all 7
  official drivers (Go needed no code change; JS/Bun/Deno via the shared
  `drivers/js` core; Node; PHP; Python; Ruby), each exposing every width
  with its own round-trip test — a bare host-language integer still
  defaults to the wire's `DECIMAL` encoding and coerces server-side into
  any numeric column, so an explicit wrapper (`{kind:'int32',...}` /
  `FieldType::int32()` / `Int32(...)`) is only needed to pin an exact wire
  width or for `ENCRYPTED CLIENT`. Also fixed, while implementing this
  increment, a latent float-overflow bug in the PHP driver's 64-bit
  integer decoder (`Protocol::i64`) that silently corrupted values at/above
  magnitude 2^63 (e.g. exactly `PHP_INT_MIN`) — replaced with a direct
  `pack('P')`/`unpack('P')` reinterpretation, which also benefits the
  existing `TIMESTAMPTZ` decode path. See `docs/design-datatypes.md` D2 and
  `docs/sql.md`.

### Fixed a transaction-rollback data-corruption bug in the core storage engine (2026-09-03)

- `ROLLBACK` (explicit or autocommit-statement-failure) could silently
  discard another transaction's already-committed row if it happened to
  share a physical B+Tree page with the rolling-back transaction — the
  engine restored the whole page to a pre-transaction image instead of
  reversing only its own row-level changes. A related variant could also
  destroy pre-existing committed rows that a page split (triggered by the
  now-aborting transaction's own insert) had physically relocated onto a
  newly allocated sibling page. Neither required a crash to trigger — both
  were live, in-process bugs under ordinary concurrent write load.
- Fixed by replaying each transaction's already-existing, durable
  per-transaction UNDO chain (previously used only by crash recovery)
  against the live buffer pool on rollback, routed through the exact
  B+Tree each record came from, instead of restoring whole pages.
  Structural changes (page splits, root promotion) are now correctly never
  reverted by rollback, matching standard B+Tree engine practice — only
  logical row content is undone.
- This was the blocker for `REBUILD INDEX ... ONLINE` (an already-built,
  uncommitted feature) reaching a testable state; un-skipping its own
  concurrency test in turn surfaced a second, initially-unexplained
  correctness issue — resolved below, and `ONLINE` rebuild is now
  supported. See `TODO.md` log #89.

### Fixed a general transaction-attribution race in the storage engine, corrupting secondary indexes under concurrent writes (2026-09-03)

- Any workload with a secondary index and real concurrent
  `INSERT`/`UPDATE`/`DELETE` traffic could silently corrupt that index —
  entries left stale in their old bucket, duplicated across old and new
  buckets, or missing entirely — with no error raised. Present since before
  this changelog's fixes above (reproduced against the last commit prior to
  this development cycle, with none of today's changes applied) and
  unrelated to either of them; it just happened to be what was actually
  breaking `REBUILD INDEX ... ONLINE`'s own concurrency test after the
  rollback fix above landed.
- Root cause: `Engine.beginLocked` set the engine's "currently active
  writer" bookkeeping field for every new transaction immediately on
  begin, synchronized only briefly and independently of the lock that
  protects a transaction's actual page-mutating work — so a transaction
  newly beginning could silently steal that attribution out from under a
  *different*, concurrently in-flight transaction's own write, before that
  writer's own next step (e.g. the index-maintenance half of an `UPDATE`,
  after its heap-update half already ran) executed. That step's effects
  then got attributed to the wrong transaction entirely, so neither
  transaction's eventual commit or rollback bookkeeping matched what it
  had actually done.
- Fixed by making a transaction's own `Enter`/`Leave` bracket (already
  correctly synchronized with the page-mutation lock) the only setter of
  this attribution for ordinary concurrent transactions; internal
  single-writer maintenance paths, where nothing else can be concurrently
  active by construction, are unaffected.
- Verified via a dedicated concurrent-write stress harness (3 writers,
  ~230 successful statements per run against a non-unique secondary
  index): 14/20 runs still failing with only the rollback fix above
  applied, 0/20 failing across 240 iterations (6 full runs) after this
  fix. `REBUILD INDEX ... ONLINE` is now genuinely safe; its capability
  row is `"supported"`. With this, **Phase 0–27 has zero remaining
  deferrals.** See `TODO.md` log #91.

### Multi-database hosting — M2 complete; dead `TaskRuntime.Cancel` retired (2026-09-03)

- Removed `TaskRuntime.Cancel` and its private `running` cancel registry —
  dead code since task execution moved to a shared worker pool. `CANCEL
  TASK` is unchanged: it has always taken effect through the per-database
  `db.taskCancels` registry, which every task-execution path wires up
  regardless of which worker or scheduler ran the task. Internal only, no
  behaviour change.
- With this, the M2 "single-node selectable multi-database routing"
  milestone is complete: realm/database routing, realm-scoped auth,
  per-connection idle eviction, process-wide buffer-memory and
  task-scheduling budgets, declarative manifest bootstrap, and serving a
  fully-managed deployment. Production-grade multi-database hosting
  (per-database WAL/PITR/Raft, quotas, registry DR) remains M3+ scope.

### Multi-database hosting — declarative bootstrap manifest wired into `nextsql init` (2026-09-03)

- `nextsql init --hosting-manifest FILE` (or `NEXTSQL_HOSTING_MANIFEST_FILE`,
  or a dotenv key) bootstraps a whole multi-realm deployment from one
  validated YAML document: every declared realm and database is registered
  and physically created in a single run.
- Any per-database root key file named in the manifest that does not exist
  yet is created first (a fresh independent AES-256 root, mode 0600), so a
  fresh deployment needs only the manifest.
- The whole document (and every key file) is validated before any state is
  mutated. Re-running with an identical manifest is a clean no-op;
  a partial run resumes.
- New `hosting.EnsureBootstrapManifestKeyFiles`. The bootstrap-user logic is
  now shared between the single-pair and manifest init paths.
- `nextsqld` now serves a manifest-bootstrapped deployment: its startup no
  longer requires a legacy-layout default database. When the registry's
  default is managed-layout (as a manifest always produces), the server
  starts with no eager primary handle and serves the default realm/database
  lazily through the database manager, exactly like every non-default
  managed database. `--key-file` is not required for such a deployment —
  only `--instance-key-file`. (`require_client_key` is not supported with a
  managed-layout default.)

### Phase 19 — `CRON` schedule expressions (2026-09-03)

- `CREATE SCHEDULE name CRON '<expr>' RUN WORKFLOW ...` — a standard
  five-field cron expression (`minute hour day-of-month month
  day-of-week`), evaluated in UTC. Each field takes `*`, a value, a range
  `a-b`, a comma list, or a step `*/n` / `a-b/n`; day-of-week is 0–6 with
  Sunday 0 (`7` also accepted). When both day fields are restricted, a day
  matches if either matches (Vixie-cron semantics).
- Numeric only — month/weekday names, `@`-macros, seconds, and
  `L`/`W`/`#` are deliberately out of scope.
- Expressions are validated at definition time, including a bounded
  forward search that rejects an unsatisfiable spec (e.g. `0 0 30 2 *`),
  and stored in canonical single-space form.
- Recurrence: on each firing the cursor advances to the next matching
  minute strictly after now, so a leader clock that jumped past several
  boundaries emits one task and skips straight to the next future
  boundary — the same forward-jump rule `EVERY` already uses. `FORBID`
  concurrency is unchanged.
- New leaf package `internal/cron`. Schedule catalog descriptor is now
  `NSSC` v2 (adds the cron expression); v1 descriptors still decode.
- Closes the last deferred item under Phase 19's SCHEDULE surface; the
  deferral was gated on "the core scheduler is proven", which the
  centralized task scheduler work (logs #81/#83) established.

### Housekeeping — P0–P27 status audit, flaky-test and `go vet` fixes (2026-09-03)

- Audited Phase 0–Phase 27: every phase and every exit gate is complete.
  The only unchecked items in that span are three intentional non-gate
  follow-ons, each blocked on a separate prerequisite: `REBUILD INDEX …
  ONLINE` (P17), cron `SCHEDULE` syntax (P19), and the terminal 100M
  B+Tree soak measurement (P16).
- Refreshed the stale summary blocks in `TODO.md` (header table, progress
  paragraph, roadmap summary, "Next action") that still described P27 as
  open after it closed on 2026-09-03.
- Fixed a flaky test: `TestCentralSchedulerReleasesEveryRefEventually`
  sampled the outstanding-ref counter once at an arbitrary instant, but
  that counter legitimately oscillates 0→1→0 within each poll tick; it now
  polls for the counter to settle at zero, matching the test's intent. No
  production code change — not a real ref leak.
- Fixed the one `go vet ./...` finding: `internal/executor/cdc.go`
  `execSubscribe` built its cancellable context before validating the
  operation filter, leaking the context on the invalid-filter error path.
  Filter validation now runs first. `go vet ./...` is clean.

### Multi-database hosting — M2-3b-3b centralized task scheduling (2026-09-03)

- Task polling itself is now centralized: one process-wide scheduler
  enumerates every open database each tick and claims/dispatches its due
  work, instead of each database running its own poll loop. Combined with
  M2-3b-3a's shared worker pool, task-scheduling goroutine count is now
  O(1) regardless of how many databases a hosting `nextsqld` has open,
  down from one poll loop plus its own workers per database.
- A database with a scheduler-claimed task still executing can't be
  evicted out from under it — reuses the existing connection refcounting
  mechanism rather than adding a second one.
- Known, deliberate tradeoff: a database connected to only very briefly
  (materially shorter than the poll interval) may see its own schedule
  fire later than before, since polling no longer happens automatically on
  every connect. A normally-held-open connection is unaffected. Delayed,
  never lost.
- Not part of the Phase 27 release gate.

### Phase 27 complete — per-realm and per-database connection limits (2026-09-03)

- Closed Phase 27's last open exit-gate item. Its original deferral no
  longer held: it assumed one `nextsqld` process could only ever open one
  database, but the multi-database hosting track had since shipped live,
  concurrent, selectable routing to more than one database per process.
- New `max_connections_per_database`/`max_connections_per_realm` config
  keys (both default 0 = unlimited), enforced the same way
  `max_connections_per_user` already was: rejected after authentication,
  before a session is created, with `exhausted`.
- A database's own connection count and its realm's are independent —
  exhausting one database's limit never blocks a connection to a different
  database in the same realm, while every database in a realm shares that
  realm's own counter.
- A single-database (non-hosted) deployment can still set either knob
  meaningfully — it collapses to a finer-grained `max_connections`.
- **Phase 27 — Operational maturity + workload governance — is now
  complete.**

### Multi-database hosting — M2-3b-3a shared task-execution worker pool (2026-09-03)

- Scheduled-task execution across every database `nextsqld` has open now
  shares one fixed-size worker pool, instead of each database spawning its
  own independent worker set — task-execution goroutine count no longer
  scales with the number of open databases. New `task_workers` config key
  (default 0 = the same built-in default each database used before).
- Each database still polls its own due tasks/schedules independently;
  only the goroutines that execute claimed work moved to the shared pool.
  Centralizing the polling itself is a separate, not-yet-built follow-on.
- Closing one database's task runtime (including the existing idle
  eviction of a secondary database) now correctly waits out any
  already-submitted work before returning, so the database can be safely
  closed right after — a new correctness requirement introduced by sharing
  workers across databases, closed as part of this change rather than left
  for later.
- Live-verified against a real `nextsqld` process with the shared pool
  sized to a single worker: two independently-scheduled databases both
  successfully executed through that one worker.
- Not part of the Phase 27 release gate.

### Multi-database hosting — M2-3b-2 global memory budget gating buffer-page grants (2026-09-03)

- A hosting `nextsqld` process can now cap the total buffer-pool memory
  committed across every database it has open at once (the primary plus
  every dbmanager-opened secondary), instead of each database's buffer
  pool growing unaccounted for against the others. New `max_total_buffer_pages`
  config key (default 0 = unbounded, unchanged behavior).
- Since a buffer pool's frames are allocated in full at open — there is no
  per-page runtime grant to gate, unlike the existing per-database disk
  storage cap — the new shared `buffer.Budget` is charged once when a
  database opens and released once when it closes, including on M2-3b-1's
  idle eviction.
- An open that would exceed the budget fails `exhausted` rather than
  growing process memory without bound; live-verified against a real
  `nextsqld` and a real second-database connection, both the rejection and
  the release-on-close/retry path.
- Deliberately scoped to the long-running server process: the one-shot
  `nextsql database create`/`nextsql init` provisioning CLI is unaffected
  (it never holds more than one buffer pool open at a time).
- Not part of the Phase 27 release gate.

### Phase 27 exit gate closed — local-commit-before-replication-ack structural fix (2026-09-03)

- Fixed the last open Phase 27 exit-gate item: `storage.Engine` used to
  commit a transaction to local storage (durable, visible, locks released)
  *before* confirming Raft quorum, so a `Replicate` failure — most commonly
  a write racing `CLUSTER TRANSFER LEADER` — could leave one un-replicated
  local row no ordinary Raft catch-up ever reconciled. Deferred twice
  before in favor of mitigations; the full structural fix has now landed.
- A transaction's commit record is now held — durable, visible, and
  lock-released only once Raft's outcome is known — via a new WAL
  durability-barrier primitive (`wal.Log.AppendHeld`/`ReleaseHold`).
- A **definite** replication failure (this node was rejected before ever
  being able to propose the entry — e.g. any write landing during a
  leadership transfer) now discards the held commit and rolls the
  transaction back cleanly: no orphaned local row, nothing for an operator
  to reconcile.
- An **ambiguous** failure (the entry was proposed, but the quorum wait
  itself failed or timed out) is structurally undecidable and keeps this
  project's existing fail-open behavior: the commit stays local, and the
  pre-existing `replSuspect` node-local flag plus `CLUSTER RECONCILE
  CONFIRM` operator workflow — unchanged — remains the answer for that
  narrower residual case, which cannot be closed without changing Raft's
  own apply contract.
- No wire-protocol or on-disk-format change.

### Multi-database hosting — M2-6 pre-authentication existence-disclosure hardening (2026-09-02)

- Closed the gap the M2-5 entry below flagged as open: connecting with an
  unknown realm name, or (in a legacy single-database deployment) an
  unknown database name, no longer returns a distinguishing error before a
  password is even checked. The handshake now always completes the full
  round trip and, only after running the real (or dummy, for an unknown
  username) password comparison, rejects an unresolved realm/database with
  the exact same generic authentication-failure response a wrong password
  produces — no distinguishing content or timing, matching the protection
  username enumeration already had.
- Deliberately not addressed here: a database-not-found error is still
  possible after successful authentication in a genuinely multi-database
  realm (it requires valid credentials already, a materially weaker,
  pre-existing gap).

### Multi-database hosting — M2-5 multi-realm routing activation (2026-09-02)

- A hosted deployment's `nextsqld` can now serve more than one realm from a
  single process: a connection may select any real realm/database pair in
  the deployment, not just the one realm pinned at startup. `dbmanager`'s
  routing (M2-3a) and realm-scoped authorization (M2-4b-1) already
  supported this; only a leftover flat equality check was blocking it.
- Fixed a real, separate bug found during live verification: the CLI's
  `ServerConfig` resolved a `--realm`/`NEXTSQL_REALM_NAME` setting but
  never actually passed it to the driver, so no server-mode command could
  select a non-default realm. `nextsql exec` gained an explicit `--realm`
  flag.
- An unrecognized realm name is still cleanly rejected.
- Not part of the Phase 27 release gate. Pre-authentication realm-name
  existence disclosure (an unknown realm returns a distinguishing error
  before any password check) remains an open, pre-existing gap, now more
  reachable than before — noted for a future dedicated hardening pass.

### Multi-database hosting — M2-4b-1 realm-scoped auth.Store/security.ACL (2026-09-02)

- `auth.Store` and `security.ACL` gained a realm dimension: every method now
  has a realm-scoped `*InRealm` sibling (`VerifyInRealm`, `GrantInRealm`,
  `AllowedScopedInRealm`, etc.); every pre-existing flat method is unchanged
  behavior (a deployment-wide `hosting.ID{}` wrapper), so this is additive
  for every non-hosted deployment.
- The same username can now exist independently, with independent
  passwords and grants, in two different realms of a hosted deployment.
- A deployment-wide `PrivAdmin`+`ScopeCluster` grant (the kind created by
  `nextsql init --user`) continues to authorize across every realm; a
  cluster-admin grant can never be narrowed to one realm, by design.
- `system.users`/`system.roles`/`system.grants` now show only the
  connected session's own realm's principals, instead of only ever showing
  deployment-wide ones once realms exist.
- New `hosting.Registry.LookupRealm`. Two on-disk credential file formats
  bumped (`auth.Store` v2→v3, `security.ACL` v1→v2); both still read every
  older version.
- Live-verified against real `nextsql`/`nextsqld` binaries and a new
  two-realm, two-server integration test proving real cross-realm password
  isolation over the wire.
- `nextsqld` still pins its wire-protocol realm to one name — this lands
  the authorization layer, not multi-realm routing activation, which
  remains open. M2-4b-2 (per-realm credential files) and M2-4b-3 (deeper
  OIDC broker realm-awareness) remain open. Not part of the Phase 27
  release gate.

### Multi-database hosting — M2-4b scoping (2026-09-02)

- No code changes — scoping and documentation only.
- Found `auth.Store` (flat `map[string]record` keyed by username) cannot
  represent per-realm usernames without a real structural change; two
  options identified (composite-key one file vs. fully separate per-realm
  files needing a new eviction manager) — a genuine design decision, not
  yet made.
- Found `nextsqld` currently pins its realm to one fixed name, so
  `dbmanager`'s multi-realm routing (accepted since M2-3a) cannot actually
  be reached today — M2-4b is a real prerequisite for multi-realm routing,
  not only an authorization feature.
- Decomposed M2-4b into M2-4b-1 (composite-key single file — recommended
  first slice), M2-4b-2 (per-realm files + eviction manager), M2-4b-3
  (OIDC broker realm-awareness, narrower than first scoped since token
  minting already carries a realm claim).

### Multi-database hosting — M2-4a `system.realms`/`system.databases` introspection (2026-09-02)

- Two new admin-only read-only system views expose the hosted deployment
  registry over SQL: `system.realms` (`realm_id`, `name`, `state`,
  `database_count`, `storage_cap_bytes`, `realm_root_delegated`) and
  `system.databases` (`realm_id`, `realm_name`, `database_id`, `name`,
  `state`, `layout`, `storage_cap_bytes`).
- New `Session.SetHostingRegistry`/`Server.HostingRegistry` plumbing wires
  `internal/hosting.Registry` into the query path, mirroring the existing
  `SetACL`/`SetAudit`/`SetAuth` setters; `nextsqld` wires it in alongside
  the pre-existing `srv.Database`/`srv.Realm` assignments.
- On a legacy/non-hosted deployment (no registry configured), or for a
  non-admin caller, both views return zero rows rather than erroring —
  same gating convention as `system.resource_groups`.
- Live-verified against a real `nextsqld` with a real two-database
  deployment: an admin session sees the real registry contents; a
  `CONNECT`-only non-admin session sees empty result sets on both views.
- No WAL/catalog/wire-protocol change; `SchemaVersion` not bumped. M2-4b
  (realm-local auth/ACL store) and M2-4c (`system.database_operations`)
  remain open. Not part of the Phase 27 release gate.

### Multi-database hosting — M2-4 dependency correction and scoping (2026-09-02)

- No code changes — scoping and documentation only.
- Corrected a stale dependency note: M2-4 (realm-scoped auth,
  `system.realms`/`system.databases`/`system.database_operations`
  introspection) does not actually depend on M2-3b-2/3 (resource
  budgeting, task-pool centralization) — those are orthogonal concerns.
  M2-4's real dependencies (M2-1, M2-2, M2-3a) are all already landed.
- Decomposed M2-4 into three further sub-increments in
  `docs/design-multidatabase-dbaas.md` §16: M2-4a (`system.realms`/
  `system.databases` — small, follows the established `system.*`
  pattern), M2-4b (realm-local auth/ACL store + the
  `(RealmID, PrincipalID, DatabaseID, privilege, scope)` authorization
  tuple — the real architectural work), M2-4c (`system.database_operations`
  — needs new operation-history tracking that doesn't exist yet).

### Multi-database hosting — M2-3b-1 reference counting + idle eviction + open-failure quarantine (2026-09-02)

- A secondary database opened via `internal/dbmanager.Manager` now closes
  when its last connection disconnects, instead of staying open until
  process exit, and reopens cleanly on the next connection. The primary
  database is pinned and never evicted.
- `Manager.Acquire` now returns an idempotent release closure, wired into
  the connection teardown path (`internal/protocol/server.go`); eviction
  reuses the already-durable `DB.Close()`, closing a secondary database's
  task runtime before it and its key envelope after, in the correct order.
- A database that repeatedly fails to open is now quarantined with
  exponential backoff (200ms base, doubling, capped at 30s) instead of
  being retried in a tight loop.
- Live-verified against a real `nextsqld`: real file descriptors
  (database file, WAL segment, undo log) confirmed open during a live
  secondary-database connection and fully closed after disconnect via
  `/proc/<pid>/fd`, with data surviving repeated evict/reopen cycles.
- No WAL/catalog/wire-protocol change. M2-3b-2 (cross-database memory
  budget) and M2-3b-3 (centralizing background task pools) remain open.
  Not part of the Phase 27 release gate.

### Multi-database hosting — M2-3b scoping and decomposition (2026-09-02)

- No code changes — scoping and documentation only.
- Investigated M2-3b's full spec (reference counting across 7 subsystems,
  idle eviction, a global memory budget, centralizing background pools)
  and decomposed it into three further sub-increments in
  `docs/design-multidatabase-dbaas.md` §9: M2-3b-1 (connection/session
  refcounting + idle eviction + open-failure quarantine — small, reuses
  existing hooks), M2-3b-2 (cross-database memory budget, larger, no
  existing infrastructure), M2-3b-3 (`TaskRuntime` centralization, a
  genuine internal redesign).
- Corrected a stale claim in the design doc: sessions, CDC, and tasks
  already have live reference registries (`DB.sessions`, `db.cdcSubs`,
  `TaskRuntime.running`) that were simply never consulted for DB
  lifecycle purposes — not "no subsystem exposes a ref today" as
  previously stated.
- Confirmed backup and replication are vacuous for this track for now:
  backup never touches a manager-opened database, and M2-3a never
  attaches replication to a secondary database.

### Multi-database hosting — M2-3a bounded DatabaseManager (2026-09-02)

- `nextsqld` can now genuinely serve more than one database: a
  connection's `Hello.Realm`/`Hello.Database` can route to a distinct,
  already-registered (`nextsql database create`) database, not just
  validate identity against the one primary database.
- New `internal/dbmanager.Manager`: a bounded, keyed (by durable database
  ID) map of open handles with single-flight open (concurrent requests
  for the same not-yet-open database share one open, never duplicate it),
  a small fixed open-database limit (`max_open_databases`, default 8),
  and no eviction — an opened database stays open until process exit.
- Secondary databases open single-node only (no Raft/replication
  attachment, no PITR archiving) and, once opened, get their own
  `TaskRuntime` and their own copies of the WAL-retention/disk-watermark/
  replica-lag monitors.
- Fixed a real bug caught by testing, not inspection: database-routing
  resolution ran after `TypeReady` was already sent to the client — the
  wire protocol's definitive success signal, read once with no further
  reads — so a routing failure would never reach the client, which would
  see a successful connection despite server-side rejection. Moved
  resolution to before `TypeReady`.
- Fixed a second real bug, caught only by live verification against a
  real `nextsql database create`d database: a managed database's key file
  unlocks an *envelope* keystore next to the database file (like the
  primary), it isn't usable directly as the database's own key.
- No reference counting, idle eviction, memory budget, or central bounded
  background pools yet — that's M2-3b, not scheduled. Not part of the
  Phase 27 release gate.

### Multi-database hosting — M2-2 Hello realm field (2026-09-02)

- Added `Hello.Realm`, an additive opt-in trailing field on the wire
  protocol handshake, so a client can identify which hosted realm it
  intends to reach. `nextsqld` validates it as a flat-string check against
  the one realm the process serves; a mismatch fails cleanly with
  `unknown realm`.
- No frame-version bump: a client that never configures a realm sends the
  exact same Hello it always has, so old/unconfigured clients remain
  permanently compatible with any server. A client that does select a
  realm requires a new-enough server and fails closed against an old one.
- Updated all 6 official drivers (Go, PHP, JS-shared[Bun+Deno], Node,
  Python, Ruby) with an optional `Realm`/`realm` config field.
- Verified live against a real `nextsqld` with two independent drivers.
- Routing/identity validation only — not yet a live `hosting.Registry`
  lookup or selectable multi-database routing (that's M2-3); not
  realm-scoped authorization (that's M2-4). Not part of the Phase 27
  release gate.

### P27 Operational maturity + workload governance — replication-orphan STRONG-read mitigation (2026-09-02)

- Investigated the "local commit precedes replication acknowledgment"
  structural fix in depth and found it needs new WAL flush-barrier
  semantics plus crash-recovery changes (a durably-flushed commit record
  can't currently be voided by a later abort record, and two unrelated
  call sites — `Checkpoint()`, and the Raft FSM-apply path — can flush a
  pending commit as a side effect) — bigger than previously scoped. Landed
  a stronger mitigation instead of the full redesign, at the user's choice.
- A local commit that fails to replicate now marks the node
  replication-suspect (new `storage.ReplicationOrphanReporter` hook,
  implemented by `*replication.Cluster`). `Cluster.StrongReadBarrier` fails
  `Unavailable` while suspect, regardless of leadership — closing the case
  a leadership check alone can't (a `Replicate` failure that isn't a
  leadership loss). Scoped to `STRONG` reads only; `BOUNDED`/`STALE` are
  unaffected, and no leadership transfer is forced.
- New `CLUSTER RECONCILE CONFIRM` SQL (cluster `ADMIN`, not in a
  transaction, node-local, `CONFIRM` mandatory) clears the flag once an
  operator has verified/repaired the node; new `nextsql cluster reconcile
  confirm` CLI subcommand. New `system.replica_health.replication_suspect`
  column for monitoring. No automatic clearing.
- Phase 27's "local commit precedes replication acknowledgment" checklist
  line stays open — this is a stronger mitigation, not the structural fix.
- No WAL/catalog/wire-protocol change.

### P27 Operational maturity + workload governance — resource-group Priority enforcement (2026-09-02)

- `RESOURCE GROUP ... WITH (PRIORITY = n)` is now enforced. `internal/scheduler/admit.go`'s
  `Admission` gate replaced its channel-semaphore wait path with a
  mutex-protected `container/heap` priority queue: when a slot frees up and
  more than one caller is queued for it, the highest-priority waiter is
  admitted first (FIFO among equal priorities).
- New `Admission.AcquireWithPriority(ctx, priority)`; the plain `Acquire(ctx)`
  entrypoint and every existing caller (per-resource-group gates, claimed-task
  execution) are unchanged and behaviorally identical.
- Only `Session.ExecContext`'s process-wide acquire now threads through the
  session's assigned resource group's `Priority` — the only gate shared
  across groups, hence the only place cross-group ordering is meaningful.
- Ordering only, never preemption: an already-admitted lower-priority query
  is never interrupted, and a waiter's own `QueueWait` timeout is unaffected
  by priority — a priority-0 caller sees identical behavior to before.
  Starvation under sustained high-priority contention is an accepted,
  unmitigated tradeoff, not a new fairness mechanism.
- Closes Phase 27's last open resource-group checklist line; one Phase 27
  exit-gate item remains (local-commit-before-replicate-ack, deliberately
  deferred).
- No WAL/catalog/wire-protocol change.

### P27 Operational maturity + workload governance — resource-group scheduler-class-integration + unbounded-pools audit (2026-09-02)

- Fixed a real gap: claimed-task/scheduled-workflow execution
  (`executor.executeClaimedTask`) ran outside the process-wide
  `scheduler.Admission` gate entirely, bounded only by `TaskRuntime`'s own
  separate worker limit. It now acquires the shared gate exactly like a
  regular query does, including the same reject-before-any-state-mutation
  behavior so a rejected task's lease simply expires and is retried later.
- Audited every other background-work class (API, `ANALYZE`, `MAINTAIN`,
  `SUBSCRIBE`, backup) and confirmed no independent unbounded pools exist
  in the live server.
- Resource-group **Priority** remains deliberately unenforced: real
  priority-ordered admission needs a `scheduler.Admission`
  concurrency-primitive redesign, judged too risky to rush into this audit.
- Closes 2 of Phase 27's last 3 open resource-group checklist lines.
- No WAL/catalog/wire-protocol change.

### Multi-database hosting — M2-1 registry realm/database creation primitives (2026-09-02)

- New `Registry.CreateRealm`/`CreateDatabase` (`internal/hosting`) and
  `nextsql realm create` / `nextsql database create` CLI: registers and
  physically provisions an additional managed database (durable
  `PROVISIONING` → create/verify-open → `ACTIVE`, idempotent and
  crash-safe on retry) at the previously-unused `LayoutManaged` path
  scheme.
- The M2 "single-node multi-database routing" milestone of the
  Multi-database hosting cross-cutting track (`docs/design-multidatabase-dbaas.md`)
  is decomposed into four gated sub-increments (M2-1..4); this is M2-1.
- `nextsqld` does not yet open or serve a database created this way — that
  is a later sub-increment (M2-3).
- Not part of the Phase 27 release gate; a separate cross-cutting track.

### P27 Operational maturity + workload governance — online format/catalog migration strategy (2026-09-02)

- New `docs/storage-format.md` "Format and catalog migration strategy"
  section: catalog-record changes (`NSCT` and friends) are safe to migrate
  online today via the existing multi-version-decode pattern; physical
  format (page/superblock) changes require the offline dump/reload path
  (`nextsql backup`/SQL copy into a freshly created database).
- Extracted `internal/upgrade/compat` — a dependency-free leaf package
  holding the format-compatibility catalog (`Family`/`Spec`/`Catalog`/
  `Check`/`Compatible`), split out of `internal/upgrade` to break an
  import cycle that had prevented it from ever being used outside its own
  package.
- `internal/storage/file.decodeSuperblock` and `catalog.DecodeTable` now
  enforce this catalog directly instead of each re-implementing their own
  version-range check, so what `nextsql diagnose` prints can no longer
  drift from what's actually enforced. The version-mismatch error now
  names the actual and supported version numbers.
- Closes the Phase 27 "Online format/catalog migration strategy where
  safe" checklist item.
- No wire-protocol change; every currently-valid superblock/catalog
  version still opens identically.

### P27 Operational maturity + workload governance — replica-lag management (2026-09-02)

- New `replica_lag_check_ms` / `replica_lag_warn_entries` config keys
  (default 0/1000). When enabled, `nextsqld` periodically reads this node's
  own `system.replica_health.apply_backlog` and logs an edge-triggered
  warning once it reaches the threshold, plus a recovery line once it drops
  back below.
- Alerting only, by design: unlike disk watermarks, nothing is rejected —
  a lagging follower doesn't affect the leader's ability to accept writes,
  and `Cluster.FollowerReadHealthy` already keeps a too-stale follower out
  of bounded-staleness read routing regardless of this setting.
- Current backlog and cumulative warn count are exposed via the metrics
  registry (`ReplicaApplyBacklog`/`ReplicaLagWarns`).
- Closes the Phase 27 "Replica-lag management" checklist item.
- No WAL/catalog/wire-format change.

### P27 Operational maturity + workload governance — disk watermark policies + capacity warnings (2026-09-02)

- New `internal/diskspace` package: cross-platform filesystem capacity check
  (`statfs`/`GetDiskFreeSpaceEx`) — physical disk space, distinct from
  `storage.Engine`'s logical per-database `StorageCapBytes`.
- New `disk_watermark_check_ms` / `disk_watermark_warn_percent` /
  `disk_watermark_reject_percent` config keys (default 0/85/95). When
  enabled, `nextsqld` periodically checks free space on the volume holding
  `--data-dir`: at the warn threshold it logs (the capacity warning); at the
  reject threshold it additionally rejects new mutating statements with
  `Unavailable`, using hysteresis so the reject state only clears once usage
  drops back below the warn threshold, not merely below the reject one.
- The reject state is a new node-local flag, independent of `CLUSTER
  MAINTENANCE ENABLE`/`DISABLE`: neither can clear the other.
- Current usage and cumulative warn/reject counts are exposed via the
  metrics registry.
- Closes the Phase 27 "Disk watermark policies" and "Capacity warnings"
  checklist items.
- No WAL/catalog/wire-format change.

### P27 Operational maturity + workload governance — backup retention management (2026-09-02)

- New `nextsql backup list --base-dir DIR` and `nextsql backup prune
  --base-dir DIR (--keep-count N | --keep-days N) [--confirm]`. Each
  immediate subdirectory of `--base-dir` with a valid backup header counts
  as one backup (anything else is silently skipped); `prune` selects
  backups older than the policy, oldest first, but never the single newest
  backup regardless of age. Without `--confirm` it only previews; nothing
  is deleted until you pass it.
- Purely additive: the existing flag-first `nextsql backup --data-dir ...
  --out ...` invocation is unchanged.
- Closes the Phase 27 "Backup retention management" checklist item.
- No WAL/catalog/wire-format change.

### P27 Operational maturity + workload governance — WAL retention management (2026-09-02)

- New `wal_retention_ms` config key: when positive and `wal_archive` is also
  set, `nextsqld` periodically advances `DB.SetWALRetentionHorizon` to the
  newest archived segment's LSN at or before `now - wal_retention_ms`,
  reusing the same PITR lookup (`backup.ResolveUntilTime`) `nextsql restore
  --until` already relies on. 0 (default) leaves the horizon unmanaged,
  matching prior behavior. A no-op without `wal_archive` — pruning without
  an archiver would destroy the only copy of that history.
- This only maintains the horizon; pruning itself is unchanged — still
  only happens during a `MAINTAIN DATABASE` you run or schedule yourself.
  `nextsqld` has no automatic maintenance scheduler.
- Closes the Phase 27 "WAL retention management" checklist item.
- No WAL/catalog/wire-format change.

### Official Python and Ruby drivers (2026-09-02)

- New `drivers/python` (stdlib only — `socket`/`ssl`/`decimal`/`json`;
  Python 3.10+) and `drivers/ruby` (stdlib only — `socket`/`openssl`/
  `bigdecimal`/`json`; Ruby 3.0+). Not published as packages — import from
  the tree directly, matching every other official driver. Full NSQL v1
  surface: `Connection`/`Cluster` (leader routing + follower reads),
  streaming `Rows`, prepared statements, idempotent exec, `node_status`,
  `set_read_consistency`, `cancel`, and every value kind (UUID, STRING/
  TEXT, DECIMAL, TIMESTAMPTZ, dense/sparse VECTOR, JSON, POINT/BOX/LINE/
  POLYGON). Field-level `ENCRYPTED CLIENT` support is not yet ported
  (tracked as a follow-on).
- Verified against a real, locally-built `nextsqld` (plaintext and TLS),
  not just unit-tested — this caught and fixed two real encode/decode bugs
  before shipping: a row-descriptor column-type entry is 6 bytes, not 7;
  and a VECTOR parameter's wire payload repeats `dim`+flag as its own
  leading bytes separately from the generic value header's metadata,
  which an initial pass had collapsed into one.
- **Found and fixed a real, previously-latent bug in the existing PHP,
  Node, Bun, and Deno drivers** (not present in Go, which already had it
  right): a failed query permanently desynced the connection, because the
  server always sends `Error` then `Ready` and these drivers never drained
  that trailing `Ready` outside a couple of call sites — the next call on
  the same connection then misreads the stale `Ready` and fails with a
  spurious "unexpected message type." Any application that caught a query
  error and kept using the connection was silently broken. Fixed in all
  four by centralizing the drain in each driver's shared "unexpected
  message" helper. Verified live against `nextsqld` through `php`, `node`,
  `bun`, and `deno` before and after; every existing driver test suite
  re-run clean.
- No WAL/catalog/wire-protocol format change.

### P27 Operational maturity + workload governance — replication-orphan detection (2026-09-02)

- New `metrics.Registry.AddReplicationOrphan()` / `Snapshot.ReplicationOrphans`
  counts a transaction that committed to local storage but then failed to
  reach Raft quorum (see the "local commit precedes replication
  acknowledgment" item below) — pure additive observability, no behavior
  change. Previously silent; now a growing count is an operator-visible
  signal.
- The underlying gap itself — a bounded, latent local/cluster data
  divergence, not an acknowledged-write loss — remains open. A design
  review found the structural fix (deferring MVCC visibility until Raft
  quorum) too large to safely attempt as a rushed change, and a post-hoc
  compensating rollback unsound in general (another transaction can already
  have observed the data by the time a quorum failure is known). See
  `TODO.md`'s Phase 27 exit gate for the full writeup and the design
  review's findings.

### P27 Operational maturity + workload governance — rolling upgrade procedure + router/replication robustness fixes (2026-09-02)

- Documented the rolling-upgrade procedure (`docs/ops.md` "Rolling
  upgrade"): transfer leadership away from a node before draining it, drain
  it (stops accepting connections and closes its listener), restart it for
  the binary swap, wait for it to catch up, repeat per node — quorum stays
  intact throughout on a 3+-voter deployment, so writes never stop landing
  cluster-wide.
- New end-to-end integration test proving the procedure's core claim (the
  first Phase 27 exit-gate line, "planned maintenance can drain without
  unnecessary transaction loss"): `tests/integration/rolling_upgrade_test.go`
  runs a 3-node cluster under continuous write load through a full
  transfer-leader → drain → simulated-restart → rejoin cycle and asserts no
  acknowledged write is lost.
- **Building that test surfaced and fixed three real robustness gaps**, all
  in code that predates this session:
  - `nextsql.Cluster` (the Go driver's routing client): a write or read
    routed to a connection that broke mid-flight (e.g. the node it targeted
    was just drained) surfaced a raw, non-retryable I/O error instead of the
    same retryable `unavailable` a genuine leader failover already produces;
    and a connection that died for good could be selected forever afterward
    (its last-known "leader" role was never invalidated), permanently
    breaking routing to the rest of the cluster. Both fixed: a transport
    failure now clears the affected connection's cached routing status and
    is reported as `unavailable`. Every other official driver has the
    equivalent client-side contract; only the Go driver's implementation
    needed the fix.
  - `protocol.ReadFrame` classified every read failure (EOF, connection
    reset) as `nerr.Protocol` (implying a malformed peer) instead of
    `nerr.IO` (a broken transport) — inconsistent with `WriteFrame`'s own
    failures, which were already `nerr.IO`. Fixed to match; genuine protocol
    violations (bad magic, unsupported version, oversized packet) are
    unaffected.
  - `replication.Cluster.Replicate` classified a `raft.Raft.Apply()` failure
    as `Internal` (non-retryable) unless it was exactly one of three
    sentinel errors. `raft.ErrLeadershipTransferInProgress` — exactly what a
    write racing `CLUSTER TRANSFER LEADER` produces — and
    `raft.ErrRaftShutdown` were missing from that list. Both added; both are
    transient, retryable conditions, not evidence of a bug.
- **Found, documented, not fixed** (tracked, out of this increment's scope):
  `storage.Engine.commitAndReplicate` commits a transaction to local storage
  before achieving Raft quorum; if quorum then fails (the same leader-
  transition race above), the local commit is not rolled back, leaving at
  most one un-replicated local row per affected node that ordinary catch-up
  never reconciles. No acknowledged write is ever lost from this — the
  property the exit gate and this increment's test depend on — but it is a
  real, latent divergence with no existing detection or repair path. See
  `docs/ops.md` "Rolling upgrade" and the TODO.md log entry for the full
  writeup.
- No WAL/catalog/wire-format change.

### P27 Operational maturity + workload governance — machine-readable operational CLI output (2026-09-02)

- New `--json` flag on `nextsql exec` and every `nextsql cluster` subcommand
  (`status`, `transfer-leader`, `drain`, `maintenance enable|disable`),
  printing a single JSON object instead of tab-separated text —
  `{"columns": [...], "rows": [[...]], "affected": N}` for the four
  SQL-backed commands, the `replication.Status` fields for `cluster
  status`. Cell values are stringified the same way the existing TSV output
  already rendered them.
- Closes the Phase 27 "Machine-readable operation status" Operational-CLI
  checklist item.
- No server-side change — pure CLI output formatting.

### P27 Operational maturity + workload governance — maintenance mode (2026-09-02)

- New `CLUSTER MAINTENANCE ENABLE|DISABLE` admin SQL statement and
  `nextsql cluster maintenance enable|disable` CLI wrapper. While enabled,
  the node this connection reached rejects every mutating statement
  (`INSERT`/`UPSERT`/`UPDATE`/`DELETE`, all DDL, and `BEGIN`) with
  `Unavailable`, reusing the same write/no-write classification
  `CLUSTER TRANSFER LEADER`'s leader-routing gate already applies; reads
  (autocommit `SELECT`, `SHOW`, `system.*`) keep working.
- Requires cluster `ADMIN`; cannot run inside a transaction. Like
  `CLUSTER DRAIN` and unlike `CLUSTER TRANSFER LEADER`, it is purely
  node-local — not Raft-replicated — so it needs no attached cluster and a
  leader failover during a maintenance window does not carry the flag to the
  new leader (documented in `docs/ops.md` "Maintenance mode" alongside the
  intended enable-drain/upgrade-disable sequence).
- New `system.replication.maintenance_mode` column (also `system.raft`,
  `SHOW CLUSTER`) surfaces the current node-local state.
- Closes the Phase 27 "Maintenance mode" Server-lifecycle checklist item.
- No WAL/catalog/wire-format change.

### P27 Operational maturity + workload governance — idle transaction timeout (2026-09-02)

- New `idle_transaction_timeout_ms` config key (default 0 = no distinct
  bound) bounds how long a connection may sit with an open transaction and no
  traffic between frames before it is force-timed-out. Distinct from
  `transaction_timeout_ms` in *how* it is enforced, not just in name:
  `transaction_timeout_ms` is checked lazily at the start of the next
  statement, so a connection that never sends another statement keeps its
  transaction (and locks) open indefinitely regardless of that setting; the
  new `idle_transaction_timeout_ms` is instead enforced by the connection's
  own socket read deadline (`protocol.Limits.IdleTxn`) — the same mechanism
  `idle_timeout_ms` already used, just with its own, typically tighter, bound
  that applies only while a transaction is open — so it actively reclaims an
  abandoned transaction even if the client goes silent.
- **Real gap found and fixed while implementing this**: tearing down a
  connection with an open transaction — by this new timeout, by the existing
  general `idle_timeout_ms`, or by a forced close at the `Drain` deadline —
  never actually rolled the transaction back. Nothing released its locks;
  they stayed held by a `*executor.Session` nothing would ever resume again
  until the whole process restarted. New `executor.Session.Abort` (an
  exported force-rollback, no-op with nothing open) is now called from the
  protocol server's connection-teardown path whenever a session still has a
  transaction open, so every disconnect path releases it deterministically.
- Closes the Phase 27 "Idle transaction timeout" Session-controls checklist
  item.
- No WAL/catalog/wire-format change.

### P27 Operational maturity + workload governance — resource group assignment + enforcement (2026-09-02)

- `SET RESOURCE GROUP name` / `RESET RESOURCE GROUP` assign/clear a session's
  workload-governance class — the one surviving `SET`/`RESET` spelling after
  `SET TENANT` was removed. Assignment requires `USAGE` on the group
  (`GRANT USAGE ON RESOURCE GROUP name TO grantee` / `REVOKE ...`, new
  `ScopeResourceGroup` RBAC scope); cluster `ADMIN` bypasses like every other
  privilege check. A name that doesn't exist fails `NotFound` for anyone.
- Resource groups are now enforced, not just stored: a non-zero
  `MAX_CONCURRENCY` adds a second, strictly additional admission gate layered
  on top of the existing process-wide `scheduler.Admission` — a query in a
  bounded group must clear both gates, so a group can restrict concurrency
  further but never exceed the process-wide safety limit. Non-zero
  `WORKERS`/`MEMORY` override the session's per-query `scheduler.Limits`
  while the assignment lasts (`WORKERS` still clamped to the process ceiling
  by `Limits.normalized()`).
- Closes the Phase 27 "Workload max concurrency" and "Workload memory budget"
  / "Workload CPU/worker budget" checklist items. Remaining open: `PRIORITY`
  enforcement, integrating the API/analytics/workflow/maintenance/backup task
  classes with this same scheduler, and the "no independent unbounded pools"
  audit.

### P27 Operational maturity + workload governance — remote drain (2026-09-02)

- New `CLUSTER DRAIN [WITH (TIMEOUT_MS = n)]` admin SQL statement asks the
  node a connection reached to begin gracefully draining itself — the same
  `protocol.Server.Drain` mechanism `nextsqld` already runs on
  SIGINT/SIGTERM, now reachable without a restart or signal. Unlike
  `CLUSTER TRANSFER LEADER` this needs no Raft cluster and is not gated on
  the target being the current leader — draining is purely local to
  whichever node the connection reaches, so a follower is exactly as
  drainable as a leader. Runs in the background on the target node; the
  statement returns a `drain_initiated` acknowledgment immediately.
- New `nextsql cluster drain [--timeout-ms N] [--addr ...] [--user ...] ...`
  CLI subcommand.
- Closes the Phase 27 "`nextsql cluster drain <node>`" Operational-CLI
  checklist item.
- New `executor.DB.SetDrainFunc`/`Drain` (nil-safe no-op → `Unavailable` in
  embedded/CLI use with no listening server); wired in `cmd/nextsqld/main.go`
  to `protocol.Server.Drain`.
- **Bug fix found while verifying this under load**: `protocol.Server`'s
  idle-connection detection (used by `Drain` since the P27 second increment)
  could hard-close a connection while its own just-finished statement's
  response was still being written back — a latent race made far more
  likely to trigger by a self-triggered `CLUSTER DRAIN` than by an
  externally-triggered SIGINT drain. Fixed by also treating a connection as
  busy while its response is mid-flight (`backend.queryConn != nil`), not
  just while a statement is executing or a transaction is open.
- No WAL/catalog/wire-format change; new lexer
  keyword `DRAIN`.

### P27 Operational maturity + workload governance — statement, transaction, and lock timeouts (2026-09-02)

- New `statement_timeout_ms` config key makes the existing per-statement
  `scheduler.Budget` wall-clock bound (`scheduler.DefaultTimeout`, 30s)
  operator-configurable — it was previously hardcoded.
- **Real gap found and fixed while auditing this**: the per-statement time
  budget was wired into a real deadline context, but the base
  SeqScan/IndexScan row-emission loops (`internal/executor/access.go`) never
  actually checked it — only specialized paths (ANALYZE, vector/full-text
  search, index rebuild, partition maintenance) did. A plain `SELECT` could
  run past its statement timeout unbounded. All six physical scan callbacks
  now check the budget per row, matching the convention already used
  elsewhere in the executor.
- New `transaction_timeout_ms` config key (default 0 = unbounded, unlike the
  statement/idle timeouts this has no historical non-zero default) bounds a
  transaction's total open lifetime; once exceeded, the next statement
  dispatched inside it — even `COMMIT` — force-aborts the transaction and
  fails `exhausted`, while the connection itself stays usable afterward.
- New `lock_timeout_ms` config key (default 0 = block indefinitely) bounds
  how long a contended, non-deadlocking key/range lock wait blocks before
  failing `exhausted`; only deadlock cycles were ever detected without this.
  Process-wide, not per-connection — the shared lock table has no
  per-connection identity to key a limit off.
- Closes the Phase 27 "Statement timeout", "Transaction timeout", and "Lock
  timeout" Session-controls checklist items. "Idle transaction timeout"
  remains open and distinct (an idle-while-in-a-transaction-specific timer,
  separate from both `idle_timeout_ms` and `transaction_timeout_ms`).
- No WAL/catalog/wire-format change.

### P27 Operational maturity + workload governance — RESOURCE GROUP design (2026-09-02)

- New `CREATE`/`ALTER`/`DROP RESOURCE GROUP` admin SQL statements declare a
  durable, catalog-persisted, WAL-recovered workload-governance descriptor
  (`MAX_CONCURRENCY`, `MEMORY`, `WORKERS`, `PRIORITY`; zero means
  unset/unbounded), gated on cluster `ADMIN` like `CREATE ROLE`/`CREATE
  USER`. New admin-only `system.resource_groups` introspection view and
  `system.capabilities` row (`resource_groups`, status `experimental`).
- **Descriptor only**: no session or user can yet be assigned to a resource
  group, and `internal/scheduler`'s process-wide admission gate and per-query
  budgets are untouched by it. This increment closes the Phase 27 "Design
  RESOURCE GROUP" checklist item; workload assignment and scheduler
  enforcement are later increments.
- No WAL/wire-format change beyond the new catalog key prefix (`U`); new
  lexer keyword `RESOURCE`.

### P27 Operational maturity + workload governance — leader transfer (2026-09-02)

- New `CLUSTER TRANSFER LEADER` admin SQL statement wraps the existing
  `replication.Cluster.TransferLeadership()` library call (previously
  reachable only from Go code) so a planned handoff ahead of a restart or
  maintenance window no longer has to wait for a crash to trigger failover.
  Cannot run inside a transaction, requires cluster `ADMIN`, fails
  `Unavailable` on a single-node deployment.
- New `nextsql cluster transfer-leader [--addr ...] [--user ...] ...` CLI
  subcommand connects to a live server and issues the statement, printing
  the same machine-readable tab-separated output as `nextsql exec`.
- No persistent, catalog, WAL, or NSQL wire-format change; new lexer
  keywords `TRANSFER`/`LEADER` and AST node `ast.TransferLeader`.

### P27 Operational maturity + workload governance — graceful shutdown (drain) (2026-09-02)

- New `protocol.Server.Drain(timeout)`: stops accepting new connections
  immediately, then closes each existing connection as soon as it is idle
  (no in-flight statement, no open transaction) instead of force-aborting
  whatever is mid-flight; anything still busy at `timeout` is force-closed.
- `nextsqld` now drains on SIGINT/SIGTERM instead of hard-closing every
  connection outright. New `shutdown_drain_ms` config key (default 30000,
  `0` disables waiting for busy connections).
- No persistent, catalog, WAL, or NSQL wire-format change.

### P27 Operational maturity + workload governance — connection/idle limits (2026-09-02)

- `max_connections` and `idle_timeout_ms` config keys make the previously
  hardcoded 128-session cap and 60 s idle deadline (`protocol.Limits`)
  operator-configurable per node.
- New `max_connections_per_user` (default 0 = unlimited) rejects a
  connection after authentication succeeds but before a session is
  created, once a user name already holds the configured number of
  concurrent connections; the client sees `exhausted`. Closing one of the
  user's connections frees a slot.
- All three are node-local and not synchronized across a Raft cluster. No
  persistent, catalog, WAL, or NSQL wire-format change.

### P26 System catalog / introspection 2.0 — exit gate closed (2026-09-02)

- Added admin-only `system.users` (`name, password_algo`), `system.roles`
  (`role, members`), and `system.grants` (`grantee, privilege, scope,
  object`) — closing the one real gap the exit-gate audit found: listing
  users, roles, or grants had no official SQL-level answer before this, so a
  Studio/Manager security dashboard would have had to read the `auth.Store`
  or `security.ACL` files directly. Never exposes a password hash or salt.
- Added nine missing `system.capabilities` rows for previously-undiscoverable
  P23/P25 surfaces: `mtls`, `token_credentials`, `oidc_broker`,
  `audit_chain`, `storage_caps`, `vector_ivf`, `vector_ivfpq`,
  `vector_sparse`, `quantized_vector_index`. Corrected a stale `fulltext`
  description missing WEIGHT/FACET.
- Closed an RBAC test-coverage gap (`system.table_stats`/`index_stats`/
  `partitions`/`workflows` previously had no dedicated RBAC test of their
  own) and confirmed realm/database visibility is a structural guarantee of
  the current single-database-per-process architecture, not a filter that
  could silently regress.
- **P26 System catalog / introspection 2.0 is now complete.** See
  `docs/system-catalog.md` "P26 exit gate closure (2026-09-02)". The current
  release gate is P27 Operational maturity + workload governance.

### P26 System catalog / introspection 2.0 — SHOW aliases (2026-09-02)

- Added `SHOW DATABASES`, `SHOW TABLES`, `SHOW INDEXES`, `SHOW CONNECTIONS`,
  `SHOW QUERIES`, `SHOW TRANSACTIONS`, `SHOW LOCKS`, `SHOW CLUSTER`, and
  `SHOW STORAGE`.
- Each command is parsed as a read from its canonical `system.*` source, so
  system-table RBAC, visibility, redaction, and stable columns remain
  authoritative. The aliases accept no clauses; filtered/paginated consumers
  use direct system-table queries.
- Corrected stale capability metadata: completed RANGE/HASH/LIST partitioning
  is `supported`; follower-read metadata now describes live
  STRONG/BOUNDED/STALE routing; client-field-encryption metadata lists every
  official driver. Added `system_schema_v2` and `system_show_aliases` rows.
- Fixed `system.storage.database` (and therefore `SHOW DATABASES`) to return
  only the configured logical database name. It no longer exposes the engine
  filesystem path; unnamed embedded databases report `default`.
- No persistent, catalog, WAL, Raft, wire-format, or system-schema-version
  change.

### P26 System catalog / introspection 2.0 — live locks (2026-09-01)

- `system.locks` now reports every currently held key/range lock in the
  storage engine instead of always returning zero rows.
- `internal/txn.LockManager` gained a `Snapshot()` method and a `tag string`
  parameter on `Acquire`/`AcquireRange` (table-name label, best-effort —
  the lock key namespace is shared across every table in one engine).
  `Manager.LockKey`/`LockRange` and `btree.Tree` (`Name`/`SetName`) thread
  the tag from the executor's table/index/vector/partition tree resolvers
  down to the lock table.
- `mode` is `shared`/`exclusive`; `granted` is always `true` (waiting
  requests are not surfaced). Visibility matches `system.transactions`:
  non-admins see only locks held by their own user's transactions.
- Docs: `docs/system-catalog.md` and its web/USAGE counterparts updated.

### P26 System catalog / introspection 2.0 — live session/query/transaction/change-stream rows (2026-09-01)

- `system.sessions`, `system.active_queries`, `system.transactions`, and
  `system.change_streams` now report real, node-local, in-memory state
  instead of always returning zero rows.
- New process-local registries on `executor.DB`: `RegisterSession` /
  `UnregisterSession` / `LiveSessions` (keyed by an atomic session-id
  counter; the protocol server registers/unregisters around each
  connection's lifetime — a `Session()` obtained directly for
  embedded/CLI/test use is never registered and stays invisible to these
  tables) and `CDCSubscriptions` / `registerCDCSubscription` /
  `unregisterCDCSubscription` / `updateCDCSubscriptionLSN` for open
  `SUBSCRIBE` streams.
- New mutex-guarded snapshot state on `executor.Session` —
  `CurrentQuery`/`beginQuery`/`endQuery` and
  `TxnSnapshot`/`setTxnActive`/`clearTxnActive` — published at existing
  statement/transaction boundaries so another session's introspection query
  can read them safely; the session's own unsynchronized `execSQL`/`s.x`
  fields stay same-goroutine-only, as before. The CDC subscription LSN is
  published the same way, via `atomic.Uint64`, since `cdc.Subscription`
  itself is not safe to read cross-goroutine.
- RBAC: a non-admin sees only their own sessions/queries/transactions
  (matching the existing `system.tasks` owner-filter pattern);
  `change_streams` is filtered by table visibility (matching
  `system.columns`/`system.indexes`).
- `system.locks` is intentionally still a stub (always empty): the shared
  `txn.LockManager` has no table attribution for held key/range locks today.
  See `TODO.md` Phase 26 for the scoped follow-on.
- New docs: `docs/system-catalog.md` and
  `docs/web/content/docs/system-catalog.md` — the first documentation for
  the whole `system.*` schema.

### P25 Security 2.0 — exit gate closed: security review sign-off (2026-09-02)

- Added `## P25 security review sign-off (2026-09-02)` to `docs/security.md`,
  in the same dated surface-by-surface review format as the existing "P16
  security review": scope is everything landed since P16 (mTLS/service
  identity, short-lived credentials, the external-IdP broker, field-level
  client encryption, password-hash evolution, audit-chain hardening).
- This is the production-gating decision for the "P25 Security 2.0 audit"
  table: every row was already `yes`/`yes`/`yes` for
  designed/implemented/tested, and this sign-off flips the production-gated
  column to `yes` except the design-only `OIDC design` row and a small set of
  explicit, documented non-goals (OCSP, optional OIDC opaque-token
  introspection, JIT provisioning, searchable/deterministic client-side
  encryption, and local-audit-file suffix-truncation detection without an
  external WORM/transparency system).
- Updated `docs/client-encryption.md`'s "Production-gating sign-off (Phase
  25)" to drop its "awaits phase-wide gate" hedge — `ENCRYPTED CLIENT` stays
  labeled `experimental` in `system.capabilities` only because no
  searchable/deterministic mode ships (a deliberate scope decision), not
  because of any open blocker.
- All four `Phase 25 exit gate` items in `TODO.md` are now checked; the
  phase-level `P25 Security 2.0` checkbox and roadmap summary are checked;
  every "current release gate" reference across `TODO.md`, `ROADMAP.md`,
  `SKILLS.md`, `AGENTS.md`, and `USAGE.md` now points at **P26 System
  catalog / introspection 2.0**.
- No code change in this entry — documentation and gate closure only, on top
  of the audit-hardening, field-encryption KMS-lifecycle, and Argon2id
  increments below.

### P25 Security 2.0 — audit hardening: tamper-evident/signed audit chain + verification tooling (2026-09-02)

- Every new `nextsql.audit` record now carries a versioned `NSAC` v1 chain
  trailer: `chain_version`, a monotonically increasing `seq`, `prev_hash`, and
  `hash = SHA-256("NSAC\x01" || prev_hash || seq-u64le || canonical-event-json)`.
  The canonical event JSON clears `seq`/`prev_hash`/`hash`/`sig`/`key_id`
  before hashing, so a caller cannot forge chain fields through the `Event`
  struct. Pre-chain JSON lines are accepted only as one contiguous legacy
  prefix; `OpenAudit` verifies the retained chain before allowing an append,
  rejects an incomplete final line, and fails closed on a symlink, non-regular
  file, or a file readable by group/others.
- Added `internal/security/auditkeys.go`: `NSAK` v1, a bounded (64-key)
  Ed25519 signing keyset with one current key, rotation overlap, retirement
  (drops the private seed, keeps the public key so historical records still
  verify), atomic mode-`0600` writes, a verify-only `WritePublic` export, and
  last-known-good reload — the same lifecycle shape as the existing `NSTK`
  short-lived-credential signing keys.
- The first configured signer appends a signed `audit.signing.enabled`
  transition record; every chained record from that point on must be signed,
  so the start of the signed segment cannot be silently moved by stripping
  the earliest signature.
- Added `internal/security/auditverify.go`: `VerifyFile` streams an audit log
  one line at a time (1 MiB line cap), classifies each line as
  legacy/chained/signed, verifies the hash chain and (given a keyset) every
  signature, and reports the first bad line and why.
- Added the `nextsql audit` CLI (`cmd/nextsql/audit.go`): `keygen`, `rotate`,
  `retire`, `list-keys`, `export-public`, and
  `verify --file F [--keyset F | --pubkey F] [--json]`.
- `nextsqld` gains `--audit-signing-keyset` / `audit_signing_keyset`: it
  refuses to start against an existing signed chain without a configured
  signer, verifies the keyset before signing, reloads it on `SIGHUP` with
  last-known-good fallback, and records `audit.signing.reload` as a
  security-setting event on both success and failure.
- No NSQL wire-format, catalog, or WAL change. This closes the last open P25
  implementable-scope checklist item; only the phase-wide exit gate (a dated
  security review sign-off) remains before P25 closes.
- Tests: `TestAuditChainVerifiesCleanLog`, `TestAuditChainDetectsTamperedLine`,
  `TestAuditChainDetectsDeletedLine`, `TestAuditChainDetectsReorderedLines`,
  `TestAuditSigningRoundTrip`, `TestAuditSigningTransitionCannotLoseSignature`,
  `TestSignedAuditCannotResumeUnsigned`, `TestAuditKeysetRotationOverlap`,
  `TestAuditKeysetReloadLastKnownGood`, `TestOpenAuditKeysetBoundsAndRejectsSymlink`,
  `FuzzDecodeAuditKeys`, `TestAuditKeygenRotateRetireListExportPublicCLI`,
  `TestAuditVerifyCLI`, `TestAuditVerifyLegacyFileCLI`.
- While verifying the full repository-wide suite for this increment, fixed
  `tests/integration/drivers_test.go`'s `TestDenoDriverUnit`: it invoked
  `deno test` with only `--allow-net`, so the Deno `FileFieldKeyring` unit
  test added by the prior increment failed closed (`NotCapable`) on
  `Deno.makeTempDir` under the full-suite run despite passing standalone;
  added `--allow-read --allow-write`.

### P25 Security 2.0 — password hashing: Argon2id migration (2026-09-02)

- Added `golang.org/x/crypto/argon2` (pinned to `v0.33.0`, the newest
  version whose own `go.mod` stays compatible with this module's `go 1.22`
  directive — no toolchain-version bump). Every new `internal/auth` login
  record (`Store.Upsert`) now hashes with Argon2id (time cost 1, memory
  64 MiB, parallelism 4, 32-byte output — the package documentation's
  recommended parameters) instead of the hand-rolled PBKDF2-HMAC-SHA256.
- `NSAU` bumped to v2: each record carries an explicit algorithm byte plus
  Argon2id's memory/parallelism fields (zero for a legacy PBKDF2 record).
  `Decode` still reads v1 files unchanged; `Encode` always writes v2, so a
  v1 file upgrades in place the next time the store persists.
- `Store.Verify` transparently re-hashes an already-confirmed-correct
  legacy password with Argon2id and persists the upgrade before returning;
  a failed verify never rehashes, and a concurrent delete/re-upsert of the
  same user is detected and skipped rather than clobbered.
- Added `internal/auth/store_bench_test.go`
  (`BenchmarkVerifyPBKDF2`/`BenchmarkVerifyArgon2id`/
  `BenchmarkConcurrentLoginAttempts`) as the "Authentication DoS benchmark"
  tracker item — Argon2id's ~64 MiB-per-attempt memory cost is documented
  in `docs/security.md` "Password hashing" as the load-bearing number for
  sizing concurrent-login capacity limits.
- Tests: `TestV1FormatDecodesAndVerifies`, `TestNewRecordsAreArgon2idFromCreation`,
  `TestTransparentRehashUpgradesToArgon2id`; extended `FuzzDecode` seed corpus.

### P25 Security 2.0 — client-encrypted fields: durable key-rotation/revocation KMS lifecycle (2026-09-02)

- Added `FileFieldKeyring` to every official driver (Go, Node.js, Bun, Deno,
  PHP): a durable, atomic, versioned, 0600 file-backed `FieldKeyProvider`
  implementing the `NSFK1` on-disk format (mirrors the server's own `NSTK`
  signing-key lifecycle). Rotation makes a new key current while retaining
  every prior live key for overlap reads, persisted across process restart.
  Revocation overwrites the revoked key's material with zeros on disk,
  refuses to resolve the id afterward, rejects revoking the current key
  directly, and a revoked id can never be reused. Corrupt, truncated, or
  structurally invalid keyring files fail closed on decode.
- The `NSFK1` format is identical across every driver: a Go-produced fixture
  opens correctly in the Node driver, proving cross-language interop.
- This closes the last open item ("durable key-rotation/revocation KMS
  lifecycle") blocking `ENCRYPTED CLIENT` field-level encryption from being
  fully production-gated; see `docs/client-encryption.md`
  "Production-gating sign-off (Phase 25)". Formal production-gating still
  awaits the single phase-wide P25 exit gate (password hashing and audit
  hardening remain open), not any `ENCRYPTED CLIENT`-specific blocker.
- Tests: `drivers/go/nextsql_test.go`, `drivers/bun/nextsql.test.js`,
  `drivers/deno/nextsql_test.js`, `drivers/node/nextsql.test.js`,
  `drivers/php/tests/unit.php`.

### P25 Security 2.0 — client-encrypted fields: PITR + replication/failover (2026-09-01)

- Added `TestEncryptedClientPITRRestoresExactCiphertextAtTarget`
  (`internal/backup`): a base backup plus archived WAL restored to a target
  LSN before a later `UPDATE` retains `TEXT ENCRYPTED CLIENT`, returns the
  exact pre-target `NSCE1.` ciphertext byte-for-byte, excludes the later
  archived write, and decrypts correctly only through the client-side
  `clientenc` helper — the restored server never sees a field key.
- Added `TestHAEncryptedClientCiphertextSurvivesLeaderFailover` (`tests/ha`):
  a three-voter Raft cluster commits an encrypted-client write on the leader,
  confirms the identical acknowledged ciphertext replicates to every
  follower, kills the leader, confirms the new leader still serves and can
  decrypt the acknowledged ciphertext (no lost commit), commits a second
  ciphertext after failover, and confirms it — and its decrypt — on the
  remaining follower.
- These close the last two open field-level client-encryption gate items.
  No catalog/WAL/wire-format change. The capability remains `experimental` in
  `system.capabilities`: durable key-rotation/revocation KMS lifecycle is the
  remaining item before production gating.

### P25 Security 2.0 — experimental client-encrypted fields (2026-09-01)

- Added `type ENCRYPTED CLIENT` for bounded scalar UUID, STRING, TEXT, DECIMAL,
  TIMESTAMPTZ, JSON, and BOOL columns. `NSCT` v10 stores the logical plaintext
  type while rows and NSQL use an opaque physical STRING. Older v1–v9 catalog
  descriptors remain readable; unknown/truncated v10 metadata fails closed.
- Added portable randomized `NSCE1.` AES-256-GCM values. The authenticated
  context binds the exact database, table, column, public key id/type header,
  and random nonce. Wrong/revoked keys, context changes, type mismatch,
  truncation, and tampering return no plaintext. The server receives no field
  key and performs bounded structural/type validation only.
- Added fail-closed opaque-only SQL semantics: parameters, NULL, same-column
  ciphertext copies, and bare projection/RETURNING are allowed. Predicates,
  joins, expressions/subqueries, defaults, PK/FK/partition keys, indexes,
  SEARCH/FACET, grouping, ordering, DISTINCT, set operations, context-changing
  rename/partition transfer, and legacy-tenant migration are rejected.
- Added provider contracts, bounded in-memory overlap keyrings, key generation,
  and encrypt/decrypt helpers across Go, Node.js/TypeScript, Bun, Deno, and PHP.
  Every runtime uses the same `NSCE1.` scalar and canonical NSJB encoding;
  Go↔non-Go ciphertext fixtures verify portability. In-memory keyrings remain
  non-durable conveniences rather than KMS storage.
- Added encrypted close/reopen/plaintext scans, exact-ciphertext physical
  backup/restore, and logical export/import coverage. PITR and
  replication/failover are now covered too (see the entry below); durable
  key-rotation/revocation KMS lifecycle remains open, so `system.capabilities`
  labels the feature `experimental`, not supported or production-gated. There
  is no deterministic/searchable mode and no NSQL frame/version change.
  Format, leakage, migration, backup, and key lifecycle contracts are in
  `docs/client-encryption.md`.
- Repository build, focused functional/race tests, and 5-second `FuzzInspect`
  plus `FuzzDecodePartitionedTable` are green. The serialized all-package run
  passed through crash and HA but saw one transient Bun live-test page-isolation
  failure; that test then passed 5 consecutive isolated runs and the complete
  integration package passed on rerun.

### P25 Security 2.0 — embedded authentication broker (2026-09-01)

- Added `nextsqld --auth-broker-listen ADDR [--auth-broker-config FILE]` for
  single-node/non-HA deployments. It serves the existing broker handler on a
  separate bounded HTTP(S) listener; the config defaults to
  `DATA-DIR/nextsql-auth-broker.conf` and uses the standalone format.
- Embedded startup requires `token_verify_keyset` and proves that the broker's
  private current issuer key is accepted by it. `SIGHUP` reloads the verifier
  before the issuer and validates the candidate issuer key before publication.
  Raft/HA rejects embedded mode; HA deployments keep the standalone broker.
- Embedded exchanges consult the live native user store and direct/transitive
  ACL role membership. Missing users and empty policy-mapped∩held role sets
  deny immediately; the SQL server still applies `ACL.AllowedScoped` on every
  statement.
- Standalone and embedded modes now share `internal/authbroker.HTTPServer`,
  including TLS 1.3, off-loopback TLS enforcement, bounded timeouts, and
  graceful shutdown. The shared runtime removes a possible double TLS wrapping
  composition in the former standalone path.
- No credential, database persistent/catalog/WAL/Raft, or NSQL wire-format
  change. Optional opaque introspection and JIT provisioning remain off.

### P25 Security 2.0 — OAuth2 client credentials (2026-08-31)

- Added `nextsql login --client-credentials [--client-secret-file FILE]` for
  confidential workloads. It performs exact-issuer discovery, obtains a
  Bearer access token from the discovered HTTPS token endpoint, exchanges it
  at the existing broker, stores no client secret, and renews expired `NSSC1.`
  credentials non-interactively from the protected secret file.
- Added per-broker-profile `access_token_audience`. The broker accepts exactly
  one of `id_token` or `access_token`; JWT access tokens require the configured
  resource audience and exact `client_id`/`azp` binding in addition to the
  existing asymmetric signature, issuer, expiry, JWKS, replay, `NSIP`, RBAC,
  and TTL boundaries. Opaque access tokens/RFC 7662 remain unimplemented.
- Client-secret reads are capped at 64 KiB and reject empty, symlink,
  non-regular, or group/other-readable files. Redirects and HTTP bodies retain
  the existing fail-closed bounds. No database persistent/catalog/WAL/Raft or
  NSQL wire-format change.

### P25 Security 2.0 — key-derived OIDC audit source (2026-08-31)

- Added bounded `token_identity_source_hint=KEY_ID:oidc[,KEY_ID:oidc...]`
  configuration. After an `NSSC1.` signature verifies under a mapped broker
  key, `nextsqld` records `identity_source` `oidc` or `mtls+oidc`.
- The label is derived from the authenticated key id, not a client claim.
  Forged signatures, unverified/unknown keys, and unknown configured values
  stay generic or fail configuration loading; no credential/token id is logged.
- Fixed the audit redactor so its closed identity-source enum preserves the
  already-documented `token` / `mtls+token` values while unknown or
  secret-shaped values remain redacted.
- No `NSSC1.` credential-format, database persistent/catalog/WAL/Raft, or NSQL
  wire change. Targeted config, protocol, integration, forged-key-id,
  secret-leak, and race tests plus the serialized repository-wide functional
  gate are green.

### P25 Security 2.0 — interactive OIDC CLI (2026-08-31)

- Added `internal/oidcclient` and `nextsql login` / `logout` / `whoami`:
  exact-issuer discovery, Authorization Code + PKCE S256, random state/nonce,
  a transient bounded loopback callback, browser/manual URL handling, code
  redemption, broker exchange, and silent refresh.
- `nextsql exec --idp NAME` and server-mode `nextsql status --idp NAME` resolve
  the stored broker credential into the mapped native principal and existing
  `NSSC1.` password slot. `nextsqld`, the database formats, and NSQL wire format
  are unchanged.
- The versioned local credential/refresh-token store uses collision-resistant
  IdP+host names, random temporary files plus atomic rename, mode `0600` under a
  real mode-`0700` directory, 1 MiB file bounds, and fail-closed permission,
  symlink, and embedded-identity validation.
- The OIDC HTTP client refuses redirects so 307/308 cannot replay an
  authorization code, refresh token, or client secret; responses are capped at
  1 MiB and endpoints are parsed/validated. Wrong-state callbacks cannot
  consume the legitimate callback, and concurrent callbacks publish once.
- `TestLoginEndToEnd` covers fake IdP → PKCE client → real broker → real
  `auth.TokenVerifier`; targeted functional and race suites plus adversarial
  redirect, callback, response-bound, and credential-store tests are green.
  Server `oidc` / `mtls+oidc` audit labeling landed in the subsequent increment
  recorded above.

### P25 Security 2.0 — authentication broker skeleton (2026-08-31)

- New package `internal/oidc`: pure, offline OpenID Connect primitives — compact
  JWS signature verification (RS/PS/ES 256/384/512; `none` and every MAC
  algorithm rejected), JWKS document parsing (RSA and EC keys), a JWKS cache
  with soft / hard TTL and rate-limited refresh that serves soft-stale keys
  through a brief IdP outage and fails closed past the hard TTL, ID-token
  validation (`iss` / `aud` / `azp` / `exp` / `iat` / `nbf` / `nonce`, skew
  ceiling 300 s), and a replay guard. Decoders are fuzzed
  (`FuzzParseJWKS`, `FuzzParseCompact`, `FuzzVerify`).
- New package `internal/authbroker` and command `cmd/nextsql-auth-broker`: the
  NextSQL **authentication broker**. `POST /v1/exchange` takes an OIDC ID token,
  validates it against the cached JWKS for the named IdP profile, maps the
  verified claims through the `NSIP` identity policy, and mints an ordinary
  `NSSC1.` short-lived credential signed by a private `NSTK` key. The broker is
  the only component that speaks OIDC — `nextsqld` keeps verifying `NSSC1.`
  credentials offline and unchanged; the broker's public issuing key simply
  goes in every server's `token_verify_keyset`.
- The minted credential's lifetime is `min(configured TTL, time until the IdP
  token expires)`; its audience is the deployment audience; its roles are the
  policy-mapped set, intersected with the principal's real RBAC membership when
  a membership feed is wired (a later increment) — otherwise the server's
  `ACL.AllowedScoped` still drops any role the principal does not hold.
- Every exchange attempt emits a structured audit record (issuer, hashed
  subject, matched rule id, principal, mapped and effective roles, outcome,
  minted token id, expiry). It never logs the ID token, the minted credential,
  or a client secret. Rejections return a generic message; the specific reason
  goes only to the audit log.
- `SIGHUP` reloads the identity policy and the issuing keyset with last
  known-good rollback.
- Integration test (`internal/authbroker`, fake IdP → broker → real
  `auth.TokenVerifier`): happy path, RBAC intersection, replay, `alg=none` /
  MAC alg / wrong `iss` / wrong `aud` / bad `nonce` / unmapped subject /
  unmapped groups / missing group claim, JWKS outage fails closed, credential
  TTL bounded by the IdP token expiry, reload keeps last known-good.
- After the subsequent interactive CLI and audit-labeling increments, client
  credentials, the embedded broker mode (`nextsqld --auth-broker-listen`), and
  optional JIT provisioning remain open.

### P25 Security 2.0 — `NSIP` identity-policy engine (2026-08-31)

- New `internal/auth/identitypolicy.go`: the offline **`NSIP` (NextSQL Identity
  Policy)** engine an external-identity broker will consult to turn verified IdP
  claims into a native principal and a no-escalation role set. Pure — no
  network, no dependency on the SQL engine.
- `PolicyDoc` is a versioned, magic-tagged, fully corruption-validated binary
  document written mode `0600` with an atomic rename; `IdentityPolicy.Reload`
  keeps the last known-good policy when a new file fails to parse, validate, or
  compile (same on-disk contract as `NSTK`/`NSTR`).
- `IdentityPolicy.Map` applies ordered, issuer-scoped subject rules (claim
  `equals`/`prefix`/`suffix`/anchored-RE2 conditions, ANDed) and a bounded pure
  transform pipeline (`lower`, `before`, `after`, `replace`) to derive the
  principal; the result must be a valid `[a-z0-9._-]{1,128}` login or the
  mapping fails closed. Groups map to roles by literal match or anchored RE2
  with `${n}` capture templates; the union is capped at 16.
- `IdentityPolicy.Authorize` runs `Map` then intersects the mapped roles with
  the principal's real RBAC membership (`IntersectRoles`); an empty intersection
  is a denial. This is the no-escalation guarantee — an external identity can
  only narrow what a native grant already allows.
- Every unmatched, ambiguous, or over-cap input is a typed `Forbidden` error.
  Tests: `internal/auth/identitypolicy_test.go`, `FuzzDecodeIdentityPolicy`,
  `FuzzMapClaims`.
- Not wired to anything yet: no broker, no `nextsqld` path, no audit change, no
  config key. `docs/security.md`'s P25 audit records the OIDC end-to-end path as
  still not implemented; the three mapping-policy rows move to
  `implemented: partial` / `tested: yes` for the engine.

### P25 Security 2.0 — external IdP (OIDC) design accepted (2026-08-31)

- Accepted design `docs/design-oidc-external-idp.md`. No code ships; this is a
  design-only increment and `docs/security.md`'s P25 audit still records OIDC
  as not implemented / not tested.
- Chosen architecture: a standalone or embedded **authentication broker** runs
  the OIDC Authorization Code + PKCE (interactive) or client-credentials
  (workload) flow, validates the IdP token against a soft/hard-TTL cached JWKS
  (`iss` / `aud` / `alg` allowlist rejecting `none` and MAC algs / `exp` /
  `nonce` / replay), and mints an existing `NSSC1.` short-lived credential. The
  `nextsqld` SQL authentication path is unchanged and never contacts the IdP;
  the broker's issuing key is just another `NSTK` key in `token_verify_keyset`.
- `NSIP` (NextSQL Identity Policy): versioned, deployment-encrypted, `SIGHUP`
  last-known-good. Issuer-scoped subject→principal rules and group→role
  mappings; the mapped role set is intersected with the principal's real RBAC
  membership (mapped-but-not-member dropped, empty ⇒ deny), so an external
  identity can only narrow a native grant and never bypass RBAC —
  `ACL.AllowedScoped` is enforced on every statement exactly as for
  hand-minted tokens.
- Broker-issued credential logins will audit as `identity_source` `oidc` /
  `mtls+oidc`, derived from the verifying key rather than attacker-controlled
  bytes. Direct server-side JWT verification (`NSIDP1.`) is documented as the
  rejected alternative.

### P25 Security 2.0 — signed short-lived credentials (2026-08-31)

- Clients may authenticate with a signed short-lived credential presented **in
  place of the password** (same `Auth` password field, same native principal,
  same RBAC). Wire form `NSSC1.` + base64url of Ed25519-signed claims
  (`internal/auth`). No new frame or auth method.
- Claims carry a signing-key id, a random token id, issued-at / not-before /
  expires-at, the native principal, and optional audience, database, realm, and
  role scopes. `TokenVerifier` fails closed on a bad/retired key, invalid
  signature, the validity window (60 s skew), a lifetime over the verifier
  maximum (default 24 h, hard ceiling 30 d), an audience mismatch against
  `token_audience` (a configured audience also rejects an unscoped credential),
  a database-scope mismatch, or revocation.
- The protocol server additionally requires the credential principal to equal
  the Hello user and to be a known native user, narrows the session to the
  credential's role scope (`ACL.AllowedScoped`, with a no-escalation guard —
  the principal must already hold every listed role), and closes the session at
  the credential's expiry.
- `token_verify_keyset=FILE` enables verification; optional
  `token_revocations=FILE` and `token_audience=STRING`. The keyset (`NSTK` v1)
  is a rotatable set of Ed25519 keys with `current`/`retired` flags; servers
  keep a verify-only copy. The revocation set (`NSTR` v1) holds revoked token
  ids (pruned at their own expiry) and per-principal "issued at or before"
  cutoffs. `SIGHUP` atomically reloads both, last known-good on failure.
- New `nextsql token` subcommands: `keygen`, `rotate`, `retire`, `list-keys`,
  `export-public`, `mint`, `revoke`, `verify`. Auth audit records
  `identity_source` `token` / `mtls+token`.
- Official drivers are unchanged — the credential goes wherever the password
  would. Non-Go driver convenience helpers are a documented follow-on.

No persistent database or NSQL wire-format change is introduced. `go build
./...`, targeted functional/race tests (`internal/auth`, `internal/security`,
`internal/protocol`, `internal/executor`, `internal/config`,
`tests/integration`), and 8 s `FuzzDecodeTokenClaims` / `FuzzDecodeTokenKeys`
are green.

### P25 Security 2.0 — mTLS identity, rotation, and revocation (2026-08-31)

- `nextsqld --tls-client-ca` / `tls_client_ca` enables TLS 1.3 mutual
  authentication with `RequireAndVerifyClientCert` against an explicit client
  CA bundle. Missing, untrusted, expired, or wrong-EKU client certificates fail
  during the standard `crypto/x509` handshake.
- The verified leaf must carry exactly one NextSQL URI SAN
  `nextsql://service/<principal>` matching the native login user. Native
  password authentication and RBAC still run; the certificate does not grant
  privileges by itself.
- Auth audit events now record `identity_source` (`native`, `mtls`, or
  `mtls+native`). The CLI accepts paired `--tls-client-cert` /
  `--tls-client-key` flags and matching environment variables.
- `nextsqld` atomically reloads its server certificate/key, client trust bundle,
  and optional `--tls-client-crl` PEM bundle on `SIGHUP`. Invalid reloads retain
  the last known-good snapshot. Trust rotation supports an explicit old+new CA
  overlap window.
- CRLs must be current, signed by an authority in the client bundle, and cover
  every non-root certificate in the verified chain. Missing coverage, stale or
  invalid CRLs, and revoked serials fail the handshake. Successful mTLS reloads
  terminate all accepted connections, including pre-authentication handshakes,
  so clients reauthenticate. OCSP is not implemented.
- The P25 audit in `docs/security.md` explicitly separates designed,
  implemented, tested, and production-gated state. Short-lived credentials,
  IdP integration, field-level client encryption, password-hash evolution, and
  signed audit remain open.

No persistent or NSQL wire-format change is introduced.
Targeted functional/race tests, command builds, and serialized
`go test -p 1 ./... -count=1` are green.

### P24 Full-text Search 2.0 — exit gate (2026-08-31)

- **Bounded fuzzy vocabulary work.** Fuzzy and typo-tolerant SEARCH now fails
  closed after inspecting 4096 distinct vocabulary terms, for both inverted
  indexes and sequential-scan fallback. Matching expansions retain the tighter
  256-term / 8192-byte / 4096-work-unit limits. OSA Damerau-Levenshtein now
  uses three bounded rows instead of a full term-length-squared matrix.
- **Compatibility and quality gate.** A golden fixture pins Phase-10 BM25
  constants and adjacent phrase behavior. End-to-end quality fixtures cover
  exact BM25 ordering, phrases, prefix, fuzzy, typo tolerance, English
  stop/stem/synonym phrases, and French/German/Spanish analyzers.
- **Encrypted recovery gate.** An analyzer-aware kill/reopen test proves
  committed English postings and analyzer metadata recover, uncommitted
  posting changes do not survive, and distinctive terms do not appear as
  plaintext in database, WAL, or UNDO files.
- Tests: `TestP24BM25PhraseCompatibilityGolden`,
  `TestFuzzyWithinMatchesReference`, `TestFuzzyVocabularyBudgetFailClosed`,
  `TestP24SearchQualityFixtures`, `TestP24FuzzyVocabularyCap`, and
  `TestP24EncryptedCrashRecovery`. `go build ./...`, targeted functional and
  race suites, a 5-second `FuzzTokenize`, and serialized
  `go test -p 1 ./... -count=1` are green.

### P24 Full-text Search 2.0 — faceting (2026-08-31)

- **Faceting.** `SELECT * FROM t SEARCH col FOR '…' FACET cat [, year …]`
  returns independent histograms over the full SEARCH match set (query-time
  only, no catalog or posting-format bump). Output is `facet STRING` (column
  name), `value STRING` (canonical display), `count DECIMAL`. Each facet
  column is its own histogram, not a `GROUP BY` cross-product. `LIMIT n` is
  per-facet top-N; `NULL` is skipped; buckets are count descending then value
  ascending. Allowed types: `STRING`, `TEXT`, `DECIMAL`, `BOOL`, `UUID`,
  `TIMESTAMPTZ`. Requires `SELECT *` and `SEARCH`. Fails closed with `JOIN`,
  `GROUP BY` / `HAVING`, `DISTINCT`, `ORDER BY`, `OFFSET`, `NEAREST`, duplicate
  columns, more than eight columns, JSON/vector/geo types, and more than 1024
  distinct values on one facet column. `FACET` is not a reserved keyword.
  Field weighting, prefix, fuzzy, typo, phrase, analyzer, and
  `HIGHLIGHT`/`SNIPPET` matching is unchanged. `EXPLAIN` shows `Facet`.
- Tests: `TestFulltextFacet` (index + seq-scan + WHERE + LIMIT + NULL skip +
  typo + WEIGHT no-op + fail-closed), `TestFacetDistinctValueCap`,
  `TestBindFulltextFacet`, `TestSearchFacetPlan`. Parser fuzz seeds include
  `FACET`. `go build ./...` + `internal/sql/parser` / `internal/sql/binder` /
  `internal/sql/optimizer` / `internal/fulltext` / `internal/executor` `go test`
  + `-race` green; `FuzzTokenize` / `FuzzParse` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/optimizer.md`, `USAGE.md`,
  `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` /
  `limits.md` / `sql.md`.

### P24 Full-text Search 2.0 — field weighting (2026-08-31)

- **Field weighting.** Optional `WEIGHT <number>` after a `SEARCH` column
  (`SEARCH title WEIGHT 3, body FOR '…'`) scales that field's BM25 term
  frequency from existing position bands. Omitted weights are 1, so
  unweighted SEARCH keeps Phase 10 / multi-field BM25. Weights are
  query-time only (no catalog or posting-format bump) and apply to
  inverted-index SEARCH, seq-scan SEARCH, and hybrid RRF. A weight must be
  a finite numeric literal in `(0, 64]`; zero, negative, non-finite, and
  oversized values fail closed. `WEIGHT` is not a reserved keyword. Prefix,
  fuzzy, typo, phrase, analyzer, and `HIGHLIGHT`/`SNIPPET` matching is
  unchanged. `EXPLAIN` shows `weights=` when a non-default weight is used.
- Tests: `TestWeightedTF`, `TestQueryScoreWeighted`, `TestCheckFieldWeight`,
  `TestBindFulltextMultiField` (weights), `TestSearchChoosesMultiFieldFulltextIndex`
  (`weights=3,1`), `TestFulltextFieldWeight` (index + seq-scan + WEIGHT 1
  no-op + HIGHLIGHT + no cross-field phrase + fail-closed 0/65). Parser
  fuzz seeds include weighted SEARCH. `go build ./...` + `internal/fulltext`
  / `internal/sql/parser` / `internal/sql/binder` / `internal/sql/optimizer`
  / `internal/executor` `go test` + `-race` green; `FuzzTokenize` /
  `FuzzParse` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/optimizer.md`, `USAGE.md`,
  `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` /
  `limits.md` / `sql.md`.

### P24 Full-text Search 2.0 — multi-field search (2026-08-31)

- **Multi-field search.** `CREATE FULLTEXT INDEX` and `SEARCH` accept one to
  eight `STRING`/`TEXT` columns (`CREATE FULLTEXT INDEX ix ON t (title, body)` /
  `SEARCH title, body FOR '…'`). A multi-column `SEARCH` uses an inverted index
  whose column list matches in the same order; a different subset or order
  seq-scans those columns. Fields are analyzed independently and scored as one
  BM25 document (term frequency and length summed). Phrase matching is
  per-field via reserved position bands (`i·(MaxDocTokens+2)`); `"database
  performance"` does not match across `title`/`body`. Duplicate columns, more
  than eight fields, and a combined token count above 100 000 fail closed.
  Prefix, fuzzy, typo, analyzer, and `HIGHLIGHT`/`SNIPPET` behaviour is
  unchanged (highlight remains per column). No catalog or posting-format bump:
  single-column indexes keep the Phase 10 posting layout.
- Tests: `TestAnalyzeFieldsPositions`, `TestBindFulltextMultiField`,
  `TestSearchChoosesMultiFieldFulltextIndex`, `TestFulltextMultiFieldSearch`
  (index + seq-scan + cross-field AND + in-field phrase + no cross-field
  phrase + subset/reorder fallback + HIGHLIGHT + prefix/fuzzy/typo + UPDATE).
  Parser fuzz seeds include multi-column CREATE/SEARCH. `go build ./...` +
  `internal/fulltext` / `internal/sql/parser` / `internal/sql/binder` /
  `internal/sql/optimizer` / `internal/executor` / `internal/catalog` /
  `internal/xport` `go test` + `-race` green; `FuzzTokenize` / `FuzzParse` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/optimizer.md`, `USAGE.md`,
  `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` /
  `limits.md` / `sql.md`.

### P24 Full-text Search 2.0 — highlight/snippet generation (2026-08-31)

- **Highlight/snippet generation.** `HIGHLIGHT(col)` and `SNIPPET(col)` are
  SELECT-list functions that require `SEARCH` (no catalog or posting-format
  bump). They wrap original document tokens whose analyzed form participates
  in the SEARCH query (exact, synonym, prefix, fuzzy, and typo), using the
  SEARCH column's analyzer, so `runs` marks `running` and `car` marks
  `automobile`. Default markers are `<mark>` and `</mark>`.
  `HIGHLIGHT(col, pre, post)` and `SNIPPET(col, width [, pre, post])` override
  markers (max 32 runes, no NUL). `HIGHLIGHT` returns the full field.
  `SNIPPET` returns a window of `width` Unicode code points (default 160,
  range 16–4096) around the densest match cluster, with `…` on a truncated
  edge. Both fail closed outside the SELECT list of a SEARCH query, in
  `WHERE` / `JOIN` / `GROUP BY` / `HAVING` / DML, and on oversize markers or
  snippet width. Seq-scan SEARCH highlights the same way. Default
  BM25/phrase/prefix/fuzzy/typo ranking is unchanged.
- Tests: `TestTokenizeSpans`, `TestHighlightExact`,
  `TestHighlightPreservesOriginalCase`, `TestHighlightPrefixFuzzyTypo`,
  `TestHighlightEnglishStemAndSynonym`, `TestHighlightEnglishDropsStops`,
  `TestHighlightCustomMarkersAndEmptyQuery`, `TestHighlightMarkerLimits`,
  `TestSnippetWindow`, `TestSnippetShortTextNoEllipsis`,
  `TestSnippetWidthBounds`, `TestHighlightsTermPrefixAndFuzzy`,
  `TestBindHighlightRequiresSearch`, `TestFulltextHighlight` (index +
  seq-scan + custom markers + prefix/fuzzy/typo + english stem/synonym +
  snippet + fail-closed without SEARCH / width). `go build ./...` +
  `internal/fulltext` / `internal/sql/binder` / `internal/sql/parser` /
  `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`,
  `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — typo tolerance (2026-08-31)

- **Typo tolerance.** Unadorned SEARCH tokens (no trailing `*` / `~`) stay
  exact when any analyzed alternative is in the searchable vocabulary, so
  Phase 10 BM25/phrase behaviour is unchanged (`cat` does not match `cot`
  when `cat` is indexed; `cats` does not match `cat`). When every alternative
  is absent, SEARCH rewrites the group as an AUTO-distance fuzzy group
  (query-time only, no catalog or posting-format bump): `databse` matches
  `database`. Typo AUTO is stricter than explicit `~` (0 for 1–4 runes, 1
  for 5–8, 2 for 9+). Prefix and explicit fuzzy groups are unchanged. Phrase
  slots follow the same rule (`"databse performance"`). BM25 scores the best
  matching term. Distinct typo-matched terms consume the existing
  query-expansion caps (256 terms / 8192 bytes / 4096 work units) and fail
  closed. Seq-scan `SEARCH` without an index uses the scanned corpus as the
  vocabulary. Analyzers still run first (stem/stop/synonym); typo fallback
  is on the analyzed term, so a typo of a synonym partner is not rewritten
  into that group.
- Tests: `TestApplyTypoToleranceMissing`,
  `TestApplyTypoTolerancePresentExactUnchanged`,
  `TestApplyTypoToleranceShortStaysExactMiss`, `TestAutoTypoDistance`,
  `TestApplyTypoTolerancePrefixAndFuzzyUnchanged`,
  `TestApplyTypoTolerancePhrase`, `TestApplyTypoToleranceSynonymGroup`,
  `TestApplyTypoToleranceNilPresent`, `TestQueryMatchesTypo`,
  `TestQueryScoreTypoBestMatch`, `TestFulltextTypoSearch` (index + seq-scan
  + short-token miss + english `catalag` + synonym skip + expansion cap).
  `go build ./...` + `internal/fulltext` / `internal/executor` `go test` +
  `-race` green; `FuzzTokenize` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`,
  `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — fuzzy matching (2026-08-31)

- **Fuzzy matching.** A trailing ASCII `~` on a SEARCH token is a fuzzy group
  (query-time only, no catalog or posting-format bump): `cat~` matches indexed
  terms within a bounded OSA Damerau-Levenshtein distance (insert, delete,
  substitute, adjacent transpose), so `cot` matches and `catalog` does not.
  Distance is AUTO from the token's rune length (0 for 1–2 runes, 1 for 3–5,
  2 for 6+) or explicit `~1` / `~2`. `~0` and `~3` or higher fail closed.
  Fuzzy tokens skip stemming, stop-word filtering, and synonym expansion
  (a misspelled word is not a complete token); French elision still applies
  (`l'homm~` is fuzzy `homm`). Matching terms are a disjunction at that
  position (AND with other groups); phrase slots accept a fuzzy term
  (`"databas~ performance"`). BM25 scores the best matching term in each
  fuzzy group. Distinct fuzzy-matched terms consume the existing
  query-expansion caps (256 terms / 8192 bytes / 4096 work units) and fail
  closed. Mixing `*` and `~` on one token fails closed. A leading or infix
  `~` is not fuzzy (`~cat` is exact `cat`). Default BM25/phrase/prefix
  behaviour for unadorned tokens is unchanged (`cat` does not match `cot`).
  Seq-scan `SEARCH` without an index uses the same fuzzy rules.
- Tests: `TestParseQueryFuzzy`, `TestParseQueryFuzzyPhrase`,
  `TestParseQueryFuzzySkipsStemAndSynonym`, `TestQueryMatchesFuzzy`,
  `TestFuzzyWithin`, `TestAutoFuzzyDistance`, `TestQueryScoreFuzzyBestMatch`,
  `TestFuzzyExpanderFailClosed`, `TestFulltextFuzzySearch` (index + seq-scan
  + english `run~` vs `running~` + synonym skip + expansion cap).
  `go build ./...` + `internal/fulltext` / `internal/executor` `go test` +
  `-race` green; `FuzzTokenize` 5 s clean with fuzzy seeds.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`,
  `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — prefix search (2026-08-31)

- **Prefix search.** A trailing ASCII `*` on a SEARCH token is a prefix group
  (query-time only, no catalog or posting-format bump): `cat*` matches indexed
  terms that start with `cat` (`cat`, `catalog`, …); exact `cat` still does
  not match `catalog`. Prefix tokens skip stemming, stop-word filtering, and
  synonym expansion (a truncated word is not a complete token); French elision
  still applies (`l'hom*` is prefix `hom`). Matching terms are a disjunction
  at that position (AND with other groups); phrase slots accept a prefix
  (`"data* performance"`). BM25 scores the best matching term in each prefix
  group. Distinct prefix-matched terms consume the existing query-expansion
  caps (256 terms / 8192 bytes / 4096 work units) and fail closed. A leading
  or infix `*` is not a wildcard (`*cat` is exact `cat`). Default BM25/phrase
  behaviour for unadorned tokens is unchanged. Seq-scan `SEARCH` without an
  index uses the same prefix rules.
- Tests: `TestParseQueryPrefix`, `TestParseQueryPrefixPhrase`,
  `TestParseQueryPrefixSkipsStemAndSynonym`, `TestQueryMatchesPrefix`,
  `TestPrefixExpanderFailClosed`, `TestPostingPrefixBounds`,
  `TestFulltextPrefixSearch` (index + seq-scan + english `run*` vs
  `running*` + expansion cap). `go build ./...` + `internal/fulltext` /
  `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean
  with prefix seeds.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`,
  `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — synonym dictionaries (2026-08-31)

- **English synonym dictionary v1.** `CREATE FULLTEXT INDEX … WITH
  (ANALYZER = 'english')` now writes analyzer revision 3: stop-word
  dictionary v1, Porter2, then synonym dictionary v1 (15 tight bidirectional
  groups: `car`/`automobile`, `database`/`db`, `buy`/`purchase`, …). Expansion
  is query-time only — index terms stay 1:1 like english v2 — and alternatives
  share the query token's position so they are a disjunction (AND across
  tokens, OR within a token). Phrase slots accept any alternative, so
  `"red car"` matches `red automobile`. BM25 scores the best alternative in
  each group (no double-count). Extra terms consume the existing query-
  expansion caps (256 terms / 8192 bytes / 4096 work units, max 8 extras per
  token). english v1 (stem only) and v2 (stem+stops) still decode and do not
  expand. Default `simple` is unchanged. Unknown names/revisions fail closed.
- Tests: `TestEnglishSynonymV1Membership`, `TestAnalyzeEnglishNoIndexSynonyms`,
  `TestParseQueryEnglishSynonyms`, `TestParseQueryEnglishSynonymPhrase`,
  `TestQueryMatchesSynonymDisjunction`, `TestEnglishSynonymWorkCounts`,
  `TestLookupAnalyzer` (writes v3), `TestTableEncodeFulltextAnalyzerV9` (v3),
  binder ANALYZER writes v3, `TestFulltextEnglishSynonyms`. `go build ./...` +
  `internal/fulltext` / `internal/catalog` / `internal/sql/parser` /
  `internal/sql/binder` / `internal/sql/optimizer` / `internal/upgrade`
  `go test` + `-race` green; `internal/executor` `TestFulltext*` green +
  `-race`; `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`,
  `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — versioned language analyzers (2026-08-31)

- **French, German, and Spanish analyzers.** `CREATE FULLTEXT INDEX … WITH
  (ANALYZER = 'french' | 'german' | 'spanish')` writes analyzer revision 1 on
  existing `NSCT` v9 (id `2`/`3`/`4`, no format bump): the published Snowball
  3.x stemmer plus that language's Snowball stop-word dictionary v1, applied
  identically at index time and query time. Remaining terms re-pack to
  consecutive positions. French elides `l'` / `qu'` / … before the stop list
  so `l'homme` matches `homme`. Default `simple` and `english` (v1 stem-only,
  v2 stem+stops) are unchanged. Unknown names and unknown catalog revisions
  fail closed. `EXPLAIN` shows `analyzer=french` (etc.).
- Tests: `TestStemFrenchFixtures`, `TestStemGermanFixtures`,
  `TestStemSpanishFixtures`, `TestAnalyzeFrenchStopsThenStems`,
  `TestAnalyzeGermanStopsThenStems`, `TestAnalyzeSpanishStopsThenStems`,
  `TestParseQueryFrenchElision`, `TestFrenchStopV1Membership` (153),
  `TestGermanStopV1Membership` (231), `TestSpanishStopV1Membership` (308),
  `TestTableEncodeFulltextAnalyzerV9` (fr/de/es), binder ANALYZER cases,
  `TestFulltextLanguageAnalyzers`. `go build ./...` + `internal/fulltext` /
  `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` /
  `internal/sql/optimizer` / `internal/upgrade` / `internal/xport` `go test`
  + `-race` green; `internal/executor` `TestFulltext*` green + `-race`;
  `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`,
  `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — stop-word dictionaries (2026-08-31)

- **English stop-word dictionary v1.** `CREATE FULLTEXT INDEX … WITH
  (ANALYZER = 'english')` now writes analyzer revision 2: stop-word
  dictionary v1 (classic 33-term Lucene EnglishAnalyzer / Snowball-small
  set) is applied before Porter2, identically at index time and query time.
  Remaining terms are re-packed to consecutive positions so BM25 length and
  phrase matching stay aligned (`"the cat sat"` matches `"cat sat"`). Default
  `simple` still has no stop list (`the` is searchable). english v1 (stem
  only) catalogs still decode and search with that pipeline. A SEARCH of only
  stop words returns no rows. Dropped stop words still consume query-expansion
  work units.
- Tests: `TestEnglishStopV1Membership`, `TestAnalyzeEnglishDropsStops`,
  `TestAnalyzeEnglishStopsThenStems`, `TestParseQueryEnglishDropsStops`,
  `TestParseQueryEnglishPhraseDropsStops`, `TestEnglishStopWorkCounts`,
  `TestTableEncodeFulltextAnalyzerV9` (v1 + v2), binder ANALYZER writes v2,
  `TestFulltextEnglishStopWords`. `go build ./...` + `internal/fulltext` /
  `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` /
  `internal/sql/optimizer` / `internal/upgrade` `go test` + `-race` green;
  `internal/executor` `TestFulltext*` green + `-race`; `FuzzTokenize` /
  `FuzzDecodePartitionedTable` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`,
  `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `fulltext.md` / `limits.md`.

### P24 Full-text Search 2.0 — stemming (2026-08-31)

- **English stemming + versioned analyzer metadata.** `CREATE FULLTEXT INDEX
  … WITH (ANALYZER = 'simple' | 'english')`. Default `simple` is the Phase 10
  tokenizer (no stemming), so existing BM25/phrase behaviour is unchanged.
  `english` is Snowball English (Porter2) revision 1, applied identically at
  index time and query time. Analyzer id + revision are stored per index on
  `NSCT` v9 (v1–v8 still decode; missing trailer is simple). Unknown analyzer
  names and unknown catalog revisions fail closed.
- **Query-expansion caps.** SEARCH expansion is fail-closed at 256 terms,
  8192 bytes, and 4096 work units (stemming is 1:1; synonym dictionaries will
  reuse the same budget).
- Tests: `TestStemEnglishFixtures`, `TestAnalyzeEnglishStems`,
  `TestParseQueryEnglishPhrase`, `TestQueryExpansionCapsFailClosed`,
  `TestTableEncodeFulltextAnalyzerV9`, `TestPartitionCatalogV5ReadsNextID`
  (NextID is read for every v5+ descriptor, not only the current write
  version), parser/binder ANALYZER cases, `TestFulltextEnglishStemming`.
  `go build ./...` + touched-package `go test` + `-race` green; `FuzzTokenize`
  / `FuzzDecodePartitionedTable` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`,
  `docs/ops.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `fulltext.md` /
  `limits.md`.

### Current release gate

P0–P27 are complete. **P28 Professional Installer + NextSQL Manager** is the
current release gate. The setup/lifecycle automation and all nine Manager MVP
slices are complete; GUI-installer M1 is implemented and targeted-tested.
Packaging the GUI entry point, full platform execution (especially `.rpm` and
Windows), silent-install coverage, the remaining encryption/TLS wizard scope,
and the accessibility baseline remain open. See `TODO.md` for the live gate.

P22 exit gate, all satisfied:

- three read-consistency modes — `STRONG` (linearizable behind a
  `raft.VerifyLeader` quorum read barrier), `BOUNDED` (within `MAX STALENESS`),
  `STALE` (unbounded) — all consistent committed prefixes, `STALE`/`BOUNDED`
  never mislabelled `STRONG`;
- replica lag + follower health via `system.replica_health` and `NodeStatus`;
- follower-read routing in the server and every official driver (Go, Node.js,
  Bun, Deno, PHP, Python, and Ruby);
- read-scaling benchmark `nextsql-bench --readscale`;
- linearizability/consistency sign-off (`docs/ha.md` "Consistency model and
  sign-off") and failover session-guarantee test.

P16 exit gate, all satisfied:

- corrected 1M-vector HNSW v10: p95 **8.061 ms**, recall@10 **1.000**,
  recall@100 **0.998**;
- 10M DELETE published (**25 ms**), crash-during-merge recovers `Check()`-clean;
- 100M analytics `< 60 s`; 10M INSERT/UPDATE published;
- security gate signed off; no unresolved correctness regressions.

The terminal randomized 100M-operation B+Tree invariant soak is a deferred
standalone measurement, not a release gate (same disposition as P18). Structural
correctness is covered by `TestRandomizedDeleteMerges`, `TestCrashDuringMerge`,
`TestBulkDeleteSoak`, the 10M DELETE run, and the soak at every scale reached
(v8: 44M clean operations).

### P23 Vector Engine 2.0 — complete (2026-08-31)

- **Production-gating sign-off.** Dated review in `docs/vector.md`
  "Production-gating sign-off (Phase 23)": `VECTOR<F16,N>` / `VECTOR<I8,N>` /
  `BITVECTOR<N>` and the quantised HNSW index are production-gated; IVF /
  IVF-PQ / sparse retrieval / dense+sparse+BM25 fusion are production-gated
  ANN paths. Official `--vecquant` 2026-08-31 reference run republished with
  p50/p95/p99, QPS, and resident heap alongside recall@10/@100, index/db size,
  and build time (encryption + WAL + fsync on). `TestPortableProductionPath`
  now covers `internal/float16` / `internal/int8vec` / `internal/bitvec` as
  well as `internal/vector`. Documented follow-ons (not gate items): a
  `BITVECTOR`/Hamming `--vecquant` row, a process-local IVF-PQ cache, a
  re-rank-free quantised HNSW mode, IVF/IVF-PQ/SPARSE on partitioned tables,
  SIMD after profiling. Tests: `go build ./...` + `internal/vector` /
  `internal/float16` / `internal/int8vec` / `internal/bitvec` /
  `internal/bench` / `internal/executor` (vector suites) `go test` + `-race`
  green. Docs: `docs/vector.md`, `docs/ops.md`, `USAGE.md`, `ROADMAP.md`,
  web `vectors.md`.

- **Official `--vecquant` sparse size/latency/recall row.**
  `nextsql-bench --vecquant` measures a `SPARSE` configuration on a
  high-dimension, low-nnz corpus independent of `--vecquant-dim`
  (`SPARSEVECTOR<N>` + `USING SPARSE`; `--vecquant-sparse-dim` default 4096,
  `--vecquant-sparse-nnz` default 24). Reports NSSV raw payload, index-build
  page delta, database size, build time, resident heap, and `NEAREST`
  p50/p95/p99 + recall@10/@100 vs exact-cosine `SparseFlat`. Reference
  (linux/amd64, 12 vCPU, encryption + WAL + fsync on; 2000 × 4096-d nnz=24,
  64 queries): raw payload 282 KiB, index 1.0 MiB, database 2.1 MiB, build
  1.17 s, p50 428 µs, recall@10 **1.000**, recall@100 **1.000**. Tests:
  `TestVectorQuantBench` (8 reports). Docs: `docs/vector.md` "Size / recall
  comparison", `docs/ops.md`, `USAGE.md`, web `vectors.md`.

- **Dense + sparse + BM25 fusion.** A `SELECT` may name two `NEAREST`
  clauses — one dense `VECTOR` column and one `SPARSEVECTOR` column — with
  optional `SEARCH`. The optimizer unions candidates from each retriever
  (HNSW/IVF/IVF-PQ, the sparse inverted index, and BM25) and reciprocal-rank
  fuses the lists (`k = 60`). A document contributes to a channel only when
  that retriever scored it. `EXPLAIN` shows `Rerank bm25+vector+sparse fusion`
  (or `vector+sparse fusion` without `SEARCH`). At most two `NEAREST`
  clauses; the pair must be one dense vector and one sparse vector
  (`BITVECTOR` is rejected). Existing `SEARCH` + single `NEAREST` hybrid
  plans are unchanged. Tests: `TestDenseSparseBM25Fusion` (each single
  channel uniquely owns one row; fused `LIMIT 3` returns all three),
  `TestDenseSparseBM25FusionPlan`, parser (third `NEAREST` rejected) and
  binder (same-column / two-dense rejected) cases. Docs: `docs/vector.md`,
  `docs/optimizer.md`, `docs/sql.md`, `USAGE.md`, web `hybrid.md` /
  `vectors.md` / `sql.md`. The official `--vecquant` sparse size/latency
  row landed in the following increment.

- **`SPARSEVECTOR<N>` SQL surface + `USING SPARSE`.** `SPARSEVECTOR<N>` is a
  distinct top-level type (`N` is the ambient dimension, 1…65535; catalog
  `VecElem = 5`). Runtime values stay sparse (index/value pairs, never widened
  to a dense `float32` array). Dense vector literals such as `(1, 0, 0.5, 0)`
  coerce by dropping zeros. Payload store uses `NSSV` v1. `CREATE VECTOR INDEX
  … USING SPARSE` (no `WITH` options) builds an inverted index over a detached
  encrypted index tree (`sqlSparse` implements `vector.SparseStore`: `NSSM`
  header + one `NSSP` posting list per dimension). Binder: requires a
  `SPARSEVECTOR` column; rejected with `QUANTIZATION`, on dense/`BITVECTOR`
  columns, on partitioned tables, and with `USING HNSW`/`IVF`/`IVFPQ` on a
  sparse column. Default `NEAREST` metric is `COSINE`; `INNER_PRODUCT` is
  accepted; `L2`/`HAMMING` are rejected. Executor: `buildSparseIndex` (CREATE
  + `REBUILD INDEX`), `maintainSparseIndex` on INSERT/UPDATE/DELETE (uses
  in-memory old/new coordinates because DELETE drops the payload first),
  `nearestSparseIndex` = `SearchSparse` with residual over-fetch ×4.
  `EXPLAIN` labels `sparse`; `nextsql export` emits `USING SPARSE`. Wire
  format flag `0x02` carries nnz + `(u32 index, f32 value)` pairs (Go protocol
  via `EncodeScalar`; JS/Node/PHP decode). Tests: `TestSparseVectorIndex`
  (HNSW/IVF/L2 rejected, exact NEAREST, INSERT/UPDATE/DELETE, no `NSSV`/`NSSM`/
  `NSSP` plaintext, restart, `REBUILD INDEX`), parser/binder cases, catalog
  fuzz seed. `go build ./...` + touched-package `go test` + `-race` green;
  `FuzzParse` + `FuzzDecodePartitionedTable` 10 s clean. Docs: `docs/vector.md`,
  `docs/sql.md`, `USAGE.md`, web `vectors.md` / `limits.md` / `sql.md`.
  Dense+sparse+BM25 fusion landed in the increment above; the official
  `--vecquant` sparse size/latency/recall row landed 2026-08-31.

- **Sparse retrieval core.** Portable inverted index over sparse vectors in
  `internal/vector/sparse.go`. A sparse vector is a strictly-ascending list of
  dimension indices plus parallel non-zero `float32` weights (`MaxSparseDim`
  `2^24`, `MaxSparseNNZ` `2^16`). `NewSparseVec` / `CheckSparse` reject zeros,
  duplicates, non-finite values, and out-of-range indices; `SparseDot` is a
  merge-join; `SparseDistance` is `−dot` (`INNER_PRODUCT`) or `1 − cosine`
  (`COSINE`). Retrieval walks one posting list per query coordinate and
  accumulates the exact inner product (`SearchSparse`); `COSINE` re-ranks the
  top `4·k` candidates against full-precision payloads when the store can
  supply them. Versioned encodings: `NSSV` v1 (dimension, nnz, delta-varint
  indices, little-endian `f32` values — overflowing deltas fail closed before
  wrap), `NSSM` v1 21-byte meta (`MaxDim`, metric, count; `COSINE` /
  `INNER_PRODUCT` only), `NSSP` v1 front-coded posting lists (varint count,
  shared-prefix + suffix primary key + `f32` weight; 4096-byte key bound before
  `make`). `SparseStore` + `SparseMem` + `AddSparse` / `RemoveSparse` /
  `PersistSparse` / `LoadSparseMem`; index keys `0x00` meta / `0x01`+`u32`
  posting. Tests: `TestSparseVecRoundTrip` / `TestNewSparseVecRejects` /
  `TestSparseDot` / `TestSparseMetaRoundTrip` / `TestSparseListRoundTrip` /
  `TestSparseSearchRecall` (inner-product inverted-index recall@10 1.0; COSINE
  rerank-all 1.0; COSINE `4·k` ≥ 0.90 on 400×4096-d nnz=24) /
  `TestSparseAddRemove` / `TestSparsePersistLoad` / `TestSparseKeyRoundTrip`;
  `FuzzDecodeSparse` / `FuzzDecodeSparseList` / `FuzzDecodeSparseMeta` (15 / 15
  / 10 s clean). `go build ./...` + `internal/vector` `go test` + `-race` green.
  Docs: `docs/vector.md` ("Sparse retrieval" + storage table), `ROADMAP.md`,
  `USAGE.md`, web `vectors.md`. SQL (`SPARSEVECTOR<N>` + `USING SPARSE`) landed
  in the increment above.

- **IVF-PQ vector index SQL surface + lifecycle wiring.** `CREATE VECTOR INDEX …
  USING IVFPQ WITH (LISTS = n, SUBSPACES = M [, PROBES = m])`. Parser
  (`ast.CreateIndex.IVFSubspaces`; `USING IVFPQ` shares the IVF `WITH` loop and
  adds `SUBSPACES`), binder (`catalog.VecMethodIVFPQ`; `SUBSPACES` required, ≤ 128,
  must divide the vector dimension; `LISTS` required ≤ 65 536, `PROBES` ≤ `LISTS`;
  rejected with `QUANTIZATION`, on a `BITVECTOR` column, and on partitioned
  tables), catalog table descriptor format **v8** (one `SUBSPACES` `u32` per index
  after the v7 method + IVF `LISTS` / `PROBES`; `DecodeTable` accepts v1..v8;
  `internal/upgrade` `FamilyCatalog` window 1..8), and the executor
  (`internal/executor/ivfpqstore.go`: `sqlIVFPQ` implements `vector.IVFPQStore`
  over the detached encrypted index tree — coarse centroids grouped like IVF, the
  codebook split into fixed-size chunks under an `IVPCG` header since it never
  fits one leaf record, one front-coded `NSPL` posting list per centroid;
  `buildIVFPQIndex` trains over a ≤ 50 000-vector heap sample and is shared by
  `CREATE` and `REBUILD INDEX`; `maintainIVFPQIndex` on `INSERT` / `UPDATE` /
  `DELETE`; `nearestIVFPQIndex` probes, ADC-scores, and re-ranks the top
  candidates exactly against the payload store). `EXPLAIN` labels the plan
  `ivfpq`; `nextsql export` emits `USING IVFPQ WITH (…)`. Crash-recovery, backup,
  PITR, and Raft are inherited from the encrypted index-tree WAL path. No
  process-local cached copy yet — a committed `NEAREST` reloads the quantiser per
  query (a documented follow-on, matching plain IVF's first increment). A new
  `F32/ivfpq` row in `nextsql-bench --vecquant` (LISTS / PROBES / SUBSPACES, index
  / db size, build time, `NEAREST` latency, recall@10/@100). Tests:
  `internal/executor` `TestIVFPQVectorIndex` (SUBSPACES required + must divide
  dim, exact-rerank search, INSERT/UPDATE/DELETE maintenance, restart, `REBUILD
  INDEX`, no `NSVV` / `NSPQ` / `NSPC` / `NSPL` / `NSIC` plaintext); parser +
  binder cases; `catalog` v8 round-trip + trailer fix + `FuzzDecodePartitionedTable`
  IVF-PQ seed; `internal/upgrade` window test; `internal/bench`
  `TestVectorQuantBench` (7 reports). `go build ./...` + touched-package `go test`
  + `-race` green; `FuzzDecodePartitionedTable` / `FuzzParse` 15 s clean. Docs:
  `docs/vector.md` ("IVF-PQ (product quantisation)" + storage table + `--vecquant`
  numbers + catalog v8), `docs/sql.md`, `docs/storage-format.md`, `docs/ops.md`,
  `USAGE.md`, `ROADMAP.md`, web `vectors.md` / `limits.md`.

- **IVF-PQ index core.** Portable in-memory inverted-file index with product
  quantisation in `internal/vector` (`ivfpq.go`): `TrainIVFPQ` trains an
  `NLIST`-centroid coarse quantiser then, over the residuals `v − centroid`, an
  `M`-subspace product-quantisation codebook of up to 256 sub-centroids each
  (deterministic k-means). `AddIVFPQ` / `RemoveIVFPQ` store an `M`-byte code per
  vector in its centroid's posting list; `SearchIVFPQ` ranks the coarse
  centroids, scores each probed list with asymmetric distance computation (a
  per-subspace query-to-sub-centroid table summed over the code bytes), and
  re-ranks the top candidates exactly when the store can supply the
  full-precision payloads (recall then tracks an unquantised IVF; ADC-only
  otherwise). `COSINE` (unit-normalised first) and `L2` only; `INNER_PRODUCT`
  rejected. Versioned encodings: `NSPQ` meta (32 bytes), `NSPC` codebook
  (contiguous `f32`), `NSPL` posting list (front-coded primary keys, `NSIL`
  scheme, plus `M` code bytes per entry); every decoder bounds its varints
  before allocating. `IVFPQStore` interface + `IVFPQMem`; `PQCodebook` with
  `EncodePQCodebook` / `DecodePQCodebook`. `internal/vector` `TestIVFPQ*`
  (meta/codebook/list round trips, probe-all + exact re-rank recall@10 ≈ 1.0,
  ADC-only recall@10 ≈ 0.70 on 700×32-d M=8, add/remove, persist+reload,
  determinism) + `FuzzDecodePQList` / `FuzzDecodePQCodebook` /
  `FuzzDecodeIVFPQMeta`. `go build ./...` + `internal/vector` `go test` + `-race`
  + fuzz green. The SQL surface (`CREATE VECTOR INDEX … USING IVFPQ`) and
  executor lifecycle wiring are a following increment. Docs: `docs/vector.md`
  ("IVF-PQ (product quantisation)" + storage table), `ROADMAP.md`, `USAGE.md`,
  web `vectors.md`.

- **Process-local IVF quantiser cache.** A committed `NEAREST` through an IVF
  index no longer reloads and decrypts the centroids, posting lists, and
  full-precision vectors from the index tree on every query: `ivfSearchStore`
  serves a shared in-memory `lockedIVF` copy, built once at commit time
  (`buildIVFIndex` hands its trained `IVFMem` to `s.pendingIVF`) or lazily on
  first search, and installed under the same generation counter and lock as the
  HNSW `lockedMem` cache. It is evicted on any mutation (`maintainIVFIndex` marks
  `s.dirtyIVF`), `REBUILD INDEX`, `DROP INDEX`, table drop/rename, or a
  replicated apply — all of which already funnel through `dropHNSW` /
  `dropAllHNSW`, now extended to the IVF map. A transaction that has modified the
  index still reads its own uncommitted state directly from the index tree.
  `internal/executor` `TestIVFProcessLocalCache`; `TestIVFVectorIndex` /
  `TestIVFCentroidGrouping` unchanged. `go build ./...` + `internal/executor`
  (vector suites) / `internal/vector` / `internal/bench` `go test` + `-race`
  green. Docs: `docs/vector.md` ("IVF index"), `ROADMAP.md`, `USAGE.md`, web
  `vectors.md`.
- **IVF row in `nextsql-bench --vecquant`** plus grouped centroid storage. The
  quantised-vector benchmark now builds an IVF index (`LISTS = 2·√rows`,
  `PROBES = LISTS/4`) over the `F32` column as a sixth configuration and reports
  the same index/db size, build time, `NEAREST` latency, and recall@10/@100 as
  the HNSW rows (reference 2000 × 128-d run: index 112 KiB vs 610–707 KiB for
  HNSW, build 0.25 s vs ~2.1 s, recall@10 0.62 at a 25 %-of-`LISTS` probe ratio
  on synthetic uniform vectors). Surfacing this hit the B+Tree leaf-record
  ceiling — a wide centroid set (many `LISTS`, high dimension) exceeds ~½ a
  logical page — so `sqlIVF.SaveCentroids` / `LoadCentroids` now split the
  centroid set across several `IVFCG`-indexed group records (legacy single-record
  `NSIC` blocks still load). The binder now also rejects `USING IVF` on a
  very-high-dimensional column (one centroid past the leaf-record ceiling, ~`N >
  2000` for `VECTOR<F32,N>`) instead of failing mid-build. `internal/bench/vecquant.go`,
  `TestVectorQuantBench`, `internal/executor` `TestIVFCentroidGrouping`,
  `internal/sql/binder` dimension-guard case; `docs/vector.md` "Size / recall
  comparison" + IVF notes + storage table, `ROADMAP.md`, `USAGE.md`, web
  `vectors.md`.

- **IVF vector index SQL surface.** `CREATE VECTOR INDEX … USING IVF WITH
  (LISTS = n [, PROBES = m])` — parser (`ast.CreateIndex.IVFLists` / `IVFProbes`),
  binder (`LISTS` required and ≤ 65 536, `PROBES` ≤ `LISTS`; rejected with
  `QUANTIZATION`, on a `BITVECTOR` column, or on a partitioned table), and
  catalog table descriptor format **v7** (`Index.VecMethod` byte + IVF
  `LISTS` / `PROBES` `u32` per index; `internal/upgrade` window 1..7). The
  executor binds the IVF store to the index's own detached encrypted B+Tree
  (`sqlIVF` over `vector.IVFStore`): `CREATE` / `REBUILD INDEX` train a coarse
  quantiser over a deterministic ≤ 50 000-vector heap sample and write the
  centroids, front-coded posting lists, and `NSIV` header in one transaction;
  `INSERT` / `UPDATE` / `DELETE` move a row's primary key between posting lists;
  `NEAREST` ranks the centroids, probes the `PROBES` nearest lists, and scores
  their vectors exactly (a differing `USING` metric falls back to exact flat).
  Crash-recovery, backup, PITR, and Raft are inherited from the encrypted
  index-tree WAL path. `EXPLAIN` labels the plan `ivf`; `nextsql export` emits
  `USING IVF WITH (…)`. `internal/executor` `TestIVFVectorIndex`, parser/binder
  cases, `catalog` v7 round-trip + `FuzzDecodePartitionedTable` seed;
  `docs/vector.md` "IVF index", `docs/sql.md`, `docs/storage-format.md`,
  `docs/ops.md`, `USAGE.md`, web `vectors.md`.

- **IVF index core.** Portable in-memory inverted-file coarse-quantiser index in
  `internal/vector`: `TrainIVF` (deterministic k-means++ + Lloyd, unit-normalised
  for `COSINE`), `AddIVF` / `RemoveIVF` (assign to the nearest centroid's posting
  list), `SearchIVF` (rank centroids, probe the `NPROBE` nearest lists, score
  exactly — recall reaches 1.0 when every list is probed), the `IVFStore`
  interface, and `IVFMem`. Versioned on-disk encodings: `NSIV` meta (25 bytes),
  `NSIC` centroid block, `NSIL` front-coded posting list (same shared-prefix +
  suffix scheme as HNSW nodes, bounded before allocation). Real-valued metrics
  only (`COSINE` / `L2` / `INNER_PRODUCT`). Not yet exposed through SQL —
  `CREATE VECTOR INDEX … USING IVF` and the executor build/rebuild/maintenance
  wiring are the next increment. `internal/vector` `TestIVF*`,
  `TestTrainIVFDeterministic`, `FuzzDecodeIVFList` / `FuzzDecodeIVFMeta`;
  `docs/vector.md` "IVF index".

- **Compressed HNSW neighbour lists.** Every HNSW graph node is written in node
  format v2: each layer's neighbour keys are sorted ascending (order is not
  meaningful in the graph) and front-coded — a varint neighbour count, then per
  key a varint shared-prefix length with the previous key plus the differing
  suffix, replacing the fixed 16-bit count and per-key length fields. Row
  primary keys in one table share a column prefix and, in a dense id space,
  several leading bytes, so the on-disk graph shrinks by roughly a third with no
  change to the decoded neighbour set, recall, or latency. v1 (fixed-width) node
  records still decode; `REBUILD INDEX` and ordinary writes re-emit v2. No
  catalog or `NSHM` meta format change. `nextsql-bench --vecquant` index-build
  delta drops accordingly (F32 610 KiB vs 980 KiB). `internal/vector`
  `TestCompressedNeighborLists`, `FuzzDecodeNode` v1/v2 seeds; `docs/vector.md`
  "Compressed neighbour lists".

- **`VECTOR<F16,N>` quantised element type.** Columns declared `VECTOR<F16,N>`
  store each element as an IEEE 754 half (2 bytes) in the detached vector
  payload store, halving that store on disk. The runtime value, distance
  functions, bounded algebra, `NEAREST`, and HNSW stay `float32` — half
  payloads are widened on read. Writes quantise at the boundary
  (round-to-nearest, ties to even) so reads match what is on disk.
  - portable `internal/float16` conversion package (no unsafe/cgo/assembly);
  - `NSVV` payload format v2 (element tag + halves), backward compatible with
    v1 F32 payloads;
  - `CREATE VECTOR INDEX ... USING HNSW` works on `F16` columns unchanged;
  - restart, encryption (`NSVV` never plaintext on disk), dimension and
    finite-value checks, and fuzz coverage all hold.

- **`VECTOR<I8,N>` quantised element type.** Columns declared `VECTOR<I8,N>`
  store each element as a signed byte with a per-vector `float32` scale
  (`absmax(v) / 127`, symmetric), roughly quartering the payload store at high
  dimension. As with `F16` the runtime value and every distance, algebra,
  `NEAREST`, and HNSW path stay `float32` — quantised payloads are widened on
  read. The scale is derived per vector at write time, so there is no
  catalog-side calibration or data scan; a zero vector round-trips exactly.
  - portable `internal/int8vec` conversion package (no unsafe/cgo/assembly);
  - `NSVV` payload format v2 extended with the `I8` element tag (`f32` scale +
    signed bytes); `F32` v1 and `F16` v2 payloads keep decoding unchanged;
  - `CREATE VECTOR INDEX ... USING HNSW` works on `I8` columns unchanged;
  - restart, encryption, dimension / finite-value checks, and fuzz coverage
    (`internal/int8vec` unit + `FuzzRoundTrip`, `internal/vector`
    `FuzzDecodePayload` I8 seed, `TestVectorI8Quantized`) all hold.

- **Quantised-vector benchmark (`nextsql-bench --vecquant`).** Seeds one vector
  set into an `F32`, an `F16`, and an `I8` column, builds an HNSW index over
  each, and reports per-element on-disk width, raw payload size, index-build page
  delta, total database size, build time, resident heap, mean quantisation
  error, and `NEAREST` p50/p95/p99 + recall@10/@100. Recall is scored against an
  exact-cosine flat search over the full-precision source vectors, so the
  `F32`→`F16`/`I8` gap is the quantisation penalty alone. Reference run
  (2000 × 128-d, linux/amd64): database 3.4 → 2.4 → 1.9 MiB as the payload store
  halves then quarters; recall@10 0.916 / 0.916 / 0.914; latency and QPS within
  noise (runtime is `float32` for every element type). `internal/bench/vecquant.go`,
  `TestVectorQuantBench`, `docs/vector.md` "Size / recall comparison". The suite
  also measures an `F32` column with an `F16`- and an `I8`-quantised HNSW graph.

- **Quantised HNSW index (`CREATE VECTOR INDEX … USING HNSW WITH (QUANTIZATION =
  'F16' | 'I8' | 'NONE')`).** The graph keeps a compact quantised copy of every
  vector beside its nodes (new `0x02` key in the index tree, `NSVV` encoding) and
  computes all traversal distances from it; `Search` then re-ranks the `ef`
  candidates against the full-precision column payloads, so the reported order
  and distances are exact and recall tracks an unquantised graph (reference
  2000 × 128-d: recall@10 0.916 for `qh-F16`, 0.912 for `qh-I8`, vs 0.916 `F32`).
  The traversal encoding is independent of the column element type. `NSHM` meta
  format v2 carries the tag (v1 headers decode with no quantisation);
  `types` catalog table format v6 stores one traversal-quantisation byte per
  index. Rows inserted or updated after the build are quantised into the graph on
  write; `REBUILD INDEX` rebuilds the quantised store; the store is encrypted and
  WAL/backup-recovered like every other index structure. This trades a small
  on-disk increase (the quantised copies are additive to the retained full
  payloads) for smaller, more cache-local traversal reads. `docs/vector.md`
  "Quantised HNSW index", `TestQuantizedHNSWIndex`, `internal/vector`
  `TestMetaQuantRoundTrip` / `TestQuantizedHNSWRerank` + `FuzzDecodeMeta` seed.
  - Default is `NONE`; existing `USING HNSW` indexes are unchanged.

- **`BITVECTOR<N>` binary vector type.** A distinct top-level column type (not a
  `VECTOR<...>` element) storing `N` single-bit elements as `ceil(N/8)` packed
  bytes — one thirty-second of `VECTOR<F32,N>`. Elements must each be `0` or `1`
  on write (a real-valued vector is rejected, never rounded); on read each widens
  back to a `float32` `0`/`1` so distance and HNSW math stay `float32`.
  - portable `internal/bitvec` packing package (no unsafe/cgo/assembly; unit +
    `FuzzRoundTrip`);
  - `NSVV` payload format v2 extended with the `BIT` element tag (packed bits,
    LSB-first), still backward compatible with v1 F32 / v2 F16 / v2 I8;
  - new `HAMMING` distance metric (`vector.MetricHamming`, differing-bit count) —
    the default and only metric for a `BITVECTOR` column; `USING HAMMING` is
    rejected on any other vector column and `USING COSINE | L2 | INNER_PRODUCT`
    is rejected on a `BITVECTOR` column;
  - `CREATE VECTOR INDEX … USING HNSW` builds a Hamming graph over a `BITVECTOR`
    column; `WITH (QUANTIZATION = …)` is rejected (the payload is already one bit
    per element);
  - `NEAREST … USING HAMMING`, `KwBitvector` / `KwHamming` lexer keywords,
    `types.VectorBit`, `Type.String()` → `BITVECTOR<N>`;
  - restart, encryption (`NSVV` never plaintext), dimension checks, parser +
    binder cases, and `internal/vector` payload/meta fuzz seeds all covered
    (`TestVectorBitvector`, `TestPayloadBitPacked`, `TestHammingDistance`,
    `TestBindBitvector`). `docs/vector.md` "BITVECTOR<N>".
  - Not yet: IVF / IVF-PQ, compressed HNSW neighbour lists, sparse retrieval.

### Multi-database hosting (track is PARTIAL)

- **Registry storage caps.** The encrypted deployment registry (`NSRM` v3) now
  carries a `StorageCapBytes` on every realm and every database (`0` = no cap).
  - `Registry.SetRealmStorageCap` / `Registry.SetDatabaseStorageCap` apply one
    durable change per encrypted generation; a no-op set does not advance the
    generation.
  - Invariants (enforced on set and revalidated on decode): a non-zero
    per-database cap may not exceed a non-zero realm cap; a realm cap may not be
    lowered below a per-database cap already set in the realm.
  - `NSRM` v1/v2 manifests decode with both caps `0`; the encoder always emits
    v3. Deterministic round-trip and decoder fuzz coverage hold.
  - CLI: `nextsql hosting set-realm-cap`, `nextsql hosting set-database-cap`,
    `nextsql hosting show` (registry root key `KEY-FILE.instance` or
    `--instance-key-file`).
  - **Realm-root delegation.** The admin runs `SetRealmRootAuth(realmID,
    secret)` (CLI `nextsql hosting set-realm-root --secret-file … | --clear`) to
    store `sha256(secret)` on the realm (`RealmRootAuthHash`, `NSRM` v3). A
    realm-root secret holder then sets only that realm's per-database caps via
    `SetDatabaseStorageCapAsRealmRoot` (CLI `set-database-cap
    --realm-secret-file …`) — constant-time secret check, still bounded by the
    realm cap, no path to the realm cap or any other realm; `Forbidden` when not
    delegated, `Unauthorized` on a bad secret.
  - **Write-path enforcement.** `nextsqld` applies `min` non-zero of the realm
    and database cap to the engine page allocator at open
    (`EffectiveStorageCapBytes`, `bytes / PhysicalPageSize`). Once the data
    file's page high-water hits the ceiling, allocating a new page fails with
    `nerr.Exhausted` ("storage cap exceeded") — `INSERT`, row-splitting
    `UPDATE`, index growth — while `DELETE` / `ROLLBACK` / in-place `UPDATE`
    keep working (freelist reuse). Data file only, not WAL/UNDO; not persisted
    (re-derived from the registry each start).
  - Cap changes take the exclusive data-directory lock (`set-realm-cap` /
    `set-database-cap` / `set-realm-root` fail with `Unavailable` against a
    running deployment); a cap edit is an overwrite and takes effect on the
    next restart. Live cap changes without a restart, and advisory
    `system.quotas` surfacing, are follow-ons (`docs/design-multidatabase-dbaas.md`
    §10.1).

### Deferred

- `REBUILD INDEX ... ONLINE`
  - blocking `REBUILD INDEX` is shipped;
  - `ONLINE` remains rejected until concurrent-write correctness is proven.

- partition-wise aggregation and partition-wise joins
  - waits for physical partitioning in P21.

### Planned roadmap

The following phases remain planned/open and are **not** current shipped functionality:

- P19 — WORKFLOW / TRIGGER / SCHEDULE / TASK
- P20 — CDC / Change Streams
- P21 — Native Table Partitioning
- P22 — Follower Reads / Read Scaling
- P23 — Vector Engine 2.0
- P24 — Full-text Search 2.0
- P25 — Security 2.0
- P26 — System Catalog / Introspection 2.0
- P27 — Operational Maturity / Workload Governance
- P28 — Professional Installer + NextSQL Manager
- P29 — Web-based NextSQL Studio
- P30 — NextSQL Intelligence + Built-in RAG

---

## Pre-release development history

Everything below predates the first tagged release (`0.0.1`); it is kept as a
single bucket rather than reconstructed into version sections.

### Added

#### Native database foundation

- Native NextSQL storage engine.
- Native NextSQL SQL dialect.
- Native NSQL wire protocol.
- Official driver implementations.
- 16 KiB logical page format.
- Versioned persistent formats.
- Versioned wire formats.
- Explicit page validation and corruption handling.
- Clustered B+Tree primary storage.
- Secondary indexes.
- Range scans.
- Buffer manager.
- Crash-safe persistence.

#### Transactions and durability

- ACID transaction model.
- MVCC version chains.
- READ COMMITTED isolation.
- SNAPSHOT isolation.
- SERIALIZABLE isolation with lock-based semantics.
- Transaction rollback.
- Deadlock detection.
- UNDO integration.
- LSN-based WAL.
- WAL segmentation and rotation.
- Group commit.
- fsync before commit acknowledgement.
- Checkpoints.
- REDO recovery.
- Partial-WAL-tail handling.
- Partial-data-write handling.
- Crash-injection coverage.

#### Encryption and security

- Encryption-by-default production storage model.
- AES-256-GCM authenticated page encryption.
- Encrypted WAL.
- Encrypted UNDO.
- Encrypted backup structures.
- Encrypted vector structures.
- Encrypted full-text structures.
- Encrypted temp/spill domains where applicable.
- Root unlock key kept outside the data volume.
- KEK → database master → domain-specific DEK hierarchy.
- Key rotation support.
- Key revocation support.
- Crypto-shredding support.
- TLS 1.3 requirements for remote production connections.
- Password authentication.
- RBAC.
- Tenant-aware access controls.
- Session auditing.
- Fail-closed handling for malformed or unauthorized operations.

#### SQL engine

- Lexer.
- Parser.
- AST.
- Catalog.
- Binder.
- Logical planner.
- Physical planner.
- Deterministic cost optimizer.
- Vectorized executor.
- Parallel execution.
- Statistics.
- Plan cache.
- `EXPLAIN`.
- `EXPLAIN ANALYZE`.

#### Relational SQL

- `CREATE TABLE`.
- `CREATE INDEX`.
- `CREATE UNIQUE INDEX`.
- `CREATE DATABASE`.
- `ALTER TABLE`.
- `DROP TABLE`.
- `INSERT`.
- `SELECT`.
- `UPDATE`.
- `DELETE`.
- `BEGIN`.
- `COMMIT`.
- `ROLLBACK`.
- `ANALYZE`.
- Foreign keys.
- `RESTRICT`.
- `NO ACTION`.
- `CASCADE`.
- `SET NULL`.
- `SET DEFAULT`.
- Inner joins.
- Left joins.
- Right joins.
- Full outer joins.
- Cross joins.
- Aggregation.
- Grouping.
- Ordering.
- `LIMIT`.
- `OFFSET`.

#### Modern SQL completeness

- `SELECT DISTINCT`.
- `HAVING`.
- searched `CASE`.
- simple `CASE`.
- `UNION`.
- `UNION ALL`.
- `INTERSECT`.
- `EXCEPT`.
- scalar subqueries.
- `IN` / `NOT IN` subqueries.
- `EXISTS` / `NOT EXISTS`.
- correlated subqueries.
- derived tables.
- CTEs.
- recursive CTEs.
- window functions.
- `ROW_NUMBER`.
- `RANK`.
- `DENSE_RANK`.
- `LAG`.
- `LEAD`.
- `FIRST_VALUE`.
- `LAST_VALUE`.
- aggregate window functions.
- UPSERT.
- `INSERT ... RETURNING`.
- `UPDATE ... RETURNING`.
- `DELETE ... RETURNING`.
- covering indexes / `INCLUDE`.
- index-only scans.
- partial indexes.
- expression indexes.
- Top-N optimization.
- improved join reordering.

#### Native JSON

- Native compact binary JSON storage.
- Typed JSON values.
- Object/array/scalar support.
- JSON path traversal.
- Partial decoding.
- JSON-path indexes.
- Transaction integration.
- WAL/recovery integration.
- Encrypted JSON persistence.
- JSON depth and size limits.
- JSON parser fuzzing.

#### Full-text search

- Native inverted index.
- Tokenizer.
- Normalization.
- Posting lists.
- Term/document frequency tracking.
- Positions.
- BM25-style ranking.
- Phrase search.
- `SEARCH column FOR '...'`.
- Transaction integration.
- WAL/recovery integration.
- Encrypted full-text index structures.

#### Vector search

- `VECTOR<F32,N>`.
- Out-of-row vector storage.
- Contiguous vector store.
- COSINE distance.
- L2 distance.
- INNER_PRODUCT.
- Exact flat vector search.
- `NEAREST ... TO`.
- HNSW.
- Encrypted ANN/vector structures.
- Bounded dimensions.
- Parallel distance calculation.

#### Hybrid query planning

- Unified relational + JSON + full-text + vector planning.
- Cost-based structured-filter-first or ANN-first execution.
- Candidate generation.
- Reranking.
- Reciprocal-rank fusion for hybrid result merging.
- `EXPLAIN` visibility into hybrid planning.

#### Geospatial

- `POINT`.
- `LOCATION`.
- `BOX`.
- `LINESTRING`.
- `POLYGON`.
- Coordinate validation.
- WKT coercion.
- `LON`.
- `LAT`.
- `DISTANCE`.
- `DISTANCE_SPHEROID`.
- `DWITHIN`.
- `WITHIN`.
- `COVERS`.
- Line length support.
- Spatial indexes.
- Optimizer integration.
- Exact residual spatial predicates.

#### Schema lifecycle and storage maintenance

- `DROP INDEX` for shipped index types.
- `DROP INDEX IF EXISTS`.
- Blocking `REBUILD INDEX`.
- Crash-safe index rebuild.
- Page reclamation.
- Durable freelist.
- Safe page reuse after restart.
- Orphan detection.
- MVCC-safe garbage eligibility.
- UNDO cleanup.
- Dead-version cleanup.
- B+Tree compaction.
- Full-text tombstone cleanup.
- HNSW tombstone strategy.
- WAL retention respecting PITR.
- `MAINTAIN DATABASE`.
- `MAINTAIN TABLE`.
- `MAINTAIN INDEX`.
- Bounded maintenance coordinator.
- Maintenance CPU budgets.
- Maintenance memory budgets.
- Maintenance I/O budgets.
- One active maintenance pass per database.
- Pause/resume support.
- Admission-aware maintenance.
- Maintenance metrics.
- Automatic statistics refresh policy.
- Bounded automatic maintenance scheduling.

#### Migrations

- Timestamped migration files.
- `migrate validate`.
- `migrate create`.
- `migrate status`.
- `migrate pending`.
- `migrate version`.
- `migrate up`.
- `migrate down`.
- `migrate force`.
- `migrate repair`.
- Transactional migration application.
- Checksum validation.
- Dirty-state detection.
- Dry-run parsing.
- Server-mode migration execution over NSQL.
- `DROP INDEX` migration parsing/validation support.

#### Native protocol and drivers

- TLS-aware NSQL connections.
- Authentication handshake.
- Typed parameters.
- Prepared statements.
- Streaming results.
- Backpressure.
- Cancellation.
- Packet-size limits.
- SQL-length limits.
- Result-size limits.
- Runtime limits.
- Worker limits.
- Memory limits.
- Attacker-controlled length validation.

Official driver surfaces include:

- Go.
- Node.js.
- Bun.
- Deno.
- TypeScript types.
- PHP.

#### Backups and recovery

- Encrypted physical backup.
- Restore.
- Backup verification.
- Restore verification.
- WAL archive integration.
- PITR.
- Restore by LSN.
- Restore by timestamp.
- Logical export.
- Logical import.

#### High availability

- Raft-based HA.
- Minimum 3-voter cluster model.
- Leader election.
- Replicated state/log.
- Synchronous quorum durability.
- Leader failover.
- Replica repair.
- Rolling maintenance support.
- Safe write rejection under quorum loss.
- Split-brain prevention.
- Deterministic follower application.
- Engineering target: leader election under 3 seconds.
- Engineering target: service recovery under 5 seconds.
- Availability target expressed as an SLO, not a zero-downtime guarantee.

#### Operational tooling

- `nextsql` CLI.
- `nextsqld` server.
- `nextsql-bench`.
- `nextsql init`.
- `nextsql exec`.
- `nextsql backup`.
- `nextsql restore`.
- `nextsql verify`.
- `nextsql export`.
- `nextsql import`.
- `nextsql diagnose`.
- `nextsql status`.
- cluster status tooling.
- Official benchmark workloads.
- Admission control.
- Bounded query queues.
- Query cancellation.
- Result limits.
- Operational diagnostics.

#### Packaging

- Linux `.deb` packaging.
- Linux `.run` packaging.
- Linux `.tar.gz` packaging.
- Windows `.zip` packaging.
- Windows installer support.
- Installer build scripts.

### Changed

- Expanded SQL from the original P0–P15 surface through the P18 implementable SQL-completeness scope.
- Expanded schema lifecycle from create-only index behavior to full shipped `DROP INDEX` plus blocking rebuild.
- Added durable storage reclamation and reuse instead of leaving detached pages permanently unreclaimed.
- Added bounded maintenance as a first-class engine responsibility.
- Migration validation now understands shipped `DROP INDEX` behavior.
- Project documentation now separates:
  - final product intent;
  - implementation/status truth;
  - sequencing;
  - agent engineering rules;
  - user/operator documentation.

### Fixed

- Corrected large sequential `DELETE` behavior after the B+Tree leaf-merge issue.
- Preserved B+Tree structural correctness through restart/recovery testing.
- Corrected vector benchmark methodology to use distinct-vector validation and report recall with latency.
- Improved consistency between README, usage documentation, project specification, and engineering-agent documentation.

### Security

- Documented the live-unlocked-host threat-model limitation explicitly.
- Reinforced the rule that keys and passwords must never be carried in connection URLs.
- Kept encryption and durability enabled in official benchmark methodology.
- Reinforced fail-closed behavior for malformed, unauthorized, or unsupported operations.

### Performance

Tracked engineering targets include:

- cached primary-key lookup p50 < 0.5 ms;
- indexed query p95 < 3 ms;
- 25K-row workload < 1 s;
- optimized 1M-row aggregation < 1 s;
- optimized 10M-row aggregation < 5 s;
- 100M analytical workload < 30–60 s;
- 1M HNSW top-10 p95 < 25 ms with recall reported.

Performance figures are hardware/context-specific engineering targets or measurements, not universal guarantees.

### Known limitations

- The engine remains under measurement.
- P16 is not yet closed.
- `REBUILD INDEX ... ONLINE` is not implemented.
- Partition-wise aggregation/join waits for native physical partitioning.
- P19–P30 are not shipped.
- Multi-primary writes are not part of the current core roadmap.
- Studio, Manager, and Intelligence are not current production surfaces until their roadmap phases complete.

---

## Changelog policy

Use the following categories when recording changes:

```text
Added
Changed
Deprecated
Removed
Fixed
Security
Performance
```

Rules:

1. Record **shipped or verified behavior**, not aspirations.
2. Put active development under `[Unreleased]`.
3. Do not mark roadmap items completed until `TODO.md` says the owning gate is green.
4. Include correctness-impacting fixes even if they are internal.
5. Include persistent-format or wire-format changes prominently.
6. Include security-relevant behavior under `Security`.
7. Include benchmark methodology changes under `Performance`.
8. Do not convert targets into measured claims.
9. Do not describe blocking operations as online.
10. Never make unsupported claims such as:
    - “unhackable”;
    - “100% secure”;
    - “zero downtime guaranteed”;
    - “impossible to lose data”.

---

## Links

- [README.md](README.md) — project overview and quick start
- [USAGE.md](USAGE.md) — current operator/application manual
- [PROJECT.md](PROJECT.md) — intended finished product
- [TODO.md](TODO.md) — current implementation/status truth
- [ROADMAP.md](ROADMAP.md) — simplified, non-authoritative roadmap derived from `TODO.md`
- [SKILLS.md](SKILLS.md) — engineering/agent contract
- [AGENTS.md](AGENTS.md) — repository agent instructions
