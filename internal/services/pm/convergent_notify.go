package pm

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"dalek/internal/contracts"
)

// ---------------------------------------------------------------------------
// ConvergentNotifier — convergent PM review 结果推送接口
// ---------------------------------------------------------------------------

// ConvergentNotifier 将 convergent PM review 的结果推送到外部通道（如飞书）。
// 实现应在无 binding 时静默跳过，不返回错误。
type ConvergentNotifier interface {
	// NotifyText 发送一段纯文本通知。
	NotifyText(ctx context.Context, text string) error
}

// convergentNotifyTimeout 限制单次通知的最长等待时间。
const convergentNotifyTimeout = 15 * time.Second

// ---------------------------------------------------------------------------
// 异步发送 helper
// ---------------------------------------------------------------------------

// convergentNotifyAsync 异步发送通知，不阻塞主流程。
func (s *Service) convergentNotifyAsync(text string) {
	if s == nil {
		return
	}
	notifier := s.getConvergentNotifier()
	if notifier == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.statusHookWG.Add(1)
	go func() {
		defer s.statusHookWG.Done()
		defer func() {
			if r := recover(); r != nil {
				s.slog().Error("convergent notify panic",
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), convergentNotifyTimeout)
		defer cancel()
		if err := notifier.NotifyText(ctx, text); err != nil {
			s.slog().Warn("convergent notify failed",
				"error", err,
				"text_len", len(text),
			)
		}
	}()
}

// ---------------------------------------------------------------------------
// 通知文本构建
// ---------------------------------------------------------------------------

// convergentBuildConvergedText 构建 converged 通知文本。
func convergentBuildConvergedText(run contracts.FocusRun, round contracts.ConvergentRound, result PMRunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Convergent] Focus #%d Round %d — CONVERGED ✓\n", run.ID, round.RoundNumber)
	fmt.Fprintf(&b, "- 总轮次: %d\n", round.RoundNumber)
	fmt.Fprintf(&b, "- PM review 次数: %d/%d\n", run.PMRunCount, run.MaxPMRuns)
	fmt.Fprintf(&b, "- 过滤问题: %d\n", result.FilteredIssues)
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		fmt.Fprintf(&b, "- 结论: %s", summary)
	}
	return b.String()
}

// convergentBuildNeedsFixText 构建 needs_fix 通知文本。
func convergentBuildNeedsFixText(run contracts.FocusRun, round contracts.ConvergentRound, result PMRunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Convergent] Focus #%d Round %d — NEEDS FIX\n", run.ID, round.RoundNumber)
	fmt.Fprintf(&b, "- 有效问题: %d\n", result.EffectiveIssues)
	fmt.Fprintf(&b, "- 过滤问题: %d\n", result.FilteredIssues)
	if len(result.FixTicketIDs) > 0 {
		ids := make([]string, 0, len(result.FixTicketIDs))
		for _, id := range result.FixTicketIDs {
			ids = append(ids, fmt.Sprintf("t%d", id))
		}
		fmt.Fprintf(&b, "- 修复 tickets: %s\n", strings.Join(ids, ", "))
	}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		fmt.Fprintf(&b, "- 结论: %s\n", summary)
	}
	fmt.Fprintf(&b, "- 下一步: Round %d batch 将自动执行修复", round.RoundNumber+1)
	return b.String()
}

// convergentBuildExhaustedText 构建 exhausted 通知文本。
func convergentBuildExhaustedText(run contracts.FocusRun, round contracts.ConvergentRound) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Convergent] Focus #%d — EXHAUSTED\n", run.ID)
	fmt.Fprintf(&b, "- PM review 已达上限 %d/%d\n", run.PMRunCount, run.MaxPMRuns)
	fmt.Fprintf(&b, "- 仍有未解决问题，需要人工介入")
	if reviewPath := strings.TrimSpace(round.ReviewPath); reviewPath != "" {
		fmt.Fprintf(&b, "\n- Review 报告: %s", reviewPath)
	}
	return b.String()
}
