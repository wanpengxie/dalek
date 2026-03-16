package contracts

import "testing"

func TestSnapshot_TableName(t *testing.T) {
	if got := (Snapshot{}).TableName(); got != "snapshots" {
		t.Fatalf("unexpected table name: %q", got)
	}
}
