package node

import (
	"context"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestSessionManager_HeartbeatMarksNodeOnline(t *testing.T) {
	registry := newNodeServiceForTest(t)
	manager := NewSessionManager(registry)

	oldSeen := time.Now().Local().Add(-10 * time.Minute).Truncate(time.Second)
	if _, err := registry.Register(context.Background(), RegisterInput{
		Name:       "node-heartbeat",
		Status:     string(contracts.NodeStatusOffline),
		LastSeenAt: &oldSeen,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	newSeen := oldSeen.Add(9 * time.Minute)
	if err := manager.Heartbeat(context.Background(), "node-heartbeat", &newSeen); err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	got, err := registry.GetByName(context.Background(), "node-heartbeat")
	if err != nil {
		t.Fatalf("GetByName failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected node after heartbeat")
	}
	if got.Status != string(contracts.NodeStatusOnline) {
		t.Fatalf("expected online after heartbeat, got=%s", got.Status)
	}
	if got.LastSeenAt == nil || !got.LastSeenAt.Equal(newSeen) {
		t.Fatalf("unexpected last_seen_at after heartbeat: %+v", got.LastSeenAt)
	}
}

func TestSessionManager_RefreshExpiredMarksOffline(t *testing.T) {
	registry := newNodeServiceForTest(t)
	manager := NewSessionManager(registry)
	fixedNow := time.Now().Local().Truncate(time.Second)
	manager.now = func() time.Time { return fixedNow }

	staleSeen := fixedNow.Add(-10 * time.Minute)
	freshSeen := fixedNow.Add(-30 * time.Second)
	if _, err := registry.Register(context.Background(), RegisterInput{
		Name:       "node-stale",
		Status:     string(contracts.NodeStatusOnline),
		LastSeenAt: &staleSeen,
	}); err != nil {
		t.Fatalf("Register stale failed: %v", err)
	}
	if _, err := registry.Register(context.Background(), RegisterInput{
		Name:       "node-fresh",
		Status:     string(contracts.NodeStatusOnline),
		LastSeenAt: &freshSeen,
	}); err != nil {
		t.Fatalf("Register fresh failed: %v", err)
	}

	res, err := manager.RefreshExpired(context.Background(), 2*time.Minute)
	if err != nil {
		t.Fatalf("RefreshExpired failed: %v", err)
	}
	if res.Checked != 2 || res.Updated != 1 {
		t.Fatalf("unexpected refresh result: %+v", res)
	}

	stale, err := registry.GetByName(context.Background(), "node-stale")
	if err != nil {
		t.Fatalf("GetByName stale failed: %v", err)
	}
	if stale == nil || stale.Status != string(contracts.NodeStatusOffline) {
		t.Fatalf("expected stale node offline, got=%+v", stale)
	}

	fresh, err := registry.GetByName(context.Background(), "node-fresh")
	if err != nil {
		t.Fatalf("GetByName fresh failed: %v", err)
	}
	if fresh == nil || fresh.Status != string(contracts.NodeStatusOnline) {
		t.Fatalf("expected fresh node stay online, got=%+v", fresh)
	}
}

func TestSessionManager_BeginSessionIncrementsEpochAndHeartbeatChecksEpoch(t *testing.T) {
	registry := newNodeServiceForTest(t)
	manager := NewSessionManager(registry)
	fixedNow := time.Now().Local().Truncate(time.Second)
	manager.now = func() time.Time { return fixedNow }

	if _, err := registry.Register(context.Background(), RegisterInput{
		Name:       "node-epoch",
		Status:     string(contracts.NodeStatusOffline),
		LastSeenAt: &fixedNow,
	}); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	begin, err := manager.BeginSession(context.Background(), "node-epoch", nil)
	if err != nil {
		t.Fatalf("BeginSession failed: %v", err)
	}
	if begin.SessionEpoch != 2 {
		t.Fatalf("expected incremented session epoch=2, got=%d", begin.SessionEpoch)
	}

	got, err := registry.GetByName(context.Background(), "node-epoch")
	if err != nil {
		t.Fatalf("GetByName failed: %v", err)
	}
	if got == nil || got.SessionEpoch != 2 {
		t.Fatalf("expected stored session epoch=2, got=%+v", got)
	}

	if err := manager.HeartbeatWithEpoch(context.Background(), "node-epoch", 1, nil); err == nil {
		t.Fatalf("expected stale epoch heartbeat to fail")
	}
	if err := manager.HeartbeatWithEpoch(context.Background(), "node-epoch", 2, nil); err != nil {
		t.Fatalf("expected matching epoch heartbeat to succeed: %v", err)
	}
}
