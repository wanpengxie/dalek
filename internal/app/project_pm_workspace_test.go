package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegration_PMWorkspaceStateSync_WritesStateFile(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	if _, err := p.SetMaxRunningWorkers(ctx, 5); err != nil {
		t.Fatalf("SetMaxRunningWorkers failed: %v", err)
	}
	if _, err := p.CreateTicketWithDescription(ctx, "pm workspace backlog", ""); err != nil {
		t.Fatalf("CreateTicketWithDescription failed: %v", err)
	}

	planPath := p.PMPlanPath()
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("MkdirAll pm plan dir failed: %v", err)
	}
	planRaw := "# PM Plan\n\n" +
		"## 运行态（冷启动必读）\n\n" +
		"current_phase: doc-design\n" +
		"current_ticket: t-web-prd\n" +
		"current_status: planning\n" +
		"last_action: wrote PRD draft\n" +
		"next_action: design doc\n" +
		"blocker: 无\n\n" +
		"## 当前 Feature：为 dalek 开发 Web 管理页面\n\n" +
		"### 必须先产出的文档\n" +
		"- **需求文档**：建议路径 `docs/product/web-console-prd.md`\n" +
		"- **设计文档**：建议路径 `docs/architecture/web-console-design.md`\n\n" +
		"### 执行 ticket 表（结构化）\n" +
		"| ticket | batch | depends_on | pm_state | deliverable |\n" +
		"| --- | --- | --- | --- | --- |\n" +
		"| T-web-prd | Batch A | - | done | 产出 web 管理页面需求文档 |\n" +
		"| T-web-design | Batch A | T-web-prd | in_progress | 产出 web 管理页面设计文档 |\n" +
		"| T-web-api-overview | Batch B | T-web-prd, T-web-design | planned | 提供 overview 聚合 API |\n\n" +
		"### 真实验收标准（核心）\n" +
		"1. 启动真实 dalek 服务，而不是只运行测试。\n" +
		"2. 在浏览器中打开 web 管理页面。\n" +
		"3. 看到概览页真实展示项目状态。\n"
	if err := os.WriteFile(planPath, []byte(planRaw), 0o644); err != nil {
		t.Fatalf("WriteFile plan failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(p.RepoRoot(), "docs", "product"), 0o755); err != nil {
		t.Fatalf("MkdirAll docs/product failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(p.RepoRoot(), "docs", "architecture"), 0o755); err != nil {
		t.Fatalf("MkdirAll docs/architecture failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p.RepoRoot(), "docs", "product", "web-console-prd.md"), []byte("# prd\n"), 0o644); err != nil {
		t.Fatalf("WriteFile PRD failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(p.RepoRoot(), "docs", "architecture", "web-console-design.md"), []byte("# design\n"), 0o644); err != nil {
		t.Fatalf("WriteFile design failed: %v", err)
	}

	got, err := p.SyncPMWorkspaceState(ctx)
	if err != nil {
		t.Fatalf("SyncPMWorkspaceState failed: %v", err)
	}
	if got.Schema != pmWorkspaceStateSchema {
		t.Fatalf("unexpected schema: got=%q want=%q", got.Schema, pmWorkspaceStateSchema)
	}
	if got.Runtime.CurrentPhase != "ticket" {
		t.Fatalf("unexpected current_phase: got=%q", got.Runtime.CurrentPhase)
	}
	if got.Runtime.CurrentTicket != "T-web-prd" {
		t.Fatalf("unexpected current_ticket: got=%q", got.Runtime.CurrentTicket)
	}
	if got.Runtime.CurrentStatus != "done" {
		t.Fatalf("unexpected current_status: got=%q", got.Runtime.CurrentStatus)
	}
	if !strings.Contains(got.Runtime.LastAction, "synced plan graph @") {
		t.Fatalf("unexpected last_action: got=%q", got.Runtime.LastAction)
	}
	if got.Runtime.NextAction != "design doc" {
		t.Fatalf("unexpected next_action: got=%q", got.Runtime.NextAction)
	}
	if got.Runtime.Blocker != "无" {
		t.Fatalf("unexpected blocker: got=%q", got.Runtime.Blocker)
	}
	if got.Runtime.CurrentFeature != "为 dalek 开发 Web 管理页面" {
		t.Fatalf("unexpected current_feature: got=%q", got.Runtime.CurrentFeature)
	}
	if got.Feature.Title != "为 dalek 开发 Web 管理页面" {
		t.Fatalf("unexpected feature title: got=%q", got.Feature.Title)
	}
	if len(got.Feature.Docs) != 2 {
		t.Fatalf("unexpected doc count: got=%d want=2", len(got.Feature.Docs))
	}
	if !got.Feature.Docs[0].Exists || !got.Feature.Docs[1].Exists {
		t.Fatalf("expected docs to exist: %+v", got.Feature.Docs)
	}
	if len(got.Feature.Tickets) != 3 {
		t.Fatalf("unexpected planned ticket count: got=%d want=3", len(got.Feature.Tickets))
	}
	tickets := map[string]PMWorkspacePlannedTicket{}
	for _, ticket := range got.Feature.Tickets {
		tickets[ticket.ID] = ticket
	}
	if tickets["T-web-design"].Status != "in_progress" {
		t.Fatalf("unexpected T-web-design status: got=%q want=in_progress", tickets["T-web-design"].Status)
	}
	if len(tickets["T-web-api-overview"].DependsOn) != 2 {
		t.Fatalf("unexpected dependency count: got=%d want=2", len(tickets["T-web-api-overview"].DependsOn))
	}
	if got.Feature.Acceptance.Status != "pending" {
		t.Fatalf("unexpected acceptance status: got=%q want=pending", got.Feature.Acceptance.Status)
	}
	if len(got.Feature.Acceptance.RequiredChecks) != 3 {
		t.Fatalf("unexpected acceptance checks count: got=%d want=3", len(got.Feature.Acceptance.RequiredChecks))
	}
	if got.Snapshot.TicketCounts["backlog"] != 1 {
		t.Fatalf("unexpected backlog count: got=%d want=1", got.Snapshot.TicketCounts["backlog"])
	}
	if got.Snapshot.WorkerStats.MaxRunning != 5 {
		t.Fatalf("unexpected max_running: got=%d want=5", got.Snapshot.WorkerStats.MaxRunning)
	}
	if got.Files.PlanPath != planPath {
		t.Fatalf("unexpected plan path: got=%q want=%q", got.Files.PlanPath, planPath)
	}
	if got.Files.PlanJSONPath != p.PMPlanJSONPath() {
		t.Fatalf("unexpected plan.json path: got=%q want=%q", got.Files.PlanJSONPath, p.PMPlanJSONPath())
	}
	if got.Files.StatePath != p.PMWorkspaceStatePath() {
		t.Fatalf("unexpected state path: got=%q want=%q", got.Files.StatePath, p.PMWorkspaceStatePath())
	}
	if got.Files.AcceptancePath != p.PMAcceptancePath() {
		t.Fatalf("unexpected acceptance path: got=%q want=%q", got.Files.AcceptancePath, p.PMAcceptancePath())
	}
	if _, err := os.Stat(p.PMWorkspaceStatePath()); err != nil {
		t.Fatalf("pm state file should exist: %v", err)
	}
	if _, err := os.Stat(p.PMPlanJSONPath()); err != nil {
		t.Fatalf("pm plan.json file should exist: %v", err)
	}
	if _, err := os.Stat(p.PMAcceptancePath()); err != nil {
		t.Fatalf("pm acceptance file should exist: %v", err)
	}
	planRendered, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read rendered plan.md failed: %v", err)
	}
	if !strings.Contains(string(planRendered), "## Goal") || !strings.Contains(string(planRendered), "## Execution Graph") {
		t.Fatalf("rendered plan.md missing required sections")
	}

	loaded, err := p.LoadPMWorkspaceState()
	if err != nil {
		t.Fatalf("LoadPMWorkspaceState failed: %v", err)
	}
	if loaded.Runtime.CurrentFeature != got.Runtime.CurrentFeature {
		t.Fatalf("loaded current_feature mismatch: got=%q want=%q", loaded.Runtime.CurrentFeature, got.Runtime.CurrentFeature)
	}
	if loaded.Snapshot.TicketCounts["backlog"] != 1 {
		t.Fatalf("loaded backlog count mismatch: got=%d want=1", loaded.Snapshot.TicketCounts["backlog"])
	}
	if len(loaded.Feature.Tickets) != 3 {
		t.Fatalf("loaded planned ticket count mismatch: got=%d want=3", len(loaded.Feature.Tickets))
	}
}

func TestIntegration_PMWorkspaceStateSync_ReadsAcceptanceEvidence(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	planPath := p.PMPlanPath()
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("MkdirAll pm plan dir failed: %v", err)
	}
	planRaw := `# PM Plan

## 当前 Feature：为 dalek 开发 Web 管理页面

### 真实验收标准（核心）
1. 启动真实 dalek 服务。
2. 打开 web 管理页面。
`
	if err := os.WriteFile(planPath, []byte(planRaw), 0o644); err != nil {
		t.Fatalf("WriteFile plan failed: %v", err)
	}
	acceptanceRaw := `# PM Acceptance Evidence

## 当前 Feature

- feature: 为 dalek 开发 Web 管理页面
- status: running

### 环境
- 启动命令：dalek serve --http :8080
- 访问 URL：http://127.0.0.1:8080/ui

### 操作步骤
1. 打开 overview 页面
2. 打开 tickets 页面

### 观察结果
- overview 渲染了真实 ticket 数据
- tickets 页面可以看到 backlog ticket

### 结论
- real acceptance in progress
`
	if err := os.MkdirAll(filepath.Dir(p.PMAcceptancePath()), 0o755); err != nil {
		t.Fatalf("MkdirAll pm acceptance dir failed: %v", err)
	}
	if err := os.WriteFile(p.PMAcceptancePath(), []byte(acceptanceRaw), 0o644); err != nil {
		t.Fatalf("WriteFile acceptance failed: %v", err)
	}

	got, err := p.SyncPMWorkspaceState(ctx)
	if err != nil {
		t.Fatalf("SyncPMWorkspaceState failed: %v", err)
	}
	if got.Feature.Acceptance.Status != "running" {
		t.Fatalf("unexpected acceptance status: got=%q want=running", got.Feature.Acceptance.Status)
	}
	if got.Feature.Acceptance.StartupCommand != "dalek serve --http :8080" {
		t.Fatalf("unexpected startup command: got=%q", got.Feature.Acceptance.StartupCommand)
	}
	if got.Feature.Acceptance.URL != "http://127.0.0.1:8080/ui" {
		t.Fatalf("unexpected acceptance url: got=%q", got.Feature.Acceptance.URL)
	}
	if len(got.Feature.Acceptance.Steps) != 2 {
		t.Fatalf("unexpected acceptance steps count: got=%d want=2", len(got.Feature.Acceptance.Steps))
	}
	if len(got.Feature.Acceptance.Observations) != 2 {
		t.Fatalf("unexpected acceptance observations count: got=%d want=2", len(got.Feature.Acceptance.Observations))
	}
	if got.Feature.Acceptance.Conclusion != "real acceptance in progress" {
		t.Fatalf("unexpected acceptance conclusion: got=%q", got.Feature.Acceptance.Conclusion)
	}
}
