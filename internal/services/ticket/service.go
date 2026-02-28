package ticket

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

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

func (s *Service) Create(ctx context.Context, title string) (*contracts.Ticket, error) {
	return s.CreateWithDescriptionAndLabel(ctx, title, "", "")
}

func (s *Service) CreateWithDescription(ctx context.Context, title, description string) (*contracts.Ticket, error) {
	return s.CreateWithDescriptionAndLabel(ctx, title, description, "")
}

func (s *Service) CreateWithDescriptionAndLabel(ctx context.Context, title, description, label string) (*contracts.Ticket, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	title = trimOneLine(title)
	if title == "" {
		return nil, fmt.Errorf("title 不能为空")
	}
	description = strings.TrimSpace(description)
	label = normalizeLabel(label)
	t := contracts.Ticket{
		Title:          title,
		Description:    description,
		Label:          label,
		WorkflowStatus: contracts.TicketBacklog,
		Priority:       contracts.TicketPriorityNone,
	}
	if err := db.WithContext(ctx).Create(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) GetByID(ctx context.Context, id uint) (*contracts.Ticket, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if id == 0 {
		return nil, fmt.Errorf("ticket id 不能为空")
	}
	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Service) List(ctx context.Context, includeArchived bool) ([]contracts.Ticket, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	q := db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Order("priority desc").
		Order("created_at asc").
		Order("id asc")
	if !includeArchived {
		q = q.Where("workflow_status != ?", contracts.TicketArchived)
	}
	var out []contracts.Ticket
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) SetPriority(ctx context.Context, ticketID uint, priority int) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ticketID == 0 {
		return fmt.Errorf("ticket id 不能为空")
	}
	now := time.Now()
	res := db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Where("id = ?", ticketID).
		Updates(map[string]any{
			"priority":   priority,
			"updated_at": now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Service) BumpPriority(ctx context.Context, ticketID uint, delta int) (int, error) {
	db, err := s.requireDB()
	if err != nil {
		return 0, err
	}
	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return 0, err
	}
	np := t.Priority + delta
	now := time.Now()
	if err := db.WithContext(ctx).
		Model(&contracts.Ticket{}).
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
		Model(&contracts.Ticket{}).
		Where("id = ?", ticketID).
		Updates(map[string]any{
			"title":       title,
			"description": description,
			"updated_at":  now,
		}).Error
}

func (s *Service) UpdateTextAndLabel(ctx context.Context, ticketID uint, title, description, label string) error {
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
	label = normalizeLabel(label)
	now := time.Now()
	return db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Where("id = ?", ticketID).
		Updates(map[string]any{
			"title":       title,
			"description": description,
			"label":       label,
			"updated_at":  now,
		}).Error
}

func trimOneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func normalizeLabel(label string) string {
	return trimOneLine(label)
}
