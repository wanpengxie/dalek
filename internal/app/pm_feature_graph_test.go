package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestPlanGraphValidateGraphRejectsCycle(t *testing.T) {
	graph := contracts.FeatureGraph{
		Schema:    contracts.PMFeatureGraphSchemaV1,
		FeatureID: "feature-cycle",
		Goal:      "cycle validation",
		Nodes: []contracts.FeatureNode{
			{
				ID:        "a",
				Type:      contracts.FeatureNodeRequirement,
				Owner:     contracts.FeatureNodeOwnerPM,
				Status:    contracts.FeatureNodePending,
				DependsOn: []string{"b"},
			},
			{
				ID:        "b",
				Type:      contracts.FeatureNodeDesign,
				Owner:     contracts.FeatureNodeOwnerPM,
				Status:    contracts.FeatureNodePending,
				DependsOn: []string{"a"},
			},
		},
		UpdatedAt: time.Now().UTC(),
	}
	err := ValidateGraph(normalizeFeatureGraph(graph, time.Now().UTC()))
	if err == nil {
		t.Fatalf("expected cycle validation error")
	}
	if !strings.Contains(err.Error(), "循环依赖") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanGraphMigrateLegacyPlanRoundTrip(t *testing.T) {
	repo := t.TempDir()
	planPath := filepath.Join(repo, ".dalek", "pm", "plan.md")
	planJSONPath := filepath.Join(repo, ".dalek", "pm", "plan.json")
	acceptancePath := filepath.Join(repo, ".dalek", "pm", "acceptance.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("mkdir pm dir failed: %v", err)
	}
	planRaw := "# PM Plan\n\n" +
		"current_phase: doc-design\n" +
		"current_ticket: t-web-prd\n" +
		"next_action: continue design\n\n" +
		"## 当前 Feature：为 dalek 开发 Web 管理页面\n\n" +
		"### 必须先产出的文档\n" +
		"- **需求文档**：建议路径 `docs/product/web-console-prd.md`\n" +
		"- **设计文档**：建议路径 `docs/architecture/web-console-design.md`\n\n" +
		"### 执行 ticket 表（结构化）\n" +
		"| ticket | batch | depends_on | pm_state | deliverable |\n" +
		"| --- | --- | --- | --- | --- |\n" +
		"| T-web-prd | Batch A | - | done | 产出 web 管理页面需求文档 |\n" +
		"| T-web-design | Batch A | T-web-prd | in_progress | 产出 web 管理页面设计文档 |\n\n" +
		"### 真实验收标准（核心）\n" +
		"1. 打开真实页面并验证数据展示。\n"
	if err := os.WriteFile(planPath, []byte(planRaw), 0o644); err != nil {
		t.Fatalf("write plan.md failed: %v", err)
	}
	acceptanceRaw := `# PM Acceptance Evidence

- status: running
`
	if err := os.WriteFile(acceptancePath, []byte(acceptanceRaw), 0o644); err != nil {
		t.Fatalf("write acceptance.md failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs", "product"), 0o755); err != nil {
		t.Fatalf("mkdir docs/product failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs", "architecture"), 0o755); err != nil {
		t.Fatalf("mkdir docs/architecture failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "product", "web-console-prd.md"), []byte("# prd\n"), 0o644); err != nil {
		t.Fatalf("write prd failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "architecture", "web-console-design.md"), []byte("# design\n"), 0o644); err != nil {
		t.Fatalf("write design failed: %v", err)
	}

	graph, err := MigrateLegacyPlan(planPath, filepath.Join(repo, ".dalek", "pm", "state.json"), acceptancePath)
	if err != nil {
		t.Fatalf("MigrateLegacyPlan failed: %v", err)
	}
	if graph.Schema != contracts.PMFeatureGraphSchemaV1 {
		t.Fatalf("unexpected schema: got=%q", graph.Schema)
	}
	if graph.FeatureID == "" || graph.Goal == "" {
		t.Fatalf("feature identity should not be empty: %+v", graph)
	}
	if len(graph.Nodes) < 5 {
		t.Fatalf("expected migrated graph contains baseline nodes, got=%d", len(graph.Nodes))
	}
	if err := SavePlanGraph(planJSONPath, graph); err != nil {
		t.Fatalf("SavePlanGraph failed: %v", err)
	}
	loaded, err := LoadPlanGraph(planJSONPath)
	if err != nil {
		t.Fatalf("LoadPlanGraph failed: %v", err)
	}
	if loaded.FeatureID != graph.FeatureID {
		t.Fatalf("feature id mismatch: got=%q want=%q", loaded.FeatureID, graph.FeatureID)
	}

	rendered := RenderPlanMarkdown(loaded)
	for _, heading := range []string{
		"## Goal",
		"## Current Status",
		"## Execution Graph",
		"## Ready Nodes",
		"## Blocked Nodes",
		"## Acceptance Gates",
		"## Latest Evidence",
		"## Next PM Action",
	} {
		if !strings.Contains(rendered, heading) {
			t.Fatalf("rendered markdown missing heading %q", heading)
		}
	}
}

func TestPlanGraphSyncStateFromGraph(t *testing.T) {
	repo := t.TempDir()
	acceptancePath := filepath.Join(repo, ".dalek", "pm", "acceptance.md")
	if err := os.MkdirAll(filepath.Dir(acceptancePath), 0o755); err != nil {
		t.Fatalf("mkdir acceptance dir failed: %v", err)
	}
	if err := os.WriteFile(acceptancePath, []byte("# acceptance\n"), 0o644); err != nil {
		t.Fatalf("write acceptance failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs", "product"), 0o755); err != nil {
		t.Fatalf("mkdir docs/product failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs", "architecture"), 0o755); err != nil {
		t.Fatalf("mkdir docs/architecture failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "product", "feature-prd.md"), []byte("# prd\n"), 0o644); err != nil {
		t.Fatalf("write prd failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "architecture", "feature-design.md"), []byte("# design\n"), 0o644); err != nil {
		t.Fatalf("write design failed: %v", err)
	}

	graph := contracts.FeatureGraph{
		Schema:    contracts.PMFeatureGraphSchemaV1,
		FeatureID: "feature-demo",
		Goal:      "demo feature",
		Docs: []contracts.FeatureDocRef{
			{Kind: "需求文档", Path: "docs/product/feature-prd.md"},
			{Kind: "设计文档", Path: "docs/architecture/feature-design.md"},
		},
		Nodes: []contracts.FeatureNode{
			{
				ID:     "requirement-main",
				Type:   contracts.FeatureNodeRequirement,
				Owner:  contracts.FeatureNodeOwnerPM,
				Status: contracts.FeatureNodeDone,
			},
			{
				ID:        "design-main",
				Type:      contracts.FeatureNodeDesign,
				Owner:     contracts.FeatureNodeOwnerPM,
				Status:    contracts.FeatureNodeDone,
				DependsOn: []string{"requirement-main"},
			},
			{
				ID:        "ticket-t1",
				Type:      contracts.FeatureNodeTicket,
				Owner:     contracts.FeatureNodeOwnerWorker,
				Status:    contracts.FeatureNodeInProgress,
				DependsOn: []string{"design-main"},
				TicketID:  "T-1",
				DoneWhen:  "实现主流程",
				Notes:     "batch=BatchA",
			},
			{
				ID:        "acceptance-01",
				Type:      contracts.FeatureNodeAcceptance,
				Owner:     contracts.FeatureNodeOwnerPM,
				Status:    contracts.FeatureNodePending,
				DependsOn: []string{"ticket-t1"},
				DoneWhen:  "真实场景验收通过",
			},
		},
		CurrentFocus: "ticket-t1",
		NextPMAction: "dispatch integration ticket",
		UpdatedAt:    time.Now().UTC(),
	}
	graph.Edges = buildFeatureEdgesFromNodes(graph.Nodes)
	graph = normalizeFeatureGraph(graph, time.Now().UTC())
	if err := ValidateGraph(graph); err != nil {
		t.Fatalf("graph validation failed: %v", err)
	}

	runtime, feature := SyncStateFromGraph(graph, pmAcceptanceSnapshot{
		Status:         "running",
		StartupCommand: "dalek serve --http :8080",
		URL:            "http://127.0.0.1:8080/ui",
		Steps:          []string{"open ui", "verify summary"},
		Observations:   []string{"rendered"},
		Conclusion:     "in progress",
	}, repo, acceptancePath)

	if runtime.CurrentPhase != "ticket" {
		t.Fatalf("unexpected runtime phase: got=%q", runtime.CurrentPhase)
	}
	if runtime.CurrentTicket != "T-1" {
		t.Fatalf("unexpected runtime ticket: got=%q", runtime.CurrentTicket)
	}
	if runtime.CurrentStatus != "in_progress" {
		t.Fatalf("unexpected runtime status: got=%q", runtime.CurrentStatus)
	}
	if runtime.NextAction != "dispatch integration ticket" {
		t.Fatalf("unexpected next action: got=%q", runtime.NextAction)
	}
	if feature.Title != "demo feature" {
		t.Fatalf("unexpected feature title: got=%q", feature.Title)
	}
	if len(feature.Docs) != 2 || !feature.Docs[0].Exists || !feature.Docs[1].Exists {
		t.Fatalf("unexpected docs: %+v", feature.Docs)
	}
	if len(feature.Tickets) != 1 || feature.Tickets[0].ID != "T-1" {
		t.Fatalf("unexpected tickets: %+v", feature.Tickets)
	}
	if feature.Tickets[0].Status != "in_progress" {
		t.Fatalf("unexpected ticket status: got=%q", feature.Tickets[0].Status)
	}
	if feature.Acceptance.Status != "running" {
		t.Fatalf("unexpected acceptance status: got=%q", feature.Acceptance.Status)
	}
	if len(feature.Acceptance.RequiredChecks) != 1 || feature.Acceptance.RequiredChecks[0] != "真实场景验收通过" {
		t.Fatalf("unexpected acceptance checks: %+v", feature.Acceptance.RequiredChecks)
	}
	if !feature.Acceptance.Exists {
		t.Fatalf("acceptance file should exist")
	}
}
