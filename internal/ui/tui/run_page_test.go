package tui

import (
	"strings"
	"testing"

	"dalek/internal/app"
	"dalek/internal/contracts"
)

func TestRunModel_RenderDetail_IncludesTaskStatusSummary(t *testing.T) {
	m := newRunModel(nil, "demo")
	m.tableRows = []app.RunView{{
		RunID:                     7,
		RunStatus:                 contracts.RunSucceeded,
		RequestID:                 "req-7",
		VerifyTarget:              "test",
		SnapshotID:                "snap-7",
		BaseCommit:                "abc123",
		SourceWorkspaceGeneration: "wg-7",
	}}
	m.statuses = map[uint]app.TaskStatus{
		7: {
			RuntimeSummary:    "verify accepted for target=test",
			SemanticMilestone: "verify_succeeded",
			LastEventType:     "run_artifact_upload_failed",
			LastEventNote:     "upload failed",
		},
	}
	m.routes = map[uint]app.TaskRouteInfo{
		7: {
			Role:        "run",
			RoleSource:  "auto_route_prompt",
			RouteReason: "prompt matched verify/test keywords",
			RouteMode:   "remote",
			RouteTarget: "http://c.example",
			RemoteRunID: 701,
		},
	}
	m.table.SetRows(runRows(m.tableRows))

	detail := m.renderDetail()
	for _, want := range []string{
		"summary:",
		"verify accepted for target=test",
		"milestone:",
		"verify_succeeded",
		"last_event:",
		"run_artifact_upload_failed",
		"last_note:",
		"upload failed",
		"role_source:",
		"auto_route_prompt",
		"route_reason:",
		"prompt matched verify/test keywords",
		"remote_run_id:",
		"701",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail missing %q in %q", want, detail)
		}
	}
}
