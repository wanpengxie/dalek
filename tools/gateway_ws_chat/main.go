package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"

	gatewayws "dalek/internal/services/channel/ws"
)

const defaultGatewayWSURL = "ws://127.0.0.1:18081/ws"

type inboundFrame = gatewayws.InboundFrame

type outboundFrame = gatewayws.OutboundFrame

func main() {
	fs := flag.NewFlagSet("gateway_ws_chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	wsURL := fs.String("url", defaultGatewayWSURL, "websocket 地址")
	conversationID := fs.String("conv", "", "会话 id（可选）")
	senderID := fs.String("sender", "ws.user", "发送者")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	targetURL, err := buildWSURL(*wsURL, *conversationID, *senderID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gateway_ws_chat 参数错误:", err)
		os.Exit(2)
	}
	conn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gateway_ws_chat 连接失败:", err)
		os.Exit(1)
	}
	defer conn.Close()

	m := newChatModel(conn, targetURL, strings.TrimSpace(*senderID))
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gateway_ws_chat 退出:", err)
		os.Exit(1)
	}
}

type recvMsg struct {
	frame outboundFrame
	raw   string
}

type readErrMsg struct {
	err error
}

type sendResultMsg struct {
	err error
}

type chatModel struct {
	conn      *websocket.Conn
	targetURL string
	senderID  string

	input    textinput.Model
	viewport viewport.Model
	lines    []string

	width  int
	height int
}

func newChatModel(conn *websocket.Conn, targetURL, senderID string) chatModel {
	input := textinput.New()
	input.Prompt = "你> "
	input.Placeholder = "输入消息，按 Enter 发送"
	input.Focus()
	input.CharLimit = 2000
	input.Width = 80

	vp := viewport.New(80, 16)
	return chatModel{
		conn:      conn,
		targetURL: strings.TrimSpace(targetURL),
		senderID:  strings.TrimSpace(senderID),
		input:     input,
		viewport:  vp,
		lines: []string{
			fmt.Sprintf("[%s] 系统: 已连接 %s", time.Now().Format("15:04:05"), strings.TrimSpace(targetURL)),
		},
	}
}

func (m chatModel) Init() tea.Cmd {
	m.refreshViewport()
	return tea.Batch(textinput.Blink, waitMessageCmd(m.conn))
}

func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = x.Width
		m.height = x.Height
		m.syncLayout()
		return m, nil
	case tea.KeyMsg:
		switch x.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.addLine("你", text)
			m.input.SetValue("")
			return m, sendMessageCmd(m.conn, inboundFrame{
				Text:     text,
				SenderID: m.senderID,
			})
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(x)
		return m, cmd
	case recvMsg:
		if x.raw != "" {
			m.addLine("服务端", x.raw)
		} else {
			m.addLine("助手", formatServerLine(x.frame))
		}
		return m, waitMessageCmd(m.conn)
	case readErrMsg:
		m.addLine("系统", "连接已断开: "+strings.TrimSpace(x.err.Error()))
		return m, tea.Quit
	case sendResultMsg:
		if x.err != nil {
			m.addLine("系统", "发送失败: "+strings.TrimSpace(x.err.Error()))
		}
		return m, nil
	}
	return m, nil
}

func (m chatModel) View() string {
	header := fmt.Sprintf("Gateway WS Chat (External)\n目标: %s\nEnter 发送，Ctrl+C 退出", m.targetURL)
	return header + "\n\n" + m.viewport.View() + "\n\n" + m.input.View()
}

func (m *chatModel) syncLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	headerLines := 5
	inputLines := 2
	viewportHeight := m.height - headerLines - inputLines
	if viewportHeight < 4 {
		viewportHeight = 4
	}
	viewportWidth := m.width
	if viewportWidth < 40 {
		viewportWidth = 40
	}
	m.viewport.Width = viewportWidth
	m.viewport.Height = viewportHeight
	m.input.Width = viewportWidth
	m.refreshViewport()
}

func (m *chatModel) refreshViewport() {
	m.viewport.SetContent(strings.Join(m.lines, "\n"))
	m.viewport.GotoBottom()
}

func (m *chatModel) addLine(role, text string) {
	line := fmt.Sprintf("[%s] %s: %s", time.Now().Format("15:04:05"), strings.TrimSpace(role), strings.TrimSpace(text))
	m.lines = append(m.lines, line)
	m.refreshViewport()
}

func waitMessageCmd(conn *websocket.Conn) tea.Cmd {
	return func() tea.Msg {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return readErrMsg{err: err}
		}
		var frame outboundFrame
		if err := json.Unmarshal(payload, &frame); err == nil && strings.TrimSpace(frame.Type) != "" {
			return recvMsg{frame: frame}
		}
		return recvMsg{raw: strings.TrimSpace(string(payload))}
	}
}

func sendMessageCmd(conn *websocket.Conn, frame inboundFrame) tea.Cmd {
	return func() tea.Msg {
		return sendResultMsg{err: conn.WriteJSON(frame)}
	}
}

func formatServerLine(frame outboundFrame) string {
	msgType := strings.TrimSpace(frame.Type)
	text := strings.TrimSpace(frame.Text)
	switch msgType {
	case "ready":
		if strings.TrimSpace(frame.ConversationID) == "" {
			return textOrFallback(text, "connected")
		}
		return fmt.Sprintf("会话=%s，%s", strings.TrimSpace(frame.ConversationID), textOrFallback(text, "connected"))
	case "assistant_message":
		status := strings.TrimSpace(frame.JobStatus)
		if status == "" {
			status = "unknown"
		}
		agent := formatAgentTag(frame.AgentProvider, frame.AgentModel)
		errType := strings.TrimSpace(frame.JobErrorType)
		if errType == "" {
			errType = "unknown"
		}
		meta := formatEventMeta(frame.RunID, frame.Seq, frame.Stream, frame.EventType)
		if strings.TrimSpace(frame.JobError) != "" {
			return fmt.Sprintf("%s\n(job_status=%s, job_error_type=%s, job_error=%s%s%s)", textOrFallback(text, "(empty reply)"), status, errType, strings.TrimSpace(frame.JobError), agent, meta)
		}
		return fmt.Sprintf("%s\n(job_status=%s%s%s)", textOrFallback(text, "(empty reply)"), status, agent, meta)
	case "assistant_event":
		evtType := strings.TrimSpace(frame.EventType)
		if evtType == "" {
			evtType = "event"
		}
		agent := formatAgentTag(frame.AgentProvider, frame.AgentModel)
		meta := formatEventMeta(frame.RunID, frame.Seq, frame.Stream, frame.EventType)
		return fmt.Sprintf("[%s%s%s] %s", evtType, agent, meta, textOrFallback(text, "(empty event)"))
	case "inbox_update":
		if text != "" {
			return text
		}
		return fmt.Sprintf("inbox(open)=%d", frame.InboxCount)
	case "error":
		return "error: " + textOrFallback(text, "(no error detail)")
	default:
		if text == "" {
			return msgType
		}
		return fmt.Sprintf("%s: %s", msgType, text)
	}
}

func textOrFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(s)
}

func formatAgentTag(provider, model string) string {
	p := strings.TrimSpace(provider)
	m := strings.TrimSpace(model)
	if p == "" && m == "" {
		return ""
	}
	if p == "" {
		return ", model=" + m
	}
	if m == "" {
		return ", provider=" + p
	}
	return ", provider=" + p + ", model=" + m
}

func formatEventMeta(runID string, seq int, stream string, eventType string) string {
	runID = strings.TrimSpace(runID)
	stream = strings.TrimSpace(stream)
	eventType = strings.TrimSpace(eventType)
	if runID == "" && seq <= 0 && stream == "" && eventType == "" {
		return ""
	}
	parts := make([]string, 0, 4)
	if runID != "" {
		parts = append(parts, "run_id="+runID)
	}
	if seq > 0 {
		parts = append(parts, fmt.Sprintf("seq=%d", seq))
	}
	if stream != "" {
		parts = append(parts, "stream="+stream)
	}
	if eventType != "" {
		parts = append(parts, "event_type="+eventType)
	}
	return ", " + strings.Join(parts, ", ")
}

func buildWSURL(rawURL, conversationID, senderID string) (string, error) {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return "", fmt.Errorf("url 不能为空")
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("url 非法: %s", target)
	}
	q := u.Query()
	if strings.TrimSpace(conversationID) != "" {
		q.Set("conv", strings.TrimSpace(conversationID))
	}
	if strings.TrimSpace(senderID) != "" {
		q.Set("sender", strings.TrimSpace(senderID))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
