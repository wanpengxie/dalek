package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalek/internal/services/subagent"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// PMRunInput is the input for submitting a PM agent run.
type PMRunInput struct {
	FocusID     uint   // convergent focus run ID
	RoundNumber int    // 1-based round number
	TicketIDs   []uint // ticket IDs executed in this batch round
	ReviewDir   string // review output directory
}

// PMRunResult is the parsed result of a PM agent run.
type PMRunResult struct {
	TaskRunID       uint   // subagent task_run_id
	Converged       bool   // PM agent verdict: converged
	FixTicketIDs    []uint // fix tickets created when not converged
	EffectiveIssues int    // number of real issues found
	FilteredIssues  int    // number of issues filtered out
	Summary         string // one-line summary
	Error           string // error message if any
}

// pmRunResultFile mirrors the JSON schema written by PM agent to result.json.
type pmRunResultFile struct {
	Verdict              string `json:"verdict"` // converged | needs_fix
	FixTicketIDs         []uint `json:"fix_ticket_ids"`
	EffectiveIssuesCount int    `json:"effective_issues_count"`
	FilteredIssuesCount  int    `json:"filtered_issues_count"`
	Summary              string `json:"summary"`
}

// ---------------------------------------------------------------------------
// PMRunSubmitter — interface for testability
// ---------------------------------------------------------------------------

// PMRunSubmitter abstracts the subagent.Submit call for PM run so that tests
// can inject a mock without standing up a full subagent service.
type PMRunSubmitter interface {
	Submit(ctx context.Context, in subagent.SubmitInput) (subagent.SubmitResult, error)
}

// ---------------------------------------------------------------------------
// submitPMRun
// ---------------------------------------------------------------------------

// submitPMRun builds the PM prompt, submits a PM agent run via the given
// submitter and returns the task_run_id. The caller (convergent controller)
// is responsible for driving the actual execution (subagent.Run).
func (s *Service) submitPMRun(ctx context.Context, submitter PMRunSubmitter, input PMRunInput) (PMRunResult, error) {
	if submitter == nil {
		return PMRunResult{}, fmt.Errorf("PMRunSubmitter 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	prompt := buildPMRunPrompt(input)
	if strings.TrimSpace(prompt) == "" {
		return PMRunResult{}, fmt.Errorf("构建 PM run prompt 失败：结果为空")
	}

	// Resolve PM agent provider/model from config.
	cfg := s.p.Config.WithDefaults().PMAgent
	provider := strings.TrimSpace(cfg.Provider)
	model := strings.TrimSpace(cfg.Model)

	requestID := newPMRequestID("pm_run")

	res, err := submitter.Submit(ctx, subagent.SubmitInput{
		RequestID: requestID,
		Provider:  provider,
		Model:     model,
		Prompt:    prompt,
	})
	if err != nil {
		return PMRunResult{
			Error: fmt.Sprintf("提交 PM agent run 失败: %s", err.Error()),
		}, err
	}

	s.slog().Info("convergent: PM run submitted",
		"focus_id", input.FocusID,
		"round", input.RoundNumber,
		"task_run_id", res.TaskRunID,
		"provider", provider,
		"model", model,
	)

	return PMRunResult{
		TaskRunID: res.TaskRunID,
	}, nil
}

// ---------------------------------------------------------------------------
// parsePMRunResult
// ---------------------------------------------------------------------------

// parsePMRunResult reads and parses {reviewDir}/result.json produced by PM
// agent. Returns a fully populated PMRunResult or an error if the file is
// missing or malformed.
func parsePMRunResult(reviewDir string) (PMRunResult, error) {
	reviewDir = strings.TrimSpace(reviewDir)
	if reviewDir == "" {
		return PMRunResult{}, fmt.Errorf("review 目录路径为空")
	}

	resultPath := filepath.Join(reviewDir, "result.json")
	data, err := os.ReadFile(resultPath)
	if err != nil {
		if os.IsNotExist(err) {
			return PMRunResult{}, fmt.Errorf("PM agent 未产出 result.json: %s", resultPath)
		}
		return PMRunResult{}, fmt.Errorf("读取 result.json 失败: %w", err)
	}

	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return PMRunResult{}, fmt.Errorf("result.json 内容为空: %s", resultPath)
	}

	var file pmRunResultFile
	if err := json.Unmarshal(data, &file); err != nil {
		return PMRunResult{}, fmt.Errorf("解析 result.json 失败: %w", err)
	}

	verdict := strings.TrimSpace(strings.ToLower(file.Verdict))
	if verdict != "converged" && verdict != "needs_fix" {
		return PMRunResult{}, fmt.Errorf("result.json 中 verdict 值非法: %q（期望 converged 或 needs_fix）", file.Verdict)
	}

	return PMRunResult{
		Converged:       verdict == "converged",
		FixTicketIDs:    file.FixTicketIDs,
		EffectiveIssues: file.EffectiveIssuesCount,
		FilteredIssues:  file.FilteredIssuesCount,
		Summary:         strings.TrimSpace(file.Summary),
	}, nil
}

// ---------------------------------------------------------------------------
// buildPMRunPrompt
// ---------------------------------------------------------------------------

// buildPMRunPrompt constructs the full PM agent prompt following spec §8.
// All dynamic variables ({focus_id}, {round_number}, {ticket_ids},
// {review_output_dir}) are interpolated into the template.
func buildPMRunPrompt(input PMRunInput) string {
	ticketList := buildTicketIDList(input.TicketIDs)
	reviewDir := strings.TrimSpace(input.ReviewDir)

	return fmt.Sprintf(`你是 PM agent。上一轮 batch run 已完成，你的任务是审查交付质量并决定是否需要修复。

## 上下文
- Convergent Focus ID: %d
- 当前轮次: Round %d
- 本轮执行的 tickets: %s
- Review 输出目录: %s

## 你的工作流程

### Step 1: 分析 batch 结果
查看每个 ticket 的交付情况：
- `+"`dalek ticket show --ticket {id}`"+` 了解任务和状态
- 在 repo root 查看 merge 后的代码变更

### Step 2: 调起多角度 code review
从不同 AI 模型获取独立审查意见：

`+"```"+`bash
dalek agent run --sync --provider codex --timeout 30m --prompt "
你是 code reviewer。审查以下 tickets 的代码变更：%s。
审查重点：正确性、安全性、边界条件、测试覆盖、架构一致性。
对每个问题标注严重级别：critical（bug/安全）、major（质量）、minor（建议）、style（风格）。
将审查结果写入 %s/review-codex.md
"
`+"```"+`

`+"```"+`bash
dalek agent run --sync --provider claude --timeout 30m --prompt "
你是 code reviewer。审查以下 tickets 的代码变更：%s。
审查重点：正确性、安全性、边界条件、测试覆盖、架构一致性。
对每个问题标注严重级别：critical（bug/安全）、major（质量）、minor（建议）、style（风格）。
将审查结果写入 %s/review-claude.md
"
`+"```"+`

### Step 3: 批判性综合
读取两份 review 结果（%s/review-codex.md 和 review-claude.md），批判性地整理：

1. 识别两份 review 的共同发现和独立发现
2. 过滤误判：
   - 对正确代码的错误指摘
   - 对已有测试覆盖的遗漏指摘
   - 过度保守的风格建议
   - severity=style 的纯风格问题
3. 只保留真正的 bug 和 blocker（critical/major 级别）
4. 将综合分析写入 %s/synthesis.md

### Step 4: 判定与执行

**若无有效 bug/blocker（收敛）**：
- 在 synthesis.md 中记录 "verdict: converged"
- 直接退出

**若有 bug/blocker 需要修复**：
- 将修复 spec 写入 %s/fix-spec.md
- 为每个修复项创建 ticket：
  `+"```"+`bash
  dalek ticket create --title "[fix] R%d: {问题简述}" \
      --description "修复描述（含问题定位、修复指导、涉及文件）" \
      --label convergent-fix \
      --priority high
  `+"```"+`
- 创建完毕后，清理 repo root：
  `+"```"+`bash
  git checkout -- .
  git clean -fd
  `+"```"+`
  并验证 `+"`git status`"+` 干净
- 退出

## 输出约定
退出前，请在 %s/result.json 中写入：
`+"```"+`json
{
  "verdict": "converged|needs_fix",
  "fix_ticket_ids": [若有，新创建的 ticket IDs],
  "effective_issues_count": 有效问题数,
  "filtered_issues_count": 被过滤问题数,
  "summary": "一句话总结"
}
`+"```"+`

## 硬约束
- 你不直接修复代码，只审查和派发
- 修复通过创建 ticket 委托给 worker
- 清理 repo root 是你退出前的必须动作（如果创建了 fix tickets）
- review 文档必须落盘到指定目录`,
		input.FocusID,
		input.RoundNumber,
		ticketList,
		reviewDir,
		// Step 2 codex
		ticketList,
		reviewDir,
		// Step 2 claude
		ticketList,
		reviewDir,
		// Step 3
		reviewDir,
		reviewDir,
		// Step 4
		reviewDir,
		input.RoundNumber,
		// 输出约定
		reviewDir,
	)
}

// ---------------------------------------------------------------------------
// ensureReviewDir
// ---------------------------------------------------------------------------

// ensureReviewDir creates the review output directory for a given convergent
// focus round and returns the path.
// Format: .dalek/pm/reviews/convergent-{focusID}/round-{round}
func ensureReviewDir(repoRoot string, focusID uint, round int) (string, error) {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return "", fmt.Errorf("repo root 路径为空")
	}
	if focusID == 0 {
		return "", fmt.Errorf("focus ID 不能为 0")
	}
	if round <= 0 {
		return "", fmt.Errorf("round 必须 >= 1")
	}

	dir := filepath.Join(repoRoot, ".dalek", "pm", "reviews",
		fmt.Sprintf("convergent-%d", focusID),
		fmt.Sprintf("round-%d", round))

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建 review 目录失败: %w", err)
	}
	return dir, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildTicketIDList formats ticket IDs as a comma-separated list like
// "t12, t13, t14".
func buildTicketIDList(ids []uint) string {
	if len(ids) == 0 {
		return "(无)"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("t%d", id))
	}
	return strings.Join(parts, ", ")
}
