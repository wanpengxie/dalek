package pm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/infra"
)

const (
	pmPlanJSONRelPath           = ".dalek/pm/plan.json"
	pmAcceptanceRelPath         = ".dalek/pm/acceptance.md"
	pmStateRelPath              = ".dalek/pm/state.json"
	pmEvidenceRelDir            = ".dalek/pm/evidence"
	defaultAcceptanceTimeoutSec = 120
	maxHTTPBodyBytes            = 2 << 20
)

// AcceptanceOp is a minimal op descriptor for the acceptance engine.
// It replaces the former contracts.PMOp after the planner loop removal.
type AcceptanceOp struct {
	Kind           string
	IdempotencyKey string
	Arguments      contracts.JSONMap
}

const (
	acceptanceOpKindRunAcceptance  = "run_acceptance"
	acceptanceOpKindSetFeatureStatus = "set_feature_status"
	acceptanceFeatureStatusDone    = "done"
)

type runAcceptancePMOpExecutor struct {
	s *Service
}

func (e runAcceptancePMOpExecutor) Reconcile(ctx context.Context, op AcceptanceOp) (bool, contracts.JSONMap, error) {
	// Journal-based idempotency removed; always return not-reconciled.
	return false, contracts.JSONMap{}, nil
}

func (e runAcceptancePMOpExecutor) Execute(ctx context.Context, op AcceptanceOp) (contracts.JSONMap, error) {
	if e.s == nil {
		return contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	return e.s.executeRunAcceptance(ctx, op)
}

type setFeatureStatusPMOpExecutor struct {
	s *Service
}

func (e setFeatureStatusPMOpExecutor) Reconcile(ctx context.Context, op AcceptanceOp) (bool, contracts.JSONMap, error) {
	if e.s == nil {
		return false, contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	status, err := parseDesiredFeatureStatus(op.Arguments)
	if err != nil {
		return false, contracts.JSONMap{}, err
	}
	current, err := e.s.readCurrentFeatureStatusFromState()
	if err != nil {
		return false, contracts.JSONMap{}, err
	}
	if current != status {
		return false, contracts.JSONMap{}, nil
	}
	if status == acceptanceFeatureStatusDone {
		gate, gerr := e.s.acceptanceGateState(ctx)
		if gerr != nil {
			return false, contracts.JSONMap{}, gerr
		}
		if !gate.Passed {
			return false, contracts.JSONMap{}, nil
		}
		return true, contracts.JSONMap{
			"status":                 status,
			"acceptance_gate_passed": true,
			"reconcile_source":       "state_file",
		}, nil
	}
	return true, contracts.JSONMap{
		"status":           status,
		"reconcile_source": "state_file",
	}, nil
}

func (e setFeatureStatusPMOpExecutor) Execute(ctx context.Context, op AcceptanceOp) (contracts.JSONMap, error) {
	if e.s == nil {
		return contracts.JSONMap{}, fmt.Errorf("pm service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	status, err := parseDesiredFeatureStatus(op.Arguments)
	if err != nil {
		return contracts.JSONMap{}, err
	}

	graph, graphPath, err := e.s.loadPMPlanGraph()
	if err != nil {
		return contracts.JSONMap{}, err
	}
	gate := evaluateAcceptanceGate(graph)
	if status == acceptanceFeatureStatusDone && !gate.Passed {
		return contracts.JSONMap{}, fmt.Errorf("acceptance gate 未通过：done=%d total=%d", gate.Done, gate.Total)
	}

	now := time.Now().UTC()
	summary := "set feature status"
	switch status {
	case "done":
		summary = "feature completed"
		graph.NextPMAction = "feature completed"
	case "verifying":
		summary = "feature verifying"
		graph.NextPMAction = "feature verifying"
	case "running":
		summary = "feature running"
		graph.NextPMAction = "feature running"
	default:
		graph.NextPMAction = "feature status=" + status
	}
	graph.UpdatedAt = now
	if err := savePMPlanGraph(graphPath, graph); err != nil {
		return contracts.JSONMap{}, err
	}
	if err := e.s.updatePMWorkspaceState(pmWorkspaceStateUpdate{
		FeatureStatus: status,
		LastAction:    summary,
	}); err != nil {
		return contracts.JSONMap{}, err
	}

	return contracts.JSONMap{
		"status":                 status,
		"acceptance_gate_passed": gate.Passed,
		"acceptance_total":       gate.Total,
		"acceptance_done":        gate.Done,
		"plan_json":              graphPath,
	}, nil
}

type acceptanceRunSpec struct {
	FeatureID                 string
	StartupCommand            string
	URL                       string
	NodeIDs                   []string
	CaseByNodeID              map[string]acceptanceCaseSpec
	AutoCreateFailureTicket   bool
	AutoDispatchFailureTicket bool
	FailureTicketLabel        string
	FailureTicketTitlePrefix  string
	FeatureStatusOnSuccess    string
	FeatureStatusOnFailure    string
}

type acceptanceCaseSpec struct {
	NodeID      string
	Name        string
	Description string
	Type        string
	TimeoutSec  int
	Command     string
	Args        []string
	URL         string
	Method      string
	Headers     map[string]string
	Body        string
	Expect      acceptanceExpectSpec
	Steps       []acceptanceStepSpec
}

type acceptanceStepSpec struct {
	Name              string
	Type              string
	TimeoutSec        int
	Command           string
	Args              []string
	URL               string
	Method            string
	Headers           map[string]string
	Body              string
	CaptureSnapshot   bool
	CaptureScreenshot bool
	Expect            acceptanceExpectSpec
}

type acceptanceExpectSpec struct {
	ExitCode          *int
	StatusCode        *int
	StdoutContains    []string
	StdoutNotContains []string
	StderrContains    []string
	BodyContains      []string
}

type acceptanceNodeRunResult struct {
	NodeID        string
	NodeTitle     string
	CaseName      string
	CaseType      string
	Status        contracts.FeatureNodeStatus
	Summary       string
	StartedAt     time.Time
	FinishedAt    time.Time
	EvidenceDir   string
	EvidenceRefs  []string
	FailureReason string
	Steps         []acceptanceStepResult
}

type acceptanceStepResult struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Status         string   `json:"status"`
	Command        string   `json:"command,omitempty"`
	URL            string   `json:"url,omitempty"`
	Method         string   `json:"method,omitempty"`
	ExitCode       *int     `json:"exit_code,omitempty"`
	HTTPStatus     *int     `json:"http_status,omitempty"`
	DurationMS     int64    `json:"duration_ms"`
	StdoutPath     string   `json:"stdout_path,omitempty"`
	StderrPath     string   `json:"stderr_path,omitempty"`
	BodyPath       string   `json:"body_path,omitempty"`
	ArtifactRefs   []string `json:"artifact_refs,omitempty"`
	SnapshotPath   string   `json:"snapshot_path,omitempty"`
	ScreenshotPath string   `json:"screenshot_path,omitempty"`
	Error          string   `json:"error,omitempty"`
}

type acceptanceBlockedError struct {
	reason string
}

func (e acceptanceBlockedError) Error() string {
	return strings.TrimSpace(e.reason)
}

type acceptanceEvidenceBundle struct {
	FeatureID   string                 `json:"feature_id"`
	FeatureGoal string                 `json:"feature_goal"`
	NodeID      string                 `json:"node_id"`
	NodeTitle   string                 `json:"node_title"`
	CaseName    string                 `json:"case_name"`
	CaseType    string                 `json:"case_type"`
	Status      string                 `json:"status"`
	Summary     string                 `json:"summary"`
	StartedAt   string                 `json:"started_at"`
	FinishedAt  string                 `json:"finished_at"`
	DurationMS  int64                  `json:"duration_ms"`
	Steps       []acceptanceStepResult `json:"steps"`
}

type acceptanceGateSummary struct {
	Total   int
	Done    int
	Failed  int
	Blocked int
	Pending int
	Passed  bool
}

type pmWorkspaceStateUpdate struct {
	AcceptanceStatus   string
	AcceptancePath     string
	RequiredChecks     []string
	StartupCommand     string
	URL                string
	Steps              []string
	Observations       []string
	Conclusion         string
	FeatureStatus      string
	LastAction         string
	FeatureTitle       string
	AcceptanceEvidence []string
}

func (s *Service) executeRunAcceptance(ctx context.Context, op AcceptanceOp) (contracts.JSONMap, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	spec, err := parseAcceptanceRunSpec(op.Arguments)
	if err != nil {
		return contracts.JSONMap{}, err
	}
	graph, graphPath, err := s.loadPMPlanGraph()
	if err != nil {
		return contracts.JSONMap{}, err
	}

	nodes, err := selectAcceptanceNodes(graph, spec.NodeIDs)
	if err != nil {
		return contracts.JSONMap{}, err
	}
	if len(nodes) == 0 {
		gate := evaluateAcceptanceGate(graph)
		return contracts.JSONMap{
			"status":                 "noop",
			"reason":                 "no acceptance nodes",
			"acceptance_gate_passed": gate.Passed,
		}, nil
	}

	now := time.Now().UTC()
	nodeByID := indexFeatureNodesByID(graph.Nodes)
	results := make([]acceptanceNodeRunResult, 0, len(nodes))
	resultByNode := make(map[string]acceptanceNodeRunResult, len(nodes))

	for _, node := range nodes {
		res := acceptanceNodeRunResult{
			NodeID:    node.ID,
			NodeTitle: node.Title,
			StartedAt: time.Now().UTC(),
		}
		if deps := unresolvedNodeDependencies(node, nodeByID); len(deps) > 0 {
			res.Status = contracts.FeatureNodeBlocked
			res.Summary = "dependencies not done: " + strings.Join(deps, ", ")
			res.FailureReason = res.Summary
			res.FinishedAt = time.Now().UTC()
			results = append(results, res)
			resultByNode[node.ID] = res
			continue
		}

		caseSpec, ok := resolveAcceptanceCaseSpec(spec, node)
		if !ok {
			res.Status = contracts.FeatureNodeBlocked
			res.Summary = "missing runnable acceptance case spec"
			res.FailureReason = res.Summary
			res.FinishedAt = time.Now().UTC()
			results = append(results, res)
			resultByNode[node.ID] = res
			continue
		}

		caseRes, runErr := s.runAcceptanceCase(ctx, graph, node, caseSpec)
		res = caseRes
		if runErr != nil {
			res.Status = contracts.FeatureNodeFailed
			res.FailureReason = strings.TrimSpace(runErr.Error())
			if res.Summary == "" {
				res.Summary = res.FailureReason
			}
		}
		results = append(results, res)
		resultByNode[node.ID] = res
	}

	for i := range graph.Nodes {
		node := graph.Nodes[i]
		if node.Type != contracts.FeatureNodeAcceptance {
			continue
		}
		if update, ok := resultByNode[node.ID]; ok {
			node.Status = update.Status
			node.EvidenceRefs = appendUniqueStrings(node.EvidenceRefs, update.EvidenceRefs)
			graph.Nodes[i] = node
		}
	}
	graph.UpdatedAt = time.Now().UTC()
	if err := savePMPlanGraph(graphPath, graph); err != nil {
		return contracts.JSONMap{}, err
	}

	gate := evaluateAcceptanceGate(graph)
	overallStatus := deriveAcceptanceStatusFromGate(gate)

	failed := collectFailedAcceptanceResults(results)
	featureStatus := strings.TrimSpace(spec.FeatureStatusOnSuccess)
	if len(failed) > 0 {
		featureStatus = firstNonEmpty(strings.TrimSpace(spec.FeatureStatusOnFailure), "running")
	}

	failureTicketID := uint(0)
	failureTicketWorkerID := uint(0)
	if len(failed) > 0 && spec.AutoCreateFailureTicket {
		ticket, workerID, ferr := s.createAcceptanceFailureTicket(ctx, graph, failed, spec)
		if ferr != nil {
			return contracts.JSONMap{}, ferr
		}
		failureTicketID = ticket.ID
		failureTicketWorkerID = workerID
	}

	acceptancePath := filepath.Join(strings.TrimSpace(s.p.RepoRoot), filepath.FromSlash(pmAcceptanceRelPath))
	requiredChecks := acceptanceChecksFromNodes(graph.Nodes)
	observations := buildAcceptanceObservations(results)
	conclusion := buildAcceptanceConclusion(overallStatus, results, failureTicketID)
	steps := buildAcceptanceSteps(requiredChecks, results)
	if err := writeAcceptanceMarkdown(acceptancePath, graph, pmWorkspaceStateUpdate{
		AcceptanceStatus:   overallStatus,
		AcceptancePath:     acceptancePath,
		RequiredChecks:     requiredChecks,
		StartupCommand:     spec.StartupCommand,
		URL:                spec.URL,
		Steps:              steps,
		Observations:       observations,
		Conclusion:         conclusion,
		FeatureStatus:      featureStatus,
		FeatureTitle:       graph.Goal,
		AcceptanceEvidence: collectEvidenceRefs(results),
	}, now, results, failureTicketID); err != nil {
		return contracts.JSONMap{}, err
	}

	if err := s.updatePMWorkspaceState(pmWorkspaceStateUpdate{
		AcceptanceStatus:   overallStatus,
		AcceptancePath:     acceptancePath,
		RequiredChecks:     requiredChecks,
		StartupCommand:     spec.StartupCommand,
		URL:                spec.URL,
		Steps:              steps,
		Observations:       observations,
		Conclusion:         conclusion,
		FeatureStatus:      featureStatus,
		LastAction:         "run acceptance",
		FeatureTitle:       graph.Goal,
		AcceptanceEvidence: collectEvidenceRefs(results),
	}); err != nil {
		return contracts.JSONMap{}, err
	}

	statusCounts := map[string]int{}
	resultRows := make([]contracts.JSONMap, 0, len(results))
	for _, res := range results {
		statusCounts[string(res.Status)]++
		resultRows = append(resultRows, contracts.JSONMap{
			"node_id":          res.NodeID,
			"node_title":       res.NodeTitle,
			"case_name":        res.CaseName,
			"case_type":        res.CaseType,
			"status":           string(res.Status),
			"summary":          res.Summary,
			"failure_reason":   res.FailureReason,
			"evidence_refs":    append([]string{}, res.EvidenceRefs...),
			"started_at":       res.StartedAt.UTC().Format(time.RFC3339),
			"finished_at":      res.FinishedAt.UTC().Format(time.RFC3339),
			"evidence_dir":     res.EvidenceDir,
			"step_result_size": len(res.Steps),
		})
	}

	return contracts.JSONMap{
		"status":                    overallStatus,
		"feature_id":                firstNonEmpty(strings.TrimSpace(spec.FeatureID), strings.TrimSpace(graph.FeatureID)),
		"feature_goal":              strings.TrimSpace(graph.Goal),
		"plan_json":                 graphPath,
		"acceptance_path":           acceptancePath,
		"nodes_total":               len(nodes),
		"results":                   resultRows,
		"status_counts":             statusCounts,
		"acceptance_gate_passed":    gate.Passed,
		"acceptance_gate_total":     gate.Total,
		"acceptance_gate_done":      gate.Done,
		"failure_ticket_id":         failureTicketID,
		"failure_ticket_worker_id":  failureTicketWorkerID,
		"failure_ticket_started":    failureTicketWorkerID != 0,
		"feature_status":            featureStatus,
		"failure_ticket_dispatched": false,
	}, nil
}

func parseAcceptanceRunSpec(args contracts.JSONMap) (acceptanceRunSpec, error) {
	args = contracts.JSONMapFromAny(args)
	spec := acceptanceRunSpec{
		FeatureID:                 strings.TrimSpace(jsonMapString(args, "feature_id")),
		StartupCommand:            strings.TrimSpace(jsonMapString(args, "startup_command")),
		URL:                       strings.TrimSpace(jsonMapString(args, "url")),
		NodeIDs:                   parseStringSliceArg(args["node_ids"]),
		CaseByNodeID:              map[string]acceptanceCaseSpec{},
		AutoCreateFailureTicket:   true,
		AutoDispatchFailureTicket: jsonMapBool(args, "auto_dispatch_failure_ticket"),
		FailureTicketLabel:        firstNonEmpty(strings.TrimSpace(jsonMapString(args, "failure_ticket_label")), "gap"),
		FailureTicketTitlePrefix:  firstNonEmpty(strings.TrimSpace(jsonMapString(args, "failure_ticket_title_prefix")), "acceptance gap"),
		FeatureStatusOnSuccess:    strings.TrimSpace(jsonMapString(args, "feature_status_on_success")),
		FeatureStatusOnFailure:    strings.TrimSpace(jsonMapString(args, "feature_status_on_failure")),
	}
	if v, ok := args["auto_create_failure_ticket"]; ok {
		spec.AutoCreateFailureTicket = coerceBool(v, true)
	}
	for _, raw := range parseCaseSpecList(args["cases"]) {
		caseSpec := normalizeAcceptanceCaseSpec(raw)
		if strings.TrimSpace(caseSpec.NodeID) == "" {
			continue
		}
		spec.CaseByNodeID[strings.TrimSpace(caseSpec.NodeID)] = caseSpec
	}

	if len(spec.CaseByNodeID) == 0 {
		fallback := normalizeAcceptanceCaseSpec(contracts.JSONMapFromAny(args))
		if strings.TrimSpace(fallback.NodeID) != "" {
			spec.CaseByNodeID[fallback.NodeID] = fallback
		}
	}
	if spec.FeatureStatusOnSuccess != "" {
		spec.FeatureStatusOnSuccess = normalizeFeatureStatusValue(spec.FeatureStatusOnSuccess)
	}
	if spec.FeatureStatusOnFailure != "" {
		spec.FeatureStatusOnFailure = normalizeFeatureStatusValue(spec.FeatureStatusOnFailure)
	}
	return spec, nil
}

func parseCaseSpecList(v any) []contracts.JSONMap {
	switch t := v.(type) {
	case nil:
		return nil
	case []contracts.JSONMap:
		return append([]contracts.JSONMap{}, t...)
	case []any:
		out := make([]contracts.JSONMap, 0, len(t))
		for _, item := range t {
			out = append(out, contracts.JSONMapFromAny(item))
		}
		return out
	default:
		single := contracts.JSONMapFromAny(t)
		if len(single) == 0 {
			return nil
		}
		return []contracts.JSONMap{single}
	}
}

func normalizeAcceptanceCaseSpec(raw contracts.JSONMap) acceptanceCaseSpec {
	raw = contracts.JSONMapFromAny(raw)
	spec := acceptanceCaseSpec{
		NodeID:      strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "node_id"), jsonMapString(raw, "gate_id"), jsonMapString(raw, "id"))),
		Name:        strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "name"), jsonMapString(raw, "title"))),
		Description: strings.TrimSpace(jsonMapString(raw, "description")),
		Type:        normalizeAcceptanceCaseType(firstNonEmpty(jsonMapString(raw, "type"), "cli")),
		TimeoutSec:  coercePositiveInt(raw["timeout_sec"], defaultAcceptanceTimeoutSec),
		Command:     strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "command"), jsonMapString(raw, "cmd"))),
		Args:        parseStringSliceArg(raw["args"]),
		URL:         strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "url"), jsonMapString(raw, "endpoint"))),
		Method:      strings.ToUpper(strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "method"), "GET"))),
		Headers:     parseStringMapArg(raw["headers"]),
		Body:        strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "body"), jsonMapString(raw, "payload"))),
		Expect:      parseAcceptanceExpect(raw),
		Steps:       parseAcceptanceSteps(raw["steps"]),
	}
	captureSnapshot := coerceBool(raw["capture_snapshot"], false)
	captureScreenshot := coerceBool(raw["capture_screenshot"], false)
	if spec.Method == "" {
		spec.Method = "GET"
	}
	if len(spec.Steps) == 0 {
		spec.Steps = []acceptanceStepSpec{{
			Name:              firstNonEmpty(spec.Name, "step-1"),
			Type:              spec.Type,
			TimeoutSec:        spec.TimeoutSec,
			Command:           spec.Command,
			Args:              append([]string{}, spec.Args...),
			URL:               spec.URL,
			Method:            spec.Method,
			Headers:           cloneStringMap(spec.Headers),
			Body:              spec.Body,
			CaptureSnapshot:   captureSnapshot,
			CaptureScreenshot: captureScreenshot,
			Expect:            spec.Expect,
		}}
	}
	if spec.Name == "" {
		spec.Name = firstNonEmpty(spec.NodeID, "acceptance")
	}
	return spec
}

func parseAcceptanceSteps(v any) []acceptanceStepSpec {
	list := parseCaseSpecList(v)
	if len(list) == 0 {
		return nil
	}
	out := make([]acceptanceStepSpec, 0, len(list))
	for idx, raw := range list {
		raw = contracts.JSONMapFromAny(raw)
		s := acceptanceStepSpec{
			Name:              strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "name"), fmt.Sprintf("step-%d", idx+1))),
			Type:              normalizeAcceptanceCaseType(firstNonEmpty(jsonMapString(raw, "type"), "cli")),
			TimeoutSec:        coercePositiveInt(raw["timeout_sec"], defaultAcceptanceTimeoutSec),
			Command:           strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "command"), jsonMapString(raw, "cmd"))),
			Args:              parseStringSliceArg(raw["args"]),
			URL:               strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "url"), jsonMapString(raw, "endpoint"))),
			Method:            strings.ToUpper(strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "method"), "GET"))),
			Headers:           parseStringMapArg(raw["headers"]),
			Body:              strings.TrimSpace(firstNonEmpty(jsonMapString(raw, "body"), jsonMapString(raw, "payload"))),
			CaptureSnapshot:   coerceBool(raw["capture_snapshot"], false),
			CaptureScreenshot: coerceBool(raw["capture_screenshot"], false),
			Expect:            parseAcceptanceExpect(raw),
		}
		if s.Method == "" {
			s.Method = "GET"
		}
		out = append(out, s)
	}
	return out
}

func parseAcceptanceExpect(raw contracts.JSONMap) acceptanceExpectSpec {
	expectRaw := contracts.JSONMapFromAny(raw["expect"])
	if len(expectRaw) == 0 {
		expectRaw = contracts.JSONMap{}
	}
	pick := func(key string) any {
		if v, ok := expectRaw[key]; ok {
			return v
		}
		return raw[key]
	}
	exitCode := coerceOptionalInt(pick("exit_code"))
	if exitCode == nil {
		exitCode = coerceOptionalInt(pick("expect_exit_code"))
	}
	statusCode := coerceOptionalInt(pick("status"))
	if statusCode == nil {
		statusCode = coerceOptionalInt(pick("expect_status"))
	}
	stdoutContains := parseStringSliceArg(firstNonNil(
		pick("stdout_contains"),
		pick("expect_stdout_contains"),
	))
	stdoutNotContains := parseStringSliceArg(firstNonNil(
		pick("stdout_not_contains"),
		pick("expect_stdout_not_contains"),
	))
	stderrContains := parseStringSliceArg(firstNonNil(
		pick("stderr_contains"),
		pick("expect_stderr_contains"),
	))
	bodyContains := parseStringSliceArg(firstNonNil(
		pick("body_contains"),
		pick("expect_body_contains"),
	))
	return acceptanceExpectSpec{
		ExitCode:          exitCode,
		StatusCode:        statusCode,
		StdoutContains:    stdoutContains,
		StdoutNotContains: stdoutNotContains,
		StderrContains:    stderrContains,
		BodyContains:      bodyContains,
	}
}

func resolveAcceptanceCaseSpec(spec acceptanceRunSpec, node contracts.FeatureNode) (acceptanceCaseSpec, bool) {
	nodeID := strings.TrimSpace(node.ID)
	if nodeID == "" {
		return acceptanceCaseSpec{}, false
	}
	if c, ok := spec.CaseByNodeID[nodeID]; ok {
		return c, true
	}
	if inferred, ok := inferAcceptanceCaseSpecFromNode(node); ok {
		return inferred, true
	}
	return acceptanceCaseSpec{}, false
}

func inferAcceptanceCaseSpecFromNode(node contracts.FeatureNode) (acceptanceCaseSpec, bool) {
	nodeID := strings.TrimSpace(node.ID)
	notes := strings.TrimSpace(node.Notes)
	if notes != "" {
		parsed := contracts.JSONMapFromAny(notes)
		if len(parsed) > 0 {
			if nested := contracts.JSONMapFromAny(parsed["acceptance"]); len(nested) > 0 {
				parsed = nested
			}
			parsed["node_id"] = nodeID
			c := normalizeAcceptanceCaseSpec(parsed)
			if c.NodeID != "" {
				return c, true
			}
		}
	}
	doneWhen := strings.TrimSpace(node.DoneWhen)
	if doneWhen == "" {
		return acceptanceCaseSpec{}, false
	}
	if cmd := extractBacktickCommand(doneWhen); cmd != "" {
		return normalizeAcceptanceCaseSpec(contracts.JSONMap{
			"node_id": nodeID,
			"name":    firstNonEmpty(strings.TrimSpace(node.Title), nodeID),
			"type":    "cli",
			"command": cmd,
		}), true
	}
	if strings.HasPrefix(strings.ToLower(doneWhen), "http://") || strings.HasPrefix(strings.ToLower(doneWhen), "https://") {
		return normalizeAcceptanceCaseSpec(contracts.JSONMap{
			"node_id": nodeID,
			"name":    firstNonEmpty(strings.TrimSpace(node.Title), nodeID),
			"type":    "http",
			"url":     doneWhen,
		}), true
	}
	if strings.Contains(strings.ToLower(doneWhen), "pw ") {
		return normalizeAcceptanceCaseSpec(contracts.JSONMap{
			"node_id": nodeID,
			"name":    firstNonEmpty(strings.TrimSpace(node.Title), nodeID),
			"type":    "pw",
			"command": doneWhen,
		}), true
	}
	return acceptanceCaseSpec{}, false
}

func extractBacktickCommand(s string) string {
	start := strings.IndexByte(s, '`')
	if start < 0 {
		return ""
	}
	rest := s[start+1:]
	end := strings.IndexByte(rest, '`')
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func (s *Service) runAcceptanceCase(ctx context.Context, graph contracts.FeatureGraph, node contracts.FeatureNode, caseSpec acceptanceCaseSpec) (acceptanceNodeRunResult, error) {
	start := time.Now().UTC()
	res := acceptanceNodeRunResult{
		NodeID:    strings.TrimSpace(node.ID),
		NodeTitle: strings.TrimSpace(node.Title),
		CaseName:  firstNonEmpty(strings.TrimSpace(caseSpec.Name), strings.TrimSpace(node.Title), strings.TrimSpace(node.ID)),
		CaseType:  normalizeAcceptanceCaseType(caseSpec.Type),
		StartedAt: start,
		Status:    contracts.FeatureNodeInProgress,
	}
	repoRoot := strings.TrimSpace(s.p.RepoRoot)
	if repoRoot == "" {
		return res, fmt.Errorf("repo_root 为空")
	}

	stamp := start.Format("20060102T150405.000Z")
	evidenceDir := filepath.Join(repoRoot, filepath.FromSlash(pmEvidenceRelDir), normalizePathSlug(firstNonEmpty(graph.FeatureID, "feature")), normalizePathSlug(res.NodeID)+"-"+normalizePathSlug(stamp))
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return res, err
	}
	res.EvidenceDir = evidenceDir
	res.EvidenceRefs = append(res.EvidenceRefs, evidenceDir)

	stepResults := make([]acceptanceStepResult, 0, len(caseSpec.Steps))
	for idx, step := range caseSpec.Steps {
		stepResult, err := s.executeAcceptanceStep(ctx, repoRoot, evidenceDir, idx+1, caseSpec, step)
		stepResults = append(stepResults, stepResult)
		if err != nil {
			res.Steps = stepResults
			if isAcceptanceBlockedError(err) {
				res.Status = contracts.FeatureNodeBlocked
			} else {
				res.Status = contracts.FeatureNodeFailed
			}
			res.FailureReason = strings.TrimSpace(err.Error())
			res.Summary = firstNonEmpty(stepResult.Error, res.FailureReason)
			res.FinishedAt = time.Now().UTC()
			res.EvidenceRefs = appendUniqueStrings(res.EvidenceRefs, stepResult.ArtifactRefs)
			if err := writeAcceptanceBundle(evidenceDir, graph, res); err != nil {
				return res, err
			}
			return res, nil
		}
		res.EvidenceRefs = appendUniqueStrings(res.EvidenceRefs, stepResult.ArtifactRefs)
	}

	res.Steps = stepResults
	res.Status = contracts.FeatureNodeDone
	res.Summary = "all acceptance steps passed"
	res.FinishedAt = time.Now().UTC()
	if err := writeAcceptanceBundle(evidenceDir, graph, res); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Service) executeAcceptanceStep(ctx context.Context, repoRoot, evidenceDir string, stepIndex int, caseSpec acceptanceCaseSpec, step acceptanceStepSpec) (acceptanceStepResult, error) {
	stepType := normalizeAcceptanceCaseType(firstNonEmpty(step.Type, caseSpec.Type))
	stepName := firstNonEmpty(strings.TrimSpace(step.Name), fmt.Sprintf("step-%d", stepIndex))
	start := time.Now().UTC()
	result := acceptanceStepResult{
		Name:   stepName,
		Type:   stepType,
		Status: "running",
		Method: strings.ToUpper(strings.TrimSpace(step.Method)),
	}
	if result.Method == "" {
		result.Method = "GET"
	}

	timeoutSec := step.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = caseSpec.TimeoutSec
	}
	if timeoutSec <= 0 {
		timeoutSec = defaultAcceptanceTimeoutSec
	}
	stepCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	stepDir := filepath.Join(evidenceDir, fmt.Sprintf("step-%02d", stepIndex))
	if err := os.MkdirAll(stepDir, 0o755); err != nil {
		return result, err
	}

	var stdout string
	var stderr string
	var body string
	var exitCode *int
	var statusCode *int

	switch stepType {
	case "http":
		method := strings.ToUpper(strings.TrimSpace(firstNonEmpty(step.Method, caseSpec.Method, "GET")))
		url := firstNonEmpty(strings.TrimSpace(step.URL), strings.TrimSpace(caseSpec.URL))
		if url == "" {
			return result, fmt.Errorf("http step 缺少 url")
		}
		result.URL = url
		result.Method = method
		st, respBody, headers, err := runHTTPAcceptanceStep(stepCtx, method, url, step.Headers, step.Body)
		statusCode = &st
		body = respBody
		stdout = headers
		if err != nil {
			result.Error = strings.TrimSpace(err.Error())
			result.Status = "blocked"
			result.DurationMS = time.Since(start).Milliseconds()
			writeStepEvidenceFiles(stepDir, &result, stdout, stderr, body)
			return result, acceptanceBlockedError{reason: firstNonEmpty(result.Error, "http acceptance unavailable")}
		}

		if err := evaluateAcceptanceExpect(step.Expect, stdout, stderr, body, exitCode, statusCode); err != nil {
			result.Error = strings.TrimSpace(err.Error())
			result.Status = "failed"
			result.DurationMS = time.Since(start).Milliseconds()
			result.HTTPStatus = statusCode
			writeStepEvidenceFiles(stepDir, &result, stdout, stderr, body)
			return result, err
		}

		result.Status = "pass"
		result.HTTPStatus = statusCode
		result.DurationMS = time.Since(start).Milliseconds()
		writeStepEvidenceFiles(stepDir, &result, stdout, stderr, body)
		return result, nil

	case "pw", "cli":
		cmd, args, commandText, err := buildCommandInvocation(stepType, caseSpec, step)
		if err != nil {
			return result, err
		}
		result.Command = commandText
		code, out, errOut, runErr := infra.RunExitCode(stepCtx, repoRoot, cmd, args...)
		stdout = out
		stderr = errOut
		exitCode = &code
		result.ExitCode = exitCode
		result.DurationMS = time.Since(start).Milliseconds()
		if runErr != nil {
			result.Error = strings.TrimSpace(runErr.Error())
			result.Status = "blocked"
			writeStepEvidenceFiles(stepDir, &result, stdout, stderr, body)
			return result, acceptanceBlockedError{reason: firstNonEmpty(result.Error, "command execution unavailable")}
		}
		if stepType == "pw" && exitCode != nil && *exitCode == 127 {
			result.Error = firstNonEmpty(strings.TrimSpace(stderr), "pw command unavailable")
			result.Status = "blocked"
			writeStepEvidenceFiles(stepDir, &result, stdout, stderr, body)
			return result, acceptanceBlockedError{reason: result.Error}
		}
		if err := evaluateAcceptanceExpect(step.Expect, stdout, stderr, body, exitCode, statusCode); err != nil {
			result.Error = strings.TrimSpace(err.Error())
			result.Status = "failed"
			writeStepEvidenceFiles(stepDir, &result, stdout, stderr, body)
			return result, err
		}
		if stepType == "pw" {
			if step.CaptureSnapshot {
				if path, captureErr := capturePWArtifact(stepCtx, repoRoot, stepDir, "snapshot"); captureErr == nil && strings.TrimSpace(path) != "" {
					result.SnapshotPath = path
					result.ArtifactRefs = appendUniqueStrings(result.ArtifactRefs, []string{path})
				}
			}
			if step.CaptureScreenshot {
				if path, captureErr := capturePWArtifact(stepCtx, repoRoot, stepDir, "screenshot"); captureErr == nil && strings.TrimSpace(path) != "" {
					result.ScreenshotPath = path
					result.ArtifactRefs = appendUniqueStrings(result.ArtifactRefs, []string{path})
				}
			}
		}
		result.Status = "pass"
		writeStepEvidenceFiles(stepDir, &result, stdout, stderr, body)
		return result, nil
	default:
		return result, fmt.Errorf("unsupported acceptance step type: %s", stepType)
	}
}

func buildCommandInvocation(stepType string, caseSpec acceptanceCaseSpec, step acceptanceStepSpec) (string, []string, string, error) {
	command := strings.TrimSpace(firstNonEmpty(step.Command, caseSpec.Command))
	args := parseStringSliceArg(firstNonNil(step.Args, caseSpec.Args))
	if stepType == "pw" {
		if command == "" {
			command = "pw"
		}
		if command == "pw" && len(args) == 0 {
			if strings.TrimSpace(step.URL) != "" {
				args = []string{"open", strings.TrimSpace(step.URL)}
			} else if strings.TrimSpace(caseSpec.URL) != "" {
				args = []string{"open", strings.TrimSpace(caseSpec.URL)}
			} else {
				args = []string{"status"}
			}
		}
	}
	if command == "" {
		return "", nil, "", fmt.Errorf("%s step 缺少 command", stepType)
	}
	if len(args) == 0 {
		return "bash", []string{"-lc", command}, command, nil
	}
	if strings.ContainsAny(command, " \t") {
		joined := strings.TrimSpace(command + " " + strings.Join(args, " "))
		return "bash", []string{"-lc", joined}, joined, nil
	}
	return command, args, strings.TrimSpace(command + " " + strings.Join(args, " ")), nil
}

func runHTTPAcceptanceStep(ctx context.Context, method, url string, headers map[string]string, body string) (int, string, string, error) {
	var bodyReader io.Reader
	if strings.TrimSpace(body) != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, "", "", err
	}
	for k, v := range headers {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	reader := io.LimitReader(resp.Body, maxHTTPBodyBytes)
	raw, readErr := io.ReadAll(reader)
	if readErr != nil {
		return resp.StatusCode, "", renderHTTPHeaders(resp.Header), readErr
	}
	return resp.StatusCode, string(raw), renderHTTPHeaders(resp.Header), nil
}

func renderHTTPHeaders(header http.Header) string {
	if len(header) == 0 {
		return ""
	}
	keys := make([]string, 0, len(header))
	for k := range header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s: %s", k, strings.Join(header.Values(k), ", ")))
	}
	return strings.Join(lines, "\n")
}

func capturePWArtifact(ctx context.Context, repoRoot, stepDir, action string) (string, error) {
	action = strings.TrimSpace(strings.ToLower(action))
	if action == "" {
		return "", fmt.Errorf("artifact action 为空")
	}
	code, stdout, stderr, err := infra.RunExitCode(ctx, repoRoot, "pw", action)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("pw %s failed: exit=%d stderr=%s", action, code, strings.TrimSpace(stderr))
	}
	content := strings.TrimSpace(stdout)
	filename := action + ".txt"
	path := filepath.Join(stepDir, filename)
	if content == "" {
		content = "(empty)"
	}
	if writeErr := os.WriteFile(path, []byte(content+"\n"), 0o644); writeErr != nil {
		return "", writeErr
	}
	return path, nil
}

func evaluateAcceptanceExpect(expect acceptanceExpectSpec, stdout, stderr, body string, exitCode, statusCode *int) error {
	if expect.ExitCode != nil {
		if exitCode == nil || *exitCode != *expect.ExitCode {
			return fmt.Errorf("unexpected exit code: got=%v want=%d", intPtrString(exitCode), *expect.ExitCode)
		}
	} else if exitCode != nil && *exitCode != 0 {
		return fmt.Errorf("unexpected exit code: got=%d want=0", *exitCode)
	}
	if expect.StatusCode != nil {
		if statusCode == nil || *statusCode != *expect.StatusCode {
			return fmt.Errorf("unexpected http status: got=%v want=%d", intPtrString(statusCode), *expect.StatusCode)
		}
	}
	for _, needle := range expect.StdoutContains {
		if !strings.Contains(stdout, needle) {
			return fmt.Errorf("stdout missing expected text: %q", needle)
		}
	}
	for _, needle := range expect.StdoutNotContains {
		if strings.Contains(stdout, needle) {
			return fmt.Errorf("stdout contains unexpected text: %q", needle)
		}
	}
	for _, needle := range expect.StderrContains {
		if !strings.Contains(stderr, needle) {
			return fmt.Errorf("stderr missing expected text: %q", needle)
		}
	}
	for _, needle := range expect.BodyContains {
		if !strings.Contains(body, needle) {
			return fmt.Errorf("response body missing expected text: %q", needle)
		}
	}
	return nil
}

func intPtrString(v *int) string {
	if v == nil {
		return "nil"
	}
	return strconv.Itoa(*v)
}

func isAcceptanceBlockedError(err error) bool {
	var blockedErr acceptanceBlockedError
	return errors.As(err, &blockedErr)
}

func writeStepEvidenceFiles(stepDir string, step *acceptanceStepResult, stdout, stderr, body string) {
	if step == nil {
		return
	}
	artifacts := make([]string, 0, 3)
	if strings.TrimSpace(stdout) != "" {
		path := filepath.Join(stepDir, "stdout.txt")
		_ = os.WriteFile(path, []byte(stdout), 0o644)
		step.StdoutPath = path
		artifacts = append(artifacts, path)
	}
	if strings.TrimSpace(stderr) != "" {
		path := filepath.Join(stepDir, "stderr.txt")
		_ = os.WriteFile(path, []byte(stderr), 0o644)
		step.StderrPath = path
		artifacts = append(artifacts, path)
	}
	if strings.TrimSpace(body) != "" {
		path := filepath.Join(stepDir, "response_body.txt")
		_ = os.WriteFile(path, []byte(body), 0o644)
		step.BodyPath = path
		artifacts = append(artifacts, path)
	}
	step.ArtifactRefs = appendUniqueStrings(step.ArtifactRefs, artifacts)
	if raw, err := json.MarshalIndent(step, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(stepDir, "step.json"), append(raw, '\n'), 0o644)
		step.ArtifactRefs = appendUniqueStrings(step.ArtifactRefs, []string{filepath.Join(stepDir, "step.json")})
	}
}

func writeAcceptanceBundle(evidenceDir string, graph contracts.FeatureGraph, res acceptanceNodeRunResult) error {
	bundle := acceptanceEvidenceBundle{
		FeatureID:   strings.TrimSpace(graph.FeatureID),
		FeatureGoal: strings.TrimSpace(graph.Goal),
		NodeID:      strings.TrimSpace(res.NodeID),
		NodeTitle:   strings.TrimSpace(res.NodeTitle),
		CaseName:    strings.TrimSpace(res.CaseName),
		CaseType:    normalizeAcceptanceCaseType(res.CaseType),
		Status:      string(res.Status),
		Summary:     strings.TrimSpace(res.Summary),
		StartedAt:   res.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt:  res.FinishedAt.UTC().Format(time.RFC3339),
		DurationMS:  res.FinishedAt.Sub(res.StartedAt).Milliseconds(),
		Steps:       append([]acceptanceStepResult{}, res.Steps...),
	}
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(evidenceDir, "bundle.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return nil
}

func collectFailedAcceptanceResults(results []acceptanceNodeRunResult) []acceptanceNodeRunResult {
	failed := make([]acceptanceNodeRunResult, 0)
	for _, res := range results {
		if res.Status == contracts.FeatureNodeFailed || res.Status == contracts.FeatureNodeBlocked {
			failed = append(failed, res)
		}
	}
	return failed
}

func (s *Service) createAcceptanceFailureTicket(ctx context.Context, graph contracts.FeatureGraph, failed []acceptanceNodeRunResult, spec acceptanceRunSpec) (contracts.Ticket, uint, error) {
	if len(failed) == 0 {
		return contracts.Ticket{}, 0, fmt.Errorf("failed acceptance 为空")
	}
	_, db, err := s.require()
	if err != nil {
		return contracts.Ticket{}, 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	failedNodeIDs := make([]string, 0, len(failed))
	details := make([]string, 0, len(failed)*2)
	for _, item := range failed {
		failedNodeIDs = append(failedNodeIDs, item.NodeID)
		details = append(details,
			fmt.Sprintf("- node: %s (%s)", item.NodeID, string(item.Status)),
			fmt.Sprintf("  summary: %s", firstNonEmpty(item.Summary, item.FailureReason, "-")),
		)
		if len(item.EvidenceRefs) > 0 {
			details = append(details, "  evidence:")
			for _, ref := range item.EvidenceRefs {
				details = append(details, "  - "+ref)
			}
		}
	}
	sort.Strings(failedNodeIDs)
	title := fmt.Sprintf("%s: %s", strings.TrimSpace(spec.FailureTicketTitlePrefix), firstNonEmpty(strings.TrimSpace(graph.Goal), strings.TrimSpace(graph.FeatureID), "feature"))
	description := strings.Join([]string{
		"acceptance failed, need follow-up fix",
		"",
		"feature_id: " + firstNonEmpty(strings.TrimSpace(spec.FeatureID), strings.TrimSpace(graph.FeatureID), "-"),
		"failed_nodes: " + strings.Join(failedNodeIDs, ","),
		"",
		"details:",
		strings.Join(details, "\n"),
	}, "\n")
	now := time.Now()
	ticket := contracts.Ticket{
		Title:          strings.TrimSpace(title),
		Description:    strings.TrimSpace(description),
		Label:          strings.TrimSpace(spec.FailureTicketLabel),
		Priority:       3,
		WorkflowStatus: contracts.TicketBacklog,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.WithContext(ctx).Create(&ticket).Error; err != nil {
		return contracts.Ticket{}, 0, err
	}
	if !spec.AutoDispatchFailureTicket {
		return ticket, 0, nil
	}
	worker, serr := s.StartTicket(ctx, ticket.ID)
	if serr != nil {
		return ticket, 0, serr
	}
	if worker == nil {
		return ticket, 0, nil
	}
	return ticket, worker.ID, nil
}

func buildAcceptanceObservations(results []acceptanceNodeRunResult) []string {
	out := make([]string, 0, len(results))
	for _, res := range results {
		line := fmt.Sprintf("[%s] %s: %s", string(res.Status), res.NodeID, firstNonEmpty(strings.TrimSpace(res.Summary), strings.TrimSpace(res.FailureReason), "-"))
		out = append(out, line)
	}
	if len(out) == 0 {
		return []string{"no acceptance execution"}
	}
	return out
}

func buildAcceptanceConclusion(status string, results []acceptanceNodeRunResult, failureTicketID uint) string {
	if status == "done" {
		return "all acceptance gates passed"
	}
	failedIDs := make([]string, 0)
	for _, res := range results {
		if res.Status == contracts.FeatureNodeFailed || res.Status == contracts.FeatureNodeBlocked {
			failedIDs = append(failedIDs, res.NodeID)
		}
	}
	sort.Strings(failedIDs)
	if failureTicketID != 0 {
		return fmt.Sprintf("acceptance failed on %s; follow-up ticket t%d created", strings.Join(failedIDs, ", "), failureTicketID)
	}
	if len(failedIDs) == 0 {
		return "acceptance incomplete"
	}
	return fmt.Sprintf("acceptance failed on %s", strings.Join(failedIDs, ", "))
}

func buildAcceptanceSteps(requiredChecks []string, results []acceptanceNodeRunResult) []string {
	if len(results) == 0 {
		return append([]string{}, requiredChecks...)
	}
	out := make([]string, 0, len(results))
	for _, res := range results {
		item := firstNonEmpty(strings.TrimSpace(res.CaseName), strings.TrimSpace(res.NodeTitle), strings.TrimSpace(res.NodeID))
		out = append(out, item)
	}
	return out
}

func collectEvidenceRefs(results []acceptanceNodeRunResult) []string {
	refs := make([]string, 0, len(results)*2)
	for _, res := range results {
		refs = append(refs, res.EvidenceRefs...)
	}
	return appendUniqueStrings(nil, refs)
}

func writeAcceptanceMarkdown(path string, graph contracts.FeatureGraph, st pmWorkspaceStateUpdate, runAt time.Time, results []acceptanceNodeRunResult, failureTicketID uint) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("acceptance path 为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	featureTitle := firstNonEmpty(strings.TrimSpace(st.FeatureTitle), strings.TrimSpace(graph.Goal), strings.TrimSpace(graph.FeatureID), "none")
	status := firstNonEmpty(strings.TrimSpace(st.AcceptanceStatus), "pending")
	startup := strings.TrimSpace(st.StartupCommand)
	url := strings.TrimSpace(st.URL)
	steps := normalizeStringList(st.Steps)
	obs := normalizeStringList(st.Observations)
	conclusion := strings.TrimSpace(st.Conclusion)
	if conclusion == "" {
		conclusion = "pending"
	}
	checks := normalizeStringList(st.RequiredChecks)
	evidence := normalizeStringList(st.AcceptanceEvidence)

	var b strings.Builder
	fmt.Fprintln(&b, "# PM Acceptance Evidence")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## 当前 Feature")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- feature: %s\n", featureTitle)
	fmt.Fprintf(&b, "- status: %s\n", status)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## 真实验收要求")
	if len(checks) == 0 {
		fmt.Fprintln(&b, "- 真实验收标准待补充")
	} else {
		for _, check := range checks {
			fmt.Fprintf(&b, "- %s\n", check)
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "### 环境")
	fmt.Fprintf(&b, "- 启动命令：%s\n", startup)
	fmt.Fprintf(&b, "- 访问 URL：%s\n", url)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "### 操作步骤")
	if len(steps) == 0 {
		fmt.Fprintln(&b, "1. pending")
	} else {
		for i, step := range steps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step)
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "### 观察结果")
	if len(obs) == 0 {
		fmt.Fprintln(&b, "- pending")
	} else {
		for _, line := range obs {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "### 结论")
	fmt.Fprintf(&b, "- %s\n", conclusion)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## 运行记录")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "### run@%s\n", runAt.UTC().Format(time.RFC3339))
	if failureTicketID != 0 {
		fmt.Fprintf(&b, "- failure_ticket: t%d\n", failureTicketID)
	}
	if len(evidence) == 0 {
		fmt.Fprintln(&b, "- evidence: none")
	} else {
		fmt.Fprintln(&b, "- evidence:")
		for _, ref := range evidence {
			fmt.Fprintf(&b, "  - %s\n", ref)
		}
	}
	if len(results) > 0 {
		fmt.Fprintln(&b, "- node_results:")
		for _, res := range results {
			fmt.Fprintf(&b, "  - %s [%s]: %s\n", res.NodeID, res.Status, firstNonEmpty(strings.TrimSpace(res.Summary), "-"))
		}
	}

	raw := []byte(strings.TrimSpace(b.String()) + "\n")
	return os.WriteFile(path, raw, 0o644)
}

func (s *Service) updatePMWorkspaceState(update pmWorkspaceStateUpdate) error {
	repoRoot := strings.TrimSpace(s.p.RepoRoot)
	if repoRoot == "" {
		return fmt.Errorf("repo_root 为空")
	}
	statePath := filepath.Join(repoRoot, filepath.FromSlash(pmStateRelPath))
	state := map[string]any{}
	raw, err := os.ReadFile(statePath)
	if err == nil {
		_ = json.Unmarshal(raw, &state)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if state == nil {
		state = map[string]any{}
	}
	if strings.TrimSpace(asString(state["schema"])) == "" {
		state["schema"] = "dalek.pm.state.v1"
	}

	files := ensureMap(state, "files")
	if strings.TrimSpace(update.AcceptancePath) != "" {
		files["acceptance_path"] = strings.TrimSpace(update.AcceptancePath)
	}

	feature := ensureMap(state, "feature")
	if strings.TrimSpace(update.FeatureTitle) != "" {
		feature["title"] = strings.TrimSpace(update.FeatureTitle)
	}
	acceptance := ensureMap(feature, "acceptance")
	if strings.TrimSpace(update.AcceptancePath) != "" {
		acceptance["path"] = strings.TrimSpace(update.AcceptancePath)
	}
	acceptance["exists"] = true
	if strings.TrimSpace(update.AcceptanceStatus) != "" {
		acceptance["status"] = strings.TrimSpace(update.AcceptanceStatus)
	}
	if checks := normalizeStringList(update.RequiredChecks); len(checks) > 0 {
		acceptance["required_checks"] = checks
	}
	if strings.TrimSpace(update.StartupCommand) != "" {
		acceptance["startup_command"] = strings.TrimSpace(update.StartupCommand)
	}
	if strings.TrimSpace(update.URL) != "" {
		acceptance["url"] = strings.TrimSpace(update.URL)
	}
	if steps := normalizeStringList(update.Steps); len(steps) > 0 {
		acceptance["steps"] = steps
	}
	if observations := normalizeStringList(update.Observations); len(observations) > 0 {
		acceptance["observations"] = observations
	}
	if strings.TrimSpace(update.Conclusion) != "" {
		acceptance["conclusion"] = strings.TrimSpace(update.Conclusion)
	}
	if refs := normalizeStringList(update.AcceptanceEvidence); len(refs) > 0 {
		acceptance["evidence_refs"] = refs
	}

	runtime := ensureMap(state, "runtime")
	if strings.TrimSpace(update.FeatureStatus) != "" {
		runtime["current_status"] = strings.TrimSpace(update.FeatureStatus)
	}
	if strings.TrimSpace(update.LastAction) != "" {
		runtime["last_action"] = strings.TrimSpace(update.LastAction)
	}
	state["updated_at"] = time.Now().UTC().Format(time.RFC3339)

	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return err
	}
	raw, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(statePath, raw, 0o644)
}

func ensureMap(root map[string]any, key string) map[string]any {
	if root == nil {
		return map[string]any{}
	}
	if existing, ok := root[key]; ok {
		switch typed := existing.(type) {
		case map[string]any:
			return typed
		case contracts.JSONMap:
			m := map[string]any(typed)
			root[key] = m
			return m
		}
	}
	m := map[string]any{}
	root[key] = m
	return m
}

func (s *Service) readCurrentFeatureStatusFromState() (string, error) {
	repoRoot := strings.TrimSpace(s.p.RepoRoot)
	if repoRoot == "" {
		return "", fmt.Errorf("repo_root 为空")
	}
	statePath := filepath.Join(repoRoot, filepath.FromSlash(pmStateRelPath))
	raw, err := os.ReadFile(statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	state := map[string]any{}
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", err
	}
	runtime := ensureMap(state, "runtime")
	return normalizeFeatureStatusValue(asString(runtime["current_status"])), nil
}

func normalizeFeatureStatusValue(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none", "pending", "ready", "idle", "planned":
		return "running"
	case "running", "active", "in_progress", "in-progress":
		return "running"
	case "verifying", "verify":
		return "verifying"
	case "done", "completed", "success":
		return "done"
	case "blocked", "wait_user", "waiting":
		return "blocked"
	default:
		return strings.TrimSpace(raw)
	}
}

func parseDesiredFeatureStatus(args contracts.JSONMap) (string, error) {
	status := strings.TrimSpace(firstNonEmpty(
		jsonMapString(args, "status"),
		jsonMapString(args, "value"),
	))
	if status == "" {
		return "", fmt.Errorf("set_feature_status 缺少 status")
	}
	status = normalizeFeatureStatusValue(status)
	switch status {
	case "running", "verifying", "done", "blocked":
		return status, nil
	default:
		return "", fmt.Errorf("set_feature_status 不支持 status=%s", status)
	}
}

func (s *Service) acceptanceGateState(ctx context.Context) (acceptanceGateSummary, error) {
	graph, _, err := s.loadPMPlanGraph()
	if err != nil {
		return acceptanceGateSummary{}, err
	}
	return evaluateAcceptanceGate(graph), nil
}

func evaluateAcceptanceGate(graph contracts.FeatureGraph) acceptanceGateSummary {
	summary := acceptanceGateSummary{}
	for _, node := range graph.Nodes {
		if node.Type != contracts.FeatureNodeAcceptance {
			continue
		}
		summary.Total++
		switch node.Status {
		case contracts.FeatureNodeDone:
			summary.Done++
		case contracts.FeatureNodeFailed:
			summary.Failed++
		case contracts.FeatureNodeBlocked:
			summary.Blocked++
		default:
			summary.Pending++
		}
	}
	summary.Passed = summary.Total > 0 && summary.Done == summary.Total
	return summary
}

func deriveAcceptanceStatusFromGate(g acceptanceGateSummary) string {
	switch {
	case g.Total == 0:
		return "pending"
	case g.Failed > 0:
		return "failed"
	case g.Done == g.Total:
		return "done"
	case g.Blocked > 0:
		return "blocked"
	default:
		return "running"
	}
}

func (s *Service) loadPMPlanGraph() (contracts.FeatureGraph, string, error) {
	repoRoot := strings.TrimSpace(s.p.RepoRoot)
	if repoRoot == "" {
		return contracts.FeatureGraph{}, "", fmt.Errorf("repo_root 为空")
	}
	path := filepath.Join(repoRoot, filepath.FromSlash(pmPlanJSONRelPath))
	graph, err := loadPMPlanGraph(path)
	if err != nil {
		return contracts.FeatureGraph{}, "", err
	}
	return graph, path, nil
}

func loadPMPlanGraph(path string) (contracts.FeatureGraph, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return contracts.FeatureGraph{}, fmt.Errorf("plan.json path 为空")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return contracts.FeatureGraph{}, fmt.Errorf("plan.json 不存在：%s（请先运行 dalek pm state sync）", path)
		}
		return contracts.FeatureGraph{}, err
	}
	var graph contracts.FeatureGraph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return contracts.FeatureGraph{}, err
	}
	graph = normalizeGraphForAcceptance(graph)
	if strings.TrimSpace(graph.FeatureID) == "" {
		return contracts.FeatureGraph{}, fmt.Errorf("plan.json 缺少 feature_id")
	}
	if len(graph.Nodes) == 0 {
		return contracts.FeatureGraph{}, fmt.Errorf("plan.json 缺少 nodes")
	}
	return graph, nil
}

func savePMPlanGraph(path string, graph contracts.FeatureGraph) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("plan.json path 为空")
	}
	graph = normalizeGraphForAcceptance(graph)
	if graph.UpdatedAt.IsZero() {
		graph.UpdatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func normalizeGraphForAcceptance(graph contracts.FeatureGraph) contracts.FeatureGraph {
	graph.Schema = strings.TrimSpace(graph.Schema)
	graph.FeatureID = strings.TrimSpace(graph.FeatureID)
	graph.Goal = strings.TrimSpace(graph.Goal)
	graph.CurrentFocus = strings.TrimSpace(graph.CurrentFocus)
	graph.NextPMAction = strings.TrimSpace(graph.NextPMAction)
	for i := range graph.Nodes {
		node := graph.Nodes[i]
		node.ID = strings.TrimSpace(node.ID)
		node.Title = strings.TrimSpace(node.Title)
		node.DoneWhen = strings.TrimSpace(node.DoneWhen)
		node.Notes = strings.TrimSpace(node.Notes)
		node.TicketID = strings.TrimSpace(node.TicketID)
		node.DependsOn = normalizeStringList(node.DependsOn)
		node.TouchSurfaces = normalizeStringList(node.TouchSurfaces)
		node.EvidenceRefs = normalizeStringList(node.EvidenceRefs)
		if node.ID == "" {
			continue
		}
		if node.Title == "" {
			node.Title = node.ID
		}
		graph.Nodes[i] = node
	}
	if graph.UpdatedAt.IsZero() {
		graph.UpdatedAt = time.Now().UTC()
	}
	return graph
}

func selectAcceptanceNodes(graph contracts.FeatureGraph, nodeIDs []string) ([]contracts.FeatureNode, error) {
	all := make([]contracts.FeatureNode, 0)
	for _, node := range graph.Nodes {
		if node.Type == contracts.FeatureNodeAcceptance {
			all = append(all, node)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	if len(nodeIDs) == 0 {
		return all, nil
	}
	lookup := map[string]contracts.FeatureNode{}
	for _, node := range all {
		lookup[node.ID] = node
	}
	out := make([]contracts.FeatureNode, 0, len(nodeIDs))
	for _, id := range normalizeStringList(nodeIDs) {
		node, ok := lookup[id]
		if !ok {
			return nil, fmt.Errorf("acceptance node 不存在: %s", id)
		}
		out = append(out, node)
	}
	return out, nil
}

func indexFeatureNodesByID(nodes []contracts.FeatureNode) map[string]contracts.FeatureNode {
	out := make(map[string]contracts.FeatureNode, len(nodes))
	for _, node := range nodes {
		id := strings.TrimSpace(node.ID)
		if id == "" {
			continue
		}
		out[id] = node
	}
	return out
}

func unresolvedNodeDependencies(node contracts.FeatureNode, nodeByID map[string]contracts.FeatureNode) []string {
	if len(node.DependsOn) == 0 {
		return nil
	}
	out := make([]string, 0)
	for _, dep := range node.DependsOn {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		depNode, ok := nodeByID[dep]
		if !ok {
			out = append(out, dep)
			continue
		}
		if depNode.Status != contracts.FeatureNodeDone {
			out = append(out, dep)
		}
	}
	return out
}

func acceptanceChecksFromNodes(nodes []contracts.FeatureNode) []string {
	checks := make([]string, 0)
	for _, node := range nodes {
		if node.Type != contracts.FeatureNodeAcceptance {
			continue
		}
		check := firstNonEmpty(strings.TrimSpace(node.DoneWhen), strings.TrimSpace(node.Title))
		if check == "" {
			continue
		}
		checks = append(checks, check)
	}
	return normalizeStringList(checks)
}

func normalizeAcceptanceCaseType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "pw", "playwright", "browser":
		return "pw"
	case "http", "curl", "api":
		return "http"
	case "cli", "shell", "bash", "command", "cmd", "":
		return "cli"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func parseStringSliceArg(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return normalizeStringList(t)
	case contracts.JSONStringSlice:
		return normalizeStringList([]string(t))
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			val := strings.TrimSpace(asString(item))
			if val == "" {
				continue
			}
			out = append(out, val)
		}
		return normalizeStringList(out)
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return nil
		}
		if strings.HasPrefix(t, "[") {
			var arr []string
			if err := json.Unmarshal([]byte(t), &arr); err == nil {
				return normalizeStringList(arr)
			}
		}
		parts := strings.Split(strings.NewReplacer("，", ",", ";", ",").Replace(t), ",")
		return normalizeStringList(parts)
	default:
		return normalizeStringList([]string{asString(v)})
	}
}

func parseStringMapArg(v any) map[string]string {
	out := map[string]string{}
	switch t := v.(type) {
	case nil:
		return out
	case map[string]string:
		for k, v := range t {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k == "" || v == "" {
				continue
			}
			out[k] = v
		}
		return out
	case map[string]any:
		for k, raw := range t {
			k = strings.TrimSpace(k)
			v := strings.TrimSpace(asString(raw))
			if k == "" || v == "" {
				continue
			}
			out[k] = v
		}
		return out
	case contracts.JSONMap:
		return parseStringMapArg(map[string]any(t))
	default:
		return out
	}
}

func normalizeStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
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

func appendUniqueStrings(base []string, extra []string) []string {
	base = normalizeStringList(base)
	extra = normalizeStringList(extra)
	if len(extra) == 0 {
		return base
	}
	seen := map[string]struct{}{}
	for _, item := range base {
		seen[item] = struct{}{}
	}
	for _, item := range extra {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		base = append(base, item)
	}
	return base
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

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

func normalizePathSlug(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "x"
	}
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "\t", "-", "\n", "-", "\r", "-")
	raw = r.Replace(raw)
	b := strings.Builder{}
	for _, ch := range raw {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		case ch == '-' || ch == '_' || ch == '.':
			b.WriteRune(ch)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "x"
	}
	return out
}

func parseStringOrInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int8:
		return int(t), true
	case int16:
		return int(t), true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case uint:
		return int(t), true
	case uint8:
		return int(t), true
	case uint16:
		return int(t), true
	case uint32:
		return int(t), true
	case uint64:
		return int(t), true
	case float32:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func coerceOptionalInt(v any) *int {
	if n, ok := parseStringOrInt(v); ok {
		return &n
	}
	return nil
}

func coercePositiveInt(v any, fallback int) int {
	if n, ok := parseStringOrInt(v); ok && n > 0 {
		return n
	}
	if fallback > 0 {
		return fallback
	}
	return defaultAcceptanceTimeoutSec
}

func coerceBool(v any, fallback bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		if t == "" {
			return fallback
		}
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// jsonMapBool extracts a bool value from a JSONMap by key.
func jsonMapBool(m contracts.JSONMap, key string) bool {
	if len(m) == 0 {
		return false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return t != 0
	default:
		return false
	}
}

// jsonMapString extracts a string value from a JSONMap by key.
func jsonMapString(m contracts.JSONMap, key string) string {
	if len(m) == 0 {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// jsonMapInt extracts an int value from a JSONMap by key.
func jsonMapInt(m contracts.JSONMap, key string) int {
	if len(m) == 0 {
		return 0
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case uint:
		return int(t)
	case uint64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}

// jsonMapUint extracts a uint value from a JSONMap by key.
func jsonMapUint(m contracts.JSONMap, key string) uint {
	n := jsonMapInt(m, key)
	if n < 0 {
		return 0
	}
	return uint(n)
}
