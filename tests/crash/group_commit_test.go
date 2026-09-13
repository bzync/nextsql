package crash

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Group commit: a commit waits for durability without holding the engine
// mutex, so commits arriving during one fsync are made durable by the next.
// That must not weaken the one property a commit exists for: once a client is
// told a commit succeeded, the row survives power loss. This test commits from
// many sessions at once, cuts power in the middle of the stream (the WAL's
// unsynced tail is discarded, as a real crash discards it), reopens through
// recovery, and requires every acknowledged row -- and no row that was never
// issued -- to be present, with the secondary index agreeing with the heap.
func TestConcurrentCommitsSurvivePowerLoss(t *testing.T) {
	for round := 0; round < 6; round++ {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			concurrentCommitCrashRound(t, 32, time.Duration(300+round*250)*time.Millisecond)
		})
	}
}

func concurrentCommitCrashRound(t *testing.T, sessions int, runFor time.Duration) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Create(path, keys, 256)
	if err != nil {
		t.Fatal(err)
	}
	setup := db.Session()
	for _, q := range []string{
		`CREATE TABLE gc (id INT64 PRIMARY KEY, owner INT64 NOT NULL, note STRING)`,
		`CREATE INDEX gc_owner ON gc (owner)`,
	} {
		if _, err := setup.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	var (
		issued atomic.Int64
		mu     sync.Mutex
		acked  = make(map[int64]int64)
		stop   atomic.Bool
		wg     sync.WaitGroup
	)
	for w := 0; w < sessions; w++ {
		wg.Add(1)
		go func(owner int64) {
			defer wg.Done()
			s := db.Session()
			for !stop.Load() {
				id := issued.Add(1)
				_, err := s.ExecContext(context.Background(), `INSERT INTO gc (id, owner, note) VALUES ($1, $2, $3)`,
					[]executor.Param{{Value: types.Int64Value(id)}, {Value: types.Int64Value(owner)}, {Value: types.StringValue("committed")}})
				if err != nil {
					return // the crash ends this session; an unacknowledged insert may or may not survive
				}
				mu.Lock()
				acked[id] = owner
				mu.Unlock()
			}
		}(int64(w))
	}
	time.Sleep(runFor)
	// Power loss while commits are in flight, then let the sessions notice.
	db.Eng.Kill()
	stop.Store(true)
	wg.Wait()

	mu.Lock()
	ackedCopy := make(map[int64]int64, len(acked))
	for k, v := range acked {
		ackedCopy[k] = v
	}
	mu.Unlock()
	if len(ackedCopy) == 0 {
		t.Fatal("no commit was acknowledged before the crash; the test exercised nothing")
	}

	reopened, err := executor.Open(path, keys, 256)
	if err != nil {
		t.Fatalf("reopen after power loss: %v", err)
	}
	defer reopened.Close()
	s := reopened.Session()
	res, err := s.Exec(`SELECT id, owner FROM gc`)
	if err != nil {
		t.Fatal(err)
	}
	present := make(map[int64]int64, len(res.Rows))
	maxIssued := issued.Load()
	for _, r := range res.Rows {
		id := r[0].Int
		if id < 1 || id > maxIssued {
			t.Fatalf("recovered a row with id %d that was never issued (max %d)", id, maxIssued)
		}
		present[id] = r[1].Int
	}
	for id, owner := range ackedCopy {
		got, ok := present[id]
		if !ok {
			t.Fatalf("acknowledged commit id=%d was lost in the crash (%d acknowledged, %d recovered)", id, len(ackedCopy), len(present))
		}
		if got != owner {
			t.Fatalf("acknowledged commit id=%d recovered with owner %d, want %d", id, got, owner)
		}
	}
	// The secondary index must describe exactly the recovered heap.
	var viaIndex int
	for owner := 0; owner < sessions; owner++ {
		r, err := s.ExecContext(context.Background(), `SELECT id FROM gc WHERE owner = $1`,
			[]executor.Param{{Value: types.Int64Value(int64(owner))}})
		if err != nil {
			t.Fatal(err)
		}
		viaIndex += len(r.Rows)
	}
	if viaIndex != len(present) {
		t.Fatalf("index lookups found %d rows, heap holds %d", viaIndex, len(present))
	}
	// And the recovered database accepts new writes.
	if _, err := s.Exec(fmt.Sprintf(`INSERT INTO gc (id, owner, note) VALUES (%d, 0, 'after')`, maxIssued+1)); err != nil {
		t.Fatalf("write after recovery: %v", err)
	}
	t.Logf("%d acknowledged, %d recovered (in-flight commits may land either way)", len(ackedCopy), len(present))
}
