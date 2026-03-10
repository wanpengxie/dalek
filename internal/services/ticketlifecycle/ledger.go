package ticketlifecycle

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

type AppendInput struct {
	TicketID       uint
	EventType      contracts.TicketLifecycleEventType
	Source         string
	ActorType      contracts.TicketLifecycleActorType
	WorkerID       uint
	TaskRunID      uint
	IdempotencyKey string
	Payload        any
	CreatedAt      time.Time
}

type SnapshotProjection struct {
	WorkflowStatus    contracts.TicketWorkflowStatus `json:"workflow_status"`
	IntegrationStatus contracts.IntegrationStatus    `json:"integration_status"`
	EventCount        int                            `json:"event_count"`
	LastSequence      uint                           `json:"last_sequence"`
}

type ConsistencyCheck struct {
	TicketID     uint                             `json:"ticket_id"`
	Snapshot     SnapshotProjection               `json:"snapshot"`
	Rebuilt      SnapshotProjection               `json:"rebuilt"`
	Mismatch     bool                             `json:"mismatch"`
	Mismatches   []string                         `json:"mismatches"`
	EventCount   int                              `json:"event_count"`
	LastSequence uint                             `json:"last_sequence"`
	Events       []contracts.TicketLifecycleEvent `json:"events,omitempty"`
}

func defaultSnapshotProjection() SnapshotProjection {
	return SnapshotProjection{
		WorkflowStatus:    contracts.TicketBacklog,
		IntegrationStatus: contracts.IntegrationNone,
	}
}

func projectSnapshotEvent(out *SnapshotProjection, ev contracts.TicketLifecycleEvent) {
	if out == nil {
		return
	}
	out.LastSequence = ev.Sequence
	switch contracts.CanonicalTicketLifecycleEventType(ev.EventType) {
	case contracts.TicketLifecycleCreated:
		out.WorkflowStatus = contracts.TicketBacklog
		out.IntegrationStatus = contracts.IntegrationNone
	case contracts.TicketLifecycleStartRequested:
		out.WorkflowStatus = contracts.TicketQueued
	case contracts.TicketLifecycleActivated:
		out.WorkflowStatus = contracts.TicketActive
	case contracts.TicketLifecycleExecutionLost:
		// execution_lost 是事实事件，本身不改变投影状态；
		// 后续由 requeued / execution_escalated 完成正式收敛。
	case contracts.TicketLifecycleRequeued:
		out.WorkflowStatus = contracts.TicketQueued
	case contracts.TicketLifecycleExecutionEscalated:
		out.WorkflowStatus = contracts.TicketBlocked
	case contracts.TicketLifecycleWaitUserReported:
		out.WorkflowStatus = contracts.TicketBlocked
	case contracts.TicketLifecycleDoneReported:
		out.WorkflowStatus = contracts.TicketDone
		out.IntegrationStatus = contracts.IntegrationNeedsMerge
	case contracts.TicketLifecycleMergeObserved:
		out.IntegrationStatus = contracts.IntegrationMerged
	case contracts.TicketLifecycleMergeAbandoned:
		out.IntegrationStatus = contracts.IntegrationAbandoned
	case contracts.TicketLifecycleArchived:
		out.WorkflowStatus = contracts.TicketArchived
	case contracts.TicketLifecycleRepaired:
		payload := ev.PayloadJSON
		if targetWorkflow := strings.TrimSpace(fmt.Sprint(payload["target_workflow"])); targetWorkflow != "" && targetWorkflow != "<nil>" {
			out.WorkflowStatus = contracts.CanonicalTicketWorkflowStatus(contracts.TicketWorkflowStatus(targetWorkflow))
		}
		if targetIntegration := strings.TrimSpace(fmt.Sprint(payload["target_integration"])); targetIntegration != "" && targetIntegration != "<nil>" {
			out.IntegrationStatus = contracts.CanonicalIntegrationStatus(contracts.IntegrationStatus(targetIntegration))
		}
	}
}

func AppendEventTx(ctx context.Context, tx *gorm.DB, input AppendInput) (*contracts.TicketLifecycleEvent, bool, error) {
	if tx == nil {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if input.TicketID == 0 {
		return nil, false, fmt.Errorf("ticket_id 不能为空")
	}
	eventType := contracts.CanonicalTicketLifecycleEventType(input.EventType)
	if strings.TrimSpace(string(eventType)) == "" {
		return nil, false, fmt.Errorf("event_type 不能为空")
	}
	actorType := contracts.CanonicalTicketLifecycleActorType(input.ActorType)
	if strings.TrimSpace(string(actorType)) == "" {
		return nil, false, fmt.Errorf("actor_type 不能为空")
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return nil, false, fmt.Errorf("idempotency_key 不能为空")
	}
	existing, found, err := findByIdempotencyKeyTx(ctx, tx, key)
	if err != nil {
		return nil, false, err
	}
	if found {
		return existing, false, nil
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now()
	}
	nextSeq, err := nextSequenceTx(ctx, tx, input.TicketID)
	if err != nil {
		return nil, false, err
	}
	ev := contracts.TicketLifecycleEvent{
		CreatedAt:      input.CreatedAt,
		TicketID:       input.TicketID,
		Sequence:       nextSeq,
		EventType:      eventType,
		Source:         strings.TrimSpace(input.Source),
		ActorType:      actorType,
		IdempotencyKey: key,
		PayloadJSON:    contracts.JSONMapFromAny(input.Payload),
	}
	if input.WorkerID > 0 {
		workerID := input.WorkerID
		ev.WorkerID = &workerID
	}
	if input.TaskRunID > 0 {
		taskRunID := input.TaskRunID
		ev.TaskRunID = &taskRunID
	}
	if err := tx.WithContext(ctx).Create(&ev).Error; err != nil {
		if !isLifecycleUniqueConflict(err) {
			return nil, false, err
		}
		existing, found, lookupErr := findByIdempotencyKeyTx(ctx, tx, key)
		if lookupErr != nil {
			return nil, false, lookupErr
		}
		if found {
			return existing, false, nil
		}
		return nil, false, err
	}
	return &ev, true, nil
}

func ProjectFromLastEvent(current SnapshotProjection, ev contracts.TicketLifecycleEvent) SnapshotProjection {
	out := current
	if strings.TrimSpace(string(out.WorkflowStatus)) == "" {
		out = defaultSnapshotProjection()
	}
	out.EventCount++
	projectSnapshotEvent(&out, ev)
	return out
}

func ListEventsByTicket(ctx context.Context, db *gorm.DB, ticketID uint) ([]contracts.TicketLifecycleEvent, error) {
	if db == nil {
		return nil, fmt.Errorf("db 为空")
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var events []contracts.TicketLifecycleEvent
	if err := db.WithContext(ctx).
		Where("ticket_id = ?", ticketID).
		Order("sequence asc").
		Order("id asc").
		Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func RebuildSnapshot(events []contracts.TicketLifecycleEvent) SnapshotProjection {
	out := defaultSnapshotProjection()
	out.EventCount = len(events)
	for _, ev := range events {
		projectSnapshotEvent(&out, ev)
	}
	return out
}

func CheckTicketConsistency(ctx context.Context, db *gorm.DB, ticketID uint) (ConsistencyCheck, error) {
	if db == nil {
		return ConsistencyCheck{}, fmt.Errorf("db 为空")
	}
	if ticketID == 0 {
		return ConsistencyCheck{}, fmt.Errorf("ticket_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var ticket contracts.Ticket
	if err := db.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
		return ConsistencyCheck{}, err
	}
	events, err := ListEventsByTicket(ctx, db, ticketID)
	if err != nil {
		return ConsistencyCheck{}, err
	}
	rebuilt := RebuildSnapshot(events)
	snapshot := SnapshotProjection{
		WorkflowStatus:    contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus),
		IntegrationStatus: contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus),
		EventCount:        len(events),
		LastSequence:      rebuilt.LastSequence,
	}
	check := ConsistencyCheck{
		TicketID:     ticketID,
		Snapshot:     snapshot,
		Rebuilt:      rebuilt,
		EventCount:   len(events),
		LastSequence: rebuilt.LastSequence,
	}
	if snapshot.WorkflowStatus != rebuilt.WorkflowStatus {
		check.Mismatches = append(check.Mismatches, fmt.Sprintf("workflow_status snapshot=%s rebuilt=%s", snapshot.WorkflowStatus, rebuilt.WorkflowStatus))
	}
	if snapshot.IntegrationStatus != rebuilt.IntegrationStatus {
		check.Mismatches = append(check.Mismatches, fmt.Sprintf("integration_status snapshot=%s rebuilt=%s", snapshot.IntegrationStatus, rebuilt.IntegrationStatus))
	}
	if len(events) == 0 {
		check.Mismatches = append(check.Mismatches, "missing lifecycle events")
	}
	check.Mismatch = len(check.Mismatches) > 0
	return check, nil
}

func CreatedIdempotencyKey(ticketID uint) string {
	return fmt.Sprintf("ticket:%d:created", ticketID)
}

func StartRequestedIdempotencyKey(ticketID uint, at time.Time) string {
	return fmt.Sprintf("ticket:%d:start_requested:%d", ticketID, at.UnixNano())
}

func ActivatedDispatchIdempotencyKey(ticketID, dispatchID uint) string {
	return fmt.Sprintf("ticket:%d:activated:dispatch:%d", ticketID, dispatchID)
}

func ActivatedDirectIdempotencyKey(ticketID uint, at time.Time) string {
	return fmt.Sprintf("ticket:%d:activated:direct:%d", ticketID, at.UnixNano())
}

func ActivatedRunIdempotencyKey(ticketID, taskRunID uint) string {
	return fmt.Sprintf("ticket:%d:activated:run:%d", ticketID, taskRunID)
}

func ExecutionLostIdempotencyKey(ticketID, taskRunID, workerID uint) string {
	if taskRunID != 0 {
		return fmt.Sprintf("ticket:%d:execution_lost:run:%d", ticketID, taskRunID)
	}
	return fmt.Sprintf("ticket:%d:execution_lost:worker:%d", ticketID, workerID)
}

func RequeuedIdempotencyKey(ticketID, taskRunID, workerID uint, retryCount int) string {
	if taskRunID != 0 {
		return fmt.Sprintf("ticket:%d:requeued:run:%d:retry:%d", ticketID, taskRunID, retryCount)
	}
	return fmt.Sprintf("ticket:%d:requeued:worker:%d:retry:%d", ticketID, workerID, retryCount)
}

func ExecutionEscalatedIdempotencyKey(ticketID, taskRunID, workerID uint, retryCount int) string {
	if taskRunID != 0 {
		return fmt.Sprintf("ticket:%d:execution_escalated:run:%d:retry:%d", ticketID, taskRunID, retryCount)
	}
	return fmt.Sprintf("ticket:%d:execution_escalated:worker:%d:retry:%d", ticketID, workerID, retryCount)
}

func WaitUserReportedIdempotencyKey(ticketID, taskRunID, workerID uint) string {
	if taskRunID != 0 {
		return fmt.Sprintf("ticket:%d:wait_user:run:%d", ticketID, taskRunID)
	}
	return fmt.Sprintf("ticket:%d:wait_user:worker:%d", ticketID, workerID)
}

func DoneReportedIdempotencyKey(ticketID, taskRunID, workerID uint) string {
	if taskRunID != 0 {
		return fmt.Sprintf("ticket:%d:done:run:%d", ticketID, taskRunID)
	}
	return fmt.Sprintf("ticket:%d:done:worker:%d", ticketID, workerID)
}

func MergeObservedIdempotencyKey(ticketID uint, anchorSHA string) string {
	return fmt.Sprintf("ticket:%d:merge_observed:%s", ticketID, strings.TrimSpace(anchorSHA))
}

func MergeAbandonedIdempotencyKey(ticketID uint, at time.Time) string {
	return fmt.Sprintf("ticket:%d:merge_abandoned:%d", ticketID, at.UnixNano())
}

func ArchivedIdempotencyKey(ticketID uint) string {
	return fmt.Sprintf("ticket:%d:archived", ticketID)
}

func RepairedIdempotencyKey(ticketID uint, source string, at time.Time) string {
	return fmt.Sprintf("ticket:%d:repaired:%s:%d", ticketID, strings.TrimSpace(source), at.UnixNano())
}

func findByIdempotencyKeyTx(ctx context.Context, tx *gorm.DB, key string) (*contracts.TicketLifecycleEvent, bool, error) {
	var existing contracts.TicketLifecycleEvent
	if err := tx.WithContext(ctx).Where("idempotency_key = ?", key).First(&existing).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &existing, true, nil
}

func nextSequenceTx(ctx context.Context, tx *gorm.DB, ticketID uint) (uint, error) {
	var row struct {
		MaxSequence uint `gorm:"column:max_sequence"`
	}
	if err := tx.WithContext(ctx).
		Model(&contracts.TicketLifecycleEvent{}).
		Select("COALESCE(MAX(sequence), 0) AS max_sequence").
		Where("ticket_id = ?", ticketID).
		Scan(&row).Error; err != nil {
		return 0, err
	}
	return row.MaxSequence + 1, nil
}

func isLifecycleUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unique constraint failed") || strings.Contains(msg, "duplicate key")
}
