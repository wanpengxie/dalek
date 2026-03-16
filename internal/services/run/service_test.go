package run

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/contracts"
	runexecsvc "dalek/internal/services/runexecutor"
	snapshotsvc "dalek/internal/services/snapshot"
	"dalek/internal/services/task"
	"dalek/internal/store"
)

type captureExecutor struct {
	reqs            []runexecsvc.ExecuteRequest
	failOnCallIndex map[int]error
}

func (e *captureExecutor) Execute(ctx context.Context, req runexecsvc.ExecuteRequest) (runexecsvc.ExecuteResult, error) {
	e.reqs = append(e.reqs, req)
	if e.failOnCallIndex != nil {
		if err := e.failOnCallIndex[len(e.reqs)]; err != nil {
			return runexecsvc.ExecuteResult{}, err
		}
	}
	return runexecsvc.ExecuteResult{
		Accepted:     true,
		Stage:        req.Stage,
		Target:       runexecsvc.TargetSpec{Name: req.TargetName},
		Command:      []string{"go", "test", "./..."},
		WorkspaceDir: req.WorkspaceDir,
		SnapshotID:   req.SnapshotID,
		BaseCommit:   req.BaseCommit,
		Summary:      stageSummaryForTest(req.Stage, req.TargetName),
	}, nil
}

type blockingVerifyExecutor struct {
	mu            sync.Mutex
	verifyStarted chan uint
	release       chan struct{}
	seenRunIDs    map[uint]struct{}
}

func (e *blockingVerifyExecutor) Execute(ctx context.Context, req runexecsvc.ExecuteRequest) (runexecsvc.ExecuteResult, error) {
	if req.Stage == runexecsvc.StageVerify {
		e.mu.Lock()
		_, seen := e.seenRunIDs[req.RunID]
		if !seen {
			if e.seenRunIDs == nil {
				e.seenRunIDs = map[uint]struct{}{}
			}
			e.seenRunIDs[req.RunID] = struct{}{}
		}
		e.mu.Unlock()
		if !seen {
			e.verifyStarted <- req.RunID
			<-e.release
		}
	}
	return runexecsvc.ExecuteResult{
		Accepted:     true,
		Stage:        req.Stage,
		Target:       runexecsvc.TargetSpec{Name: req.TargetName},
		Command:      []string{"go", "test", "./..."},
		WorkspaceDir: req.WorkspaceDir,
		SnapshotID:   req.SnapshotID,
		BaseCommit:   req.BaseCommit,
		Summary:      stageSummaryForTest(req.Stage, req.TargetName),
	}, nil
}

func stageSummaryForTest(stage runexecsvc.Stage, target string) string {
	switch stage {
	case runexecsvc.StageBootstrap:
		return "bootstrap accepted for target=" + target
	case runexecsvc.StageRepair:
		return "repair accepted for target=" + target
	case runexecsvc.StageVerify:
		return "verify accepted for target=" + target
	default:
		return "preflight accepted for target=" + target
	}
}

func newRunServiceForTest(t *testing.T) (*Service, *task.Service) {
	t.Helper()

	rootDir := t.TempDir()
	dbPath := filepath.Join(rootDir, "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	taskSvc := task.New(db)
	snapshotCatalog := snapshotsvc.NewCatalog(db)
	snapshotStore, err := snapshotsvc.NewFileStore(filepath.Join(rootDir, "snapshots"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	snapshotApply := snapshotsvc.NewApplyService(snapshotCatalog, snapshotStore)
	return New(db, taskSvc, nil, snapshotApply), taskSvc
}

func TestService_Submit_CreatesRunViewAndTaskRun(t *testing.T) {
	svc, taskSvc := newRunServiceForTest(t)

	res, err := svc.Submit(context.Background(), SubmitInput{
		ProjectKey:   "demo",
		TicketID:     42,
		RequestID:    "run-req-1",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("expected accepted result")
	}
	if res.RunID == 0 || res.TaskRunID == 0 {
		t.Fatalf("expected non-zero run ids: %+v", res)
	}
	if res.RunID != res.TaskRunID {
		t.Fatalf("expected run id to equal task run id: %+v", res)
	}
	if res.RunStatus != contracts.RunSucceeded {
		t.Fatalf("unexpected run status: %s", res.RunStatus)
	}

	taskRun, err := taskSvc.FindRunByID(context.Background(), res.TaskRunID)
	if err != nil {
		t.Fatalf("FindRunByID failed: %v", err)
	}
	if taskRun == nil {
		t.Fatalf("expected created task run")
	}
	if taskRun.OwnerType != contracts.TaskOwnerPM {
		t.Fatalf("unexpected owner type: %s", taskRun.OwnerType)
	}
	if taskRun.TaskType != contracts.TaskTypeRunVerify {
		t.Fatalf("unexpected task type: %s", taskRun.TaskType)
	}

	view, err := svc.Get(context.Background(), res.RunID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if view == nil {
		t.Fatalf("expected created run view")
	}
	if view.RequestID != "run-req-1" {
		t.Fatalf("unexpected request id: %s", view.RequestID)
	}
	if view.VerifyTarget != "test" {
		t.Fatalf("unexpected verify target: %s", view.VerifyTarget)
	}

	status, err := taskSvc.GetStatusByRunID(context.Background(), res.TaskRunID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil {
		t.Fatalf("expected task status after submit")
	}
	if status.LastEventType != "run_verify_succeeded" {
		t.Fatalf("unexpected last event type: %s", status.LastEventType)
	}
	if status.RuntimeSummary != "verify accepted for target=test" {
		t.Fatalf("unexpected runtime summary: %s", status.RuntimeSummary)
	}
	if status.SemanticMilestone != "verify_succeeded" {
		t.Fatalf("unexpected semantic milestone: %s", status.SemanticMilestone)
	}
}

func TestService_Submit_IdempotentByRequestID(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	first, err := svc.Submit(context.Background(), SubmitInput{
		ProjectKey:   "demo",
		RequestID:    "run-req-idempotent",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("first Submit failed: %v", err)
	}
	second, err := svc.Submit(context.Background(), SubmitInput{
		ProjectKey:   "demo",
		RequestID:    "run-req-idempotent",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("second Submit failed: %v", err)
	}
	if first.RunID != second.RunID {
		t.Fatalf("expected idempotent run id: first=%d second=%d", first.RunID, second.RunID)
	}
	if first.TaskRunID != second.TaskRunID {
		t.Fatalf("expected idempotent task run id: first=%d second=%d", first.TaskRunID, second.TaskRunID)
	}
}

func TestService_Get_ProjectsTaskStatusIntoRunView(t *testing.T) {
	svc, taskSvc := newRunServiceForTest(t)
	ctx := context.Background()
	fixedNow := time.Now().Local().Truncate(time.Second)
	svc.now = func() time.Time { return fixedNow }

	runRec, err := taskSvc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeRunVerify,
		ProjectKey:         "demo",
		TicketID:           7,
		SubjectType:        "run",
		SubjectID:          "test",
		RequestID:          "run-projection-1",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	viewSeed := contracts.RunView{
		RunID:        runRec.ID,
		TaskRunID:    runRec.ID,
		ProjectKey:   "demo",
		RequestID:    "run-projection-1",
		TicketID:     7,
		RunStatus:    contracts.RunRequested,
		VerifyTarget: "test",
	}
	if err := svc.db.WithContext(ctx).Create(&viewSeed).Error; err != nil {
		t.Fatalf("seed run view failed: %v", err)
	}

	if err := taskSvc.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runRec.ID,
		EventType: "run_requested",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskPending,
			"run_status":          contracts.RunRequested,
		},
		CreatedAt: fixedNow,
	}); err != nil {
		t.Fatalf("AppendEvent run_requested failed: %v", err)
	}

	view, err := svc.Get(ctx, runRec.ID)
	if err != nil {
		t.Fatalf("Get after submit failed: %v", err)
	}
	if view == nil || view.RunStatus != contracts.RunRequested {
		t.Fatalf("expected requested after submit, got=%+v", view)
	}

	lease := fixedNow.Add(2 * time.Minute)
	if err := taskSvc.MarkRunRunning(ctx, runRec.ID, "runner-1", &lease, fixedNow, true); err != nil {
		t.Fatalf("MarkRunRunning failed: %v", err)
	}
	if err := taskSvc.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runRec.ID,
		State:      contracts.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "run executing",
		Source:     "run.test",
		ObservedAt: fixedNow,
	}); err != nil {
		t.Fatalf("AppendRuntimeSample failed: %v", err)
	}
	view, err = svc.Get(ctx, runRec.ID)
	if err != nil {
		t.Fatalf("Get after running failed: %v", err)
	}
	if view == nil || view.RunStatus != contracts.RunRunning {
		t.Fatalf("expected running after projection, got=%+v", view)
	}

	if err := taskSvc.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runRec.ID,
		State:      contracts.TaskHealthWaitingUser,
		NeedsUser:  true,
		Summary:    "approval required",
		Source:     "run.test",
		ObservedAt: fixedNow.Add(time.Second),
	}); err != nil {
		t.Fatalf("AppendRuntimeSample waiting_user failed: %v", err)
	}
	if err := taskSvc.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  runRec.ID,
		Phase:      contracts.TaskPhaseBlocked,
		Milestone:  "approval_requested",
		NextAction: string(contracts.NextWaitUser),
		Summary:    "approval required",
		ReportedAt: fixedNow.Add(time.Second),
	}); err != nil {
		t.Fatalf("AppendSemanticReport waiting_user failed: %v", err)
	}
	status, err := taskSvc.GetStatusByRunID(ctx, runRec.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil {
		t.Fatalf("expected task status after waiting_user samples")
	}
	view, err = svc.Get(ctx, runRec.ID)
	if err != nil {
		t.Fatalf("Get after waiting approval failed: %v", err)
	}
	if view == nil || view.RunStatus != contracts.RunWaitingApproval {
		t.Fatalf("expected waiting approval after projection, got=%+v", view)
	}

	if err := taskSvc.MarkRunSucceeded(ctx, runRec.ID, `{"ok":true}`, fixedNow.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkRunSucceeded failed: %v", err)
	}
	view, err = svc.Get(ctx, runRec.ID)
	if err != nil {
		t.Fatalf("Get after success failed: %v", err)
	}
	if view == nil || view.RunStatus != contracts.RunSucceeded {
		t.Fatalf("expected succeeded after projection, got=%+v", view)
	}
}

func TestService_List_ReturnsRuns(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	ctx := context.Background()

	first, err := svc.Submit(ctx, SubmitInput{
		ProjectKey:   "demo",
		RequestID:    "run-list-1",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("submit first run failed: %v", err)
	}
	second, err := svc.Submit(ctx, SubmitInput{
		ProjectKey:   "demo",
		RequestID:    "run-list-2",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("submit second run failed: %v", err)
	}

	items, err := svc.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least 2 runs, got=%d", len(items))
	}
	found := map[uint]bool{
		first.RunID:  false,
		second.RunID: false,
	}
	for _, item := range items {
		if _, ok := found[item.RunID]; ok {
			found[item.RunID] = true
		}
	}
	for id, ok := range found {
		if !ok {
			t.Fatalf("missing run_id=%d in list", id)
		}
	}
}

func TestService_Submit_RejectsUnknownVerifyTarget(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	_, err := svc.Submit(context.Background(), SubmitInput{
		ProjectKey:   "demo",
		RequestID:    "run-req-invalid-target",
		VerifyTarget: "go test ./...",
	})
	if err == nil {
		t.Fatalf("expected invalid verify target to be rejected")
	}
}

func TestService_Submit_AttachesReadySnapshot(t *testing.T) {
	svc, taskSvc := newRunServiceForTest(t)
	ctx := context.Background()
	capture := &captureExecutor{}
	svc.exec = capture
	catalog := snapshotsvc.NewCatalog(svc.db)
	if svc.snap == nil || svc.snap.Store() == nil {
		t.Fatalf("expected snapshot apply service with file store")
	}
	path, digest, err := svc.snap.Store().WriteManifestPack("snap-run-1", snapshotsvc.Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		Files: []snapshotsvc.ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 420},
		},
	})
	if err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	if _, err := catalog.Create(ctx, snapshotsvc.CreateInput{
		SnapshotID:     "snap-run-1",
		ProjectKey:     "demo",
		NodeName:       "node-b",
		BaseCommit:     "abc123",
		ManifestDigest: digest,
		ManifestJSON:   `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
		ArtifactPath:   path,
	}); err != nil {
		t.Fatalf("snapshot create failed: %v", err)
	}
	if err := catalog.MarkReady(ctx, "snap-run-1", path); err != nil {
		t.Fatalf("snapshot mark ready failed: %v", err)
	}

	res, err := svc.Submit(ctx, SubmitInput{
		ProjectKey:   "demo",
		RequestID:    "run-req-snapshot-1",
		VerifyTarget: "test",
		SnapshotID:   "snap-run-1",
		BaseCommit:   "abc123",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if res.RunStatus != contracts.RunSucceeded {
		t.Fatalf("unexpected run status: %s", res.RunStatus)
	}
	if res.SnapshotID != "snap-run-1" || res.BaseCommit != "abc123" || res.SourceWorkspaceGeneration != "wg-1" {
		t.Fatalf("unexpected snapshot binding: %+v", res)
	}

	view, err := svc.Get(ctx, res.RunID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if view == nil || view.SnapshotID != "snap-run-1" || view.RunStatus != contracts.RunSucceeded {
		t.Fatalf("unexpected run view: %+v", view)
	}
	snapshotRec, err := catalog.GetBySnapshotID(ctx, "snap-run-1")
	if err != nil {
		t.Fatalf("GetBySnapshotID failed: %v", err)
	}
	if snapshotRec == nil || snapshotRec.RefCount != 0 {
		t.Fatalf("unexpected snapshot ref_count after terminal submit: %+v", snapshotRec)
	}

	status, err := taskSvc.GetStatusByRunID(ctx, res.RunID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.LastEventType != "run_verify_succeeded" {
		t.Fatalf("unexpected task status: %+v", status)
	}
	events, err := taskSvc.ListEvents(ctx, res.RunID, 20)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	foundSnapshotApply := false
	for _, ev := range events {
		if ev.EventType == "run_snapshot_apply_accepted" {
			foundSnapshotApply = true
			break
		}
	}
	if !foundSnapshotApply {
		t.Fatalf("expected run_snapshot_apply_accepted event, got=%+v", events)
	}
	if len(capture.reqs) != 5 {
		t.Fatalf("expected 5 executor stages, got=%d", len(capture.reqs))
	}
	var preflightReq runexecsvc.ExecuteRequest
	foundPreflight := false
	verifyCount := 0
	for _, req := range capture.reqs {
		if req.Stage == runexecsvc.StagePreflight {
			preflightReq = req
			foundPreflight = true
		}
		if req.Stage == runexecsvc.StageVerify {
			verifyCount++
		}
	}
	if !foundPreflight || preflightReq.SnapshotID != "snap-run-1" {
		t.Fatalf("unexpected preflight request set: %+v", capture.reqs)
	}
	if preflightReq.BaseCommit != "abc123" {
		t.Fatalf("unexpected preflight base commit: %+v", preflightReq)
	}
	if preflightReq.WorkspaceDir == "" || !strings.Contains(preflightReq.WorkspaceDir, "snap-run-1") {
		t.Fatalf("expected materialized workspace dir in preflight request, got=%q", preflightReq.WorkspaceDir)
	}
	if verifyCount != 2 {
		t.Fatalf("expected prepare+execute verify calls, got=%d", verifyCount)
	}
}

func TestService_Submit_TransitionsToFailedWhenVerifyExecutionFails(t *testing.T) {
	svc, taskSvc := newRunServiceForTest(t)
	ctx := context.Background()
	catalog := snapshotsvc.NewCatalog(svc.db)
	path, digest, err := svc.snap.Store().WriteManifestPack("snap-run-fail-1", snapshotsvc.Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-fail",
		Files: []snapshotsvc.ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 420},
		},
	})
	if err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	if _, err := catalog.Create(ctx, snapshotsvc.CreateInput{
		SnapshotID:     "snap-run-fail-1",
		ProjectKey:     "demo",
		NodeName:       "node-b",
		BaseCommit:     "abc123",
		ManifestDigest: digest,
		ManifestJSON:   `{"base_commit":"abc123","workspace_generation":"wg-fail","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
		ArtifactPath:   path,
	}); err != nil {
		t.Fatalf("snapshot create failed: %v", err)
	}
	if err := catalog.MarkReady(ctx, "snap-run-fail-1", path); err != nil {
		t.Fatalf("snapshot mark ready failed: %v", err)
	}
	svc.exec = &captureExecutor{
		failOnCallIndex: map[int]error{
			5: fmt.Errorf("verify stub failed"),
		},
	}

	res, err := svc.Submit(ctx, SubmitInput{
		ProjectKey:   "demo",
		RequestID:    "run-req-failed-1",
		VerifyTarget: "test",
		SnapshotID:   "snap-run-fail-1",
		BaseCommit:   "abc123",
	})
	if err != nil {
		t.Fatalf("Submit failed unexpectedly: %v", err)
	}
	if res.RunStatus != contracts.RunFailed {
		t.Fatalf("expected failed run status, got=%s", res.RunStatus)
	}

	view, err := svc.Get(ctx, res.RunID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if view == nil || view.RunStatus != contracts.RunFailed {
		t.Fatalf("unexpected failed run view: %+v", view)
	}

	status, err := taskSvc.GetStatusByRunID(ctx, res.TaskRunID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.LastEventType != "run_verify_failed" {
		t.Fatalf("unexpected task status: %+v", status)
	}
	if status.RuntimeSummary != "verify failed" {
		t.Fatalf("unexpected runtime summary: %s", status.RuntimeSummary)
	}
	if status.SemanticMilestone != "verify_failed" {
		t.Fatalf("unexpected semantic milestone: %s", status.SemanticMilestone)
	}
	snapshotRec, err := catalog.GetBySnapshotID(ctx, "snap-run-fail-1")
	if err != nil {
		t.Fatalf("GetBySnapshotID failed: %v", err)
	}
	if snapshotRec == nil || snapshotRec.RefCount != 0 {
		t.Fatalf("unexpected snapshot ref_count after failure: %+v", snapshotRec)
	}
}

func TestService_RecordArtifactFailure_DoesNotOverrideRunStatus(t *testing.T) {
	svc, taskSvc := newRunServiceForTest(t)
	ctx := context.Background()

	res, err := svc.Submit(ctx, SubmitInput{
		ProjectKey:   "demo",
		RequestID:    "run-artifact-1",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if res.RunStatus != contracts.RunSucceeded {
		t.Fatalf("expected succeeded run, got=%s", res.RunStatus)
	}
	if err := svc.RecordArtifactFailure(ctx, res.RunID, "report.json", "upload failed"); err != nil {
		t.Fatalf("RecordArtifactFailure failed: %v", err)
	}
	view, err := svc.Get(ctx, res.RunID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if view == nil || view.RunStatus != contracts.RunSucceeded {
		t.Fatalf("unexpected run view after artifact failure: %+v", view)
	}
	events, err := taskSvc.ListEvents(ctx, res.RunID, 20)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.EventType == "run_artifact_upload_failed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected run_artifact_upload_failed event, got=%+v", events)
	}
	status, err := taskSvc.GetStatusByRunID(ctx, res.RunID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil {
		t.Fatalf("expected task status after artifact failure")
	}
	if status.RuntimeSummary != "verify accepted for target=test" {
		t.Fatalf("unexpected runtime summary after artifact failure: %+v", status)
	}
	if status.SemanticMilestone != "verify_succeeded" {
		t.Fatalf("unexpected semantic milestone after artifact failure: %+v", status)
	}
	if status.LastEventType != "run_artifact_upload_failed" {
		t.Fatalf("expected last event to record artifact failure: %+v", status)
	}
}

func TestService_Cancel_TransitionsPendingRunToCanceled(t *testing.T) {
	svc, taskSvc := newRunServiceForTest(t)
	ctx := context.Background()
	runRec, err := taskSvc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeRunVerify,
		ProjectKey:         "demo",
		SubjectType:        "run",
		SubjectID:          "test",
		RequestID:          "run-cancel-1",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if err := svc.db.WithContext(ctx).Create(&contracts.RunView{
		RunID:        runRec.ID,
		TaskRunID:    runRec.ID,
		ProjectKey:   "demo",
		RequestID:    "run-cancel-1",
		RunStatus:    contracts.RunReadyToRun,
		VerifyTarget: "test",
	}).Error; err != nil {
		t.Fatalf("seed run view failed: %v", err)
	}

	res, err := svc.Cancel(ctx, runRec.ID)
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if !res.Canceled {
		t.Fatalf("expected canceled result: %+v", res)
	}

	view, err := svc.Get(ctx, runRec.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if view == nil || view.RunStatus != contracts.RunCanceled {
		t.Fatalf("unexpected canceled run view: %+v", view)
	}

	status, err := taskSvc.GetStatusByRunID(ctx, runRec.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.LastEventType != "run_canceled" {
		t.Fatalf("unexpected task status: %+v", status)
	}
	if status.RuntimeSummary != "run canceled" {
		t.Fatalf("unexpected runtime summary: %s", status.RuntimeSummary)
	}
	if status.SemanticMilestone != "run_canceled" {
		t.Fatalf("unexpected semantic milestone: %s", status.SemanticMilestone)
	}
}

func TestService_Submit_SerializesVerifyByWorkspaceGeneration(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	exec := &blockingVerifyExecutor{
		verifyStarted: make(chan uint, 4),
		release:       make(chan struct{}),
	}
	svc.exec = exec

	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)

	go func() {
		_, err := svc.Submit(context.Background(), SubmitInput{
			ProjectKey:                "demo",
			RequestID:                 "run-wg-1",
			VerifyTarget:              "test",
			SourceWorkspaceGeneration: "wg-shared",
		})
		firstDone <- err
	}()

	firstRunID := <-exec.verifyStarted

	go func() {
		_, err := svc.Submit(context.Background(), SubmitInput{
			ProjectKey:                "demo",
			RequestID:                 "run-wg-2",
			VerifyTarget:              "test",
			SourceWorkspaceGeneration: "wg-shared",
		})
		secondDone <- err
	}()

	select {
	case secondRunID := <-exec.verifyStarted:
		t.Fatalf("second run reached verify before first finished: first=%d second=%d", firstRunID, secondRunID)
	case <-time.After(150 * time.Millisecond):
	}

	close(exec.release)

	if err := <-firstDone; err != nil {
		t.Fatalf("first Submit failed: %v", err)
	}
	select {
	case secondRunID := <-exec.verifyStarted:
		if secondRunID == firstRunID {
			t.Fatalf("expected distinct second run id, got=%d", secondRunID)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected second run to start verify after first finished")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second Submit failed: %v", err)
	}
}

func TestService_Submit_DefaultProjectLimitPreventsProjectFromUsingFullNodeCapacity(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	svc.sched = NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"local": 3}})
	exec := &blockingVerifyExecutor{
		verifyStarted: make(chan uint, 8),
		release:       make(chan struct{}),
	}
	svc.exec = exec

	done := make([]chan error, 3)
	for i := range done {
		done[i] = make(chan error, 1)
	}

	go func() {
		_, err := svc.Submit(context.Background(), SubmitInput{
			ProjectKey:   "demo",
			RequestID:    "run-project-cap-1",
			VerifyTarget: "test",
		})
		done[0] <- err
	}()
	firstRunID := <-exec.verifyStarted

	go func() {
		_, err := svc.Submit(context.Background(), SubmitInput{
			ProjectKey:   "demo",
			RequestID:    "run-project-cap-2",
			VerifyTarget: "test",
		})
		done[1] <- err
	}()
	secondRunID := <-exec.verifyStarted
	if secondRunID == firstRunID {
		t.Fatalf("expected distinct second run id, got=%d", secondRunID)
	}

	go func() {
		_, err := svc.Submit(context.Background(), SubmitInput{
			ProjectKey:   "demo",
			RequestID:    "run-project-cap-3",
			VerifyTarget: "test",
		})
		done[2] <- err
	}()

	select {
	case thirdRunID := <-exec.verifyStarted:
		t.Fatalf("third run should wait for same-project slot before verify: third=%d first=%d second=%d", thirdRunID, firstRunID, secondRunID)
	case <-time.After(150 * time.Millisecond):
	}

	exec.release <- struct{}{}

	if err := <-done[0]; err != nil {
		t.Fatalf("first Submit failed: %v", err)
	}

	select {
	case thirdRunID := <-exec.verifyStarted:
		if thirdRunID == firstRunID || thirdRunID == secondRunID {
			t.Fatalf("expected distinct third run id, got=%d", thirdRunID)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected third run to start verify after project slot released")
	}

	exec.release <- struct{}{}
	exec.release <- struct{}{}

	if err := <-done[1]; err != nil {
		t.Fatalf("second Submit failed: %v", err)
	}
	if err := <-done[2]; err != nil {
		t.Fatalf("third Submit failed: %v", err)
	}
}
