package sdkrunner

import (
	"context"
	"encoding/json"
	"testing"

	claude "github.com/wanpengxie/go-claude-agent-sdk"
)

func TestClaudeRunnerSettingsJSON_ValidJSON(t *testing.T) {
	raw := ClaudeRunnerSettingsJSON()
	if raw == "" {
		t.Fatalf("settings json should not be empty")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("settings json should be valid: %v", err)
	}

	if _, ok := parsed["permissions"]; !ok {
		t.Fatalf("settings should include permissions")
	}
}

func TestAutoApproveClaudeTool_Allows(t *testing.T) {
	result, err := autoApproveClaudeTool(context.Background(), "bash", map[string]any{
		"command": "cd /tmp/demo && git status --short",
	}, claude.ToolPermissionContext{})
	if err != nil {
		t.Fatalf("auto approve should not error: %v", err)
	}
	if _, ok := result.(*claude.PermissionResultAllow); !ok {
		t.Fatalf("expected allow result, got %T", result)
	}
}
