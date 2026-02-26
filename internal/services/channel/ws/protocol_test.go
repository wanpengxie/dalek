package ws

import (
	"strings"
	"testing"
	"time"

	"dalek/internal/store"
)

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: "/ws"},
		{in: "ws", want: "/ws"},
		{in: "/chat", want: "/chat"},
		{in: " /v1/ws ", want: "/v1/ws"},
	}
	for _, tc := range cases {
		got := NormalizePath(tc.in)
		if got != tc.want {
			t.Fatalf("NormalizePath(%q)=%q, want=%q", tc.in, got, tc.want)
		}
	}
}

func TestParseInboundText(t *testing.T) {
	text, sender, err := ParseInboundText([]byte("hello"))
	if err != nil {
		t.Fatalf("plain text parse failed: %v", err)
	}
	if text != "hello" || sender != "" {
		t.Fatalf("plain text parse unexpected: text=%q sender=%q", text, sender)
	}

	text, sender, err = ParseInboundText([]byte(`{"text":"  hi  ","sender_id":"u1"}`))
	if err != nil {
		t.Fatalf("json text parse failed: %v", err)
	}
	if text != "hi" || sender != "u1" {
		t.Fatalf("json text parse unexpected: text=%q sender=%q", text, sender)
	}

	if _, _, err := ParseInboundText([]byte(`{"text":"   "}`)); err == nil {
		t.Fatalf("expected empty text to fail")
	}
}

func TestFormatInboxSummary(t *testing.T) {
	if got := FormatInboxSummary(nil); got != "inbox(open)=0" {
		t.Fatalf("empty summary mismatch: %q", got)
	}

	items := []store.InboxItem{
		{
			ID:       1,
			Severity: store.InboxWarn,
			Reason:   store.InboxQuestion,
			Title:    "first",
			TicketID: 11,
		},
		{
			ID:       2,
			Severity: store.InboxBlocker,
			Reason:   store.InboxIncident,
			Title:    "second",
			TicketID: 12,
		},
	}
	got := FormatInboxSummary(items)
	if !strings.Contains(got, "inbox(open)=2") || !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("summary unexpected:\n%s", got)
	}
}

func TestBuildInboxUpdateFrame(t *testing.T) {
	items := []store.InboxItem{
		{
			ID:        7,
			Status:    store.InboxOpen,
			Severity:  store.InboxWarn,
			Reason:    store.InboxQuestion,
			Title:     "need follow up",
			TicketID:  3,
			WorkerID:  0,
			UpdatedAt: time.Unix(1700000000, 0).UTC(),
		},
	}
	frame := BuildInboxUpdateFrame("conv-1", items)
	if frame.Type != FrameTypeInboxUpdate {
		t.Fatalf("type mismatch: %s", frame.Type)
	}
	if frame.ConversationID != "conv-1" {
		t.Fatalf("conversation mismatch: %s", frame.ConversationID)
	}
	if frame.InboxCount != 1 || len(frame.InboxItems) != 1 {
		t.Fatalf("inbox count mismatch: count=%d items=%d", frame.InboxCount, len(frame.InboxItems))
	}
	if !strings.Contains(frame.Text, "need follow up") {
		t.Fatalf("summary text unexpected: %s", frame.Text)
	}
}

func TestDeriveEventType(t *testing.T) {
	cases := []struct {
		stream string
		phase  string
		want   string
	}{
		{stream: "lifecycle", phase: "start", want: "start"},
		{stream: "assistant", phase: "", want: "assistant"},
		{stream: "tool", phase: "", want: "tool"},
		{stream: "error", phase: "", want: "error"},
		{stream: "", phase: "end", want: "end"},
	}
	for _, tc := range cases {
		got := DeriveEventType(tc.stream, tc.phase)
		if got != tc.want {
			t.Fatalf("DeriveEventType(%q,%q)=%q want=%q", tc.stream, tc.phase, got, tc.want)
		}
	}
}
