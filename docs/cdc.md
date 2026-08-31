# CDC / change streams

## Current implementation status

P20 provides a committed-WAL CDC core and a native continuous SQL/NSQL surface.

Ordinary SQL `INSERT`, `UPDATE`, and `DELETE` mutations stage a logical
change in the same storage transaction as the row/index/page work. Trigger and
foreign-key cascade mutations pass through the same row mutation helpers. The
batched insert helpers and decimal patch helper also stage changes. The
optimized `BulkDeleteAll` heap-swap helper stages every primary-key delete in
the same transaction for batches up to 8,192 rows. Larger tables use bounded
8,192-row delete commits instead of producing an oversized logical batch.

## Durability and ordering

Each change is an encrypted `RecChange` / `NSCD` v1 WAL record. Changes for one
transaction are bounded in memory and appended contiguously immediately before
the transaction's `Commit` record. The CDC decoder buffers those records and
emits one atomic transaction only after it observes the matching commit.

Delivery order is:

```text
commit LSN order
  -> change LSN order within the committed transaction
```

An abort, crash-stranded batch, or transaction without a durable commit emits
nothing. Recovery ignores logical change records; physical page-image recovery
remains authoritative.

The delivery unit contains:

- database identity;
- stable table ID and table name at mutation time;
- INSERT/UPDATE/DELETE operation;
- encoded primary-key identity;
- old primary key for a key-changing UPDATE;
- current and old tenant identity where applicable;
- transaction ID;
- change LSN and commit LSN.
- optional before and after `NSRW` row images.

Image capture is a durable, explicit per-table policy:

```sql
ALTER TABLE orders SET CDC IMAGES FULL;
ALTER TABLE orders SET CDC IMAGES KEYS;
```

`KEYS` is the default and writes no row images. `FULL` writes before images for
UPDATE/DELETE and after images for INSERT/UPDATE. The policy affects future
changes only. Images are hex-encoded `NSRW` values in table column order.
Opt-in records still share the one-logical-page record cap and the per-
transaction byte cap; exceeding either aborts the write. This makes WAL
amplification explicit, bounded, and disabled by default.

## Resume and retention

The commit LSN is the resume token. A consumer persists the token only after it
has processed the entire transaction, then resumes strictly after that commit.
This provides ordered at-least-once delivery at the internal API boundary.

Subscriptions check the oldest locally retained WAL segment. A token older
than retained history fails with typed `not_found` / `change history expired`;
the implementation never silently jumps forward. An active subscription pins
its oldest required live-WAL LSN. Archived pruning and disposable checkpoint
pruning take the minimum of their recovery/PITR horizon and all CDC pins.
Closing or canceling the stream releases the pin, including NSQL error and idle
cleanup.

Pins are process-local, not durable replication slots. After restart, a client
must reconnect with its persisted token before maintenance advances past it;
otherwise `change history expired` is the explicit outcome.

## Native subscription surface

```sql
SUBSCRIBE TO orders;
SUBSCRIBE TO orders WHERE operation = 'UPDATE';
SUBSCRIBE TO orders AFTER 1842;
```

`AFTER` accepts an unsigned decimal commit LSN and resumes strictly after that
transaction boundary. `SUBSCRIBE` is continuous and cannot run inside an
explicit transaction. It may be prepared through the official driver. Cancel
the query context (or issue the native cancel operation) before closing a
stream; cancellation returns the typed `canceled` error and leaves the session
reusable.

The optional predicate is deliberately limited to equality on committed
operation metadata: `operation = 'INSERT'`, `'UPDATE'`, or `'DELETE'`. General
row predicates are rejected because historical/default events may omit images;
the server never fetches a later row version and misrepresents it as the
committed change.

Each row has string-typed columns in this order:

```text
operation, database_id, table_id, table, tenant, old_tenant,
primary_key, old_primary_key, transaction_id, change_lsn,
commit_lsn, resume_token, lag_lsn, before_image, after_image
```

Primary keys are the hexadecimal form of NextSQL's encoded composite key.
Identifiers and LSNs use unsigned decimal strings. All events from one
transaction are sent in one bounded vector batch when they fit the configured
batch limit; larger allowed transactions remain contiguous across batches and
share one resume token. Persist that token only after processing every row with
that value.

## Backpressure and resource bounds

The internal subscription is pull-based and owns no goroutine. `Next(ctx)`
returns one committed transaction at a time. A slow caller therefore does not
create an unbounded server queue.

Bounds cover:

- changes and bytes staged by one storage transaction;
- pending decoder transactions;
- changes and bytes retained by the decoder;
- records and bytes read by each WAL scan;
- polling cancellation through `context.Context`.

Limit exhaustion returns a typed error. Once staging fails, the storage
transaction is marked incomplete and cannot commit; rollback is required.
Embedded callers must call `Result.Close` when they stop without another pull;
the NSQL server does this automatically on every completion/error path.

The NSQL server sends one bounded batch and waits for `FlowAck` before pulling
the next transaction. The normal query timeout, idle timeout, packet limit,
and cumulative result-byte limit apply. A client that does not acknowledge a
batch is canceled at the idle deadline; it does not accumulate a server queue.

## Observability

`nextsql status` reports process-local `cdc_subscriptions`, `cdc_active`,
`cdc_transactions`, `cdc_events`, `cdc_errors`, and `cdc_lag_lsn`. Metrics do
not use table, tenant, key, or token labels. The stream also carries `lag_lsn`
on every event for consumer-side alerting.

## Database isolation

New CDC records and public subscriptions are scoped to the connection's
selected database. They do not derive or accept a row-tenant selector.
Historical WAL tenant fields remain decodable for recovery compatibility but
new writes leave them empty.

The public surface requires an explicit table-scoped `CDC` privilege:

```sql
GRANT CDC ON TABLE orders TO streamer;
REVOKE CDC ON TABLE orders FROM streamer;
```

Authorization is checked at statement admission and again for every stream
pull, so a revoke stops an open stream. The stable table-ID filter is applied
before delivery.
Subscription attempts are audit logged as `cdc.subscribe` without keys or
resume tokens. Transport uses the existing TLS-only production NSQL path.

## Persistence integration

Change records use the WAL DEK and existing authenticated WAL envelope. They
are included automatically in WAL archival, encrypted backup/PITR history, and
Raft WAL batches. A resumed subscription on a newly elected leader continues
strictly after the last acknowledged commit token. Restart and three-voter
leader-failover tests cover this path. No new plaintext sidecar or unversioned
persistent structure is introduced.

Older binaries that do not recognize `RecChange` fail closed when scanning a
WAL containing it. Downgrade/rollback therefore requires restoring a backup
whose WAL predates the first logical change record or using a migration tool
that explicitly understands this record type.
