package pm

import "time"

const (
	// Base env keys.
	envProjectKey        = "DALEK_PROJECT_KEY"
	envRepoRoot          = "DALEK_REPO_ROOT"
	envDBPath            = "DALEK_DB_PATH"
	envWorktreePath      = "DALEK_WORKTREE_PATH"
	envBranch            = "DALEK_BRANCH"
	envTicketID          = "DALEK_TICKET_ID"
	envWorkerID          = "DALEK_WORKER_ID"
	envTicketTitle       = "DALEK_TICKET_TITLE"
	envTicketDescription = "DALEK_TICKET_DESCRIPTION"

	// Dispatch env keys.
	dispatchDepthEnvKey = "DALEK_DISPATCH_DEPTH"
)

const (
	defaultContinuePrompt = "继续执行任务"
	closureRepairPrompt   = "上一轮执行已经结束，但当前 stage 尚未闭合。现在只允许做收口补救：检查当前代码、测试结果、`.dalek/state.json` 与 git/worktree 事实，修正缺失或矛盾信息后再次执行 `dalek worker report`。不要扩展任务范围；如果任务未完成请用 continue，如果需要人工介入请用 wait_user 并明确 blockers，只有所有 phase 已 done 且 worktree clean 时才允许上报 done。"
)

const (
	defaultWorkerReadyTimeout           = 8 * time.Second
	defaultWorkerReadyPollInterval      = 200 * time.Millisecond
	workflowStatusNotifyTimeout         = 10 * time.Second
	defaultAgentBudget                  = 10
	dispatchLeaseRenewInterval          = 10 * time.Second
	dispatchLeaseTTLBuffer              = 60 * time.Second
	dispatchLeaseTTLMin                 = 2 * time.Minute
	defaultDispatchPollInterval         = 100 * time.Millisecond
	leaseRenewalEscalateThreshold  uint = 3 // 连续失败次数达到此阈值后升级为 Error 日志

	managerStartTimeout = 5*time.Minute + 30*time.Second

	defaultZombieStallThreshold  = 10 * time.Minute
	defaultZombieMaxRetries      = 3
	defaultZombieRetryBackoff    = 60 * time.Second
	defaultZombieRetryBackoffMax = 10 * time.Minute

	defaultWorkerLoopClosureRepairAttempts = 1
)
