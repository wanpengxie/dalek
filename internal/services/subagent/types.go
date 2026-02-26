package subagent

type SubmitInput struct {
	RequestID string
	Provider  string
	Model     string
	Prompt    string
}

type SubmitResult struct {
	Accepted bool

	TaskRunID  uint
	RequestID  string
	Provider   string
	Model      string
	RuntimeDir string
}

type RunInput struct {
	RunnerID string
}
