package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/contracts"
	gatewayws "dalek/internal/services/channel/ws"
)

type ChatRequest struct {
	TargetURL string
	Text      string
	SenderID  string
}

type ChatResult struct {
	FinalFrame  gatewayws.OutboundFrame
	EventFrames []gatewayws.OutboundFrame
}

func RunChat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	targetURL := strings.TrimSpace(req.TargetURL)
	text := strings.TrimSpace(req.Text)
	senderID := strings.TrimSpace(req.SenderID)

	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, targetURL, nil)
	if err != nil {
		if resp != nil {
			return ChatResult{}, fmt.Errorf("连接 gateway daemon 失败（http=%d）: %w；请先执行 `dalek daemon start`", resp.StatusCode, err)
		}
		lower := strings.ToLower(strings.TrimSpace(err.Error()))
		if strings.Contains(lower, "connection refused") || strings.Contains(lower, "cannot assign requested address") || strings.Contains(lower, "no such host") {
			return ChatResult{}, fmt.Errorf("gateway daemon 未启动或不可达（%s），请先执行 `dalek daemon start`", targetURL)
		}
		return ChatResult{}, fmt.Errorf("连接 gateway daemon 失败: %w", err)
	}
	defer conn.Close()

	for {
		frame, readErr := ReadFrame(ctx, conn)
		if readErr != nil {
			return ChatResult{}, fmt.Errorf("读取 gateway 握手帧失败: %w", readErr)
		}
		switch strings.TrimSpace(frame.Type) {
		case gatewayws.FrameTypeReady:
			goto SEND
		case gatewayws.FrameTypeError:
			return ChatResult{FinalFrame: NormalizeChatFinalFrame(frame)}, nil
		default:
			// 忽略非 ready 的握手前帧。
		}
	}

SEND:
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	}
	if err := conn.WriteJSON(gatewayws.InboundFrame{
		Text:     text,
		SenderID: senderID,
	}); err != nil {
		return ChatResult{}, fmt.Errorf("发送消息到 gateway daemon 失败: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Time{})

	events := make([]gatewayws.OutboundFrame, 0, 8)
	for {
		frame, readErr := ReadFrame(ctx, conn)
		if readErr != nil {
			return ChatResult{EventFrames: events}, fmt.Errorf("读取 gateway 响应失败: %w", readErr)
		}
		switch strings.TrimSpace(frame.Type) {
		case gatewayws.FrameTypeAssistantEvent, gatewayws.FrameTypeInboxUpdate:
			events = append(events, frame)
		case gatewayws.FrameTypeAssistantMessage, gatewayws.FrameTypeError:
			return ChatResult{FinalFrame: NormalizeChatFinalFrame(frame), EventFrames: events}, nil
		default:
			// 兼容未来扩展帧，当前忽略。
		}
	}
}

func ReadFrame(ctx context.Context, conn *websocket.Conn) (gatewayws.OutboundFrame, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return gatewayws.OutboundFrame{}, ctx.Err()
		}
		return gatewayws.OutboundFrame{}, err
	}
	var frame gatewayws.OutboundFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return gatewayws.OutboundFrame{}, fmt.Errorf("解析 gateway 帧失败: %w", err)
	}
	return frame, nil
}

func NormalizeChatFinalFrame(frame gatewayws.OutboundFrame) gatewayws.OutboundFrame {
	frame.Type = strings.TrimSpace(frame.Type)
	frame.Text = strings.TrimSpace(frame.Text)
	frame.JobStatus = strings.TrimSpace(frame.JobStatus)
	frame.JobErrorType = strings.TrimSpace(frame.JobErrorType)
	frame.JobError = strings.TrimSpace(frame.JobError)
	if frame.Type == gatewayws.FrameTypeError {
		if frame.JobStatus == "" {
			frame.JobStatus = string(contracts.ChannelTurnFailed)
		}
		if frame.JobError == "" {
			frame.JobError = frame.Text
		}
		if frame.JobErrorType == "" {
			frame.JobErrorType = "runtime"
		}
	}
	if frame.JobStatus == "" {
		frame.JobStatus = string(contracts.ChannelTurnSucceeded)
	}
	return frame
}
