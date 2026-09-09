-- Read-only assertions recorded against the RELEASED binary when a fixture is
-- cut, and replayed by tests/upgrade against the current binary. Every result
-- must be deterministic (explicit ORDER BY, no clock, no identifiers).
SELECT id, name, region, joined FROM customers ORDER BY id
SELECT id, customer_id, total, note FROM orders ORDER BY id
SELECT id, metadata.category, metadata.qty FROM orders ORDER BY id
SELECT COUNT(*) FROM orders WHERE customer_id = 1
SELECT c.name, o.total FROM customers c JOIN orders o ON o.customer_id = c.id ORDER BY o.id
SELECT region, COUNT(*) FROM customers GROUP BY region ORDER BY region
SELECT id FROM orders SEARCH note FOR 'databases' ORDER BY id
SELECT id FROM orders SEARCH note FOR 'shipping' ORDER BY id
SELECT id FROM docs NEAREST embedding TO (1.0, 0.0, 0.0, 0.0) LIMIT 3
SELECT id FROM docs NEAREST embedding TO (0.0, 0.0, 1.0, 0.0) LIMIT 1
SELECT id, label FROM places ORDER BY id
SELECT region, id, kind FROM events ORDER BY region, id
SELECT id, kind FROM events WHERE region = 'us' ORDER BY id
