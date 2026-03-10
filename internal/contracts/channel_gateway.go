package contracts

import (
	"fmt"
	"strings"
	"time"
)

const (
	ChannelInboundSchemaV1 = "dalek.channel_inbound.v1"
	TurnResponseSchemaV1   = "dalek.turn_response.v1"
)

type InboundAttachment struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

type InboundEnvelope struct {
	Schema string `json:"schema"`

	ChannelType ChannelType `json:"channel_type"`
	Adapter     string      `json:"adapter"`
	BindingID   uint        `json:"binding_id"`

	PeerMessageID      string              `json:"peer_message_id"`
	PeerConversationID string              `json:"peer_conversation_id"`
	SenderID           string              `json:"sender_id"`
	SenderName         string              `json:"sender_name"`
	Text               string              `json:"text"`
	Attachments        []InboundAttachment `json:"attachments"`
	ReceivedAt         string              `json:"received_at"`
}

func (e *InboundEnvelope) Normalize() {
	if e == nil {
		return
	}
	e.Schema = strings.TrimSpace(e.Schema)
	if e.Schema == "" {
		e.Schema = ChannelInboundSchemaV1
	}
	e.ChannelType = ChannelType(strings.ToLower(strings.TrimSpace(string(e.ChannelType))))
	e.Adapter = strings.TrimSpace(e.Adapter)
	e.PeerMessageID = strings.TrimSpace(e.PeerMessageID)
	e.PeerConversationID = strings.TrimSpace(e.PeerConversationID)
	e.SenderID = strings.TrimSpace(e.SenderID)
	e.SenderName = strings.TrimSpace(e.SenderName)
	e.Text = strings.TrimSpace(e.Text)
	e.ReceivedAt = strings.TrimSpace(e.ReceivedAt)
	if e.ReceivedAt == "" {
		e.ReceivedAt = time.Now().Format(time.RFC3339)
	}
	if e.Attachments == nil {
		e.Attachments = []InboundAttachment{}
	}
}

func (e InboundEnvelope) Validate() error {
	if strings.TrimSpace(e.Schema) != ChannelInboundSchemaV1 {
		return fmt.Errorf("inbound schema 非法: %s", strings.TrimSpace(e.Schema))
	}
	switch ChannelType(strings.ToLower(strings.TrimSpace(string(e.ChannelType)))) {
	case ChannelTypeWeb, ChannelTypeIM, ChannelTypeCLI, ChannelTypeAPI:
	default:
		return fmt.Errorf("channel_type 非法: %s", strings.TrimSpace(string(e.ChannelType)))
	}
	if strings.TrimSpace(e.Adapter) == "" {
		return fmt.Errorf("adapter 不能为空")
	}
	if strings.TrimSpace(e.PeerMessageID) == "" {
		return fmt.Errorf("peer_message_id 不能为空")
	}
	if strings.TrimSpace(e.PeerConversationID) == "" {
		return fmt.Errorf("peer_conversation_id 不能为空")
	}
	if strings.TrimSpace(e.ReceivedAt) == "" {
		return fmt.Errorf("received_at 不能为空")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(e.ReceivedAt)); err != nil {
		return fmt.Errorf("received_at 非法: %v", err)
	}
	return nil
}

const (
	ActionListTickets     = "list_tickets"
	ActionTicketDetail    = "ticket_detail"
	ActionCreateTicket    = "create_ticket"
	ActionStartTicket     = "start_ticket"
	ActionInterruptTicket = "interrupt_ticket"
	ActionStopTicket      = "stop_ticket"
	ActionArchiveTicket   = "archive_ticket"
	ActionListMergeItems  = "list_merge_items"
)

type TurnAction struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

func (a *TurnAction) Normalize() {
	if a == nil {
		return
	}
	a.Name = strings.TrimSpace(a.Name)
	if a.Args == nil {
		a.Args = map[string]any{}
	}
}

func (a TurnAction) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("action.name 不能为空")
	}
	return nil
}

type TurnResponse struct {
	Schema               string       `json:"schema"`
	ReplyText            string       `json:"reply_text"`
	RequiresConfirmation bool         `json:"requires_confirmation"`
	Actions              []TurnAction `json:"actions"`
	Reason               string       `json:"reason"`
}

func (r *TurnResponse) Normalize() {
	if r == nil {
		return
	}
	r.Schema = strings.TrimSpace(r.Schema)
	if r.Schema == "" {
		r.Schema = TurnResponseSchemaV1
	}
	r.ReplyText = strings.TrimSpace(r.ReplyText)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.Actions == nil {
		r.Actions = []TurnAction{}
	}
	for i := range r.Actions {
		r.Actions[i].Normalize()
	}
}

func (r TurnResponse) Validate() error {
	if strings.TrimSpace(r.Schema) != TurnResponseSchemaV1 {
		return fmt.Errorf("turn_response schema 非法: %s", strings.TrimSpace(r.Schema))
	}
	for i := range r.Actions {
		if err := r.Actions[i].Validate(); err != nil {
			return fmt.Errorf("action[%d] 非法: %w", i, err)
		}
	}
	return nil
}
