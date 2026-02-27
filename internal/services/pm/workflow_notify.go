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
	sendService *gatewaysendsvc.Service
	logger      *slog.Logger
	now         func() time.Time
}

type OutboxEnqueueStatusNotifier struct {
	projectName string
	projectDB   *gorm.DB
	gatewayDB   *gorm.DB
	logger      *slog.Logger
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
		sendService: gatewaysendsvc.NewServiceWithDB(gatewayDB, resolver, sender, logger),
		logger:      logger,
		now:         time.Now,
	}
}

func NewOutboxEnqueueStatusNotifier(
	projectName string,
	projectDB, gatewayDB *gorm.DB,
	loggers ...*slog.Logger,
) *OutboxEnqueueStatusNotifier {
	logger := core.DiscardLogger()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &OutboxEnqueueStatusNotifier{
		projectName: strings.TrimSpace(projectName),
		projectDB:   projectDB,
		gatewayDB:   gatewayDB,
		logger:      logger,
	}
}

func (n *GatewayStatusNotifier) OnStatusChange(ctx context.Context, event StatusChangeEvent) error {
	if n == nil {
		return nil
	}
	event.FromStatus = contracts.CanonicalTicketWorkflowStatus(event.FromStatus)
	event.ToStatus = contracts.CanonicalTicketWorkflowStatus(event.ToStatus)
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
	if n.sendService == nil {
		return fmt.Errorf("gateway status notifier 缺少 gateway send service")
	}
	text, err := n.buildNotifyText(ctx, event)
	if err != nil {
		return err
	}
	_, err = n.sendService.Send(ctx, projectName, text)
	if err != nil {
		if errors.Is(err, gatewaysendsvc.ErrBindingNotFound) {
			return nil
		}
		return err
	}
	return nil
}

func (n *OutboxEnqueueStatusNotifier) OnStatusChange(ctx context.Context, event StatusChangeEvent) error {
	if n == nil {
		return nil
	}
	event.FromStatus = contracts.CanonicalTicketWorkflowStatus(event.FromStatus)
	event.ToStatus = contracts.CanonicalTicketWorkflowStatus(event.ToStatus)
	if event.TicketID == 0 || event.FromStatus == "" || event.ToStatus == "" || event.FromStatus == event.ToStatus {
		return nil
	}
	if !shouldNotifyTicketStatusChange(event) {
		return nil
	}
	projectName := strings.TrimSpace(n.projectName)
	if projectName == "" {
		return fmt.Errorf("outbox enqueue notifier 缺少 project name")
	}
	if n.projectDB == nil {
		return fmt.Errorf("outbox enqueue notifier 缺少 project db")
	}
	if n.gatewayDB == nil {
		return fmt.Errorf("outbox enqueue notifier 缺少 gateway db")
	}

	text, err := n.buildNotifyText(ctx, event)
	if err != nil {
		return err
	}

	repo := gatewaysendsvc.NewGormRepository(n.gatewayDB)
	logger := core.EnsureLogger(n.logger)
	bindings, err := repo.FindEnabledBindings(ctx, projectName, contracts.ChannelTypeIM, gatewaysendsvc.AdapterFeishu)
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}

	var enqueueErrs []error
	enqueued := 0
	for _, binding := range bindings {
		chatID := strings.TrimSpace(binding.PeerProjectKey)
		if chatID == "" {
			logger.Warn("skip outbox enqueue: empty chat_id", "project", projectName, "binding_id", binding.ID)
			continue
		}
		if _, duplicated, dupErr := repo.FindRecentDuplicateDelivery(ctx, binding, text); dupErr != nil {
			enqueueErrs = append(enqueueErrs, fmt.Errorf("binding=%d dedup failed: %w", binding.ID, dupErr))
			continue
		} else if duplicated {
			logger.Info("skip outbox enqueue: dedup hit", "project", projectName, "binding_id", binding.ID)
			continue
		}
		if _, createErr := repo.CreatePending(ctx, binding, projectName, text); createErr != nil {
			enqueueErrs = append(enqueueErrs, fmt.Errorf("binding=%d enqueue failed: %w", binding.ID, createErr))
			continue
		}
		enqueued++
	}
	if enqueued == 0 && len(enqueueErrs) == 0 {
		return nil
	}
	if len(enqueueErrs) > 0 {
		return errors.Join(enqueueErrs...)
	}
	return nil
}

func (n *GatewayStatusNotifier) loadTicketTitle(ctx context.Context, ticketID uint) (string, error) {
	if n == nil {
		return fmt.Sprintf("t%d", ticketID), nil
	}
	return loadTicketTitleForDB(ctx, n.projectDB, ticketID)
}

func shouldNotifyTicketStatusChange(event StatusChangeEvent) bool {
	switch contracts.CanonicalTicketWorkflowStatus(event.ToStatus) {
	case contracts.TicketDone, contracts.TicketBlocked:
		return true
	default:
		return false
	}
}

func (n *GatewayStatusNotifier) loadMergeDetail(ctx context.Context, ticketID uint) (string, error) {
	if n == nil {
		return "", nil
	}
	return loadMergeDetailForDB(ctx, n.projectDB, ticketID)
}

func (n *GatewayStatusNotifier) buildNotifyText(ctx context.Context, event StatusChangeEvent) (string, error) {
	title, err := n.loadTicketTitle(ctx, event.TicketID)
	if err != nil {
		return "", err
	}
	enriched, err := enrichDoneStatusDetail(ctx, n.projectDB, event)
	if err != nil {
		return "", err
	}
	return buildStatusChangeNotifyText(enriched, title), nil
}

func (n *OutboxEnqueueStatusNotifier) buildNotifyText(ctx context.Context, event StatusChangeEvent) (string, error) {
	title, err := loadTicketTitleForDB(ctx, n.projectDB, event.TicketID)
	if err != nil {
		return "", err
	}
	enriched, err := enrichDoneStatusDetail(ctx, n.projectDB, event)
	if err != nil {
		return "", err
	}
	return buildStatusChangeNotifyText(enriched, title), nil
}

func enrichDoneStatusDetail(ctx context.Context, projectDB *gorm.DB, event StatusChangeEvent) (StatusChangeEvent, error) {
	if contracts.CanonicalTicketWorkflowStatus(event.ToStatus) != contracts.TicketDone {
		return event, nil
	}
	mergeDetail, err := loadMergeDetailForDB(ctx, projectDB, event.TicketID)
	if err != nil || strings.TrimSpace(mergeDetail) == "" {
		return event, err
	}
	if cur := strings.TrimSpace(event.Detail); cur != "" {
		event.Detail = strings.TrimSpace(mergeDetail) + "\n" + cur
		return event, nil
	}
	event.Detail = strings.TrimSpace(mergeDetail)
	return event, nil
}

func loadTicketTitleForDB(ctx context.Context, projectDB *gorm.DB, ticketID uint) (string, error) {
	if projectDB == nil || ticketID == 0 {
		return fmt.Sprintf("t%d", ticketID), nil
	}
	var t contracts.Ticket
	if err := projectDB.WithContext(ctx).Select("id", "title").First(&t, ticketID).Error; err != nil {
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

func loadMergeDetailForDB(ctx context.Context, projectDB *gorm.DB, ticketID uint) (string, error) {
	if projectDB == nil || ticketID == 0 {
		return "", nil
	}
	var mi contracts.MergeItem
	err := projectDB.WithContext(ctx).
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
	from := contracts.CanonicalTicketWorkflowStatus(event.FromStatus)
	to := contracts.CanonicalTicketWorkflowStatus(event.ToStatus)
	source := strings.TrimSpace(event.Source)
	if source == "" {
		source = "unknown"
	}
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}

	lines := []string{
		fmt.Sprintf("[ticket] t%d %s -> %s", event.TicketID, from, to),
		fmt.Sprintf("**%s**", ticketTitle),
		"",
		fmt.Sprintf("- 状态: `%s -> %s`", from, to),
	}
	if event.WorkerID > 0 {
		lines = append(lines, fmt.Sprintf("- Worker: `w%d`", event.WorkerID))
	}
	if d := strings.TrimSpace(event.Detail); d != "" {
		lines = append(lines, "- 详情:")
		for _, detailLine := range strings.Split(d, "\n") {
			detailLine = strings.TrimSpace(detailLine)
			if detailLine == "" {
				continue
			}
			lines = append(lines, "  "+detailLine)
		}
	}
	lines = append(lines,
		fmt.Sprintf("- 来源: `%s`", source),
		fmt.Sprintf("- 时间: `%s`", occurredAt.Local().Format("2006-01-02 15:04:05")),
	)
	return strings.Join(lines, "\n")
}

func (s *Service) buildStatusChangeEvent(ticketID uint, from, to contracts.TicketWorkflowStatus, source string, occurredAt time.Time) *StatusChangeEvent {
	from = contracts.CanonicalTicketWorkflowStatus(from)
	to = contracts.CanonicalTicketWorkflowStatus(to)
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
	s.statusHookWG.Add(1)
	go func() {
		defer s.statusHookWG.Done()
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

// WaitStatusChangeHooks 等待当前进程内已触发的状态通知回调执行完成。
// 用于短生命周期 CLI 场景，避免进程退出导致异步通知丢失。
func (s *Service) WaitStatusChangeHooks(ctx context.Context) error {
	if s == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		s.statusHookWG.Wait()
		close(done)
	}()
	if ctx == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
