# Bookkeeping Workflow

## Sales / receivables

Track separately:

- customer/order/subscription reference;
- invoice number/date/due date;
- gross amount;
- discounts/credits;
- taxes/withholding metadata when applicable;
- payment(s);
- refunds/chargebacks;
- outstanding balance;
- payment-provider and bank settlement references.

Never overwrite the original invoice economics when a later credit/refund occurs.

## Purchases / payables

Capture:

- vendor identity;
- bill/document reference;
- purchase purpose/cost center/project;
- invoice/bill date and due date;
- expense/asset/prepayment classification candidate;
- tax/withholding metadata;
- approval evidence;
- payment and bank reference.

## Cash and bank

Reconcile book balance to external statement balance with an explicit list of reconciling items. Typical categories include outstanding payments, deposits in transit, processor settlements, fees, interest, bank charges and timing differences.

## Payment gateways

Do not post only the net bank deposit. Preserve the gross customer payment and separately record provider fees, refunds, chargebacks, reserves and FX when present.

## Period close handoff

At minimum resolve or document:

- unreconciled bank/processor items;
- old receivables/payables;
- duplicate or orphan transactions;
- uncategorized expenses;
- unmatched credits/refunds;
- suspense/clearing balances;
- missing source documents;
- transactions posted into an incorrect period.
