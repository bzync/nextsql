package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The task runtime opens and aborts a write transaction on every idle poll.
// Those aborts must not disturb the allocator under a running migration: once
// a DROP had put pages on the freelist, an abort handed a page the migration
// had just taken out a second time, and CREATE TABLE failed with "page already
// cached".
func TestIdleTaskPollDuringDDLKeepsPagesUnique(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 256)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	var polls sync.WaitGroup
	polls.Add(1)
	go func() {
		defer polls.Done()
		for ctx.Err() == nil {
			_, _ = db.DispatchDueSchedules(ctx, time.Now(), 16)
		}
	}()
	defer func() {
		cancel()
		polls.Wait()
	}()

	s := db.Session()
	for i := 0; i < 150; i++ {
		for _, stmt := range []string{
			fmt.Sprintf(`CREATE TABLE keep_%d (id UUID PRIMARY KEY, body TEXT)`, i),
			fmt.Sprintf(`CREATE TABLE scratch_%d (id UUID PRIMARY KEY, body TEXT)`, i),
			fmt.Sprintf(`CREATE INDEX keep_%d_body ON keep_%d (body)`, i, i),
			fmt.Sprintf(`DROP TABLE scratch_%d`, i),
		} {
			if _, err := s.Exec(stmt); err != nil {
				t.Fatalf("iteration %d: %s: %v", i, stmt, err)
			}
		}
	}
	cancel()
	polls.Wait()

	orphans, err := db.OrphanPages()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("%d orphaned pages after the run: %v", len(orphans), orphans)
	}
}

// An idle poll has nothing to write. It runs several times a second on every
// server, so WAL records from it would grow the log and wake the group commit.
func TestIdleTaskPollWritesNoWAL(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	before := db.Eng.WAL.NextLSN()
	for i := 0; i < 50; i++ {
		if _, err := db.DispatchDueSchedules(ctx, now, 16); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ClaimDueTasks(ctx, now, 16); err != nil {
			t.Fatal(err)
		}
		if _, err := db.PurgeTaskHistory(ctx, now, 16); err != nil {
			t.Fatal(err)
		}
	}
	if after := db.Eng.WAL.NextLSN(); after != before {
		t.Fatalf("idle polls wrote %d WAL records", after-before)
	}
}

// A rolled-back CREATE TABLE owns pages nothing else can reach. Removing the
// allocator reload from rollback must not turn them into a permanent leak.
func TestRollbackOfCreateTableLeavesNoOrphans(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	body := strings.Repeat("x", 600)
	execOK(t, s, `CREATE TABLE seed (id STRING PRIMARY KEY)`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE TABLE doomed (id STRING PRIMARY KEY, body STRING)`)
	execOK(t, s, `CREATE INDEX doomed_body ON doomed (body)`)
	for i := 0; i < 100; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO doomed (id, body) VALUES ('%04d', '%s')`, i, body))
	}
	execOK(t, s, `ROLLBACK`)
	execOK(t, s, `INSERT INTO seed (id) VALUES ('next commit drains the reclaim queue')`)
	orphans, err := db.OrphanPages()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("%d pages leaked by the rollback: %v", len(orphans), orphans)
	}
}

// Rollback keeps splits, so a rolled-back insert into an existing table leaves
// a valid tree, and later writes reuse its pages rather than fighting them.
func TestRollbackAfterSplitsLeavesValidTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	body := strings.Repeat("x", 600)
	execOK(t, s, `CREATE TABLE free_source (id STRING PRIMARY KEY, body STRING)`)
	for i := 0; i < 300; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO free_source (id, body) VALUES ('%04d', '%s')`, i, body))
	}
	execOK(t, s, `DROP TABLE free_source`)
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, body STRING)`)
	for round := 0; round < 3; round++ {
		execOK(t, s, `BEGIN`)
		for i := 0; i < 100; i++ {
			execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, body) VALUES ('r%d-%04d', '%s')`, round, i, body))
		}
		execOK(t, s, `ROLLBACK`)
		for i := 0; i < 50; i++ {
			execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, body) VALUES ('k%d-%04d', '%s')`, round, i, body))
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rep, err := db.CheckPageOwnership()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Clean() {
		t.Fatalf("damage after rollbacks: %+v", rep)
	}
	res, err := db.Session().Exec(`SELECT id FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 150 {
		t.Fatalf("read %d rows, want 150", len(res.Rows))
	}
}

// DETACH puts committed trees in the transaction's name maps. Rolling back must
// free only the trees the transaction created.
func TestRollbackFreesOnlyTreesTheTransactionCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE plain (id STRING PRIMARY KEY, body STRING)`)
	execOK(t, s, `CREATE INDEX plain_body ON plain (body)`)
	execOK(t, s, `INSERT INTO plain (id, body) VALUES ('1', 'a'), ('2', 'b')`)
	execOK(t, s, `CREATE TABLE parts (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `INSERT INTO parts (region, id) VALUES ('us', '1'), ('eu', '2')`)

	execOK(t, s, `BEGIN`)
	execOK(t, s, `ALTER TABLE parts DETACH PARTITION europe`)
	execOK(t, s, `ROLLBACK`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE INDEX plain_id_body ON plain (id, body)`)
	execOK(t, s, `ROLLBACK`)
	// A statement that fails on its own, with no explicit transaction.
	execOK(t, s, `INSERT INTO plain (id, body) VALUES ('3', 'dup'), ('4', 'dup')`)
	if _, err := s.Exec(`CREATE UNIQUE INDEX plain_unique ON plain (body)`); err == nil {
		t.Fatal("unique index over duplicate values was accepted")
	}
	execOK(t, s, `DELETE FROM plain WHERE id = '4'`)
	execOK(t, s, `DELETE FROM plain WHERE id = '3'`)

	verify := func(when string, db *DB) {
		t.Helper()
		s := db.Session()
		for sql, want := range map[string]int{
			`SELECT id FROM plain`:                  2,
			`SELECT id FROM plain WHERE body = 'a'`: 1,
			`SELECT id FROM parts`:                  2,
		} {
			res, err := s.Exec(sql)
			if err != nil {
				t.Fatalf("%s: %s: %v", when, sql, err)
			}
			if len(res.Rows) != want {
				t.Fatalf("%s: %s read %d rows, want %d", when, sql, len(res.Rows), want)
			}
		}
		orphans, err := db.OrphanPages()
		if err != nil {
			t.Fatal(err)
		}
		if len(orphans) != 0 {
			t.Fatalf("%s: orphans %v", when, orphans)
		}
		rep, err := db.CheckPageOwnership()
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Clean() {
			t.Fatalf("%s: damage %+v", when, rep)
		}
	}
	verify("live", db)
	// Reuse the reclaimed pages, so a wrongly freed one would overwrite data.
	body := strings.Repeat("x", 600)
	execOK(t, s, `CREATE TABLE reuse (id STRING PRIMARY KEY, body STRING)`)
	for i := 0; i < 60; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO reuse (id, body) VALUES ('%04d', '%s')`, i, body))
	}
	verify("after reuse", db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	verify("after restart", db)
}
