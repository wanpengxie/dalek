package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/services/core"
)

const (
	defaultDaemonFeishuAdapter = "im.feishu"

	daemonFeishuCardDefaultTitle    = "dalek"
	daemonFeishuCardTitleMaxRunes   = 64
	daemonFeishuCardMarkdownMaxRune = 28000
	daemonFeishuStreamProgressMax   = 240
	daemonFeishuProgressHistoryMax  = 20
	daemonFeishuUserNameCacheTTL    = 10 * time.Minute
	daemonFeishuEventDedupTTL       = 24 * time.Hour
	daemonFeishuEventDedupCap       = 10000
)

const DefaultAdapter = defaultDaemonFeishuAdapter

var (
	daemonFeishuRelayTimeout     = 10 * time.Minute
	daemonFeishuRelayIdleTimeout = 5 * time.Minute
)

type daemonFeishuWebhookOptions struct {
	Adapter           string
	VerifyToken       string
	EventDeduplicator *channelsvc.EventDeduplicator
	Logger            *slog.Logger
	RelayTimeout      time.Duration
	RelayIdleTimeout  time.Duration
}

type HandlerOptions = daemonFeishuWebhookOptions

type SenderConfig struct {
	Enabled     bool
	AppID       string
	AppSecret   string
	BaseURL     string
	UserNameTTL time.Duration
	Logger      *slog.Logger
}

type daemonFeishuWebhookRequest struct {
	Type      string `json:"type"`
	Token     string `json:"token"`
	Challenge string `json:"challenge"`

	Header struct {
		EventType string `json:"event_type"`
		Token     string `json:"token"`
		EventID   string `json:"event_id"`
	} `json:"header"`

	Event struct {
		Type          string `json:"type"`
		Token         string `json:"token"`
		EventID       string `json:"event_id"`
		OpenMessageID string `json:"open_message_id"`
		OpenChatID    string `json:"open_chat_id"`
		Sender        struct {
			SenderID struct {
				OpenID string `json:"open_id"`
				UserID string `json:"user_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Operator struct {
			OperatorID struct {
				OpenID string `json:"open_id"`
				UserID string `json:"user_id"`
			} `json:"operator_id"`
			OpenID string `json:"open_id"`
			UserID string `json:"user_id"`
			Name   string `json:"name"`
		} `json:"operator"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"message"`
		Action struct {
			Tag   string         `json:"tag"`
			Value map[string]any `json:"value"`
		} `json:"action"`
		Context struct {
			OpenMessageID string `json:"open_message_id"`
			OpenChatID    string `json:"open_chat_id"`
			ChatID        string `json:"chat_id"`
		} `json:"context"`
	} `json:"event"`
}

type daemonFeishuCardActionPayload struct {
	ChatID          string
	MessageID       string
	PendingActionID uint
	Decision        channelsvc.PendingActionDecision
	DeciderID       string
	DeciderName     string
	Note            string
}

type daemonFeishuMessageSender interface {
	SendText(ctx context.Context, chatID, text string) error
	SendCard(ctx context.Context, chatID, title, markdown string) error
	SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error)
	PatchCard(ctx context.Context, messageID, cardJSON string) error
	GetUserName(ctx context.Context, userID string) (string, error)
}

type MessageSender = daemonFeishuMessageSender

type daemonFeishuNoopSender struct{}

type NoopSender = daemonFeishuNoopSender

func (s *daemonFeishuNoopSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	_ = chatID
	_ = text
	return nil
}

func (s *daemonFeishuNoopSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	_ = chatID
	_ = title
	_ = markdown
	return nil
}

func (s *daemonFeishuNoopSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	_ = chatID
	_ = cardJSON
	return "", nil
}

func (s *daemonFeishuNoopSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	_ = ctx
	_ = messageID
	_ = cardJSON
	return nil
}

func (s *daemonFeishuNoopSender) GetUserName(ctx context.Context, userID string) (string, error) {
	_ = ctx
	_ = userID
	return "", nil
}

type daemonFeishuUserNameCacheEntry struct {
	name      string
	expiresAt time.Time
}

type daemonFeishuHTTPSender struct {
	client    *http.Client
	baseURL   string
	appID     string
	appSecret string
	logger    *slog.Logger

	mu         sync.Mutex
	token      string
	tokenUntil time.Time

	userNameTTL time.Duration
	userNames   sync.Map
}

func NewSender(cfg SenderConfig) MessageSender {
	logger := core.EnsureLogger(cfg.Logger).With("service", "feishu_sender")
	if !cfg.Enabled {
		return &daemonFeishuNoopSender{}
	}
	appID := strings.TrimSpace(cfg.AppID)
	appSecret := strings.TrimSpace(cfg.AppSecret)
	if appID == "" || appSecret == "" {
		return &daemonFeishuNoopSender{}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://open.feishu.cn"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "https://open.feishu.cn"
	}
	return &daemonFeishuHTTPSender{
		client:      &http.Client{Timeout: 12 * time.Second},
		baseURL:     baseURL,
		appID:       appID,
		appSecret:   appSecret,
		logger:      logger,
		userNameTTL: cfg.UserNameTTL,
	}
}

func NewWebhookHandler(gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender MessageSender, rawOpt HandlerOptions) http.HandlerFunc {
	opt := rawOpt
	opt.Logger = core.EnsureLogger(opt.Logger).With("service", "feishu_webhook")
	return newDaemonFeishuWebhookHandler(gateway, resolver, sender, opt, opt.Logger)
}

func TryHandleBindCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender MessageSender, adapter, chatID, text string) bool {
	return tryHandleDaemonFeishuBindCommand(ctx, gateway, resolver, sender, adapter, chatID, text)
}

func TryHandleUnbindCommand(ctx context.Context, gateway *channelsvc.Gateway, sender MessageSender, adapter, chatID, text string) bool {
	return tryHandleDaemonFeishuUnbindCommand(ctx, gateway, sender, adapter, chatID, text)
}

func TryHandleInterruptCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender MessageSender, adapter, chatID, text string) bool {
	return tryHandleDaemonFeishuInterruptCommand(ctx, gateway, resolver, sender, adapter, chatID, text)
}

func TryHandleNewCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender MessageSender, adapter, chatID, text string) bool {
	return tryHandleDaemonFeishuNewCommand(ctx, gateway, resolver, sender, adapter, chatID, text)
}

func BuildUnboundHint(resolver channelsvc.ProjectResolver) string {
	return buildDaemonFeishuUnboundHint(resolver)
}

func AppendProgressLine(lines []string, line string, maxLines int) []string {
	return appendDaemonFeishuProgressLine(lines, line, maxLines)
}

func NormalizeCardMarkdown(markdown string) string {
	return normalizeDaemonFeishuCardMarkdown(markdown)
}

func ResolveCardProjectName(projectName string, resolver channelsvc.ProjectResolver) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return ""
	}
	if resolver != nil {
		project, err := resolver.Resolve(projectName)
		if err == nil && project != nil {
			if base := daemonFeishuRepoBaseName(project.RepoRoot); base != "" {
				return base
			}
		}
	}
	return projectName
}

func newDaemonFeishuWebhookHandler(gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender daemonFeishuMessageSender, rawOpt daemonFeishuWebhookOptions, logger *slog.Logger) http.HandlerFunc {
	opt := rawOpt
	opt.Adapter = strings.TrimSpace(opt.Adapter)
	if opt.Adapter == "" {
		opt.Adapter = defaultDaemonFeishuAdapter
	}
	relayTimeout := daemonFeishuRelayTimeout
	if opt.RelayTimeout > 0 {
		relayTimeout = opt.RelayTimeout
	}
	relayIdleTimeout := daemonFeishuRelayIdleTimeout
	if opt.RelayIdleTimeout > 0 {
		relayIdleTimeout = opt.RelayIdleTimeout
	}
	if sender == nil {
		sender = &daemonFeishuNoopSender{}
	}
	dedup := opt.EventDeduplicator
	if dedup == nil {
		dedup = channelsvc.NewEventDeduplicator(daemonFeishuEventDedupTTL, daemonFeishuEventDedupCap)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		reqCtx := r.Context()
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if gateway == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"code":  1,
				"error": "gateway runtime unavailable",
			})
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}
		var req daemonFeishuWebhookRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		verifyToken := strings.TrimSpace(opt.VerifyToken)
		if verifyToken == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"code": 1,
				"msg":  "verification token not configured",
			})
			return
		}
		requestToken := strings.TrimSpace(req.Header.Token)
		if requestToken == "" {
			requestToken = strings.TrimSpace(req.Token)
		}
		if requestToken == "" {
			requestToken = strings.TrimSpace(req.Event.Token)
		}
		if requestToken != verifyToken {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"code": 1,
				"msg":  "invalid token",
			})
			return
		}

		if strings.EqualFold(strings.TrimSpace(req.Type), "url_verification") {
			writeJSON(w, http.StatusOK, map[string]any{
				"challenge": req.Challenge,
			})
			return
		}

		eventType := strings.TrimSpace(req.Header.EventType)
		if eventType == "" {
			eventType = strings.TrimSpace(req.Event.Type)
		}
		eventID := strings.TrimSpace(req.Header.EventID)
		if eventID == "" {
			eventID = strings.TrimSpace(req.Event.EventID)
		}
		if eventID != "" && dedup.IsDuplicate(eventID) {
			peerID := strings.TrimSpace(req.Event.Message.MessageID)
			if peerID == "" {
				peerID = strings.TrimSpace(req.Event.Context.OpenMessageID)
			}
			if peerID == "" {
				peerID = strings.TrimSpace(req.Event.OpenMessageID)
			}
			logDaemonFeishuf(logger, "daemon feishu dedup: dedup_type=event_id dedup_key=%s peer_msg=%s action=skip",
				eventID, peerID)
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		switch eventType {
		case "card.action.trigger", "card.action.trigger_v1":
			handleDaemonFeishuCardActionTrigger(reqCtx, w, gateway, resolver, sender, opt, req, logger)
			return
		case "im.message.receive_v1":
			// 继续走消息处理链路
		default:
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if strings.EqualFold(strings.TrimSpace(req.Event.Sender.SenderType), "app") {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}

		chatID := strings.TrimSpace(req.Event.Message.ChatID)
		msgID := strings.TrimSpace(req.Event.Message.MessageID)
		if chatID == "" || msgID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code": 1,
				"msg":  "missing chat/message id",
			})
			return
		}

		text, supported := parseDaemonFeishuMessageText(
			req.Event.Message.MessageType,
			req.Event.Message.Content,
		)
		if !supported {
			_ = sender.SendText(reqCtx, chatID, "暂不支持此类消息")
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		text = strings.TrimSpace(text)
		if text == "" {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}

		senderID := strings.TrimSpace(req.Event.Sender.SenderID.OpenID)
		if senderID == "" {
			senderID = strings.TrimSpace(req.Event.Sender.SenderID.UserID)
		}
		senderName := ""
		if senderID != "" {
			if resolvedName, nameErr := sender.GetUserName(reqCtx, senderID); nameErr != nil {
				logDaemonFeishuf(logger, "GetUserName failed for %s: %v", senderID, nameErr)
			} else {
				senderName = strings.TrimSpace(resolvedName)
			}
		}
		if senderID == "" {
			senderID = "feishu.user"
		}

		if handled := tryHandleDaemonFeishuBindCommand(reqCtx, gateway, resolver, sender, opt.Adapter, chatID, text); handled {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if handled := tryHandleDaemonFeishuUnbindCommand(reqCtx, gateway, sender, opt.Adapter, chatID, text); handled {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if handled := tryHandleDaemonFeishuNewCommand(reqCtx, gateway, resolver, sender, opt.Adapter, chatID, text); handled {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if handled := tryHandleDaemonFeishuInterruptCommand(reqCtx, gateway, resolver, sender, opt.Adapter, chatID, text); handled {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}

		boundProject, err := gateway.LookupBoundProject(reqCtx, contracts.ChannelTypeIM, opt.Adapter, chatID)
		if err != nil {
			logDaemonFeishuf(logger, "lookup bound project failed: chat=%s err=%v", chatID, err)
			_ = sender.SendText(reqCtx, chatID, "读取绑定失败，请稍后重试")
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if boundProject == "" {
			_ = sender.SendText(reqCtx, chatID, buildDaemonFeishuUnboundHint(resolver))
			writeJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}

		sub, unsubscribe := gateway.EventBus().Subscribe(boundProject, chatID, 256)
		// relay 可能长于 webhook 请求生命周期，使用独立上下文避免随 HTTP 连接提前取消。
		relayCtx, relayCancel := context.WithTimeout(context.Background(), relayTimeout)
		progressCtx, progressCancel := context.WithCancel(relayCtx)

		var (
			progressMu        sync.Mutex
			progressCardMsgID string
			progressLines     []string
			progressFinalized bool
		)
		var writeMu sync.Mutex

		logFinal := func(format string, args ...any) {
			baseArgs := []any{chatID, msgID}
			allArgs := append(baseArgs, args...)
			logDaemonFeishuf(logger, "daemon feishu final-reply: chat=%s peer_msg=%s "+format, allArgs...)
		}
		markOutbox := func(outboxID uint, delivered bool, cause error) {
			if gateway == nil || outboxID == 0 {
				return
			}
			// outbox 回写不能依赖 relayCtx，否则在 final reply 成功后 relayCancel 会导致状态无法落库。
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := gateway.MarkOutboxDelivery(ctx, outboxID, delivered, cause); err != nil {
				logFinal("mark outbox failed outbox=%d delivered=%v err=%v", outboxID, delivered, err)
				return
			}
			logFinal("mark outbox ok outbox=%d delivered=%v", outboxID, delivered)
		}

		var (
			finalMu      sync.Mutex
			finalSent    bool
			finalOutbox  uint
			finalAttempt int
		)
		sendTextFallback := func(attempt int, source string, outboxID uint, reply string, prevErr error) bool {
			textCtx, textCancel := context.WithTimeout(context.Background(), 8*time.Second)
			textErr := sender.SendText(textCtx, chatID, reply)
			textCancel()
			if textErr == nil {
				finalSent = true
				relayCancel()
				logFinal("attempt=%d source=%s text_fallback_success", attempt, source)
				markOutbox(outboxID, true, nil)
				return true
			}
			if prevErr != nil {
				logFinal("attempt=%d source=%s text_fallback_failed err=%v; text_err=%v", attempt, source, prevErr, textErr)
				markOutbox(outboxID, false, fmt.Errorf("%w; text fallback failed: %v", prevErr, textErr))
				relayCancel()
				return false
			}
			logFinal("attempt=%d source=%s text_fallback_failed err=%v", attempt, source, textErr)
			markOutbox(outboxID, false, textErr)
			relayCancel()
			return false
		}
		sendFinalReply := func(source, reply string) {
			reply = strings.TrimSpace(reply)
			if reply == "" {
				reply = "(empty reply)"
			}

			finalMu.Lock()
			defer finalMu.Unlock()

			if finalSent {
				logFinal("skip source=%s reason=already_sent", source)
				return
			}
			finalAttempt++
			attempt := finalAttempt
			outboxID := finalOutbox

			progressMu.Lock()
			progressFinalized = true
			progressMu.Unlock()

			writeMu.Lock()
			progressCancel()
			progressMu.Lock()
			mid := progressCardMsgID
			lines := append([]string(nil), progressLines...)
			progressMu.Unlock()
			if mid != "" {
				collapseJSON := buildDaemonFeishuProgressCollapsedCardJSON(lines)
				ctx, cancel := context.WithTimeout(relayCtx, 5*time.Second)
				err := sender.PatchCard(ctx, mid, collapseJSON)
				cancel()
				if err != nil {
					logFinal("attempt=%d source=%s progress_collapse_failed mid=%s err=%v", attempt, source, mid, err)
				} else {
					logFinal("attempt=%d source=%s progress_collapse_success mid=%s", attempt, source, mid)
				}
			}
			writeMu.Unlock()

			logFinal("attempt=%d source=%s outbox=%d has_progress_card=%v reply_len=%d", attempt, source, outboxID, mid != "", utf8.RuneCountInString(reply))

			cardJSON := buildDaemonFeishuResultCardJSON(reply)
			var lastErr error
			for i := 0; i < 2; i++ {
				sendCtx, sendCancel := context.WithTimeout(context.Background(), 8*time.Second)
				resultMid, sendErr := sender.SendCardInteractive(sendCtx, chatID, cardJSON)
				sendCancel()
				if sendErr == nil && resultMid != "" {
					finalSent = true
					relayCancel()
					logFinal("attempt=%d source=%s result_send_success mid=%s", attempt, source, resultMid)
					markOutbox(outboxID, true, nil)
					return
				}
				if sendErr == nil {
					sendErr = fmt.Errorf("feishu send result failed: empty message_id")
				}
				lastErr = sendErr
				if i == 0 {
					time.Sleep(200 * time.Millisecond)
				}
			}
			logFinal("attempt=%d source=%s result_send_failed err=%v", attempt, source, lastErr)
			_ = sendTextFallback(attempt, source, outboxID, reply, lastErr)
		}
		sendApprovalReply := func(source string, result channelsvc.ProcessResult) {
			reply := result.ReplyText
			if reply == "" {
				reply = "检测到待审批操作，请点击按钮确认。"
			}
			fallback := buildDaemonFeishuApprovalFallbackText(reply, result.PendingActions)

			finalMu.Lock()
			defer finalMu.Unlock()

			if finalSent {
				logFinal("skip source=%s reason=already_sent", source)
				return
			}
			finalAttempt++
			attempt := finalAttempt
			outboxID := finalOutbox

			progressMu.Lock()
			progressFinalized = true
			progressMu.Unlock()

			writeMu.Lock()
			progressCancel()
			progressMu.Lock()
			mid := progressCardMsgID
			lines := append([]string(nil), progressLines...)
			progressMu.Unlock()
			if mid != "" {
				collapseJSON := buildDaemonFeishuProgressCollapsedCardJSON(lines)
				ctx, cancel := context.WithTimeout(relayCtx, 5*time.Second)
				err := sender.PatchCard(ctx, mid, collapseJSON)
				cancel()
				if err != nil {
					logFinal("attempt=%d source=%s progress_collapse_failed mid=%s err=%v", attempt, source, mid, err)
				} else {
					logFinal("attempt=%d source=%s progress_collapse_success mid=%s", attempt, source, mid)
				}
			}
			writeMu.Unlock()

			cardJSON := buildDaemonFeishuApprovalCardJSON(reply, result.PendingActions)
			var lastErr error
			for i := 0; i < 2; i++ {
				sendCtx, sendCancel := context.WithTimeout(context.Background(), 8*time.Second)
				resultMid, sendErr := sender.SendCardInteractive(sendCtx, chatID, cardJSON)
				sendCancel()
				if sendErr == nil && resultMid != "" {
					finalSent = true
					relayCancel()
					logFinal("attempt=%d source=%s approval_send_success mid=%s", attempt, source, resultMid)
					markOutbox(outboxID, true, nil)
					return
				}
				if sendErr == nil {
					sendErr = fmt.Errorf("feishu send approval failed: empty message_id")
				}
				lastErr = sendErr
				if i == 0 {
					time.Sleep(200 * time.Millisecond)
				}
			}
			logFinal("attempt=%d source=%s approval_send_failed err=%v", attempt, source, lastErr)
			_ = sendTextFallback(attempt, source, outboxID, fallback, lastErr)
		}
		sendRealtimeApprovalCard := func(source, reply string, pending []channelsvc.PendingActionView) bool {
			if len(pending) == 0 {
				return true
			}
			reply = strings.TrimSpace(reply)
			if reply == "" {
				reply = "检测到待审批操作，请点击按钮确认。"
			}
			cardJSON := buildDaemonFeishuApprovalCardJSON(reply, pending)

			writeMu.Lock()
			sendCtx, sendCancel := context.WithTimeout(relayCtx, 8*time.Second)
			resultMid, sendErr := sender.SendCardInteractive(sendCtx, chatID, cardJSON)
			sendCancel()
			writeMu.Unlock()
			if sendErr == nil && resultMid != "" {
				logFinal("source=%s approval_realtime_send_success mid=%s", source, resultMid)
				return true
			}
			if sendErr == nil {
				sendErr = fmt.Errorf("feishu send realtime approval failed: empty message_id")
			}
			logFinal("source=%s approval_realtime_send_failed err=%v", source, sendErr)

			fallback := buildDaemonFeishuApprovalFallbackText(reply, pending)
			textCtx, textCancel := context.WithTimeout(relayCtx, 8*time.Second)
			textErr := sender.SendText(textCtx, chatID, fallback)
			textCancel()
			if textErr != nil {
				logFinal("source=%s approval_realtime_text_fallback_failed send_err=%v text_err=%v", source, sendErr, textErr)
			}
			return false
		}

		go func() {
			defer unsubscribe()
			defer progressCancel()

			idleTimeout := relayIdleTimeout
			var (
				idleTimer *time.Timer
				idleC     <-chan time.Time
			)
			approvalCardSent := map[uint]struct{}{}
			resetIdleTimer := func() {}
			if idleTimeout > 0 {
				idleTimer = time.NewTimer(idleTimeout)
				idleC = idleTimer.C
				resetIdleTimer = func() {
					if !idleTimer.Stop() {
						select {
						case <-idleTimer.C:
						default:
						}
					}
					idleTimer.Reset(idleTimeout)
				}
				defer idleTimer.Stop()
			}

			lastProgress := ""
			lastProgressAt := time.Time{}
			for {
				select {
				case <-relayCtx.Done():
					if relayCtx.Err() == context.DeadlineExceeded {
						sendFinalReply("timeout", "处理超时")
					}
					return
				case <-idleC:
					sendFinalReply("timeout", "处理超时")
					return
				case ev, ok := <-sub:
					if !ok {
						return
					}
					if ev.PeerMessageID != msgID {
						continue
					}
					resetIdleTimer()

					switch ev.Type {
					case "assistant_event":
						if ev.EventType == channelsvc.ToolApprovalEventType {
							payload, ok := channelsvc.ParseToolApprovalEventPayload(ev.Text)
							if !ok {
								continue
							}
							fresh := make([]channelsvc.PendingActionView, 0, len(payload.PendingActions))
							for _, item := range payload.PendingActions {
								if item.ID == 0 {
									continue
								}
								if _, sent := approvalCardSent[item.ID]; sent {
									continue
								}
								fresh = append(fresh, item)
							}
							if len(fresh) == 0 {
								continue
							}
							if sendRealtimeApprovalCard("relay_event", payload.Message, fresh) {
								for _, item := range fresh {
									approvalCardSent[item.ID] = struct{}{}
								}
							}
							continue
						}

						progress := buildDaemonFeishuStreamProgress(ev)
						if progress == "" {
							continue
						}
						now := time.Now()
						if progress == lastProgress {
							continue
						}
						if !lastProgressAt.IsZero() && now.Sub(lastProgressAt) < 1500*time.Millisecond {
							continue
						}
						lastProgress = progress
						lastProgressAt = now

						writeMu.Lock()
						progressMu.Lock()
						if progressFinalized {
							progressMu.Unlock()
							writeMu.Unlock()
							continue
						}
						progressLines = appendDaemonFeishuProgressLine(progressLines, progress, daemonFeishuProgressHistoryMax)
						curLines := append([]string(nil), progressLines...)
						mid := progressCardMsgID
						progressMu.Unlock()

						cardJSON := buildDaemonFeishuProgressCardJSON(curLines)
						if mid == "" {
							ctx, cancel := context.WithTimeout(progressCtx, 5*time.Second)
							newMid, err := sender.SendCardInteractive(ctx, chatID, cardJSON)
							cancel()
							if err == nil && newMid != "" {
								progressMu.Lock()
								if progressCardMsgID == "" {
									progressCardMsgID = newMid
								}
								progressMu.Unlock()
							}
						} else {
							ctx, cancel := context.WithTimeout(progressCtx, 5*time.Second)
							err := sender.PatchCard(ctx, mid, cardJSON)
							cancel()
							if err != nil {
								progressMu.Lock()
								if !progressFinalized && progressCardMsgID == mid {
									progressCardMsgID = ""
								}
								progressMu.Unlock()
							}
						}
						writeMu.Unlock()
					case "assistant_message", "error":
						reply := ev.Text
						if reply == "" {
							reply = ev.JobError
						}
						sendFinalReply("relay", reply)
						return
					}
				}
			}
		}()

		submitErr := gateway.Submit(reqCtx, channelsvc.GatewayInboundRequest{
			ProjectName:    boundProject,
			PeerProjectKey: chatID,
			Envelope: contracts.InboundEnvelope{
				Schema:             contracts.ChannelInboundSchemaV1,
				ChannelType:        contracts.ChannelTypeIM,
				Adapter:            opt.Adapter,
				PeerMessageID:      msgID,
				PeerConversationID: chatID,
				SenderID:           senderID,
				SenderName:         senderName,
				Text:               text,
				ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
			},
			Callback: func(result channelsvc.ProcessResult, runErr error) {
				reply := ""
				if runErr != nil {
					reply = "处理失败"
					if errMsg := runErr.Error(); errMsg != "" {
						reply = reply + "：" + errMsg
					}
				} else {
					reply = result.ReplyText
					if reply == "" {
						reply = result.JobError
					}
					if reply == "" {
						reply = "(empty reply)"
					}
				}

				finalMu.Lock()
				if result.OutboxID > 0 && finalOutbox == 0 {
					finalOutbox = result.OutboxID
				}
				outboxID := finalOutbox
				alreadySent := finalSent
				finalMu.Unlock()

				logFinal("callback job_status=%s outbox=%d run_err=%v", string(result.JobStatus), outboxID, runErr)
				if runErr == nil && !isGatewayTurnTerminalStatus(string(result.JobStatus)) {
					logFinal("callback non_terminal status=%s skip_final_reply", string(result.JobStatus))
					return
				}
				if runErr == nil && len(result.PendingActions) > 0 {
					sendApprovalReply("callback", result)
					return
				}
				if alreadySent {
					markOutbox(outboxID, true, nil)
					return
				}
				go sendFinalReply("callback", reply)
			},
		})
		if submitErr != nil {
			relayCancel()
			progressCancel()
			unsubscribe()

			msg := submitErr.Error()
			if submitErr == channelsvc.ErrInboundQueueFull {
				msg = "排队中，请稍后再试。"
			}
			logDaemonFeishuf(logger, "submit inbound failed: project=%s chat=%s err=%v", boundProject, chatID, submitErr)
			_ = sender.SendText(reqCtx, chatID, msg)
		}

		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
	}
}

func handleDaemonFeishuCardActionTrigger(ctx context.Context, w http.ResponseWriter, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender daemonFeishuMessageSender, opt daemonFeishuWebhookOptions, req daemonFeishuWebhookRequest, logger *slog.Logger) {
	eventType := strings.TrimSpace(req.Header.EventType)
	if eventType == "" {
		eventType = strings.TrimSpace(req.Event.Type)
	}
	eventID := strings.TrimSpace(req.Header.EventID)
	if eventID == "" {
		eventID = strings.TrimSpace(req.Event.EventID)
	}
	payload, err := parseDaemonFeishuCardActionPayload(req)
	if err != nil {
		logDaemonFeishuf(logger, "parse card action failed: type=%s event_id=%s err=%v", eventType, eventID, err)
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}
	logDaemonFeishuf(logger, "card action received: type=%s event_id=%s chat=%s msg=%s action=%d decision=%s",
		eventType, eventID, payload.ChatID, payload.MessageID, payload.PendingActionID, payload.Decision)

	boundProject, err := gateway.LookupBoundProject(ctx, contracts.ChannelTypeIM, opt.Adapter, payload.ChatID)
	if err != nil {
		logDaemonFeishuf(logger, "lookup bound project failed: chat=%s err=%v", payload.ChatID, err)
		_ = sender.SendText(ctx, payload.ChatID, "读取绑定失败，请稍后重试")
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}
	if boundProject == "" {
		_ = sender.SendText(ctx, payload.ChatID, buildDaemonFeishuUnboundHint(resolver))
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}

	projectCtx, err := resolver.Resolve(boundProject)
	if err != nil {
		logDaemonFeishuf(logger, "resolve project failed: project=%s err=%v", boundProject, err)
		_ = sender.SendText(ctx, payload.ChatID, "项目上下文不可用，请稍后重试")
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}
	decider, ok := projectCtx.Runtime.(channelsvc.ProjectRuntimePendingActionDecider)
	if !ok {
		logDaemonFeishuf(logger, "project runtime does not support pending action decision: project=%s", boundProject)
		_ = sender.SendText(ctx, payload.ChatID, "当前项目尚未启用审批能力")
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}

	decisionResult, err := decider.DecidePendingAction(ctx, channelsvc.PendingActionDecisionRequest{
		ChannelType:        contracts.ChannelTypeIM,
		Adapter:            opt.Adapter,
		PeerConversationID: payload.ChatID,
		PendingActionID:    payload.PendingActionID,
		Decision:           payload.Decision,
		Decider:            payload.DeciderID,
		Note:               payload.Note,
	})
	if err != nil {
		logDaemonFeishuf(logger, "decide pending action failed: project=%s chat=%s action=%d err=%v", boundProject, payload.ChatID, payload.PendingActionID, err)
		_ = sender.SendText(ctx, payload.ChatID, "审批处理失败："+err.Error())
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}
	logDaemonFeishuf(logger, "card action decided: project=%s chat=%s action=%d decision=%s status=%s",
		boundProject, payload.ChatID, payload.PendingActionID, payload.Decision, decisionResult.Action.Status)

	cardJSON := buildDaemonFeishuApprovalDecisionCardJSON(decisionResult)

	// Return updated card JSON in response body so Feishu replaces the
	// original card inline (buttons disappear, result shown immediately).
	toastType := "success"
	if decisionResult.Decision == channelsvc.PendingActionReject {
		toastType = "info"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"toast": map[string]any{
			"type":    toastType,
			"content": decisionResult.Message,
		},
		"card": map[string]any{
			"type": "raw",
			"data": json.RawMessage(cardJSON),
		},
	})
}

func parseDaemonFeishuCardActionPayload(req daemonFeishuWebhookRequest) (daemonFeishuCardActionPayload, error) {
	chatID := strings.TrimSpace(req.Event.Context.OpenChatID)
	if chatID == "" {
		chatID = strings.TrimSpace(req.Event.OpenChatID)
	}
	if chatID == "" {
		chatID = strings.TrimSpace(req.Event.Context.ChatID)
	}
	if chatID == "" {
		chatID = strings.TrimSpace(req.Event.Message.ChatID)
	}
	if chatID == "" {
		return daemonFeishuCardActionPayload{}, fmt.Errorf("card action 缺少 chat_id")
	}
	messageID := strings.TrimSpace(req.Event.Context.OpenMessageID)
	if messageID == "" {
		messageID = strings.TrimSpace(req.Event.OpenMessageID)
	}
	if messageID == "" {
		messageID = strings.TrimSpace(req.Event.Message.MessageID)
	}
	value := req.Event.Action.Value
	if len(value) == 0 {
		return daemonFeishuCardActionPayload{}, fmt.Errorf("card action 缺少 value")
	}
	actionID, ok := readDaemonFeishuMapUint(value, "pending_action_id", "action_id")
	if !ok || actionID == 0 {
		return daemonFeishuCardActionPayload{}, fmt.Errorf("card action 缺少 pending_action_id")
	}
	decisionRaw := strings.ToLower(readDaemonFeishuMapString(value, "decision"))
	decision := channelsvc.PendingActionDecision(decisionRaw)
	if decision != channelsvc.PendingActionApprove && decision != channelsvc.PendingActionReject {
		return daemonFeishuCardActionPayload{}, fmt.Errorf("card action decision 非法: %s", decisionRaw)
	}
	deciderID := strings.TrimSpace(req.Event.Operator.OperatorID.OpenID)
	if deciderID == "" {
		deciderID = strings.TrimSpace(req.Event.Operator.OpenID)
	}
	if deciderID == "" {
		deciderID = strings.TrimSpace(req.Event.Operator.OperatorID.UserID)
	}
	if deciderID == "" {
		deciderID = strings.TrimSpace(req.Event.Operator.UserID)
	}
	if deciderID == "" {
		deciderID = strings.TrimSpace(req.Event.Sender.SenderID.OpenID)
	}
	if deciderID == "" {
		deciderID = strings.TrimSpace(req.Event.Sender.SenderID.UserID)
	}
	if deciderID == "" {
		deciderID = "feishu.user"
	}
	deciderName := strings.TrimSpace(req.Event.Operator.Name)
	if deciderName == "" {
		deciderName = deciderID
	}
	note := strings.TrimSpace(readDaemonFeishuMapString(value, "note", "reason"))
	return daemonFeishuCardActionPayload{
		ChatID:          chatID,
		MessageID:       messageID,
		PendingActionID: actionID,
		Decision:        decision,
		DeciderID:       deciderID,
		DeciderName:     deciderName,
		Note:            note,
	}, nil
}

func readDaemonFeishuMapString(value map[string]any, keys ...string) string {
	if len(value) == 0 {
		return ""
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		raw, ok := value[key]
		if !ok {
			continue
		}
		switch x := raw.(type) {
		case string:
			if s := strings.TrimSpace(x); s != "" {
				return s
			}
		case fmt.Stringer:
			if s := strings.TrimSpace(x.String()); s != "" {
				return s
			}
		default:
			s := strings.TrimSpace(fmt.Sprint(raw))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func readDaemonFeishuMapUint(value map[string]any, keys ...string) (uint, bool) {
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		raw, ok := value[key]
		if !ok {
			continue
		}
		switch x := raw.(type) {
		case uint:
			if x > 0 {
				return x, true
			}
		case uint64:
			if x > 0 {
				return uint(x), true
			}
		case int:
			if x > 0 {
				return uint(x), true
			}
		case int64:
			if x > 0 {
				return uint(x), true
			}
		case float64:
			if x > 0 {
				return uint(x), true
			}
		case string:
			n, err := strconv.ParseUint(x, 10, 64)
			if err == nil && n > 0 {
				return uint(n), true
			}
		}
	}
	return 0, false
}

func tryHandleDaemonFeishuBindCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender daemonFeishuMessageSender, adapter, chatID, text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(trimmed), "/bind") {
		return false
	}
	fields := strings.Fields(trimmed)
	if len(fields) != 2 {
		_ = sender.SendText(ctx, chatID, "命令格式错误，请使用 /bind <项目名>")
		return true
	}
	projectName := fields[1]
	if projectName == "" {
		_ = sender.SendText(ctx, chatID, "命令格式错误，请使用 /bind <项目名>")
		return true
	}
	if resolver != nil {
		if _, err := resolver.Resolve(projectName); err != nil {
			_ = sender.SendText(ctx, chatID, "项目不存在："+projectName+"\n\n"+buildDaemonFeishuProjectList(resolver))
			return true
		}
	}
	prevProject, err := gateway.BindProject(ctx, contracts.ChannelTypeIM, adapter, chatID, projectName)
	if err != nil {
		_ = sender.SendText(ctx, chatID, "绑定失败，请稍后重试")
		return true
	}
	if prevProject == "" || prevProject == projectName {
		_ = sender.SendText(ctx, chatID, "已绑定到 project "+projectName)
		return true
	}
	_ = sender.SendText(ctx, chatID, "已切换到 "+projectName)
	return true
}

func tryHandleDaemonFeishuUnbindCommand(ctx context.Context, gateway *channelsvc.Gateway, sender daemonFeishuMessageSender, adapter, chatID, text string) bool {
	if strings.ToLower(strings.TrimSpace(text)) != "/unbind" {
		return false
	}
	removed, err := gateway.UnbindProject(ctx, contracts.ChannelTypeIM, adapter, chatID)
	if err != nil {
		_ = sender.SendText(ctx, chatID, "解绑失败，请稍后重试")
		return true
	}
	if removed {
		_ = sender.SendText(ctx, chatID, "已解绑")
	} else {
		_ = sender.SendText(ctx, chatID, "当前未绑定项目")
	}
	return true
}

func tryHandleDaemonFeishuInterruptCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender daemonFeishuMessageSender, adapter, chatID, text string) bool {
	normalized := strings.ToLower(text)
	if normalized != "/interrupt" && normalized != "/stop" {
		return false
	}
	projectName, interruptResult, err := gateway.InterruptBoundConversation(
		ctx,
		contracts.ChannelTypeIM,
		adapter,
		chatID,
		chatID,
	)
	if err != nil {
		_ = sender.SendText(ctx, chatID, "中断失败，请稍后重试")
		return true
	}
	if projectName == "" {
		_ = sender.SendText(ctx, chatID, buildDaemonFeishuUnboundHint(resolver))
		return true
	}
	switch interruptResult.Status {
	case channelsvc.InterruptStatusHit:
		_ = sender.SendText(ctx, chatID, "已发送中断信号")
		return true
	case channelsvc.InterruptStatusMiss:
		_ = sender.SendText(ctx, chatID, "当前没有可中断的会话")
		return true
	case channelsvc.InterruptStatusExecutionFailure:
		_ = sender.SendText(ctx, chatID, "中断执行失败，请稍后重试")
		return true
	default:
		_ = sender.SendText(ctx, chatID, "中断失败，请稍后重试")
		return true
	}
}

func tryHandleDaemonFeishuNewCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender daemonFeishuMessageSender, adapter, chatID, text string) bool {
	if strings.ToLower(text) != "/new" {
		return false
	}
	projectName, reset, err := gateway.ResetBoundConversationSession(
		ctx,
		contracts.ChannelTypeIM,
		adapter,
		chatID,
		chatID,
	)
	if err != nil {
		_ = sender.SendText(ctx, chatID, "重置会话失败，请稍后重试")
		return true
	}
	if projectName == "" {
		_ = sender.SendText(ctx, chatID, buildDaemonFeishuUnboundHint(resolver))
		return true
	}
	if reset {
		_ = sender.SendText(ctx, chatID, "已重置会话，下条消息将开启新 session")
		return true
	}
	_ = sender.SendText(ctx, chatID, "当前没有可重置的会话")
	return true
}

func buildDaemonFeishuUnboundHint(resolver channelsvc.ProjectResolver) string {
	return "本群尚未绑定项目。\n\n" + buildDaemonFeishuProjectList(resolver) + "\n\n请发送 /bind <项目名> 进行绑定。"
}

func buildDaemonFeishuProjectList(resolver channelsvc.ProjectResolver) string {
	projects := []string{}
	if resolver != nil {
		list, err := resolver.ListProjects()
		if err == nil {
			projects = append(projects, list...)
		}
	}
	for i := range projects {
		projects[i] = strings.TrimSpace(projects[i])
	}
	clean := make([]string, 0, len(projects))
	for _, p := range projects {
		if p == "" {
			continue
		}
		clean = append(clean, p)
	}
	sort.Strings(clean)
	if len(clean) == 0 {
		return "可用项目：\n  • （暂无项目）"
	}
	lines := []string{"可用项目："}
	for _, p := range clean {
		lines = append(lines, "  • "+p)
	}
	return strings.Join(lines, "\n")
}

func buildDaemonFeishuStreamProgress(ev channelsvc.GatewayEvent) string {
	if ev.Type != "assistant_event" {
		return ""
	}
	stream := ev.Stream
	eventType := ev.EventType
	text := ev.Text
	if text == "" {
		text = ev.JobError
	}

	switch stream {
	case "lifecycle":
		switch eventType {
		case "start":
			if text == "" {
				return "处理中：已开始执行"
			}
			return truncateDaemonFeishuRunes("处理中："+text, daemonFeishuStreamProgressMax)
		case "error":
			if text == "" {
				return "处理中：执行失败"
			}
			return truncateDaemonFeishuRunes("处理中：执行失败 - "+text, daemonFeishuStreamProgressMax)
		default:
			if text == "" {
				return ""
			}
			return truncateDaemonFeishuRunes("处理中："+text, daemonFeishuStreamProgressMax)
		}
	case "tool":
		if text == "" {
			return ""
		}
		return truncateDaemonFeishuRunes("处理中：工具 "+text, daemonFeishuStreamProgressMax)
	case "assistant":
		if text == "" {
			return ""
		}
		return truncateDaemonFeishuRunes("处理中："+text, daemonFeishuStreamProgressMax)
	case "error":
		if text == "" {
			return ""
		}
		return truncateDaemonFeishuRunes("处理中：错误 - "+text, daemonFeishuStreamProgressMax)
	default:
		if text == "" {
			return ""
		}
		return truncateDaemonFeishuRunes("处理中："+text, daemonFeishuStreamProgressMax)
	}
}

func appendDaemonFeishuProgressLine(lines []string, line string, maxLines int) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return lines
	}
	if maxLines <= 0 {
		maxLines = 1
	}
	lines = append(lines, line)
	if len(lines) > maxLines {
		lines = append([]string(nil), lines[len(lines)-maxLines:]...)
	}
	return lines
}

func buildDaemonFeishuProgressCardJSON(progressLines []string) string {
	var md strings.Builder
	for _, line := range progressLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		md.WriteString("- ")
		md.WriteString(line)
		md.WriteByte('\n')
	}
	content := md.String()
	if content == "" {
		content = "处理中..."
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			"update_multi":     true,
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": content,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func buildDaemonFeishuProgressCollapsedCardJSON(progressLines []string) string {
	var md strings.Builder
	for _, line := range progressLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		md.WriteString("- ")
		md.WriteString(line)
		md.WriteByte('\n')
	}
	progressContent := md.String()
	if progressContent == "" {
		progressContent = "（无）"
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			"update_multi":     true,
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":      "collapsible_panel",
					"expanded": false,
					"header": map[string]any{
						"title": map[string]any{
							"tag":     "plain_text",
							"content": "查看处理过程",
						},
					},
					"elements": []any{
						map[string]any{
							"tag":     "markdown",
							"content": progressContent,
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func buildDaemonFeishuResultCardJSON(replyMarkdown string) string {
	replyMarkdown = normalizeDaemonFeishuCardMarkdown(replyMarkdown)
	if replyMarkdown == "" {
		replyMarkdown = "(empty reply)"
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": replyMarkdown,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func buildDaemonFeishuApprovalFallbackText(reply string, pending []channelsvc.PendingActionView) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = "检测到待审批操作。"
	}
	lines := []string{reply, "", "待审批操作："}
	if len(pending) == 0 {
		lines = append(lines, "- （无）")
	} else {
		for _, item := range pending {
			lines = append(lines, fmt.Sprintf("- #%d %s", item.ID, formatDaemonFeishuPendingAction(item)))
		}
	}
	lines = append(lines, "", "请在飞书卡片中点击“批准/拒绝”。")
	return strings.Join(lines, "\n")
}

func buildDaemonFeishuApprovalCardJSON(reply string, pending []channelsvc.PendingActionView) string {
	reply = normalizeDaemonFeishuCardMarkdown(reply)
	if reply == "" {
		reply = "检测到待审批操作，请点击按钮确认。"
	}
	elements := make([]any, 0, len(pending)*2+1)
	elements = append(elements, map[string]any{
		"tag":     "markdown",
		"content": reply,
	})
	if len(pending) == 0 {
		elements = append(elements, map[string]any{
			"tag":     "markdown",
			"content": "- （无可审批操作）",
		})
	} else {
		for _, item := range pending {
			actionID := strconv.FormatUint(uint64(item.ID), 10)
			approveValue := map[string]any{
				"pending_action_id": actionID,
				"decision":          string(channelsvc.PendingActionApprove),
			}
			rejectValue := map[string]any{
				"pending_action_id": actionID,
				"decision":          string(channelsvc.PendingActionReject),
			}
			elements = append(elements, map[string]any{
				"tag":     "markdown",
				"content": fmt.Sprintf("- #%d `%s`", item.ID, normalizeDaemonFeishuCardMarkdown(formatDaemonFeishuPendingAction(item))),
			})
			elements = append(elements, map[string]any{
				"tag":  "button",
				"type": "primary",
				"text": map[string]any{
					"tag":     "plain_text",
					"content": "批准",
				},
				"value": approveValue,
				"behaviors": []any{
					map[string]any{
						"type":  "callback",
						"value": approveValue,
					},
				},
			})
			elements = append(elements, map[string]any{
				"tag":  "button",
				"type": "default",
				"text": map[string]any{
					"tag":     "plain_text",
					"content": "拒绝",
				},
				"value": rejectValue,
				"behaviors": []any{
					map[string]any{
						"type":  "callback",
						"value": rejectValue,
					},
				},
			})
		}
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			"update_multi":     true,
		},
		"body": map[string]any{
			"elements": elements,
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func buildDaemonFeishuApprovalDecisionCardJSON(result channelsvc.PendingActionDecisionResult) string {
	actionLabel := formatDaemonFeishuPendingAction(result.Action)
	status := string(result.Action.Status)
	if status == "" {
		status = "unknown"
	}
	message := normalizeDaemonFeishuCardMarkdown(result.Message)
	if message == "" {
		message = "审批处理完成。"
	}
	lines := []string{
		"**审批结果**",
		fmt.Sprintf("- 操作：`%s`", normalizeDaemonFeishuCardMarkdown(actionLabel)),
		fmt.Sprintf("- 状态：`%s`", status),
		message,
	}
	if note := result.Action.DecisionNote; note != "" {
		lines = append(lines, fmt.Sprintf("- 备注：%s", normalizeDaemonFeishuCardMarkdown(note)))
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			"update_multi":     true,
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": strings.Join(lines, "\n"),
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b)
}

func formatDaemonFeishuPendingAction(item channelsvc.PendingActionView) string {
	name := item.Action.Name
	if name == "" {
		name = "unknown_action"
	}
	args := item.Action.Args
	if len(args) == 0 {
		return name
	}
	parts := make([]string, 0, len(args))
	for k, v := range args {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", key, v))
	}
	if len(parts) == 0 {
		return name
	}
	sort.Strings(parts)
	return name + "(" + strings.Join(parts, ", ") + ")"
}

func parseDaemonFeishuMessageText(messageType, content string) (string, bool) {
	messageType = strings.ToLower(strings.TrimSpace(messageType))
	content = strings.TrimSpace(content)
	switch messageType {
	case "text":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(content), &payload); err == nil {
			return strings.TrimSpace(payload.Text), true
		}
		return content, true
	case "post":
		var obj map[string]any
		if err := json.Unmarshal([]byte(content), &obj); err != nil {
			return "", false
		}
		if txt, ok := obj["text"].(string); ok {
			return strings.TrimSpace(txt), true
		}
		return "", false
	default:
		return "", false
	}
}

func BuildWebhookPath(secretPath string) string {
	return buildDaemonFeishuWebhookPath(secretPath)
}

func buildDaemonFeishuWebhookPath(secretPath string) string {
	segment := normalizeDaemonFeishuWebhookSecretPath(secretPath)
	if segment == "" {
		return "/feishu/webhook"
	}
	return "/feishu/webhook/" + segment
}

func NormalizeWebhookSecretPath(raw string) string {
	return normalizeDaemonFeishuWebhookSecretPath(raw)
}

func normalizeDaemonFeishuWebhookSecretPath(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(s, "feishu/webhook/")
	s = strings.TrimPrefix(s, "/feishu/webhook/")
	s = strings.Trim(s, "/")
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, ch := range s {
		if ch >= 'a' && ch <= 'z' ||
			ch >= 'A' && ch <= 'Z' ||
			ch >= '0' && ch <= '9' ||
			ch == '-' || ch == '_' {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func daemonFeishuRepoBaseName(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(repoRoot))
	if base == "." || base == "" {
		return ""
	}
	return base
}

func (s *daemonFeishuHTTPSender) SendText(ctx context.Context, chatID, text string) error {
	if s == nil {
		return nil
	}
	chatID = strings.TrimSpace(chatID)
	text = strings.TrimSpace(text)
	if chatID == "" || text == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	_, err = s.sendMessage(ctx, chatID, "text", string(payload))
	if err != nil {
		s.logf("send text failed: chat=%s err=%v", chatID, err)
	}
	return err
}

func (s *daemonFeishuHTTPSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	if s == nil {
		return nil
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	title = sanitizeDaemonFeishuCardTitle(title)
	markdown = normalizeDaemonFeishuCardMarkdown(markdown)
	if markdown == "" {
		return nil
	}
	cardBody, err := json.Marshal(map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]string{
				"tag":     "plain_text",
				"content": title,
			},
		},
		"body": map[string]any{
			"elements": []map[string]any{
				{
					"tag":     "markdown",
					"content": markdown,
				},
			},
		},
	})
	if err != nil {
		return err
	}
	_, err = s.sendMessage(ctx, chatID, "interactive", string(cardBody))
	if err == nil {
		return nil
	}
	if !isDaemonFeishuCardContentError(err) {
		s.logf("send card failed: chat=%s err=%v", chatID, err)
		return err
	}

	degraded := degradeDaemonFeishuCardMarkdown(markdown)
	if degraded == "" || degraded == markdown {
		s.logf("send card failed without degradable markdown: chat=%s err=%v", chatID, err)
		return err
	}
	retryBody, marshalErr := json.Marshal(map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]string{
				"tag":     "plain_text",
				"content": title,
			},
		},
		"body": map[string]any{
			"elements": []map[string]any{
				{
					"tag":     "markdown",
					"content": degraded,
				},
			},
		},
	})
	if marshalErr != nil {
		return err
	}
	_, retryErr := s.sendMessage(ctx, chatID, "interactive", string(retryBody))
	if retryErr == nil {
		return nil
	}
	s.logf("send card degrade retry failed: chat=%s err=%v", chatID, retryErr)
	return retryErr
}

func (s *daemonFeishuHTTPSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	if s == nil {
		return "", nil
	}
	chatID = strings.TrimSpace(chatID)
	cardJSON = strings.TrimSpace(cardJSON)
	if chatID == "" || cardJSON == "" {
		return "", nil
	}
	return s.sendMessage(ctx, chatID, "interactive", cardJSON)
}

func (s *daemonFeishuHTTPSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	if s == nil {
		return nil
	}
	messageID = strings.TrimSpace(messageID)
	cardJSON = strings.TrimSpace(cardJSON)
	if messageID == "" || cardJSON == "" {
		return nil
	}
	token, err := s.getTenantToken(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"content": cardJSON})
	if err != nil {
		return err
	}
	u := strings.TrimRight(s.baseURL, "/") + "/open-apis/im/v1/messages/" + url.PathEscape(messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("feishu patch card failed: http=%d body=%s", resp.StatusCode, string(raw))
	}
	if readErr != nil {
		return fmt.Errorf("feishu patch card failed: read body: %w", readErr)
	}
	var ack struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return fmt.Errorf("feishu patch card failed: invalid json: %v body=%s", err, string(raw))
	}
	if ack.Code != 0 {
		return fmt.Errorf("feishu patch card failed: code=%d msg=%s", ack.Code, ack.Msg)
	}
	return nil
}

func (s *daemonFeishuHTTPSender) GetUserName(ctx context.Context, userID string) (string, error) {
	if s == nil {
		return "", nil
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", nil
	}
	if cachedName, ok := s.getCachedUserName(userID); ok {
		return cachedName, nil
	}
	token, err := s.getTenantToken(ctx)
	if err != nil {
		return "", err
	}

	u, err := url.Parse(strings.TrimRight(s.baseURL, "/") + "/open-apis/contact/v3/users/" + url.PathEscape(userID))
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("user_id_type", "open_id")
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("feishu get user failed: http=%d body=%s", resp.StatusCode, string(raw))
	}
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			User struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", fmt.Errorf("feishu get user failed: code=%d msg=%s", out.Code, out.Msg)
	}
	name := out.Data.User.Name
	if name == "" {
		return "", nil
	}
	cacheTTL := s.userNameTTL
	if cacheTTL <= 0 {
		cacheTTL = daemonFeishuUserNameCacheTTL
	}
	s.userNames.Store(userID, daemonFeishuUserNameCacheEntry{
		name:      name,
		expiresAt: time.Now().Add(cacheTTL),
	})
	return name, nil
}

func (s *daemonFeishuHTTPSender) getCachedUserName(userID string) (string, bool) {
	if s == nil {
		return "", false
	}
	raw, ok := s.userNames.Load(userID)
	if !ok {
		return "", false
	}
	entry, ok := raw.(daemonFeishuUserNameCacheEntry)
	if !ok {
		s.userNames.Delete(userID)
		return "", false
	}
	if entry.expiresAt.IsZero() || time.Now().After(entry.expiresAt) {
		s.userNames.Delete(userID)
		return "", false
	}
	name := entry.name
	if name == "" {
		s.userNames.Delete(userID)
		return "", false
	}
	return name, true
}

func (s *daemonFeishuHTTPSender) sendMessage(ctx context.Context, chatID, msgType, content string) (string, error) {
	if s == nil {
		return "", nil
	}
	chatID = strings.TrimSpace(chatID)
	msgType = strings.TrimSpace(msgType)
	content = strings.TrimSpace(content)
	if chatID == "" || msgType == "" || content == "" {
		return "", nil
	}
	token, err := s.getTenantToken(ctx)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"receive_id": chatID,
		"msg_type":   msgType,
		"content":    content,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	u, err := url.Parse(strings.TrimRight(s.baseURL, "/") + "/open-apis/im/v1/messages")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("receive_id_type", "chat_id")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("feishu send message failed: http=%d body=%s", resp.StatusCode, string(raw))
	}
	if readErr != nil {
		return "", fmt.Errorf("feishu send message failed: read body: %w", readErr)
	}
	var ack struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return "", fmt.Errorf("feishu send message failed: invalid json: %v body=%s", err, string(raw))
	}
	if ack.Code != 0 {
		return "", fmt.Errorf("feishu send message failed: code=%d msg=%s", ack.Code, ack.Msg)
	}
	return ack.Data.MessageID, nil
}

func (s *daemonFeishuHTTPSender) getTenantToken(ctx context.Context) (string, error) {
	if s == nil {
		return "", fmt.Errorf("sender 为空")
	}
	s.mu.Lock()
	if s.token != "" && time.Now().Before(s.tokenUntil) {
		tok := s.token
		s.mu.Unlock()
		return tok, nil
	}
	s.mu.Unlock()

	payload := map[string]string{
		"app_id":     s.appID,
		"app_secret": s.appSecret,
	}
	body, _ := json.Marshal(payload)
	u := strings.TrimRight(s.baseURL, "/") + "/open-apis/auth/v3/tenant_access_token/internal"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("feishu token failed: http=%d body=%s", resp.StatusCode, string(raw))
	}
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", fmt.Errorf("feishu token failed: code=%d msg=%s", out.Code, out.Msg)
	}
	token := out.TenantAccessToken
	if token == "" {
		return "", fmt.Errorf("feishu token empty")
	}
	expiresIn := out.Expire
	if expiresIn <= 0 {
		expiresIn = 7200
	}
	until := time.Now().Add(time.Duration(expiresIn-60) * time.Second)

	s.mu.Lock()
	s.token = token
	s.tokenUntil = until
	s.mu.Unlock()
	return token, nil
}

func sanitizeDaemonFeishuCardTitle(title string) string {
	title = strings.ReplaceAll(strings.ReplaceAll(title, "\r", " "), "\n", " ")
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		title = daemonFeishuCardDefaultTitle
	}
	return truncateDaemonFeishuRunes(title, daemonFeishuCardTitleMaxRunes)
}

func normalizeDaemonFeishuCardMarkdown(markdown string) string {
	md := strings.ReplaceAll(markdown, "\r\n", "\n")
	md = strings.ReplaceAll(md, "\r", "\n")
	md = strings.TrimSpace(md)
	if md == "" {
		return ""
	}

	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines)+1)
	inFence := false
	for _, line := range lines {
		trimRight := strings.TrimRight(line, " \t")
		leftTrim := strings.TrimLeft(trimRight, " \t")
		prefix := trimRight[:len(trimRight)-len(leftTrim)]
		if strings.HasPrefix(leftTrim, "~~~") {
			leftTrim = "```" + strings.TrimPrefix(leftTrim, "~~~")
			trimRight = prefix + leftTrim
		}
		if strings.HasPrefix(leftTrim, "```") {
			lang := strings.TrimPrefix(leftTrim, "```")
			lang = strings.TrimPrefix(lang, "{")
			lang = strings.TrimSuffix(lang, "}")
			trimRight = prefix + "```"
			if lang != "" {
				trimRight += lang
			}
			inFence = !inFence
		}
		out = append(out, trimRight)
	}
	if inFence {
		out = append(out, "```")
	}
	normalized := strings.Join(out, "\n")
	if normalized == "" {
		return ""
	}
	if utf8.RuneCountInString(normalized) > daemonFeishuCardMarkdownMaxRune {
		normalized = truncateDaemonFeishuRunes(normalized, daemonFeishuCardMarkdownMaxRune) + "\n\n...(内容过长，已截断)"
	}
	return normalized
}

func degradeDaemonFeishuCardMarkdown(markdown string) string {
	normalized := normalizeDaemonFeishuCardMarkdown(markdown)
	if normalized == "" {
		return ""
	}
	lines := strings.Split(normalized, "\n")
	for i := range lines {
		if strings.Contains(lines[i], "|") {
			lines[i] = strings.ReplaceAll(lines[i], "|", " / ")
		}
	}
	return strings.Join(lines, "\n")
}

func isDaemonFeishuCardContentError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "code\":230099") ||
		strings.Contains(msg, "code=230099") ||
		strings.Contains(msg, "failed to create card content") ||
		strings.Contains(msg, "table number over limit") ||
		strings.Contains(msg, "errcode: 11310")
}

func truncateDaemonFeishuRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	if w == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal_error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func isGatewayTurnTerminalStatus(status string) bool {
	status = strings.ToLower(status)
	if status == "" {
		return true
	}
	return status == "succeeded" || status == "failed"
}

func (s *daemonFeishuHTTPSender) logf(format string, args ...any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Info(fmt.Sprintf(format, args...))
}

func logDaemonFeishuf(logger *slog.Logger, format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Info(fmt.Sprintf(format, args...))
}
