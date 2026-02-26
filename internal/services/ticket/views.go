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

func ComputeTicketCapability(workflow contracts.TicketWorkflowStatus, w *contracts.Worker, sessionAlive bool, sessionProbeFailed bool, hasActiveDispatch bool, runtimeNeedsUser bool, runtimeHealth contracts.TaskRuntimeHealthState) contracts.TicketCapability {
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

func computeDerivedRuntimeHealth(latestWorker *contracts.Worker, sessionAlive bool, sessionProbeFailed bool, taskRunID uint, runtimeHealth contracts.TaskRuntimeHealthState) contracts.TaskRuntimeHealthState {
	if latestWorker == nil && taskRunID == 0 {
		return contracts.TaskHealthUnknown
	}
	if !sessionProbeFailed && !sessionAlive {
		// session 不在时，给一个更直观的派生态。
		if latestWorker != nil {
			switch latestWorker.Status {
			case contracts.WorkerStopped:
				return contracts.TaskHealthDead
			case contracts.WorkerFailed:
				return contracts.TaskHealthStalled
			case contracts.WorkerRunning:
				// session 不在线时，避免 UI 误显示“在线/运行中”。
				return contracts.TaskHealthDead
			default:
				return runtimeHealth
			}
		}
		return runtimeHealth
	}
	// session 探测失败：保守处理为 unknown，不降级到 dead/backlog。
	if runtimeHealth == contracts.TaskHealthDead {
		return contracts.TaskHealthUnknown
	}
	return runtimeHealth
}
