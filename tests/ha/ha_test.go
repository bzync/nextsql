package ha

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/backup"
	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

type haFieldKeys struct{ key clientenc.Key }

func (p haFieldKeys) CurrentFieldKey(context.Context, string, string, string) (clientenc.Key, error) {
	return p.key, nil
}

func (p haFieldKeys) FieldKey(_ context.Context, _, _, _, id string) (clientenc.Key, error) {
	if id != p.key.ID {
		return clientenc.Key{}, nerr.New(nerr.NotFound, "test", "field key missing")
	}
	return p.key, nil
}

type node struct {
	id      string
	db      *executor.DB
	cluster *replication.Cluster
	trans   *raft.InmemTransport
	addr    raft.ServerAddress
}

func cluster3(t *testing.T) []*node {
	t.Helper()
	nodes, _, _ := cluster3Keys(t)
	return nodes
}

// cluster3Keys is cluster3, additionally returning the shared key provider
// and peer list so a test can build a new node (e.g. a repaired replica
// restored from backup) with the same identity/keys and rejoin it.
func cluster3Keys(t *testing.T) ([]*node, crypto.KeyProvider, []replication.Peer) {
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
	peers := make([]replication.Peer, n)
	for i := 0; i < n; i++ {
		peers[i] = replication.Peer{ID: fmt.Sprintf("n%d", i+1), Address: string(addrs[i])}
	}
	nodes := make([]*node, n)
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
		nodes[i] = &node{id: peers[i].ID, db: db, cluster: cl, trans: trans[i], addr: addrs[i]}
	}
	if _, err := nodes[0].cluster.WaitForLeader(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := nodes[0].cluster.JoinPeers(peers); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if nodes[0].cluster.Voters() >= n && live(nodes) != nil {
			return nodes, keys, peers
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cluster did not reach 3 voters")
	return nodes, keys, peers
}

// staleSession reads a node's locally applied state without the STRONG Raft
// read barrier, so a follower can be inspected directly.
func staleSession(t *testing.T, n *node) *executor.Session {
	t.Helper()
	s := n.db.Session()
	if err := s.SetReadConsistency(executor.ReadStale); err != nil {
		t.Fatal(err)
	}
	return s
}

// boundedSession reads a node's applied state behind the BOUNDED freshness
// gate: served while the node is the leader or a follower within maxStaleness
// of the leader, rejected as unavailable once it falls further behind.
func boundedSession(t *testing.T, n *node, maxStaleness time.Duration) *executor.Session {
	t.Helper()
	s := n.db.Session()
	if err := s.SetReadConsistency(executor.ReadBounded); err != nil {
		t.Fatal(err)
	}
	s.SetMaxStaleness(maxStaleness)
	return s
}

func live(nodes []*node) *node {
	var found *node
	n := 0
	for _, nd := range nodes {
		if nd.cluster != nil && nd.cluster.IsLeader() {
			n++
			found = nd
		}
	}
	if n == 1 {
		return found
	}
	return nil
}

func leader(t *testing.T, nodes []*node) *node {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n := live(nodes); n != nil {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no leader")
	return nil
}

func waitAll(t *testing.T, nodes []*node, lsn uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for _, n := range nodes {
			if n.cluster != nil && uint64(n.cluster.AppliedLSN()) < lsn {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for LSN %d", lsn)
}

func TestHAThreeNodeQuorumCommit(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := lead.db.Session().Exec(`INSERT INTO t (id) VALUES ('ok')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))
	var fol *node
	for _, n := range nodes {
		if n != lead {
			fol = n
			break
		}
	}
	// A STRONG (default) read on a follower is rejected, not served stale.
	if _, err := fol.db.Session().Exec(`SELECT id FROM t`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("follower strong read %v", err)
	}
	// A STALE read observes the follower's applied state.
	res, err := staleSession(t, fol).Exec(`SELECT id FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("%+v", res.Rows)
	}
	if _, err := fol.db.Session().Exec(`INSERT INTO t (id) VALUES ('no')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("follower write %v", err)
	}
}

func TestHABoundedFollowerRead(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := lead.db.Session().Exec(`INSERT INTO t (id) VALUES ('ok')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))
	var fol *node
	for _, n := range nodes {
		if n != lead {
			fol = n
			break
		}
	}

	// A caught-up follower serves a BOUNDED read from local state.
	res, err := boundedSession(t, fol, time.Minute).Exec(`SELECT id FROM t`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("bounded follower read: err=%v rows=%+v", err, res)
	}
	// The leader always passes the BOUNDED gate.
	if _, err := boundedSession(t, lead, time.Nanosecond).Exec(`SELECT id FROM t`); err != nil {
		t.Fatalf("bounded leader read: %v", err)
	}

	// Partition the follower: once it stops hearing the leader past the healthy
	// window, BOUNDED reads are rejected as unavailable rather than served stale.
	for _, n := range nodes {
		if n != fol {
			fol.trans.Disconnect(n.addr)
			n.trans.Disconnect(fol.addr)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, err := boundedSession(t, fol, time.Minute).Exec(`SELECT id FROM t`)
		if nerr.HasCode(err, nerr.Unavailable) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("partitioned follower kept serving BOUNDED reads")
}

func TestHAKillLeader(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := lead.db.Session().Exec(`INSERT INTO t (id) VALUES ('durable')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	start := time.Now()
	_ = lead.cluster.Shutdown()
	lead.trans.DisconnectAll()
	lead.cluster = nil
	var rest []*node
	for _, n := range nodes {
		if n.cluster != nil {
			rest = append(rest, n)
		}
	}
	if _, err := rest[0].cluster.WaitForLeader(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	cur := leader(t, rest)
	res, err := cur.db.Session().Exec(`SELECT id FROM t WHERE id = 'durable'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatal("acknowledged quorum commit was lost")
	}
	if _, err := cur.db.Session().Exec(`INSERT INTO t (id) VALUES ('after')`); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("service recovery %s", time.Since(start))
	}
}

// TestHALeaderTransferSQL exercises the CLUSTER TRANSFER LEADER admin
// statement (the SQL surface for replication.Cluster.TransferLeadership)
// against a live 3-node cluster: a planned handoff, unlike TestHAKillLeader's
// crash failover, must move leadership to a different voter without any
// unavailability window a client would observe as a write rejection.
func TestHALeaderTransferSQL(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	before := lead.id
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	if _, err := lead.db.Session().Exec(`CLUSTER TRANSFER LEADER`); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var after *node
	for time.Now().Before(deadline) {
		if n := live(nodes); n != nil && n.id != before {
			after = n
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if after == nil {
		t.Fatal("leadership did not move to another voter")
	}
	if _, err := after.db.Session().Exec(`INSERT INTO t (id) VALUES ('post-transfer')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(after.cluster.AppliedLSN()))
	res, err := after.db.Session().Exec(`SELECT id FROM t WHERE id = 'post-transfer'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatal("write after leader transfer was lost")
	}
}

func TestHAEncryptedClientCiphertextSurvivesLeaderFailover(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE accounts (id STRING PRIMARY KEY, secret TEXT ENCRYPTED CLIENT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	fieldKey := clientenc.Key{ID: "ha-v1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 6
	}
	provider := haFieldKeys{key: fieldKey}
	beforeFailover, err := clientenc.Encrypt(context.Background(), provider, "app", "accounts", "secret", types.TextValue("secret-before-failover"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lead.db.Session().ExecContext(context.Background(), `INSERT INTO accounts (id, secret) VALUES ('1', $1)`, []executor.Param{{Value: types.StringValue(beforeFailover)}}); err != nil {
		t.Fatal(err)
	}
	writtenLSN := uint64(lead.cluster.AppliedLSN())
	waitAll(t, nodes, writtenLSN)
	for _, n := range nodes {
		res, err := staleSession(t, n).Exec(`SELECT secret FROM accounts WHERE id = '1'`)
		if err != nil || len(res.Rows) != 1 || res.Rows[0][0].Str != beforeFailover {
			t.Fatalf("replica %s before failover: rows=%+v err=%v", n.id, res.Rows, err)
		}
	}

	if err := lead.cluster.Shutdown(); err != nil {
		t.Fatal(err)
	}
	lead.trans.DisconnectAll()
	lead.cluster = nil
	remaining := make([]*node, 0, 2)
	for _, n := range nodes {
		if n.cluster != nil {
			remaining = append(remaining, n)
		}
	}
	if _, err := remaining[0].cluster.WaitForLeader(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	newLeader := leader(t, remaining)
	res, err := newLeader.db.Session().Exec(`SELECT secret FROM accounts WHERE id = '1'`)
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0].Str != beforeFailover {
		t.Fatalf("new leader lost acknowledged client ciphertext: rows=%+v err=%v", res.Rows, err)
	}
	plain, err := clientenc.Decrypt(context.Background(), provider, "app", "accounts", "secret", res.Rows[0][0].Str)
	if err != nil || plain.Typ.Kind != types.KindText || plain.Str != "secret-before-failover" {
		t.Fatalf("new-leader decrypt: value=%+v err=%v", plain, err)
	}

	afterFailover, err := clientenc.Encrypt(context.Background(), provider, "app", "accounts", "secret", types.TextValue("secret-after-failover"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newLeader.db.Session().ExecContext(context.Background(), `UPDATE accounts SET secret = $1 WHERE id = '1'`, []executor.Param{{Value: types.StringValue(afterFailover)}}); err != nil {
		t.Fatal(err)
	}
	waitAll(t, remaining, uint64(newLeader.cluster.AppliedLSN()))
	for _, n := range remaining {
		res, err := staleSession(t, n).Exec(`SELECT secret FROM accounts WHERE id = '1'`)
		if err != nil || len(res.Rows) != 1 || res.Rows[0][0].Str != afterFailover {
			t.Fatalf("replica %s after failover: rows=%+v err=%v", n.id, res.Rows, err)
		}
		plain, err := clientenc.Decrypt(context.Background(), provider, "app", "accounts", "secret", res.Rows[0][0].Str)
		if err != nil || plain.Str != "secret-after-failover" {
			t.Fatalf("replica %s post-failover decrypt: value=%+v err=%v", n.id, plain, err)
		}
	}
}

func TestHAPartitionRejectsMinorityWrites(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	iso := nodes[0]
	iso.trans.Disconnect(nodes[1].addr)
	iso.trans.Disconnect(nodes[2].addr)
	nodes[1].trans.Disconnect(iso.addr)
	nodes[2].trans.Disconnect(iso.addr)

	deadline := time.Now().Add(4 * time.Second)
	var maj *node
	for time.Now().Before(deadline) {
		maj = nil
		for _, n := range []*node{nodes[1], nodes[2]} {
			if n.cluster.IsLeader() {
				maj = n
			}
		}
		if maj != nil {
			if _, err := maj.db.Session().Exec(`INSERT INTO t (id) VALUES ('maj')`); err == nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if maj == nil {
		t.Fatal("majority did not elect a leader")
	}
	if _, err := iso.db.Session().Exec(`INSERT INTO t (id) VALUES ('iso')`); err == nil {
		t.Fatal("isolated node acknowledged a write")
	}
}

func TestHAReplicaRepair(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := lead.db.Session().Exec(`INSERT INTO t (id) VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	var lag *node
	for _, n := range nodes {
		if n != lead {
			lag = n
			break
		}
	}
	for _, n := range nodes {
		if n != lag {
			lag.trans.Disconnect(n.addr)
			n.trans.Disconnect(lag.addr)
		}
	}
	var rest []*node
	for _, n := range nodes {
		if n != lag {
			rest = append(rest, n)
		}
	}
	if _, err := rest[0].cluster.WaitForLeader(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	lead = leader(t, rest)
	if _, err := lead.db.Session().Exec(`INSERT INTO t (id) VALUES ('two')`); err != nil {
		t.Fatal(err)
	}
	for _, n := range rest {
		lag.trans.Connect(n.addr, n.trans)
		n.trans.Connect(lag.addr, lag.trans)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))
	res, err := staleSession(t, lag).Exec(`SELECT id FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("repair rows %d %+v", len(res.Rows), res.Rows)
	}
}

// TestHAReplicaRepairFromBackupAddVoter covers the OTHER documented replica
// repair path (docs/ha.md "Replica repair and rolling maintenance": "A wiped
// replica is restored from `nextsql backup` / `nextsql restore` (same
// identity and keys), then rejoined with `AddVoter`") — distinct from
// TestHAReplicaRepair above, which only covers a lagging-but-reachable
// follower catching up from the Raft log. Here the replica's own data is
// gone entirely: it is repaired from an out-of-band backup plus Raft log
// replay for whatever committed after the backup was taken, exercising
// AddVoter for a node with pre-existing (restored) data rather than an
// empty one.
//
// This test found a real data-loss bug in that documented procedure on
// first write (2026-09-03, TODO.md log #95) — there was previously zero
// coverage of this path. backup.Create/Restore each open the data file
// directly (via storage.Open/OpenWith) and ran their own checkpoint, which
// allocated and consumed several WAL LSN numbers purely as local
// housekeeping, unrelated to replication. Once the repaired replica
// rejoined via AddVoter, internal/storage/engine.go's ApplyReplicated
// would skip any replayed batch whose LSN was less than the engine's own
// e.WAL.NextLSN() — a check meant to make replaying the whole Raft log
// against already-caught-up data an idempotent no-op. But that local
// housekeeping inflated NextLSN() past LSN numbers the leader originally
// assigned to real, not-yet-applied writes (like the 'two' row this test
// inserts after the backup), so ApplyReplicated silently treated them as
// already-applied and never replayed them — the repaired replica
// permanently, silently lost any write between the backup and the rejoin.
// Fixed the same session: Engine.Checkpoint() (only two callers in the
// whole codebase, Close() and backup.Create) now skips writing a fresh
// checkpoint record when nothing has happened since the engine was
// opened (new Engine.openNextLSN field), so backup.Create's checkpoint of
// an already-cleanly-closed file no longer perturbs its LSN numbering. No
// on-disk format change. See TODO.md log #95 for the full writeup.
func TestHAReplicaRepairFromBackupAddVoter(t *testing.T) {
	nodes, keys, peers := cluster3Keys(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := lead.db.Session().Exec(`INSERT INTO t (id) VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	var victim *node
	for _, n := range nodes {
		if n != lead {
			victim = n
			break
		}
	}
	dbPath := victim.db.Eng.Path()
	dataDir := filepath.Dir(dbPath)

	// Simulate the victim being wiped: shut its engine and Raft
	// participation down first (backup.Create opens the data file directly
	// and must not race a still-live engine on the same file), then take
	// the last backup of it while it was healthy.
	if err := victim.cluster.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := victim.db.Close(); err != nil {
		t.Fatal(err)
	}
	backupDest := filepath.Join(t.TempDir(), "backup")
	if _, err := backup.Create(dataDir, backupDest, keys, backup.Options{}); err != nil {
		t.Fatal(err)
	}
	if err := lead.cluster.RemoveServer(victim.id); err != nil {
		t.Fatal(err)
	}

	var rest []*node
	for _, n := range nodes {
		if n != victim {
			rest = append(rest, n)
		}
	}
	lead = leader(t, rest)

	// The cluster keeps taking writes the backup does not have — the
	// restored replica must catch these up via ordinary Raft log replay,
	// not just the backup.
	if _, err := lead.db.Session().Exec(`INSERT INTO t (id) VALUES ('two')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, rest, uint64(lead.cluster.AppliedLSN()))

	// Restore the backup into a fresh data directory — the repaired
	// replacement for the wiped node — and open it.
	restoreDir := filepath.Join(t.TempDir(), "restored")
	if _, err := backup.Restore(backupDest, restoreDir, keys, backup.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	newDB, err := executor.Open(filepath.Join(restoreDir, "nextsql.db"), keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = newDB.Close() })

	// Rejoin under the same node ID and network address (same identity),
	// reusing the original in-memory transport, which Shutdown left intact.
	newCl, err := replication.Open(replication.Config{
		NodeID:       victim.id,
		Peers:        peers,
		Bootstrap:    false,
		Keys:         keys,
		Inmem:        true,
		Transport:    victim.trans,
		ApplyTimeout: 3 * time.Second,
	}, newDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = newCl.Shutdown() })
	newDB.AttachCluster(newCl)
	restored := &node{id: victim.id, db: newDB, cluster: newCl, trans: victim.trans, addr: victim.addr}

	if err := lead.cluster.AddVoter(restored.id, string(restored.addr)); err != nil {
		t.Fatal(err)
	}
	waitAll(t, []*node{restored}, uint64(lead.cluster.AppliedLSN()))

	res, err := staleSession(t, restored).Exec(`SELECT id FROM t ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0][0].Str != "one" || res.Rows[1][0].Str != "two" {
		t.Fatalf("repaired replica rows: %+v", res.Rows)
	}
}

func TestHARollingMaintenance(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	for i, step := range nodes {
		for _, n := range nodes {
			if n != step {
				step.trans.Disconnect(n.addr)
				n.trans.Disconnect(step.addr)
			}
		}
		var rest []*node
		for _, n := range nodes {
			if n != step {
				rest = append(rest, n)
			}
		}
		if _, err := rest[0].cluster.WaitForLeader(3 * time.Second); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		cur := leader(t, rest)
		if _, err := cur.db.Session().Exec(fmt.Sprintf(`INSERT INTO t (id) VALUES ('r%d')`, i)); err != nil {
			t.Fatal(err)
		}
		for _, n := range rest {
			step.trans.Connect(n.addr, n.trans)
			n.trans.Connect(step.addr, step.trans)
		}
		if _, err := nodes[0].cluster.WaitForLeader(3 * time.Second); err != nil {
			t.Fatal(err)
		}
		lead = leader(t, nodes)
		waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))
	}
	res, err := lead.db.Session().Exec(`SELECT id FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("after rolling: %+v", res.Rows)
	}
}

func TestHAFKCascadeFollowersMatchLeader(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	s := lead.db.Session()
	if _, err := s.Exec(`CREATE TABLE parents (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO parents (id) VALUES ('p1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO children (id, parent_id) VALUES ('c1', 'p1'), ('c2', 'p1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))
	for _, n := range nodes {
		sess := staleSession(t, n)
		res, err := sess.Exec(`SELECT id FROM parents`)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 0 {
			t.Fatalf("%s parents %d (followers apply WAL, not FK actions)", n.id, len(res.Rows))
		}
		res, err = sess.Exec(`SELECT id FROM children`)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 0 {
			t.Fatalf("%s children %d", n.id, len(res.Rows))
		}
	}
}

// system.replica_health surfaces each node's replication position and
// freshness over SQL: the leader is healthy with zero staleness, and a
// follower reports its role, a visible leader, and a bounded contact age.
func TestHAReplicaHealthSurface(t *testing.T) {
	nodes := cluster3(t)
	lead := leader(t, nodes)
	if _, err := lead.db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := lead.db.Session().Exec(`INSERT INTO t (id) VALUES ('a')`); err != nil {
		t.Fatal(err)
	}
	waitAll(t, nodes, uint64(lead.cluster.AppliedLSN()))

	// staleSession so a follower can answer without the STRONG barrier.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := nodesAllFollowersHealthy(nodes); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	for _, n := range nodes {
		res, err := staleSession(t, n).Exec(`SELECT * FROM system.replica_health`)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("%s: %d rows", n.id, len(res.Rows))
		}
		col := map[string]int{}
		for i, c := range res.Columns {
			col[c] = i
		}
		row := res.Rows[0]
		role := row[col["role"]].Str
		hasLeader := row[col["has_leader"]].Bool
		lastContact := row[col["last_contact_ms"]].Dec.String()
		healthy := row[col["healthy"]].Bool
		if !hasLeader {
			t.Fatalf("%s: no leader visible", n.id)
		}
		if n == lead {
			if role != "leader" || !healthy || lastContact != "0" {
				t.Fatalf("%s leader row: %+v", n.id, row)
			}
			continue
		}
		if role != "follower" {
			t.Fatalf("%s: role %q", n.id, role)
		}
		if !healthy {
			t.Fatalf("%s: follower unhealthy: %+v", n.id, row)
		}
	}
}

func nodesAllFollowersHealthy(nodes []*node) (string, bool) {
	for _, n := range nodes {
		if n.cluster.IsLeader() {
			continue
		}
		if !n.cluster.ReplicaHealth().Healthy {
			return n.id, false
		}
	}
	return "", true
}
