package tui

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"dalek/internal/app"
	"dalek/internal/contracts"
)

func kvLine(key, value string, width int) string {
	k := lipgloss.NewStyle().Foreground(cMuted).Render(key)
	v := lipgloss.NewStyle().Foreground(cText).Render(value)
	return cutANSI(k+" "+v, width)
}

func (m model) dispatchProcessState(v app.TicketView) string {
	if m.dispatchTicketID != 0 && m.dispatchTicketID == v.Ticket.ID {
		return "dispatch请求中"
	}
	if v.TaskRunID == 0 {
		return "未派发"
	}
	switch formatExecutionState(v) {
	case "等待输入":
		return "等待用户输入"
	case "运行中":
		return "worker执行中"
	case "空闲":
		return "worker空闲待命"
	case "异常":
		return "worker异常"
	case "已停止":
		return "worker已停止"
	case "启动中":
		return "worker启动中"
	case "待观测":
		return "worker待观测(" + formatSessionState(v) + ")"
	default:
		return "状态待观测"
	}
}

func timeAndAge(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return fmt.Sprintf("%s (%s前)", t.Local().Format("15:04:05"), shortDuration(time.Since(*t)))
}

func partitionColor(section string) lipgloss.TerminalColor {
	switch strings.TrimSpace(section) {
	case "manager":
		return cInfo
	case "running":
		return cOk
	case "wait":
		return cWarn
	case "merge":
		return cTitle
	case "backlog":
		return cNeutral
	case "done":
		return cOk
	case "archive":
		return cFaint
	default:
		return cMuted
	}
}

func partitionCell(section string) string {
	label := strings.TrimSpace(section)
	if label == "" {
		label = "-"
	}
	return label
}

func ticketStatusBadge(s contracts.TicketWorkflowStatus) string {
	switch s {
	case contracts.TicketDone:
		return badge("完成", cOk)
	case contracts.TicketBlocked:
		return badge("阻塞", cDanger)
	case contracts.TicketQueued:
		return badge("排队", cWarn)
	case contracts.TicketActive:
		return badge("进行中", cInfo)
	case contracts.TicketArchived:
		return badge("归档", cFaint)
	default:
		return badge("待办", cNeutral)
	}
}

func runtimeStatusBadge(v app.TicketView) string {
	switch formatExecutionState(v) {
	case "等待输入":
		return badge("等待输入", cWarn)
	case "运行中":
		return badge("运行中", cOk)
	case "空闲":
		return badge("空闲", cNeutral)
	case "异常":
		return badge("错误", cDanger)
	case "已停止":
		return badge("已停止", cNeutral)
	case "启动中":
		return badge("启动中", cInfo)
	case "待观测":
		if v.SessionAlive {
			return badge("待观测", cInfo)
		}
		return badge("待观测", cNeutral)
	case "未启动":
		return badge("未启动", cNeutral)
	default:
		return badge("未知", cNeutral)
	}
}

func tailTail(lines []string, n int) []string {
	if n <= 0 {
		return []string{}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) < n {
		pad := make([]string, n-len(lines))
		lines = append(pad, lines...)
	}
	return lines
}

func padBottom(lines []string, n int) []string {
	for len(lines) < n {
		lines = append(lines, "")
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines
}

func trimLeft(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(rs[len(rs)-maxLen:])
	}
	return "..." + string(rs[len(rs)-(maxLen-3):])
}

func (m model) selectedRow() rowRef {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.rowRefs) {
		return rowRef{kind: rowNone}
	}
	return m.rowRefs[i]
}

func (m model) selectedTicketID() uint {
	ref := m.selectedRow()
	if ref.kind == rowTicket {
		return ref.ticketID
	}
	return 0
}

type ticketAction string

const (
	ticketActionStart     ticketAction = "start"
	ticketActionDispatch  ticketAction = "dispatch"
	ticketActionWorkerRun ticketAction = "worker_run"
	ticketActionInterrupt ticketAction = "interrupt"
	ticketActionStop      ticketAction = "stop"
	ticketActionAttach    ticketAction = "attach"
	ticketActionArchive   ticketAction = "archive"
	ticketActionEdit      ticketAction = "edit"
	ticketActionEvents    ticketAction = "events"
	ticketActionPriority  ticketAction = "priority"
	ticketActionStatus    ticketAction = "status"
)

func actionLabel(action ticketAction) string {
	switch action {
	case ticketActionStart:
		return "启动(s)"
	case ticketActionDispatch:
		return "派发(p)"
	case ticketActionWorkerRun:
		return "重新跑(r)"
	case ticketActionInterrupt:
		return "中断(i)"
	case ticketActionStop:
		return "停止(k)"
	case ticketActionAttach:
		return "日志(a)"
	case ticketActionArchive:
		return "归档(d)"
	case ticketActionEdit:
		return "编辑(e)"
	case ticketActionEvents:
		return "事件(v)"
	case ticketActionPriority:
		return "优先级(+/-)"
	case ticketActionStatus:
		return "状态(0-4)"
	default:
		return string(action)
	}
}

func actionDeniedStatus(action ticketAction, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "当前状态不允许"
	}
	return fmt.Sprintf("不支持 %s：%s", actionLabel(action), reason)
}

func (m model) selectedTicketForAction(action ticketAction) (uint, bool, string) {
	ref := m.selectedRow()
	if ref.kind != rowTicket || ref.ticketID == 0 {
		return 0, false, ""
	}
	// 优先使用 capability（权威门禁），避免按分区文本误判锁死操作。
	if v, ok := m.viewsByID[ref.ticketID]; ok {
		cap := v.Capability
		allowed := false
		switch action {
		case ticketActionStart:
			allowed = cap.CanStart
		case ticketActionDispatch:
			allowed = cap.CanDispatch
		case ticketActionWorkerRun:
			allowed = cap.CanDispatch
		case ticketActionInterrupt:
			// interrupt 需要 session 可用，复用 stop 的 capability。
			allowed = cap.CanStop
		case ticketActionStop:
			allowed = cap.CanStop
		case ticketActionAttach:
			allowed = cap.CanAttach
		case ticketActionArchive:
			allowed = cap.CanArchive
		case ticketActionEdit, ticketActionEvents, ticketActionPriority, ticketActionStatus:
			allowed = v.Ticket.WorkflowStatus != contracts.TicketArchived
		default:
			allowed = false
		}
		if allowed {
			return ref.ticketID, true, ""
		}
		return 0, false, actionDeniedStatus(action, cap.Reason)
	}

	// 非活跃视图（例如 archive 行）仅允许只读查看，禁止写操作。
	if t, ok := m.ticketsByID[ref.ticketID]; ok {
		if t.WorkflowStatus == contracts.TicketArchived {
			return 0, false, actionDeniedStatus(action, "已归档")
		}
	}
	return 0, false, actionDeniedStatus(action, "详情尚未加载")
}

func (m model) ticketCount() int {
	n := 0
	for _, r := range m.rowRefs {
		if r.kind == rowTicket {
			n++
		}
	}
	return n
}

func (m model) orderedViews() []app.TicketView {
	out := make([]app.TicketView, 0, len(m.viewsByID))
	for _, r := range m.rowRefs {
		if r.kind != rowTicket || r.ticketID == 0 {
			continue
		}
		v, ok := m.viewsByID[r.ticketID]
		if !ok {
			continue
		}
		out = append(out, v)
	}
	if len(out) != 0 {
		return out
	}
	for _, v := range m.viewsByID {
		out = append(out, v)
	}
	return out
}

type mergeQueueSummary struct {
	Total         int
	Proposed      int
	ChecksRunning int
	Ready         int
	Approved      int
	Blocked       int
}

func summarizeMergeQueue(items []contracts.MergeItem) mergeQueueSummary {
	out := mergeQueueSummary{Total: len(items)}
	for _, it := range items {
		switch it.Status {
		case contracts.MergeProposed:
			out.Proposed++
		case contracts.MergeChecksRunning:
			out.ChecksRunning++
		case contracts.MergeReady:
			out.Ready++
		case contracts.MergeApproved:
			out.Approved++
		case contracts.MergeBlocked:
			out.Blocked++
		}
	}
	return out
}

func (m model) activeMergeItems() []contracts.MergeItem {
	out := make([]contracts.MergeItem, 0, len(m.mergeItems))
	for _, it := range m.mergeItems {
		switch it.Status {
		case contracts.MergeProposed, contracts.MergeChecksRunning, contracts.MergeReady, contracts.MergeApproved, contracts.MergeBlocked:
			out = append(out, it)
		default:
			// 终态（merged/discarded）及未知状态默认不进入活跃队列，避免 UI 残留。
			continue
		}
	}
	return out
}

func (m model) canCaptureTail(ref rowRef) bool {
	switch ref.kind {
	case rowManager:
		return false
	case rowTicket:
		v, ok := m.viewsByID[ref.ticketID]
		if !ok || v.LatestWorker == nil {
			return false
		}
		return strings.TrimSpace(v.LatestWorker.LogPath) != ""
	default:
		return false
	}
}

func (m model) tickCmd() tea.Cmd {
	interval := m.p.RefreshInterval()
	if interval <= 0 {
		interval = time.Second
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) refreshCmd() tea.Cmd {
	// 自动刷新：只更新列表（不跑 watcher）
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		views, err := m.p.ListTicketViews(ctx)
		merges, merr := m.p.ListMergeItems(ctx, app.ListMergeOptions{Limit: 200})
		tickets, terr := m.p.ListTickets(ctx, true)
		return refreshedMsg{
			Views:           views,
			MergeItems:      merges,
			ArchivedTickets: tickets,
			MergeErr:        merr,
			ArchiveErr:      terr,
			Err:             err,
			Manual:          false,
			TicketID:        0,
			StartedAt:       time.Time{},
			FinishedAt:      time.Time{},
		}
	}
}

func (m model) manualRefreshCmd(ticketID uint) tea.Cmd {
	started := time.Now()
	return func() tea.Msg {
		// 手动刷新：直接刷新列表。
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		views, err := m.p.ListTicketViews(ctx)
		merges, merr := m.p.ListMergeItems(ctx, app.ListMergeOptions{Limit: 200})
		tickets, terr := m.p.ListTickets(ctx, true)
		return refreshedMsg{
			Views:           views,
			MergeItems:      merges,
			ArchivedTickets: tickets,
			MergeErr:        merr,
			ArchiveErr:      terr,
			Err:             err,
			Manual:          true,
			TicketID:        ticketID,
			StartedAt:       started,
			FinishedAt:      time.Now(),
		}
	}
}

func (m model) tailCmd(ref rowRef) tea.Cmd {
	started := time.Now()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		var pv contracts.TailPreview
		var err error
		switch ref.kind {
		case rowManager:
			// manager 交互已移除：保留 manager 行，提示使用状态命令观测。
			pv = contracts.TailPreview{
				Source:     "manager",
				CapturedAt: time.Now(),
				Lines: []string{
					"manager session 交互已移除",
					"请使用 manager tick/status 查看调度状态",
				},
			}
		case rowTicket:
			pv, err = m.p.CaptureTicketTail(ctx, ref.ticketID, tailCaptureLines)
		default:
			err = fmt.Errorf("未选择可抓取的行")
		}
		return tailMsg{
			Ref:        ref,
			Preview:    pv,
			Err:        err,
			StartedAt:  started,
			FinishedAt: time.Now(),
		}
	}
}

func (m model) createTicketCmd(title, description, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		t, err := m.p.CreateTicketWithDescriptionAndLabel(ctx, title, description, label)
		id := uint(0)
		if t != nil {
			id = t.ID
		}
		return createdMsg{TicketID: id, Err: err}
	}
}

func (m model) startTicketCmd(id uint) tea.Cmd {
	return func() tea.Msg {
		// start 包含 worktree 创建 + PM bootstrap（可能涉及依赖安装），60s 不够用。
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		_, err := m.p.StartTicket(ctx, id)
		return startedMsg{TicketID: id, Err: err}
	}
}

func (m model) dispatchTicketCmd(id uint) tea.Cmd {
	return func() tea.Msg {
		client, err := app.NewDaemonAPIClientFromHome(m.home)
		if err != nil {
			return dispatchedMsg{TicketID: id, Err: fmt.Errorf("daemon 不在线: %w", err)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		autoStart := true
		r, err := client.SubmitDispatch(ctx, app.DaemonDispatchSubmitRequest{
			Project:   m.projectName,
			TicketID:  id,
			AutoStart: &autoStart,
		})
		if err != nil {
			return dispatchedMsg{TicketID: id, Err: err}
		}
		return dispatchedMsg{TicketID: id, Receipt: r, Err: nil}
	}
}

func (m model) workerRunTicketCmd(id uint) tea.Cmd {
	return func() tea.Msg {
		client, err := app.NewDaemonAPIClientFromHome(m.home)
		if err != nil {
			return workerRunMsg{TicketID: id, Err: fmt.Errorf("daemon 不在线: %w", err)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := client.SubmitWorkerRun(ctx, app.DaemonWorkerRunSubmitRequest{
			Project:  m.projectName,
			TicketID: id,
			Prompt:   "根据当前状态，继续执行任务",
		})
		if err != nil {
			return workerRunMsg{TicketID: id, Err: err}
		}
		return workerRunMsg{TicketID: id, Receipt: r, Err: nil}
	}
}

func (m model) interruptTicketCmd(id uint) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := m.p.InterruptTicket(ctx, id)
		return interruptedMsg{TicketID: id, Result: r, Err: err}
	}
}

func (m model) stopTicketCmd(id uint) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := m.p.StopTicket(ctx, id)
		return stoppedMsg{TicketID: id, Err: err}
	}
}

func (m model) openTicketTmuxCmd(id uint) tea.Cmd {
	return func() tea.Msg {
		worktreePath, err := m.ticketWorktreePath(id)
		if err != nil {
			return attachedMsg{TicketID: id, Err: err}
		}
		session := tmuxSessionForWorktree(worktreePath)
		cmd := exec.Command("tmux", "new-session", "-A", "-s", session, "-c", worktreePath)
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return attachedMsg{TicketID: id, Err: err}
		})()
	}
}

func (m model) ticketWorktreePath(id uint) (string, error) {
	if v, ok := m.viewsByID[id]; ok && v.LatestWorker != nil {
		if path := strings.TrimSpace(v.LatestWorker.WorktreePath); path != "" {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}
	if m.p == nil {
		return "", fmt.Errorf("project 不可用，无法读取 worktree")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w, err := m.p.LatestWorker(ctx, id)
	if err != nil {
		return "", fmt.Errorf("读取 worker 失败: %w", err)
	}
	if w == nil {
		return "", fmt.Errorf("该 ticket 尚未启动 worker，请先按 s 启动")
	}
	path := strings.TrimSpace(w.WorktreePath)
	if path == "" {
		return "", fmt.Errorf("该 ticket 尚无 worktree 路径，请先按 s 启动")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("worktree 目录不存在: %s", path)
		}
		return "", fmt.Errorf("检查 worktree 失败: %w", err)
	}
	return path, nil
}

func (m model) worktreeReadyInView(id uint) bool {
	v, ok := m.viewsByID[id]
	if !ok || v.LatestWorker == nil {
		return false
	}
	path := strings.TrimSpace(v.LatestWorker.WorktreePath)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func tmuxSessionForWorktree(worktreePath string) string {
	path := filepath.Clean(strings.TrimSpace(worktreePath))
	base := sanitizeTmuxSessionToken(filepath.Base(path))
	if base == "" {
		base = "worktree"
	}
	sum := sha1.Sum([]byte(path))
	// 使用路径 hash 保证“同一 worktree 固定 session 名”，并避免重名冲突。
	suffix := hex.EncodeToString(sum[:4])
	return fmt.Sprintf("wt-%s-%s", base, suffix)
}

func sanitizeTmuxSessionToken(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if ok {
			_, _ = b.WriteRune(r)
			prevDash = false
			continue
		}
		if b.Len() == 0 || prevDash {
			continue
		}
		_ = b.WriteByte('-')
		prevDash = true
	}
	return strings.Trim(b.String(), "-")
}

func (m model) archiveTicketCmd(id uint) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := m.p.ArchiveTicket(ctx, id)
		return archivedMsg{TicketID: id, Err: err}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func trimCell(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	s = oneLine(strings.TrimSpace(s))
	if ansi.StringWidth(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return ansi.Cut(s, 0, maxLen)
	}
	return ansi.Cut(s, 0, maxLen-3) + "..."
}

func labelOrDash(label string) string {
	label = oneLine(strings.TrimSpace(label))
	if label == "" {
		return "-"
	}
	return label
}

func defaultTableLayout() tableLayout {
	return tableLayout{
		section:  8,
		id:       6,
		priority: 2,
		label:    8,
		status:   8,
		runtime:  10,
		title:    34,
		output:   18,
	}
}

func tableTotalWidth(layout tableLayout) int {
	const gapCount = 7
	return layout.section + layout.id + layout.priority + layout.label + layout.status + layout.runtime + layout.title + layout.output + gapCount
}

func tableColumns(layout tableLayout) []table.Column {
	return []table.Column{
		{Title: "分区", Width: layout.section},
		{Title: "ID", Width: layout.id},
		{Title: "P", Width: layout.priority},
		{Title: "标签", Width: layout.label},
		{Title: "状态", Width: layout.status},
		{Title: "运行", Width: layout.runtime},
		{Title: "标题", Width: layout.title},
		{Title: "输出", Width: layout.output},
	}
}

func colorizePartitionColumn(view string) string {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		lines[i] = colorizePartitionLine(line)
	}
	return strings.Join(lines, "\n")
}

func colorizePartitionLine(line string) string {
	for _, section := range []string{"manager", "running", "wait", "merge", "backlog", "done", "archive"} {
		idx := strings.Index(line, section)
		if idx < 0 {
			continue
		}
		if strings.TrimSpace(line[:idx]) != "" {
			continue
		}
		next := idx + len(section)
		if next < len(line) {
			r, _ := utf8.DecodeRuneInString(line[next:])
			if r != utf8.RuneError && !unicode.IsSpace(r) {
				continue
			}
		}
		colored := lipgloss.NewStyle().Foreground(partitionColor(section)).Bold(true).Render(section)
		return line[:idx] + colored + line[next:]
	}
	return line
}

func cutANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Cut(s, 0, width)
}

func styleTicketStatusCell(s contracts.TicketWorkflowStatus, label string) string {
	switch s {
	case contracts.TicketDone:
		return lipgloss.NewStyle().Foreground(cOk).Render(label)
	case contracts.TicketBlocked:
		return lipgloss.NewStyle().Foreground(cDanger).Render(label)
	case contracts.TicketQueued:
		return lipgloss.NewStyle().Foreground(cWarn).Render(label)
	case contracts.TicketActive:
		return lipgloss.NewStyle().Foreground(cInfo).Render(label)
	case contracts.TicketArchived:
		return lipgloss.NewStyle().Foreground(cFaint).Render(label)
	default:
		return lipgloss.NewStyle().Foreground(cMuted).Render(label)
	}
}

func styleRuntimeCell(v app.TicketView, label string) string {
	switch formatExecutionState(v) {
	case "等待输入":
		return lipgloss.NewStyle().Foreground(cWarn).Bold(true).Render(label)
	case "运行中":
		return lipgloss.NewStyle().Foreground(cOk).Render(label)
	case "空闲":
		return lipgloss.NewStyle().Foreground(cMuted).Render(label)
	case "异常":
		return lipgloss.NewStyle().Foreground(cDanger).Bold(true).Render(label)
	case "已停止", "未启动":
		return lipgloss.NewStyle().Foreground(cMuted).Render(label)
	case "启动中":
		return lipgloss.NewStyle().Foreground(cInfo).Render(label)
	case "待观测":
		if v.SessionAlive {
			return lipgloss.NewStyle().Foreground(cInfo).Render(label)
		}
		return lipgloss.NewStyle().Foreground(cMuted).Render(label)
	default:
		return lipgloss.NewStyle().Foreground(cMuted).Render(label)
	}
}

func formatTicketStatus(s contracts.TicketWorkflowStatus) string {
	switch s {
	case contracts.TicketDone:
		return "完成"
	case contracts.TicketBlocked:
		return "阻塞"
	case contracts.TicketQueued:
		return "排队"
	case contracts.TicketActive:
		return "进行中"
	case contracts.TicketArchived:
		return "归档"
	default:
		return "待办"
	}
}

func formatRuntimeState(v app.TicketView) string {
	return formatExecutionState(v)
}

func formatExecutionState(v app.TicketView) string {
	if v.RuntimeNeedsUser || v.RuntimeHealthState == contracts.TaskHealthWaitingUser {
		return "等待输入"
	}
	if v.LatestWorker == nil {
		return "未启动"
	}
	switch v.RuntimeHealthState {
	case contracts.TaskHealthIdle:
		return "空闲"
	case contracts.TaskHealthBusy:
		return "运行中"
	case contracts.TaskHealthStalled:
		return "异常"
	case contracts.TaskHealthDead:
		return "已停止"
	case contracts.TaskHealthAlive:
		if v.SessionAlive {
			return "空闲"
		}
		if v.SessionProbeFailed {
			return "待观测"
		}
		return "已停止"
	case contracts.TaskHealthUnknown:
		switch v.LatestWorker.Status {
		case contracts.WorkerCreating:
			return "启动中"
		case contracts.WorkerFailed:
			return "异常"
		case contracts.WorkerStopped:
			return "已停止"
		}
		if v.SessionProbeFailed {
			return "待观测"
		}
		if v.SessionAlive {
			return "待观测"
		}
		return "已停止"
	default:
		if v.SessionProbeFailed || v.SessionAlive {
			return "待观测"
		}
		return "未知"
	}
}

func formatSessionState(v app.TicketView) string {
	if v.LatestWorker == nil {
		return "无运行体"
	}
	if strings.TrimSpace(v.LatestWorker.LogPath) != "" {
		if v.SessionProbeFailed {
			return "运行未知"
		}
		if v.SessionAlive {
			return "运行中"
		}
		return "运行离线"
	}
	return "无运行体"
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		min := int(d.Minutes())
		sec := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", min, sec)
	}
	h := int(d.Hours())
	min := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, min)
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

type backlogReorderPlan struct {
	ticketID       uint
	targetTicketID uint
	ticketPriority int
	targetPriority int
	direction      int
}

func (m model) planBacklogReorder(direction int) (backlogReorderPlan, bool, string) {
	id, ok, denied := m.selectedTicketForAction(ticketActionPriority)
	if !ok {
		return backlogReorderPlan{}, false, denied
	}
	if direction != -1 && direction != 1 {
		return backlogReorderPlan{}, false, "未知移动方向"
	}
	sel := m.selectedRow()
	if sel.section != "backlog" {
		return backlogReorderPlan{}, false, "仅 backlog 区支持 Shift+K/J 手动排序"
	}

	backlogIDs := make([]uint, 0, len(m.rowRefs))
	idx := -1
	for _, ref := range m.rowRefs {
		if ref.kind != rowTicket || ref.section != "backlog" || ref.ticketID == 0 {
			continue
		}
		if ref.ticketID == id {
			idx = len(backlogIDs)
		}
		backlogIDs = append(backlogIDs, ref.ticketID)
	}
	if idx < 0 || idx >= len(backlogIDs) {
		return backlogReorderPlan{}, false, "未找到 backlog 位置（请稍后重试）"
	}

	targetIdx := idx + direction
	if targetIdx < 0 {
		return backlogReorderPlan{}, false, "已在 backlog 顶部"
	}
	if targetIdx >= len(backlogIDs) {
		return backlogReorderPlan{}, false, "已在 backlog 底部"
	}
	targetID := backlogIDs[targetIdx]

	selfView, ok := m.viewsByID[id]
	if !ok {
		return backlogReorderPlan{}, false, "详情尚未加载（等一下自动刷新）"
	}
	targetView, ok := m.viewsByID[targetID]
	if !ok {
		return backlogReorderPlan{}, false, "目标详情尚未加载（等一下自动刷新）"
	}

	selfPriority := selfView.Ticket.Priority
	targetPriority := targetView.Ticket.Priority
	nextSelf := targetPriority
	nextTarget := selfPriority
	if selfPriority == targetPriority {
		if direction < 0 {
			nextSelf = targetPriority + 1
		} else {
			nextSelf = targetPriority - 1
		}
		nextTarget = targetPriority
	}

	return backlogReorderPlan{
		ticketID:       id,
		targetTicketID: targetID,
		ticketPriority: nextSelf,
		targetPriority: nextTarget,
		direction:      direction,
	}, true, ""
}

func (m model) reorderBacklogCmd(plan backlogReorderPlan) tea.Cmd {
	return func() tea.Msg {
		if m.p == nil {
			return ticketReorderMsg{Err: fmt.Errorf("project 为空")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := m.p.SetTicketPriority(ctx, plan.ticketID, plan.ticketPriority); err != nil {
			return ticketReorderMsg{Err: err}
		}
		if err := m.p.SetTicketPriority(ctx, plan.targetTicketID, plan.targetPriority); err != nil {
			return ticketReorderMsg{Err: err}
		}
		return ticketReorderMsg{
			TicketID:       plan.ticketID,
			TargetTicketID: plan.targetTicketID,
			TicketPriority: plan.ticketPriority,
			TargetPriority: plan.targetPriority,
			Direction:      plan.direction,
		}
	}
}

func (m model) setTicketStatusCmd(ticketID uint, status contracts.TicketWorkflowStatus) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := m.p.SetTicketWorkflowStatus(ctx, ticketID, status)
		return ticketStatusMsg{TicketID: ticketID, Status: status, Err: err}
	}
}

func (m model) bumpPriorityCmd(ticketID uint, delta int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		np, err := m.p.BumpTicketPriority(ctx, ticketID, delta)
		return ticketPriorityMsg{TicketID: ticketID, Priority: np, Delta: delta, Err: err}
	}
}

func (m model) updateTicketTextCmd(ticketID uint, title, desc, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := m.p.UpdateTicketTextAndLabel(ctx, ticketID, title, desc, label)
		return ticketTextMsg{TicketID: ticketID, Err: err}
	}
}

func truncateMiddle(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(rs[:maxRunes])
	}
	// 尽量保留两头，方便定位关键内容（尤其是 JSON/命令行）。
	head := (maxRunes - 3) / 2
	tail := maxRunes - 3 - head
	if tail < 0 {
		tail = 0
	}
	return string(rs[:head]) + "..." + string(rs[len(rs)-tail:])
}
