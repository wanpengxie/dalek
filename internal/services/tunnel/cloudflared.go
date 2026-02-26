package tunnel

import (
	"errors"
	"fmt"
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
	DefaultCloudflaredBinary = "cloudflared"
	DefaultProvider          = "cloudflare_tunnel"
	DefaultStopTimeout       = 5 * time.Second

	cloudflaredStartupProbe = 500 * time.Millisecond
)

type RuntimeConfig struct {
	Provider          string
	Enabled           bool
	Name              string
	Hostname          string
	CloudflaredBin    string
	ListenAddr        string
	FeishuWebhookPath string
}

func NewRuntimeConfig(provider string, enabled bool, name, hostname, cloudflaredBin, listenAddr, feishuWebhookPath string) (RuntimeConfig, error) {
	cfg := RuntimeConfig{
		Provider:          strings.TrimSpace(strings.ToLower(provider)),
		Enabled:           enabled,
		Name:              strings.TrimSpace(name),
		Hostname:          strings.TrimSpace(hostname),
		CloudflaredBin:    strings.TrimSpace(cloudflaredBin),
		ListenAddr:        strings.TrimSpace(listenAddr),
		FeishuWebhookPath: strings.TrimSpace(feishuWebhookPath),
	}
	if cfg.CloudflaredBin == "" {
		cfg.CloudflaredBin = DefaultCloudflaredBinary
	}
	if cfg.Provider == "" {
		cfg.Provider = DefaultProvider
	}
	if err := ValidateRuntimeConfig(cfg); err != nil {
		return RuntimeConfig{}, err
	}
	return cfg, nil
}

func ValidateRuntimeConfig(cfg RuntimeConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.Provider) != DefaultProvider {
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

type Process struct {
	cmd        *exec.Cmd
	done       chan error
	configPath string
	stopOnce   sync.Once
}

func StartCloudflaredProcess(runtimeCfg RuntimeConfig) (*Process, error) {
	if !runtimeCfg.Enabled {
		return nil, nil
	}

	originURL, err := BuildOriginURL(runtimeCfg.ListenAddr)
	if err != nil {
		return nil, err
	}
	configBody, err := BuildCloudflaredConfig(
		runtimeCfg.Name,
		runtimeCfg.Hostname,
		originURL,
		runtimeCfg.FeishuWebhookPath,
	)
	if err != nil {
		return nil, err
	}
	configPath, err := WriteCloudflaredConfig(configBody)
	if err != nil {
		return nil, err
	}

	bin := strings.TrimSpace(runtimeCfg.CloudflaredBin)
	if bin == "" {
		bin = DefaultCloudflaredBinary
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

	proc := &Process{
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
	case <-time.After(cloudflaredStartupProbe):
	}
	return proc, nil
}

func (p *Process) Done() <-chan error {
	if p == nil {
		return nil
	}
	return p.done
}

func (p *Process) Stop(timeout time.Duration) error {
	if p == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = DefaultStopTimeout
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

func BuildOriginURL(listenAddr string) (string, error) {
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

func BuildCloudflaredConfig(tunnelName, hostname, originURL, feishuWebhookPath string) (string, error) {
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

	feishuPattern, err := BuildIngressPathPattern(feishuWebhookPath)
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

func BuildIngressPathPattern(rawPath string) (string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", fmt.Errorf("webhook path 不能为空")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.Contains(path, "*") {
		return "", fmt.Errorf("webhook path 不允许包含 *")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(part) {
			return "", fmt.Errorf("webhook path 包含非法字符: %s", part)
		}
	}
	return "^" + regexp.QuoteMeta(path) + "$", nil
}

func WriteCloudflaredConfig(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("cloudflared config 不能为空")
	}
	f, err := os.CreateTemp("", "dalek-cloudflared-*.yml")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(f.Name())
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
	v := strings.TrimSpace(raw)
	v = strings.ReplaceAll(v, `'`, `''`)
	return "'" + v + "'"
}
