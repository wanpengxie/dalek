package contracts

import "time"

type SnapshotStatus string

const (
	SnapshotPreparing SnapshotStatus = "preparing"
	SnapshotReady     SnapshotStatus = "ready"
	SnapshotFailed    SnapshotStatus = "failed"
	SnapshotExpired   SnapshotStatus = "expired"
)

type Snapshot struct {
	ID uint `gorm:"primaryKey"`

	SnapshotID string `gorm:"column:snapshot_id;type:text;not null;uniqueIndex"`
	ProjectKey string `gorm:"column:project_key;type:text;not null;default:'';index"`
	NodeName   string `gorm:"column:node_name;type:text;not null;default:'';index"`

	BaseCommit          string `gorm:"column:base_commit;type:text;not null;default:''"`
	WorkspaceGeneration string `gorm:"column:workspace_generation;type:text;not null;default:''"`
	ManifestDigest      string `gorm:"column:manifest_digest;type:text;not null;default:''"`
	ManifestJSON        string `gorm:"column:manifest_json;type:text;not null;default:''"`

	Status       string     `gorm:"column:status;type:text;not null;default:'preparing';index"`
	ArtifactPath string     `gorm:"column:artifact_path;type:text;not null;default:''"`
	RefCount     int        `gorm:"column:ref_count;not null;default:0"`
	ExpiresAt    *time.Time `gorm:"column:expires_at"`
	LastUsedAt   *time.Time `gorm:"column:last_used_at"`
	ErrorMessage string     `gorm:"column:error_message;type:text;not null;default:''"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Snapshot) TableName() string {
	return "snapshots"
}
