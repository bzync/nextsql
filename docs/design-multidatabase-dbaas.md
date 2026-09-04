# Proposed Multi-Database Hosting and Subscription Isolation

> Status: **ACCEPTED DESIGN — M1 FOUNDATION COMPLETE, M2 COMPLETE
> (M2-1/M2-2/M2-3a/M2-3b-1/M2-3b-2/M2-3b-3a/M2-3b-3b/M2-3b-3c/M2-4a/M2-4b-1/
> M2-5/M2-6 LANDED), M3-1 LANDED 2026-09-04 (suspend/resume enforcement),
> M3-3 LANDED 2026-09-04 (offline drop/tombstone physical reclamation);
> M3-2, M3-4, M3-5 NOT STARTED; NOT PRODUCTION-GATED**
>
> This document is a design and delivery plan. `TODO.md` remains authoritative
> for implementation status and sequencing. Nothing in this document changes a
> shipped capability claim.

Implemented foundation (2026-08-28): versioned encrypted `NSRE`/`NSRM`
deployment registry, stable deployment/realm/database identities, bounded
lifecycle validation, durable nonce high-water publication, restartable
`nextsql init` bootstrap, a separate deployment registry root, and `nextsqld`
verification of the active default database. The server, init, and explicit
offline legacy-default adoption path share an exclusive deployment lock;
adoption preserves and recovery-verifies the existing file identity before
activation and never discovers sibling files.

**M2-1 landed 2026-09-02**: `Registry.CreateRealm`/`CreateDatabase` and
`nextsql realm create`/`nextsql database create` register and physically
provision an additional managed database (§16 below).

**M2-2 landed 2026-09-02**: `Hello.Realm`, an additive opt-in wire field
letting a client identify which realm it intends to reach, validated by
`nextsqld` as a flat-string check against the one realm it serves (§8, §16
below). This is routing/identity validation only, not live multi-database
routing.

**M2-3a landed 2026-09-02**: `internal/dbmanager.Manager` gives `nextsqld`
its first live multi-database routing — a connection's `Hello.Realm`/
`Hello.Database` can now reach a genuinely different, already-registered
database, not just validate identity against the one primary database
(§9, §16 below). Bounded to a small fixed open-database limit, and
secondary databases are single-node only (no Raft attachment).

**M2-3b-1 landed 2026-09-02**: a secondary database now closes when idle
(its last connection disconnects) and reopens cleanly on the next one,
instead of staying open forever once opened; a database that repeatedly
fails to open is quarantined with exponential backoff instead of being
retried in a tight loop (§9, §16 below). The Preloaded primary is pinned
and never evicted.

**M2-3b-2 landed 2026-09-03**: a process-wide `buffer.Budget` now optionally
caps the total buffer-pool frames committed across every database `nextsqld`
has open at once (`max_total_buffer_pages`, 0 = unbounded default); a
database's reservation releases the moment it closes, including M2-3b-1 idle
eviction (§9, §16 below).

**M2-3b-3a landed 2026-09-03**: every open database's scheduled-task
execution now submits to one shared, fixed-size `executor.TaskPool`
(`task_workers` config key) instead of each database spawning its own
worker set — task-execution goroutine count no longer scales with the
number of open databases. Each database still polled its own due
tasks/schedules independently at this point (§9, §16 below).

**M2-3b-3b landed 2026-09-03**: the polling itself is now centralized too —
one process-wide `executor.CentralScheduler` enumerates every open
database each tick (via a new `dbmanager.Manager.Snapshot`) instead of one
poll loop per database, reducing polling goroutines from O(open databases)
to O(1) on top of 3a's shared workers (§9, §16 below). M2-3b-3c retired
the already-dead-in-production `TaskRuntime.Cancel`/`running` registry
(2026-09-03) — with that, M2-3b (and the whole M2 selectable-hosting
milestone) is complete.

ID-layout migration/rollback of the *existing* legacy default database,
realm-local auth stores (§5.2, M2-4), quotas, HA replication, and
independent operational lifecycle remain open.

## 1. Purpose

Evolve NextSQL from one database per `nextsqld` process into a native managed
database data plane in which one bounded server or cluster can host multiple
subscription accounts and multiple databases per account.

The target product shape is:

```text
NextSQL service / deployment
└── nextsqld instance or HA cluster
    ├── realm: customer-a
    │   ├── authentication realm, roles, plan, and quotas
    │   ├── database: production
    │   └── database: analytics
    └── realm: customer-b
        ├── authentication realm, roles, plan, and quotas
        └── database: production
```

This provides a shared subscription tier without removing the ability to sell
dedicated-process, dedicated-cluster, or dedicated-node tiers.

Suggested service tiers:

| Tier | Isolation boundary |
|---|---|
| Shared | Multiple bounded realms/databases in one `nextsqld` deployment. |
| Dedicated instance | One customer realm in one `nextsqld` process. |
| Dedicated cluster | One customer realm on a dedicated voter set. |
| Enterprise | Dedicated cluster plus customer-specific keys, network policy, backup policy, and resource ceilings. |

The commercial control plane owns signup, subscription state, plan selection,
and provisioning requests. The `nextsqld` data plane owns authentication,
authorization, durable plan enforcement, database lifecycle, and query
execution. A billing or control-plane outage must not participate in SQL commit
correctness. Data-plane decisions use the last authorized durable policy for a
defined bounded lease.

The feature must preserve NextSQL's priority order: correctness, durability,
security, integrity, availability, latency, throughput, efficiency, developer
experience, then features.

## 2. Current Baseline and Gap

The current implementation is deliberately single-database:

- `nextsqld` opens `DATA-DIR/nextsql.db`;
- `protocol.Server` owns one database handle, auth store, ACL store, audit log,
  and task runtime;
- the Hello `database` field is optional and does not select an engine;
- `CREATE DATABASE name` creates a sibling file but does not register, route,
  serve, back up, recover, or replicate that database as part of the running
  server;
- auth and ACL files are scoped to the one configured data directory;
- HA is attached to the one opened executor database.

Therefore the existing `CREATE DATABASE` implementation is a useful storage
primitive, not multi-database server support. It must not be advertised as the
latter.

## 3. Terminology

Use distinct names for distinct security boundaries:

| Term | Meaning |
|---|---|
| Deployment | One standalone `nextsqld` or one HA cluster exposed as a service endpoint. |
| Instance | One `nextsqld` process participating in a deployment. |
| Realm | Subscription/account security boundary containing principals, roles, databases, plan, and quotas. |
| Database | Independent catalog, transaction, WAL, recovery, encryption, backup, and task boundary. |
| Plan | Versioned resource policy assigned to a realm; not a billing system. |

Do not reuse `TENANT` for the subscription boundary. Shared row tenancy has
been removed; realm/database isolation is the hosting security boundary.

## 4. Non-Goals for the First Production Release

- Cross-database transactions.
- Cross-database joins or three-part object names.
- A `USE database` statement that changes a live session's database.
- Multi-primary writes.
- Automatic sharding or tenant placement across arbitrary clusters.
- An in-engine payment processor or dependency on a billing provider for
  transaction correctness.
- Sharing tables, indexes, WAL, task state, CDC state, or result-cache entries
  between databases.
- Treating physical locality or encryption keys as authorization.
- Sending root unlock keys in connection strings.

Each connection is bound immutably to one realm and one database. Clients open
a new connection to change either boundary.

## 5. Security and Identity Model

### 5.1 Stable identities

Generate and persist independent non-reusing identifiers:

```text
DeploymentID
RealmID
DatabaseID
Database file identity
```

Names are mutable labels. Filesystem paths, cache keys, audit correlation,
replication commands, backup manifests, CDC tokens, task identifiers, and
authorization decisions use stable IDs. A dropped ID is tombstoned and never
silently reused.

Realm names are unique within a deployment. Database names are unique within a
realm, so `customer-a/production` and `customer-b/production` are distinct.
All user-controlled names receive bounded length, Unicode/normalization, and
path-traversal validation before persistence.

### 5.2 Authentication and authorization

Each realm has its own principal namespace, password/identity store, roles, and
grants. The same username may exist independently in two realms.

**M2-4b-1 landed 2026-09-02**: `auth.Store`/`security.ACL` gained a `RealmID`
dimension (`hosting.ID{}` for deployment-wide, unioned with a realm's own
grants/roles for ordinary privileges — authorization is additive) covering
the principal namespace, password store, roles, and grants exactly as
described above, in a single composite-keyed file (not the per-realm-file
layout §7 originally sketched — that remains open as M2-4b-2, needed only
for isolation-at-rest/crypto-shred, not for correctness). The authorization
tuple below does not yet carry a `DatabaseID` dimension — every grant so far
is realm-scoped, not additionally database-scoped within a realm; that
remains open. `PrivAdmin+ScopeCluster`/`PrivAdmin+ScopeAdmin` always mean
deployment-wide regardless of the realm a grant names — a first, narrow
instance of "deployment administration is separate from realm
administration" below; a distinct **realm-admin** privilege (bounded to one
realm, e.g. a future `ScopeRealm`) is not yet implemented. See
`docs/design-multidatabase-dbaas.md` §16 and `TODO.md` log #76 for the full
implementation writeup.

The authorization tuple is at least:

```text
(RealmID, PrincipalID, DatabaseID, privilege, object scope)
```

Connection routing never grants access. After authentication, the selected
principal must have `CONNECT` on the selected database or realm/deployment
administrative authority. Database creation requires a distinct bounded
realm-level privilege. Deployment administration is separate from realm
administration.

Pre-authentication errors must not disclose whether another customer's realm,
database, or username exists. The audit record may retain the internal reason
after secret redaction.

User password files authenticate principals. There is no shared `db.pw` that
acts as both an encryption key and a database login.

### 5.3 Key hierarchy

Recommended shared-tier hierarchy:

```text
External/KMS realm root
├── database A KEK → database A master → database A domain DEKs
└── database B KEK → database B master → database B domain DEKs
```

Every database receives independent KEK, master, page, WAL, UNDO, backup,
vector, full-text, temp, and replication keys. Sharing a realm root is an
explicit convenience/blast-radius tradeoff; a dedicated tier may override it
with one external root per database.

Raw roots are never stored in the data directory, registry, URLs, logs, audit,
metrics, or connection profiles. Registry records contain only bounded key
provider references and key versions. Production service deployments should
support an established KMS/HSM provider; local key files remain a supported
offline/dedicated mode.

Realm auth, ACL, plan, and registry state need an authenticated, versioned
encryption domain. A deployment control key must not be derived from one
customer's root. Deleting or shredding one realm must not make the deployment
registry unreadable.

The current `REQUIRE CLIENT KEY` flow is not automatically suitable for a
shared service. Its multi-realm semantics must be redesigned and separately
gated; managed shared tiers should normally unlock through a server-side
KMS/provider, while client-provided roots may remain a dedicated deployment
option.

### 5.4 Isolation invariants

For a session bound to `(RealmID, DatabaseID)`:

- it cannot obtain a handle to another realm or database;
- prepared statements, cancellation secrets, idempotency keys, result caches,
  plan caches, CDC tokens, tasks, schedules, workflows, and temporary files are
  scoped by both IDs;
- system views reveal only authorized realms/databases;
- logs and metrics do not expose another realm's object names or secrets;
- backup, restore, PITR, import, export, and maintenance resolve stable IDs and
  reauthorize at execution time;
- legacy `tenant_id` tables remain ADMIN-only for migration and cannot become a
  second authorization system inside the selected database.

Cross-realm leakage tolerance is zero.

## 6. Durable Instance Registry

Introduce a versioned, checksummed, authenticated, encrypted registry owned by
the deployment rather than any user database. Do not serialize raw Go structs.

Minimum records:

```text
Deployment
Realm
Database
Principal/identity-provider binding
Role/grant scope
Plan assignment and quota overrides
Key-provider reference and key version
Lifecycle operation and idempotency record
Tombstone
```

Database lifecycle states are explicit:

```text
PROVISIONING → ACTIVE → SUSPENDED → DELETING → TOMBSTONED
                    ↘ FAILED
```

Every transition has one leader authority, a durable operation ID, idempotent
retry semantics, audit records, and restart recovery. `ACTIVE` is published
only after required files, envelopes, WAL, catalog, and replication state are
durable and verified.

The registry must define:

- format and schema version;
- corruption validation and fail-closed behavior;
- encryption and nonce domain;
- WAL/consensus semantics;
- crash points and restart reconciliation;
- backup, restore, and disaster-recovery behavior;
- rolling upgrade and rollback limits;
- reclamation and tombstone retention;
- bounded maximum realms, databases, principals, roles, and operations.

Do not use directory enumeration as the source of truth.

## 7. Storage Layout

Use immutable IDs in paths, not user-controlled names. A possible logical
layout is:

```text
DATA-DIR/
├── instance/
│   ├── registry
│   ├── registry.keys
│   ├── registry.wal/
│   └── audit/
└── realms/<RealmID>/
    ├── security/
    └── databases/<DatabaseID>/
        ├── nextsql.db
        ├── nextsql.db.keys
        ├── wal/
        ├── undo/
        ├── tasks/
        └── temp/
```

Exact filenames remain a format decision. Every path must be derived from a
validated stable ID beneath a validated data root. Symlink and path traversal
behavior must fail closed.

Each database remains independently checkable, recoverable, backed up, and
crypto-shreddable. An account-wide snapshot is not transactionally consistent
across databases unless a separately designed coordinated snapshot protocol is
used and explicitly reported.

## 8. Connection Protocol and Drivers

**M2-2 landed 2026-09-02**: `Hello.Realm`, an additive opt-in trailing
field (not the negotiated protocol revision this section originally
envisioned — see §19 item 7 for why that turned out unnecessary: the frame
header's `Version` is a hard equality gate with no negotiation, so realm
selection is added the same way `NSCT` catalog records add a field, not via
a version bump). See `docs/protocol.md`'s Hello section for the wire-level
detail.

**M2-5 landed 2026-09-02**: a non-empty `Hello.Realm` is now validated
against the deployment's actual registry (`hosting.Registry.LookupRealm`,
rejecting an unknown name `not_found`) whenever a `HostingRegistry` is
configured — not just flat equality against one pinned default realm, which
was M2-2's original, narrower behavior (a legacy/non-hosted deployment,
with no `HostingRegistry` at all, keeps that exact flat-equality behavior
unchanged). Combined with M2-3a's `dbmanager` routing and M2-4b-1's
realm-scoped authorization, a Hello may now legitimately select any real
realm/database pair in the deployment, routed and isolated correctly. The
rest of this section (capability negotiation, the batch-manifest bootstrap
below) remains open.

The Hello selection is logically (partially landed, see above):

```text
realm
database
user/service identity
protocol capabilities
```

Do not overload `database` with `realm/database`; add an explicit versioned
realm field. Limits apply before allocation.

Configuration surface:

```text
--realm / NEXTSQL_REALM_NAME / driver Config.Realm
--database / NEXTSQL_DATABASE / driver Config.Database
--instance-key-file / NEXTSQL_INSTANCE_KEY_FILE
```

Single-pair bootstrap uses `NEXTSQL_REALM_NAME` plus `NEXTSQL_DATABASE`.
Planned batch bootstrap uses a file path rather than numbered environment
variables:

```text
NEXTSQL_HOSTING_MANIFEST_FILE=/etc/nextsql/hosting.yaml
```

The external manifest lists bounded realms, databases, and per-database key
file paths. Init validates the complete document before mutation, provisions
independent catalog/WAL/key domains, and publishes one registry generation
atomically. Reapplying an identical manifest is idempotent; partial creation,
duplicate names, missing key paths, and attempts to mutate immutable IDs fail
closed.

**Landed (2026-09-03):** `nextsql init --hosting-manifest FILE` (or
`NEXTSQL_HOSTING_MANIFEST_FILE`, or a dotenv key) takes this path.
`hosting.EnsureBootstrapManifestKeyFiles` first creates any missing
per-database root key file (a fresh independent AES-256 root, mode 0600),
then `LoadDeploymentBootstrap` validates the whole document,
`EnsureManifest` publishes one `PROVISIONING` generation covering every
realm/database, and each managed database is physically created and set
`ACTIVE` (reusing the same `activateManagedDatabase` path as
`nextsql database create`). Re-running with an identical manifest is a
clean no-op; a partial run resumes.

`nextsqld` serves a manifest-bootstrapped deployment directly: since its
default is `LayoutManaged` (a manifest gives every database, the default
included, its own key file), `openHostedDefault` accepts it and the eager
primary open at `DATA-DIR/nextsql.db` is skipped — the process starts with
no primary handle and `dbmanager` opens and serves the default realm/
database lazily on the first connection, the same path every non-default
managed database already uses. Such a deployment needs only
`--instance-key-file` (no `--key-file`), and does not support
`--require-client-key`. It has no WAL archiver / PITR / Raft for any
database yet — the same M3 gap every managed database shares.

Local init/adoption and `nextsqld` support process environment plus discovered
or explicit dotenv files with explicit flags taking precedence. Environment
values identify paths and logical resources; they never contain raw root key
bytes. `NEXTSQL_DATABASE` is authoritative for the logical bootstrap/adoption
name when no explicit `--database` flag is present.

Server/bootstrap credentials use the distinct `NEXTSQL_SERVER_USER` and
`NEXTSQL_SERVER_PASSWORD_FILE` (preferred) or `NEXTSQL_SERVER_PASS` variables.
They never fall back into client `NEXTSQL_DATABASE_USER` authentication.

Rules:

- multi-database mode requires an explicit selection unless a deployment has a
  configured default realm and default database;
- protocol v1 clients may route only to that default pair;
- an empty database must never become a wildcard;
- realm/database binding is immutable for the connection;
- cancel requests and reconnect/resume tokens include the routing scope;
- root keys and passwords remain out of addresses and URLs;
- remote connections require TLS, with mTLS/service identity integrated when
  Phase 25 provides it.

All official drivers, CLI commands, migrations, Studio, and Manager must use
the same server-authoritative routing and capability negotiation.

## 9. Bounded Database Engine Manager

**Decomposed 2026-09-02** into two sub-increments, following the same
"smallest coherent increment" discipline the M2 milestone itself was
decomposed with (§16), after a scoping investigation found the full
requirement list below spans subsystems with zero existing refcounting or
pooling infrastructure to build on (no `singleflight`, no reference
counting, and `internal/executor.TaskRuntime` spawns its own goroutine set
per instance today — exactly the per-database pool this section asks to
centralize):

- **M2-3a — DatabaseManager exists, connections route through it, small
  fixed open-database limit** (landed 2026-09-02). Correctness-first, per
  this section's own allowance below ("initial correctness-first policy
  may keep a small fixed open-database limit"). `internal/dbmanager.Manager`:
  a keyed (registry database ID), mutex-guarded map of open handles with
  hand-rolled single-flight (no `x/sync` dependency exists in this repo) —
  deliberately *not* built on `scheduler.Admission` as originally sketched,
  since that type is a per-request acquire/release queueing gate, the
  wrong shape for "permanently consume one of N slots, never released,"
  which is this slice's actual (no-eviction) access pattern; flagged as a
  reality-vs-sketch deviation rather than forced to fit. Connections route
  through `Acquire(realm, database)` at the one narrow seam identified in
  `protocol.Server.serveConn` (confirmed by investigation that
  `Server.DB`/`Server.Tasks` are each touched in only a handful of places,
  not smeared across request handling) — additively, via a new
  `Server.Databases` field alongside the unchanged `DB`/`Tasks` (nil means
  every connection uses the pre-M2-3a path, byte-for-byte). Also
  restructured the Phase 27 WAL-retention/disk-watermark/replica-lag
  monitors (`cmd/nextsqld/main.go`) into per-DB start-on-open — their tick
  functions were confirmed already pure/DB-parameterized, so only the
  `start*` wrapper lifecycle changed, one call site per monitor inside the
  `Opener` closure that opens an additional registered `LayoutManaged`
  database (single-node only: no `startCluster`/`installArchiver` for a
  secondary database, out of scope until M2-3b or later). **No** reference
  counting, **no** idle eviction, **no** memory budget, **no** central
  bounded background pools — those are M2-3b.

  Three real bugs were caught building this, each by an end-to-end test
  actually exercising the new path rather than by inspection: (1) database
  resolution originally ran *after* `TypeReady` was sent — the wire
  protocol's definitive "handshake succeeded" signal, read once by
  `drivers/go`'s `Conn.handshake` with no further reads — so a routing
  failure would never reach the client, which would see success despite
  the server internally rejecting the connection; fixed by moving
  resolution before `TypeReady`. (2) a secondary managed database's
  `KeyRef` is a standalone *root* key that unlocks an *envelope* keystore
  next to the database file (exactly like the primary), not a key usable
  directly on the database file — the `Opener` originally used it
  directly, surfaced only once verified live against a real `nextsql
  database create`-provisioned database (the test fixture had made the
  identical mistake in both creating and opening the test database, so it
  was self-consistently wrong and didn't catch this). (3) in the test
  fixture only, the envelope was closed before the database it protects,
  backwards from the correct order.
- **M2-3b — Full §9 spec.** **Decomposed 2026-09-02** into three further
  sub-increments after a scoping investigation found the pieces have very
  different risk/readiness levels — correcting this section's own earlier
  "none of these subsystems expose an incrementable/decrementable ref
  today" framing along the way: sessions (`DB.sessions`/`RegisterSession`/
  `UnregisterSession`, `internal/executor/db.go`), CDC
  (`db.cdcSubs`/`registerCDCSubscription`, same file), and tasks
  (`TaskRuntime.running`, `internal/executor/task_runtime.go`) already
  have live, incrementable/decrementable registries — they were simply
  never consulted by anything DB-lifecycle-related. Backup and replication
  are confirmed vacuous for now: backup never touches a manager-opened
  database (still fully offline/CLI-only), and M2-3a deliberately never
  attaches replication to a secondary database at all, so there is nothing
  to count for either until later work reaches them.
  - **M2-3b-1 — Connection/session reference counting + idle eviction +
    open-failure quarantine** (landed 2026-09-02). The smallest,
    independently-landable slice, and the actual headline capability this
    section promises: it's what turns M2-3a from "opens and never closes"
    into "opens and closes when idle." Every `entry` in
    `internal/dbmanager.Manager`'s open map now carries a `refs` counter
    and a `pinned` flag; `Acquire` returns a release closure (idempotent,
    mirroring `scheduler.Admission.enter()`'s "once" guard) paired 1:1 with
    the existing single `Acquire` call site
    (`internal/protocol/server.go`'s `serveConn`, in the same
    per-connection defer that already calls `DB.UnregisterSession`).
    Eviction triggers once refs reaches zero on a non-pinned entry —
    the Preloaded primary is `pinned` and deliberately never evicted, since
    `Opener` only ever handles `LayoutManaged` databases and would refuse
    to reopen the primary's `LayoutLegacyDefault`, making eviction of the
    primary unrecoverable. Eviction safely reuses `DB.Close()` as-is (via a
    per-entry `cleanup` closure `Opener` now also returns), since
    `Engine.Close()` (`internal/storage/engine.go`) already
    checkpoints/flushes/closes the WAL durably — no new durability
    mechanism needed, only orchestration, and `nextsqld`'s real `Opener`
    closes its `TaskRuntime` *before* the database (so no background task
    call races the close) and its envelope *after* (the final
    checkpoint/flush needs the key material). No explicit
    `PauseMaintenance` wiring was needed after all — refcount reaching
    zero already implies no session/task could be mid-`MAINTAIN`
    synchronously for that database, so maintenance rides along for free,
    same as CDC. Quarantine + backoff on a failed open landed in the same
    `Acquire`/open path, an independent exponential-backoff implementation
    (same overflow-safe-shift shape as `internal/executor/task.go`'s
    task-retry logic, not a shared call into it) — checked before the
    open-limit check, since a quarantined database should never compete
    for a slot. Live-verified against a real `nextsqld`: real file
    descriptors (database file, WAL segment, undo log) confirmed open via
    `/proc/<pid>/fd` while a secondary-database connection was live, and
    fully closed within the poll window after disconnect, with data
    surviving repeated evict/reopen cycles.
  - **M2-3b-2 — Global memory budget gating buffer-page grants (landed
    2026-09-03).** New `buffer.Budget` (`internal/storage/buffer/budget.go`):
    a mutex-guarded frame counter with an optional cap, `Reserve`/`Release`,
    nil-safe (a nil `*Budget` is unbounded, matching every pre-existing
    caller exactly). Since a `Pool`'s frames are allocated in full at
    construction — there is no dynamic per-page grant to gate at runtime,
    only the all-or-nothing decision of whether a new database's Pool may be
    built at all — the budget is charged once per `Engine` open and released
    once at `Engine.Close()` (`internal/storage/engine.go`: `OpenOptions`
    gained a `Budget *buffer.Budget` field, threaded into
    `buffer.NewWithBudget`; `Engine` carries `budget`/`budgetFrames` to
    release the exact reservation on close, regardless of who calls
    `Close()` — normal shutdown, or M2-3b-1 idle eviction). New
    `max_total_buffer_pages` config key (0 = unbounded default; rejected by
    `Config.Validate` if positive but below `buffer_pages`, since otherwise
    even the primary database could never open). `cmd/nextsqld/main.go`
    constructs one `buffer.NewBudget(cfg.MaxTotalBufferPages)` shared across
    all three of its `executor.Open` call sites (primary, dbmanager
    secondary opener, `REQUIRE CLIENT KEY` lazy primary open) — all switched
    to `executor.OpenWith(..., storage.OpenOptions{Budget: bufBudget})`.
    Deliberately scoped to the long-running server process only:
    `storage.Create`/`CreateWithIdentity` (the one-shot `nextsql database
    create` provisioning CLI path, which exits immediately after) were left
    unbudgeted — that process never holds more than one `Pool` open at a
    time, so there is nothing to gate. No dedicated `system.*`/metrics
    observability surface yet (`Budget.Used()`/`Cap()` exist but are not
    wired to any introspection table) — deliberately deferred as a follow-on,
    not required for the gating behavior itself. Tests:
    `internal/storage/buffer/budget_test.go` (unit, incl. nil-safety and a
    concurrent Reserve/Release race), `internal/storage/engine_test.go`
    `TestBufferBudgetGatesConcurrentOpens`/`TestBufferBudgetNilUnbounded`
    (real `Engine`s, two databases sharing a budget sized for only one),
    `internal/config/config_test.go` (load/validate coverage for the new
    key). `go build ./...` clean; `go vet ./...` unchanged (same
    pre-existing unrelated `internal/executor/cdc.go` finding). All green
    under `-race`: `internal/storage` (incl. `buffer`/`btree`, 254.9s),
    `internal/config`, `internal/dbmanager`, `cmd/nextsqld`. **Live
    verification against real `nextsql`/`nextsqld` binaries**: bootstrapped
    a real deployment (`nextsql init` realm `default`/database `default`,
    `nextsql realm create` for a second realm `r2`/database `db2`), started
    a real `nextsqld` with `buffer_pages=8`/`max_total_buffer_pages=10`
    (enough for the primary alone, not both) — a real client connection to
    `db2` was rejected `exhausted: global buffer memory budget exceeded`;
    raised to `max_total_buffer_pages=16` (exactly primary+secondary) on a
    clean restart and the same connection to `db2` then reached real SQL
    execution (past the budget gate entirely), confirming both the
    rejection and the release/retry path work against a real process, not
    just in-process fakes.
  - **M2-3b-3 — Centralizing `TaskRuntime`'s per-database goroutine
    pools into shared bounded pools.** Scoped 2026-09-03 (Explore fork)
    and decomposed after confirming this is a genuine new-component
    design, not a one-shot parameterization tweak — no existing
    fan-out-poller/shared-worker-pool type to build on, and a real
    correctness hazard at the M2-3b-1 eviction boundary once workers are
    shared (see M2-3b-3a below).
    - **M2-3b-3a — shared bounded worker pool + DB-tagged job type, per-DB
      polling kept** (landed 2026-09-03). New `executor.TaskPool`
      (`internal/executor/task_pool.go`): one fixed-size worker set
      (`Workers` goroutines, `task_workers` config key, 0 = the same
      default every individual runtime used before) shared process-wide,
      constructed once instead of once per open database. `TaskRuntime`
      keeps its own per-database `coordinate()`/`cycle()` poll loop
      unchanged in shape — the harder "one scheduler enumerates every open
      database" fan-out is deliberately **not** built here, that is
      M2-3b-3b — but now submits claims, tagged with the submitting
      `*TaskRuntime`, to the shared pool instead of a per-runtime `jobs`
      channel. Total task-execution goroutines no longer scale with the
      number of open databases (before: `Workers+1` per open database;
      now: `Workers` once, process-wide). **The correctness hazard a
      shared pool introduces**: closing one database's `TaskRuntime` can
      no longer synchronously stop the exact goroutines that might touch
      its `*DB` — a pool worker could be mid-execution of that database's
      job, or about to pick one up from the shared queue, at the instant
      `Close` is called. Closed by a new per-runtime `inFlight
      sync.WaitGroup`, incremented when `cycle()` hands a claim to the
      pool and decremented once a pool worker finishes executing it:
      `TaskRuntime.Close()` waits it out (after stopping its own
      coordinator) before returning, so by the time the caller (M2-3b-1
      eviction, or shutdown) proceeds to close the database itself, no
      pool worker holds or will pick up a reference to it.
      `cmd/nextsqld/main.go` constructs the pool once, deliberately with a
      background (not the signal-aware server) context, with its `Close`
      deferred *before* every other close-related defer — so it runs
      *last*, strictly after every `TaskRuntime` submitting to it
      (primary via `srv.Close()`, every secondary via the `dbMgr`/cleanup
      defer) has already closed; documented as `TaskPool.Close`'s
      precondition. A drive-by investigation into the real `CANCEL TASK`
      path found `TaskRuntime.Cancel`/its `running` registry are dead code
      today — production cancellation already goes through
      `db.RequestTaskCancellation`/`db.taskCancels`, a separate,
      already-correctly-DB-scoped mechanism — left as-is, not removed
      (out of this increment's scope). Tests:
      `TestTaskPoolSharedAcrossTwoRuntimes` (two real databases share a
      one-worker pool, both succeed),
      `TestTaskRuntimeCloseAllowsSafeDBCloseWhilePoolShared` (mirrors real
      M2-3b-1 eviction: one runtime closes, its database closes right
      after, while the shared pool and another open database's runtime
      keep running — clean under `-race`). All green under `-race`:
      `internal/executor` (full), `internal/config`, `cmd/nextsqld`,
      `tests/integration`. **Live verification against real
      `nextsql`/`nextsqld` binaries**: `nextsqld` started with
      `task_workers=1` — a primary-database `EVERY '1s'` schedule ticked
      10 times over ~5s through the shared pool, while a second
      realm/database's independently-scheduled workflow, kept alive via a
      rapid-reconnect loop, also successfully executed through that same
      single shared worker — confirming the fan-out works in a real
      process. Docs: this section, §16, `docs/web/content/docs/config.md`
      (`task_workers`), `TODO.md`, `CHANGELOG.md`.
    - **M2-3b-3b — centralize the polling itself: one `CentralScheduler`
      enumerates every open database each tick instead of one poll loop
      per database** (landed 2026-09-03). New `executor.CentralScheduler`
      (`internal/executor/central_scheduler.go`) replaces every
      dbmanager-open database's own `TaskRuntime` with one process-wide
      `coordinate()`/`cycle()` loop that, each tick, asks a
      `DatabaseLister` — a plain function type, not a direct
      `dbmanager.Manager` reference (`dbmanager` already imports
      `executor`, so the reverse import would cycle) — for every currently
      open database, and claims/dispatches/submits each one's due work to
      the same shared `TaskPool` (M2-3b-3a). `cmd/nextsqld/main.go` bridges
      the two with a small closure around the new `dbmanager.Manager.
      Snapshot() []DBHandle`. Reduces polling goroutines from O(open
      databases) to O(1), on top of 3a's already-shared execution workers.
      `Snapshot` hands out a ref-held handle for every open entry, reusing
      `Acquire`/`release`'s existing refcounting rather than inventing a
      second concurrency primitive — a database with a scheduler-claimed
      task still in flight naturally can't be evicted (M2-3b-1) until the
      scheduler's own ref on it releases too, closing the "must coordinate
      with eviction" hazard this item's own prior writeup flagged, with
      zero new coordination code in `dbmanager` itself. `taskJob`
      (M2-3b-3a's shared-pool job type) was refactored to carry
      `(db, task, config)` directly instead of `*TaskRuntime`, so both
      `TaskRuntime.cycle` and `CentralScheduler.cycleOne` submit compatible
      jobs; the execution body moved to a shared `runClaimedTask` function.
      `TaskRuntime.running`/`Cancel` (already confirmed dead in production
      by 3a) were briefly kept working via an optional per-job `onStart`
      hook, then deleted outright in M2-3b-3c. **A second release-timing hazard, found and closed during
      this item's own design**: a claim submitted to the shared pool
      executes asynchronously, so releasing a `DBRef` the moment
      submission finishes (rather than the moment execution finishes) would
      let the database evict out from under a still-running job. Closed
      with a per-tick, per-database `sync.WaitGroup` plus one short-lived
      goroutine per database per tick that waits on it before releasing —
      cheap given ticks are infrequent (250ms default) and the open-database
      count is small and bounded — tracked by a new `CentralScheduler.
      inFlight`, mirrored on `TaskRuntime.inFlight`'s own guarantee, so
      `Close()` never returns while one is still pending. `cmd/nextsqld/
      main.go`: the primary now gets its own dedicated `TaskRuntime` only
      when there is no hosting registry at all; once one exists, one
      `CentralScheduler` covers the primary (via `dbMgr.Preload`) and every
      dbmanager-opened secondary alike, and the Opener's per-secondary
      cleanup closure no longer needs any task-runtime-specific ordering of
      its own. `CentralScheduler.Close()` is deferred immediately after
      it starts, inside the `hostingRegistry != nil` block (registered
      *after* the top-level dbMgr/secondary-cleanup defer, so it runs
      first — LIFO — and always finishes draining before `dbMgr`
      force-closes any database at shutdown). **Deliberately scoped out**:
      the `REQUIRE CLIENT KEY` lazy-open path's own dedicated primary
      `TaskRuntime` is untouched — a narrow, rare deployment shape
      (combining `REQUIRE CLIENT KEY` with hosting) that becomes
      redundantly (not incorrectly — claiming is transactionally exclusive)
      polled by both once that primary is later `Preload`ed. **A
      behavioral tradeoff, also flagged rather than silently left**: 3a's
      per-database `TaskRuntime` polled once immediately on construction,
      so opening even a very brief connection guaranteed at least one poll
      attempt; `CentralScheduler` has no per-connection synchronization —
      a database opened only for a very short, bursty request may see zero
      scheduling attempts before eviction. A realistically-held-open
      connection is unaffected (proven live below); only extremely bursty
      one-shot connections to a hosted secondary are affected, and only in
      degree (delayed, not lost), not correctness. Tests:
      `TestCentralSchedulerAcrossTwoDatabases`,
      `TestCentralSchedulerReleasesEveryRefEventually`,
      `TestCentralSchedulerCloseWaitsOutstandingRefs`,
      `TestStartCentralSchedulerValidatesArgs`; `dbmanager` gained
      `TestSnapshotEmptyWhenNothingOpen`/`TestSnapshotHoldsRefUntilReleased`.
      All green under `-race`: `internal/executor` (full, 121.4s),
      `internal/dbmanager`, `cmd/nextsqld`, `tests/integration`. **Live
      verification against real `nextsql`/`nextsqld` binaries**: the
      primary's `EVERY '1s'` schedule accumulated 16 rows over ~5s with
      zero dedicated primary `TaskRuntime` (confirming `CentralScheduler`
      alone drives it); the same rapid-reconnect-loop trick that worked
      for 3a's live check produced 0 rows for the second database here —
      exactly the documented tradeoff, reproduced live rather than only in
      theory — while a realistically-held-open 6-second connection to that
      same database then accumulated exactly 6 rows, confirming the
      underlying mechanism once the tradeoff's precondition is met. Docs:
      this section, §16, `TODO.md`, `CHANGELOG.md`.
    - **M2-3b-3c — retired the dead `TaskRuntime.Cancel`/`running` registry**
      (2026-09-03). Confirmed dead in production by M2-3b-3a (real
      cancellation flows through `db.taskCancels`, populated by
      `runClaimedTask` regardless of submitter) and had no non-test caller,
      so deleted outright rather than DB-scoped: `TaskRuntime.Cancel`, the
      `running map[string]context.CancelFunc` field, its `sync.Mutex` (which
      guarded only `running`), and the `taskJob.onStart` hook that fed it.
      `runClaimedTask` lost its now-unused `onStart` parameter.
      Behaviour-preserving — nothing production-real used any of it.

  Depends on M2-3a (landed). Not yet scheduled.

Replace the single database pointer with a bounded `DatabaseManager` that maps
authorized stable IDs to engine handles.

Required behavior (M2-3a delivers the first three bullets in reduced,
fixed-limit form; the rest is M2-3b):

- hard maximum registered and simultaneously open databases;
- single-flight open so concurrent connects do not duplicate recovery;
- bounded open/recovery queue and deadline;
- explicit reference counts for sessions, transactions, CDC, tasks, backup,
  maintenance, and replication;
- idle eviction only when no reference or durable work remains;
- close performs checkpoint/drain according to the durability contract;
- failed open is quarantined with bounded retry/backoff, not a tight loop;
- global memory is budgeted before per-database buffer pages are granted;
- background maintenance, scheduler, CDC, backup, and replication work uses
  central bounded pools rather than one unbounded goroutine set per database;
- overload results in bounded queueing, throttling, rejection, spill, or
  cancellation, never OOM.

Initial correctness-first policy may keep a small fixed open-database limit.
Scale is earned through measurement; it is not claimed from registration count.

## 10. Subscription Plans, Quotas, and Metering

The engine enforces durable plan policy but does not process payments.
Provisioning/control-plane software maps a commercial subscription to a
versioned NextSQL plan.

Minimum hierarchical limits:

```text
deployment hard safety ceiling
→ realm plan and overrides
→ database allocation
→ user/resource-group limits
```

Plan fields should include bounded values for:

- databases per realm;
- connections and concurrent queries;
- query queue depth and wait time;
- memory/buffer pages;
- CPU/worker concurrency;
- storage, WAL, temporary spill, and backup bytes;
- result rows/bytes and statement/transaction timeouts;
- workflows, schedules, task concurrency, and retained task history;
- CDC streams, lag, retention pins, and subscriber buffers;
- vector dimensions/index size and full-text maintenance work;
- backup frequency/retention and restore concurrency.

Plan changes are versioned, audited, and applied deterministically. Reducing a
limit defines whether existing work drains, is cancelled, or remains until its
lease ends. No limit may exceed deployment hard safety ceilings.

### 10.1 Storage caps

The registry manifest (`NSRM` v3) carries a `StorageCapBytes` on every realm and
every database (`0` = no cap). The hosting administrator sets them offline:

- `Registry.SetRealmStorageCap(realmID, bytes)` — realm-wide cap;
- `Registry.SetDatabaseStorageCap(realmID, databaseID, bytes)` — per-database
  cap;
- CLI: `nextsql hosting set-realm-cap` / `set-database-cap` / `show`.

Invariants enforced at write time and revalidated on decode: a non-zero
per-database cap may not exceed a non-zero realm cap, and a realm cap may not be
lowered below a per-database cap already set in that realm. Older `NSRM` v1/v2
manifests decode with both caps `0`; the encoder always emits v3. Setting a cap
is an overwrite — the new value replaces the old one (subject to the invariants);
`--cap-bytes 0` clears it; setting the same value is a no-op and does not spend a
registry generation.

**Enforcement.** `nextsqld` reads the realm and database caps at open time and
applies `EffectiveStorageCapBytes` (the smaller non-zero of the two) to the
engine's page allocator as a page-count ceiling
(`bytes / PhysicalPageSize`). Once the data file's logical high-water reaches
that ceiling, any operation that needs a **new** page — `INSERT`, a
row-splitting `UPDATE`, index or tree growth — fails with `nerr.Exhausted`
("storage cap exceeded"). `DELETE`, `ROLLBACK`, and in-place `UPDATE` keep
working because they reuse pages already on the freelist; freeing pages (dead
version cleanup + `ReclaimPages`) then lets subsequent inserts succeed again
without growing the file. The cap covers the main data file only — WAL and UNDO
are separate files with their own bounds. It is not persisted in the database
(it is re-derived from the registry every start), and it is not enforced in
`REQUIRE CLIENT KEY` mode's lazily-opened databases until that path is wired.

**Updating a cap.** The registry can only be written while `nextsqld` is
stopped: `set-realm-cap` / `set-database-cap` / `set-realm-root` take the
exclusive data-directory lock and fail with `Unavailable` against a running
deployment. So the update flow is: stop the server → run the `set-*-cap`
command (overwrites the value) → start the server (the new ceiling is applied at
open). A running-server control-plane path for live cap changes without a
restart is a follow-on.

**Advisory surfacing.** `system.quotas` (M3) exposes the storage caps
read-only, admin-only, and empty on a legacy/non-hosted deployment — the same
convention as `system.realms`/`system.databases`. One row per realm and one per
database from the registry manifest, with `cap_bytes` (that scope's own
configured cap) and `effective_cap_bytes` (`EffectiveStorageCapBytes` for a
database row, the realm cap for a realm row). The usage columns — `used_bytes`,
`pct_of_cap`, `over_cap`, gated by `usage_known` — are populated only for the
row matching the session's own connected realm+database, because no other
database's engine is reachable from a single connection; `used_bytes` is the
data-file logical high-water (`allocator.Next()` pages × `PhysicalPageSize`),
the same quantity the cap is enforced against. The view never errors and never
bounds anything: the authoritative over-cap signal is still the write-path
`nerr.Exhausted` rejection. Per-realm aggregate usage, a cross-database usage
roll-up, and metrics for "N% of cap" / over-cap alerts remain follow-ons.

**Authorization.** Two levels:

- The hosting/deployment admin (holds the registry root) sets any realm cap and
  any per-database cap via `SetRealmStorageCap` / `SetDatabaseStorageCap`, and
  delegates realm-root management with `SetRealmRootAuth(realmID, secret)` —
  which stores only `sha256(secret)` on the realm (`RealmRootAuthHash`, 32
  bytes, `NSRM` v3; all-zero = no delegation).
- A **realm-root secret holder** calls
  `SetDatabaseStorageCapAsRealmRoot(realmID, databaseID, bytes, secret)`. The
  secret is verified in constant time against the realm's hash; it authorises
  only per-database caps *in that one realm*, still bounded by the realm cap,
  and gives no path to the realm cap or any other realm. Fails closed when the
  realm has no delegation configured (`Forbidden`) or the secret is wrong /
  missing (`Unauthorized`).

Offline, the CLI still opens the registry with the deployment root, so the
realm-root secret is defence-in-depth plus the exact code path a future server /
reseller control plane uses to expose quota self-management to a realm owner
without deployment-level access. This maps onto the reseller tiering vocabulary:

| Tier | What is sold | Control surface |
| --- | --- | --- |
| **Daemon** | a whole standalone `nextsqld` instance | deployment registry root; all `nextsql hosting` verbs |
| **Realm** | one `nextsql hosting` realm (a subscription owning many databases) | a realm-root delegation secret — per-database caps in that realm only, under the realm cap; no registry root |
| **Nano** | a single database, connection only | that database's own SQL users/roles; no registry or realm access |

The realm-root delegation secret is the mechanism that lets a Daemon operator
resell Realm tiers without handing out the registry root; a Nano is just one
database inside some realm with no delegation of its own.

If usage is used for billing, it needs a durable append-only usage ledger with
stable event IDs and idempotent export. Lossy process metrics are suitable for
observability, not authoritative invoices. Billing-provider outages must not
enter transaction correctness paths. Existing service may use the last durable
policy for a bounded lease; new provisioning and privilege expansion fail
closed when authority is unavailable.

Suggested lifecycle policy:

```text
ACTIVE → GRACE/READ_ONLY (optional policy) → SUSPENDED → RETENTION → DELETE
```

Nonpayment must never immediately destroy data. Destructive deletion requires
an explicit retention policy, authorization, audit, backup policy, and exact
target resolution. Crypto-shredding remains a separate strongly confirmed
operation.

## 11. SQL and Administrative Surface

### 11.1 Deployment initialization

`nextsql init` initializes a deployment registry and may atomically bootstrap
one realm, one database, and one administrative principal:

```text
nextsql init \
  --data-dir /var/lib/nextsql \
  --realm customer-a \
  --database production \
  --realm-key-file /etc/nextsql/customer-a.root.key \
  --user admin \
  --password-file /run/secrets/nextsql-admin
```

The exact flags remain a CLI design decision. Required semantics are:

- creating a missing local root key remains explicit and reports its protected
  path without printing key material;
- deployment control keys and realm roots remain off the data volume;
- partial initialization records a restartable intent and never publishes an
  incomplete database as `ACTIVE`;
- re-running init against existing state either resumes the exact operation
  idempotently or fails with an actionable non-destructive error;
- init never overwrites an existing registry, database, key, auth store, or ACL;
- bootstrap credentials grant only the documented deployment/realm/database
  authority;
- machine-readable output returns stable IDs and safe connection fields, never
  passwords or root material.

Additional realms and databases are created through the authenticated
administrative interface. `nextsqld` starts from the deployment registry and
opens database engines lazily through bounded admission; it does not eagerly
open every registered database or start an unbounded worker set for each one.

### 11.2 Database-local SQL and administration

Keep normal SQL database-local. The first release does not support cross-
database object references.

`CREATE DATABASE name` should become a durable request in the current realm:

1. authorize realm-level database creation;
2. reserve a stable DatabaseID and operation ID;
3. replicate/persist `PROVISIONING`;
4. create a new identity, envelope, database, WAL, and catalog;
5. verify open/recovery;
6. publish `ACTIVE`;
7. return the stable operation result idempotently.

It remains forbidden inside a user transaction. The current sibling-file-only
`CREATE DATABASE` SQL statement is unaffected and still exists for
non-hosted/embedded use (no registry present); it must not be advertised as
hosting support and does not interact with anything below.

**M2-1, landed 2026-09-02, implements steps 2-6 above as an offline CLI
operation** (`nextsql realm create` / `nextsql database create` — not yet
the "authenticated administrative interface" over a live connection this
section describes, and not yet reachable by SQL `CREATE DATABASE` at all):
reserve a stable DatabaseID (`ID(identity.Database)`), persist
`PROVISIONING` in one registry generation, create the identity/envelope/
database/WAL/catalog at the ID-based `LayoutManaged` path, verify it opens,
publish `ACTIVE`. Idempotent: a repeated call with the same names recognizes
an in-progress or completed prior attempt and resumes/no-ops rather than
erroring or duplicating, so a crash between any two of these steps is
safely retryable. Step 1 (realm-level authorization) and step 7 (a
queryable stable operation record, `system.database_operations`) remain
open — this CLI is invoked with the same registry-root key as `nextsql
init`, equivalent to deployment-admin authority, not a separate authorized
principal. Live serving of the created database — steps that would make it
reachable over the wire protocol — is M2-2/M2-3.

**M3-1, landed 2026-09-04, implements `database suspend`/`resume`** from the
list below — the same offline, registry-root-key, exclusive-data-dir-lock
shape as M2-1's `create`, not yet the live administrative protocol either.
Unlike `create`, this pair is enforced, not just recorded: `nextsqld`'s
`dbmanager.Manager.Acquire` (via `hosting.Registry.Lookup`) refuses to route
any new connection to a suspended database once the process is restarted
with the updated registry.

**M3-3, landed 2026-09-04, implements `database drop`** — same offline shape
again. `StateDeleting`/`StateTombstoned` and `Lookup`'s fail-closed handling
of both already existed; this adds the missing physical half: transition to
`StateDeleting`, `os.RemoveAll` the managed database's whole ID-based
directory (db file + `.keys`/`.wal`/`.undo`/`.isolated` sidecars, all
colocated under it), transition to `StateTombstoned`. Idempotent and
resumable across a crash at any step. Scoped to `LayoutManaged` databases
and never the deployment default (§16 M3-3 has the full writeup). `realm
suspend`/`resume`, `database rename`, and every `list`/`status`/`plan`/
`operation` verb below remain unimplemented (M3-2, M2-4b-2, §16).

Administrative surface should include native, machine-readable equivalents of:

```text
nextsql realm create|list|status|suspend|resume
nextsql database create|list|status|rename|suspend|resume|drop
nextsql plan assign|show
nextsql operation status|cancel
```

Realm creation and destructive lifecycle operations should use an explicit
administrative protocol, not filesystem manipulation. `system.realms`,
`system.databases`, `system.database_operations`, `system.usage`, and
`system.quotas` must apply RBAC and redaction. Manager consumes those official
interfaces.

## 12. Transactions, WAL, Recovery, and Replication

Each user transaction belongs to exactly one DatabaseID. WAL records, UNDO,
LSNs, snapshots, locks, idempotency records, and cache invalidations are scoped
to that ID. No commit may be acknowledged before the selected database's
durability and configured quorum boundary.

For the first HA implementation, prefer one bounded deployment-level Raft
group that replicates registry operations and database-tagged commands to the
same fixed voter set. This avoids an unbounded Raft group and heartbeat set per
database. It accepts cross-database head-of-line blocking as a correctness-first
v1 tradeoff. Resource admission prevents one realm from monopolizing proposal
capacity.

Required command envelope:

```text
format version
DeploymentID
RealmID
DatabaseID or registry scope
operation ID / idempotency key
payload type and bounded payload
integrity/encryption metadata
```

All voters deterministically create the same registry state and DatabaseID.
Local file identities and encryption material must follow a documented
replication/key-distribution design; do not copy raw keys in ordinary Raft
payloads. Database activation requires the defined quorum readiness boundary.

Test leader kill and network partition during every lifecycle transition,
including before/after registry commit, file creation, initial checkpoint,
activation, suspension, restore publication, and deletion tombstone. A failed
follower must repair one database without exposing or corrupting another.

Future placement or multiple Raft groups require a separate bounded design and
must not be implied by this release.

## 13. Backup, Restore, PITR, Export, and Deletion

- Backup and PITR target one stable DatabaseID by default.
- Backup manifests include DeploymentID, RealmID, DatabaseID, database
  identity, key metadata, plan-independent format versions, and source LSN.
- Registry/security backup is separate and required for full deployment
  disaster recovery.
- Restore-as-new creates a new DatabaseID and rewraps under the destination
  realm key hierarchy.
- In-place restore requires suspension, no live references, exact target
  confirmation, leader authority, and atomic publication.
- Cross-realm restore requires explicit source and destination authorization
  and cannot reuse source grants automatically.
- Exported logical data contains no credentials, keys, roles, or subscription
  policy unless a separately authorized metadata export is requested.
- Delete first blocks new work, drains/cancels according to policy, verifies
  retention/backup requirements, publishes a tombstone, and reclaims through a
  recoverable staged workflow.
- Crypto-shred is separately authorized and clearly irreversible.

## 14. Observability and Operations

Every metric and audit event carries stable deployment, realm, and database
labels internally. Public labels must be bounded and must avoid high-cardinality
user input where unsafe.

Expose at minimum:

- registered/open/failed/suspended database counts;
- open/recovery/eviction latency;
- connections, queued queries, cancellations, and rejections by scope;
- buffer, CPU/worker, storage, WAL, temp, task, and CDC usage versus quota;
- lifecycle operation state and failure reason with secret redaction;
- per-database recovery, checkpoint, backup, replication, and lag state;
- usage-ledger export health when authoritative metering is enabled.

Operational actions are authorized server-side and audited. Manager never
reads raw registry, database, page, WAL, key, or auth files.

## 15. Compatibility and Migration

### 15.1 Existing single-database deployments

Provide an offline, restartable migration with preflight and rollback intent:

1. stop/drain the old server and acquire an exclusive data-directory lock;
2. verify database, keystore, auth, ACL, audit, WAL, and backup prerequisites;
3. create and fsync a versioned migration intent;
4. assign a default RealmID and preserve the existing DatabaseID/file identity;
5. move the database and sidecars into the ID-based layout using same-filesystem
   atomic renames where available;
6. create realm auth/ACL state and the deployment registry;
7. fsync files and directories, mark migration complete, and reopen/verify;
8. preserve rollback metadata until explicitly finalized.

Rollback is supported only until a documented point of no return, such as
creation of additional realms/databases or a new-format registry mutation.
The tool must never delete the old layout merely because a later step failed.

Protocol v1 clients route to the configured default realm/database during a
documented compatibility window. Protocol v2 is required to select non-default
resources.

### 15.2 Existing sibling files from `CREATE DATABASE`

Do not auto-adopt files discovered beside `nextsql.db`. Provide an explicit
inspect/adopt command that validates identity, encryption provider, corruption,
name collision, permissions, and recovery state before registering a file.
Unknown files remain untouched.

## 16. Delivery Sequence

This work is not currently an authoritative phase in `TODO.md`. If accepted,
add a dedicated multi-database hosting gate after Phase 27 workload governance
and before Phase 28 Manager relies on database lifecycle APIs. Phase 25
identity/security and Phase 26 system introspection are dependencies. P16 and
the existing earlier release gates retain priority.

Implement only the smallest coherent increment at each milestone.

### M0 — Adopt the contract

- Approve terminology, non-goals, isolation model, key hierarchy, HA model,
  limits, and compatibility policy.
- Update `PROJECT.md`, `TODO.md`, `ROADMAP.md`, protocol docs, security docs,
  storage-format docs, and capability registry.
- Assign persistent/protocol format versions and feature capability names.
- Mark current `CREATE DATABASE` behavior accurately until replacement.

Exit: authoritative documentation agrees; no implementation claim is made.

### M1 — Versioned local registry and migration foundation

- Implement encrypted/authenticated deployment registry and decoder fuzzing.
- Add stable DeploymentID/RealmID/DatabaseID and lifecycle records.
- Add data-directory lock, migration intent, crash recovery, and default
  realm/database adoption for existing deployments.
- Keep serving one default database only.

Exit: old single-database state migrates and rolls back safely; registry
corruption fails closed; restart/crash/fuzz tests pass.

### M2 — Single-node multi-database routing

Decomposed 2026-09-02 into four independently-gated sub-increments
(`TODO.md` "Cross-cutting track — Multi-database hosting / subscription
isolation" carries the authoritative checklist and status):

- **M2-1 — Registry realm/database creation primitives.** ✅ Landed
  2026-09-02. `Registry.CreateRealm`/`CreateDatabase` (`internal/hosting`),
  `nextsql realm create`/`nextsql database create` CLI. Registers a new
  managed database (reserved stable ID, durable `PROVISIONING` →
  create/verify-open → `ACTIVE`, idempotent crash-safe retry) at the
  already-defined `LayoutManaged` path. `nextsqld` does not yet open or
  serve it — that is M2-3.
- **M2-2 — Hello realm field (additive, protocol-compatible)** (2026-09-02).
  `Hello.Realm`, a new opt-in trailing field (not a frame-version bump,
  mirroring `NSCT`'s version-gated trailing-field pattern); `nextsqld`
  extended its existing single-name Hello check with a parallel flat-string
  realm check (`Server.Realm`, compared by equality against the one realm
  the process serves) — **not yet a live `hosting.Registry` lookup**, that
  remains M2-3 once a process can serve more than one realm/database at
  once. All 6 official drivers updated (Go/PHP/JS-shared[Bun+Deno]/Node/
  Python/Ruby), each gaining an optional `Realm`/`realm` config field
  emitted on the wire only when non-empty, so an unconfigured client's
  Hello is byte-identical to the pre-realm shape. Verified live against a
  real `nextsqld` with two independent drivers (Go, Python).
- **M2-3 — Bounded DatabaseManager and per-connection routing.**
  Decomposed 2026-09-02 into M2-3a/M2-3b (see §9) after a scoping
  investigation found the full spec spans subsystems with zero existing
  refcounting/pooling infrastructure.
  - **M2-3a — manager exists, small fixed open-database limit, connections
    route through it, Phase 27 monitors become per-DB** (2026-09-02).
    `internal/dbmanager.Manager`: keyed by registry database ID, hand-rolled
    single-flight open, bounded to a small fixed limit with no eviction.
    `protocol.Server.Databases` is additive alongside the unchanged
    `DB`/`Tasks` fields — nil means every connection uses the pre-M2-3a
    path unchanged. `nextsqld`'s `Opener` opens an additional registered
    `LayoutManaged` database single-node only (no Raft/archiver for
    secondaries). A real routing-order bug was caught by the new
    end-to-end test and fixed: database resolution had to move before
    `TypeReady` is sent, since that frame is the wire protocol's definitive
    success signal and nothing is read after it. Live-verified against a
    real `nextsqld` with a real `nextsql database create`-provisioned
    second database — see `TODO.md`'s log entry for two further real bugs
    this caught (secondary-database key material needs the same
    root-key-unlocks-an-envelope indirection as the primary; envelope/database
    close ordering).
  - **M2-3b — full §9 spec.** Decomposed 2026-09-02 into M2-3b-1/2/3 (see
    §9) after a scoping investigation found very different risk levels per
    piece — and found sessions/CDC/tasks already have live counters to
    reuse, correcting §9's own earlier "none of these expose a ref today."
    - **M2-3b-1 — connection/session refcounting + idle eviction +
      open-failure quarantine** (landed 2026-09-02). A secondary database
      now closes when its last connection disconnects and reopens cleanly
      on demand; the Preloaded primary is pinned and never evicted. See §9
      for the full writeup.
    - **M2-3b-2 — global memory budget** (landed 2026-09-03). See §9 for
      the full writeup.
    - **M2-3b-3a — shared bounded worker pool, per-DB polling kept**
      (landed 2026-09-03). See §9 for the full writeup.
    - **M2-3b-3b — centralize polling itself: one `CentralScheduler`
      enumerates every open database each tick** (landed 2026-09-03). See
      §9 for the full writeup.
    - **M2-3b-3c — retired the dead `TaskRuntime.Cancel`/`running`
      registry** (landed 2026-09-03). See §9. With this, M2-3b is complete.
- **M2-4 — Realm-scoped auth, database-scoped `CONNECT`, system views.**
  **Dependency corrected 2026-09-02**: the "depends on M2-1..3" note
  predates M2-3's own split and was overbroad — access control/
  introspection (M2-4) and resource budgeting/task-pool architecture
  (M2-3b-2/3) are orthogonal concerns, confirmed by direct reading of §5.2
  and the M2-3b-2/3 scope. M2-4 only needs M2-1 (registry, to resolve
  `RealmID`/`DatabaseID`), M2-2 (`Hello.Realm`), and M2-3a (routing exists
  to authorize against) — all landed; M2-3b-1/2/3 are not prerequisites.
  Decomposed into three further sub-increments after a scoping
  investigation found the same "very different sizes" shape M2-3 and
  M2-3b each had:
  - **M2-4a — `system.realms`/`system.databases` read-only introspection
    (LANDED 2026-09-02).** `internal/system/schema.go` registers the two
    tables; `internal/executor/system.go` dispatches to new
    `systemRealmsRows`/`systemDatabasesRows`, following
    `systemResourceGroupsRows`'s admin-only gate exactly — deployment
    topology across realms is not tenant-visible data. The plumbing gap
    (neither `protocol.Server` nor `executor.Session` held a
    `hosting.Registry` reference) is closed: `Server.HostingRegistry` field
    → `Session.SetHostingRegistry` setter → wired in `serveConn` alongside
    the existing `SetACL`/`SetAudit`/`SetAuth` calls, same shape as those
    setters; `cmd/nextsqld/main.go` sets `srv.HostingRegistry` alongside
    the pre-existing `srv.Database`/`srv.Realm` assignments. A nil registry
    (every legacy/non-hosted deployment) makes both views degrade to empty
    rows, never an error. `hosting.State`/`hosting.Layout` have no
    `String()` method, so two small local helpers
    (`hostingStateName`/`hostingLayoutName`) map them to the same
    lowercase-snake strings used elsewhere in `system.*`. Tests:
    `internal/executor/hosting_system_test.go` (nil-registry empty-rows
    case; RBAC + content case against a real `hosting.Registry`). Live-
    verified against a real `nextsqld` with a real two-database deployment
    (`nextsql init` + `nextsql database create`): admin sees the real
    registry contents, a `CONNECT`-only non-admin sees zero rows on both
    views. See `TODO.md` log #74 for the full writeup.
  - **M2-4b — realm-local `auth.Store`/`security.ACL` + the
    `(RealmID, PrincipalID, DatabaseID, privilege, scope)` authorization
    tuple.** A dedicated scoping investigation (2026-09-02) confirmed the
    call-site count (~10 non-test sites: `internal/executor/session.go`'s
    `acl`/`users` fields + setters, `internal/protocol/server.go`'s
    `Server.Auth`/`ACL` fields and `serveConn`'s verify/`AllowedScoped`
    calls, `internal/executor/task.go`+`task_runtime.go`,
    `cmd/nextsqld/main.go`'s `auth.OpenOrCreate`/`startEmbeddedAuthBroker`,
    `cmd/nextsql/main.go`'s CLI-side `auth.OpenOrCreate`) and found two
    structural facts that sharpen the earlier scoping:
    - **`auth.Store` cannot support §5.2's "same username independently in
      two realms" as-is** — it's a flat `map[string]record` keyed only by
      username, not a file-layout question alone. Two real options: (i)
      composite-key the existing single file by `(RealmID, username)` — no
      new file-layout/lifecycle infrastructure, a version bump in the same
      shape `auth.Store` already used once (`fileVersionV1`→`fileVersion=2`,
      PBKDF2→Argon2id, dual-decode old/new); or (ii) fully separate
      per-realm files under `realms/<RealmID>/security/` per §7's literal
      layout, which needs new bounded-open/eviction infrastructure (a
      `dbmanager`-shaped manager for realm auth stores, since realm count
      can scale like database count) — a real blast-radius/crypto-shred
      tradeoff against implementation cost, not a default to guess past.
    - **A previously-unflagged concrete finding**: `cmd/nextsqld/main.go`
      always sets `srv.Realm` to the one hosted deployment's default realm
      name, so `serveConn`'s existing flat-equality realm check currently
      rejects any `hello.Realm` other than that one — meaning
      `dbmanager.Manager.Acquire`'s `realmName` parameter, despite already
      accepting arbitrary realm names since M2-3a, is **unreachable with
      any other value today**. Every current deployment is "multi-database
      within one fixed realm," not yet multi-realm. M2-4b is therefore a
      genuine prerequisite for real multi-realm routing to ever activate,
      not only an authorization nicety.
    - `security.ACL`'s `Grant{Grantee, Priv, Scope, Object}` has no
      `RealmID` field either; adding one (zero value = deployment-wide,
      backward compatible) is a precedented format bump, same shape as
      above — not a novel problem for this codebase.
    - `serveConn` already knows `hello.Realm` immediately after decode,
      well before the password/ACL checks — inserting realm-aware
      authorization there is small and localized, not scattered; the
      `HostingRegistry == nil` legacy-fallback convention (already
      established for M2-3a/M2-4a) extends cleanly to "today's single
      global `Store`/`ACL`, byte-for-byte unchanged."
    - `internal/authbroker` already threads a `Realm` field through token
      *minting* (`exchangeRequest.Realm` → `TokenMintRequest.Realm` →
      `TokenClaims.Realm`); the gap is narrower than originally scoped —
      `serveConn`'s token-claim *verification* path checks `claims.Database`
      but never `claims.Realm` (currently harmless only because of the
      single-pinned-realm finding above). Broker realm-awareness does not
      need its own deep scoping pass; it's a small verification-side check.

    Decomposed into:
    - **M2-4b-1 — composite-key single file (LANDED 2026-09-02).** The
      user chose this over M2-4b-2's per-realm-file layout. `auth.Store`/
      `security.ACL` each gained a full set of `*InRealm` sibling methods
      (`VerifyInRealm`, `HasInRealm`, `UpsertInRealm`, `DeleteInRealm`,
      `SnapshotInRealm`; `GrantInRealm`, `RevokeInRealm`,
      `CreateRoleInRealm`, `DropRoleInRealm`, `GrantRoleInRealm`,
      `RevokeRoleInRealm`, `AddUserInRealm`, `DropUserInRealm`,
      `AllowedInRealm`, `AllowedScopedInRealm`, `RolesForInRealm`) —
      every existing flat method (`Verify`, `Grant`, `Allowed`, ...) became
      a one-line `hosting.ID{}` wrapper, so every pre-existing caller and
      test outside the two packages' own suites needed **zero changes**.
      `hosting.ID{}` means deployment-wide: for `auth.Store` it *shadows*
      (a realm-scoped identity of the same name takes precedence — identity
      is exclusive); for `security.ACL` ordinary grants/roles it *unions*
      (authorization is additive) — **except** `PrivAdmin+ScopeCluster`/
      `PrivAdmin+ScopeAdmin`, which always normalize to and match only at
      `hosting.ID{}` regardless of the realm requested, a design decision
      made during grounding (not in the original scoping) to prevent a
      realm-scoped cluster-admin grant from newly leaking cross-realm
      visibility into `isAdmin()`-gated views like M2-4a's
      `system.realms`/`system.databases` — closing a real §5.4 "cross-realm
      leakage tolerance is zero" regression risk by construction. On-disk
      formats: `auth.Store`'s `fileVersion` 2→3, `security.ACL`'s
      `aclVersion` 1→2 (its first-ever bump), both dual-decoding every
      older format with an implicit `Realm: hosting.ID{}`. New
      `hosting.Registry.LookupRealm(realmName)` resolves a realm name to
      its ID without requiring a database name (needed since identity must
      resolve before routing decides which database to open). New
      `Session.realmID`/`SetRealmID`; `authAllowed`'s one call to
      `AllowedScopedInRealm(s.realmID, ...)` made the entire executor RBAC
      surface realm-aware through a single chokepoint. `serveConn` resolves
      `realmID` via `LookupRealm` right after the existing flat
      `s.Realm`/`hello.Realm` precheck, before any password work; added the
      `claims.Realm` mismatch check parallel to the existing
      `claims.Database` one. `authbroker.RoleMembershipFunc` gained a
      leading realm-name parameter (forced by `RolesFor`'s own signature
      change), kept the package decoupled from `internal/hosting` (name
      only, never an ID) — `cmd/nextsqld/main.go`'s `RoleMembership`
      closure resolves it. Live-verified against real `nextsql`/`nextsqld`
      binaries, and a new `tests/integration/multirealm_auth_test.go`
      proves real cross-realm password isolation over the wire using two
      independent single-database `protocol.Server`s sharing one
      `*auth.Store`/`*security.ACL` — two servers, not one multi-realm-
      routed server, since the `srv.Realm`-pinning limitation this
      increment's own scoping surfaced is not itself fixed by M2-4b-1. See
      `TODO.md` log #76 for the full writeup.
    - **M2-4b-2**: separate per-realm files under `realms/<RealmID>/
      security/` (§7's literal layout) + a bounded open/eviction manager
      for them — only worth it if/when realm count needs real
      isolation-at-rest (crypto-shredding one realm's credentials
      independently of others). Not required for M2-4b-1's correctness.
      Not yet scheduled.
    - **M2-4b-3**: realm-aware embedded OIDC broker beyond what M2-4b-1
      already did (deeper per-realm IdP profile/policy config) — kept
      separate since the minting side already carried `Realm` before this
      increment and the verification-side gap M2-4b-1 found is now closed.
      Not yet scheduled.
  - **M2-4c — `system.database_operations`.** Needs new operation-history
    tracking in `internal/hosting` that doesn't exist yet (realms/
    databases only carry a *current* `State`, not a transition log) — not
    a pure read-through like M2-4a, and not needed by M2-4a/b. Best folded
    into whichever future M3 lifecycle work first introduces an
    operation-history concept, rather than built standalone here. Not yet
    scheduled.
- **M2-5 — multi-realm routing activation (LANDED 2026-09-02).** M2-4b-1's
  own scoping had found `dbmanager` and its `Opener` were already fully
  realm-agnostic since M2-3a, but `serveConn`'s flat `s.Realm != "" &&
  hello.Realm != "" && hello.Realm != s.Realm` equality precheck
  unconditionally rejected any Hello naming a realm other than the one
  `cmd/nextsqld/main.go` pins `srv.Realm` to at startup — meaning
  M2-4b-1's own `LookupRealm`-based resolution was itself still
  unreachable for any second realm. Fixed by scoping that precheck to
  `s.HostingRegistry == nil`, mirroring the exact existing pattern already
  used for the analogous `s.databaseManager() == nil` guard on the
  `s.Database` check immediately below it — once a `HostingRegistry` is
  configured, `LookupRealm` becomes the sole, authoritative,
  registry-backed realm check. `srv.Realm` is unchanged in meaning: still
  the fallback used when a Hello omits `Realm`. Live verification surfaced
  a real, separate companion gap: `internal/cli/connect.go`'s
  `ServerConfig` resolved `Settings.Realm` (already read from `--realm`/
  `NEXTSQL_REALM_NAME`) but never set it on the driver `Config` — silently
  dropping it for every server-mode CLI command, not just `nextsql exec`.
  Fixed the missing field and added an explicit `--realm` flag to
  `nextsql exec`. New `tests/integration/multirealm_routing_test.go`
  proves real cross-realm database routing/isolation through one server
  (`TestMultiRealmRoutingThroughOneServer`) and that an unknown realm is
  still rejected cleanly (`TestUnknownRealmStillRejectedCleanly`).
  Live-verified against real `nextsql`/`nextsqld` binaries with two real
  realms. Noted but deliberately out of scope: §5.2's "pre-authentication
  errors must not disclose whether another realm... exists" is not met
  today (an unknown-realm probe returns a distinguishing `NotFound` before
  any password check) — not newly introduced by this fix (the same
  category of exposure already existed for the one pinned realm name and
  for unknown database names since M2-3a), but now reachable for realm
  names too; a proper fix needs a deliberate pre-auth error-redaction pass
  across realm/database/username together, not a side effect of this
  activation fix. See `TODO.md` log #77 for the full writeup.

- **M2-6 — Pre-authentication realm/database existence-disclosure hardening**
  (2026-09-02). Closes the gap M2-5 flagged as deliberately out of scope.
  `internal/protocol/server.go`'s `serveConn` had two flat-string prechecks
  (`s.Realm`/`s.Database` mismatch, legacy non-hosted paths) and one
  `HostingRegistry.LookupRealm` call, all three returning a distinguishing
  `NotFound` immediately after the `Hello` frame — before `HelloOK` is even
  sent, let alone a password read — letting an entirely unauthenticated
  peer enumerate valid realm/database names with zero credentials.
  Username enumeration was already closed (`auth.Store.VerifyInRealm`
  already ran a dummy Argon2id comparison for an unknown user and returned
  the same generic `Unauthorized "authentication failed"` either way); this
  extends the identical treatment to realm/database names. Fix: none of the
  three checks return early any more. A new `identityOK bool` records
  whether the requested realm/database actually resolve; the connection
  still proceeds through the full `HelloOK` → `Auth` round trip regardless,
  runs the real (or dummy) password verification exactly as it would for a
  valid realm, and only *after* that folds `!identityOK` into the same
  generic `Unauthorized "authentication failed"` outcome a wrong password
  produces — same nerr code, same message, same cost (the real/dummy hash
  comparison already ran), so an unauthenticated prober cannot distinguish
  "wrong realm"/"wrong database" from "wrong password" by response content
  or timing. `realmID` still resolves to *some* value (`hosting.ID{}` on an
  unresolvable realm) purely so the verification call has something to run
  against; `identityOK`, not `realmID`'s value, decides the outcome, so an
  unknown realm can never authenticate via `VerifyInRealm`'s existing
  same-name deployment-wide fallback. Deliberately out of scope: the
  post-authentication database-not-found rejection from `dbmanager.Manager.Acquire`
  (reachable only with already-valid credentials in the resolved realm — a
  materially weaker, already-accepted disclosure since M2-3a, not a
  credential-free oracle) and the mTLS service-identity checks (a separate
  identity mechanism, unrelated to realm/database/username confidentiality).
  Tests: `tests/integration/protocol_test.go` `TestRealmMismatchRejected`
  and `TestUnknownRealmStillRejectedCleanly` updated to assert `Unauthorized`
  (previously `NotFound`) — both deliberately use a *correct* password so
  the rejection can only be attributed to the realm name, proving the fix
  rather than merely not regressing; new
  `TestDatabaseNameMismatchRejectedGenerically` gives the legacy
  single-pinned-database precheck its first-ever coverage (same
  correct-password-but-wrong-name shape). All green under `-race`:
  `internal/protocol`, `tests/integration` (full), `internal/auth`,
  `internal/security`, `internal/dbmanager`, `internal/hosting`,
  `cmd/nextsqld`, `cmd/nextsql`. See `TODO.md` log #78 for the full writeup.

Exit (unchanged): at least two realms with identical user/database names
remain isolated across concurrent connect, prepared, cancel, DDL, DML,
restart, and recovery tests; protocol v1 default routing remains
compatible. Not yet met — M2-5/M2-6's isolation proof is real-database-level
across two genuinely different realms (two distinct databases, table
visibility, over one live server) but not yet the full adversarial matrix
(§17) this exit criterion describes. Pre-authentication existence
disclosure for realm/database names is now closed (M2-6); the
post-authentication database-not-found case noted above remains open.

### M3 — Independent operational lifecycle

Decomposed 2026-09-04 (mirroring M2's own §9/§16 "smallest coherent
increment" decomposition discipline), now that M2 is complete, into:

- **M3-1 — suspend/resume lifecycle enforcement (LANDED 2026-09-04).**
  `hosting.Registry.SetDatabaseState`'s validated `StateActive` ↔
  `StateSuspended` transition already existed (`internal/hosting/registry.go`,
  landed with M0/M1's state machine), but nothing actually enforced it: a
  suspended database's registry row was durable and visible in
  `system.databases`, but `dbmanager.Manager.Acquire` (the sole production
  call site of `hosting.Registry.Lookup`, §9) would still resolve and open
  it exactly like an Active one — "suspend" recorded intent without ever
  blocking a connection. Fixed at the one place that matters: `Lookup` now
  fails closed for any non-Active database (`StateProvisioning`/`StateFailed`
  → `Unavailable`, `StateDeleting`/`StateTombstoned` → `NotFound`,
  `StateSuspended` → `Unavailable "database suspended"`) and, defensively,
  for any non-Active realm the same way (`CreateDatabase` already checked
  `realm.State` for the same reason; no `SetRealmState` exists yet to
  actually reach that path today — realm suspend/delete stays the separate,
  already-flagged M2-4b-2 gap). Every other `Registry` method that reads
  `State` (`CreateRealm`/`CreateDatabase`/`SetDatabaseState`/the CLI's
  `Manifest()`-walking resolvers) deliberately keeps seeing every state,
  transient ones included — they need that to make their own idempotency
  decisions; only connection routing rejects a non-Active pair. Safe to be
  message-specific post-auth: `dbmanager.Manager.Acquire` runs after
  `TypeAuthOK` (`internal/protocol/server.go`), the same "post-authentication
  ... a materially weaker, already-accepted disclosure" precedent M2-6
  already established for the pre-existing not-found case. New
  `nextsql database suspend`/`resume --realm NAME --database NAME --confirm`
  (§11.2), following set-realm-cap/set-database-cap's exact offline pattern
  (`openHostingRegistryForCLI`'s exclusive data-dir lock — fails
  `Unavailable` against a running `nextsqld`; a state edit is an overwrite,
  applied on the next restart, the same documented shape as a live cap
  edit without a restart). `security.ActionDatabaseSuspend`/`ActionDatabaseResume`
  audit actions. Tests: `internal/hosting` —
  `TestLookupRejectsNonActiveDatabaseState` (table across all five
  non-Active states), `TestLookupRejectsNonActiveRealm`;
  `TestLookupResolvesRealmAndDatabaseCaseInsensitively` updated to activate
  the database first (it used to pass by accident, resolving a
  still-`StateProvisioning` database — the exact bug this increment closes).
  `cmd/nextsql` — `TestDatabaseSuspendResumeCLI` (missing `--confirm`,
  deployment-lock rejection, unknown realm, and — the enforcement proof,
  not just persistence — a real `hosting.Registry.Lookup` call rejecting
  the suspended pair and succeeding again once resumed). All green under
  `-race`: `internal/hosting`, `cmd/nextsql`, `internal/dbmanager`,
  `cmd/nextsqld`, `internal/protocol`, `internal/security`,
  `tests/integration`. **Live-verified end to end** against real
  `nextsql`/`nextsqld` binaries: a two-realm deployment, `INSERT` against
  `acme/prod` succeeds while Active; stop the server, `database suspend`,
  restart — `acme/prod` now rejects with `unavailable: database suspended`
  while the unrelated default database keeps working normally; `database
  resume` + restart — `acme/prod` accepts connections again and its
  earlier row survived untouched. See `TODO.md` log #112 for the full
  writeup.
- **M3-2 — rename.** Not started. A durable name change for an existing
  realm or database, distinct from its stable `ID` (every `ManagedDatabasePath`/
  audit/registry reference is already ID-based, so renaming should not by
  itself require touching physical files) — needs its own scoping pass for
  in-flight-connection behavior (an open `dbmanager` entry is keyed by ID,
  not name, so a rename likely needs no eviction, but that must be verified,
  not assumed) and collision handling against another realm/database
  already holding the target name.
- **M3-3 — drop/tombstone (LANDED 2026-09-04).** `StateDeleting`/
  `StateTombstoned` already existed in the state machine and `CanTransition`
  already allowed `StateActive/StateSuspended/StateProvisioning/StateFailed →
  StateDeleting → StateTombstoned`, and M3-1's `Lookup` fix already failed
  closed for both (`NotFound`) — the gap was exclusively the *physical*
  side: nothing reclaimed a tombstoned managed database's on-disk files.
  New `nextsql database drop --realm NAME --database NAME --confirm`
  (`cmd/nextsql/main.go` `dropDatabase`), the same offline
  `openHostingRegistryForCLI` exclusive-lock pattern as `suspend`/`resume`/
  `set-*-cap` (fails `Unavailable` against a running `nextsqld`, so there is
  never a live connection to evict — the server cannot be up while this
  runs). Flow: reject the deployment default realm/database pair and any
  non-`LayoutManaged` database (`LayoutLegacyDefault` lives directly at
  `DATA-DIR/nextsql.db` with no per-ID directory to safely reclaim, and
  every tool that omits `--realm`/`--database` assumes that path exists) →
  `SetDatabaseState(..., StateDeleting)` → `os.RemoveAll` the whole
  `ManagedDatabasePath` parent directory (the db file plus its `.keys`
  keystore, `.wal`/`.undo` directories, and `.isolated` integrity-registry
  sidecar are all colocated under `realms/<RealmID>/databases/<DatabaseID>/`,
  so one `RemoveAll` reclaims every artifact) → `SetDatabaseState(...,
  StateTombstoned)`. Idempotent and crash-resumable at every step: a
  database already `StateTombstoned` reports success without erroring
  (files were already reclaimed by an earlier, possibly-interrupted run);
  `StateDeleting → StateDeleting` is a valid no-op transition, so a run that
  crashed after the first state write but before reclaiming files resumes
  cleanly on retry (`os.RemoveAll` is itself idempotent). New
  `security.ActionDatabaseDrop` audit action. **Deliberately out of scope
  for this slice** (unchanged open items): rename (M3-2), realm-level
  delete (part of the still-open M2-4b-2 realm-suspend gap), and reclaiming
  a database's live buffer-pool/`TaskRuntime` footprint if it happens to be
  open in `dbmanager` at the moment of deletion — not reachable today
  because the offline lock already requires the server to be down for this
  command to run at all; a future *online* drop/suspend control-plane
  operation would need to address it. Tests: `internal/security`
  (`ActionDatabaseDrop` constant); `cmd/nextsql/realm_database_test.go` —
  `TestDatabaseDropCLI` (missing `--confirm`, deployment-lock rejection,
  unknown realm leaves the managed directory untouched, a successful drop
  tombstones the registry record *and* removes the on-disk directory *and*
  makes `Lookup` fail `NotFound`, and a repeated drop of an
  already-tombstoned database is a no-op success), 
  `TestDatabaseDropRejectsDeploymentDefault` (the default database's state
  and its `DATA-DIR/nextsql.db` file are both untouched by a rejected drop).
  All green under `-race`: `cmd/nextsql`, `internal/hosting`,
  `internal/dbmanager`, `internal/security`, `internal/protocol`,
  `cmd/nextsqld`, `tests/integration`; `go build ./...` / `go vet ./...`
  clean. **Live-verified end to end** against real `nextsql`/`nextsqld`
  binaries: a two-realm deployment, `CREATE TABLE`/`INSERT`/`SELECT`
  against `acme/prod` while Active; stop the server, inspect the on-disk
  tree (db file + `.keys`/`.wal`/`.undo` present under the ID directory),
  `database drop --confirm` → registry state 5 (Tombstoned), the whole
  `databases/<DatabaseID>` directory gone; restart — the unrelated default
  database keeps working normally (`CREATE TABLE`/`SELECT` succeed) while
  `acme/prod` now rejects every statement with `nextsql: nextsql not_found:
  nextsql: database deleted`. See `TODO.md` log #114 for the full writeup.
- **M3-4 — per-database independent WAL/UNDO/cache/idempotency/task/CDC/
  maintenance/temp-spill scope.** Largely already true as a side effect of
  M2-3a's design (every managed database is its own `executor.DB`/`Engine`
  with its own WAL, UNDO, buffer pool, and `TaskRuntime`/idempotency store,
  opened and closed independently) — the real remaining gap, not yet
  scoped, is backup/restore/PITR/import/export: today's `nextsql backup`/
  `restore`/`export`/`import` all take a single `--data-dir`, with no
  concept of "which managed database inside this deployment," and no
  per-database WAL archiver is ever installed for a managed (non-primary)
  database (`internal/hosting`'s M0/M1 registry-storage-caps note already
  flags this same limitation from the caps angle).
- **M3-5 — key rotation, rewrap, restore-as-new, and crypto-shred
  boundaries.** Not started; M3-3's physical-reclamation shape now exists
  (`os.RemoveAll` of the managed database's ID directory, including its
  `.keys` keystore) but this item is specifically about doing that *and*
  proving the key material is unrecoverable (crypto-shred), plus rotation/
  rewrap/restore-as-new for a *live* database — still needs its own scoping
  pass.

Exit: one database can crash, restore, rotate, suspend, or be deleted without
incorrect results, key use, state loss, or service interruption in another.
Not yet met — M3-1 (suspend) and M3-3 (drop/tombstone, offline) are landed;
rename, crash/restore/rotate (M3-2, M3-4, M3-5) remain open.

### M4 — Workload governance and subscription enforcement

- Integrate Phase 27 global/realm/database/user resource hierarchy.
- Add durable plan assignments, quota overrides, admission, and usage ledger.
- Centralize all background work in bounded schedulers.
- Add overload, noisy-neighbor, and quota-change tests and benchmarks.

Exit: adversarial load in one realm stays within its limits and does not cause
OOM, unbounded queues/goroutines, durability failure, authorization bypass, or
unbounded latency growth in another beyond documented service objectives.

### M5 — HA and disaster recovery

- Replicate registry and database-tagged commands through the chosen bounded
  consensus model.
- Implement leader authority, quorum activation, follower repair, promotion,
  registry/security recovery, and rolling upgrade.
- Verify backup/restore/PITR and key behavior across failover.

Exit: three-voter partition, leader-kill, follower-repair, rolling-maintenance,
and lifecycle crash matrix passes without cross-database corruption, duplicate
creation, premature activation, or commit-before-quorum acknowledgment.

### M6 — Manager/control-plane and production gate

- Add idempotent provisioning API/CLI used by Installer and Manager.
- Add plan/lifecycle/usage operational UI through official interfaces.
- Add secure credential delivery without exposing root keys.
- Run full functional, race, fuzz, recovery, security, HA, upgrade, resource,
  and benchmark gates.

Exit: shared hosting is production-gated only when every exit requirement below
is green and documentation states measured capacity rather than aspirational
scale.

## 17. Adversarial Test Matrix

At minimum test:

- identical realm/database/user/role/table names across isolation boundaries;
- wrong realm, wrong database, missing database, suspended database, and stale
  name after rename/drop without enumeration leakage;
- cross-realm role membership, `CONNECT`, grants, system views, and audit;
- prepared statement IDs, cancel secrets, idempotency keys, cache entries,
  task IDs, CDC tokens, backup IDs, and operation IDs reused across databases;
- concurrent open/evict/recover/drop and server shutdown;
- crash after every lifecycle durable boundary;
- corrupt/truncated registry, database descriptor, key reference, and operation
  record fuzz inputs;
- wrong root, wrong realm root, rotated/revoked key, missing KMS, and snapshot
  rollback nonce behavior;
- transaction/WAL/UNDO recovery isolated between databases;
- backup/restore/PITR/import/export path and identity confusion;
- symlink, traversal, Unicode-confusable, overlong, and reserved names;
- global, realm, database, user, query, task, CDC, temp, WAL, and backup quota
  exhaustion;
- thousands of registered databases with a small open limit and bounded memory,
  goroutine, descriptor, queue, and recovery work;
- slow/noisy realm alongside latency-sensitive realm with durability and
  encryption enabled;
- leader kill, network partition, follower repair, and rolling upgrade during
  create/activate/suspend/restore/delete;
- old client/default database compatibility and downgrade rejection after the
  point of no return.

Run applicable unit, integration, restart, crash-injection, WAL/recovery,
transaction, concurrency, race, fuzz, Raft/failover, backup/restore, PITR,
RBAC, realm/database isolation, protocol/driver, resource-limit, and benchmark suites.

## 18. Production Exit Gate

- [ ] Authoritative product scope and roadmap include the feature and sequence.
- [ ] Versioned registry and every new persistent record are documented.
- [ ] Corruption, truncation, wrong-key, and decoder fuzz tests fail closed.
- [ ] Existing deployment migration is restartable, verified, and tested.
- [ ] Protocol capability negotiation and all official drivers are compatible.
- [ ] Realm authentication and database authorization have adversarial tests.
- [ ] Cross-realm and cross-database leakage tolerance is zero in the test suite.
- [ ] Engine opening, queues, goroutines, memory, descriptors, tasks, CDC, and
      maintenance are bounded and measured.
- [ ] Commit durability, WAL, recovery, and independent PITR remain correct.
- [ ] Backup/restore/import/export cannot confuse identities or key domains.
- [ ] Key creation, rotation, revocation, rewrap, restore, and shredding pass.
- [ ] Subscription limits cannot exceed deployment safety limits or bypass RBAC.
- [ ] Authoritative usage metering is durable/idempotent if used for billing.
- [ ] Three-voter creation/failover/repair/deletion lifecycle matrix passes.
- [ ] Rolling upgrade and compatibility-window behavior are documented/tested.
- [ ] Manager and provisioning tooling use only official authorized interfaces.
- [ ] Benchmarks retain fsync, WAL, encryption, checksums, MVCC, authentication,
      authorization, and durability and report isolation/noisy-neighbor context.
- [ ] `TODO.md` claims completion only after every applicable gate is green.

## 19. Decisions Required Before M1

1. Approve `REALM` (recommended) versus `ACCOUNT` or `PROJECT` as the external
   subscription-boundary term.
2. **DECIDED 2026-09-02 (before starting M2-2):** realm identities and roles
   with database-scoped grants — the recommended option — is formally
   adopted. This was already the assumption throughout §5.2 ("Each realm has
   its own principal namespace... authorization tuple is at least `(RealmID,
   PrincipalID, DatabaseID, privilege, object scope)`") and what M2-1's
   registry model (`internal/hosting.Registry`) already assumes; this entry
   just records it as no longer open. **Scoping note for M2-2 specifically**:
   the realm-local principal namespace and the `(RealmID, PrincipalID,
   DatabaseID, ...)` authorization tuple are *not* implemented by M2-2 — that
   is M2-4's job. M2-2 adds only a `Hello.Realm` field used purely for
   *routing/identity validation* (does the connecting client's requested
   realm+database match what this `nextsqld` process actually has open);
   authentication and authorization for a Hello carrying a realm selection
   still resolve through today's single deployment-wide `auth.Store`/ACL,
   completely unchanged, until M2-4 lands.
3. Approve realm-root sharing with a per-database-root dedicated-tier override.
4. Approve the deployment-level Raft v1 model versus another bounded consensus
   design.
5. Define exact shared-tier hard limits and the first measured capacity target.
6. Define suspension, retention, deletion, and crypto-shred policies.
7. **DECIDED 2026-09-02 (before starting M2-2):** there is no protocol
   version bump and therefore no "downgrade point of no return" in the
   traditional sense — §8's `Hello.Realm` field is added as a single
   additive, *opt-in* trailing field on the wire (mirroring the `NSCT`
   catalog record's V1 special case: read one more length-prefixed field
   only if bytes remain past `User`, default to absent otherwise), not a
   frame-header `Version` bump, per M2-1's own finding that the frame
   header's `Version` is a hard equality gate with no negotiation. Concrete
   compatibility rule: **a driver emits the trailing `Realm` field on the
   wire only when the caller actually configured a non-empty realm** — an
   unconfigured/default-pair client sends byte-for-byte the same Hello it
   always has, so it is permanently, unconditionally compatible with any
   server (this is the actual "compatibility window": unbounded for clients
   not opting into realm selection). A client that *does* select a
   non-default realm requires a server new enough to decode the trailing
   field; against an older server, `DecodeHello`'s strict trailing-byte
   check makes this fail closed with a decode error rather than silently
   ignoring the selection and connecting to whatever the old server happens
   to have open — deliberate, since silently falling through to the wrong
   database on a security/isolation-relevant field would be worse than a
   clear connection failure. There is therefore no deprecation deadline to
   define: old, unpatched clients are supported forever by construction, and
   new clients simply require a new-enough server the moment they actually
   use the feature.
8. Assign the authoritative roadmap phase and sequencing dependencies.
