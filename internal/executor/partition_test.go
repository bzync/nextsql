package executor

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/wal"
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

func TestPartitionAnalyzeStatsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE partition_stats (
		region STRING NOT NULL,
		id STRING NOT NULL,
		emb VECTOR<F32,4>,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `CREATE INDEX ix_partition_stats_id ON partition_stats (id)`)
	execOK(t, s, `INSERT INTO partition_stats (region, id) VALUES
		('us', '1'),
		('eu', '2'), ('eu', '3'), ('eu', '4')`)
	execOK(t, s, `ANALYZE partition_stats`)

	tab, ok := db.Cat.Get("partition_stats")
	if !ok || tab.Partitioning == nil {
		t.Fatal("partitioned table missing")
	}
	st, ok := db.Cat.Stats("partition_stats")
	if !ok || st.Rows != 4 || len(st.Partitions) != 2 {
		t.Fatalf("partition stats missing: ok=%v stats=%+v", ok, st)
	}
	if st.Partitions[0].ID != tab.Partitioning.Partitions[0].ID || st.Partitions[0].Rows != 1 ||
		st.Partitions[1].ID != tab.Partitioning.Partitions[1].ID || st.Partitions[1].Rows != 3 {
		t.Fatalf("partition stats do not match stable identities: %+v", st.Partitions)
	}
	for _, part := range st.Partitions {
		if len(part.Columns) != 3 || len(part.Indexes) != 1 || part.Indexes[0].Name != "ix_partition_stats_id" ||
			len(part.Vectors) != 1 || part.Vectors[0].Ord != 2 || part.Vectors[0].Dim != 4 || part.Vectors[0].Count != 0 {
			t.Fatalf("partition sketches missing before restart: %+v", part)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, ok = db.Cat.Stats("partition_stats")
	if !ok || st.Rows != 4 || len(st.Partitions) != 2 || st.Partitions[0].Rows != 1 || st.Partitions[1].Rows != 3 ||
		len(st.Partitions[0].Columns) != 3 || len(st.Partitions[1].Indexes) != 1 || len(st.Partitions[1].Vectors) != 1 {
		t.Fatalf("partition stats after restart: ok=%v stats=%+v", ok, st)
	}
}

func TestPartitionStatsDropDoesNotLeaveOrphans(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 48)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE disposable_stats (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION us VALUES IN ('us'),
		PARTITION eu VALUES IN ('eu')
	)`)
	execOK(t, s, `ANALYZE disposable_stats`)
	execOK(t, s, `DROP TABLE disposable_stats`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 48)
	if err != nil {
		t.Fatalf("partition statistics side records survived DROP TABLE: %v", err)
	}
	defer db.Close()
	if _, ok := db.Cat.Stats("disposable_stats"); ok {
		t.Fatal("dropped table statistics survived restart")
	}
}

func TestPartitionStatsStaleSnapshotFallsBackToGlobal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 48)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE stale_stats (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION us VALUES IN ('us'),
		PARTITION eu VALUES IN ('eu')
	)`)
	execOK(t, s, `INSERT INTO stale_stats (region, id) VALUES ('us', '1')`)
	execOK(t, s, `ANALYZE stale_stats`)

	raw, err := db.CatTree.Lookup(catalog.StatsKey("stale_stats"))
	if err != nil {
		t.Fatal(err)
	}
	global, err := catalog.DecodeStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.CatTree.Begin()
	if err != nil {
		t.Fatal(err)
	}
	global.Rows = 101 // simulate an older writer that refreshes NSST only
	updated, err := catalog.EncodeStats(global)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Update(catalog.StatsKey("stale_stats"), updated); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path, keys, 48)
	if err != nil {
		t.Fatalf("stale NSPS record prevented restart: %v", err)
	}
	defer db.Close()
	got, ok := db.Cat.Stats("stale_stats")
	if !ok || got.Rows != 101 || len(got.Partitions) != 2 {
		t.Fatalf("global fallback statistics: ok=%v stats=%+v", ok, got)
	}
	for _, part := range got.Partitions {
		if len(part.Columns) != 0 || len(part.Indexes) != 0 || len(part.Vectors) != 0 {
			t.Fatalf("stale local sketches were accepted: %+v", part)
		}
	}
}

func TestPartitionStatsByteBoundTrimsLowestPriorityColumns(t *testing.T) {
	part := catalog.PartitionStats{ID: 2, Rows: 1}
	for ord := 0; ord < catalog.MaxPartitionSketchColumns; ord++ {
		part.Columns = append(part.Columns, catalog.ColumnStats{
			Ord: ord, HasMinMax: true,
			Min: types.StringValue(strings.Repeat("a", 256)),
			Max: types.StringValue(strings.Repeat("z", 256)),
		})
	}
	bounded := boundPartitionStats(1, part)
	if len(bounded.Columns) == 0 || len(bounded.Columns) >= len(part.Columns) {
		t.Fatalf("unexpected bounded sketch width: got=%d original=%d", len(bounded.Columns), len(part.Columns))
	}
	if bounded.Columns[0].Ord != 0 {
		t.Fatalf("priority prefix was not preserved: %+v", bounded.Columns)
	}
	if raw, err := catalog.EncodePartitionStats(1, [32]byte{}, bounded); err != nil || len(raw) > catalog.MaxPartitionStatsBytes {
		t.Fatalf("bounded partition statistics do not encode: bytes=%d err=%v", len(raw), err)
	}
}

func TestTenantPartitionSyntaxRemoved(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	_, err := s.Exec(`CREATE TABLE tenant_events (id STRING, tenant_id UUID NOT NULL, v STRING, PRIMARY KEY (tenant_id, id)) PARTITION BY TENANT (tenant_id) (PARTITION p VALUES IN ('` + tenantA + `'))`)
	if !nerr.HasCode(err, nerr.Syntax) || !strings.Contains(err.Error(), "isolated") {
		t.Fatalf("PARTITION BY TENANT was not rejected with hosting guidance: %v", err)
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

func TestMultiColumnHashPartitionRoutingPruningAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE tenant_sessions (
		account_id STRING NOT NULL,
		shard      STRING NOT NULL,
		id         STRING NOT NULL,
		v          STRING,
		PRIMARY KEY (account_id, shard, id)
	) PARTITION BY HASH (account_id, shard) (
		PARTITION h0 MODULUS 4 REMAINDER 0,
		PARTITION h1 MODULUS 4 REMAINDER 1,
		PARTITION h2 MODULUS 4 REMAINDER 2,
		PARTITION h3 MODULUS 4 REMAINDER 3
	)`)
	execOK(t, s, `INSERT INTO tenant_sessions (account_id, shard, id, v) VALUES
		('acme', 'a', '1', 'one'),
		('acme', 'b', '2', 'two'),
		('globex', 'a', '3', 'three'),
		('initech', 'c', '4', 'four')`)
	if got := execOK(t, s, `SELECT id FROM tenant_sessions ORDER BY id`).Rows; len(got) != 4 {
		t.Fatalf("rows=%d: %+v", len(got), got)
	}

	// Both partition columns pinned to a single equality: prunes to one partition.
	plan := execOK(t, s, `EXPLAIN SELECT * FROM tenant_sessions WHERE account_id = 'acme' AND shard = 'b'`)
	last := plan.Rows[len(plan.Rows)-1][0].Str
	if !strings.Contains(last, "partitions=[") || strings.Contains(last, "partitions=all[") {
		t.Fatalf("multi-column hash pruning missing from EXPLAIN: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM tenant_sessions WHERE account_id = 'acme' AND shard = 'b'`).Rows; len(got) != 1 || got[0][0].Str != "2" {
		t.Fatalf("pruned multi-column hash lookup=%+v", got)
	}

	// Only one partition column constrained: every partition is retained.
	plan = execOK(t, s, `EXPLAIN SELECT * FROM tenant_sessions WHERE account_id = 'acme'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=all[4]") {
		t.Fatalf("partial key must retain all partitions: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM tenant_sessions WHERE account_id = 'acme' ORDER BY id`).Rows; len(got) != 2 {
		t.Fatalf("partial-key scan lost rows: %+v", got)
	}

	// Cross-partition UPDATE that changes a partition column moves the row.
	execOK(t, s, `UPDATE tenant_sessions SET shard = 'z' WHERE account_id = 'acme' AND shard = 'b' AND id = '2'`)
	if got := execOK(t, s, `SELECT v FROM tenant_sessions WHERE account_id = 'acme' AND shard = 'b'`).Rows; len(got) != 0 {
		t.Fatalf("moved row remained under old tuple: %+v", got)
	}
	if got := execOK(t, s, `SELECT v FROM tenant_sessions WHERE account_id = 'acme' AND shard = 'z'`).Rows; len(got) != 1 {
		t.Fatalf("moved row missing under new tuple: %+v", got)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := execOK(t, s, `SELECT id FROM tenant_sessions ORDER BY id`).Rows; len(got) != 4 {
		t.Fatalf("reopened rows=%d: %+v", len(got), got)
	}
	// Routing stays deterministic across restart: the pinned tuple still prunes
	// to a single partition and finds the moved row.
	if got := execOK(t, s, `SELECT id FROM tenant_sessions WHERE account_id = 'acme' AND shard = 'z'`).Rows; len(got) != 1 || got[0][0].Str != "2" {
		t.Fatalf("post-restart multi-column hash lookup=%+v", got)
	}
}

func TestMultiColumnRangeAndListPartitionKeyArityRejected(t *testing.T) {
	s := testDB(t).Session()
	// RANGE (a, b) with a single-value LESS THAN bound is an arity mismatch.
	if _, err := s.Exec(`CREATE TABLE mc_range_bad (
		a STRING NOT NULL,
		b STRING NOT NULL,
		PRIMARY KEY (a, b)
	) PARTITION BY RANGE (a, b) (
		PARTITION p0 VALUES LESS THAN ('m'),
		PARTITION p1 VALUES LESS THAN MAXVALUE
	)`); err == nil || !strings.Contains(err.Error(), "partition tuple arity") {
		t.Fatalf("multi-column RANGE arity not rejected: %v", err)
	}
	// LIST (a) with a two-element membership tuple is an arity mismatch.
	if _, err := s.Exec(`CREATE TABLE mc_list_bad (
		a STRING NOT NULL,
		b STRING NOT NULL,
		PRIMARY KEY (a, b)
	) PARTITION BY LIST (a) (
		PARTITION p0 VALUES IN (('x', 'y'))
	)`); err == nil || !strings.Contains(err.Error(), "partition tuple arity") {
		t.Fatalf("single-column LIST tuple arity not rejected: %v", err)
	}
}

func TestMultiColumnRangePartitionRoutingPruningAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	// Partition boundaries fall on disjoint leading-column ('region') ranges so
	// leading-column pruning is tight; the trailing '' bucket sentinel keeps every
	// ('region', *) tuple inside the same leading-column band.
	execOK(t, s, `CREATE TABLE ledger (
		region STRING NOT NULL,
		bucket STRING NOT NULL,
		id     STRING NOT NULL,
		v      STRING,
		PRIMARY KEY (region, bucket, id)
	) PARTITION BY RANGE (region, bucket) (
		PARTITION p_lo  VALUES LESS THAN ('m', ''),
		PARTITION p_mid VALUES LESS THAN ('t', ''),
		PARTITION p_hi  VALUES LESS THAN MAXVALUE
	)`)
	execOK(t, s, `INSERT INTO ledger (region, bucket, id, v) VALUES
		('eu', 'gold', '1', 'lo'),
		('nl', 'gold', '2', 'mid'),
		('nl', 'bronze', '3', 'mid'),
		('us', 'gold', '4', 'hi')`)
	if got := execOK(t, s, `SELECT id FROM ledger ORDER BY id`).Rows; len(got) != 4 {
		t.Fatalf("rows=%d: %+v", len(got), got)
	}

	// Leading-column equality prunes to the single band that can hold 'nl'.
	plan := execOK(t, s, `EXPLAIN SELECT * FROM ledger WHERE region = 'nl'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=[p_mid]") {
		t.Fatalf("multi-column RANGE leading-column pruning missing: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM ledger WHERE region = 'nl' ORDER BY id`).Rows; len(got) != 2 {
		t.Fatalf("pruned leading-column scan lost rows: %+v", got)
	}

	// A different leading-column value prunes to the top band.
	plan = execOK(t, s, `EXPLAIN SELECT * FROM ledger WHERE region = 'us'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=[p_hi]") {
		t.Fatalf("multi-column RANGE pruning to top band missing: %+v", plan.Rows)
	}

	// A predicate that does not touch the partition key retains every partition.
	plan = execOK(t, s, `EXPLAIN SELECT * FROM ledger WHERE v = 'mid'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=all[3]") {
		t.Fatalf("non-key predicate must retain all partitions: %+v", plan.Rows)
	}

	// Cross-partition UPDATE that moves a row across a leading-column boundary.
	execOK(t, s, `UPDATE ledger SET region = 'us' WHERE region = 'eu' AND id = '1'`)
	if got := execOK(t, s, `SELECT v FROM ledger WHERE region = 'eu'`).Rows; len(got) != 0 {
		t.Fatalf("moved row remained under old tuple: %+v", got)
	}
	if got := execOK(t, s, `SELECT id FROM ledger WHERE region = 'us' ORDER BY id`).Rows; len(got) != 2 {
		t.Fatalf("moved row missing under new tuple: %+v", got)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := execOK(t, s, `SELECT id FROM ledger ORDER BY id`).Rows; len(got) != 4 {
		t.Fatalf("reopened rows=%d: %+v", len(got), got)
	}
	if got := execOK(t, s, `SELECT id FROM ledger WHERE region = 'nl' ORDER BY id`).Rows; len(got) != 2 {
		t.Fatalf("post-restart leading-column scan lost rows: %+v", got)
	}
}

// TestMultiColumnRangePartitionTrailingColumnPruning covers RANGE partitions
// that share a leading partition-key value: a predicate that also pins or bounds
// the trailing partition-key column must prune to the single band that can hold
// the tuple, not every band that shares the leading value.
func TestMultiColumnRangePartitionTrailingColumnPruning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE metrics (
		region STRING NOT NULL,
		shard  STRING NOT NULL,
		id     STRING NOT NULL,
		v      STRING,
		PRIMARY KEY (region, shard, id)
	) PARTITION BY RANGE (region, shard) (
		PARTITION p_a VALUES LESS THAN ('eu', 'm'),
		PARTITION p_b VALUES LESS THAN ('eu', 't'),
		PARTITION p_c VALUES LESS THAN ('eu', 'z'),
		PARTITION p_z VALUES LESS THAN MAXVALUE
	)`)
	execOK(t, s, `INSERT INTO metrics (region, shard, id, v) VALUES
		('ap', 'x', '1', 'a'),
		('eu', 'a', '2', 'a'),
		('eu', 'm', '3', 'b'),
		('eu', 'p', '4', 'b'),
		('eu', 't', '5', 'c'),
		('eu', 'y', '6', 'c'),
		('us', 'a', '7', 'z')`)
	if got := execOK(t, s, `SELECT id FROM metrics ORDER BY id`).Rows; len(got) != 7 {
		t.Fatalf("rows=%d: %+v", len(got), got)
	}

	// Leading value shared by three bands; the trailing equality prunes to one.
	plan := execOK(t, s, `EXPLAIN SELECT * FROM metrics WHERE region = 'eu' AND shard = 'p'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=[p_b]") {
		t.Fatalf("trailing-column RANGE pruning missing: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM metrics WHERE region = 'eu' AND shard = 'p'`).Rows; len(got) != 1 || got[0][0].Str != "4" {
		t.Fatalf("trailing-column pruned lookup lost rows: %+v", got)
	}

	// A bounded trailing range prunes the bands it cannot reach.
	plan = execOK(t, s, `EXPLAIN SELECT * FROM metrics WHERE region = 'eu' AND shard >= 'n' AND shard < 's'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=[p_b]") {
		t.Fatalf("trailing-column RANGE bound pruning missing: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM metrics WHERE region = 'eu' AND shard >= 'n' AND shard < 's' ORDER BY id`).Rows; len(got) != 1 || got[0][0].Str != "4" {
		t.Fatalf("trailing-column range scan lost rows: %+v", got)
	}

	// Exactly on a band boundary: the tuple belongs to the upper (inclusive) band.
	plan = execOK(t, s, `EXPLAIN SELECT * FROM metrics WHERE region = 'eu' AND shard = 'm'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=[p_b]") {
		t.Fatalf("boundary tuple pruned to wrong band: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM metrics WHERE region = 'eu' AND shard = 'm'`).Rows; len(got) != 1 || got[0][0].Str != "3" {
		t.Fatalf("boundary tuple lookup lost rows: %+v", got)
	}

	// Leading value alone still cannot separate bands that share it.
	plan = execOK(t, s, `EXPLAIN SELECT * FROM metrics WHERE region = 'eu'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=all[4]") {
		t.Fatalf("leading-only predicate must retain every shared band: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM metrics WHERE region = 'eu' ORDER BY id`).Rows; len(got) != 5 {
		t.Fatalf("leading-only scan rows=%d: %+v", len(got), got)
	}

	// A different leading value prunes to the trailing band.
	plan = execOK(t, s, `EXPLAIN SELECT * FROM metrics WHERE region = 'us' AND shard = 'a'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=[p_z]") {
		t.Fatalf("distinct leading value pruning missing: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM metrics WHERE region = 'us' AND shard = 'a'`).Rows; len(got) != 1 || got[0][0].Str != "7" {
		t.Fatalf("distinct leading value scan lost rows: %+v", got)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := execOK(t, s, `SELECT id FROM metrics WHERE region = 'eu' AND shard = 'p'`).Rows; len(got) != 1 || got[0][0].Str != "4" {
		t.Fatalf("post-restart trailing-column pruned lookup lost rows: %+v", got)
	}
}

func TestMultiColumnListPartitionRoutingPruningAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE placements (
		region STRING NOT NULL,
		tier   STRING NOT NULL,
		id     STRING NOT NULL,
		v      STRING,
		PRIMARY KEY (region, tier, id)
	) PARTITION BY LIST (region, tier) (
		PARTITION hot  VALUES IN (('us', 'gold'), ('eu', 'gold')),
		PARTITION cold VALUES IN (('us', 'bronze'), ('eu', 'bronze'))
	)`)
	execOK(t, s, `INSERT INTO placements (region, tier, id, v) VALUES
		('us', 'gold', '1', 'a'),
		('eu', 'gold', '2', 'b'),
		('us', 'bronze', '3', 'c'),
		('eu', 'bronze', '4', 'd')`)
	if _, err := s.Exec(`INSERT INTO placements (region, tier, id) VALUES ('us', 'silver', '5')`); err == nil {
		t.Fatal("tuple outside every LIST partition unexpectedly succeeded")
	}

	// Both partition columns pinned: prunes to one partition.
	plan := execOK(t, s, `EXPLAIN SELECT * FROM placements WHERE region = 'eu' AND tier = 'bronze'`)
	last := plan.Rows[len(plan.Rows)-1][0].Str
	if !strings.Contains(last, "partitions=[cold]") {
		t.Fatalf("multi-column LIST pruning missing: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT id FROM placements WHERE region = 'eu' AND tier = 'bronze'`).Rows; len(got) != 1 || got[0][0].Str != "4" {
		t.Fatalf("pruned multi-column LIST lookup=%+v", got)
	}

	// Only one column pinned: every partition retained.
	plan = execOK(t, s, `EXPLAIN SELECT * FROM placements WHERE region = 'eu'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=all[2]") {
		t.Fatalf("partial LIST key must retain all partitions: %+v", plan.Rows)
	}

	// Cross-partition UPDATE moving a row from cold to hot.
	execOK(t, s, `UPDATE placements SET tier = 'gold' WHERE region = 'us' AND tier = 'bronze' AND id = '3'`)
	if got := execOK(t, s, `SELECT v FROM placements WHERE region = 'us' AND tier = 'bronze'`).Rows; len(got) != 0 {
		t.Fatalf("moved row remained in old LIST partition: %+v", got)
	}
	if got := execOK(t, s, `SELECT id FROM placements WHERE region = 'us' AND tier = 'gold' ORDER BY id`).Rows; len(got) != 2 {
		t.Fatalf("moved row missing from new LIST partition: %+v", got)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := execOK(t, s, `SELECT id FROM placements ORDER BY id`).Rows; len(got) != 4 {
		t.Fatalf("reopened rows=%d: %+v", len(got), got)
	}
	if got := execOK(t, s, `SELECT id FROM placements WHERE region = 'eu' AND tier = 'gold'`).Rows; len(got) != 1 || got[0][0].Str != "2" {
		t.Fatalf("post-restart multi-column LIST lookup=%+v", got)
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

func TestPartitionAddDropLifecycleAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE lifecycle_events (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `ALTER TABLE lifecycle_events ADD PARTITION asia VALUES IN ('ap', 'au')`)
	execOK(t, s, `INSERT INTO lifecycle_events (region, id) VALUES ('ap', '1')`)
	plan := execOK(t, s, `EXPLAIN SELECT * FROM lifecycle_events WHERE region = 'ap'`)
	if len(plan.Rows) == 0 || !strings.Contains(plan.Rows[len(plan.Rows)-1][0].Str, "partitions=[asia]") {
		t.Fatalf("added partition not pruned: %+v", plan.Rows)
	}
	if _, err := s.Exec(`ALTER TABLE lifecycle_events DROP PARTITION asia`); err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("non-empty partition drop was not rejected: %v", err)
	}
	execOK(t, s, `ALTER TABLE lifecycle_events DROP PARTITION europe`)
	if _, err := s.Exec(`INSERT INTO lifecycle_events (region, id) VALUES ('eu', '2')`); err == nil {
		t.Fatal("dropped LIST value still accepted")
	}
	execOK(t, s, `BEGIN`)
	execOK(t, s, `ALTER TABLE lifecycle_events ADD PARTITION canada VALUES IN ('ca')`)
	execOK(t, s, `ROLLBACK`)
	if _, err := s.Exec(`INSERT INTO lifecycle_events (region, id) VALUES ('ca', '3')`); err == nil {
		t.Fatal("rolled-back partition addition still routed rows")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := execOK(t, s, `SELECT id FROM lifecycle_events`).Rows; len(got) != 1 || got[0][0].Str != "1" {
		t.Fatalf("reopened lifecycle rows=%+v", got)
	}
	if _, err := s.Exec(`INSERT INTO lifecycle_events (region, id) VALUES ('eu', '4')`); err == nil {
		t.Fatal("dropped partition returned after restart")
	}

	execOK(t, s, `CREATE TABLE range_lifecycle (
		k STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (k, id)
	) PARTITION BY RANGE (k) (
		PARTITION low VALUES LESS THAN ('m')
	)`)
	execOK(t, s, `ALTER TABLE range_lifecycle ADD PARTITION high VALUES LESS THAN MAXVALUE`)
	execOK(t, s, `INSERT INTO range_lifecycle (k, id) VALUES ('z', '1')`)
	if _, err := s.Exec(`ALTER TABLE range_lifecycle ADD PARTITION impossible VALUES LESS THAN MAXVALUE`); err == nil || !strings.Contains(err.Error(), "MAXVALUE") {
		t.Fatalf("append after MAXVALUE was not rejected: %v", err)
	}

	execOK(t, s, `CREATE TABLE hash_lifecycle (
		k STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (k, id)
	) PARTITION BY HASH (k) (
		PARTITION h0 MODULUS 2 REMAINDER 0,
		PARTITION h1 MODULUS 2 REMAINDER 1
	)`)
	if _, err := s.Exec(`ALTER TABLE hash_lifecycle DROP PARTITION h1`); err == nil || !strings.Contains(err.Error(), "redistribution") {
		t.Fatalf("HASH membership change was not rejected: %v", err)
	}

	execOK(t, s, `CREATE TABLE id_lifecycle (
		k STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (k, id)
	) PARTITION BY LIST (k) (
		PARTITION first VALUES IN ('a'),
		PARTITION second VALUES IN ('b')
	)`)
	execOK(t, s, `ALTER TABLE id_lifecycle DROP PARTITION second`)
	execOK(t, s, `ALTER TABLE id_lifecycle ADD PARTITION third VALUES IN ('c')`)
	tab, ok := db.Cat.Get("id_lifecycle")
	if !ok || len(tab.Partitioning.Partitions) != 2 || tab.Partitioning.Partitions[1].ID != 3 || tab.Partitioning.NextID != 4 {
		t.Fatalf("partition identities were reused: %+v", tab)
	}

	execOK(t, s, `CREATE TABLE vector_lifecycle (
		k STRING NOT NULL,
		id STRING NOT NULL,
		emb VECTOR<F32,3>,
		PRIMARY KEY (k, id)
	) PARTITION BY LIST (k) (
		PARTITION first VALUES IN ('a')
	)`)
	execOK(t, s, `ALTER TABLE vector_lifecycle ADD PARTITION removable VALUES IN ('b')`)
	vectorTab, ok := db.Cat.Get("vector_lifecycle")
	if !ok || len(vectorTab.Partitioning.Partitions) != 2 || vectorTab.Partitioning.Partitions[1].VecMeta == 0 {
		t.Fatalf("partition-local vector tree was not allocated: %+v", vectorTab)
	}
	droppedID := vectorTab.Partitioning.Partitions[1].ID
	execOK(t, s, `ALTER TABLE vector_lifecycle DROP PARTITION removable`)
	db.drainCommittedReclaims()
	if err := db.LastReclaimError(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.partitionHeap("vector_lifecycle", droppedID); err == nil {
		t.Fatal("reclaimed partition heap remained registered")
	}
	if _, err := db.partitionVec("vector_lifecycle", droppedID); err == nil {
		t.Fatal("reclaimed partition vector tree remained registered")
	}
}

func TestPartitionAttachDetachOwnershipAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 96)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE routed_events (
		region STRING NOT NULL,
		id DECIMAL(12,0) DEFAULT AI(),
		name STRING NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `CREATE INDEX ix_attach_name ON routed_events (name)`)
	execOK(t, s, `CREATE TABLE apac (
		region STRING NOT NULL,
		id DECIMAL(12,0) DEFAULT AI(),
		name STRING NOT NULL,
		PRIMARY KEY (region, id)
	)`)
	execOK(t, s, `CREATE INDEX ix_attach_name ON apac (name)`)
	execOK(t, s, `INSERT INTO apac (region, id, name) VALUES ('ap', 50, 'attached')`)
	execOK(t, s, `ALTER TABLE routed_events ATTACH PARTITION apac VALUES IN ('ap')`)
	if _, err := s.Exec(`SELECT * FROM apac`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("attached source table remained visible: %v", err)
	}
	got := execOK(t, s, `SELECT region, id FROM routed_events WHERE name = 'attached'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "ap" || got.Rows[0][1].Dec.String() != "50" {
		t.Fatalf("attached rows/index missing: %+v", got.Rows)
	}
	execOK(t, s, `INSERT INTO routed_events (region, name) VALUES ('ap', 'after-attach')`)
	got = execOK(t, s, `SELECT id FROM routed_events WHERE name = 'after-attach'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "51" {
		t.Fatalf("parent AI sequence did not absorb attached rows: %+v", got.Rows)
	}

	execOK(t, s, `CREATE TABLE invalid_attach (
		region STRING NOT NULL,
		id DECIMAL(12,0) DEFAULT AI(),
		name STRING NOT NULL,
		PRIMARY KEY (region, id)
	)`)
	execOK(t, s, `CREATE INDEX ix_attach_name ON invalid_attach (name)`)
	execOK(t, s, `INSERT INTO invalid_attach (region, name) VALUES ('wrong', 'rejected')`)
	if _, err := s.Exec(`ALTER TABLE routed_events ATTACH PARTITION invalid_attach VALUES IN ('ca')`); err == nil || !strings.Contains(err.Error(), "outside the partition rule") {
		t.Fatalf("out-of-rule attach was not rejected: %v", err)
	}
	if got := execOK(t, s, `SELECT name FROM invalid_attach`).Rows; len(got) != 1 {
		t.Fatalf("failed attach consumed source table: %+v", got)
	}

	execOK(t, s, `CREATE TABLE rollback_attach (
		region STRING NOT NULL,
		id DECIMAL(12,0) DEFAULT AI(),
		name STRING NOT NULL,
		PRIMARY KEY (region, id)
	)`)
	execOK(t, s, `CREATE INDEX ix_attach_name ON rollback_attach (name)`)
	execOK(t, s, `INSERT INTO rollback_attach (region, name) VALUES ('rb', 'rollback')`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `ALTER TABLE routed_events ATTACH PARTITION rollback_attach VALUES IN ('rb')`)
	execOK(t, s, `ROLLBACK`)
	if got := execOK(t, s, `SELECT name FROM rollback_attach`).Rows; len(got) != 1 {
		t.Fatalf("rolled-back attach lost source: %+v", got)
	}
	if _, err := s.Exec(`INSERT INTO routed_events (region, name) VALUES ('rb', 'not-routed')`); err == nil {
		t.Fatal("rolled-back attach still routed parent rows")
	}

	execOK(t, s, `ALTER TABLE routed_events DETACH PARTITION apac`)
	if got := execOK(t, s, `SELECT name FROM routed_events WHERE region = 'ap'`).Rows; len(got) != 0 {
		t.Fatalf("detached rows remained in parent: %+v", got)
	}
	got = execOK(t, s, `SELECT name FROM apac ORDER BY name`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "after-attach" || got.Rows[1][0].Str != "attached" {
		t.Fatalf("detached table rows/index missing: %+v", got.Rows)
	}
	execOK(t, s, `INSERT INTO apac (region, name) VALUES ('ap', 'after-detach')`)
	got = execOK(t, s, `SELECT id FROM apac WHERE name = 'after-detach'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "52" {
		t.Fatalf("detached AI sequence did not continue: %+v", got.Rows)
	}

	execOK(t, s, `BEGIN`)
	execOK(t, s, `ALTER TABLE routed_events DETACH PARTITION europe`)
	execOK(t, s, `ROLLBACK`)
	if _, err := s.Exec(`SELECT * FROM europe`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("rolled-back detach published a table: %v", err)
	}
	execOK(t, s, `INSERT INTO routed_events (region, name) VALUES ('eu', 'still-routed')`)

	tab, ok := db.Cat.Get("routed_events")
	if !ok || tab.Partitioning == nil || tab.Partitioning.NextID != 4 {
		t.Fatalf("detach changed stable-ID high water: %+v", tab)
	}
	execOK(t, s, `ALTER TABLE routed_events ADD PARTITION canada VALUES IN ('ca')`)
	tab, _ = db.Cat.Get("routed_events")
	if got := tab.Partitioning.Partitions[len(tab.Partitioning.Partitions)-1].ID; got != 4 {
		t.Fatalf("detached stable ID was reused: got %d", got)
	}

	db.Eng.Kill()
	db, err = Open(path, keys, 96)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := execOK(t, s, `SELECT name FROM apac ORDER BY name`).Rows; len(got) != 3 {
		t.Fatalf("detached table failed restart: %+v", got)
	}
	if got := execOK(t, s, `SELECT name FROM routed_events WHERE region = 'eu'`).Rows; len(got) != 1 || got[0][0].Str != "still-routed" {
		t.Fatalf("parent failed restart after detach: %+v", got)
	}
}

func TestPartitionAttachDetachCrashBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE crash_parent (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION base VALUES IN ('base')
	)`)
	execOK(t, s, `CREATE TABLE crash_attach (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	)`)
	execOK(t, s, `INSERT INTO crash_attach (region, id) VALUES ('new', '1')`)
	db.Eng.SetCrash(wal.PointBeforeCommitRecord)
	if _, err := s.Exec(`ALTER TABLE crash_parent ATTACH PARTITION crash_attach VALUES IN ('new')`); !wal.IsCrash(err) {
		t.Fatalf("expected attach commit crash, got %v", err)
	}
	db.Eng.Kill()
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s = db.Session()
	if got := execOK(t, s, `SELECT id FROM crash_attach`).Rows; len(got) != 1 || got[0][0].Str != "1" {
		t.Fatalf("crashed attach consumed source: %+v", got)
	}
	if _, err := s.Exec(`INSERT INTO crash_parent (region, id) VALUES ('new', '2')`); err == nil {
		t.Fatal("crashed attach published routing metadata")
	}

	execOK(t, s, `ALTER TABLE crash_parent ATTACH PARTITION crash_attach VALUES IN ('new')`)
	db.Eng.SetCrash(wal.PointBeforeCommitRecord)
	if _, err := s.Exec(`ALTER TABLE crash_parent DETACH PARTITION crash_attach`); !wal.IsCrash(err) {
		t.Fatalf("expected detach commit crash, got %v", err)
	}
	db.Eng.Kill()
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if _, err := s.Exec(`SELECT * FROM crash_attach`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("crashed detach published standalone table: %v", err)
	}
	if got := execOK(t, s, `SELECT id FROM crash_parent WHERE region = 'new'`).Rows; len(got) != 1 || got[0][0].Str != "1" {
		t.Fatalf("crashed detach lost parent row: %+v", got)
	}
}

func TestPartitionAttachRequiresMatchingSchemaAndIndexes(t *testing.T) {
	s := testDB(t).Session()
	execOK(t, s, `CREATE TABLE attach_parent (
		region STRING NOT NULL,
		id STRING NOT NULL,
		name STRING,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION base VALUES IN ('base')
	)`)
	execOK(t, s, `CREATE INDEX ix_attach_match ON attach_parent (name)`)
	execOK(t, s, `CREATE TABLE mismatched (
		region STRING NOT NULL,
		id STRING NOT NULL,
		name TEXT,
		PRIMARY KEY (region, id)
	)`)
	if _, err := s.Exec(`ALTER TABLE attach_parent ATTACH PARTITION mismatched VALUES IN ('m')`); err == nil || !strings.Contains(err.Error(), "schema does not match") {
		t.Fatalf("schema mismatch was not rejected: %v", err)
	}
	execOK(t, s, `CREATE TABLE missing_index (
		region STRING NOT NULL,
		id STRING NOT NULL,
		name STRING,
		PRIMARY KEY (region, id)
	)`)
	if _, err := s.Exec(`ALTER TABLE attach_parent ATTACH PARTITION missing_index VALUES IN ('i')`); err == nil || !strings.Contains(err.Error(), "indexes do not match") {
		t.Fatalf("index mismatch was not rejected: %v", err)
	}
}

func TestPartitionAttachDetachRBAC(t *testing.T) {
	db := testDB(t)
	admin := db.Session()
	execOK(t, admin, `CREATE TABLE rbac_parent (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION base VALUES IN ('base')
	)`)
	execOK(t, admin, `CREATE TABLE rbac_source (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	)`)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivAlter, security.ScopeTable, "rbac_parent"); err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	attach := `ALTER TABLE rbac_parent ATTACH PARTITION rbac_source VALUES IN ('new')`
	if _, err := app.Exec(attach); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ATTACH without source DROP was not denied: %v", err)
	}
	if err := acl.Grant("app", security.PrivDrop, security.ScopeTable, "rbac_source"); err != nil {
		t.Fatal(err)
	}
	execOK(t, app, attach)
	if _, err := app.Exec(`ALTER TABLE rbac_parent DETACH PARTITION rbac_source`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("DETACH without database CREATE was not denied: %v", err)
	}
	if err := acl.Grant("app", security.PrivCreate, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	execOK(t, app, `ALTER TABLE rbac_parent DETACH PARTITION rbac_source`)
}

func TestPartitionLocalIndexesAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE partition_indexed (
		region STRING NOT NULL,
		id STRING NOT NULL,
		name STRING NOT NULL,
		note STRING,
		metadata JSON,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `INSERT INTO partition_indexed (region, id, name, note, metadata) VALUES
		('us', '1', 'alpha', 'one', '{"category":"a"}'),
		('eu', '2', 'beta', 'two', '{"category":"b"}')`)
	execOK(t, s, `CREATE INDEX ix_partition_name ON partition_indexed (name) INCLUDE (note)`)
	plan := execOK(t, s, `EXPLAIN SELECT name, note FROM partition_indexed WHERE name = 'alpha'`)
	if !explainHas(plan, "IndexScan") || !explainHas(plan, "ix_partition_name") || !explainHas(plan, "partitions=all[2]") {
		t.Fatalf("partition-local covering plan: %+v", explainOps(plan))
	}
	got := execOK(t, s, `SELECT name, note FROM partition_indexed WHERE name = 'alpha'`)
	if len(got.Rows) != 1 || got.Rows[0][1].Str != "one" {
		t.Fatalf("partition-local covering lookup: %+v", got.Rows)
	}
	tab, ok := db.Cat.Get("partition_indexed")
	if !ok || len(tab.Indexes) != 1 || tab.Indexes[0].Meta != 0 {
		t.Fatalf("logical partition index metadata: %+v", tab)
	}
	for _, part := range tab.Partitioning.Partitions {
		if len(part.Indexes) != 1 || part.Indexes[0].Meta == 0 {
			t.Fatalf("partition-local root missing: %+v", part)
		}
	}

	execOK(t, s, `UPDATE partition_indexed SET region = 'eu' WHERE region = 'us' AND id = '1'`)
	got = execOK(t, s, `SELECT region, note FROM partition_indexed WHERE name = 'alpha'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "eu" {
		t.Fatalf("cross-partition index maintenance: %+v", got.Rows)
	}
	execOK(t, s, `ALTER TABLE partition_indexed ADD PARTITION asia VALUES IN ('ap')`)
	execOK(t, s, `INSERT INTO partition_indexed (region, id, name, note, metadata) VALUES ('ap', '3', 'gamma', 'three', '{"category":"c"}')`)
	got = execOK(t, s, `SELECT note FROM partition_indexed WHERE region = 'ap' AND name = 'gamma'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "three" {
		t.Fatalf("index on added partition: %+v", got.Rows)
	}
	execOK(t, s, `REBUILD INDEX ix_partition_name`)
	got = execOK(t, s, `SELECT note FROM partition_indexed WHERE name = 'beta'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "two" {
		t.Fatalf("rebuilt partition index: %+v", got.Rows)
	}
	execOK(t, s, `CREATE INDEX ix_partition_category ON partition_indexed (metadata.category)`)
	got = execOK(t, s, `SELECT id FROM partition_indexed WHERE metadata.category = 'c'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "3" {
		t.Fatalf("partition-local JSON index: %+v", got.Rows)
	}
	// Cross-partition secondary UNIQUE: the build succeeds because every name is
	// distinct across partitions, and later writes are enforced against every
	// partition-local root.
	execOK(t, s, `CREATE UNIQUE INDEX ux_partition_name ON partition_indexed (name)`)
	if _, err := s.Exec(`INSERT INTO partition_indexed (region, id, name, note, metadata) VALUES ('us', '9', 'beta', 'dup', '{"category":"a"}')`); err == nil || !strings.Contains(err.Error(), "across partitions") {
		t.Fatalf("cross-partition UNIQUE INSERT not rejected: %v", err)
	}
	if _, err := s.Exec(`UPDATE partition_indexed SET name = 'gamma' WHERE region = 'eu' AND id = '2'`); err == nil || !strings.Contains(err.Error(), "across partitions") {
		t.Fatalf("cross-partition UNIQUE UPDATE not rejected: %v", err)
	}
	execOK(t, s, `INSERT INTO partition_indexed (region, id, name, note, metadata) VALUES ('us', '9', 'delta', 'ok', '{"category":"a"}')`)
	execOK(t, s, `DELETE FROM partition_indexed WHERE region = 'us' AND id = '9'`)
	execOK(t, s, `DROP INDEX ux_partition_name`)
	tab, _ = db.Cat.Get("partition_indexed")
	var indexedParts []uint32
	for _, part := range tab.Partitioning.Partitions {
		indexedParts = append(indexedParts, part.ID)
	}
	execOK(t, s, `DROP INDEX ix_partition_name`)
	for _, id := range indexedParts {
		if _, err := db.partitionIndex("partition_indexed", id, "ix_partition_name"); err == nil {
			t.Fatalf("reclaimed local index handle remains for partition %d", id)
		}
	}
	tab, _ = db.Cat.Get("partition_indexed")
	for _, part := range tab.Partitioning.Partitions {
		for _, idx := range part.Indexes {
			if idx.Name == "ix_partition_name" {
				t.Fatalf("dropped local index remains in catalog: %+v", part)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got = execOK(t, s, `SELECT id FROM partition_indexed WHERE metadata.category = 'a'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "1" {
		t.Fatalf("reopened partition-local JSON index: %+v", got.Rows)
	}
}

func expectErrContains(t *testing.T, s *Session, sql, want string) {
	t.Helper()
	if _, err := s.Exec(sql); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: expected error containing %q, got %v", sql, want, err)
	}
}

func TestPartitionCrossPartitionUnique(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE accounts (
		shard STRING NOT NULL,
		id STRING NOT NULL,
		email STRING NOT NULL,
		PRIMARY KEY (shard, id)
	) PARTITION BY LIST (shard) (
		PARTITION west VALUES IN ('w'),
		PARTITION east VALUES IN ('e')
	)`)
	execOK(t, s, `INSERT INTO accounts (shard, id, email) VALUES ('w', '1', 'a@x'), ('e', '2', 'a@x')`)

	// Non-plain UNIQUE indexes stay rejected on partitioned tables.
	expectErrContains(t, s, `CREATE UNIQUE INDEX ux_bad ON accounts (email) WHERE id > '0'`, "not supported in this slice")
	expectErrContains(t, s, `CREATE UNIQUE INDEX ux_bad ON accounts (LOWER(email))`, "not supported in this slice")

	// PK-target UPSERT routes to the proposed row's partition heap.
	execOK(t, s, `UPSERT INTO accounts (shard, id, email) VALUES ('w', '1', 'z@x') ON UNIQUE (shard, id) SET email = excluded.email`)
	got := execOK(t, s, `SELECT email FROM accounts WHERE shard = 'w' AND id = '1'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "z@x" {
		t.Fatalf("PK-target UPSERT update: %+v", got.Rows)
	}
	execOK(t, s, `UPSERT INTO accounts (shard, id, email) VALUES ('e', '7', 'e7@x') ON UNIQUE (shard, id) SET email = excluded.email`)
	got = execOK(t, s, `SELECT email FROM accounts WHERE shard = 'e' AND id = '7'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "e7@x" {
		t.Fatalf("PK-target UPSERT insert: %+v", got.Rows)
	}
	execOK(t, s, `UPSERT INTO accounts (shard, id, email) VALUES ('w', '1', 'a@x') ON UNIQUE (shard, id) SET email = excluded.email`)

	// Build-time check: a value duplicated across partitions blocks the index.
	expectErrContains(t, s, `CREATE UNIQUE INDEX ux_email ON accounts (email)`, "across partitions")

	execOK(t, s, `UPDATE accounts SET email = 'b@x' WHERE shard = 'e' AND id = '2'`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_email ON accounts (email)`)

	// Write-path enforcement against every partition-local root.
	expectErrContains(t, s, `INSERT INTO accounts (shard, id, email) VALUES ('w', '3', 'b@x')`, "across partitions")
	execOK(t, s, `INSERT INTO accounts (shard, id, email) VALUES ('w', '3', 'c@x')`)
	expectErrContains(t, s, `UPDATE accounts SET email = 'b@x' WHERE shard = 'w' AND id = '3'`, "across partitions")

	// Same statement fails deterministically (follower replay parity).
	_, e1 := s.Exec(`INSERT INTO accounts (shard, id, email) VALUES ('w', '9', 'b@x')`)
	_, e2 := s.Exec(`INSERT INTO accounts (shard, id, email) VALUES ('w', '9', 'b@x')`)
	if e1 == nil || e2 == nil || e1.Error() != e2.Error() {
		t.Fatalf("non-deterministic cross-partition UNIQUE rejection: %v / %v", e1, e2)
	}

	// Cross-partition move: a clean move rewrites the local roots correctly.
	execOK(t, s, `UPDATE accounts SET shard = 'e' WHERE shard = 'w' AND id = '1'`) // a@x moves w -> e
	got = execOK(t, s, `SELECT shard FROM accounts WHERE email = 'a@x'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "e" {
		t.Fatalf("clean cross-partition move: %+v", got.Rows)
	}
	// A move whose new email already lives in the destination partition is a
	// plain duplicate rejected by the destination local root.
	execOK(t, s, `INSERT INTO accounts (shard, id, email) VALUES ('w', '8', 'z@x')`)
	expectErrContains(t, s, `UPDATE accounts SET shard = 'e', email = 'b@x' WHERE shard = 'w' AND id = '8'`, "duplicate")

	// Survives restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	expectErrContains(t, s, `INSERT INTO accounts (shard, id, email) VALUES ('w', '20', 'b@x')`, "across partitions")
	execOK(t, s, `INSERT INTO accounts (shard, id, email) VALUES ('w', '20', 'd@x')`)

	// ATTACH PARTITION validates incoming keys against existing partitions.
	execOK(t, s, `CREATE TABLE south (
		shard STRING NOT NULL,
		id STRING NOT NULL,
		email STRING NOT NULL,
		PRIMARY KEY (shard, id)
	)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_email ON south (email)`)
	execOK(t, s, `INSERT INTO south (shard, id, email) VALUES ('s', '40', 'b@x')`)
	expectErrContains(t, s, `ALTER TABLE accounts ATTACH PARTITION south VALUES IN ('s')`, "duplicate")
	execOK(t, s, `UPDATE south SET email = 'unique@x' WHERE id = '40'`)
	execOK(t, s, `ALTER TABLE accounts ATTACH PARTITION south VALUES IN ('s')`)
	expectErrContains(t, s, `INSERT INTO accounts (shard, id, email) VALUES ('e', '41', 'unique@x')`, "across partitions")

	// Cross-partition move blocked by a *third* partition's local root.
	execOK(t, s, `INSERT INTO accounts (shard, id, email) VALUES ('w', '50', 'movable@x')`)
	expectErrContains(t, s, `UPDATE accounts SET shard = 'e', email = 'unique@x' WHERE shard = 'w' AND id = '50'`, "across partitions")
	execOK(t, s, `UPDATE accounts SET shard = 'e' WHERE shard = 'w' AND id = '50'`)
}

// TestPartitionUpsert covers UPSERT on RANGE/HASH/LIST partitioned tables:
// PK-target insert/update routed to the owning partition heap, secondary
// cross-partition UNIQUE conflict resolution against any partition-local root,
// a partition-changing UPSERT SET that moves the row between heaps, and
// cross-partition UNIQUE still fail-closed through the UPSERT write path.
func TestPartitionUpsert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE ledger (
		region STRING NOT NULL,
		id STRING NOT NULL,
		code STRING NOT NULL,
		balance DECIMAL(12,2) NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION amer VALUES IN ('a'),
		PARTITION emea VALUES IN ('e'),
		PARTITION apac VALUES IN ('p')
	)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_code ON ledger (code)`)
	execOK(t, s, `INSERT INTO ledger (region, id, code, balance) VALUES
		('a', '1', 'AAA', 100), ('e', '2', 'EEE', 200), ('p', '3', 'PPP', 300)`)

	// PK-target UPSERT: update the row in its own partition heap.
	res := execOK(t, s, `UPSERT INTO ledger (region, id, code, balance) VALUES ('e', '2', 'EEE', 999)
		ON UNIQUE (region, id) SET balance = excluded.balance`)
	if res.Affected != 1 {
		t.Fatalf("pk upsert affected=%d", res.Affected)
	}
	got := execOK(t, s, `SELECT balance FROM ledger WHERE region = 'e' AND id = '2'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "999.00" {
		t.Fatalf("pk upsert update: %+v", got.Rows)
	}
	// PK-target UPSERT: fresh insert routed to the apac heap.
	execOK(t, s, `UPSERT INTO ledger (region, id, code, balance) VALUES ('p', '4', 'PP4', 40)
		ON UNIQUE (region, id) SET balance = excluded.balance`)
	got = execOK(t, s, `SELECT code FROM ledger WHERE region = 'p' AND id = '4'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "PP4" {
		t.Fatalf("pk upsert insert: %+v", got.Rows)
	}

	// Secondary cross-partition UNIQUE target: the conflicting 'AAA' row lives in
	// the amer partition even though the proposed PK routes to emea. The update
	// wins on the code key and rewrites the existing amer row.
	execOK(t, s, `UPSERT INTO ledger (region, id, code, balance) VALUES ('e', '99', 'AAA', 555)
		ON UNIQUE (code) SET balance = excluded.balance`)
	got = execOK(t, s, `SELECT region, id, balance FROM ledger WHERE code = 'AAA'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" || got.Rows[0][1].Str != "1" || got.Rows[0][2].Dec.String() != "555.00" {
		t.Fatalf("cross-partition unique upsert: %+v", got.Rows)
	}

	// Secondary UNIQUE target with no conflict: plain insert routed by PK.
	execOK(t, s, `UPSERT INTO ledger (region, id, code, balance) VALUES ('a', '5', 'AA5', 5)
		ON UNIQUE (code) SET balance = excluded.balance`)
	got = execOK(t, s, `SELECT COUNT(*) FROM ledger`)
	if got.Rows[0][0].Dec.String() != "5" {
		t.Fatalf("row count after unique-target insert: %+v", got.Rows)
	}

	// UPSERT SET that changes the partition column moves the row between heaps.
	execOK(t, s, `UPSERT INTO ledger (region, id, code, balance) VALUES ('a', '1', 'AAA', 555)
		ON UNIQUE (region, id) SET region = 'p'`)
	got = execOK(t, s, `SELECT region FROM ledger WHERE code = 'AAA'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "p" {
		t.Fatalf("partition-moving upsert: %+v", got.Rows)
	}

	// Cross-partition UNIQUE still fail-closed through the UPSERT insert path:
	// inserting a new PK whose code duplicates another partition is rejected.
	expectErrContains(t, s, `UPSERT INTO ledger (region, id, code, balance) VALUES ('a', '6', 'EEE', 1)
		ON UNIQUE (region, id) SET balance = excluded.balance`, "across partitions")

	// Survives restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got = execOK(t, s, `SELECT region, balance FROM ledger WHERE code = 'AAA'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "p" || got.Rows[0][1].Dec.String() != "555.00" {
		t.Fatalf("reopened upsert state: %+v", got.Rows)
	}
	execOK(t, s, `UPSERT INTO ledger (region, id, code, balance) VALUES ('e', '2', 'EEE', 111)
		ON UNIQUE (region, id) SET balance = excluded.balance`)
	got = execOK(t, s, `SELECT balance FROM ledger WHERE region = 'e' AND id = '2'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "111.00" {
		t.Fatalf("post-restart upsert: %+v", got.Rows)
	}
}

// TestPartitionCrossPartitionUniqueSerializedWriters checks that a second
// transaction inserting the same UNIQUE value into a different partition blocks
// on the shared key lock held by the first transaction and, once that
// transaction commits, is rejected by the cross-partition probe rather than
// admitting a duplicate.
func TestPartitionCrossPartitionUniqueSerializedWriters(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE reg (
		shard STRING NOT NULL,
		id STRING NOT NULL,
		email STRING NOT NULL,
		PRIMARY KEY (shard, id)
	) PARTITION BY LIST (shard) (
		PARTITION s0 VALUES IN ('0'),
		PARTITION s1 VALUES IN ('1')
	)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_reg_email ON reg (email)`)

	a := db.Session()
	execOK(t, a, `BEGIN`)
	execOK(t, a, `INSERT INTO reg (shard, id, email) VALUES ('0', '1', 'race@x')`)

	b := db.Session()
	done := make(chan error, 1)
	go func() {
		_, err := b.Exec(`INSERT INTO reg (shard, id, email) VALUES ('1', '1', 'race@x')`)
		done <- err
	}()

	// B must not complete while A still holds the key lock.
	select {
	case err := <-done:
		t.Fatalf("second writer completed before the first committed: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	execOK(t, a, `COMMIT`)

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "across partitions") {
			t.Fatalf("second writer was not rejected after commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second writer did not unblock after the first committed")
	}

	got := execOK(t, s, `SELECT shard FROM reg WHERE email = 'race@x'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "0" {
		t.Fatalf("cross-partition UNIQUE admitted a duplicate: %+v", got.Rows)
	}
}

// TestPartitionWiseAggregation checks that aggregation over a partitioned table
// runs partition-wise (one partial hash aggregation per surviving partition,
// merged) and returns results identical to a single aggregation over the union.
func TestPartitionWiseAggregation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE part_kv (
		bucket STRING NOT NULL,
		k      STRING NOT NULL,
		n      DECIMAL(10,0) NOT NULL,
		PRIMARY KEY (bucket, k)
	) PARTITION BY RANGE (bucket) (
		PARTITION b0 VALUES LESS THAN ('2'),
		PARTITION b1 VALUES LESS THAN ('4'),
		PARTITION b2 VALUES LESS THAN ('6'),
		PARTITION b3 VALUES LESS THAN MAXVALUE
	)`)
	// 'shared' lands in a different partition per bucket, so its group must be
	// folded across partitions during the merge.
	execOK(t, s, `INSERT INTO part_kv (bucket, k, n) VALUES
		('1', 'shared', '10'), ('1', 'a', '1'),
		('3', 'shared', '20'), ('3', 'b', '2'),
		('5', 'shared', '30'), ('5', 'c', '3'),
		('7', 'shared', '40'), ('7', 'd', '4')`)

	// Unpruned SUM: EXPLAIN marks the Aggregate partition-wise and keeps every band.
	plan := execOK(t, s, `EXPLAIN SELECT SUM(n) FROM part_kv`)
	var sawPW, sawAll bool
	for _, r := range plan.Rows {
		if strings.Contains(r[0].Str, "Aggregate") && strings.Contains(r[0].Str, "partition-wise") {
			sawPW = true
		}
		if strings.Contains(r[0].Str, "partitions=all[4]") {
			sawAll = true
		}
	}
	if !sawPW || !sawAll {
		t.Fatalf("partition-wise aggregate / all-bands scan missing from EXPLAIN: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT SUM(n) FROM part_kv`).Rows; len(got) != 1 || got[0][0].Dec.String() != "110" {
		t.Fatalf("unpruned SUM = %+v, want 110", got)
	}
	if got := execOK(t, s, `SELECT COUNT(*) FROM part_kv`).Rows; len(got) != 1 || got[0][0].Dec.String() != "8" {
		t.Fatalf("unpruned COUNT(*) = %+v, want 8", got)
	}

	// GROUP BY a column whose groups span partitions: the 'shared' group is
	// present in all four partitions and must be merged into a single row.
	grouped := execOK(t, s, `SELECT k, SUM(n) FROM part_kv GROUP BY k ORDER BY k`).Rows
	want := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "shared": "100"}
	if len(grouped) != len(want) {
		t.Fatalf("GROUP BY k rows = %+v, want %d groups", grouped, len(want))
	}
	for _, r := range grouped {
		if want[r[0].Str] != r[1].Dec.String() {
			t.Fatalf("GROUP BY k group %q = %s, want %s", r[0].Str, r[1].Dec.String(), want[r[0].Str])
		}
	}

	// Pruned aggregation still folds correctly and prunes in EXPLAIN.
	plan = execOK(t, s, `EXPLAIN SELECT SUM(n) FROM part_kv WHERE bucket = '3'`)
	if last := plan.Rows[len(plan.Rows)-1][0].Str; !strings.Contains(last, "partitions=[b1]") {
		t.Fatalf("pruned aggregate scan missing partition filter: %+v", plan.Rows)
	}
	if got := execOK(t, s, `SELECT SUM(n) FROM part_kv WHERE bucket = '3'`).Rows; len(got) != 1 || got[0][0].Dec.String() != "22" {
		t.Fatalf("pruned SUM = %+v, want 22", got)
	}

	// Survives restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := execOK(t, db.Session(), `SELECT SUM(n) FROM part_kv`).Rows; len(got) != 1 || got[0][0].Dec.String() != "110" {
		t.Fatalf("reopened SUM = %+v, want 110", got)
	}
}

// TestPartitionWiseJoin checks that an equi-join between two identically
// partitioned tables on their partition key runs as one join per aligned
// partition pair (visible as "partition-wise" in EXPLAIN) and returns results
// identical to a single join over the whole relations, including for pruned
// inputs, LEFT joins, HASH schemes, and after restart. A join that is not
// partition-aligned falls back to the generic path.
func TestPartitionWiseJoin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()

	// Partition key g is not the leading PK column, so a predicate on g prunes
	// partitions without collapsing the scan into a PK index range.
	mkRange := func(name string) {
		execOK(t, s, `CREATE TABLE `+name+` (
			g STRING NOT NULL,
			id STRING NOT NULL,
			v  DECIMAL(10,0) NOT NULL,
			PRIMARY KEY (id, g)
		) PARTITION BY RANGE (g) (
			PARTITION p0 VALUES LESS THAN ('3'),
			PARTITION p1 VALUES LESS THAN ('6'),
			PARTITION p2 VALUES LESS THAN MAXVALUE
		)`)
	}
	mkRange("lhs")
	mkRange("rhs")
	// A non-aligned partner: different RANGE bounds.
	execOK(t, s, `CREATE TABLE skew (
		g STRING NOT NULL,
		id STRING NOT NULL,
		v  DECIMAL(10,0) NOT NULL,
		PRIMARY KEY (id, g)
	) PARTITION BY RANGE (g) (
		PARTITION q0 VALUES LESS THAN ('4'),
		PARTITION q1 VALUES LESS THAN MAXVALUE
	)`)

	execOK(t, s, `INSERT INTO lhs (g, id, v) VALUES
		('1','a','10'), ('1','b','11'), ('4','c','40'), ('7','d','70'), ('7','e','71')`)
	execOK(t, s, `INSERT INTO rhs (g, id, v) VALUES
		('1','a','100'), ('4','c','400'), ('4','x','401'), ('7','d','700'), ('9','z','900')`)
	execOK(t, s, `INSERT INTO skew (g, id, v) VALUES ('1','a','1'), ('4','c','4'), ('7','d','7')`)

	explainHas := func(q, want string) {
		t.Helper()
		plan := execOK(t, s, "EXPLAIN "+q)
		for _, r := range plan.Rows {
			if strings.Contains(r[0].Str, want) {
				return
			}
		}
		t.Fatalf("EXPLAIN %q missing %q: %+v", q, want, plan.Rows)
	}
	explainLacks := func(q, unwanted string) {
		t.Helper()
		plan := execOK(t, s, "EXPLAIN "+q)
		for _, r := range plan.Rows {
			if strings.Contains(r[0].Str, unwanted) {
				t.Fatalf("EXPLAIN %q unexpectedly has %q: %+v", q, unwanted, plan.Rows)
			}
		}
	}

	// Inner join on the partition key: partition-wise, matches the union.
	inner := `SELECT lhs.id, lhs.v, rhs.v FROM lhs JOIN rhs ON lhs.g = rhs.g AND lhs.id = rhs.id ORDER BY lhs.id`
	explainHas(inner, "partition-wise")
	got := execOK(t, s, inner).Rows
	if len(got) != 3 ||
		got[0][0].Str != "a" || got[0][1].Dec.String() != "10" || got[0][2].Dec.String() != "100" ||
		got[1][0].Str != "c" || got[1][1].Dec.String() != "40" || got[1][2].Dec.String() != "400" ||
		got[2][0].Str != "d" || got[2][1].Dec.String() != "70" || got[2][2].Dec.String() != "700" {
		t.Fatalf("partition-wise inner join = %+v", got)
	}

	// Pruned inner join: WHERE on the partition key drops every pair but one.
	pruned := `SELECT lhs.id FROM lhs JOIN rhs ON lhs.g = rhs.g AND lhs.id = rhs.id WHERE lhs.g = '7' ORDER BY lhs.id`
	explainHas(pruned, "partition-wise")
	if got := execOK(t, s, pruned).Rows; len(got) != 1 || got[0][0].Str != "d" {
		t.Fatalf("pruned partition-wise join = %+v", got)
	}

	// LEFT join: unmatched left rows survive with NULLs, still partition-wise.
	left := `SELECT lhs.id, rhs.v FROM lhs LEFT JOIN rhs ON lhs.g = rhs.g AND lhs.id = rhs.id ORDER BY lhs.id`
	explainHas(left, "partition-wise")
	lrows := execOK(t, s, left).Rows
	if len(lrows) != 5 {
		t.Fatalf("LEFT partition-wise join rows = %+v", lrows)
	}
	// Matches: a(1/a), c(4/c), d(7/d); unmatched: b, e.
	nulls := 0
	for _, r := range lrows {
		if r[1].Null {
			nulls++
		}
	}
	if nulls != 2 {
		t.Fatalf("LEFT join produced %d NULL right rows, want 2: %+v", nulls, lrows)
	}

	// Not partition-aligned: join key is not the partition key.
	explainLacks(`SELECT lhs.id FROM lhs JOIN rhs ON lhs.id = rhs.id`, "partition-wise")
	// Not partition-aligned: incompatible partition bounds.
	explainLacks(`SELECT lhs.id FROM lhs JOIN skew ON lhs.g = skew.g AND lhs.id = skew.id`, "partition-wise")
	skewJoin := execOK(t, s, `SELECT lhs.id FROM lhs JOIN skew ON lhs.g = skew.g AND lhs.id = skew.id ORDER BY lhs.id`).Rows
	if len(skewJoin) != 3 {
		t.Fatalf("non-aligned join = %+v", skewJoin)
	}

	// HASH scheme, multi-row groups.
	mkHash := func(name string) {
		execOK(t, s, `CREATE TABLE `+name+` (
			k STRING NOT NULL PRIMARY KEY,
			v DECIMAL(10,0) NOT NULL
		) PARTITION BY HASH (k) (
			PARTITION `+name+`0 MODULUS 4 REMAINDER 0,
			PARTITION `+name+`1 MODULUS 4 REMAINDER 1,
			PARTITION `+name+`2 MODULUS 4 REMAINDER 2,
			PARTITION `+name+`3 MODULUS 4 REMAINDER 3
		)`)
	}
	mkHash("hl")
	mkHash("hr")
	execOK(t, s, `INSERT INTO hl (k, v) VALUES ('p','1'),('q','2'),('r','3'),('s','4')`)
	execOK(t, s, `INSERT INTO hr (k, v) VALUES ('p','10'),('r','30'),('t','40')`)
	hj := `SELECT hl.k, hl.v, hr.v FROM hl JOIN hr ON hl.k = hr.k ORDER BY hl.k`
	explainHas(hj, "partition-wise")
	hrows := execOK(t, s, hj).Rows
	if len(hrows) != 2 || hrows[0][0].Str != "p" || hrows[1][0].Str != "r" ||
		hrows[0][2].Dec.String() != "10" || hrows[1][2].Dec.String() != "30" {
		t.Fatalf("HASH partition-wise join = %+v", hrows)
	}

	// Survives restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := execOK(t, db.Session(), inner).Rows; len(got) != 3 {
		t.Fatalf("reopened partition-wise join = %+v", got)
	}
}

// TestPartitionCrossPartitionUniqueSustainedConcurrentWrites is a sustained,
// randomized adversarial stress test for the cross-partition UNIQUE probe —
// the same shape of test (TestRebuildIndexOnlineConcurrentWrites,
// internal/executor/online_rebuild_test.go) that found a real
// data-integrity bug in REBUILD INDEX ... ONLINE (TODO.md log #93).
// TestPartitionCrossPartitionUniqueSerializedWriters above is a single,
// hand-arranged two-writer race with one deterministic window; this instead
// runs many goroutines for a while, forcing frequent UNIQUE collisions
// across four partitions via a small value pool, and checks the one
// invariant that actually matters: no duplicate ever gets committed,
// regardless of which partition it lands in or how the races interleave.
func TestPartitionCrossPartitionUniqueSustainedConcurrentWrites(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cpu_race (
		shard STRING NOT NULL,
		id DECIMAL(10,0) NOT NULL,
		email STRING NOT NULL,
		PRIMARY KEY (shard, id)
	) PARTITION BY LIST (shard) (
		PARTITION s0 VALUES IN ('0'),
		PARTITION s1 VALUES IN ('1'),
		PARTITION s2 VALUES IN ('2'),
		PARTITION s3 VALUES IN ('3')
	)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_cpu_email ON cpu_race (email)`)

	const idSpace = 400   // per-shard id range, so shard+id stays a stable PK per slot
	const emailPool = 24  // small on purpose: forces frequent cross-shard collisions
	var stop atomic.Bool
	var writes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(4)
	for w := 0; w < 4; w++ {
		go func(seed int) {
			defer wg.Done()
			ws := db.Session()
			r := uint64(seed*2654435761 + 1)
			tolerant := func(err error) bool {
				return nerr.HasCode(err, nerr.Serialization) || nerr.HasCode(err, nerr.Deadlock) ||
					nerr.HasCode(err, nerr.AlreadyExists) || nerr.HasCode(err, nerr.NotFound)
			}
			for !stop.Load() {
				r = r*6364136223846793005 + 1442695040888963407
				shard := int(r>>17) % 4
				id := int(r>>29) % idSpace
				email := int(r>>41) % emailPool
				var q string
				switch r >> 61 & 3 {
				case 0, 1: // insert (or reinsert into a slot another writer just deleted)
					q = fmt.Sprintf(`INSERT INTO cpu_race (shard, id, email) VALUES ('%d', %d, 'e%d@x')`, shard, id, email)
				case 2: // update email of an existing row, possibly colliding with another shard
					q = fmt.Sprintf(`UPDATE cpu_race SET email = 'e%d@x' WHERE shard = '%d' AND id = %d`, email, shard, id)
				default: // delete, freeing the slot and its email for reuse
					q = fmt.Sprintf(`DELETE FROM cpu_race WHERE shard = '%d' AND id = %d`, shard, id)
				}
				if _, err := ws.Exec(q); err != nil {
					if tolerant(err) {
						continue
					}
					t.Errorf("concurrent write %q: %v", q, err)
					return
				}
				writes.Add(1)
			}
		}(w + 1)
	}

	for writes.Load() < 800 {
		time.Sleep(time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()

	if err := db.LastReclaimError(); err != nil {
		t.Fatalf("reclaim error: %v", err)
	}

	plainCount := execOK(t, s, `SELECT COUNT(*) FROM cpu_race`)
	allRaw := execOK(t, s, `SELECT shard, id, email FROM cpu_race`)
	t.Logf("DEBUG plainCount=%v rawRowCount=%d", plainCount.Rows, len(allRaw.Rows))
	groupSum := execOK(t, s, `SELECT email, COUNT(*) AS c FROM cpu_race GROUP BY email`)
	sum := 0
	for _, r := range groupSum.Rows {
		sum += int(r[1].Dec.Coef.Int64())
	}
	t.Logf("DEBUG groupBy distinct emails=%d summedCount=%d", len(groupSum.Rows), sum)

	dupes := execOK(t, s, `SELECT email, COUNT(*) AS c FROM cpu_race GROUP BY email HAVING c > 1`)
	if len(dupes.Rows) != 0 {
		for _, d := range dupes.Rows {
			email := d[0].Str
			rows := execOK(t, s, fmt.Sprintf(`SELECT shard, id FROM cpu_race WHERE email = '%s'`, email))
			t.Logf("DEBUG duplicate email=%s rows=%+v", email, rows.Rows)
		}
		t.Fatalf("cross-partition UNIQUE admitted duplicates: %+v", dupes.Rows)
	}
	// Cross-check the same invariant against the index path directly, in
	// case the aggregate query above and the UNIQUE index disagree about
	// what's actually there.
	all := execOK(t, s, `SELECT shard, id, email FROM cpu_race`)
	seen := make(map[string][2]string, len(all.Rows))
	for _, row := range all.Rows {
		email := row[2].Str
		if prior, ok := seen[email]; ok {
			t.Fatalf("duplicate email %q: shard=%s id=%s and shard=%s id=%s", email, prior[0], prior[1], row[0].Str, row[1].Dec.String())
		}
		seen[email] = [2]string{row[0].Str, row[1].Dec.String()}
		lookup := execOK(t, s, fmt.Sprintf(`SELECT shard, id FROM cpu_race WHERE email = '%s'`, email))
		if len(lookup.Rows) != 1 || lookup.Rows[0][0].Str != row[0].Str || lookup.Rows[0][1].Dec.String() != row[1].Dec.String() {
			t.Fatalf("index lookup for email %q = %+v, want the single heap row shard=%s id=%s", email, lookup.Rows, row[0].Str, row[1].Dec.String())
		}
	}
}
