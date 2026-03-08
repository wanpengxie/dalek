package ticket

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type QueryService struct {
	p *core.Project
}

func NewQueryService(p *core.Project) *QueryService {
	return &QueryService{p: p}
}

type ticketViewData struct {
	tickets                    []contracts.Ticket
	latestWorkerByTicket       map[uint]contracts.Worker
	runtimeAliveByWorker       map[uint]bool
	runtimeProbeFailedByWorker map[uint]bool
	latestTaskByTicket         map[uint]store.TaskStatusView
	activeDispatchByTicket     map[uint]contracts.PMDispatchJob
}

func newTicketViewData() ticketViewData {
	return ticketViewData{
		latestWorkerByTicket:       map[uint]contracts.Worker{},
		runtimeAliveByWorker:       map[uint]bool{},
		runtimeProbeFailedByWorker: map[uint]bool{},
		latestTaskByTicket:         map[uint]store.TaskStatusView{},
		activeDispatchByTicket:     map[uint]contracts.PMDispatchJob{},
	}
}

func (s *QueryService) require() (*core.Project, error) {
	if s == nil || s.p == nil {
		return nil, fmt.Errorf("ticket query service 缺少 project 上下文")
	}
	if s.p.DB == nil {
		return nil, fmt.Errorf("ticket query service 缺少 DB")
	}
	if s.p.TaskRuntime == nil {
		return nil, fmt.Errorf("ticket query service 缺少 task runtime")
	}
	return s.p, nil
}

func (s *QueryService) db() (*gorm.DB, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	return p.DB, nil
}

func (s *QueryService) taskRuntimeForDB(db *gorm.DB) (core.TaskRuntime, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("task runtime db 为空")
	}
	return p.TaskRuntime.ForDB(db), nil
}

func (s *QueryService) ListTicketViews(ctx context.Context) ([]TicketView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := s.fetchTicketViewData(ctx)
	if err != nil {
		return nil, err
	}

	views := make([]TicketView, 0, len(data.tickets))
	for _, t := range data.tickets {
		view := buildTicketView(t, data)
		views = append(views, view)
	}
	return views, nil
}

func (s *QueryService) GetTicketViewByID(ctx context.Context, ticketID uint) (*TicketView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket id 不能为空")
	}

	db, err := s.db()
	if err != nil {
		return nil, err
	}
	data := newTicketViewData()

	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return nil, err
	}
	data.tickets = append(data.tickets, t)

	var worker contracts.Worker
	if err := db.WithContext(ctx).
		Where("ticket_id = ?", ticketID).
		Order("id desc").
		First(&worker).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		data.latestWorkerByTicket[ticketID] = worker
	}

	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		return nil, err
	}
	taskViews, err := taskRuntime.ListStatus(ctx, contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		TicketID:        ticketID,
		IncludeTerminal: true,
		Limit:           500,
	})
	if err != nil {
		return nil, err
	}
	for _, tv := range taskViews {
		if tv.TicketID != ticketID {
			continue
		}
		if _, ok := data.latestTaskByTicket[tv.TicketID]; ok {
			continue
		}
		data.latestTaskByTicket[tv.TicketID] = tv
		if tv.WorkerID != 0 {
			state := strings.TrimSpace(strings.ToLower(tv.OrchestrationState))
			if state == string(contracts.TaskPending) || state == string(contracts.TaskRunning) {
				data.runtimeAliveByWorker[tv.WorkerID] = true
			}
		}
	}

	var job contracts.PMDispatchJob
	if err := db.WithContext(ctx).
		Where("ticket_id = ? AND status IN ?", ticketID, []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning}).
		Order("id desc").
		First(&job).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		data.activeDispatchByTicket[ticketID] = job
	}

	view := buildTicketView(t, data)
	return &view, nil
}

func (s *QueryService) fetchTicketViewData(ctx context.Context) (ticketViewData, error) {
	db, err := s.db()
	if err != nil {
		return ticketViewData{}, err
	}

	data := newTicketViewData()

	if err := db.WithContext(ctx).
		Where("workflow_status != ?", contracts.TicketArchived).
		Order("priority desc").
		Order("created_at asc").
		Order("id asc").
		Find(&data.tickets).Error; err != nil {
		return ticketViewData{}, err
	}

	ticketIDs := make([]uint, 0, len(data.tickets))
	for _, t := range data.tickets {
		if t.ID == 0 {
			continue
		}
		ticketIDs = append(ticketIDs, t.ID)
	}

	if len(ticketIDs) > 0 {
		var workers []contracts.Worker
		if err := db.WithContext(ctx).
			Where("ticket_id IN ?", ticketIDs).
			Order("ticket_id asc").
			Order("id desc").
			Find(&workers).Error; err != nil {
			return ticketViewData{}, err
		}
		for _, w := range workers {
			if _, ok := data.latestWorkerByTicket[w.TicketID]; ok {
				continue
			}
			data.latestWorkerByTicket[w.TicketID] = w
		}
	}

	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		return ticketViewData{}, err
	}
	taskViews, err := taskRuntime.ListStatus(ctx, contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		IncludeTerminal: true,
		Limit:           5000,
	})
	if err != nil {
		return ticketViewData{}, err
	}
	for _, tv := range taskViews {
		if tv.TicketID == 0 {
			continue
		}
		if _, ok := data.latestTaskByTicket[tv.TicketID]; ok {
			continue
		}
		data.latestTaskByTicket[tv.TicketID] = tv
		if tv.WorkerID != 0 {
			state := strings.TrimSpace(strings.ToLower(tv.OrchestrationState))
			if state == string(contracts.TaskPending) || state == string(contracts.TaskRunning) {
				data.runtimeAliveByWorker[tv.WorkerID] = true
			}
		}
	}

	if len(ticketIDs) > 0 {
		var jobs []contracts.PMDispatchJob
		if err := db.WithContext(ctx).
			Where("ticket_id IN ? AND status IN ?", ticketIDs, []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning}).
			Order("ticket_id asc").
			Order("id desc").
			Find(&jobs).Error; err != nil {
			return ticketViewData{}, err
		}
		for _, job := range jobs {
			if job.TicketID == 0 {
				continue
			}
			if _, ok := data.activeDispatchByTicket[job.TicketID]; ok {
				continue
			}
			data.activeDispatchByTicket[job.TicketID] = job
		}
	}

	return data, nil
}

func buildTicketView(t contracts.Ticket, data ticketViewData) TicketView {
	d := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)

	var lw *contracts.Worker
	alive := false
	sessionProbeFailed := false
	runID := uint(0)
	rHealth := contracts.TaskHealthUnknown
	rNeeds := false
	rSummary := ""
	var rObservedAt *time.Time
	semPhase := contracts.TaskSemanticPhase("")
	semNext := ""
	semSummary := ""
	var semReportedAt *time.Time
	lastEventType := ""
	lastEventNote := ""
	var lastEventAt *time.Time

	if w, ok := data.latestWorkerByTicket[t.ID]; ok {
		ww := w
		lw = &ww
		if data.runtimeProbeFailedByWorker[ww.ID] {
			sessionProbeFailed = true
		}
		if data.runtimeAliveByWorker[ww.ID] {
			alive = true
		}
	}

	if tv, ok := data.latestTaskByTicket[t.ID]; ok {
		runID = tv.RunID
		rHealth = contracts.TaskRuntimeHealthState(strings.TrimSpace(tv.RuntimeHealthState))
		if strings.TrimSpace(string(rHealth)) == "" {
			rHealth = contracts.TaskHealthUnknown
		}
		rNeeds = tv.RuntimeNeedsUser
		rSummary = strings.TrimSpace(tv.RuntimeSummary)
		rObservedAt = tv.RuntimeObservedAt
		semPhase = contracts.TaskSemanticPhase(strings.TrimSpace(tv.SemanticPhase))
		semNext = strings.TrimSpace(tv.SemanticNextAction)
		semSummary = strings.TrimSpace(tv.SemanticSummary)
		semReportedAt = tv.SemanticReportedAt
		lastEventType = strings.TrimSpace(tv.LastEventType)
		lastEventNote = strings.TrimSpace(tv.LastEventNote)
		lastEventAt = tv.LastEventAt
	}

	_, hasDispatch := data.activeDispatchByTicket[t.ID]
	capability := ComputeTicketCapability(d, lw, alive, sessionProbeFailed, hasDispatch, rNeeds, rHealth)
	rHealth = computeDerivedRuntimeHealth(lw, alive, sessionProbeFailed, runID, rHealth)

	return TicketView{
		Ticket:             t,
		LatestWorker:       lw,
		SessionAlive:       alive,
		SessionProbeFailed: sessionProbeFailed,
		DerivedStatus:      d,
		Capability:         capability,
		TaskRunID:          runID,
		RuntimeHealthState: rHealth,
		RuntimeNeedsUser:   rNeeds,
		RuntimeSummary:     rSummary,
		RuntimeObservedAt:  rObservedAt,
		SemanticPhase:      semPhase,
		SemanticNextAction: semNext,
		SemanticSummary:    semSummary,
		SemanticReportedAt: semReportedAt,
		LastEventType:      lastEventType,
		LastEventNote:      lastEventNote,
		LastEventAt:        lastEventAt,
	}
}
