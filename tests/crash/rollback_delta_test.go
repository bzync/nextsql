package crash

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Rollbacks, conflicts and page deltas under power loss.
//
// A rollback changes pages without logging them, so a page's content can stop
// matching the logged state its LSN names -- which a page delta relies on. The
// engine keeps a rolled-back transaction's pages unflushable until undo ends
// and discards their delta bases. This test drives that path hard: concurrent
// transfers that conflict and roll back constantly, deliberate rollbacks, and
// a checkpoint mid-stream, then power loss. Recovery must neither fail closed
// on a delta nor lose or invent money: every committed transfer moved money,
// every rolled-back one did not, so the total is exact.
func TestRollbacksWithPageDeltasSurvivePowerLoss(t *testing.T) {
	for round := 0; round < 3; round++ {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			rollbackDeltaRound(t, int64(round))
		})
	}
}

func rollbackDeltaRound(t *testing.T, seed int64) {
	const (
		accounts = 200
		start    = 1000
		sessions = 16
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := executor.Create(path, keys, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !db.Eng.WAL.PageDeltas() {
		t.Fatal("the database under test does not use page deltas")
	}
	setup := db.Session()
	if _, err := setup.Exec(`CREATE TABLE bank (id INT64 PRIMARY KEY, balance INT64 NOT NULL, note STRING)`); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(`CREATE INDEX bank_balance ON bank (balance)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < accounts; i++ {
		if _, err := setup.Exec(fmt.Sprintf(`INSERT INTO bank VALUES (%d, %d, 'account-%d-with-some-padding-text')`, i, start, i)); err != nil {
			t.Fatal(err)
		}
	}

	var (
		stop                      atomic.Bool
		commits, rollbacks, fails atomic.Int64
		wg                        sync.WaitGroup
	)
	ctx := context.Background()
	for w := 0; w < sessions; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed*100 + int64(w)))
			s := db.Session()
			for !stop.Load() {
				from, to := rng.Intn(accounts), rng.Intn(accounts)
				if from == to {
					continue
				}
				amt := int64(1 + rng.Intn(9))
				if _, err := s.Exec(`BEGIN`); err != nil {
					return
				}
				step := func(sql string, a, b int64) bool {
					_, err := s.ExecContext(ctx, sql, []executor.Param{{Value: types.Int64Value(a)}, {Value: types.Int64Value(b)}})
					return err == nil
				}
				ok := step(`UPDATE bank SET balance = balance - $1 WHERE id = $2`, amt, int64(from)) &&
					step(`UPDATE bank SET balance = balance + $1 WHERE id = $2`, amt, int64(to))
				if ok && rng.Intn(4) == 0 {
					// Deliberate rollback after both writes landed.
					_, _ = s.Exec(`ROLLBACK`)
					rollbacks.Add(1)
					continue
				}
				if !ok {
					_, _ = s.Exec(`ROLLBACK`)
					fails.Add(1)
					continue
				}
				if _, err := s.Exec(`COMMIT`); err != nil {
					_, _ = s.Exec(`ROLLBACK`)
					fails.Add(1)
					continue
				}
				commits.Add(1)
			}
		}(w)
	}
	time.Sleep(700 * time.Millisecond)
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatalf("checkpoint under load: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	db.Eng.Kill() // power loss mid-stream
	stop.Store(true)
	wg.Wait()
	if commits.Load() == 0 || rollbacks.Load() == 0 {
		t.Fatalf("load did not exercise both paths: %d commits, %d rollbacks", commits.Load(), rollbacks.Load())
	}

	reopened, err := executor.Open(path, keys, 128)
	if err != nil {
		t.Fatalf("recovery after rollbacks with page deltas: %v", err)
	}
	defer reopened.Close()
	s := reopened.Session()
	res, err := s.Exec(`SELECT SUM(balance), COUNT(*) FROM bank`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Rows[0][0].Dec.String(), fmt.Sprint(accounts*start); got != want || res.Rows[0][1].Dec.String() != fmt.Sprint(accounts) {
		t.Fatalf("after recovery the total is %s over %s accounts, want %s over %d", got, res.Rows[0][1].Dec.String(), want, accounts)
	}
	// The index must agree with the heap row by row.
	for i := 0; i < accounts; i += 17 {
		r, err := s.ExecContext(ctx, `SELECT balance FROM bank WHERE id = $1`, []executor.Param{{Value: types.Int64Value(int64(i))}})
		if err != nil || len(r.Rows) != 1 {
			t.Fatalf("account %d after recovery: %v %v", i, r, err)
		}
		bal := r.Rows[0][0].Int
		via, err := s.ExecContext(ctx, `SELECT id FROM bank WHERE balance = $1`, []executor.Param{{Value: types.Int64Value(bal)}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range via.Rows {
			if row[0].Int == int64(i) {
				found = true
			}
		}
		if !found {
			t.Fatalf("index lookup by balance %d does not find account %d", bal, i)
		}
	}
	t.Logf("%d commits, %d deliberate rollbacks, %d conflict rollbacks", commits.Load(), rollbacks.Load(), fails.Load())
}
