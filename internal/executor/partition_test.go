package executor

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPartitionRowsVisibleAndRecover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE events (id STRING, k STRING NOT NULL, v STRING, PRIMARY KEY (k, id)) PARTITION BY RANGE (k) (PARTITION p0 VALUES LESS THAN ('m'), PARTITION p1 VALUES LESS THAN MAXVALUE)`)
	execOK(t, s, `INSERT INTO events (id, k, v) VALUES ('1', 'a', 'early'), ('2', 'z', 'late')`)
	if got := execOK(t, s, `SELECT * FROM events`).Rows; len(got) != 2 {
		t.Fatalf("same-process rows=%d: %+v", len(got), got)
	}
	plan := execOK(t, s, `EXPLAIN SELECT * FROM events WHERE k = 'a'`)
	if len(plan.Rows) == 0 || !strings.Contains(plan.Rows[len(plan.Rows)-1][0].Str, "partitions=[p0]") {
		t.Fatalf("range pruning missing from EXPLAIN: %+v", plan.Rows)
	}
	execOK(t, s, `UPDATE events SET k = 'z' WHERE id = '1'`)
	if got := execOK(t, s, `SELECT * FROM events WHERE k = 'a'`).Rows; len(got) != 0 {
		t.Fatalf("moved row remained in old partition: %+v", got)
	}
	if got := execOK(t, s, `SELECT * FROM events WHERE k = 'z'`).Rows; len(got) != 2 {
		t.Fatalf("moved row missing from new partition: %+v", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := execOK(t, db.Session(), `SELECT * FROM events`).Rows; len(got) != 2 {
		t.Fatalf("reopened rows=%d: %+v", len(got), got)
	}
}

func TestTenantPartitionRoutingAndIsolation(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE tenant_events (id STRING, tenant_id UUID NOT NULL, v STRING, PRIMARY KEY (tenant_id, id)) PARTITION BY TENANT (tenant_id) (PARTITION p_a VALUES IN ('`+tenantA+`'), PARTITION p_b VALUES IN ('`+tenantB+`'))`)
	execOK(t, s, `INSERT INTO tenant_events (id, tenant_id, v) VALUES ('a', '`+tenantA+`', 'alpha'), ('b', '`+tenantB+`', 'beta')`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	if got := execOK(t, s, `SELECT id FROM tenant_events`).Rows; len(got) != 1 || got[0][0].Str != "a" {
		t.Fatalf("tenant A rows=%+v", got)
	}
	if got := execOK(t, s, `UPDATE tenant_events SET v = 'ALPHA'`).Affected; got != 1 {
		t.Fatalf("tenant A update affected=%d", got)
	}
	if _, err := s.Exec(`UPDATE tenant_events SET tenant_id = '` + tenantB + `' WHERE id = 'a'`); err == nil {
		t.Fatal("cross-tenant reassignment unexpectedly succeeded")
	}
}

func TestHashPartitionRoutingPruningAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE hashed_events (
		k STRING NOT NULL,
		id STRING NOT NULL,
		v STRING,
		PRIMARY KEY (k, id)
	) PARTITION BY HASH (k) (
		PARTITION h0 MODULUS 4 REMAINDER 0,
		PARTITION h1 MODULUS 4 REMAINDER 1,
		PARTITION h2 MODULUS 4 REMAINDER 2,
		PARTITION h3 MODULUS 4 REMAINDER 3
	)`)
	execOK(t, s, `INSERT INTO hashed_events (k, id, v) VALUES
		('b', '0', 'zero'),
		('f', '1', 'one'),
		('n', '2', 'two'),
		('c', '3', 'three')`)
	if got := execOK(t, s, `SELECT id FROM hashed_events ORDER BY id`).Rows; len(got) != 4 {
		t.Fatalf("rows=%d: %+v", len(got), got)
	}
	plan := execOK(t, s, `EXPLAIN SELECT * FROM hashed_events WHERE k = 'n'`)
	if len(plan.Rows) == 0 || !strings.Contains(plan.Rows[len(plan.Rows)-1][0].Str, "partitions=[h2]") {
		t.Fatalf("hash pruning missing from EXPLAIN: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT COUNT(*) FROM hashed_events WHERE k = 'n'`).Rows; len(got) != 1 || got[0][0].Dec.String() != "1" {
		t.Fatalf("pruned hash count=%+v", got)
	}
	plan = execOK(t, s, `EXPLAIN SELECT * FROM hashed_events WHERE k = 'n' OR v = 'zero'`)
	if len(plan.Rows) == 0 || !strings.Contains(plan.Rows[len(plan.Rows)-1][0].Str, "partitions=all[4]") {
		t.Fatalf("unsafe OR was not conservatively retained: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM hashed_events WHERE k = 'n' OR v = 'zero' ORDER BY id`).Rows; len(got) != 2 {
		t.Fatalf("unsafe OR lost rows: %+v", got)
	}
	execOK(t, s, `UPDATE hashed_events SET k = 'f' WHERE k = 'b' AND id = '0'`)
	if got := execOK(t, s, `SELECT v FROM hashed_events WHERE k = 'b'`).Rows; len(got) != 0 {
		t.Fatalf("moved row remained in old hash partition: %+v", got)
	}
	if got := execOK(t, s, `SELECT v FROM hashed_events WHERE k = 'f' ORDER BY v`).Rows; len(got) != 2 {
		t.Fatalf("moved row missing from new hash partition: %+v", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := execOK(t, db.Session(), `SELECT id FROM hashed_events ORDER BY id`).Rows; len(got) != 4 {
		t.Fatalf("reopened rows=%d: %+v", len(got), got)
	}
}

func TestListPartitionRoutingPruningAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE regional_events (
		region STRING NOT NULL,
		id STRING NOT NULL,
		v STRING,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us', 'ca'),
		PARTITION elsewhere VALUES IN ('eu', 'ap')
	)`)
	execOK(t, s, `INSERT INTO regional_events (region, id, v) VALUES
		('us', '1', 'west'),
		('ca', '2', 'north'),
		('eu', '3', 'east'),
		('ap', '4', 'south')`)
	plan := execOK(t, s, `EXPLAIN SELECT * FROM regional_events WHERE region = 'eu'`)
	if len(plan.Rows) == 0 || !strings.Contains(plan.Rows[len(plan.Rows)-1][0].Str, "partitions=[elsewhere]") {
		t.Fatalf("list pruning missing from EXPLAIN: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT COUNT(*) FROM regional_events WHERE region = 'eu'`).Rows; len(got) != 1 || got[0][0].Dec.String() != "1" {
		t.Fatalf("pruned list count=%+v", got)
	}
	execOK(t, s, `UPDATE regional_events SET region = 'eu' WHERE region = 'us' AND id = '1'`)
	if got := execOK(t, s, `SELECT id FROM regional_events WHERE region = 'us'`).Rows; len(got) != 0 {
		t.Fatalf("moved row remained in old list partition: %+v", got)
	}
	if got := execOK(t, s, `SELECT id FROM regional_events WHERE region = 'eu' ORDER BY id`).Rows; len(got) != 2 {
		t.Fatalf("moved row missing from new list partition: %+v", got)
	}
	if _, err := s.Exec(`INSERT INTO regional_events (region, id, v) VALUES ('unknown', '5', 'rejected')`); err == nil {
		t.Fatal("LIST value outside every partition unexpectedly succeeded")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := execOK(t, db.Session(), `SELECT id FROM regional_events ORDER BY id`).Rows; len(got) != 4 {
		t.Fatalf("reopened rows=%d: %+v", len(got), got)
	}
}

func TestPartitionPrimaryKeyMustIncludePartitionColumn(t *testing.T) {
	s := testDB(t).Session()
	if _, err := s.Exec(`CREATE TABLE bad_partition_key (
		id STRING PRIMARY KEY,
		k STRING NOT NULL
	) PARTITION BY HASH (k) (
		PARTITION h0 MODULUS 2 REMAINDER 0,
		PARTITION h1 MODULUS 2 REMAINDER 1
	)`); err == nil || !strings.Contains(err.Error(), "primary key must include every partition column") {
		t.Fatalf("missing partition-key uniqueness rejection: %v", err)
	}
}
