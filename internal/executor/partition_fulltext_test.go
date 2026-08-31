package executor

import (
	"path/filepath"
	"testing"
)

// TestPartitionLocalFulltextIndex exercises a partition-local FULLTEXT index
// through create, cross-partition DML, ADD PARTITION, blocking REBUILD, restart,
// and DROP with per-partition root reclamation. BM25 is scored over every
// partition-local root as a single logical corpus.
func TestPartitionLocalFulltextIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE partition_docs (
		region STRING NOT NULL,
		id STRING NOT NULL,
		body TEXT NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `INSERT INTO partition_docs (region, id, body) VALUES
		('us', '1', 'the quick brown cat sat on the mat'),
		('us', '2', 'a loyal dog guards the yard'),
		('eu', '3', 'the cat and the dog are friends')`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_body ON partition_docs (body)`)

	tab, ok := db.Cat.Get("partition_docs")
	if !ok || len(tab.Indexes) != 1 || tab.Indexes[0].Meta != 0 {
		t.Fatalf("logical fulltext index metadata: %+v", tab)
	}
	for _, part := range tab.Partitioning.Partitions {
		if len(part.Indexes) != 1 || part.Indexes[0].Meta == 0 {
			t.Fatalf("partition-local fulltext root missing: %+v", part)
		}
	}

	plan := execOK(t, s, `EXPLAIN SELECT id FROM partition_docs SEARCH body FOR 'cat'`)
	if !explainHas(plan, "Search") || !explainHas(plan, "ix_body") {
		t.Fatalf("partition-local fulltext plan: %+v", explainOps(plan))
	}

	got := execOK(t, s, `SELECT region, id FROM partition_docs SEARCH body FOR 'cat'`)
	if len(got.Rows) != 2 {
		t.Fatalf("cross-partition SEARCH rows = %d: %+v", len(got.Rows), got.Rows)
	}

	// Phrase search spanning only one partition.
	got = execOK(t, s, `SELECT id FROM partition_docs SEARCH body FOR '"brown cat"'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "1" {
		t.Fatalf("partition-local phrase search: %+v", got.Rows)
	}

	// Insert maintains the partition-local inverted index.
	execOK(t, s, `INSERT INTO partition_docs (region, id, body) VALUES ('eu', '4', 'the cat naps in the sun')`)
	got = execOK(t, s, `SELECT id FROM partition_docs SEARCH body FOR 'cat naps'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "4" {
		t.Fatalf("insert fulltext maintenance: %+v", got.Rows)
	}

	// Cross-partition move must carry the document and its corpus stats.
	execOK(t, s, `UPDATE partition_docs SET region = 'eu' WHERE region = 'us' AND id = '1'`)
	got = execOK(t, s, `SELECT region FROM partition_docs SEARCH body FOR '"brown cat"'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "eu" {
		t.Fatalf("cross-partition fulltext move: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id FROM partition_docs SEARCH body FOR 'cat'`)
	if len(got.Rows) != 3 {
		t.Fatalf("SEARCH after move rows = %d: %+v", len(got.Rows), got.Rows)
	}

	// Delete removes the document from search.
	execOK(t, s, `DELETE FROM partition_docs WHERE region = 'eu' AND id = '4'`)
	got = execOK(t, s, `SELECT id FROM partition_docs SEARCH body FOR 'naps'`)
	if len(got.Rows) != 0 {
		t.Fatalf("delete fulltext maintenance: %+v", got.Rows)
	}

	// ADD PARTITION then index a fresh partition.
	execOK(t, s, `ALTER TABLE partition_docs ADD PARTITION asia VALUES IN ('ap')`)
	execOK(t, s, `INSERT INTO partition_docs (region, id, body) VALUES ('ap', '5', 'a cat prowls the alley at night')`)
	got = execOK(t, s, `SELECT id FROM partition_docs SEARCH body FOR 'prowls'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "5" {
		t.Fatalf("fulltext on added partition: %+v", got.Rows)
	}

	// Blocking rebuild reconstructs every partition-local root.
	execOK(t, s, `REBUILD INDEX ix_body`)
	got = execOK(t, s, `SELECT id FROM partition_docs SEARCH body FOR 'cat'`)
	if len(got.Rows) != 3 {
		t.Fatalf("rebuilt partition fulltext rows = %d: %+v", len(got.Rows), got.Rows)
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
	got = execOK(t, s, `SELECT id FROM partition_docs SEARCH body FOR 'dog'`)
	if len(got.Rows) != 2 {
		t.Fatalf("reopened partition fulltext rows = %d: %+v", len(got.Rows), got.Rows)
	}

	// DROP reclaims every partition-local root.
	tab, _ = db.Cat.Get("partition_docs")
	var partIDs []uint32
	for _, part := range tab.Partitioning.Partitions {
		partIDs = append(partIDs, part.ID)
	}
	execOK(t, s, `DROP INDEX ix_body`)
	for _, id := range partIDs {
		if _, err := db.partitionIndex("partition_docs", id, "ix_body"); err == nil {
			t.Fatalf("partition-local fulltext root remains for partition %d", id)
		}
	}
	// SEARCH still works after the index is dropped, falling back to a
	// partition-spanning sequential scan with identical results.
	got = execOK(t, s, `SELECT id FROM partition_docs SEARCH body FOR 'cat'`)
	if len(got.Rows) != 3 {
		t.Fatalf("post-drop fulltext scan rows = %d: %+v", len(got.Rows), got.Rows)
	}
}
