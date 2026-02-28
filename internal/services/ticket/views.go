package ticket

import (
	"strings"
	"time"

	"dalek/internal/contracts"
)

type TicketView struct {
	Ticket       contracts.Ticket
	LatestWorker *contracts.Worker
	SessionAlive bool
	// SessionProbeFailed=true 表示运行态探测失败（不是“离线”）。
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

func ComputeTicketCapability(workflow contracts.TicketWorkflowStatus, w *contracts.Worker, sessionAlive bool, sessionProbeFailed bool, hasActiveDispatch bool, runtimeNeedsUser bool, runtimeHealth contracts.TaskRuntimeHealthState) contracts.TicketCapability {
	wf := contracts.TicketWorkflowStatus(strings.TrimSpace(strings.ToLower(string(workflow))))
	if wf == "" {
		wf = contracts.TicketBacklog
	}

	hasWorker := w != nil
	workerStatus := contracts.WorkerStatus("")
	hasRuntimeHandle := false
	hasRuntimeLog := false
	tmuxSession := ""
	if w != nil {
		workerStatus = w.Status
		hasRuntimeHandle = w.ProcessPID > 0
		hasRuntimeLog = strings.TrimSpace(w.LogPath) != ""
		tmuxSession = strings.TrimSpace(w.TmuxSession)
	}
	hasSession := tmuxSession != ""
	runtimeKnownDead := hasRuntimeHandle && !sessionProbeFailed && !sessionAlive
	tmuxKnownDead := hasSession && !hasRuntimeHandle && !sessionProbeFailed && !sessionAlive

	cap := contracts.TicketCapability{}
	isDone := wf == contracts.TicketDone
	isArchived := wf == contracts.TicketArchived

	// start：只按 workflow 门禁（资源是否存在由后端决定是否复用/重建）。
	cap.CanStart = !isDone && !isArchived

	// dispatch：默认允许自动 start，所以仅保留 workflow/并发门禁。
	cap.CanDispatch = !isDone &&
		!isArchived &&
		!hasActiveDispatch

	// attach：优先 runtime 日志 attach；无 runtime 句柄时回退 tmux attach。
	cap.CanAttach = hasWorker &&
		((hasRuntimeHandle && hasRuntimeLog) ||
			(hasSession && !tmuxKnownDead))

	// stop：有 runtime 句柄或 tmux 会话都允许。
	cap.CanStop = hasWorker && (hasRuntimeHandle || hasSession)

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
	case hasRuntimeHandle && !hasRuntimeLog:
		cap.Reason = "worker 缺少日志路径（dispatch 时将自动修复）"
	case !hasRuntimeHandle && !hasSession:
		cap.Reason = "worker 缺少运行句柄（dispatch 时将自动修复）"
	case runtimeKnownDead:
		cap.Reason = "worker 进程不在线（dispatch 时将自动修复）"
	case tmuxKnownDead:
		cap.Reason = "tmux session 不在线（dispatch 时将自动修复）"
	case sessionProbeFailed:
		cap.Reason = "运行态探测失败"
	}

	return cap
}

func computeDerivedRuntimeHealth(latestWorker *contracts.Worker, sessionAlive bool, sessionProbeFailed bool, taskRunID uint, runtimeHealth contracts.TaskRuntimeHealthState) contracts.TaskRuntimeHealthState {
	if latestWorker == nil && taskRunID == 0 {
		return contracts.TaskHealthUnknown
	}
	if latestWorker == nil || latestWorker.ProcessPID <= 0 {
		// 历史 tmux worker：没有 runtime 句柄时不做“离线=dead”推断，避免误判。
		return runtimeHealth
	}
	if !sessionProbeFailed && !sessionAlive {
		// runtime 不在线时，给一个更直观的派生态。
		if latestWorker != nil {
			switch latestWorker.Status {
			case contracts.WorkerStopped:
				return contracts.TaskHealthDead
			case contracts.WorkerFailed:
				return contracts.TaskHealthStalled
			case contracts.WorkerRunning:
				// runtime 不在线时，避免 UI 误显示“在线/运行中”。
				return contracts.TaskHealthDead
			default:
				return runtimeHealth
			}
		}
		return runtimeHealth
	}
	// 运行态探测失败：保守处理为 unknown，不降级到 dead/backlog。
	if runtimeHealth == contracts.TaskHealthDead {
		return contracts.TaskHealthUnknown
	}
	return runtimeHealth
}
