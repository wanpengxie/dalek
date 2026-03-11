package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
)

const (
	workerBootstrapKernelTemplate = "templates/project/control/skills/start-ticket-runtime/assets/worker-agents.md.template"
	workerBootstrapPlanTemplate   = "templates/project/control/skills/start-ticket-runtime/assets/plan.md.template"

	workerKernelTitlePlaceholder       = "{{TICKET_TITLE：业务任务标题。用于定义本轮交付主题，回答“这轮要完成什么业务目标”。}}"
	workerKernelDescPlaceholder        = "{{TICKET_DESCRIPTION：业务需求正文。用于说明背景、目标、约束与期望结果，回答“为什么做、做到什么算有效”。}}"
	workerKernelAttachmentsPlaceholder = "{{OTHER_DOCUMENTS：需求相关的附属资料与上下文集合。可包含对话记录、GitHub issue、用户与 agent 的详细讨论文档，以及文档路径/接口说明/截图线索等。它是参考输入，不是执行步骤。}}"
	workerKernelPlanRefPlaceholder     = "{{PLAN_REF：执行规划主文档入口，默认 @.dalek/PLAN.md。必须持续对齐 PLAN 中的需求拆解、方案与验证口径。}}"

	workerPlanTitlePlaceholder       = "{{TICKET_TITLE}}"
	workerPlanDescriptionPlaceholder = "{{TICKET_DESCRIPTION}}"
	workerPlanPromptPlaceholder      = "{{ENTRY_PROMPT}}"
)

type bootstrapGitFacts = repo.WorktreeGitBaseline

type bootstrapStateFile struct {
	Ticket struct {
		ID       string `json:"id"`
		WorkerID string `json:"worker_id"`
	} `json:"ticket"`
	Phases    bootstrapStatePhases `json:"phases"`
	Blockers  []string             `json:"blockers"`
	Code      bootstrapCodeState   `json:"code"`
	UpdatedAt string               `json:"updated_at"`
}

type bootstrapStatePhases struct {
	CurrentID     string               `json:"current_id"`
	CurrentStatus string               `json:"current_status"`
	NextAction    string               `json:"next_action"`
	Summary       string               `json:"summary"`
	Items         []bootstrapPhaseItem `json:"items"`
}

type bootstrapPhaseItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Goal   string `json:"goal"`
	Status string `json:"status"`
	Order  int    `json:"order"`
}

type bootstrapCodeState struct {
	HeadSHA           string `json:"head_sha"`
	WorkingTree       string `json:"working_tree"`
	LastCommitSubject string `json:"last_commit_subject"`
}

func (s *Service) ensureWorkerBootstrap(ctx context.Context, t contracts.Ticket, w contracts.Worker, entryPrompt string) (repo.ContractPaths, error) {
	p, db, err := s.require()
	if err != nil {
		return repo.ContractPaths{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if t.ID == 0 {
		return repo.ContractPaths{}, fmt.Errorf("bootstrap 失败：ticket 不能为空")
	}
	if w.ID == 0 {
		return repo.ContractPaths{}, fmt.Errorf("bootstrap 失败：worker 不能为空")
	}
	if strings.TrimSpace(w.WorktreePath) == "" {
		return repo.ContractPaths{}, fmt.Errorf("bootstrap 失败：worker worktree_path 为空")
	}

	var latest contracts.Ticket
	if err := db.WithContext(ctx).First(&latest, t.ID).Error; err != nil {
		return repo.ContractPaths{}, err
	}

	paths, err := repo.EnsureWorktreeContract(strings.TrimSpace(w.WorktreePath))
	if err != nil {
		return repo.ContractPaths{}, err
	}
	now := time.Now()
	gitFacts := repo.InspectWorktreeGitBaseline(ctx, strings.TrimSpace(w.WorktreePath), p.Git)

	kernelContent, err := renderWorkerKernelBootstrap(latest, w, gitFacts, now)
	if err != nil {
		return repo.ContractPaths{}, err
	}
	planContent, err := renderWorkerPlanBootstrap(latest, w, entryPrompt)
	if err != nil {
		return repo.ContractPaths{}, err
	}
	stateContent, err := renderWorkerStateBootstrap(latest, w, gitFacts, now)
	if err != nil {
		return repo.ContractPaths{}, err
	}

	if err := ensureBootstrapFile(paths.AgentKernelMD, kernelContent, 0o644, false); err != nil {
		return repo.ContractPaths{}, err
	}
	if err := ensureBootstrapFile(paths.PlanMD, planContent, 0o644, false); err != nil {
		return repo.ContractPaths{}, err
	}
	if err := ensureBootstrapFile(paths.StateJSON, stateContent, 0o644, true); err != nil {
		return repo.ContractPaths{}, err
	}
	return paths, nil
}

func renderWorkerKernelBootstrap(t contracts.Ticket, w contracts.Worker, facts bootstrapGitFacts, now time.Time) (string, error) {
	raw, err := repo.ReadSeedTemplate(workerBootstrapKernelTemplate)
	if err != nil {
		return "", err
	}
	rendered := strings.NewReplacer(
		workerKernelTitlePlaceholder, bootstrapTicketTitle(t),
		workerKernelDescPlaceholder, bootstrapTicketDescription(t),
		workerKernelAttachmentsPlaceholder, bootstrapAttachments(t, w),
		workerKernelPlanRefPlaceholder, "@.dalek/PLAN.md",
	).Replace(raw)

	rendered, err = replaceTaggedSection(rendered, "current_state", bootstrapCurrentState(t, w, facts, now))
	if err != nil {
		return "", err
	}
	return rendered, nil
}

func renderWorkerPlanBootstrap(t contracts.Ticket, w contracts.Worker, entryPrompt string) (string, error) {
	raw, err := repo.ReadSeedTemplate(workerBootstrapPlanTemplate)
	if err != nil {
		return "", err
	}
	description := bootstrapPlanDescription(t, w)
	prompt := strings.TrimSpace(entryPrompt)
	if prompt == "" {
		prompt = defaultContinuePrompt
	}
	return strings.NewReplacer(
		workerPlanTitlePlaceholder, bootstrapTicketTitle(t),
		workerPlanDescriptionPlaceholder, description,
		workerPlanPromptPlaceholder, prompt,
	).Replace(raw), nil
}

func renderWorkerStateBootstrap(t contracts.Ticket, w contracts.Worker, facts bootstrapGitFacts, now time.Time) (string, error) {
	state := bootstrapStateFile{
		Phases: bootstrapStatePhases{
			CurrentID:     "phase-understanding",
			CurrentStatus: "running",
			NextAction:    string(contracts.NextContinue),
			Summary:       "bootstrap 已生成，等待 worker 读取上下文、代码和文档后推进实现",
			Items: []bootstrapPhaseItem{
				{
					ID:     "phase-understanding",
					Name:   "需求理解与代码探索",
					Goal:   "读取 bootstrap 上下文、ticket 事实与源码，确认约束与方案",
					Status: "in_progress",
					Order:  1,
				},
				{
					ID:     "phase-implementation",
					Name:   "实现改动",
					Goal:   "按探索结论实现需求并保持改动可审计",
					Status: "pending",
					Order:  2,
				},
				{
					ID:     "phase-validation",
					Name:   "验证结果",
					Goal:   "执行必要测试并确认验收口径",
					Status: "pending",
					Order:  3,
				},
				{
					ID:     "phase-handoff",
					Name:   "收口与汇报",
					Goal:   "同步状态、总结风险并执行 worker report",
					Status: "pending",
					Order:  4,
				},
			},
		},
		Blockers: []string{},
		Code: bootstrapCodeState{
			HeadSHA:           bootstrapFactOrUnknown(facts.HeadSHA),
			WorkingTree:       bootstrapWorkingTreeStatus(facts.WorkingTreeStatus),
			LastCommitSubject: bootstrapFactOrUnknown(facts.LastCommitSubject),
		},
		UpdatedAt: now.Format(time.RFC3339),
	}
	state.Ticket.ID = fmt.Sprintf("%d", t.ID)
	state.Ticket.WorkerID = fmt.Sprintf("%d", w.ID)

	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("渲染 state.json 失败: %w", err)
	}
	return string(append(b, '\n')), nil
}

func buildWorkerEntrypointPrompt(entryPrompt string) string {
	supplemental := strings.TrimSpace(entryPrompt)
	if supplemental == "" {
		supplemental = defaultContinuePrompt
	}
	const prompt = "你正在当前 ticket 的 worker worktree 中继续交付。\n" +
		"先读取并遵循 `.dalek/agent-kernel.md`，再按其中 context_loading 顺序读取 `.dalek/state.json` 和 `.dalek/PLAN.md`。\n" +
		"以本地代码、git 检查点和 worktree 事实为真相源推进实现，并在本轮结束前执行 dalek worker report --next <continue|done|wait_user> --summary \"...\"。\n\n" +
		"本轮补充指令：%s"
	return strings.TrimSpace(fmt.Sprintf(prompt, supplemental))
}

func bootstrapCurrentState(t contracts.Ticket, w contracts.Worker, facts bootstrapGitFacts, now time.Time) string {
	return strings.TrimSpace(fmt.Sprintf(`当前运行状态（必须持续维护，不得删除本区块）：

1. 当前阶段：phase-understanding（需求理解与代码探索）
   本轮目标：读取 bootstrap 上下文、ticket 事实、代码与文档，形成可执行方案并开始实现。

2. phases 结构（4 个阶段）：
   - phase-understanding: 需求理解与代码探索 (in_progress)
   - phase-implementation: 实现改动 (pending)
   - phase-validation: 验证结果 (pending)
   - phase-handoff: 收口与汇报 (pending)

3. 阻塞与风险：当前无已知阻塞。主要风险是 ticket 真实约束、worktree 基线和现有实现可能存在偏差，需要先对账再编码。

4. 代码状态：HEAD=%s / working tree=%s / last_commit=%s
   ticket=t%d worker=w%d target_ref=%s worktree=%s

5. 下一步：按 context_loading 顺序读取 state.json、PLAN.md 和 git 基线，然后开始探索需求与实现路径。

6. 更新时间：%s`,
		bootstrapFactOrUnknown(facts.HeadSHA),
		bootstrapWorkingTreeStatus(facts.WorkingTreeStatus),
		bootstrapFactOrUnknown(facts.LastCommitSubject),
		t.ID,
		w.ID,
		bootstrapTargetRef(t),
		bootstrapFactOrUnknown(strings.TrimSpace(w.WorktreePath)),
		now.Format(time.RFC3339),
	))
}

func bootstrapAttachments(t contracts.Ticket, w contracts.Worker) string {
	return strings.TrimSpace(fmt.Sprintf(`Ticket 事实（self-driven bootstrap）：
- ticket_id: t%d
- worker_id: w%d
- target_ref: %s
- worktree: %s
- worker_branch: %s

本地文档入口：
- @.dalek/PLAN.md
- README.md（如果存在）
- docs/ 与仓库内相关设计文档（如果存在）

说明：worker 需要先基于这些本地事实自行探索代码库，再补充计划与实现细节。`,
		t.ID,
		w.ID,
		bootstrapTargetRef(t),
		bootstrapFactOrUnknown(strings.TrimSpace(w.WorktreePath)),
		bootstrapFactOrUnknown(strings.TrimSpace(w.Branch)),
	))
}

func bootstrapPlanDescription(t contracts.Ticket, w contracts.Worker) string {
	base := strings.TrimSpace(t.Description)
	if base == "" {
		base = "(ticket 未提供额外描述，请先从代码、git 历史和本地文档补齐上下文)"
	}
	return strings.TrimSpace(fmt.Sprintf(`%s

已知启动事实：
- ticket_id: t%d
- worker_id: w%d
- target_ref: %s
- worktree: %s
- worker_branch: %s`,
		base,
		t.ID,
		w.ID,
		bootstrapTargetRef(t),
		bootstrapFactOrUnknown(strings.TrimSpace(w.WorktreePath)),
		bootstrapFactOrUnknown(strings.TrimSpace(w.Branch)),
	))
}

func bootstrapTicketTitle(t contracts.Ticket) string {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		return fmt.Sprintf("t%d", t.ID)
	}
	return title
}

func bootstrapTicketDescription(t contracts.Ticket) string {
	desc := strings.TrimSpace(t.Description)
	if desc == "" {
		return "(ticket 未提供额外描述，请先从本地代码、git 历史和 repo 文档中补齐上下文)"
	}
	return desc
}

func bootstrapTargetRef(t contracts.Ticket) string {
	target := strings.TrimSpace(t.TargetBranch)
	if target == "" {
		return "(unset)"
	}
	return target
}

func bootstrapFactOrUnknown(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	return v
}

func bootstrapWorkingTreeStatus(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "dirty":
		return "dirty"
	case "clean":
		return "clean"
	default:
		return "unknown"
	}
}

func replaceTaggedSection(raw, tag, content string) (string, error) {
	open := "<" + strings.TrimSpace(tag) + ">"
	close := "</" + strings.TrimSpace(tag) + ">"
	start := strings.Index(raw, open)
	if start < 0 {
		return "", fmt.Errorf("bootstrap 模板缺少区块 %s", open)
	}
	end := strings.Index(raw[start:], close)
	if end < 0 {
		return "", fmt.Errorf("bootstrap 模板缺少区块 %s", close)
	}
	end += start
	return raw[:start+len(open)] + "\n" + strings.TrimSpace(content) + "\n" + raw[end:], nil
}

func ensureBootstrapFile(path, content string, mode os.FileMode, jsonStrict bool) error {
	existing, err := os.ReadFile(path)
	if err == nil && !bootstrapFileNeedsRefresh(existing, jsonStrict) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取 bootstrap 文件失败(%s): %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("写入 bootstrap 文件失败(%s): %w", path, err)
	}
	return nil
}

func bootstrapFileNeedsRefresh(content []byte, jsonStrict bool) bool {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return true
	}
	if strings.Contains(trimmed, "{{") {
		return true
	}
	if jsonStrict && !json.Valid(content) {
		return true
	}
	return false
}
