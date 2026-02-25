package provider

import (
	"fmt"
	"strings"
)

type CodexProvider struct {
	Command         string
	Model           string
	ReasoningEffort string
	ExtraFlags      []string
}

func (p CodexProvider) Name() string { return "codex" }

func (p CodexProvider) BuildCommand(prompt string) (string, []string) {
	command := strings.TrimSpace(p.Command)
	if command == "" {
		command = "codex"
	}
	args := make([]string, 0, 16)
	if p.Model != "" {
		args = append(args, "-m", p.Model)
	}
	if p.ReasoningEffort != "" {
		args = append(args, "-c", fmt.Sprintf(`model_reasoning_effort="%s"`, p.ReasoningEffort))
	}
	args = append(args, "exec", "--full-auto", "--color", "never")
	if len(p.ExtraFlags) > 0 {
		args = append(args, p.ExtraFlags...)
	}
	args = append(args, prompt)
	return command, args
}

func (p CodexProvider) ParseOutput(stdout string) ParsedOutput {
	return parseOutputJSONL(stdout)
}
