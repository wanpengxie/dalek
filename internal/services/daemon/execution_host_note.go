package daemon

import (
	"context"
	"fmt"
	"strings"
)

func (h *ExecutionHost) SubmitNote(ctx context.Context, req NoteSubmitRequest) (NoteSubmitReceipt, error) {
	if h == nil || h.resolver == nil {
		return NoteSubmitReceipt{}, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		return NoteSubmitReceipt{}, fmt.Errorf("project 不能为空")
	}
	raw := strings.TrimSpace(req.Text)
	if raw == "" {
		return NoteSubmitReceipt{}, fmt.Errorf("note text 不能为空")
	}
	project, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return NoteSubmitReceipt{}, err
	}
	res, err := project.AddNote(ctx, raw)
	if err != nil {
		return NoteSubmitReceipt{}, err
	}
	h.notifyNoteAdded(projectName)
	return NoteSubmitReceipt{
		Accepted:     true,
		Project:      projectName,
		NoteID:       res.NoteID,
		ShapedItemID: res.ShapedItemID,
		Deduped:      res.Deduped,
	}, nil
}
