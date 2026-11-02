package executor

import (
	"context"
	"fmt"
	"path/filepath"
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
