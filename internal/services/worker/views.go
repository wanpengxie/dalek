package worker

import (
	"context"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/store"
)

type TicketView struct {
	Ticket       store.Ticket
	LatestWorker *store.Worker
	SessionAlive bool
	// SessionProbeFailed=true 表示 tmux 探测失败（不是“离线”）。
	SessionProbeFailed bool

	DerivedStatus contracts.TicketWorkflowStatus

	Capability contracts.TicketCapability

	TaskRunID uint

	RuntimeHealthState contracts.TaskRuntimeHealthState
	RuntimeNeedsUser   bool
	RuntimeSummary     string
	RuntimeObservedAt  *time.Time

	SemanticPhase      contracts.TaskSemanticPhase
	SemanticNextAction string
	SemanticSummary    string
	SemanticReportedAt *time.Time

	LastEventType string
	LastEventNote string
	LastEventAt   *time.Time
}

func computeTicketCapability(workflow contracts.TicketWorkflowStatus, w *store.Worker, sessionAlive bool, sessionProbeFailed bool, hasActiveDispatch bool, runtimeNeedsUser bool, runtimeHealth contracts.TaskRuntimeHealthState) contracts.TicketCapability {
	wf := contracts.TicketWorkflowStatus(strings.TrimSpace(strings.ToLower(string(workflow))))
	if wf == "" {
		wf = contracts.TicketBacklog
	}

	hasWorker := w != nil
	workerStatus := contracts.WorkerStatus("")
	session := ""
	if w != nil {
		workerStatus = w.Status
		session = strings.TrimSpace(w.TmuxSession)
	}
	hasSession := session != ""
	tmuxKnownDead := hasSession && !sessionProbeFailed && !sessionAlive

	cap := contracts.TicketCapability{}
	isDone := wf == contracts.TicketDone
	isArchived := wf == contracts.TicketArchived

	// start：只按 workflow 门禁（资源是否存在由后端决定是否复用/重建）。
	cap.CanStart = !isDone && !isArchived

	// dispatch：默认允许自动 start，所以仅保留 workflow/并发门禁。
	cap.CanDispatch = !isDone &&
		!isArchived &&
		!hasActiveDispatch

	// attach：需要 session 可用（tmux 探测失败视为 unknown，允许尝试）。
	cap.CanAttach = hasWorker &&
		hasSession &&
		!tmuxKnownDead

	// stop：只要有 session 名就允许（即使 session 不在线，kill-session 也是幂等清理）。
	cap.CanStop = hasWorker && hasSession

	// archive：要求没有 running worker，且没有进行中的 dispatch。
	cap.CanArchive = !isArchived &&
		!hasActiveDispatch &&
		(!hasWorker || workerStatus != contracts.WorkerRunning)

	// Reason：给 UI 一个可读提示（不做门禁）。
	switch {
	case wf == contracts.TicketArchived:
		cap.Reason = "已归档"
	case isDone:
		cap.Reason = "已完成"
	case runtimeNeedsUser:
		cap.Reason = "等待输入"
	case runtimeHealth == contracts.TaskHealthStalled:
		cap.Reason = "运行错误"
	case hasActiveDispatch:
		cap.Reason = "dispatch 进行中"
	case !hasWorker:
		cap.Reason = "将自动启动 worker"
	case !hasSession:
		cap.Reason = "worker 缺少 session（dispatch 时将自动修复）"
	case tmuxKnownDead:
		cap.Reason = "tmux session 不在线（dispatch 时将自动修复）"
	case sessionProbeFailed:
		cap.Reason = "tmux 探测失败"
	}

	return cap
}

func (s *Service) ListTicketViews(ctx context.Context) ([]TicketView, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	cfg, err := s.cfg()
	if err != nil {
		return nil, err
	}
	tmuxc, err := s.tmux()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var tickets []store.Ticket
	if err := db.WithContext(ctx).
		Where("workflow_status != ?", contracts.TicketArchived).
		Order("priority desc").
		Order("updated_at desc").
		Order("id desc").
		Find(&tickets).Error; err != nil {
		return nil, err
	}

	ticketIDs := make([]uint, 0, len(tickets))
	for _, t := range tickets {
		if t.ID == 0 {
			continue
		}
		ticketIDs = append(ticketIDs, t.ID)
	}

	var workers []store.Worker
	if len(ticketIDs) > 0 {
		if err := db.WithContext(ctx).
			Where("ticket_id IN ?", ticketIDs).
			Order("ticket_id asc").
			Order("id desc").
			Find(&workers).Error; err != nil {
			return nil, err
		}
	}
	latest := map[uint]store.Worker{}
	for _, w := range workers {
		if _, ok := latest[w.TicketID]; ok {
			continue
		}
		latest[w.TicketID] = w
	}

	// 注意：一个项目内可能混用多个 tmux socket（例如历史 worker 记录或手工迁移）。
	// 不能只查当前 config socket，否则会把活着的 session 误判成已停止。
	sessionsBySocket := map[string]map[string]bool{}
	sessionProbeFailedBySocket := map[string]bool{}
	socketSet := map[string]struct{}{}
	defaultSocket := strings.TrimSpace(cfg.TmuxSocket)
	for _, w := range latest {
		if strings.TrimSpace(w.TmuxSession) == "" {
			continue
		}
		socket := strings.TrimSpace(w.TmuxSocket)
		if socket == "" {
			socket = defaultSocket
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
			sessionProbeFailedBySocket[socket] = true
			sessions = map[string]bool{}
		}
		sessionsBySocket[socket] = sessions
	}

	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		return nil, err
	}
	taskViews, err := taskRuntime.ListStatus(ctx, core.TaskRuntimeListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		IncludeTerminal: true,
		Limit:           5000,
	})
	if err != nil {
		return nil, err
	}
	latestTaskByTicket := map[uint]store.TaskStatusView{}
	for _, tv := range taskViews {
		if tv.TicketID == 0 {
			continue
		}
		if _, ok := latestTaskByTicket[tv.TicketID]; ok {
			continue
		}
		latestTaskByTicket[tv.TicketID] = tv
	}

	activeDispatchByTicket := map[uint]store.PMDispatchJob{}
	if len(ticketIDs) > 0 {
		var jobs []store.PMDispatchJob
		if err := db.WithContext(ctx).
			Where("ticket_id IN ? AND status IN ?", ticketIDs, []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning}).
			Order("ticket_id asc").
			Order("id desc").
			Find(&jobs).Error; err != nil {
			return nil, err
		}
		for _, job := range jobs {
			if job.TicketID == 0 {
				continue
			}
			if _, ok := activeDispatchByTicket[job.TicketID]; ok {
				continue
			}
			activeDispatchByTicket[job.TicketID] = job
		}
	}

	views := make([]TicketView, 0, len(tickets))
	for _, t := range tickets {
		d := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)

		var lw *store.Worker
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
		if w, ok := latest[t.ID]; ok {
			ww := w
			lw = &ww
			if strings.TrimSpace(ww.TmuxSession) != "" {
				socket := strings.TrimSpace(ww.TmuxSocket)
				if socket == "" {
					socket = defaultSocket
				}
				if socket != "" {
					if sessionProbeFailedBySocket[socket] {
						sessionProbeFailed = true
					} else {
						alive = sessionsBySocket[socket][strings.TrimSpace(ww.TmuxSession)]
					}
				}
			}
		}

		if tv, ok := latestTaskByTicket[t.ID]; ok {
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

		_, hasDispatch := activeDispatchByTicket[t.ID]
		cap := computeTicketCapability(d, lw, alive, sessionProbeFailed, hasDispatch, rNeeds, rHealth)

		if lw == nil && runID == 0 {
			rHealth = contracts.TaskHealthUnknown
		} else if !sessionProbeFailed && !alive {
			// session 不在时，给一个更直观的派生态
			if lw != nil {
				switch lw.Status {
				case contracts.WorkerStopped:
					rHealth = contracts.TaskHealthDead
				case contracts.WorkerFailed:
					rHealth = contracts.TaskHealthStalled
				case contracts.WorkerRunning:
					// session 不在线时，避免 UI 误显示“在线/运行中”。
					rHealth = contracts.TaskHealthDead
				default:
					// unknown
				}
			}
		} else {
			// session 探测失败：保守处理为 unknown，不降级到 dead/backlog。
			if rHealth == contracts.TaskHealthDead {
				rHealth = contracts.TaskHealthUnknown
			}
		}

		views = append(views, TicketView{
			Ticket:             t,
			LatestWorker:       lw,
			SessionAlive:       alive,
			DerivedStatus:      d,
			Capability:         cap,
			TaskRunID:          runID,
			RuntimeHealthState: rHealth,
			RuntimeNeedsUser:   rNeeds,
			RuntimeSummary:     rSummary,
			RuntimeObservedAt:  rObservedAt,
			SessionProbeFailed: sessionProbeFailed,
			SemanticPhase:      semPhase,
			SemanticNextAction: semNext,
			SemanticSummary:    semSummary,
			SemanticReportedAt: semReportedAt,
			LastEventType:      lastEventType,
			LastEventNote:      lastEventNote,
			LastEventAt:        lastEventAt,
		})
	}
	return views, nil
}
