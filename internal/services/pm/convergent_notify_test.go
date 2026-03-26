package pm

import (
	"context"
	"strings"
	"sync"
	"testing"

	"dalek/internal/contracts"
)

// ---------------------------------------------------------------------------
// mock notifier
// ---------------------------------------------------------------------------

type mockConvergentNotifier struct {
	mu    sync.Mutex
	texts []string
}

func (m *mockConvergentNotifier) NotifyText(_ context.Context, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.texts = append(m.texts, text)
	return nil
}

func (m *mockConvergentNotifier) getTexts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.texts))
	copy(cp, m.texts)
	return cp
}

// ---------------------------------------------------------------------------
// Text building tests
// ---------------------------------------------------------------------------

func TestConvergentBuildConvergedText(t *testing.T) {
	run := contracts.FocusRun{}
	run.ID = 42
	run.PMRunCount = 3
	run.MaxPMRuns = 5
	round := contracts.ConvergentRound{RoundNumber: 3}
	result := PMRunResult{
		Converged:      true,
		FilteredIssues: 2,
		Summary:        "所有问题已修复",
	}

	text := convergentBuildConvergedText(run, round, result)

	if !strings.Contains(text, "Focus #42") {
		t.Errorf("expected Focus #42, got: %s", text)
	}
	if !strings.Contains(text, "CONVERGED") {
		t.Errorf("expected CONVERGED, got: %s", text)
	}
	if !strings.Contains(text, "3/5") {
		t.Errorf("expected PM run count 3/5, got: %s", text)
	}
	if !strings.Contains(text, "过滤问题: 2") {
		t.Errorf("expected 过滤问题: 2, got: %s", text)
	}
	if !strings.Contains(text, "所有问题已修复") {
		t.Errorf("expected summary, got: %s", text)
	}
}

func TestConvergentBuildNeedsFixText(t *testing.T) {
	run := contracts.FocusRun{}
	run.ID = 10
	round := contracts.ConvergentRound{RoundNumber: 2}
	result := PMRunResult{
		Converged:       false,
		EffectiveIssues: 3,
		FilteredIssues:  1,
		FixTicketIDs:    []uint{100, 101},
		Summary:         "发现 3 个 bug",
	}

	text := convergentBuildNeedsFixText(run, round, result)

	if !strings.Contains(text, "Focus #10") {
		t.Errorf("expected Focus #10, got: %s", text)
	}
	if !strings.Contains(text, "NEEDS FIX") {
		t.Errorf("expected NEEDS FIX, got: %s", text)
	}
	if !strings.Contains(text, "有效问题: 3") {
		t.Errorf("expected 有效问题: 3, got: %s", text)
	}
	if !strings.Contains(text, "t100, t101") {
		t.Errorf("expected fix ticket ids, got: %s", text)
	}
	if !strings.Contains(text, "Round 3") {
		t.Errorf("expected Round 3 next step, got: %s", text)
	}
}

func TestConvergentBuildExhaustedText(t *testing.T) {
	run := contracts.FocusRun{}
	run.ID = 7
	run.PMRunCount = 5
	run.MaxPMRuns = 5
	round := contracts.ConvergentRound{
		RoundNumber: 4,
		ReviewPath:  "/tmp/reviews/round-4",
	}

	text := convergentBuildExhaustedText(run, round)

	if !strings.Contains(text, "Focus #7") {
		t.Errorf("expected Focus #7, got: %s", text)
	}
	if !strings.Contains(text, "EXHAUSTED") {
		t.Errorf("expected EXHAUSTED, got: %s", text)
	}
	if !strings.Contains(text, "5/5") {
		t.Errorf("expected 5/5, got: %s", text)
	}
	if !strings.Contains(text, "/tmp/reviews/round-4") {
		t.Errorf("expected review path, got: %s", text)
	}
}

func TestConvergentBuildConvergedText_EmptySummary(t *testing.T) {
	run := contracts.FocusRun{}
	run.ID = 1
	run.PMRunCount = 1
	run.MaxPMRuns = 5
	round := contracts.ConvergentRound{RoundNumber: 1}
	result := PMRunResult{Converged: true, Summary: ""}

	text := convergentBuildConvergedText(run, round, result)

	if strings.Contains(text, "结论:") {
		t.Errorf("should not contain 结论 when summary empty, got: %s", text)
	}
}

func TestConvergentBuildExhaustedText_NoReviewPath(t *testing.T) {
	run := contracts.FocusRun{}
	run.ID = 1
	run.PMRunCount = 3
	run.MaxPMRuns = 3
	round := contracts.ConvergentRound{RoundNumber: 2}

	text := convergentBuildExhaustedText(run, round)

	if strings.Contains(text, "Review 报告") {
		t.Errorf("should not contain Review 报告 when path empty, got: %s", text)
	}
}

// ---------------------------------------------------------------------------
// convergentNotifyAsync integration
// ---------------------------------------------------------------------------

func TestConvergentNotifyAsync_NilNotifier(t *testing.T) {
	svc := &Service{}
	// Should not panic with nil notifier.
	svc.convergentNotifyAsync("hello")
}

func TestConvergentNotifyAsync_EmptyText(t *testing.T) {
	mock := &mockConvergentNotifier{}
	svc := &Service{}
	svc.SetConvergentNotifier(mock)

	svc.convergentNotifyAsync("")
	svc.convergentNotifyAsync("  ")

	// Wait for any goroutines (there should be none).
	svc.WaitStatusChangeHooks(context.Background())

	if got := mock.getTexts(); len(got) != 0 {
		t.Errorf("expected no calls, got %d", len(got))
	}
}

func TestConvergentNotifyAsync_SendsText(t *testing.T) {
	mock := &mockConvergentNotifier{}
	svc := &Service{}
	svc.SetConvergentNotifier(mock)

	svc.convergentNotifyAsync("test notification")
	_ = svc.WaitStatusChangeHooks(context.Background())

	texts := mock.getTexts()
	if len(texts) != 1 {
		t.Fatalf("expected 1 call, got %d", len(texts))
	}
	if texts[0] != "test notification" {
		t.Errorf("expected 'test notification', got %q", texts[0])
	}
}
