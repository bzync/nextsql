---
name: book-keeping-skill
description: Bzync Software Development Services bookkeeping skill for transaction capture, double-entry source records, invoicing/collections support, payables, cash and bank reconciliation, expense evidence, period controls, audit trails, and month-end bookkeeping handoff. Use for bookkeeping workflows, financial-data models, ledger integrations, reconciliation, source-document controls, or operational finance automation. For accounting judgments or financial-statement policy, coordinate with accounting-skill.
---

# Book-Keeping Skill

Maintain complete, traceable and reconcilable financial source records for **Bzync Software Development Services** and Bzync-managed projects.

Bookkeeping answers **what happened, when, for how much, between which accounts/parties, and what evidence supports it**. Do not silently turn bookkeeping classification into an accounting-policy conclusion when judgment is required.

## Core responsibilities

- transaction intake and source-document capture;
- customer invoices, receipts, collections and credit notes;
- supplier bills, payments and expense reimbursements;
- cash and bank activity;
- payment-gateway settlement and fee reconciliation;
- double-entry journals and posting controls;
- accounts receivable and accounts payable subledgers;
- tax/category tags without inventing current tax treatment;
- fixed-asset source register handoff;
- payroll summary interface without duplicating the payroll source of truth;
- bank/processor/subledger reconciliation;
- period close preparation and accounting handoff;
- immutable audit trail and correction history.

## Non-negotiable bookkeeping invariants

1. Every posted monetary event is traceable to a source or approved journal.
2. Posted journal entries balance: total debits = total credits.
3. Corrections use reversals/adjustments; do not erase posted history.
4. External transaction IDs are idempotency keys where available.
5. Money is stored with currency and appropriate precision; never use binary floating point for ledger amounts.
6. Original transaction amount, settlement amount, fees and FX effects remain distinguishable.
7. Reconciliation status is separate from posting status.
8. Closed periods cannot be silently rewritten.
9. Attachments/evidence have provenance and retention metadata.
10. Access to create, approve, post, reconcile and close should be separable when operationally feasible.

## Workflow

```text
source event/document
 -> validate required fields
 -> identify counterparty + business purpose
 -> classify accounts/tax tags
 -> draft entry/subledger record
 -> approval when required
 -> post immutable entry
 -> settle/collect/pay
 -> reconcile external statement
 -> investigate differences
 -> period close handoff
```

## Repository-aware behavior

When used inside a Bzync codebase, inspect the current project and `bzync-project` first when available.

For billing/payment systems map the real flow, for example:

```text
order/subscription/invoice
 -> payment intent/charge
 -> provider webhook
 -> customer receipt
 -> settlement batch
 -> provider fee
 -> bank deposit
 -> reconciliation
```

Do not equate a payment-provider `paid` event with revenue recognition. Bookkeeping records the event; `accounting-skill` determines recognition policy.

## Philippine compliance awareness

For Philippine books, invoices/receipts, tax tags, retention periods, registration, e-invoicing/e-receipting, filing or BIR-specific treatment:

- verify the **current** rule from official BIR/government authority before implementation;
- record the source, issuance/date and effective period;
- do not hardcode tax rates, thresholds or form requirements from memory;
- distinguish business registration form and tax registration facts from assumptions;
- escalate ambiguous statutory/accounting treatment to the responsible accountant/CPA or legal adviser.

## Output contract

For substantial bookkeeping work provide:

```text
Business event
Source/evidence
Proposed bookkeeping treatment
Debit/credit entries or data mapping
Subledger impact
Reconciliation method
Controls/approvals
Period/tax metadata
Exceptions requiring accountant review
Verification/tests
```

## References

- `references/BOOKKEEPING-WORKFLOW.md`
- `references/INTERNAL-CONTROLS.md`
- `references/CHART-OF-ACCOUNTS.md`
- `templates/MONTH-END-CHECKLIST.md`
- `schemas/journal-entry.schema.json`
