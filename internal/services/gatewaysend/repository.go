package gatewaysend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

type GormRepository struct {
	db  *gorm.DB
	now func() time.Time
}

func NewGormRepository(db *gorm.DB) *GormRepository {
	return &GormRepository{db: db, now: time.Now}
}

func (r *GormRepository) Ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("gateway db 未初始化")
	}
	return nil
}

func (r *GormRepository) dbOrErr() (*gorm.DB, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("gateway db 为空")
	}
	return r.db, nil
}

func (r *GormRepository) nowOrDefault() time.Time {
	if r != nil && r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *GormRepository) FindEnabledBindings(ctx context.Context, projectName string, channelType contracts.ChannelType, adapter string) ([]contracts.ChannelBinding, error) {
	db, err := r.dbOrErr()
	if err != nil {
		return nil, err
	}
	projectName = strings.TrimSpace(projectName)
	adapter = strings.TrimSpace(adapter)

	var bindings []contracts.ChannelBinding
	if err := db.WithContext(ctx).
		Where("project_name = ? AND channel_type = ? AND adapter = ? AND enabled = 1", projectName, channelType, adapter).
		Order("id ASC").
		Find(&bindings).Error; err != nil {
		return nil, err
	}
	return bindings, nil
}

func (r *GormRepository) FindRecentDuplicateDelivery(ctx context.Context, binding contracts.ChannelBinding, text string) (Delivery, bool, error) {
	db, err := r.dbOrErr()
	if err != nil {
		return Delivery{}, false, err
	}
	chatID := strings.TrimSpace(binding.PeerProjectKey)
	if chatID == "" {
		return Delivery{}, false, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Delivery{}, false, nil
	}

	var conv contracts.ChannelConversation
	err = db.WithContext(ctx).
		Where("binding_id = ? AND peer_conversation_id = ?", binding.ID, chatID).
		First(&conv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Delivery{}, false, nil
		}
		return Delivery{}, false, err
	}

	cutoff := r.nowOrDefault().Add(-sendDedupWindow)
	var msg contracts.ChannelMessage
	err = db.WithContext(ctx).
		Where("conversation_id = ? AND direction = ? AND adapter = ? AND content_text = ? AND status = ? AND created_at >= ?",
			conv.ID,
			contracts.ChannelMessageOut,
			strings.TrimSpace(binding.Adapter),
			text,
			contracts.ChannelMessageSent,
			cutoff).
		Order("id DESC").
		First(&msg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Delivery{}, false, nil
		}
		return Delivery{}, false, err
	}

	var outbox contracts.ChannelOutbox
	if err := db.WithContext(ctx).Where("message_id = ?", msg.ID).First(&outbox).Error; err != nil {
		return Delivery{}, false, err
	}
	if outbox.Status != contracts.ChannelOutboxSent {
		return Delivery{}, false, nil
	}

	return Delivery{
		BindingID:      binding.ID,
		ConversationID: conv.ID,
		MessageID:      msg.ID,
		OutboxID:       outbox.ID,
		ChatID:         chatID,
		Status:         string(outbox.Status),
		Error:          strings.TrimSpace(outbox.LastError),
	}, true, nil
}

func (r *GormRepository) CreatePending(ctx context.Context, binding contracts.ChannelBinding, projectName, text string) (persistState, error) {
	db, err := r.dbOrErr()
	if err != nil {
		return persistState{}, err
	}
	var out persistState
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		conv, err := ensureConversationTx(ctx, tx, binding.ID, binding.PeerProjectKey)
		if err != nil {
			return err
		}
		out.conversation = conv

		now := r.nowOrDefault()
		payload := map[string]any{
			"schema":          payloadSchemaV1,
			"project":         strings.TrimSpace(projectName),
			"binding_id":      binding.ID,
			"conversation_id": conv.ID,
			"chat_id":         strings.TrimSpace(binding.PeerProjectKey),
			"text":            strings.TrimSpace(text),
			"created_at":      now.Format(time.RFC3339),
		}
		payloadJSON := marshalPayload(payload)
		peerMessageID := fmt.Sprintf("send-%d-%s", now.UnixNano(), randomHex(2))
		message := contracts.ChannelMessage{
			ConversationID: conv.ID,
			Direction:      contracts.ChannelMessageOut,
			Adapter:        strings.TrimSpace(binding.Adapter),
			PeerMessageID:  &peerMessageID,
			SenderID:       "gateway.send",
			ContentText:    strings.TrimSpace(text),
			PayloadJSON:    payloadJSON,
			Status:         contracts.ChannelMessageProcessed,
		}
		if err := tx.WithContext(ctx).Create(&message).Error; err != nil {
			return err
		}
		out.message = message

		outbox := contracts.ChannelOutbox{
			MessageID:   message.ID,
			Adapter:     strings.TrimSpace(binding.Adapter),
			PayloadJSON: payloadJSON,
			Status:      contracts.ChannelOutboxPending,
			RetryCount:  0,
			LastError:   "",
		}
		if err := tx.WithContext(ctx).Create(&outbox).Error; err != nil {
			return err
		}
		out.outbox = outbox

		payload["message_id"] = message.ID
		payload["outbox_id"] = outbox.ID
		payloadJSON = marshalPayload(payload)

		if err := tx.WithContext(ctx).Model(&contracts.ChannelMessage{}).
			Where("id = ?", message.ID).
			Update("payload_json", payloadJSON).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Model(&contracts.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Update("payload_json", payloadJSON).Error
	})
	if err != nil {
		return persistState{}, err
	}
	return out, nil
}

func ensureConversationTx(ctx context.Context, tx *gorm.DB, bindingID uint, peerConversationID string) (contracts.ChannelConversation, error) {
	peerConversationID = strings.TrimSpace(peerConversationID)
	var conv contracts.ChannelConversation
	err := tx.WithContext(ctx).
		Where("binding_id = ? AND peer_conversation_id = ?", bindingID, peerConversationID).
		First(&conv).Error
	if err == nil {
		return conv, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return contracts.ChannelConversation{}, err
	}
	conv = contracts.ChannelConversation{
		BindingID:          bindingID,
		PeerConversationID: peerConversationID,
	}
	if err := tx.WithContext(ctx).Create(&conv).Error; err != nil {
		return contracts.ChannelConversation{}, err
	}
	return conv, nil
}

func (r *GormRepository) MarkSending(ctx context.Context, outboxID uint) error {
	db, err := r.dbOrErr()
	if err != nil {
		return err
	}
	now := r.nowOrDefault()
	res := db.WithContext(ctx).Model(&contracts.ChannelOutbox{}).
		Where("id = ? AND status IN ?", outboxID, []contracts.ChannelOutboxStatus{
			contracts.ChannelOutboxPending,
			contracts.ChannelOutboxFailed,
		}).
		Updates(map[string]any{
			"status":        contracts.ChannelOutboxSending,
			"retry_count":   gorm.Expr("retry_count + 1"),
			"last_error":    "",
			"next_retry_at": nil,
			"updated_at":    now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: id=%d", ErrOutboxNotSendable, outboxID)
	}
	return nil
}

func (r *GormRepository) MarkSent(ctx context.Context, state persistState) error {
	db, err := r.dbOrErr()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := r.nowOrDefault()
		if err := tx.WithContext(ctx).Model(&contracts.ChannelOutbox{}).
			Where("id = ?", state.outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxSent,
				"last_error":    "",
				"next_retry_at": nil,
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.ChannelMessage{}).
			Where("id = ?", state.message.ID).
			Update("status", contracts.ChannelMessageSent).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Model(&contracts.ChannelConversation{}).
			Where("id = ?", state.conversation.ID).
			Updates(map[string]any{
				"last_message_at": &now,
				"updated_at":      now,
			}).Error
	})
}

func (r *GormRepository) MarkFailed(ctx context.Context, state persistState, cause error) error {
	db, err := r.dbOrErr()
	if err != nil {
		return err
	}
	errMsg := strings.TrimSpace(fmt.Sprint(cause))
	if errMsg == "" || errMsg == "<nil>" {
		errMsg = "gateway send failed"
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := r.nowOrDefault()
		if err := tx.WithContext(ctx).Model(&contracts.ChannelOutbox{}).
			Where("id = ?", state.outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxFailed,
				"last_error":    errMsg,
				"next_retry_at": nil,
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Model(&contracts.ChannelMessage{}).
			Where("id = ?", state.message.ID).
			Update("status", contracts.ChannelMessageFailed).Error
	})
}

func (r *GormRepository) MarkFailedRetryable(ctx context.Context, state persistState, cause error, nextRetryAt time.Time) error {
	db, err := r.dbOrErr()
	if err != nil {
		return err
	}
	errMsg := strings.TrimSpace(fmt.Sprint(cause))
	if errMsg == "" || errMsg == "<nil>" {
		errMsg = "gateway send failed"
	}
	if nextRetryAt.IsZero() {
		nextRetryAt = r.nowOrDefault()
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := r.nowOrDefault()
		if err := tx.WithContext(ctx).Model(&contracts.ChannelOutbox{}).
			Where("id = ?", state.outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxFailed,
				"last_error":    errMsg,
				"next_retry_at": &nextRetryAt,
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Model(&contracts.ChannelMessage{}).
			Where("id = ?", state.message.ID).
			Update("status", contracts.ChannelMessageFailed).Error
	})
}

func (r *GormRepository) MarkDead(ctx context.Context, state persistState, cause error) error {
	db, err := r.dbOrErr()
	if err != nil {
		return err
	}
	errMsg := strings.TrimSpace(fmt.Sprint(cause))
	if errMsg == "" || errMsg == "<nil>" {
		errMsg = "gateway send failed"
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := r.nowOrDefault()
		if err := tx.WithContext(ctx).Model(&contracts.ChannelOutbox{}).
			Where("id = ?", state.outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxDead,
				"last_error":    errMsg,
				"next_retry_at": nil,
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Model(&contracts.ChannelMessage{}).
			Where("id = ?", state.message.ID).
			Update("status", contracts.ChannelMessageFailed).Error
	})
}

func (r *GormRepository) FindRetryableOutbox(ctx context.Context, now time.Time, limit int) ([]retryableOutbox, error) {
	db, err := r.dbOrErr()
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = r.nowOrDefault()
	}
	if limit <= 0 {
		limit = 20
	}
	var outboxes []contracts.ChannelOutbox
	if err := db.WithContext(ctx).
		Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", contracts.ChannelOutboxFailed, now).
		Order("next_retry_at ASC, id ASC").
		Limit(limit).
		Find(&outboxes).Error; err != nil {
		return nil, err
	}
	items := make([]retryableOutbox, 0, len(outboxes))
	for _, outbox := range outboxes {
		var message contracts.ChannelMessage
		if err := db.WithContext(ctx).First(&message, outbox.MessageID).Error; err != nil {
			return nil, err
		}
		var conversation contracts.ChannelConversation
		if err := db.WithContext(ctx).First(&conversation, message.ConversationID).Error; err != nil {
			return nil, err
		}
		var binding contracts.ChannelBinding
		if err := db.WithContext(ctx).First(&binding, conversation.BindingID).Error; err != nil {
			return nil, err
		}

		projectName := strings.TrimSpace(binding.ProjectName)
		text := strings.TrimSpace(message.ContentText)
		payload := contracts.JSONMapFromAny(message.PayloadJSON)
		if projectName == "" {
			if p, ok := payload["project"].(string); ok {
				projectName = strings.TrimSpace(p)
			}
		}
		if text == "" {
			if t, ok := payload["text"].(string); ok {
				text = strings.TrimSpace(t)
			}
		}

		items = append(items, retryableOutbox{
			binding: binding,
			project: projectName,
			text:    text,
			state: persistState{
				conversation: conversation,
				message:      message,
				outbox:       outbox,
			},
		})
	}
	return items, nil
}
