package parser

import "testing"

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
	})
}
