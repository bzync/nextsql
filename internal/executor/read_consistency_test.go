package executor

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

type fakeReadGate struct {
	write    error
	strong   error
	follower func(time.Duration) error
}

func (g fakeReadGate) AllowWrite() error        { return g.write }
func (g fakeReadGate) StrongReadBarrier() error { return g.strong }
func (g fakeReadGate) FollowerReadHealthy(d time.Duration) error {
	if g.follower == nil {
		return nil
	}
	return g.follower(d)
}

func TestReadConsistencyModes(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('a', 'x')`)

	unavail := nerr.New(nerr.Unavailable, "test.gate", "not the leader")

	// STRONG (default) on a node that fails the barrier: rejected, not served stale.
	db.SetGate(fakeReadGate{strong: unavail})
	if _, err := s.Exec(`SELECT n FROM t`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("strong read past a failed barrier: %v", err)
	}

	// BOUNDED: served locally when the freshness gate passes, and the session's
	// MAX STALENESS is handed to the gate.
	var gotBound time.Duration
	db.SetGate(fakeReadGate{strong: unavail, follower: func(d time.Duration) error { gotBound = d; return nil }})
	if err := s.SetReadConsistency(ReadBounded); err != nil {
		t.Fatal(err)
	}
	s.SetMaxStaleness(2 * time.Second)
	if res, err := s.Exec(`SELECT n FROM t`); err != nil || len(res.Rows) != 1 {
		t.Fatalf("bounded read: err=%v rows=%+v", err, res)
	}
	if gotBound != 2*time.Second {
		t.Fatalf("bounded staleness passed to gate = %v, want 2s", gotBound)
	}

	// BOUNDED: rejected when this node has fallen outside the bound.
	db.SetGate(fakeReadGate{follower: func(time.Duration) error { return unavail }})
	if _, err := s.Exec(`SELECT n FROM t`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("bounded read past the staleness bound: %v", err)
	}

	// BOUNDED with no explicit MAX STALENESS uses the default window.
	s2 := db.Session()
	if err := s2.SetReadConsistency(ReadBounded); err != nil {
		t.Fatal(err)
	}
	gotBound = 0
	db.SetGate(fakeReadGate{follower: func(d time.Duration) error { gotBound = d; return nil }})
	if _, err := s2.Exec(`SELECT n FROM t`); err != nil {
		t.Fatal(err)
	}
	if gotBound != DefaultMaxStaleness {
		t.Fatalf("default bounded window = %v, want %v", gotBound, DefaultMaxStaleness)
	}

	// STALE consults no gate at all.
	db.SetGate(fakeReadGate{strong: unavail, follower: func(time.Duration) error { return unavail }})
	s3 := db.Session()
	if err := s3.SetReadConsistency(ReadStale); err != nil {
		t.Fatal(err)
	}
	if res, err := s3.Exec(`SELECT n FROM t`); err != nil || len(res.Rows) != 1 {
		t.Fatalf("stale read: err=%v rows=%+v", err, res)
	}

	// Writes stay leader-gated regardless of read-consistency mode.
	db.SetGate(fakeReadGate{write: unavail})
	if _, err := s3.Exec(`INSERT INTO t (id, n) VALUES ('b', 'y')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("write on a follower: %v", err)
	}

	db.SetGate(nil)
	if _, err := s3.Exec(`SELECT n FROM t`); err != nil {
		t.Fatalf("single-node read: %v", err)
	}
}

func TestSetReadConsistencyRejectsUnknownMode(t *testing.T) {
	s := testDB(t).Session()
	if err := s.SetReadConsistency(ReadConsistency(0x7f)); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("unknown mode: %v", err)
	}
}
