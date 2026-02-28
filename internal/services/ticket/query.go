package ticket

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/infra"
	"dalek/internal/repo"
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
	defaultSocket              string
	latestWorkerByTicket       map[uint]contracts.Worker
	runtimeAliveByWorker       map[uint]bool
	runtimeProbeFailedByWorker map[uint]bool
	sessionsBySocket           map[string]map[string]bool
	sessionProbeFailedBySocket map[string]bool
	latestTaskByTicket         map[uint]store.TaskStatusView
	activeDispatchByTicket     map[uint]contracts.PMDispatchJob
}

func (s *QueryService) require() (*core.Project, error) {
	if s == nil || s.p == nil {
		return nil, fmt.Errorf("ticket query service 缺少 project 上下文")
	}
	if s.p.DB == nil {
		return nil, fmt.Errorf("ticket query service 缺少 DB")
	}
	if s.p.Tmux == nil {
		return nil, fmt.Errorf("ticket query service 缺少 tmux client")
	}
	if s.p.WorkerRuntime == nil {
		return nil, fmt.Errorf("ticket query service 缺少 worker runtime")
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

func (s *QueryService) cfg() (repo.Config, error) {
	p, err := s.require()
	if err != nil {
		return repo.Config{}, err
	}
	return p.Config.WithDefaults(), nil
}

func (s *QueryService) tmux() (infra.TmuxClient, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	return p.Tmux, nil
}

func (s *QueryService) runtime() (infra.WorkerRuntime, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	return p.WorkerRuntime, nil
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

func (s *QueryService) fetchTicketViewData(ctx context.Context) (ticketViewData, error) {
	db, err := s.db()
	if err != nil {
		return ticketViewData{}, err
	}
	cfg, err := s.cfg()
	if err != nil {
		return ticketViewData{}, err
	}
	tmuxc, err := s.tmux()
	if err != nil {
		return ticketViewData{}, err
	}
	runtime, err := s.runtime()
	if err != nil {
		return ticketViewData{}, err
	}

	data := ticketViewData{
		defaultSocket:              strings.TrimSpace(cfg.TmuxSocket),
		latestWorkerByTicket:       map[uint]contracts.Worker{},
		runtimeAliveByWorker:       map[uint]bool{},
		runtimeProbeFailedByWorker: map[uint]bool{},
		sessionsBySocket:           map[string]map[string]bool{},
		sessionProbeFailedBySocket: map[string]bool{},
		latestTaskByTicket:         map[uint]store.TaskStatusView{},
		activeDispatchByTicket:     map[uint]contracts.PMDispatchJob{},
	}

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

	socketSet := map[string]struct{}{}
	for _, w := range data.latestWorkerByTicket {
		if hasTicketWorkerRuntimeHandle(w) {
			alive, aerr := runtime.IsAlive(ctx, ticketWorkerRuntimeHandle(w))
			if aerr != nil {
				data.runtimeProbeFailedByWorker[w.ID] = true
			} else {
				data.runtimeAliveByWorker[w.ID] = alive
				if alive {
					continue
				}
			}
		}
		if strings.TrimSpace(w.TmuxSession) == "" {
			continue
		}
		socket := strings.TrimSpace(w.TmuxSocket)
		if socket == "" {
			socket = data.defaultSocket
		}
		if socket == "" {
			continue
		}
		socketSet[socket] = struct{}{}
	}
	for socket := range socketSet {
		sessions, err := tmuxc.ListSessions(ctx, socket)
		if err != nil {
			// tmux 探测失败时不能直接当作“不在线”，否则会把活跃 session 误判为 backlog/dead。
			// 记录失败标记，后续走保守降级（unknown），避免误伤调度操作。
			data.sessionProbeFailedBySocket[socket] = true
			sessions = map[string]bool{}
		}
		data.sessionsBySocket[socket] = sessions
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
		if !alive && strings.TrimSpace(ww.TmuxSession) != "" {
			socket := strings.TrimSpace(ww.TmuxSocket)
			if socket == "" {
				socket = data.defaultSocket
			}
			if socket != "" {
				if data.sessionProbeFailedBySocket[socket] {
					sessionProbeFailed = true
				} else {
					alive = data.sessionsBySocket[socket][strings.TrimSpace(ww.TmuxSession)]
				}
			}
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

func hasTicketWorkerRuntimeHandle(w contracts.Worker) bool {
	return w.ProcessPID > 0
}

func ticketWorkerRuntimeHandle(w contracts.Worker) infra.WorkerProcessHandle {
	h := infra.WorkerProcessHandle{
		PID:     w.ProcessPID,
		LogPath: strings.TrimSpace(w.LogPath),
	}
	if w.StartedAt != nil {
		h.StartedAt = *w.StartedAt
	}
	return h
}
