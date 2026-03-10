package sdkrunner

import (
	"encoding/json"
	"testing"

	agentprovider "dalek/internal/agent/provider"
	claude "github.com/wanpengxie/go-claude-agent-sdk"
	codexsdk "github.com/wanpengxie/go-codex-sdk"
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

func TestCodexSandboxMode_DefaultWorkspaceWrite(t *testing.T) {
	if got := codexSandboxMode(Request{}); got != codexsdk.SandboxWorkspaceWrite {
		t.Fatalf("expected workspace-write, got %q", got)
	}
}

func TestCodexSandboxMode_DangerFullAccess(t *testing.T) {
	if got := codexSandboxMode(Request{AgentConfig: agentprovider.AgentConfig{DangerFullAccess: true}}); got != codexsdk.SandboxDangerFullAccess {
		t.Fatalf("expected danger-full-access, got %q", got)
	}
}

func TestClaudePermissionMode_DefaultEmpty(t *testing.T) {
	if got := claudePermissionMode(Request{}); got != "" {
		t.Fatalf("expected empty permission mode, got %q", got)
	}
}

func TestClaudePermissionMode_BypassPermissions(t *testing.T) {
	if got := claudePermissionMode(Request{AgentConfig: agentprovider.AgentConfig{BypassPermissions: true}}); got != claude.PermissionBypassPermissions {
		t.Fatalf("expected bypassPermissions, got %q", got)
	}
}
