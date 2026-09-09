-- Extra committed writes made just before the generator SIGKILLs the server,
-- so the dirty fixture's WAL carries redo work that no checkpoint covered.
-- Every statement here is acknowledged before the kill, so a later release
-- must recover all of it.
CREATE TABLE replay (id INT64 PRIMARY KEY, note STRING)
INSERT INTO replay (id, note) VALUES (1, 'committed before the kill')
INSERT INTO replay (id, note) VALUES (2, 'also committed before the kill')
INSERT INTO customers (id, name, region, joined) VALUES (4, 'edsger', 'emea', '2026-04-05T06:07:08Z')
UPDATE orders SET total = 20.5 WHERE id = 10
DELETE FROM events WHERE region = 'eu'
