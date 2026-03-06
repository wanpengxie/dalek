package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/services/core"
	daemonsvc "dalek/internal/services/daemon"
	gatewaysendsvc "dalek/internal/services/gatewaysend"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type daemonPublicGatewayComponent struct {
	home     *Home
	resolver channelsvc.ProjectResolver
	listen   string
	logger   *slog.Logger

	queueDepth     int
	webhookPath    string
	feishuEnabled  bool
	feishuDisabled string
	verifyToken    string
	adapter        string
	sender         daemonFeishuMessageSender
	tunnelProvider string
	tunnelEnabled  bool
	tunnelDisabled string
	tunnelName     string
	tunnelHost     string
	tunnelBin      string

	gateway   *channelsvc.Gateway
	gatewayDB *gorm.DB

	listener net.Listener
	server   *http.Server

	tunnelSupervisor *daemonPublicTunnelSupervisor
	tunnelCancel     context.CancelFunc
	tunnelDone       chan struct{}
}

func newDaemonPublicGatewayComponent(home *Home, logger *slog.Logger, resolvers ...channelsvc.ProjectResolver) *daemonPublicGatewayComponent {
	logger = core.EnsureLogger(logger).With("component", "public_gateway")
	var resolver channelsvc.ProjectResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	if resolver == nil && home != nil {
		resolver = newDaemonGatewayProjectResolver(home, NewProjectRegistry(home))
	}
	cfg := DefaultHomeConfig()
	if home != nil {
		cfg = home.Config.WithDefaults()
	}
	feishuCfg := cfg.Daemon.Public.Feishu
	ingressCfg := cfg.Daemon.Public.Ingress
	webhookPath := resolveDaemonFeishuWebhookPath(cfg)
	if strings.TrimSpace(webhookPath) == "" {
		webhookPath = "/feishu/webhook"
	}
	feishuCfg, feishuDisabled := resolveDaemonPublicFeishuRuntimeConfig(feishuCfg)
	ingressCfg, tunnelDisabled := resolveDaemonPublicIngressRuntimeConfig(
		strings.TrimSpace(cfg.Daemon.Public.Listen),
		webhookPath,
		feishuCfg.Enabled,
		ingressCfg,
	)
	return &daemonPublicGatewayComponent{
		home:           home,
		resolver:       resolver,
		listen:         strings.TrimSpace(cfg.Daemon.Public.Listen),
		logger:         logger,
		queueDepth:     cfg.Gateway.QueueDepth,
		webhookPath:    webhookPath,
		feishuEnabled:  feishuCfg.Enabled,
		feishuDisabled: feishuDisabled,
		verifyToken:    strings.TrimSpace(feishuCfg.VerificationToken),
		adapter:        defaultDaemonFeishuAdapter,
		sender:         newDaemonFeishuSender(feishuCfg, logger),
		tunnelProvider: strings.TrimSpace(ingressCfg.Provider),
		tunnelEnabled:  ingressCfg.Enabled,
		tunnelDisabled: tunnelDisabled,
		tunnelName:     strings.TrimSpace(ingressCfg.TunnelName),
		tunnelHost:     strings.TrimSpace(ingressCfg.Hostname),
		tunnelBin:      strings.TrimSpace(ingressCfg.CloudflaredBin),
	}
}

func (c *daemonPublicGatewayComponent) Name() string {
	return "public_gateway"
}

func (c *daemonPublicGatewayComponent) Start(ctx context.Context) error {
	_ = ctx
	if c == nil || c.home == nil {
		return fmt.Errorf("public gateway 组件未初始化")
	}
	addr := strings.TrimSpace(c.listen)
	if addr == "" {
		return fmt.Errorf("public gateway listen 为空")
	}
	if c.server != nil {
		return fmt.Errorf("public gateway 已启动")
	}
	if reason := strings.TrimSpace(c.feishuDisabled); reason != "" {
		c.logf("public feishu disabled; fallback to local-only gateway: %s", reason)
	}
	if reason := strings.TrimSpace(c.tunnelDisabled); reason != "" {
		c.logf("public tunnel disabled; fallback to local-only gateway: %s", reason)
	}

	gatewayDBPath := strings.TrimSpace(c.home.GatewayDBPath)
	if gatewayDBPath == "" {
		return fmt.Errorf("gateway db path 为空")
	}
	gatewayDB, err := store.OpenGatewayDB(gatewayDBPath)
	if err != nil {
		return fmt.Errorf("打开 gateway db 失败: %w", err)
	}
	if c.resolver == nil {
		return fmt.Errorf("public gateway resolver 未初始化")
	}
	gateway, err := channelsvc.NewGateway(gatewayDB, c.resolver, channelsvc.GatewayOptions{
		QueueDepth: c.queueDepth,
		Logger:     c.logger,
	})
	if err != nil {
		_ = closeDaemonGatewayDB(gatewayDB)
		return fmt.Errorf("创建 gateway runtime 失败: %w", err)
	}

	mux := http.NewServeMux()
	webhookPath := strings.TrimSpace(c.webhookPath)
	if webhookPath == "" {
		webhookPath = "/feishu/webhook"
	}
	if c.feishuEnabled {
		var gatewaySendResolver contracts.ProjectMetaResolver
		if resolved, ok := any(c.resolver).(contracts.ProjectMetaResolver); ok {
			gatewaySendResolver = resolved
		}
		chatReplySender := gatewaysendsvc.NewServiceWithDB(gatewayDB, gatewaySendResolver, c.sender, c.logger)
		mux.HandleFunc(webhookPath, newDaemonFeishuWebhookHandler(gateway, c.resolver, c.sender, daemonFeishuWebhookOptions{
			Adapter:         c.adapter,
			VerifyToken:     c.verifyToken,
			ChatReplySender: chatReplySender,
		}, c.logger))
	} else {
		mux.HandleFunc(webhookPath, func(w http.ResponseWriter, r *http.Request) {
			writePublicJSON(w, http.StatusNotFound, map[string]any{
				"error": "feishu_disabled",
				"cause": "daemon.public.feishu.enabled=false",
			})
		})
	}
	tunnelCfg, err := newDaemonPublicTunnelRuntimeConfig(
		c.tunnelProvider,
		c.tunnelEnabled,
		c.tunnelName,
		c.tunnelHost,
		c.tunnelBin,
		addr,
		webhookPath,
	)
	if err != nil {
		_ = closeDaemonGatewayDB(gatewayDB)
		return fmt.Errorf("public tunnel 配置无效: %w", err)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writePublicJSON(w, http.StatusNotFound, map[string]any{
			"error": "not_found",
			"cause": "public listener 路径不存在",
		})
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_ = closeDaemonGatewayDB(gatewayDB)
		return err
	}
	server := &http.Server{
		Handler:           daemonsvc.RecoverMiddleware(c.logger.With("component", "public_gateway"))(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	c.gatewayDB = gatewayDB
	c.gateway = gateway
	c.listener = ln
	c.server = server
	go func() {
		if serveErr := server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			c.logf("public gateway serve failed: %v", serveErr)
		}
	}()
	if tunnelCfg.Enabled {
		tunnelCtx, cancel := context.WithCancel(context.Background())
		supervisor := &daemonPublicTunnelSupervisor{
			RuntimeConfig: tunnelCfg,
			Logger:        c.logger,
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			supervisor.Run(tunnelCtx)
		}()
		c.tunnelSupervisor = supervisor
		c.tunnelCancel = cancel
		c.tunnelDone = done
		c.logf("public tunnel supervisor started: hostname=%s", tunnelCfg.Hostname)
	}
	c.logf("public gateway listening on %s webhook=%s", addr, webhookPath)
	return nil
}

func resolveDaemonPublicFeishuRuntimeConfig(cfg HomeDaemonPublicFeishuConfig) (HomeDaemonPublicFeishuConfig, string) {
	cfg = normalizeDaemonPublicFeishuRuntimeConfig(cfg)
	if !cfg.Enabled {
		return cfg, ""
	}
	missing := make([]string, 0, 3)
	if strings.TrimSpace(cfg.AppID) == "" {
		missing = append(missing, "app_id")
	}
	if strings.TrimSpace(cfg.AppSecret) == "" {
		missing = append(missing, "app_secret")
	}
	if strings.TrimSpace(cfg.VerificationToken) == "" {
		missing = append(missing, "verification_token")
	}
	if len(missing) == 0 {
		return cfg, ""
	}
	cfg.Enabled = false
	return cfg, fmt.Sprintf("daemon.public.feishu 缺少 %s", strings.Join(missing, "/"))
}

func resolveDaemonPublicIngressRuntimeConfig(listenAddr, webhookPath string, feishuEnabled bool, cfg HomeDaemonPublicIngressConfig) (HomeDaemonPublicIngressConfig, string) {
	cfg = normalizeDaemonPublicIngressRuntimeConfig(cfg)
	if !cfg.Enabled {
		return cfg, ""
	}
	if !feishuEnabled {
		cfg.Enabled = false
		return cfg, "daemon.public.feishu 未启用"
	}
	if _, err := newDaemonPublicTunnelRuntimeConfig(
		cfg.Provider,
		true,
		cfg.TunnelName,
		cfg.Hostname,
		cfg.CloudflaredBin,
		listenAddr,
		webhookPath,
	); err != nil {
		cfg.Enabled = false
		return cfg, err.Error()
	}
	return cfg, ""
}

func normalizeDaemonPublicFeishuRuntimeConfig(cfg HomeDaemonPublicFeishuConfig) HomeDaemonPublicFeishuConfig {
	cfg.AppID = strings.TrimSpace(cfg.AppID)
	cfg.AppSecret = strings.TrimSpace(cfg.AppSecret)
	cfg.VerificationToken = strings.TrimSpace(cfg.VerificationToken)
	cfg.WebhookSecretPath = strings.TrimSpace(cfg.WebhookSecretPath)
	cfg.WebhookPath = strings.TrimSpace(cfg.WebhookPath)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	return cfg
}

func normalizeDaemonPublicIngressRuntimeConfig(cfg HomeDaemonPublicIngressConfig) HomeDaemonPublicIngressConfig {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.TunnelName = strings.TrimSpace(cfg.TunnelName)
	cfg.Hostname = strings.TrimSpace(cfg.Hostname)
	cfg.CloudflaredBin = strings.TrimSpace(cfg.CloudflaredBin)
	return cfg
}

func (c *daemonPublicGatewayComponent) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}

	var shutdownErr error
	recordErr := func(err error) {
		if err != nil && shutdownErr == nil {
			shutdownErr = err
		}
	}
	if err := c.stopTunnel(ctx); err != nil {
		recordErr(err)
	}
	if c.server != nil {
		shutdownCtx := ctx
		if shutdownCtx == nil {
			var cancel context.CancelFunc
			shutdownCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
		}
		recordErr(c.server.Shutdown(shutdownCtx))
		c.server = nil
		c.listener = nil
	}
	if c.gateway != nil {
		stopCtx := ctx
		if stopCtx == nil {
			var cancel context.CancelFunc
			stopCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
		}
		recordErr(c.gateway.Stop(stopCtx))
	}
	if c.gatewayDB != nil {
		if err := closeDaemonGatewayDB(c.gatewayDB); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		c.gatewayDB = nil
	}
	c.gateway = nil
	return shutdownErr
}

func (c *daemonPublicGatewayComponent) stopTunnel(ctx context.Context) error {
	if c == nil {
		return nil
	}
	supervisor := c.tunnelSupervisor
	cancel := c.tunnelCancel
	done := c.tunnelDone
	c.tunnelSupervisor = nil
	c.tunnelCancel = nil
	c.tunnelDone = nil

	if cancel == nil && supervisor == nil && done == nil {
		return nil
	}
	if cancel != nil {
		cancel()
	}

	timeout := 5 * time.Second
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			remain := time.Until(deadline)
			if remain <= 0 {
				remain = 200 * time.Millisecond
			}
			if remain < timeout {
				timeout = remain
			}
		}
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if supervisor != nil {
		supervisor.Stop(timeout)
	}
	if done == nil {
		return nil
	}
	waitTimer := time.NewTimer(timeout)
	defer waitTimer.Stop()
	select {
	case <-done:
		return nil
	case <-waitTimer.C:
		return fmt.Errorf("public tunnel 停止超时")
	}
}

func (c *daemonPublicGatewayComponent) logf(format string, args ...any) {
	if c == nil || c.logger == nil {
		return
	}
	c.logger.Info(fmt.Sprintf(format, args...))
}

func closeDaemonGatewayDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func writePublicJSON(w http.ResponseWriter, code int, payload any) {
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
