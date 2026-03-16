package contracts

import "time"

type NodeStatus string

const (
	NodeStatusUnknown  NodeStatus = "unknown"
	NodeStatusOnline   NodeStatus = "online"
	NodeStatusOffline  NodeStatus = "offline"
	NodeStatusDegraded NodeStatus = "degraded"
)

type NodeRole string

const (
	NodeRoleControl NodeRole = "control"
	NodeRoleDev     NodeRole = "dev"
	NodeRoleRun     NodeRole = "run"
)

// Node 是控制面上的节点元数据。当前最小版本先落元数据和能力字段。
type Node struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	Name string `gorm:"type:text;not null;uniqueIndex"`

	Endpoint        string `gorm:"type:text;not null;default:''"`
	AuthMode        string `gorm:"column:auth_mode;type:text;not null;default:''"`
	Status          string `gorm:"type:text;not null;default:'unknown';index"`
	Version         string `gorm:"type:text;not null;default:''"`
	ProtocolVersion string `gorm:"column:protocol_version;type:text;not null;default:''"`

	RoleCapabilities     JSONStringSlice `gorm:"column:role_capabilities_json;type:text;not null;default:'[]'"`
	ProviderModes        JSONStringSlice `gorm:"column:provider_modes_json;type:text;not null;default:'[]'"`
	DefaultProvider      string          `gorm:"column:default_provider;type:text;not null;default:''"`
	ProviderCapabilities JSONMap         `gorm:"column:provider_capabilities_json;type:text;not null;default:'{}'"`

	SessionAffinity string     `gorm:"column:session_affinity;type:text;not null;default:''"`
	SessionEpoch    int        `gorm:"column:session_epoch;not null;default:1"`
	LastSeenAt      *time.Time `gorm:"column:last_seen_at"`
}

func (Node) TableName() string {
	return "nodes"
}
