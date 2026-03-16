package runexecutor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/infra"
)

type Executor interface {
	Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
}

type ExecuteRequest struct {
	ProjectKey   string
	RunID        uint
	TaskRunID    uint
	TargetName   string
	Stage        Stage
	WorkspaceDir string
	SnapshotID   string
	BaseCommit   string
	Attempt      int
}

type ExecuteResult struct {
	Accepted     bool
	Stage        Stage
	Target       TargetSpec
	Command      []string
	WorkspaceDir string
	SnapshotID   string
	BaseCommit   string
	Summary      string
	StartedAt    time.Time
	FinishedAt   time.Time
}

type StubExecutor struct {
	targets TargetCatalog
	now     func() time.Time
}

func NewStubExecutor(targets TargetCatalog) *StubExecutor {
	if targets == nil {
		targets = New(nil)
	}
	return &StubExecutor{
		targets: targets,
		now:     time.Now,
	}
}

type LocalExecutor struct {
	targets TargetCatalog
	runner  infra.CommandRunner
	now     func() time.Time
}

func NewLocalExecutor(targets TargetCatalog, runner infra.CommandRunner) *LocalExecutor {
	if targets == nil {
		targets = New(nil)
	}
	if runner == nil {
		runner = infra.NewExecRunner()
	}
	return &LocalExecutor{
		targets: targets,
		runner:  runner,
		now:     time.Now,
	}
}

func (e *StubExecutor) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if e == nil || e.targets == nil {
		return ExecuteResult{}, fmt.Errorf("run executor 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ExecuteResult{}, err
	}
	stage, err := normalizeStage(req.Stage)
	if err != nil {
		return ExecuteResult{}, err
	}
	target, err := e.targets.ResolveTarget(req.TargetName)
	if err != nil {
		return ExecuteResult{}, err
	}
	now := e.now()
	command := selectStageCommand(stage, target)
	if len(command) == 0 && stage == StageVerify {
		return ExecuteResult{}, fmt.Errorf("verify_target 未配置命令: %s", target.Name)
	}
	return ExecuteResult{
		Accepted:     true,
		Stage:        stage,
		Target:       target,
		Command:      append([]string(nil), command...),
		WorkspaceDir: strings.TrimSpace(req.WorkspaceDir),
		SnapshotID:   strings.TrimSpace(req.SnapshotID),
		BaseCommit:   strings.TrimSpace(req.BaseCommit),
		Summary:      stageSummary(stage, target.Name),
		StartedAt:    now,
		FinishedAt:   now,
	}, nil
}

func normalizeStage(stage Stage) (Stage, error) {
	stage = Stage(strings.TrimSpace(strings.ToLower(string(stage))))
	switch stage {
	case StagePreflight, StageBootstrap, StageVerify, StageRepair:
		return stage, nil
	default:
		return "", fmt.Errorf("run executor stage 非法: %s", strings.TrimSpace(string(stage)))
	}
}

func stageSummary(stage Stage, targetName string) string {
	switch stage {
	case StagePreflight:
		return "preflight accepted for target=" + targetName
	case StageBootstrap:
		return "bootstrap accepted for target=" + targetName
	case StageRepair:
		return "repair accepted for target=" + targetName
	default:
		return "verify accepted for target=" + targetName
	}
}

func (e *LocalExecutor) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if e == nil || e.targets == nil || e.runner == nil {
		return ExecuteResult{}, fmt.Errorf("run executor 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ExecuteResult{}, err
	}
	stage, err := normalizeStage(req.Stage)
	if err != nil {
		return ExecuteResult{}, err
	}
	target, err := e.targets.ResolveTarget(req.TargetName)
	if err != nil {
		return ExecuteResult{}, err
	}
	command := selectStageCommand(stage, target)
	if len(command) == 0 {
		if stage != StageVerify {
			now := e.now()
			return ExecuteResult{
				Accepted:     true,
				Stage:        stage,
				Target:       target,
				Command:      nil,
				WorkspaceDir: strings.TrimSpace(req.WorkspaceDir),
				SnapshotID:   strings.TrimSpace(req.SnapshotID),
				BaseCommit:   strings.TrimSpace(req.BaseCommit),
				Summary:      stageSkippedSummary(stage, target.Name),
				StartedAt:    now,
				FinishedAt:   now,
			}, nil
		}
		return ExecuteResult{}, fmt.Errorf("verify_target 未配置命令: %s", target.Name)
	}

	execCtx := ctx
	cancel := func() {}
	timeout := selectStageTimeout(stage, target)
	if timeout > 0 {
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining > 0 && remaining < timeout {
				execCtx, cancel = context.WithTimeout(ctx, remaining)
			} else {
				execCtx, cancel = context.WithTimeout(ctx, timeout)
			}
		} else {
			execCtx, cancel = context.WithTimeout(ctx, timeout)
		}
	}
	defer cancel()

	now := e.now()
	exitCode, stdout, stderr, runErr := e.runner.RunExitCode(execCtx, strings.TrimSpace(req.WorkspaceDir), command[0], command[1:]...)
	finished := e.now()
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) {
			return ExecuteResult{}, fmt.Errorf("run executor 超时: %w", runErr)
		}
		if errors.Is(runErr, context.Canceled) {
			return ExecuteResult{}, fmt.Errorf("run executor 已取消: %w", runErr)
		}
		return ExecuteResult{}, fmt.Errorf("run executor 执行失败: %w", runErr)
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg != "" {
			msg = ": " + msg
		}
		return ExecuteResult{}, fmt.Errorf("run executor exit=%d%s", exitCode, msg)
	}

	summary := fmt.Sprintf("%s (exit=%d)", stageSummary(stage, target.Name), exitCode)
	if stdout != "" {
		summary = strings.TrimSpace(summary + " " + strings.TrimSpace(stdout))
	}

	return ExecuteResult{
		Accepted:     true,
		Stage:        stage,
		Target:       target,
		Command:      append([]string(nil), command...),
		WorkspaceDir: strings.TrimSpace(req.WorkspaceDir),
		SnapshotID:   strings.TrimSpace(req.SnapshotID),
		BaseCommit:   strings.TrimSpace(req.BaseCommit),
		Summary:      summary,
		StartedAt:    now,
		FinishedAt:   finished,
	}, nil
}

func selectStageCommand(stage Stage, target TargetSpec) []string {
	switch stage {
	case StageBootstrap:
		if len(target.Bootstrap.Command) > 0 {
			return target.Bootstrap.Command
		}
	case StageRepair:
		if len(target.Repair.Command) > 0 {
			return target.Repair.Command
		}
	case StagePreflight:
		if len(target.Preflight.Command) > 0 {
			return target.Preflight.Command
		}
	case StageVerify:
		return target.Command
	}
	return nil
}

func selectStageTimeout(stage Stage, target TargetSpec) time.Duration {
	switch stage {
	case StageBootstrap:
		if target.Bootstrap.Timeout > 0 {
			return target.Bootstrap.Timeout
		}
	case StageRepair:
		if target.Repair.Timeout > 0 {
			return target.Repair.Timeout
		}
	case StagePreflight:
		if target.Preflight.Timeout > 0 {
			return target.Preflight.Timeout
		}
	case StageVerify:
		return target.Timeout
	}
	return target.Timeout
}

func stageSkippedSummary(stage Stage, targetName string) string {
	switch stage {
	case StagePreflight:
		return "preflight skipped (no command) for target=" + targetName
	case StageBootstrap:
		return "bootstrap skipped (no command) for target=" + targetName
	case StageRepair:
		return "repair skipped (no command) for target=" + targetName
	default:
		return "verify skipped (no command) for target=" + targetName
	}
}
