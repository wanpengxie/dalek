package pm

import "time"

const (
	// Base env keys.
	envProjectKey        = "DALEK_PROJECT_KEY"
	envRepoRoot          = "DALEK_REPO_ROOT"
	envDBPath            = "DALEK_DB_PATH"
	envWorktreePath      = "DALEK_WORKTREE_PATH"
	envBranch            = "DALEK_BRANCH"
	envTmuxSocket        = "DALEK_TMUX_SOCKET"
	envTmuxSession       = "DALEK_TMUX_SESSION"
	envTicketID          = "DALEK_TICKET_ID"
	envWorkerID          = "DALEK_WORKER_ID"
	envTicketTitle       = "DALEK_TICKET_TITLE"
	envTicketDescription = "DALEK_TICKET_DESCRIPTION"

	// Dispatch env keys.
	envDispatchRequestID     = "DALEK_DISPATCH_REQUEST_ID"
	envDispatchEntryPrompt   = "DALEK_DISPATCH_ENTRY_PROMPT"
	envDispatchPromptTpl     = "DALEK_DISPATCH_PROMPT_TEMPLATE"
	dispatchDepthEnvKey      = "DALEK_DISPATCH_DEPTH"
	dispatchPromptTemplateID = "builtin://pm_dispatch_prompt_v1"
	dispatchPromptTemplate   = "templates/pm/dispatch_prompt_v1.tmpl"
)

const (
	defaultContinuePrompt = "继续执行任务"
)

const (
	defaultWorkerReadyTimeout      = 8 * time.Second
	defaultWorkerReadyPollInterval = 200 * time.Millisecond
	workflowStatusNotifyTimeout    = 10 * time.Second

	dispatchLeaseRenewInterval  = 10 * time.Second
	dispatchLeaseTTLBuffer      = 60 * time.Second
	dispatchLeaseTTLMin         = 2 * time.Minute
	defaultDispatchPollInterval = 100 * time.Millisecond

	managerStartTimeout      = 5*time.Minute + 30*time.Second
	tmuxListSessionsTimeout  = 2 * time.Second
	tmuxNewSessionTimeout    = 5 * time.Second
	tmuxObserveTargetTimeout = 5 * time.Second
)
