package pm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	gatewaysendsvc "dalek/internal/services/gatewaysend"
	"dalek/internal/store"

	"gorm.io/gorm"
)

// WorkflowStatusChangeHook 是 ticket workflow 状态变更后的可选回调。
type WorkflowStatusChangeHook interface {
	OnStatusChange(ctx context.Context, event StatusChangeEvent) error
}

type StatusChangeEvent struct {
	TicketID   uint
	WorkerID   uint
	FromStatus contracts.TicketWorkflowStatus
	ToStatus   contracts.TicketWorkflowStatus
	Source     string
	Detail     string
	OccurredAt time.Time
}

type GatewayStatusNotifier struct {
	projectName string
	projectDB   *gorm.DB
	gatewayDB   *gorm.DB
	resolver    contracts.ProjectMetaResolver
	sender      gatewaysendsvc.MessageSender
	logger      *slog.Logger
	now         func() time.Time
}

func NewGatewayStatusNotifier(
	projectName string,
	projectDB, gatewayDB *gorm.DB,
	resolver contracts.ProjectMetaResolver,
	sender gatewaysendsvc.MessageSender,
	loggers ...*slog.Logger,
) *GatewayStatusNotifier {
	logger := core.DiscardLogger()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &GatewayStatusNotifier{
		projectName: strings.TrimSpace(projectName),
		projectDB:   projectDB,
		gatewayDB:   gatewayDB,
		resolver:    resolver,
		sender:      sender,
		logger:      logger,
		now:         time.Now,
	}
}

func (n *GatewayStatusNotifier) OnStatusChange(ctx context.Context, event StatusChangeEvent) error {
	if n == nil {
		return nil
	}
	event.FromStatus = normalizeTicketWorkflowStatus(event.FromStatus)
	event.ToStatus = normalizeTicketWorkflowStatus(event.ToStatus)
	if event.TicketID == 0 || event.FromStatus == "" || event.ToStatus == "" || event.FromStatus == event.ToStatus {
		return nil
	}
	if !shouldNotifyTicketStatusChange(event) {
		return nil
	}
	projectName := strings.TrimSpace(n.projectName)
	if projectName == "" {
		return fmt.Errorf("gateway status notifier 缺少 project name")
	}
	if n.projectDB == nil {
		return fmt.Errorf("gateway status notifier 缺少 project db")
	}
	if n.gatewayDB == nil {
		return fmt.Errorf("gateway status notifier 缺少 gateway db")
	}
	title, err := n.loadTicketTitle(ctx, event.TicketID)
	if err != nil {
		return err
	}
	if event.ToStatus == contracts.TicketDone {
		if mergeDetail, merr := n.loadMergeDetail(ctx, event.TicketID); merr == nil && strings.TrimSpace(mergeDetail) != "" {
			if cur := strings.TrimSpace(event.Detail); cur != "" {
				event.Detail = strings.TrimSpace(mergeDetail) + "\n" + cur
			} else {
				event.Detail = strings.TrimSpace(mergeDetail)
			}
		}
	}
	text := buildStatusChangeNotifyText(event, title)
	_, err = gatewaysendsvc.SendProjectTextWithLogger(ctx, n.gatewayDB, n.resolver, n.sender, n.logger, projectName, text)
	if err != nil {
		if errors.Is(err, gatewaysendsvc.ErrBindingNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func (n *GatewayStatusNotifier) loadTicketTitle(ctx context.Context, ticketID uint) (string, error) {
	if n == nil || n.projectDB == nil || ticketID == 0 {
		return fmt.Sprintf("t%d", ticketID), nil
	}
	var t store.Ticket
	if err := n.projectDB.WithContext(ctx).Select("id", "title").First(&t, ticketID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Sprintf("t%d", ticketID), nil
		}
		return "", err
	}
	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = fmt.Sprintf("t%d", ticketID)
	}
	return title, nil
}

func shouldNotifyTicketStatusChange(event StatusChangeEvent) bool {
	switch normalizeTicketWorkflowStatus(event.ToStatus) {
	case contracts.TicketDone, contracts.TicketBlocked:
		return true
	default:
		return false
	}
}

func (n *GatewayStatusNotifier) loadMergeDetail(ctx context.Context, ticketID uint) (string, error) {
	if n == nil || n.projectDB == nil || ticketID == 0 {
		return "", nil
	}
	var mi store.MergeItem
	err := n.projectDB.WithContext(ctx).
		Where("ticket_id = ? AND status != ?", ticketID, contracts.MergeMerged).
		Order("id desc").
		First(&mi).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	branch := strings.TrimSpace(mi.Branch)
	if branch == "" {
		return "", nil
	}
	return fmt.Sprintf("merge_item=%d  branch=%s", mi.ID, branch), nil
}

func buildStatusChangeNotifyText(event StatusChangeEvent, ticketTitle string) string {
	ticketTitle = strings.TrimSpace(ticketTitle)
	if ticketTitle == "" {
		ticketTitle = fmt.Sprintf("t%d", event.TicketID)
	}
	source := strings.TrimSpace(event.Source)
	if source == "" {
		source = "unknown"
	}
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}

	lines := []string{
		fmt.Sprintf(
			"[ticket] t%d %s -> %s",
			event.TicketID,
			normalizeTicketWorkflowStatus(event.FromStatus),
			normalizeTicketWorkflowStatus(event.ToStatus),
		),
		ticketTitle,
	}
	details := make([]string, 0, 2)
	if event.WorkerID > 0 {
		details = append(details, fmt.Sprintf("worker: w%d", event.WorkerID))
	}
	if d := strings.TrimSpace(event.Detail); d != "" {
		details = append(details, d)
	}
	if len(details) > 0 {
		lines = append(lines, "详情:")
		lines = append(lines, strings.Join(details, "\n"))
	}
	lines = append(lines, fmt.Sprintf("来源: %s | 时间: %s", source, occurredAt.Local().Format("2006-01-02 15:04:05")))
	return strings.Join(lines, "\n")
}

func (s *Service) buildStatusChangeEvent(ticketID uint, from, to contracts.TicketWorkflowStatus, source string, occurredAt time.Time) *StatusChangeEvent {
	from = normalizeTicketWorkflowStatus(from)
	to = normalizeTicketWorkflowStatus(to)
	if ticketID == 0 || from == "" || to == "" || from == to {
		return nil
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	return &StatusChangeEvent{
		TicketID:   ticketID,
		FromStatus: from,
		ToStatus:   to,
		Source:     strings.TrimSpace(source),
		OccurredAt: occurredAt,
	}
}

func (s *Service) emitStatusChangeHookAsync(event *StatusChangeEvent) {
	if s == nil || event == nil {
		return
	}
	hook := s.getStatusChangeHook()
	if hook == nil {
		return
	}
	ev := *event
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.slog().Error("pm workflow status notify panic",
					"ticket_id", ev.TicketID,
					"source", strings.TrimSpace(ev.Source),
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), workflowStatusNotifyTimeout)
		defer cancel()
		if err := hook.OnStatusChange(ctx, ev); err != nil {
			s.slog().Warn("pm workflow status notify failed",
				"ticket_id", ev.TicketID,
				"from", ev.FromStatus,
				"to", ev.ToStatus,
				"source", strings.TrimSpace(ev.Source),
				"error", err,
			)
		}
	}()
}
