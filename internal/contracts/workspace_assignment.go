package contracts

import "time"

// WorkspaceAssignment 表示项目在某节点上的一个工作区绑定。
type WorkspaceAssignment struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ProjectKey string `gorm:"column:project_key;type:text;not null;index"`
	NodeID     uint   `gorm:"column:node_id;not null;index"`
	Role       string `gorm:"type:text;not null;default:'';index"`

	RepoRoot      string `gorm:"column:repo_root;type:text;not null;default:''"`
	DefaultBranch string `gorm:"column:default_branch;type:text;not null;default:''"`

	BootstrapStatus string `gorm:"column:bootstrap_status;type:text;not null;default:''"`
	EnvStatus       string `gorm:"column:env_status;type:text;not null;default:''"`

	WorkspaceGeneration string `gorm:"column:workspace_generation;type:text;not null;default:''"`
	DesiredRevision     string `gorm:"column:desired_revision;type:text;not null;default:''"`
	CurrentRevision     string `gorm:"column:current_revision;type:text;not null;default:''"`

	DirtyPolicy          string     `gorm:"column:dirty_policy;type:text;not null;default:''"`
	BootstrapFingerprint string     `gorm:"column:bootstrap_fingerprint;type:text;not null;default:''"`
	CapacityHint         int        `gorm:"column:capacity_hint;not null;default:0"`
	LastVerifiedAt       *time.Time `gorm:"column:last_verified_at"`
	LastError            string     `gorm:"column:last_error;type:text;not null;default:''"`
}

func (WorkspaceAssignment) TableName() string {
	return "workspace_assignments"
}
