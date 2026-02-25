package agentcli

type OutputMode string

const (
	OutputText  OutputMode = "text"
	OutputJSON  OutputMode = "json"
	OutputJSONL OutputMode = "jsonl"
)

type InputMode string

const (
	InputArg   InputMode = "arg"
	InputStdin InputMode = "stdin"
)

type SessionMode string

const (
	SessionAlways   SessionMode = "always"
	SessionExisting SessionMode = "existing"
	SessionNone     SessionMode = "none"
)

type Backend struct {
	Command string

	Args       []string
	ResumeArgs []string

	Output       OutputMode
	ResumeOutput OutputMode
	Input        InputMode

	MaxPromptArgChars int

	ModelArg     string
	ModelAliases map[string]string

	SessionArg    string
	SessionArgs   []string
	SessionMode   SessionMode
	SessionFields []string
}

type Event struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	RawJSON   string `json:"raw_json,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type Result struct {
	Command string `json:"command"`

	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`

	OutputMode OutputMode `json:"output_mode"`

	Text      string `json:"text,omitempty"`
	SessionID string `json:"session_id,omitempty"`

	Events []Event `json:"events,omitempty"`
}

type RunRequest struct {
	WorkDir   string
	Prompt    string
	Model     string
	SessionID string
}

type ResolvedBackend struct {
	Provider string
	Model    string
	Backend  Backend
}

type ConfigOverride struct {
	Provider     string
	Model        string
	Command      string
	Output       string
	ResumeOutput string
}
