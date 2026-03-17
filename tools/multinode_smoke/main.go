package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type smokeRunner struct {
	dalek   string
	home    string
	project string
	timeout time.Duration
}

type taskRequestEnvelope struct {
	Schema string `json:"schema"`
	Task   struct {
		Accepted    bool   `json:"accepted"`
		Role        string `json:"role"`
		RoleSource  string `json:"role_source"`
		RouteReason string `json:"route_reason"`
		RouteMode   string `json:"route_mode"`
		RouteTarget string `json:"route_target"`
		TaskRunID   uint   `json:"task_run_id"`
		RemoteRunID uint   `json:"remote_run_id"`
		RequestID   string `json:"request_id"`
		TicketID    uint   `json:"ticket_id"`
	} `json:"task"`
}

type taskShowEnvelope struct {
	Schema string `json:"schema"`
	Task   struct {
		RunID       uint   `json:"run_id"`
		Role        string `json:"role"`
		RoleSource  string `json:"role_source"`
		RouteReason string `json:"route_reason"`
		RouteMode   string `json:"route_mode"`
		RouteTarget string `json:"route_target"`
		RemoteRunID uint   `json:"remote_run_id"`
	} `json:"task"`
}

type taskEventsEnvelope struct {
	Schema string `json:"schema"`
	Events []struct {
		Type string         `json:"type"`
		Note string         `json:"note"`
		Data map[string]any `json:"payload"`
	} `json:"events"`
}

type runShowEnvelope struct {
	Schema string `json:"schema"`
	Run    struct {
		RunID     uint   `json:"run_id"`
		RunStatus string `json:"run_status"`
		RequestID string `json:"request_id"`
	} `json:"run"`
	TaskStatus struct {
		LastEventType  string `json:"last_event_type"`
		RuntimeSummary string `json:"runtime_summary"`
	} `json:"task_status"`
}

type runLogsEnvelope struct {
	Schema string `json:"schema"`
	Logs   struct {
		Found bool   `json:"found"`
		RunID uint   `json:"run_id"`
		Tail  string `json:"tail"`
	} `json:"logs"`
}

type runArtifactsEnvelope struct {
	Schema    string `json:"schema"`
	Artifacts struct {
		Found bool `json:"found"`
		RunID uint `json:"run_id"`
	} `json:"artifacts"`
}

func main() {
	var (
		dalek        = flag.String("dalek", "./dalek", "dalek binary path")
		home         = flag.String("home", "", "dalek home dir")
		project      = flag.String("project", "", "project name")
		ticket       = flag.Uint("ticket", 0, "ticket id")
		devPrompt    = flag.String("dev-prompt", "", "optional dev task prompt")
		verifyTarget = flag.String("verify-target", "", "optional verify target")
		timeout      = flag.Duration("timeout", 20*time.Second, "per command timeout")
	)
	flag.Parse()

	if *ticket == 0 && (strings.TrimSpace(*devPrompt) != "" || strings.TrimSpace(*verifyTarget) != "") {
		failf("--ticket is required when --dev-prompt or --verify-target is set")
	}

	runner := smokeRunner{
		dalek:   strings.TrimSpace(*dalek),
		home:    strings.TrimSpace(*home),
		project: strings.TrimSpace(*project),
		timeout: *timeout,
	}

	fmt.Println("== Multi-Node V2 Smoke ==")
	runner.runTextStep("daemon status", "daemon", "status")
	runner.runTextStep("node ls", "node", "ls")

	if prompt := strings.TrimSpace(*devPrompt); prompt != "" {
		req := runner.runTaskRequest("dev task request", *ticket, "--prompt", prompt)
		expect(req.Task.Accepted, "dev request not accepted")
		expect(strings.TrimSpace(req.Task.Role) == "dev", "expected dev role, got %q", req.Task.Role)
		runner.runTaskReadback("dev", req.Task.TaskRunID, req.Task.RoleSource)
	}

	if target := strings.TrimSpace(*verifyTarget); target != "" {
		req := runner.runTaskRequest("run task request", *ticket, "--verify-target", target)
		expect(req.Task.Accepted, "run request not accepted")
		expect(strings.TrimSpace(req.Task.Role) == "run", "expected run role, got %q", req.Task.Role)
		runner.runTaskReadback("run", req.Task.TaskRunID, req.Task.RoleSource)
		runner.runRunReadback(req.Task.TaskRunID)
	}

	fmt.Println("SMOKE_OK")
}

func (r smokeRunner) runTaskRequest(label string, ticket uint, extra ...string) taskRequestEnvelope {
	args := []string{"task", "request", "--ticket", fmt.Sprintf("%d", ticket), "-o", "json"}
	args = append(args, extra...)
	var out taskRequestEnvelope
	r.runJSONStep(label, &out, args...)
	expect(out.Schema == "dalek.task.request.v1", "unexpected task request schema: %q", out.Schema)
	expect(out.Task.TaskRunID != 0, "task request missing task_run_id")
	fmt.Printf("%s: run=%d role=%s route=%s source=%s\n", label, out.Task.TaskRunID, out.Task.Role, firstNonEmpty(out.Task.RouteTarget, out.Task.RouteMode), out.Task.RoleSource)
	return out
}

func (r smokeRunner) runTaskReadback(kind string, runID uint, expectedRoleSource string) {
	var show taskShowEnvelope
	r.runJSONStep(kind+" task show", &show, "task", "show", "--id", fmt.Sprintf("%d", runID), "-o", "json")
	expect(show.Schema == "dalek.task.show.v2", "unexpected task show schema: %q", show.Schema)
	expect(show.Task.RunID == runID, "task show run id mismatch: got=%d want=%d", show.Task.RunID, runID)
	expect(strings.TrimSpace(show.Task.Role) != "", "task show missing role")
	if expectedRoleSource != "" {
		expect(strings.TrimSpace(show.Task.RoleSource) == strings.TrimSpace(expectedRoleSource), "task show role_source mismatch: got=%q want=%q", show.Task.RoleSource, expectedRoleSource)
	}

	var events taskEventsEnvelope
	r.runJSONStep(kind+" task events", &events, "task", "events", "--id", fmt.Sprintf("%d", runID), "--limit", "50", "-o", "json")
	expect(events.Schema == "dalek.task.events.v1", "unexpected task events schema: %q", events.Schema)
	foundRouteEvent := false
	for _, ev := range events.Events {
		if strings.TrimSpace(ev.Type) == "task_request_routed" {
			foundRouteEvent = true
			break
		}
	}
	expect(foundRouteEvent, "task events missing task_request_routed")
}

func (r smokeRunner) runRunReadback(runID uint) {
	var show runShowEnvelope
	r.runJSONStep("run show", &show, "run", "show", "--id", fmt.Sprintf("%d", runID), "-o", "json")
	expect(show.Schema == "dalek.run.show.v1", "unexpected run show schema: %q", show.Schema)
	expect(show.Run.RunID == runID, "run show run id mismatch: got=%d want=%d", show.Run.RunID, runID)

	var logs runLogsEnvelope
	r.runJSONStep("run logs", &logs, "run", "logs", "--id", fmt.Sprintf("%d", runID), "-o", "json")
	expect(logs.Schema == "dalek.run.logs.v1", "unexpected run logs schema: %q", logs.Schema)
	expect(logs.Logs.RunID == runID, "run logs run id mismatch: got=%d want=%d", logs.Logs.RunID, runID)

	var artifacts runArtifactsEnvelope
	r.runJSONStep("run artifact ls", &artifacts, "run", "artifact", "ls", "--id", fmt.Sprintf("%d", runID), "-o", "json")
	expect(artifacts.Schema == "dalek.run.artifacts.v1", "unexpected run artifacts schema: %q", artifacts.Schema)
	expect(artifacts.Artifacts.RunID == runID, "run artifacts run id mismatch: got=%d want=%d", artifacts.Artifacts.RunID, runID)
}

func (r smokeRunner) runTextStep(label string, args ...string) {
	out := r.runCommand(label, args...)
	if strings.TrimSpace(out) == "" {
		fmt.Printf("%s: ok (empty output)\n", label)
		return
	}
	fmt.Printf("%s: ok\n", label)
}

func (r smokeRunner) runJSONStep(label string, out any, args ...string) {
	raw := r.runCommand(label, args...)
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		failf("%s: decode json failed: %v\nraw=%s", label, err, raw)
	}
}

func (r smokeRunner) runCommand(label string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	cmdArgs := make([]string, 0, len(args)+4)
	if r.home != "" {
		cmdArgs = append(cmdArgs, "--home", r.home)
	}
	if r.project != "" {
		cmdArgs = append(cmdArgs, "--project", r.project)
	}
	cmdArgs = append(cmdArgs, args...)
	fmt.Printf(">> %s: %s %s\n", label, r.dalek, strings.Join(cmdArgs, " "))
	cmd := exec.CommandContext(ctx, r.dalek, cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		failf("%s failed: %v\n%s", label, err, string(out))
	}
	return string(out)
}

func expect(ok bool, format string, args ...any) {
	if ok {
		return
	}
	failf(format, args...)
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "SMOKE_FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "-"
}
