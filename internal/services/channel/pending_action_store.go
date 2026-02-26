package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

const channelPendingActionExpireAfter = 24 * time.Hour

var ErrPendingActionNotFound = errors.New("pending action 不存在")

type PendingActionDecision string

const (
	PendingActionApprove PendingActionDecision = "approve"
	PendingActionReject  PendingActionDecision = "reject"
)

type PendingActionView struct {
	ID             uint                                 `json:"id"`
	ConversationID uint                                 `json:"conversation_id"`
	JobID          uint                                 `json:"job_id"`
	Action         contracts.TurnAction                 `json:"action"`
	Status         contracts.ChannelPendingActionStatus `json:"status"`
	Decider        string                               `json:"decider"`
	DecisionNote   string                               `json:"decision_note"`
	CreatedAt      time.Time                            `json:"created_at"`
	UpdatedAt      time.Time                            `json:"updated_at"`
	DecidedAt      *time.Time                           `json:"decided_at,omitempty"`
	ExecutedAt     *time.Time                           `json:"executed_at,omitempty"`
}

type PendingActionDecisionRequest struct {
	ChannelType        contracts.ChannelType
	Adapter            string
	PeerConversationID string
	PendingActionID    uint
	Decision           PendingActionDecision
	Decider            string
	Note               string
}

type PendingActionDecisionResult struct {
	Action            PendingActionView     `json:"action"`
	Decision          PendingActionDecision `json:"decision,omitempty"`
	Message           string                `json:"message"`
	ExecutionMessage  string                `json:"execution_message,omitempty"`
	AlreadyFinalState bool                  `json:"already_final_state,omitempty"`
}

type ProjectRuntimePendingActionDecider interface {
	DecidePendingAction(ctx context.Context, req PendingActionDecisionRequest) (PendingActionDecisionResult, error)
}

func (d PendingActionDecision) normalize() PendingActionDecision {
	return PendingActionDecision(strings.ToLower(strings.TrimSpace(string(d))))
}

func (d PendingActionDecision) valid() bool {
	switch d.normalize() {
	case PendingActionApprove, PendingActionReject:
		return true
	default:
		return false
	}
}

func (s *Service) CreatePendingActions(ctx context.Context, conversationID, jobID uint, actions []contracts.TurnAction) ([]PendingActionView, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows := []store.ChannelPendingAction{}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, cerr := s.createPendingActionsTx(ctx, tx, conversationID, jobID, actions)
		if cerr != nil {
			return cerr
		}
		rows = append(rows, created...)
		return nil
	}); err != nil {
		return nil, err
	}
	return pendingActionViewsFromModels(rows), nil
}

func (s *Service) createPendingActionsTx(ctx context.Context, tx *gorm.DB, conversationID, jobID uint, actions []contracts.TurnAction) ([]store.ChannelPendingAction, error) {
	if tx == nil {
		return nil, fmt.Errorf("tx 不能为空")
	}
	if conversationID == 0 || jobID == 0 {
		return nil, fmt.Errorf("conversation_id/job_id 不能为空")
	}
	if len(actions) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	rows := make([]store.ChannelPendingAction, 0, len(actions))
	for _, action := range actions {
		action.Normalize()
		if err := action.Validate(); err != nil {
			return nil, err
		}
		actionJSON, err := json.Marshal(action)
		if err != nil {
			return nil, fmt.Errorf("序列化 pending action 失败: %w", err)
		}
		row := store.ChannelPendingAction{
			ConversationID: conversationID,
			JobID:          jobID,
			ActionJSON:     strings.TrimSpace(string(actionJSON)),
			Status:         contracts.ChannelPendingActionPending,
			Decider:        "",
			DecisionNote:   "",
		}
		if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Service) ListPendingActions(ctx context.Context, jobID uint) ([]PendingActionView, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return []PendingActionView{}, nil
	}
	var rows []store.ChannelPendingAction
	if err := db.WithContext(ctx).
		Where("job_id = ?", jobID).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return pendingActionViewsFromModels(rows), nil
}

func (s *Service) getPendingActionByID(ctx context.Context, actionID uint) (store.ChannelPendingAction, error) {
	_, db, err := s.require()
	if err != nil {
		return store.ChannelPendingAction{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var row store.ChannelPendingAction
	if err := db.WithContext(ctx).First(&row, actionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return store.ChannelPendingAction{}, fmt.Errorf("%w: %d", ErrPendingActionNotFound, actionID)
		}
		return store.ChannelPendingAction{}, err
	}
	return row, nil
}

func (s *Service) updatePendingAction(ctx context.Context, actionID uint, expectStatus contracts.ChannelPendingActionStatus, values map[string]any) (int64, error) {
	_, db, err := s.require()
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if actionID == 0 {
		return 0, fmt.Errorf("action_id 不能为空")
	}
	query := db.WithContext(ctx).Model(&store.ChannelPendingAction{}).Where("id = ?", actionID)
	if expectStatus != "" {
		query = query.Where("status = ?", expectStatus)
	}
	res := query.Updates(values)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func pendingActionRowToView(row store.ChannelPendingAction) PendingActionView {
	action, err := decodePendingTurnAction(row.ActionJSON)
	if err != nil {
		action = contracts.TurnAction{
			Name: "invalid_action",
			Args: map[string]any{
				"raw_action_json": strings.TrimSpace(row.ActionJSON),
			},
		}
	}
	return PendingActionView{
		ID:             row.ID,
		ConversationID: row.ConversationID,
		JobID:          row.JobID,
		Action:         action,
		Status:         row.Status,
		Decider:        strings.TrimSpace(row.Decider),
		DecisionNote:   strings.TrimSpace(row.DecisionNote),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		DecidedAt:      row.DecidedAt,
		ExecutedAt:     row.ExecutedAt,
	}
}

func pendingActionViewsFromModels(rows []store.ChannelPendingAction) []PendingActionView {
	if len(rows) == 0 {
		return nil
	}
	views := make([]PendingActionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, pendingActionRowToView(row))
	}
	return views
}

func copyPendingActionViews(in []PendingActionView) []PendingActionView {
	if len(in) == 0 {
		return nil
	}
	out := make([]PendingActionView, 0, len(in))
	for _, item := range in {
		copied := item
		copied.Action.Normalize()
		if len(item.Action.Args) > 0 {
			copied.Action.Args = make(map[string]any, len(item.Action.Args))
			for k, v := range item.Action.Args {
				copied.Action.Args[strings.TrimSpace(k)] = v
			}
		}
		if item.DecidedAt != nil {
			t := *item.DecidedAt
			copied.DecidedAt = &t
		}
		if item.ExecutedAt != nil {
			t := *item.ExecutedAt
			copied.ExecutedAt = &t
		}
		out = append(out, copied)
	}
	return out
}

func decodePendingTurnAction(raw string) (contracts.TurnAction, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return contracts.TurnAction{}, fmt.Errorf("action_json 为空")
	}
	var action contracts.TurnAction
	if err := json.Unmarshal([]byte(raw), &action); err != nil {
		return contracts.TurnAction{}, err
	}
	action.Normalize()
	if err := action.Validate(); err != nil {
		return contracts.TurnAction{}, err
	}
	return action, nil
}
