package ticketatomic

import (
	"fmt"
	"strings"

	"dalek/internal/contracts"
)

type BaseResolution struct {
	BaseBranch      string
	FreezeTargetRef string
}

func NormalizeTargetRefInput(raw string) (string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return "", fmt.Errorf("target_ref 不能为空")
	}
	if strings.EqualFold(ref, "HEAD") {
		return "", fmt.Errorf("target_ref 非法: %s", raw)
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		if strings.TrimSpace(strings.TrimPrefix(ref, "refs/heads/")) == "" {
			return "", fmt.Errorf("target_ref 非法: %s", raw)
		}
		return ref, nil
	}
	if strings.HasPrefix(ref, "refs/") {
		return "", fmt.Errorf("target_ref 仅支持 refs/heads/*: %s", raw)
	}
	short := strings.TrimSpace(strings.TrimPrefix(ref, "heads/"))
	if short == "" {
		return "", fmt.Errorf("target_ref 非法: %s", raw)
	}
	return "refs/heads/" + short, nil
}

func NormalizeOptionalTargetRef(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return NormalizeTargetRefInput(raw)
}

func CurrentBranchTargetRef(currentBranch string, currentErr error) (string, error) {
	if currentErr != nil {
		return "", currentErr
	}
	branch := strings.TrimSpace(currentBranch)
	if branch == "" || strings.EqualFold(branch, "HEAD") {
		return "", fmt.Errorf("current branch unavailable")
	}
	return NormalizeTargetRefInput(branch)
}

func ResolveCreateTargetRef(explicitRef, currentBranch string, currentErr error) (string, error) {
	if strings.TrimSpace(explicitRef) != "" {
		return NormalizeTargetRefInput(explicitRef)
	}
	targetRef, err := CurrentBranchTargetRef(currentBranch, currentErr)
	if err != nil {
		return "", fmt.Errorf("无法创建 ticket：当前仓库不在明确分支上，且未提供 --target-ref。ticket 必须在创建时冻结唯一 target_ref。请显式指定 --target-ref，或先 checkout 到目标分支后重试。")
	}
	return targetRef, nil
}

func ResolveStartBase(ticket contracts.Ticket, requestedBase, currentBranch string, currentErr error) (BaseResolution, error) {
	targetRef, err := NormalizeOptionalTargetRef(ticket.TargetBranch)
	if err != nil {
		return BaseResolution{}, fmt.Errorf("ticket t%d 的 target_ref 非法: %w", ticket.ID, err)
	}

	requestedRef := ""
	if strings.TrimSpace(requestedBase) != "" {
		requestedRef, err = NormalizeTargetRefInput(requestedBase)
		if err != nil {
			return BaseResolution{}, fmt.Errorf("ticket start 被拒绝：ticket t%d --base 非法: %w", ticket.ID, err)
		}
	}

	if targetRef != "" {
		if requestedRef != "" && requestedRef != targetRef {
			return BaseResolution{}, fmt.Errorf("ticket start 被拒绝：ticket t%d --base=%s 与 ticket.target_ref=%s 不一致。当前 ticket 是单 ref 原子任务，如需在新 ref 上执行，请创建新 ticket。", ticket.ID, requestedRef, targetRef)
		}
		return BaseResolution{BaseBranch: targetRef}, nil
	}

	if strings.EqualFold(strings.TrimSpace(ticket.Label), "integration") {
		return BaseResolution{}, fmt.Errorf("ticket 执行被拒绝：integration ticket t%d 缺少有效 target_ref。请修复 ticket.target_ref，或新建一张正确 target_ref 的 ticket。", ticket.ID)
	}

	currentRef, err := CurrentBranchTargetRef(currentBranch, currentErr)
	if err != nil {
		return BaseResolution{}, fmt.Errorf("ticket 执行被拒绝：ticket t%d 缺少 target_ref，且当前仓库不在明确分支上。请修复 ticket.target_ref，或先 checkout 到目标分支后重试。", ticket.ID)
	}
	if requestedRef != "" && requestedRef != currentRef {
		return BaseResolution{}, fmt.Errorf("ticket start 被拒绝：ticket t%d 仍是历史空 target_ref，首次冻结只能使用当前分支 %s，但收到 --base=%s。当前 ticket 是单 ref 原子任务，如需在新 ref 上执行，请创建新 ticket。", ticket.ID, currentRef, requestedRef)
	}
	return BaseResolution{
		BaseBranch:      currentRef,
		FreezeTargetRef: currentRef,
	}, nil
}
