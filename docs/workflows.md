# Workflows, triggers, schedules, and tasks (P19)

This document defines the native NextSQL P19 contract. The synchronous manual
workflow slice is implemented in the parser, binder, versioned catalog,
planner, and executor. Its parser/descriptor fuzz, transaction, restart/crash,
backup/PITR, WAL/Raft failover, RBAC/database isolation, resource, race, and
TLS driver gates pass as of 2026-08-24. The synchronous row-trigger slice also passes its
parser/descriptor fuzz, atomicity, restart/crash, backup/PITR, WAL/Raft
failover, RBAC/database isolation, cycle/depth, race, audit-redaction, and TLS
driver gates.
The schedule and durable task runtime are implemented, and a clean
repository-wide functional invocation passed on 2026-08-29. P19 uses one
model:

```text
WORKFLOW
├── manual RUN WORKFLOW
├── TRIGGER invocation
└── SCHEDULE invocation
     ↓
    TASK (only for asynchronous or scheduled execution)
```

Triggers, schedules, and durable tasks build on the same workflow descriptor
and do not introduce a second procedure language.

## Canonical workflow SQL

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
DROP WORKFLOW IF EXISTS fulfill_order;
```

The v1 grammar is:

```text
create_workflow := CREATE WORKFLOW [IF NOT EXISTS] name
                   '(' [parameter {',' parameter}] ')'
                   AS BEGIN workflow_statement {';' workflow_statement} [';'] END

parameter       := name data_type

run_workflow    := RUN WORKFLOW name '(' [expression {',' expression}] ')'

alter_workflow  := ALTER WORKFLOW name RENAME TO name

drop_workflow   := DROP WORKFLOW [IF EXISTS] name
```

`OR REPLACE`, output parameters, default arguments, variadic arguments,
overloading, dynamic SQL, exception blocks, transaction-control statements,
and security-definer execution are not part of v1. A workflow name is unique
within its database, so argument types do not participate in name resolution.
Unquoted names follow normal lowercase folding; quoted names are preserved.

Parameter types use the native SQL types. Parameters are immutable and are
referenced as `$name` in the body. Bare identifiers remain column names, so
there is no implicit parameter-versus-column precedence. An undeclared or
positional parameter in a body is rejected at create time. Argument count is
checked by the binder. Values are evaluated and coerced to the declared types
immediately before the invocation starts.

## Body validation and dependencies

The complete body is parsed and bound when the workflow is created. NextSQL
stores a versioned descriptor containing the normalized AST, parameter types,
and owner. It does not persist raw Go structs or reparse unchecked text during
execution. Each body statement is rebound against the transaction's visible
catalog immediately before execution.

V1 bodies may contain bounded DML (`INSERT`, `UPSERT`, `UPDATE`, and `DELETE`)
and synchronous `RUN WORKFLOW`. Result-producing `SELECT`, DDL, `BEGIN`,
`COMMIT`, `ROLLBACK`, `CREATE DATABASE`, maintenance statements, administrative
statements, and task/schedule/trigger DDL are rejected in a body. This keeps the
first transaction and result contract explicit.

Create fails if a referenced table, column, nested workflow, or required type
is missing. The descriptor records stable table and nested-workflow IDs plus
their names. A referenced table cannot be altered or dropped, and a referenced
workflow cannot be renamed or dropped. Catalog reload rejects mismatched
dependency identities. Self-invocation and forward workflow references are
rejected, preventing indirect cycles in the current grammar, while runtime
depth checking remains defense in depth.

## Transaction and durability semantics

Manual `RUN WORKFLOW` is synchronous and atomic:

- In autocommit mode, one transaction contains the entire workflow invocation,
  including nested workflow calls.
- Inside an explicit transaction, the workflow participates in the caller's
  transaction and does not commit independently.
- The first failed statement aborts the invocation. In autocommit mode the
  whole transaction rolls back. In an explicit transaction the session follows
  the same aborted-transaction behavior as other statement failures.
- Workflow catalog changes use the ordinary encrypted, authenticated catalog
  transaction and WAL path. Targeted crash recovery, backup/restore, LSN PITR,
  WAL replication, leader-write gating, and TLS driver tests cover the manual
  slice. Trigger, schedule, and task replication/failover gates are also covered.

`RUN WORKFLOW` returns one command result with the sum of affected rows from its
top-level body statements. V1 bodies cannot return row sets. Audit records carry
the workflow action, name, caller, outcome, and remote identity when available,
but never parameter values or secrets. Realm/database, elapsed-time, and affected-row
audit fields remain future audit-schema work.

## Hosted-database authorization semantics

V1 workflows always use invoker rights. Creating one requires database
`CREATE`; altering or dropping one requires the corresponding `ALTER` or
`DROP` privilege on function scope for that workflow. Running one requires
`EXECUTE` on function scope for that workflow.
The invoker must also hold every privilege required by every body statement;
`EXECUTE` never bypasses table or administrative authorization.

A workflow captures no row-tenant identity. Each invocation remains inside the
database selected when the connection/task is admitted. Synchronous trigger
invocation uses the DML caller's principal. Scheduled invocation stores the
schedule creator, then reapplies that principal and rechecks workflow/body
privileges on every task attempt. New schedule/task descriptors leave legacy
tenant fields empty. Neither path silently acquires elevated rights or changes
the selected database.

## Resource bounds

The server rejects a workflow descriptor or invocation that exceeds any hard
limit. V1 limits are deliberately small and deterministic:

| Resource | V1 hard limit |
|---|---:|
| Parameters | 64 |
| Statements in one workflow body | 256 |
| Nested workflow depth | 8 |
| Distinct workflows visited by one invocation | 64 |
| Stored normalized descriptor | 1 MiB |

Every statement also consumes the caller's existing time, memory, I/O,
affected-row, result-byte, and cancellation budgets. Nested calls share those
budgets rather than resetting them. Limit exhaustion returns `exhausted` and
rolls back according to the transaction rules above. Synchronous workflow
execution does not start a goroutine per invocation and does not create a
durable task.

## Row triggers

The native v1 trigger grammar is deliberately row-level and invokes an
existing workflow:

```sql
CREATE TRIGGER audit_order_insert
AFTER INSERT ON orders
FOR EACH ROW
RUN WORKFLOW record_order_event(NEW.id, 'insert');

ALTER TRIGGER audit_order_insert RENAME TO audit_created_order;
DROP TRIGGER IF EXISTS audit_created_order;
```

```text
create_trigger := CREATE TRIGGER [IF NOT EXISTS] name
                  (BEFORE | AFTER) (INSERT | UPDATE | DELETE)
                  ON table FOR EACH ROW
                  RUN WORKFLOW workflow '(' [trigger_expr {',' trigger_expr}] ')'

alter_trigger  := ALTER TRIGGER name RENAME TO name
drop_trigger   := DROP TRIGGER [IF EXISTS] name
```

`trigger_expr` is a deterministic expression over literals and event rows.
`INSERT` exposes `NEW.column`, `DELETE` exposes `OLD.column`, and `UPDATE`
exposes both. Bare column names, SQL/session parameters, subqueries, and
volatile calls are rejected. V1 `BEFORE` triggers cannot replace or mutate the
pending `NEW` row; they may only run the referenced workflow before the row is
written. `AFTER` triggers run after the row and its indexes have been changed,
but before the transaction can commit.

Every row invocation is synchronous and belongs to the originating DML
transaction. A trigger or workflow error aborts the entire DML statement and
transaction according to the workflow rules above. Trigger arguments are
coerced to workflow parameter types. Trigger order is deterministic by stable
trigger ID, never map iteration order. Trigger nesting is capped at 8 and all
nested workflows share the caller's query budget. Static dependency analysis
rejects trigger/workflow mutation cycles; the runtime depth cap remains a
fail-closed defense.

Triggers use invoker rights inside the caller's admitted database. The caller must
hold the original DML privilege, `EXECUTE` on each invoked workflow, and all
body privileges. Trigger definitions store stable table/workflow IDs and are
replicated as catalog WAL. Followers apply resulting WAL rather than firing
the trigger independently.

## Schedules and durable tasks

The active P19 schedule/task implementation uses native SQL:

```sql
CREATE SCHEDULE hourly
EVERY '1h'
RUN WORKFLOW rollup('hour');

CREATE SCHEDULE once
AT '2026-08-25T00:00:00Z'
RUN WORKFLOW close_period('august');

ALTER SCHEDULE hourly RENAME TO hourly_rollup;
DROP SCHEDULE IF EXISTS once;

SHOW TASKS AFTER 's/00000007/1787616000000000000' LIMIT 100;
CANCEL TASK 's/00000007/1787616000000000000';
```

`EVERY` accepts a native Go-style duration from one second through 8760 hours.
`AT` accepts a future RFC 3339 timestamp and is stored as a canonical UTC Unix
nanosecond. V1 arguments are literals, are capped at 64, and are type-checked
against the referenced workflow at definition time. A schedule stores its
stable workflow ID, owner, creation time, last firing, and next firing. The
versioned legacy tenant field is empty for new schedules.

The leader reads a chronological schedule index in batches of at most 256. A
firing creates a deterministic task ID from the stable schedule ID and due
time, then advances the schedule cursor in the same catalog transaction and
replicated WAL outcome. A one-shot schedule disables itself. A recurring
schedule whose leader clock jumps forward emits one task for its oldest due
boundary and advances directly to the first future boundary; it does not burst
one task per missed interval. Followers reject dispatch and task claims.

Task descriptors are versioned, bounded to 1 MiB, and contain the durable ID,
state, source, owner, stable workflow identity, schedule identity,
literal arguments, due/update times, attempt limits, timeout, exponential
backoff base, lease, idempotency key, concurrency policy, cancellation flag,
bounded error metadata, and retention deadline. Arguments are executed but are
not returned by `SHOW TASKS` or written to audit/error metadata.

The runtime has one coordinator and a fixed worker pool (default two, hard
maximum sixteen). It reserves capacity before claiming and never creates a
goroutine per task. Due, active-concurrency, workflow-dependency, owner, and
retention indexes make polling, dependency checks, pagination, and cleanup
bounded independently of retained history. `FORBID` reserves a source when its
task is created; while that task is pending, running, or retrying, later due
boundaries advance without queueing another task. Terminal history is retained
for seven days and purged in batches of at most 256.

Claiming changes a task to `RUNNING`, increments its attempt, and installs a
durable lease. A lease deadline is itself in the due index, so a new leader or
restarted process can reclaim abandoned work. The workflow body and
`SUCCEEDED` transition commit in one transaction. A failed body rolls back
before `RETRYING` or `FINAL_FAILED` metadata is persisted. Consequently an old
and a replacement worker may both compute after a severe pause or clock jump,
but the task-row write conflict permits only one committed database effect.
Workflows cannot perform external side effects or dynamic SQL in this version.

Scheduled execution uses invoker rights as the stored schedule owner in the
admitted database. `EXECUTE` and every body privilege are rechecked on each attempt;
revocation therefore fails closed. Non-admin `SHOW TASKS` uses an owner index
and does not expose another principal's tasks. Non-admin cancellation requires
ownership. A durable
cancellation write fences a late success before the local worker is signaled.

### Clock and failover semantics

Leader wall time decides only whether a persisted next-fire or lease deadline
is eligible. It does not order commits. A backward clock step delays firing and
lease recovery until the clock catches up. A forward step applies the skipped-
interval rule above and may cause an expired lease to be reclaimed early. The
original worker is fenced by the atomic task-row update. Schedule cursor/task
creation and all task state transitions are replicated before acknowledgement,
so a new leader continues from the committed cursor and deterministic task ID
without creating a second firing for that boundary.

This section describes the completed native P19 v1 slice. Its targeted
functional/race/fuzz, backup/PITR, three-node Raft, TLS driver, and
documentation gates pass. The repository-wide functional gate also passed on
2026-08-29.

## Required verification before claiming shipment

The manual workflow increment requires parser tests and fuzzing, descriptor
decoder fuzzing, binder/type/dependency tests, autocommit and explicit
transaction rollback tests, restart/crash/WAL recovery, backup/restore and PITR,
leader/follower behavior, adversarial RBAC and database-isolation tests, nested
cycle/depth tests, cancellation and resource-limit tests, protocol/driver tests,
audit redaction tests, and documentation updates. The trigger increment adds
event-row validation, all six timing/event combinations, atomic failure,
catalog restart/crash, backup/PITR, deterministic WAL/Raft failover, invoker
RBAC and database isolation, static cycle and runtime depth defenses, audit
redaction, race, fuzz, and TLS prepared-driver tests. The implemented schedule
and task increment additionally requires deterministic duplicate prevention,
clock-step and lease recovery behavior, retry/final-failure atomicity, durable
cancellation, retention, fixed-worker backpressure, PITR, failover, and TLS
driver coverage.
