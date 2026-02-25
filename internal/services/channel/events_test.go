package channel

import (
	"errors"
	"testing"
	"time"

	"dalek/internal/services/channel/agentcli"
)

func TestSynthesizeEventsFromCLIResult_Success(t *testing.T) {
	startedAt := time.Unix(1700000000, 0)
	events := SynthesizeEventsFromCLIResult("run-test-1", startedAt, []agentcli.Event{
		{Type: "assistant", Text: "delta-1", RawJSON: `{"content":[{"text":"delta-1"}],"model":"claude-opus-4-6"}`},
		{Type: "assistant", Text: "delta-2", RawJSON: `{"content":[{"text":"delta-2"}],"model":"claude-opus-4-6"}`},
	}, "final-reply", nil, "claude")

	// text-only assistant 事件被 renderer 过滤，仅保留显式 AppendAssistantText 的 final-reply。
	// 期望: lifecycle-start + 1 个 assistant final-reply + lifecycle-end = 3
	if len(events) != 3 {
		t.Fatalf("unexpected events len: %d", len(events))
	}
	if events[0].Stream != StreamLifecycle || events[0].Data.Phase != "start" {
		t.Fatalf("event[0] should be lifecycle start, got %+v", events[0])
	}
	if events[1].Stream != StreamAssistant || events[1].Data.Text != "final-reply" {
		t.Fatalf("event[1] should be assistant final, got %+v", events[1])
	}
	if events[2].Stream != StreamLifecycle || events[2].Data.Phase != "end" {
		t.Fatalf("event[2] should be lifecycle end, got %+v", events[2])
	}
	for _, ev := range events {
		if ev.RunID != "run-test-1" {
			t.Fatalf("run_id mismatch: %s", ev.RunID)
		}
	}
}

func TestSynthesizeEventsFromCLIResult_Error(t *testing.T) {
	startedAt := time.Unix(1700000000, 0)
	runErr := errors.New("network broken pipe")
	events := SynthesizeEventsFromCLIResult("run-test-2", startedAt, nil, "", runErr, "claude")

	if len(events) != 2 {
		t.Fatalf("unexpected events len: %d", len(events))
	}
	if events[0].Stream != StreamLifecycle || events[0].Data.Phase != "start" {
		t.Fatalf("event[0] should be lifecycle start, got %+v", events[0])
	}
	if events[1].Stream != StreamLifecycle || events[1].Data.Phase != "error" {
		t.Fatalf("event[1] should be lifecycle error, got %+v", events[1])
	}
	if events[1].Data.ErrorType != "network" {
		t.Fatalf("error type mismatch: %s", events[1].Data.ErrorType)
	}
}

func TestAppendLifecycleErrorEvent(t *testing.T) {
	startedAt := time.Unix(1700000000, 0)
	in := SynthesizeEventsFromCLIResult("run-test-3", startedAt, nil, "ok", nil, "claude")
	out := AppendLifecycleErrorEvent("run-test-3", startedAt, in, errors.New("context deadline exceeded"))

	if len(out) != len(in)+1 {
		t.Fatalf("append error event should increase length by 1, got in=%d out=%d", len(in), len(out))
	}
	last := out[len(out)-1]
	if last.Stream != StreamLifecycle || last.Data.Phase != "error" {
		t.Fatalf("last event should be lifecycle error, got %+v", last)
	}
	if last.Seq <= out[len(out)-2].Seq {
		t.Fatalf("last seq should be increasing, got prev=%d last=%d", out[len(out)-2].Seq, last.Seq)
	}
}
