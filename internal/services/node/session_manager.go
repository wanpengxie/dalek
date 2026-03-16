package node

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
)

type SessionManager struct {
	registry *Service
	now      func() time.Time
}

type RefreshExpiredResult struct {
	Checked int
	Updated int
}

type BeginSessionResult struct {
	Name         string
	SessionEpoch int
	LastSeenAt   time.Time
}

func NewSessionManager(registry *Service) *SessionManager {
	return &SessionManager{
		registry: registry,
		now:      time.Now,
	}
}

func (m *SessionManager) Heartbeat(ctx context.Context, name string, observedAt *time.Time) error {
	return m.HeartbeatWithEpoch(ctx, name, 0, observedAt)
}

func (m *SessionManager) BeginSession(ctx context.Context, name string, observedAt *time.Time) (BeginSessionResult, error) {
	if m == nil || m.registry == nil {
		return BeginSessionResult{}, fmt.Errorf("node session manager 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return BeginSessionResult{}, fmt.Errorf("name 不能为空")
	}
	if observedAt == nil {
		now := m.now()
		observedAt = &now
	}
	node, err := m.registry.GetByName(ctx, name)
	if err != nil {
		return BeginSessionResult{}, err
	}
	if node == nil {
		return BeginSessionResult{}, fmt.Errorf("node 不存在: %s", name)
	}
	nextEpoch := node.SessionEpoch + 1
	if nextEpoch <= 0 {
		nextEpoch = 1
	}
	if err := m.registry.updateSessionState(ctx, name, string(contracts.NodeStatusOnline), observedAt, nextEpoch); err != nil {
		return BeginSessionResult{}, err
	}
	return BeginSessionResult{
		Name:         name,
		SessionEpoch: nextEpoch,
		LastSeenAt:   observedAt.Local(),
	}, nil
}

func (m *SessionManager) HeartbeatWithEpoch(ctx context.Context, name string, expectedEpoch int, observedAt *time.Time) error {
	if m == nil || m.registry == nil {
		return fmt.Errorf("node session manager 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name 不能为空")
	}
	node, err := m.registry.GetByName(ctx, name)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("node 不存在: %s", name)
	}
	if expectedEpoch > 0 && node.SessionEpoch != expectedEpoch {
		return fmt.Errorf("node session epoch 不匹配: name=%s want=%d got=%d", name, expectedEpoch, node.SessionEpoch)
	}
	if observedAt == nil {
		now := m.now()
		observedAt = &now
	}
	return m.registry.updateSessionState(ctx, name, string(contracts.NodeStatusOnline), observedAt, node.SessionEpoch)
}

func (m *SessionManager) RefreshExpired(ctx context.Context, leaseTTL time.Duration) (RefreshExpiredResult, error) {
	if m == nil || m.registry == nil {
		return RefreshExpiredResult{}, fmt.Errorf("node session manager 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if leaseTTL <= 0 {
		return RefreshExpiredResult{}, fmt.Errorf("leaseTTL 必须大于 0")
	}
	nodes, err := m.registry.List(ctx, ListOptions{Limit: 1000})
	if err != nil {
		return RefreshExpiredResult{}, err
	}
	now := m.now()
	cutoff := now.Add(-leaseTTL)
	result := RefreshExpiredResult{Checked: len(nodes)}
	for _, rec := range nodes {
		if rec.LastSeenAt == nil {
			continue
		}
		if rec.LastSeenAt.After(cutoff) {
			continue
		}
		if strings.TrimSpace(rec.Status) == string(contracts.NodeStatusOffline) {
			continue
		}
		lastSeen := *rec.LastSeenAt
		if err := m.registry.UpdateStatus(ctx, rec.Name, string(contracts.NodeStatusOffline), &lastSeen); err != nil {
			return result, err
		}
		result.Updated++
	}
	return result, nil
}
