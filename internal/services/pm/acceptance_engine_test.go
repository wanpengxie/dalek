package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestRunAcceptancePMOpExecutor_ExecutesCasesAndWritesBackArtifacts(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	pwBin := filepath.Join(t.TempDir(), "pw")
	pwScript := "#!/usr/bin/env bash\nset -euo pipefail\necho \"pw-ok $*\"\n"
	if err := os.WriteFile(pwBin, []byte(pwScript), 0o755); err != nil {
		t.Fatalf("write fake pw failed: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(pwBin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	graphPath := writeTestPlanGraph(t, p.RepoRoot, contracts.FeatureGraph{
		Schema:    contracts.PMFeatureGraphSchemaV1,
		FeatureID: "feature-acceptance-engine",
		Goal:      "acceptance engine",
		Nodes: []contracts.FeatureNode{
			{ID: "requirement-main", Type: contracts.FeatureNodeRequirement, Status: contracts.FeatureNodeDone},
			{ID: "design-main", Type: contracts.FeatureNodeDesign, Status: contracts.FeatureNodeDone, DependsOn: []string{"requirement-main"}},
			{ID: "ticket-main", Type: contracts.FeatureNodeTicket, Status: contracts.FeatureNodeDone, DependsOn: []string{"design-main"}},
			{ID: "acceptance-cli", Type: contracts.FeatureNodeAcceptance, Status: contracts.FeatureNodePending, DependsOn: []string{"ticket-main"}, DoneWhen: "CLI acceptance"},
			{ID: "acceptance-http", Type: contracts.FeatureNodeAcceptance, Status: contracts.FeatureNodePending, DependsOn: []string{"ticket-main"}, DoneWhen: "HTTP acceptance"},
			{ID: "acceptance-pw", Type: contracts.FeatureNodeAcceptance, Status: contracts.FeatureNodePending, DependsOn: []string{"ticket-main"}, DoneWhen: "PW acceptance"},
		},
		UpdatedAt: time.Now().UTC(),
	})

	op := contracts.PMOp{
		Kind: contracts.PMOpRunAcceptance,
		Arguments: contracts.JSONMap{
			"startup_command": "dalek serve --http :8080",
			"url":             server.URL,
			"cases": []any{
				map[string]any{
					"node_id":                "acceptance-cli",
					"type":                   "cli",
					"name":                   "cli smoke",
					"command":                "printf 'cli-pass\\n'",
					"expect_stdout_contains": []any{"cli-pass"},
				},
				map[string]any{
					"node_id":              "acceptance-http",
					"type":                 "http",
					"name":                 "http smoke",
					"url":                  server.URL + "/health",
					"method":               "GET",
					"expect_status":        200,
					"expect_body_contains": []any{"ok"},
				},
				map[string]any{
					"node_id":                "acceptance-pw",
					"type":                   "pw",
					"name":                   "pw smoke",
					"args":                   []any{"status"},
					"expect_stdout_contains": []any{"pw-ok"},
				},
			},
		},
	}

	res, err := runAcceptancePMOpExecutor{s: svc}.Execute(ctx, op)
	if err != nil {
		t.Fatalf("execute run_acceptance failed: %v", err)
	}
	if got := strings.TrimSpace(asString(res["status"])); got != "done" {
		t.Fatalf("unexpected status: got=%q want=done", got)
	}
	if !coerceBool(res["acceptance_gate_passed"], false) {
		t.Fatalf("expected acceptance_gate_passed=true")
	}

	graph := readTestPlanGraph(t, graphPath)
	for _, nodeID := range []string{"acceptance-cli", "acceptance-http", "acceptance-pw"} {
		node := findNodeByID(t, graph.Nodes, nodeID)
		if node.Status != contracts.FeatureNodeDone {
			t.Fatalf("node %s status mismatch: got=%s want=done", nodeID, node.Status)
		}
		if len(node.EvidenceRefs) == 0 {
			t.Fatalf("node %s missing evidence refs", nodeID)
		}
	}

	acceptancePath := filepath.Join(p.RepoRoot, ".dalek", "pm", "acceptance.md")
	acceptanceRaw, err := os.ReadFile(acceptancePath)
	if err != nil {
		t.Fatalf("read acceptance.md failed: %v", err)
	}
	if !strings.Contains(string(acceptanceRaw), "- status: done") {
		t.Fatalf("acceptance.md should contain done status")
	}

	statePath := filepath.Join(p.RepoRoot, ".dalek", "pm", "state.json")
	state := readJSONFileAsMap(t, statePath)
	status := nestedString(state, "feature", "acceptance", "status")
	if status != "done" {
		t.Fatalf("state acceptance status mismatch: got=%q want=done", status)
	}
}

func TestRunAcceptancePMOpExecutor_FailureCreatesGapTicketAndRollsBackFeatureStatus(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	graphPath := writeTestPlanGraph(t, p.RepoRoot, contracts.FeatureGraph{
		Schema:    contracts.PMFeatureGraphSchemaV1,
		FeatureID: "feature-acceptance-gap",
		Goal:      "acceptance failure flow",
		Nodes: []contracts.FeatureNode{
			{ID: "ticket-main", Type: contracts.FeatureNodeTicket, Status: contracts.FeatureNodeDone},
			{ID: "acceptance-01", Type: contracts.FeatureNodeAcceptance, Status: contracts.FeatureNodePending, DependsOn: []string{"ticket-main"}, DoneWhen: "must pass"},
		},
		UpdatedAt: time.Now().UTC(),
	})

	op := contracts.PMOp{
		Kind: contracts.PMOpRunAcceptance,
		Arguments: contracts.JSONMap{
			"cases": []any{
				map[string]any{
					"node_id": "acceptance-01",
					"type":    "cli",
					"command": "echo fail >&2; exit 1",
				},
			},
		},
	}

	res, err := runAcceptancePMOpExecutor{s: svc}.Execute(ctx, op)
	if err != nil {
		t.Fatalf("execute run_acceptance failed: %v", err)
	}
	if got := strings.TrimSpace(asString(res["status"])); got != "failed" {
		t.Fatalf("unexpected status: got=%q want=failed", got)
	}

	failureTicketID := uint(jsonMapInt(contracts.JSONMapFromAny(res), "failure_ticket_id"))
	if failureTicketID == 0 {
		t.Fatalf("expected failure_ticket_id > 0")
	}

	var ticket contracts.Ticket
	if err := p.DB.WithContext(ctx).First(&ticket, failureTicketID).Error; err != nil {
		t.Fatalf("load failure ticket failed: %v", err)
	}
	if strings.TrimSpace(ticket.Label) != "gap" {
		t.Fatalf("unexpected failure ticket label: got=%q want=gap", ticket.Label)
	}

	graph := readTestPlanGraph(t, graphPath)
	node := findNodeByID(t, graph.Nodes, "acceptance-01")
	if node.Status != contracts.FeatureNodeFailed {
		t.Fatalf("acceptance node status mismatch: got=%s want=failed", node.Status)
	}

	state := readJSONFileAsMap(t, filepath.Join(p.RepoRoot, ".dalek", "pm", "state.json"))
	currentStatus := nestedString(state, "runtime", "current_status")
	if currentStatus != "running" {
		t.Fatalf("runtime.current_status mismatch: got=%q want=running", currentStatus)
	}
}

func TestSetFeatureStatusPMOpExecutor_RequiresAcceptanceGateForDone(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	graphPath := writeTestPlanGraph(t, p.RepoRoot, contracts.FeatureGraph{
		Schema:    contracts.PMFeatureGraphSchemaV1,
		FeatureID: "feature-status-gate",
		Goal:      "set feature status gate",
		Nodes: []contracts.FeatureNode{
			{ID: "ticket-main", Type: contracts.FeatureNodeTicket, Status: contracts.FeatureNodeDone},
			{ID: "acceptance-01", Type: contracts.FeatureNodeAcceptance, Status: contracts.FeatureNodePending, DependsOn: []string{"ticket-main"}},
		},
		UpdatedAt: time.Now().UTC(),
	})

	_, err := setFeatureStatusPMOpExecutor{s: svc}.Execute(ctx, contracts.PMOp{
		Kind:      contracts.PMOpSetFeatureStatus,
		Arguments: contracts.JSONMap{"status": "done"},
	})
	if err == nil {
		t.Fatalf("expected done gate failure")
	}
	if !strings.Contains(err.Error(), "acceptance gate") {
		t.Fatalf("unexpected error: %v", err)
	}

	graph := readTestPlanGraph(t, graphPath)
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "acceptance-01" {
			graph.Nodes[i].Status = contracts.FeatureNodeDone
		}
	}
	writeTestPlanGraph(t, p.RepoRoot, graph)

	res, err := setFeatureStatusPMOpExecutor{s: svc}.Execute(ctx, contracts.PMOp{
		Kind:      contracts.PMOpSetFeatureStatus,
		Arguments: contracts.JSONMap{"status": "done"},
	})
	if err != nil {
		t.Fatalf("set_feature_status done failed: %v", err)
	}
	if got := strings.TrimSpace(asString(res["status"])); got != "done" {
		t.Fatalf("unexpected result status: got=%q want=done", got)
	}
	if !coerceBool(res["acceptance_gate_passed"], false) {
		t.Fatalf("expected acceptance_gate_passed=true")
	}

	state := readJSONFileAsMap(t, filepath.Join(p.RepoRoot, ".dalek", "pm", "state.json"))
	if got := nestedString(state, "runtime", "current_status"); got != "done" {
		t.Fatalf("runtime.current_status mismatch: got=%q want=done", got)
	}
}

func writeTestPlanGraph(t *testing.T, repoRoot string, graph contracts.FeatureGraph) string {
	t.Helper()
	graphPath := filepath.Join(repoRoot, ".dalek", "pm", "plan.json")
	if err := os.MkdirAll(filepath.Dir(graphPath), 0o755); err != nil {
		t.Fatalf("mkdir pm dir failed: %v", err)
	}
	if graph.UpdatedAt.IsZero() {
		graph.UpdatedAt = time.Now().UTC()
	}
	raw, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		t.Fatalf("marshal graph failed: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(graphPath, raw, 0o644); err != nil {
		t.Fatalf("write graph failed: %v", err)
	}
	return graphPath
}

func readTestPlanGraph(t *testing.T, path string) contracts.FeatureGraph {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read graph failed: %v", err)
	}
	var graph contracts.FeatureGraph
	if err := json.Unmarshal(raw, &graph); err != nil {
		t.Fatalf("unmarshal graph failed: %v", err)
	}
	return graph
}

func findNodeByID(t *testing.T, nodes []contracts.FeatureNode, id string) contracts.FeatureNode {
	t.Helper()
	for _, node := range nodes {
		if strings.TrimSpace(node.ID) == strings.TrimSpace(id) {
			return node
		}
	}
	t.Fatalf("node not found: %s", id)
	return contracts.FeatureNode{}
}

func readJSONFileAsMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json file failed(%s): %v", path, err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal json failed(%s): %v", path, err)
	}
	return out
}

func nestedString(root map[string]any, keys ...string) string {
	if len(keys) == 0 {
		return ""
	}
	current := any(root)
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		next, exists := m[key]
		if !exists {
			return ""
		}
		current = next
	}
	return strings.TrimSpace(fmt.Sprintf("%v", current))
}
