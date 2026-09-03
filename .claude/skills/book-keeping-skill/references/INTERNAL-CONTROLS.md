# Bookkeeping Internal Controls

Use controls proportional to company size and risk.

## Access and approval

Prefer separation between:

- initiating a payment/refund;
- approving it;
- posting the accounting record;
- reconciling the bank/processor;
- reopening a closed period.

When staffing makes strict segregation impossible, require compensating review and immutable logs.

## Data integrity

- Never physically delete posted financial entries during normal operations.
- Use unique source/provider transaction IDs to prevent duplicate posting.
- Keep `created_by`, `approved_by`, `posted_by`, timestamps and reversal links.
- Store reason codes for manual journals and adjustments.
- Lock closed periods and log reopen authorization.
- Keep document hashes or equivalent evidence when appropriate.

## Reconciliation

A reconciliation should record:

```text
account/source
statement period
statement ending balance
book ending balance
reconciling items
difference
preparer
reviewer
completed_at
```

A zero mathematical difference does not prove correct classification; material unusual items still need review.
