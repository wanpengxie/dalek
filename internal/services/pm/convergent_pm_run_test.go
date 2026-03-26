package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dalek/internal/services/subagent"
)

// ---------------------------------------------------------------------------
// mock PMRunSubmitter
// ---------------------------------------------------------------------------

type mockPMRunSubmitter struct {
	submitFunc func(ctx context.Context, in subagent.SubmitInput) (subagent.SubmitResult, error)
	lastInput  subagent.SubmitInput
}

func (m *mockPMRunSubmitter) Submit(ctx context.Context, in subagent.SubmitInput) (subagent.SubmitResult, error) {
	m.lastInput = in
	if m.submitFunc != nil {
		return m.submitFunc(ctx, in)
	}
	return subagent.SubmitResult{
		Accepted:  true,
		TaskRunID: 42,
		RequestID: in.RequestID,
		Provider:  in.Provider,
		Model:     in.Model,
	}, nil
}

// ---------------------------------------------------------------------------
// buildPMRunPrompt tests
// ---------------------------------------------------------------------------

func TestBuildPMRunPrompt_ContainsDynamicVariables(t *testing.T) {
	input := PMRunInput{
		FocusID:     7,
		RoundNumber: 2,
		TicketIDs:   []uint{10, 11, 12},
		ReviewDir:   ".dalek/pm/reviews/convergent-7/round-2",
	}

	prompt := buildPMRunPrompt(input)

	checks := []struct {
		name    string
		pattern string
	}{
		{"focus_id", "Convergent Focus ID: 7"},
		{"round_number", "Round 2"},
		{"ticket_ids", "t10, t11, t12"},
		{"review_dir", ".dalek/pm/reviews/convergent-7/round-2"},
		{"codex_reviewer", "--provider codex"},
		{"claude_reviewer", "--provider claude"},
		{"review_codex_output", "review-codex.md"},
		{"review_claude_output", "review-claude.md"},
		{"synthesis", "synthesis.md"},
		{"fix_spec", "fix-spec.md"},
		{"result_json", "result.json"},
		{"verdict_converged", "converged"},
		{"verdict_needs_fix", "needs_fix"},
		{"git_checkout", "git checkout -- ."},
		{"git_clean", "git clean -fd"},
		{"round_in_fix_title", "[fix] R2"},
	}

	for _, c := range checks {
		if !strings.Contains(prompt, c.pattern) {
			t.Errorf("prompt missing %s pattern %q", c.name, c.pattern)
		}
	}
}

func TestBuildPMRunPrompt_WithReviewScope(t *testing.T) {
	input := PMRunInput{
		FocusID:     10,
		RoundNumber: 1,
		TicketIDs:   nil,
		ReviewDir:   ".dalek/pm/reviews/convergent-10/round-1",
		ReviewScope: "审查 main 分支最近 5 个 commits 的代码变更",
	}

	prompt := buildPMRunPrompt(input)

	checks := []struct {
		name    string
		pattern string
	}{
		{"focus_id", "Convergent Focus ID: 10"},
		{"round_number", "Round 1"},
		{"review_scope", "审查 main 分支最近 5 个 commits 的代码变更"},
		{"review_dir", ".dalek/pm/reviews/convergent-10/round-1"},
		{"codex_reviewer", "--provider codex"},
		{"claude_reviewer", "--provider claude"},
		{"result_json", "result.json"},
		{"git_log", "git log"},
		{"git_diff", "git diff"},
	}

	for _, c := range checks {
		if !strings.Contains(prompt, c.pattern) {
			t.Errorf("review-scope prompt missing %s pattern %q", c.name, c.pattern)
		}
	}

	// Should NOT contain the ticket-based prompt content
	if strings.Contains(prompt, "batch run 已完成") {
		t.Error("review-scope prompt should not contain batch-based preamble")
	}
}

func TestBuildPMRunPrompt_ReviewScopeOverridesTickets(t *testing.T) {
	// When both ReviewScope and TicketIDs are set, ReviewScope takes precedence
	input := PMRunInput{
		FocusID:     5,
		RoundNumber: 1,
		TicketIDs:   []uint{10, 11},
		ReviewDir:   "/tmp/review",
		ReviewScope: "审查特定范围",
	}

	prompt := buildPMRunPrompt(input)
	if !strings.Contains(prompt, "审查特定范围") {
		t.Error("expected review scope in prompt")
	}
	if strings.Contains(prompt, "batch run 已完成") {
		t.Error("review-scope prompt should not contain batch-based preamble")
	}
}

func TestBuildPMRunPrompt_EmptyTicketIDs(t *testing.T) {
	input := PMRunInput{
		FocusID:     1,
		RoundNumber: 1,
		TicketIDs:   nil,
		ReviewDir:   "/tmp/review",
	}

	prompt := buildPMRunPrompt(input)
	if !strings.Contains(prompt, "(无)") {
		t.Error("expected '(无)' for empty ticket list")
	}
}

func TestBuildTicketIDList(t *testing.T) {
	tests := []struct {
		ids  []uint
		want string
	}{
		{nil, "(无)"},
		{[]uint{}, "(无)"},
		{[]uint{5}, "t5"},
		{[]uint{1, 2, 3}, "t1, t2, t3"},
	}
	for _, tt := range tests {
		got := buildTicketIDList(tt.ids)
		if got != tt.want {
			t.Errorf("buildTicketIDList(%v) = %q, want %q", tt.ids, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// parsePMRunResult tests
// ---------------------------------------------------------------------------

func TestParsePMRunResult_Converged(t *testing.T) {
	dir := t.TempDir()
	writeResultJSON(t, dir, pmRunResultFile{
		Verdict:              "converged",
		FixTicketIDs:         nil,
		EffectiveIssuesCount: 0,
		FilteredIssuesCount:  3,
		Summary:              "所有代码审查通过",
	})

	result, err := parsePMRunResult(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Converged {
		t.Error("expected Converged=true")
	}
	if len(result.FixTicketIDs) != 0 {
		t.Errorf("expected no fix tickets, got %v", result.FixTicketIDs)
	}
	if result.FilteredIssues != 3 {
		t.Errorf("expected FilteredIssues=3, got %d", result.FilteredIssues)
	}
	if result.Summary != "所有代码审查通过" {
		t.Errorf("unexpected summary: %q", result.Summary)
	}
}

func TestParsePMRunResult_NeedsFix(t *testing.T) {
	dir := t.TempDir()
	writeResultJSON(t, dir, pmRunResultFile{
		Verdict:              "needs_fix",
		FixTicketIDs:         []uint{25, 26},
		EffectiveIssuesCount: 2,
		FilteredIssuesCount:  1,
		Summary:              "发现 2 个 critical bug",
	})

	result, err := parsePMRunResult(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Converged {
		t.Error("expected Converged=false")
	}
	if len(result.FixTicketIDs) != 2 || result.FixTicketIDs[0] != 25 || result.FixTicketIDs[1] != 26 {
		t.Errorf("unexpected fix tickets: %v", result.FixTicketIDs)
	}
	if result.EffectiveIssues != 2 {
		t.Errorf("expected EffectiveIssues=2, got %d", result.EffectiveIssues)
	}
}

func TestParsePMRunResult_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := parsePMRunResult(dir)
	if err == nil {
		t.Fatal("expected error for missing result.json")
	}
	if !strings.Contains(err.Error(), "未产出 result.json") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParsePMRunResult_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte("   "), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := parsePMRunResult(dir)
	if err == nil {
		t.Fatal("expected error for empty result.json")
	}
	if !strings.Contains(err.Error(), "内容为空") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParsePMRunResult_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte("{not json}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := parsePMRunResult(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "解析 result.json 失败") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParsePMRunResult_InvalidVerdict(t *testing.T) {
	dir := t.TempDir()
	writeResultJSON(t, dir, pmRunResultFile{
		Verdict: "unknown_value",
		Summary: "bad",
	})
	_, err := parsePMRunResult(dir)
	if err == nil {
		t.Fatal("expected error for invalid verdict")
	}
	if !strings.Contains(err.Error(), "verdict 值非法") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParsePMRunResult_EmptyDir(t *testing.T) {
	_, err := parsePMRunResult("")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
	if !strings.Contains(err.Error(), "路径为空") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ensureReviewDir tests
// ---------------------------------------------------------------------------

func TestEnsureReviewDir_CreatesDirectory(t *testing.T) {
	root := t.TempDir()
	dir, err := ensureReviewDir(root, 7, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(root, ".dalek", "pm", "reviews", "convergent-7", "round-3")
	if dir != expected {
		t.Errorf("expected dir=%q, got=%q", expected, dir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
}

func TestEnsureReviewDir_Idempotent(t *testing.T) {
	root := t.TempDir()
	dir1, err := ensureReviewDir(root, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	dir2, err := ensureReviewDir(root, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if dir1 != dir2 {
		t.Errorf("expected same dir, got %q and %q", dir1, dir2)
	}
}

func TestEnsureReviewDir_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		focusID  uint
		round    int
		wantErr  string
	}{
		{"empty_root", "", 1, 1, "repo root 路径为空"},
		{"zero_focus", "/tmp", 0, 1, "focus ID 不能为 0"},
		{"zero_round", "/tmp", 1, 0, "round 必须 >= 1"},
		{"negative_round", "/tmp", 1, -1, "round 必须 >= 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ensureReviewDir(tt.root, tt.focusID, tt.round)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// submitPMRun tests
// ---------------------------------------------------------------------------

func TestSubmitPMRun_Success(t *testing.T) {
	svc, _, _ := newServiceForTest(t)

	mock := &mockPMRunSubmitter{}
	input := PMRunInput{
		FocusID:     5,
		RoundNumber: 1,
		TicketIDs:   []uint{10, 11},
		ReviewDir:   ".dalek/pm/reviews/convergent-5/round-1",
	}

	result, err := svc.submitPMRun(context.Background(), mock, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TaskRunID != 42 {
		t.Errorf("expected TaskRunID=42, got=%d", result.TaskRunID)
	}

	// Verify the submitted prompt contains dynamic variables
	if !strings.Contains(mock.lastInput.Prompt, "Convergent Focus ID: 5") {
		t.Error("submitted prompt missing focus_id")
	}
	if !strings.Contains(mock.lastInput.Prompt, "t10, t11") {
		t.Error("submitted prompt missing ticket IDs")
	}
	if strings.TrimSpace(mock.lastInput.Provider) == "" {
		t.Error("submitted provider should not be empty")
	}
}

func TestSubmitPMRun_SubmitError(t *testing.T) {
	svc, _, _ := newServiceForTest(t)

	mock := &mockPMRunSubmitter{
		submitFunc: func(ctx context.Context, in subagent.SubmitInput) (subagent.SubmitResult, error) {
			return subagent.SubmitResult{}, fmt.Errorf("submit failed")
		},
	}

	_, err := svc.submitPMRun(context.Background(), mock, PMRunInput{
		FocusID:     1,
		RoundNumber: 1,
		TicketIDs:   []uint{1},
		ReviewDir:   "/tmp/review",
	})
	if err == nil {
		t.Fatal("expected error from submit failure")
	}
	if !strings.Contains(err.Error(), "submit failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSubmitPMRun_NilSubmitter(t *testing.T) {
	svc, _, _ := newServiceForTest(t)

	_, err := svc.submitPMRun(context.Background(), nil, PMRunInput{
		FocusID:     1,
		RoundNumber: 1,
		TicketIDs:   []uint{1},
		ReviewDir:   "/tmp/review",
	})
	if err == nil {
		t.Fatal("expected error for nil submitter")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeResultJSON(t *testing.T, dir string, data pmRunResultFile) {
	t.Helper()
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), raw, 0o644); err != nil {
		t.Fatalf("write result.json: %v", err)
	}
}
