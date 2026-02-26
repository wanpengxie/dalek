package ws

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/store"
)

func ParseInboundText(payload []byte) (string, string, error) {
	raw := strings.TrimSpace(string(payload))
	if raw == "" {
		return "", "", fmt.Errorf("消息不能为空")
	}

	var frame InboundFrame
	if err := json.Unmarshal(payload, &frame); err == nil {
		text := strings.TrimSpace(frame.Text)
		senderID := strings.TrimSpace(frame.SenderID)
		if text == "" {
			return "", "", fmt.Errorf("消息不能为空")
		}
		return text, senderID, nil
	}

	var asString string
	if err := json.Unmarshal(payload, &asString); err == nil {
		text := strings.TrimSpace(asString)
		if text == "" {
			return "", "", fmt.Errorf("消息不能为空")
		}
		return text, "", nil
	}

	return raw, "", nil
}

func NormalizePath(rawPath string) string {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "/ws"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func BuildConversationID(prefix string) string {
	p := strings.TrimSpace(prefix)
	if p == "" {
		p = "ws"
	}
	return fmt.Sprintf("%s-%s", p, randomHex(4))
}

func NextPeerMessageID(seq uint64) string {
	return fmt.Sprintf("ws-%d-%d-%s", time.Now().UnixNano(), seq, randomHex(2))
}

func DeriveEventType(stream, phase string) string {
	stream = strings.TrimSpace(stream)
	phase = strings.TrimSpace(phase)
	if stream == "lifecycle" {
		if phase != "" {
			return phase
		}
		return "lifecycle"
	}
	if stream == "assistant" {
		return "assistant"
	}
	if stream == "error" {
		return "error"
	}
	if stream == "tool" {
		return "tool"
	}
	return phase
}

func BuildInboxUpdateFrame(conversationID string, items []store.InboxItem) OutboundFrame {
	return OutboundFrame{
		Type:           FrameTypeInboxUpdate,
		ConversationID: strings.TrimSpace(conversationID),
		Text:           FormatInboxSummary(items),
		InboxCount:     len(items),
		InboxItems:     toInboxItems(items),
		At:             FormatTimestamp(time.Now()),
	}
}

func FormatInboxSummary(items []store.InboxItem) string {
	if len(items) == 0 {
		return "inbox(open)=0"
	}
	const previewN = 3
	lines := make([]string, 0, previewN+1)
	lines = append(lines, fmt.Sprintf("inbox(open)=%d", len(items)))
	n := len(items)
	if n > previewN {
		n = previewN
	}
	for i := 0; i < n; i++ {
		it := items[i]
		lines = append(lines, fmt.Sprintf("#%d %s/%s t%d %s",
			it.ID,
			strings.TrimSpace(string(it.Severity)),
			strings.TrimSpace(string(it.Reason)),
			it.TicketID,
			strings.TrimSpace(it.Title),
		))
	}
	return strings.Join(lines, "\n")
}

func FormatTimestamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}

func digestInboxItems(items []store.InboxItem) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%d|%s|%s|%s|%s|%d|%d|%s",
			it.ID,
			strings.TrimSpace(string(it.Status)),
			strings.TrimSpace(string(it.Severity)),
			strings.TrimSpace(string(it.Reason)),
			strings.TrimSpace(it.Title),
			it.TicketID,
			it.WorkerID,
			it.UpdatedAt.Local().Format(time.RFC3339Nano),
		))
	}
	return strings.Join(parts, ";")
}

func toInboxItems(items []store.InboxItem) []InboxItem {
	out := make([]InboxItem, 0, len(items))
	for _, it := range items {
		out = append(out, InboxItem{
			ID:        it.ID,
			Status:    strings.TrimSpace(string(it.Status)),
			Severity:  strings.TrimSpace(string(it.Severity)),
			Reason:    strings.TrimSpace(string(it.Reason)),
			Title:     strings.TrimSpace(it.Title),
			TicketID:  it.TicketID,
			WorkerID:  it.WorkerID,
			UpdatedAt: it.UpdatedAt.Local().Format(time.RFC3339),
		})
	}
	return out
}

func randomHex(nbytes int) string {
	if nbytes <= 0 {
		nbytes = 4
	}
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
