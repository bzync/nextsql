package setup

import "fmt"

// This file is the decision logic for integrating an in-place upgrade with a
// Raft-clustered deployment's rolling-upgrade procedure. Like the rest of the
// package it performs no I/O: the command layer stats the data directory for
// a raft/ state directory, reads the key-free cluster status file, and parses
// the config, then hands the observations here.
//
// The offline in-place upgrade of one stopped node is safe on its own — it
// runs WAL recovery only and the Raft log replays on restart, touching no
// replicated state — so the acknowledgment gate below is about *sequencing*,
// not a technical barrier. What is unsafe is running the upgrade on several
// nodes at once, or on a node that was never drained or was still the leader:
// that is what opens an availability or data-loss window. See docs/ops.md
// "Rolling upgrade".

// ClusterRole is the last-known Raft role of a node, taken from its on-disk
// cluster status file. It is advisory only: that file is written while the
// server runs and is stale by the time an offline upgrade reads it.
type ClusterRole string

const (
	ClusterRoleUnknown  ClusterRole = "unknown"
	ClusterRoleLeader   ClusterRole = "leader"
	ClusterRoleFollower ClusterRole = "follower"
)

// ClusterUpgradeInput is what the command layer learned about a node's Raft
// membership before an in-place upgrade.
type ClusterUpgradeInput struct {
	// Clustered is true when the data directory shows any evidence of Raft
	// membership: a raft/ state directory, a cluster status file, or
	// node_id + raft_bind together in the config.
	Clustered bool
	// LastKnownRole comes from the status file when one is present.
	LastKnownRole ClusterRole
	// Voters is the voter count last recorded in the status file (0 = unknown).
	Voters int
	// Acknowledged is set when the operator passed --cluster-node, asserting
	// this node has already been drained and, if it was the leader, that
	// leadership was transferred and a new leader confirmed.
	Acknowledged bool
	// DryRun mirrors the runner's --dry-run: guidance is always emitted and
	// nothing is ever blocked.
	DryRun bool
}

// RollingStep is one ordered action in the per-node rolling-upgrade
// procedure. The list is emitted for a clustered node so an OS installer or
// the Manager GUI can drive the same sequence documented in docs/ops.md
// "Rolling upgrade".
type RollingStep struct {
	Order  int    `json:"order"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// ClusterUpgradeGuidance is the conclusion of the rolling-upgrade check.
type ClusterUpgradeGuidance struct {
	Clustered bool `json:"clustered"`
	// Proceed is true when the in-place upgrade of this one node may run now:
	// the node is standalone, or --cluster-node acknowledged the
	// drain / leadership-transfer prerequisites, or this is a dry run.
	Proceed  bool          `json:"proceed"`
	Blocking []string      `json:"blocking,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
	Steps    []RollingStep `json:"steps,omitempty"`
}

// PlanRollingUpgrade decides whether a single node's offline in-place upgrade
// may run now and returns the per-node rolling procedure for a clustered
// node. A standalone node always proceeds with no extra steps.
func PlanRollingUpgrade(in ClusterUpgradeInput) ClusterUpgradeGuidance {
	g := ClusterUpgradeGuidance{Clustered: in.Clustered}
	if !in.Clustered {
		g.Proceed = true
		return g
	}

	role := in.LastKnownRole
	if role == "" {
		role = ClusterRoleUnknown
	}
	g.Steps = rollingSteps(role)

	if in.Voters > 0 && in.Voters < 3 {
		g.Warnings = append(g.Warnings, fmt.Sprintf(
			"the last recorded voter count was %d; a cluster with fewer than 3 voters loses quorum while any one node is down for upgrade",
			in.Voters))
	}
	if role == ClusterRoleLeader {
		g.Warnings = append(g.Warnings,
			"this node was the leader when it last wrote its status file; transfer leadership (nextsql cluster transfer-leader) and confirm a new leader before draining it")
	}

	switch {
	case in.DryRun, in.Acknowledged:
		g.Proceed = true
	default:
		g.Proceed = false
		g.Blocking = append(g.Blocking,
			"this data directory is a Raft cluster node; drain it (nextsql cluster drain) and, if it is the leader, transfer leadership first, then re-run with --cluster-node")
	}
	return g
}

func rollingSteps(role ClusterRole) []RollingStep {
	var steps []RollingStep
	n := 0
	add := func(name, detail string) {
		n++
		steps = append(steps, RollingStep{Order: n, Name: name, Detail: detail})
	}
	if role != ClusterRoleFollower {
		add("transfer-leadership",
			"if this node is the current leader, run `nextsql cluster transfer-leader` against it and wait for a new leader to be confirmed (SHOW CLUSTER on another node); skip for a follower")
	}
	add("drain",
		"run `nextsql cluster drain [--timeout-ms N]` against this node so busy connections finish before its listener closes")
	add("stop-and-upgrade",
		"stop the process, then run `nextsql lifecycle upgrade --cluster-node` (this command) to run WAL recovery and re-verify the store under the new binary")
	add("restart-and-catch-up",
		"start the upgraded node; it rejoins as a follower — wait for system.replica_health.apply_backlog to reach 0 (or applied_lsn to match the leader's) before touching the next node")
	add("next-node",
		"repeat for every remaining node, one at a time; a 3-voter cluster keeps quorum (2 of 3) throughout")
	return steps
}
