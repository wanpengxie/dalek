package contracts

import "testing"

func TestWorkspaceAssignment_TableName(t *testing.T) {
	if got := (WorkspaceAssignment{}).TableName(); got != "workspace_assignments" {
		t.Fatalf("unexpected table name: %s", got)
	}
}
