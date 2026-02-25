package tui

import (
	"strings"
	"testing"

	"dalek/internal/app"
	"dalek/internal/store"
)

func TestSummarizeQueueStatus(t *testing.T) {
	views := []app.TicketView{
		{Ticket: store.Ticket{ID: 1, Title: "待办"}, DerivedStatus: store.TicketBacklog},
		{Ticket: store.Ticket{ID: 2, Title: "排队"}, DerivedStatus: store.TicketQueued},
		{Ticket: store.Ticket{ID: 3, Title: "阻塞"}, DerivedStatus: store.TicketBlocked},
		{Ticket: store.Ticket{ID: 4, Title: "运行中"}, DerivedStatus: store.TicketActive},
		{Ticket: store.Ticket{ID: 5, Title: "完成"}, DerivedStatus: store.TicketDone},
		{Ticket: store.Ticket{ID: 6, Title: "未知状态"}, DerivedStatus: store.TicketWorkflowStatus("other")},
	}

	got := summarizeQueueStatus(views)
	if got.Total != 6 {
		t.Fatalf("Total = %d, want 6", got.Total)
	}
	if got.Backlog != 2 {
		t.Fatalf("Backlog = %d, want 2", got.Backlog)
	}
	if got.Queued != 1 {
		t.Fatalf("Queued = %d, want 1", got.Queued)
	}
	if got.Blocked != 1 {
		t.Fatalf("Blocked = %d, want 1", got.Blocked)
	}
	if got.Running != 1 {
		t.Fatalf("Running = %d, want 1", got.Running)
	}
	if got.Done != 1 {
		t.Fatalf("Done = %d, want 1", got.Done)
	}
}

func TestCollectPendingIssuesPriorityDedupAndLimit(t *testing.T) {
	views := []app.TicketView{
		{
			Ticket:             store.Ticket{ID: 10, Title: "阻塞中的任务"},
			DerivedStatus:      store.TicketBlocked,
			RuntimeHealthState: store.TaskHealthUnknown,
			RuntimeNeedsUser:   false,
		},
		{
			Ticket:             store.Ticket{ID: 11, Title: "错误任务"},
			DerivedStatus:      store.TicketActive,
			RuntimeHealthState: store.TaskHealthStalled,
			RuntimeNeedsUser:   false,
			RuntimeSummary:     "panic: boom",
		},
		{
			Ticket:             store.Ticket{ID: 12, Title: "等待确认"},
			DerivedStatus:      store.TicketActive,
			RuntimeHealthState: store.TaskHealthBusy,
			RuntimeNeedsUser:   true,
			RuntimeSummary:     "需要你确认输入",
		},
		{
			Ticket:             store.Ticket{ID: 13, Title: "多重问题"},
			DerivedStatus:      store.TicketBlocked,
			RuntimeHealthState: store.TaskHealthStalled,
			RuntimeNeedsUser:   true,
			RuntimeSummary:     "请先确认",
		},
	}

	got := collectPendingIssues(views, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if !strings.Contains(got[0], "t12 等待输入：需要你确认输入") {
		t.Fatalf("issue[0] = %q", got[0])
	}
	if !strings.Contains(got[1], "t13 等待输入：请先确认") {
		t.Fatalf("issue[1] = %q", got[1])
	}
	if !strings.Contains(got[2], "t11 运行错误：panic: boom") {
		t.Fatalf("issue[2] = %q", got[2])
	}

	all := collectPendingIssues(views, 10)
	countT13 := 0
	for _, line := range all {
		if strings.Contains(line, "t13") {
			countT13++
		}
	}
	if countT13 != 1 {
		t.Fatalf("ticket t13 should appear once, got %d", countT13)
	}
	if !containsLine(all, "t10 状态阻塞：阻塞中的任务") {
		t.Fatalf("blocked issue not found: %v", all)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}
