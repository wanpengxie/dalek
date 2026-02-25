package provider

import "strings"

type ClaudeProvider struct {
	Command    string
	Model      string
	ExtraFlags []string
}

func (p ClaudeProvider) Name() string { return "claude" }

func (p ClaudeProvider) BuildCommand(prompt string) (string, []string) {
	command := strings.TrimSpace(p.Command)
	if command == "" {
		command = "claude"
	}
	args := []string{
		"-p",
		"--output-format", "json",
		"--dangerously-skip-permissions",
	}
	if p.Model != "" {
		args = append(args, "--model", p.Model)
	}
	if len(p.ExtraFlags) > 0 {
		args = append(args, p.ExtraFlags...)
	}
	args = append(args, prompt)
	return command, args
}

func (p ClaudeProvider) ParseOutput(stdout string) ParsedOutput {
	return parseOutputJSON(stdout)
}
