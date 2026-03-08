package sdkrunner

import (
	"encoding/json"
	"testing"
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
