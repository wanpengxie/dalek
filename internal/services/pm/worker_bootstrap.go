package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
)

const (
	workerKernelTitlePlaceholder = "{{TICKET_TITLE}}"
	workerKernelDescPlaceholder  = "{{TICKET_DESCRIPTION}}"
	// 共享占位符：kernel 和 state.json 模板复用同一套
	placeholderTicketID          = "{{DALEK_TICKET_ID}}"
	placeholderWorkerID          = "{{DALEK_WORKER_ID}}"
	placeholderHeadSHA           = "{{HEAD_SHA}}"
	placeholderWorkingTreeStatus = "{{WORKING_TREE_STATUS}}"
	placeholderLastCommitSubject = "{{LAST_COMMIT_SUBJECT}}"
	placeholderNow               = "{{NOW_RFC3339}}"
	placeholderTargetRef         = "{{TARGET_REF}}"
	placeholderWorktreePath      = "{{WORKTREE_PATH}}"
	placeholderWorkerBranch      = "{{WORKER_BRANCH}}"
)

type bootstrapGitFacts = repo.WorktreeGitBaseline

type workerBootstrapMode string

const (
	workerBootstrapModeFirstBootstrap workerBootstrapMode = "first_bootstrap"
	workerBootstrapModeRecoveryRepair workerBootstrapMode = "recovery_repair"
)

type bootstrapFileWriteOptions struct {
	JSONStrict     bool
	ForceOverwrite bool
}

func (m workerBootstrapMode) forceOverwrite() bool {
	return m == workerBootstrapModeFirstBootstrap
}

func (s *Service) ensureWorkerBootstrap(ctx context.Context, t contracts.Ticket, w contracts.Worker, entryPrompt string, mode workerBootstrapMode) (repo.ContractPaths, error) {
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

	// 复制 control/ 和 pm/ 到 worktree，使 worker 可访问控制策略和 PM 上下文
	if err := copyControlAndPMToWorktree(p.Layout, paths.Dir); err != nil {
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

	forceOverwrite := mode.forceOverwrite()
	if err := ensureBootstrapFile(paths.AgentKernelMD, kernelContent, 0o644, bootstrapFileWriteOptions{
		ForceOverwrite: forceOverwrite,
	}); err != nil {
		return repo.ContractPaths{}, err
	}
	if err := ensureBootstrapFile(paths.StateJSON, stateContent, 0o644, bootstrapFileWriteOptions{
		JSONStrict:     true,
		ForceOverwrite: forceOverwrite,
	}); err != nil {
		return repo.ContractPaths{}, err
	}
	return paths, nil
}

func (s *Service) determineWorkerBootstrapMode(ctx context.Context, workerID uint) (workerBootstrapMode, error) {
	_, db, err := s.require()
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return "", fmt.Errorf("worker_id 不能为空")
	}

	var existing int64
	if err := db.WithContext(ctx).
		Model(&contracts.TaskRun{}).
		Where("owner_type = ? AND task_type = ? AND worker_id = ?", contracts.TaskOwnerWorker, contracts.TaskTypeDeliverTicket, workerID).
		Count(&existing).Error; err != nil {
		return "", fmt.Errorf("查询 worker 历史 deliver_ticket runs 失败（w%d）: %w", workerID, err)
	}
	if existing == 0 {
		return workerBootstrapModeFirstBootstrap, nil
	}
	return workerBootstrapModeRecoveryRepair, nil
}

func renderWorkerKernelBootstrap(layout repo.Layout, t contracts.Ticket, w contracts.Worker, facts bootstrapGitFacts, now time.Time) (string, error) {
	raw, err := repo.ReadControlWorkerKernelTemplate(layout)
	if err != nil {
		return "", err
	}
	rendered := strings.NewReplacer(
		// task_context 占位符（title / description 整块替换）
		workerKernelTitlePlaceholder, bootstrapTicketTitle(t),
		workerKernelDescPlaceholder, bootstrapTicketDescription(t),
		// 共享动态占位符
		placeholderTicketID, fmt.Sprintf("%d", t.ID),
		placeholderWorkerID, fmt.Sprintf("%d", w.ID),
		placeholderHeadSHA, bootstrapFactOrUnknown(facts.HeadSHA),
		placeholderWorkingTreeStatus, bootstrapWorkingTreeStatus(facts.WorkingTreeStatus),
		placeholderLastCommitSubject, bootstrapFactOrUnknown(facts.LastCommitSubject),
		placeholderTargetRef, bootstrapTargetRef(t),
		placeholderWorktreePath, bootstrapFactOrUnknown(strings.TrimSpace(w.WorktreePath)),
		placeholderWorkerBranch, bootstrapFactOrUnknown(strings.TrimSpace(w.Branch)),
		placeholderNow, now.Format(time.RFC3339),
	).Replace(raw)
	return rendered, nil
}

func renderWorkerStateBootstrap(layout repo.Layout, t contracts.Ticket, w contracts.Worker, facts bootstrapGitFacts, now time.Time) (string, error) {
	raw, err := repo.ReadControlWorkerStateTemplate(layout)
	if err != nil {
		return "", err
	}
	rendered := strings.NewReplacer(
		placeholderTicketID, fmt.Sprintf("%d", t.ID),
		placeholderWorkerID, fmt.Sprintf("%d", w.ID),
		placeholderHeadSHA, bootstrapFactOrUnknown(facts.HeadSHA),
		placeholderWorkingTreeStatus, bootstrapWorkingTreeStatus(facts.WorkingTreeStatus),
		placeholderLastCommitSubject, bootstrapFactOrUnknown(facts.LastCommitSubject),
		placeholderNow, now.Format(time.RFC3339),
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
		"如果 `<discovery>` 区块为空，必须先完成 `<initialization>` SOP 中的深度探索，将结论写入 `<discovery>` 后才能开始实现。\n" +
		"以本地代码、git 检查点和 worktree 事实为真相源推进实现，并在本轮结束前执行 dalek worker report --next <continue|done|wait_user> --summary \"...\"。\n\n" +
		"本轮补充指令：%s"
	return strings.TrimSpace(fmt.Sprintf(prompt, supplemental))
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

func ensureBootstrapFile(path, content string, mode os.FileMode, opt bootstrapFileWriteOptions) error {
	if !opt.ForceOverwrite {
		existing, err := os.ReadFile(path)
		if err == nil && !bootstrapFileNeedsRefresh(existing, opt.JSONStrict) {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("读取 bootstrap 文件失败(%s): %w", path, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("写入 bootstrap 文件失败(%s): %w", path, err)
	}
	return nil
}

// copyControlAndPMToWorktree 将主仓库的 control/ 和 pm/ 递归复制到 worktree 的 .dalek/ 下。
// 源目录不存在时静默跳过，每次 bootstrap 都刷新覆盖。
func copyControlAndPMToWorktree(layout repo.Layout, worktreeDalekDir string) error {
	dirs := []struct {
		src string
		dst string
	}{
		{src: layout.ControlDir, dst: filepath.Join(worktreeDalekDir, "control")},
		{src: layout.PMDir, dst: filepath.Join(worktreeDalekDir, "pm")},
	}
	for _, d := range dirs {
		if err := repo.CopyDirRecursive(d.src, d.dst); err != nil {
			return fmt.Errorf("复制 %s 到 worktree 失败: %w", d.src, err)
		}
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
