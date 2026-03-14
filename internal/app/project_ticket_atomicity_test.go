package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"dalek/internal/contracts"
)

func TestProjectCreateTicket_DetachedHeadRequiresExplicitTargetRef(t *testing.T) {
	p := newIntegrationProject(t)
	repoRoot := p.RepoRoot()
	head := strings.TrimSpace(mustRunGitForProjectTicketAtomicity(t, repoRoot, "rev-parse", "HEAD"))
	mustRunGitForProjectTicketAtomicity(t, repoRoot, "checkout", "--detach", head)

	if _, err := p.CreateTicketWithDescription(context.Background(), "detached-create", "detached create should fail"); err == nil || !strings.Contains(err.Error(), "未提供 --target-ref") {
		t.Fatalf("expected detached HEAD rejection, got=%v", err)
	}

	tk, err := p.CreateTicketWithDescriptionAndLabelAndPriorityAndTarget(
		context.Background(),
		"detached-create-explicit",
		"detached create with explicit target should succeed",
		"",
		contracts.TicketPriorityNone,
		"release/v1",
	)
	if err != nil {
		t.Fatalf("explicit target create failed: %v", err)
	}
	if strings.TrimSpace(tk.TargetBranch) != "refs/heads/release/v1" {
		t.Fatalf("unexpected explicit target branch: %q", tk.TargetBranch)
	}
}

func mustRunGitForProjectTicketAtomicity(t *testing.T, repoRoot string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
