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
