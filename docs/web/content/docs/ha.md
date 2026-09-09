# High availability

Optional Raft cluster ([hashicorp/raft](https://github.com/hashicorp/raft)). Minimum **3 voting nodes**. NextSQL does not invent consensus.

A write is acknowledged only after:

1. The leader executes the statement and flushes its local WAL.
2. The sealed replication batch is committed on a Raft quorum.

If there is no leader, writes fail closed (`unavailable`). SQL is **not** re-executed on followers, so `UUID()` / `NOW()` / `AI()` stay deterministic. Foreign-key cascades are sequences of those same insert/update/delete records produced on the leader.

RPO = 0 for acknowledged quorum-synchronous commits under the covered node-failure model (loss of the leader, or any minority). There is no split-brain write path.

Engineering targets on a healthy 3-node cluster: leader election `< 3 s`, service recovery `< 5 s`. Continuous service is a design objective (`≥ 99.999%` availability SLO), not a zero-downtime claim.

## Start three nodes

Only **one** node bootstraps. The other two use the same `--raft-join` list without `--raft-bootstrap`. All replicas share the keystore / root unlock key.

```bash
# node n1 (bootstrap)
./nextsqld --data-dir /var/lib/nextsql-n1 --key-file /etc/nextsql/root.key \
  --tls-cert cert.pem --tls-key key.pem --listen 0.0.0.0:7210 \
  --user app --password-file /tmp/nextsql.pw \
  --node-id n1 --raft-bind 10.0.0.1:7211 \
  --raft-join n1=10.0.0.1:7211,n2=10.0.0.2:7211,n3=10.0.0.3:7211 \
  --raft-bootstrap

# node n2
./nextsqld --data-dir /var/lib/nextsql-n2 --key-file /etc/nextsql/root.key \
  --tls-cert cert.pem --tls-key key.pem --listen 0.0.0.0:7210 \
  --user app --password-file /tmp/nextsql.pw \
  --node-id n2 --raft-bind 10.0.0.2:7211 \
  --raft-join n1=10.0.0.1:7211,n2=10.0.0.2:7211,n3=10.0.0.3:7211

# node n3 — same as n2 with n3 / 10.0.0.3
```

```bash
./nextsql cluster status --data-dir /var/lib/nextsql-n1
# node n1
# state Leader
# leader n1
# voters 3
# has_leader true
```

## Raft timings

The four Raft intervals are tunable, for an operator on a slower link or a
contended node. Each defaults to the value shown when left unset:

| Setting | Flag | Default |
|---|---|---|
| `raft_heartbeat_ms` | `--raft-heartbeat-ms` | 250 ms |
| `raft_election_ms` | `--raft-election-ms` | 250 ms |
| `raft_leader_lease_ms` | `--raft-leader-lease-ms` | 200 ms |
| `raft_commit_timeout_ms` | `--raft-commit-timeout-ms` | 50 ms |

`raft_leader_lease_ms` must not exceed `raft_heartbeat_ms`, and `raft_election_ms` must be at least `raft_heartbeat_ms`; both are consensus-protocol requirements and both are checked at configuration time, not at the next restart. Raising `raft_heartbeat_ms` also widens the follower-read freshness window — it is five heartbeats, and therefore the default `MAX STALENESS` of a `BOUNDED` read. Lowering these below what the network can carry makes a cluster re-campaign instead of converging, which is slower than the timeout it was trying to beat.

## Read consistency

Every read runs in one mode (session default `STRONG`):

- **`STRONG`** — linearizable: observes every write acknowledged before it began, cluster-wide. Served only on the leader, behind a Raft read barrier (`VerifyLeader` quorum round trip), so a partitioned former leader cannot answer one. Read-your-writes survives a leader failover.
- **`BOUNDED`** — served from a member within `MAX STALENESS` of the leader, or rejected. With no explicit bound it uses the cluster's healthy-contact window, five times its configured `raft_heartbeat_ms`. Cheap (no quorum round trip); no cross-node read-your-writes.
- **`STALE`** — served from any member's applied state, unbounded lag. Always a consistent committed prefix, never relabelled `STRONG`.

Every official driver ships a cluster routing client (`OpenCluster` / `connectCluster` / `NextSQL\Cluster::connect`) that sends eligible reads to a healthy follower and everything else to the leader. `nextsql-bench --readscale` measures the barrier cost and leader read-offload. Full argument: [`docs/ha.md`](https://github.com/bzync/nextsql/blob/main/docs/ha.md) "Consistency model and sign-off".

Replica repair has two proven paths: a lagging follower that reconnects, and a wiped replica restored with `nextsql backup` / `restore` (same identity and keys) then rejoined with `AddVoter`. Raft logs are ciphertext (replication DEK). HA is not a substitute for backups.

On Raft, connect migrators and writers to the **leader**.

Engine note: [`docs/ha.md`](https://github.com/bzync/nextsql/blob/main/docs/ha.md).
