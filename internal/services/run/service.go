package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	runexecsvc "dalek/internal/services/runexecutor"
	snapshotsvc "dalek/internal/services/snapshot"
	tasksvc "dalek/internal/services/task"

	"gorm.io/gorm"
)

type Service struct {
	db      *gorm.DB
	task    *tasksvc.Service
	targets runexecsvc.TargetCatalog
	exec    runexecsvc.Executor
	snap    *snapshotsvc.ApplyService
	sched   *RunScheduler
	now     func() time.Time
}

type SubmitInput struct {
	ProjectKey                string
	TicketID                  uint
	RequestID                 string
	VerifyTarget              string
	SnapshotID                string
	BaseCommit                string
	SourceWorkspaceGeneration string
}

type SubmitResult struct {
	Accepted                  bool
	RunID                     uint
	TaskRunID                 uint
	RequestID                 string
	RunStatus                 contracts.RunStatus
	VerifyTarget              string
	SnapshotID                string
	BaseCommit                string
	SourceWorkspaceGeneration string
}

type CancelResult = tasksvc.CancelRunResult

func New(db *gorm.DB, task *tasksvc.Service, targets runexecsvc.TargetCatalog, snap *snapshotsvc.ApplyService) *Service {
	if targets == nil {
		targets = runexecsvc.New(nil)
	}
		return &Service{
			db:      db,
			task:    task,
			targets: targets,
			exec:    runexecsvc.NewStubExecutor(targets),
			snap:    snap,
			sched:   NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"local": 4}}),
			now:     time.Now,
		}
}

func (s *Service) Submit(ctx context.Context, in SubmitInput) (SubmitResult, error) {
	if s == nil || s.db == nil || s.task == nil {
		return SubmitResult{}, fmt.Errorf("run service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectKey := strings.TrimSpace(in.ProjectKey)
	if projectKey == "" {
		return SubmitResult{}, fmt.Errorf("project_key 不能为空")
	}
	verifyTarget := strings.TrimSpace(in.VerifyTarget)
	if verifyTarget == "" {
		return SubmitResult{}, fmt.Errorf("verify_target 不能为空")
	}
	targetSpec, err := s.targets.ResolveTarget(verifyTarget)
	if err != nil {
		return SubmitResult{}, err
	}
	verifyTarget = targetSpec.Name
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("run_%d", s.now().UnixNano())
	}

	existingRun, err := s.task.FindRunByRequestID(ctx, requestID)
	if err != nil {
		return SubmitResult{}, err
	}
	if existingRun != nil {
		if existingRun.OwnerType != contracts.TaskOwnerPM || strings.TrimSpace(existingRun.TaskType) != contracts.TaskTypeRunVerify {
			return SubmitResult{}, fmt.Errorf("request_id 已被其他任务占用: %s", requestID)
		}
		existingView, verr := s.Get(ctx, existingRun.ID)
		if verr != nil {
			return SubmitResult{}, verr
		}
		if existingView == nil {
			return SubmitResult{}, fmt.Errorf("run_view 不存在: run_id=%d", existingRun.ID)
		}
		return SubmitResult{
			Accepted:                  true,
			RunID:                     existingView.RunID,
			TaskRunID:                 existingView.TaskRunID,
			RequestID:                 existingView.RequestID,
			RunStatus:                 existingView.RunStatus,
			VerifyTarget:              existingView.VerifyTarget,
			SnapshotID:                existingView.SnapshotID,
			BaseCommit:                existingView.BaseCommit,
			SourceWorkspaceGeneration: existingView.SourceWorkspaceGeneration,
		}, nil
	}

	runStatus := contracts.RunRequested
	snapshotID := strings.TrimSpace(in.SnapshotID)
	baseCommit := strings.TrimSpace(in.BaseCommit)
	sourceWorkspaceGeneration := strings.TrimSpace(in.SourceWorkspaceGeneration)
	workspaceDir := ""
	if snapshotID != "" {
		if s.snap == nil {
			return SubmitResult{}, fmt.Errorf("snapshot apply service 未初始化")
		}
		attached, err := s.snap.AttachReadySnapshot(ctx, snapshotsvc.AttachInput{
			SnapshotID: snapshotID,
			BaseCommit: baseCommit,
		})
		if err != nil {
			return SubmitResult{}, err
		}
		snapshotID = attached.SnapshotID
		baseCommit = attached.BaseCommit
		if sourceWorkspaceGeneration == "" {
			sourceWorkspaceGeneration = attached.WorkspaceGeneration
		}
		if s.snap.Catalog() == nil {
			return SubmitResult{}, fmt.Errorf("snapshot catalog 未初始化")
		}
		if err := s.snap.Catalog().AcquireReference(ctx, snapshotID); err != nil {
			return SubmitResult{}, err
		}
		runStatus = contracts.RunSnapshotReady
	}

	requestPayload, _ := json.Marshal(map[string]any{
		"verify_target": verifyTarget,
		"target_spec": map[string]any{
			"name":        targetSpec.Name,
			"description": targetSpec.Description,
			"command":     append([]string(nil), targetSpec.Command...),
			"timeout_ms":  targetSpec.Timeout.Milliseconds(),
		},
		"snapshot_id":                 snapshotID,
		"base_commit":                 baseCommit,
		"source_workspace_generation": sourceWorkspaceGeneration,
	})
	createdRun, err := s.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeRunVerify,
		ProjectKey:         projectKey,
		TicketID:           in.TicketID,
		SubjectType:        "run",
		SubjectID:          verifyTarget,
		RequestID:          requestID,
		OrchestrationState: contracts.TaskPending,
		RequestPayloadJSON: string(requestPayload),
	})
	if err != nil {
		return SubmitResult{}, err
	}
	if createdRun.OwnerType != contracts.TaskOwnerPM || strings.TrimSpace(createdRun.TaskType) != contracts.TaskTypeRunVerify {
		return SubmitResult{}, fmt.Errorf("request_id 已被其他任务占用: %s", requestID)
	}

	view := contracts.RunView{
		RunID:                     createdRun.ID,
		TaskRunID:                 createdRun.ID,
		ProjectKey:                projectKey,
		RequestID:                 requestID,
		TicketID:                  in.TicketID,
		WorkerID:                  createdRun.WorkerID,
		RunStatus:                 runStatus,
		VerifyTarget:              verifyTarget,
		SnapshotID:                snapshotID,
		BaseCommit:                baseCommit,
		SourceWorkspaceGeneration: sourceWorkspaceGeneration,
	}
	if err := s.db.WithContext(ctx).Create(&view).Error; err != nil {
		return SubmitResult{}, err
	}

	now := s.now()
	_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: createdRun.ID,
		EventType: "run_requested",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskPending,
			"run_status":          contracts.RunRequested,
		},
		Note:      "run verify requested",
		CreatedAt: now,
	})
	_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  createdRun.ID,
		State:      contracts.TaskHealthIdle,
		NeedsUser:  false,
		Summary:    "run requested",
		Source:     "run.submit",
		ObservedAt: now,
	})
	_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  createdRun.ID,
		Phase:      contracts.TaskPhasePlanning,
		Milestone:  "run_requested",
		NextAction: "continue",
		Summary:    "run accepted",
		ReportedAt: now,
	})
	if snapshotID != "" {
		_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: createdRun.ID,
			EventType: "run_snapshot_ready",
			ToState: map[string]any{
				"orchestration_state": contracts.TaskPending,
				"run_status":          contracts.RunSnapshotReady,
				"snapshot_id":         snapshotID,
			},
			Note:      "snapshot attached: " + snapshotID,
			CreatedAt: now,
			Payload: map[string]any{
				"snapshot_id":                 snapshotID,
				"base_commit":                 baseCommit,
				"source_workspace_generation": sourceWorkspaceGeneration,
			},
		})
		_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
			TaskRunID:  createdRun.ID,
			State:      contracts.TaskHealthIdle,
			NeedsUser:  false,
			Summary:    "snapshot ready: " + snapshotID,
			Source:     "run.snapshot",
			ObservedAt: now,
			Metrics: map[string]any{
				"snapshot_id": snapshotID,
				"base_commit": baseCommit,
			},
		})
		_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
			TaskRunID:  createdRun.ID,
			Phase:      contracts.TaskPhasePlanning,
			Milestone:  "snapshot_ready",
			NextAction: string(contracts.NextContinue),
			Summary:    "snapshot ready: " + snapshotID,
			ReportedAt: now,
			Payload: map[string]any{
				"snapshot_id": snapshotID,
			},
		})
		applyRes, err := s.snap.ApplyToWorkspace(ctx, snapshotsvc.WorkspaceApplyInput{
			SnapshotID: snapshotID,
			BaseCommit: baseCommit,
		})
		if err != nil {
			_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
				TaskRunID: createdRun.ID,
				EventType: "run_snapshot_apply_failed",
				ToState: map[string]any{
					"orchestration_state": contracts.TaskPending,
					"run_status":          contracts.RunSnapshotReady,
					"snapshot_id":         snapshotID,
				},
				Note:      strings.TrimSpace(err.Error()),
				CreatedAt: now,
			})
			_ = s.snap.Catalog().ReleaseReference(ctx, snapshotID)
			return SubmitResult{}, err
		}
		_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: createdRun.ID,
			EventType: "run_snapshot_apply_accepted",
			ToState: map[string]any{
				"orchestration_state": contracts.TaskPending,
				"run_status":          contracts.RunSnapshotReady,
				"snapshot_id":         snapshotID,
			},
			Note:      applyRes.Summary,
			CreatedAt: now,
			Payload: map[string]any{
				"snapshot_id":          snapshotID,
				"manifest_digest":      applyRes.ManifestDigest,
				"applied_file_count":   applyRes.AppliedFileCount,
				"workspace_dir":        applyRes.WorkspaceDir,
				"plan_path":            applyRes.PlanPath,
				"workspace_generation": applyRes.WorkspaceGeneration,
			},
		})
		_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
			TaskRunID:  createdRun.ID,
			State:      contracts.TaskHealthIdle,
			NeedsUser:  false,
			Summary:    applyRes.Summary,
			Source:     "run.snapshot.apply",
			ObservedAt: now,
			Metrics: map[string]any{
				"snapshot_id":        snapshotID,
				"manifest_digest":    applyRes.ManifestDigest,
				"applied_file_count": applyRes.AppliedFileCount,
				"workspace_dir":      applyRes.WorkspaceDir,
			},
		})
		_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
			TaskRunID:  createdRun.ID,
			Phase:      contracts.TaskPhasePlanning,
			Milestone:  "snapshot_apply_accepted",
			NextAction: string(contracts.NextContinue),
			Summary:    applyRes.Summary,
			ReportedAt: now,
			Payload: map[string]any{
				"snapshot_id": snapshotID,
			},
		})
		workspaceDir = applyRes.WorkspaceDir
	}
		workspaceKey := deriveWorkspaceKey(createdRun.ID, projectKey, snapshotID, sourceWorkspaceGeneration, workspaceDir)
		if s.sched != nil {
			if err := s.sched.Acquire(ctx, "local", createdRun.ID, projectKey, workspaceKey); err != nil {
				return SubmitResult{}, err
			}
			defer s.sched.Finish(createdRun.ID)
		}
	s.recordPreparation(ctx, createdRun.ID, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir, now)
	s.recordExecution(ctx, createdRun.ID, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir, now)
	if latest, err := s.Get(ctx, createdRun.ID); err == nil && latest != nil {
		view = *latest
	}

	return SubmitResult{
		Accepted:                  true,
		RunID:                     view.RunID,
		TaskRunID:                 view.TaskRunID,
		RequestID:                 view.RequestID,
		RunStatus:                 view.RunStatus,
		VerifyTarget:              view.VerifyTarget,
		SnapshotID:                view.SnapshotID,
		BaseCommit:                view.BaseCommit,
		SourceWorkspaceGeneration: view.SourceWorkspaceGeneration,
	}, nil
}

func (s *Service) Cancel(ctx context.Context, runID uint) (CancelResult, error) {
	if s == nil || s.task == nil {
		return CancelResult{}, fmt.Errorf("run service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return CancelResult{}, fmt.Errorf("run_id 不能为空")
	}
	view, err := s.Get(ctx, runID)
	if err != nil {
		return CancelResult{}, err
	}
	now := s.now()
	res, err := s.task.CancelRun(ctx, runID, now)
	if err != nil {
		return CancelResult{}, err
	}
	if !res.Canceled {
		return res, nil
	}
	_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "run_canceled",
		FromState: map[string]any{
			"orchestration_state": res.FromState,
		},
		ToState: map[string]any{
			"orchestration_state": contracts.TaskCanceled,
			"run_status":          contracts.RunCanceled,
		},
		Note:      strings.TrimSpace(res.Reason),
		CreatedAt: now,
	})
	_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runID,
		State:      contracts.TaskHealthIdle,
		NeedsUser:  false,
		Summary:    "run canceled",
		Source:     "run.cancel",
		ObservedAt: now,
		Metrics: map[string]any{
			"reason": strings.TrimSpace(res.Reason),
		},
	})
	_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  runID,
		Phase:      contracts.TaskPhaseDone,
		Milestone:  "run_canceled",
		NextAction: string(contracts.NextContinue),
		Summary:    strings.TrimSpace(res.Reason),
		ReportedAt: now,
	})
	s.releaseSnapshotReference(ctx, strings.TrimSpace(view.SnapshotID))
	return res, nil
}

func (s *Service) RecordArtifactFailure(ctx context.Context, runID uint, artifactName, reason string) error {
	if s == nil || s.task == nil {
		return fmt.Errorf("run service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	view, err := s.Get(ctx, runID)
	if err != nil {
		return err
	}
	if view == nil {
		return fmt.Errorf("run 不存在: %d", runID)
	}
	status, err := s.task.GetStatusByRunID(ctx, runID)
	if err != nil {
		return err
	}
	artifactName = strings.TrimSpace(artifactName)
	reason = strings.TrimSpace(reason)
	now := s.now()
	if status != nil && status.LastEventAt != nil && !status.LastEventAt.IsZero() && !now.After(*status.LastEventAt) {
		now = status.LastEventAt.Add(time.Millisecond)
	}
	if err := s.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "run_artifact_upload_failed",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskPending,
			"run_status":          view.RunStatus,
		},
		Note:      reason,
		CreatedAt: now,
		Payload: map[string]any{
			"artifact_name": artifactName,
			"reason":        reason,
		},
	}); err != nil {
		return err
	}
	return nil
}

func (s *Service) recordPreparation(ctx context.Context, runID uint, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir string, observedAt time.Time) {
	s.recordStageAccepted(ctx, runID, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir, runexecsvc.StageBootstrap, observedAt)
	s.recordStageAccepted(ctx, runID, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir, runexecsvc.StageRepair, observedAt)
	s.recordStageAccepted(ctx, runID, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir, runexecsvc.StagePreflight, observedAt)
	s.recordStageAccepted(ctx, runID, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir, runexecsvc.StageVerify, observedAt)
}

func (s *Service) recordExecution(ctx context.Context, runID uint, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir string, observedAt time.Time) {
	if s == nil || s.task == nil || s.exec == nil {
		return
	}
	lease := observedAt.Add(2 * time.Minute)
	runnerID := "runexec-stub"
	if err := s.task.MarkRunRunning(ctx, runID, runnerID, &lease, observedAt, true); err != nil {
		return
	}
	_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "run_verify_started",
		FromState: map[string]any{
			"orchestration_state": contracts.TaskPending,
			"run_status":          contracts.RunReadyToRun,
		},
		ToState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
			"run_status":          contracts.RunRunning,
		},
		Note:      "verify started",
		CreatedAt: observedAt,
		Payload: map[string]any{
			"workspace_dir": workspaceDir,
			"snapshot_id":   snapshotID,
			"base_commit":   baseCommit,
			"runner_id":     runnerID,
		},
	})
	_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runID,
		State:      contracts.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "verify started",
		Source:     "run.verify",
		ObservedAt: observedAt,
		Metrics: map[string]any{
			"workspace_dir": workspaceDir,
			"snapshot_id":   snapshotID,
		},
	})
	_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  runID,
		Phase:      contracts.TaskPhaseTesting,
		Milestone:  "verify_started",
		NextAction: string(contracts.NextContinue),
		Summary:    "verify started",
		ReportedAt: observedAt,
		Payload: map[string]any{
			"workspace_dir": workspaceDir,
			"snapshot_id":   snapshotID,
		},
	})
	res, err := s.exec.Execute(ctx, runexecsvc.ExecuteRequest{
		ProjectKey:   projectKey,
		RunID:        runID,
		TaskRunID:    runID,
		TargetName:   verifyTarget,
		Stage:        runexecsvc.StageVerify,
		WorkspaceDir: workspaceDir,
		SnapshotID:   snapshotID,
		BaseCommit:   baseCommit,
		Attempt:      1,
	})
	finishedAt := observedAt.Add(time.Second)
	if err != nil {
		_ = s.task.MarkRunFailed(ctx, runID, "verify_failed", strings.TrimSpace(err.Error()), finishedAt)
		_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: runID,
			EventType: "run_verify_failed",
			FromState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
				"run_status":          contracts.RunRunning,
			},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskFailed,
				"run_status":          contracts.RunFailed,
			},
			Note:      strings.TrimSpace(err.Error()),
			CreatedAt: finishedAt,
			Payload: map[string]any{
				"workspace_dir": workspaceDir,
				"snapshot_id":   snapshotID,
				"base_commit":   baseCommit,
				"runner_id":     runnerID,
			},
		})
		_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
			TaskRunID:  runID,
			State:      contracts.TaskHealthDead,
			NeedsUser:  false,
			Summary:    "verify failed",
			Source:     "run.verify",
			ObservedAt: finishedAt,
			Metrics: map[string]any{
				"error":         strings.TrimSpace(err.Error()),
				"workspace_dir": workspaceDir,
				"snapshot_id":   snapshotID,
			},
		})
		_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
			TaskRunID:  runID,
			Phase:      contracts.TaskPhaseBlocked,
			Milestone:  "verify_failed",
			NextAction: string(contracts.NextWaitUser),
			Summary:    strings.TrimSpace(err.Error()),
			ReportedAt: finishedAt,
			Payload: map[string]any{
				"workspace_dir": workspaceDir,
				"snapshot_id":   snapshotID,
				"base_commit":   baseCommit,
			},
		})
		s.releaseSnapshotReference(ctx, snapshotID)
		return
	}
	resultPayload, _ := json.Marshal(map[string]any{
		"stage":         res.Stage,
		"verify_target": res.Target.Name,
		"command":       append([]string(nil), res.Command...),
		"workspace_dir": res.WorkspaceDir,
		"snapshot_id":   res.SnapshotID,
		"base_commit":   res.BaseCommit,
	})
	_ = s.task.MarkRunSucceeded(ctx, runID, string(resultPayload), finishedAt)
	_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "run_verify_succeeded",
		FromState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
			"run_status":          contracts.RunRunning,
		},
		ToState: map[string]any{
			"orchestration_state": contracts.TaskSucceeded,
			"run_status":          contracts.RunSucceeded,
		},
		Note:      res.Summary,
		CreatedAt: finishedAt,
		Payload: map[string]any{
			"workspace_dir": res.WorkspaceDir,
			"snapshot_id":   res.SnapshotID,
			"base_commit":   res.BaseCommit,
		},
	})
	_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runID,
		State:      contracts.TaskHealthIdle,
		NeedsUser:  false,
		Summary:    res.Summary,
		Source:     "run.verify",
		ObservedAt: finishedAt,
		Metrics: map[string]any{
			"workspace_dir": res.WorkspaceDir,
			"snapshot_id":   res.SnapshotID,
		},
	})
	_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  runID,
		Phase:      contracts.TaskPhaseDone,
		Milestone:  "verify_succeeded",
		NextAction: string(contracts.NextContinue),
		Summary:    res.Summary,
		ReportedAt: finishedAt,
		Payload: map[string]any{
			"workspace_dir": res.WorkspaceDir,
			"snapshot_id":   res.SnapshotID,
		},
	})
	s.releaseSnapshotReference(ctx, snapshotID)
}

func deriveWorkspaceKey(runID uint, projectKey, snapshotID, workspaceGeneration, workspaceDir string) string {
	switch {
	case strings.TrimSpace(workspaceGeneration) != "":
		return "wg:" + strings.TrimSpace(workspaceGeneration)
	case strings.TrimSpace(snapshotID) != "":
		return "snap:" + strings.TrimSpace(snapshotID)
	case strings.TrimSpace(workspaceDir) != "":
		return "dir:" + strings.TrimSpace(workspaceDir)
	case strings.TrimSpace(projectKey) != "":
		return fmt.Sprintf("run:%s:%d", strings.TrimSpace(projectKey), runID)
	default:
		return fmt.Sprintf("run:%d", runID)
	}
}

func (s *Service) releaseSnapshotReference(ctx context.Context, snapshotID string) {
	if s == nil || s.snap == nil || s.snap.Catalog() == nil {
		return
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return
	}
	_ = s.snap.Catalog().ReleaseReference(ctx, snapshotID)
}

func (s *Service) recordStageAccepted(ctx context.Context, runID uint, projectKey, verifyTarget, snapshotID, baseCommit, workspaceDir string, stage runexecsvc.Stage, observedAt time.Time) {
	if s == nil || s.task == nil || s.exec == nil {
		return
	}
	res, err := s.exec.Execute(ctx, runexecsvc.ExecuteRequest{
		ProjectKey:   projectKey,
		RunID:        runID,
		TaskRunID:    runID,
		TargetName:   verifyTarget,
		Stage:        stage,
		WorkspaceDir: workspaceDir,
		SnapshotID:   snapshotID,
		BaseCommit:   baseCommit,
		Attempt:      1,
	})
	if err != nil {
		eventType, runStatus, source, milestone := stageFailureMeta(stage)
		_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: runID,
			EventType: eventType,
			ToState: map[string]any{
				"orchestration_state": contracts.TaskPending,
				"run_status":          runStatus,
			},
			Note:      strings.TrimSpace(err.Error()),
			CreatedAt: observedAt,
		})
		_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
			TaskRunID:  runID,
			State:      contracts.TaskHealthStalled,
			NeedsUser:  false,
			Summary:    string(stage) + " failed",
			Source:     source,
			ObservedAt: observedAt,
			Metrics: map[string]any{
				"stage":         stage,
				"verify_target": verifyTarget,
				"error":         strings.TrimSpace(err.Error()),
			},
		})
		_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
			TaskRunID:  runID,
			Phase:      contracts.TaskPhaseBlocked,
			Milestone:  milestone,
			NextAction: string(contracts.NextWaitUser),
			Summary:    strings.TrimSpace(err.Error()),
			ReportedAt: observedAt,
			Payload: map[string]any{
				"stage":         stage,
				"verify_target": verifyTarget,
			},
		})
		return
	}

	eventType, runStatus, source, milestone := stageAcceptedMeta(stage)
	_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: eventType,
		ToState: map[string]any{
			"orchestration_state": contracts.TaskPending,
			"run_status":          runStatus,
		},
		Note:      res.Summary,
		CreatedAt: observedAt,
		Payload: map[string]any{
			"stage":         res.Stage,
			"verify_target": res.Target.Name,
			"command":       append([]string(nil), res.Command...),
			"workspace_dir": res.WorkspaceDir,
			"snapshot_id":   res.SnapshotID,
			"base_commit":   res.BaseCommit,
		},
	})
	_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runID,
		State:      contracts.TaskHealthIdle,
		NeedsUser:  false,
		Summary:    res.Summary,
		Source:     source,
		ObservedAt: observedAt,
		Metrics: map[string]any{
			"stage":          res.Stage,
			"verify_target":  res.Target.Name,
			"command_length": len(res.Command),
			"workspace_dir":  res.WorkspaceDir,
			"snapshot_id":    res.SnapshotID,
		},
	})
	_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  runID,
		Phase:      contracts.TaskPhasePlanning,
		Milestone:  milestone,
		NextAction: string(contracts.NextContinue),
		Summary:    res.Summary,
		ReportedAt: observedAt,
		Payload: map[string]any{
			"stage":         res.Stage,
			"verify_target": res.Target.Name,
			"workspace_dir": res.WorkspaceDir,
			"snapshot_id":   res.SnapshotID,
		},
	})
}

func stageAcceptedMeta(stage runexecsvc.Stage) (string, contracts.RunStatus, string, string) {
	switch stage {
	case runexecsvc.StageBootstrap:
		return "run_bootstrap_accepted", contracts.RunEnvPreparing, "run.bootstrap", "bootstrap_accepted"
	case runexecsvc.StageRepair:
		return "run_repair_accepted", contracts.RunEnvPreparing, "run.repair", "repair_accepted"
	case runexecsvc.StageVerify:
		return "run_verify_prepared", contracts.RunReadyToRun, "run.verify.prepare", "verify_prepared"
	default:
		return "run_preflight_accepted", contracts.RunRequested, "run.preflight", "preflight_accepted"
	}
}

func stageFailureMeta(stage runexecsvc.Stage) (string, contracts.RunStatus, string, string) {
	switch stage {
	case runexecsvc.StageBootstrap:
		return "run_bootstrap_failed", contracts.RunEnvPreparing, "run.bootstrap", "bootstrap_failed"
	case runexecsvc.StageRepair:
		return "run_repair_failed", contracts.RunEnvPreparing, "run.repair", "repair_failed"
	case runexecsvc.StageVerify:
		return "run_verify_prepare_failed", contracts.RunReadyToRun, "run.verify.prepare", "verify_prepare_failed"
	default:
		return "run_preflight_failed", contracts.RunRequested, "run.preflight", "preflight_failed"
	}
}

func (s *Service) Get(ctx context.Context, runID uint) (*contracts.RunView, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("run service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	var out contracts.RunView
	if err := s.db.WithContext(ctx).Where("run_id = ?", runID).First(&out).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	if s.task != nil {
		status, err := s.task.GetStatusByRunID(ctx, runID)
		if err != nil {
			return nil, err
		}
		if status != nil {
			nextStatus := deriveRunStatus(*status, out.RunStatus)
			updates := map[string]any{}
			if out.RunStatus != nextStatus {
				updates["run_status"] = nextStatus
				out.RunStatus = nextStatus
			}
			if out.ProjectKey == "" && strings.TrimSpace(status.ProjectKey) != "" {
				updates["project_key"] = strings.TrimSpace(status.ProjectKey)
				out.ProjectKey = strings.TrimSpace(status.ProjectKey)
			}
			if out.TicketID == 0 && status.TicketID != 0 {
				updates["ticket_id"] = status.TicketID
				out.TicketID = status.TicketID
			}
			if out.WorkerID == 0 && status.WorkerID != 0 {
				updates["worker_id"] = status.WorkerID
				out.WorkerID = status.WorkerID
			}
			if len(updates) > 0 {
				if err := s.db.WithContext(ctx).Model(&contracts.RunView{}).Where("run_id = ?", runID).Updates(updates).Error; err != nil {
					return nil, err
				}
			}
		}
	}
	return &out, nil
}

func (s *Service) List(ctx context.Context, limit int) ([]contracts.RunView, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("run service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 100
	}
	var out []contracts.RunView
	if err := s.db.WithContext(ctx).Order("updated_at desc").Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	if out == nil {
		return []contracts.RunView{}, nil
	}
	return out, nil
}

func (s *Service) GetByRequestID(ctx context.Context, requestID string) (*contracts.RunView, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("run service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, fmt.Errorf("request_id 不能为空")
	}
	var out contracts.RunView
	if err := s.db.WithContext(ctx).Where("request_id = ?", requestID).First(&out).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return s.Get(ctx, out.RunID)
}

func deriveRunStatus(status contracts.TaskStatusView, current contracts.RunStatus) contracts.RunStatus {
	orch := strings.TrimSpace(strings.ToLower(status.OrchestrationState))
	runtime := strings.TrimSpace(strings.ToLower(status.RuntimeHealthState))
	lastEventType := strings.TrimSpace(strings.ToLower(status.LastEventType))
	nextAction := strings.TrimSpace(strings.ToLower(status.SemanticNextAction))

	switch orch {
	case string(contracts.TaskPending):
		if lastEventType == "run_verify_prepared" {
			return contracts.RunReadyToRun
		}
		if lastEventType == "run_bootstrap_accepted" {
			return contracts.RunEnvPreparing
		}
		if current == contracts.RunSnapshotReady && (lastEventType == "run_snapshot_ready" || lastEventType == "run_snapshot_apply_accepted" || lastEventType == "run_preflight_accepted") {
			return contracts.RunSnapshotReady
		}
		if current == contracts.RunRequested && (lastEventType == "run_requested" || lastEventType == "run_preflight_accepted") {
			return contracts.RunRequested
		}
		return contracts.RunQueued
	case string(contracts.TaskRunning):
		if status.RuntimeNeedsUser || runtime == string(contracts.TaskHealthWaitingUser) || nextAction == string(contracts.NextWaitUser) {
			return contracts.RunWaitingApproval
		}
		return contracts.RunRunning
	case string(contracts.TaskSucceeded):
		return contracts.RunSucceeded
	case string(contracts.TaskFailed):
		return contracts.RunFailed
	case string(contracts.TaskCanceled):
		return contracts.RunCanceled
	default:
		return current
	}
}
