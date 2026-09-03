package main

import (
	"context"
	"testing"

	"github.com/bzync/nextsql/internal/executor"
)

func TestReplicaLagTickNotAttachedOnSingleNodeDB(t *testing.T) {
	db := testDB(t)
	backlog, attached := replicaLagTick(db)
	if attached {
		t.Fatal("expected attached=false for a DB with no cluster attached")
	}
	if backlog != 0 {
		t.Fatalf("backlog = %d, want 0", backlog)
	}
}

func TestReplicaLagEdge(t *testing.T) {
	cases := []struct {
		name                           string
		wasWarned                      bool
		backlog, warnEntries           uint64
		nowWarned, logWarn, logRecover bool
	}{
		{"below threshold stays clear", false, 10, 1000, false, false, false},
		{"crosses at threshold trips warn", false, 1000, 1000, true, true, false},
		{"well above threshold trips warn", false, 5000, 1000, true, true, false},
		{"already warned stays warned mid-band", true, 1500, 1000, true, false, false},
		{"already warned exactly at threshold stays warned", true, 1000, 1000, true, false, false},
		{"drops below threshold recovers", true, 999, 1000, false, false, true},
		{"drops to zero recovers", true, 0, 1000, false, false, true},
		{"not warned and zero backlog stays clear", false, 0, 1000, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nowWarned, logWarn, logRecover := replicaLagEdge(tc.wasWarned, tc.backlog, tc.warnEntries)
			if nowWarned != tc.nowWarned || logWarn != tc.logWarn || logRecover != tc.logRecover {
				t.Fatalf("replicaLagEdge(%v, %d, %d) = (%v, %v, %v), want (%v, %v, %v)",
					tc.wasWarned, tc.backlog, tc.warnEntries, nowWarned, logWarn, logRecover,
					tc.nowWarned, tc.logWarn, tc.logRecover)
			}
		})
	}
}

func TestReplicaLagEdgeNeverLogsBothAtOnce(t *testing.T) {
	// A sanity guard on the state machine itself, independent of any
	// specific case above: warn and recover must never both fire from a
	// single transition.
	for _, wasWarned := range []bool{false, true} {
		for _, backlog := range []uint64{0, 1, 999, 1000, 1001, 5000} {
			for _, warnEntries := range []uint64{0, 1, 1000} {
				_, logWarn, logRecover := replicaLagEdge(wasWarned, backlog, warnEntries)
				if logWarn && logRecover {
					t.Fatalf("replicaLagEdge(%v, %d, %d) set both logWarn and logRecover", wasWarned, backlog, warnEntries)
				}
			}
		}
	}
}

func TestStartReplicaLagMonitorNoopWithoutPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := quietLog()
	startReplicaLagMonitor(ctx, nil, 1000, 1000, log)
	startReplicaLagMonitor(ctx, &executor.DB{}, 0, 1000, log)
}
