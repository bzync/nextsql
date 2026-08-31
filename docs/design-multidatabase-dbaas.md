# Proposed Multi-Database Hosting and Subscription Isolation

> Status: **ACCEPTED DESIGN — M1 FOUNDATION PARTIAL; NOT PRODUCTION-GATED**
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
activation and never discovers sibling files. ID-layout migration/rollback,
multi-database engine routing, realm-local auth stores, quotas, HA replication,
and independent operational lifecycle remain open.

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

Add a capability/version-negotiated protocol revision. The Hello selection
becomes logically:

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
closed. This batch bootstrap and live multi-database router are planned, not
part of the current default-pair runtime.

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

Replace the single database pointer with a bounded `DatabaseManager` that maps
authorized stable IDs to engine handles.

Required behavior:

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

Advisory surfacing (`system.quotas`, metrics for "N% of cap", over-cap alerts)
is a separate follow-on; today the signal is the write rejection itself.

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
behavior must be migrated or replaced; it cannot coexist as an unregistered
side effect in production multi-database mode.

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

- Add protocol v2 realm field, capability negotiation, CLI, and official driver
  support.
- Implement realm authentication, database-scoped `CONNECT`, and immutable
  session routing.
- Implement bounded DatabaseManager with a deliberately small open limit.
- Replace sibling-file-only `CREATE DATABASE` with registered lifecycle.
- Add system views and machine-readable operations.

Exit: at least two realms with identical user/database names remain isolated
across concurrent connect, prepared, cancel, DDL, DML, restart, and recovery
tests; protocol v1 default routing remains compatible.

### M3 — Independent operational lifecycle

- Scope WAL, UNDO, caches, idempotency, tasks, workflows, schedules, CDC,
  maintenance, temp/spill, backup, restore, PITR, import, and export.
- Implement suspend/resume/rename/drop/tombstone workflows.
- Implement key rotation, rewrap, restore-as-new, and crypto-shred boundaries.

Exit: one database can crash, restore, rotate, suspend, or be deleted without
incorrect results, key use, state loss, or service interruption in another.

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
2. Decide whether realm users/roles are shared across all realm databases or
   database-local principals are also required in v1. Recommended: realm
   identities and roles with database-scoped grants.
3. Approve realm-root sharing with a per-database-root dedicated-tier override.
4. Approve the deployment-level Raft v1 model versus another bounded consensus
   design.
5. Define exact shared-tier hard limits and the first measured capacity target.
6. Define suspension, retention, deletion, and crypto-shred policies.
7. Decide the protocol v1 compatibility window and downgrade point of no return.
8. Assign the authoritative roadmap phase and sequencing dependencies.
