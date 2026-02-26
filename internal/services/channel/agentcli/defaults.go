package agentcli

import (
	"os"
	"strings"
)

const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

func ResolveBackend(cfg ConfigOverride) ResolvedBackend {
	provider := normalizeProvider(cfg.Provider)
	if envProvider := os.Getenv("DALEK_GATEWAY_AGENT_PROVIDER"); envProvider != "" {
		provider = normalizeProvider(envProvider)
	}

	var backend Backend
	switch provider {
	case ProviderCodex:
		backend = DefaultCodexBackend()
	default:
		backend = DefaultClaudeBackend()
		provider = ProviderClaude
	}

	if cmd := cfg.Command; cmd != "" {
		backend.Command = cmd
	}
	if output := parseOutputMode(cfg.Output); output != "" {
		backend.Output = output
	}
	if resumeOutput := parseOutputMode(cfg.ResumeOutput); resumeOutput != "" {
		backend.ResumeOutput = resumeOutput
	}
	backend = applyEnvOverrides(backend)

	model := cfg.Model
	if envModel := os.Getenv("DALEK_GATEWAY_AGENT_MODEL"); envModel != "" {
		model = envModel
	}

	return ResolvedBackend{
		Provider: provider,
		Model:    model,
		Backend:  backend,
	}
}

func ResolveBackendFromEnv() ResolvedBackend {
	return ResolveBackend(ConfigOverride{})
}

func DefaultClaudeBackend() Backend {
	return applyEnvOverrides(Backend{
		Command: "claude",
		Args:    []string{"-p", "--output-format", "json", "--dangerously-skip-permissions"},
		ResumeArgs: []string{
			"-p", "--output-format", "json", "--dangerously-skip-permissions",
			"--resume", "{sessionId}",
		},

		Output: OutputJSON,
		// resume 继续使用 JSON，保证 session_id 可持续提取。
		ResumeOutput: OutputJSON,
		Input:        InputArg,

		ModelArg:    "--model",
		SessionArg:  "--session-id",
		SessionMode: SessionAlways,

		SessionFields: defaultSessionFields(),
	})
}

func DefaultCodexBackend() Backend {
	return applyEnvOverrides(Backend{
		Command: "codex",
		Args: []string{
			"exec",
			"--json",
			"--dangerously-bypass-approvals-and-sandbox",
			"--skip-git-repo-check",
		},
		ResumeArgs: []string{
			"exec",
			"resume",
			"--json",
			"--dangerously-bypass-approvals-and-sandbox",
			"--skip-git-repo-check",
			"{sessionId}",
		},

		Output:       OutputJSONL,
		ResumeOutput: OutputJSONL,
		Input:        InputArg,

		ModelArg:      "--model",
		SessionMode:   SessionExisting,
		SessionFields: defaultSessionFields(),
	})
}

func defaultSessionFields() []string {
	return []string{
		"session_id",
		"sessionId",
		"conversation_id",
		"conversationId",
		"thread_id",
	}
}

func applyEnvOverrides(in Backend) Backend {
	out := in
	if cmd := os.Getenv("DALEK_GATEWAY_AGENT_COMMAND"); cmd != "" {
		out.Command = cmd
	}
	if output := parseOutputMode(os.Getenv("DALEK_GATEWAY_AGENT_OUTPUT")); output != "" {
		out.Output = output
	}
	if resumeOutput := parseOutputMode(os.Getenv("DALEK_GATEWAY_AGENT_RESUME_OUTPUT")); resumeOutput != "" {
		out.ResumeOutput = resumeOutput
	}
	return out
}

func normalizeProvider(raw string) string {
	switch strings.ToLower(raw) {
	case "claude", "claude-cli":
		return ProviderClaude
	case "codex", "codex-cli":
		return ProviderCodex
	default:
		return ProviderClaude
	}
}

func parseOutputMode(raw string) OutputMode {
	switch strings.ToLower(raw) {
	case string(OutputText):
		return OutputText
	case string(OutputJSON):
		return OutputJSON
	case string(OutputJSONL):
		return OutputJSONL
	default:
		return ""
	}
}
