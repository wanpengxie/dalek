package gatewaysend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

type Service struct {
	repo     Repository
	sender   MessageSender
	resolver contracts.ProjectMetaResolver
	logger   *slog.Logger
	policy   RetryPolicy
	now      func() time.Time
}

type chatReplySender interface {
	SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error)
}

type createPendingWithPayloadRepo interface {
	CreatePendingWithPayload(ctx context.Context, binding contracts.ChannelBinding, projectName, text string, payloadExtra contracts.JSONMap) (persistState, error)
}

type sendContent struct {
	text     string
	cardJSON string
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
		policy:   DefaultRetryPolicy(),
		now:      time.Now,
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
	if ctx == nil {
		ctx = context.Background()
	}
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
	content := sendContent{text: text}
	for _, binding := range bindings {
		delivery, err := s.sendOneBinding(ctx, binding, projectName, cardProjectName, content, true)
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

func (s *Service) SendChatReply(ctx context.Context, projectName, chatID, text, cardJSON string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	projectName = strings.TrimSpace(projectName)
	chatID = strings.TrimSpace(chatID)
	text = strings.TrimSpace(text)
	cardJSON = strings.TrimSpace(cardJSON)
	if projectName == "" {
		return fmt.Errorf("project 不能为空")
	}
	if chatID == "" {
		return fmt.Errorf("chat_id 不能为空")
	}
	if text == "" && cardJSON == "" {
		return fmt.Errorf("text 与 card_json 不能同时为空")
	}
	if s == nil || s.repo == nil {
		return fmt.Errorf("gateway repository 为空")
	}

	binding, err := s.findBindingByChatID(ctx, projectName, chatID)
	if err != nil {
		return err
	}
	cardProjectName := resolveCardProjectName(projectName, s.resolver)
	_, err = s.sendOneBinding(ctx, binding, projectName, cardProjectName, sendContent{
		text:     text,
		cardJSON: cardJSON,
	}, false)
	return err
}

func (s *Service) findBindingByChatID(ctx context.Context, projectName, chatID string) (contracts.ChannelBinding, error) {
	bindings, err := s.repo.FindEnabledBindings(ctx, projectName, contracts.ChannelTypeIM, AdapterFeishu)
	if err != nil {
		return contracts.ChannelBinding{}, err
	}
	for _, binding := range bindings {
		if strings.TrimSpace(binding.PeerProjectKey) == chatID {
			return binding, nil
		}
	}
	return contracts.ChannelBinding{}, fmt.Errorf("%w: project=%s chat_id=%s", ErrBindingNotFound, projectName, chatID)
}

func (s *Service) sendOneBinding(ctx context.Context, binding contracts.ChannelBinding, projectName, cardProjectName string, content sendContent, enableDedup bool) (Delivery, error) {
	repo := s.repo
	logger := core.EnsureLogger(s.logger)

	chatID := strings.TrimSpace(binding.PeerProjectKey)
	out := Delivery{
		BindingID: binding.ID,
		ChatID:    chatID,
	}
	if chatID == "" {
		return out, fmt.Errorf("binding %d 缺少 chat_id", binding.ID)
	}
	content.text = strings.TrimSpace(content.text)
	content.cardJSON = strings.TrimSpace(content.cardJSON)
	if content.text == "" {
		return out, fmt.Errorf("text 不能为空")
	}
	if enableDedup {
		if reused, ok, err := repo.FindRecentDuplicateDelivery(ctx, binding, content.text); err != nil {
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
				"text_len", len(content.text),
			)
			return reused, nil
		}
	}

	state, err := s.createPending(ctx, binding, projectName, content)
	if err != nil {
		return out, err
	}
	out.ConversationID = state.conversation.ID
	out.MessageID = state.message.ID
	out.OutboxID = state.outbox.ID

	if err := s.sendPersistedOutbox(ctx, binding, state, cardProjectName, content); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Service) createPending(ctx context.Context, binding contracts.ChannelBinding, projectName string, content sendContent) (persistState, error) {
	if payloadRepo, ok := s.repo.(createPendingWithPayloadRepo); ok && content.cardJSON != "" {
		return payloadRepo.CreatePendingWithPayload(ctx, binding, projectName, content.text, contracts.JSONMap{
			payloadKeyCardJSON: content.cardJSON,
			payloadKeySendMode: payloadSendModeInteractive,
		})
	}
	return s.repo.CreatePending(ctx, binding, projectName, content.text)
}

func (s *Service) sendPersistedOutbox(ctx context.Context, binding contracts.ChannelBinding, state persistState, cardProjectName string, content sendContent) error {
	repo := s.repo
	if repo == nil {
		return fmt.Errorf("gateway repository 为空")
	}
	persistCtx := ctx
	if persistCtx == nil {
		persistCtx = context.Background()
	}
	if err := repo.MarkSending(persistCtx, state.outbox.ID); err != nil {
		if errors.Is(err, ErrOutboxNotSendable) {
			return err
		}
		if markErr := repo.MarkFailed(context.Background(), state, err); markErr != nil {
			return fmt.Errorf("%w; mark failed error: %v", err, markErr)
		}
		return err
	}
	state.outbox.RetryCount++

	sendCtx := persistCtx
	if err := s.sendContentWithFallback(sendCtx, binding, cardProjectName, content); err != nil {
		if markErr := s.markFailedWithRetryPolicy(state, err); markErr != nil {
			return fmt.Errorf("%w; persist retry state failed: %v", err, markErr)
		}
		return err
	}

	if err := repo.MarkSent(persistCtx, state); err != nil {
		return err
	}
	return nil
}

func (s *Service) sendContentWithFallback(ctx context.Context, binding contracts.ChannelBinding, cardProjectName string, content sendContent) error {
	content.text = strings.TrimSpace(content.text)
	content.cardJSON = strings.TrimSpace(content.cardJSON)
	if content.cardJSON != "" {
		if sender, ok := s.sender.(chatReplySender); ok {
			chatID := strings.TrimSpace(binding.PeerProjectKey)
			resultMid, err := sender.SendCardInteractive(ctx, chatID, content.cardJSON)
			if err == nil && strings.TrimSpace(resultMid) != "" {
				return nil
			}
			if err == nil {
				err = fmt.Errorf("feishu send interactive failed: empty message_id")
			}
			core.EnsureLogger(s.logger).Warn("gateway send interactive card failed, fallback to card/text",
				"binding_id", binding.ID,
				"chat_id", chatID,
				"error", err,
			)
		}
	}
	return s.sendCardWithTextFallback(ctx, binding, cardProjectName, content.text)
}

func (s *Service) sendCardWithTextFallback(ctx context.Context, binding contracts.ChannelBinding, cardProjectName, text string) error {
	sender := s.sender
	if sender == nil {
		sender = &NoopSender{}
	}
	chatID := strings.TrimSpace(binding.PeerProjectKey)
	if err := sender.SendCard(ctx, chatID, buildCardTitle(cardProjectName), text); err == nil {
		return nil
	} else {
		core.EnsureLogger(s.logger).Warn("gateway send card failed, fallback to text",
			"binding_id", binding.ID,
			"chat_id", chatID,
			"error", err,
		)
		if textErr := sender.SendText(ctx, chatID, text); textErr == nil {
			return nil
		} else {
			return fmt.Errorf("send card failed: %w; text fallback failed: %v", err, textErr)
		}
	}
}

func (s *Service) markFailedWithRetryPolicy(state persistState, cause error) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("gateway repository 为空")
	}
	policy := s.policy.normalize()
	attempt := state.outbox.RetryCount
	if policy.IsExhausted(attempt) {
		return s.repo.MarkDead(context.Background(), state, cause)
	}
	nextRetryAt := policy.NextRetryAt(attempt, s.nowOrDefault())
	return s.repo.MarkFailedRetryable(context.Background(), state, cause, nextRetryAt)
}

func (s *Service) sendRetryableOutbox(ctx context.Context, item retryableOutbox) error {
	projectName := strings.TrimSpace(item.project)
	cardProjectName := resolveCardProjectName(projectName, s.resolver)
	return s.sendPersistedOutbox(ctx, item.binding, item.state, cardProjectName, sendContent{
		text:     item.text,
		cardJSON: item.cardJSON,
	})
}

func (s *Service) nowOrDefault() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}
