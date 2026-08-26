# Full-text search

```sql
CREATE TABLE articles (
    id    UUID PRIMARY KEY DEFAULT UUID(),
    title STRING NOT NULL,
    body  TEXT
);

CREATE FULLTEXT INDEX ix_body ON articles (body);

SELECT title FROM articles SEARCH body FOR 'database performance' LIMIT 20;
SELECT title FROM articles SEARCH body FOR '"database performance"';
```

`SEARCH col FOR <string>` sits after `WHERE` / `GROUP BY` and before `LIMIT`. Unquoted tokens are required (AND) and ranked with **BM25**. Double-quoted groups are phrases (consecutive positions). Results are score descending, then primary key.

## Tokenizer

Letters and digits; Unicode lowercase; hyphens split tokens; apostrophes inside a token are kept. **No stemmer and no stop-word list** — `cat` does not match `cats`.

`CREATE FULLTEXT INDEX` takes one `STRING`/`TEXT` column. It cannot be `UNIQUE` and cannot use a JSON path.

Without an index, `SEARCH` still runs over the heap (or a sargable `WHERE` path). `EXPLAIN` shows `Search … fulltext` or `Search … seq`.

## Limits

| Limit | Value |
|---|---|
| Term | 128 runes |
| Document | 100 000 tokens |
| Query | 64 tokens |

Engine note: [`docs/fulltext.md`](https://github.com/bzync/nextsql/blob/main/docs/fulltext.md).
