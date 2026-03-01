package repo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShouldUpgradeProject(t *testing.T) {
	tests := []struct {
		name    string
		current string
		target  string
		force   bool
		want    bool
	}{
		{name: "force always upgrade", current: "v1.0.0", target: "v1.0.0", force: true, want: true},
		{name: "empty current upgrade", current: "", target: "v1.0.0", want: true},
		{name: "same version skip", current: "v1.0.0", target: "v1.0.0", want: false},
		{name: "different version upgrade", current: "v1.0.0", target: "v1.1.0", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldUpgradeProject(tt.current, tt.target, tt.force); got != tt.want {
				t.Fatalf("ShouldUpgradeProject()=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestRecordProjectDalekVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".dalek_project.json")
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	got, err := RecordProjectDalekVersion(path, "demo", "demo-key", "/repo/demo", "v1.2.3", at)
	if err != nil {
		t.Fatalf("RecordProjectDalekVersion failed: %v", err)
	}
	if got.Schema != ProjectMetaSchemaV1 {
		t.Fatalf("unexpected schema: %s", got.Schema)
	}
	if got.DalekVersion != "v1.2.3" {
		t.Fatalf("unexpected dalek_version: %s", got.DalekVersion)
	}
	if !got.UpgradedAt.Equal(at) {
		t.Fatalf("unexpected upgraded_at: %s", got.UpgradedAt.Format(time.RFC3339))
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read meta failed: %v", err)
	}
	if len(raw) == 0 {
		t.Fatalf("meta file should not be empty")
	}

	loaded, exists, err := LoadProjectMeta(path)
	if err != nil {
		t.Fatalf("LoadProjectMeta failed: %v", err)
	}
	if !exists {
		t.Fatalf("expected project meta exists")
	}
	if loaded.DalekVersion != "v1.2.3" {
		t.Fatalf("unexpected loaded version: %s", loaded.DalekVersion)
	}
}
