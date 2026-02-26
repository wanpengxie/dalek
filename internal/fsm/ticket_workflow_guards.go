package fsm

import "dalek/internal/contracts"

// CanStartTicket 判断 ticket 是否允许执行 start 入口。
// 兼容历史行为：仅 done/archived 禁止 start，其余状态允许进入 start 流程。
func CanStartTicket(status contracts.TicketWorkflowStatus) bool {
	st := contracts.CanonicalTicketWorkflowStatus(status)
	return st != contracts.TicketDone && !TicketWorkflowTable.IsTerminal(st)
}

// CanDispatchTicket 判断 ticket 是否允许进入 dispatch（含 direct dispatch）。
func CanDispatchTicket(status contracts.TicketWorkflowStatus) bool {
	return CanStartTicket(status)
}

// CanArchiveTicket 判断 ticket 当前 workflow 状态是否允许归档（仅看 workflow 语义）。
func CanArchiveTicket(status contracts.TicketWorkflowStatus) bool {
	st := contracts.CanonicalTicketWorkflowStatus(status)
	if TicketWorkflowTable.IsTerminal(st) {
		return false
	}
	if CanTicketWorkflowTransition(st, contracts.TicketArchived) {
		return true
	}
	// 兼容历史数据：未知状态在 PM 归档入口仍允许落到 archived。
	return !TicketWorkflowTable.IsKnownState(st)
}

// CanManualSetWorkflowStatus 判断是否允许手工 set workflow_status。
func CanManualSetWorkflowStatus(current contracts.TicketWorkflowStatus) bool {
	return !TicketWorkflowTable.IsTerminal(contracts.CanonicalTicketWorkflowStatus(current))
}

// ShouldPromoteOnDispatchClaim 判断 dispatch claim 时是否需要推进到 active。
func ShouldPromoteOnDispatchClaim(status contracts.TicketWorkflowStatus) bool {
	st := contracts.CanonicalTicketWorkflowStatus(status)
	switch st {
	case contracts.TicketDone, contracts.TicketArchived, contracts.TicketActive:
		return false
	}
	if CanTicketWorkflowTransition(st, contracts.TicketActive) {
		return true
	}
	// 兼容历史行为：backlog/未知状态在 claim 时仍可被提升到 active。
	return st == contracts.TicketBacklog || !TicketWorkflowTable.IsKnownState(st)
}

// ShouldDemoteOnDispatchFailed 判断 dispatch failed 时是否需要降级到 blocked。
func ShouldDemoteOnDispatchFailed(status contracts.TicketWorkflowStatus) bool {
	st := contracts.CanonicalTicketWorkflowStatus(status)
	switch st {
	case contracts.TicketDone, contracts.TicketArchived, contracts.TicketBlocked:
		return false
	}
	if CanTicketWorkflowTransition(st, contracts.TicketBlocked) {
		return true
	}
	// 兼容历史行为：backlog/未知状态在 dispatch failed 时仍可降级到 blocked。
	return st == contracts.TicketBacklog || !TicketWorkflowTable.IsKnownState(st)
}

// ShouldApplyWorkerReport 判断 worker report 是否应继续尝试推进 workflow。
func ShouldApplyWorkerReport(status contracts.TicketWorkflowStatus) bool {
	return !TicketWorkflowTable.IsTerminal(contracts.CanonicalTicketWorkflowStatus(status))
}

// CanReportPromoteTo 判断 worker report 推进到目标状态是否合法。
func CanReportPromoteTo(current, target contracts.TicketWorkflowStatus) bool {
	from := contracts.CanonicalTicketWorkflowStatus(current)
	to := contracts.CanonicalTicketWorkflowStatus(target)
	if !ShouldApplyWorkerReport(from) {
		return false
	}
	if from == contracts.TicketDone && to == contracts.TicketActive {
		return false
	}
	if from == to {
		return true
	}
	if CanTicketWorkflowTransition(from, to) {
		return true
	}
	// 兼容历史行为：除 done->active 外，report 允许越级推进。
	return true
}
