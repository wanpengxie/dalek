package app

import (
	"context"
	"strings"
	"time"

	tunnelsvc "dalek/internal/services/tunnel"
)

const (
	defaultDaemonCloudflaredBinary           = tunnelsvc.DefaultCloudflaredBinary
	daemonPublicTunnelMinBackoff             = tunnelsvc.MinBackoff
	daemonPublicTunnelMaxBackoff             = tunnelsvc.MaxBackoff
	daemonPublicTunnelStableThreshold        = tunnelsvc.StableThreshold
	daemonPublicTunnelMaxConsecutiveFailures = tunnelsvc.MaxConsecutiveFailures
	daemonPublicTunnelStopTimeout            = tunnelsvc.DefaultStopTimeout
)

type daemonPublicTunnelRuntimeConfig = tunnelsvc.RuntimeConfig

type daemonPublicTunnelProcess struct {
	inner *tunnelsvc.Process
	done  chan error
}

type daemonPublicTunnelStartFunc = tunnelsvc.StartFunc
type daemonPublicTunnelWaitFunc = tunnelsvc.WaitFunc
type daemonPublicTunnelSupervisor = tunnelsvc.Supervisor

func newDaemonPublicTunnelRuntimeConfig(provider string, enabled bool, name, hostname, cloudflaredBin, listenAddr, feishuWebhookPath string) (daemonPublicTunnelRuntimeConfig, error) {
	return tunnelsvc.NewRuntimeConfig(provider, enabled, name, hostname, cloudflaredBin, listenAddr, feishuWebhookPath)
}

func validateDaemonPublicTunnelRuntimeConfig(cfg daemonPublicTunnelRuntimeConfig) error {
	return tunnelsvc.ValidateRuntimeConfig(cfg)
}

func waitDaemonPublicTunnelBackoff(ctx context.Context, d time.Duration) bool {
	return tunnelsvc.WaitBackoff(ctx, d)
}

func maybeStartDaemonPublicTunnelProcess(runtimeCfg daemonPublicTunnelRuntimeConfig) (tunnelsvc.ProcessHandle, error) {
	if !runtimeCfg.Enabled {
		return nil, nil
	}
	if err := validateDaemonPublicTunnelRuntimeConfig(runtimeCfg); err != nil {
		return nil, err
	}
	proc, err := tunnelsvc.StartCloudflaredProcess(tunnelsvc.RuntimeConfig{
		Enabled:           runtimeCfg.Enabled,
		Name:              strings.TrimSpace(runtimeCfg.Name),
		Hostname:          strings.TrimSpace(runtimeCfg.Hostname),
		CloudflaredBin:    strings.TrimSpace(runtimeCfg.CloudflaredBin),
		ListenAddr:        strings.TrimSpace(runtimeCfg.ListenAddr),
		FeishuWebhookPath: strings.TrimSpace(runtimeCfg.FeishuWebhookPath),
	})
	if err != nil {
		return nil, err
	}
	if proc == nil {
		return nil, nil
	}
	return &daemonPublicTunnelProcess{inner: proc}, nil
}

func (p *daemonPublicTunnelProcess) Done() <-chan error {
	if p == nil {
		return nil
	}
	if p.done != nil {
		return p.done
	}
	if p.inner != nil {
		return p.inner.Done()
	}
	return p.done
}

func (p *daemonPublicTunnelProcess) Stop(timeout time.Duration) error {
	if p == nil {
		return nil
	}
	if p.inner == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = daemonPublicTunnelStopTimeout
	}
	return p.inner.Stop(timeout)
}

func buildDaemonPublicTunnelOriginURL(listenAddr string) (string, error) {
	return tunnelsvc.BuildOriginURL(listenAddr)
}

func buildDaemonPublicTunnelCloudflaredConfig(tunnelName, hostname, originURL, feishuWebhookPath string) (string, error) {
	return tunnelsvc.BuildCloudflaredConfig(tunnelName, hostname, originURL, feishuWebhookPath)
}

func buildDaemonPublicTunnelIngressPathPattern(rawPath string) (string, error) {
	return tunnelsvc.BuildIngressPathPattern(rawPath)
}

func writeDaemonPublicTunnelCloudflaredConfig(body string) (string, error) {
	return tunnelsvc.WriteCloudflaredConfig(body)
}
