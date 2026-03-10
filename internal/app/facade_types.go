package app

import (
	"fmt"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/repo"
	pmsvc "dalek/internal/services/pm"
	"dalek/internal/services/ticketlifecycle"
)

// ProjectConfig 是 app 层对外暴露的项目配置类型。
// repo.Config 不属于 contracts 公共接口体系，保留 facade 别名避免上层直接依赖 repo 包。
type ProjectConfig = repo.Config
type MergeSyncRefResult = pmsvc.SyncRefResult
type MergeRetargetResult = pmsvc.RetargetResult
type MergeRescanResult = pmsvc.RescanResult
type TicketLifecycleSnapshot = ticketlifecycle.SnapshotProjection
type TicketLifecycleConsistency = ticketlifecycle.ConsistencyCheck

// ParseTaskOwnerType 校验并转换 CLI 输入的 owner type 字符串。
func ParseTaskOwnerType(raw string) (contracts.TaskOwnerType, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", nil
	}
	switch contracts.TaskOwnerType(raw) {
	case contracts.TaskOwnerWorker, contracts.TaskOwnerPM, contracts.TaskOwnerSubagent, contracts.TaskOwnerChannel:
		return contracts.TaskOwnerType(raw), nil
	default:
		return "", fmt.Errorf("owner 仅支持 worker|pm|subagent|channel")
	}
}

// ParseInboxStatus 校验并转换 CLI 输入的 inbox 状态字符串。
func ParseInboxStatus(raw string) (contracts.InboxStatus, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(contracts.InboxOpen):
		return contracts.InboxOpen, nil
	case string(contracts.InboxDone):
		return contracts.InboxDone, nil
	case string(contracts.InboxSnoozed):
		return contracts.InboxSnoozed, nil
	default:
		return "", fmt.Errorf("非法 inbox 状态: %s", strings.TrimSpace(raw))
	}
}

// ParseMergeStatus 校验并转换 CLI 输入的 merge 状态字符串。
func ParseMergeStatus(raw string) (contracts.MergeStatus, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", nil
	}
	switch contracts.MergeStatus(raw) {
	case contracts.MergeProposed, contracts.MergeChecksRunning, contracts.MergeReady, contracts.MergeApproved, contracts.MergeMerged, contracts.MergeDiscarded, contracts.MergeBlocked:
		return contracts.MergeStatus(raw), nil
	default:
		return "", fmt.Errorf("非法 merge 状态: %s", strings.TrimSpace(raw))
	}
}
