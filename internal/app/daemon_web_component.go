package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"dalek/internal/services/core"
	daemonsvc "dalek/internal/services/daemon"
	"dalek/internal/web"
)

type daemonWebComponent struct {
	listen          string
	internalAPIAddr string
	logger          *slog.Logger

	listener net.Listener
	server   *http.Server
}

func newDaemonWebComponent(home *Home, logger *slog.Logger) *daemonWebComponent {
	logger = core.EnsureLogger(logger).With("component", "web_console")
	cfg := DefaultHomeConfig()
	if home != nil {
		cfg = home.Config.WithDefaults()
	}
	return &daemonWebComponent{
		listen:          strings.TrimSpace(cfg.Daemon.Web.Listen),
		internalAPIAddr: strings.TrimSpace(cfg.Daemon.Internal.Listen),
		logger:          logger,
	}
}

func (c *daemonWebComponent) Name() string {
	return "web_console"
}

func (c *daemonWebComponent) Start(ctx context.Context) error {
	_ = ctx
	if c == nil {
		return fmt.Errorf("web component 未初始化")
	}
	if c.server != nil {
		return fmt.Errorf("web component 已启动")
	}
	addr := strings.TrimSpace(c.listen)
	if addr == "" {
		return fmt.Errorf("web listen 为空")
	}
	internalAPIAddr := strings.TrimSpace(c.internalAPIAddr)
	if internalAPIAddr == "" {
		return fmt.Errorf("internal api listen 为空")
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		return fmt.Errorf("加载 web 静态资源失败: %w", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	handler := web.NewHandler(staticFS, internalAPIAddr)
	server := &http.Server{
		Handler:           daemonsvc.RecoverMiddleware(c.logger.With("component", "web_console"))(handler),
		ReadHeaderTimeout: 5 * time.Second,
	}
	c.listener = ln
	c.server = server
	go func() {
		if serveErr := server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			c.logf("web console serve failed: %v", serveErr)
		}
	}()
	c.logf("web console listening on %s", ln.Addr().String())
	return nil
}

func (c *daemonWebComponent) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if c.server == nil {
		return nil
	}
	shutdownCtx := ctx
	if shutdownCtx == nil {
		var cancel context.CancelFunc
		shutdownCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	err := c.server.Shutdown(shutdownCtx)
	c.listener = nil
	c.server = nil
	return err
}

func (c *daemonWebComponent) logf(format string, args ...any) {
	if c == nil || c.logger == nil {
		return
	}
	c.logger.Info(fmt.Sprintf(format, args...))
}
