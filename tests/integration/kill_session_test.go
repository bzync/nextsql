package integration

import (
	"context"
	"testing"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
)

// Killing a session terminates its TCP connection, rolls back any open transaction,
// releases all held locks, and unregisters it from system.sessions.
func TestKillSessionOverProtocol(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)

	// Conn 1 (victim)
	var victim *nextsql.Conn = openApp(t, addr, tlsCfg)
	ctx := context.Background()

	if _, err := victim.Exec(ctx, `CREATE TABLE kill_test (id INT64 PRIMARY KEY, val INT64 NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := victim.Exec(ctx, `INSERT INTO kill_test VALUES (1, 100)`); err != nil {
		t.Fatal(err)
	}

	// Query victim's own session ID while it is the only connected session
	sessRes, err := victim.Exec(ctx, `SELECT session_id FROM system.sessions`)
	if err != nil {
		t.Fatalf("SELECT session_id: %v", err)
	}
	if len(sessRes.Rows) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessRes.Rows))
	}
	victimID := sessRes.Rows[0][0].Str
	if victimID == "" {
		t.Fatal("empty victimID")
	}

	// Conn 2 (operator)
	operator := openApp(t, addr, tlsCfg)

	// Begin txn on victim and hold a lock
	if _, err := victim.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := victim.Exec(ctx, `UPDATE kill_test SET val = 200 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	// Operator kills victim session
	killRes, err := operator.Exec(ctx, `KILL SESSION '`+victimID+`'`)
	if err != nil {
		t.Fatalf("KILL SESSION: %v", err)
	}
	if killRes.Affected != 1 {
		t.Fatalf("expected 1 session killed, got %d", killRes.Affected)
	}

	// Victim's connection must be terminated by the server
	// Waiting for victim's socket to realize it is closed
	time.Sleep(100 * time.Millisecond)
	_, err = victim.Exec(ctx, `SELECT * FROM kill_test`)
	if err == nil {
		t.Fatal("expected victim connection to fail after being killed, but it succeeded")
	}

	// Locks must be released: operator can now update id=1 immediately
	if _, err := operator.Exec(ctx, `UPDATE kill_test SET val = 300 WHERE id = 1`); err != nil {
		t.Fatalf("operator failed to update row after victim killed: %v", err)
	}

	// Verify uncommitted update from victim was rolled back and operator's update applied
	check, err := operator.Exec(ctx, `SELECT val FROM kill_test WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(check.Rows) != 1 || check.Rows[0][0].Int != 300 {
		t.Fatalf("unexpected value: %v", check.Rows)
	}

	// Verify victim session is unregistered from system.sessions
	afterRes, err := operator.Exec(ctx, `SELECT session_id FROM system.sessions`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range afterRes.Rows {
		if row[0].Str == victimID {
			t.Fatalf("session %s still visible in system.sessions", victimID)
		}
	}
}
