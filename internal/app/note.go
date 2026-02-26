package app

import (
	"context"
	"fmt"
	"time"

	"dalek/internal/contracts"
	notebooksvc "dalek/internal/services/notebook"
)

func (p *Project) notebookService() (*notebooksvc.Service, error) {
	if p == nil || p.notebook == nil {
		return nil, fmt.Errorf("project notebook service 为空")
	}
	return p.notebook, nil
}

func (p *Project) AddNote(ctx context.Context, rawText string) (NoteAddResult, error) {
	svc, err := p.notebookService()
	if err != nil {
		return NoteAddResult{}, err
	}
	return svc.AddNote(ctx, rawText)
}

func (p *Project) NotebookShapingSkillPath() string {
	svc, err := p.notebookService()
	if err != nil {
		return ""
	}
	return svc.NotebookShapingSkillPath()
}

func (p *Project) ProcessOnePendingNote(ctx context.Context) (bool, error) {
	svc, err := p.notebookService()
	if err != nil {
		return false, err
	}
	return svc.ProcessOnePendingNote(ctx)
}

func (p *Project) RecoverStuckShapingNotes(ctx context.Context, stale time.Duration) (int, error) {
	svc, err := p.notebookService()
	if err != nil {
		return 0, err
	}
	return svc.RecoverStuckShapingNotes(ctx, stale)
}

func (p *Project) ListNotes(ctx context.Context, opt ListNoteOptions) ([]NoteView, error) {
	svc, err := p.notebookService()
	if err != nil {
		return nil, err
	}
	return svc.ListNotes(ctx, opt)
}

func (p *Project) GetNote(ctx context.Context, id uint) (*NoteView, error) {
	svc, err := p.notebookService()
	if err != nil {
		return nil, err
	}
	return svc.GetNote(ctx, id)
}

func (p *Project) ApproveNote(ctx context.Context, id uint, reviewedBy string) (*contracts.Ticket, error) {
	svc, err := p.notebookService()
	if err != nil {
		return nil, err
	}
	return svc.ApproveNote(ctx, id, reviewedBy)
}

func (p *Project) RejectNote(ctx context.Context, id uint, reason string) error {
	svc, err := p.notebookService()
	if err != nil {
		return err
	}
	return svc.RejectNote(ctx, id, reason)
}

func (p *Project) DiscardNote(ctx context.Context, id uint) error {
	svc, err := p.notebookService()
	if err != nil {
		return err
	}
	return svc.DiscardNote(ctx, id)
}
