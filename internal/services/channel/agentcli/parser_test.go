package agentcli

import (
	"strings"
	"testing"
)

func TestParseOutput_JSONFallbackRaw(t *testing.T) {
	raw := "not-json"
	text, sessionID, events := parseOutput(raw, OutputJSON, Backend{})
	if text != "not-json" {
		t.Fatalf("unexpected text: %q", text)
	}
	if sessionID != "" {
		t.Fatalf("unexpected session id: %q", sessionID)
	}
	if len(events) != 0 {
		t.Fatalf("unexpected events len: %d", len(events))
	}
}

func TestParseOutput_JSONWithWrapperLine(t *testing.T) {
	raw := `[claude-wrapper] 未检测到 claude-code-proxy，直接连接 Anthropic
{"type":"result","result":"你好！","session_id":"db432141-22f7-43e2-8d4a-15c5928ec3f6"}`
	text, sessionID, events := parseOutput(raw, OutputJSON, Backend{})
	if strings.TrimSpace(text) != "你好！" {
		t.Fatalf("unexpected text: %q", text)
	}
	if strings.TrimSpace(sessionID) != "db432141-22f7-43e2-8d4a-15c5928ec3f6" {
		t.Fatalf("unexpected session id: %q", sessionID)
	}
	if len(events) != 0 {
		t.Fatalf("unexpected events len: %d", len(events))
	}
}

func TestParseJSON_ContentArray(t *testing.T) {
	raw := `{"sessionId":"abc","content":[{"text":"hello"},{"text":"world"}]}`
	text, sid, ok := parseJSON(raw, nil)
	if !ok {
		t.Fatalf("parseJSON should succeed")
	}
	if strings.TrimSpace(sid) != "abc" {
		t.Fatalf("unexpected sid: %q", sid)
	}
	if strings.TrimSpace(text) != "hello\nworld" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestParseJSONL_NoValidLine(t *testing.T) {
	_, _, _, ok := parseJSONL("line1\nline2", nil)
	if ok {
		t.Fatalf("parseJSONL should fail for non-jsonl payload")
	}
}
