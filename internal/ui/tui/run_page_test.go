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
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail missing %q in %q", want, detail)
		}
	}
}
