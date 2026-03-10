package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

func TestArchiveTicket_RejectsNeedsMerge(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "archive-needs-merge")
	setTicketArchiveState(t, p.DB, tk.ID, contracts.TicketDone, contracts.IntegrationNeedsMerge)

	err := svc.ArchiveTicket(context.Background(), tk.ID)
	if err == nil {
		t.Fatal("ArchiveTicket should reject done+needs_merge")
	}
	if !strings.Contains(err.Error(), "当前状态不允许归档") {
		t.Fatalf("expected descriptive archive error, got=%v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("ticket should stay done, got=%s", ticket.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus); got != contracts.IntegrationNeedsMerge {
		t.Fatalf("integration_status should stay needs_merge, got=%s", got)
	}
}

func TestArchiveTicket_RejectsAlreadyArchived(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "archive-already-archived")
	setTicketArchiveState(t, p.DB, tk.ID, contracts.TicketArchived, contracts.IntegrationNone)

	err := svc.ArchiveTicket(context.Background(), tk.ID)
	if err == nil {
		t.Fatal("ArchiveTicket should reject archived ticket")
	}
	if !strings.Contains(err.Error(), "当前状态不允许归档") {
		t.Fatalf("expected descriptive archive error, got=%v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketArchived {
		t.Fatalf("ticket should stay archived, got=%s", ticket.WorkflowStatus)
	}
}

func TestArchiveTicket_AllowsMergedAndAbandoned(t *testing.T) {
	tests := []struct {
		name        string
		integration contracts.IntegrationStatus
	}{
		{name: "merged", integration: contracts.IntegrationMerged},
		{name: "abandoned", integration: contracts.IntegrationAbandoned},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, p, _ := newServiceForTest(t)
			tk := createTicket(t, p.DB, "archive-"+tc.name)
			setTicketArchiveState(t, p.DB, tk.ID, contracts.TicketDone, tc.integration)

			if err := svc.ArchiveTicket(context.Background(), tk.ID); err != nil {
				t.Fatalf("ArchiveTicket failed: %v", err)
			}

			var ticket contracts.Ticket
			if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
				t.Fatalf("query ticket failed: %v", err)
			}
			if ticket.WorkflowStatus != contracts.TicketArchived {
				t.Fatalf("ticket should be archived, got=%s", ticket.WorkflowStatus)
			}
			if got := contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus); got != tc.integration {
				t.Fatalf("integration_status should stay %s, got=%s", tc.integration, got)
			}
		})
	}
}

func setTicketArchiveState(t *testing.T, db *gorm.DB, ticketID uint, workflow contracts.TicketWorkflowStatus, integration contracts.IntegrationStatus) {
	t.Helper()
	now := time.Now()
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&contracts.Ticket{}).Where("id = ?", ticketID).Updates(map[string]any{
			"workflow_status":    workflow,
			"integration_status": integration,
			"updated_at":         now,
		}).Error; err != nil {
			return err
		}
		if _, _, err := ticketlifecycle.AppendEventTx(context.Background(), tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleCreated,
			Source:         "test.archive",
			ActorType:      contracts.TicketLifecycleActorUser,
			IdempotencyKey: ticketlifecycle.CreatedIdempotencyKey(ticketID),
			CreatedAt:      now,
		}); err != nil {
			return err
		}
		if workflow == contracts.TicketDone || workflow == contracts.TicketArchived || integration == contracts.IntegrationNeedsMerge || integration == contracts.IntegrationMerged || integration == contracts.IntegrationAbandoned {
			if _, _, err := ticketlifecycle.AppendEventTx(context.Background(), tx, ticketlifecycle.AppendInput{
				TicketID:       ticketID,
				EventType:      contracts.TicketLifecycleDoneReported,
				Source:         "test.archive",
				ActorType:      contracts.TicketLifecycleActorWorker,
				IdempotencyKey: ticketlifecycle.DoneReportedIdempotencyKey(ticketID, 1, 0),
				CreatedAt:      now.Add(time.Nanosecond),
			}); err != nil {
				return err
			}
		}
		switch integration {
		case contracts.IntegrationMerged:
			if _, _, err := ticketlifecycle.AppendEventTx(context.Background(), tx, ticketlifecycle.AppendInput{
				TicketID:       ticketID,
				EventType:      contracts.TicketLifecycleMergeObserved,
				Source:         "test.archive",
				ActorType:      contracts.TicketLifecycleActorSystem,
				IdempotencyKey: ticketlifecycle.MergeObservedIdempotencyKey(ticketID, "archive-test-anchor"),
				CreatedAt:      now.Add(2 * time.Nanosecond),
			}); err != nil {
				return err
			}
		case contracts.IntegrationAbandoned:
			if _, _, err := ticketlifecycle.AppendEventTx(context.Background(), tx, ticketlifecycle.AppendInput{
				TicketID:       ticketID,
				EventType:      contracts.TicketLifecycleMergeAbandoned,
				Source:         "test.archive",
				ActorType:      contracts.TicketLifecycleActorUser,
				IdempotencyKey: ticketlifecycle.MergeAbandonedIdempotencyKey(ticketID, now.Add(3*time.Nanosecond)),
				CreatedAt:      now.Add(3 * time.Nanosecond),
			}); err != nil {
				return err
			}
		}
		if workflow == contracts.TicketArchived {
			if _, _, err := ticketlifecycle.AppendEventTx(context.Background(), tx, ticketlifecycle.AppendInput{
				TicketID:       ticketID,
				EventType:      contracts.TicketLifecycleArchived,
				Source:         "test.archive",
				ActorType:      contracts.TicketLifecycleActorUser,
				IdempotencyKey: ticketlifecycle.ArchivedIdempotencyKey(ticketID),
				CreatedAt:      now.Add(4 * time.Nanosecond),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("set ticket state failed: %v", err)
	}
}
