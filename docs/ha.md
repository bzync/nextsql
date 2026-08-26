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
- Not a substitute for backups. Raft is the replication log; PITR is
  still `docs/backup.md`.
- A live unlocked leader still has keys in RAM. See `docs/security.md`.
