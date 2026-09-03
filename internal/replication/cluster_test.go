package replication

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

type recordApplier struct {
	mu   sync.Mutex
	recs []wal.Record
}

func (a *recordApplier) ApplyRecords(recs []wal.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recs = append(a.recs, recs...)
	return nil
}

func (a *recordApplier) last() format.LSN {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.recs) == 0 {
		return 0
	}
	return a.recs[len(a.recs)-1].LSN
}

func testKeys(t *testing.T) crypto.KeyProvider {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func startRaft(t *testing.T, n int) ([]*Cluster, []*raft.InmemTransport, []raft.ServerAddress, []*recordApplier) {
	t.Helper()
	if n < 3 {
		t.Fatal("need 3 voters")
	}
	keys := testKeys(t)
	addrs := make([]raft.ServerAddress, n)
	trans := make([]*raft.InmemTransport, n)
	for i := 0; i < n; i++ {
		a, tr := raft.NewInmemTransport("")
		addrs[i], trans[i] = a, tr
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				trans[i].Connect(addrs[j], trans[j])
			}
		}
	}
	peers := make([]Peer, n)
	for i := 0; i < n; i++ {
		peers[i] = Peer{ID: string(rune('a' + i)), Address: string(addrs[i])}
	}
	// Use n1/n2/n3 ids for readability.
	for i := range peers {
		peers[i].ID = "n" + string(rune('1'+i))
	}
	cls := make([]*Cluster, n)
	apps := make([]*recordApplier, n)
	for i := 0; i < n; i++ {
		apps[i] = &recordApplier{}
		cl, err := Open(Config{
			NodeID:       peers[i].ID,
			Peers:        peers,
			Bootstrap:    i == 0,
			Keys:         keys,
			Inmem:        true,
			Transport:    trans[i],
			ApplyTimeout: 3 * time.Second,
		}, apps[i])
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cl.Shutdown() })
		cls[i] = cl
	}
	if _, err := cls[0].WaitForLeader(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := cls[0].JoinPeers(peers); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cls[0].Voters() >= n && liveLeader(cls) != nil {
			return cls, trans, addrs, apps
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cluster did not reach %d voters", n)
	return cls, trans, addrs, apps
}

func liveLeader(cls []*Cluster) *Cluster {
	var found *Cluster
	n := 0
	for _, c := range cls {
		if c != nil && c.IsLeader() {
			n++
			found = c
		}
	}
	if n == 1 {
		return found
	}
	return nil
}

func raftLeader(t *testing.T, cls []*Cluster) *Cluster {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c := liveLeader(cls); c != nil {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no leader")
	return nil
}

func TestRaftReplicateQuorum(t *testing.T) {
	cls, _, _, apps := startRaft(t, 3)
	lead := raftLeader(t, cls)
	recs := []wal.Record{
		{Type: wal.RecBegin, LSN: 1, TxnID: 1},
		{Type: wal.RecCommit, LSN: 2, TxnID: 1, PrevLSN: 1},
	}
	if err := lead.Replicate(recs); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for i, c := range cls {
			if c.IsLeader() {
				continue
			}
			if apps[i].last() < 2 && c.AppliedLSN() < 2 {
				ok = false
			}
		}
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("followers did not apply")
}

// TestReplicateOnNonLeaderIsDefiniteNotProposed proves a Replicate call
// rejected before raft.Apply is ever attempted (this node is not the
// leader) is classified as definite/not-proposed — the case
// storage.Engine.commitAndReplicate is safe to discard a held, not-yet-
// durable local commit for, as opposed to an ambiguous in-doubt failure
// (see TestIsRetryableApplyErr for those).
func TestReplicateOnNonLeaderIsDefiniteNotProposed(t *testing.T) {
	cls, _, _, _ := startRaft(t, 3)
	lead := raftLeader(t, cls)
	var follower *Cluster
	for _, c := range cls {
		if c != lead {
			follower = c
			break
		}
	}
	if follower == nil {
		t.Fatal("no follower found")
	}
	recs := []wal.Record{
		{Type: wal.RecBegin, LSN: 1, TxnID: 1},
		{Type: wal.RecCommit, LSN: 2, TxnID: 1, PrevLSN: 1},
	}
	err := follower.Replicate(recs)
	if err == nil {
		t.Fatal("Replicate on a follower must fail")
	}
	np, ok := err.(interface{ NotProposed() bool })
	if !ok || !np.NotProposed() {
		t.Fatalf("Replicate on a follower = %v (%T), want a NotProposed error", err, err)
	}
	if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("Replicate on a follower = %v, want Unavailable", err)
	}
}

func TestRaftKillLeaderElection(t *testing.T) {
	cls, trans, _, _ := startRaft(t, 3)
	lead := raftLeader(t, cls)
	start := time.Now()
	if err := lead.Shutdown(); err != nil {
		t.Fatal(err)
	}
	for i, c := range cls {
		if c == lead {
			trans[i].DisconnectAll()
		}
	}
	var rest []*Cluster
	for _, c := range cls {
		if c != lead {
			rest = append(rest, c)
		}
	}
	if _, err := rest[0].WaitForLeader(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("election %s exceeds 3s target", time.Since(start))
	}
	newLead := raftLeader(t, rest)
	if newLead == lead {
		t.Fatal("dead leader still selected")
	}
}

func TestRaftPartitionNoSplitBrainWrite(t *testing.T) {
	cls, trans, addrs, _ := startRaft(t, 3)
	// Isolate node 0.
	trans[0].Disconnect(addrs[1])
	trans[0].Disconnect(addrs[2])
	trans[1].Disconnect(addrs[0])
	trans[2].Disconnect(addrs[0])

	deadline := time.Now().Add(4 * time.Second)
	var maj *Cluster
	for time.Now().Before(deadline) {
		var n int
		maj = nil
		for _, c := range []*Cluster{cls[1], cls[2]} {
			if c.IsLeader() {
				n++
				maj = c
			}
		}
		if n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if maj == nil {
		t.Fatal("majority has no leader")
	}
	recs := []wal.Record{{Type: wal.RecBegin, LSN: 1, TxnID: 1}, {Type: wal.RecCommit, LSN: 2, TxnID: 1, PrevLSN: 1}}
	if err := maj.Replicate(recs); err != nil {
		t.Fatal(err)
	}
	if err := cls[0].Replicate(recs); err == nil {
		t.Fatal("isolated node must not commit")
	}
}

func TestOpenRejectsFewerThanThreeVoters(t *testing.T) {
	keys := testKeys(t)
	_, trans := raft.NewInmemTransport("")
	_, err := Open(Config{
		NodeID:    "n1",
		Keys:      keys,
		Inmem:     true,
		Transport: trans,
		Peers:     []Peer{{ID: "n1", Address: "a"}, {ID: "n2", Address: "b"}},
		Bootstrap: true,
	}, nil)
	if err == nil {
		t.Fatal("expected fewer-than-3 voters to fail")
	}
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestIsRetryableApplyErr(t *testing.T) {
	retryable := []error{
		raft.ErrNotLeader, raft.ErrLeadershipLost, raft.ErrEnqueueTimeout,
		raft.ErrLeadershipTransferInProgress, raft.ErrRaftShutdown,
	}
	for _, err := range retryable {
		if !isRetryableApplyErr(err) {
			t.Errorf("%v: want retryable", err)
		}
	}
	notRetryable := []error{errors.New("some other apply failure"), raft.ErrAbortedByRestore, nil}
	for _, err := range notRetryable {
		if isRetryableApplyErr(err) {
			t.Errorf("%v: want not retryable", err)
		}
	}
}

func TestParsePeers(t *testing.T) {
	got, err := ParsePeers("n1=127.0.0.1:1,n2=127.0.0.1:2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "n1" || got[1].Address != "127.0.0.1:2" {
		t.Fatalf("%+v", got)
	}
	if _, err := ParsePeers("n1"); err == nil {
		t.Fatal("expected parse error")
	}
}
