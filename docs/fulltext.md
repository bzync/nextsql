# Full-text search (Phase 10)

Native inverted index on `STRING` / `TEXT` columns. Tokenization, postings, BM25, and phrase search use the same WAL, MVCC, and page-encrypted B+Tree as every other secondary index.

## SQL

```sql
CREATE FULLTEXT INDEX ix_body ON articles (body);
-- CREATE FULLTEXT INDEX ix_tb ON articles (title, body);
-- CREATE FULLTEXT INDEX ix_en ON articles (body) WITH (ANALYZER = 'english');
-- CREATE FULLTEXT INDEX ix_fr ON articles (body) WITH (ANALYZER = 'french');

SELECT * FROM articles SEARCH body FOR 'database performance' LIMIT 20;
SELECT * FROM articles SEARCH title, body FOR 'database performance' LIMIT 20;
SELECT * FROM articles SEARCH title WEIGHT 3, body FOR 'database performance' LIMIT 20;
SELECT title FROM articles WHERE title = 'one' SEARCH body FOR 'cat';
SELECT title FROM articles SEARCH body FOR '"database performance"';
SELECT title FROM articles SEARCH body FOR 'cat*';
SELECT title FROM articles SEARCH body FOR '"data* performance"';
SELECT title FROM articles SEARCH body FOR 'cat~';
SELECT title FROM articles SEARCH body FOR '"databas~ performance"';
SELECT title FROM articles SEARCH body FOR 'databse';
SELECT title FROM articles SEARCH body FOR '"databse performance"';
SELECT title, HIGHLIGHT(body) FROM articles SEARCH body FOR 'database performance';
SELECT title, SNIPPET(body) FROM articles SEARCH body FOR 'database performance';
SELECT title, HIGHLIGHT(body, '**', '**') FROM articles SEARCH body FOR 'cat*';
SELECT title, SNIPPET(body, 120, '<em>', '</em>') FROM articles SEARCH body FOR 'databse';
SELECT * FROM articles SEARCH body FOR 'database performance' FACET category;
SELECT * FROM articles SEARCH title WEIGHT 3, body FOR 'database' FACET category, year LIMIT 5;
```

`CREATE FULLTEXT INDEX` takes one to eight `STRING` or `TEXT` columns, in order. It cannot be `UNIQUE`, cannot use a JSON path, and cannot list a column twice.

Optional `WITH (ANALYZER = 'simple' | 'english' | 'french' | 'german' | 'spanish')` selects a versioned analyzer. The default is `simple` (Phase 10 tokenizer, no stemming, no stop-word list), so existing indexes and `SEARCH` stay compatible: `cat` does not match `cats`, and `the` is a searchable term. `english` is Snowball English (Porter2) plus stop-word dictionary v1 plus synonym dictionary v1. Stops and stems are applied identically at index time and query time so BM25 and phrase positions stay aligned. Synonym expansion is query-time only (index terms stay 1:1): alternatives share the query token's position and are a disjunction, so `car` matches `automobile` and `"red car"` matches `red automobile`. BM25 scores the best alternative in each group. New `ANALYZER = 'english'` indexes store revision 3; revision 1 (stem only) and revision 2 (stem + stops) still decode and search with those pipelines (no synonym expansion). `french`, `german`, and `spanish` are Snowball 3.x stemmers plus that language's Snowball stop-word dictionary v1 (catalog revision 1). French elides `l'` / `d'` / `qu'` / … before the stop list so `l'homme` matches `homme`. Unknown analyzer names and unknown catalog revisions fail closed. `EXPLAIN` shows `analyzer=<name>` when a non-simple analyzer is used.

Analyzer metadata lives on the `NSCT` table descriptor (format v9: per-index analyzer id + revision) and therefore participates in WAL, recovery, replication, encryption, and backup/restore. Replicas run the same deterministic stemmer, stop-word list, and synonym dictionary.

`SEARCH col [, col …] FOR <string>` is a `SELECT` clause (after `WHERE` / `GROUP BY`, before `FACET` / `LIMIT`). The query is a string literal or a parameter. Combined with `NEAREST`, SEARCH remains a required-term filter and also contributes a BM25 rank to hybrid RRF (`docs/optimizer.md`). A multi-column `SEARCH` uses a `FULLTEXT` index whose column list matches in the same order; a different subset or order falls back to a sequential scan of those columns. Fields are analyzed independently and scored as one BM25 document (term frequency and length are summed). Optional `WEIGHT <number>` after a column scales that field's term frequency (`SEARCH title WEIGHT 3, body FOR '…'`); omitted weights are 1, so unweighted SEARCH keeps the same BM25. Weights are query-time only (no catalog or posting-format bump) and apply to inverted-index and seq-scan SEARCH, including hybrid RRF. A weight must be a finite numeric literal in `(0, 64]`; zero, negative, non-finite, and oversized values fail closed. `WEIGHT` is not a reserved keyword, so a column named `weight` still works. Phrase matching is per-field: `"database performance"` does not match `title=database` plus `body=performance`. Duplicate columns fail closed. `HIGHLIGHT(col)` / `SNIPPET(col)` still wrap one column at a time against the same query.

`SEARCH` may be combined with `INNER JOIN` when the search column belongs to the `FROM` table. The plan ranks that table first (`Search` / `Rerank`), then inner-joins the ranked stream. Rank order is preserved through a 1:1 join; a 1:N join duplicates ranks. A search column on a joined table is rejected. Outer join + `SEARCH` is not supported. The engine does not insert an implicit `ORDER BY`; an explicit `ORDER BY` re-sorts after ranking.

Unquoted query tokens are required terms (AND) and are ranked with BM25. A trailing ASCII `*` on a token is prefix search: `cat*` matches indexed terms that start with `cat` (`cat`, `catalog`, …); exact `cat` still does not match `catalog`. Prefix tokens skip stemming, stop-word filtering, and synonym expansion (a truncated word is not a complete token); French elision still applies, so `l'hom*` is prefix `hom`. Matching terms are a disjunction at that position (AND with other groups). Phrase slots accept a prefix (`"data* performance"`). BM25 scores the best matching term in each prefix group. Distinct prefix-matched terms consume the query-expansion caps and fail closed. A leading or infix `*` is not a wildcard (`*cat` is exact `cat`; `c*t` is AND of `c` and `t`).

A trailing ASCII `~` is fuzzy matching: `cat~` matches indexed terms within a bounded OSA Damerau-Levenshtein distance (insert, delete, substitute, adjacent transpose), so `cot` matches and `catalog` does not. Distance is AUTO from the token's rune length (0 for 1–2 runes, 1 for 3–5, 2 for 6+) or explicit `~1` / `~2`. `~0` and `~3` or higher fail closed. Fuzzy tokens skip stemming, stop-word filtering, and synonym expansion; French elision still applies (`l'homm~` is fuzzy `homm`). Matching terms are a disjunction at that position. Phrase slots accept a fuzzy term (`"databas~ performance"`). BM25 scores the best matching term in each fuzzy group. Distinct fuzzy-matched terms consume the query-expansion caps and fail closed. A fuzzy or typo-tolerant query may inspect at most 4096 distinct vocabulary terms before failing closed; edit-distance evaluation uses bounded linear memory. Mixing `*` and `~` on one token fails closed. A leading or infix `~` is not fuzzy (`~cat` is exact `cat`).

Unadorned tokens (no trailing `*` / `~`) stay exact when any analyzed alternative is in the searchable vocabulary, so `cat` does not match `cot` when `cat` is indexed. When every alternative is absent, SEARCH applies typo tolerance: the group is rewritten as an AUTO-distance fuzzy group (`databse` matches `database`). Typo AUTO is stricter than explicit `~` (0 for 1–4 runes, 1 for 5–8, 2 for 9+) so short exact queries stay Phase 10 compatible (`cats` does not match `cat`; `cta` does not match `cat`). Prefix and explicit fuzzy groups are unchanged. Phrase slots follow the same rule (`"databse performance"`). Distinct typo-matched terms consume the query-expansion caps and fail closed. Seq-scan `SEARCH` without an index uses the scanned corpus as the vocabulary. Double-quoted groups are phrases: those tokens must appear at consecutive positions. Results are ordered by score descending, then primary key.

`HIGHLIGHT(col)` and `SNIPPET(col)` are SELECT-list functions that require `SEARCH`. They mark original document tokens whose analyzed form participates in the same query (exact, synonym, prefix, fuzzy, and typo), using the SEARCH column's analyzer. Stemming wraps the source token (`runs` marks `running`). Default markers are `<mark>` and `</mark>`. `HIGHLIGHT(col, pre, post)` and `SNIPPET(col, width [, pre, post])` override markers; each marker is at most 32 runes and must not contain NUL. `HIGHLIGHT` returns the full field. `SNIPPET` returns a window of `width` Unicode code points (default 160, range 16–4096) around the densest match cluster, with `…` on a truncated edge. Both fail closed outside the SELECT list of a SEARCH query. Seq-scan SEARCH highlights the same way.

`SELECT * … SEARCH … FACET col [, col …]` replaces document rows with independent histograms over the **full** SEARCH match set (`WHERE` residual included; ranking `LIMIT` is not a document page). Each facet column produces its own buckets, not a `GROUP BY` cross-product. The result schema is `facet STRING` (column name), `value STRING` (canonical display of the value), `count DECIMAL` (matching documents). Buckets are ordered by the `FACET` list, then count descending, then value ascending. `LIMIT n` is **per-facet top-N**, not a cap on the stacked result. `NULL` values are skipped. Allowed types are `STRING`, `TEXT`, `DECIMAL`, `BOOL`, `UUID`, and `TIMESTAMPTZ`. Faceting is query-time only (no catalog or posting-format bump) and works with inverted-index and seq-scan SEARCH, including field weights (weights do not change counts). `FACET` is not a reserved keyword. It requires `SELECT *` and `SEARCH`, and fails closed with `JOIN`, `GROUP BY` / `HAVING`, `DISTINCT`, `ORDER BY`, `OFFSET`, `NEAREST`, duplicate columns, more than eight columns, JSON/vector/geo types, and more than 1024 distinct values on one facet column. Prefix, fuzzy, typo, phrase, analyzer, and `HIGHLIGHT`/`SNIPPET` matching is unchanged (`HIGHLIGHT`/`SNIPPET` still need a document SELECT list, so they do not mix with `FACET`). `EXPLAIN` shows `Facet`.

Without a full-text index, `SEARCH` still runs: it tokenizes matching heap rows (or a sargable access path from `WHERE`) and scores them the same way. `EXPLAIN` shows `Search … fulltext` when the inverted index is used, or `Search … seq` otherwise.

## Tokenizer

Letters and digits form tokens. Text is folded with Unicode lowercasing. Hyphens and other punctuation split tokens (`noise-cancelling` → `noise`, `cancelling`). Apostrophes inside a token are kept (`don't`). In a SEARCH string, a trailing ASCII `*` is a prefix operator and a trailing ASCII `~` is a fuzzy operator (query-only; document text still splits on `*` and `~`). The `simple` analyzer does not stem and has no stop-word list: `cat` does not match `cats`. The `english` analyzer drops stop-word dictionary v1 (the classic 33-term Lucene EnglishAnalyzer / Snowball-small set: `a`, `an`, `and`, `are`, `as`, `at`, `be`, `but`, `by`, `for`, `if`, `in`, `into`, `is`, `it`, `no`, `not`, `of`, `on`, `or`, `such`, `that`, `the`, `their`, `then`, `there`, `these`, `they`, `this`, `to`, `was`, `will`, `with`) then stems each remaining token. Remaining terms are re-packed to consecutive positions, so `"the running cats"` and `"running cats"` both match `running cats`. At query time english v3 also expands synonym dictionary v1 (tight bidirectional groups compiled through Porter2: `car`/`automobile`, `database`/`db`, `buy`/`purchase`, `big`/`large`, `fast`/`quick`/`rapid`, `movie`/`film`, `phone`/`telephone`, `child`/`kid`, `jump`/`leap`, `begin`/`start`, `end`/`finish`, `error`/`mistake`, `help`/`assist`, `show`/`display`, `smart`/`intelligent`). Alternatives share the token position (OR); unquoted tokens stay AND. `french`, `german`, and `spanish` drop that language's Snowball stop list then apply the matching Snowball stemmer; French also elides `l'` / `qu'` / … first. A SEARCH that is only stop words returns no rows.

Limits (fail closed on `ANALYZE` / `SEARCH`): term 128 runes, document 100 000 tokens (combined across SEARCH fields), query 64 tokens, query text 1 048 576 runes, 8 FULLTEXT/SEARCH fields, field weight `(0, 64]`, 8 FACET columns, 1024 distinct values per facet column. Query-side analyzer expansion is also fail-closed: at most 256 expanded terms, 8192 bytes of expanded terms, and 4096 expansion work units (stemming is 1:1; dropped stop words still consume one work unit; each synonym alternative consumes one work unit; at most 8 extra alternatives per token; each distinct prefix-, fuzzy-, or typo-matched term consumes one work unit). Fuzzy/typo edit-distance work additionally inspects at most 4096 distinct vocabulary terms. `HIGHLIGHT`/`SNIPPET` markers are at most 32 runes; snippet width is 16–4096 runes.

Shipped analyzers:

| Name | Catalog id/rev | Pipeline |
|---|---|---|
| `simple` | `0/0` (also `0/1`) | Unicode lowercase tokenizer only |
| `english` | `1/3` (write); `1/1` and `1/2` still decode | stop-word dictionary v1 (33 terms) then Porter2; query-time synonym dictionary v1 |
| `french` | `2/1` | elision, Snowball French stop list v1, Snowball French |
| `german` | `3/1` | Snowball German stop list v1, Snowball German |
| `spanish` | `4/1` | Snowball Spanish stop list v1, Snowball Spanish |

## Inverted index

Each full-text index is a detached B+Tree (encrypted pages, WAL, MVCC). Keys:

| Kind | Key | Value |
|---|---|---|
| stats | `0x00` | version, document count, token count |
| posting | `0x01` + term + `0x00` + primary key | version, tf, positions |
| doclen | `0x02` + primary key | version, token count |

`NULL` and empty text produce no postings. Multi-column indexes store one posting list per term: field `i` uses positions in `[i·(100000+2), …)` so a phrase cannot match across a field boundary. Combined token count is still fail-closed at 100 000. Insert, update, delete, `CREATE INDEX` on existing rows, and rollback maintain the tree the same way as other secondary indexes. Single-column indexes keep the Phase 10 posting layout (no format bump).

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
-- or: CREATE FULLTEXT INDEX ix_tb ON articles (title, body);
-- or: CREATE FULLTEXT INDEX ix_en ON articles (body) WITH (ANALYZER = 'english');
-- or: CREATE FULLTEXT INDEX ix_fr ON articles (body) WITH (ANALYZER = 'french');

SELECT title FROM articles SEARCH body FOR 'cat';
SELECT title FROM articles SEARCH title, body FOR 'database performance';
SELECT title FROM articles SEARCH title WEIGHT 3, body FOR 'database performance';
SELECT title FROM articles SEARCH body FOR 'cat*';
SELECT title FROM articles SEARCH body FOR 'cat~';
SELECT title FROM articles SEARCH body FOR '"database performance"';
SELECT title FROM articles SEARCH body FOR '"data* performance"';
SELECT title FROM articles SEARCH body FOR '"databas~ performance"';
SELECT title FROM articles SEARCH body FOR 'databse';
SELECT title FROM articles SEARCH body FOR '"databse performance"';
SELECT title, HIGHLIGHT(body) FROM articles SEARCH body FOR 'database performance';
SELECT title, SNIPPET(body) FROM articles SEARCH body FOR 'cat';
SELECT * FROM articles SEARCH body FOR 'cat' FACET category;
SELECT * FROM articles SEARCH title WEIGHT 3, body FOR 'database' FACET category, year LIMIT 5;
```
