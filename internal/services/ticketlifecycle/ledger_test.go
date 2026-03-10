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

func TestRebuildSnapshot_ExecutionConvergenceEvents(t *testing.T) {
	events := []contracts.TicketLifecycleEvent{
		{EventType: contracts.TicketLifecycleCreated},
		{EventType: contracts.TicketLifecycleStartRequested},
		{EventType: contracts.TicketLifecycleActivated},
		{EventType: contracts.TicketLifecycleExecutionLost},
		{EventType: contracts.TicketLifecycleRequeued},
		{EventType: contracts.TicketLifecycleActivated},
		{EventType: contracts.TicketLifecycleExecutionLost},
		{EventType: contracts.TicketLifecycleExecutionEscalated},
	}
	got := RebuildSnapshot(events)
	if got.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected blocked after execution_escalated, got=%s", got.WorkflowStatus)
	}
	if got.EventCount != len(events) {
		t.Fatalf("expected event_count=%d, got=%d", len(events), got.EventCount)
	}
}

func TestProjectFromLastEvent_MatchesRebuildSnapshot(t *testing.T) {
	events := []contracts.TicketLifecycleEvent{
		{Sequence: 1, EventType: contracts.TicketLifecycleCreated},
		{Sequence: 2, EventType: contracts.TicketLifecycleStartRequested},
		{Sequence: 3, EventType: contracts.TicketLifecycleActivated},
		{Sequence: 4, EventType: contracts.TicketLifecycleExecutionLost},
		{Sequence: 5, EventType: contracts.TicketLifecycleRequeued},
		{Sequence: 6, EventType: contracts.TicketLifecycleActivated},
		{Sequence: 7, EventType: contracts.TicketLifecycleDoneReported},
		{Sequence: 8, EventType: contracts.TicketLifecycleMergeObserved},
		{Sequence: 9, EventType: contracts.TicketLifecycleArchived},
	}

	var projected SnapshotProjection
	for _, ev := range events {
		projected = ProjectFromLastEvent(projected, ev)
	}
	rebuilt := RebuildSnapshot(events)
	if projected.WorkflowStatus != rebuilt.WorkflowStatus {
		t.Fatalf("workflow mismatch: projected=%s rebuilt=%s", projected.WorkflowStatus, rebuilt.WorkflowStatus)
	}
	if projected.IntegrationStatus != rebuilt.IntegrationStatus {
		t.Fatalf("integration mismatch: projected=%s rebuilt=%s", projected.IntegrationStatus, rebuilt.IntegrationStatus)
	}
	if projected.EventCount != rebuilt.EventCount {
		t.Fatalf("event_count mismatch: projected=%d rebuilt=%d", projected.EventCount, rebuilt.EventCount)
	}
	if projected.LastSequence != rebuilt.LastSequence {
		t.Fatalf("last_sequence mismatch: projected=%d rebuilt=%d", projected.LastSequence, rebuilt.LastSequence)
	}
}
