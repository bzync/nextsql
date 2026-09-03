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
P17      complete except REBUILD INDEX ... ONLINE deferred
P18      implementable scope complete
P19      complete — v1 implementation and clean repository-wide functional gate green
P20      complete — native committed CDC streaming, images, retention, RBAC, and failover verified
P21      complete — RANGE/HASH/LIST (1–8 col keys) DDL, routing, tuple-tight pruning, recovery, ADD/DROP plus validated ATTACH/DETACH ownership transfer, local B+Tree-family/FULLTEXT/HNSW indexes, cross-partition secondary UNIQUE, partition-aware UPSERT, stable-ID statistics + costing, bounded maintenance, backup/restore/PITR, benchmarks, randomized pruning-soundness property test, and explicit offline legacy TENANT migration (`nextsql hosting migrate-tenant`); distributed sharding is a separate future phase
P22      complete — follower reads / read scaling
P23      complete — Vector Engine 2.0 (quantised types, quantised HNSW, IVF/IVF-PQ, sparse retrieval, dense+sparse+BM25 fusion; production-gating sign-off 2026-08-31)
P24      complete — Full-text Search 2.0; compatibility, adversarial bounds, quality, and encrypted recovery exit gate closed 2026-08-31
P25      complete — Security 2.0; mTLS, short-lived credentials, external IdP broker, field-level client encryption, password-hash evolution, and audit-chain hardening all production-gated; exit gate closed 2026-09-02
P26      complete — System catalog / introspection 2.0; virtual system schema, live session/security-administration tables, SHOW aliases, and an authoritative capability registry all production-gated; exit gate closed 2026-09-02
P27–P30 planned/open
Hosting   partial — accepted multi-database M1 registry/bootstrap foundation; selectable multi-engine routing remains open
```

Cross-cutting baseline work also includes rich bounded operations over the
existing geo and F32 vector types, a WAL/catalog-invalidated SELECT result
cache, and durable database-user-scoped mutation idempotency. This does not close
P23 follow-ons such as a `BITVECTOR`/Hamming `--vecquant` row or an IVF-PQ
process-local cache.

The managed-hosting foundation now adds an encrypted/versioned deployment
registry, separate registry root, stable realm/database identities, resumable
`nextsql init`, default database verification, a server-held deployment lock,
and explicit restartable offline adoption of an existing default database. It
remains one served engine per `nextsqld`; ID-layout/sibling-file migration,
realm routing, bounded multi-engine management, quotas, independent operations,
and HA lifecycle are still planned. See
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
2.0 is complete (exit gate closed 2026-09-02); the current release gate is
P27 Operational maturity + workload governance.

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
database into an isolated hosted deployment ships as `nextsql hosting
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
  feed. Optional opaque-token introspection and JIT provisioning remain off;
- field-level client encryption — experimental SQL/catalog/server slice plus
  portable randomized `NSCE1.` helpers for Go, Node.js/TypeScript, Bun, Deno,
  and PHP, PITR (exact-ciphertext restore-to-target-LSN), replication/failover
  (no lost acknowledged ciphertext across a three-voter leader failover), and
  durable key-rotation/revocation (`FileFieldKeyring` in every official
  driver) all landed and tested;
- password-hash evolution — Argon2id migration, versioned records, PBKDF2
  compatibility, transparent rehash, DoS benchmarks;
- audit hardening — a versioned hash-chained audit log with optional Ed25519
  signatures via a rotatable `NSAK` keyset, plus `nextsql audit` verification
  tooling.

The phase-wide exit gate — a dated security review sign-off — closed
2026-09-02 (`docs/security.md` "P25 security review sign-off"), so
`ENCRYPTED CLIENT` and every item above is now formally production-gated.
`ENCRYPTED CLIENT` stays labeled `experimental` in `system.capabilities` only
because no searchable/deterministic mode ships (a deliberate scope decision,
not an open blocker).

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
already satisfied. The current release gate is **P27 Operational maturity +
workload governance**.

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

## P27 — Workload Governance

Planned:

- resource groups;
- CPU/memory quotas;
- concurrency limits;
- workload priorities;
- graceful draining;
- improved diagnostics.

---

## P28 — Installer + Manager

Professional lifecycle management and operational UI.

---

## P29 — NextSQL Studio

Web-based professional database development interface with:

- SQL editor;
- explorer;
- profiling;
- multimodel tools;
- workflow/task/CDC tooling.

---

## P30 — NextSQL Intelligence

Built-in, permission-aware RAG/AI assistance for:

- docs;
- schema;
- SQL;
- performance;
- security;
- HA;
- workflows;
- CDC.

AI remains optional and non-authoritative.

---

## Beyond Core P30

Not part of the committed core roadmap:

- multi-primary writes;
- automatic distributed sharding;
- autonomous shard placement.

Any future work here requires separate production gating.
