package pm

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

type plannerPMOpExecutor interface {
	Reconcile(ctx context.Context, op contracts.PMOp) (bool, contracts.JSONMap, error)
	Execute(ctx context.Context, op contracts.PMOp) (contracts.JSONMap, error)
}

func (s *Service) plannerPMOpExecutor(kind contracts.PMOpKind) plannerPMOpExecutor {
	switch normalizePlannerPMOpKind(kind) {
	case contracts.PMOpCreateTicket:
		return createTicketPMOpExecutor{s: s}
	case contracts.PMOpCreateIntegration:
		return createIntegrationTicketPMOpExecutor{s: s}
	case contracts.PMOpStartTicket:
		return startTicketPMOpExecutor{s: s}
	case contracts.PMOpCloseInbox:
		return closeInboxPMOpExecutor{s: s}
	case contracts.PMOpRunAcceptance:
		return runAcceptancePMOpExecutor{s: s}
	case contracts.PMOpSetFeatureStatus:
		return setFeatureStatusPMOpExecutor{s: s}
	case contracts.PMOpWriteRequirementDoc:
		return noopPMOpExecutor{kind: contracts.PMOpWriteRequirementDoc, reason: "需求文档写入由 planner 文本产出承载，当前记录为已处理"}
	case contracts.PMOpWriteDesignDoc:
		return noopPMOpExecutor{kind: contracts.PMOpWriteDesignDoc, reason: "设计文档写入由 planner 文本产出承载，当前记录为已处理"}
	default:
		return nil
	}
}

type noopPMOpExecutor struct {
	kind   contracts.PMOpKind
	reason string
}

func (e noopPMOpExecutor) Reconcile(context.Context, contracts.PMOp) (bool, contracts.JSONMap, error) {
	return false, contracts.JSONMap{}, nil
}

func (e noopPMOpExecutor) Execute(context.Context, contracts.PMOp) (contracts.JSONMap, error) {
	return contracts.JSONMap{
		"status": "noop",
		"kind":   strings.TrimSpace(string(e.kind)),
		"reason": strings.TrimSpace(e.reason),
	}, nil
}

type createTicketPMOpExecutor struct {
	s *Service
}

func (e createTicketPMOpExecutor) Reconcile(ctx context.Context, op contracts.PMOp) (bool, contracts.JSONMap, error) {
	if e.s == nil {
		return false, contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	_, db, err := e.s.require()
	if err != nil {
		return false, contracts.JSONMap{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if row, err := e.s.findLatestSucceededPMOpByIdempotency(ctx, contracts.PMOpCreateTicket, strings.TrimSpace(op.IdempotencyKey)); err != nil {
		return false, contracts.JSONMap{}, err
	} else if row != nil {
		ticketID := jsonMapUint(row.ResultJSON, "ticket_id")
		if ticketID != 0 {
			var t contracts.Ticket
			if err := db.WithContext(ctx).Select("id", "workflow_status", "title").First(&t, ticketID).Error; err == nil {
				return true, contracts.JSONMap{
					"ticket_id":        t.ID,
					"workflow_status":  strings.TrimSpace(string(t.WorkflowStatus)),
					"reconcile_source": "idempotency_journal",
				}, nil
			}
		}
	}
	title := strings.TrimSpace(jsonMapString(op.Arguments, "title"))
	if title == "" {
		return false, contracts.JSONMap{}, nil
	}
	label := strings.TrimSpace(jsonMapString(op.Arguments, "label"))

	var t contracts.Ticket
	q := db.WithContext(ctx).Model(&contracts.Ticket{}).Where("title = ?", title)
	if label != "" {
		q = q.Where("label = ?", label)
	}
	err = q.Order("id desc").First(&t).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, contracts.JSONMap{}, nil
		}
		return false, contracts.JSONMap{}, err
	}
	return true, contracts.JSONMap{
		"ticket_id":        t.ID,
		"workflow_status":  strings.TrimSpace(string(t.WorkflowStatus)),
		"reconcile_source": "ticket_lookup",
	}, nil
}

func (e createTicketPMOpExecutor) Execute(ctx context.Context, op contracts.PMOp) (contracts.JSONMap, error) {
	if e.s == nil {
		return contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	_, db, err := e.s.require()
	if err != nil {
		return contracts.JSONMap{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	title := strings.TrimSpace(jsonMapString(op.Arguments, "title"))
	if title == "" {
		return contracts.JSONMap{}, fmt.Errorf("create_ticket 缺少 title")
	}
	description := strings.TrimSpace(jsonMapString(op.Arguments, "description"))
	label := strings.TrimSpace(jsonMapString(op.Arguments, "label"))
	priority := jsonMapInt(op.Arguments, "priority")
	now := time.Now()
	row := contracts.Ticket{
		Title:          title,
		Description:    description,
		Label:          label,
		Priority:       priority,
		WorkflowStatus: contracts.TicketBacklog,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return contracts.JSONMap{}, err
	}
	return contracts.JSONMap{
		"ticket_id":       row.ID,
		"title":           row.Title,
		"workflow_status": strings.TrimSpace(string(row.WorkflowStatus)),
	}, nil
}

type createIntegrationTicketPMOpExecutor struct {
	s *Service
}

func (e createIntegrationTicketPMOpExecutor) Reconcile(ctx context.Context, op contracts.PMOp) (bool, contracts.JSONMap, error) {
	return createTicketPMOpExecutor{s: e.s}.Reconcile(ctx, op)
}

func (e createIntegrationTicketPMOpExecutor) Execute(ctx context.Context, op contracts.PMOp) (contracts.JSONMap, error) {
	args := contracts.JSONMapFromAny(op.Arguments)
	if strings.TrimSpace(jsonMapString(args, "label")) == "" {
		args["label"] = "integration"
	}
	op.Arguments = args
	return createTicketPMOpExecutor{s: e.s}.Execute(ctx, op)
}

type startTicketPMOpExecutor struct {
	s *Service
}

func (e startTicketPMOpExecutor) Reconcile(ctx context.Context, op contracts.PMOp) (bool, contracts.JSONMap, error) {
	if e.s == nil {
		return false, contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	_, db, err := e.s.require()
	if err != nil {
		return false, contracts.JSONMap{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticketID := jsonMapUint(op.Arguments, "ticket_id")
	if ticketID == 0 {
		return false, contracts.JSONMap{}, fmt.Errorf("start_ticket 缺少 ticket_id")
	}
	var t contracts.Ticket
	if err := db.WithContext(ctx).Select("id", "workflow_status").First(&t, ticketID).Error; err != nil {
		return false, contracts.JSONMap{}, err
	}
	switch contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) {
	case contracts.TicketQueued, contracts.TicketActive, contracts.TicketDone, contracts.TicketArchived:
		return true, contracts.JSONMap{
			"ticket_id":        ticketID,
			"workflow_status":  strings.TrimSpace(string(t.WorkflowStatus)),
			"reconcile_source": "ticket_workflow_status",
		}, nil
	default:
		return false, contracts.JSONMap{}, nil
	}
}

func (e startTicketPMOpExecutor) Execute(ctx context.Context, op contracts.PMOp) (contracts.JSONMap, error) {
	if e.s == nil {
		return contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	ticketID := jsonMapUint(op.Arguments, "ticket_id")
	if ticketID == 0 {
		return contracts.JSONMap{}, fmt.Errorf("start_ticket 缺少 ticket_id")
	}
	baseBranch := strings.TrimSpace(jsonMapString(op.Arguments, "base_branch"))
	worker, err := e.s.StartTicketWithOptions(ctx, ticketID, StartOptions{
		BaseBranch: baseBranch,
	})
	if err != nil {
		return contracts.JSONMap{}, err
	}
	workflowStatus := string(contracts.TicketQueued)
	if worker != nil && worker.ID != 0 {
		return contracts.JSONMap{
			"ticket_id":       ticketID,
			"worker_id":       worker.ID,
			"workflow_status": workflowStatus,
		}, nil
	}
	return contracts.JSONMap{
		"ticket_id":       ticketID,
		"workflow_status": workflowStatus,
	}, nil
}

type closeInboxPMOpExecutor struct {
	s *Service
}

func (e closeInboxPMOpExecutor) Reconcile(ctx context.Context, op contracts.PMOp) (bool, contracts.JSONMap, error) {
	if e.s == nil {
		return false, contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	_, db, err := e.s.require()
	if err != nil {
		return false, contracts.JSONMap{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if inboxID := jsonMapUint(op.Arguments, "inbox_id"); inboxID != 0 {
		var item contracts.InboxItem
		if err := db.WithContext(ctx).Select("id", "status").First(&item, inboxID).Error; err != nil {
			return false, contracts.JSONMap{}, err
		}
		if item.Status == contracts.InboxDone {
			return true, contracts.JSONMap{
				"inbox_id":         item.ID,
				"status":           string(item.Status),
				"reconcile_source": "inbox_status",
			}, nil
		}
		return false, contracts.JSONMap{}, nil
	}

	key := strings.TrimSpace(jsonMapString(op.Arguments, "inbox_key"))
	if key == "" {
		return false, contracts.JSONMap{}, fmt.Errorf("close_inbox 缺少 inbox_id 或 inbox_key")
	}
	var cnt int64
	if err := db.WithContext(ctx).
		Model(&contracts.InboxItem{}).
		Where("key = ? AND status = ?", key, contracts.InboxOpen).
		Count(&cnt).Error; err != nil {
		return false, contracts.JSONMap{}, err
	}
	if cnt == 0 {
		return true, contracts.JSONMap{
			"inbox_key":        key,
			"closed_count":     0,
			"reconcile_source": "open_count",
		}, nil
	}
	return false, contracts.JSONMap{}, nil
}

func (e closeInboxPMOpExecutor) Execute(ctx context.Context, op contracts.PMOp) (contracts.JSONMap, error) {
	if e.s == nil {
		return contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	_, db, err := e.s.require()
	if err != nil {
		return contracts.JSONMap{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if inboxID := jsonMapUint(op.Arguments, "inbox_id"); inboxID != 0 {
		if err := e.s.CloseInboxItem(ctx, inboxID); err != nil {
			return contracts.JSONMap{}, err
		}
		return contracts.JSONMap{
			"inbox_id":       inboxID,
			"closed_count":   1,
			"status":         string(contracts.InboxDone),
			"close_strategy": "by_id",
		}, nil
	}
	key := strings.TrimSpace(jsonMapString(op.Arguments, "inbox_key"))
	if key == "" {
		return contracts.JSONMap{}, fmt.Errorf("close_inbox 缺少 inbox_id 或 inbox_key")
	}
	now := time.Now()
	res := db.WithContext(ctx).
		Model(&contracts.InboxItem{}).
		Where("key = ? AND status = ?", key, contracts.InboxOpen).
		Updates(map[string]any{
			"status":     contracts.InboxDone,
			"closed_at":  &now,
			"updated_at": now,
		})
	if res.Error != nil {
		return contracts.JSONMap{}, res.Error
	}
	return contracts.JSONMap{
		"inbox_key":      key,
		"closed_count":   res.RowsAffected,
		"status":         string(contracts.InboxDone),
		"close_strategy": "by_key",
	}, nil
}

func buildPMOpFromJournal(entry contracts.PMOpJournalEntry) contracts.PMOp {
	return contracts.PMOp{
		OpID:           strings.TrimSpace(entry.OpID),
		FeatureID:      strings.TrimSpace(entry.FeatureID),
		RequestID:      strings.TrimSpace(entry.RequestID),
		Kind:           contracts.PMOpKind(strings.TrimSpace(string(entry.Kind))),
		Arguments:      contracts.JSONMapFromAny(entry.ArgumentsJSON),
		Preconditions:  contracts.JSONStringSliceFromAny(entry.PrecondsJSON),
		IdempotencyKey: strings.TrimSpace(entry.IdempotencyKey),
		Critical:       entry.Critical,
	}
}

func jsonMapString(m contracts.JSONMap, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

func jsonMapInt(m contracts.JSONMap, key string) int {
	key = strings.TrimSpace(key)
	if key == "" || m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int8:
		return int(t)
	case int16:
		return int(t)
	case int32:
		return int(t)
	case int64:
		return int(t)
	case uint:
		return int(t)
	case uint8:
		return int(t)
	case uint16:
		return int(t)
	case uint32:
		return int(t)
	case uint64:
		return int(t)
	case float32:
		return int(t)
	case float64:
		return int(t)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(t))
		return i
	default:
		i, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprintf("%v", t)))
		return i
	}
}

func jsonMapUint(m contracts.JSONMap, key string) uint {
	n := jsonMapInt(m, key)
	if n <= 0 {
		return 0
	}
	return uint(n)
}

func jsonMapBool(m contracts.JSONMap, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || m == nil {
		return false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "y", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
