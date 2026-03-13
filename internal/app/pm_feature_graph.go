package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"dalek/internal/contracts"
)

const defaultPMPlanJSONRelPath = ".dalek/pm/plan.json"

func (p *Project) PMPlanJSONPath() string {
	root := strings.TrimSpace(p.RepoRoot())
	if root == "" {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(defaultPMPlanJSONRelPath))
}

func LoadPlanGraph(path string) (contracts.FeatureGraph, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return contracts.FeatureGraph{}, fmt.Errorf("plan graph path 为空")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return contracts.FeatureGraph{}, err
	}
	var graph contracts.FeatureGraph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return contracts.FeatureGraph{}, err
	}
	graph = normalizeFeatureGraph(graph, time.Now().UTC())
	if err := ValidateGraph(graph); err != nil {
		return contracts.FeatureGraph{}, err
	}
	return graph, nil
}

func SavePlanGraph(path string, graph contracts.FeatureGraph) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("plan graph path 为空")
	}
	graph = normalizeFeatureGraph(graph, time.Now().UTC())
	if err := ValidateGraph(graph); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return writePMWorkspaceFileAtomic(path, raw, 0o644)
}

func ValidateGraph(graph contracts.FeatureGraph) error {
	if strings.TrimSpace(graph.FeatureID) == "" {
		return fmt.Errorf("feature_id 不能为空")
	}
	if strings.TrimSpace(graph.Goal) == "" {
		return fmt.Errorf("goal 不能为空")
	}
	if len(graph.Nodes) == 0 {
		return fmt.Errorf("nodes 不能为空")
	}

	nodeByID := make(map[string]contracts.FeatureNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodeID := strings.TrimSpace(node.ID)
		if nodeID == "" {
			return fmt.Errorf("存在空 node id")
		}
		if _, exists := nodeByID[nodeID]; exists {
			return fmt.Errorf("node id 重复: %s", nodeID)
		}
		if !isValidFeatureNodeType(node.Type) {
			return fmt.Errorf("node %s type 无效: %s", nodeID, node.Type)
		}
		if !isValidFeatureNodeOwner(node.Owner) {
			return fmt.Errorf("node %s owner 无效: %s", nodeID, node.Owner)
		}
		if !isValidFeatureNodeStatus(node.Status) {
			return fmt.Errorf("node %s status 无效: %s", nodeID, node.Status)
		}
		if node.EstimatedSize != "" && !isValidFeatureNodeSize(node.EstimatedSize) {
			return fmt.Errorf("node %s estimated_size 无效: %s", nodeID, node.EstimatedSize)
		}
		nodeByID[nodeID] = node
	}

	for _, node := range graph.Nodes {
		for _, dep := range node.DependsOn {
			if _, ok := nodeByID[dep]; !ok {
				return fmt.Errorf("node %s depends_on 无效引用: %s", node.ID, dep)
			}
		}
	}
	for _, edge := range graph.Edges {
		if !isValidFeatureEdgeType(edge.Type) {
			return fmt.Errorf("edge type 无效: %s", edge.Type)
		}
		if strings.TrimSpace(edge.From) == "" || strings.TrimSpace(edge.To) == "" {
			return fmt.Errorf("edge from/to 不能为空")
		}
		if edge.From == edge.To {
			return fmt.Errorf("edge 不允许自环: %s", edge.From)
		}
		if _, ok := nodeByID[edge.From]; !ok {
			return fmt.Errorf("edge from 无效引用: %s", edge.From)
		}
		if _, ok := nodeByID[edge.To]; !ok {
			return fmt.Errorf("edge to 无效引用: %s", edge.To)
		}
	}
	if focus := strings.TrimSpace(graph.CurrentFocus); focus != "" {
		if _, ok := nodeByID[focus]; !ok {
			return fmt.Errorf("current_focus 无效引用: %s", focus)
		}
	}

	depMap := buildGraphDependencyMap(graph)
	if cycle := detectGraphCycle(depMap); len(cycle) > 0 {
		return fmt.Errorf("graph 存在循环依赖: %s", strings.Join(cycle, " -> "))
	}

	for _, node := range graph.Nodes {
		if node.Status != contracts.FeatureNodeDone {
			continue
		}
		for _, dep := range node.DependsOn {
			depNode := nodeByID[dep]
			if depNode.Status != contracts.FeatureNodeDone {
				return fmt.Errorf("node %s 为 done，但依赖 %s 状态为 %s", node.ID, dep, depNode.Status)
			}
		}
	}
	return nil
}

func MigrateLegacyPlan(planPath, statePath, acceptancePath string) (contracts.FeatureGraph, error) {
	plan := readPMPlanSnapshot(planPath)
	legacyState, hasLegacyState := loadLegacyPMWorkspaceStateForMigration(statePath)
	acceptance := readPMAcceptanceSnapshot(acceptancePath)

	featureTitle := normalizePMFeatureTitle(plan.CurrentFeature)
	if featureTitle == "" && hasLegacyState {
		featureTitle = normalizePMFeatureTitle(legacyState.Feature.Title)
	}
	if featureTitle == "" {
		featureTitle = "PM Feature Graph"
	}

	featureID := slugifyFeatureID(featureTitle)
	if featureID == "" {
		featureID = "feature-pm-graph"
	}

	docs := mergeLegacyFeatureDocs(plan.FeatureDocs, hasLegacyState, legacyState.Feature.Docs)
	tickets := mergeLegacyPlannedTickets(plan.PlannedTickets, hasLegacyState, legacyState.Feature.Tickets)
	acceptanceChecks := mergeLegacyAcceptanceChecks(plan.Acceptance, hasLegacyState, legacyState.Feature.Acceptance.RequiredChecks)
	if len(acceptanceChecks) == 0 {
		acceptanceChecks = []string{"完成真实场景验收并记录 evidence"}
	}

	repoRoot := inferRepoRootFromPMPath(planPath)
	requirementDoc, designDoc := splitRequirementAndDesignDocs(docs)
	requirementStatus := contracts.FeatureNodePending
	if requirementDoc.Path != "" && fileExists(resolveRepoPath(repoRoot, requirementDoc.Path)) {
		requirementStatus = contracts.FeatureNodeDone
	}
	designStatus := contracts.FeatureNodePending
	if designDoc.Path != "" && fileExists(resolveRepoPath(repoRoot, designDoc.Path)) {
		designStatus = contracts.FeatureNodeDone
	}

	nodes := make([]contracts.FeatureNode, 0, 4+len(tickets)+len(acceptanceChecks))
	nodes = append(nodes, contracts.FeatureNode{
		ID:            "requirement-main",
		Type:          contracts.FeatureNodeRequirement,
		Title:         "Requirement Baseline",
		Owner:         contracts.FeatureNodeOwnerPM,
		Status:        requirementStatus,
		DoneWhen:      "需求文档已完成并覆盖目标/范围/验收口径",
		TouchSurfaces: maybeDocPath(requirementDoc.Path),
		EstimatedSize: contracts.FeatureSizeM,
	})
	nodes = append(nodes, contracts.FeatureNode{
		ID:            "design-main",
		Type:          contracts.FeatureNodeDesign,
		Title:         "Design Baseline",
		Owner:         contracts.FeatureNodeOwnerPM,
		Status:        designStatus,
		DependsOn:     []string{"requirement-main"},
		DoneWhen:      "设计文档已完成并与需求对齐",
		TouchSurfaces: maybeDocPath(designDoc.Path),
		EstimatedSize: contracts.FeatureSizeM,
	})

	ticketNodeIDByTicketID := make(map[string]string, len(tickets))
	for _, ticket := range tickets {
		ticketID := strings.TrimSpace(ticket.ID)
		if ticketID == "" {
			continue
		}
		nodeID := normalizeGraphNodeID("ticket-" + ticketID)
		if nodeID == "" {
			nodeID = "ticket-" + strconv.Itoa(len(ticketNodeIDByTicketID)+1)
		}
		ticketNodeIDByTicketID[strings.ToLower(ticketID)] = nodeID
	}

	for _, ticket := range tickets {
		ticketID := strings.TrimSpace(ticket.ID)
		if ticketID == "" {
			continue
		}
		nodeID := ticketNodeIDByTicketID[strings.ToLower(ticketID)]
		deps := mapTicketDependsToNodeIDs(ticket.DependsOn, ticketNodeIDByTicketID)
		if len(deps) == 0 {
			deps = []string{"design-main"}
		}
		node := contracts.FeatureNode{
			ID:            nodeID,
			Type:          contracts.FeatureNodeTicket,
			Title:         firstNonEmpty(strings.TrimSpace(ticket.Deliverable), ticketID),
			Owner:         contracts.FeatureNodeOwnerWorker,
			Status:        mapLegacyTicketStatusToNodeStatus(ticket.Status),
			DependsOn:     deps,
			DoneWhen:      strings.TrimSpace(ticket.Deliverable),
			EstimatedSize: contracts.FeatureSizeM,
			TicketID:      ticketID,
		}
		if batch := strings.TrimSpace(ticket.Batch); batch != "" {
			node.Notes = "batch=" + batch
		}
		nodes = append(nodes, node)
	}

	acceptanceStatus := mapLegacyAcceptanceStatusToNodeStatus(acceptance.Status)
	ticketDependencySet := make([]string, 0, len(ticketNodeIDByTicketID))
	for _, nodeID := range ticketNodeIDByTicketID {
		ticketDependencySet = append(ticketDependencySet, nodeID)
	}
	sort.Strings(ticketDependencySet)
	if len(ticketDependencySet) == 0 {
		ticketDependencySet = []string{"design-main"}
	}
	evidenceRefs := []string{}
	if strings.TrimSpace(acceptancePath) != "" {
		evidenceRefs = append(evidenceRefs, strings.TrimSpace(acceptancePath))
	}
	for i, check := range acceptanceChecks {
		gateID := fmt.Sprintf("acceptance-%02d", i+1)
		nodes = append(nodes, contracts.FeatureNode{
			ID:            gateID,
			Type:          contracts.FeatureNodeAcceptance,
			Title:         fmt.Sprintf("Acceptance Gate %02d", i+1),
			Owner:         contracts.FeatureNodeOwnerPM,
			Status:        acceptanceStatus,
			DependsOn:     cloneStringSlice(ticketDependencySet),
			DoneWhen:      strings.TrimSpace(check),
			EvidenceRefs:  cloneStringSlice(evidenceRefs),
			EstimatedSize: contracts.FeatureSizeS,
		})
	}

	currentFocus := resolveLegacyCurrentFocus(plan, nodes, ticketNodeIDByTicketID)
	nextPMAction := strings.TrimSpace(plan.NextAction)
	if nextPMAction == "" && hasLegacyState {
		nextPMAction = strings.TrimSpace(legacyState.Runtime.NextAction)
	}
	if nextPMAction == "" {
		nextPMAction = defaultPMRuntimeNextAction
	}

	graph := contracts.FeatureGraph{
		Schema:       contracts.PMFeatureGraphSchemaV1,
		FeatureID:    featureID,
		Goal:         featureTitle,
		Docs:         docsToFeatureDocRefs(docs),
		Nodes:        nodes,
		CurrentFocus: currentFocus,
		NextPMAction: nextPMAction,
		UpdatedAt:    time.Now().UTC(),
	}
	graph.Edges = buildFeatureEdgesFromNodes(graph.Nodes)
	graph = normalizeFeatureGraph(graph, time.Now().UTC())
	if err := ValidateGraph(graph); err != nil {
		return contracts.FeatureGraph{}, err
	}
	return graph, nil
}

func RenderPlanMarkdown(graph contracts.FeatureGraph) string {
	graph = normalizeFeatureGraph(graph, time.Now().UTC())
	nodeByID := mapFeatureNodesByID(graph.Nodes)
	focus := selectGraphFocusNode(graph)

	currentPhase := defaultPMRuntimeCurrentPhase
	currentTicket := defaultPMRuntimeTicket
	currentStatus := defaultPMRuntimeStatus
	lastAction := defaultPMRuntimeLastAction
	blocker := defaultPMRuntimeBlocker
	if focus != nil {
		currentPhase = string(focus.Type)
		currentStatus = string(focus.Status)
		if ticketID := strings.TrimSpace(focus.TicketID); ticketID != "" {
			currentTicket = ticketID
		} else if focus.Type == contracts.FeatureNodeTicket || focus.Type == contracts.FeatureNodeIntegration {
			currentTicket = focus.ID
		}
	}
	if len(graph.Nodes) > 0 {
		lastAction = fmt.Sprintf("synced from plan.json @ %s", graph.UpdatedAt.UTC().Format(time.RFC3339))
	}
	if b := firstBlockedNode(graph); b != nil {
		blocker = firstNonEmpty(strings.TrimSpace(b.Title), b.ID)
	}
	nextAction := firstNonEmpty(strings.TrimSpace(graph.NextPMAction), defaultPMRuntimeNextAction)

	statusCounts := map[string]int{}
	for _, node := range graph.Nodes {
		statusCounts[string(node.Status)]++
	}

	orderedNodes := orderFeatureGraphNodes(graph)
	readyNodes := collectReadyNodes(graph)
	blockedNodes := collectBlockedNodes(graph)
	acceptanceNodes := collectGraphNodesByType(graph, contracts.FeatureNodeAcceptance)
	evidence := collectLatestEvidenceRefs(graph)

	var b strings.Builder
	fmt.Fprintln(&b, "# PM Plan (Rendered)")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "> Source of truth: `.dalek/pm/plan.json`")
	fmt.Fprintf(&b, "> Generated at: %s\n", graph.UpdatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintln(&b, "> This file is rendered from plan.json. Do not edit directly.")
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "## Current Feature: %s\n\n", firstNonEmpty(strings.TrimSpace(graph.Goal), strings.TrimSpace(graph.FeatureID), "none"))
	fmt.Fprintln(&b, "current_phase: "+currentPhase)
	fmt.Fprintln(&b, "current_ticket: "+currentTicket)
	fmt.Fprintln(&b, "current_status: "+currentStatus)
	fmt.Fprintln(&b, "last_action: "+lastAction)
	fmt.Fprintln(&b, "next_action: "+nextAction)
	fmt.Fprintln(&b, "blocker: "+blocker)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Goal")
	fmt.Fprintf(&b, "- feature_id: %s\n", firstNonEmpty(strings.TrimSpace(graph.FeatureID), "none"))
	fmt.Fprintf(&b, "- goal: %s\n", firstNonEmpty(strings.TrimSpace(graph.Goal), "none"))
	if len(graph.Docs) > 0 {
		for _, doc := range graph.Docs {
			fmt.Fprintf(&b, "- doc: %s (%s)\n", firstNonEmpty(strings.TrimSpace(doc.Path), "-"), firstNonEmpty(strings.TrimSpace(doc.Kind), "doc"))
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Current Status")
	fmt.Fprintf(&b, "- current_focus: %s\n", firstNonEmpty(strings.TrimSpace(graph.CurrentFocus), "none"))
	fmt.Fprintf(&b, "- next_pm_action: %s\n", nextAction)
	fmt.Fprintf(&b, "- node_counts: pending=%d in_progress=%d done=%d blocked=%d failed=%d\n",
		statusCounts[string(contracts.FeatureNodePending)],
		statusCounts[string(contracts.FeatureNodeInProgress)],
		statusCounts[string(contracts.FeatureNodeDone)],
		statusCounts[string(contracts.FeatureNodeBlocked)],
		statusCounts[string(contracts.FeatureNodeFailed)],
	)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Execution Graph")
	fmt.Fprintln(&b, "| id | type | owner | status | depends_on | title | done_when |")
	fmt.Fprintln(&b, "| --- | --- | --- | --- | --- | --- | --- |")
	for _, node := range orderedNodes {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdownCell(node.ID),
			escapeMarkdownCell(string(node.Type)),
			escapeMarkdownCell(string(node.Owner)),
			escapeMarkdownCell(string(node.Status)),
			escapeMarkdownCell(strings.Join(node.DependsOn, ",")),
			escapeMarkdownCell(node.Title),
			escapeMarkdownCell(node.DoneWhen),
		)
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Ready Nodes")
	if len(readyNodes) == 0 {
		fmt.Fprintln(&b, "- none")
	} else {
		for _, node := range readyNodes {
			fmt.Fprintf(&b, "- `%s` (%s): %s\n", node.ID, node.Type, firstNonEmpty(strings.TrimSpace(node.Title), "-"))
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Blocked Nodes")
	if len(blockedNodes) == 0 {
		fmt.Fprintln(&b, "- none")
	} else {
		for _, node := range blockedNodes {
			waitingOn := unresolvedDependencies(node, nodeByID)
			if len(waitingOn) == 0 {
				fmt.Fprintf(&b, "- `%s`: %s\n", node.ID, firstNonEmpty(strings.TrimSpace(node.Title), "-"))
				continue
			}
			fmt.Fprintf(&b, "- `%s`: %s (waiting_on: %s)\n", node.ID, firstNonEmpty(strings.TrimSpace(node.Title), "-"), strings.Join(waitingOn, ", "))
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Acceptance Gates")
	if len(acceptanceNodes) == 0 {
		fmt.Fprintln(&b, "- none")
	} else {
		for _, node := range acceptanceNodes {
			fmt.Fprintf(&b, "- `%s` [%s]: %s\n", node.ID, node.Status, firstNonEmpty(strings.TrimSpace(node.DoneWhen), strings.TrimSpace(node.Title), "-"))
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Latest Evidence")
	if len(evidence) == 0 {
		fmt.Fprintln(&b, "- none")
	} else {
		for _, ref := range evidence {
			fmt.Fprintf(&b, "- %s\n", ref)
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Next PM Action")
	fmt.Fprintf(&b, "- %s\n", nextAction)
	return strings.TrimSpace(b.String()) + "\n"
}

func SyncStateFromGraph(graph contracts.FeatureGraph, acceptance pmAcceptanceSnapshot, repoRoot, acceptancePath string) (PMWorkspaceRuntime, PMWorkspaceFeature) {
	graph = normalizeFeatureGraph(graph, time.Now().UTC())
	nodeByID := mapFeatureNodesByID(graph.Nodes)
	focus := selectGraphFocusNode(graph)

	runtime := PMWorkspaceRuntime{
		CurrentPhase:   defaultPMRuntimeCurrentPhase,
		CurrentTicket:  defaultPMRuntimeTicket,
		CurrentStatus:  defaultPMRuntimeStatus,
		LastAction:     defaultPMRuntimeLastAction,
		NextAction:     defaultPMRuntimeNextAction,
		Blocker:        defaultPMRuntimeBlocker,
		CurrentFeature: normalizePMFeatureTitle(graph.Goal),
	}
	if runtime.CurrentFeature == "" {
		runtime.CurrentFeature = normalizePMFeatureTitle(graph.FeatureID)
	}
	if focus != nil {
		runtime.CurrentPhase = string(focus.Type)
		runtime.CurrentStatus = string(focus.Status)
		if strings.TrimSpace(focus.TicketID) != "" {
			runtime.CurrentTicket = strings.TrimSpace(focus.TicketID)
		} else if focus.Type == contracts.FeatureNodeTicket || focus.Type == contracts.FeatureNodeIntegration {
			runtime.CurrentTicket = strings.TrimSpace(focus.ID)
		}
	}
	if strings.TrimSpace(graph.NextPMAction) != "" {
		runtime.NextAction = strings.TrimSpace(graph.NextPMAction)
	}
	if !graph.UpdatedAt.IsZero() {
		runtime.LastAction = "synced plan graph @ " + graph.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if blocked := firstBlockedNode(graph); blocked != nil {
		runtime.Blocker = firstNonEmpty(strings.TrimSpace(blocked.Title), blocked.ID)
	}

	feature := PMWorkspaceFeature{
		Title:      runtime.CurrentFeature,
		Docs:       resolvePMWorkspaceDocs(repoRoot, featureDocRefsToWorkspaceDocs(graph.Docs)),
		Tickets:    graphNodesToPlannedTickets(graph, nodeByID),
		Acceptance: PMWorkspaceAcceptance{},
	}
	if feature.Title == "" {
		feature.Title = firstNonEmpty(strings.TrimSpace(graph.Goal), strings.TrimSpace(graph.FeatureID))
	}
	if strings.TrimSpace(acceptancePath) == "" {
		acceptancePath = filepath.Join(repoRoot, filepath.FromSlash(defaultPMAcceptanceRelPath))
	}
	feature.Acceptance.Path = strings.TrimSpace(acceptancePath)
	feature.Acceptance.Exists = fileExists(feature.Acceptance.Path)
	feature.Acceptance.Status = normalizePMAcceptanceStatus(firstNonEmpty(strings.TrimSpace(acceptance.Status), deriveAcceptanceStatusFromGraph(graph)))
	feature.Acceptance.RequiredChecks = acceptanceChecksFromGraph(graph)
	feature.Acceptance.StartupCommand = strings.TrimSpace(acceptance.StartupCommand)
	feature.Acceptance.URL = strings.TrimSpace(acceptance.URL)
	feature.Acceptance.Steps = cloneStringSlice(acceptance.Steps)
	feature.Acceptance.Observations = cloneStringSlice(acceptance.Observations)
	feature.Acceptance.Conclusion = strings.TrimSpace(acceptance.Conclusion)

	return runtime, feature
}

func loadOrMigratePlanGraph(planJSONPath, planPath, statePath, acceptancePath string) (contracts.FeatureGraph, bool, error) {
	planJSONPath = strings.TrimSpace(planJSONPath)
	if planJSONPath == "" {
		return contracts.FeatureGraph{}, false, fmt.Errorf("plan graph path 为空")
	}
	if info, err := os.Stat(planJSONPath); err == nil && !info.IsDir() {
		graph, err := LoadPlanGraph(planJSONPath)
		return graph, false, err
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return contracts.FeatureGraph{}, false, err
	}

	graph, err := MigrateLegacyPlan(planPath, statePath, acceptancePath)
	if err != nil {
		return contracts.FeatureGraph{}, false, err
	}
	if err := SavePlanGraph(planJSONPath, graph); err != nil {
		return contracts.FeatureGraph{}, false, err
	}
	return graph, true, nil
}

func syncPlanMarkdownFromGraph(planPath string, graph contracts.FeatureGraph) error {
	planPath = strings.TrimSpace(planPath)
	if planPath == "" {
		return fmt.Errorf("plan path 为空")
	}
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		return err
	}
	return writePMWorkspaceFileAtomic(planPath, []byte(RenderPlanMarkdown(graph)), 0o644)
}

func normalizeFeatureGraph(graph contracts.FeatureGraph, now time.Time) contracts.FeatureGraph {
	graph.Schema = strings.TrimSpace(graph.Schema)
	if graph.Schema == "" {
		graph.Schema = contracts.PMFeatureGraphSchemaV1
	}
	graph.FeatureID = normalizeGraphNodeID(graph.FeatureID)
	graph.Goal = strings.TrimSpace(graph.Goal)
	graph.CurrentFocus = normalizeGraphNodeID(graph.CurrentFocus)
	graph.NextPMAction = strings.TrimSpace(graph.NextPMAction)

	docs := make([]contracts.FeatureDocRef, 0, len(graph.Docs))
	docSeen := map[string]struct{}{}
	for _, doc := range graph.Docs {
		doc.Kind = strings.TrimSpace(doc.Kind)
		doc.Path = strings.TrimSpace(doc.Path)
		if doc.Path == "" {
			continue
		}
		key := strings.ToLower(doc.Path + "|" + doc.Kind)
		if _, exists := docSeen[key]; exists {
			continue
		}
		docSeen[key] = struct{}{}
		docs = append(docs, doc)
	}
	graph.Docs = docs

	nodes := make([]contracts.FeatureNode, 0, len(graph.Nodes))
	nodeSeen := map[string]struct{}{}
	for _, node := range graph.Nodes {
		node.ID = normalizeGraphNodeID(node.ID)
		if node.ID == "" {
			continue
		}
		if _, exists := nodeSeen[node.ID]; exists {
			continue
		}
		nodeSeen[node.ID] = struct{}{}
		node.Type = normalizeFeatureNodeType(node.Type)
		node.Owner = normalizeFeatureNodeOwner(node.Owner)
		node.Status = normalizeFeatureNodeStatus(node.Status)
		node.Title = strings.TrimSpace(node.Title)
		if node.Title == "" {
			node.Title = node.ID
		}
		node.DoneWhen = strings.TrimSpace(node.DoneWhen)
		node.TicketID = strings.TrimSpace(node.TicketID)
		node.Notes = strings.TrimSpace(node.Notes)
		node.EstimatedSize = normalizeFeatureNodeSize(node.EstimatedSize)
		node.DependsOn = normalizeGraphNodeIDList(node.DependsOn)
		node.TouchSurfaces = normalizeStringList(node.TouchSurfaces)
		node.EvidenceRefs = normalizeStringList(node.EvidenceRefs)
		nodes = append(nodes, node)
	}
	graph.Nodes = nodes

	edges := make([]contracts.FeatureEdge, 0, len(graph.Edges))
	edgeSeen := map[string]struct{}{}
	for _, edge := range graph.Edges {
		edge.From = normalizeGraphNodeID(edge.From)
		edge.To = normalizeGraphNodeID(edge.To)
		if edge.From == "" || edge.To == "" || edge.From == edge.To {
			continue
		}
		edge.Type = normalizeFeatureEdgeType(edge.Type)
		key := strings.ToLower(edge.From + "|" + edge.To + "|" + string(edge.Type))
		if _, exists := edgeSeen[key]; exists {
			continue
		}
		edgeSeen[key] = struct{}{}
		edges = append(edges, edge)
	}
	graph.Edges = edges

	if graph.CurrentFocus == "" {
		if focus := selectGraphFocusNode(graph); focus != nil {
			graph.CurrentFocus = focus.ID
		}
	}
	if graph.NextPMAction == "" {
		graph.NextPMAction = defaultPMRuntimeNextAction
	}
	if graph.UpdatedAt.IsZero() {
		graph.UpdatedAt = now.UTC()
	}
	return graph
}

func normalizeFeatureNodeType(t contracts.FeatureNodeType) contracts.FeatureNodeType {
	switch strings.ToLower(strings.TrimSpace(string(t))) {
	case string(contracts.FeatureNodeRequirement):
		return contracts.FeatureNodeRequirement
	case string(contracts.FeatureNodeDesign):
		return contracts.FeatureNodeDesign
	case string(contracts.FeatureNodeTicket):
		return contracts.FeatureNodeTicket
	case string(contracts.FeatureNodeIntegration):
		return contracts.FeatureNodeIntegration
	case string(contracts.FeatureNodeAcceptance):
		return contracts.FeatureNodeAcceptance
	default:
		return contracts.FeatureNodeRequirement
	}
}

func normalizeFeatureNodeOwner(owner contracts.FeatureNodeOwner) contracts.FeatureNodeOwner {
	switch strings.ToLower(strings.TrimSpace(string(owner))) {
	case string(contracts.FeatureNodeOwnerPM):
		return contracts.FeatureNodeOwnerPM
	case string(contracts.FeatureNodeOwnerWorker), "dev", "developer":
		return contracts.FeatureNodeOwnerWorker
	case string(contracts.FeatureNodeOwnerUser), "human":
		return contracts.FeatureNodeOwnerUser
	case string(contracts.FeatureNodeOwnerSystem), "daemon":
		return contracts.FeatureNodeOwnerSystem
	default:
		return contracts.FeatureNodeOwnerPM
	}
}

func normalizeFeatureNodeStatus(status contracts.FeatureNodeStatus) contracts.FeatureNodeStatus {
	switch strings.ToLower(strings.TrimSpace(string(status))) {
	case "", "pending", "planned", "backlog", "ready", "queued":
		return contracts.FeatureNodePending
	case "in_progress", "in-progress", "inprogress", "running", "active", "planning":
		return contracts.FeatureNodeInProgress
	case "done", "completed", "merged", "success", "succeeded":
		return contracts.FeatureNodeDone
	case "blocked", "wait_user", "waiting", "on_hold", "hold":
		return contracts.FeatureNodeBlocked
	case "failed", "error":
		return contracts.FeatureNodeFailed
	default:
		return contracts.FeatureNodePending
	}
}

func normalizeFeatureNodeSize(size contracts.FeatureNodeSize) contracts.FeatureNodeSize {
	switch strings.ToLower(strings.TrimSpace(string(size))) {
	case string(contracts.FeatureSizeXS):
		return contracts.FeatureSizeXS
	case string(contracts.FeatureSizeS):
		return contracts.FeatureSizeS
	case string(contracts.FeatureSizeM):
		return contracts.FeatureSizeM
	case string(contracts.FeatureSizeL):
		return contracts.FeatureSizeL
	case string(contracts.FeatureSizeXL):
		return contracts.FeatureSizeXL
	default:
		return ""
	}
}

func normalizeFeatureEdgeType(t contracts.FeatureEdgeType) contracts.FeatureEdgeType {
	switch strings.ToLower(strings.TrimSpace(string(t))) {
	case string(contracts.FeatureEdgeDependsOn):
		return contracts.FeatureEdgeDependsOn
	case string(contracts.FeatureEdgeBlocks):
		return contracts.FeatureEdgeBlocks
	case string(contracts.FeatureEdgeValidates):
		return contracts.FeatureEdgeValidates
	default:
		return contracts.FeatureEdgeDependsOn
	}
}

func isValidFeatureNodeType(t contracts.FeatureNodeType) bool {
	switch t {
	case contracts.FeatureNodeRequirement, contracts.FeatureNodeDesign, contracts.FeatureNodeTicket, contracts.FeatureNodeIntegration, contracts.FeatureNodeAcceptance:
		return true
	default:
		return false
	}
}

func isValidFeatureNodeOwner(owner contracts.FeatureNodeOwner) bool {
	switch owner {
	case contracts.FeatureNodeOwnerPM, contracts.FeatureNodeOwnerWorker, contracts.FeatureNodeOwnerUser, contracts.FeatureNodeOwnerSystem:
		return true
	default:
		return false
	}
}

func isValidFeatureNodeStatus(status contracts.FeatureNodeStatus) bool {
	switch status {
	case contracts.FeatureNodePending, contracts.FeatureNodeInProgress, contracts.FeatureNodeDone, contracts.FeatureNodeBlocked, contracts.FeatureNodeFailed:
		return true
	default:
		return false
	}
}

func isValidFeatureNodeSize(size contracts.FeatureNodeSize) bool {
	switch size {
	case contracts.FeatureSizeXS, contracts.FeatureSizeS, contracts.FeatureSizeM, contracts.FeatureSizeL, contracts.FeatureSizeXL:
		return true
	default:
		return false
	}
}

func isValidFeatureEdgeType(t contracts.FeatureEdgeType) bool {
	switch t {
	case contracts.FeatureEdgeDependsOn, contracts.FeatureEdgeBlocks, contracts.FeatureEdgeValidates:
		return true
	default:
		return false
	}
}

func buildFeatureEdgesFromNodes(nodes []contracts.FeatureNode) []contracts.FeatureEdge {
	edges := make([]contracts.FeatureEdge, 0, len(nodes)*2)
	seen := map[string]struct{}{}
	appendEdge := func(from, to string, t contracts.FeatureEdgeType) {
		key := strings.ToLower(from + "|" + to + "|" + string(t))
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		edges = append(edges, contracts.FeatureEdge{
			From: from,
			To:   to,
			Type: t,
		})
	}
	for _, node := range nodes {
		for _, dep := range node.DependsOn {
			appendEdge(dep, node.ID, contracts.FeatureEdgeDependsOn)
			if node.Type == contracts.FeatureNodeAcceptance {
				appendEdge(dep, node.ID, contracts.FeatureEdgeValidates)
			}
		}
	}
	return edges
}

func buildGraphDependencyMap(graph contracts.FeatureGraph) map[string][]string {
	deps := make(map[string][]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		deps[node.ID] = append([]string{}, node.DependsOn...)
	}
	for _, edge := range graph.Edges {
		if edge.Type != contracts.FeatureEdgeDependsOn {
			continue
		}
		list := deps[edge.To]
		list = append(list, edge.From)
		deps[edge.To] = normalizeGraphNodeIDList(list)
	}
	return deps
}

func detectGraphCycle(depMap map[string][]string) []string {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := map[string]int{}
	stack := make([]string, 0, len(depMap))
	var cycle []string
	var visit func(string) bool
	visit = func(node string) bool {
		switch state[node] {
		case visiting:
			idx := -1
			for i := range stack {
				if stack[i] == node {
					idx = i
					break
				}
			}
			if idx >= 0 {
				cycle = append([]string{}, stack[idx:]...)
				cycle = append(cycle, node)
			} else {
				cycle = []string{node, node}
			}
			return true
		case visited:
			return false
		}
		state[node] = visiting
		stack = append(stack, node)
		next := append([]string{}, depMap[node]...)
		sort.Strings(next)
		for _, dep := range next {
			if visit(dep) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = visited
		return false
	}

	keys := make([]string, 0, len(depMap))
	for node := range depMap {
		keys = append(keys, node)
	}
	sort.Strings(keys)
	for _, node := range keys {
		if state[node] != unvisited {
			continue
		}
		if visit(node) {
			return cycle
		}
	}
	return nil
}

func mapFeatureNodesByID(nodes []contracts.FeatureNode) map[string]contracts.FeatureNode {
	out := make(map[string]contracts.FeatureNode, len(nodes))
	for _, node := range nodes {
		out[node.ID] = node
	}
	return out
}

func orderFeatureGraphNodes(graph contracts.FeatureGraph) []contracts.FeatureNode {
	if len(graph.Nodes) == 0 {
		return nil
	}
	nodeByID := mapFeatureNodesByID(graph.Nodes)
	inDegree := make(map[string]int, len(graph.Nodes))
	reverseEdges := make(map[string][]string, len(graph.Nodes))

	for _, node := range graph.Nodes {
		inDegree[node.ID] = 0
	}
	depMap := buildGraphDependencyMap(graph)
	for nodeID, deps := range depMap {
		for _, dep := range deps {
			if _, ok := inDegree[dep]; !ok {
				continue
			}
			inDegree[nodeID]++
			reverseEdges[dep] = append(reverseEdges[dep], nodeID)
		}
	}

	queue := make([]string, 0, len(graph.Nodes))
	for nodeID, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, nodeID)
		}
	}
	sort.Strings(queue)
	orderedIDs := make([]string, 0, len(graph.Nodes))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		orderedIDs = append(orderedIDs, current)
		nextNodes := append([]string{}, reverseEdges[current]...)
		sort.Strings(nextNodes)
		for _, next := range nextNodes {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
				sort.Strings(queue)
			}
		}
	}
	if len(orderedIDs) != len(graph.Nodes) {
		fallback := append([]contracts.FeatureNode{}, graph.Nodes...)
		sort.Slice(fallback, func(i, j int) bool { return fallback[i].ID < fallback[j].ID })
		return fallback
	}
	ordered := make([]contracts.FeatureNode, 0, len(orderedIDs))
	for _, nodeID := range orderedIDs {
		ordered = append(ordered, nodeByID[nodeID])
	}
	return ordered
}

func collectReadyNodes(graph contracts.FeatureGraph) []contracts.FeatureNode {
	if len(graph.Nodes) == 0 {
		return nil
	}
	nodeByID := mapFeatureNodesByID(graph.Nodes)
	ready := make([]contracts.FeatureNode, 0)
	for _, node := range orderFeatureGraphNodes(graph) {
		if node.Status != contracts.FeatureNodePending {
			continue
		}
		depsDone := true
		for _, dep := range node.DependsOn {
			depNode, ok := nodeByID[dep]
			if !ok || depNode.Status != contracts.FeatureNodeDone {
				depsDone = false
				break
			}
		}
		if depsDone {
			ready = append(ready, node)
		}
	}
	return ready
}

func collectBlockedNodes(graph contracts.FeatureGraph) []contracts.FeatureNode {
	if len(graph.Nodes) == 0 {
		return nil
	}
	nodeByID := mapFeatureNodesByID(graph.Nodes)
	blocked := make([]contracts.FeatureNode, 0)
	for _, node := range orderFeatureGraphNodes(graph) {
		if node.Status == contracts.FeatureNodeBlocked {
			blocked = append(blocked, node)
			continue
		}
		if node.Status != contracts.FeatureNodePending && node.Status != contracts.FeatureNodeInProgress {
			continue
		}
		if len(unresolvedDependencies(node, nodeByID)) > 0 {
			blocked = append(blocked, node)
		}
	}
	return blocked
}

func unresolvedDependencies(node contracts.FeatureNode, nodeByID map[string]contracts.FeatureNode) []string {
	if len(node.DependsOn) == 0 {
		return nil
	}
	waitingOn := make([]string, 0, len(node.DependsOn))
	for _, dep := range node.DependsOn {
		depNode, ok := nodeByID[dep]
		if !ok {
			waitingOn = append(waitingOn, dep)
			continue
		}
		if depNode.Status != contracts.FeatureNodeDone {
			waitingOn = append(waitingOn, dep)
		}
	}
	return waitingOn
}

func collectGraphNodesByType(graph contracts.FeatureGraph, nodeType contracts.FeatureNodeType) []contracts.FeatureNode {
	nodes := make([]contracts.FeatureNode, 0)
	for _, node := range orderFeatureGraphNodes(graph) {
		if node.Type == nodeType {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func collectLatestEvidenceRefs(graph contracts.FeatureGraph) []string {
	refs := make([]string, 0, 8)
	seen := map[string]struct{}{}
	for _, node := range orderFeatureGraphNodes(graph) {
		for _, ref := range node.EvidenceRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if _, exists := seen[ref]; exists {
				continue
			}
			seen[ref] = struct{}{}
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	if len(refs) > 12 {
		return refs[len(refs)-12:]
	}
	return refs
}

func selectGraphFocusNode(graph contracts.FeatureGraph) *contracts.FeatureNode {
	if len(graph.Nodes) == 0 {
		return nil
	}
	nodeByID := mapFeatureNodesByID(graph.Nodes)
	if focusID := strings.TrimSpace(graph.CurrentFocus); focusID != "" {
		if node, ok := nodeByID[focusID]; ok {
			node := node
			return &node
		}
	}
	ordered := orderFeatureGraphNodes(graph)
	for _, node := range ordered {
		if node.Status == contracts.FeatureNodeInProgress {
			node := node
			return &node
		}
	}
	for _, node := range ordered {
		if node.Status == contracts.FeatureNodeBlocked {
			node := node
			return &node
		}
	}
	for _, node := range ordered {
		if node.Status == contracts.FeatureNodePending {
			node := node
			return &node
		}
	}
	node := ordered[0]
	return &node
}

func firstBlockedNode(graph contracts.FeatureGraph) *contracts.FeatureNode {
	for _, node := range orderFeatureGraphNodes(graph) {
		if node.Status == contracts.FeatureNodeBlocked {
			node := node
			return &node
		}
	}
	return nil
}

func graphNodesToPlannedTickets(graph contracts.FeatureGraph, nodeByID map[string]contracts.FeatureNode) []PMWorkspacePlannedTicket {
	nodes := make([]contracts.FeatureNode, 0)
	for _, node := range orderFeatureGraphNodes(graph) {
		if node.Type != contracts.FeatureNodeTicket && node.Type != contracts.FeatureNodeIntegration {
			continue
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nil
	}

	out := make([]PMWorkspacePlannedTicket, 0, len(nodes))
	for _, node := range nodes {
		ticketID := strings.TrimSpace(node.TicketID)
		if ticketID == "" {
			ticketID = node.ID
		}
		deps := make([]string, 0, len(node.DependsOn))
		for _, dep := range node.DependsOn {
			depNode, ok := nodeByID[dep]
			if !ok {
				continue
			}
			if depNode.Type != contracts.FeatureNodeTicket && depNode.Type != contracts.FeatureNodeIntegration {
				continue
			}
			depTicketID := strings.TrimSpace(depNode.TicketID)
			if depTicketID == "" {
				depTicketID = depNode.ID
			}
			deps = append(deps, depTicketID)
		}
		out = append(out, PMWorkspacePlannedTicket{
			ID:          ticketID,
			Batch:       extractBatchFromNotes(node.Notes),
			Status:      mapNodeStatusToPlannedTicketStatus(node.Status),
			DependsOn:   normalizeStringList(deps),
			Deliverable: strings.TrimSpace(node.DoneWhen),
		})
	}
	return normalizePMPlannedTickets(out)
}

func acceptanceChecksFromGraph(graph contracts.FeatureGraph) []string {
	checks := make([]string, 0)
	for _, node := range orderFeatureGraphNodes(graph) {
		if node.Type != contracts.FeatureNodeAcceptance {
			continue
		}
		check := firstNonEmpty(strings.TrimSpace(node.DoneWhen), strings.TrimSpace(node.Title))
		if check != "" {
			checks = append(checks, check)
		}
	}
	return normalizeStringList(checks)
}

func deriveAcceptanceStatusFromGraph(graph contracts.FeatureGraph) string {
	acceptanceNodes := collectGraphNodesByType(graph, contracts.FeatureNodeAcceptance)
	if len(acceptanceNodes) == 0 {
		return defaultPMAcceptanceStatus
	}
	anyFailed := false
	anyBlocked := false
	anyRunning := false
	allDone := true
	for _, node := range acceptanceNodes {
		switch node.Status {
		case contracts.FeatureNodeFailed:
			anyFailed = true
			allDone = false
		case contracts.FeatureNodeBlocked:
			anyBlocked = true
			allDone = false
		case contracts.FeatureNodeInProgress:
			anyRunning = true
			allDone = false
		case contracts.FeatureNodeDone:
			// keep allDone=true
		default:
			allDone = false
		}
	}
	switch {
	case anyFailed:
		return "failed"
	case allDone:
		return "done"
	case anyRunning:
		return "running"
	case anyBlocked:
		return "blocked"
	default:
		return "pending"
	}
}

func mapNodeStatusToPlannedTicketStatus(status contracts.FeatureNodeStatus) string {
	switch status {
	case contracts.FeatureNodePending:
		return "planned"
	case contracts.FeatureNodeInProgress:
		return "in_progress"
	case contracts.FeatureNodeDone:
		return "done"
	case contracts.FeatureNodeBlocked:
		return "blocked"
	case contracts.FeatureNodeFailed:
		return "failed"
	default:
		return "planned"
	}
}

func mapLegacyTicketStatusToNodeStatus(status string) contracts.FeatureNodeStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "-", "none", "planned", "plan", "backlog", "ready", "queued":
		return contracts.FeatureNodePending
	case "in_progress", "in-progress", "running", "active":
		return contracts.FeatureNodeInProgress
	case "done", "completed", "merged":
		return contracts.FeatureNodeDone
	case "blocked", "wait_user", "waiting":
		return contracts.FeatureNodeBlocked
	case "failed", "error":
		return contracts.FeatureNodeFailed
	default:
		return contracts.FeatureNodePending
	}
}

func mapLegacyAcceptanceStatusToNodeStatus(status string) contracts.FeatureNodeStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "completed", "passed", "success":
		return contracts.FeatureNodeDone
	case "running", "in_progress", "in-progress":
		return contracts.FeatureNodeInProgress
	case "blocked":
		return contracts.FeatureNodeBlocked
	case "failed", "error":
		return contracts.FeatureNodeFailed
	default:
		return contracts.FeatureNodePending
	}
}

func mergeLegacyFeatureDocs(planDocs []PMWorkspaceDoc, hasState bool, stateDocs []PMWorkspaceDoc) []PMWorkspaceDoc {
	merged := normalizePMWorkspaceDocs(planDocs)
	seen := map[string]struct{}{}
	for _, doc := range merged {
		seen[strings.ToLower(doc.Path)] = struct{}{}
	}
	if hasState {
		for _, doc := range normalizePMWorkspaceDocs(stateDocs) {
			key := strings.ToLower(doc.Path)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, doc)
		}
	}
	return normalizePMWorkspaceDocs(merged)
}

func mergeLegacyPlannedTickets(planTickets []PMWorkspacePlannedTicket, hasState bool, stateTickets []PMWorkspacePlannedTicket) []PMWorkspacePlannedTicket {
	merged := normalizePMPlannedTickets(planTickets)
	seen := map[string]struct{}{}
	for _, tk := range merged {
		seen[strings.ToLower(tk.ID)] = struct{}{}
	}
	if hasState {
		for _, tk := range normalizePMPlannedTickets(stateTickets) {
			key := strings.ToLower(tk.ID)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, tk)
		}
	}
	return normalizePMPlannedTickets(merged)
}

func mergeLegacyAcceptanceChecks(planChecks []string, hasState bool, stateChecks []string) []string {
	merged := normalizeStringList(planChecks)
	seen := map[string]struct{}{}
	for _, check := range merged {
		seen[strings.ToLower(check)] = struct{}{}
	}
	if hasState {
		for _, check := range normalizeStringList(stateChecks) {
			key := strings.ToLower(check)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, check)
		}
	}
	return normalizeStringList(merged)
}

func loadLegacyPMWorkspaceStateForMigration(path string) (PMWorkspaceState, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return PMWorkspaceState{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return PMWorkspaceState{}, false
	}
	var state PMWorkspaceState
	if err := json.Unmarshal(raw, &state); err != nil {
		return PMWorkspaceState{}, false
	}
	return normalizePMWorkspaceState(state, path, "", "", ""), true
}

func inferRepoRootFromPMPath(planPath string) string {
	planPath = strings.TrimSpace(planPath)
	if planPath == "" {
		return ""
	}
	dir := filepath.Dir(planPath)
	if strings.EqualFold(filepath.Base(dir), "pm") && strings.EqualFold(filepath.Base(filepath.Dir(dir)), ".dalek") {
		return filepath.Dir(filepath.Dir(dir))
	}
	return filepath.Dir(dir)
}

func splitRequirementAndDesignDocs(docs []PMWorkspaceDoc) (PMWorkspaceDoc, PMWorkspaceDoc) {
	var requirement PMWorkspaceDoc
	var design PMWorkspaceDoc
	for _, doc := range docs {
		label := strings.ToLower(strings.TrimSpace(doc.Kind))
		path := strings.ToLower(strings.TrimSpace(doc.Path))
		if requirement.Path == "" && (strings.Contains(label, "需求") || strings.Contains(label, "require") || strings.Contains(path, "prd")) {
			requirement = doc
			continue
		}
		if design.Path == "" && (strings.Contains(label, "设计") || strings.Contains(label, "design")) {
			design = doc
			continue
		}
	}
	if requirement.Path == "" && len(docs) > 0 {
		requirement = docs[0]
	}
	if design.Path == "" && len(docs) > 1 {
		design = docs[1]
	}
	return requirement, design
}

func docsToFeatureDocRefs(docs []PMWorkspaceDoc) []contracts.FeatureDocRef {
	if len(docs) == 0 {
		return nil
	}
	out := make([]contracts.FeatureDocRef, 0, len(docs))
	for _, doc := range docs {
		out = append(out, contracts.FeatureDocRef{
			Kind: strings.TrimSpace(doc.Kind),
			Path: strings.TrimSpace(doc.Path),
		})
	}
	return out
}

func featureDocRefsToWorkspaceDocs(docs []contracts.FeatureDocRef) []PMWorkspaceDoc {
	if len(docs) == 0 {
		return nil
	}
	out := make([]PMWorkspaceDoc, 0, len(docs))
	for _, doc := range docs {
		if strings.TrimSpace(doc.Path) == "" {
			continue
		}
		out = append(out, PMWorkspaceDoc{
			Kind: strings.TrimSpace(doc.Kind),
			Path: strings.TrimSpace(doc.Path),
		})
	}
	return normalizePMWorkspaceDocs(out)
}

func mapTicketDependsToNodeIDs(dependsOn []string, ticketNodeIDByTicketID map[string]string) []string {
	if len(dependsOn) == 0 {
		return nil
	}
	out := make([]string, 0, len(dependsOn))
	for _, dep := range dependsOn {
		key := strings.ToLower(strings.TrimSpace(dep))
		if key == "" {
			continue
		}
		if nodeID, ok := ticketNodeIDByTicketID[key]; ok {
			out = append(out, nodeID)
			continue
		}
		out = append(out, normalizeGraphNodeID("ticket-"+dep))
	}
	return normalizeGraphNodeIDList(out)
}

func resolveLegacyCurrentFocus(plan pmPlanSnapshot, nodes []contracts.FeatureNode, ticketNodeIDByTicketID map[string]string) string {
	currentTicket := strings.ToLower(strings.TrimSpace(plan.CurrentTicket))
	if currentTicket != "" {
		if nodeID, ok := ticketNodeIDByTicketID[currentTicket]; ok {
			return nodeID
		}
	}
	if phase := strings.ToLower(strings.TrimSpace(plan.CurrentPhase)); phase != "" {
		for _, node := range nodes {
			if strings.EqualFold(string(node.Type), phase) {
				return node.ID
			}
		}
	}
	graph := contracts.FeatureGraph{Nodes: nodes}
	if focus := selectGraphFocusNode(graph); focus != nil {
		return focus.ID
	}
	return ""
}

func maybeDocPath(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return []string{path}
}

func slugifyFeatureID(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	if title == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range title {
		isLetter := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isLetter {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	featureID := strings.Trim(b.String(), "-")
	if featureID == "" {
		return ""
	}
	return "feature-" + featureID
}

func normalizeGraphNodeIDList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = normalizeGraphNodeID(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeGraphNodeID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
			lastDash = true
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	id := strings.Trim(b.String(), "-")
	return id
}

func normalizeStringList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extractBatchFromNotes(notes string) string {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return ""
	}
	lower := strings.ToLower(notes)
	if !strings.Contains(lower, "batch=") {
		return ""
	}
	idx := strings.Index(lower, "batch=")
	if idx < 0 {
		return ""
	}
	value := strings.TrimSpace(notes[idx+len("batch="):])
	if value == "" {
		return ""
	}
	if sep := strings.IndexAny(value, ",; "); sep >= 0 {
		value = value[:sep]
	}
	return strings.TrimSpace(value)
}

func escapeMarkdownCell(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = strings.ReplaceAll(raw, "|", "\\|")
	if raw == "" {
		return "-"
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
