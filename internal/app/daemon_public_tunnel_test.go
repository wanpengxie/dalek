package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tunnelsvc "dalek/internal/services/tunnel"
)

func TestNewDaemonPublicTunnelRuntimeConfig(t *testing.T) {
	t.Run("empty config keeps tunnel disabled", func(t *testing.T) {
		got, err := newDaemonPublicTunnelRuntimeConfig("", false, "", "", "", "127.0.0.1:18080", "/feishu/webhook/x")
		if err != nil {
			t.Fatalf("newDaemonPublicTunnelRuntimeConfig failed: %v", err)
		}
		if got.Enabled {
			t.Fatalf("expected tunnel disabled, got enabled")
		}
	})

	t.Run("name only should fail", func(t *testing.T) {
		_, err := newDaemonPublicTunnelRuntimeConfig("cloudflare_tunnel", true, "gw-prod", "", "", "127.0.0.1:18080", "/feishu/webhook/x")
		if err == nil {
			t.Fatalf("expected error for name-only config")
		}
	})

	t.Run("hostname only should fail", func(t *testing.T) {
		_, err := newDaemonPublicTunnelRuntimeConfig("cloudflare_tunnel", true, "", "gw.example.com", "", "127.0.0.1:18080", "/feishu/webhook/x")
		if err == nil {
			t.Fatalf("expected error for hostname-only config")
		}
	})

	t.Run("full config should enable and default bin", func(t *testing.T) {
		got, err := newDaemonPublicTunnelRuntimeConfig("cloudflare_tunnel", true, "gw-prod", "gw.example.com", "", "127.0.0.1:18080", "/feishu/webhook/x")
		if err != nil {
			t.Fatalf("newDaemonPublicTunnelRuntimeConfig failed: %v", err)
		}
		if !got.Enabled {
			t.Fatalf("expected tunnel enabled")
		}
		if got.CloudflaredBin != defaultDaemonCloudflaredBinary {
			t.Fatalf("unexpected default cloudflared bin: got=%q", got.CloudflaredBin)
		}
	})
}

func TestBuildDaemonPublicTunnelOriginURL(t *testing.T) {
	tests := []struct {
		name   string
		listen string
		want   string
	}{
		{name: "loopback", listen: "127.0.0.1:18080", want: "http://127.0.0.1:18080"},
		{name: "zero-host", listen: ":18080", want: "http://127.0.0.1:18080"},
		{name: "all-ipv4", listen: "0.0.0.0:18080", want: "http://127.0.0.1:18080"},
		{name: "all-ipv6", listen: "[::]:18080", want: "http://127.0.0.1:18080"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildDaemonPublicTunnelOriginURL(tc.listen)
			if err != nil {
				t.Fatalf("buildDaemonPublicTunnelOriginURL failed: %v", err)
			}
			if got != tc.want {
				t.Fatalf("origin url mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestBuildDaemonPublicTunnelCloudflaredConfig_OnlyAllowsFeishu(t *testing.T) {
	feishuPath := "/feishu/events/cli_xxx"
	feishuPattern, _ := buildDaemonPublicTunnelIngressPathPattern(feishuPath)

	body, err := buildDaemonPublicTunnelCloudflaredConfig("gw-prod", "gw.example.com", "http://127.0.0.1:18080", feishuPath)
	if err != nil {
		t.Fatalf("buildDaemonPublicTunnelCloudflaredConfig failed: %v", err)
	}
	if strings.Count(body, "hostname: 'gw.example.com'") != 1 {
		t.Fatalf("expected 1 ingress hostname rule, got body:\n%s", body)
	}
	if !strings.Contains(body, "path: '"+feishuPattern+"'") {
		t.Fatalf("feishu ingress pattern missing, body:\n%s", body)
	}
	if !strings.Contains(body, "- service: 'http_status:404'") {
		t.Fatalf("404 fallback ingress missing, body:\n%s", body)
	}
}

func TestDaemonPublicTunnelSupervisor_CircuitBreakerOnStartFailures(t *testing.T) {
	var logs bytes.Buffer
	startCalls := 0
	waitCalls := 0
	supervisor := &daemonPublicTunnelSupervisor{
		RuntimeConfig: daemonPublicTunnelRuntimeConfig{
			Enabled:  true,
			Hostname: "gw.example.com",
		},
		Logger:                 slog.New(slog.NewTextHandler(&logs, nil)),
		MaxConsecutiveFailures: 3,
		StartFn: func(daemonPublicTunnelRuntimeConfig) (tunnelsvc.ProcessHandle, error) {
			startCalls++
			return nil, errors.New("invalid tunnel config")
		},
		WaitFn: func(context.Context, time.Duration) bool {
			waitCalls++
			return true
		},
	}

	supervisor.Run(context.Background())

	if !supervisor.CircuitOpen() {
		t.Fatalf("expected circuit open after max failures")
	}
	if startCalls != 3 {
		t.Fatalf("unexpected start calls: got=%d want=%d", startCalls, 3)
	}
	if waitCalls != 2 {
		t.Fatalf("unexpected wait calls: got=%d want=%d", waitCalls, 2)
	}
	if !strings.Contains(logs.String(), "cloudflared 熔断: 连续失败 3 次") {
		t.Fatalf("circuit breaker log missing, logs:\n%s", logs.String())
	}
}

func TestDaemonPublicTunnelSupervisor_ResetFailuresAfterStableRun(t *testing.T) {
	var logs bytes.Buffer
	attempt := 0
	waitCalls := 0
	base := time.Unix(0, 0)
	nowCalls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	supervisor := &daemonPublicTunnelSupervisor{
		RuntimeConfig: daemonPublicTunnelRuntimeConfig{
			Enabled:  true,
			Hostname: "gw.example.com",
		},
		Logger:                 slog.New(slog.NewTextHandler(&logs, nil)),
		MaxConsecutiveFailures: 2,
		StartFn: func(daemonPublicTunnelRuntimeConfig) (tunnelsvc.ProcessHandle, error) {
			attempt++
			switch attempt {
			case 1:
				return nil, errors.New("start failed once")
			case 2:
				return newExitedDaemonPublicTunnelProcessForTest(errors.New("stable process exited")), nil
			case 3:
				return nil, errors.New("start failed after stable run")
			default:
				t.Fatalf("unexpected start attempt: %d", attempt)
				return nil, nil
			}
		},
		WaitFn: func(context.Context, time.Duration) bool {
			waitCalls++
			if waitCalls >= 3 {
				cancel()
				return false
			}
			return true
		},
		NowFn: func() time.Time {
			nowCalls++
			switch nowCalls {
			case 1:
				return base
			case 2:
				return base.Add(daemonPublicTunnelStableThreshold + time.Second)
			default:
				return base.Add(daemonPublicTunnelStableThreshold + time.Second)
			}
		},
	}

	supervisor.Run(ctx)

	if supervisor.CircuitOpen() {
		t.Fatalf("expected circuit to stay closed after stable reset")
	}
	if attempt != 3 {
		t.Fatalf("unexpected start attempts: got=%d want=%d", attempt, 3)
	}
	if waitCalls != 3 {
		t.Fatalf("expected third wait (no trip after reset), got=%d", waitCalls)
	}
	if supervisor.ConsecutiveFailures() != 1 {
		t.Fatalf("unexpected consecutive failures after reset: got=%d want=%d", supervisor.ConsecutiveFailures(), 1)
	}
	if strings.Contains(logs.String(), "cloudflared 熔断") {
		t.Fatalf("unexpected circuit breaker log, logs:\n%s", logs.String())
	}
}

func TestDaemonPublicTunnelSupervisor_NormalRunDoesNotTripCircuit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var startCalls atomic.Int32
	var waitCalls atomic.Int32
	supervisor := &daemonPublicTunnelSupervisor{
		RuntimeConfig: daemonPublicTunnelRuntimeConfig{
			Enabled:  true,
			Hostname: "gw.example.com",
		},
		MaxConsecutiveFailures: 2,
		StartFn: func(daemonPublicTunnelRuntimeConfig) (tunnelsvc.ProcessHandle, error) {
			startCalls.Add(1)
			return &daemonPublicTunnelProcess{
				done: make(chan error),
			}, nil
		},
		WaitFn: func(context.Context, time.Duration) bool {
			waitCalls.Add(1)
			return true
		},
	}

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		supervisor.Run(ctx)
	}()

	deadline := time.After(1 * time.Second)
	for startCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("supervisor did not start process in time")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()

	select {
	case <-runDone:
	case <-time.After(1 * time.Second):
		t.Fatalf("supervisor did not stop after context cancellation")
	}

	if supervisor.CircuitOpen() {
		t.Fatalf("expected circuit open=false in normal run")
	}
	if startCalls.Load() != 1 {
		t.Fatalf("unexpected start calls: got=%d want=%d", startCalls.Load(), 1)
	}
	if waitCalls.Load() != 0 {
		t.Fatalf("unexpected wait calls in normal run: got=%d want=%d", waitCalls.Load(), 0)
	}
}

func newExitedDaemonPublicTunnelProcessForTest(err error) *daemonPublicTunnelProcess {
	done := make(chan error, 1)
	done <- err
	close(done)
	return &daemonPublicTunnelProcess{done: done}
}
