package agentcli

import "testing"

func TestResolveBackend_DefaultClaude(t *testing.T) {
	got := ResolveBackend(ConfigOverride{})
	if got.Provider != ProviderClaude {
		t.Fatalf("provider mismatch: %s", got.Provider)
	}
	if got.Backend.Command != "claude" {
		t.Fatalf("command mismatch: %s", got.Backend.Command)
	}
}

func TestResolveBackend_ConfigOverridesProviderAndModel(t *testing.T) {
	got := ResolveBackend(ConfigOverride{
		Provider: "codex",
		Model:    "gpt-5-codex",
	})
	if got.Provider != ProviderCodex {
		t.Fatalf("provider mismatch: %s", got.Provider)
	}
	if got.Model != "gpt-5-codex" {
		t.Fatalf("model mismatch: %s", got.Model)
	}
	if got.Backend.Command != "codex" {
		t.Fatalf("command mismatch: %s", got.Backend.Command)
	}
	if got.Backend.Output != OutputJSONL {
		t.Fatalf("output mode mismatch: %s", got.Backend.Output)
	}
	if got.Backend.SessionMode != SessionExisting {
		t.Fatalf("session mode mismatch: %s", got.Backend.SessionMode)
	}
}

func TestResolveBackend_EnvOverridesConfig(t *testing.T) {
	t.Setenv("DALEK_GATEWAY_AGENT_PROVIDER", "codex")
	t.Setenv("DALEK_GATEWAY_AGENT_MODEL", "gpt-5-env")
	t.Setenv("DALEK_GATEWAY_AGENT_COMMAND", "/tmp/env-agent")
	t.Setenv("DALEK_GATEWAY_AGENT_OUTPUT", "json")
	t.Setenv("DALEK_GATEWAY_AGENT_RESUME_OUTPUT", "text")

	got := ResolveBackend(ConfigOverride{
		Provider:     "claude",
		Model:        "gpt-5-config",
		Command:      "/tmp/config-agent",
		Output:       "jsonl",
		ResumeOutput: "json",
	})
	if got.Provider != ProviderCodex {
		t.Fatalf("provider mismatch: %s", got.Provider)
	}
	if got.Model != "gpt-5-env" {
		t.Fatalf("model mismatch: %s", got.Model)
	}
	if got.Backend.Command != "/tmp/env-agent" {
		t.Fatalf("command mismatch: %s", got.Backend.Command)
	}
	if got.Backend.Output != OutputJSON {
		t.Fatalf("output mismatch: %s", got.Backend.Output)
	}
	if got.Backend.ResumeOutput != OutputText {
		t.Fatalf("resume output mismatch: %s", got.Backend.ResumeOutput)
	}
}

func TestDefaultClaudeBackend_EnableJSONAndResume(t *testing.T) {
	backend := DefaultClaudeBackend()
	if backend.Output != OutputJSON {
		t.Fatalf("claude output should be json, got=%s", backend.Output)
	}
	if backend.SessionMode != SessionAlways {
		t.Fatalf("claude session mode should be always, got=%s", backend.SessionMode)
	}
	if len(backend.ResumeArgs) == 0 {
		t.Fatalf("claude resume args should not be empty")
	}
	if backend.SessionArg != "--session-id" {
		t.Fatalf("claude session arg mismatch: %s", backend.SessionArg)
	}
	if backend.ModelArg != "--model" {
		t.Fatalf("claude model arg mismatch: %s", backend.ModelArg)
	}
	if !hasArg(backend.Args, "--dangerously-skip-permissions") {
		t.Fatalf("claude args should include --dangerously-skip-permissions, got=%v", backend.Args)
	}
	if !hasArg(backend.ResumeArgs, "--dangerously-skip-permissions") {
		t.Fatalf("claude resume args should include --dangerously-skip-permissions, got=%v", backend.ResumeArgs)
	}
}

func TestDefaultCodexBackend_PermissionsOpen(t *testing.T) {
	backend := DefaultCodexBackend()
	if !hasArg(backend.Args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("codex args should include --dangerously-bypass-approvals-and-sandbox, got=%v", backend.Args)
	}
	if !hasArg(backend.ResumeArgs, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("codex resume args should include --dangerously-bypass-approvals-and-sandbox, got=%v", backend.ResumeArgs)
	}
	if backend.ResumeOutput != OutputJSONL {
		t.Fatalf("codex resume output should be jsonl, got=%v", backend.ResumeOutput)
	}
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func hasOrderedPair(args []string, first, second string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == first && args[i+1] == second {
			return true
		}
	}
	return false
}
