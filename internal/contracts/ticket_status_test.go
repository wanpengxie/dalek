package contracts

import "testing"

func TestCanonicalTicketWorkflowStatus(t *testing.T) {
	tests := []struct {
		name string
		in   TicketWorkflowStatus
		want TicketWorkflowStatus
	}{
		{name: "empty_to_backlog", in: "", want: TicketBacklog},
		{name: "trim_and_case", in: "  BackLog ", want: TicketBacklog},
		{name: "queue_alias", in: "queue", want: TicketQueued},
		{name: "running_alias", in: "running", want: TicketActive},
		{name: "in_progress_alias", in: "in_progress", want: TicketActive},
		{name: "wait_user_alias", in: "wait_user", want: TicketBlocked},
		{name: "completed_alias", in: "completed", want: TicketDone},
		{name: "archive_alias", in: "archive", want: TicketArchived},
		{name: "unknown_passthrough", in: "legacy_custom", want: "legacy_custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanonicalTicketWorkflowStatus(tt.in); got != tt.want {
				t.Fatalf("CanonicalTicketWorkflowStatus(%q)=%q, want=%q", tt.in, got, tt.want)
			}
		})
	}
}
