package app

import (
	"testing"
	"time"

	"dalek/internal/repo"
	runexecsvc "dalek/internal/services/runexecutor"
)

func TestBuildRunTargetCatalog_UsesProjectConfigTargets(t *testing.T) {
	catalog := buildRunTargetCatalog(repo.Config{
		RunTargets: map[string]repo.RunTargetConfig{
			"smoke": {
				Description: "Smoke suite",
				Command:     []string{"make", "smoke"},
				TimeoutMS:   45000,
			},
		},
	})

	spec, err := catalog.ResolveTarget("smoke")
	if err != nil {
		t.Fatalf("ResolveTarget failed: %v", err)
	}
	if spec.Description != "Smoke suite" {
		t.Fatalf("unexpected description: %q", spec.Description)
	}
	if len(spec.Command) != 2 || spec.Command[0] != "make" || spec.Command[1] != "smoke" {
		t.Fatalf("unexpected command: %+v", spec.Command)
	}
	if spec.Timeout != 45*time.Second {
		t.Fatalf("unexpected timeout: %s", spec.Timeout)
	}
}

func TestBuildRunTargetCatalog_FallsBackToDefaults(t *testing.T) {
	catalog := buildRunTargetCatalog(repo.Config{})

	spec, err := catalog.ResolveTarget("test")
	if err != nil {
		t.Fatalf("ResolveTarget default failed: %v", err)
	}
	if spec.Name != "test" {
		t.Fatalf("unexpected default target: %+v", spec)
	}
	if _, err := catalog.ResolveTarget("unknown"); err == nil {
		t.Fatalf("expected unknown target to fail")
	}

	if _, ok := catalog.(*runexecsvc.Service); !ok {
		t.Fatalf("expected runexecutor service catalog implementation")
	}
}
