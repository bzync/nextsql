package executor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/cdc"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

func TestApplyReplicatedSQL(t *testing.T) {
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
	src, err := CreateWithIdentity(filepath.Join(t.TempDir(), "src.db"), ident, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	s := src.Session()
	if _, err := s.Exec(`CREATE TABLE t (id STRING PRIMARY KEY, n DECIMAL(10,0))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO t (id, n) VALUES ('k', 4)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE WORKFLOW put_t(id STRING, n DECIMAL(10,0)) AS BEGIN INSERT INTO t (id, n) VALUES ($id, $n); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`RUN WORKFLOW put_t('workflow', 7)`); err != nil {
		t.Fatal(err)
	}
	recs, _, err := src.Eng.WAL.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("expected WAL records")
	}

	dst, err := CreateWithIdentity(filepath.Join(t.TempDir(), "dst.db"), ident, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ApplyRecords(recs); err != nil {
		t.Fatal(err)
	}
	res, err := dst.Session().Exec(`SELECT id, n FROM t WHERE id = 'k'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "k" {
		t.Fatalf("replica rows %+v", res.Rows)
	}
	res, err = dst.Session().Exec(`SELECT n FROM t WHERE id = 'workflow'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Dec.String() != "7" {
		t.Fatalf("replicated workflow effects %+v", res.Rows)
	}
	if _, err := dst.Session().Exec(`RUN WORKFLOW put_t('catalog', 9)`); err != nil {
		t.Fatalf("replicated workflow catalog: %v", err)
	}
}

func TestApplyReplicatedUpsert(t *testing.T) {
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
	src, err := CreateWithIdentity(filepath.Join(t.TempDir(), "src.db"), ident, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	s := src.Session()
	if _, err := s.Exec(`CREATE TABLE t (id STRING PRIMARY KEY, n DECIMAL(10,0))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPSERT INTO t (id, n) VALUES ('k', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPSERT INTO t (id, n) VALUES ('k', 9)`); err != nil {
		t.Fatal(err)
	}
	recs, _, err := src.Eng.WAL.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := CreateWithIdentity(filepath.Join(t.TempDir(), "dst.db"), ident, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ApplyRecords(recs); err != nil {
		t.Fatal(err)
	}
	res, err := dst.Session().Exec(`SELECT n FROM t WHERE id = 'k'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Dec.String() != "9" {
		t.Fatalf("replica upsert %+v", res.Rows)
	}
}

func TestDropIndexReplicatesThroughRaft(t *testing.T) {
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

	const nodes = 3
	dbs := make([]*DB, nodes)
	addrs := make([]raft.ServerAddress, nodes)
	transports := make([]*raft.InmemTransport, nodes)
	for i := 0; i < nodes; i++ {
		dbs[i], err = CreateWithIdentity(filepath.Join(t.TempDir(), "node.db"), ident, keys, 16)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = dbs[i].Close() })
		addr, transport := raft.NewInmemTransport("")
		addrs[i], transports[i] = addr, transport
	}
	for i := range transports {
		for j := range transports {
			if i != j {
				transports[i].Connect(addrs[j], transports[j])
			}
		}
	}
	peers := make([]replication.Peer, nodes)
	for i := range peers {
		peers[i] = replication.Peer{ID: "n" + string(rune('1'+i)), Address: string(addrs[i])}
	}
	clusters := make([]*replication.Cluster, nodes)
	for i := range clusters {
		clusters[i], err = replication.Open(replication.Config{
			NodeID:       peers[i].ID,
			Peers:        peers,
			Bootstrap:    i == 0,
			Keys:         keys,
			Inmem:        true,
			Transport:    transports[i],
			ApplyTimeout: 3 * time.Second,
		}, dbs[i])
		if err != nil {
			t.Fatal(err)
		}
		dbs[i].AttachCluster(clusters[i])
		t.Cleanup(func() { _ = clusters[i].Shutdown() })
	}
	if _, err := clusters[0].WaitForLeader(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := clusters[0].JoinPeers(peers); err != nil {
		t.Fatal(err)
	}

	leader := -1
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for i, cluster := range clusters {
			if cluster.IsLeader() && cluster.Voters() == nodes {
				leader = i
				break
			}
		}
		if leader >= 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if leader < 0 {
		t.Fatal("three-node cluster did not elect a leader")
	}
	s := dbs[leader].Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	waitForReplicatedIndex(t, dbs, true)
	execOK(t, s, `DROP INDEX ix_n`)
	waitForReplicatedIndex(t, dbs, false)
}

func TestRebuildIndexReplicatesThroughRaft(t *testing.T) {
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
	const nodes = 3
	dbs := make([]*DB, nodes)
	addrs := make([]raft.ServerAddress, nodes)
	transports := make([]*raft.InmemTransport, nodes)
	for i := 0; i < nodes; i++ {
		dbs[i], err = CreateWithIdentity(filepath.Join(t.TempDir(), "node.db"), ident, keys, 16)
		if err != nil {
			t.Fatal(err)
		}
		i := i
		t.Cleanup(func() { _ = dbs[i].Close() })
		addr, transport := raft.NewInmemTransport("")
		addrs[i], transports[i] = addr, transport
	}
	for i := range transports {
		for j := range transports {
			if i != j {
				transports[i].Connect(addrs[j], transports[j])
			}
		}
	}
	peers := make([]replication.Peer, nodes)
	for i := range peers {
		peers[i] = replication.Peer{ID: "r" + string(rune('1'+i)), Address: string(addrs[i])}
	}
	clusters := make([]*replication.Cluster, nodes)
	for i := range clusters {
		clusters[i], err = replication.Open(replication.Config{
			NodeID: peers[i].ID, Peers: peers, Bootstrap: i == 0, Keys: keys,
			Inmem: true, Transport: transports[i], ApplyTimeout: 3 * time.Second,
		}, dbs[i])
		if err != nil {
			t.Fatal(err)
		}
		dbs[i].AttachCluster(clusters[i])
		i := i
		t.Cleanup(func() { _ = clusters[i].Shutdown() })
	}
	if _, err := clusters[0].WaitForLeader(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := clusters[0].JoinPeers(peers); err != nil {
		t.Fatal(err)
	}
	leader := -1
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for i, cluster := range clusters {
			if cluster.IsLeader() && cluster.Voters() == nodes {
				leader = i
				break
			}
		}
		if leader >= 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if leader < 0 {
		t.Fatal("three-node cluster did not elect a leader")
	}
	s := dbs[leader].Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('1', 'a'), ('2', 'b')`)
	waitForReplicatedIndex(t, dbs, true)
	before := replicatedIndexMeta(t, dbs[leader])
	execOK(t, s, `REBUILD INDEX ix_n`)
	waitForReplicatedRebuild(t, dbs, before)
}

func TestWorkflowReplicatesAndSurvivesRaftFailover(t *testing.T) {
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
	const nodes = 3
	dbs := make([]*DB, nodes)
	addrs := make([]raft.ServerAddress, nodes)
	transports := make([]*raft.InmemTransport, nodes)
	for i := 0; i < nodes; i++ {
		dbs[i], err = CreateWithIdentity(filepath.Join(t.TempDir(), "node.db"), ident, keys, 16)
		if err != nil {
			t.Fatal(err)
		}
		i := i
		t.Cleanup(func() { _ = dbs[i].Close() })
		addr, transport := raft.NewInmemTransport("")
		addrs[i], transports[i] = addr, transport
	}
	for i := range transports {
		for j := range transports {
			if i != j {
				transports[i].Connect(addrs[j], transports[j])
			}
		}
	}
	peers := make([]replication.Peer, nodes)
	for i := range peers {
		peers[i] = replication.Peer{ID: "w" + string(rune('1'+i)), Address: string(addrs[i])}
	}
	clusters := make([]*replication.Cluster, nodes)
	for i := range clusters {
		clusters[i], err = replication.Open(replication.Config{
			NodeID: peers[i].ID, Peers: peers, Bootstrap: i == 0, Keys: keys,
			Inmem: true, Transport: transports[i], ApplyTimeout: 3 * time.Second,
		}, dbs[i])
		if err != nil {
			t.Fatal(err)
		}
		dbs[i].AttachCluster(clusters[i])
		i := i
		t.Cleanup(func() { _ = clusters[i].Shutdown() })
	}
	if _, err := clusters[0].WaitForLeader(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := clusters[0].JoinPeers(peers); err != nil {
		t.Fatal(err)
	}
	leader := waitForWorkflowLeader(t, clusters, -1)
	s := dbs[leader].Session()
	execOK(t, s, `CREATE TABLE jobs (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE job_audit (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW put_job(id STRING) AS BEGIN INSERT INTO jobs (id) VALUES ($id); END`)
	execOK(t, s, `CREATE WORKFLOW audit_job(id STRING) AS BEGIN INSERT INTO job_audit (id) VALUES ($id); END`)
	execOK(t, s, `CREATE TRIGGER audit_job_insert AFTER INSERT ON jobs FOR EACH ROW RUN WORKFLOW audit_job(NEW.id)`)
	execOK(t, s, `CREATE SCHEDULE put_job_hourly EVERY '1h' RUN WORKFLOW put_job('scheduled')`)
	execOK(t, s, `RUN WORKFLOW put_job('before-failover')`)
	waitForWorkflowReplica(t, dbs, "before-failover", -1)
	schedule, ok := dbs[leader].schedule("put_job_hourly")
	if !ok {
		t.Fatal("leader schedule missing")
	}
	firstDue := schedule.NextFireNS
	if got, err := dbs[leader].DispatchDueSchedules(context.Background(), time.Unix(0, firstDue), 8); err != nil || got != 1 {
		t.Fatalf("leader dispatch got=%d err=%v", got, err)
	}
	firstTaskID := scheduledTaskID(schedule.ID, firstDue)
	waitForTaskReplicaState(t, dbs, firstTaskID, catalog.TaskPending, -1)
	claimed, err := dbs[leader].ClaimDueTasks(context.Background(), time.Unix(0, firstDue), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("leader claim count=%d err=%v", len(claimed), err)
	}
	if err := dbs[leader].executeClaimedTask(context.Background(), claimed[0], nil, nil, scheduler.DefaultLimits(), func() time.Time { return time.Unix(0, firstDue+1) }); err != nil {
		t.Fatal(err)
	}
	waitForTaskReplicaState(t, dbs, firstTaskID, catalog.TaskSucceeded, -1)
	waitForWorkflowReplica(t, dbs, "scheduled", -1)
	resume := dbs[leader].Eng.WAL.DurableLSN()

	if err := clusters[leader].Shutdown(); err != nil {
		t.Fatal(err)
	}
	newLeader := waitForWorkflowLeader(t, clusters, leader)
	if got := execOK(t, dbs[newLeader].Session(), `RUN WORKFLOW put_job('after-failover')`).Affected; got != 1 {
		t.Fatalf("affected=%d", got)
	}
	waitForWorkflowReplica(t, dbs, "after-failover", leader)
	sub, err := cdc.Subscribe(dbs[newLeader].Eng.WAL, resume, cdc.Filter{Tables: map[string]struct{}{"jobs": {}}}, cdc.Limits{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	cdcCtx, cdcCancel := context.WithTimeout(context.Background(), 3*time.Second)
	change, err := sub.Next(cdcCtx)
	cdcCancel()
	if err != nil || len(change.Events) != 1 || change.Events[0].Operation != wal.ChangeInsert || change.Token <= resume {
		t.Fatalf("CDC resume after failover: change=%+v err=%v", change, err)
	}
	schedule, ok = dbs[newLeader].schedule("put_job_hourly")
	if !ok || schedule.NextFireNS <= firstDue {
		t.Fatalf("failover schedule=%+v ok=%v", schedule, ok)
	}
	secondDue := schedule.NextFireNS
	if got, err := dbs[newLeader].DispatchDueSchedules(context.Background(), time.Unix(0, secondDue), 8); err != nil || got != 1 {
		t.Fatalf("new leader dispatch got=%d err=%v", got, err)
	}
	waitForTaskReplicaState(t, dbs, scheduledTaskID(schedule.ID, secondDue), catalog.TaskPending, leader)
}

func waitForWorkflowLeader(t *testing.T, clusters []*replication.Cluster, exclude int) int {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for i, cluster := range clusters {
			if i != exclude && cluster.IsLeader() {
				return i
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("workflow cluster did not elect a leader")
	return -1
}

func waitForWorkflowReplica(t *testing.T, dbs []*DB, id string, exclude int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for i, db := range dbs {
			if i == exclude {
				continue
			}
			if _, ok := db.workflow("put_job"); !ok {
				all = false
				break
			}
			if _, ok := db.trigger("audit_job_insert"); !ok {
				all = false
				break
			}
			if schedule, ok := db.schedule("put_job_hourly"); !ok || schedule.WorkflowID == 0 {
				all = false
				break
			}
			res, err := db.Session().Exec(`SELECT id FROM jobs WHERE id = '` + id + `'`)
			if err != nil || len(res.Rows) != 1 {
				all = false
				break
			}
			res, err = db.Session().Exec(`SELECT id FROM job_audit WHERE id = '` + id + `'`)
			if err != nil || len(res.Rows) != 1 {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("workflow %q did not converge on every replica", id)
}

func waitForTaskReplicaState(t *testing.T, dbs []*DB, id string, state catalog.TaskState, exclude int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for i, db := range dbs {
			if i == exclude {
				continue
			}
			task, ok, err := db.task(id)
			if err != nil || !ok || task.IdempotencyKey != id || task.State != state {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not replicate", id)
}

func replicatedIndexMeta(t *testing.T, db *DB) format.PageID {
	t.Helper()
	tab, ok := db.Cat.Get("t")
	if !ok {
		t.Fatal("replicated table missing")
	}
	for _, idx := range tab.Indexes {
		if idx.Name == "ix_n" {
			return idx.Meta
		}
	}
	t.Fatal("replicated index missing")
	return 0
}

func waitForReplicatedRebuild(t *testing.T, dbs []*DB, before format.PageID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		want := replicatedIndexMeta(t, dbs[0])
		if want != 0 && want != before {
			matched := true
			for _, db := range dbs[1:] {
				if replicatedIndexMeta(t, db) != want {
					matched = false
					break
				}
			}
			if matched {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rebuilt index metadata did not converge on every follower")
}

func waitForReplicatedIndex(t *testing.T, dbs []*DB, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		matched := true
		for _, db := range dbs {
			tab, ok := db.Cat.Get("t")
			if !ok {
				matched = false
				break
			}
			found := false
			for _, idx := range tab.Indexes {
				found = found || idx.Name == "ix_n"
			}
			if found != want {
				matched = false
				break
			}
		}
		if matched {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	states := make([]bool, len(dbs))
	for i, db := range dbs {
		if tab, ok := db.Cat.Get("t"); ok {
			for _, idx := range tab.Indexes {
				states[i] = states[i] || idx.Name == "ix_n"
			}
		}
	}
	t.Fatalf("replicated index presence did not become %v: states=%v", want, states)
}
