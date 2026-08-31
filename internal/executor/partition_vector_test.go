package executor

import (
	"path/filepath"
	"testing"
)

// TestPartitionLocalVectorIndex exercises a partition-local HNSW index through
// create, cross-partition DML, ADD PARTITION, blocking REBUILD, restart, and
// DROP with per-partition root reclamation. NEAREST searches every
// partition-local graph and merges by distance so partitioning never changes
// recall or ranking.
func TestPartitionLocalVectorIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE pv (
		region STRING NOT NULL,
		id STRING NOT NULL,
		name STRING NOT NULL,
		emb VECTOR<F32,3> NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `INSERT INTO pv (region, id, name, emb) VALUES
		('us', '1', 'x', (1, 0, 0)),
		('us', '2', 'y', (0, 1, 0)),
		('eu', '3', 'z', (0, 0, 1)),
		('eu', '4', 'w', (0.9, 0.1, 0))`)
	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON pv (emb) USING HNSW`)

	tab, ok := db.Cat.Get("pv")
	if !ok || len(tab.Indexes) != 1 || !tab.Indexes[0].Vector || tab.Indexes[0].Meta != 0 {
		t.Fatalf("logical vector index metadata: %+v", tab)
	}
	for _, part := range tab.Partitioning.Partitions {
		if len(part.Indexes) != 1 || part.Indexes[0].Meta == 0 {
			t.Fatalf("partition-local HNSW root missing: %+v", part)
		}
	}

	plan := execOK(t, s, `EXPLAIN SELECT id FROM pv NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if !explainHas(plan, "Nearest") || !explainHas(plan, "ix_emb") {
		t.Fatalf("partition-local vector plan: %+v", explainOps(plan))
	}

	// Top-2 across partitions: exact match in americas, then the near vector in europe.
	got := execOK(t, s, `SELECT region, id FROM pv NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if len(got.Rows) != 2 ||
		got.Rows[0][0].Str != "us" || got.Rows[0][1].Str != "1" ||
		got.Rows[1][0].Str != "eu" || got.Rows[1][1].Str != "4" {
		t.Fatalf("cross-partition NEAREST top-2: %+v", got.Rows)
	}

	// Heap-fetch path (non-covering projection).
	got = execOK(t, s, `SELECT name FROM pv NEAREST emb TO (0, 1, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "y" {
		t.Fatalf("NEAREST heap fetch: %+v", got.Rows)
	}

	// Insert maintains the partition-local graph.
	execOK(t, s, `INSERT INTO pv (region, id, name, emb) VALUES ('eu', '5', 'q', (0.98, 0.02, 0))`)
	got = execOK(t, s, `SELECT region, id FROM pv NEAREST emb TO (0.97, 0.03, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "eu" || got.Rows[0][1].Str != "5" {
		t.Fatalf("insert graph maintenance: %+v", got.Rows)
	}

	// Cross-partition move carries the vector between partition graphs.
	execOK(t, s, `UPDATE pv SET region = 'eu' WHERE region = 'us' AND id = '2'`)
	got = execOK(t, s, `SELECT region, id FROM pv NEAREST emb TO (0, 1, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "eu" || got.Rows[0][1].Str != "2" {
		t.Fatalf("cross-partition vector move: %+v", got.Rows)
	}

	// Delete removes the vector from search.
	execOK(t, s, `DELETE FROM pv WHERE region = 'eu' AND id = '3'`)
	got = execOK(t, s, `SELECT region, id FROM pv NEAREST emb TO (0, 0, 1) LIMIT 1`)
	if len(got.Rows) != 1 || (got.Rows[0][0].Str == "eu" && got.Rows[0][1].Str == "3") {
		t.Fatalf("delete graph maintenance still returns z: %+v", got.Rows)
	}

	// ADD PARTITION then index a fresh partition.
	execOK(t, s, `ALTER TABLE pv ADD PARTITION asia VALUES IN ('ap')`)
	execOK(t, s, `INSERT INTO pv (region, id, name, emb) VALUES ('ap', '6', 'c', (0.5, 0.5, 0.5))`)
	got = execOK(t, s, `SELECT region, id FROM pv NEAREST emb TO (0.5, 0.5, 0.5) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "ap" || got.Rows[0][1].Str != "6" {
		t.Fatalf("vector index on added partition: %+v", got.Rows)
	}

	// Blocking rebuild reconstructs every partition-local graph.
	execOK(t, s, `REBUILD INDEX ix_emb`)
	got = execOK(t, s, `SELECT region, id FROM pv NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "us" || got.Rows[0][1].Str != "1" {
		t.Fatalf("rebuilt partition vector index: %+v", got.Rows)
	}

	// Restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got = execOK(t, s, `SELECT region, id FROM pv NEAREST emb TO (0.5, 0.5, 0.5) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "ap" || got.Rows[0][1].Str != "6" {
		t.Fatalf("reopened partition vector search: %+v", got.Rows)
	}

	// DROP reclaims every partition-local root; NEAREST falls back to a
	// partition-spanning flat scan with identical top results.
	tab, _ = db.Cat.Get("pv")
	var partIDs []uint32
	for _, part := range tab.Partitioning.Partitions {
		partIDs = append(partIDs, part.ID)
	}
	execOK(t, s, `DROP INDEX ix_emb`)
	for _, id := range partIDs {
		if _, err := db.partitionIndex("pv", id, "ix_emb"); err == nil {
			t.Fatalf("partition-local HNSW root remains for partition %d", id)
		}
	}
	plan = execOK(t, s, `EXPLAIN SELECT id FROM pv NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if !explainHas(plan, "Nearest") || !explainHas(plan, "flat") {
		t.Fatalf("post-drop flat plan: %+v", explainOps(plan))
	}
	got = execOK(t, s, `SELECT region, id FROM pv NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "us" || got.Rows[0][1].Str != "1" {
		t.Fatalf("post-drop flat NEAREST: %+v", got.Rows)
	}
}

// TestPartitionFlatNearestNoIndex pins that vector payloads are stored per
// partition and flat NEAREST spans every partition even without an index.
func TestPartitionFlatNearestNoIndex(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE pv2 (
		region STRING NOT NULL,
		id STRING NOT NULL,
		emb VECTOR<F32,3> NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION a VALUES IN ('a'),
		PARTITION b VALUES IN ('b')
	)`)
	execOK(t, s, `INSERT INTO pv2 (region, id, emb) VALUES
		('a', '1', (1, 0, 0)),
		('b', '2', (0, 1, 0)),
		('b', '3', (0.9, 0.1, 0))`)
	got := execOK(t, s, `SELECT region, id FROM pv2 NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if len(got.Rows) != 2 ||
		got.Rows[0][0].Str != "a" || got.Rows[0][1].Str != "1" ||
		got.Rows[1][0].Str != "b" || got.Rows[1][1].Str != "3" {
		t.Fatalf("partition-spanning flat NEAREST: %+v", got.Rows)
	}
}

// TestPartitionPruningAwareNearest pins that a residual predicate constraining
// the partition key narrows both indexed and flat NEAREST to the surviving
// partition graphs: EXPLAIN reports the pruned set and results never include
// rows the predicate ruled out, even when a closer neighbour lives in a pruned
// partition.
func TestPartitionPruningAwareNearest(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE pv3 (
		region STRING NOT NULL,
		id STRING NOT NULL,
		emb VECTOR<F32,3> NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu'),
		PARTITION asia VALUES IN ('ap')
	)`)
	execOK(t, s, `INSERT INTO pv3 (region, id, emb) VALUES
		('us', '1', (0.8, 0.2, 0)),
		('us', '2', (0, 1, 0)),
		('eu', '3', (1, 0, 0)),
		('ap', '4', (0.99, 0.01, 0))`)

	// Flat (no vector index): WHERE on the partition key prunes to one partition.
	plan := execOK(t, s, `EXPLAIN SELECT region, id FROM pv3 WHERE region = 'us' NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if !explainHas(plan, "partitions=[americas]") {
		t.Fatalf("flat NEAREST partition prune not in EXPLAIN: %+v", explainOps(plan))
	}
	got := execOK(t, s, `SELECT region, id FROM pv3 WHERE region = 'us' NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if len(got.Rows) != 2 ||
		got.Rows[0][0].Str != "us" || got.Rows[0][1].Str != "1" ||
		got.Rows[1][0].Str != "us" || got.Rows[1][1].Str != "2" {
		t.Fatalf("pruned flat NEAREST leaked other partitions: %+v", got.Rows)
	}

	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON pv3 (emb) USING HNSW`)

	// Indexed: same prune, and the exact match in europe must not appear.
	plan = execOK(t, s, `EXPLAIN SELECT region, id FROM pv3 WHERE (region = 'us' OR region = 'ap') NEAREST emb TO (1, 0, 0) LIMIT 3`)
	if !explainHas(plan, "partitions=[") || explainHas(plan, "europe") {
		t.Fatalf("indexed NEAREST partition prune not in EXPLAIN: %+v", explainOps(plan))
	}
	got = execOK(t, s, `SELECT region, id FROM pv3 WHERE (region = 'us' OR region = 'ap') NEAREST emb TO (1, 0, 0) LIMIT 3`)
	if len(got.Rows) != 3 {
		t.Fatalf("pruned indexed NEAREST row count: %+v", got.Rows)
	}
	for _, r := range got.Rows {
		if r[0].Str == "eu" {
			t.Fatalf("pruned indexed NEAREST returned a europe row: %+v", got.Rows)
		}
	}
	if got.Rows[0][0].Str != "ap" || got.Rows[0][1].Str != "4" {
		t.Fatalf("pruned indexed NEAREST ranking: %+v", got.Rows)
	}

	// No predicate on the partition key: every partition is still searched and
	// the europe exact match wins.
	got = execOK(t, s, `SELECT region, id FROM pv3 NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "eu" || got.Rows[0][1].Str != "3" {
		t.Fatalf("unpruned NEAREST should span all partitions: %+v", got.Rows)
	}
}

// TestPartitionPruningAwareHybridCandidates pins that a residual predicate
// constraining the partition key narrows hybrid SEARCH+NEAREST candidate
// generation to the surviving partition-local graphs and heaps: EXPLAIN reports
// the pruned set on the Candidates node and the fused result never includes a
// row the predicate ruled out, even when a pruned partition holds both a closer
// vector and a stronger text match.
func TestPartitionPruningAwareHybridCandidates(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE ph (
		region STRING NOT NULL,
		id STRING NOT NULL,
		body TEXT NOT NULL,
		emb VECTOR<F32,3> NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu'),
		PARTITION asia VALUES IN ('ap')
	)`)
	execOK(t, s, `INSERT INTO ph (region, id, body, emb) VALUES
		('us', '1', 'cat sat on the mat', (0.8, 0.2, 0)),
		('us', '2', 'a cat and a dog', (0, 1, 0)),
		('eu', '3', 'cat cat cat cat', (1, 0, 0)),
		('ap', '4', 'the cat', (0.6, 0.4, 0))`)
	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON ph (emb) USING HNSW`)

	// ann-filter hybrid: the residual on the partition key must prune the
	// partition-local HNSW graphs that are opened and searched. Europe holds
	// both the exact vector and the strongest BM25 doc; it must not appear.
	plan := execOK(t, s, `EXPLAIN SELECT region, id FROM ph
		WHERE region = 'us'
		SEARCH body FOR 'cat' NEAREST emb TO (1, 0, 0) LIMIT 3`)
	if !explainHas(plan, "Candidates") || !explainHas(plan, "partitions=[americas]") {
		t.Fatalf("hybrid Candidates partition prune not in EXPLAIN: %+v", explainOps(plan))
	}
	if explainHas(plan, "europe") || explainHas(plan, "asia") {
		t.Fatalf("hybrid EXPLAIN retained pruned partitions: %+v", explainOps(plan))
	}
	got := execOK(t, s, `SELECT region, id FROM ph
		WHERE region = 'us'
		SEARCH body FOR 'cat' NEAREST emb TO (1, 0, 0) LIMIT 3`)
	if len(got.Rows) != 2 {
		t.Fatalf("pruned hybrid row count: %+v", got.Rows)
	}
	for _, r := range got.Rows {
		if r[0].Str != "us" {
			t.Fatalf("pruned hybrid leaked partition %q: %+v", r[0].Str, got.Rows)
		}
	}

	// Multi-partition OR prune: europe still excluded.
	got = execOK(t, s, `SELECT region, id FROM ph
		WHERE (region = 'us' OR region = 'ap')
		SEARCH body FOR 'cat' NEAREST emb TO (1, 0, 0) LIMIT 4`)
	if len(got.Rows) != 3 {
		t.Fatalf("OR-pruned hybrid row count: %+v", got.Rows)
	}
	for _, r := range got.Rows {
		if r[0].Str == "eu" {
			t.Fatalf("OR-pruned hybrid returned a europe row: %+v", got.Rows)
		}
	}

	// No predicate on the partition key: every partition is searched and the
	// europe row (exact vector + strongest text) wins.
	got = execOK(t, s, `SELECT region, id FROM ph
		SEARCH body FOR 'cat' NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "eu" || got.Rows[0][1].Str != "3" {
		t.Fatalf("unpruned hybrid should span all partitions: %+v", got.Rows)
	}
}
