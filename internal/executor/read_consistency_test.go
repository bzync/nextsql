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

// fakeStalenessGate additionally names its own default freshness window, the
// way an attached cluster does from its configured heartbeat.
type fakeStalenessGate struct {
	fakeReadGate
	window time.Duration
}

func (g fakeStalenessGate) DefaultMaxStaleness() time.Duration { return g.window }

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

// A BOUNDED read with no explicit MAX STALENESS takes its bound from the gate
// when the gate names one. An attached cluster derives that from its configured
// heartbeat, so raising raft_heartbeat_ms widens the default bound instead of
// leaving a fixed bound rejecting reads the health model considers fresh.
func TestBoundedReadDefaultFollowsGateWindow(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('a', 'x')`)
	if err := s.SetReadConsistency(ReadBounded); err != nil {
		t.Fatal(err)
	}

	var got time.Duration
	record := fakeReadGate{follower: func(d time.Duration) error { got = d; return nil }}

	// A gate that names a window: that window is the default bound.
	const window = 12 * time.Second
	if window == DefaultMaxStaleness {
		t.Fatal("test premise broken: the gate window equals the package default")
	}
	db.SetGate(fakeStalenessGate{fakeReadGate: record, window: window})
	if _, err := s.Exec(`SELECT n FROM t`); err != nil {
		t.Fatal(err)
	}
	if got != window {
		t.Fatalf("default bound = %v, want the gate's %v", got, window)
	}

	// An explicit MAX STALENESS still wins over the gate's default.
	s.SetMaxStaleness(3 * time.Second)
	got = 0
	if _, err := s.Exec(`SELECT n FROM t`); err != nil {
		t.Fatal(err)
	}
	if got != 3*time.Second {
		t.Fatalf("explicit MAX STALENESS = %v, want 3s", got)
	}

	// A gate that names no window falls back to the package default rather
	// than to zero, which FollowerReadHealthy reads as unbounded staleness.
	s2 := db.Session()
	if err := s2.SetReadConsistency(ReadBounded); err != nil {
		t.Fatal(err)
	}
	got = 0
	db.SetGate(record)
	if _, err := s2.Exec(`SELECT n FROM t`); err != nil {
		t.Fatal(err)
	}
	if got != DefaultMaxStaleness {
		t.Fatalf("fallback bound = %v, want %v", got, DefaultMaxStaleness)
	}

	// A gate that names a nonsensical zero window is treated as naming none,
	// not as asking for unbounded staleness.
	got = 0
	db.SetGate(fakeStalenessGate{fakeReadGate: record, window: 0})
	if _, err := s2.Exec(`SELECT n FROM t`); err != nil {
		t.Fatal(err)
	}
	if got != DefaultMaxStaleness {
		t.Fatalf("zero-window gate bound = %v, want %v", got, DefaultMaxStaleness)
	}

	db.SetGate(nil)
}
