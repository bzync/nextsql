package executor

import (
	"fmt"
	"testing"
)

func TestMinMaxIndexOptimization(t *testing.T) {
	db := newCancelDB(t)
	sess := db.Session()

	mustExec(t, sess, "CREATE TABLE items (id INT64 PRIMARY KEY, val INT64, tag STRING)")
	mustExec(t, sess, "CREATE INDEX ix_val ON items(val)")
	mustExec(t, sess, "CREATE INDEX ix_tag ON items(tag)")

	// 1. Empty table returns NULL
	rows := rowsOf(t, sess, "SELECT MIN(id), MAX(id), MIN(val), MAX(val), MIN(tag), MAX(tag) FROM items")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	for i := range rows[0] {
		if rows[0][i] != "NULL" {
			t.Fatalf("col %d expected NULL, got %q", i, rows[0][i])
		}
	}

	// 2. Populate data
	for i := 1; i <= 100; i++ {
		tag := fmt.Sprintf("t%03d", i)
		v := i * 10
		mustExec(t, sess, fmt.Sprintf("INSERT INTO items VALUES (%d, %d, '%s')", i, v, tag))
	}

	// 3. PK MIN and MAX
	rows = rowsOf(t, sess, "SELECT MIN(id), MAX(id) FROM items")
	if len(rows) != 1 || rows[0][0] != "1" || rows[0][1] != "100" {
		t.Fatalf("PK MIN/MAX: expected 1, 100; got %v", rows)
	}

	// 4. Secondary index (INT64) MIN and MAX
	rows = rowsOf(t, sess, "SELECT MIN(val), MAX(val) FROM items")
	if len(rows) != 1 || rows[0][0] != "10" || rows[0][1] != "1000" {
		t.Fatalf("val MIN/MAX: expected 10, 1000; got %v", rows)
	}

	// 5. Secondary index (STRING) MIN and MAX
	rows = rowsOf(t, sess, "SELECT MIN(tag), MAX(tag) FROM items")
	if len(rows) != 1 || rows[0][0] != "t001" || rows[0][1] != "t100" {
		t.Fatalf("tag MIN/MAX: expected t001, t100; got %v", rows)
	}

	// 6. Aliases and expressions over MIN/MAX
	rows = rowsOf(t, sess, "SELECT MIN(id) AS lowest, MAX(val) + 5 AS highest FROM items")
	if len(rows) != 1 || rows[0][0] != "1" || rows[0][1] != "1005" {
		t.Fatalf("alias/expr: expected 1, 1005; got %v", rows)
	}

	// 7. Table with NULL values in indexed column
	mustExec(t, sess, "CREATE TABLE nullable_tab (id INT64 PRIMARY KEY, num INT64)")
	mustExec(t, sess, "CREATE INDEX ix_num ON nullable_tab(num)")
	mustExec(t, sess, "INSERT INTO nullable_tab VALUES (1, NULL), (2, 42), (3, 7), (4, NULL), (5, 99)")

	rows = rowsOf(t, sess, "SELECT MIN(num), MAX(num) FROM nullable_tab")
	if len(rows) != 1 || rows[0][0] != "7" || rows[0][1] != "99" {
		t.Fatalf("nullable MIN/MAX: expected 7, 99; got %v", rows)
	}

	// 8. Table with ONLY NULLs in indexed column
	mustExec(t, sess, "CREATE TABLE all_nulls (id INT64 PRIMARY KEY, num INT64)")
	mustExec(t, sess, "CREATE INDEX ix_all_nulls_num ON all_nulls(num)")
	mustExec(t, sess, "INSERT INTO all_nulls VALUES (1, NULL), (2, NULL)")

	rows = rowsOf(t, sess, "SELECT MIN(num), MAX(num) FROM all_nulls")
	if len(rows) != 1 || rows[0][0] != "NULL" || rows[0][1] != "NULL" {
		t.Fatalf("all-null MIN/MAX: expected NULL, NULL; got %v", rows)
	}

	// 9. Bounded range MIN/MAX on secondary index
	rows = rowsOf(t, sess, "SELECT MIN(val), MAX(val) FROM items WHERE val > 50")
	if len(rows) != 1 || rows[0][0] != "60" || rows[0][1] != "1000" {
		t.Fatalf("bounded MIN/MAX val > 50: expected 60, 1000; got %v", rows)
	}

	rows = rowsOf(t, sess, "SELECT MIN(val), MAX(val) FROM items WHERE val >= 200 AND val <= 800")
	if len(rows) != 1 || rows[0][0] != "200" || rows[0][1] != "800" {
		t.Fatalf("bounded MIN/MAX val [200, 800]: expected 200, 800; got %v", rows)
	}

	rows = rowsOf(t, sess, "SELECT MIN(val), MAX(val) FROM items WHERE val < 500")
	if len(rows) != 1 || rows[0][0] != "10" || rows[0][1] != "490" {
		t.Fatalf("bounded MIN/MAX val < 500: expected 10, 490; got %v", rows)
	}

	// 10. Bounded range MIN/MAX on primary key
	rows = rowsOf(t, sess, "SELECT MIN(id), MAX(id) FROM items WHERE id > 10 AND id < 20")
	if len(rows) != 1 || rows[0][0] != "11" || rows[0][1] != "19" {
		t.Fatalf("bounded PK MIN/MAX: expected 11, 19; got %v", rows)
	}

	// 11. Bounded range with no matches returns NULL
	rows = rowsOf(t, sess, "SELECT MIN(val), MAX(val) FROM items WHERE val > 9999")
	if len(rows) != 1 || rows[0][0] != "NULL" || rows[0][1] != "NULL" {
		t.Fatalf("bounded empty MIN/MAX: expected NULL, NULL; got %v", rows)
	}

	// 12. Composite secondary index prefix MIN/MAX
	mustExec(t, sess, "CREATE TABLE events (id INT64 PRIMARY KEY, tenant STRING, ts INT64, payload STRING)")
	mustExec(t, sess, "CREATE INDEX ix_tenant_ts ON events(tenant, ts)")

	for i := 1; i <= 50; i++ {
		mustExec(t, sess, fmt.Sprintf("INSERT INTO events VALUES (%d, 'acme', %d, 'p_%d')", i, i*10, i))
		mustExec(t, sess, fmt.Sprintf("INSERT INTO events VALUES (%d, 'beta', %d, 'b_%d')", 50+i, i*100, i))
	}

	rows = rowsOf(t, sess, "SELECT MIN(ts), MAX(ts) FROM events WHERE tenant = 'acme'")
	if len(rows) != 1 || rows[0][0] != "10" || rows[0][1] != "500" {
		t.Fatalf("composite secondary index MIN/MAX 'acme': expected 10, 500; got %v", rows)
	}

	rows = rowsOf(t, sess, "SELECT MIN(ts), MAX(ts) FROM events WHERE tenant = 'beta'")
	if len(rows) != 1 || rows[0][0] != "100" || rows[0][1] != "5000" {
		t.Fatalf("composite secondary index MIN/MAX 'beta': expected 100, 5000; got %v", rows)
	}

	rows = rowsOf(t, sess, "SELECT MIN(ts), MAX(ts) FROM events WHERE tenant = 'nonexistent'")
	if len(rows) != 1 || rows[0][0] != "NULL" || rows[0][1] != "NULL" {
		t.Fatalf("composite secondary index MIN/MAX 'nonexistent': expected NULL, NULL; got %v", rows)
	}

	// 13. Composite primary key prefix MIN/MAX
	mustExec(t, sess, "CREATE TABLE comp_pk (tenant STRING, seq INT64, data STRING, PRIMARY KEY(tenant, seq))")
	for i := 1; i <= 30; i++ {
		mustExec(t, sess, fmt.Sprintf("INSERT INTO comp_pk VALUES ('t1', %d, 'd1_%d')", i*5, i))
		mustExec(t, sess, fmt.Sprintf("INSERT INTO comp_pk VALUES ('t2', %d, 'd2_%d')", i*20, i))
	}

	rows = rowsOf(t, sess, "SELECT MIN(seq), MAX(seq) FROM comp_pk WHERE tenant = 't1'")
	if len(rows) != 1 || rows[0][0] != "5" || rows[0][1] != "150" {
		t.Fatalf("composite PK MIN/MAX 't1': expected 5, 150; got %v", rows)
	}

	rows = rowsOf(t, sess, "SELECT MIN(seq), MAX(seq) FROM comp_pk WHERE tenant = 't2'")
	if len(rows) != 1 || rows[0][0] != "20" || rows[0][1] != "600" {
		t.Fatalf("composite PK MIN/MAX 't2': expected 20, 600; got %v", rows)
	}

	rows = rowsOf(t, sess, "SELECT MIN(seq), MAX(seq) FROM comp_pk WHERE tenant = 't_none'")
	if len(rows) != 1 || rows[0][0] != "NULL" || rows[0][1] != "NULL" {
		t.Fatalf("composite PK MIN/MAX 't_none': expected NULL, NULL; got %v", rows)
	}

	// 14. 3-column composite index with 2 equality prefix columns
	mustExec(t, sess, "CREATE TABLE triple (id INT64 PRIMARY KEY, a INT64, b STRING, c INT64)")
	mustExec(t, sess, "CREATE INDEX ix_abc ON triple(a, b, c)")

	mustExec(t, sess, "INSERT INTO triple VALUES (1, 1, 'x', 10), (2, 1, 'x', 50), (3, 1, 'x', 25)")
	mustExec(t, sess, "INSERT INTO triple VALUES (4, 1, 'y', 100), (5, 1, 'y', 300), (6, 1, 'y', 200)")
	mustExec(t, sess, "INSERT INTO triple VALUES (7, 2, 'x', 7), (8, 2, 'x', 77), (9, 2, 'x', 17)")

	rows = rowsOf(t, sess, "SELECT MIN(c), MAX(c) FROM triple WHERE a = 1 AND b = 'x'")
	if len(rows) != 1 || rows[0][0] != "10" || rows[0][1] != "50" {
		t.Fatalf("triple index MIN/MAX (1, 'x'): expected 10, 50; got %v", rows)
	}

	rows = rowsOf(t, sess, "SELECT MIN(c), MAX(c) FROM triple WHERE a = 1 AND b = 'y'")
	if len(rows) != 1 || rows[0][0] != "100" || rows[0][1] != "300" {
		t.Fatalf("triple index MIN/MAX (1, 'y'): expected 100, 300; got %v", rows)
	}

	rows = rowsOf(t, sess, "SELECT MIN(c), MAX(c) FROM triple WHERE a = 2 AND b = 'x'")
	if len(rows) != 1 || rows[0][0] != "7" || rows[0][1] != "77" {
		t.Fatalf("triple index MIN/MAX (2, 'x'): expected 7, 77; got %v", rows)
	}
}
