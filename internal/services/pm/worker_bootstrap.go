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
	workerKernelTitlePlaceholder       = "{{TICKET_TITLE：业务任务标题。用于定义本轮交付主题，回答“这轮要完成什么业务目标”。}}"
	workerKernelDescPlaceholder        = "{{TICKET_DESCRIPTION：业务需求正文。用于说明背景、目标、约束与期望结果，回答“为什么做、做到什么算有效”。}}"
	workerKernelAttachmentsPlaceholder = "{{OTHER_DOCUMENTS：需求相关的附属资料与上下文集合。可包含对话记录、GitHub issue、用户与 agent 的详细讨论文档，以及文档路径/接口说明/截图线索等。它是参考输入，不是执行步骤。}}"

	workerStateTicketIDPlaceholder          = "{{DALEK_TICKET_ID}}"
	workerStateWorkerIDPlaceholder          = "{{DALEK_WORKER_ID}}"
	workerStateSummaryPlaceholder           = "{{SUMMARY}}"
	workerStateHeadSHAPlaceholder           = "{{HEAD_SHA}}"
	workerStateWorkingTreeStatusPlaceholder = "{{WORKING_TREE_STATUS}}"
	workerStateLastCommitSubjectPlaceholder = "{{LAST_COMMIT_SUBJECT}}"
	workerStateNowPlaceholder               = "{{NOW_RFC3339}}"
)

type bootstrapGitFacts = repo.WorktreeGitBaseline

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

	kernelContent, err := renderWorkerKernelBootstrap(p.Layout, latest, w, gitFacts, now)
	if err != nil {
		return repo.ContractPaths{}, err
	}
	stateContent, err := renderWorkerStateBootstrap(p.Layout, latest, w, gitFacts, now)
	if err != nil {
		return repo.ContractPaths{}, err
	}

	if err := ensureBootstrapFile(paths.AgentKernelMD, kernelContent, 0o644, false); err != nil {
		return repo.ContractPaths{}, err
	}
	if err := ensureBootstrapFile(paths.StateJSON, stateContent, 0o644, true); err != nil {
		return repo.ContractPaths{}, err
	}
	return paths, nil
}

func renderWorkerKernelBootstrap(layout repo.Layout, t contracts.Ticket, w contracts.Worker, facts bootstrapGitFacts, now time.Time) (string, error) {
	raw, err := repo.ReadControlWorkerKernelTemplate(layout)
	if err != nil {
		return "", err
	}
	rendered := strings.NewReplacer(
		workerKernelTitlePlaceholder, bootstrapTicketTitle(t),
		workerKernelDescPlaceholder, bootstrapTicketDescription(t),
		workerKernelAttachmentsPlaceholder, bootstrapAttachments(t, w),
	).Replace(raw)

	rendered, err = replaceTaggedSection(rendered, "current_state", bootstrapCurrentState(t, w, facts, now))
	if err != nil {
		return "", err
	}
	return rendered, nil
}

func renderWorkerStateBootstrap(layout repo.Layout, t contracts.Ticket, w contracts.Worker, facts bootstrapGitFacts, now time.Time) (string, error) {
	raw, err := repo.ReadControlWorkerStateTemplate(layout)
	if err != nil {
		return "", err
	}
	rendered := strings.NewReplacer(
		workerStateTicketIDPlaceholder, fmt.Sprintf("%d", t.ID),
		workerStateWorkerIDPlaceholder, fmt.Sprintf("%d", w.ID),
		workerStateSummaryPlaceholder, "bootstrap 已生成，等待 worker 读取上下文、代码和文档后推进实现",
		workerStateHeadSHAPlaceholder, bootstrapFactOrUnknown(facts.HeadSHA),
		workerStateWorkingTreeStatusPlaceholder, bootstrapWorkingTreeStatus(facts.WorkingTreeStatus),
		workerStateLastCommitSubjectPlaceholder, bootstrapFactOrUnknown(facts.LastCommitSubject),
		workerStateNowPlaceholder, now.Format(time.RFC3339),
	).Replace(raw)
	if !json.Valid([]byte(rendered)) {
		return "", fmt.Errorf("渲染 state.json 失败: 结果不是合法 JSON")
	}
	if !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	return rendered, nil
}

func buildWorkerEntrypointPrompt(entryPrompt string) string {
	supplemental := strings.TrimSpace(entryPrompt)
	if supplemental == "" {
		supplemental = defaultContinuePrompt
	}
	const prompt = "你正在当前 ticket 的 worker worktree 中继续交付。\n" +
		"先读取并遵循 `.dalek/agent-kernel.md`，再按其中 context_loading 顺序读取 `.dalek/state.json`。\n" +
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

5. 下一步：按 context_loading 顺序读取 state.json 和 git 基线，然后开始探索需求与实现路径。

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
