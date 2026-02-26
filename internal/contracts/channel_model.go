package contracts

import "time"

// ChannelBinding 是项目到外部通道的绑定关系。
type ChannelBinding struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ProjectName string      `gorm:"type:text;not null;default:'';index"`
	ChannelType ChannelType `gorm:"type:text;not null;index;uniqueIndex:idx_channel_binding_identity,priority:1"`
	Adapter     string      `gorm:"type:text;not null;index;uniqueIndex:idx_channel_binding_identity,priority:2"`

	PeerProjectKey string `gorm:"type:text;not null;default:'';uniqueIndex:idx_channel_binding_identity,priority:3"`
	RolePolicyJSON string `gorm:"type:text;not null;default:'{}'"`
	Enabled        bool   `gorm:"not null;default:true;index"`
}

// ChannelConversation 是通道会话映射。
type ChannelConversation struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	BindingID          uint   `gorm:"not null;index;uniqueIndex:idx_channel_conversation_peer,priority:1"`
	PeerConversationID string `gorm:"type:text;not null;default:'';uniqueIndex:idx_channel_conversation_peer,priority:2"`

	Title          string     `gorm:"type:text;not null;default:''"`
	Summary        string     `gorm:"type:text;not null;default:''"`
	AgentSessionID string     `gorm:"type:text;not null;default:''"`
	LastMessageAt  *time.Time `gorm:""`
}

// ChannelMessage 是通道消息模型。
type ChannelMessage struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`

	ConversationID uint                    `gorm:"not null;index;uniqueIndex:idx_channel_message_dedup,priority:2"`
	Direction      ChannelMessageDirection `gorm:"type:text;not null;index;uniqueIndex:idx_channel_message_dedup,priority:1"`

	Adapter       string  `gorm:"type:text;not null;default:'';index;uniqueIndex:idx_channel_message_dedup,priority:3"`
	PeerMessageID *string `gorm:"type:text;uniqueIndex:idx_channel_message_dedup,priority:4"`

	SenderID    string               `gorm:"type:text;not null;default:''"`
	SenderName  string               `gorm:"type:text;not null;default:''"`
	ContentText string               `gorm:"type:text;not null;default:''"`
	PayloadJSON string               `gorm:"type:text;not null;default:'{}'"`
	Status      ChannelMessageStatus `gorm:"type:text;not null;index"`
}

// ChannelTurnJob 是通道轮次处理任务。
type ChannelTurnJob struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ConversationID   uint `gorm:"not null;index"`
	InboundMessageID uint `gorm:"not null;uniqueIndex"`

	Status         ChannelTurnJobStatus `gorm:"type:text;not null;index"`
	RunnerID       string               `gorm:"type:text;not null;default:''"`
	LeaseExpiresAt *time.Time           `gorm:""`
	Attempt        int                  `gorm:"not null;default:0"`
	Error          string               `gorm:"type:text;not null;default:''"`
	ResultJSON     string               `gorm:"type:text;not null;default:''"`

	StartedAt  *time.Time `gorm:""`
	FinishedAt *time.Time `gorm:""`
}

// ChannelPendingAction 是待决动作记录。
type ChannelPendingAction struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ConversationID uint `gorm:"not null;index"`
	JobID          uint `gorm:"not null;index"`

	ActionJSON   string                     `gorm:"type:text;not null;default:'{}'"`
	Status       ChannelPendingActionStatus `gorm:"type:text;not null;index"`
	Decider      string                     `gorm:"type:text;not null;default:''"`
	DecisionNote string                     `gorm:"type:text;not null;default:''"`

	DecidedAt  *time.Time `gorm:""`
	ExecutedAt *time.Time `gorm:""`
}

// EventBusLog 是事件总线日志落库模型。
type EventBusLog struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	Project        string `gorm:"type:text;not null;default:'';index"`
	ConversationID string `gorm:"type:text;not null;default:''"`
	PeerMessageID  string `gorm:"type:text;not null;default:'';index"`

	Type      string `gorm:"type:text;not null;default:''"`
	RunID     string `gorm:"type:text;not null;default:''"`
	Seq       int    `gorm:"not null;default:0"`
	Stream    string `gorm:"type:text;not null;default:''"`
	EventType string `gorm:"type:text;not null;default:''"`
	Text      string `gorm:"type:text;not null;default:''"`

	AgentProvider string `gorm:"type:text;not null;default:''"`
	AgentModel    string `gorm:"type:text;not null;default:''"`
	JobStatus     string `gorm:"type:text;not null;default:''"`
	JobError      string `gorm:"type:text;not null;default:''"`
	JobErrorType  string `gorm:"type:text;not null;default:''"`
}

// ChannelOutbox 是待发送出站消息队列。
type ChannelOutbox struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	MessageID uint   `gorm:"not null;uniqueIndex"`
	Adapter   string `gorm:"type:text;not null;default:'';index"`

	PayloadJSON string             `gorm:"type:text;not null;default:'{}'"`
	Status      ChannelOutboxStatus `gorm:"type:text;not null;index"`
	RetryCount  int                `gorm:"not null;default:0"`
	NextRetryAt *time.Time         `gorm:""`
	LastError   string             `gorm:"type:text;not null;default:''"`
}
