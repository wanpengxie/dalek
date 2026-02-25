package task

import (
	"context"
	"testing"

	"dalek/internal/store"
)

func TestService_SubagentRunRoundTrip(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()

	taskRun, err := svc.CreateRun(ctx, CreateRunInput{
		OwnerType:          store.TaskOwnerSubagent,
		TaskType:           store.TaskTypeSubagentRun,
		ProjectKey:         "demo",
		TicketID:           0,
		WorkerID:           0,
		SubjectType:        "project",
		SubjectID:          "demo",
		RequestID:          "sub-req-1",
		OrchestrationState: store.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	rec, err := svc.CreateSubagentRun(ctx, CreateSubagentRunInput{
		ProjectKey: "demo",
		TaskRunID:  taskRun.ID,
		RequestID:  "sub-req-1",
		Provider:   "claude",
		Model:      "sonnet",
		Prompt:     "实现 agent 子命令",
		CWD:        "/tmp/demo",
		RuntimeDir: "/tmp/demo/.runtime/1",
	})
	if err != nil {
		t.Fatalf("CreateSubagentRun failed: %v", err)
	}
	if rec.ID == 0 {
		t.Fatalf("expected subagent run id")
	}
	if rec.TaskRunID != taskRun.ID {
		t.Fatalf("unexpected task_run_id: got=%d want=%d", rec.TaskRunID, taskRun.ID)
	}

	byRun, err := svc.FindSubagentRunByTaskRunID(ctx, taskRun.ID)
	if err != nil {
		t.Fatalf("FindSubagentRunByTaskRunID failed: %v", err)
	}
	if byRun == nil || byRun.ID != rec.ID {
		t.Fatalf("expected find by task_run_id returns same record")
	}

	byReq, err := svc.FindSubagentRunByRequestID(ctx, "demo", "sub-req-1")
	if err != nil {
		t.Fatalf("FindSubagentRunByRequestID failed: %v", err)
	}
	if byReq == nil || byReq.ID != rec.ID {
		t.Fatalf("expected find by request_id returns same record")
	}

	rows, err := svc.ListSubagentRuns(ctx, "demo", 10)
	if err != nil {
		t.Fatalf("ListSubagentRuns failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 subagent run, got=%d", len(rows))
	}
	if rows[0].ID != rec.ID {
		t.Fatalf("unexpected list record id: got=%d want=%d", rows[0].ID, rec.ID)
	}
}

func TestService_CreateSubagentRun_DuplicateReturnsExisting(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()

	taskRun, err := svc.CreateRun(ctx, CreateRunInput{
		OwnerType:          store.TaskOwnerSubagent,
		TaskType:           store.TaskTypeSubagentRun,
		ProjectKey:         "demo",
		SubjectType:        "project",
		SubjectID:          "demo",
		RequestID:          "sub-req-dup",
		OrchestrationState: store.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	in := CreateSubagentRunInput{
		ProjectKey: "demo",
		TaskRunID:  taskRun.ID,
		RequestID:  "sub-req-dup",
		Provider:   "codex",
		Model:      "gpt-5.3-codex",
		Prompt:     "修复测试",
		CWD:        "/repo",
		RuntimeDir: "/repo/.dalek/agents/demo/1",
	}
	first, err := svc.CreateSubagentRun(ctx, in)
	if err != nil {
		t.Fatalf("first CreateSubagentRun failed: %v", err)
	}
	second, err := svc.CreateSubagentRun(ctx, in)
	if err != nil {
		t.Fatalf("second CreateSubagentRun failed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected duplicate CreateSubagentRun returns same id: first=%d second=%d", first.ID, second.ID)
	}
}
