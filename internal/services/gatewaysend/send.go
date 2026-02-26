package gatewaysend

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

const (
	Path             = "/api/send"
	AdapterFeishu    = "im.feishu"
	ResponseSchemaV1 = "dalek.gateway_send.response.v1"
	payloadSchemaV1  = "dalek.gateway_send.payload.v1"
	sendDedupWindow  = 30 * time.Second
)

type Request struct {
	Project string `json:"project"`
	Text    string `json:"text"`
}

type Delivery struct {
	BindingID      uint   `json:"binding_id"`
	ConversationID uint   `json:"conversation_id"`
	MessageID      uint   `json:"message_id"`
	OutboxID       uint   `json:"outbox_id"`
	ChatID         string `json:"chat_id"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

type Response struct {
	Schema    string     `json:"schema"`
	Project   string     `json:"project"`
	Text      string     `json:"text"`
	Delivered int        `json:"delivered"`
	Failed    int        `json:"failed"`
	Results   []Delivery `json:"results,omitempty"`
}

type MessageSender interface {
	SendCard(ctx context.Context, chatID, title, markdown string) error
}

type NoopSender struct{}

func (s *NoopSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	_ = chatID
	_ = title
	_ = markdown
	return nil
}

type HandlerOptions struct {
	DB        *gorm.DB
	Resolver  contracts.ProjectMetaResolver
	Sender    MessageSender
	Logger    *slog.Logger
	AuthToken string
}

type persistState struct {
	conversation store.ChannelConversation
	message      store.ChannelMessage
	outbox       store.ChannelOutbox
}

var ErrBindingNotFound = errors.New("project 未绑定飞书 chat_id")

func NewHandler(opt HandlerOptions) http.HandlerFunc {
	sender := opt.Sender
	if sender == nil {
		sender = &NoopSender{}
	}
	logger := opt.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	authToken := strings.TrimSpace(opt.AuthToken)

	return func(w http.ResponseWriter, r *http.Request) {
		if authToken != "" && !isRequestAuthorized(r, authToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if opt.DB == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"code":  1,
				"error": "gateway db 未初始化",
			})
			return
		}

		var req Request
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code":  1,
				"error": "invalid json",
			})
			return
		}
		req.Project = strings.TrimSpace(req.Project)
		req.Text = strings.TrimSpace(req.Text)
		if req.Project == "" || req.Text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code":  1,
				"error": "project/text 不能为空",
			})
			return
		}

		result, err := SendProjectTextWithLogger(r.Context(), opt.DB, opt.Resolver, sender, logger, req.Project, req.Text)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrBindingNotFound) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{
				"code":    1,
				"error":   err.Error(),
				"project": req.Project,
			})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func SendProjectText(ctx context.Context, db *gorm.DB, resolver contracts.ProjectMetaResolver, sender MessageSender, projectName, text string) (Response, error) {
	return SendProjectTextWithLogger(ctx, db, resolver, sender, nil, projectName, text)
}

func SendProjectTextWithLogger(ctx context.Context, db *gorm.DB, resolver contracts.ProjectMetaResolver, sender MessageSender, logger *slog.Logger, projectName, text string) (Response, error) {
	projectName = strings.TrimSpace(projectName)
	text = strings.TrimSpace(text)
	if projectName == "" {
		return Response{}, fmt.Errorf("project 不能为空")
	}
	if text == "" {
		return Response{}, fmt.Errorf("text 不能为空")
	}
	if db == nil {
		return Response{}, fmt.Errorf("gateway db 为空")
	}
	if sender == nil {
		sender = &NoopSender{}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	var bindings []store.ChannelBinding
	if err := db.WithContext(ctx).
		Where("project_name = ? AND channel_type = ? AND adapter = ? AND enabled = 1",
			projectName,
			contracts.ChannelTypeIM,
			AdapterFeishu).
		Order("id ASC").
		Find(&bindings).Error; err != nil {
		return Response{}, err
	}
	if len(bindings) == 0 {
		return Response{}, fmt.Errorf("%w: project=%s", ErrBindingNotFound, projectName)
	}
	cardProjectName := resolveCardProjectName(projectName, resolver)

	results := make([]Delivery, 0, len(bindings))
	delivered := 0
	failed := 0
	for _, binding := range bindings {
		delivery, err := sendOneBinding(ctx, db, sender, logger, binding, projectName, cardProjectName, text)
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

func sendOneBinding(ctx context.Context, db *gorm.DB, sender MessageSender, logger *slog.Logger, binding store.ChannelBinding, projectName, cardProjectName, text string) (Delivery, error) {
	chatID := strings.TrimSpace(binding.PeerProjectKey)
	out := Delivery{
		BindingID: binding.ID,
		ChatID:    chatID,
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if chatID == "" {
		return out, fmt.Errorf("binding %d 缺少 chat_id", binding.ID)
	}
	if reused, ok, err := findRecentDuplicateDelivery(ctx, db, binding, text); err != nil {
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

	state, err := createPending(ctx, db, binding, projectName, text)
	if err != nil {
		return out, err
	}
	out.ConversationID = state.conversation.ID
	out.MessageID = state.message.ID
	out.OutboxID = state.outbox.ID

	if err := markSending(ctx, db, state.outbox.ID); err != nil {
		_ = markFailed(context.Background(), db, state, err)
		return out, err
	}

	sendCtx := ctx
	if sendCtx == nil {
		sendCtx = context.Background()
	}
	if err := sender.SendCard(sendCtx, chatID, buildCardTitle(cardProjectName), text); err != nil {
		_ = markFailed(context.Background(), db, state, err)
		return out, err
	}

	if err := markSent(ctx, db, state); err != nil {
		return out, err
	}
	return out, nil
}

func findRecentDuplicateDelivery(ctx context.Context, db *gorm.DB, binding store.ChannelBinding, text string) (Delivery, bool, error) {
	chatID := strings.TrimSpace(binding.PeerProjectKey)
	if chatID == "" {
		return Delivery{}, false, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Delivery{}, false, nil
	}

	var conv store.ChannelConversation
	err := db.WithContext(ctx).
		Where("binding_id = ? AND peer_conversation_id = ?", binding.ID, chatID).
		First(&conv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Delivery{}, false, nil
		}
		return Delivery{}, false, err
	}

	cutoff := time.Now().Add(-sendDedupWindow)
	var msg store.ChannelMessage
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

	var outbox store.ChannelOutbox
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

func createPending(ctx context.Context, db *gorm.DB, binding store.ChannelBinding, projectName, text string) (persistState, error) {
	var out persistState
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		conv, err := ensureConversationTx(ctx, tx, binding.ID, binding.PeerProjectKey)
		if err != nil {
			return err
		}
		out.conversation = conv

		payload := map[string]any{
			"schema":          payloadSchemaV1,
			"project":         strings.TrimSpace(projectName),
			"binding_id":      binding.ID,
			"conversation_id": conv.ID,
			"chat_id":         strings.TrimSpace(binding.PeerProjectKey),
			"text":            strings.TrimSpace(text),
			"created_at":      time.Now().Format(time.RFC3339),
		}
		payloadJSON := marshalPayload(payload)
		peerMessageID := fmt.Sprintf("send-%d-%s", time.Now().UnixNano(), randomHex(2))
		message := store.ChannelMessage{
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

		outbox := store.ChannelOutbox{
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

		if err := tx.WithContext(ctx).Model(&store.ChannelMessage{}).
			Where("id = ?", message.ID).
			Update("payload_json", payloadJSON).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Model(&store.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Update("payload_json", payloadJSON).Error
	})
	if err != nil {
		return persistState{}, err
	}
	return out, nil
}

func ensureConversationTx(ctx context.Context, tx *gorm.DB, bindingID uint, peerConversationID string) (store.ChannelConversation, error) {
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

func markSending(ctx context.Context, db *gorm.DB, outboxID uint) error {
	now := time.Now()
	res := db.WithContext(ctx).Model(&store.ChannelOutbox{}).
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
		return fmt.Errorf("outbox 状态不可发送: id=%d", outboxID)
	}
	return nil
}

func markSent(ctx context.Context, db *gorm.DB, state persistState) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.WithContext(ctx).Model(&store.ChannelOutbox{}).
			Where("id = ?", state.outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxSent,
				"last_error":    "",
				"next_retry_at": nil,
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&store.ChannelMessage{}).
			Where("id = ?", state.message.ID).
			Update("status", contracts.ChannelMessageSent).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Model(&store.ChannelConversation{}).
			Where("id = ?", state.conversation.ID).
			Updates(map[string]any{
				"last_message_at": &now,
				"updated_at":      now,
			}).Error
	})
}

func markFailed(ctx context.Context, db *gorm.DB, state persistState, cause error) error {
	errMsg := strings.TrimSpace(fmt.Sprint(cause))
	if errMsg == "" || errMsg == "<nil>" {
		errMsg = "gateway send failed"
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.WithContext(ctx).Model(&store.ChannelOutbox{}).
			Where("id = ?", state.outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxFailed,
				"last_error":    errMsg,
				"next_retry_at": nil,
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Model(&store.ChannelMessage{}).
			Where("id = ?", state.message.ID).
			Update("status", contracts.ChannelMessageFailed).Error
	})
}

func resolveCardProjectName(projectName string, resolver contracts.ProjectMetaResolver) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return ""
	}
	if resolver != nil {
		if project, err := resolver.ResolveProjectMeta(projectName); err == nil && project != nil {
			if base := repoBaseName(project.RepoRoot); base != "" {
				return base
			}
		}
	}
	return projectName
}

func repoBaseName(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return ""
	}
	base := strings.TrimSpace(filepath.Base(filepath.Clean(repoRoot)))
	if base == "" || base == "." {
		return ""
	}
	return base
}

func buildCardTitle(projectName string) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "dalek 通知"
	}
	return projectName
}

func marshalPayload(payload map[string]any) string {
	b, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func extractRequestToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if tok := strings.TrimSpace(r.URL.Query().Get("token")); tok != "" {
		return tok
	}
	if tok := strings.TrimSpace(r.Header.Get("X-Dalek-Token")); tok != "" {
		return tok
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authz) >= len("Bearer ") && strings.EqualFold(authz[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(authz[len("Bearer "):])
	}
	return ""
}

func isRequestAuthorized(r *http.Request, expectedToken string) bool {
	expectedToken = strings.TrimSpace(expectedToken)
	if expectedToken == "" {
		return false
	}
	actualToken := strings.TrimSpace(extractRequestToken(r))
	if actualToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actualToken), []byte(expectedToken)) == 1
}

func randomHex(nbytes int) string {
	if nbytes <= 0 {
		nbytes = 4
	}
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
