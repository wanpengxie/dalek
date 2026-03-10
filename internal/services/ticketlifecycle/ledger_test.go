package ticketlifecycle

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

func TestAppendEventTx_AssignsSequenceAndDedupesByIdempotencyKey(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	ticket := contracts.Ticket{Title: "ledger-test", Description: "desc", WorkflowStatus: contracts.TicketBacklog}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		if _, inserted, err := AppendEventTx(context.Background(), tx, AppendInput{
			TicketID:       ticket.ID,
			EventType:      contracts.TicketLifecycleCreated,
			Source:         "test",
			ActorType:      contracts.TicketLifecycleActorUser,
			IdempotencyKey: CreatedIdempotencyKey(ticket.ID),
		}); err != nil {
			return err
		} else if !inserted {
			t.Fatalf("first append should insert")
		}
		if ev, inserted, err := AppendEventTx(context.Background(), tx, AppendInput{
			TicketID:       ticket.ID,
			EventType:      contracts.TicketLifecycleCreated,
			Source:         "test",
			ActorType:      contracts.TicketLifecycleActorUser,
			IdempotencyKey: CreatedIdempotencyKey(ticket.ID),
		}); err != nil {
			return err
		} else if inserted || ev == nil || ev.Sequence != 1 {
			t.Fatalf("duplicate append should reuse first event, inserted=%v ev=%+v", inserted, ev)
		}
		_, _, err := AppendEventTx(context.Background(), tx, AppendInput{
			TicketID:       ticket.ID,
			EventType:      contracts.TicketLifecycleStartRequested,
			Source:         "test",
			ActorType:      contracts.TicketLifecycleActorUser,
			IdempotencyKey: StartRequestedIdempotencyKey(ticket.ID, ticket.CreatedAt.Add(time.Second)),
		})
		return err
	})
	if err != nil {
		t.Fatalf("transaction failed: %v", err)
	}

	var events []contracts.TicketLifecycleEvent
	if err := db.Where("ticket_id = ?", ticket.ID).Order("sequence asc").Find(&events).Error; err != nil {
		t.Fatalf("query lifecycle events failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 lifecycle events, got=%d", len(events))
	}
	if events[0].Sequence != 1 || events[1].Sequence != 2 {
		t.Fatalf("unexpected lifecycle sequences: %+v", events)
	}
}

func TestCheckTicketConsistency_RebuildsSnapshotAndDetectsMismatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	ticket := contracts.Ticket{
		Title:             "consistency",
		Description:       "desc",
		WorkflowStatus:    contracts.TicketDone,
		IntegrationStatus: contracts.IntegrationNeedsMerge,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		inputs := []AppendInput{
			{
				TicketID:       ticket.ID,
				EventType:      contracts.TicketLifecycleCreated,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorUser,
				IdempotencyKey: CreatedIdempotencyKey(ticket.ID),
			},
			{
				TicketID:       ticket.ID,
				EventType:      contracts.TicketLifecycleStartRequested,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorUser,
				IdempotencyKey: StartRequestedIdempotencyKey(ticket.ID, ticket.CreatedAt.Add(time.Second)),
			},
			{
				TicketID:       ticket.ID,
				EventType:      contracts.TicketLifecycleActivated,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorPM,
				IdempotencyKey: ActivatedDirectIdempotencyKey(ticket.ID, ticket.CreatedAt.Add(2*time.Second)),
			},
			{
				TicketID:       ticket.ID,
				EventType:      contracts.TicketLifecycleDoneReported,
				Source:         "test",
				ActorType:      contracts.TicketLifecycleActorWorker,
				IdempotencyKey: DoneReportedIdempotencyKey(ticket.ID, 42, 0),
			},
		}
		for _, input := range inputs {
			if _, _, err := AppendEventTx(context.Background(), tx, input); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("append lifecycle chain failed: %v", err)
	}

	check, err := CheckTicketConsistency(context.Background(), db, ticket.ID)
	if err != nil {
		t.Fatalf("CheckTicketConsistency failed: %v", err)
	}
	if check.Mismatch {
		t.Fatalf("expected consistent lifecycle projection, mismatches=%v", check.Mismatches)
	}
	if check.Rebuilt.WorkflowStatus != contracts.TicketDone || check.Rebuilt.IntegrationStatus != contracts.IntegrationNeedsMerge {
		t.Fatalf("unexpected rebuilt snapshot: %+v", check.Rebuilt)
	}

	if err := db.Model(&contracts.Ticket{}).Where("id = ?", ticket.ID).Update("integration_status", contracts.IntegrationMerged).Error; err != nil {
		t.Fatalf("force mismatch failed: %v", err)
	}
	check, err = CheckTicketConsistency(context.Background(), db, ticket.ID)
	if err != nil {
		t.Fatalf("CheckTicketConsistency mismatch failed: %v", err)
	}
	if !check.Mismatch {
		t.Fatalf("expected mismatch after snapshot drift")
	}
}
