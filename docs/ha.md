# High availability (Phase 15)

Continuous service is a design objective, not a “100% uptime” claim.
A properly configured cluster targets `>= 99.999%` availability as an
SLO. Correctness beats aggressive failover.

## Topology

```text
                 NextSQL Endpoint
                        │
                  ┌─────┴─────┐
                  │           │
                  ▼           ▼
               Node A       Node B
               Leader       Replica
                  \           /
                   \         /
                    Node C
                    Replica

                    Raft Quorum
```

Minimum HA recommendation: **3 voting nodes**. Consensus is
[hashicorp/raft](https://github.com/hashicorp/raft). NextSQL does not
invent a consensus algorithm.

## Durability policy

A write is acknowledged only after:

1. The leader executes the statement and flushes its local WAL.
2. The WAL-record batch is sealed with the replication DEK
   (`crypto.DomainRepl` = `'R'`).
3. Raft commits that command on a quorum.

RPO = 0 for those acknowledged quorum-synchronous commits under the
covered node-failure model (loss of the leader, or any minority). If
Raft cannot identify a leader, writes are rejected (`unavailable`).
There is no split-brain write path.

Followers apply the same physical WAL records (page images, tree meta,
allocator state). SQL is not re-executed, so `UUID()` / `NOW()` / `AI()` stay
deterministic across replicas. Foreign-key `CASCADE` / `SET NULL` /
`SET DEFAULT` are sequences of those same insert/update/delete records
produced on the leader; replicas never interpret the constraint action.

Schedule polling and task claims are leader-gated. Schedule cursor advancement
and deterministic task creation commit in one replicated transaction; task
leases, retries, cancellation, terminal state, and workflow effects are also
replicated state. Followers do not dispatch or execute tasks. After election,
the new leader continues from the committed cursor and reclaims only an expired
durable lease, with the task-row write conflict fencing a stale worker.

## Read consistency (Phase 22)

Every read runs in one of these modes. The session default is `STRONG`.

### `STRONG` (default)

A `STRONG` read observes every write that was acknowledged before the read
began — it is read-after-write consistent across the whole cluster, not just
within one session.

`STRONG` reads are served only on the leader, and only after a **Raft read
barrier**: the leader calls `VerifyLeader`, a round trip that confirms a quorum
still recognises it as leader. This blocks the stale-leader anomaly — a leader
that has been partitioned away from the quorum fails the barrier and cannot
answer a `STRONG` read from its own log.

Consequences:

- A `STRONG` read on a follower is rejected with `unavailable` and
  leader-routing guidance. Followers never silently serve stale data as
  `STRONG`.
- A `STRONG` read costs one quorum round trip. It is a correctness barrier,
  not a lease-only fast path; a cheaper lease-based path and follower-served
  modes are later Phase 22 increments.

### `BOUNDED`

A `BOUNDED` read is served from a member's local applied state, but only while
that member is **within `MAX STALENESS` of the leader**: the leader always
passes, and a follower passes only while it still sees a leader and was last
contacted by it within the bound. A follower that has fallen further behind —
or lost leader contact entirely — is rejected with `unavailable` so the caller
routes elsewhere. The freshness gate is `Cluster.FollowerReadHealthy`.

`BOUNDED` is opt-in per session (`Session.SetReadConsistency(ReadBounded)` plus
`Session.SetMaxStaleness`; `0` selects the default window, five heartbeats). It
does **not** take a quorum round trip — it is a lag check against local Raft
state — so it is a cheap follower-servable read with a freshness floor, unlike
`STRONG`.

### `STALE`

A `STALE` read is served from the local node's applied Raft state with **no
freshness barrier**. Any member that still sees a leader can serve it. The
caller has explicitly traded freshness for locality and availability: the
result reflects everything this node has applied, which may lag the leader.

`STALE` is opt-in per session (`Session.SetReadConsistency`). It is never the
default and a `STALE` result is never labelled `STRONG`.

## Consistency model and sign-off (Phase 22)

This is the dated consistency review for the Phase 22 exit gate. It states
what each read mode guarantees, the argument that `STRONG` reads are
linearizable, and what a failover does not break. It is a review, not a proof
of zero defects.

### Guarantee per mode

| Mode | Guarantee | Served by |
|---|---|---|
| `STRONG` | Linearizable. A `STRONG` read observes every write whose commit was acknowledged before the read began, cluster-wide — not just this session's writes. Equivalent to a read placed in the single global commit order immediately before it executes. | Leader only, after a verified Raft read barrier |
| `BOUNDED` | Read-your-writes and monotonic reads are **not** guaranteed cross-node, but the serving node is provably within `MAX STALENESS` of the leader when it answers (or the read is rejected). Within one node the applied log is a prefix of the global commit order, so a single `BOUNDED` session pinned to one node sees monotonic state. | Leader, or a follower inside the freshness bound |
| `STALE` | The result reflects a prefix of the global commit order — every version it returns was really committed, in commit order, with no torn or phantom state — but that prefix may lag the leader by an unbounded amount. No cross-node recency guarantee. | Any member that still sees a leader |

No mode ever returns uncommitted, rolled-back, or reordered data. `BOUNDED`
and `STALE` trade **recency**, never **integrity**: a follower applies the
leader's WAL batches in log order behind the same MVCC visibility rules, so it
only ever exposes a consistent committed snapshot that is some prefix of the
leader's.

### Why `STRONG` reads are linearizable

A `STRONG` read runs only after `Cluster.StrongReadBarrier`
(`internal/replication/read.go`), which requires both:

1. **This node is the Raft leader.** Raft's election safety and leader
   completeness guarantee that a leader's log contains every entry that was
   committed in any previous term. So the leader's applied state is a superset
   of every acknowledged write.
2. **Leadership is still held, confirmed by a quorum round trip.**
   `raft.VerifyLeader` exchanges a heartbeat with a majority and succeeds only
   if that majority still recognises this node as leader in the current term.
   Because any two majorities intersect, a different node cannot have been
   elected and committed a newer write without this check seeing evidence of
   the term change. This blocks the **stale-leader anomaly**: a leader
   partitioned away from the quorum fails `VerifyLeader` and cannot answer a
   `STRONG` read from its now-possibly-stale log.

Between the barrier returning and the read executing, the node cannot have
silently lost and regained leadership without a term change that a subsequent
barrier would catch; writes are single-leader and go through the same FSM, so
the read sees a state at least as new as the barrier point, which already
dominates every pre-read acknowledged write. Reads take no Raft log entry —
the barrier is a read fence, not a replicated operation.

The barrier costs one quorum round trip per `STRONG` read. `nextsql-bench
--readscale` measures this directly: dropping the barrier (`STALE` on the
leader) roughly doubles single-node read throughput, so the `VerifyLeader`
round trip is the whole added cost. A leader-lease fast path (serve `STRONG`
from the leader without the round trip while a lease is provably valid) is a
**possible future optimization, deliberately not implemented** — it trades a
clock-bound assumption for latency and is not needed for correctness.

### Failover and session guarantees

A leader change does not violate a session's guarantees beyond its declared
mode:

- **`STRONG` across failover.** Any write acknowledged before the failover was
  committed to a majority, so the new leader's log contains it (leader
  completeness). The next `STRONG` read after the new leader is elected
  observes it — read-your-writes and monotonic reads hold across the leader
  change. While the router's cached view still points at the old leader a
  `STRONG` read may be rejected `unavailable` once; a retry lands on the new
  leader. Covered by `TestFollowerReadFailoverSessionGuarantee`
  (`tests/integration`) and `TestHAKillLeader` (`tests/ha`).
- **`STALE` / `BOUNDED` across failover.** A session that switched to `STALE`
  or `BOUNDED` and is routed to a different node after failover may observe an
  earlier prefix than a previous read on the old node — it can appear to go
  backwards. This is the **documented trade-off of those modes**, not a
  violation. A client that needs read-your-writes after a failover uses
  `STRONG` (or stays pinned to one node and accepts its `BOUNDED` freshness
  bound). The partitioned former leader never regresses below the state it had
  already applied; it only stops advancing until it rejoins.
- **No lost acknowledged commit.** Unchanged from Phase 15: quorum-synchronous
  commit plus leader completeness means a failover within covered failures
  never drops an acknowledged write. `TestHAKillLeader`,
  `TestHAPartitionRejectsMinorityWrites`.

### Test evidence

| Property | Test |
|---|---|
| Leader serves `STRONG`; every follower rejects it routably | `TestStrongReadBarrierLeaderOnly` |
| Partitioned former leader cannot serve `STRONG` from its own log | `TestStrongReadBarrierRejectsIsolatedLeader` |
| `BOUNDED` served only within the freshness bound, rejected once outside it | `TestHABoundedFollowerRead` |
| `STALE` is a distinct opt-in mode, never the default, never relabelled | `TestHAThreeNodeQuorumCommit`, `TestReadConsistencyModes` |
| `STRONG` session keeps read-your-writes + monotonic reads across a leader failover | `TestFollowerReadFailoverSessionGuarantee` |
| No acknowledged quorum commit lost on leader kill | `TestHAKillLeader` |
| Read-barrier cost isolated and measured | `nextsql-bench --readscale`, `TestReadScaleBench` |

### Sign-off

`STRONG` reads satisfy a linearizability guarantee under the covered failure
model (node crash, network partition, leader failover; not Byzantine faults,
not correlated loss of a majority's durable state, not clock-based leases —
none are used). `STALE` and `BOUNDED` results are always consistent committed
prefixes and are never mislabelled `STRONG`. Failover preserves session
guarantees within the declared mode. No consistency defect is tracked as open
after this review (2026-08-30).

## Replica lag and follower health

Each node exposes a key-free health snapshot through `system.replica_health`
(and the plaintext `nextsql.cluster.json` status file):

| Column | Meaning |
|---|---|
| `role` | `leader`, `follower`, `candidate`, or `shutdown` |
| `has_leader` | whether this node currently sees a cluster leader |
| `applied_lsn` | WAL LSN of the last batch this node's FSM installed |
| `commit_index` / `applied_index` | Raft log positions |
| `apply_backlog` | `commit_index - applied_index`: entries known committed but not yet applied locally (0 on the leader and a caught-up follower) |
| `last_contact_ms` | age of the last leader contact — `0` on the leader, `-1` on a follower that has never heard from a leader |
| `healthy` | leader, or a follower that sees a leader and was contacted within `HealthyContactWindow` (5 heartbeats) |

A follower that is partitioned from the leader stops being contacted; its
`last_contact_ms` grows past the window and `healthy` flips to `false`.

`Cluster.FollowerReadHealthy(maxStaleness)` is the shared gate for
bounded-staleness reads and follower-read routing: the leader always passes; a
follower passes only while it still sees a leader and — when `maxStaleness > 0` —
was contacted within that bound. A rejected node returns `unavailable` so the
caller can route elsewhere. `STALE` reads do **not** call this gate; they are
unbounded by definition.

## Follower-read routing

The native protocol carries two additive messages for routing:

- `SetReadConsistency` sets a connection's mode and `MAX STALENESS` window
  (`Conn.SetReadConsistency` in Go, `conn.setReadConsistency(mode, maxStalenessMs)`
  in the JS drivers, `Client::setReadConsistency` in PHP). The server applies it
  to the session; subsequent reads run under it.
- `NodeStatus` returns a node's key-free `NodeStatus` (role, `has_leader`,
  `healthy`, applied LSN, last-contact age, apply backlog) — the same snapshot
  as `system.replica_health`, without needing a SQL round trip or a
  non-`STRONG` session to read it (`Conn.NodeStatus` in Go,
  `conn.nodeStatus()` in the JS drivers, `Client::nodeStatus` in PHP).

Every official driver ships a routing client over the cluster members —
`nextsql.Cluster` (`OpenCluster` with `Config.Nodes`) in Go, `connectCluster`
in the Node / Bun / Deno drivers (`cfg.nodes`, `cfg.readConsistency`,
`cfg.maxStalenessMs`), and `NextSQL\Cluster::connect` in PHP
(`readConsistency` / `maxStalenessMs` config keys). With the read-consistency
mode set to `Bounded` or `Stale` it sends eligible read-only statements to a
healthy follower (round-robin, falling back to the leader) and everything
else — writes, DDL, transaction control, and `Strong` reads — to the leader. A
follower that rejects a routed read with `unavailable` is retried on the
leader. Node roles are re-probed with a 500 ms TTL via `NodeStatus`. Explicit
transactions and `EXPLAIN` always run on the leader. The router is a client
convenience — the server independently enforces every barrier.

### Writes

Writes are unaffected: they always route to the leader (see **Durability
policy**) or fail with `unavailable`. Transaction-control statements
(`BEGIN` / `COMMIT` / `ROLLBACK`) are leader-gated, so an explicit transaction —
read-only or not — runs only on the leader.

### Read scaling

`nextsql-bench --readscale` builds a 3-node single-leader cluster (encryption,
WAL, and fsync on) and drives primary-key point reads under five phases:
`STRONG` on the leader, `STALE` on the leader, `STALE` over the leader plus one
follower, `STALE` over all three, and `BOUNDED` over all three. It reports the
aggregate read QPS, the slice of it served by the leader (`leader-qps`), and the
aggregate ratio against the `stale-1n` baseline.

Two effects are measurable:

- **Read-barrier cost.** `STRONG` reads each pay a Raft `VerifyLeader` quorum
  round trip; `STALE` / `BOUNDED` reads serve local applied state and skip it.
  On the reference host `STALE` roughly doubles single-node read throughput
  (`~103k → ~202k` QPS) and halves p99 (`270 µs → 203 µs`).
- **Leader read-offload.** Routing `STALE` reads across all three members drops
  the leader's own read load ~3.5× (`~202k → ~57k` QPS) while the aggregate
  holds within ~15–20% — the residual is cross-node scheduling on a single
  shared host. `BOUNDED` tracks `STALE` because the leader always passes the
  freshness gate.

Aggregate read QPS is CPU-bound on one host — every "node" is goroutines on the
same cores — so it does not grow with node count here. A real deployment adds a
host per replica, converting the leader-offload into aggregate headroom.

Reference numbers (linux/amd64, 12 vCPU, ext4, AES-256-GCM, 10k rows, 8
readers/node, 5 s/phase):

| phase | mode | nodes | read-qps | leader-qps | p99 | agg-ratio |
|---|---|---|---|---|---|---|
| strong-1n | STRONG | 1 | 103,457 | 103,457 | 270 µs | — |
| stale-1n | STALE | 1 | 201,709 | 201,709 | 203 µs | 1.00× |
| stale-2n | STALE | 2 | 179,928 | 85,843 | 1.06 ms | 0.89× |
| stale-3n | STALE | 3 | 168,384 | 57,325 | 2.39 ms | 0.83× |
| bounded-3n | BOUNDED | 3 | 166,601 | 56,308 | 2.41 ms | 0.83× |

## Failover targets

These are engineering targets on a healthy 3-node cluster:

| Event | Target |
|---|---|
| Failure detection | seconds |
| Leader election | `< 3 s` |
| Service recovery (new leader accepts writes) | `< 5 s` |

Default Raft timeouts: heartbeat / election 250 ms, lease 200 ms,
commit 50 ms. Do not lower them to chase the target if the network
cannot support that.

## Replica repair and rolling maintenance

A lagging replica that can still reach the leader catches up from the
Raft log. Taking one voter down leaves a 2-of-3 quorum; writes continue
on the remaining leader. Bringing the node back reconnects it and it
applies missed batches.

A wiped replica is restored from `nextsql backup` / `nextsql restore`
(same identity and keys), then rejoined with `AddVoter`. Stolen Raft
logs are ciphertext: command payloads are AES-256-GCM under the
replication DEK.

## Operations

```text
nextsqld --data-dir DIR --key-file FILE \
  --node-id n1 --raft-bind 127.0.0.1:7211 \
  --raft-join n1=127.0.0.1:7211,n2=127.0.0.1:7212,n3=127.0.0.1:7213 \
  --raft-bootstrap

nextsql cluster status --data-dir DIR
nextsql status --local --data-dir DIR --key-file FILE
# default nextsql status (no --local) is a server ping (mode server)
```

Only one node bootstraps. The other two start with the same
`--raft-join` list and no `--raft-bootstrap`. All replicas of one
database share the keystore / root unlock key. `--key-file` is never a
URL.

Membership changes are audit events (`cluster.membership`). Audit
records never contain passwords, keys, tokens, or secrets.

## What this is not

- Not “guaranteed zero downtime”.
- Not a multi-primary write mesh.
- Follower-read routing ships in every official driver (Go, Node, Bun, Deno,
  PHP): the wire messages, `BOUNDED` mode, and per-driver cluster clients are
  implemented. The read-scaling benchmark is published above
  (`nextsql-bench --readscale`), and the Phase 22 exit gate is closed — see
  **Consistency model and sign-off** for the linearizability argument and the
  failover session-guarantee test. The router is a client convenience — the
  server independently enforces every barrier.
- Not a substitute for backups. Raft is the replication log; PITR is
  still `docs/backup.md`.
- A live unlocked leader still has keys in RAM. See `docs/security.md`.
