package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"

	"gorm.io/gorm"
)

func TestCLI_E2E_TicketCheckShowsLifecycleExplanation(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")

	h, err := app.OpenHome(home)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	project, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	t.Cleanup(func() {
		_ = project.Close()
	})

	ctx := context.Background()
	tk, err := project.CreateTicketWithDescription(ctx, "ticket-check", "ticket-check description")
	if err != nil {
		t.Fatalf("CreateTicketWithDescription failed: %v", err)
	}
	db, err := project.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	now := time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC)
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("ticket_id = ?", tk.ID).Delete(&contracts.TicketLifecycleEvent{}).Error; err != nil {
			return err
		}
		workerID := uint(7)
		taskRunID := uint(77)
		events := []contracts.TicketLifecycleEvent{
			{
				CreatedAt:      now,
				TicketID:       tk.ID,
				Sequence:       1,
				EventType:      contracts.TicketLifecycleCreated,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorUser,
				IdempotencyKey: "test:created",
			},
			{
				CreatedAt:      now.Add(time.Second),
				TicketID:       tk.ID,
				Sequence:       2,
				EventType:      contracts.TicketLifecycleStartRequested,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorUser,
				IdempotencyKey: "test:start_requested",
				PayloadJSON: contracts.JSONMapFromAny(map[string]any{
					"ticket_id": tk.ID,
					"worker_id": workerID,
				}),
			},
			{
				CreatedAt:      now.Add(2 * time.Second),
				TicketID:       tk.ID,
				Sequence:       3,
				EventType:      contracts.TicketLifecycleActivated,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorSystem,
				IdempotencyKey: "test:activated",
				PayloadJSON: contracts.JSONMapFromAny(map[string]any{
					"ticket_id":   tk.ID,
					"worker_id":   workerID,
					"task_run_id": taskRunID,
				}),
			},
			{
				CreatedAt:      now.Add(3 * time.Second),
				TicketID:       tk.ID,
				Sequence:       4,
				EventType:      contracts.TicketLifecycleExecutionLost,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorSystem,
				IdempotencyKey: "test:execution_lost",
				PayloadJSON: contracts.JSONMapFromAny(map[string]any{
					"ticket_id":        tk.ID,
					"worker_id":        workerID,
					"task_run_id":      taskRunID,
					"failure_code":     "runtime_stalled",
					"observation_kind": "visibility_timeout",
					"reason":           "最近活动距今 11m0s（阈值 10m0s）",
					"last_seen_at":     now.Add(-11 * time.Minute),
				}),
			},
			{
				CreatedAt:      now.Add(4 * time.Second),
				TicketID:       tk.ID,
				Sequence:       5,
				EventType:      contracts.TicketLifecycleExecutionEscalated,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorSystem,
				IdempotencyKey: "test:execution_escalated",
				PayloadJSON: contracts.JSONMapFromAny(map[string]any{
					"ticket_id":        tk.ID,
					"worker_id":        workerID,
					"task_run_id":      taskRunID,
					"failure_code":     "runtime_stalled",
					"observation_kind": "visibility_timeout",
					"reason":           "最近活动距今 11m0s（阈值 10m0s）",
					"retry_count":      3,
					"blocked_reason":   "system_incident",
					"last_seen_at":     now.Add(-11 * time.Minute),
				}),
			},
		}
		for _, ev := range events {
			ev.WorkerID = &workerID
			ev.TaskRunID = &taskRunID
			if ev.EventType == contracts.TicketLifecycleCreated {
				ev.WorkerID = nil
				ev.TaskRunID = nil
			}
			if ev.EventType == contracts.TicketLifecycleStartRequested {
				ev.TaskRunID = nil
			}
			if err := tx.Create(&ev).Error; err != nil {
				return err
			}
		}
		return tx.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
			"workflow_status": contracts.TicketBlocked,
			"updated_at":      now.Add(5 * time.Second),
		}).Error
	}); err != nil {
		t.Fatalf("seed lifecycle chain failed: %v", err)
	}

	jsonOut, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "check", "--ticket", "1", "-o", "json")
	var checkResp struct {
		Schema     string `json:"schema"`
		Consistent bool   `json:"consistent"`
		Rebuilt    struct {
			WorkflowStatus string `json:"workflow_status"`
			Explanation    struct {
				EventType       string `json:"event_type"`
				BlockedReason   string `json:"blocked_reason"`
				ObservationKind string `json:"observation_kind"`
				FailureCode     string `json:"failure_code"`
				RetryCount      int    `json:"retry_count"`
			} `json:"explanation"`
		} `json:"rebuilt"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOut)), &checkResp); err != nil {
		t.Fatalf("decode ticket check json failed: %v\nraw=%s", err, jsonOut)
	}
	if checkResp.Schema != "dalek.ticket.lifecycle_check.v1" {
		t.Fatalf("unexpected schema: %q", checkResp.Schema)
	}
	if !checkResp.Consistent {
		t.Fatalf("expected consistent lifecycle check, raw=%s", jsonOut)
	}
	if checkResp.Rebuilt.WorkflowStatus != string(contracts.TicketBlocked) {
		t.Fatalf("expected rebuilt workflow blocked, got=%s", checkResp.Rebuilt.WorkflowStatus)
	}
	if checkResp.Rebuilt.Explanation.EventType != string(contracts.TicketLifecycleExecutionEscalated) {
		t.Fatalf("expected explanation event_type=%s, got=%s", contracts.TicketLifecycleExecutionEscalated, checkResp.Rebuilt.Explanation.EventType)
	}
	if checkResp.Rebuilt.Explanation.BlockedReason != "system_incident" {
		t.Fatalf("expected blocked_reason=system_incident, got=%q", checkResp.Rebuilt.Explanation.BlockedReason)
	}
	if checkResp.Rebuilt.Explanation.ObservationKind != "visibility_timeout" {
		t.Fatalf("expected observation_kind=visibility_timeout, got=%q", checkResp.Rebuilt.Explanation.ObservationKind)
	}
	if checkResp.Rebuilt.Explanation.FailureCode != "runtime_stalled" {
		t.Fatalf("expected failure_code=runtime_stalled, got=%q", checkResp.Rebuilt.Explanation.FailureCode)
	}
	if checkResp.Rebuilt.Explanation.RetryCount != 3 {
		t.Fatalf("expected retry_count=3, got=%d", checkResp.Rebuilt.Explanation.RetryCount)
	}

	textOut, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "check", "--ticket", "1")
	if !strings.Contains(textOut, "rebuilt_reason: event=ticket.execution_escalated blocked_reason=system_incident observation=visibility_timeout failure=runtime_stalled retry=3") {
		t.Fatalf("expected rebuilt_reason in text output, got:\n%s", textOut)
	}
}
