-- Corpus written into every retained upgrade fixture. One statement per line;
-- the generator (scripts/make-upgrade-fixture.sh) feeds them to the RELEASED
-- binary of the release being retained, so this file must only use syntax that
-- every retained release accepts. Adding a statement here does not change an
-- already-retained archive: it takes effect the next time a fixture is cut.
--
-- It deliberately spans every persistent structure a format change could
-- strand: clustered heap, secondary/unique/JSON-path indexes, foreign keys,
-- JSON documents, a full-text index, a vector index, geospatial values, a
-- partitioned table, ANALYZE statistics, and the auth/ACL sidecars.
CREATE TABLE customers (id INT64 PRIMARY KEY, name STRING NOT NULL, region STRING, joined TIMESTAMPTZ)
CREATE TABLE orders (id INT64 PRIMARY KEY, customer_id INT64 REFERENCES customers(id) ON DELETE CASCADE, total FLOAT64, note TEXT, metadata JSON)
CREATE INDEX idx_orders_customer ON orders (customer_id)
CREATE UNIQUE INDEX idx_customers_name ON customers (name)
CREATE INDEX idx_orders_category ON orders (metadata.category)
INSERT INTO customers (id, name, region, joined) VALUES (1, 'ada', 'emea', '2026-01-02T03:04:05Z')
INSERT INTO customers (id, name, region, joined) VALUES (2, 'grace', 'amer', '2026-02-03T04:05:06Z')
INSERT INTO customers (id, name, region, joined) VALUES (3, 'linus', 'apac', '2026-03-04T05:06:07Z')
INSERT INTO orders (id, customer_id, total, note, metadata) VALUES (10, 1, 19.5, 'first order, ships monday', '{"category":"books","tags":["new","paper"],"qty":2}')
INSERT INTO orders (id, customer_id, total, note, metadata) VALUES (11, 1, 42.25, 'second order of hardware parts', '{"category":"hardware","tags":["bolt"],"qty":7}')
INSERT INTO orders (id, customer_id, total, note, metadata) VALUES (12, 2, 7.75, 'a quiet order about databases', '{"category":"books","tags":["db"],"qty":1}')
CREATE FULLTEXT INDEX idx_orders_note ON orders (note) WITH (ANALYZER = 'english')
CREATE TABLE docs (id INT64 PRIMARY KEY, body TEXT, embedding VECTOR<F32,4>)
INSERT INTO docs (id, body, embedding) VALUES (1, 'vector search over documents', (1.0, 0.0, 0.0, 0.0))
INSERT INTO docs (id, body, embedding) VALUES (2, 'relational rows and columns', (0.0, 1.0, 0.0, 0.0))
INSERT INTO docs (id, body, embedding) VALUES (3, 'geospatial points and polygons', (0.0, 0.0, 1.0, 0.0))
CREATE VECTOR INDEX idx_docs_embedding ON docs (embedding) USING HNSW
CREATE TABLE places (id INT64 PRIMARY KEY, label STRING, loc POINT, area POLYGON)
INSERT INTO places (id, label, loc, area) VALUES (1, 'origin', POINT(0, 0), 'POLYGON((0 0, 1 0, 1 1, 0 1, 0 0))')
INSERT INTO places (id, label, loc, area) VALUES (2, 'east', POINT(10, 0.5), 'POLYGON((10 0, 11 0, 11 1, 10 1, 10 0))')
CREATE SPATIAL INDEX idx_places_loc ON places (loc)
CREATE TABLE events (region STRING NOT NULL, id INT64, kind STRING, PRIMARY KEY (region, id)) PARTITION BY LIST (region) (PARTITION americas VALUES IN ('us', 'ca'), PARTITION elsewhere VALUES IN ('eu', 'ap'))
INSERT INTO events (region, id, kind) VALUES ('us', 1, 'created')
INSERT INTO events (region, id, kind) VALUES ('ca', 2, 'updated')
INSERT INTO events (region, id, kind) VALUES ('eu', 3, 'deleted')
CREATE ROLE fixture_reader
GRANT SELECT ON customers TO fixture_reader
GRANT CONNECT ON DATABASE TO fixture_reader
CREATE USER fixture_reporter IDENTIFIED BY 'fixture-only-password-1'
GRANT fixture_reader TO fixture_reporter
ANALYZE customers
ANALYZE orders
