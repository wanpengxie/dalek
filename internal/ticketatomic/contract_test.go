package ticketatomic

import (
	"errors"
	"strings"
	"testing"

	"dalek/internal/contracts"
)

func TestResolveCreateTargetRef(t *testing.T) {
	t.Run("explicit target ref wins", func(t *testing.T) {
		got, err := ResolveCreateTargetRef("release/v1", "", errors.New("detached HEAD"))
		if err != nil {
			t.Fatalf("ResolveCreateTargetRef failed: %v", err)
		}
		if got != "refs/heads/release/v1" {
			t.Fatalf("unexpected target ref: %q", got)
		}
	})

	t.Run("current branch freezes target ref", func(t *testing.T) {
		got, err := ResolveCreateTargetRef("", "feature/current", nil)
		if err != nil {
			t.Fatalf("ResolveCreateTargetRef failed: %v", err)
		}
		if got != "refs/heads/feature/current" {
			t.Fatalf("unexpected target ref: %q", got)
		}
	})

	t.Run("detached head without target ref is rejected", func(t *testing.T) {
		_, err := ResolveCreateTargetRef("", "", errors.New("detached HEAD"))
		if err == nil || !strings.Contains(err.Error(), "未提供 --target-ref") {
			t.Fatalf("expected detached head error, got=%v", err)
		}
	})
}

func TestResolveCreateTargetRefWithForce(t *testing.T) {
	t.Run("mismatch without force is rejected", func(t *testing.T) {
		_, err := ResolveCreateTargetRefWithForce("refs/heads/main", "feature/x", nil, false)
		if err == nil || !strings.Contains(err.Error(), "target-ref 不匹配") {
			t.Fatalf("expected mismatch error, got=%v", err)
		}
	})

	t.Run("mismatch with force succeeds", func(t *testing.T) {
		got, err := ResolveCreateTargetRefWithForce("refs/heads/main", "feature/x", nil, true)
		if err != nil {
			t.Fatalf("ResolveCreateTargetRefWithForce failed: %v", err)
		}
		if got != "refs/heads/main" {
			t.Fatalf("unexpected target ref: %q", got)
		}
	})

	t.Run("matching ref without force succeeds", func(t *testing.T) {
		got, err := ResolveCreateTargetRefWithForce("refs/heads/feature/x", "feature/x", nil, false)
		if err != nil {
			t.Fatalf("ResolveCreateTargetRefWithForce failed: %v", err)
		}
		if got != "refs/heads/feature/x" {
			t.Fatalf("unexpected target ref: %q", got)
		}
	})

	t.Run("short name matching without force succeeds", func(t *testing.T) {
		got, err := ResolveCreateTargetRefWithForce("main", "main", nil, false)
		if err != nil {
			t.Fatalf("ResolveCreateTargetRefWithForce failed: %v", err)
		}
		if got != "refs/heads/main" {
			t.Fatalf("unexpected target ref: %q", got)
		}
	})

	t.Run("explicit ref with detached HEAD skips mismatch check", func(t *testing.T) {
		got, err := ResolveCreateTargetRefWithForce("release/v1", "", errors.New("detached HEAD"), false)
		if err != nil {
			t.Fatalf("ResolveCreateTargetRefWithForce failed: %v", err)
		}
		if got != "refs/heads/release/v1" {
			t.Fatalf("unexpected target ref: %q", got)
		}
	})

	t.Run("no explicit ref falls back to current branch", func(t *testing.T) {
		got, err := ResolveCreateTargetRefWithForce("", "feature/current", nil, false)
		if err != nil {
			t.Fatalf("ResolveCreateTargetRefWithForce failed: %v", err)
		}
		if got != "refs/heads/feature/current" {
			t.Fatalf("unexpected target ref: %q", got)
		}
	})
}

func TestResolveStartBase(t *testing.T) {
	t.Run("frozen target ref becomes base branch", func(t *testing.T) {
		res, err := ResolveStartBase(contracts.Ticket{ID: 1, TargetBranch: "refs/heads/main"}, "", "feature/current", nil)
		if err != nil {
			t.Fatalf("ResolveStartBase failed: %v", err)
		}
		if res.BaseBranch != "refs/heads/main" || res.FreezeTargetRef != "" {
			t.Fatalf("unexpected resolution: %+v", res)
		}
	})

	t.Run("mismatched base is rejected for frozen ticket", func(t *testing.T) {
		_, err := ResolveStartBase(contracts.Ticket{ID: 2, TargetBranch: "refs/heads/main"}, "release/v1", "main", nil)
		if err == nil || !strings.Contains(err.Error(), "与 ticket.target_ref=refs/heads/main 不一致") {
			t.Fatalf("expected mismatch error, got=%v", err)
		}
	})

	t.Run("legacy empty target freezes current branch once", func(t *testing.T) {
		res, err := ResolveStartBase(contracts.Ticket{ID: 3}, "", "feature/current", nil)
		if err != nil {
			t.Fatalf("ResolveStartBase failed: %v", err)
		}
		if res.BaseBranch != "refs/heads/feature/current" || res.FreezeTargetRef != "refs/heads/feature/current" {
			t.Fatalf("unexpected resolution: %+v", res)
		}
	})

	t.Run("legacy empty target rejects mismatched requested base", func(t *testing.T) {
		_, err := ResolveStartBase(contracts.Ticket{ID: 4}, "release/v1", "main", nil)
		if err == nil || !strings.Contains(err.Error(), "首次冻结只能使用当前分支 refs/heads/main") {
			t.Fatalf("expected legacy mismatch error, got=%v", err)
		}
	})

	t.Run("integration ticket without target ref is rejected", func(t *testing.T) {
		_, err := ResolveStartBase(contracts.Ticket{ID: 5, Label: "integration"}, "", "main", nil)
		if err == nil || !strings.Contains(err.Error(), "integration ticket t5 缺少有效 target_ref") {
			t.Fatalf("expected integration target error, got=%v", err)
		}
	})
}
