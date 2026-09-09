package replication

import (
	"fmt"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/limits"
	"github.com/bzync/nextsql/internal/nerr"
)

// Raft protocol intervals. These are the values a cluster runs with when the
// operator configures none; every one of them is tunable (raft_heartbeat_ms,
// raft_election_ms, raft_leader_lease_ms, raft_commit_timeout_ms), and the
// accepted range for each is in internal/limits.
const (
	DefaultHeartbeat     = limits.DefaultRaftHeartbeatMS * time.Millisecond
	DefaultElection      = limits.DefaultRaftElectionMS * time.Millisecond
	DefaultLeaderLease   = limits.DefaultRaftLeaderLeaseMS * time.Millisecond
	DefaultCommitTimeout = limits.DefaultRaftCommitTimeoutMS * time.Millisecond
)

// healthyContactHeartbeats is how many heartbeats a follower may miss before
// it is treated as potentially partitioned and arbitrarily stale. It is
// generous enough to ride out a single election without flapping: an election
// costs one election timeout plus the round it takes to install a leader, and
// the election timeout is never below the heartbeat (limits.CheckRaftTimings).
//
// It is a multiplier rather than a duration because the freshness model is
// expressed in heartbeats, not in milliseconds. An operator who raises
// raft_heartbeat_ms for a slow link has said contact is expected less often;
// a fixed window would then judge every follower unhealthy between heartbeats.
const healthyContactHeartbeats = 5

// Timings is one cluster's resolved Raft intervals. The zero value means
// "unconfigured": Resolve fills each zero field with its default, so a
// partially configured set runs with defaults for the rest.
type Timings struct {
	Heartbeat     time.Duration
	Election      time.Duration
	LeaderLease   time.Duration
	CommitTimeout time.Duration
}

// DefaultTimings returns the intervals a cluster runs with when the operator
// configures none.
func DefaultTimings() Timings {
	return Timings{
		Heartbeat:     DefaultHeartbeat,
		Election:      DefaultElection,
		LeaderLease:   DefaultLeaderLease,
		CommitTimeout: DefaultCommitTimeout,
	}
}

// Resolve fills every unset (zero) interval with its default and validates the
// result — both the individual ranges and the two relationships between them
// (see limits.CheckRaftTimings). The returned Timings is what the cluster runs
// with; a rejected set returns an invalid-argument error and no cluster.
//
// Every interval must be a whole, positive number of milliseconds. That is not
// pedantry: the whole configuration surface is milliseconds, and anything finer
// would have to be truncated to be range-checked, which is how a set this
// function accepts becomes one raft.ValidateConfig rejects — a 250.9 ms
// heartbeat and a 250.5 ms election both truncate to 250 and compare equal here
// while Raft still sees an election shorter than its heartbeat. A negative
// duration is refused for the same reason rather than read as unconfigured.
func (t Timings) Resolve() (Timings, error) {
	d := DefaultTimings()
	fields := []struct {
		key string
		v   *time.Duration
		def time.Duration
	}{
		{limits.RaftTimingKeys.Heartbeat, &t.Heartbeat, d.Heartbeat},
		{limits.RaftTimingKeys.Election, &t.Election, d.Election},
		{limits.RaftTimingKeys.LeaderLease, &t.LeaderLease, d.LeaderLease},
		{limits.RaftTimingKeys.CommitTimeout, &t.CommitTimeout, d.CommitTimeout},
	}
	for _, f := range fields {
		if *f.v == 0 {
			*f.v = f.def
			continue
		}
		if *f.v < 0 {
			return Timings{}, nerr.New(nerr.InvalidArgument, "replication.Timings.Resolve",
				fmt.Sprintf("%s is negative (%v); use 0 to leave the default", f.key, *f.v))
		}
		if *f.v%time.Millisecond != 0 {
			return Timings{}, nerr.New(nerr.InvalidArgument, "replication.Timings.Resolve",
				fmt.Sprintf("%s (%v) must be a whole number of milliseconds", f.key, *f.v))
		}
	}
	if err := limits.CheckRaftTimings(
		millis(t.Heartbeat), millis(t.Election),
		millis(t.LeaderLease), millis(t.CommitTimeout),
	); err != nil {
		return Timings{}, err
	}
	return t, nil
}

// applyTo installs these intervals on a Raft configuration. It is the single
// place the four fields are mapped, so a test can prove the mapping rather than
// only that Open accepted the numbers — a heartbeat written into
// ElectionTimeout would otherwise be invisible until a cluster misbehaved.
func (t Timings) applyTo(rc *raft.Config) {
	rc.HeartbeatTimeout = t.Heartbeat
	rc.ElectionTimeout = t.Election
	rc.LeaderLeaseTimeout = t.LeaderLease
	rc.CommitTimeout = t.CommitTimeout
}

// HealthyContactWindow is how long this cluster's followers may go without
// leader contact before ReplicaHealth reports them unhealthy and
// FollowerReadHealthy refuses to serve a bounded read from them.
//
// It is derived from the configured heartbeat, so raising raft_heartbeat_ms
// widens the freshness window with it rather than leaving the health model
// judging every follower unhealthy between heartbeats.
func (t Timings) HealthyContactWindow() time.Duration {
	if t.Heartbeat <= 0 {
		return healthyContactHeartbeats * DefaultHeartbeat
	}
	return healthyContactHeartbeats * t.Heartbeat
}

// millis converts a whole-millisecond duration to its millisecond count.
// Resolve has already refused anything that is not one, so this is exact.
func millis(d time.Duration) int { return int(d / time.Millisecond) }
