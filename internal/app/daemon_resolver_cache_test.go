package app

import (
	"sync"
	"testing"
)

func TestProjectRegistry_OpenReturnsSameInstance(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	registry := NewProjectRegistry(home)

	first, err := registry.Open("demo")
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	second, err := registry.Open("demo")
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	if first != second {
		t.Fatalf("expected same project instance")
	}
}

func TestProjectRegistry_ConcurrentOpen(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	registry := NewProjectRegistry(home)

	warm, err := registry.Open("demo")
	if err != nil {
		t.Fatalf("warm open failed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := registry.Open("demo")
			if err != nil {
				errCh <- err
				return
			}
			if got != warm {
				errCh <- errRegistryOpenNotSingleton
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent open failed: %v", err)
	}
}

func TestProjectRegistry_CloseAll_Idempotent(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	registry := NewProjectRegistry(home)

	first, err := registry.Open("demo")
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	if got := registry.ListOpenProjectNames(); len(got) != 1 || got[0] != "demo" {
		t.Fatalf("unexpected opened list: %#v", got)
	}

	if err := registry.CloseAll(); err != nil {
		t.Fatalf("first close all failed: %v", err)
	}
	if err := registry.CloseAll(); err != nil {
		t.Fatalf("second close all should be idempotent: %v", err)
	}
	if got := registry.ListOpenProjectNames(); len(got) != 0 {
		t.Fatalf("registry should be empty after close all: %#v", got)
	}

	reopened, err := registry.Open("demo")
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	if reopened == first {
		t.Fatalf("expected reopen to create a new project instance")
	}
}

func TestDaemonGatewayProjectResolver_UsesRegistry(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	registry := NewProjectRegistry(home)
	resolver := newDaemonGatewayProjectResolver(home, registry)

	first, err := resolver.Resolve("demo")
	if err != nil {
		t.Fatalf("first resolve failed: %v", err)
	}
	second, err := resolver.Resolve("demo")
	if err != nil {
		t.Fatalf("second resolve failed: %v", err)
	}
	firstRuntime, ok := first.Runtime.(*daemonGatewayProjectRuntime)
	if !ok || firstRuntime == nil || firstRuntime.project == nil {
		t.Fatalf("unexpected runtime type: %T", first.Runtime)
	}
	secondRuntime, ok := second.Runtime.(*daemonGatewayProjectRuntime)
	if !ok || secondRuntime == nil || secondRuntime.project == nil {
		t.Fatalf("unexpected runtime type: %T", second.Runtime)
	}
	if firstRuntime.project != secondRuntime.project {
		t.Fatalf("expected resolver to reuse project from registry")
	}
}

func TestDaemonProjectResolver_UsesRegistry(t *testing.T) {
	home, _ := newIntegrationHomeProject(t)
	registry := NewProjectRegistry(home)
	resolver := newDaemonProjectResolver(home, registry)

	first, err := resolver.OpenProject("demo")
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	second, err := resolver.OpenProject("demo")
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	firstAdapter, ok := first.(*daemonProjectAdapter)
	if !ok || firstAdapter == nil || firstAdapter.project == nil {
		t.Fatalf("unexpected project type: %T", first)
	}
	secondAdapter, ok := second.(*daemonProjectAdapter)
	if !ok || secondAdapter == nil || secondAdapter.project == nil {
		t.Fatalf("unexpected project type: %T", second)
	}
	if firstAdapter.project != secondAdapter.project {
		t.Fatalf("expected resolver to reuse same project instance")
	}
}

var errRegistryOpenNotSingleton = &registryOpenNotSingletonError{}

type registryOpenNotSingletonError struct{}

func (e *registryOpenNotSingletonError) Error() string {
	return "project registry returned different instance"
}
