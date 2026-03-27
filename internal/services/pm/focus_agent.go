package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/repo"
)

// focusAgentAction 是 PM agent triage 的返回动作。
type focusAgentAction struct {
	Action string `json:"action"` // restart | skip_merge | wait_user
	Reason string `json:"reason"`
}

// callPMAgentTriage 调用 PM agent 判断 blocked/failed ticket 的处理方式。
func (s *Service) callPMAgentTriage(ctx context.Context, ticketID uint, status string, summary string) (focusAgentAction, error) {
	prompt := fmt.Sprintf(`你是 PM agent。ticket T%d 执行遇到问题：
- 状态：%s
- 原因：%s

请查看 ticket 详情（dalek ticket show --ticket %d）了解更多上下文后，判断下一步操作。

选择之一：
- restart：重新执行此 ticket（适用于临时错误、可重试的失败）
- skip_merge：跳过失败直接合并当前代码到目标分支（适用于非关键失败如测试环境缺失，开发代码本身没问题）
- wait_user：需要用户介入（适用于缺少权限/凭证/外部资源/不可替代的业务决策）

只回复 JSON：{"action": "restart|skip_merge|wait_user", "reason": "你的判断理由"}`,
		ticketID, status, summary, ticketID)

	result, err := s.runPMAgent(ctx, prompt)
	if err != nil {
		return focusAgentAction{Action: "wait_user", Reason: "PM agent 调用失败: " + err.Error()}, nil
	}

	var action focusAgentAction
	// 尝试从 agent 输出中提取 JSON
	text := strings.TrimSpace(result.Text)
	if idx := strings.Index(text, "{"); idx >= 0 {
		if end := strings.LastIndex(text, "}"); end > idx {
			text = text[idx : end+1]
		}
	}
	if err := json.Unmarshal([]byte(text), &action); err != nil {
		// 如果解析失败，尝试从文本中识别关键词
		lower := strings.ToLower(result.Text)
		switch {
		case strings.Contains(lower, "restart"):
			action = focusAgentAction{Action: "restart", Reason: "从输出中推断"}
		case strings.Contains(lower, "skip_merge") || strings.Contains(lower, "skip"):
			action = focusAgentAction{Action: "skip_merge", Reason: "从输出中推断"}
		default:
			action = focusAgentAction{Action: "wait_user", Reason: "无法解析 PM agent 输出，默认等待用户"}
		}
	}

	// 验证 action 合法性
	switch action.Action {
	case "restart", "skip_merge", "wait_user":
		// ok
	default:
		action.Action = "wait_user"
		action.Reason = "未知动作 " + action.Action + "，默认等待用户"
	}
	return action, nil
}

// callPMAgentResolveConflict 调用 PM agent 解决 merge 冲突。
func (s *Service) callPMAgentResolveConflict(ctx context.Context, ticketID uint, branch string, attempt int) error {
	var prompt string
	if attempt == 0 {
		prompt = fmt.Sprintf(`你是 PM agent，当前在 repo root 目录。

正在将 ticket T%d 的分支 %s merge 到目标分支，发生了冲突。

请：
1. 运行 git status 了解冲突文件
2. 解决所有冲突（保留两边的有效改动，参考 ticket 的上下文理解改动意图）
3. git add 所有解决后的文件
4. git commit 完成 merge

commit message：Merge branch '%s' (ticket T%d)

重要：必须确保所有冲突标记都被清除，且 git commit 成功执行。`, ticketID, branch, branch, ticketID)
	} else {
		prompt = fmt.Sprintf(`你是 PM agent。上一次尝试解决 merge 冲突未成功，仍有未解决的文件。
这是第 %d 次重试。

请检查 git status，解决所有剩余冲突，执行 git add 并 git commit 完成 merge。
commit message：Merge branch '%s' (ticket T%d)`, attempt+1, branch, ticketID)
	}

	_, err := s.runPMAgent(ctx, prompt)
	return err
}

// runPMAgent 使用 sdkrunner 同步调用 PM agent。
func (s *Service) runPMAgent(ctx context.Context, prompt string) (sdkrunner.Result, error) {
	pmRole := s.p.Config.WithDefaults().PMAgent
	providers := s.p.Providers
	if len(providers) == 0 {
		providers = repo.DefaultProviders()
	}
	agentCfg, err := repo.ResolveAgentConfig(pmRole.Provider, providers)
	if err != nil {
		return sdkrunner.Result{}, fmt.Errorf("pm_agent provider 解析失败: %w", err)
	}

	repoRoot := s.p.RepoRoot
	s.slog().Info("focus: calling PM agent",
		"work_dir", repoRoot,
		"provider", agentCfg.Provider,
		"model", agentCfg.Model,
	)

	result, err := sdkrunner.Run(ctx, sdkrunner.Request{
		AgentConfig: agentCfg,
		Prompt:      prompt,
		WorkDir:     repoRoot,
	}, func(ev sdkrunner.Event) {
		// 流式事件，暂不处理
	})
	if err != nil {
		s.slog().Warn("focus: PM agent call failed", "error", err)
		return result, err
	}
	s.slog().Info("focus: PM agent call completed",
		"text_len", len(result.Text),
	)
	return result, nil
}
