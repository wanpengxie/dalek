package ticket

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) requireDB() (*gorm.DB, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ticket service db 为空")
	}
	return s.db, nil
}

func (s *Service) Create(ctx context.Context, title string) (*store.Ticket, error) {
	return s.CreateWithDescription(ctx, title, "")
}

func (s *Service) CreateWithDescription(ctx context.Context, title, description string) (*store.Ticket, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	title = trimOneLine(title)
	if title == "" {
		return nil, fmt.Errorf("title 不能为空")
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return nil, fmt.Errorf("description 不能为空")
	}
	t := store.Ticket{
		Title:          title,
		Description:    description,
		WorkflowStatus: contracts.TicketBacklog,
		Priority:       0,
	}
	if err := db.WithContext(ctx).Create(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) List(ctx context.Context, includeArchived bool) ([]store.Ticket, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	q := db.WithContext(ctx).Model(&store.Ticket{}).Order("priority desc").Order("updated_at desc").Order("id desc")
	if !includeArchived {
		q = q.Where("workflow_status != ?", contracts.TicketArchived)
	}
	var out []store.Ticket
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) BumpPriority(ctx context.Context, ticketID uint, delta int) (int, error) {
	db, err := s.requireDB()
	if err != nil {
		return 0, err
	}
	var t store.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return 0, err
	}
	np := t.Priority + delta
	now := time.Now()
	if err := db.WithContext(ctx).
		Model(&store.Ticket{}).
		Where("id = ?", ticketID).
		Updates(map[string]any{
			"priority":   np,
			"updated_at": now,
		}).Error; err != nil {
		return 0, err
	}
	return np, nil
}

func (s *Service) UpdateText(ctx context.Context, ticketID uint, title, description string) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	title = trimOneLine(title)
	if title == "" {
		return fmt.Errorf("title 不能为空")
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return fmt.Errorf("description 不能为空")
	}
	now := time.Now()
	return db.WithContext(ctx).
		Model(&store.Ticket{}).
		Where("id = ?", ticketID).
		Updates(map[string]any{
			"title":       title,
			"description": description,
			"updated_at":  now,
		}).Error
}

func trimOneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}
