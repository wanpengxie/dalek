package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	pmWorkspaceStateSchema       = "dalek.pm.state.v1"
	defaultPMAcceptanceRelPath   = ".dalek/pm/acceptance.md"
	defaultPMAcceptanceStatus    = "pending"
	defaultPMRuntimeCurrentPhase = "none"
	defaultPMRuntimeTicket       = "none"
	defaultPMRuntimeStatus       = "idle"
	defaultPMRuntimeLastAction   = "无"
	defaultPMRuntimeNextAction   = "等待新任务"
	defaultPMRuntimeBlocker      = "无"
)

type PMWorkspaceFiles struct {
	PlanPath       string `json:"plan_path"`
	PlanJSONPath   string `json:"plan_json_path,omitempty"`
	StatePath      string `json:"state_path"`
	AcceptancePath string `json:"acceptance_path"`
}

type PMWorkspaceRuntime struct {
	CurrentPhase   string `json:"current_phase"`
	CurrentTicket  string `json:"current_ticket"`
	CurrentStatus  string `json:"current_status"`
	LastAction     string `json:"last_action"`
	NextAction     string `json:"next_action"`
	Blocker        string `json:"blocker"`
	CurrentFeature string `json:"current_feature,omitempty"`
}

type PMWorkspaceSnapshot struct {
	TicketCounts map[string]int       `json:"ticket_counts"`
	WorkerStats  DashboardWorkerStats `json:"worker_stats"`
	MergeCounts  map[string]int       `json:"merge_counts"`
	InboxCounts  DashboardInboxCounts `json:"inbox_counts"`
}

type PMWorkspaceDoc struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type PMWorkspacePlannedTicket struct {
	ID          string   `json:"id"`
	Batch       string   `json:"batch,omitempty"`
	Status      string   `json:"status,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Deliverable string   `json:"deliverable,omitempty"`
}

type PMWorkspaceAcceptance struct {
	Path           string   `json:"path"`
	Exists         bool     `json:"exists"`
	Status         string   `json:"status"`
	RequiredChecks []string `json:"required_checks,omitempty"`
	StartupCommand string   `json:"startup_command,omitempty"`
	URL            string   `json:"url,omitempty"`
	Steps          []string `json:"steps,omitempty"`
	Observations   []string `json:"observations,omitempty"`
	Conclusion     string   `json:"conclusion,omitempty"`
}

type PMWorkspaceFeature struct {
	Title      string                     `json:"title,omitempty"`
	Docs       []PMWorkspaceDoc           `json:"docs,omitempty"`
	Tickets    []PMWorkspacePlannedTicket `json:"tickets,omitempty"`
	Acceptance PMWorkspaceAcceptance      `json:"acceptance"`
}

type PMWorkspaceState struct {
	Schema    string              `json:"schema"`
	Files     PMWorkspaceFiles    `json:"files"`
	Runtime   PMWorkspaceRuntime  `json:"runtime"`
	Snapshot  PMWorkspaceSnapshot `json:"snapshot"`
	Feature   PMWorkspaceFeature  `json:"feature"`
	UpdatedAt time.Time           `json:"updated_at"`
}

type pmPlanSnapshot struct {
	CurrentPhase   string
	CurrentTicket  string
	CurrentStatus  string
	LastAction     string
	NextAction     string
	Blocker        string
	CurrentFeature string
	FeatureDocs    []PMWorkspaceDoc
	PlannedTickets []PMWorkspacePlannedTicket
	Acceptance     []string
}

type pmAcceptanceSnapshot struct {
	Status         string
	StartupCommand string
	URL            string
	Steps          []string
	Observations   []string
	Conclusion     string
}

func (p *Project) PMPlanPath() string {
	root := strings.TrimSpace(p.RepoRoot())
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".dalek", "pm", "plan.md")
}

func (p *Project) PMWorkspaceStatePath() string {
	root := strings.TrimSpace(p.RepoRoot())
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".dalek", "pm", "state.json")
}

func (p *Project) PMAcceptancePath() string {
	root := strings.TrimSpace(p.RepoRoot())
	if root == "" {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(defaultPMAcceptanceRelPath))
}

func (p *Project) LoadPMWorkspaceState() (PMWorkspaceState, error) {
	path := strings.TrimSpace(p.PMWorkspaceStatePath())
	if path == "" {
		return PMWorkspaceState{}, fmt.Errorf("pm state path 为空")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return PMWorkspaceState{}, err
	}
	var state PMWorkspaceState
	if err := json.Unmarshal(raw, &state); err != nil {
		return PMWorkspaceState{}, err
	}
	return normalizePMWorkspaceState(state, path, p.PMPlanPath(), p.PMPlanJSONPath(), p.PMAcceptancePath()), nil
}

func (p *Project) SyncPMWorkspaceState(ctx context.Context) (PMWorkspaceState, error) {
	state, err := p.buildPMWorkspaceState(ctx)
	if err != nil {
		return PMWorkspaceState{}, err
	}
	path := strings.TrimSpace(state.Files.StatePath)
	if path == "" {
		return PMWorkspaceState{}, fmt.Errorf("pm state path 为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return PMWorkspaceState{}, err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return PMWorkspaceState{}, err
	}
	raw = append(raw, '\n')
	if err := writePMWorkspaceFileAtomic(path, raw, 0o644); err != nil {
		return PMWorkspaceState{}, err
	}
	return state, nil
}

func (p *Project) buildPMWorkspaceState(ctx context.Context) (PMWorkspaceState, error) {
	if p == nil {
		return PMWorkspaceState{}, fmt.Errorf("project 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dashboard, err := p.Dashboard(ctx)
	if err != nil {
		return PMWorkspaceState{}, err
	}
	planPath := p.PMPlanPath()
	planJSONPath := p.PMPlanJSONPath()
	statePath := p.PMWorkspaceStatePath()
	acceptancePath := p.PMAcceptancePath()
	repoRoot := strings.TrimSpace(p.RepoRoot())
	graph, _, err := loadOrMigratePlanGraph(planJSONPath, planPath, statePath, acceptancePath)
	if err != nil {
		return PMWorkspaceState{}, err
	}
	graph = normalizeFeatureGraph(graph, time.Now().UTC())
	if err := SavePlanGraph(planJSONPath, graph); err != nil {
		return PMWorkspaceState{}, err
	}
	if err := ensurePMAcceptanceFile(acceptancePath, graph.Goal); err != nil {
		return PMWorkspaceState{}, err
	}
	if err := syncPlanMarkdownFromGraph(planPath, graph); err != nil {
		return PMWorkspaceState{}, err
	}
	acceptance := readPMAcceptanceSnapshot(acceptancePath)
	runtime, feature := SyncStateFromGraph(graph, acceptance, repoRoot, acceptancePath)
	return normalizePMWorkspaceState(PMWorkspaceState{
		Schema: pmWorkspaceStateSchema,
		Files: PMWorkspaceFiles{
			PlanPath:       planPath,
			PlanJSONPath:   planJSONPath,
			StatePath:      statePath,
			AcceptancePath: acceptancePath,
		},
		Runtime: runtime,
		Snapshot: PMWorkspaceSnapshot{
			TicketCounts: cloneIntMap(dashboard.TicketCounts),
			WorkerStats:  dashboard.WorkerStats,
			MergeCounts:  cloneIntMap(dashboard.MergeCounts),
			InboxCounts:  dashboard.InboxCounts,
		},
		Feature:   feature,
		UpdatedAt: time.Now().UTC(),
	}, statePath, planPath, planJSONPath, acceptancePath), nil
}

func normalizePMWorkspaceState(state PMWorkspaceState, statePath, planPath, planJSONPath, acceptancePath string) PMWorkspaceState {
	state.Schema = strings.TrimSpace(state.Schema)
	if state.Schema == "" {
		state.Schema = pmWorkspaceStateSchema
	}
	if state.Files.PlanPath == "" {
		state.Files.PlanPath = strings.TrimSpace(planPath)
	}
	if state.Files.PlanJSONPath == "" {
		state.Files.PlanJSONPath = strings.TrimSpace(planJSONPath)
	}
	if state.Files.StatePath == "" {
		state.Files.StatePath = strings.TrimSpace(statePath)
	}
	if state.Files.AcceptancePath == "" {
		state.Files.AcceptancePath = strings.TrimSpace(acceptancePath)
	}
	if strings.TrimSpace(state.Runtime.CurrentPhase) == "" {
		state.Runtime.CurrentPhase = defaultPMRuntimeCurrentPhase
	}
	if strings.TrimSpace(state.Runtime.CurrentTicket) == "" {
		state.Runtime.CurrentTicket = defaultPMRuntimeTicket
	}
	if strings.TrimSpace(state.Runtime.CurrentStatus) == "" {
		state.Runtime.CurrentStatus = defaultPMRuntimeStatus
	}
	if strings.TrimSpace(state.Runtime.LastAction) == "" {
		state.Runtime.LastAction = defaultPMRuntimeLastAction
	}
	if strings.TrimSpace(state.Runtime.NextAction) == "" {
		state.Runtime.NextAction = defaultPMRuntimeNextAction
	}
	if strings.TrimSpace(state.Runtime.Blocker) == "" {
		state.Runtime.Blocker = defaultPMRuntimeBlocker
	}
	state.Runtime.CurrentFeature = normalizePMFeatureTitle(state.Runtime.CurrentFeature)
	if state.Runtime.CurrentFeature == "" {
		state.Runtime.CurrentFeature = normalizePMFeatureTitle(state.Feature.Title)
	}
	state.Feature.Title = normalizePMFeatureTitle(state.Feature.Title)
	if state.Feature.Title == "" {
		state.Feature.Title = state.Runtime.CurrentFeature
	}
	state.Feature.Docs = normalizePMWorkspaceDocs(state.Feature.Docs)
	state.Feature.Tickets = normalizePMPlannedTickets(state.Feature.Tickets)
	if state.Feature.Acceptance.Path == "" {
		state.Feature.Acceptance.Path = strings.TrimSpace(acceptancePath)
	}
	state.Feature.Acceptance.Status = normalizePMAcceptanceStatus(state.Feature.Acceptance.Status)
	state.Feature.Acceptance.RequiredChecks = cloneStringSlice(state.Feature.Acceptance.RequiredChecks)
	state.Feature.Acceptance.Steps = cloneStringSlice(state.Feature.Acceptance.Steps)
	state.Feature.Acceptance.Observations = cloneStringSlice(state.Feature.Acceptance.Observations)
	if state.Snapshot.TicketCounts == nil {
		state.Snapshot.TicketCounts = newDashboardTicketCounts()
	}
	if state.Snapshot.MergeCounts == nil {
		state.Snapshot.MergeCounts = newDashboardMergeCounts()
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func readPMPlanSnapshot(path string) pmPlanSnapshot {
	out := pmPlanSnapshot{
		CurrentPhase:  defaultPMRuntimeCurrentPhase,
		CurrentTicket: defaultPMRuntimeTicket,
		CurrentStatus: defaultPMRuntimeStatus,
		LastAction:    defaultPMRuntimeLastAction,
		NextAction:    defaultPMRuntimeNextAction,
		Blocker:       defaultPMRuntimeBlocker,
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	lines := strings.Split(string(raw), "\n")
	out.FeatureDocs = parsePMFeatureDocs(lines)
	out.PlannedTickets = parsePMPlannedTickets(lines)
	out.Acceptance = parsePMAcceptanceChecks(lines)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "## 当前 Feature：") {
			out.CurrentFeature = normalizePMFeatureTitle(strings.TrimSpace(strings.TrimPrefix(trimmed, "## 当前 Feature：")))
			continue
		}
		if strings.HasPrefix(trimmed, "## Current Feature:") {
			out.CurrentFeature = normalizePMFeatureTitle(strings.TrimSpace(strings.TrimPrefix(trimmed, "## Current Feature:")))
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "current_phase":
			if value != "" {
				out.CurrentPhase = value
			}
		case "current_ticket":
			if value != "" {
				out.CurrentTicket = value
			}
		case "current_status":
			if value != "" {
				out.CurrentStatus = value
			}
		case "last_action":
			if value != "" {
				out.LastAction = value
			}
		case "next_action":
			if value != "" {
				out.NextAction = value
			}
		case "blocker":
			if value != "" {
				out.Blocker = value
			}
		}
	}
	return out
}

func parsePMFeatureDocs(lines []string) []PMWorkspaceDoc {
	section := extractMarkdownSection(lines, "### 必须先产出的文档", "### Required Docs")
	if len(section) == 0 {
		return nil
	}
	docs := make([]PMWorkspaceDoc, 0, 2)
	for _, line := range section {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		path := extractFirstBacktickValue(trimmed)
		if path == "" {
			continue
		}
		label := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if idx := strings.IndexAny(label, "：:"); idx >= 0 {
			label = strings.TrimSpace(label[:idx])
		}
		label = strings.Trim(label, "*` ")
		if label == "" {
			label = "文档"
		}
		docs = append(docs, PMWorkspaceDoc{
			Kind: label,
			Path: path,
		})
	}
	return docs
}

func parsePMPlannedTickets(lines []string) []PMWorkspacePlannedTicket {
	section := extractMarkdownSection(lines, "### 执行 ticket 表（结构化）", "### Structured Ticket Plan")
	if len(section) == 0 {
		return nil
	}
	tickets := make([]PMWorkspacePlannedTicket, 0, len(section))
	for _, line := range section {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := parseMarkdownTableRow(trimmed)
		if len(cells) < 5 || isMarkdownTableSeparatorRow(cells) {
			continue
		}
		if strings.EqualFold(cells[0], "ticket") {
			continue
		}
		ticketID := strings.TrimSpace(cells[0])
		if ticketID == "" {
			continue
		}
		tickets = append(tickets, PMWorkspacePlannedTicket{
			ID:          ticketID,
			Batch:       strings.TrimSpace(cells[1]),
			DependsOn:   parsePMDependsOn(cells[2]),
			Status:      normalizePMPlannedTicketStatus(cells[3]),
			Deliverable: strings.TrimSpace(cells[4]),
		})
	}
	return tickets
}

func parsePMAcceptanceChecks(lines []string) []string {
	section := extractMarkdownSection(lines, "### 真实验收标准（核心）", "### Real Acceptance Criteria")
	if len(section) == 0 {
		return nil
	}
	return splitOrderedMarkdownItems(section)
}

func readPMAcceptanceSnapshot(path string) pmAcceptanceSnapshot {
	out := pmAcceptanceSnapshot{Status: defaultPMAcceptanceStatus}
	path = strings.TrimSpace(path)
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if value, ok := parseMarkdownKeyValue(trimmed, "status"); ok {
			out.Status = value
			break
		}
	}
	envSection := extractMarkdownSection(lines, "### 环境", "### Environment")
	for _, line := range envSection {
		trimmed := strings.TrimSpace(line)
		if value := parseMarkdownLabeledValue(trimmed, "启动命令", "startup command"); value != "" {
			out.StartupCommand = value
		}
		if value := parseMarkdownLabeledValue(trimmed, "访问 URL", "url"); value != "" {
			out.URL = value
		}
	}
	out.Steps = splitOrderedMarkdownItems(extractMarkdownSection(lines, "### 操作步骤", "### Steps"))
	out.Observations = parseMarkdownBullets(extractMarkdownSection(lines, "### 观察结果", "### Observations"))
	conclusion := parseMarkdownBullets(extractMarkdownSection(lines, "### 结论", "### Conclusion"))
	if len(conclusion) > 0 {
		out.Conclusion = strings.Join(conclusion, " ")
	}
	out.Status = normalizePMAcceptanceStatus(out.Status)
	return out
}

func ensurePMAcceptanceFile(path, featureTitle string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("pm acceptance path 为空")
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	title := normalizePMFeatureTitle(featureTitle)
	if title == "" {
		title = "none"
	}
	raw := fmt.Sprintf(`# PM Acceptance Evidence

## 当前 Feature

- feature: %s
- status: pending

## 真实验收要求

- PM 必须以真实场景为主完成验收
- go test / go build 只能作为辅助证据
- 只有真实产品链路通过，才能判定 feature 完成

## Evidence 模板

### 环境
- 启动命令：
- 访问 URL：

### 操作步骤
1.
2.
3.

### 观察结果
- 

### 结论
- pending
`, title)
	return writePMWorkspaceFileAtomic(path, []byte(raw), 0o644)
}

func normalizePMFeatureTitle(raw string) string {
	value := strings.TrimSpace(raw)
	switch strings.ToLower(value) {
	case "", "none":
		return ""
	}
	if value == "无" {
		return ""
	}
	return value
}

func normalizePMWorkspaceDocs(src []PMWorkspaceDoc) []PMWorkspaceDoc {
	if len(src) == 0 {
		return nil
	}
	docs := make([]PMWorkspaceDoc, 0, len(src))
	for _, doc := range src {
		doc.Kind = strings.Trim(strings.TrimSpace(doc.Kind), "*`")
		doc.Path = strings.TrimSpace(doc.Path)
		if doc.Path == "" {
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil
	}
	return docs
}

func normalizePMPlannedTickets(src []PMWorkspacePlannedTicket) []PMWorkspacePlannedTicket {
	if len(src) == 0 {
		return nil
	}
	tickets := make([]PMWorkspacePlannedTicket, 0, len(src))
	for _, ticket := range src {
		ticket.ID = strings.TrimSpace(ticket.ID)
		if ticket.ID == "" {
			continue
		}
		ticket.Batch = strings.TrimSpace(ticket.Batch)
		ticket.Status = normalizePMPlannedTicketStatus(ticket.Status)
		ticket.DependsOn = parsePMDependsOn(strings.Join(ticket.DependsOn, ","))
		ticket.Deliverable = strings.TrimSpace(ticket.Deliverable)
		tickets = append(tickets, ticket)
	}
	if len(tickets) == 0 {
		return nil
	}
	return tickets
}

func resolvePMWorkspaceDocs(repoRoot string, docs []PMWorkspaceDoc) []PMWorkspaceDoc {
	docs = normalizePMWorkspaceDocs(docs)
	if len(docs) == 0 {
		return nil
	}
	out := make([]PMWorkspaceDoc, 0, len(docs))
	for _, doc := range docs {
		doc.Exists = fileExists(resolveRepoPath(repoRoot, doc.Path))
		out = append(out, doc)
	}
	return out
}

func cloneIntMap(src map[string]int) map[string]int {
	if len(src) == 0 {
		return map[string]int{}
	}
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneStringSlice(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, 0, len(src))
	for _, item := range src {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		dst = append(dst, item)
	}
	if len(dst) == 0 {
		return nil
	}
	return dst
}

func normalizePMAcceptanceStatus(raw string) string {
	value := strings.TrimSpace(raw)
	switch strings.ToLower(value) {
	case "", "-", "none":
		return "pending"
	}
	if value == "无" {
		return "pending"
	}
	return value
}

func normalizePMPlannedTicketStatus(raw string) string {
	value := strings.TrimSpace(raw)
	switch strings.ToLower(value) {
	case "", "-", "none":
		return "planned"
	}
	if value == "无" {
		return "planned"
	}
	return value
}

func parsePMDependsOn(raw string) []string {
	raw = strings.NewReplacer("，", ",", ";", ",").Replace(strings.TrimSpace(raw))
	switch strings.ToLower(raw) {
	case "", "-", "none":
		return nil
	}
	if raw == "无" {
		return nil
	}
	parts := strings.Split(raw, ",")
	deps := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		deps = append(deps, part)
	}
	if len(deps) == 0 {
		return nil
	}
	return deps
}

func extractMarkdownSection(lines []string, headings ...string) []string {
	if len(lines) == 0 || len(headings) == 0 {
		return nil
	}
	candidates := make(map[string]struct{}, len(headings))
	for _, heading := range headings {
		heading = strings.TrimSpace(heading)
		if heading != "" {
			candidates[heading] = struct{}{}
		}
	}
	start := -1
	level := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if _, ok := candidates[trimmed]; ok {
			start = i + 1
			level = markdownHeadingLevel(trimmed)
			break
		}
	}
	if start < 0 {
		return nil
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		nextLevel := markdownHeadingLevel(trimmed)
		if nextLevel > 0 && nextLevel <= level {
			end = i
			break
		}
	}
	return lines[start:end]
}

func markdownHeadingLevel(line string) int {
	line = strings.TrimSpace(line)
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	if count == 0 || count >= len(line) || line[count] != ' ' {
		return 0
	}
	return count
}

func extractFirstBacktickValue(line string) string {
	start := strings.IndexByte(line, '`')
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(line[start+1:], '`')
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(line[start+1 : start+1+end])
}

func parseMarkdownTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return nil
	}
	parts := strings.Split(line, "|")
	if len(parts) < 3 {
		return nil
	}
	row := make([]string, 0, len(parts)-2)
	for _, part := range parts[1 : len(parts)-1] {
		row = append(row, strings.TrimSpace(part))
	}
	return row
}

func isMarkdownTableSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			return false
		}
		for _, r := range cell {
			if r != '-' && r != ':' && r != ' ' {
				return false
			}
		}
	}
	return true
}

func splitOrderedMarkdownItems(lines []string) []string {
	items := make([]string, 0, len(lines))
	current := ""
	flush := func() {
		current = strings.TrimSpace(current)
		if current != "" {
			items = append(items, current)
		}
		current = ""
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if item, ok := parseOrderedMarkdownItem(trimmed); ok {
			flush()
			current = item
			continue
		}
		if current == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		}
		current = strings.TrimSpace(current + " " + trimmed)
	}
	flush()
	return items
}

func parseOrderedMarkdownItem(line string) (string, bool) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != '.' {
		return "", false
	}
	return strings.TrimSpace(line[i+1:]), true
}

func parseMarkdownBullets(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func parseMarkdownKeyValue(line, key string) (string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
	field, value, ok := strings.Cut(line, ":")
	if !ok {
		field, value, ok = strings.Cut(line, "：")
	}
	if !ok {
		return "", false
	}
	if strings.TrimSpace(strings.ToLower(field)) != strings.ToLower(strings.TrimSpace(key)) {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func parseMarkdownLabeledValue(line string, labels ...string) string {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		prefixes := []string{label + ":", label + "："}
		for _, prefix := range prefixes {
			if strings.HasPrefix(strings.ToLower(line), strings.ToLower(prefix)) {
				return strings.TrimSpace(line[len(prefix):])
			}
		}
	}
	return ""
}

func resolveRepoPath(repoRoot, target string) string {
	target = strings.TrimSpace(target)
	if target == "" || filepath.IsAbs(target) {
		return target
	}
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return target
	}
	return filepath.Join(repoRoot, filepath.FromSlash(target))
}

func fileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func writePMWorkspaceFileAtomic(path string, raw []byte, mode os.FileMode) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path 不能为空")
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pm-state.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil && !errors.Is(err, os.ErrPermission) {
		return err
	}
	return os.Rename(tmpName, path)
}
