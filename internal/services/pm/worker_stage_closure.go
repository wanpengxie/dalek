package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/repo"

	"gorm.io/gorm"
)

type workerLoopClosureExhaustedError struct {
	Stage     int
	LastRunID uint
	Decision  workerLoopStageClosureDecision
}

func (e *workerLoopClosureExhaustedError) Error() string {
	if e == nil {
		return "worker stage closure repair exhausted"
	}
	code := strings.TrimSpace(e.Decision.ReasonCode)
	if code == "" {
		code = "unknown"
	}
	if e.LastRunID != 0 {
		return fmt.Sprintf("worker stage closure repair exhausted(stage=%d run_id=%d reason=%s)", e.Stage, e.LastRunID, code)
	}
	return fmt.Sprintf("worker stage closure repair exhausted(stage=%d reason=%s)", e.Stage, code)
}

type workerLoopStageClosureDecision struct {
	NextAction  string
	ReasonCode  string
	Issues      []string
	Accepted    bool
	Repairable  bool
	ReportFound bool
	Report      contracts.WorkerReport
	State       workerLoopStateSnapshot
	Git         workerLoopGitSnapshot
	RawRunError string
}

type workerLoopGitSnapshot struct {
	HeadSHA   string
	HeadKnown bool

	Dirty      bool
	DirtyKnown bool
}

type workerLoopStateSnapshot struct {
	Path                    string
	Exists                  bool
	Valid                   bool
	ParseError              string
	HasTemplatePlaceholders bool

	NextAction    string
	Summary       string
	Blockers      []string
	CurrentStatus string
	PhaseStatuses []string
	AllPhasesDone bool

	CodeHeadSHA       string
	CodeWorkingTree   string
	LastCommitSubject string
}

type workerLoopStateFile struct {
	Phases struct {
		CurrentStatus string `json:"current_status"`
		NextAction    string `json:"next_action"`
		Summary       string `json:"summary"`
		Items         []struct {
			Status string `json:"status"`
		} `json:"items"`
	} `json:"phases"`
	Blockers []string `json:"blockers"`
	Code     struct {
		HeadSHA           string `json:"head_sha"`
		WorkingTree       string `json:"working_tree"`
		LastCommitSubject string `json:"last_commit_subject"`
	} `json:"code"`
}

func (s *Service) evaluateWorkerLoopStageClosure(ctx context.Context, ticketID uint, w contracts.Worker, runID uint, rawRunErr error) (workerLoopStageClosureDecision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	decision := workerLoopStageClosureDecision{
		Repairable: true,
		State:      readWorkerLoopStateSnapshot(strings.TrimSpace(w.WorktreePath)),
		Git:        s.inspectWorkerLoopGitSnapshot(ctx, strings.TrimSpace(w.WorktreePath)),
	}
	if rawRunErr != nil {
		decision.RawRunError = strings.TrimSpace(rawRunErr.Error())
	}
	report, found, err := s.loadWorkerLoopCandidateReport(ctx, ticketID, w, runID)
	if err != nil {
		return workerLoopStageClosureDecision{}, err
	}
	decision.ReportFound = found
	if found {
		decision.Report = report
		decision.NextAction = normalizeWorkerLoopNextAction(report.NextAction)
	}
	return s.evaluateWorkerLoopClosureCandidate(ctx, ticketID, w, runID, decision)
}

func (s *Service) guardWorkerLoopTerminalReport(ctx context.Context, r contracts.WorkerReport) (contracts.WorkerReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.Normalize()
	next := normalizeWorkerLoopNextAction(r.NextAction)
	if next != string(contracts.NextDone) && next != string(contracts.NextWaitUser) {
		return r, nil
	}
	if r.TaskRunID == 0 {
		return contracts.WorkerReport{}, fmt.Errorf("worker loop closure 缺少 task_run_id")
	}
	w, err := s.worker.WorkerByID(ctx, r.WorkerID)
	if err != nil {
		return contracts.WorkerReport{}, fmt.Errorf("读取 worker 失败: %w", err)
	}
	if w == nil {
		return contracts.WorkerReport{}, fmt.Errorf("worker 不存在: w%d", r.WorkerID)
	}
	ticketID := r.TicketID
	if ticketID == 0 {
		ticketID = w.TicketID
		r.TicketID = ticketID
	}
	decision, err := s.evaluateWorkerLoopClosureCandidate(ctx, ticketID, *w, r.TaskRunID, workerLoopStageClosureDecision{
		NextAction:  next,
		ReasonCode:  "direct_guard",
		Repairable:  true,
		ReportFound: true,
		Report:      r,
		State:       readWorkerLoopStateSnapshot(strings.TrimSpace(w.WorktreePath)),
		Git:         s.inspectWorkerLoopGitSnapshot(ctx, strings.TrimSpace(w.WorktreePath)),
	})
	if err != nil {
		return contracts.WorkerReport{}, err
	}
	if !decision.Accepted {
		return contracts.WorkerReport{}, fmt.Errorf("worker loop closure 校验失败: %s", strings.Join(decision.Issues, "；"))
	}
	return decision.Report, nil
}

func (s *Service) loadWorkerLoopCandidateReport(ctx context.Context, ticketID uint, w contracts.Worker, runID uint) (contracts.WorkerReport, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return contracts.WorkerReport{}, false, nil
	}
	_, db, err := s.require()
	if err != nil {
		return contracts.WorkerReport{}, false, err
	}
	var row contracts.TaskSemanticReport
	if err := db.WithContext(ctx).
		Where("task_run_id = ? AND milestone = ?", runID, "agent_report").
		Order("reported_at desc").
		Order("id desc").
		First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return contracts.WorkerReport{}, false, nil
		}
		return contracts.WorkerReport{}, false, fmt.Errorf("读取 worker loop terminal report 失败: %w", err)
	}
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   ticketID,
		TaskRunID:  runID,
		Summary:    strings.TrimSpace(row.Summary),
		NextAction: strings.TrimSpace(row.NextAction),
		HeadSHA:    workerClosureJSONMapString(row.ReportPayloadJSON, "head_sha"),
		Dirty:      workerClosureJSONMapBool(row.ReportPayloadJSON, "dirty"),
		NeedsUser:  workerClosureJSONMapBool(row.ReportPayloadJSON, "needs_user"),
		Blockers:   workerClosureJSONMapStringSlice(row.ReportPayloadJSON, "blockers"),
	}
	report.Normalize()
	return report, true, nil
}

func (s *Service) evaluateWorkerLoopClosureCandidate(ctx context.Context, ticketID uint, w contracts.Worker, runID uint, decision workerLoopStageClosureDecision) (workerLoopStageClosureDecision, error) {
	report := decision.Report
	report.Normalize()
	if report.TicketID == 0 {
		report.TicketID = ticketID
	}
	if report.TaskRunID == 0 {
		report.TaskRunID = runID
	}
	decision.Report = report
	decision.NextAction = normalizeWorkerLoopNextAction(report.NextAction)
	if !decision.ReportFound {
		decision.ReasonCode = "missing_report"
		decision.Issues = append(decision.Issues, "当前 raw run 未找到 worker report，无法闭合当前 stage。")
		return decision, nil
	}
	if err := report.Validate(); err != nil {
		decision.ReasonCode = "invalid_report"
		decision.Issues = append(decision.Issues, "worker report 非法: "+strings.TrimSpace(err.Error()))
		return decision, nil
	}
	bindingIssues, err := s.validateWorkerLoopTaskRunBinding(ctx, ticketID, w.ID, runID, report.TaskRunID)
	if err != nil {
		return workerLoopStageClosureDecision{}, err
	}
	if len(bindingIssues) > 0 {
		decision.ReasonCode = "invalid_report"
		decision.Issues = append(decision.Issues, bindingIssues...)
	}
	if report.TaskRunID != runID {
		decision.ReasonCode = "invalid_report"
		decision.Issues = append(decision.Issues, fmt.Sprintf("worker report 绑定的 task_run_id=%d，与当前 run_id=%d 不一致。", report.TaskRunID, runID))
	}
	if w.ID != 0 && report.WorkerID != 0 && report.WorkerID != w.ID {
		decision.ReasonCode = "invalid_report"
		decision.Issues = append(decision.Issues, fmt.Sprintf("worker report 绑定的 worker_id=%d，与当前 worker_id=%d 不一致。", report.WorkerID, w.ID))
	}
	if ticketID != 0 && report.TicketID != 0 && report.TicketID != ticketID {
		decision.ReasonCode = "invalid_report"
		decision.Issues = append(decision.Issues, fmt.Sprintf("worker report 绑定的 ticket_id=%d，与当前 ticket_id=%d 不一致。", report.TicketID, ticketID))
	}
	if decision.NextAction == "" {
		decision.ReasonCode = "missing_report"
		decision.Issues = append(decision.Issues, "worker report 缺少 next_action，当前 stage 不能直接收口。")
		return decision, nil
	}
	if decision.NextAction == string(contracts.NextContinue) {
		if strings.TrimSpace(decision.RawRunError) != "" {
			decision.ReasonCode = "abnormal_exit"
			decision.Issues = append(decision.Issues, "raw run 异常退出，不能直接按 continue 推进下一轮。")
			return decision, nil
		}
		if len(decision.Issues) == 0 {
			decision.Accepted = true
		}
		return decision, nil
	}

	if !decision.State.Exists {
		if decision.ReasonCode == "" {
			decision.ReasonCode = "invalid_state"
		}
		decision.Issues = append(decision.Issues, "缺少 `.dalek/state.json`，无法完成 closure check。")
	}
	if !decision.State.Valid {
		if decision.ReasonCode == "" {
			decision.ReasonCode = "invalid_state"
		}
		msg := "`.dalek/state.json` 非法"
		if strings.TrimSpace(decision.State.ParseError) != "" {
			msg += ": " + strings.TrimSpace(decision.State.ParseError)
		}
		decision.Issues = append(decision.Issues, msg)
	}
	if decision.State.HasTemplatePlaceholders {
		if decision.ReasonCode == "" {
			decision.ReasonCode = "invalid_state"
		}
		decision.Issues = append(decision.Issues, "`.dalek/state.json` 仍包含模板占位符，说明 worker 初始化或状态同步未完成。")
	}

	switch decision.NextAction {
	case string(contracts.NextDone):
		decision = evaluateDoneClosureCandidate(decision)
	case string(contracts.NextWaitUser):
		decision = evaluateWaitUserClosureCandidate(decision)
	}
	if len(decision.Issues) == 0 {
		decision.Accepted = true
		if decision.ReasonCode == "" {
			decision.ReasonCode = decision.NextAction
		}
	}
	return decision, nil
}

func evaluateDoneClosureCandidate(decision workerLoopStageClosureDecision) workerLoopStageClosureDecision {
	reportBlockers := cleanStringSlice(decision.Report.Blockers)
	stateBlockers := cleanStringSlice(decision.State.Blockers)
	candidateAnchor := firstValidWorkerLoopHead(decision.Report.HeadSHA, decision.State.CodeHeadSHA)
	if candidateAnchor == "" && decision.Git.HeadKnown && !decision.Git.Dirty {
		candidateAnchor = strings.TrimSpace(decision.Git.HeadSHA)
	}
	if !meaningfulWorkerSummary(decision.Report.Summary) {
		decision.Issues = append(decision.Issues, "done closure 缺少非空 summary。")
	}
	if !decision.State.AllPhasesDone {
		decision.Issues = append(decision.Issues, "`.dalek/state.json` 中 phases.items[*].status 未全部为 done。")
	}
	if len(reportBlockers) > 0 || len(stateBlockers) > 0 {
		decision.Issues = append(decision.Issues, "done closure 要求 blockers 为空，但 report 或 state.json 仍存在 blockers。")
	}
	if decision.Report.Dirty {
		decision.Issues = append(decision.Issues, "worker report 标记 worktree dirty，不能直接收口为 done。")
	}
	if !decision.Git.DirtyKnown {
		decision.Issues = append(decision.Issues, "无法确认当前 worktree 是否 clean，不能保守收口为 done。")
	} else if decision.Git.Dirty {
		decision.Issues = append(decision.Issues, "当前 worktree 仍然 dirty，不能收口为 done。")
	}
	if stateWT := normalizeWorkerLoopWorkingTreeState(decision.State.CodeWorkingTree); stateWT == "dirty" {
		decision.Issues = append(decision.Issues, "`.dalek/state.json` 记录的 code.working_tree=dirty，不能收口为 done。")
	} else if stateWT != "" && decision.Git.DirtyKnown {
		actual := "clean"
		if decision.Git.Dirty {
			actual = "dirty"
		}
		if stateWT != actual {
			decision.Issues = append(decision.Issues, fmt.Sprintf("state.json code.working_tree=%s 与实际 git 状态=%s 不一致。", stateWT, actual))
		}
	}
	if decision.Git.HeadKnown && looksLikeGitCommit(decision.Report.HeadSHA) && decision.Report.HeadSHA != decision.Git.HeadSHA {
		decision.Issues = append(decision.Issues, fmt.Sprintf("worker report head_sha=%s 与实际 HEAD=%s 不一致。", decision.Report.HeadSHA, decision.Git.HeadSHA))
	}
	if stateHead := strings.TrimSpace(decision.State.CodeHeadSHA); stateHead != "" && looksLikeGitCommit(stateHead) && decision.Git.HeadKnown && stateHead != decision.Git.HeadSHA {
		decision.Issues = append(decision.Issues, fmt.Sprintf("state.json code.head_sha=%s 与实际 HEAD=%s 不一致。", stateHead, decision.Git.HeadSHA))
	}
	if !looksLikeGitCommit(candidateAnchor) {
		decision.Issues = append(decision.Issues, "done closure 缺少可冻结的 clean git anchor。")
	} else {
		decision.Report.HeadSHA = candidateAnchor
		decision.Report.Dirty = false
	}
	if decision.ReasonCode == "" && len(decision.Issues) > 0 {
		decision.ReasonCode = "done_not_landable"
	}
	return decision
}

func evaluateWaitUserClosureCandidate(decision workerLoopStageClosureDecision) workerLoopStageClosureDecision {
	if !meaningfulWorkerSummary(decision.Report.Summary) {
		decision.Issues = append(decision.Issues, "wait_user closure 缺少能解释阻塞原因的 summary。")
	}
	if len(cleanStringSlice(decision.Report.Blockers)) == 0 {
		decision.Issues = append(decision.Issues, "wait_user closure 缺少 blockers，无法说明为什么需要人工介入。")
	}
	if decision.ReasonCode == "" && len(decision.Issues) > 0 {
		decision.ReasonCode = "wait_user_incomplete"
	}
	return decision
}

func (d workerLoopStageClosureDecision) fallbackSummary() string {
	switch d.NextAction {
	case string(contracts.NextDone):
		return "worker 声称任务已完成，但 closure 校验未通过，系统已自动阻塞并请求人工介入。"
	case string(contracts.NextWaitUser):
		return "worker 请求人工介入，但 closure 材料不完整，系统已自动阻塞并请求人工介入。"
	default:
		if d.ReportFound {
			return "worker 本轮执行已结束，但 closure 校验未通过，系统已自动阻塞并请求人工介入。"
		}
		return "worker 本轮执行已结束，但未提交足以收口的 worker report，系统已自动阻塞并请求人工介入。"
	}
}

func (d workerLoopStageClosureDecision) fallbackBlockers() []string {
	out := make([]string, 0, len(d.Issues)+6)
	for _, issue := range d.Issues {
		issue = strings.TrimSpace(issue)
		if issue == "" {
			continue
		}
		out = append(out, issue)
	}
	if strings.TrimSpace(d.RawRunError) != "" {
		out = append(out, "raw run error: "+strings.TrimSpace(d.RawRunError))
	}
	if d.ReportFound {
		if d.Report.TaskRunID != 0 {
			out = append(out, fmt.Sprintf("closure 所属 task_run_id=%d。", d.Report.TaskRunID))
		}
		if summary := strings.TrimSpace(d.Report.Summary); summary != "" {
			out = append(out, fmt.Sprintf("worker report summary=%q。", summary))
		}
	}
	if summary := strings.TrimSpace(d.State.Summary); summary != "" {
		out = append(out, fmt.Sprintf("state.json summary=%q。", summary))
	}
	for _, blocker := range cleanStringSlice(d.State.Blockers) {
		out = append(out, "state.json blocker: "+blocker)
	}
	return cleanStringSlice(out)
}

func buildWorkerLoopClosureRepairPrompt(decision workerLoopStageClosureDecision) string {
	lines := []string{closureRepairPrompt, "", "当前未闭合原因："}
	for _, issue := range cleanStringSlice(decision.Issues) {
		lines = append(lines, "- "+issue)
	}
	if strings.TrimSpace(decision.RawRunError) != "" {
		lines = append(lines, "- raw run error: "+strings.TrimSpace(decision.RawRunError))
	}
	lines = append(lines, "", "本轮要求：")
	lines = append(lines, "- 优先补齐收口材料，不要扩展任务范围。")
	lines = append(lines, "- 更新 `.dalek/state.json`，确保 next_action / summary / blockers 与最终 report 一致。")
	lines = append(lines, "- 完成后再次执行 `dalek worker report`。")
	return strings.Join(lines, "\n")
}

func (s *Service) inspectWorkerLoopGitSnapshot(ctx context.Context, worktreePath string) workerLoopGitSnapshot {
	snapshot := workerLoopGitSnapshot{}
	if ctx == nil {
		ctx = context.Background()
	}
	worktreePath = strings.TrimSpace(worktreePath)
	if worktreePath == "" {
		return snapshot
	}
	p, _, err := s.require()
	if err != nil {
		return snapshot
	}
	facts := repo.InspectWorktreeGitBaseline(ctx, worktreePath, p.Git)
	head := strings.TrimSpace(facts.HeadSHA)
	if looksLikeGitCommit(head) {
		snapshot.HeadSHA = head
		snapshot.HeadKnown = true
	}
	switch strings.TrimSpace(strings.ToLower(facts.WorkingTreeStatus)) {
	case "clean":
		snapshot.DirtyKnown = true
		snapshot.Dirty = false
	case "dirty":
		snapshot.DirtyKnown = true
		snapshot.Dirty = true
	}
	return snapshot
}

func readWorkerLoopStateSnapshot(worktreePath string) workerLoopStateSnapshot {
	worktreePath = strings.TrimSpace(worktreePath)
	statePath := ""
	if worktreePath != "" {
		statePath = filepath.Join(worktreePath, ".dalek", "state.json")
	}
	snapshot := workerLoopStateSnapshot{
		Path: statePath,
	}
	if statePath == "" {
		return snapshot
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return snapshot
	}
	snapshot.Exists = true
	trimmed := strings.TrimSpace(string(raw))
	if strings.Contains(trimmed, "{{") && strings.Contains(trimmed, "}}") {
		snapshot.HasTemplatePlaceholders = true
	}
	var state workerLoopStateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		snapshot.ParseError = err.Error()
		return snapshot
	}
	snapshot.Valid = true
	snapshot.NextAction = strings.TrimSpace(state.Phases.NextAction)
	snapshot.Summary = strings.TrimSpace(state.Phases.Summary)
	snapshot.Blockers = cleanStringSlice(state.Blockers)
	snapshot.CurrentStatus = strings.TrimSpace(state.Phases.CurrentStatus)
	snapshot.CodeHeadSHA = strings.TrimSpace(state.Code.HeadSHA)
	snapshot.CodeWorkingTree = strings.TrimSpace(state.Code.WorkingTree)
	snapshot.LastCommitSubject = strings.TrimSpace(state.Code.LastCommitSubject)
	snapshot.AllPhasesDone = true
	if len(state.Phases.Items) > 0 {
		phaseStatuses := make([]string, 0, len(state.Phases.Items))
		allDone := true
		for _, item := range state.Phases.Items {
			status := strings.TrimSpace(strings.ToLower(item.Status))
			if status == "" {
				allDone = false
			}
			phaseStatuses = append(phaseStatuses, status)
			if status != "done" {
				allDone = false
			}
		}
		snapshot.PhaseStatuses = phaseStatuses
		snapshot.AllPhasesDone = allDone
	}
	return snapshot
}

func normalizeWorkerLoopNextAction(next string) string {
	return strings.TrimSpace(strings.ToLower(next))
}

func normalizeWorkerLoopWorkingTreeState(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "clean":
		return "clean"
	case "dirty":
		return "dirty"
	default:
		return ""
	}
}

func meaningfulWorkerSummary(summary string) bool {
	summary = strings.TrimSpace(summary)
	return summary != "" && summary != "-"
}

func (s *Service) validateWorkerLoopTaskRunBinding(ctx context.Context, ticketID, workerID, currentRunID, reportRunID uint) ([]string, error) {
	if reportRunID == 0 {
		return []string{"worker report 缺少 task_run_id。"}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	var run contracts.TaskRun
	if err := db.WithContext(ctx).
		Select("id", "ticket_id", "worker_id", "owner_type").
		First(&run, reportRunID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return []string{fmt.Sprintf("worker report 绑定的 task_run_id=%d 不存在。", reportRunID)}, nil
		}
		return nil, err
	}
	issues := make([]string, 0, 4)
	if currentRunID != 0 && run.ID != currentRunID {
		issues = append(issues, fmt.Sprintf("worker report 绑定的 task_run_id=%d，与当前 run_id=%d 不一致。", run.ID, currentRunID))
	}
	if run.OwnerType != contracts.TaskOwnerWorker {
		issues = append(issues, fmt.Sprintf("task_run_id=%d 不是 worker run。", run.ID))
	}
	if ticketID != 0 && run.TicketID != ticketID {
		issues = append(issues, fmt.Sprintf("task_run_id=%d 绑定的 ticket_id=%d，与当前 ticket_id=%d 不一致。", run.ID, run.TicketID, ticketID))
	}
	if workerID != 0 && run.WorkerID != workerID {
		issues = append(issues, fmt.Sprintf("task_run_id=%d 绑定的 worker_id=%d，与当前 worker_id=%d 不一致。", run.ID, run.WorkerID, workerID))
	}
	return issues, nil
}

func firstValidWorkerLoopHead(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if looksLikeGitCommit(value) {
			return value
		}
	}
	return ""
}
