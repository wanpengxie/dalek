package pm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

func TestCalculateHealthMetrics_AggregatesExpectedFields(t *testing.T) {
	svc, project, _ := newServiceForTest(t)
	db := project.DB
	now := time.Now().UTC()
	inside := now.Add(-2 * time.Hour)
	outside := now.Add(-10 * 24 * time.Hour)

	plannerTimeoutRun := createTaskRunForMetrics(t, db, contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "pm_planner_run",
		ProjectKey:         "demo",
		RequestID:          "planner-timeout-in",
		OrchestrationState: contracts.TaskFailed,
		ErrorCode:          "planner_timeout",
		CreatedAt:          inside,
		UpdatedAt:          inside,
	})
	createTaskRunForMetrics(t, db, contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "pm_planner_run",
		ProjectKey:         "demo",
		RequestID:          "planner-ok-in",
		OrchestrationState: contracts.TaskSucceeded,
		CreatedAt:          inside.Add(5 * time.Minute),
		UpdatedAt:          inside.Add(5 * time.Minute),
	})
	createTaskRunForMetrics(t, db, contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "pm_planner_run",
		ProjectKey:         "demo",
		RequestID:          "planner-timeout-out",
		OrchestrationState: contracts.TaskFailed,
		ErrorCode:          "planner_timeout",
		CreatedAt:          outside,
		UpdatedAt:          outside,
	})
	createTaskRunForMetrics(t, db, contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		RequestID:          "dispatch-failed-in",
		OrchestrationState: contracts.TaskFailed,
		ErrorCode:          "bootstrap_failed",
		CreatedAt:          inside,
		UpdatedAt:          inside,
	})
	createTaskRunForMetrics(t, db, contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		RequestID:          "dispatch-ok-in",
		OrchestrationState: contracts.TaskSucceeded,
		CreatedAt:          inside.Add(8 * time.Minute),
		UpdatedAt:          inside.Add(8 * time.Minute),
	})
	createTaskRunForMetrics(t, db, contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		RequestID:          "dispatch-failed-out",
		OrchestrationState: contracts.TaskFailed,
		ErrorCode:          "bootstrap_failed",
		CreatedAt:          outside,
		UpdatedAt:          outside,
	})

	createTaskEventForMetrics(t, db, contracts.TaskEvent{
		TaskRunID: plannerTimeoutRun.ID,
		EventType: "terminal_state_overridden",
		CreatedAt: inside,
		FromStateJSON: contracts.JSONMap{
			"orchestration_state": "failed",
		},
		ToStateJSON: contracts.JSONMap{
			"orchestration_state": "canceled",
		},
	})
	createTaskEventForMetrics(t, db, contracts.TaskEvent{
		TaskRunID:     plannerTimeoutRun.ID,
		EventType:     "duplicate_terminal_report",
		CreatedAt:     inside.Add(1 * time.Minute),
		FromStateJSON: contracts.JSONMap{},
		ToStateJSON:   contracts.JSONMap{},
	})
	createTaskEventForMetrics(t, db, contracts.TaskEvent{
		TaskRunID:     plannerTimeoutRun.ID,
		EventType:     "terminal_state_overridden",
		CreatedAt:     outside,
		FromStateJSON: contracts.JSONMap{},
		ToStateJSON:   contracts.JSONMap{},
	})

	createMergeItemForMetrics(t, db, contracts.MergeItem{
		Status:    contracts.MergeDiscarded,
		TicketID:  1,
		WorkerID:  1,
		Branch:    "feature/t1",
		CreatedAt: inside,
		UpdatedAt: inside,
	})
	createMergeItemForMetrics(t, db, contracts.MergeItem{
		Status:    contracts.MergeDiscarded,
		TicketID:  2,
		WorkerID:  2,
		Branch:    "feature/t2",
		CreatedAt: outside,
		UpdatedAt: outside,
	})

	createInboxForMetrics(t, db, contracts.InboxItem{
		Key:       "needs-user-in",
		Status:    contracts.InboxOpen,
		Severity:  contracts.InboxBlocker,
		Reason:    contracts.InboxNeedsUser,
		Title:     "needs user",
		CreatedAt: inside,
		UpdatedAt: inside,
	})
	createInboxForMetrics(t, db, contracts.InboxItem{
		Key:       "needs-user-out",
		Status:    contracts.InboxOpen,
		Severity:  contracts.InboxBlocker,
		Reason:    contracts.InboxNeedsUser,
		Title:     "needs user",
		CreatedAt: outside,
		UpdatedAt: outside,
	})

	writePlanGraphForMetrics(t, project.RepoRoot, contracts.FeatureGraph{
		Schema:    contracts.PMFeatureGraphSchemaV1,
		FeatureID: "feature-health",
		Goal:      "health metrics",
		Nodes: []contracts.FeatureNode{
			{ID: "integration-1", Type: contracts.FeatureNodeIntegration, Status: contracts.FeatureNodePending},
			{ID: "integration-2", Type: contracts.FeatureNodeIntegration, Status: contracts.FeatureNodeInProgress},
			{ID: "ticket-1", Type: contracts.FeatureNodeTicket, Status: contracts.FeatureNodeInProgress, TicketID: "1"},
		},
	})

	since := now.Add(-24 * time.Hour)
	until := now
	metrics, err := svc.CalculateHealthMetrics(context.Background(), HealthMetricsOptions{
		Since: &since,
		Until: &until,
	})
	if err != nil {
		t.Fatalf("CalculateHealthMetrics failed: %v", err)
	}

	// Planner metrics are now always zero (planner loop removed).
	if metrics.PlannerRunCount != 0 || metrics.PlannerTimeoutCount != 0 {
		t.Fatalf("unexpected planner counts (should be zero): %+v", metrics)
	}
	if metrics.WorkerRunCount != 2 || metrics.WorkerBootstrapFailureCount != 1 {
		t.Fatalf("unexpected worker-run counts: %+v", metrics)
	}
	if metrics.WorkerBootstrapFailureRate != 0.5 {
		t.Fatalf("unexpected worker_bootstrap_failure_rate: got=%v want=0.5", metrics.WorkerBootstrapFailureRate)
	}
	if metrics.TerminalStateConflictCount != 1 {
		t.Fatalf("unexpected terminal_state_conflict_count: got=%d want=1", metrics.TerminalStateConflictCount)
	}
	if metrics.DuplicateTerminalReportCount != 1 {
		t.Fatalf("unexpected duplicate_terminal_report_count: got=%d want=1", metrics.DuplicateTerminalReportCount)
	}
	if metrics.MergeDiscardCount != 1 {
		t.Fatalf("unexpected merge_discard_count: got=%d want=1", metrics.MergeDiscardCount)
	}
	if metrics.ManualInterventionCount != 1 {
		t.Fatalf("unexpected manual_intervention_count: got=%d want=1", metrics.ManualInterventionCount)
	}
	if metrics.IntegrationTicketCount != 2 {
		t.Fatalf("unexpected integration_ticket_count: got=%d want=2", metrics.IntegrationTicketCount)
	}
	if metrics.RealAcceptancePassRate != nil {
		t.Fatalf("real_acceptance_pass_rate should be nil before T27")
	}
}

func createTaskRunForMetrics(t *testing.T, db *gorm.DB, run contracts.TaskRun) contracts.TaskRun {
	t.Helper()
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("create task run failed: %v", err)
	}
	return run
}

func createTaskEventForMetrics(t *testing.T, db *gorm.DB, event contracts.TaskEvent) {
	t.Helper()
	if err := db.Create(&event).Error; err != nil {
		t.Fatalf("create task event failed: %v", err)
	}
}

func createMergeItemForMetrics(t *testing.T, db *gorm.DB, item contracts.MergeItem) {
	t.Helper()
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create merge item failed: %v", err)
	}
}

func createInboxForMetrics(t *testing.T, db *gorm.DB, item contracts.InboxItem) {
	t.Helper()
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create inbox item failed: %v", err)
	}
}

func writePlanGraphForMetrics(t *testing.T, repoRoot string, graph contracts.FeatureGraph) {
	t.Helper()
	path := filepath.Join(repoRoot, ".dalek", "pm", "plan.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir plan graph dir failed: %v", err)
	}
	raw, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan graph failed: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write plan graph failed: %v", err)
	}
}
