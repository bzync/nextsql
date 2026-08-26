# Server configuration

```text
nextsqld --data-dir DIR --key-file FILE
         [--listen 127.0.0.1:7210] [--config FILE]
         [--tls-cert FILE --tls-key FILE]
         [--require-client-key]
         [--user NAME --password-file FILE]
         [--auth-file FILE] [--audit-file FILE]
         [--buffer-pages N] [--log-level debug|info|warn|error]
         [--wal-archive DIR]
         [--node-id ID --raft-bind ADDR --raft-join id=addr,... [--raft-bootstrap]]
```

`--data-dir` is required (flag or config). `--key-file` is required unless `--require-client-key` is set.

## Config file (`--config`)

Simple `key=value`. Comments start with `#`. Unknown keys are rejected. Command-line flags override the file.

```text
data_dir=/var/lib/nextsql
key_file=/etc/nextsql/root.key
listen_addr=127.0.0.1:7210
log_level=info
buffer_pages=1024
tls_cert=/etc/nextsql/server.crt
tls_key=/etc/nextsql/server.key
require_client_key=false
audit_file=
wal_archive=/var/lib/nextsql-wal
max_inflight_queries=32
max_query_queue=128
query_queue_wait_ms=5000
max_result_rows=1000000
node_id=
raft_bind=
raft_join=
raft_bootstrap=false
```

## Admission and budgets

Every `Exec` takes an in-flight slot. If all slots are busy, the query queues. If the queue is full or the wait exceeds `query_queue_wait_ms`, the server returns `unavailable` instead of growing without bound.

Defaults: 32 in-flight, 128 queued, 5 s wait.

Per-query budgets (defaults): 64 MiB memory, 256 MiB spill, 1 GiB I/O, 30 s, 1 000 000 result rows / 64 MiB result bytes. Exceeding a budget fails with `exhausted`. Worker goroutines are bounded (`min(GOMAXPROCS, 8)` per query through a process pool).

Wire defaults: 1 MiB packet, 1 MiB SQL, 256 parameters, 64 prepared statements per session, 128 concurrent sessions, 60 s idle.

Structured logs never contain passwords, keys, tokens, or secrets.
