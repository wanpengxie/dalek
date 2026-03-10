package pm

import (
	"testing"

	"dalek/internal/contracts"
)

func TestParsePlannerPMOps_MarkerPayload(t *testing.T) {
	raw := `
分析结果如下：
<pmops>
{
  "ops": [
    {
      "kind": "create_ticket",
      "idempotency_key": "create-ticket:test",
      "arguments": {"title": "pmops parser test"}
    }
  ]
}
</pmops>
`
	ops, err := parsePlannerPMOps(raw, 12, "req_1")
	if err != nil {
		t.Fatalf("parsePlannerPMOps failed: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got=%d", len(ops))
	}
	op := ops[0]
	if op.Kind != contracts.PMOpCreateTicket {
		t.Fatalf("expected kind=create_ticket, got=%s", op.Kind)
	}
	if op.OpID == "" {
		t.Fatalf("expected op_id generated")
	}
	if op.RequestID != "req_1" {
		t.Fatalf("expected request_id=req_1, got=%q", op.RequestID)
	}
	if op.IdempotencyKey != "create-ticket:test" {
		t.Fatalf("expected idempotency key preserved, got=%q", op.IdempotencyKey)
	}
}

func TestParsePlannerPMOps_PlainTextWithoutJSONReturnsEmpty(t *testing.T) {
	ops, err := parsePlannerPMOps("planner done without json", 7, "req_2")
	if err != nil {
		t.Fatalf("parsePlannerPMOps should not fail: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("expected no ops parsed, got=%d", len(ops))
	}
}

func TestPlannerPMOpIsCritical_StartTicket(t *testing.T) {
	if !plannerPMOpIsCritical(contracts.PMOp{Kind: contracts.PMOpStartTicket}) {
		t.Fatalf("expected start_ticket marked as critical")
	}
}
