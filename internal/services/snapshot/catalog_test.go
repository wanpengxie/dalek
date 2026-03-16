package snapshot

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

func newCatalogForTest(t *testing.T) *Catalog {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	return NewCatalog(db)
}

func TestCatalog_CreateAndGetBySnapshotID(t *testing.T) {
	catalog := newCatalogForTest(t)
	now := time.Date(2026, 3, 14, 18, 0, 0, 0, time.UTC)
	catalog.now = func() time.Time { return now }

	created, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:          "snap-1",
		ProjectKey:          "demo",
		NodeName:            "node-b",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		ManifestJSON:        `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if created.SnapshotID != "snap-1" || created.Status != string(contracts.SnapshotPreparing) {
		t.Fatalf("unexpected create result: %+v", created)
	}

	got, err := catalog.GetBySnapshotID(context.Background(), "snap-1")
	if err != nil {
		t.Fatalf("GetBySnapshotID failed: %v", err)
	}
	if got == nil || got.ManifestDigest == "" || got.BaseCommit != "abc123" {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
}

func TestCatalog_Create_IsIdempotentBySnapshotID(t *testing.T) {
	catalog := newCatalogForTest(t)

	first, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID: "snap-idempotent",
		ProjectKey: "demo",
	})
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}
	second, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID: "snap-idempotent",
		ProjectKey: "demo",
	})
	if err != nil {
		t.Fatalf("second Create failed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected idempotent snapshot create: first=%d second=%d", first.ID, second.ID)
	}
}

func TestCatalog_MarkReady(t *testing.T) {
	catalog := newCatalogForTest(t)
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID: "snap-ready",
		ProjectKey: "demo",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := catalog.MarkReady(context.Background(), "snap-ready", "/tmp/snapshots/snap-ready.tar"); err != nil {
		t.Fatalf("MarkReady failed: %v", err)
	}
	got, err := catalog.GetBySnapshotID(context.Background(), "snap-ready")
	if err != nil {
		t.Fatalf("GetBySnapshotID failed: %v", err)
	}
	if got == nil || got.Status != string(contracts.SnapshotReady) || got.ArtifactPath != "/tmp/snapshots/snap-ready.tar" {
		t.Fatalf("unexpected ready snapshot: %+v", got)
	}
}

func TestCatalog_Create_RejectsManifestDigestMismatch(t *testing.T) {
	catalog := newCatalogForTest(t)

	_, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:     "snap-bad-digest",
		ProjectKey:     "demo",
		ManifestDigest: "sha256:notmatch",
		ManifestJSON:   `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
	})
	if err == nil {
		t.Fatalf("expected digest mismatch to fail")
	}
}

func TestCatalog_AcquireReleaseAndListGarbageEligible(t *testing.T) {
	catalog := newCatalogForTest(t)
	now := time.Date(2026, 3, 14, 19, 0, 0, 0, time.UTC)
	catalog.now = func() time.Time { return now }
	expiresAt := now.Add(-time.Minute)
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:   "snap-gc-1",
		ProjectKey:   "demo",
		Status:       string(contracts.SnapshotReady),
		ExpiresAt:    &expiresAt,
		ManifestJSON: `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := catalog.AcquireReference(context.Background(), "snap-gc-1"); err != nil {
		t.Fatalf("AcquireReference failed: %v", err)
	}
	got, err := catalog.GetBySnapshotID(context.Background(), "snap-gc-1")
	if err != nil {
		t.Fatalf("GetBySnapshotID failed: %v", err)
	}
	if got == nil || got.RefCount != 1 {
		t.Fatalf("unexpected ref_count after acquire: %+v", got)
	}

	items, err := catalog.ListGarbageEligible(context.Background(), now, 10)
	if err != nil {
		t.Fatalf("ListGarbageEligible failed: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected referenced snapshot not eligible, got=%+v", items)
	}

	if err := catalog.ReleaseReference(context.Background(), "snap-gc-1"); err != nil {
		t.Fatalf("ReleaseReference failed: %v", err)
	}
	items, err = catalog.ListGarbageEligible(context.Background(), now, 10)
	if err != nil {
		t.Fatalf("ListGarbageEligible after release failed: %v", err)
	}
	if len(items) != 1 || items[0].SnapshotID != "snap-gc-1" {
		t.Fatalf("expected released snapshot eligible, got=%+v", items)
	}
}
