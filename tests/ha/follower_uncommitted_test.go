package ha

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/executor"
)

// TestHAFollowersNeverServeUncommittedLeaderVersions pins GAPS.md's open
// CRITICAL row (log #301): a follower installs the leader's page images but
// receives none of the leader's undo records, and ApplyReplicated registers
// only *committed* transaction ids — txn.Manager.statusLocked then defaults an
// id it has never heard of to StatusCommitted. A committed transaction's page
// image legitimately carries a *different*, still-open transaction's row
// versions, because the two share the page. So the follower serves the open
// transaction's value as though it were committed, and keeps serving it after
// the leader rolls back (a rollback changes pages without logging them).
//
// The sequence is deliberately the one from log #301: an open write on the
// leader, a *separate* committed write that pushes the shared page into the
// replicated stream, then a STALE read on every follower.
func TestHAFollowersNeverServeUncommittedLeaderVersions(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)

	if _, err := lead.db.Session().Exec(`CREATE TABLE acct (id STRING PRIMARY KEY, balance STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	// Two rows that will share a page, so one transaction's commit carries
	// the other's uncommitted version.
	if _, err := lead.db.Session().Exec(`INSERT INTO acct (id, balance) VALUES ('a','100'), ('b','100')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	// Session A: change 'a' and hold the transaction open.
	open := lead.db.Session()
	if _, err := open.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := open.Exec(`UPDATE acct SET balance = 'UNCOMMITTED' WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}

	// Session B: commit a change to the neighbouring row. Its page image is
	// what carries A's uncommitted version into the replicated stream.
	if _, err := lead.db.Session().Exec(`UPDATE acct SET balance = '200' WHERE id = 'b'`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	readAll := func(stage string) {
		t.Helper()
		for _, n := range nodes {
			if n == lead || n.cluster == nil {
				continue
			}
			res, err := staleSession(t, n).Exec(`SELECT balance FROM acct WHERE id = 'a'`)
			if err != nil {
				t.Fatalf("%s: follower %s read: %v", stage, n.id, err)
			}
			if len(res.Rows) != 1 {
				t.Fatalf("%s: follower %s returned %d rows", stage, n.id, len(res.Rows))
			}
			if got := res.Rows[0][0].Str; got == "UNCOMMITTED" {
				t.Errorf("%s: follower %s served an UNCOMMITTED value for 'a'", stage, n.id)
			} else if got != "100" {
				t.Errorf("%s: follower %s served %q for 'a', want the committed 100", stage, n.id, got)
			}
		}
	}
	readAll("while the leader transaction is still open")

	// Roll back. A rollback changes pages without logging them, so nothing
	// tells the follower the value it installed is now dead.
	if _, err := open.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	// Give replication a moment; there may be nothing to replicate at all,
	// which is precisely the problem.
	time.Sleep(300 * time.Millisecond)
	readAll("after the leader rolled back")

	// The leader itself must of course be right.
	res, err := lead.db.Session().Exec(`SELECT balance FROM acct WHERE id = 'a'`)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Rows[0][0].Str; got != "100" {
		t.Fatalf("leader shows %q for 'a' after rollback, want 100", got)
	}

	// Failover: a node promoted while it holds the rolled-back version makes
	// it the cluster's committed state.
	_ = lead.cluster.Shutdown()
	lead.trans.DisconnectAll()
	lead.cluster = nil
	var rest []*node
	for _, n := range nodes {
		if n.cluster != nil {
			rest = append(rest, n)
		}
	}
	if _, err := rest[0].cluster.WaitForLeader(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	cur := leader(t, rest)
	promoted, err := cur.db.Session().Exec(`SELECT balance FROM acct WHERE id = 'a'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted.Rows) != 1 {
		t.Fatalf("promoted leader returned %d rows for 'a'", len(promoted.Rows))
	}
	if got := promoted.Rows[0][0].Str; got == "UNCOMMITTED" {
		t.Fatalf("promoted leader committed a rolled-back value for 'a': %q", got)
	} else if got != "100" {
		t.Fatalf("promoted leader shows %q for 'a', want the committed 100", got)
	}
}

var _ = executor.ReadStale
