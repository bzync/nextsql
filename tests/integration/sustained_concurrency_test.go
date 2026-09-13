package integration

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Sustained mixed load over TLS with an exact invariant. Writers move money
// between accounts in explicit transactions; readers sum every balance in a
// snapshot. Money is only moved, never created, so every sum a reader sees and
// the final total must equal the starting total exactly. A lost update, a dirty
// read, a torn or crossed response frame, or a session that stops answering
// breaks that equality instead of hiding in throughput noise. Only the
// conflicts a correct engine is allowed to report -- serialization, deadlock,
// conflict and lock-wait exhaustion -- are retried; anything else fails.
func TestSustainedMixedLoadKeepsExactInvariant(t *testing.T) {
	const (
		accounts = 64
		start    = 1000
		writers  = 32
		readers  = 16
		churners = 4
		runFor   = 4 * time.Second
	)
	addr, tlsCfg := startTLSServer(t)
	setup := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := setup.Exec(ctx, `CREATE TABLE bank (id INT64 PRIMARY KEY, balance INT64 NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < accounts; i++ {
		if _, err := setup.Exec(ctx, `INSERT INTO bank (id, balance) VALUES ($1, $2)`, types.Int64Value(int64(i)), types.Int64Value(start)); err != nil {
			t.Fatal(err)
		}
	}
	want := int64(accounts * start)
	cfg := burstConfig(addr, tlsCfg)
	goroutinesBefore := runtime.NumGoroutine()

	var (
		stop                      atomic.Bool
		transfers, retries, reads atomic.Int64
		failMu                    sync.Mutex
		failures                  []string
		wg                        sync.WaitGroup
	)
	fail := func(format string, args ...any) {
		failMu.Lock()
		if len(failures) < 10 {
			failures = append(failures, fmt.Sprintf(format, args...))
		}
		failMu.Unlock()
		stop.Store(true)
	}
	retryable := func(err error) bool {
		return nerr.HasCode(err, nerr.Serialization) || nerr.HasCode(err, nerr.Deadlock) ||
			nerr.HasCode(err, nerr.Conflict) || nerr.HasCode(err, nerr.Exhausted)
	}

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			conn, err := openWithin(cfg)
			if err != nil {
				fail("writer open: %v", err)
				return
			}
			defer conn.Close()
			for !stop.Load() {
				from, to := rng.Intn(accounts), rng.Intn(accounts)
				if from == to {
					continue
				}
				amount := int64(rng.Intn(20) + 1)
				err := transfer(ctx, conn, from, to, amount)
				switch {
				case err == nil:
					transfers.Add(1)
				case retryable(err):
					retries.Add(1)
					_, _ = conn.Exec(ctx, `ROLLBACK`)
				default:
					fail("transfer %d->%d: %v", from, to, err)
					return
				}
			}
		}(int64(w))
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := openWithin(cfg)
			if err != nil {
				fail("reader open: %v", err)
				return
			}
			defer conn.Close()
			for !stop.Load() {
				res, err := conn.Exec(ctx, `SELECT SUM(balance), COUNT(*) FROM bank`)
				if err != nil {
					if retryable(err) {
						continue
					}
					fail("reader sum: %v", err)
					return
				}
				if got := res.Rows[0][0].Dec.String(); got != fmt.Sprint(want) || res.Rows[0][1].Dec.String() != fmt.Sprint(accounts) {
					fail("a snapshot read saw total %s over %s accounts, want %d over %d", got, res.Rows[0][1].Dec.String(), want, accounts)
					return
				}
				reads.Add(1)
			}
		}()
	}
	// Connection churn alongside: sessions that open, do one read, and leave.
	for c := 0; c < churners; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				conn, err := openWithin(cfg)
				if err != nil {
					fail("churn open: %v", err)
					return
				}
				if _, err := conn.Exec(ctx, `SELECT balance FROM bank WHERE id = $1`, types.Int64Value(1)); err != nil && !retryable(err) {
					fail("churn read: %v", err)
				}
				_ = conn.Close()
			}
		}()
	}

	time.Sleep(runFor)
	stop.Store(true)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("clients did not finish within 60 s of being stopped: a session hung")
	}
	for _, f := range failures {
		t.Error(f)
	}
	if t.Failed() {
		return
	}

	res, err := setup.Exec(ctx, `SELECT SUM(balance) FROM bank`)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Rows[0][0].Dec.String(); got != fmt.Sprint(want) {
		t.Fatalf("final total %s, want %d: money was created or destroyed", got, want)
	}
	if transfers.Load() == 0 || reads.Load() == 0 {
		t.Fatalf("the load did nothing: %d transfers, %d reads", transfers.Load(), reads.Load())
	}
	// Client-side goroutines settle back (driver goroutines exit with their
	// connections); a leak here means sessions are not being torn down.
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > goroutinesBefore+32 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > goroutinesBefore+32 {
		t.Fatalf("%d goroutines after the load, %d before: sessions leaked", n, goroutinesBefore)
	}
	t.Logf("%d transfers (%d conflict retries), %d consistent snapshot reads", transfers.Load(), retries.Load(), reads.Load())
}

func transfer(ctx context.Context, conn *nextsql.Conn, from, to int, amount int64) error {
	if _, err := conn.Exec(ctx, `BEGIN`); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `UPDATE bank SET balance = balance - $1 WHERE id = $2`, types.Int64Value(amount), types.Int64Value(int64(from))); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `UPDATE bank SET balance = balance + $1 WHERE id = $2`, types.Int64Value(amount), types.Int64Value(int64(to))); err != nil {
		return err
	}
	_, err := conn.Exec(ctx, `COMMIT`)
	return err
}
