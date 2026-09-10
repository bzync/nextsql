# Workflows

Triggers and schedules invoke a workflow. Only asynchronous or scheduled execution becomes a durable **TASK**.

```arch
WORKFLOW
├── manual RUN WORKFLOW
├── TRIGGER invocation
└── SCHEDULE invocation
     ↓
    TASK
```

There is no second procedure language.

## WORKFLOW

```sql
CREATE WORKFLOW fulfill_order(
    order_id UUID,
    note TEXT
)
AS BEGIN
    UPDATE orders
       SET status = 'processing'
     WHERE id = $order_id;
    INSERT INTO order_events (id, order_id, note)
    VALUES (UUID(), $order_id, $note);
END;

RUN WORKFLOW fulfill_order($1, $2);
ALTER WORKFLOW fulfill_order RENAME TO ship_order;
DROP WORKFLOW IF EXISTS ship_order;
```

Parameters are immutable and referenced as `$name`. Bare identifiers remain column names. The body is parsed and bound at create time.

V1 bodies may contain bounded `INSERT` / `UPSERT` / `UPDATE` / `DELETE` and nested `RUN WORKFLOW`. Result-producing `SELECT`, DDL, `BEGIN` / `COMMIT` / `ROLLBACK`, and trigger/schedule DDL are rejected.

Manual `RUN WORKFLOW` is synchronous and atomic: autocommit wraps the whole invocation; inside an explicit transaction it participates in the caller's transaction. The result is one command with the sum of affected rows. V1 bodies cannot return row sets.

V1 always uses **invoker rights**. `CREATE` needs database `CREATE`; `RUN` needs `EXECUTE` on the workflow **and** every privilege the body statements require.

| Resource | V1 hard limit |
|---|---:|
| Parameters | 64 |
| Statements in one body | 256 |
| Nested workflow depth | 8 |
| Distinct workflows visited | 64 |
| Stored descriptor | 1 MiB |

`OR REPLACE`, output parameters, defaults, overloading, dynamic SQL, exception blocks, and security-definer execution are not part of v1.

## TRIGGER

Row-level only. A trigger invokes an existing workflow:

```sql
CREATE TRIGGER audit_order_insert
AFTER INSERT ON orders
FOR EACH ROW
RUN WORKFLOW record_order_event(NEW.id, 'insert');
```

`INSERT` exposes `NEW.column`, `DELETE` exposes `OLD.column`, `UPDATE` exposes both. V1 `BEFORE` triggers cannot mutate `NEW`. Every invocation is synchronous in the originating DML transaction; an error aborts the statement. Nesting is capped at 8.

## SCHEDULE and TASK

```sql
CREATE SCHEDULE hourly
EVERY '1h'
RUN WORKFLOW rollup('hour');

CREATE SCHEDULE once
AT '2026-08-25T00:00:00Z'
RUN WORKFLOW close_period('august');

CREATE SCHEDULE weekday_report
CRON '30 3 * * 1-5'
RUN WORKFLOW rollup('daily');

SHOW TASKS AFTER 's/00000007/1787616000000000000' LIMIT 100;
CANCEL TASK 's/00000007/1787616000000000000';
```

`EVERY` is a Go-style duration from 1s through 8760h. `AT` is a future RFC 3339 timestamp stored as UTC. `CRON` is a five-field expression evaluated in **UTC**. Scheduled invocation stores the schedule creator, then reapplies that principal and rechecks privileges on every task attempt.

Inspect live state with `system.workflows`, `system.tasks`, `system.triggers`, and `system.schedules`. See [System catalog](/docs/system-catalog).

Engine note: [`docs/workflows.md`](https://github.com/bzync/nextsql/blob/main/docs/workflows.md).
