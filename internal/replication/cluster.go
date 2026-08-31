package replication

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

const (
	DefaultApplyTimeout  = 2 * time.Second
	DefaultHeartbeat     = 250 * time.Millisecond
	DefaultElection      = 250 * time.Millisecond
	DefaultLeaderLease   = 200 * time.Millisecond
	DefaultCommitTimeout = 50 * time.Millisecond
	MinVotingNodes       = 3
	statusFileName       = "nextsql.cluster.json"
	raftDBName           = "raft.db"
)

// Peer is one voting member.
type Peer struct {
	ID      string
	Address string
}

// Config starts a Raft replica. Transport/stores may be injected for tests.
type Config struct {
	NodeID       string
	Bind         string
	Dir          string
	Peers        []Peer
	Bootstrap    bool
	Keys         crypto.KeyProvider
	ApplyTimeout time.Duration
	// AllowMinority permits fewer than 3 voting peers (tests only).
	AllowMinority bool
	// Inmem uses in-process transport and stores. Tests set this and Transport.
	Inmem     bool
	Transport raft.Transport
	LogStore  raft.LogStore
	Stable    raft.StableStore
	Snaps     raft.SnapshotStore
	Logger    hclog.Logger
}

// Status is a plaintext cluster snapshot. It never contains keys.
type Status struct {
	NodeID    string `json:"node_id"`
	State     string `json:"state"`
	LeaderID  string `json:"leader_id,omitempty"`
	Leader    string `json:"leader_addr,omitempty"`
	Applied   uint64 `json:"applied_lsn"`
	Voters    int    `json:"voters"`
	HasLeader bool   `json:"has_leader"`
	// ApplyBacklog is the count of Raft log entries known committed but not yet
	// applied to this node's FSM (0 on the leader and on a caught-up follower).
	ApplyBacklog uint64 `json:"apply_backlog"`
	// LastContactMS is the age in milliseconds of the last leader contact: 0 on
	// the leader, -1 on a follower that has never heard from a leader.
	LastContactMS int64 `json:"last_contact_ms"`
	// Healthy reports whether this node is a safe follower-read target now.
	Healthy bool `json:"healthy"`
}

// Cluster is one Raft node plus the WAL-batch FSM.
type Cluster struct {
	mu     sync.Mutex
	cfg    Config
	raft   *raft.Raft
	fsm    *fsm
	dek    *crypto.DEK
	keys   crypto.KeyProvider
	trans  raft.Transport
	status string
}

// Open starts Raft. The caller must Close the cluster.
func Open(cfg Config, applier Applier) (*Cluster, error) {
	if cfg.NodeID == "" {
		return nil, nerr.New(nerr.InvalidArgument, "replication.Open", "node id is required")
	}
	if cfg.Keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "replication.Open", "nil key provider")
	}
	if !cfg.AllowMinority && len(cfg.Peers) < MinVotingNodes {
		return nil, nerr.New(nerr.InvalidArgument, "replication.Open", "HA requires at least 3 voting nodes")
	}
	if cfg.ApplyTimeout <= 0 {
		cfg.ApplyTimeout = DefaultApplyTimeout
	}
	dek, err := cfg.Keys.Current()
	if err != nil {
		return nil, err
	}
	rc := raft.DefaultConfig()
	rc.LocalID = raft.ServerID(cfg.NodeID)
	rc.HeartbeatTimeout = DefaultHeartbeat
	rc.ElectionTimeout = DefaultElection
	rc.LeaderLeaseTimeout = DefaultLeaderLease
	rc.CommitTimeout = DefaultCommitTimeout
	rc.SnapshotInterval = 120 * time.Second
	rc.SnapshotThreshold = 1 << 20
	if cfg.Logger != nil {
		rc.Logger = cfg.Logger
	} else {
		rc.Logger = hclog.NewNullLogger()
	}

	logStore := cfg.LogStore
	stable := cfg.Stable
	snaps := cfg.Snaps
	trans := cfg.Transport
	if !cfg.Inmem {
		if cfg.Dir == "" {
			return nil, nerr.New(nerr.InvalidArgument, "replication.Open", "raft directory is required")
		}
		if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
			return nil, nerr.Wrap(nerr.IO, "replication.Open", "mkdir", err)
		}
		if logStore == nil || stable == nil {
			bolt, err := raftboltdb.New(raftboltdb.Options{
				Path: filepath.Join(cfg.Dir, raftDBName),
			})
			if err != nil {
				return nil, nerr.Wrap(nerr.IO, "replication.Open", "raft store", err)
			}
			logStore = bolt
			stable = bolt
		}
		if snaps == nil {
			s, err := raft.NewFileSnapshotStore(cfg.Dir, 2, io.Discard)
			if err != nil {
				return nil, nerr.Wrap(nerr.IO, "replication.Open", "snapshot store", err)
			}
			snaps = s
		}
		if trans == nil {
			if cfg.Bind == "" {
				return nil, nerr.New(nerr.InvalidArgument, "replication.Open", "raft bind address is required")
			}
			advertise, err := net.ResolveTCPAddr("tcp", cfg.Bind)
			if err != nil {
				return nil, nerr.Wrap(nerr.InvalidArgument, "replication.Open", "raft bind", err)
			}
			t, err := raft.NewTCPTransport(cfg.Bind, advertise, 3, 10*time.Second, io.Discard)
			if err != nil {
				return nil, nerr.Wrap(nerr.IO, "replication.Open", "raft transport", err)
			}
			trans = t
		}
	} else {
		if trans == nil {
			return nil, nerr.New(nerr.InvalidArgument, "replication.Open", "in-memory cluster requires a transport")
		}
		if logStore == nil {
			s := raft.NewInmemStore()
			logStore = s
			if stable == nil {
				stable = s
			}
		}
		if stable == nil {
			stable = raft.NewInmemStore()
		}
		if snaps == nil {
			snaps = raft.NewInmemSnapshotStore()
		}
	}

	f := newFSM(cfg.Keys, applier)
	r, err := raft.NewRaft(rc, f, logStore, stable, snaps, trans)
	if err != nil {
		return nil, nerr.Wrap(nerr.Internal, "replication.Open", "raft", err)
	}
	cfg.LogStore = logStore
	cfg.Stable = stable
	cfg.Snaps = snaps
	c := &Cluster{
		cfg:   cfg,
		raft:  r,
		fsm:   f,
		dek:   dek,
		keys:  cfg.Keys,
		trans: trans,
	}
	if cfg.Bootstrap {
		if err := c.bootstrap(); err != nil {
			_ = r.Shutdown().Error()
			return nil, err
		}
	}
	return c, nil
}

func (c *Cluster) bootstrap() error {
	hasState, err := raft.HasExistingState(c.cfg.LogStore, c.cfg.Stable, c.cfg.Snaps)
	if err != nil {
		hasState = false
	}
	if hasState {
		return nil
	}
	addr := c.cfg.Bind
	if c.trans != nil {
		if taddr := string(c.trans.LocalAddr()); taddr != "" {
			addr = taddr
		}
	}
	if addr == "" {
		return nerr.New(nerr.InvalidArgument, "replication.bootstrap", "bind address is required")
	}
	// Bootstrap this node alone so it can become leader (quorum 1), then
	// AddVoter the rest. Bootstrapping all voters at once races with startup.
	f := c.raft.BootstrapCluster(raft.Configuration{
		Servers: []raft.Server{{
			Suffrage: raft.Voter,
			ID:       raft.ServerID(c.cfg.NodeID),
			Address:  raft.ServerAddress(addr),
		}},
	})
	if err := f.Error(); err != nil && err != raft.ErrCantBootstrap {
		return nerr.Wrap(nerr.Internal, "replication.bootstrap", "bootstrap", err)
	}
	return nil
}

// JoinPeers adds the other voting members. Call after every node is listening.
func (c *Cluster) JoinPeers(peers []Peer) error {
	if c == nil {
		return nerr.New(nerr.Unavailable, "replication.JoinPeers", "cluster is closed")
	}
	if _, err := c.WaitForLeader(5 * time.Second); err != nil {
		return err
	}
	if !c.IsLeader() {
		return nil
	}
	for _, p := range peers {
		if p.ID == "" || p.Address == "" || p.ID == c.cfg.NodeID {
			continue
		}
		var last error
		for i := 0; i < 20; i++ {
			last = c.AddVoter(p.ID, p.Address)
			if last == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if last != nil {
			return last
		}
	}
	return nil
}

// Replicate proposes a WAL batch and waits for quorum commit.
func (c *Cluster) Replicate(recs []wal.Record) error {
	if c == nil || c.raft == nil {
		return nerr.New(nerr.Unavailable, "replication.Replicate", "cluster is closed")
	}
	if c.raft.State() != raft.Leader {
		return c.notLeader("replication.Replicate")
	}
	data, err := EncodeCommand(c.dek, recs)
	if err != nil {
		return err
	}
	last := recs[len(recs)-1].LSN
	c.fsm.markLocal(last)
	f := c.raft.Apply(data, c.cfg.ApplyTimeout)
	if err := f.Error(); err != nil {
		if err == raft.ErrNotLeader || err == raft.ErrLeadershipLost || err == raft.ErrEnqueueTimeout {
			return nerr.Wrap(nerr.Unavailable, "replication.Replicate", "quorum commit failed", err)
		}
		return nerr.Wrap(nerr.Internal, "replication.Replicate", "apply", err)
	}
	if resp := f.Response(); resp != nil {
		if e, ok := resp.(error); ok && e != nil {
			return e
		}
	}
	return nil
}

// AllowWrite rejects writes when this node is not the leader or no
// leader can be identified (no split brain).
func (c *Cluster) AllowWrite() error {
	if c == nil || c.raft == nil {
		return nerr.New(nerr.Unavailable, "replication.AllowWrite", "cluster is closed")
	}
	if c.raft.State() == raft.Leader {
		return nil
	}
	return c.notLeader("replication.AllowWrite")
}

func (c *Cluster) notLeader(op string) error {
	addr, id := c.raft.LeaderWithID()
	if addr == "" || id == "" {
		return nerr.New(nerr.Unavailable, op, "no leader")
	}
	return nerr.New(nerr.Unavailable, op, "not the leader")
}

func (c *Cluster) IsLeader() bool {
	return c != nil && c.raft != nil && c.raft.State() == raft.Leader
}

func (c *Cluster) LeaderID() string {
	if c == nil || c.raft == nil {
		return ""
	}
	_, id := c.raft.LeaderWithID()
	return string(id)
}

func (c *Cluster) AppliedLSN() format.LSN {
	if c == nil || c.fsm == nil {
		return 0
	}
	return c.fsm.LastLSN()
}

// WaitForLeader blocks until a leader is known or the timeout expires.
func (c *Cluster) WaitForLeader(d time.Duration) (string, error) {
	if c == nil || c.raft == nil {
		return "", nerr.New(nerr.Unavailable, "replication.WaitForLeader", "cluster is closed")
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_, id := c.raft.LeaderWithID()
		if id != "" {
			return string(id), nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "", nerr.New(nerr.Unavailable, "replication.WaitForLeader", "no leader")
}

// AddVoter adds a voting member. Used for replica repair and rolling join.
func (c *Cluster) AddVoter(id, addr string) error {
	if err := c.AllowWrite(); err != nil {
		return err
	}
	f := c.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, c.cfg.ApplyTimeout)
	if err := f.Error(); err != nil {
		return nerr.Wrap(nerr.Internal, "replication.AddVoter", "add voter", err)
	}
	return nil
}

// RemoveServer drops a member. Used for rolling maintenance.
func (c *Cluster) RemoveServer(id string) error {
	if err := c.AllowWrite(); err != nil {
		return err
	}
	f := c.raft.RemoveServer(raft.ServerID(id), 0, c.cfg.ApplyTimeout)
	if err := f.Error(); err != nil {
		return nerr.Wrap(nerr.Internal, "replication.RemoveServer", "remove", err)
	}
	return nil
}

// TransferLeadership asks the current leader to step down.
func (c *Cluster) TransferLeadership() error {
	if c == nil || c.raft == nil {
		return nerr.New(nerr.Unavailable, "replication.TransferLeadership", "cluster is closed")
	}
	f := c.raft.LeadershipTransfer()
	if err := f.Error(); err != nil && err != raft.ErrNotLeader {
		return nerr.Wrap(nerr.Internal, "replication.TransferLeadership", "transfer", err)
	}
	return nil
}

func (c *Cluster) Voters() int {
	if c == nil || c.raft == nil {
		return 0
	}
	f := c.raft.GetConfiguration()
	if err := f.Error(); err != nil {
		return 0
	}
	n := 0
	for _, s := range f.Configuration().Servers {
		if s.Suffrage == raft.Voter {
			n++
		}
	}
	return n
}

func (c *Cluster) Status() Status {
	st := Status{NodeID: c.cfg.NodeID, State: "shutdown"}
	if c == nil || c.raft == nil {
		return st
	}
	st.State = c.raft.State().String()
	addr, id := c.raft.LeaderWithID()
	st.Leader = string(addr)
	st.LeaderID = string(id)
	st.HasLeader = id != ""
	st.Applied = uint64(c.AppliedLSN())
	st.Voters = c.Voters()
	h := c.ReplicaHealth()
	st.ApplyBacklog = h.ApplyBacklog
	st.Healthy = h.Healthy
	if h.LastContact == NeverContacted {
		st.LastContactMS = -1
	} else {
		st.LastContactMS = h.LastContact.Milliseconds()
	}
	return st
}

// WriteStatus persists a key-free status file next to the data directory.
func (c *Cluster) WriteStatus(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	path := filepath.Join(dataDir, statusFileName)
	body, err := json.MarshalIndent(c.Status(), "", "  ")
	if err != nil {
		return nerr.Wrap(nerr.Internal, "replication.WriteStatus", "encode", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "replication.WriteStatus", "write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nerr.Wrap(nerr.IO, "replication.WriteStatus", "rename", err)
	}
	c.mu.Lock()
	c.status = path
	c.mu.Unlock()
	return nil
}

// ReadStatusFile loads the last written cluster status.
func ReadStatusFile(dataDir string) (Status, error) {
	path := filepath.Join(dataDir, statusFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		return Status{}, nerr.Wrap(nerr.NotFound, "replication.ReadStatusFile", "read", err)
	}
	var st Status
	if err := json.Unmarshal(body, &st); err != nil {
		return Status{}, nerr.New(nerr.InvalidFormat, "replication.ReadStatusFile", "invalid cluster status")
	}
	return st, nil
}

func (c *Cluster) Shutdown() error {
	if c == nil || c.raft == nil {
		return nil
	}
	f := c.raft.Shutdown()
	if err := f.Error(); err != nil {
		return nerr.Wrap(nerr.Internal, "replication.Shutdown", "shutdown", err)
	}
	return nil
}

// ParsePeers reads `id=addr,id=addr` membership lists.
func ParsePeers(s string) ([]Peer, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nerr.New(nerr.InvalidArgument, "replication.ParsePeers", "empty peer list")
	}
	parts := strings.Split(s, ",")
	out := make([]Peer, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, addr, ok := strings.Cut(p, "=")
		if !ok {
			return nil, nerr.New(nerr.InvalidArgument, "replication.ParsePeers", "expected id=addr")
		}
		id = strings.TrimSpace(id)
		addr = strings.TrimSpace(addr)
		if id == "" || addr == "" {
			return nil, nerr.New(nerr.InvalidArgument, "replication.ParsePeers", "peer id and address are required")
		}
		if _, dup := seen[id]; dup {
			return nil, nerr.New(nerr.InvalidArgument, "replication.ParsePeers", "duplicate peer id")
		}
		seen[id] = struct{}{}
		out = append(out, Peer{ID: id, Address: addr})
	}
	if len(out) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "replication.ParsePeers", "empty peer list")
	}
	return out, nil
}

// ReplKeys returns the DomainRepl provider when keys is an Envelope.
func ReplKeys(keys crypto.KeyProvider) crypto.KeyProvider {
	type provider interface {
		Provider(byte) crypto.KeyProvider
	}
	if p, ok := keys.(provider); ok {
		return p.Provider(crypto.DomainRepl)
	}
	return keys
}
