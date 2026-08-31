package integration

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/storage/format"
)

// clusterNode is one member: an executor DB behind a Raft cluster, exposed over
// a TLS native-protocol server.
type clusterNode struct {
	id       string
	db       *executor.DB
	cluster  *replication.Cluster
	addr     string
	trans    *raft.InmemTransport
	raftAddr raft.ServerAddress
}

func startProtocolCluster3(t *testing.T) ([]*clusterNode, *tls.Config) {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}

	const n = 3
	addrs := make([]raft.ServerAddress, n)
	trans := make([]*raft.InmemTransport, n)
	for i := range trans {
		addrs[i], trans[i] = raft.NewInmemTransport("")
	}
	for i := range trans {
		for j := range trans {
			if i != j {
				trans[i].Connect(addrs[j], trans[j])
			}
		}
	}
	peers := make([]replication.Peer, n)
	for i := range peers {
		peers[i] = replication.Peer{ID: fmt.Sprintf("n%d", i+1), Address: string(addrs[i])}
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err := security.WriteSelfSigned(certPath, keyPath, "localhost"); err != nil {
		t.Fatal(err)
	}
	srvTLS, err := security.ServerTLS(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := security.ClientTLSFromPEM("localhost", pemBytes)
	if err != nil {
		t.Fatal(err)
	}

	nodes := make([]*clusterNode, n)
	for i := 0; i < n; i++ {
		db, err := executor.CreateWithIdentity(filepath.Join(t.TempDir(), "nextsql.db"), ident, keys, 32)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		cl, err := replication.Open(replication.Config{
			NodeID:       peers[i].ID,
			Peers:        peers,
			Bootstrap:    i == 0,
			Keys:         keys,
			Inmem:        true,
			Transport:    trans[i],
			ApplyTimeout: 3 * time.Second,
		}, db)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cl.Shutdown() })
		db.AttachCluster(cl)

		users, err := auth.Create(filepath.Join(t.TempDir(), "nextsql.users"))
		if err != nil {
			t.Fatal(err)
		}
		if err := users.Upsert("app", "s3cret"); err != nil {
			t.Fatal(err)
		}
		srv := protocol.NewServer(db, users)
		srv.TLS = srvTLS
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		t.Cleanup(func() { _ = srv.Close() })
		go func() { _ = srv.ListenAndServe(ctx, "127.0.0.1:0") }()
		deadline := time.Now().Add(2 * time.Second)
		for srv.Addr() == nil && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if srv.Addr() == nil {
			t.Fatal("server did not start")
		}
		nodes[i] = &clusterNode{
			id:       peers[i].ID,
			db:       db,
			cluster:  cl,
			addr:     srv.Addr().String(),
			trans:    trans[i],
			raftAddr: addrs[i],
		}
	}

	if _, err := nodes[0].cluster.WaitForLeader(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := nodes[0].cluster.JoinPeers(peers); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if nodes[0].cluster.Voters() >= n {
			return nodes, clientTLS
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cluster did not reach 3 voters")
	return nil, nil
}

func nodeAddrs(nodes []*clusterNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.addr
	}
	return out
}

func TestDriverFollowerReadRouting(t *testing.T) {
	nodes, clientTLS := startProtocolCluster3(t)
	ctx := context.Background()

	cl, err := nextsql.OpenCluster(ctx, nextsql.Config{
		Nodes:           nodeAddrs(nodes),
		Database:        "production",
		User:            "app",
		Password:        "s3cret",
		TLS:             clientTLS,
		ReadConsistency: nextsql.Stale,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	// DDL + writes route to the leader.
	if _, err := cl.Exec(ctx, `CREATE TABLE kv (id STRING PRIMARY KEY, n STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Exec(ctx, `INSERT INTO kv (id, n) VALUES ('a', 'x'), ('b', 'y')`); err != nil {
		t.Fatal(err)
	}

	// A STALE read is answered by some member; poll until replication catches up.
	// A STALE read accepts schema and row lag, so a follower may transiently
	// not know the table yet; poll until replication catches up.
	rows := 0
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := cl.Exec(ctx, `SELECT n FROM kv ORDER BY id`); err == nil {
			if rows = len(res.Rows); rows == 2 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rows != 2 {
		t.Fatalf("stale read saw %d rows", rows)
	}

	// The router sees exactly one leader and two followers.
	seen := cl.Nodes(ctx)
	var leaders, followers int
	for _, s := range seen {
		switch s.Role {
		case "leader":
			leaders++
		case "follower":
			followers++
		}
	}
	if leaders != 1 || followers != 2 {
		t.Fatalf("cluster view: %+v", seen)
	}

	// A direct connection to a follower: default STRONG is rejected while a
	// generous BOUNDED bound is served from local state.
	var follower *clusterNode
	for _, n := range nodes {
		if !n.cluster.IsLeader() {
			follower = n
			break
		}
	}
	strong, err := nextsql.OpenContext(ctx, nextsql.Config{
		Address: follower.addr, Database: "production", User: "app", Password: "s3cret", TLS: clientTLS,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer strong.Close()
	if _, err := strong.Exec(ctx, `SELECT n FROM kv`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("strong follower read: %v", err)
	}
	if err := strong.SetReadConsistency(ctx, nextsql.Bounded, time.Hour); err != nil {
		t.Fatal(err)
	}
	rows = 0
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := strong.Exec(ctx, `SELECT n FROM kv`); err == nil {
			if rows = len(res.Rows); rows == 2 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rows != 2 {
		t.Fatalf("bounded follower read saw %d rows", rows)
	}
}

// TestFollowerReadFailoverSessionGuarantee is the Phase 22 failover
// session-guarantee gate. A client holds one logical session over the cluster
// (default STRONG reads). After a write is acknowledged the leader is
// partitioned away and the surviving majority elects a new one. The session's
// guarantees must survive the leader change within the documented mode:
//
//   - STRONG reads stay read-after-write: every write acknowledged before the
//     failover is still visible after it (no lost acknowledged commit), and the
//     visible set never shrinks across the leader change (monotonic reads).
//   - a STALE read routed to a partitioned former leader may only ever go
//     forward relative to that node's own applied state — it is allowed to lag
//     the new leader, which is the documented STALE trade-off, but it must not
//     lose a write the node had already applied.
func TestFollowerReadFailoverSessionGuarantee(t *testing.T) {
	nodes, clientTLS := startProtocolCluster3(t)
	ctx := context.Background()

	cl, err := nextsql.OpenCluster(ctx, nextsql.Config{
		Nodes:    nodeAddrs(nodes),
		Database: "production",
		User:     "app",
		Password: "s3cret",
		TLS:      clientTLS,
		// Default STRONG consistency: the router is a leader-failover wrapper.
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	if _, err := cl.Exec(ctx, `CREATE TABLE kv (id STRING PRIMARY KEY, n STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Exec(ctx, `INSERT INTO kv (id, n) VALUES ('a', 'x'), ('b', 'y')`); err != nil {
		t.Fatal(err)
	}

	// Read-your-writes before failover.
	if res, err := cl.Exec(ctx, `SELECT id FROM kv ORDER BY id`); err != nil || len(res.Rows) != 2 {
		t.Fatalf("pre-failover strong read: err=%v rows=%d", err, len(res.Rows))
	}

	// Identify and partition the current leader from the other two nodes.
	var old *clusterNode
	for _, n := range nodes {
		if n.cluster.IsLeader() {
			old = n
			break
		}
	}
	if old == nil {
		t.Fatal("no leader before failover")
	}
	for _, n := range nodes {
		if n != old {
			old.trans.Disconnect(n.raftAddr)
			n.trans.Disconnect(old.raftAddr)
		}
	}

	// The surviving majority must elect a new leader.
	var rest []*clusterNode
	for _, n := range nodes {
		if n != old {
			rest = append(rest, n)
		}
	}
	if _, err := rest[0].cluster.WaitForLeader(5 * time.Second); err != nil {
		t.Fatalf("majority elected no leader: %v", err)
	}

	// The session's STRONG reads recover on the new leader and still observe
	// every acknowledged write. The router returns unavailable once while its
	// cached view still points at the old leader; a client retries.
	seen := 0
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res, err := cl.Exec(ctx, `SELECT id FROM kv ORDER BY id`)
		if err == nil {
			seen = len(res.Rows)
			if seen == 2 {
				break
			}
		} else if !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("post-failover strong read: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if seen != 2 {
		t.Fatalf("post-failover strong read lost an acknowledged write: saw %d rows", seen)
	}

	// Monotonic reads and read-your-writes continue on the new leader.
	if _, err := cl.Exec(ctx, `INSERT INTO kv (id, n) VALUES ('c', 'z')`); err != nil {
		t.Fatalf("post-failover write: %v", err)
	}
	if res, err := cl.Exec(ctx, `SELECT id FROM kv ORDER BY id`); err != nil || len(res.Rows) != 3 {
		t.Fatalf("post-failover monotonic read: err=%v rows=%d", err, len(res.Rows))
	}

	// A STALE read against the partitioned former leader still serves its own
	// applied state — it never regressed below the writes it had applied,
	// though it is permitted to lag the new leader (it has not seen 'c').
	stale, err := nextsql.OpenContext(ctx, nextsql.Config{
		Address: old.addr, Database: "production", User: "app", Password: "s3cret", TLS: clientTLS,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	if err := stale.SetReadConsistency(ctx, nextsql.Stale, 0); err != nil {
		t.Fatal(err)
	}
	res, err := stale.Exec(ctx, `SELECT id FROM kv ORDER BY id`)
	if err != nil {
		t.Fatalf("stale read on former leader: %v", err)
	}
	if len(res.Rows) < 2 {
		t.Fatalf("stale read on former leader regressed below applied state: %d rows", len(res.Rows))
	}

	// Reconnecting the former leader lets it rejoin and converge.
	for _, n := range rest {
		old.trans.Connect(n.raftAddr, n.trans)
		n.trans.Connect(old.raftAddr, old.trans)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := stale.Exec(ctx, `SELECT id FROM kv`); err == nil && len(res.Rows) == 3 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("former leader did not converge after rejoining")
}
