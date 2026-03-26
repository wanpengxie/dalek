package pm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	gatewaysendsvc "dalek/internal/services/gatewaysend"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// GatewayConvergentNotifier — 通过 gateway send service 推送 convergent 通知
// ---------------------------------------------------------------------------

// GatewayConvergentNotifier 通过 gateway send service 将 convergent PM review
// 结果推送到飞书 channel。未配置 binding 时静默跳过。
type GatewayConvergentNotifier struct {
	projectName string
	sendService *gatewaysendsvc.Service
	logger      *slog.Logger
}

// NewGatewayConvergentNotifier 创建一个通过 gateway 直接发送的 convergent notifier。
func NewGatewayConvergentNotifier(
	projectName string,
	gatewayDB *gorm.DB,
	resolver contracts.ProjectMetaResolver,
	sender gatewaysendsvc.MessageSender,
	loggers ...*slog.Logger,
) *GatewayConvergentNotifier {
	logger := core.DiscardLogger()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &GatewayConvergentNotifier{
		projectName: strings.TrimSpace(projectName),
		sendService: gatewaysendsvc.NewServiceWithDB(gatewayDB, resolver, sender, logger),
		logger:      logger,
	}
}

func (n *GatewayConvergentNotifier) NotifyText(ctx context.Context, text string) error {
	if n == nil {
		return nil
	}
	projectName := strings.TrimSpace(n.projectName)
	if projectName == "" {
		return nil
	}
	if n.sendService == nil {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	_, err := n.sendService.Send(ctx, projectName, text)
	if err != nil {
		if errors.Is(err, gatewaysendsvc.ErrBindingNotFound) {
			return nil // 未配置飞书 channel，静默跳过
		}
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// OutboxConvergentNotifier — 通过 outbox 入队推送 convergent 通知
// ---------------------------------------------------------------------------

// OutboxConvergentNotifier 将 convergent 通知写入 outbox 队列，由后台任务投递。
// 用于 CLI 路径（非 daemon），避免进程退出导致消息丢失。
type OutboxConvergentNotifier struct {
	projectName string
	gatewayDB   *gorm.DB
	logger      *slog.Logger
}

// NewOutboxConvergentNotifier 创建一个入队型 convergent notifier。
func NewOutboxConvergentNotifier(
	projectName string,
	gatewayDB *gorm.DB,
	loggers ...*slog.Logger,
) *OutboxConvergentNotifier {
	logger := core.DiscardLogger()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &OutboxConvergentNotifier{
		projectName: strings.TrimSpace(projectName),
		gatewayDB:   gatewayDB,
		logger:      logger,
	}
}

func (n *OutboxConvergentNotifier) NotifyText(ctx context.Context, text string) error {
	if n == nil {
		return nil
	}
	projectName := strings.TrimSpace(n.projectName)
	if projectName == "" {
		return nil
	}
	if n.gatewayDB == nil {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	repo := gatewaysendsvc.NewGormRepository(n.gatewayDB)
	logger := core.EnsureLogger(n.logger)
	bindings, err := repo.FindEnabledBindings(ctx, projectName, contracts.ChannelTypeIM, gatewaysendsvc.AdapterFeishu)
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil // 未配置飞书 channel，静默跳过
	}

	var enqueueErrs []error
	enqueued := 0
	for _, binding := range bindings {
		chatID := strings.TrimSpace(binding.PeerProjectKey)
		if chatID == "" {
			logger.Warn("skip convergent outbox enqueue: empty chat_id", "project", projectName, "binding_id", binding.ID)
			continue
		}
		if _, duplicated, dupErr := repo.FindRecentDuplicateDelivery(ctx, binding, text); dupErr != nil {
			enqueueErrs = append(enqueueErrs, fmt.Errorf("binding=%d dedup failed: %w", binding.ID, dupErr))
			continue
		} else if duplicated {
			logger.Info("skip convergent outbox enqueue: dedup hit", "project", projectName, "binding_id", binding.ID)
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
