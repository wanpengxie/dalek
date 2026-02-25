package app

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"dalek/internal/repo"
)

func TestDaemonGatewayProjectResolver_CacheHitWithinTTL(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	resolver := newDaemonGatewayProjectResolver(home)
	resolver.ttl = 5 * time.Second

	first, err := resolver.Resolve("demo")
	if err != nil {
		t.Fatalf("first resolve failed: %v", err)
	}
	second, err := resolver.Resolve("demo")
	if err != nil {
		t.Fatalf("second resolve failed: %v", err)
	}
	if first != second {
		t.Fatalf("expected cache hit within ttl")
	}
}

func TestDaemonGatewayProjectResolver_ReloadAfterTTLExpiry(t *testing.T) {
	home, p := newIntegrationHomeProject(t)
	resolver := newDaemonGatewayProjectResolver(home)
	resolver.ttl = 20 * time.Millisecond

	first, err := resolver.Resolve("demo")
	if err != nil {
		t.Fatalf("first resolve failed: %v", err)
	}
	firstRuntime, ok := first.Runtime.(*daemonGatewayProjectRuntime)
	if !ok || firstRuntime == nil || firstRuntime.project == nil {
		t.Fatalf("unexpected runtime type: %T", first.Runtime)
	}

	cfg := p.core.Config
	cfg.GatewayAgent.TurnTimeoutMS = 2750
	cfgPath := filepath.Join(p.ProjectDir(), "config.json")
	if err := repo.WriteConfigAtomic(cfgPath, cfg); err != nil {
		t.Fatalf("WriteConfigAtomic failed: %v", err)
	}

	time.Sleep(resolver.ttl + 15*time.Millisecond)

	second, err := resolver.Resolve("demo")
	if err != nil {
		t.Fatalf("second resolve failed: %v", err)
	}
	if first == second {
		t.Fatalf("expected cache reload after ttl expiry")
	}
	secondRuntime, ok := second.Runtime.(*daemonGatewayProjectRuntime)
	if !ok || secondRuntime == nil || secondRuntime.project == nil {
		t.Fatalf("unexpected runtime type after reload: %T", second.Runtime)
	}
	if firstRuntime.project == secondRuntime.project {
		t.Fatalf("expected runtime project reloaded after ttl expiry")
	}
	if got := secondRuntime.project.GatewayTurnTimeout(); got != 2750*time.Millisecond {
		t.Fatalf("gateway timeout not reloaded, got=%s", got)
	}
}

func TestDaemonGatewayProjectResolver_ConcurrentResolve(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	resolver := newDaemonGatewayProjectResolver(home)
	resolver.ttl = 5 * time.Second

	warm, err := resolver.Resolve("demo")
	if err != nil {
		t.Fatalf("warm resolve failed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := resolver.Resolve("demo")
			if err != nil {
				errCh <- err
				return
			}
			if got != warm {
				errCh <- errCacheMissWithinTTL
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent resolve failed: %v", err)
	}
}

func TestDaemonProjectResolver_CacheHitWithinTTL(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	resolver := newDaemonProjectResolver(home)
	resolver.ttl = 5 * time.Second

	first, err := resolver.OpenProject("demo")
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	second, err := resolver.OpenProject("demo")
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}

	firstAdapter, ok := first.(*daemonProjectAdapter)
	if !ok {
		t.Fatalf("unexpected project type: %T", first)
	}
	secondAdapter, ok := second.(*daemonProjectAdapter)
	if !ok {
		t.Fatalf("unexpected project type: %T", second)
	}
	if firstAdapter != secondAdapter {
		t.Fatalf("expected cache hit within ttl")
	}
}

func TestDaemonProjectResolver_ReloadAfterTTLExpiry(t *testing.T) {
	home, p := newIntegrationHomeProject(t)
	resolver := newDaemonProjectResolver(home)
	resolver.ttl = 20 * time.Millisecond

	first, err := resolver.OpenProject("demo")
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	firstAdapter, ok := first.(*daemonProjectAdapter)
	if !ok || firstAdapter == nil || firstAdapter.project == nil {
		t.Fatalf("unexpected project type: %T", first)
	}

	cfg := p.core.Config
	cfg.WorkerAgent.Provider = "claude"
	cfg.WorkerAgent.Model = "opus"
	cfg.WorkerAgent.ReasoningEffort = ""
	cfgPath := filepath.Join(p.ProjectDir(), "config.json")
	if err := repo.WriteConfigAtomic(cfgPath, cfg); err != nil {
		t.Fatalf("WriteConfigAtomic failed: %v", err)
	}

	time.Sleep(resolver.ttl + 15*time.Millisecond)

	second, err := resolver.OpenProject("demo")
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	secondAdapter, ok := second.(*daemonProjectAdapter)
	if !ok || secondAdapter == nil || secondAdapter.project == nil {
		t.Fatalf("unexpected project type after reload: %T", second)
	}
	if firstAdapter == secondAdapter {
		t.Fatalf("expected cache reload after ttl expiry")
	}
	if got := secondAdapter.project.core.Config.WorkerAgent.Provider; got != "claude" {
		t.Fatalf("worker provider not reloaded, got=%q", got)
	}
}

func TestDaemonProjectResolver_ConcurrentOpenProject(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	resolver := newDaemonProjectResolver(home)
	resolver.ttl = 5 * time.Second

	warm, err := resolver.OpenProject("demo")
	if err != nil {
		t.Fatalf("warm open failed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := resolver.OpenProject("demo")
			if err != nil {
				errCh <- err
				return
			}
			if got != warm {
				errCh <- errCacheMissWithinTTL
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent open failed: %v", err)
	}
}

var errCacheMissWithinTTL = &resolverCacheMissWithinTTLError{}

type resolverCacheMissWithinTTLError struct{}

func (e *resolverCacheMissWithinTTLError) Error() string {
	return "unexpected cache miss within ttl"
}
