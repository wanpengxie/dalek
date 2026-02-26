package gatewaysend

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

type Service struct {
	repo     Repository
	sender   MessageSender
	resolver contracts.ProjectMetaResolver
	logger   *slog.Logger
}

func NewService(repo Repository, sender MessageSender, resolver contracts.ProjectMetaResolver, logger *slog.Logger) *Service {
	if sender == nil {
		sender = &NoopSender{}
	}
	return &Service{
		repo:     repo,
		sender:   sender,
		resolver: resolver,
		logger:   core.EnsureLogger(logger),
	}
}

func NewServiceWithDB(db *gorm.DB, resolver contracts.ProjectMetaResolver, sender MessageSender, logger *slog.Logger) *Service {
	return NewService(NewGormRepository(db), sender, resolver, logger)
}

func (s *Service) Ready() error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("gateway send service 未初始化")
	}
	if checker, ok := s.repo.(interface{ Ready() error }); ok {
		if err := checker.Ready(); err != nil {
			return err
		}
	}
	return nil
}

func SendProjectText(ctx context.Context, db *gorm.DB, resolver contracts.ProjectMetaResolver, sender MessageSender, projectName, text string) (Response, error) {
	return SendProjectTextWithLogger(ctx, db, resolver, sender, nil, projectName, text)
}

func SendProjectTextWithLogger(ctx context.Context, db *gorm.DB, resolver contracts.ProjectMetaResolver, sender MessageSender, logger *slog.Logger, projectName, text string) (Response, error) {
	svc := NewServiceWithDB(db, resolver, sender, logger)
	return svc.Send(ctx, projectName, text)
}

func (s *Service) Send(ctx context.Context, projectName, text string) (Response, error) {
	projectName = strings.TrimSpace(projectName)
	text = strings.TrimSpace(text)
	if projectName == "" {
		return Response{}, fmt.Errorf("project 不能为空")
	}
	if text == "" {
		return Response{}, fmt.Errorf("text 不能为空")
	}
	if s == nil || s.repo == nil {
		return Response{}, fmt.Errorf("gateway repository 为空")
	}

	bindings, err := s.repo.FindEnabledBindings(ctx, projectName, contracts.ChannelTypeIM, AdapterFeishu)
	if err != nil {
		return Response{}, err
	}
	if len(bindings) == 0 {
		return Response{}, fmt.Errorf("%w: project=%s", ErrBindingNotFound, projectName)
	}

	cardProjectName := resolveCardProjectName(projectName, s.resolver)
	results := make([]Delivery, 0, len(bindings))
	delivered := 0
	failed := 0
	for _, binding := range bindings {
		delivery, err := s.sendOneBinding(ctx, binding, projectName, cardProjectName, text)
		if err != nil {
			delivery.Status = string(contracts.ChannelOutboxFailed)
			delivery.Error = strings.TrimSpace(err.Error())
			failed++
		} else {
			delivery.Status = string(contracts.ChannelOutboxSent)
			delivered++
		}
		results = append(results, delivery)
	}

	return Response{
		Schema:    ResponseSchemaV1,
		Project:   projectName,
		Text:      text,
		Delivered: delivered,
		Failed:    failed,
		Results:   results,
	}, nil
}

func (s *Service) sendOneBinding(ctx context.Context, binding contracts.ChannelBinding, projectName, cardProjectName, text string) (Delivery, error) {
	repo := s.repo
	logger := core.EnsureLogger(s.logger)
	sender := s.sender
	if sender == nil {
		sender = &NoopSender{}
	}

	chatID := strings.TrimSpace(binding.PeerProjectKey)
	out := Delivery{
		BindingID: binding.ID,
		ChatID:    chatID,
	}
	if chatID == "" {
		return out, fmt.Errorf("binding %d 缺少 chat_id", binding.ID)
	}
	if reused, ok, err := repo.FindRecentDuplicateDelivery(ctx, binding, text); err != nil {
		return out, err
	} else if ok {
		logger.Info("gateway send dedup",
			"dedup_type", "send_content",
			"binding_id", reused.BindingID,
			"conversation_id", reused.ConversationID,
			"message_id", reused.MessageID,
			"outbox_id", reused.OutboxID,
			"action", "skip",
			"window", sendDedupWindow.String(),
			"text_len", len(strings.TrimSpace(text)),
		)
		return reused, nil
	}

	state, err := repo.CreatePending(ctx, binding, projectName, text)
	if err != nil {
		return out, err
	}
	out.ConversationID = state.conversation.ID
	out.MessageID = state.message.ID
	out.OutboxID = state.outbox.ID

	if err := repo.MarkSending(ctx, state.outbox.ID); err != nil {
		_ = repo.MarkFailed(context.Background(), state, err)
		return out, err
	}

	sendCtx := ctx
	if sendCtx == nil {
		sendCtx = context.Background()
	}
	if err := sender.SendCard(sendCtx, chatID, buildCardTitle(cardProjectName), text); err != nil {
		_ = repo.MarkFailed(context.Background(), state, err)
		return out, err
	}

	if err := repo.MarkSent(ctx, state); err != nil {
		return out, err
	}
	return out, nil
}
