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

func ComputeTicketCapability(workflow contracts.TicketWorkflowStatus, w *contracts.Worker, sessionAlive bool, sessionProbeFailed bool, hasActiveWorkerRun bool, runtimeNeedsUser bool, runtimeHealth contracts.TaskRuntimeHealthState) contracts.TicketCapability {
	wf := contracts.TicketWorkflowStatus(strings.TrimSpace(strings.ToLower(string(workflow))))
	if wf == "" {
		wf = contracts.TicketBacklog
	}

	hasWorker := w != nil
	workerStatus := contracts.WorkerStatus("")
	hasRuntimeHandle := false
	hasRuntimeLog := false
	if w != nil {
		workerStatus = w.Status
		hasRuntimeHandle = strings.TrimSpace(w.LogPath) != ""
		hasRuntimeLog = strings.TrimSpace(w.LogPath) != ""
	}
	runtimeKnownDead := hasRuntimeHandle && !sessionProbeFailed && !sessionAlive

	cap := contracts.TicketCapability{}
	isDone := wf == contracts.TicketDone
	isArchived := wf == contracts.TicketArchived

	// start：只按 workflow 门禁（资源是否存在由后端决定是否复用/重建）。
	cap.CanStart = !isDone && !isArchived

	// queue run：默认允许自动 start，所以仅保留 workflow/并发门禁。
	cap.CanQueueRun = !isDone &&
		!isArchived &&
		!hasActiveWorkerRun
	cap.CanDispatch = cap.CanQueueRun

	// attach：只支持 runtime 日志 attach。
	cap.CanAttach = hasWorker &&
		(hasRuntimeHandle && hasRuntimeLog)

	// stop：仅 runtime 句柄可停止。
	cap.CanStop = hasWorker && hasRuntimeHandle

	// archive：要求没有 running worker，且没有进行中的 worker run。
	cap.CanArchive = !isArchived &&
		!hasActiveWorkerRun &&
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
	case hasActiveWorkerRun:
		cap.Reason = "worker run 进行中"
	case !hasWorker:
		cap.Reason = "将自动准备 worker"
	case hasRuntimeHandle && !hasRuntimeLog:
		cap.Reason = "worker 缺少日志路径（启动执行前将自动修复）"
	case !hasRuntimeHandle:
		cap.Reason = "worker 缺少运行日志锚点（启动执行前将自动修复）"
	case runtimeKnownDead:
		cap.Reason = "worker 运行通道不在线"
	case sessionProbeFailed:
		cap.Reason = "运行态探测失败"
	}

	return cap
}

func computeDerivedRuntimeHealth(latestWorker *contracts.Worker, sessionAlive bool, sessionProbeFailed bool, taskRunID uint, hasActiveWorkerRun bool, runtimeHealth contracts.TaskRuntimeHealthState) contracts.TaskRuntimeHealthState {
	if hasActiveWorkerRun {
		if runtimeHealth == contracts.TaskHealthWaitingUser || runtimeHealth == contracts.TaskHealthStalled {
			return runtimeHealth
		}
		return contracts.TaskHealthBusy
	}
	if latestWorker == nil && taskRunID == 0 {
		return contracts.TaskHealthUnknown
	}
	if latestWorker == nil || strings.TrimSpace(latestWorker.LogPath) == "" {
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
