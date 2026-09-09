package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/limits"
	"github.com/bzync/nextsql/internal/nerr"
)

func writeConf(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nextsql.conf")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The four Raft intervals are configurable from a file and survive a
// Marshal/Load round trip, so a value an operator sets is the value the server
// reports back.
func TestRaftTimingsRoundTrip(t *testing.T) {
	cfg, err := Load(writeConf(t, strings.Join([]string{
		"raft_heartbeat_ms=1000",
		"raft_election_ms=2000",
		"raft_leader_lease_ms=800",
		"raft_commit_timeout_ms=120",
	}, "\n")+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RaftHeartbeatMS != 1000 || cfg.RaftElectionMS != 2000 ||
		cfg.RaftLeaderLeaseMS != 800 || cfg.RaftCommitTimeoutMS != 120 {
		t.Fatalf("parsed raft timings: %+v", cfg)
	}
	again, err := Load(writeConf(t, string(cfg.Marshal())))
	if err != nil {
		t.Fatal(err)
	}
	if again.RaftHeartbeatMS != cfg.RaftHeartbeatMS || again.RaftElectionMS != cfg.RaftElectionMS ||
		again.RaftLeaderLeaseMS != cfg.RaftLeaderLeaseMS || again.RaftCommitTimeoutMS != cfg.RaftCommitTimeoutMS {
		t.Fatalf("round trip lost raft timings: %+v", again)
	}
}

// An out-of-range interval is refused where it is written, naming the accepted
// range, rather than reaching the cluster.
func TestRaftTimingOutOfRangeRejectedAtLoad(t *testing.T) {
	for _, body := range []string{
		"raft_heartbeat_ms=1\n",
		"raft_election_ms=600000\n",
		"raft_leader_lease_ms=-5\n",
	} {
		if _, err := Load(writeConf(t, body)); err == nil {
			t.Fatalf("accepted %q", strings.TrimSpace(body))
		} else if !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("want invalid-argument for %q, got %v", strings.TrimSpace(body), err)
		}
	}
}

// The relationships between the intervals are enforced at configuration time.
// An operator who writes a lease longer than the heartbeat learns now, not at
// the restart where the cluster refuses to start.
func TestRaftTimingRelationshipsRejectedAtValidate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"lease past heartbeat", func(c *Config) {
			c.RaftHeartbeatMS, c.RaftLeaderLeaseMS = 300, 900
		}, "raft_leader_lease_ms"},
		{"election under heartbeat", func(c *Config) {
			c.RaftHeartbeatMS, c.RaftElectionMS, c.RaftLeaderLeaseMS = 900, 300, 200
		}, "raft_election_ms"},
		// Only the heartbeat is raised: the election default (250 ms) is now
		// below it, so a partially configured set must be judged as it will
		// actually run rather than as though the unset half did not exist.
		{"raised heartbeat against a defaulted election", func(c *Config) {
			c.RaftHeartbeatMS = 5000
		}, "raft_election_ms"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.NodeID, cfg.RaftBind = "n1", "127.0.0.1:7300"
			tc.mut(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("accepted an invalid interval set")
			}
			if !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("want invalid-argument, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error does not name %s: %v", tc.want, err)
			}
		})
	}
}

// A configuration that sets none of them stays valid, and a coherent set is
// accepted.
func TestRaftTimingDefaultsAndValidSetAccepted(t *testing.T) {
	cfg := Default()
	cfg.NodeID, cfg.RaftBind = "n1", "127.0.0.1:7300"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unconfigured raft timings rejected: %v", err)
	}
	cfg.RaftHeartbeatMS = 2000
	cfg.RaftElectionMS = 4000
	cfg.RaftLeaderLeaseMS = 1500
	cfg.RaftCommitTimeoutMS = 200
	if err := cfg.Validate(); err != nil {
		t.Fatalf("coherent raft timings rejected: %v", err)
	}
}

// The catalog carries all four keys, so they are range-checked, documented and
// visible to an operator reading docs/limits.md rather than being undocumented
// constants with a flag in front of them.
func TestRaftTimingsAreCatalogued(t *testing.T) {
	for _, key := range []string{
		limits.RaftTimingKeys.Heartbeat,
		limits.RaftTimingKeys.Election,
		limits.RaftTimingKeys.LeaderLease,
		limits.RaftTimingKeys.CommitTimeout,
	} {
		s, ok := limits.Lookup(key)
		if !ok {
			t.Fatalf("%s is not in the limit catalog", key)
		}
		if s.Class != limits.ClassTime || s.Unit != limits.UnitMillis {
			t.Fatalf("%s is catalogued as %s/%s", key, s.Class, s.Unit)
		}
		if !s.AcceptsZero() {
			t.Fatalf("%s must accept zero as leave-the-default", key)
		}
		cfg := Default()
		if v, ok := cfg.limitValue(key); !ok || v != 0 {
			t.Fatalf("%s is not reachable from Config.limitValue (got %d, ok=%v)", key, v, ok)
		}
	}
}

// SET CONFIG goes through the same relationship check, so an operator cannot
// write a combination through SQL that the server would then refuse to restart
// on.
func TestSetConfigEnforcesRaftTimingRelationships(t *testing.T) {
	base := Default()
	base.NodeID, base.RaftBind = "n1", "127.0.0.1:7300"

	// A lease past the default heartbeat is refused.
	if _, err := base.WithSetting("raft_leader_lease_ms", "900", false); err == nil {
		t.Fatal("SET CONFIG accepted a lease longer than the heartbeat")
	} else if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid-argument, got %v", err)
	}

	// Raising the heartbeat alone leaves the defaulted election below it.
	if _, err := base.WithSetting("raft_heartbeat_ms", "5000", false); err == nil {
		t.Fatal("SET CONFIG accepted a heartbeat above the defaulted election")
	}

	// Raising the election first, then the heartbeat, is accepted, and the
	// value survives into the resulting config.
	next, err := base.WithSetting("raft_election_ms", "5000", false)
	if err != nil {
		t.Fatal(err)
	}
	next, err = next.WithSetting("raft_heartbeat_ms", "5000", false)
	if err != nil {
		t.Fatalf("SET CONFIG refused a coherent set: %v", err)
	}
	if next.RaftHeartbeatMS != 5000 || next.RaftElectionMS != 5000 {
		t.Fatalf("SET CONFIG lost the values: %+v", next)
	}

	// DEFAULT resets it back to unset.
	back, err := next.WithSetting("raft_heartbeat_ms", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if back.RaftHeartbeatMS != 0 {
		t.Fatalf("reset left raft_heartbeat_ms = %d", back.RaftHeartbeatMS)
	}
}
