package runexecutor

import (
	"testing"
	"time"
)

func TestTargetSpecsFromConfig_FiltersInvalid(t *testing.T) {
	specs := TargetSpecsFromConfig(map[string]TargetConfig{
		"": {
			Description: "missing name",
			Command:     []string{"go", "test"},
		},
		"bad name": {
			Description: "spaces not allowed",
			Command:     []string{"go", "test"},
		},
		"empty-cmd": {
			Description: "no command",
			Command:     []string{},
		},
		"ok": {
			Description:        "valid",
			Command:            []string{"go", "test", "./..."},
			TimeoutMS:          1000,
			PreflightCommand:   []string{"go", "test", "./..."},
			PreflightTimeoutMS: 200,
			BootstrapCommand:   []string{"bash", "-lc", "bootstrap"},
			BootstrapTimeoutMS: 300,
			RepairCommand:      []string{"bash", "-lc", "repair"},
			RepairTimeoutMS:    400,
		},
	})
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got=%d", len(specs))
	}
	if specs[0].Name != "ok" {
		t.Fatalf("unexpected spec: %+v", specs[0])
	}
	if len(specs[0].Command) == 0 {
		t.Fatalf("expected command to be preserved")
	}
	if len(specs[0].Preflight.Command) != 3 || specs[0].Preflight.Timeout != 200*time.Millisecond {
		t.Fatalf("unexpected preflight config: %+v", specs[0].Preflight)
	}
	if len(specs[0].Bootstrap.Command) != 3 || specs[0].Bootstrap.Timeout != 300*time.Millisecond {
		t.Fatalf("unexpected bootstrap config: %+v", specs[0].Bootstrap)
	}
	if len(specs[0].Repair.Command) != 3 || specs[0].Repair.Timeout != 400*time.Millisecond {
		t.Fatalf("unexpected repair config: %+v", specs[0].Repair)
	}
}
