# Changelog

All notable changes to **NextSQL** are documented in this file.

NextSQL is currently under active development as `0.1.0-dev`.

This changelog follows the project source-of-truth model:

```text
TODO.md    = current implementation/status truth
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

P16 correctness/SLO closure, **P22 follower reads / read scaling**, and
**P23 Vector Engine 2.0** are all **complete** (P16 paper-closed 2026-08-30;
P22 exit gate closed 2026-08-30 with the linearizability/consistency sign-off
in `docs/ha.md` and the `TestFollowerReadFailoverSessionGuarantee` failover
session-guarantee test; P23 exit gate closed 2026-08-31 with the
production-gating sign-off in `docs/vector.md`). The current release gate is
**P24 Full-text Search 2.0**.

P22 exit gate, all satisfied:

- three read-consistency modes — `STRONG` (linearizable behind a
  `raft.VerifyLeader` quorum read barrier), `BOUNDED` (within `MAX STALENESS`),
  `STALE` (unbounded) — all consistent committed prefixes, `STALE`/`BOUNDED`
  never mislabelled `STRONG`;
- replica lag + follower health via `system.replica_health` and `NodeStatus`;
- follower-read routing in the server and every official driver (Go, Node, Bun,
  Deno, PHP);
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

## [0.1.0-dev]

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

- `0.1.0-dev` remains under measurement.
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
