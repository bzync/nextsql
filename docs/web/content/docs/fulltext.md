# Full-text search

```sql
CREATE TABLE articles (
    id    UUID PRIMARY KEY DEFAULT UUID(),
    title STRING NOT NULL,
    body  TEXT
);

CREATE FULLTEXT INDEX ix_body ON articles (body);
-- CREATE FULLTEXT INDEX ix_tb ON articles (title, body);
-- CREATE FULLTEXT INDEX ix_en ON articles (body) WITH (ANALYZER = 'english');

SELECT title FROM articles SEARCH body FOR 'database performance' LIMIT 20;
SELECT title FROM articles SEARCH title, body FOR 'database performance' LIMIT 20;
SELECT title FROM articles SEARCH title WEIGHT 3, body FOR 'database performance' LIMIT 20;
SELECT title FROM articles SEARCH body FOR '"database performance"';
SELECT title FROM articles SEARCH body FOR 'cat*';
SELECT title FROM articles SEARCH body FOR '"data* performance"';
SELECT title FROM articles SEARCH body FOR 'cat~';
SELECT title FROM articles SEARCH body FOR '"databas~ performance"';
SELECT title FROM articles SEARCH body FOR 'databse';
SELECT title FROM articles SEARCH body FOR '"databse performance"';
SELECT title, HIGHLIGHT(body) FROM articles SEARCH body FOR 'database performance';
SELECT title, SNIPPET(body) FROM articles SEARCH body FOR 'cat';
SELECT * FROM articles SEARCH body FOR 'database performance' FACET category;
SELECT * FROM articles SEARCH title WEIGHT 3, body FOR 'database' FACET category, year LIMIT 5;
```

`SEARCH col [, col …] FOR <string>` sits after `WHERE` / `GROUP BY` and before `FACET` / `LIMIT`. Unquoted tokens are required (AND) and ranked with **BM25**. A multi-column `SEARCH` uses a `FULLTEXT` index whose column list matches in the same order; phrases do not cross fields. Optional `WEIGHT <number>` after a column scales that field's BM25 term frequency (`SEARCH title WEIGHT 3, body FOR '…'`; omitted = 1; range `(0, 64]`; query-time only). A trailing ASCII `*` is prefix search (`cat*` matches `catalog`; exact `cat` does not). A trailing ASCII `~` is fuzzy matching (`cat~` matches `cot`; optional `~1` / `~2`; AUTO distance by token length). Prefix and fuzzy tokens skip stemming, stop words, and synonyms; matching terms are a disjunction at that position. Unadorned tokens apply typo tolerance only when the analyzed term is absent from the vocabulary (`databse` matches `database`; `cat` does not match `cot` when `cat` is indexed; AUTO typo is 0/1/2 for 1–4 / 5–8 / 9+ runes). Distinct prefix-, fuzzy-, or typo-matched terms consume the query-expansion caps and fail closed; fuzzy/typo edit-distance work inspects at most 4096 distinct vocabulary terms. Double-quoted groups are phrases (consecutive positions). Results are score descending, then primary key.

`HIGHLIGHT(col)` wraps original matching tokens in the full field (`<mark>` / `</mark>` by default). `SNIPPET(col)` returns a window around the densest match cluster (default 160 Unicode code points, range 16–4096, `…` on a truncated edge). Both require `SEARCH` and use the same analyzer as ranking (stems, synonyms, prefix, fuzzy, typo). `HIGHLIGHT(col, pre, post)` and `SNIPPET(col, width [, pre, post])` override markers (max 32 runes). They fail closed outside the SELECT list of a SEARCH query.

`SELECT * … SEARCH … FACET col [, col …]` returns independent histograms over the full match set (`facet`, `value`, `count`). `LIMIT` is per-facet top-N; `NULL` is skipped; 1–8 discrete columns and 1024 distinct values fail closed. Requires `SELECT *` and `SEARCH`.

## Tokenizer

Letters and digits; Unicode lowercase; hyphens split tokens; apostrophes inside a token are kept. Default analyzer `simple` does not stem and has no stop-word list — `cat` does not match `cats`, and `the` is searchable. `WITH (ANALYZER = 'english')` applies Snowball English (Porter2) plus stop-word dictionary v1 at index and query time, and synonym dictionary v1 at query time (`car` matches `automobile`; remaining terms stay consecutive for phrases). `french` / `german` / `spanish` apply that language's Snowball stemmer and stop list (French also elides `l'` / `qu'` / …).

`CREATE FULLTEXT INDEX` takes one to eight `STRING`/`TEXT` columns. It cannot be `UNIQUE`, cannot use a JSON path, and cannot list a column twice. Optional `WITH (ANALYZER = 'simple' | 'english' | 'french' | 'german' | 'spanish')`.

Without an index, `SEARCH` still runs over the heap (or a sargable `WHERE` path). `EXPLAIN` shows `Search … fulltext` or `Search … seq`.

## Limits

| Limit | Value |
|---|---|
| Term | 128 runes |
| Document | 100 000 tokens (combined across SEARCH fields) |
| Fields | 8 FULLTEXT/SEARCH columns |
| Field weight | `(0, 64]` (default 1) |
| Facet columns | 8 |
| Distinct values per facet | 1024 |
| Query | 64 tokens |
| Query expansion | 256 terms / 8192 bytes / 4096 work units |
| Fuzzy vocabulary scan | 4096 distinct terms |
| Highlight marker | 32 runes |
| Snippet width | 16–4096 Unicode code points (default 160) |

Engine note: [`docs/fulltext.md`](https://github.com/bzync/nextsql/blob/main/docs/fulltext.md).
