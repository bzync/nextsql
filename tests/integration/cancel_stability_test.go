package integration

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Cancelling a statement must leave its connection usable.
//
// The server used to interrupt a cancelled statement by pulling the connection
// deadline to "now". On a TLS connection that is only safe while the goroutine
// serving the statement is blocked in a read. When it lands during a write --
// a result batch the client has not finished reading -- crypto/tls abandons the
// record half-written and latches the error on the write side. The session goes
// on reading requests and running them but can never answer again: the client
// waits out the whole idle timeout, and the close alert the server finally
// sends lands after the half-written record, so the client reports
// `tls: bad record MAC`. That is how TestNativeSubscribeOverTLSAndPreparedCancellation
// failed intermittently.
//
// Kernel socket buffers cannot make that write block reliably (loopback
// buffers grow to megabytes, and a receive buffer below the loopback MSS stalls
// TCP itself), so gatedListener holds the server's bytes instead, after
// letting part of a write through, exactly as a full socket does.

// gatedListener wraps every accepted connection in a gatedConn.
type gatedListener struct {
	net.Listener
	mu    sync.Mutex
	conns []*gatedConn
}

func (l *gatedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	g := &gatedConn{Conn: c, wake: make(chan struct{})}
	l.mu.Lock()
	l.conns = append(l.conns, g)
	l.mu.Unlock()
	return g, nil
}

// holdExisting arms the gate on every connection accepted so far, so a
// connection opened afterwards (a driver's cancel side-channel) is unaffected.
// Each armed connection lets allowance more bytes through and then blocks.
func (l *gatedListener) holdExisting(allowance int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		c.arm(allowance)
	}
}

func (l *gatedListener) releaseAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		c.release()
	}
}

// gatedConn behaves like a connection whose peer has stopped reading once the
// gate is armed and its allowance is spent: a write sends what fits and then
// blocks until the gate is released or the write deadline passes, in which
// case it returns a timeout after a partial write -- what a real socket does.
type gatedConn struct {
	net.Conn
	mu        sync.Mutex
	armed     bool
	allowance int
	deadline  time.Time
	wake      chan struct{} // closed and replaced whenever state changes
}

func (c *gatedConn) arm(allowance int) {
	c.mu.Lock()
	c.armed, c.allowance = true, allowance
	c.broadcastLocked()
	c.mu.Unlock()
}

func (c *gatedConn) release() {
	c.mu.Lock()
	c.armed = false
	c.broadcastLocked()
	c.mu.Unlock()
}

func (c *gatedConn) broadcastLocked() {
	close(c.wake)
	c.wake = make(chan struct{})
}

func (c *gatedConn) SetDeadline(t time.Time) error {
	c.setWriteDeadline(t)
	return c.Conn.SetDeadline(t)
}

func (c *gatedConn) SetWriteDeadline(t time.Time) error {
	c.setWriteDeadline(t)
	return c.Conn.SetWriteDeadline(t)
}

func (c *gatedConn) setWriteDeadline(t time.Time) {
	c.mu.Lock()
	c.deadline = t
	c.broadcastLocked()
	c.mu.Unlock()
}

func (c *gatedConn) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		c.mu.Lock()
		if !c.armed {
			c.mu.Unlock()
			n, err := c.Conn.Write(p[written:])
			return written + n, err
		}
		if c.allowance > 0 {
			n := len(p) - written
			if n > c.allowance {
				n = c.allowance
			}
			c.allowance -= n
			c.mu.Unlock()
			m, err := c.Conn.Write(p[written : written+n])
			written += m
			if err != nil {
				return written, err
			}
			continue
		}
		deadline, wake := c.deadline, c.wake
		c.mu.Unlock()
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return written, os.ErrDeadlineExceeded
			}
			timer := time.NewTimer(d)
			defer timer.Stop()
			timeout = timer.C
		}
		select {
		case <-wake:
		case <-timeout:
			return written, os.ErrDeadlineExceeded
		}
	}
	return written, nil
}

func seedWideRows(t *testing.T, conn *nextsql.Conn, rows int) {
	t.Helper()
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `CREATE TABLE wide_rows (id INT64 PRIMARY KEY, body STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", 3<<10)
	for i := 0; i < rows; i += 50 {
		sql := `INSERT INTO wide_rows (id, body) VALUES `
		var params []types.Value
		for j := 0; j < 50 && i+j < rows; j++ {
			if j > 0 {
				sql += `, `
			}
			n := len(params)
			sql += "($" + itoaInt(n+1) + ", $" + itoaInt(n+2) + ")"
			params = append(params, types.Int64Value(int64(i+j)), types.StringValue(body))
		}
		if _, err := conn.Exec(ctx, sql, params...); err != nil {
			t.Fatal(err)
		}
	}
}

// The server is provably inside a write -- part of a result batch sent, the
// rest held -- when the cancel arrives. Once the gate opens, the client must
// read a well-formed end to the statement and the session must answer again.
func TestCancelDuringBlockedResultWriteKeepsConnectionUsable(t *testing.T) {
	gate := &gatedListener{}
	addr, tlsCfg := startTLSServerOn(t, func(ln net.Listener) net.Listener {
		gate.Listener = ln
		return gate
	})
	conn := openApp(t, addr, tlsCfg)
	const total = 2000
	seedWideRows(t, conn, total)

	// Let the RowDesc and the first ~64 KiB of the first ~770 KiB batch
	// through, then hold the server mid-write.
	gate.holdExisting(64 << 10)
	rows, err := conn.Query(context.Background(), `SELECT id, body FROM wide_rows ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the server is now blocked in Write
	if err := conn.Cancel(context.Background()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // the cancel has been applied server-side
	gate.releaseAll()

	drained := make(chan error, 1)
	go func() {
		for rows.Next() {
		}
		drained <- rows.Close()
	}()
	select {
	case err := <-drained:
		// Completing (the batch was already being written) and stopping with
		// Canceled are both legitimate; a transport failure is not.
		if err != nil && !nerr.HasCode(err, nerr.Canceled) {
			t.Fatalf("the cancelled statement's response was damaged: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the cancelled statement never finished: the server stopped answering on this connection")
	}
	assertConnUsable(t, conn, total)
}

// A cancel that arrives after its statement finished must not reach the next
// statement on the connection.
func TestLateCancelDoesNotReachTheNextStatement(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	seedWideRows(t, conn, 50)
	if err := conn.Cancel(context.Background()); err != nil {
		t.Fatalf("cancel with nothing running: %v", err)
	}
	assertConnUsable(t, conn, 50)
}

func assertConnUsable(t *testing.T, conn *nextsql.Conn, want int) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		res, err := conn.Exec(context.Background(), `SELECT COUNT(*) FROM wide_rows`)
		if err == nil && (len(res.Rows) != 1 || res.Rows[0][0].Dec.String() != itoaInt(want)) {
			err = nerr.New(nerr.Internal, "test", "unexpected count")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("connection unusable after cancel: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("connection hung after cancel")
	}
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// A context cancelled at the moment its statement finishes starts a cancel
// request on another goroutine that can reach the server only after the
// statement is over. The driver must not hand the connection to the next
// statement while that request is still in flight, or the late cancel stops
// the wrong statement.
func TestContextCancelRacingCompletionSparesTheNextStatement(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	const total = 300
	seedWideRows(t, conn, total)
	for i := 0; i < 60; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		rows, err := conn.Query(ctx, `SELECT id FROM wide_rows ORDER BY id`)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		seen := 0
		for rows.Next() {
			seen++
			if seen == total {
				// Every row is in hand and the server has already sent, or is
				// sending, CommandComplete: cancel now races the completion.
				cancel()
			}
		}
		_ = rows.Close()
		cancel() // idempotent; releases the context if the stream ended early
		res, err := conn.Exec(context.Background(), `SELECT COUNT(*) FROM wide_rows WHERE body <> ''`)
		if err != nil {
			t.Fatalf("iteration %d: the statement after a cancelled context was hit by a late cancel: %v", i, err)
		}
		if res.Rows[0][0].Dec.String() != itoaInt(total) {
			t.Fatalf("iteration %d: count %v", i, res.Rows)
		}
	}
}
