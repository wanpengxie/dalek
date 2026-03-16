package runexecutor

import (
	"strings"
	"testing"
)

func TestService_ResolveTarget_AcceptsDefaultTargets(t *testing.T) {
	svc := New(nil)

	spec, err := svc.ResolveTarget(" test ")
	if err != nil {
		t.Fatalf("ResolveTarget failed: %v", err)
	}
	if spec.Name != "test" {
		t.Fatalf("unexpected target name: %q", spec.Name)
	}
	if len(spec.Command) != 3 || spec.Command[0] != "go" {
		t.Fatalf("unexpected command template: %+v", spec.Command)
	}
}

func TestService_ResolveTarget_RejectsShellInjectionLikeTargets(t *testing.T) {
	svc := New(nil)

	cases := []string{
		"go test ./...",
		"test && rm -rf /",
		"test;echo pwned",
		"../test",
	}
	for _, raw := range cases {
		_, err := svc.ResolveTarget(raw)
		if err == nil {
			t.Fatalf("expected target %q to be rejected", raw)
		}
		if !strings.Contains(err.Error(), "verify_target") {
			t.Fatalf("unexpected error for %q: %v", raw, err)
		}
	}
}

func TestService_ListTargets_Sorted(t *testing.T) {
	svc := New([]TargetSpec{
		{Name: "build", Command: []string{"go", "build", "./..."}},
		{Name: "test", Command: []string{"go", "test", "./..."}},
		{Name: "lint", Command: []string{"golangci-lint", "run"}},
	})

	targets := svc.ListTargets()
	if len(targets) != 3 {
		t.Fatalf("unexpected target count: %d", len(targets))
	}
	if targets[0].Name != "build" || targets[1].Name != "lint" || targets[2].Name != "test" {
		t.Fatalf("unexpected target order: %+v", targets)
	}
}
