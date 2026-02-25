package provider

// Provider 负责为具体 agent CLI 构建命令，并在需要时解析输出。
type Provider interface {
	Name() string
	BuildCommand(prompt string) (bin string, args []string)
	ParseOutput(stdout string) ParsedOutput
}

// ParsedOutput 是对 agent 输出的轻量结构化提取结果。
type ParsedOutput struct {
	Text   string `json:"text,omitempty"`
	Events []any  `json:"events,omitempty"`
}
