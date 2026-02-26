package notebook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dalek/internal/store"
	"dalek/internal/testutil"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	cp, _, _ := testutil.NewTestProject(t)
	return New(cp)
}

func ensureNotebookSkill(t *testing.T, svc *Service) {
	t.Helper()
	skillPath := svc.NotebookShapingSkillPath()
	if strings.TrimSpace(skillPath) == "" {
		t.Fatalf("skill path should not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll skill dir failed: %v", err)
	}
	content := `---
version: "1"
defaults:
  scope_estimate: "L"
  acceptance_template: |
    - [ ] CSV 导出支持批量与筛选
    - [ ] 覆盖导出成功与失败路径测试
title_rules:
  max_length: 60
  strip_markdown: true
---

# notebook shaping

优先突出业务目标与验收边界。`
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile skill failed: %v", err)
	}
}

func TestService_AddNote_DedupByHash(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	first, err := svc.AddNote(ctx, "支持导出 CSV")
	if err != nil {
		t.Fatalf("AddNote(first) failed: %v", err)
	}
	if first.Deduped {
		t.Fatalf("first add should not dedup")
	}

	second, err := svc.AddNote(ctx, "  支持导出   CSV  ")
	if err != nil {
		t.Fatalf("AddNote(second) failed: %v", err)
	}
	if !second.Deduped {
		t.Fatalf("second add should dedup")
	}
	if second.NoteID != first.NoteID {
		t.Fatalf("dedup note id mismatch: got=%d want=%d", second.NoteID, first.NoteID)
	}
}

func TestService_ProcessOnePendingNote_AndList(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	ensureNotebookSkill(t, svc)

	added, err := svc.AddNote(ctx, "支持批量导出 CSV")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	ok, err := svc.ProcessOnePendingNote(ctx)
	if err != nil {
		t.Fatalf("ProcessOnePendingNote failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected one note processed")
	}

	list, err := svc.ListNotes(ctx, ListNoteOptions{StatusOnly: string(store.NoteShaped), Limit: 10})
	if err != nil {
		t.Fatalf("ListNotes failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one shaped note, got=%d", len(list))
	}
	item := list[0]
	if item.ID != added.NoteID {
		t.Fatalf("note id mismatch: got=%d want=%d", item.ID, added.NoteID)
	}
	if item.Status != string(store.NoteShaped) {
		t.Fatalf("expected shaped status, got=%s", item.Status)
	}
	if item.Shaped == nil {
		t.Fatalf("expected shaped view")
	}
	if strings.TrimSpace(item.Shaped.ScopeEstimate) != "L" {
		t.Fatalf("expected scope estimate from skill, got=%q", item.Shaped.ScopeEstimate)
	}
	var acceptance []string
	if err := json.Unmarshal([]byte(item.Shaped.AcceptanceJSON), &acceptance); err != nil {
		t.Fatalf("unmarshal acceptance_json failed: %v", err)
	}
	if len(acceptance) != 2 {
		t.Fatalf("expected 2 acceptance items, got=%d raw=%s", len(acceptance), item.Shaped.AcceptanceJSON)
	}
}

func TestService_ApproveNote_CreatesTicketAndUpdatesShaped(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	ensureNotebookSkill(t, svc)

	added, err := svc.AddNote(ctx, "审批测试：支持导出 CSV")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	if _, err := svc.ProcessOnePendingNote(ctx); err != nil {
		t.Fatalf("ProcessOnePendingNote failed: %v", err)
	}

	tk, err := svc.ApproveNote(ctx, added.NoteID, "pm")
	if err != nil {
		t.Fatalf("ApproveNote failed: %v", err)
	}
	if tk == nil || tk.ID == 0 {
		t.Fatalf("expected created ticket")
	}

	note, err := svc.GetNote(ctx, added.NoteID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if note == nil || note.Shaped == nil {
		t.Fatalf("expected shaped note exists")
	}
	if note.Shaped.Status != string(store.ShapedApproved) {
		t.Fatalf("expected shaped approved, got=%s", note.Shaped.Status)
	}
	if note.Shaped.TicketID != tk.ID {
		t.Fatalf("ticket id mismatch: got=%d want=%d", note.Shaped.TicketID, tk.ID)
	}
}
