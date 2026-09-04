package setup

import "testing"

func TestPlanRollingUpgradeStandalone(t *testing.T) {
	g := PlanRollingUpgrade(ClusterUpgradeInput{Clustered: false})
	if !g.Proceed {
		t.Fatal("a standalone node must proceed")
	}
	if len(g.Steps) != 0 || len(g.Blocking) != 0 || len(g.Warnings) != 0 {
		t.Fatalf("standalone guidance should be empty: %+v", g)
	}
}

func TestPlanRollingUpgradeClusteredBlocksWithoutAck(t *testing.T) {
	g := PlanRollingUpgrade(ClusterUpgradeInput{
		Clustered:     true,
		LastKnownRole: ClusterRoleFollower,
		Voters:        3,
	})
	if g.Proceed {
		t.Fatal("a clustered node must not proceed without acknowledgment")
	}
	if len(g.Blocking) == 0 {
		t.Fatal("expected a blocking reason")
	}
	if len(g.Steps) == 0 {
		t.Fatal("expected the rolling procedure to be listed even when blocked")
	}
	// A healthy 3-voter follower needs no warning.
	if len(g.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", g.Warnings)
	}
	// A follower's procedure skips the leadership-transfer step.
	if g.Steps[0].Name == "transfer-leadership" {
		t.Fatalf("follower procedure should not lead with transfer-leadership: %+v", g.Steps)
	}
}

func TestPlanRollingUpgradeClusteredProceedsWithAck(t *testing.T) {
	g := PlanRollingUpgrade(ClusterUpgradeInput{
		Clustered:     true,
		LastKnownRole: ClusterRoleFollower,
		Voters:        3,
		Acknowledged:  true,
	})
	if !g.Proceed {
		t.Fatal("an acknowledged clustered node must proceed")
	}
	if len(g.Blocking) != 0 {
		t.Fatalf("acknowledged node should have no blocking reasons: %v", g.Blocking)
	}
}

func TestPlanRollingUpgradeDryRunProceedsAndWarns(t *testing.T) {
	g := PlanRollingUpgrade(ClusterUpgradeInput{
		Clustered:     true,
		LastKnownRole: ClusterRoleLeader,
		Voters:        2,
		DryRun:        true,
	})
	if !g.Proceed {
		t.Fatal("a dry run must always proceed")
	}
	if len(g.Warnings) < 2 {
		t.Fatalf("expected a low-voter warning and a leader warning: %v", g.Warnings)
	}
	if g.Steps[0].Name != "transfer-leadership" {
		t.Fatalf("leader/unknown procedure should lead with transfer-leadership: %+v", g.Steps)
	}
}

func TestPlanRollingUpgradeUnknownRoleKeepsTransferStep(t *testing.T) {
	g := PlanRollingUpgrade(ClusterUpgradeInput{Clustered: true, Acknowledged: true})
	if len(g.Steps) == 0 || g.Steps[0].Name != "transfer-leadership" {
		t.Fatalf("unknown role should keep the transfer-leadership step: %+v", g.Steps)
	}
	// Orders are 1..N contiguous.
	for i, s := range g.Steps {
		if s.Order != i+1 {
			t.Fatalf("step %d has order %d", i, s.Order)
		}
	}
}
