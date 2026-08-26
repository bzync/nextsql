# Change streams

NextSQL CDC emits committed transactions from the encrypted WAL. Uncommitted,
aborted, and crash-stranded changes are never exposed.

```sql
GRANT CDC ON TABLE orders TO streamer;
SUBSCRIBE TO orders;
SUBSCRIBE TO orders WHERE operation = 'UPDATE';
SUBSCRIBE TO orders AFTER 1842;
```

`AFTER` is a commit-LSN resume token. Delivery is ordered by commit LSN and
then change LSN inside a transaction. Persist the token only after processing
all rows with that token for ordered at-least-once delivery.

The only v1 predicate is exact `operation` equality for `INSERT`, `UPDATE`, or
`DELETE`. General row predicates are rejected because historical and default
events may not contain a committed row image.

The result columns are:

```text
operation, database_id, table_id, table, tenant, old_tenant,
primary_key, old_primary_key, transaction_id, change_lsn,
commit_lsn, resume_token, lag_lsn, before_image, after_image
```

V1 identity is key-based: primary keys use hexadecimal NextSQL key encoding,
and row images are disabled by default. Opt in for future changes with
`ALTER TABLE orders SET CDC IMAGES FULL`; restore the bounded default with
`ALTER TABLE orders SET CDC IMAGES KEYS`. Full mode adds before images for
UPDATE/DELETE and after images for INSERT/UPDATE as hex `NSRW` values.
`SUBSCRIBE` cannot run in an explicit transaction.
Cancel the query context before closing the continuous result.

The server sends a bounded batch and waits for NSQL `FlowAck` before pulling
more WAL. Query, idle, packet, and result-byte limits still apply. Tokens older
than live WAL fail with `change history expired`. Active streams pin their
required live-WAL horizon until close/cancel. Pins are process-local, so after
restart a client must reconnect with its persisted token before pruning
advances past it.

`nextsql status` exposes process-local subscription, active-stream,
transaction, event, error, and latest-lag counters without table, tenant, key,
or token labels. Each event also includes `lag_lsn`.

For tenant-keyed tables, `SET TENANT` is required for non-admin sessions and
SQL cannot choose another subscription tenant. `CDC` is checked at admission
and on every pull, so revoke stops an open stream. Subscription control is
recorded as `cdc.subscribe` without keys or tokens.
