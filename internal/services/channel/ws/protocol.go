package ws

const (
	FrameTypeReady            = "ready"
	FrameTypeAssistantEvent   = "assistant_event"
	FrameTypeAssistantMessage = "assistant_message"
	FrameTypeInboxUpdate      = "inbox_update"
	FrameTypeError            = "error"
)

type InboundFrame struct {
	Text     string `json:"text"`
	SenderID string `json:"sender_id,omitempty"`
}

type InboxItem struct {
	ID        uint   `json:"id"`
	Status    string `json:"status"`
	Severity  string `json:"severity"`
	Reason    string `json:"reason"`
	Title     string `json:"title"`
	TicketID  uint   `json:"ticket_id"`
	WorkerID  uint   `json:"worker_id"`
	UpdatedAt string `json:"updated_at"`
}

type OutboundFrame struct {
	Type           string      `json:"type"`
	ConversationID string      `json:"conversation_id,omitempty"`
	PeerMessageID  string      `json:"peer_message_id,omitempty"`
	RunID          string      `json:"run_id,omitempty"`
	Seq            int         `json:"seq,omitempty"`
	Stream         string      `json:"stream,omitempty"`
	Text           string      `json:"text,omitempty"`
	EventType      string      `json:"event_type,omitempty"`
	AgentProvider  string      `json:"agent_provider,omitempty"`
	AgentModel     string      `json:"agent_model,omitempty"`
	JobStatus      string      `json:"job_status,omitempty"`
	JobErrorType   string      `json:"job_error_type,omitempty"`
	JobError       string      `json:"job_error,omitempty"`
	InboxCount     int         `json:"inbox_count,omitempty"`
	InboxItems     []InboxItem `json:"inbox_items,omitempty"`
	At             string      `json:"at,omitempty"`
}
