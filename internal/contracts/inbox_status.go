package contracts

type InboxStatus string

const (
	InboxOpen    InboxStatus = "open"
	InboxDone    InboxStatus = "done"
	InboxSnoozed InboxStatus = "snoozed"
)

type InboxSeverity string

const (
	InboxInfo    InboxSeverity = "info"
	InboxWarn    InboxSeverity = "warn"
	InboxBlocker InboxSeverity = "blocker"
)

type InboxReason string

const (
	InboxNeedsUser        InboxReason = "needs_user"
	InboxApprovalRequired InboxReason = "approval_required"
	InboxQuestion         InboxReason = "question"
	InboxIncident         InboxReason = "incident"
)
