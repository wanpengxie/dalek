package contracts

import "testing"

func TestNodeRoleConstants_AreStable(t *testing.T) {
	got := []NodeRole{NodeRoleControl, NodeRoleDev, NodeRoleRun}
	want := []NodeRole{"control", "dev", "run"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected role at %d: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestNodeStatusConstants_AreStable(t *testing.T) {
	got := []NodeStatus{NodeStatusUnknown, NodeStatusOnline, NodeStatusOffline, NodeStatusDegraded}
	want := []NodeStatus{"unknown", "online", "offline", "degraded"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected status at %d: got=%q want=%q", i, got[i], want[i])
		}
	}
}
