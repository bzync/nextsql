# Business and Finance Governance

Use the specialist skill that owns the question instead of blending distinct responsibilities.

## `book-keeping-skill`

Use for:

- transaction/source-document capture;
- invoice/payment/refund/fee records;
- AR/AP;
- journals;
- bank/payment-provider reconciliation;
- period posting controls;
- audit trail and financial-data integrity.

## `accounting-skill`

Use for:

- recognition/measurement policy;
- accruals/deferrals;
- revenue recognition;
- capitalization/depreciation;
- FX accounting;
- month/year-end close;
- financial statements;
- accounting-policy judgments.

## `business-skill`

Use for:

- business model and offer design;
- pricing/packaging;
- unit economics;
- forecasting;
- sales/go-to-market;
- customer/operating metrics;
- investment/business cases;
- growth, profitability and operational tradeoffs.

## Cross-functional financial feature rule

For billing, subscription, checkout, payment, credit, refund, wallet, invoicing, reseller or revenue-sensitive features, review all relevant layers:

```text
business economics / customer promise
 -> accounting recognition requirements
 -> bookkeeping transaction/audit requirements
 -> architecture/data/API design
 -> implementation/UI
 -> reconciliation/close reporting
 -> telemetry and controls
```

A technically correct payment feature is incomplete if it cannot be reconciled or produces ambiguous accounting records.

## Compliance-sensitive decisions

Never assume current Philippine tax/accounting/registration requirements from memory. Resolve the entity/context, verify current official sources and effective dates, and flag professional review where required.
