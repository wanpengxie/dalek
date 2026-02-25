package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
)

type gatewayFeishuHandlerOptions struct {
	Adapter           string
	VerifyToken       string
	LogWriter         io.Writer // 日志输出；nil 时退化为 os.Stderr
	EventDeduplicator *channelsvc.EventDeduplicator
}

type feishuMessageSender interface {
	SendText(ctx context.Context, chatID, text string) error
	SendCard(ctx context.Context, chatID, title, markdown string) error
	SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error)
	PatchCard(ctx context.Context, messageID, cardJSON string) error
	GetUserName(ctx context.Context, userID string) (string, error)
}

type noopFeishuSender struct{}

func (s *noopFeishuSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	_ = chatID
	_ = text
	return nil
}

func (s *noopFeishuSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	_ = chatID
	_ = title
	_ = markdown
	return nil
}

func (s *noopFeishuSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	_ = chatID
	_ = cardJSON
	return "", nil
}

func (s *noopFeishuSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	_ = ctx
	_ = messageID
	_ = cardJSON
	return nil
}

func (s *noopFeishuSender) GetUserName(ctx context.Context, userID string) (string, error) {
	_ = ctx
	_ = userID
	return "", nil
}

type feishuUserNameCacheEntry struct {
	name      string
	expiresAt time.Time
}

type feishuHTTPSender struct {
	client    *http.Client
	baseURL   string
	appID     string
	appSecret string

	mu         sync.Mutex
	token      string
	tokenUntil time.Time

	userNameTTL time.Duration
	userNames   sync.Map
}

type feishuWebhookRequest struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`

	Header struct {
		EventType string `json:"event_type"`
		Token     string `json:"token"`
		EventID   string `json:"event_id"`
	} `json:"header"`

	Event struct {
		Sender struct {
			SenderID struct {
				OpenID string `json:"open_id"`
				UserID string `json:"user_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"message"`
	} `json:"event"`
}

type feishuSenderConfig struct {
	AppID     string
	AppSecret string
	BaseURL   string
}

const (
	feishuCardDefaultTitle    = "dalek"
	feishuCardTitleMaxRunes   = 64
	feishuCardMarkdownMaxRune = 28000
	feishuStreamProgressMax   = 240
	feishuProgressHistoryMax  = 20
	feishuUserNameCacheTTL    = 10 * time.Minute
	feishuEventDedupTTL       = 5 * time.Minute
	feishuEventDedupCap       = 10000
)

func newFeishuSenderFromEnv() feishuMessageSender {
	return newFeishuSender(feishuSenderConfig{
		AppID:     os.Getenv("FEISHU_APP_ID"),
		AppSecret: os.Getenv("FEISHU_APP_SECRET"),
		BaseURL:   os.Getenv("DALEK_FEISHU_BASE_URL"),
	})
}

func newFeishuSender(raw feishuSenderConfig) feishuMessageSender {
	appID := strings.TrimSpace(raw.AppID)
	appSecret := strings.TrimSpace(raw.AppSecret)
	if appID == "" || appSecret == "" {
		return &noopFeishuSender{}
	}
	baseURL := strings.TrimSpace(raw.BaseURL)
	if baseURL == "" {
		baseURL = "https://open.feishu.cn"
	}
	return &feishuHTTPSender{
		client:      &http.Client{Timeout: 10 * time.Second},
		baseURL:     strings.TrimRight(baseURL, "/"),
		appID:       appID,
		appSecret:   appSecret,
		userNameTTL: feishuUserNameCacheTTL,
	}
}

func newGatewayFeishuWebhookHandler(gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender feishuMessageSender, rawOpt gatewayFeishuHandlerOptions) http.HandlerFunc {
	opt := rawOpt
	if strings.TrimSpace(opt.Adapter) == "" {
		opt.Adapter = "im.feishu"
	}
	if sender == nil {
		sender = &noopFeishuSender{}
	}
	logOut := opt.LogWriter
	if logOut == nil {
		logOut = os.Stderr
	}
	dedup := opt.EventDeduplicator
	if dedup == nil {
		dedup = channelsvc.NewEventDeduplicator(feishuEventDedupTTL, feishuEventDedupCap)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}
		var req feishuWebhookRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		if strings.EqualFold(strings.TrimSpace(req.Type), "url_verification") {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"challenge": strings.TrimSpace(req.Challenge)})
			return
		}

		if strings.TrimSpace(opt.VerifyToken) != "" {
			if strings.TrimSpace(req.Header.Token) != strings.TrimSpace(opt.VerifyToken) {
				writeGatewayJSON(w, http.StatusUnauthorized, map[string]any{"code": 1, "msg": "invalid token"})
				return
			}
		}

		if strings.TrimSpace(req.Header.EventType) != "im.message.receive_v1" {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}

		if strings.EqualFold(strings.TrimSpace(req.Event.Sender.SenderType), "app") {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		eventID := strings.TrimSpace(req.Header.EventID)
		if eventID != "" && dedup.IsDuplicate(eventID) {
			fmtGatewayLog(logOut, "gateway feishu dedup: dedup_type=event_id dedup_key=%s peer_msg=%s action=skip",
				eventID, strings.TrimSpace(req.Event.Message.MessageID))
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}

		chatID := strings.TrimSpace(req.Event.Message.ChatID)
		msgID := strings.TrimSpace(req.Event.Message.MessageID)
		if chatID == "" || msgID == "" {
			writeGatewayJSON(w, http.StatusBadRequest, map[string]any{"code": 1, "msg": "missing chat/message id"})
			return
		}

		text, supported := parseFeishuMessageText(strings.TrimSpace(req.Event.Message.MessageType), strings.TrimSpace(req.Event.Message.Content))
		if !supported {
			_ = sender.SendText(context.Background(), chatID, "暂不支持此类消息")
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		text = strings.TrimSpace(text)
		if text == "" {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}

		senderID := strings.TrimSpace(req.Event.Sender.SenderID.OpenID)
		if senderID == "" {
			senderID = strings.TrimSpace(req.Event.Sender.SenderID.UserID)
		}
		senderName := ""
		if senderID != "" {
			if resolvedName, nameErr := sender.GetUserName(context.Background(), senderID); nameErr != nil {
				fmtGatewayLog(logOut, "[feishu] GetUserName failed for %s: %v", senderID, nameErr)
			} else {
				senderName = strings.TrimSpace(resolvedName)
			}
		}
		if senderID == "" {
			senderID = "feishu.user"
		}

		if handled := tryHandleFeishuBindCommand(context.Background(), gateway, resolver, sender, opt.Adapter, chatID, text); handled {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if handled := tryHandleFeishuUnbindCommand(context.Background(), gateway, sender, opt.Adapter, chatID, text); handled {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if handled := tryHandleFeishuNewCommand(context.Background(), gateway, resolver, sender, opt.Adapter, chatID, text); handled {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if handled := tryHandleFeishuInterruptCommand(context.Background(), gateway, resolver, sender, opt.Adapter, chatID, text); handled {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}

		boundProject, err := gateway.LookupBoundProject(context.Background(), contracts.ChannelTypeIM, opt.Adapter, chatID)
		if err != nil {
			_ = sender.SendText(context.Background(), chatID, "读取绑定失败，请稍后重试")
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		if strings.TrimSpace(boundProject) == "" {
			_ = sender.SendText(context.Background(), chatID, buildFeishuUnboundHint(resolver))
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
			return
		}
		fmtGatewayLog(logOut, "gateway feishu inbound: chat=%s peer_msg=%s project=%s sender=%s sender_name=%s text_len=%d",
			chatID, msgID, boundProject, senderID, senderName, len(text))

		sub, unsubscribe := gateway.EventBus().Subscribe(boundProject, chatID, 256)
		relayCtx, relayCancel := context.WithCancel(context.Background())
		progressCtx, progressCancel := context.WithCancel(context.Background())

		var (
			progressMu        sync.Mutex
			progressCardMsgID string
			progressLines     []string
			progressFinalized bool
		)
		// 单写通道：所有飞书写操作（Send/Patch）必须串行，避免进度与终态并发写导致覆盖竞态。
		var writeMu sync.Mutex

		logFinal := func(format string, args ...any) {
			prefix := fmt.Sprintf("gateway feishu final-reply: chat=%s peer_msg=%s ", strings.TrimSpace(chatID), strings.TrimSpace(msgID))
			fmtGatewayLog(logOut, "%s", prefix+fmt.Sprintf(format, args...))
		}
		markOutbox := func(outboxID uint, delivered bool, cause error) {
			if gateway == nil || outboxID == 0 {
				return
			}
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
				logFinal("attempt=%d source=%s text_fallback_success", attempt, strings.TrimSpace(source))
				markOutbox(outboxID, true, nil)
				return true
			}
			if prevErr != nil {
				logFinal("attempt=%d source=%s text_fallback_failed err=%v; text_err=%v", attempt, strings.TrimSpace(source), prevErr, textErr)
				markOutbox(outboxID, false, fmt.Errorf("%w; text fallback failed: %v", prevErr, textErr))
				return false
			}
			logFinal("attempt=%d source=%s text_fallback_failed err=%v", attempt, strings.TrimSpace(source), textErr)
			markOutbox(outboxID, false, textErr)
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
				logFinal("skip source=%s reason=already_sent", strings.TrimSpace(source))
				return
			}
			finalAttempt++
			attempt := finalAttempt
			outboxID := finalOutbox

			progressMu.Lock()
			progressFinalized = true
			progressMu.Unlock()
			// 终态开始：停止产生新进度，并等待在途进度写入完成，再收起进度卡片。
			// 注意：不能在 writeMu.Lock 之前调用 progressCancel()！
			// 否则 relay 协程的在途 PatchCard 会因 context.Canceled 提前返回并释放 writeMu，
			// 但 HTTP 请求可能已到达飞书服务器仍在处理中。此时 sendFinalReply 立即 patch 收起进度卡，
			// 导致两个 PatchCard 请求在飞书服务端并发处理，进度 patch 可能覆盖“收起”结果。
			writeMu.Lock()
			// 现在已持有 writeMu，不会有在途的进度 PatchCard，安全取消 progressCtx。
			progressCancel()
			progressMu.Lock()
			mid := strings.TrimSpace(progressCardMsgID)
			lines := append([]string(nil), progressLines...)
			progressMu.Unlock()
			if mid != "" {
				collapseJSON := buildFeishuProgressCollapsedCardJSON(lines)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := sender.PatchCard(ctx, mid, collapseJSON)
				cancel()
				if err != nil {
					logFinal("attempt=%d source=%s progress_collapse_failed mid=%s err=%v", attempt, strings.TrimSpace(source), mid, err)
				} else {
					logFinal("attempt=%d source=%s progress_collapse_success mid=%s", attempt, strings.TrimSpace(source), mid)
				}
			}
			writeMu.Unlock()

			logFinal("attempt=%d source=%s outbox=%d has_progress_card=%v reply_len=%d", attempt, strings.TrimSpace(source), outboxID, mid != "", utf8.RuneCountInString(reply))

			cardJSON := buildFeishuResultCardJSON(reply)
			var lastErr error
			for i := 0; i < 2; i++ {
				sendCtx, sendCancel := context.WithTimeout(context.Background(), 8*time.Second)
				resultMid, sendErr := sender.SendCardInteractive(sendCtx, chatID, cardJSON)
				sendCancel()
				if sendErr == nil && strings.TrimSpace(resultMid) != "" {
					finalSent = true
					relayCancel()
					logFinal("attempt=%d source=%s result_send_success mid=%s", attempt, strings.TrimSpace(source), strings.TrimSpace(resultMid))
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
			logFinal("attempt=%d source=%s result_send_failed err=%v", attempt, strings.TrimSpace(source), lastErr)
			_ = sendTextFallback(attempt, source, outboxID, reply, lastErr)
		}

		go func() {
			defer unsubscribe()
			defer progressCancel()
			lastProgress := ""
			lastProgressAt := time.Time{}
			for {
				select {
				case <-relayCtx.Done():
					return
				case ev, ok := <-sub:
					if !ok {
						return
					}
					if strings.TrimSpace(ev.PeerMessageID) != msgID {
						continue
					}
					switch strings.TrimSpace(ev.Type) {
					case "assistant_event":
						progress := buildFeishuStreamProgress(ev)
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
						progressLines = appendFeishuProgressLine(progressLines, progress, feishuProgressHistoryMax)
						curLines := append([]string(nil), progressLines...)
						mid := progressCardMsgID
						progressMu.Unlock()

						cardJSON := buildFeishuProgressCardJSON(curLines)

						if mid == "" {
							ctx, cancel := context.WithTimeout(progressCtx, 5*time.Second)
							newMid, err := sender.SendCardInteractive(ctx, chatID, cardJSON)
							cancel()
							if err == nil && strings.TrimSpace(newMid) != "" {
								progressMu.Lock()
								if progressCardMsgID == "" {
									progressCardMsgID = strings.TrimSpace(newMid)
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
									// 当前进度卡已不可用，后续进度重新发新卡。
									progressCardMsgID = ""
								}
								progressMu.Unlock()
							}
						}
						writeMu.Unlock()
					case "assistant_message", "error":
						reply := strings.TrimSpace(ev.Text)
						if reply == "" {
							reply = strings.TrimSpace(ev.JobError)
						}
						sendFinalReply("relay", reply)
						return
					}
				}
			}
		}()

		err = gateway.Submit(context.Background(), channelsvc.GatewayInboundRequest{
			ProjectName:    boundProject,
			PeerProjectKey: chatID,
			Envelope: contracts.InboundEnvelope{
				Schema:             contracts.ChannelInboundSchemaV1,
				ChannelType:        contracts.ChannelTypeIM,
				Adapter:            opt.Adapter,
				PeerConversationID: chatID,
				PeerMessageID:      msgID,
				SenderID:           senderID,
				SenderName:         senderName,
				Text:               text,
				ReceivedAt:         time.Now().Format(time.RFC3339),
			},
			Callback: func(res channelsvc.ProcessResult, runErr error) {
				reply := ""
				if runErr != nil {
					reply = fmt.Sprintf("处理失败: %s", strings.TrimSpace(runErr.Error()))
				} else {
					reply = strings.TrimSpace(res.ReplyText)
					if reply == "" {
						reply = strings.TrimSpace(res.JobError)
					}
					if reply == "" {
						reply = "(empty reply)"
					}
				}
				finalMu.Lock()
				if res.OutboxID > 0 && finalOutbox == 0 {
					finalOutbox = res.OutboxID
				}
				outboxID := finalOutbox
				alreadySent := finalSent
				finalMu.Unlock()

				logFinal("callback job_status=%s outbox=%d run_err=%v", strings.TrimSpace(string(res.JobStatus)), outboxID, runErr)
				if runErr == nil && !isGatewayTurnTerminalStatus(strings.TrimSpace(string(res.JobStatus))) {
					logFinal("callback non_terminal status=%s skip_final_reply", strings.TrimSpace(string(res.JobStatus)))
					return
				}
				if alreadySent {
					markOutbox(outboxID, true, nil)
					return
				}
				go sendFinalReply("callback", reply)
			},
		})
		if err != nil {
			relayCancel()
			progressCancel()
			unsubscribe()
			msg := strings.TrimSpace(err.Error())
			if err == channelsvc.ErrInboundQueueFull {
				msg = "排队中，请稍后再试。"
			}
			fmtGatewayLog(logOut, "gateway feishu submit-error: chat=%s peer_msg=%s err=%v", chatID, msgID, err)
			_ = sender.SendText(context.Background(), chatID, msg)
		}

		writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0})
	}
}

func tryHandleFeishuBindCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender feishuMessageSender, adapter, chatID, text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(trimmed), "/bind") {
		return false
	}
	fields := strings.Fields(trimmed)
	if len(fields) != 2 {
		_ = sender.SendText(ctx, chatID, "命令格式错误，请使用 /bind <项目名>")
		return true
	}
	projectName := strings.TrimSpace(fields[1])
	if projectName == "" {
		_ = sender.SendText(ctx, chatID, "命令格式错误，请使用 /bind <项目名>")
		return true
	}
	if resolver != nil {
		if _, err := resolver.Resolve(projectName); err != nil {
			_ = sender.SendText(ctx, chatID, "项目不存在："+projectName+"\n\n"+buildFeishuProjectList(resolver))
			return true
		}
	}
	prevProject, err := gateway.BindProject(ctx, contracts.ChannelTypeIM, strings.TrimSpace(adapter), strings.TrimSpace(chatID), projectName)
	if err != nil {
		_ = sender.SendText(ctx, chatID, "绑定失败，请稍后重试")
		return true
	}
	prevProject = strings.TrimSpace(prevProject)
	if prevProject == "" || prevProject == projectName {
		_ = sender.SendText(ctx, chatID, "已绑定到 project "+projectName)
		return true
	}
	_ = sender.SendText(ctx, chatID, "已切换到 "+projectName)
	return true
}

func tryHandleFeishuUnbindCommand(ctx context.Context, gateway *channelsvc.Gateway, sender feishuMessageSender, adapter, chatID, text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.TrimSpace(strings.ToLower(trimmed)) != "/unbind" {
		return false
	}
	removed, err := gateway.UnbindProject(ctx, contracts.ChannelTypeIM, strings.TrimSpace(adapter), strings.TrimSpace(chatID))
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

func tryHandleFeishuInterruptCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender feishuMessageSender, adapter, chatID, text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.TrimSpace(strings.ToLower(trimmed)) != "/stop" {
		return false
	}
	projectName, interruptResult, err := gateway.InterruptBoundConversation(
		ctx,
		contracts.ChannelTypeIM,
		strings.TrimSpace(adapter),
		strings.TrimSpace(chatID),
		strings.TrimSpace(chatID),
	)
	if err != nil {
		_ = sender.SendText(ctx, chatID, "中断失败，请稍后重试")
		return true
	}
	if strings.TrimSpace(projectName) == "" {
		_ = sender.SendText(ctx, chatID, buildFeishuUnboundHint(resolver))
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

func tryHandleFeishuNewCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender feishuMessageSender, adapter, chatID, text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.TrimSpace(strings.ToLower(trimmed)) != "/new" {
		return false
	}
	projectName, reset, err := gateway.ResetBoundConversationSession(
		ctx,
		contracts.ChannelTypeIM,
		strings.TrimSpace(adapter),
		strings.TrimSpace(chatID),
		strings.TrimSpace(chatID),
	)
	if err != nil {
		_ = sender.SendText(ctx, chatID, "重置会话失败，请稍后重试")
		return true
	}
	if strings.TrimSpace(projectName) == "" {
		_ = sender.SendText(ctx, chatID, buildFeishuUnboundHint(resolver))
		return true
	}
	if reset {
		_ = sender.SendText(ctx, chatID, "已重置会话，下条消息将开启新 session")
		return true
	}
	_ = sender.SendText(ctx, chatID, "当前没有可重置的会话")
	return true
}

func buildFeishuUnboundHint(resolver channelsvc.ProjectResolver) string {
	list := buildFeishuProjectList(resolver)
	return "本群尚未绑定项目。\n\n" + list + "\n\n请发送 /bind <项目名> 进行绑定。"
}

func buildFeishuProjectList(resolver channelsvc.ProjectResolver) string {
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

func buildFeishuStreamProgress(ev channelsvc.GatewayEvent) string {
	if strings.TrimSpace(ev.Type) != "assistant_event" {
		return ""
	}
	stream := strings.TrimSpace(ev.Stream)
	eventType := strings.TrimSpace(ev.EventType)
	text := strings.TrimSpace(ev.Text)
	if text == "" {
		text = strings.TrimSpace(ev.JobError)
	}

	switch stream {
	case "lifecycle":
		switch eventType {
		case "start":
			if text == "" {
				return "处理中：已开始执行"
			}
			return truncateRunes("处理中："+text, feishuStreamProgressMax)
		case "error":
			if text == "" {
				return "处理中：执行失败"
			}
			return truncateRunes("处理中：执行失败 - "+text, feishuStreamProgressMax)
		default:
			if text == "" {
				return ""
			}
			return truncateRunes("处理中："+text, feishuStreamProgressMax)
		}
	case "tool":
		if text == "" {
			return ""
		}
		return truncateRunes("处理中：工具 "+text, feishuStreamProgressMax)
	case "assistant":
		if text == "" {
			return ""
		}
		return truncateRunes("处理中："+text, feishuStreamProgressMax)
	case "error":
		if text == "" {
			return ""
		}
		return truncateRunes("处理中：错误 - "+text, feishuStreamProgressMax)
	default:
		if text == "" {
			return ""
		}
		return truncateRunes("处理中："+text, feishuStreamProgressMax)
	}
}

func appendFeishuProgressLine(lines []string, line string, maxLines int) []string {
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

func buildFeishuProgressCardJSON(progressLines []string) string {
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
	content := strings.TrimSpace(md.String())
	if content == "" {
		content = "处理中..."
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			// 作为可被 PATCH 更新的共享卡片，需显式声明 update_multi=true。
			"update_multi": true,
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

func buildFeishuProgressCollapsedCardJSON(progressLines []string) string {
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
	progressContent := strings.TrimSpace(md.String())
	if progressContent == "" {
		progressContent = "（无）"
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			// 作为可被 PATCH 更新的共享卡片，需显式声明 update_multi=true。
			"update_multi": true,
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

func buildFeishuResultCardJSON(replyMarkdown string) string {
	replyMarkdown = normalizeFeishuCardMarkdown(replyMarkdown)
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

func parseFeishuMessageText(messageType, content string) (string, bool) {
	messageType = strings.ToLower(strings.TrimSpace(messageType))
	switch messageType {
	case "text":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(content), &payload); err == nil {
			return strings.TrimSpace(payload.Text), true
		}
		return strings.TrimSpace(content), true
	case "post":
		// 富文本尽量提取 text 字段，提取失败按不支持处理。
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

func writeGatewayJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *feishuHTTPSender) SendText(ctx context.Context, chatID, text string) error {
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
	return err
}

func (s *feishuHTTPSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	if s == nil {
		return nil
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	title = sanitizeFeishuCardTitle(title)
	markdown = normalizeFeishuCardMarkdown(markdown)
	if markdown == "" {
		return nil
	}
	cardBody, err := json.Marshal(map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
			// 作为可被 PATCH 更新的共享卡片，需显式声明 update_multi=true。
			"update_multi": true,
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
	return err
}

func (s *feishuHTTPSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
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

func (s *feishuHTTPSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
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
		return fmt.Errorf("feishu patch card failed: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if readErr != nil {
		return fmt.Errorf("feishu patch card failed: read body: %w", readErr)
	}
	var ack struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return fmt.Errorf("feishu patch card failed: invalid json: %v body=%s", err, strings.TrimSpace(string(raw)))
	}
	if ack.Code != 0 {
		return fmt.Errorf("feishu patch card failed: code=%d msg=%s", ack.Code, strings.TrimSpace(ack.Msg))
	}
	return nil
}

func (s *feishuHTTPSender) GetUserName(ctx context.Context, userID string) (string, error) {
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
		return "", fmt.Errorf("feishu get user failed: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	log.Printf("[feishu] GetUserName raw response for %s: %s", userID, string(raw))
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
		return "", fmt.Errorf("feishu get user failed: code=%d msg=%s", out.Code, strings.TrimSpace(out.Msg))
	}
	name := strings.TrimSpace(out.Data.User.Name)
	if name == "" {
		log.Printf("[feishu] GetUserName for %s returned code=0 but name is empty", userID)
		return "", nil
	}
	cacheTTL := s.userNameTTL
	if cacheTTL <= 0 {
		cacheTTL = feishuUserNameCacheTTL
	}
	s.userNames.Store(userID, feishuUserNameCacheEntry{
		name:      name,
		expiresAt: time.Now().Add(cacheTTL),
	})
	return name, nil
}

func (s *feishuHTTPSender) getCachedUserName(userID string) (string, bool) {
	if s == nil {
		return "", false
	}
	raw, ok := s.userNames.Load(userID)
	if !ok {
		return "", false
	}
	entry, ok := raw.(feishuUserNameCacheEntry)
	if !ok {
		s.userNames.Delete(userID)
		return "", false
	}
	if entry.expiresAt.IsZero() || time.Now().After(entry.expiresAt) {
		s.userNames.Delete(userID)
		return "", false
	}
	name := strings.TrimSpace(entry.name)
	if name == "" {
		s.userNames.Delete(userID)
		return "", false
	}
	return name, true
}

func (s *feishuHTTPSender) sendMessage(ctx context.Context, chatID, msgType, content string) (string, error) {
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
		return "", fmt.Errorf("feishu send message failed: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
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
		return "", fmt.Errorf("feishu send message failed: invalid json: %v body=%s", err, strings.TrimSpace(string(raw)))
	}
	if ack.Code != 0 {
		return "", fmt.Errorf("feishu send message failed: code=%d msg=%s", ack.Code, strings.TrimSpace(ack.Msg))
	}
	return strings.TrimSpace(ack.Data.MessageID), nil
}

func buildFeishuAgentReplyCardTitle(projectName string) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "dalek 回复"
	}
	return projectName
}

func buildFeishuGatewaySendCardTitle(projectName string) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "dalek 通知"
	}
	return projectName
}

func resolveFeishuCardProjectName(projectName string, resolver channelsvc.ProjectResolver) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return ""
	}
	if resolver != nil {
		if project, err := resolver.Resolve(projectName); err == nil && project != nil {
			if base := feishuRepoBaseName(project.RepoRoot); base != "" {
				return base
			}
		}
	}
	return projectName
}

func feishuRepoBaseName(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return ""
	}
	base := strings.TrimSpace(filepath.Base(filepath.Clean(repoRoot)))
	if base == "." || base == "" {
		return ""
	}
	return base
}

func sanitizeFeishuCardTitle(title string) string {
	title = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(title, "\r", " "), "\n", " "))
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		title = feishuCardDefaultTitle
	}
	return truncateRunes(title, feishuCardTitleMaxRunes)
}

func normalizeFeishuCardMarkdown(markdown string) string {
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
			lang := strings.TrimSpace(strings.TrimPrefix(leftTrim, "```"))
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
	normalized := strings.TrimSpace(strings.Join(out, "\n"))
	if normalized == "" {
		return ""
	}
	if utf8.RuneCountInString(normalized) > feishuCardMarkdownMaxRune {
		normalized = truncateRunes(normalized, feishuCardMarkdownMaxRune) + "\n\n...(内容过长，已截断)"
	}
	return normalized
}

func degradeFeishuCardMarkdown(markdown string) string {
	normalized := normalizeFeishuCardMarkdown(markdown)
	if normalized == "" {
		return ""
	}
	lines := strings.Split(normalized, "\n")
	for i := range lines {
		if strings.Contains(lines[i], "|") {
			// 飞书会把带竖线的内容当作表格解析，超限时直接拒绝卡片；这里降级成普通文本。
			lines[i] = strings.ReplaceAll(lines[i], "|", " / ")
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isFeishuCardContentError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "code\":230099") ||
		strings.Contains(msg, "code=230099") ||
		strings.Contains(msg, "failed to create card content") ||
		strings.Contains(msg, "table number over limit") ||
		strings.Contains(msg, "errcode: 11310")
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}

func isGatewayTurnTerminalStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return true
	}
	return status == "succeeded" || status == "failed"
}

func (s *feishuHTTPSender) getTenantToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	if strings.TrimSpace(s.token) != "" && time.Now().Before(s.tokenUntil) {
		tok := s.token
		s.mu.Unlock()
		return tok, nil
	}
	s.mu.Unlock()

	payload := map[string]string{
		"app_id":     strings.TrimSpace(s.appID),
		"app_secret": strings.TrimSpace(s.appSecret),
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
		return "", fmt.Errorf("feishu token failed: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
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
		return "", fmt.Errorf("feishu token failed: code=%d msg=%s", out.Code, strings.TrimSpace(out.Msg))
	}
	token := strings.TrimSpace(out.TenantAccessToken)
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
