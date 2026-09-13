package integration

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Connection bursts: many clients arriving in the same instant rather than one
// after another. The sequential limit tests (TestPerUserConnectionLimit and its
// siblings) prove the counters are right when admissions are serialized by the
// test; they cannot see a counter that is read and incremented without holding
// the lock, a slot that is never released on an early-exit path, or two
// sessions whose frames interleave. Every burst here releases its clients from
// one barrier so the accept loop, the TLS handshakes and the per-user
// admission check all run concurrently, and every one bounds its own wait so a
// wedged server fails the test instead of hanging it.

const burstDialTimeout = 30 * time.Second

func burstConfig(addr string, tlsCfg *tls.Config) nextsql.Config {
	return nextsql.Config{Address: addr, Database: "production", User: "app", Password: "s3cret", TLS: tlsCfg}
}

// burst runs fn on n goroutines released together and waits for all of them.
func burst(n int, fn func(i int)) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			fn(i)
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
}

// openWithin opens a connection, failing rather than blocking past the bound.
func openWithin(cfg nextsql.Config) (*nextsql.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), burstDialTimeout)
	defer cancel()
	return nextsql.OpenContext(ctx, cfg)
}

// Every client in a burst below the session ceiling is admitted, authenticates,
// and runs its own statements without seeing another session's parameters or
// results. Each client writes a row carrying its own id and reads exactly that
// row back through a bound parameter, so frames crossed between sessions show
// up as a wrong row rather than passing silently.
func TestConnectionBurstIsolatesEverySession(t *testing.T) {
	const clients = 64 // below DefaultMaxSessions (128)
	addr, tlsCfg := startTLSServer(t)
	setup := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := setup.Exec(ctx, `CREATE TABLE burst_rows (id INT64 PRIMARY KEY, owner STRING NOT NULL, even BOOL NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, clients)
	burst(clients, func(i int) {
		conn, err := openWithin(burstConfig(addr, tlsCfg))
		if err != nil {
			errs <- fmt.Errorf("client %d open: %w", i, err)
			return
		}
		defer conn.Close()
		owner := fmt.Sprintf("client-%03d", i)
		if _, err := conn.Exec(ctx, `INSERT INTO burst_rows (id, owner, even) VALUES ($1, $2, $3)`,
			types.Int64Value(int64(i)), types.StringValue(owner), types.BoolValue(i%2 == 0)); err != nil {
			errs <- fmt.Errorf("client %d insert: %w", i, err)
			return
		}
		res, err := conn.Exec(ctx, `SELECT owner, even FROM burst_rows WHERE id = $1`, types.Int64Value(int64(i)))
		if err != nil {
			errs <- fmt.Errorf("client %d select: %w", i, err)
			return
		}
		if len(res.Rows) != 1 || res.Rows[0][0].Str != owner || res.Rows[0][1].Bool != (i%2 == 0) {
			errs <- fmt.Errorf("client %d read %+v, want its own row %s", i, res.Rows, owner)
		}
	})
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}

	res, err := setup.Exec(ctx, `SELECT COUNT(*) FROM burst_rows`)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Rows[0][0].Dec.String(); got != fmt.Sprint(clients) {
		t.Fatalf("burst stored %s rows, want %d", got, clients)
	}
	evens, err := setup.Exec(ctx, `SELECT COUNT(*) FROM burst_rows WHERE even`)
	if err != nil {
		t.Fatal(err)
	}
	if got := evens.Rows[0][0].Dec.String(); got != fmt.Sprint(clients/2) {
		t.Fatalf("burst stored %s even rows, want %d", got, clients/2)
	}
}

// A burst far over the per-user limit admits at most the limit, rejects every
// other client with an explicit Exhausted error (never a hang, never a silent
// admission past the limit), and leaks no slot: once the admitted clients
// leave, exactly the limit can be admitted again, again as a burst.
func TestConnectionBurstOverPerUserLimitFailsClosed(t *testing.T) {
	const (
		limit   = 8
		clients = 48
	)
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		lim := srv.Limits
		lim.MaxSessionsPerUser = limit
		srv.Limits = lim
	})
	cfg := burstConfig(addr, tlsCfg)

	var (
		mu       sync.Mutex
		admitted []*nextsql.Conn
		other    []error
	)
	var exhausted atomic.Int64
	// Every admitted client holds its connection until the whole burst has
	// resolved, so the limit is contended for the entire burst.
	burst(clients, func(i int) {
		conn, err := openWithin(cfg)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			admitted = append(admitted, conn)
		case nerr.HasCode(err, nerr.Exhausted):
			exhausted.Add(1)
		default:
			other = append(other, fmt.Errorf("client %d: %w", i, err))
		}
	})
	for _, err := range other {
		t.Errorf("rejected with something other than Exhausted: %v", err)
	}
	if len(admitted) > limit {
		t.Errorf("admitted %d connections for one user, limit is %d", len(admitted), limit)
	}
	if len(admitted) == 0 {
		t.Error("admitted no connection at all")
	}
	if got := int64(len(admitted)) + exhausted.Load() + int64(len(other)); got != clients {
		t.Errorf("accounted for %d outcomes, want %d", got, clients)
	}
	// Every admitted session is usable while the limit is saturated.
	for i, conn := range admitted {
		if _, err := conn.Exec(context.Background(), `SELECT 1`); err != nil {
			t.Errorf("admitted connection %d unusable under a saturated limit: %v", i, err)
		}
	}
	for _, conn := range admitted {
		_ = conn.Close()
	}
	if t.Failed() {
		return
	}
	assertBurstRecovers(t, cfg, limit)
}

// A burst over the global session ceiling is refused at accept, before TLS, so
// the server spends nothing on a client it cannot seat. Whatever the rejection
// looks like to the client, the ceiling is never exceeded, nothing hangs, and
// every slot comes back once the admitted clients leave.
func TestConnectionBurstOverSessionCeilingRecovers(t *testing.T) {
	const (
		ceiling = 6
		clients = 40
	)
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		lim := srv.Limits
		lim.MaxSessions = ceiling
		srv.Limits = lim
	})
	cfg := burstConfig(addr, tlsCfg)

	var (
		mu       sync.Mutex
		admitted []*nextsql.Conn
	)
	var refused atomic.Int64
	burst(clients, func(int) {
		began := time.Now()
		conn, err := openWithin(cfg)
		if err != nil {
			if time.Since(began) >= burstDialTimeout {
				t.Errorf("a refused client waited out its deadline instead of being refused: %v", err)
			}
			refused.Add(1)
			return
		}
		mu.Lock()
		admitted = append(admitted, conn)
		mu.Unlock()
	})
	if len(admitted) > ceiling {
		t.Errorf("admitted %d sessions, ceiling is %d", len(admitted), ceiling)
	}
	if len(admitted) == 0 {
		t.Error("admitted no session at all")
	}
	if got := int64(len(admitted)) + refused.Load(); got != clients {
		t.Errorf("accounted for %d outcomes, want %d", got, clients)
	}
	for _, conn := range admitted {
		_ = conn.Close()
	}
	if t.Failed() {
		return
	}
	assertBurstRecovers(t, cfg, ceiling)
}

// Clients that open a TCP connection and drop it -- before TLS, and halfway
// through it -- must give their slots back. A burst of them followed by a burst
// of real clients up to the ceiling proves no abandoned socket is still counted.
func TestConnectionBurstOfAbandonedSocketsReleasesSlots(t *testing.T) {
	const (
		ceiling   = 6
		abandoned = 64
	)
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		lim := srv.Limits
		lim.MaxSessions = ceiling
		srv.Limits = lim
	})
	burst(abandoned, func(i int) {
		raw, err := net.DialTimeout("tcp", addr, burstDialTimeout)
		if err != nil {
			return // refused at accept is a legitimate outcome for a burst over the ceiling
		}
		if i%2 == 1 {
			// Start a TLS handshake and walk away from it mid-flight.
			_ = raw.SetDeadline(time.Now().Add(50 * time.Millisecond))
			_ = tls.Client(raw, tlsCfg.Clone()).Handshake()
		}
		_ = raw.Close()
	})
	assertBurstRecovers(t, burstConfig(addr, tlsCfg), ceiling)
}

// assertBurstRecovers requires that n clients can be admitted together. The
// server releases a slot when its connection goroutine notices the close, which
// is asynchronous, so the whole burst is retried until a deadline rather than
// assumed to be instant.
func assertBurstRecovers(t *testing.T, cfg nextsql.Config, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for attempt := 1; ; attempt++ {
		conns := make([]*nextsql.Conn, n)
		errs := make([]error, n)
		burst(n, func(i int) { conns[i], errs[i] = openWithin(cfg) })
		var firstErr error
		for i, conn := range conns {
			if errs[i] != nil {
				if firstErr == nil {
					firstErr = errs[i]
				}
				continue
			}
			if _, err := conn.Exec(context.Background(), `SELECT 1`); err != nil && firstErr == nil {
				firstErr = err
			}
			_ = conn.Close()
		}
		if firstErr == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a burst of %d connections was still refused after %d attempts (a slot leaked?): %v", n, attempt, firstErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
