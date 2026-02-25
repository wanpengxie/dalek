package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/store"
)

type testChannelRequest struct {
	ChatID   string `json:"chat_id"`
	SenderID string `json:"sender_id,omitempty"`
	Text     string `json:"text"`
}

type homeProjectResolver struct {
	home *app.Home

	mu    sync.Mutex
	cache map[string]*channelsvc.ProjectContext
}

func newHomeProjectResolver(home *app.Home) *homeProjectResolver {
	return &homeProjectResolver{
		home:  home,
		cache: map[string]*channelsvc.ProjectContext{},
	}
}

func (r *homeProjectResolver) Resolve(name string) (*channelsvc.ProjectContext, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("project resolver 未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name 不能为空")
	}

	r.mu.Lock()
	if cached, ok := r.cache[name]; ok && cached != nil {
		r.mu.Unlock()
		return cached, nil
	}
	r.mu.Unlock()

	p, err := r.home.OpenProjectByName(name)
	if err != nil {
		return nil, err
	}
	ctx := &channelsvc.ProjectContext{
		Name:     strings.TrimSpace(p.Name()),
		RepoRoot: strings.TrimSpace(p.RepoRoot()),
		Runtime: &appProjectRuntime{
			project: p,
			channel: p.ChannelService(),
		},
	}

	r.mu.Lock()
	r.cache[name] = ctx
	r.mu.Unlock()
	return ctx, nil
}

func (r *homeProjectResolver) ListProjects() ([]string, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("project resolver 未初始化")
	}
	projects, err := r.home.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

type appProjectRuntime struct {
	project *app.Project
	channel *channelsvc.Service
}

func (r *appProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	if r == nil || r.channel == nil {
		return channelsvc.ProcessResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.channel.ProcessInbound(ctx, env)
}

func (r *appProjectRuntime) GatewayTurnTimeout() time.Duration {
	if r == nil || r.project == nil {
		return 120 * time.Second
	}
	return r.project.GatewayTurnTimeout()
}

func main() {
	fs := flag.NewFlagSet("gateway_cli_test_server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listenAddr := fs.String("listen", "127.0.0.1:18181", "监听地址（仅本机联调）")
	homeFlag := fs.String("home", "", "dalek Home 目录")
	pathFlag := fs.String("path", "/cli/test/channel", "测试通道路径")
	adapterFlag := fs.String("adapter", "im.cli.test", "测试通道 adapter")
	queueDepth := fs.Int("queue-depth", 32, "每个 project 入站队列深度")
	waitTimeout := fs.Duration("wait-timeout", 120*time.Second, "等待 agent 回调超时")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	homeDir, err := app.ResolveHomeDir(strings.TrimSpace(*homeFlag))
	if err != nil {
		fmt.Fprintln(os.Stderr, "解析 -home 失败:", err)
		os.Exit(1)
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开 Home 失败:", err)
		os.Exit(1)
	}
	gatewayDBPath := strings.TrimSpace(h.GatewayDBPath)
	if gatewayDBPath == "" {
		gatewayDBPath = filepath.Join(homeDir, "gateway.db")
	}
	gatewayDB, err := store.OpenGatewayDB(gatewayDBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开 gateway.db 失败:", err)
		os.Exit(1)
	}

	resolver := newHomeProjectResolver(h)
	gateway, err := channelsvc.NewGateway(gatewayDB, resolver, channelsvc.GatewayOptions{
		QueueDepth: *queueDepth,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建 gateway runtime 失败:", err)
		os.Exit(1)
	}

	path := strings.TrimSpace(*pathFlag)
	if path == "" {
		path = "/cli/test/channel"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	adapter := strings.TrimSpace(*adapterFlag)
	if adapter == "" {
		adapter = "im.cli.test"
	}
	handler := newCLITestChannelHandler(gateway, resolver, adapter, *waitTimeout)

	mux := http.NewServeMux()
	mux.HandleFunc(path, handler)

	addr := strings.TrimSpace(*listenAddr)
	if addr == "" {
		fmt.Fprintln(os.Stderr, "-listen 不能为空")
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "gateway cli test server listening: http://%s%s\n", addr, path)
	fmt.Fprintln(os.Stderr, "warning: 该端点无认证，仅用于本机联调，默认绑定 127.0.0.1")
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, "server 退出:", err)
		os.Exit(1)
	}
}

func newCLITestChannelHandler(gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, adapter string, waitTimeout time.Duration) http.HandlerFunc {
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = "im.cli.test"
	}
	if waitTimeout <= 0 {
		waitTimeout = 120 * time.Second
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req testChannelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": 1, "msg": "invalid json"})
			return
		}
		chatID := strings.TrimSpace(req.ChatID)
		text := strings.TrimSpace(req.Text)
		senderID := strings.TrimSpace(req.SenderID)
		if chatID == "" || text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": 1, "msg": "chat_id/text required"})
			return
		}
		if senderID == "" {
			senderID = "cli.test.user"
		}

		if ok, reply := tryHandleBind(gateway, resolver, adapter, chatID, text); ok {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "reply": reply})
			return
		}
		if ok, reply := tryHandleUnbind(gateway, adapter, chatID, text); ok {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "reply": reply})
			return
		}

		projectName, err := gateway.LookupBoundProject(context.Background(), contracts.ChannelTypeIM, adapter, chatID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"code": 1, "msg": "lookup failed: " + strings.TrimSpace(err.Error())})
			return
		}
		if strings.TrimSpace(projectName) == "" {
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "reply": buildUnboundHint(resolver)})
			return
		}

		replyCh := make(chan string, 1)
		errCh := make(chan error, 1)
		submitErr := gateway.Submit(context.Background(), channelsvc.GatewayInboundRequest{
			ProjectName:    projectName,
			PeerProjectKey: chatID,
			Envelope: contracts.InboundEnvelope{
				Schema:             contracts.ChannelInboundSchemaV1,
				ChannelType:        contracts.ChannelTypeIM,
				Adapter:            adapter,
				PeerConversationID: chatID,
				PeerMessageID:      fmt.Sprintf("cli-test-%d", time.Now().UnixNano()),
				SenderID:           senderID,
				Text:               text,
				ReceivedAt:         time.Now().Format(time.RFC3339),
			},
			Callback: func(res channelsvc.ProcessResult, runErr error) {
				if runErr != nil {
					errCh <- runErr
					return
				}
				reply := strings.TrimSpace(res.ReplyText)
				if reply == "" {
					reply = strings.TrimSpace(res.JobError)
				}
				if reply == "" {
					reply = "(empty reply)"
				}
				replyCh <- reply
			},
		})
		if submitErr != nil {
			msg := strings.TrimSpace(submitErr.Error())
			if submitErr == channelsvc.ErrInboundQueueFull {
				msg = "排队中，请稍后再试。"
			}
			writeJSON(w, http.StatusOK, map[string]any{"code": 1, "msg": msg})
			return
		}

		select {
		case reply := <-replyCh:
			writeJSON(w, http.StatusOK, map[string]any{"code": 0, "project": projectName, "reply": reply})
		case runErr := <-errCh:
			writeJSON(w, http.StatusOK, map[string]any{"code": 1, "msg": strings.TrimSpace(runErr.Error())})
		case <-time.After(waitTimeout):
			writeJSON(w, http.StatusOK, map[string]any{"code": 1, "msg": fmt.Sprintf("wait callback timeout (%s)", waitTimeout.String())})
		}
	}
}

func tryHandleBind(gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, adapter, chatID, text string) (bool, string) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(trimmed), "/bind") {
		return false, ""
	}
	fields := strings.Fields(trimmed)
	if len(fields) != 2 || strings.TrimSpace(fields[1]) == "" {
		return true, "命令格式错误，请使用 /bind <项目名>"
	}
	projectName := strings.TrimSpace(fields[1])
	if resolver != nil {
		if _, err := resolver.Resolve(projectName); err != nil {
			return true, "项目不存在：" + projectName + "\n\n" + buildProjectList(resolver)
		}
	}
	prevProject, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, adapter, chatID, projectName)
	if err != nil {
		return true, "绑定失败，请稍后重试"
	}
	prevProject = strings.TrimSpace(prevProject)
	if prevProject == "" || prevProject == projectName {
		return true, "已绑定到 project " + projectName
	}
	return true, "已切换到 " + projectName
}

func tryHandleUnbind(gateway *channelsvc.Gateway, adapter, chatID, text string) (bool, string) {
	if strings.TrimSpace(strings.ToLower(strings.TrimSpace(text))) != "/unbind" {
		return false, ""
	}
	removed, err := gateway.UnbindProject(context.Background(), contracts.ChannelTypeIM, adapter, chatID)
	if err != nil {
		return true, "解绑失败，请稍后重试"
	}
	if removed {
		return true, "已解绑"
	}
	return true, "当前未绑定项目"
}

func buildUnboundHint(resolver channelsvc.ProjectResolver) string {
	return "本群尚未绑定项目。\n\n" + buildProjectList(resolver) + "\n\n请发送 /bind <项目名> 进行绑定。"
}

func buildProjectList(resolver channelsvc.ProjectResolver) string {
	projects := []string{}
	if resolver != nil {
		list, err := resolver.ListProjects()
		if err == nil {
			projects = append(projects, list...)
		}
	}
	clean := make([]string, 0, len(projects))
	for _, p := range projects {
		p = strings.TrimSpace(p)
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
