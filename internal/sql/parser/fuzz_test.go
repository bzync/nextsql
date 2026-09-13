package parser

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
)

func FuzzParse(f *testing.F) {
	seeds := []string{
		"SELECT * FROM t",
		"SELECT id, name FROM products WHERE price BETWEEN 1 AND 2 ORDER BY price DESC LIMIT 10",
		"SELECT n FROM t ORDER BY n LIMIT 5 OFFSET 10",
		"SELECT 1",
		"SELECT 1 AS x, 2+2, NOW(), UUID()",
		"SELECT 1 WHERE 1 = 0 ORDER BY 1 LIMIT 1 OFFSET 1",
		"SELECT 1 GROUP BY 1",
		"DROP TABLE IF EXISTS t",
		"ALTER TABLE t ADD note STRING",
		"ALTER TABLE t DROP COLUMN note",
		"ALTER TABLE t RENAME TO u",
		"CREATE DATABASE IF NOT EXISTS app",
		"CREATE WORKFLOW set_note(id UUID, note TEXT) AS BEGIN UPDATE t SET note = $note WHERE id = $id; END",
		"RUN WORKFLOW set_note($1, 'ready')",
		"ALTER WORKFLOW set_note RENAME TO update_note",
		"DROP WORKFLOW IF EXISTS update_note",
		"CREATE TRIGGER audit_insert AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW audit_order(NEW.id)",
		"ALTER TRIGGER audit_insert RENAME TO audit_created",
		"DROP TRIGGER IF EXISTS audit_created",
		"SHOW DATABASES",
		"SHOW TABLES",
		"SHOW INDEXES",
		"SHOW CONNECTIONS",
		"SHOW QUERIES",
		"SHOW TRANSACTIONS",
		"SHOW LOCKS",
		"SHOW CLUSTER",
		"SHOW STORAGE",
		"SELECT id, name FROM products WHERE price BETWEEN 1 AND 2 LIMIT 10",
		"CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)",
		"CREATE TABLE t (id INT64 PRIMARY KEY, active BOOL NOT NULL DEFAULT FALSE)",
		"CREATE TABLE t (active BOOL PRIMARY KEY, \"bool\" STRING)",
		"SELECT CAST(active AS BOOL) FROM t WHERE active",
		"CREATE TABLE t (id INT64 PRIMARY KEY, area GEOGRAPHY(Geometry, 4326), plan GEOMETRY(geometry))",
		"CREATE TABLE c PRIMARY KEY (id) AS SELECT id, name FROM t",
		"CREATE TABLE c PRIMARY KEY (a, b) AS SELECT a, b, c FROM t WHERE c > 1",
		"CREATE TABLE c PRIMARY KEY (id) AS WITH w AS (SELECT id FROM t) SELECT id FROM w",
		"CREATE TABLE c PRIMARY KEY (id) AS SELECT id FROM t UNION SELECT id FROM u",
		"CREATE TABLE c PRIMARY KEY (n) AS SELECT name AS n, COUNT(*) AS k FROM t GROUP BY name",
		"CREATE TABLE t (id DECIMAL(18,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)",
		"CREATE INDEX i ON t (n)",
		"INSERT INTO t (id, n) VALUES (UUID(), 'x')",
		"INSERT INTO t (id, n) VALUES (UUID(), 'x') RETURNING id",
		"UPSERT INTO t (id, n) VALUES ('k', 'x') ON UNIQUE (id) SET n = excluded.n",
		"UPSERT INTO t (email, n) VALUES ('a@b', 'x') RETURNING *",
		"UPDATE t SET n = 'y' WHERE id IS NOT NULL",
		"UPDATE t SET n = 'y' RETURNING n",
		"DELETE FROM t WHERE n = 'x' OR n IS NULL",
		"DELETE FROM t RETURNING *",
		"BEGIN SERIALIZABLE",
		"COMMIT",
		"ROLLBACK",
		"EXPLAIN SELECT * FROM t",
		"EXPLAIN ANALYZE SELECT id FROM t WHERE n = 1 LIMIT 1",
		"ANALYZE t",
		"SET TENANT = '11111111-1111-1111-1111-111111111111'",
		"RESET TENANT",
		"SET CONFIG buffer_pages = 4096",
		"SET CONFIG log_level = 'debug'",
		"SET CONFIG require_client_key = TRUE",
		"SET CONFIG raft_bind = DEFAULT",
		"BACKUP DATABASE",
		"VERIFY BACKUP 'backup-20260101T000000Z'",
		"CREATE TABLE p (id UUID PRIMARY KEY, loc POINT, area BOX, route LINESTRING, zone POLYGON)",
		"SELECT * FROM p WHERE WITHIN(loc, POLYGON('POLYGON((-74 40, -73 40, -73 41, -74 41, -74 40))'))",
		"SELECT DISTANCE_SPHEROID(a, b), ST_Length(route) FROM p",
		"CREATE SPATIAL INDEX ix ON p (loc)",
		"CREATE FULLTEXT INDEX ix ON t (body)",
		"CREATE FULLTEXT INDEX ix ON t (title, body)",
		"CREATE VECTOR INDEX ix ON t (emb) USING HNSW",
		"CREATE VECTOR INDEX ix ON t (emb) USING HNSW WITH (QUANTIZATION = 'I8')",
		"CREATE VECTOR INDEX ix ON t (emb) USING IVF WITH (LISTS = 128, PROBES = 8)",
		"CREATE VECTOR INDEX ix ON t (emb) USING IVF WITH (LISTS = 64)",
		"CREATE VECTOR INDEX ix ON t (emb) USING IVFPQ WITH (LISTS = 256, PROBES = 16, SUBSPACES = 8)",
		"CREATE TABLE s (id UUID PRIMARY KEY, emb SPARSEVECTOR<30522>)",
		"CREATE VECTOR INDEX ix ON t (emb) USING SPARSE",
		"SELECT * FROM t SEARCH body FOR 'database performance' LIMIT 20",
		"SELECT * FROM t SEARCH title, body FOR 'database performance' LIMIT 20",
		"SELECT * FROM t SEARCH title WEIGHT 3, body FOR 'database performance' LIMIT 20",
		"SELECT * FROM t SEARCH body FOR 'database performance' FACET category LIMIT 5",
		"SELECT * FROM t SEARCH title, body FOR 'q' FACET category, year",
		"SELECT id FROM t NEAREST emb TO $query USING COSINE LIMIT 10",
		"SELECT id FROM t SEARCH body FOR 'q' NEAREST emb TO $d NEAREST sparse TO $s LIMIT 10",
		"WITH c AS (SELECT id FROM t) SELECT id FROM c",
		"WITH RECURSIVE w AS (SELECT id FROM t UNION ALL SELECT id FROM w) SELECT id FROM w",
		"WITH a AS MATERIALIZED (SELECT id FROM t), b AS NOT MATERIALIZED (SELECT id FROM a) SELECT id FROM b",
		"SELECT k, ROW_NUMBER() OVER (PARTITION BY k ORDER BY v) FROM t",
		"CREATE TABLE hp (k STRING NOT NULL, id STRING NOT NULL, PRIMARY KEY (k, id)) PARTITION BY HASH (k) (PARTITION h0 MODULUS 2 REMAINDER 0, PARTITION h1 MODULUS 2 REMAINDER 1)",
		"ALTER TABLE hp ADD PARTITION h2 VALUES IN ('x')",
		"ALTER TABLE hp DROP PARTITION h1",
		"CREATE TABLE lp (region STRING NOT NULL, id STRING NOT NULL, PRIMARY KEY (region, id)) PARTITION BY LIST (region) (PARTITION west VALUES IN ('us', 'ca'), PARTITION east VALUES IN ('eu', 'ap'))",
		"CREATE TABLE mcr (a STRING NOT NULL, b STRING NOT NULL, id STRING NOT NULL, PRIMARY KEY (a, b, id)) PARTITION BY RANGE (a, b) (PARTITION p0 VALUES LESS THAN ('m', 'z'), PARTITION p1 VALUES LESS THAN MAXVALUE)",
		"CREATE TABLE mcl (a STRING NOT NULL, b STRING NOT NULL, id STRING NOT NULL, PRIMARY KEY (a, b, id)) PARTITION BY LIST (a, b) (PARTITION p0 VALUES IN (('us', 'gold'), ('eu', 'gold')), PARTITION p1 VALUES IN (('us', 'bronze')))",
		"SELECT SUM(v) OVER (ORDER BY k ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM t",
		"SELECT RANK() OVER (), DENSE_RANK() OVER (ORDER BY v), LAG(v) OVER (ORDER BY v) FROM t",
		"SELECT * FROM p WHERE DWITHIN(loc, POINT(-73.98, 40.75), 1000)",
		"CREATE INDEX ix ON t (metadata.category)",
		"SELECT metadata.category FROM t WHERE metadata.category = 'electronics'",
		"SELECT metadata.tags.0 FROM t WHERE metadata.tags.0 = 'x'",
		"CREATE INDEX ix ON t (metadata.tags.0)",
		"SELECT metadata.0.0.0 FROM t",
		"SELECT metadata.tags.99999999999999999999 FROM t",
		"SELECT 1 + .5",
		"INSERT INTO t SELECT a, b FROM u",
		"INSERT INTO t (a, b) SELECT a, b FROM u WHERE a > 1 RETURNING a",
		"INSERT INTO t WITH c AS (SELECT a FROM u) SELECT a FROM c",
		"INSERT INTO t SELECT a FROM u UNION SELECT a FROM v",
		"INSERT INTO t SELECT 1, 'x'",
		"INSERT INTO t SELECT",
		"INSERT INTO t VALUES",
		"'",
		"/*",
		"SELECT",
		string([]byte{0, 1, 2, 255}),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		stmt, err := Parse(src)
		if err != nil {
			if stmt != nil {
				t.Fatalf("error with non-nil stmt")
			}
			return
		}
		if stmt == nil {
			t.Fatalf("nil stmt without error")
		}

		// ParseDiag must agree with Parse and, on failure, always hand back
		// an in-range offset an editor can point at.
		dstmt, diag, derr := ParseDiag(src)
		if (derr != nil) != (err != nil) {
			t.Fatalf("ParseDiag/Parse disagree on error for %q", src)
		}
		if derr != nil {
			if diag == nil {
				t.Fatalf("ParseDiag returned an error but no diag for %q", src)
			}
			if diag.Offset < 0 || diag.Offset > len(src) {
				t.Fatalf("ParseDiag offset %d out of [0,%d] for %q", diag.Offset, len(src), src)
			}
			if diag.Message == "" {
				t.Fatalf("ParseDiag empty message for %q", src)
			}
			return
		}
		if diag != nil || dstmt == nil {
			t.Fatalf("ParseDiag: clean parse but diag=%v stmt=%v for %q", diag, dstmt, src)
		}

		// Nothing the parser accepts may nest deeper than every later stage
		// can walk: an unbounded tree is an unbounded goroutine stack, which
		// takes the whole process down rather than failing one statement.
		if ast.ExceedsDepth(stmt, ast.MaxNestingDepth) {
			t.Fatalf("accepted a statement nested past ast.MaxNestingDepth for %q", src)
		}

		// An INSERT has exactly one source. Rows and Query are mutually
		// exclusive: a statement carrying both would have a source the later
		// stages disagree about, and one carrying neither would write an
		// unspecified row.
		if ins, ok := stmt.(ast.Insert); ok {
			if (len(ins.Rows) == 0) == (ins.Query == nil) {
				t.Fatalf("INSERT with %d value rows and query=%v for %q", len(ins.Rows), ins.Query != nil, src)
			}
		}

		// A CREATE TABLE never carries both shapes: a written column list, or
		// a query whose output supplies the columns. Both at once would leave
		// the binder choosing between two schemas. A query source also always
		// names its key, since a query's output carries none and there is
		// nothing else to cluster the table on.
		//
		// Note the converse is deliberately not asserted: `CREATE TABLE t
		// (PRIMARY KEY (c))` parses to neither shape, which the binder rejects
		// as a table with no columns. That laxity predates the query form.
		if ct, ok := stmt.(ast.CreateTable); ok {
			if ct.Query != nil && len(ct.Columns) > 0 {
				t.Fatalf("CREATE TABLE with both %d columns and a query source for %q", len(ct.Columns), src)
			}
			if ct.Query != nil && len(ct.PK) == 0 {
				t.Fatalf("CREATE TABLE ... AS with no PRIMARY KEY for %q", src)
			}
		}
	})
}
