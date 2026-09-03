# Source and Verification Rules

## Official-source first

Use official DepEd/CHED sources for policy claims. Store URLs and last verification date.

## PDFs and enclosures

The web landing page may summarize an issuance while the controlling details are in a PDF enclosure/annex. For exact grading weights, tables, formulas, competencies, eligibility, or exceptions, inspect the official enclosure before implementing.

## Source hash

When downloading a source document for a production policy corpus, calculate a SHA-256 hash and store it with the retrieval timestamp. If a government-hosted file changes without changing its URL, the hash reveals that the corpus must be reviewed.

## Publication is not always effectivity

Record separately:

```text
issued_at
published_at
effective_from
effective_until
pilot_from
full_implementation_from
```

## Never infer supersession from year alone

A 2026 issuance does not automatically supersede a 2015 issuance. Supersession must be explicit or established provision-by-provision from authoritative text.

## Secondary sources

Secondary sources may point to a document, but mark the record unverified until an official source is checked.
