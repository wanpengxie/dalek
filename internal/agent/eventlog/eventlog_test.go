package eventlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesFileAndDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALEK_HOME", dir)

	logger, err := Open("myproject", "run-001")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer logger.Close()

	path := filepath.Join(dir, "logs", "myproject", "run-001.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
}

func TestWriteHeader(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALEK_HOME", dir)

	logger, err := Open("proj", "run-h")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	err = logger.WriteHeader(RunMeta{
		RunID:    "run-h",
		Project:  "proj",
		Provider: "claude",
		Model:    "claude-opus-4-6",
		Layer:    "chat_runner",
	})
	if err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	logger.Close()

	rec := readFirstLine(t, filepath.Join(dir, "logs", "proj", "run-h.jsonl"))
	assertEqual(t, rec["schema"], "dalek.run_event_log.v1")
	assertEqual(t, rec["phase"], "start")
	assertEqual(t, rec["run_id"], "run-h")
	assertEqual(t, rec["project"], "proj")
	assertEqual(t, rec["provider"], "claude")
	assertEqual(t, rec["model"], "claude-opus-4-6")
	assertEqual(t, rec["layer"], "chat_runner")
	if _, ok := rec["ts"]; !ok {
		t.Fatal("missing ts field")
	}
}

func TestWriteEventNoDoubleEscape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALEK_HOME", dir)

	logger, err := Open("proj", "run-e")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rawJSON := `{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"hello"}}`
	err = logger.WriteEvent(1, "item.completed", rawJSON)
	if err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	logger.Close()

	rec := readFirstLine(t, filepath.Join(dir, "logs", "proj", "run-e.jsonl"))
	assertEqual(t, rec["schema"], "dalek.run_event_log.v1")
	assertEqual(t, rec["phase"], "event")
	// seq should be float64 from json.Unmarshal
	if seq, ok := rec["seq"].(float64); !ok || seq != 1 {
		t.Fatalf("expected seq=1, got %v", rec["seq"])
	}
	assertEqual(t, rec["event_type"], "item.completed")

	// raw 字段应为对象而非转义字符串
	rawMap, ok := rec["raw"].(map[string]any)
	if !ok {
		t.Fatalf("expected raw to be object, got %T: %v", rec["raw"], rec["raw"])
	}
	if rawMap["type"] != "item.completed" {
		t.Fatalf("raw.type: %v", rawMap["type"])
	}
}

func TestWriteFooter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALEK_HOME", dir)

	logger, err := Open("proj", "run-f")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	err = logger.WriteFooter(RunFooter{
		RunID:      "run-f",
		ReplyText:  "done",
		DurationMS: 12345,
		SessionID:  "sess-1",
	})
	if err != nil {
		t.Fatalf("WriteFooter: %v", err)
	}
	logger.Close()

	rec := readFirstLine(t, filepath.Join(dir, "logs", "proj", "run-f.jsonl"))
	assertEqual(t, rec["schema"], "dalek.run_event_log.v1")
	assertEqual(t, rec["phase"], "end")
	assertEqual(t, rec["run_id"], "run-f")
	assertEqual(t, rec["reply_text"], "done")
	assertEqual(t, rec["session_id"], "sess-1")
	if dur, ok := rec["duration_ms"].(float64); !ok || dur != 12345 {
		t.Fatalf("expected duration_ms=12345, got %v", rec["duration_ms"])
	}
}

func TestFullLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALEK_HOME", dir)

	logger, err := Open("proj", "run-full")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := logger.WriteHeader(RunMeta{
		RunID:    "run-full",
		Project:  "proj",
		Provider: "codex",
		Model:    "gpt-5.3-codex",
	}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	events := []struct {
		seq       int
		eventType string
		raw       string
	}{
		{1, "thread.started", `{"type":"thread.started","thread_id":"t1"}`},
		{2, "turn.started", `{"type":"turn.started"}`},
		{3, "item.completed", `{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"thinking..."}}`},
	}
	for _, ev := range events {
		if err := logger.WriteEvent(ev.seq, ev.eventType, ev.raw); err != nil {
			t.Fatalf("WriteEvent seq=%d: %v", ev.seq, err)
		}
	}

	if err := logger.WriteFooter(RunFooter{
		RunID:      "run-full",
		ReplyText:  "all done",
		DurationMS: 569150,
	}); err != nil {
		t.Fatalf("WriteFooter: %v", err)
	}
	logger.Close()

	path := filepath.Join(dir, "logs", "proj", "run-full.jsonl")
	lines := readAllLines(t, path)
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}

	// header
	assertEqual(t, lines[0]["phase"], "start")
	assertEqual(t, lines[0]["provider"], "codex")

	// events
	for i := 1; i <= 3; i++ {
		assertEqual(t, lines[i]["phase"], "event")
	}

	// footer
	assertEqual(t, lines[4]["phase"], "end")
	assertEqual(t, lines[4]["reply_text"], "all done")
}

func TestWriteEventEmptyRawJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALEK_HOME", dir)

	logger, err := Open("proj", "run-empty")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := logger.WriteEvent(1, "stream_event", ""); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	logger.Close()

	rec := readFirstLine(t, filepath.Join(dir, "logs", "proj", "run-empty.jsonl"))
	assertEqual(t, rec["event_type"], "stream_event")
	if _, ok := rec["raw"]; ok {
		t.Fatal("expected no raw field for empty rawJSON")
	}
}

func TestWriteEventInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DALEK_HOME", dir)

	logger, err := Open("proj", "run-invalid")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := logger.WriteEvent(1, "broken", "not valid json {{{"); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	logger.Close()

	rec := readFirstLine(t, filepath.Join(dir, "logs", "proj", "run-invalid.jsonl"))
	assertEqual(t, rec["event_type"], "broken")
	// raw 应 fallback 为字符串
	raw, ok := rec["raw"].(string)
	if !ok {
		t.Fatalf("expected raw to be string, got %T: %v", rec["raw"], rec["raw"])
	}
	if raw != "not valid json {{{" {
		t.Fatalf("unexpected raw value: %s", raw)
	}
}

// --- helpers ---

func readFirstLine(t *testing.T, path string) map[string]any {
	t.Helper()
	lines := readAllLines(t, path)
	if len(lines) == 0 {
		t.Fatalf("file %s is empty", path)
	}
	return lines[0]
}

func readAllLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var result []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("unmarshal line: %v\nline: %s", err, scanner.Text())
		}
		result = append(result, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	return result
}

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
