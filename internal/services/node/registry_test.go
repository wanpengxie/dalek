package node

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

func newNodeServiceForTest(t *testing.T) *Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	return New(db)
}

func TestService_RegisterAndGetByName(t *testing.T) {
	svc := newNodeServiceForTest(t)
	now := time.Now().Local().Truncate(time.Second)

	rec, err := svc.Register(context.Background(), RegisterInput{
		Name:                 "run-node-1",
		Endpoint:             "https://node.example.test",
		AuthMode:             "token",
		Status:               string(contracts.NodeStatusOnline),
		ProtocolVersion:      "v1",
		RoleCapabilities:     []string{"run"},
		ProviderModes:        []string{"run_executor"},
		DefaultProvider:      "run_executor",
		ProviderCapabilities: map[string]any{"run_executor": map[string]any{"verify": true}},
		LastSeenAt:           &now,
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if rec.ID == 0 {
		t.Fatalf("expected non-zero node id")
	}

	got, err := svc.GetByName(context.Background(), "run-node-1")
	if err != nil {
		t.Fatalf("GetByName failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected existing node")
	}
	if got.Name != "run-node-1" {
		t.Fatalf("unexpected name: %s", got.Name)
	}
	if got.Status != string(contracts.NodeStatusOnline) {
		t.Fatalf("unexpected status: %s", got.Status)
	}
}

func TestService_Register_DuplicateReturnsExisting(t *testing.T) {
	svc := newNodeServiceForTest(t)

	first, err := svc.Register(context.Background(), RegisterInput{
		Name:   "node-dup",
		Status: string(contracts.NodeStatusOnline),
	})
	if err != nil {
		t.Fatalf("first Register failed: %v", err)
	}
	second, err := svc.Register(context.Background(), RegisterInput{
		Name:   "node-dup",
		Status: string(contracts.NodeStatusOffline),
	})
	if err != nil {
		t.Fatalf("second Register failed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected duplicate register to return existing id: first=%d second=%d", first.ID, second.ID)
	}
	if second.Status != first.Status {
		t.Fatalf("duplicate register should return stored row, got status=%s want=%s", second.Status, first.Status)
	}
}

func TestService_ListAndUpdateStatus(t *testing.T) {
	svc := newNodeServiceForTest(t)
	ctx := context.Background()

	if _, err := svc.Register(ctx, RegisterInput{Name: "node-a", Status: string(contracts.NodeStatusOnline)}); err != nil {
		t.Fatalf("register node-a failed: %v", err)
	}
	if _, err := svc.Register(ctx, RegisterInput{Name: "node-b", Status: string(contracts.NodeStatusOffline)}); err != nil {
		t.Fatalf("register node-b failed: %v", err)
	}

	list, err := svc.List(ctx, ListOptions{Status: string(contracts.NodeStatusOnline), Limit: 10})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 || list[0].Name != "node-a" {
		t.Fatalf("unexpected online node list: %+v", list)
	}

	now := time.Now().Local().Add(time.Minute).Truncate(time.Second)
	if err := svc.UpdateStatus(ctx, "node-b", string(contracts.NodeStatusDegraded), &now); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	got, err := svc.GetByName(ctx, "node-b")
	if err != nil {
		t.Fatalf("GetByName node-b failed: %v", err)
	}
	if got == nil || got.Status != string(contracts.NodeStatusDegraded) {
		t.Fatalf("unexpected updated node: %+v", got)
	}
	if got.LastSeenAt == nil || !got.LastSeenAt.Equal(now) {
		t.Fatalf("unexpected last_seen_at after update: %+v", got.LastSeenAt)
	}
}

func TestService_GetSchedulable_FiltersByRoleAndProvider(t *testing.T) {
	svc := newNodeServiceForTest(t)
	ctx := context.Background()

	if _, err := svc.Register(ctx, RegisterInput{
		Name:             "run-node",
		Status:           string(contracts.NodeStatusOnline),
		RoleCapabilities: []string{"run"},
		ProviderModes:    []string{"run_executor"},
	}); err != nil {
		t.Fatalf("register run-node failed: %v", err)
	}
	if _, err := svc.Register(ctx, RegisterInput{
		Name:             "dev-node",
		Status:           string(contracts.NodeStatusOnline),
		RoleCapabilities: []string{"dev"},
		ProviderModes:    []string{"codex"},
	}); err != nil {
		t.Fatalf("register dev-node failed: %v", err)
	}
	if _, err := svc.Register(ctx, RegisterInput{
		Name:             "offline-run-node",
		Status:           string(contracts.NodeStatusOffline),
		RoleCapabilities: []string{"run"},
		ProviderModes:    []string{"run_executor"},
	}); err != nil {
		t.Fatalf("register offline-run-node failed: %v", err)
	}

	rows, err := svc.GetSchedulable(ctx, "run", "run_executor", 10)
	if err != nil {
		t.Fatalf("GetSchedulable failed: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "run-node" {
		t.Fatalf("unexpected schedulable result: %+v", rows)
	}
}

func TestService_RemoveByName(t *testing.T) {
	svc := newNodeServiceForTest(t)
	ctx := context.Background()

	if _, err := svc.Register(ctx, RegisterInput{Name: "node-remove"}); err != nil {
		t.Fatalf("register node failed: %v", err)
	}
	removed, err := svc.RemoveByName(ctx, "node-remove")
	if err != nil {
		t.Fatalf("RemoveByName failed: %v", err)
	}
	if !removed {
		t.Fatalf("expected node to be removed")
	}
	removed, err = svc.RemoveByName(ctx, "node-remove")
	if err != nil {
		t.Fatalf("RemoveByName second time failed: %v", err)
	}
	if removed {
		t.Fatalf("expected RemoveByName to return false when missing")
	}
}
