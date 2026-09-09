package replication

import (
	"testing"
	"time"
)

// The freshness boundary itself, checked without racing Raft. A follower is a
// safe read target only while it sees a leader and its contact age is inside
// *its own cluster's* window — which is why a raised heartbeat must carry the
// window with it.
func TestHealthyContactBoundary(t *testing.T) {
	const window = 5 * time.Second
	cases := []struct {
		name        string
		hasLeader   bool
		lastContact time.Duration
		want        bool
	}{
		{"fresh contact", true, time.Millisecond, true},
		{"exactly at the window", true, window, true},
		{"one nanosecond past the window", true, window + time.Nanosecond, false},
		{"no leader visible", false, time.Millisecond, false},
		{"never contacted", true, NeverContacted, false},
		{"never contacted and no leader", false, NeverContacted, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := healthyContact(tc.hasLeader, tc.lastContact, window); got != tc.want {
				t.Fatalf("healthyContact(%v, %v, %v) = %v, want %v",
					tc.hasLeader, tc.lastContact, window, got, tc.want)
			}
		})
	}
}

// The regression the first attempt at configurable timings caused, stated as a
// decision rather than as a race: a follower on a 3 s heartbeat contacted 2 s
// ago is healthy under its own window and unhealthy under the default one. If
// the window ever stops following the heartbeat, this is what breaks.
func TestRaisedHeartbeatDoesNotStrandHealthyFollowers(t *testing.T) {
	tm, err := (Timings{Heartbeat: 3 * time.Second, Election: 6 * time.Second}).Resolve()
	if err != nil {
		t.Fatal(err)
	}
	const contact = 2 * time.Second
	if contact <= DefaultHealthyContactWindow {
		t.Fatalf("test premise broken: %v is inside the default window %v", contact, DefaultHealthyContactWindow)
	}
	if healthyContact(true, contact, DefaultHealthyContactWindow) {
		t.Fatal("test premise broken: the fixed default window accepted this contact age")
	}
	if !healthyContact(true, contact, tm.HealthyContactWindow()) {
		t.Fatalf("a follower contacted %v ago on a %v heartbeat was judged unhealthy; "+
			"the freshness window is not following the configured heartbeat", contact, tm.Heartbeat)
	}
}
