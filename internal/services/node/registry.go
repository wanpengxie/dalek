package node

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

type Service struct {
	db  *gorm.DB
	now func() time.Time
}

type RegisterInput struct {
	Name string

	Endpoint        string
	AuthMode        string
	Status          string
	Version         string
	ProtocolVersion string

	RoleCapabilities     any
	ProviderModes        any
	DefaultProvider      string
	ProviderCapabilities any
	SessionAffinity      string
	LastSeenAt           *time.Time
}

type ListOptions struct {
	Status          string
	RoleCapability  string
	ProviderMode    string
	OnlySchedulable bool
	Limit           int
}

func New(db *gorm.DB) *Service {
	return &Service{
		db:  db,
		now: time.Now,
	}
}

func (s *Service) requireDB() (*gorm.DB, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("node service db 为空")
	}
	return s.db, nil
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (contracts.Node, error) {
	db, err := s.requireDB()
	if err != nil {
		return contracts.Node{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return contracts.Node{}, fmt.Errorf("name 不能为空")
	}
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = string(contracts.NodeStatusUnknown)
	}
	rec := contracts.Node{
		Name:                 name,
		Endpoint:             strings.TrimSpace(in.Endpoint),
		AuthMode:             strings.TrimSpace(in.AuthMode),
		Status:               status,
		Version:              strings.TrimSpace(in.Version),
		ProtocolVersion:      strings.TrimSpace(in.ProtocolVersion),
		RoleCapabilities:     toJSONStringSlice(in.RoleCapabilities),
		ProviderModes:        toJSONStringSlice(in.ProviderModes),
		DefaultProvider:      strings.TrimSpace(in.DefaultProvider),
		ProviderCapabilities: contracts.JSONMapFromAny(in.ProviderCapabilities),
		SessionAffinity:      strings.TrimSpace(in.SessionAffinity),
		SessionEpoch:         1,
		LastSeenAt:           in.LastSeenAt,
	}
	if rec.LastSeenAt == nil {
		now := s.now()
		rec.LastSeenAt = &now
	}
	if err := db.WithContext(ctx).Create(&rec).Error; err != nil {
		if isNodeNameUniqueConflict(err) {
			existing, ferr := s.GetByName(ctx, name)
			if ferr != nil {
				return contracts.Node{}, ferr
			}
			if existing != nil {
				return *existing, nil
			}
		}
		return contracts.Node{}, err
	}
	return rec, nil
}

func (s *Service) GetByName(ctx context.Context, name string) (*contracts.Node, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name 不能为空")
	}
	var rec contracts.Node
	if err := db.WithContext(ctx).Where("name = ?", name).First(&rec).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (s *Service) RemoveByName(ctx context.Context, name string) (bool, error) {
	db, err := s.requireDB()
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("name 不能为空")
	}
	res := db.WithContext(ctx).Where("name = ?", name).Delete(&contracts.Node{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (s *Service) List(ctx context.Context, opt ListOptions) ([]contracts.Node, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = 100
	}
	q := db.WithContext(ctx).Model(&contracts.Node{})
	if status := strings.TrimSpace(opt.Status); status != "" {
		q = q.Where("status = ?", status)
	}
	if opt.OnlySchedulable {
		q = q.Where("status = ?", string(contracts.NodeStatusOnline))
	}
	if role := strings.TrimSpace(opt.RoleCapability); role != "" {
		q = q.Where("role_capabilities_json LIKE ?", jsonArrayContainsLike(role))
	}
	if provider := strings.TrimSpace(opt.ProviderMode); provider != "" {
		q = q.Where("provider_modes_json LIKE ?", jsonArrayContainsLike(provider))
	}
	var out []contracts.Node
	if err := q.Order("id asc").Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	if out == nil {
		return []contracts.Node{}, nil
	}
	return out, nil
}

func (s *Service) GetSchedulable(ctx context.Context, roleCapability, providerMode string, limit int) ([]contracts.Node, error) {
	return s.List(ctx, ListOptions{
		RoleCapability:  strings.TrimSpace(roleCapability),
		ProviderMode:    strings.TrimSpace(providerMode),
		OnlySchedulable: true,
		Limit:           limit,
	})
}

func jsonArrayContainsLike(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `%` + `"` + v + `"` + `%`
}

func toJSONStringSlice(v any) contracts.JSONStringSlice {
	switch t := v.(type) {
	case nil:
		return contracts.JSONStringSlice{}
	case contracts.JSONStringSlice:
		return append(contracts.JSONStringSlice(nil), t...)
	case []string:
		out := make(contracts.JSONStringSlice, 0, len(t))
		for _, item := range t {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			out = append(out, item)
		}
		return out
	case string:
		item := strings.TrimSpace(t)
		if item == "" {
			return contracts.JSONStringSlice{}
		}
		return contracts.JSONStringSlice{item}
	default:
		return contracts.JSONStringSlice{}
	}
}

func (s *Service) UpdateStatus(ctx context.Context, name, status string, lastSeenAt *time.Time) error {
	return s.updateSessionState(ctx, name, status, lastSeenAt, 0)
}

func (s *Service) updateSessionState(ctx context.Context, name, status string, lastSeenAt *time.Time, sessionEpoch int) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name 不能为空")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return fmt.Errorf("status 不能为空")
	}
	updates := map[string]any{
		"status": status,
	}
	if lastSeenAt == nil {
		now := s.now()
		lastSeenAt = &now
	}
	updates["last_seen_at"] = lastSeenAt
	if sessionEpoch > 0 {
		updates["session_epoch"] = sessionEpoch
	}
	res := db.WithContext(ctx).Model(&contracts.Node{}).Where("name = ?", name).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("node 不存在: %s", name)
	}
	return nil
}

func isNodeNameUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	if err == gorm.ErrDuplicatedKey {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "nodes.name")
}
