package channel

import (
	"context"
	"testing"

	"dalek/internal/contracts"
)

func TestActionPriorityLabel(t *testing.T) {
	cases := []struct {
		priority int
		want     string
	}{
		{priority: 0, want: "none(0)"},
		{priority: 1, want: "low(1)"},
		{priority: 2, want: "medium(2)"},
		{priority: 3, want: "high(3)"},
		{priority: 9, want: "9"},
	}
	for _, tc := range cases {
		if got := actionPriorityLabel(tc.priority); got != tc.want {
			t.Fatalf("actionPriorityLabel(%d)=%q, want=%q", tc.priority, got, tc.want)
		}
	}
}

func TestActionExecutor_SubmitTaskRequest(t *testing.T) {
	executor := NewActionExecutor(nil, nil, nil, testTaskRequestActionAdapter{
		submit: func(ctx context.Context, in SubmitTaskRequestActionInput) (SubmitTaskRequestActionResult, error) {
			_ = ctx
			if in.TicketID != 12 {
				t.Fatalf("unexpected ticket id: %d", in.TicketID)
			}
			if in.ForceRole != "run" {
				t.Fatalf("unexpected role: %q", in.ForceRole)
			}
			if in.VerifyTarget != "test" {
				t.Fatalf("unexpected verify target: %q", in.VerifyTarget)
			}
			return SubmitTaskRequestActionResult{
				Accepted:     true,
				Role:         "run",
				RoleSource:   "auto_route_prompt",
				RouteReason:  "prompt matched verify/test keywords",
				RouteMode:    "remote",
				RouteTarget:  "http://c.example",
				TaskRunID:    301,
				RemoteRunID:  902,
				RequestID:    "req-1",
				TicketID:     in.TicketID,
				VerifyTarget: in.VerifyTarget,
			}, nil
		},
	})

	res, err := executor.Execute(context.Background(), contracts.TurnAction{
		Name: contracts.ActionSubmitTaskRequest,
		Args: map[string]any{
			"ticket_id":     12,
			"role":          "run",
			"verify_target": "test",
		},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got=%+v", res)
	}
	if res.ActionName != contracts.ActionSubmitTaskRequest {
		t.Fatalf("unexpected action name: %q", res.ActionName)
	}
	taskReq, ok := res.Data["task_request"].(map[string]any)
	if !ok {
		t.Fatalf("missing task_request data: %+v", res.Data)
	}
	if got := taskReq["task_run_id"]; got != uint(301) {
		t.Fatalf("unexpected task_run_id: %#v", got)
	}
	if got := taskReq["role_source"]; got != "auto_route_prompt" {
		t.Fatalf("unexpected role_source: %#v", got)
	}
}
