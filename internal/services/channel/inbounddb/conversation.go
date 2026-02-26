package inbounddb

import (
	"context"
	"errors"
	"strings"

	"dalek/internal/store"

	"gorm.io/gorm"
)

func EnsureConversationTx(ctx context.Context, tx *gorm.DB, bindingID uint, peerConversationID string) (store.ChannelConversation, error) {
	var conv store.ChannelConversation
	err := tx.WithContext(ctx).
		Where("binding_id = ? AND peer_conversation_id = ?", bindingID, strings.TrimSpace(peerConversationID)).
		First(&conv).Error
	if err == nil {
		return conv, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return store.ChannelConversation{}, err
	}
	conv = store.ChannelConversation{
		BindingID:          bindingID,
		PeerConversationID: strings.TrimSpace(peerConversationID),
	}
	if err := tx.WithContext(ctx).Create(&conv).Error; err != nil {
		return store.ChannelConversation{}, err
	}
	return conv, nil
}
