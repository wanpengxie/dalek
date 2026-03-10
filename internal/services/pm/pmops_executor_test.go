package pm

import (
	"context"
	"testing"

	"dalek/internal/contracts"
)

func TestStartTicketPMOpExecutor_ExecutesQueuedProjection(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "pmop-start-ticket")

	res, err := startTicketPMOpExecutor{s: svc}.Execute(context.Background(), contracts.PMOp{
		Kind: contracts.PMOpStartTicket,
		Arguments: contracts.JSONMap{
			"ticket_id": tk.ID,
		},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if got := jsonMapUint(res, "ticket_id"); got != tk.ID {
		t.Fatalf("unexpected ticket_id in result: got=%d want=%d", got, tk.ID)
	}
	if got := jsonMapString(res, "workflow_status"); got != string(contracts.TicketQueued) {
		t.Fatalf("unexpected workflow_status in result: got=%q want=%q", got, contracts.TicketQueued)
	}

	var after contracts.Ticket
	if err := p.DB.First(&after, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if after.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected queued workflow after start op, got=%s", after.WorkflowStatus)
	}
}
