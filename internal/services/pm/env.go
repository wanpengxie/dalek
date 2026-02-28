package pm

import (
	"fmt"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
)

func buildBaseEnv(p *core.Project, t contracts.Ticket, w contracts.Worker) map[string]string {
	if p == nil {
		return map[string]string{
			envTicketID:          fmt.Sprintf("%d", t.ID),
			envWorkerID:          fmt.Sprintf("%d", w.ID),
			envTicketTitle:       strings.TrimSpace(t.Title),
			envTicketDescription: strings.TrimSpace(t.Description),
		}
	}

	return map[string]string{
		envProjectKey:        strings.TrimSpace(p.Key),
		envRepoRoot:          strings.TrimSpace(p.RepoRoot),
		envDBPath:            strings.TrimSpace(p.DBPath()),
		envWorktreePath:      strings.TrimSpace(w.WorktreePath),
		envBranch:            strings.TrimSpace(w.Branch),
		envTicketID:          fmt.Sprintf("%d", t.ID),
		envWorkerID:          fmt.Sprintf("%d", w.ID),
		envTicketTitle:       strings.TrimSpace(t.Title),
		envTicketDescription: strings.TrimSpace(t.Description),
	}
}
