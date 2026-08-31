package bench

import (
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/storage/format"
)

// readScaleNodes is the fixed cluster size for the read-scaling benchmark: one
// leader plus two followers, the minimum voting configuration.
const readScaleNodes = 3

// ReadScaleOptions configures the follower-read scaling benchmark. Encryption,
// WAL, and fsync stay on, matching every other official workload.
type ReadScaleOptions struct {
	Dir         string
	BufferPages int
	Duration    time.Duration
	// Readers is the number of concurrent point-read goroutines pinned to each
	// serving node. The 1-node phases run Readers goroutines; the 3-node phases
	// run 3*Readers so per-node load stays constant and the QPS delta is pure
	// horizontal scaling.
	Readers int
	// Rows seeded into the replicated table. Kept well above the 128-entry
	// result cache so cycled point lookups stay cache-misses and measure real
	// read execution (and, for STRONG, the Raft read-barrier round trip).
	Rows int
}

// ReadScaleReport is one measured phase.
type ReadScaleReport struct {
	Name    string // "strong-1n", "stale-1n", "stale-2n", "stale-3n", "bounded-3n"
	Mode    string // STRONG | STALE | BOUNDED
	Nodes   int    // serving nodes in this phase
	Readers int    // total concurrent reader goroutines
	Ops     int64
	Errors  int64
	Elapsed time.Duration
	QPS     float64
	P50     time.Duration
	P95     time.Duration
	P99     time.Duration
	P999    time.Duration
	// LeaderOps / LeaderQPS is the read work served by the leader in this phase.
	// Follower routing pushes it toward Ops/Nodes; on a leader-only phase it
	// equals the total. This drop is the read-offload the leader gets.
	LeaderOps int64
	LeaderQPS float64
	// AggQPSRatio is this phase's aggregate read QPS over the stale-1n baseline.
	// On a single host aggregate read QPS is CPU-bound and does not grow with
	// node count (every "node" is goroutines on the same cores); the scaling
	// win is the LeaderQPS drop. A real deployment adds a host per replica.
	AggQPSRatio float64
	Hardware    Hardware
}

// ReadScaleSuite is a labeled read-scaling run.
type ReadScaleSuite struct {
	Hardware Hardware
	Reports  []ReadScaleReport
}

// rsNode is one in-process cluster member.
type rsNode struct {
	id      string
	db      *executor.DB
	cluster *replication.Cluster
}

// RunReadScale measures how follower reads scale read throughput across a
// 3-node single-leader cluster. It reports:
//
//   - strong-1n:  STRONG reads on the leader — every read pays a Raft
//     VerifyLeader quorum round trip. This is the read-barrier cost.
//   - stale-1n:   STALE reads on the leader — no barrier; the scaling baseline.
//   - stale-2n:   STALE reads spread over the leader and one follower.
//   - stale-3n:   STALE reads spread over all three members.
//   - bounded-3n: BOUNDED reads over all three, behind the freshness gate.
//
// AggQPSRatio is each phase's aggregate read QPS over the stale-1n QPS;
// LeaderQPS is the slice of that served by the leader.
func RunReadScale(opt ReadScaleOptions) (*ReadScaleSuite, error) {
	if opt.Dir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "bench.RunReadScale", "dir is required")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 256
	}
	if opt.Duration <= 0 {
		opt.Duration = time.Second
	}
	if opt.Readers < 1 {
		opt.Readers = 8
	}
	if opt.Rows < 512 {
		opt.Rows = 5000
	}

	nodes, err := startReadScaleCluster(opt)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, n := range nodes {
			if n.cluster != nil {
				_ = n.cluster.Shutdown()
			}
			if n.db != nil {
				_ = n.db.Close()
			}
		}
	}()

	leader, followers, err := readScaleRoles(nodes)
	if err != nil {
		return nil, err
	}

	if err := seedReadScale(leader, opt.Rows); err != nil {
		return nil, err
	}
	if err := waitReadScaleReplicated(followers, opt.Rows, 15*time.Second); err != nil {
		return nil, err
	}

	hw := detectHardware(opt.Dir, opt.Rows, opt.Readers, opt.BufferPages)
	hw.Concurrency = opt.Readers
	suite := &ReadScaleSuite{Hardware: hw}

	all := append([]*rsNode{leader}, followers...)
	phases := []struct {
		name    string
		mode    executor.ReadConsistency
		modeStr string
		serving []*rsNode
	}{
		{"strong-1n", executor.ReadStrong, "STRONG", []*rsNode{leader}},
		{"stale-1n", executor.ReadStale, "STALE", []*rsNode{leader}},
		{"stale-2n", executor.ReadStale, "STALE", []*rsNode{leader, followers[0]}},
		{"stale-3n", executor.ReadStale, "STALE", all},
		{"bounded-3n", executor.ReadBounded, "BOUNDED", all},
	}

	var baseline float64
	for _, p := range phases {
		rep := runReadScalePhase(p.name, p.modeStr, p.mode, leader, p.serving, opt, hw)
		if p.name == "stale-1n" {
			baseline = rep.QPS
		}
		if baseline > 0 {
			rep.AggQPSRatio = rep.QPS / baseline
		}
		suite.Reports = append(suite.Reports, rep)
	}
	return suite, nil
}

// runReadScalePhase drives opt.Readers point-read goroutines against every node
// in serving for opt.Duration and returns the aggregate measurement, tracking
// how much of the read work landed on the leader.
func runReadScalePhase(name, modeStr string, mode executor.ReadConsistency, leader *rsNode, serving []*rsNode, opt ReadScaleOptions, hw Hardware) ReadScaleReport {
	total := opt.Readers * len(serving)
	var (
		ops       atomic.Int64
		leaderOps atomic.Int64
		errs      atomic.Int64
		mu        sync.Mutex
		lat       []int64
		wg        sync.WaitGroup
	)
	start := time.Now()
	stop := start.Add(opt.Duration)

	for si, node := range serving {
		isLeader := node == leader
		for r := 0; r < opt.Readers; r++ {
			wg.Add(1)
			// Stagger each reader's starting key so concurrent readers on a
			// node touch different pages and different cache slots.
			base := (si*opt.Readers + r) * 97
			go func(n *rsNode, base int, isLeader bool) {
				defer wg.Done()
				s := n.db.Session()
				s.SetLimits(sloLimits())
				_ = s.SetReadConsistency(mode)
				if mode == executor.ReadBounded {
					s.SetMaxStaleness(time.Minute)
				}
				for i := 0; time.Now().Before(stop); i++ {
					key := (base + i) % opt.Rows
					t0 := time.Now()
					_, err := s.Exec(fmt.Sprintf(`SELECT n FROM kv WHERE id = 'k%d'`, key))
					d := time.Since(t0)
					if err != nil {
						errs.Add(1)
						continue
					}
					ops.Add(1)
					if isLeader {
						leaderOps.Add(1)
					}
					mu.Lock()
					lat = append(lat, d.Nanoseconds())
					mu.Unlock()
				}
			}(node, base, isLeader)
		}
	}

	wg.Wait()
	elapsed := time.Since(start)

	rep := ReadScaleReport{
		Name:      name,
		Mode:      modeStr,
		Nodes:     len(serving),
		Readers:   total,
		Ops:       ops.Load(),
		Errors:    errs.Load(),
		Elapsed:   elapsed,
		LeaderOps: leaderOps.Load(),
		Hardware:  hw,
	}
	if elapsed.Seconds() > 0 {
		rep.QPS = float64(rep.Ops) / elapsed.Seconds()
		rep.LeaderQPS = float64(rep.LeaderOps) / elapsed.Seconds()
	}
	rep.P50, rep.P95, rep.P99, rep.P999 = latencyPct(lat)
	return rep
}

func seedReadScale(leader *rsNode, rows int) error {
	s := leader.db.Session()
	s.SetLimits(sloLimits())
	if err := mustExec(s, `CREATE TABLE kv (id STRING PRIMARY KEY, n DECIMAL(12,2) NOT NULL, note TEXT)`); err != nil {
		return err
	}
	return insertBatches(s, rows, 512, 4096, func(start, end int) string {
		var b []byte
		b = append(b, `INSERT INTO kv (id, n, note) VALUES `...)
		for i := start; i < end; i++ {
			if i > start {
				b = append(b, ',')
			}
			b = append(b, fmt.Sprintf(`('k%d', %d, 'row %d')`, i, i, i)...)
		}
		return string(b)
	})
}

// waitReadScaleReplicated blocks until every follower serves the full row set
// from its locally applied state (STALE read, no barrier).
func waitReadScaleReplicated(followers []*rsNode, rows int, timeout time.Duration) error {
	want := strconv.Itoa(rows)
	deadline := time.Now().Add(timeout)
	for _, f := range followers {
		s := f.db.Session()
		s.SetLimits(sloLimits())
		_ = s.SetReadConsistency(executor.ReadStale)
		for {
			res, err := s.Exec(`SELECT COUNT(*) FROM kv`)
			if err == nil && res != nil && len(res.Rows) == 1 && len(res.Rows[0]) == 1 && res.Rows[0][0].String() == want {
				break
			}
			if time.Now().After(deadline) {
				return nerr.New(nerr.Unavailable, "bench.RunReadScale", "followers did not replicate the seed set in time")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	return nil
}

func readScaleRoles(nodes []*rsNode) (leader *rsNode, followers []*rsNode, err error) {
	for _, n := range nodes {
		if n.cluster.IsLeader() {
			leader = n
		} else {
			followers = append(followers, n)
		}
	}
	if leader == nil || len(followers) != readScaleNodes-1 {
		return nil, nil, nerr.New(nerr.Unavailable, "bench.RunReadScale", "cluster has no single leader")
	}
	return leader, followers, nil
}

// startReadScaleCluster brings up a 3-node in-process Raft cluster of encrypted
// executor databases and waits for a stable 3-voter configuration.
func startReadScaleCluster(opt ReadScaleOptions) ([]*rsNode, error) {
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return nil, err
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		return nil, err
	}
	ident, err := format.NewIdentity()
	if err != nil {
		return nil, err
	}

	addrs := make([]raft.ServerAddress, readScaleNodes)
	trans := make([]*raft.InmemTransport, readScaleNodes)
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
	peers := make([]replication.Peer, readScaleNodes)
	for i := range peers {
		peers[i] = replication.Peer{ID: fmt.Sprintf("n%d", i+1), Address: string(addrs[i])}
	}

	nodes := make([]*rsNode, readScaleNodes)
	for i := 0; i < readScaleNodes; i++ {
		db, err := executor.CreateWithIdentity(filepath.Join(opt.Dir, fmt.Sprintf("n%d", i+1), "nextsql.db"), ident, keys, opt.BufferPages)
		if err != nil {
			return nil, err
		}
		db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{
			MaxInflight: opt.Readers*2 + 4,
			MaxQueue:    opt.Readers * 8,
			QueueWait:   5 * time.Second,
		}))
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
			_ = db.Close()
			return nil, err
		}
		db.AttachCluster(cl)
		nodes[i] = &rsNode{id: peers[i].ID, db: db, cluster: cl}
	}

	if _, err := nodes[0].cluster.WaitForLeader(5 * time.Second); err != nil {
		return nil, err
	}
	if err := nodes[0].cluster.JoinPeers(peers); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if nodes[0].cluster.Voters() >= readScaleNodes {
			return nodes, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, nerr.New(nerr.Unavailable, "bench.RunReadScale", "cluster did not reach 3 voters")
}
