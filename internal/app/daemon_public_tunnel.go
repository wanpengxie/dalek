package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultDaemonCloudflaredBinary           = "cloudflared"
	daemonPublicTunnelStartupProbe           = 500 * time.Millisecond
	daemonPublicTunnelMinBackoff             = 2 * time.Second
	daemonPublicTunnelMaxBackoff             = 60 * time.Second
	daemonPublicTunnelStableThreshold        = 30 * time.Second
	daemonPublicTunnelMaxConsecutiveFailures = 5
	daemonPublicTunnelStopTimeout            = 5 * time.Second
)

type daemonPublicTunnelRuntimeConfig struct {
	Provider          string
	Enabled           bool
	Name              string
	Hostname          string
	CloudflaredBin    string
	ListenAddr        string
	FeishuWebhookPath string
}

type daemonPublicTunnelProcess struct {
	cmd        *exec.Cmd
	done       chan error
	configPath string
	stopOnce   sync.Once
}

type daemonPublicTunnelStartFunc func(daemonPublicTunnelRuntimeConfig) (*daemonPublicTunnelProcess, error)
type daemonPublicTunnelWaitFunc func(context.Context, time.Duration) bool

type daemonPublicTunnelSupervisor struct {
	runtimeCfg daemonPublicTunnelRuntimeConfig
	logger     *log.Logger

	startFn                daemonPublicTunnelStartFunc
	waitFn                 daemonPublicTunnelWaitFunc
	nowFn                  func() time.Time
	maxConsecutiveFailures int

	mu                  sync.Mutex
	current             *daemonPublicTunnelProcess
	stopped             bool
	consecutiveFailures int
	circuitOpen         bool
}

func newDaemonPublicTunnelRuntimeConfig(provider string, enabled bool, name, hostname, cloudflaredBin, listenAddr, feishuWebhookPath string) (daemonPublicTunnelRuntimeConfig, error) {
	cfg := daemonPublicTunnelRuntimeConfig{
		Provider:          strings.TrimSpace(strings.ToLower(provider)),
		Enabled:           enabled,
		Name:              strings.TrimSpace(name),
		Hostname:          strings.TrimSpace(hostname),
		CloudflaredBin:    strings.TrimSpace(cloudflaredBin),
		ListenAddr:        strings.TrimSpace(listenAddr),
		FeishuWebhookPath: strings.TrimSpace(feishuWebhookPath),
	}
	if cfg.CloudflaredBin == "" {
		cfg.CloudflaredBin = defaultDaemonCloudflaredBinary
	}
	if cfg.Provider == "" {
		cfg.Provider = defaultDaemonPublicIngressProvider
	}
	if err := validateDaemonPublicTunnelRuntimeConfig(cfg); err != nil {
		return daemonPublicTunnelRuntimeConfig{}, err
	}
	return cfg, nil
}

func validateDaemonPublicTunnelRuntimeConfig(cfg daemonPublicTunnelRuntimeConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.Provider) != defaultDaemonPublicIngressProvider {
		return fmt.Errorf("daemon.public.ingress.provider 暂不支持: %s", strings.TrimSpace(cfg.Provider))
	}
	name := strings.TrimSpace(cfg.Name)
	hostname := strings.TrimSpace(cfg.Hostname)
	if name == "" {
		return fmt.Errorf("daemon.public.ingress.tunnel_name 不能为空")
	}
	if hostname == "" {
		return fmt.Errorf("daemon.public.ingress.hostname 不能为空")
	}
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		return fmt.Errorf("public listen 不能为空")
	}
	if strings.TrimSpace(cfg.FeishuWebhookPath) == "" {
		return fmt.Errorf("feishu webhook path 不能为空")
	}
	return nil
}

func (s *daemonPublicTunnelSupervisor) Run(ctx context.Context) {
	if s == nil {
		return
	}
	startFn := s.startFn
	if startFn == nil {
		startFn = maybeStartDaemonPublicTunnelProcess
	}
	waitFn := s.waitFn
	if waitFn == nil {
		waitFn = waitDaemonPublicTunnelBackoff
	}
	nowFn := s.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}

	backoff := daemonPublicTunnelMinBackoff
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		proc, err := startFn(s.runtimeCfg)
		if err != nil {
			tripped, failures, maxFailures := s.recordFailure()
			if tripped {
				s.logf("cloudflared 熔断: 连续失败 %d 次，停止重试。最后错误: %v", failures, err)
				return
			}
			s.logf("cloudflared 启动失败: err=%v failures=%d/%d retry_in=%s", err, failures, maxFailures, backoff)
			if !waitFn(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, daemonPublicTunnelMaxBackoff)
			continue
		}
		if proc == nil {
			return
		}

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			_ = proc.Stop(daemonPublicTunnelStopTimeout)
			return
		}
		s.current = proc
		s.mu.Unlock()

		startedAt := nowFn()
		s.logf("cloudflare tunnel 已连接: https://%s", s.runtimeCfg.Hostname)
		select {
		case <-ctx.Done():
			return
		case err := <-proc.Done():
			s.mu.Lock()
			if s.current == proc {
				s.current = nil
			}
			s.mu.Unlock()
			uptime := nowFn().Sub(startedAt)
			if uptime >= daemonPublicTunnelStableThreshold {
				s.resetFailures()
				backoff = daemonPublicTunnelMinBackoff
				s.logf("cloudflared 进程退出: uptime=%s err=%v stable=true retry_in=%s", uptime.Round(time.Second), err, backoff)
			} else {
				tripped, failures, maxFailures := s.recordFailure()
				if tripped {
					s.logf("cloudflared 熔断: 连续失败 %d 次，停止重试。最后错误: %v", failures, err)
					return
				}
				s.logf("cloudflared 进程退出: uptime=%s err=%v failures=%d/%d retry_in=%s", uptime.Round(time.Second), err, failures, maxFailures, backoff)
			}
		}

		if !waitFn(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, daemonPublicTunnelMaxBackoff)
	}
}

func (s *daemonPublicTunnelSupervisor) Stop(timeout time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	proc := s.current
	s.current = nil
	s.mu.Unlock()
	if proc != nil {
		_ = proc.Stop(timeout)
	}
}

func (s *daemonPublicTunnelSupervisor) CircuitOpen() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.circuitOpen
}

func (s *daemonPublicTunnelSupervisor) logf(format string, args ...any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Printf(format, args...)
}

func (s *daemonPublicTunnelSupervisor) recordFailure() (tripped bool, failures int, maxFailures int) {
	if s == nil {
		return false, 0, daemonPublicTunnelMaxConsecutiveFailures
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	maxFailures = s.maxConsecutiveFailures
	if maxFailures <= 0 {
		maxFailures = daemonPublicTunnelMaxConsecutiveFailures
	}
	s.consecutiveFailures++
	failures = s.consecutiveFailures
	if failures >= maxFailures {
		s.circuitOpen = true
		tripped = true
	}
	return tripped, failures, maxFailures
}

func (s *daemonPublicTunnelSupervisor) resetFailures() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveFailures = 0
}

func waitDaemonPublicTunnelBackoff(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = daemonPublicTunnelMinBackoff
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func maybeStartDaemonPublicTunnelProcess(runtimeCfg daemonPublicTunnelRuntimeConfig) (*daemonPublicTunnelProcess, error) {
	if !runtimeCfg.Enabled {
		return nil, nil
	}
	if err := validateDaemonPublicTunnelRuntimeConfig(runtimeCfg); err != nil {
		return nil, err
	}

	originURL, err := buildDaemonPublicTunnelOriginURL(runtimeCfg.ListenAddr)
	if err != nil {
		return nil, err
	}
	configBody, err := buildDaemonPublicTunnelCloudflaredConfig(
		runtimeCfg.Name,
		runtimeCfg.Hostname,
		originURL,
		runtimeCfg.FeishuWebhookPath,
	)
	if err != nil {
		return nil, err
	}
	configPath, err := writeDaemonPublicTunnelCloudflaredConfig(configBody)
	if err != nil {
		return nil, err
	}

	bin := strings.TrimSpace(runtimeCfg.CloudflaredBin)
	if bin == "" {
		bin = defaultDaemonCloudflaredBinary
	}
	cleanupCmd := exec.Command(bin, "tunnel", "cleanup", strings.TrimSpace(runtimeCfg.Name))
	cleanupCmd.Stdout = os.Stderr
	cleanupCmd.Stderr = os.Stderr
	if err := cleanupCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cloudflared tunnel cleanup（非致命）: %v\n", err)
	}

	cmd := exec.Command(bin, "tunnel", "--config", configPath, "run", strings.TrimSpace(runtimeCfg.Name))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = os.Remove(configPath)
		return nil, fmt.Errorf("启动 cloudflared 失败: %w", err)
	}

	proc := &daemonPublicTunnelProcess{
		cmd:        cmd,
		done:       make(chan error, 1),
		configPath: configPath,
	}
	go func() {
		proc.done <- cmd.Wait()
		close(proc.done)
	}()

	select {
	case err := <-proc.done:
		_ = os.Remove(configPath)
		if err == nil {
			return nil, fmt.Errorf("cloudflared 已退出")
		}
		return nil, fmt.Errorf("cloudflared 启动后立即退出: %w", err)
	case <-time.After(daemonPublicTunnelStartupProbe):
	}

	return proc, nil
}

func (p *daemonPublicTunnelProcess) Done() <-chan error {
	if p == nil {
		return nil
	}
	return p.done
}

func (p *daemonPublicTunnelProcess) Stop(timeout time.Duration) error {
	if p == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = daemonPublicTunnelStopTimeout
	}

	var stopErr error
	p.stopOnce.Do(func() {
		defer func() {
			if p.configPath != "" {
				_ = os.Remove(p.configPath)
			}
		}()

		if p.cmd == nil || p.cmd.Process == nil {
			return
		}
		if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			if killErr := p.cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				stopErr = killErr
			}
		}

		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-p.done:
		case <-timer.C:
			if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				stopErr = err
			}
			<-p.done
		}
	})
	return stopErr
}

func buildDaemonPublicTunnelOriginURL(listenAddr string) (string, error) {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		return "", fmt.Errorf("gateway 监听地址为空")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("解析 gateway 监听地址失败: %w", err)
	}
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func buildDaemonPublicTunnelCloudflaredConfig(tunnelName, hostname, originURL, feishuWebhookPath string) (string, error) {
	tunnelName = strings.TrimSpace(tunnelName)
	hostname = strings.TrimSpace(hostname)
	originURL = strings.TrimSpace(originURL)
	if tunnelName == "" {
		return "", fmt.Errorf("gateway.tunnel.name 不能为空")
	}
	if hostname == "" {
		return "", fmt.Errorf("gateway.tunnel.hostname 不能为空")
	}
	if originURL == "" {
		return "", fmt.Errorf("tunnel origin url 不能为空")
	}

	feishuPattern, err := buildDaemonPublicTunnelIngressPathPattern(feishuWebhookPath)
	if err != nil {
		return "", fmt.Errorf("feishu ingress path 无效: %w", err)
	}

	var b strings.Builder
	b.WriteString("tunnel: ")
	b.WriteString(toSingleQuotedYAMLString(tunnelName))
	b.WriteByte('\n')
	b.WriteString("ingress:\n")
	b.WriteString("  - hostname: ")
	b.WriteString(toSingleQuotedYAMLString(hostname))
	b.WriteByte('\n')
	b.WriteString("    path: ")
	b.WriteString(toSingleQuotedYAMLString(feishuPattern))
	b.WriteByte('\n')
	b.WriteString("    service: ")
	b.WriteString(toSingleQuotedYAMLString(originURL))
	b.WriteByte('\n')
	b.WriteString("  - service: 'http_status:404'\n")
	return b.String(), nil
}

func buildDaemonPublicTunnelIngressPathPattern(rawPath string) (string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "^" + regexp.QuoteMeta(path) + "$", nil
}

func writeDaemonPublicTunnelCloudflaredConfig(body string) (string, error) {
	f, err := os.CreateTemp("", "dalek-cloudflared-*.yml")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func toSingleQuotedYAMLString(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "'", "''")
	return "'" + raw + "'"
}
