package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	nodeagentsvc "dalek/internal/services/nodeagent"
)

type testNodeProjectResolver struct {
	project InternalNodeProject
}

func (r *testNodeProjectResolver) OpenNodeProject(name string) (InternalNodeProject, error) {
	if r == nil || r.project == nil {
		return nil, context.Canceled
	}
	return r.project, nil
}

type testNodeProject struct {
	registration     NodeRegistration
	lease            NodeSessionLease
	runSubmission    NodeRunSubmission
	cancelResult     NodeRunCancelResult
	logs             NodeRunLogs
	artifacts        NodeRunArtifacts
	snapshotUpload   NodeSnapshotUploadResult
	snapshotChunk    NodeSnapshotChunkUploadResult
	snapshotDownload NodeSnapshotDownloadResult
	lastHeartbeat    struct {
		name  string
		epoch int
	}
	runView *NodeRunView
}

func (p *testNodeProject) RegisterNode(ctx context.Context, opt NodeRegisterOptions) (NodeRegistration, error) {
	_ = ctx
	p.registration = NodeRegistration{
		Name:                 opt.Name,
		Endpoint:             opt.Endpoint,
		AuthMode:             opt.AuthMode,
		Status:               opt.Status,
		Version:              opt.Version,
		ProtocolVersion:      opt.ProtocolVersion,
		RoleCapabilities:     append([]string(nil), opt.RoleCapabilities...),
		ProviderModes:        append([]string(nil), opt.ProviderModes...),
		DefaultProvider:      opt.DefaultProvider,
		ProviderCapabilities: opt.ProviderCapabilities,
		SessionAffinity:      opt.SessionAffinity,
		LastSeenAt:           opt.LastSeenAt,
	}
	return p.registration, nil
}

func (p *testNodeProject) BeginNodeSession(ctx context.Context, name string, observedAt *time.Time) (NodeSessionLease, error) {
	_ = ctx
	if observedAt == nil {
		now := time.Now()
		observedAt = &now
	}
	p.lease = NodeSessionLease{
		Name:         name,
		SessionEpoch: 2,
		LastSeenAt:   observedAt.Local(),
	}
	return p.lease, nil
}

func (p *testNodeProject) HeartbeatNodeWithEpoch(ctx context.Context, name string, sessionEpoch int, observedAt *time.Time) error {
	_ = ctx
	_ = observedAt
	p.lastHeartbeat.name = name
	p.lastHeartbeat.epoch = sessionEpoch
	if sessionEpoch != p.lease.SessionEpoch {
		return context.DeadlineExceeded
	}
	return nil
}

func (p *testNodeProject) SubmitRun(ctx context.Context, opt NodeRunSubmitOptions) (NodeRunSubmission, error) {
	_ = ctx
	if p.runSubmission.RunID == 0 {
		p.runSubmission = NodeRunSubmission{
			Accepted:     true,
			RunID:        42,
			TaskRunID:    42,
			RequestID:    opt.RequestID,
			RunStatus:    "requested",
			VerifyTarget: opt.VerifyTarget,
			SnapshotID:   opt.SnapshotID,
			BaseCommit:   opt.BaseCommit,
		}
	}
	if strings.TrimSpace(p.runSubmission.RequestID) == "" {
		p.runSubmission.RequestID = opt.RequestID
	}
	if strings.TrimSpace(p.runSubmission.VerifyTarget) == "" {
		p.runSubmission.VerifyTarget = opt.VerifyTarget
	}
	if strings.TrimSpace(p.runSubmission.SnapshotID) == "" {
		p.runSubmission.SnapshotID = opt.SnapshotID
	}
	if strings.TrimSpace(p.runSubmission.BaseCommit) == "" {
		p.runSubmission.BaseCommit = opt.BaseCommit
	}
	return p.runSubmission, nil
}

func (p *testNodeProject) GetRun(ctx context.Context, runID uint) (*NodeRunView, error) {
	_ = ctx
	if p.runView == nil || p.runView.RunID != runID {
		return nil, nil
	}
	return p.runView, nil
}

func (p *testNodeProject) GetRunByRequestID(ctx context.Context, requestID string) (*NodeRunView, error) {
	_ = ctx
	if p.runView == nil || strings.TrimSpace(p.runView.RequestID) != strings.TrimSpace(requestID) {
		return nil, nil
	}
	return p.runView, nil
}

func (p *testNodeProject) CancelRun(ctx context.Context, runID uint) (NodeRunCancelResult, error) {
	_ = ctx
	if p.runView == nil || p.runView.RunID != runID {
		return NodeRunCancelResult{Found: false, Canceled: false, Reason: "run not found"}, nil
	}
	if !p.cancelResult.Found {
		p.cancelResult = NodeRunCancelResult{Found: true, Canceled: true, Reason: "cancel signal sent"}
	}
	return p.cancelResult, nil
}

func (p *testNodeProject) GetRunLogs(ctx context.Context, runID uint, lines int) (NodeRunLogs, error) {
	_ = ctx
	_ = lines
	if p.runView == nil || p.runView.RunID != runID {
		return NodeRunLogs{Found: false, RunID: runID}, nil
	}
	if !p.logs.Found {
		p.logs = NodeRunLogs{Found: true, RunID: runID, Tail: "2026-03-14T11:00:00Z run_preflight_accepted"}
	}
	return p.logs, nil
}

func (p *testNodeProject) ListRunArtifacts(ctx context.Context, runID uint) (NodeRunArtifacts, error) {
	_ = ctx
	if p.runView == nil || p.runView.RunID != runID {
		return NodeRunArtifacts{Found: false, RunID: runID}, nil
	}
	if !p.artifacts.Found {
		p.artifacts = NodeRunArtifacts{Found: true, RunID: runID, Artifacts: []NodeRunArtifact{}}
	}
	return p.artifacts, nil
}

func (p *testNodeProject) UploadSnapshot(ctx context.Context, opt NodeSnapshotUploadOptions) (NodeSnapshotUploadResult, error) {
	_ = ctx
	if p.snapshotUpload.SnapshotID == "" {
		p.snapshotUpload = NodeSnapshotUploadResult{
			SnapshotID:          opt.SnapshotID,
			Status:              "ready",
			ManifestDigest:      "sha256:upload",
			ArtifactPath:        "/tmp/" + opt.SnapshotID + "/manifest.json",
			BaseCommit:          opt.BaseCommit,
			WorkspaceGeneration: opt.WorkspaceGeneration,
		}
	}
	p.snapshotDownload = NodeSnapshotDownloadResult{
		Found:               true,
		SnapshotID:          opt.SnapshotID,
		Status:              "ready",
		ManifestDigest:      p.snapshotUpload.ManifestDigest,
		ManifestJSON:        opt.ManifestJSON,
		ArtifactPath:        p.snapshotUpload.ArtifactPath,
		BaseCommit:          opt.BaseCommit,
		WorkspaceGeneration: opt.WorkspaceGeneration,
	}
	return p.snapshotUpload, nil
}

func (p *testNodeProject) UploadSnapshotChunk(ctx context.Context, opt NodeSnapshotChunkUploadOptions) (NodeSnapshotChunkUploadResult, error) {
	_ = ctx
	if !p.snapshotChunk.Accepted {
		status := "preparing"
		if opt.IsFinal {
			status = "ready"
		}
		p.snapshotChunk = NodeSnapshotChunkUploadResult{
			Accepted:            true,
			SnapshotID:          opt.SnapshotID,
			Status:              status,
			NextIndex:           opt.ChunkIndex + 1,
			ManifestDigest:      "sha256:upload-chunk",
			ArtifactPath:        "/tmp/" + opt.SnapshotID + "/manifest.json",
			BaseCommit:          opt.BaseCommit,
			WorkspaceGeneration: opt.WorkspaceGeneration,
		}
	}
	return p.snapshotChunk, nil
}

func (p *testNodeProject) DownloadSnapshot(ctx context.Context, snapshotID string) (NodeSnapshotDownloadResult, error) {
	_ = ctx
	if p.snapshotDownload.SnapshotID != snapshotID {
		return NodeSnapshotDownloadResult{Found: false, SnapshotID: snapshotID}, nil
	}
	return p.snapshotDownload, nil
}

func startTestInternalAPIForNode(t *testing.T, project InternalNodeProject) *InternalAPI {
	t.Helper()

	host, err := NewExecutionHost(&testExecutionHostResolver{project: &testExecutionHostProject{}}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	svc, err := NewInternalAPI(host, InternalAPIConfig{
		ListenAddr:     "127.0.0.1:0",
		NodeAgentToken: "node-token-1",
	}, InternalAPIOptions{
		NodeProjectResolver: &testNodeProjectResolver{project: project},
	})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
	})
	return svc
}

func postNodeAPI(t *testing.T, svc *InternalAPI, path string, payload any, withToken bool) (int, []byte) {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+svc.listener.Addr().String()+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if withToken {
		req.Header.Set("Authorization", "Bearer node-token-1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return resp.StatusCode, body
}

func TestInternalAPINodeRegister_RequiresBearerToken(t *testing.T) {
	svc := startTestInternalAPIForNode(t, &testNodeProject{})

	status, body := postNodeAPI(t, svc, "/api/node/register", nodeagentsvc.RegisterRequest{
		Meta: nodeagentsvc.RequestMeta{ProjectKey: "demo", ProtocolVersion: nodeagentsvc.ProtocolVersionV1},
		Name: "node-a",
	}, false)
	if status != http.StatusForbidden {
		t.Fatalf("unexpected status: %d body=%s", status, string(body))
	}
}

func TestInternalAPINodeRegister_Success(t *testing.T) {
	project := &testNodeProject{}
	svc := startTestInternalAPIForNode(t, project)
	now := time.Date(2026, 3, 14, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))

	status, body := postNodeAPI(t, svc, "/api/node/register", nodeagentsvc.RegisterRequest{
		Meta:                 nodeagentsvc.RequestMeta{ProjectKey: "demo", ProtocolVersion: nodeagentsvc.ProtocolVersionV1},
		Name:                 "node-a",
		Endpoint:             "http://10.0.0.10:19001",
		AuthMode:             "bearer",
		Version:              "v1.0.0",
		ProtocolVersion:      nodeagentsvc.ProtocolVersionV1,
		RoleCapabilities:     []string{"run"},
		ProviderModes:        []string{"codex"},
		DefaultProvider:      "codex",
		ProviderCapabilities: map[string]any{"codex": "gpt-5"},
		SessionAffinity:      "run",
		LastSeenAt:           now.Format(time.RFC3339),
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", status, string(body))
	}
	var resp nodeagentsvc.RegisterResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if !resp.Accepted || resp.Name != "node-a" || resp.SessionEpoch != 2 || resp.Status != "online" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if project.registration.Name != "node-a" || project.registration.DefaultProvider != "codex" {
		t.Fatalf("unexpected registration payload: %+v", project.registration)
	}
}

func TestInternalAPINodeHeartbeatAndRunQuery_Success(t *testing.T) {
	project := &testNodeProject{
		lease: NodeSessionLease{
			Name:         "node-a",
			SessionEpoch: 3,
			LastSeenAt:   time.Now(),
		},
		runSubmission: NodeRunSubmission{
			Accepted:     true,
			RunID:        42,
			TaskRunID:    42,
			RequestID:    "run-req-1",
			RunStatus:    "snapshot_ready",
			VerifyTarget: "test",
			SnapshotID:   "snap-1",
			BaseCommit:   "abc123",
		},
		runView: &NodeRunView{
			RunID:          42,
			TaskRunID:      42,
			ProjectKey:     "demo",
			RunStatus:      "snapshot_ready",
			LifecycleStage: "ready",
			VerifyTarget:   "test",
			SnapshotID:     "snap-1",
			BaseCommit:     "abc123",
			Summary:        "verify prepared",
			LastEventType:  "run_verify_prepared",
			LastEventNote:  "verify accepted for target=test",
			ArtifactCount:  1,
			UpdatedAt:      time.Date(2026, 3, 14, 11, 0, 0, 0, time.UTC),
		},
		logs: NodeRunLogs{
			Found: true,
			RunID: 42,
			Tail:  "2026-03-14T11:00:00Z run_preflight_accepted",
		},
		artifacts: NodeRunArtifacts{
			Found: true,
			RunID: 42,
			Artifacts: []NodeRunArtifact{
				{Name: "apply-plan.json", Kind: "plan", Ref: "/tmp/run-42/apply-plan.json"},
			},
			Issues: []NodeRunArtifactIssue{
				{Name: "report.json", Status: "upload_failed", Reason: "upload failed"},
			},
		},
	}
	svc := startTestInternalAPIForNode(t, project)

	status, body := postNodeAPI(t, svc, "/api/node/heartbeat", nodeagentsvc.HeartbeatRequest{
		Meta:         nodeagentsvc.RequestMeta{ProjectKey: "demo", ProtocolVersion: nodeagentsvc.ProtocolVersionV1},
		Name:         "node-a",
		SessionEpoch: 3,
		ObservedAt:   time.Now().Format(time.RFC3339),
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected heartbeat status: %d body=%s", status, string(body))
	}
	if project.lastHeartbeat.name != "node-a" || project.lastHeartbeat.epoch != 3 {
		t.Fatalf("unexpected heartbeat payload: %+v", project.lastHeartbeat)
	}

	status, body = postNodeAPI(t, svc, "/api/node/run/submit", nodeagentsvc.RunSubmitRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			RequestID:       "run-req-1",
			SessionEpoch:    3,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
		NodeName:     "node-a",
		VerifyTarget: "test",
		SnapshotID:   "snap-1",
		BaseCommit:   "abc123",
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected run submit status: %d body=%s", status, string(body))
	}
	var submitResp nodeagentsvc.RunSubmitResponse
	if err := json.Unmarshal(body, &submitResp); err != nil {
		t.Fatalf("decode run submit failed: %v", err)
	}
	if !submitResp.Accepted || submitResp.RunID != 42 || submitResp.Status != "snapshot_ready" {
		t.Fatalf("unexpected run submit response: %+v", submitResp)
	}

	status, body = postNodeAPI(t, svc, "/api/node/snapshot/upload", nodeagentsvc.SnapshotUploadRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			SessionEpoch:    3,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
		SnapshotID:          "snap-1",
		NodeName:            "node-a",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		ManifestJSON:        `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected snapshot upload status: %d body=%s", status, string(body))
	}
	var uploadResp nodeagentsvc.SnapshotUploadResponse
	if err := json.Unmarshal(body, &uploadResp); err != nil {
		t.Fatalf("decode snapshot upload failed: %v", err)
	}
	if !uploadResp.Accepted || uploadResp.SnapshotID != "snap-1" || uploadResp.Status != "ready" {
		t.Fatalf("unexpected snapshot upload response: %+v", uploadResp)
	}

	status, body = postNodeAPI(t, svc, "/api/node/snapshot/upload-chunk", nodeagentsvc.SnapshotChunkUploadRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			SessionEpoch:    3,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
		SnapshotID:          "snap-1",
		NodeName:            "node-a",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		ChunkIndex:          0,
		ChunkData:           "bWFuaWZlc3Q=",
		IsFinal:             true,
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected snapshot chunk upload status: %d body=%s", status, string(body))
	}
	var chunkResp nodeagentsvc.SnapshotChunkUploadResponse
	if err := json.Unmarshal(body, &chunkResp); err != nil {
		t.Fatalf("decode snapshot chunk upload failed: %v", err)
	}
	if !chunkResp.Accepted || chunkResp.SnapshotID != "snap-1" || chunkResp.Status != "ready" {
		t.Fatalf("unexpected snapshot chunk upload response: %+v", chunkResp)
	}

	status, body = postNodeAPI(t, svc, "/api/node/snapshot/download", nodeagentsvc.SnapshotDownloadRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
		SnapshotID: "snap-1",
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected snapshot download status: %d body=%s", status, string(body))
	}
	var downloadResp nodeagentsvc.SnapshotDownloadResponse
	if err := json.Unmarshal(body, &downloadResp); err != nil {
		t.Fatalf("decode snapshot download failed: %v", err)
	}
	if !downloadResp.Found || downloadResp.SnapshotID != "snap-1" || downloadResp.Status != "ready" {
		t.Fatalf("unexpected snapshot download response: %+v", downloadResp)
	}

	status, body = postNodeAPI(t, svc, "/api/node/run/query", nodeagentsvc.RunQueryRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			RunID:           42,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected run query status: %d body=%s", status, string(body))
	}
	var resp nodeagentsvc.RunQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode run query failed: %v", err)
	}
	if !resp.Found || resp.RunID != 42 || resp.Status != "snapshot_ready" || resp.LifecycleStage != "ready" || resp.VerifyTarget != "test" || resp.SnapshotID != "snap-1" || resp.ArtifactCount != 1 || resp.LastEventType != "run_verify_prepared" {
		t.Fatalf("unexpected run query response: %+v", resp)
	}

	status, body = postNodeAPI(t, svc, "/api/node/run/logs", nodeagentsvc.RunLogsRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			RunID:           42,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
		Lines: 20,
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected run logs status: %d body=%s", status, string(body))
	}
	var logsResp nodeagentsvc.RunLogsResponse
	if err := json.Unmarshal(body, &logsResp); err != nil {
		t.Fatalf("decode run logs failed: %v", err)
	}
	if !logsResp.Found || !strings.Contains(logsResp.Tail, "run_preflight_accepted") {
		t.Fatalf("unexpected run logs response: %+v", logsResp)
	}

	status, body = postNodeAPI(t, svc, "/api/node/run/artifacts", nodeagentsvc.RunQueryRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			RunID:           42,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected run artifacts status: %d body=%s", status, string(body))
	}
	var artifactsResp nodeagentsvc.RunArtifactsResponse
	if err := json.Unmarshal(body, &artifactsResp); err != nil {
		t.Fatalf("decode run artifacts failed: %v", err)
	}
	if !artifactsResp.Found || len(artifactsResp.Artifacts) != 1 || artifactsResp.Artifacts[0].Name != "apply-plan.json" {
		t.Fatalf("unexpected run artifacts response: %+v", artifactsResp)
	}
	if len(artifactsResp.Issues) != 1 || artifactsResp.Issues[0].Status != "upload_failed" {
		t.Fatalf("unexpected run artifact issues response: %+v", artifactsResp)
	}

	status, body = postNodeAPI(t, svc, "/api/node/run/cancel", nodeagentsvc.RunCancelRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			RunID:           42,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
		Reason: "stop",
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected run cancel status: %d body=%s", status, string(body))
	}
	var cancelResp nodeagentsvc.RunCancelResponse
	if err := json.Unmarshal(body, &cancelResp); err != nil {
		t.Fatalf("decode run cancel failed: %v", err)
	}
	if !cancelResp.Found || !cancelResp.Canceled {
		t.Fatalf("unexpected run cancel response: %+v", cancelResp)
	}
}

func TestInternalAPINodeRunQuery_RecoveryStage(t *testing.T) {
	project := &testNodeProject{
		runView: &NodeRunView{
			RunID:          99,
			TaskRunID:      99,
			ProjectKey:     "demo",
			RunStatus:      "node_offline",
			LifecycleStage: "recovery",
			VerifyTarget:   "test",
			SnapshotID:     "snap-2",
		},
	}
	svc := startTestInternalAPIForNode(t, project)

	status, body := postNodeAPI(t, svc, "/api/node/run/query", nodeagentsvc.RunQueryRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			RunID:           99,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected run query status: %d body=%s", status, string(body))
	}
	var resp nodeagentsvc.RunQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode run query failed: %v", err)
	}
	if !resp.Found || resp.RunID != 99 || resp.Status != "node_offline" || resp.LifecycleStage != "recovery" {
		t.Fatalf("unexpected run query response: %+v", resp)
	}
}

func TestInternalAPINodeRunQuery_ByRequestID(t *testing.T) {
	project := &testNodeProject{
		runView: &NodeRunView{
			RunID:          77,
			TaskRunID:      77,
			ProjectKey:     "demo",
			RequestID:      "req-77",
			RunStatus:      "reconciling",
			LifecycleStage: "recovery",
			VerifyTarget:   "test",
			SnapshotID:     "snap-77",
		},
	}
	svc := startTestInternalAPIForNode(t, project)

	status, body := postNodeAPI(t, svc, "/api/node/run/query", nodeagentsvc.RunQueryRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			RequestID:       "req-77",
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
	}, true)
	if status != http.StatusOK {
		t.Fatalf("unexpected run query status: %d body=%s", status, string(body))
	}
	var resp nodeagentsvc.RunQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode run query failed: %v", err)
	}
	if !resp.Found || resp.RunID != 77 || resp.Status != "reconciling" || resp.LifecycleStage != "recovery" {
		t.Fatalf("unexpected run query by request id response: %+v", resp)
	}
}

func TestInternalAPINodeRunSubmit_RejectsStaleSessionEpoch(t *testing.T) {
	project := &testNodeProject{
		lease: NodeSessionLease{
			Name:         "node-a",
			SessionEpoch: 3,
			LastSeenAt:   time.Now(),
		},
	}
	svc := startTestInternalAPIForNode(t, project)

	status, body := postNodeAPI(t, svc, "/api/node/run/submit", nodeagentsvc.RunSubmitRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      "demo",
			RequestID:       "run-req-stale",
			SessionEpoch:    2,
			ProtocolVersion: nodeagentsvc.ProtocolVersionV1,
		},
		NodeName:     "node-a",
		VerifyTarget: "test",
		SnapshotID:   "snap-stale",
	}, true)
	if status != http.StatusConflict {
		t.Fatalf("unexpected stale run submit status: %d body=%s", status, string(body))
	}
	if !strings.Contains(string(body), "session") {
		t.Fatalf("expected session conflict body, got=%s", string(body))
	}
}
