# NextSQL Roadmap

> Human-readable roadmap summary.
>
> This file is **not authoritative for status**.
>
> `TODO.md` is the source of truth for implementation status, sequencing, dependencies, and gates.  
> This file is a simplified, non-authoritative view derived from it.

---

## Current State

```text
P0–P15  complete
P16      complete — exit gate green; terminal 100M B+Tree soak deferred as a standalone measurement
P17      complete — ONLINE rebuild proven for non-partitioned B+Tree/UNIQUE/JSON-path/spatial indexes; blocking fallback elsewhere
P18      implementable scope complete
P19      complete — v1 implementation and clean repository-wide functional gate green
P20      complete — native committed CDC streaming, images, retention, RBAC, and failover verified
P21      complete — RANGE/HASH/LIST (1–8 col keys) DDL, routing, tuple-tight pruning, recovery, ADD/DROP plus validated ATTACH/DETACH ownership transfer, local B+Tree-family/FULLTEXT/HNSW indexes, cross-partition secondary UNIQUE, partition-aware UPSERT, stable-ID statistics + costing, bounded maintenance, backup/restore/PITR, benchmarks, randomized pruning-soundness property test, and explicit offline legacy TENANT migration (`nextsql registry migrate-tenant`); distributed sharding is a separate future phase
P22      complete — follower reads / read scaling
P23      complete — Vector Engine 2.0 (quantised types, quantised HNSW, IVF/IVF-PQ, sparse retrieval, dense+sparse+BM25 fusion; production-gating sign-off 2026-08-31)
P24      complete — Full-text Search 2.0; compatibility, adversarial bounds, quality, and encrypted recovery exit gate closed 2026-08-31
P25      complete — Security 2.0; mTLS, short-lived credentials, external IdP broker, field-level client encryption, password-hash evolution, and audit-chain hardening all production-gated; exit gate closed 2026-09-02
P26      complete — System catalog / introspection 2.0; virtual system schema, live session/security-administration tables, SHOW aliases, and an authoritative capability registry all production-gated; exit gate closed 2026-09-02
P27      complete — lifecycle/drain, session controls, resource groups, operational CLI, rolling-upgrade, and connection-governance gate closed 2026-09-03
P28      in progress — Setup/Operations largely complete; production/developer deployment profile and fail-closed preflight landed; remaining recovery-key and Windows/macOS items are capability/environment blocked
P29      in progress — M1 workspace + M2 streaming/virtualization + M3 bounded result tools/native inspectors + all five dedicated native explorers (JSON, Full-text, Vector, Hybrid, Geo) + Users/Roles, live Transaction/Lock, verified Audit, bounded per-tab plan comparison, and ANALYZE-only profiler implemented; MVP gate open
Hosting   partial — selectable bounded multi-realm/multi-database routing (M2) complete; M3 suspend/resume, rename, and drop landed; independent backup/PITR/key/HA lifecycle remains open
```

Cross-cutting baseline work also includes rich bounded operations over the
existing geo and F32 vector types, a WAL/catalog-invalidated SELECT result
cache, and durable database-user-scoped mutation idempotency. This does not close
P23 follow-ons such as a `BITVECTOR`/Hamming `--vecquant` row or an IVF-PQ
process-local cache.

The managed-hosting track now provides an encrypted/versioned deployment
registry, separate registry root, stable realm/database identities, declarative
bootstrap, realm-scoped auth, storage caps, and bounded per-connection routing
through `internal/dbmanager`. Idle secondary databases evict, open failures are
quarantined, buffer memory is budgeted process-wide, and task execution/polling
uses shared bounded infrastructure. Suspend/resume and offline managed-database
drop are implemented. Database-addressed backup/PITR/import/export,
key lifecycle, registry DR/Raft, and multi-database HA remain open. Realm
and database rename is implemented. See
`docs/design-multidatabase-dbaas.md`.

---

## P16 — Correctness / SLO closure (complete)

Exit gate (all green):

1. corrected 1M HNSW validation — v10 p95 **8.061 ms**;
2. p95 target satisfied;
3. recall reported — recall@10 **1.000**, recall@100 **0.998**;
4. 10M DELETE published, crash-during-merge `Check()`-clean, 100M analytics
   `< 60 s`, 10M INSERT/UPDATE published;
5. security sign-off; no unresolved correctness regressions.

The terminal randomized 100M-operation B+Tree invariant soak is a deferred
standalone measurement, not a release gate (paper-closed 2026-08-30, same
disposition as P18). P22 follower reads / read scaling is complete (exit gate
closed 2026-08-30). P23 Vector Engine 2.0 is complete (exit gate closed
2026-08-31). P24 Full-text Search 2.0 is complete. P25 Security 2.0 is
complete (exit gate closed 2026-09-02). P26 System catalog / introspection
2.0 is complete (exit gate closed 2026-09-02). P27 closed 2026-09-03; the
current release gate is P28's remaining installer work.

---

## P19 — Automation

Manual synchronous `WORKFLOW`, synchronous row `TRIGGER`, native schedules,
and the bounded durable `TASK` runtime are implemented. Targeted functional,
race, fuzz, PITR, Raft failover, and TLS-driver gates pass. A clean
repository-wide functional invocation passed every package on 2026-08-29,
including the storage B+Tree package, so P19 is complete.

```text
WORKFLOW
├── manual
├── trigger
└── schedule
      ↓
     TASK
```

---

## P20 — CDC

The committed-WAL core provides versioned changes, commit-only
ordered transaction delivery, bounded pull backpressure, native SQL/NSQL
streaming, database/table filtering, commit-LSN resume, explicit history expiry,
runtime RBAC revocation, audit, cancellation, and prepared-driver support.
Active streams also pin their live-WAL horizon through pruning. Key-only is the
default and durable per-table FULL images are explicitly bounded. Safe
operation predicates, restart, and three-voter leader-failover resume are
covered. Process diagnostics expose bounded
CDC activity, delivery, error, and lag counters.

Target surface:

- ordering;
- resume tokens;
- backpressure;
- database/RBAC enforcement.

---

## P21 — Partitioning

Complete. The bounded, versioned `NSCT` v5 catalog descriptor and a tested
RANGE/HASH/LIST DDL (one-to-eight-column keys; tuple bounds/membership for
RANGE/LIST), routing, tuple-tight pruning, and recovery slice are implemented.
Empty RANGE-tail and LIST partition ADD/DROP lifecycle DDL and validated
non-copying ATTACH/DETACH ownership transfer are implemented with non-reusing
stable IDs. Partition-local plain/covering/partial/expression/JSON-path/spatial,
FULLTEXT, and HNSW indexes support CREATE, routed DML, scan, rebuild, drop,
reclamation, and restart; cross-partition plain-column secondary UNIQUE is
enforced (exclusive key lock plus per-partition probe) and `UPSERT` on
RANGE/HASH/LIST tables resolves against the partition-local roots. `NSST` v3 row
counts and compact, versioned `NSPS` v1 column/index/vector sketches provide
pruning-aware local costing with conservative global fallback. Base backup and
archived-WAL PITR preserve partition descriptors, rows, local index roots,
pruning, and stable-ID statistics. Partition-aware bounded table/index
maintenance and `nextsql-bench --partition` benchmarks are implemented, and
`TestPartitionPruningSoundness` is a randomized pruning-soundness property test.
Explicit offline migration from a legacy `tenant_id` / `PARTITION BY TENANT`
database into an isolated hosted deployment ships as `nextsql registry
migrate-tenant` (bounded, point-verified, resumable). Legacy TENANT descriptors
are recovery/offline-migration compatibility only; distributed sharding is a
separate future phase.

Shipped modes: RANGE, HASH, LIST.

Unblocks partition-wise aggregation/join work (P18 follow-on).

---

## P22 — Follower Reads — complete (2026-08-30)

Read scaling with explicit consistency:

- `STRONG` — leader-only, linearizable behind a `raft.VerifyLeader` quorum read
  barrier (sign-off in `docs/ha.md` "Consistency model and sign-off");
- `BOUNDED` — served within `MAX STALENESS` of the leader, no quorum round trip;
- `STALE` — any member, unbounded lag; always a consistent committed prefix.

Follower-read routing ships in the server and every official driver; the
`nextsql-bench --readscale` benchmark measures the barrier cost and leader
read-offload. Exit gate closed: linearizability/consistency sign-off +
`TestFollowerReadFailoverSessionGuarantee` (session guarantees hold across a
leader failover).

---

## P23 — Vector Engine 2.0

The existing F32 type now has bounded dimension/norm/normalize,
add/subtract/scale, dot, cosine-distance, and L1 operations.

- F16 — **done**: `VECTOR<F16,N>` stores IEEE 754 half elements (half the
  payload-store size), widened to `float32` for all math; HNSW works unchanged.
- I8 — **done**: `VECTOR<I8,N>` stores signed bytes with a per-vector `float32`
  scale (~¼ the payload-store size at high dimension), widened to `float32` for
  all math; HNSW works unchanged.
- Size / recall benchmark — **done**: `nextsql-bench --vecquant` compares `F32`
  vs `F16` vs `I8` element types, an `F32` column with an `F16`/`I8`-quantised
  HNSW graph, an `F32` column with an IVF and an IVF-PQ index, and a
  `SPARSEVECTOR` inverted index on a high-dimension, low-nnz corpus, on
  payload/index/database size, index build time, `NEAREST` latency, and
  recall@10/@100 (`docs/vector.md`).
- Quantized HNSW index — **done**: `CREATE VECTOR INDEX … USING HNSW WITH
  (QUANTIZATION = 'F16' | 'I8')` traverses the graph on a compact quantised copy
  of each vector and re-ranks the final candidates against the full-precision
  payloads, so recall tracks an unquantised graph. The win is cache-local
  traversal reads.
- Bit vectors — **done**: `BITVECTOR<N>` packs `N` single-bit elements into
  `ceil(N/8)` bytes (1/32 of `VECTOR<F32,N>`), widened to `float32` `0`/`1` for
  all math. The new `HAMMING` metric (differing-bit count) is the default and
  only metric for a bit column; HNSW builds a Hamming graph.
- Compressed neighbour lists — **done**: HNSW node records front-code each
  layer's neighbour keys (sorted, varint shared-prefix + suffix), shrinking the
  on-disk graph by roughly a third with no change to the decoded neighbours,
  recall, or latency. v1 fixed-width records still decode.

- IVF index — **done**: `CREATE VECTOR INDEX … USING IVF WITH (LISTS = n
  [, PROBES = m])`. A portable inverted-file coarse-quantiser index in
  `internal/vector` (deterministic k-means++ training, per-centroid posting
  lists front-coded on disk as `NSIV` / `NSIC` / `NSIL`, exact-scoring probe
  search whose recall rises with `PROBES`), wired through the parser, binder,
  catalog table descriptor format v7, and the executor: `CREATE` / `REBUILD
  INDEX` train over a ≤ 50 000-vector heap sample; `INSERT` / `UPDATE` /
  `DELETE` maintain the posting lists; `NEAREST` probes and scores exactly.
  Centroids and lists live in the index's own detached encrypted B+Tree, so
  crash-recovery, backup, PITR, and Raft are inherited. A wide centroid set is
  split across several B+Tree records transparently. Real-valued metrics only;
  not on partitioned tables. Measured by `nextsql-bench --vecquant` (an `F32` +
  IVF row: ~10× smaller index and ~10× faster build than HNSW, lower recall at a
  25 %-of-`LISTS` probe ratio on synthetic uniform vectors). A committed
  `NEAREST` is served from a process-local copy of the quantiser (centroids,
  posting lists, and vectors in memory), built at commit or lazily on first
  search and evicted on mutation / rebuild / drop / replicated apply — the same
  generation-tracked cache the HNSW graph uses.

- IVF-PQ index — **done**: `CREATE VECTOR INDEX … USING IVFPQ WITH (LISTS = n,
  SUBSPACES = M [, PROBES = m])`. The IVF coarse quantiser plus an `M`-subspace
  product-quantisation codebook over the residuals: a posting list stores an
  `M`-byte code per vector instead of a full vector, search ADC-scores each
  probed list and re-ranks the top candidates exactly against the column's
  payload store (recall tracks an unquantised IVF). `SUBSPACES` must divide the
  dimension (≤ 128); `COSINE` / `L2` only; not on partitioned tables. Wired
  through the parser, binder, catalog table descriptor format **v8**, and the
  executor (`CREATE` / `REBUILD INDEX` train over a ≤ 50 000-vector heap sample;
  `INSERT` / `UPDATE` / `DELETE` maintain the posting lists; `NEAREST` probes,
  ADC-scores, and re-ranks). The index lives in its own detached encrypted
  B+Tree — coarse centroids grouped like IVF, the codebook split into chunks
  under an `IVPCG` header, one front-coded `NSPL` posting list per centroid — so
  crash-recovery, backup, PITR, and Raft are inherited. Portable core
  (`TrainIVFPQ` / `AddIVFPQ` / `RemoveIVFPQ` / `SearchIVFPQ`, `NSPQ` / `NSPC` /
  `NSPL` encodings) in `internal/vector`. Measured by `nextsql-bench --vecquant`
  (an `F32` + IVF-PQ row). No process-local cached copy yet — a committed
  `NEAREST` reloads the quantiser from the index tree (a documented follow-on).

- Sparse retrieval — **done**: `SPARSEVECTOR<N>` stores only non-zero
  coordinates (`NSSV`); `CREATE VECTOR INDEX … USING SPARSE` builds an inverted
  index (`NSSM` / `NSSP`) and ranks with exact inner product (optional COSINE
  re-rank). SQL `N` is 1…65535. Not on partitioned tables. The portable core
  (`internal/vector/sparse.go`) plus parser/binder/executor lifecycle
  (build/rebuild/maintain/search) landed 2026-08-30. Official `--vecquant`
  `SPARSE` size/latency/recall row landed 2026-08-31 (2000 × 4096-d nnz=24:
  recall@10/@100 1.000, p50 428 µs).

- Dense + sparse + BM25 fusion — **done**: a second `NEAREST` clause (one
  dense `VECTOR`, one `SPARSEVECTOR`, optional `SEARCH`) unions candidates
  from each retriever and reciprocal-rank fuses them. Measured benefit: fused
  `LIMIT 3` surfaces each channel's unique hit (`TestDenseSparseBM25Fusion`).

P23 exit gate closed 2026-08-31. Production-gating sign-off:
`docs/vector.md` "Production-gating sign-off (Phase 23)".

Documented follow-ons (not gate items):

- a `BITVECTOR` / Hamming `--vecquant` row;
- a process-local IVF-PQ quantiser cache;
- a re-rank-free quantised HNSW mode that drops the full payload;
- IVF / IVF-PQ / `USING SPARSE` on partitioned tables;
- SIMD after profiling.

---

## P24 — Full-Text Search 2.0 (complete 2026-08-31)

Stemming landed (2026-08-31): versioned analyzer metadata on `NSCT` v9,
`CREATE FULLTEXT INDEX … WITH (ANALYZER = 'simple' | 'english')`, Snowball
English (Porter2) v1 at index and query time, fail-closed query-expansion
caps. Default `simple` keeps Phase 10 BM25/phrase behaviour.

Stop-word dictionaries landed (2026-08-31): english analyzer v2 applies
stop-word dictionary v1 (33-term Lucene EnglishAnalyzer / Snowball-small
set) before stemming, identically at index and query time; remaining terms
re-pack to consecutive positions. `simple` has no stop list. english v1
(stem only) still decodes.

Versioned language analyzers landed (2026-08-31): `french` / `german` /
`spanish` (Snowball 3.x stemmer + that language's Snowball stop-word
dictionary v1) on existing `NSCT` v9 analyzer id + revision. French elides
`l'` / `qu'` / … before the stop list. `simple` and `english` unchanged.

Synonym dictionaries landed (2026-08-31): english analyzer v3 applies
synonym dictionary v1 at query time (15 tight bidirectional groups,
fail-closed expansion caps, OR at the token position). Index terms stay
1:1 like v2. english v1/v2 still decode. `simple` unchanged.

Prefix search landed (2026-08-31): trailing ASCII `*` on a SEARCH token
(`cat*`, `"data* performance"`). Query-time only; prefix tokens skip
stem/stop/synonym; distinct matches consume the expansion caps and fail
closed. Exact unadorned tokens keep Phase 10 BM25/phrase behaviour.

Fuzzy matching landed (2026-08-31): trailing ASCII `~` on a SEARCH token
(`cat~`, `cat~1`, `cat~2`, `"databas~ performance"`). Query-time only;
OSA Damerau-Levenshtein with AUTO distance (0/1/2 by rune length); fuzzy
tokens skip stem/stop/synonym; distinct matches consume the expansion
caps and fail closed. Exact unadorned tokens keep Phase 10 BM25/phrase
and prefix behaviour.

Typo tolerance landed (2026-08-31): unadorned tokens whose analyzed
alternatives are all absent from the vocabulary become AUTO fuzzy
(`databse` matches `database`). Typo AUTO is 0/1/2 for 1–4 / 5–8 / 9+
runes, stricter than explicit `~`, so `cats` does not match `cat` and
`cat` does not match `cot` when `cat` is indexed. Prefix and explicit
fuzzy groups are unchanged. Distinct matches consume the expansion caps
and fail closed.

Highlight/snippet generation landed (2026-08-31): `HIGHLIGHT(col)` and
`SNIPPET(col)` in the SELECT list of a SEARCH query wrap original matching
tokens (exact/synonym/prefix/fuzzy/typo) with `<mark>` (overrideable);
`SNIPPET` is a bounded window around the densest match cluster. Fail closed
outside SEARCH SELECT lists; marker and width bounds. No catalog/format bump.

Multi-field search landed (2026-08-31): `CREATE FULLTEXT INDEX` /
`SEARCH col [, col …]` take 1–8 STRING/TEXT columns. A matching column list
(same order) uses the inverted index; subset/reorder seq-scans. Fields are
one BM25 document; phrases do not cross fields (position bands). No
catalog/format bump. Prefix/fuzzy/typo and HIGHLIGHT/SNIPPET unchanged.

Field weighting landed (2026-08-31): optional `WEIGHT <number>` after a
SEARCH column (`SEARCH title WEIGHT 3, body FOR '…'`) scales that field's
BM25 term frequency from position bands. Omitted weights are 1; range
`(0, 64]`; query-time only (no catalog/format bump). Matching and
HIGHLIGHT/SNIPPET are unchanged.

Faceting landed (2026-08-31): `SELECT * … SEARCH … FACET col [, col …]`
returns independent histograms over the full SEARCH match set (`facet`,
`value`, `count`). `LIMIT` is per-facet top-N; `NULL` is skipped; 1–8
discrete columns and 1024 distinct values fail closed. Query-time only
(no catalog/format bump). Requires `SELECT *` and `SEARCH`.

Exit gate closed (2026-08-31):

- Phase-10 BM25 constants and phrase semantics are pinned by a golden fixture;
- fuzzy/typo expansion and vocabulary inspection fail closed, and OSA distance
  uses bounded linear memory;
- end-to-end quality fixtures cover every shipped analyzer and query expansion;
- analyzer-aware encrypted kill/reopen recovery is covered;
- build, targeted functional/race, fuzz, and a serialized repository-wide
  functional invocation are green.

Documented follow-ons (not gate items):

- further language analyzers beyond french/german/spanish;
- runtime/index optimizations;

---

## P25 — Security 2.0

**Complete (exit gate closed 2026-09-02).** The dated checklist audit and the
security review sign-off are both recorded in `docs/security.md`. The first
mTLS/service-identity surface is implemented and tested: a configured client CA
requires verified client certificates, the URI SAN
`nextsql://service/<principal>` binds the certificate to the native login user,
and audit records identify the authentication source. Native password and RBAC
checks remain mandatory. `SIGHUP` atomically reloads a validated server key
pair, client trust bundle, and optional fail-closed X.509 CRL bundle; invalid
reloads retain last-known-good state, and successful mTLS reloads close all
accepted connections (including pre-auth handshakes) to force reauthentication.
OCSP is still open.

Signed short-lived credentials are implemented and tested: an Ed25519-signed
`NSSC1.` credential presented in place of the password, bounded by an explicit
expiry and optional audience / database / realm / role scope, with a rotatable
signing keyset (`NSTK`), a fail-closed revocation set (`NSTR`, by token id or
per-principal cutoff), `SIGHUP` reload, `nextsql token` tooling, and
`identity_source` audit. Role scope narrows the session and cannot escalate.

Also production-gated:

- external identity providers — required surface complete; design accepted (`docs/design-oidc-external-idp.md`:
  a brokered OIDC token exchange that mints an `NSSC1.` credential, with an
  `NSIP` identity policy whose group→role mapping cannot escalate past native
  RBAC). The offline `NSIP` policy engine (`internal/auth`) and the
  authentication **broker** (`cmd/nextsql-auth-broker`) are implemented and
  tested: the broker's `POST /v1/exchange` validates an OIDC ID token against a
  soft/hard-TTL cached JWKS (`internal/oidc`), maps it through the `NSIP`
  policy, and mints an `NSSC1.` credential its issuing `NSTK` key signs — the
  `nextsqld` SQL auth path is unchanged and never calls the IdP. The interactive
  client flow is implemented: `nextsql login` uses discovery + Authorization
  Code/PKCE + a bounded loopback callback, stores/refreshes the broker-minted
  credential with strict local permissions, and `exec` / server `status` accept
  `--idp`; `logout` / `whoami` are available. Server audit labeling is also
  implemented: a bounded operator map labels only credentials successfully
  verified by dedicated broker key ids as `oidc` / `mtls+oidc`; forged tokens
  stay generic and no source claim is trusted. JWT client credentials are also
  implemented: protected secret-file token acquisition, explicit resource
  audience + client binding at the broker, and non-interactive renewal. The
  embedded single-node mode is implemented on a separate bounded listener with
  issuer/verifier compatibility checks and a live native-user/ACL membership
  feed. Optional RFC 7662 opaque-token introspection and bounded JIT
  provisioning are implemented as fail-closed controls and remain off by
  default;
- field-level client encryption — production-gated randomized `NSCE1.` and
  explicit deterministic-equality `NSCE2.` SQL/catalog/server paths plus
  helpers for Go, Node.js/TypeScript, Bun, and PHP, PITR
  (exact-ciphertext restore-to-target-LSN), replication/failover
  (no lost acknowledged ciphertext across a three-voter leader failover), and
  durable key-rotation/revocation (`FileFieldKeyring` in every helper-bearing
  driver) all landed and tested;
- password-hash evolution — Argon2id migration, versioned records, PBKDF2
  compatibility, transparent rehash, DoS benchmarks;
- audit hardening — a versioned hash-chained audit log with optional Ed25519
  signatures via a rotatable `NSAK` keyset, plus `nextsql audit` verification
  tooling.

The phase-wide exit gate — a dated security review sign-off — closed
2026-09-02 (`docs/security.md` "P25 security review sign-off"), so
`ENCRYPTED CLIENT` and every item above is now formally production-gated.
The later deterministic extension adds explicit `ENCRYPTED CLIENT
DETERMINISTIC`, HKDF-separated `NSCE2` RFC 5297 AES-SIV, equality-only
B-tree/UNIQUE use, cross-driver fixtures, fuzz, PITR, and failover coverage.
`field_encryption_client` is now `supported`; broader searchable encryption
remains outside the shipped surface.

---

## P26 — System catalog / introspection 2.0 (complete)

The virtual `system` schema core is implemented with stable columns and
permission-aware redaction. All 5 live tables landed 2026-09-01
(`system.sessions`, `system.active_queries`, `system.transactions`,
`system.change_streams`, `system.locks` — node-local, in-memory,
RBAC-filtered; see `docs/system-catalog.md`). All nine planned `SHOW`
convenience aliases landed 2026-09-02. The phase-wide exit gate closed
2026-09-02 (`docs/system-catalog.md` "P26 exit gate closure"): the one real
gap it found — Manager's planned users/roles/privileges administration and
security dashboard had no official read source — is closed by new
admin-only `system.users`/`system.roles`/`system.grants`; the capability
registry gained rows for every previously-undiscoverable P23/P25 surface;
RBAC-coverage and realm/database-visibility were audited and confirmed
already satisfied. P27 later closed on 2026-09-03.

Stable native introspection for:

- schema;
- sessions;
- locks;
- security administration (users, roles, grants);
- replication;
- backups;
- maintenance;
- automation;
- CDC;
- partitions;
- vector/full-text structures.

---

## P27 — Workload Governance (complete)

Implemented and exit-gated 2026-09-03:

- resource groups;
- CPU/memory quotas;
- concurrency limits;
- workload priorities;
- graceful draining;
- improved diagnostics.

---

## P28 — NextSQL Admin: Setup + Operations modes

Professional lifecycle management and operational UI, unified as one product,
NextSQL Admin (2026-09-05 merge, see `docs/design-admin.md`) — one binary
`nextsql-admin` with Setup/Operations/Studio modes, replacing the formerly
separate Installer/Manager/Studio products.

**In progress.** The installer automation + lifecycle CLI backbone
(`nextsql setup` / `nextsql lifecycle …`) is done. **The Operations-mode MVP
is complete (2026-09-04)** — all nine slices M1–M9 (Overview, Databases,
Activity, Security, Backups, Cluster, Maintenance, Configuration,
Logs & Diagnostics), a loopback web app that drives the server only through
the NSQL protocol as the operator's own user. New SQL surface it added:
`SET CONFIG`, `BACKUP DATABASE`, `VERIFY BACKUP`; new `system.*` tables:
`metrics`, `server_log`, `backups`, plus the M4 security tables and
`system.config`. Restore/PITR stays CLI-only. Setup mode is a loopback web
wizard with token auth and the welcome → location → resources →
administrator → summary → install → completion flow, delegating plan/install
to `nextsql setup`. Its Linux `.tar.gz`/`.run`/`.deb` packaging integration
and M5 accessibility pass are complete; Setup and Operations modes share the
same branded RUI shell/theme behavior, backed by real-Chrome keyboard and axe
WCAG 2.2 A/AA regression tests. Developer and production deployment profiles
plus a fail-closed production security preflight landed (log #208): the GUI
defaults to production; `nextsqld` re-enforces `deployment_profile=production`
at start. Remaining: recovery-key UX, Windows/macOS packaging and execution,
and upgrade/repair verification through the installer path.

---

## P29 — NextSQL Admin: Studio mode (NextSQL Studio)

**In progress.** M1 is an authenticated, capability-aware three-pane
workspace in the shared Admin shell: authorized table/column/index discovery,
a single NextSQL editor with execution and real lock-wait cancellation, and a
typed result preview. M2 streams ordered, bounded NDJSON row batches and mounts
only a visible-window slice in the result DOM while preserving row/byte/time
limits; the one-line result status reports rows and columns for a read and the
affected-row count for a column-less write or DDL, never a misleading "0 rows". M3 adds safe bounded cell/row copy, CSV/JSON export, and JSON/vector/
fixed-geo/TIMESTAMPTZ inspection. All five originally scoped dedicated native
explorers are also complete: JSON tree/raw/path selection with authorized
live-index status; a capability/catalog-driven Full-text SEARCH builder with
HIGHLIGHT/SNIPPET output, ordinal BM25 rank, and copy/insert/explicit bounded
execution; a capability/catalog-driven Vector NEAREST builder with
column-kind-restricted metric selection and a live dimension/domain-validating
inspector; a Hybrid Explorer composing an optional structured filter with
those same Full-text/Vector builders into one WHERE+SEARCH+NEAREST statement,
with an Explain action that reuses the existing graphical EXPLAIN tree; and a
Geo Explorer with an accessible click-to-draw world canvas generating native
DWITHIN/WITHIN queries over a POINT column. A Users & roles privilege
explorer (Developer operations scope) is also implemented: a read-only view
over the same admin-only `system.users`/`system.roles`/`system.grants`
catalog Operations mode's Security view exposes, with a per-grant "Revoke…"
action that prefills the existing GRANT/REVOKE builder from a real grant row.
A read-only Transaction console + Lock explorer also reuses Operations mode's
existing authorized live Activity bundle, refreshes on every open/manual
Refresh, and truthfully omits unsupported cross-session kill/rollback actions.
A read-only Audit viewer similarly reuses the existing admin-only Security
bundle's chain verification and bounded 200-record tail, retaining suspect
records when verification fails and exposing no mutation action. Graphical
EXPLAIN/EXPLAIN ANALYZE now also supports a bounded per-tab baseline and
deterministic structural-path comparison without guessed semantic matching;
server-reported values are called measured only for analyzed snapshots. Its
ANALYZE-only profiler adds bounded per-operator estimate-error signals and
reported maxima while explicitly refusing to sum inclusive/query-level
metrics or invent percentages. A bounded, keyboard-operable catalog-aware
IntelliSense list now offers table/column-name completion (no NextSQL
keyword completion, since the only ground truth for that set lives in the
lexer and would silently drift) — table names come free from the loaded
catalog, and column names for FROM/JOIN-referenced tables are fetched
lazily, only while the panel is open, through the same per-table route
every explorer already uses. A deterministic misspelled-table-name notice
builds on that same extraction: a bare FROM/JOIN target within a small
edit distance of exactly one real catalog table gets a live, non-
interrupting "did you mean" fix, while a schema-qualified target, a
truncated catalog, or a tie between equally-close names is never flagged
or guessed — column/alias/function-name suggestions are deliberately out
of scope, since a plain identifier scan cannot safely tell those apart.
The database explorer's lazy table detail now also shows a bounded
**Statistics** section — the table's `system.table_stats` row and its
`system.index_stats` rows, labelled as `ANALYZE`-written estimates rather
than a live count — by extending the existing per-table detail bundle
with two more authorized reads, no new route. A read-only Workflows,
relationships, tasks & change-streams explorer over the authorized
`system.workflows` / `system.triggers` / `system.schedules` /
`system.tasks` / `system.change_streams` catalog (one small dedicated
bundle route) lists native definitions, durable scheduled tasks (with a
client-side per-workflow filter), and open CDC `SUBSCRIBE` consumers with
their resume `lsn`. Its bounded accessible diagram renders
`TABLE → TRIGGER → WORKFLOW` and `SCHEDULE → WORKFLOW`, backed by an
always-visible text alternative and a fail-closed large/partial-graph
fallback. It refetches on every open since task/subscription state is live;
authoring stays in the editor and there is no cancel/retry-task or stream
pause/resume control. IntelliSense also completes native JSON
paths when the caret is inside a dotted path, offering only the paths a
referenced table is actually indexed on (the sole JSON structure the
server exposes metadata for) — never a guessed path. In a `NEAREST`
clause it completes vector-typed column names and the `USING` metrics that
column kind actually accepts; it does not complete inside `TO (...)`.
The RBAC boundary —
that a Studio session is confined to the logged-in user's own grants
across every Studio route — is integration-test-covered
(`TestAdminStudioEnforcesRBAC`). The operator can tag the current
connection's environment (dev/test/staging/production); a production tag
shows a standing banner and turns on a read-only safety mode that holds
every write behind a confirmation (the analyze endpoint now returns a
`write` flag mirroring nextsqld's own mutation classification). A
**Switch connection…** control re-targets the session to a different realm
or database on the same `nextsqld` without signing out — a fresh
authenticated connection (password supplied each time, never stored)
swapped in atomically, failing closed on a busy connection or a bad
credential — and the toolbar shows the server address it targets. A
read-consistency selector sets the session to strong (default), bounded
(with a staleness bound) or stale — a live session-control change on the
current connection affecting reads only, with a warning badge whenever the
mode is not strong. The switch form also offers the realm/database pairs
recently used on the current server as quick-fill buttons (stored in the
browser, never a credential). Parsed-AST pre-run analysis also marks
`CREATE`/`DROP USER` and `CREATE`/`DROP ROLE` as realm-wide: the confirmation
names the connected realm/database and explains the cross-database reach,
including inside a consolidated script warning. These
are the connection-manager slices landed ahead of the full multi-target
profile model (named multi-host profiles, TLS/mTLS fields, and OS
credential storage still to come). The database explorer itself is now a lazy-loaded object tree: a
Tables branch whose nodes fetch each table's existing per-table detail
bundle once to reveal Columns/Indexes sub-branches (selecting a name
still opens the full inspector), plus a read-only Workflows branch that
runs the existing workflows bundle on first open — no new route.
A new read-only `system.foreign_keys` catalog view (child table,
constraint, ordinal, column, referenced table/column, `ON DELETE` /
`ON UPDATE` action; visibility-filtered like `system.columns`) exposes
referential structure to any client; Studio consumes it in a per-table
**Foreign keys** inspector section (outbound constraints plus a
**Referenced by** grid of inbound references), an outbound-only
schema-tree sub-branch, and a **Schema diagram** explorer — an inline-SVG
entity-relationship view laid out from the whole foreign-key catalog, with
a grouped relationship list as its text alternative and large-schema
fallback (no layout library, pure unit-tested layout math). A **Search
objects** finder gives keyboard-driven ranked lookup across table and
workflow names, and a **command palette** (Ctrl/Cmd+K) does the same across
Studio's own actions — run a query, open an explorer, switch connection —
without adding any behavior of its own. Unsaved editor tabs (title + SQL text only) are mirrored to
the browser and restored on reload, so a crash or accidental close does not
lose work — a narrow, documented exception to Studio's otherwise
disk-free client state. A **Saved** panel keeps named, tag-grouped SQL
snippets in the same per-connection browser storage (load / update / rename
/ delete), with git-friendly file export (a stable, order-independent
document) and merge-by-id import. A buffer that references positional
placeholders (`$1..$N`) gets a **Parameters** bind panel; the values ride
the query request as a bounded positional array and are coerced to each
placeholder's type by the server. The table inspector's **DDL** section
shows the canonical `CREATE TABLE`/`CREATE INDEX` for the selected table,
from a new `system.table_ddl` catalog view backed by the same renderer that
produces backup/restore SQL export. A **Design schema…** modal builds a new
`CREATE TABLE` or `CREATE INDEX` (UNIQUE / FULLTEXT / VECTOR / SPATIAL)
from a form with a live DDL preview and loads it into the editor for
review — it never executes. Its bounded **Constraints** section unifies
the already-authorized primary-key, UNIQUE-index, NOT-NULL, and foreign-key
metadata without adding a server route. A read-only **Migrations** explorer
shows the database's `nsql_schema_migrations` history — applied versions,
current version, and any dirty state — via a new
`GET /api/v1/studio/migrations` read; authoring and applying migrations stay
with the `nextsql migrate` CLI, which holds the local migration files. A
**Generate data…** builder produces deterministic, seed-driven `INSERT`
scripts of synthetic rows from a table's authorized column metadata and loads
them into the editor for review — bounded, never executed, no server route.
The table inspector also has a **Dependencies** section — inbound foreign-key
references plus the row triggers defined on the table, read from
`system.triggers` (no new route) — with a matching schema-tree **Triggers**
sub-branch. An **Import data…** builder parses a pasted or loaded
CSV / TSV / JSON / NDJSON document, maps its fields to a table's authorized
columns, and builds a bounded, type-checked `INSERT` script into the editor
for review — never executed, no server route. A **Parameterized DML…** builder
emits a positional-parameter (`$1..$N`) `INSERT`, `UPDATE`, or `DELETE`
statement template for a table from its authorized column metadata into the
editor, bound in the existing Parameters panel before it runs — never executed,
no server route; `UPDATE`/`DELETE` require an explicit WHERE key. The query-tab
strip scrolls horizontally rather than wrapping. All use only the official Go
driver over NSQL and pass the current real-browser accessibility audit. The
shared browser gate also runs Setup, Operations, and Studio at DPR 2, asserting
their compact layouts, no page-level horizontal overflow or undersized raster
source, loaded scalable fonts, and axe WCAG 2.2 AA before resetting device
metrics; this closes Studio's browser/CSS high-DPI item without changing the
unverified Windows/macOS package status. Explorer/inspector visibility and
pane widths, plus the last authorized table name, persist in per-connection
browser storage with accessible splitters and Hide/Show/Reset layout controls
— never a credential. See
`docs/design-admin-studio.md`.

The open MVP expands that foundation into:

- saved multi-environment connection management and secure OS credential storage;
- source-position binder diagnostics (parser diagnostics and the data-editing grid are done);
- general spatial preview;
- profiling;
- multimodel tools;
- workflow/task/CDC tooling.

---

## Beyond the core product

Former P30 (NextSQL Intelligence + built-in RAG) is **removed from the
product**, not deferred. Do not implement it.

Not part of the committed core roadmap:

- multi-primary writes;
- automatic distributed sharding;
- autonomous shard placement.

Any future work here requires separate production gating.
