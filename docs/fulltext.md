# Full-text search (Phase 10)

Native inverted index on `STRING` / `TEXT` columns. Tokenization, postings, BM25, and phrase search use the same WAL, MVCC, and page-encrypted B+Tree as every other secondary index.

## SQL

```sql
CREATE FULLTEXT INDEX ix_body ON articles (body);

SELECT * FROM articles SEARCH body FOR 'database performance' LIMIT 20;
SELECT title FROM articles WHERE title = 'one' SEARCH body FOR 'cat';
SELECT title FROM articles SEARCH body FOR '"database performance"';
```

`CREATE FULLTEXT INDEX` requires one `STRING` or `TEXT` column. It cannot be `UNIQUE` and cannot use a JSON path.

`SEARCH col FOR <string>` is a `SELECT` clause (after `WHERE` / `GROUP BY`, before `LIMIT`). The query is a string literal or a parameter. Combined with `NEAREST`, SEARCH remains a required-term filter and also contributes a BM25 rank to hybrid RRF (`docs/optimizer.md`).

`SEARCH` may be combined with `INNER JOIN` when the search column belongs to the `FROM` table. The plan ranks that table first (`Search` / `Rerank`), then inner-joins the ranked stream. Rank order is preserved through a 1:1 join; a 1:N join duplicates ranks. A search column on a joined table is rejected. Outer join + `SEARCH` is not supported. The engine does not insert an implicit `ORDER BY`; an explicit `ORDER BY` re-sorts after ranking.

Unquoted query tokens are required terms (AND) and are ranked with BM25. Double-quoted groups are phrases: those tokens must appear at consecutive positions. Results are ordered by score descending, then primary key.

Without a full-text index, `SEARCH` still runs: it tokenizes matching heap rows (or a sargable access path from `WHERE`) and scores them the same way. `EXPLAIN` shows `Search … fulltext` when the inverted index is used, or `Search … seq` otherwise.

## Tokenizer

Letters and digits form tokens. Text is folded with Unicode lowercasing. Hyphens and other punctuation split tokens (`noise-cancelling` → `noise`, `cancelling`). Apostrophes inside a token are kept (`don't`). There is no stemmer and no stop-word list: `cat` does not match `cats`.

Limits (fail closed on `ANALYZE` / `SEARCH`): term 128 runes, document 100 000 tokens, query 64 tokens, query text 1 048 576 runes.

## Inverted index

Each full-text index is a detached B+Tree (encrypted pages, WAL, MVCC). Keys:

| Kind | Key | Value |
|---|---|---|
| stats | `0x00` | version, document count, token count |
| posting | `0x01` + term + `0x00` + primary key | version, tf, positions |
| doclen | `0x02` + primary key | version, token count |

`NULL` and empty text produce no postings. Insert, update, delete, `CREATE INDEX` on existing rows, and rollback maintain the tree the same way as other secondary indexes.

BM25 uses Lucene-style IDF `ln(1 + (N − df + 0.5) / (df + 0.5))` with `k1 = 1.2` and `b = 0.75`. Scores are tested against known fixtures.

## Encryption

Postings live in ordinary table pages. Production files, WAL, and UNDO stay encrypted. Distinctive terms must not appear as readable plaintext on disk.

## Examples

```sql
CREATE TABLE articles (
    id    UUID PRIMARY KEY DEFAULT UUID(),
    title STRING NOT NULL,
    body  TEXT
);

INSERT INTO articles (title, body) VALUES
    ('one', 'the cat sat'),
    ('two', 'the cat sat on the mat'),
    ('four', 'database performance tuning');

CREATE FULLTEXT INDEX ix_body ON articles (body);

SELECT title FROM articles SEARCH body FOR 'cat';
SELECT title FROM articles SEARCH body FOR '"database performance"';
```
