package contracts

type ChannelType string

const (
	ChannelTypeWeb ChannelType = "web"
	ChannelTypeIM  ChannelType = "im"
	ChannelTypeCLI ChannelType = "cli"
	ChannelTypeAPI ChannelType = "api"
)

type ChannelMessageDirection string

const (
	ChannelMessageIn  ChannelMessageDirection = "in"
	ChannelMessageOut ChannelMessageDirection = "out"
)

type ChannelMessageStatus string

const (
	ChannelMessageAccepted  ChannelMessageStatus = "accepted"
	ChannelMessageProcessed ChannelMessageStatus = "processed"
	ChannelMessageSent      ChannelMessageStatus = "sent"
	ChannelMessageFailed    ChannelMessageStatus = "failed"
)

type ChannelTurnJobStatus string

const (
	ChannelTurnPending   ChannelTurnJobStatus = "pending"
	ChannelTurnRunning   ChannelTurnJobStatus = "running"
	ChannelTurnSucceeded ChannelTurnJobStatus = "succeeded"
	ChannelTurnFailed    ChannelTurnJobStatus = "failed"
)

type ChannelPendingActionStatus string

const (
	ChannelPendingActionPending  ChannelPendingActionStatus = "pending"
	ChannelPendingActionApproved ChannelPendingActionStatus = "approved"
	ChannelPendingActionRejected ChannelPendingActionStatus = "rejected"
	ChannelPendingActionExecuted ChannelPendingActionStatus = "executed"
	ChannelPendingActionFailed   ChannelPendingActionStatus = "failed"
)

type ChannelOutboxStatus string

const (
	ChannelOutboxPending ChannelOutboxStatus = "pending"
	ChannelOutboxSending ChannelOutboxStatus = "sending"
	ChannelOutboxSent    ChannelOutboxStatus = "sent"
	ChannelOutboxFailed  ChannelOutboxStatus = "failed"
	ChannelOutboxDead    ChannelOutboxStatus = "dead"
)
