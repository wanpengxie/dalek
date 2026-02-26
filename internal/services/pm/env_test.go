package pm

import (
	"testing"

	"dalek/internal/repo"
	"dalek/internal/services/core"
	"dalek/internal/store"
)

func TestBuildBaseEnvConsistency(t *testing.T) {
	t.Parallel()

	p := &core.Project{
		Key:      " project-a ",
		RepoRoot: " /tmp/repo ",
		Layout: repo.Layout{
			DBPath: " /tmp/repo/dalek.db ",
		},
	}
	ticket := store.Ticket{
		ID:          12,
		Title:       "  fix t12  ",
		Description: "  tighten pm env  ",
	}
	worker := store.Worker{
		ID:           34,
		WorktreePath: " /tmp/worktree/t12 ",
		Branch:       " feat/t12 ",
		TmuxSocket:   " dalek-sock ",
		TmuxSession:  " ts-pm-w34 ",
	}

	got := buildBaseEnv(p, ticket, worker)
	expected := map[string]string{
		envProjectKey:        "project-a",
		envRepoRoot:          "/tmp/repo",
		envDBPath:            "/tmp/repo/dalek.db",
		envWorktreePath:      "/tmp/worktree/t12",
		envBranch:            "feat/t12",
		envTmuxSocket:        "dalek-sock",
		envTmuxSession:       "ts-pm-w34",
		envTicketID:          "12",
		envWorkerID:          "34",
		envTicketTitle:       "fix t12",
		envTicketDescription: "tighten pm env",
	}

	if len(got) != len(expected) {
		t.Fatalf("buildBaseEnv key count mismatch: got=%d want=%d", len(got), len(expected))
	}
	for k, want := range expected {
		if got[k] != want {
			t.Fatalf("buildBaseEnv[%s] = %q, want %q", k, got[k], want)
		}
	}
}
