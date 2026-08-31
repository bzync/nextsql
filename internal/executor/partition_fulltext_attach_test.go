package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

// TestPartitionAttachDetachFulltextIndex verifies that ATTACH PARTITION and
// DETACH PARTITION transfer a partition-local FULLTEXT inverted-index root
// (postings plus the per-partition BM25 corpus stats) without copying, and that
// SEARCH keeps scoring every partition-local root as one logical corpus across
// the transfer and a restart.
func TestPartitionAttachDetachFulltextIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 96)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE ft_events (
		region STRING NOT NULL,
		id STRING NOT NULL,
		body TEXT NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `INSERT INTO ft_events (region, id, body) VALUES
		('us', '1', 'the quick brown cat sat on the mat'),
		('eu', '2', 'the cat and the dog are friends')`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_ft ON ft_events (body)`)

	// Standalone table with a matching FULLTEXT index and its own documents.
	execOK(t, s, `CREATE TABLE ft_apac (
		region STRING NOT NULL,
		id STRING NOT NULL,
		body TEXT NOT NULL,
		PRIMARY KEY (region, id)
	)`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_ft ON ft_apac (body)`)
	execOK(t, s, `INSERT INTO ft_apac (region, id, body) VALUES ('ap', '10', 'a cat prowls the alley at night')`)

	execOK(t, s, `ALTER TABLE ft_events ATTACH PARTITION ft_apac VALUES IN ('ap')`)
	if _, err := s.Exec(`SELECT * FROM ft_apac`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("attached source table remained visible: %v", err)
	}

	// The attached inverted index answers SEARCH without a rebuild.
	got := execOK(t, s, `SELECT region, id FROM ft_events SEARCH body FOR 'prowls'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "ap" || got.Rows[0][1].Str != "10" {
		t.Fatalf("attached fulltext root missing: %+v", got.Rows)
	}
	// The corpus is merged across every partition-local root.
	got = execOK(t, s, `SELECT id FROM ft_events SEARCH body FOR 'cat'`)
	if len(got.Rows) != 3 {
		t.Fatalf("merged fulltext corpus rows = %d: %+v", len(got.Rows), got.Rows)
	}
	// Post-attach DML routes to and maintains the attached partition-local root.
	execOK(t, s, `INSERT INTO ft_events (region, id, body) VALUES ('ap', '11', 'the cat naps in the sun')`)
	got = execOK(t, s, `SELECT id FROM ft_events SEARCH body FOR 'naps'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "11" {
		t.Fatalf("post-attach fulltext maintenance: %+v", got.Rows)
	}

	// DETACH returns the partition as a standalone table that keeps its index.
	execOK(t, s, `ALTER TABLE ft_events DETACH PARTITION ft_apac`)
	if got := execOK(t, s, `SELECT id FROM ft_events SEARCH body FOR 'cat'`).Rows; len(got) != 2 {
		t.Fatalf("detached docs remained searchable in parent: %+v", got)
	}
	got = execOK(t, s, `SELECT id FROM ft_apac SEARCH body FOR 'cat' ORDER BY id`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "10" || got.Rows[1][0].Str != "11" {
		t.Fatalf("detached table fulltext index missing: %+v", got.Rows)
	}
	// The detached table keeps maintaining its own inverted index.
	execOK(t, s, `INSERT INTO ft_apac (region, id, body) VALUES ('ap', '12', 'the cat wanders everywhere')`)
	if got := execOK(t, s, `SELECT id FROM ft_apac SEARCH body FOR 'everywhere'`).Rows; len(got) != 1 {
		t.Fatalf("detached fulltext maintenance: %+v", got)
	}

	// Restart: both trees keep their transferred roots.
	db.Eng.Kill()
	db, err = Open(path, keys, 96)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := execOK(t, s, `SELECT id FROM ft_apac SEARCH body FOR 'cat'`).Rows; len(got) != 3 {
		t.Fatalf("detached fulltext failed restart: %+v", got)
	}
	if got := execOK(t, s, `SELECT id FROM ft_events SEARCH body FOR 'dog'`).Rows; len(got) != 1 {
		t.Fatalf("parent fulltext failed restart after detach: %+v", got)
	}
}
