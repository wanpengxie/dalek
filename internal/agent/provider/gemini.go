package provider

import "strings"

type GeminiProvider struct {
	Command           string
	Model             string
	ExtraFlags        []string
	BypassPermissions bool // true → --approval-mode yolo
}

func (p GeminiProvider) Name() string { return "gemini" }

func (p GeminiProvider) BuildCommand(prompt string) (string, []string) {
	command := strings.TrimSpace(p.Command)
	if command == "" {
		command = "gemini"
	}
	args := make([]string, 0, 16)
	if p.Model != "" {
		args = append(args, "--model", p.Model)
	}
	args = append(args, "--output-format", "json")
	if p.BypassPermissions {
		args = append(args, "--approval-mode", "yolo")
	}
	if len(p.ExtraFlags) > 0 {
		args = append(args, p.ExtraFlags...)
	}
	args = append(args, "-p", prompt)
	return command, args
}

func (p GeminiProvider) ParseOutput(stdout string) ParsedOutput {
	return parseOutputJSON(stdout)
}
