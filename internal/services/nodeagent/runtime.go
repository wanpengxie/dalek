package nodeagent

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/contracts"
	snapshotsvc "dalek/internal/services/snapshot"
)

type WorkerLoopConfig struct {
	ProjectKey       string
	NodeName         string
	Endpoint         string
	AuthMode         string
	Version          string
	ProtocolVersion  string
	SessionAffinity  string
	DefaultProvider  string
	RoleCapabilities []string
	ProviderModes    []string
	SnapshotDir      string
}

type WorkerLoop struct {
	client *Client
	cfg    WorkerLoopConfig
	now    func() time.Time

	sessionEpoch  int
	registered    bool
	lastHeartbeat time.Time
}

type HeartbeatLoopConfig struct {
	Count    int
	Interval time.Duration
}

type HeartbeatLoopResult struct {
	Register       RegisterResponse
	LastHeartbeat  HeartbeatResponse
	HeartbeatCount int
}

type SubmitRunInput struct {
	RequestID    string
	VerifyTarget string
	SnapshotID   string
	BaseCommit   string
}

type UploadSnapshotInput struct {
	SnapshotID          string
	BaseCommit          string
	WorkspaceGeneration string
	ManifestJSON        string
	ExpiresAt           *time.Time
}

type UploadSnapshotChunkInput struct {
	SnapshotID          string
	BaseCommit          string
	WorkspaceGeneration string
	ChunkIndex          int
	ChunkData           []byte
	IsFinal             bool
	ExpiresAt           *time.Time
}

type ApplySnapshotInput struct {
	SnapshotID   string
	BaseCommit   string
	WorkspaceDir string
}

type ApplySnapshotResult struct {
	SnapshotID          string
	BaseCommit          string
	WorkspaceGeneration string
	ManifestDigest      string
	PlanPath            string
	WorkspaceDir        string
	AppliedFileCount    int
	Summary             string
}

type SubmitAndInspectResult struct {
	Submission     RunSubmitResponse
	Inspect        InspectRunResult
	PollCount      int
	HeartbeatCount int
}

type InspectRunResult struct {
	Run       RunQueryResponse
	Logs      RunLogsResponse
	Artifacts RunArtifactsResponse
}

type WaitConfig struct {
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
}

type StageWatchResult struct {
	Inspect        InspectRunResult
	PollCount      int
	HeartbeatCount int
}

const defaultSnapshotUploadChunkBytes = 192 * 1024

func NewWorkerLoop(client *Client, cfg WorkerLoopConfig) (*WorkerLoop, error) {
	if client == nil {
		return nil, fmt.Errorf("node agent client 为空")
	}
	if strings.TrimSpace(cfg.ProjectKey) == "" {
		return nil, fmt.Errorf("project_key 不能为空")
	}
	if strings.TrimSpace(cfg.NodeName) == "" {
		return nil, fmt.Errorf("node_name 不能为空")
	}
	if strings.TrimSpace(cfg.ProtocolVersion) == "" {
		cfg.ProtocolVersion = ProtocolVersionV1
	}
	return &WorkerLoop{
		client: client,
		cfg:    cfg,
		now:    time.Now,
	}, nil
}

func (l *WorkerLoop) SessionEpoch() int {
	if l == nil {
		return 0
	}
	return l.sessionEpoch
}

func (l *WorkerLoop) LastHeartbeat() time.Time {
	if l == nil {
		return time.Time{}
	}
	return l.lastHeartbeat
}

func (l *WorkerLoop) Register(ctx context.Context) (RegisterResponse, error) {
	if l == nil || l.client == nil {
		return RegisterResponse{}, fmt.Errorf("worker loop 未初始化")
	}
	now := l.now().UTC()
	resp, err := l.client.Register(ctx, RegisterRequest{
		Meta: RequestMeta{
			ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
			ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
			SentAt:          now.Format(time.RFC3339),
		},
		Name:             strings.TrimSpace(l.cfg.NodeName),
		Endpoint:         strings.TrimSpace(l.cfg.Endpoint),
		AuthMode:         strings.TrimSpace(l.cfg.AuthMode),
		Version:          strings.TrimSpace(l.cfg.Version),
		ProtocolVersion:  strings.TrimSpace(l.cfg.ProtocolVersion),
		SessionAffinity:  strings.TrimSpace(l.cfg.SessionAffinity),
		DefaultProvider:  strings.TrimSpace(l.cfg.DefaultProvider),
		RoleCapabilities: append([]string(nil), l.cfg.RoleCapabilities...),
		ProviderModes:    append([]string(nil), l.cfg.ProviderModes...),
		LastSeenAt:       now.Format(time.RFC3339),
	})
	if err != nil {
		return RegisterResponse{}, err
	}
	if resp.Accepted {
		l.sessionEpoch = resp.SessionEpoch
		l.registered = true
	}
	return resp, nil
}

func (l *WorkerLoop) Heartbeat(ctx context.Context) (HeartbeatResponse, error) {
	resp, err := l.heartbeatOnce(ctx)
	if err == nil {
		return resp, nil
	}
	if !isRecoverableSessionError(err) {
		return HeartbeatResponse{}, err
	}
	registerResp, regErr := l.Register(ctx)
	if regErr != nil {
		return HeartbeatResponse{}, regErr
	}
	if !registerResp.Accepted || l.sessionEpoch <= 0 {
		return HeartbeatResponse{}, fmt.Errorf("worker loop re-register 未被接受")
	}
	return l.heartbeatOnce(ctx)
}

func (l *WorkerLoop) heartbeatOnce(ctx context.Context) (HeartbeatResponse, error) {
	if l == nil || l.client == nil {
		return HeartbeatResponse{}, fmt.Errorf("worker loop 未初始化")
	}
	if !l.registered || l.sessionEpoch <= 0 {
		return HeartbeatResponse{}, fmt.Errorf("worker loop 尚未 register")
	}
	now := l.now().UTC()
	resp, err := l.client.Heartbeat(ctx, HeartbeatRequest{
		Meta: RequestMeta{
			ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
			ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
			SentAt:          now.Format(time.RFC3339),
		},
		Name:         strings.TrimSpace(l.cfg.NodeName),
		SessionEpoch: l.sessionEpoch,
		ObservedAt:   now.Format(time.RFC3339),
		Status:       "online",
	})
	if err != nil {
		return HeartbeatResponse{}, err
	}
	if resp.Accepted {
		l.sessionEpoch = resp.SessionEpoch
		l.lastHeartbeat = now
	}
	return resp, nil
}

func (l *WorkerLoop) RunHeartbeatLoop(ctx context.Context, cfg HeartbeatLoopConfig) (HeartbeatLoopResult, error) {
	if l == nil || l.client == nil {
		return HeartbeatLoopResult{}, fmt.Errorf("worker loop 未初始化")
	}
	if cfg.Count <= 0 {
		return HeartbeatLoopResult{}, fmt.Errorf("heartbeat count 必须大于 0")
	}
	if cfg.Interval < 0 {
		return HeartbeatLoopResult{}, fmt.Errorf("heartbeat interval 不能为负值")
	}

	registerResp, err := l.Register(ctx)
	if err != nil {
		return HeartbeatLoopResult{}, err
	}
	result := HeartbeatLoopResult{Register: registerResp}
	for i := 0; i < cfg.Count; i++ {
		heartbeatResp, err := l.Heartbeat(ctx)
		if err != nil {
			return HeartbeatLoopResult{}, err
		}
		result.LastHeartbeat = heartbeatResp
		result.HeartbeatCount++
		if i+1 < cfg.Count && cfg.Interval > 0 {
			select {
			case <-ctx.Done():
				return HeartbeatLoopResult{}, ctx.Err()
			case <-time.After(cfg.Interval):
			}
		}
	}
	return result, nil
}

func (l *WorkerLoop) SubmitRun(ctx context.Context, in SubmitRunInput) (RunSubmitResponse, error) {
	if l == nil || l.client == nil {
		return RunSubmitResponse{}, fmt.Errorf("worker loop 未初始化")
	}
	if !l.registered || l.sessionEpoch <= 0 {
		return RunSubmitResponse{}, fmt.Errorf("worker loop 尚未 register")
	}
	verifyTarget := strings.TrimSpace(in.VerifyTarget)
	if verifyTarget == "" {
		return RunSubmitResponse{}, fmt.Errorf("verify_target 不能为空")
	}
	now := l.now().UTC()
	return l.client.SubmitRun(ctx, RunSubmitRequest{
		Meta: RequestMeta{
			RequestID:       strings.TrimSpace(in.RequestID),
			ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
			SessionEpoch:    l.sessionEpoch,
			ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
			SentAt:          now.Format(time.RFC3339),
		},
		NodeName:     strings.TrimSpace(l.cfg.NodeName),
		VerifyTarget: verifyTarget,
		SnapshotID:   strings.TrimSpace(in.SnapshotID),
		BaseCommit:   strings.TrimSpace(in.BaseCommit),
	})
}

func (l *WorkerLoop) UploadSnapshot(ctx context.Context, in UploadSnapshotInput) (SnapshotUploadResponse, error) {
	if l == nil || l.client == nil {
		return SnapshotUploadResponse{}, fmt.Errorf("worker loop 未初始化")
	}
	if !l.registered || l.sessionEpoch <= 0 {
		return SnapshotUploadResponse{}, fmt.Errorf("worker loop 尚未 register")
	}
	snapshotID := strings.TrimSpace(in.SnapshotID)
	if snapshotID == "" {
		return SnapshotUploadResponse{}, fmt.Errorf("snapshot_id 不能为空")
	}
	manifestJSON := strings.TrimSpace(in.ManifestJSON)
	if manifestJSON == "" {
		return SnapshotUploadResponse{}, fmt.Errorf("manifest_json 不能为空")
	}
	if len(manifestJSON) > defaultSnapshotUploadChunkBytes {
		return l.uploadSnapshotInChunks(ctx, in, []byte(manifestJSON))
	}
	now := l.now().UTC()
	expiresAt := ""
	if in.ExpiresAt != nil && !in.ExpiresAt.IsZero() {
		expiresAt = in.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return l.client.UploadSnapshot(ctx, SnapshotUploadRequest{
		Meta: RequestMeta{
			ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
			SessionEpoch:    l.sessionEpoch,
			ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
			SentAt:          now.Format(time.RFC3339),
		},
		SnapshotID:          snapshotID,
		NodeName:            strings.TrimSpace(l.cfg.NodeName),
		BaseCommit:          strings.TrimSpace(in.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(in.WorkspaceGeneration),
		ManifestJSON:        manifestJSON,
		ExpiresAt:           expiresAt,
	})
}

func (l *WorkerLoop) uploadSnapshotInChunks(ctx context.Context, in UploadSnapshotInput, raw []byte) (SnapshotUploadResponse, error) {
	if len(raw) == 0 {
		return SnapshotUploadResponse{}, fmt.Errorf("manifest_json 不能为空")
	}
	chunkIndex := 0
	var last SnapshotChunkUploadResponse
	for offset := 0; offset < len(raw); offset += defaultSnapshotUploadChunkBytes {
		end := offset + defaultSnapshotUploadChunkBytes
		if end > len(raw) {
			end = len(raw)
		}
		chunk := raw[offset:end]
		res, err := l.UploadSnapshotChunk(ctx, UploadSnapshotChunkInput{
			SnapshotID:          strings.TrimSpace(in.SnapshotID),
			BaseCommit:          strings.TrimSpace(in.BaseCommit),
			WorkspaceGeneration: strings.TrimSpace(in.WorkspaceGeneration),
			ChunkIndex:          chunkIndex,
			ChunkData:           chunk,
			IsFinal:             end == len(raw),
			ExpiresAt:           in.ExpiresAt,
		})
		if err != nil {
			return SnapshotUploadResponse{}, err
		}
		last = res
		chunkIndex++
	}
	return SnapshotUploadResponse{
		Accepted:            last.Accepted,
		SnapshotID:          last.SnapshotID,
		Status:              last.Status,
		ManifestDigest:      last.ManifestDigest,
		ArtifactPath:        last.ArtifactPath,
		BaseCommit:          last.BaseCommit,
		WorkspaceGeneration: last.WorkspaceGeneration,
	}, nil
}

func (l *WorkerLoop) UploadSnapshotChunk(ctx context.Context, in UploadSnapshotChunkInput) (SnapshotChunkUploadResponse, error) {
	if l == nil || l.client == nil {
		return SnapshotChunkUploadResponse{}, fmt.Errorf("worker loop 未初始化")
	}
	if !l.registered || l.sessionEpoch <= 0 {
		return SnapshotChunkUploadResponse{}, fmt.Errorf("worker loop 尚未 register")
	}
	snapshotID := strings.TrimSpace(in.SnapshotID)
	if snapshotID == "" {
		return SnapshotChunkUploadResponse{}, fmt.Errorf("snapshot_id 不能为空")
	}
	if in.ChunkIndex < 0 {
		return SnapshotChunkUploadResponse{}, fmt.Errorf("chunk_index 不能为负值")
	}
	if len(in.ChunkData) == 0 {
		return SnapshotChunkUploadResponse{}, fmt.Errorf("chunk_data 不能为空")
	}
	now := l.now().UTC()
	expiresAt := ""
	if in.ExpiresAt != nil && !in.ExpiresAt.IsZero() {
		expiresAt = in.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return l.client.UploadSnapshotChunk(ctx, SnapshotChunkUploadRequest{
		Meta: RequestMeta{
			ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
			SessionEpoch:    l.sessionEpoch,
			ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
			SentAt:          now.Format(time.RFC3339),
		},
		SnapshotID:          snapshotID,
		NodeName:            strings.TrimSpace(l.cfg.NodeName),
		BaseCommit:          strings.TrimSpace(in.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(in.WorkspaceGeneration),
		ChunkIndex:          in.ChunkIndex,
		ChunkData:           base64.StdEncoding.EncodeToString(in.ChunkData),
		IsFinal:             in.IsFinal,
		ExpiresAt:           expiresAt,
	})
}

func (l *WorkerLoop) DownloadSnapshotAndApply(ctx context.Context, in ApplySnapshotInput) (ApplySnapshotResult, error) {
	if l == nil || l.client == nil {
		return ApplySnapshotResult{}, fmt.Errorf("worker loop 未初始化")
	}
	if !l.registered || l.sessionEpoch <= 0 {
		return ApplySnapshotResult{}, fmt.Errorf("worker loop 尚未 register")
	}
	snapshotID := strings.TrimSpace(in.SnapshotID)
	if snapshotID == "" {
		return ApplySnapshotResult{}, fmt.Errorf("snapshot_id 不能为空")
	}
	download, err := l.client.DownloadSnapshot(ctx, SnapshotDownloadRequest{
		Meta: RequestMeta{
			ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
			SessionEpoch:    l.sessionEpoch,
			ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
		},
		SnapshotID: snapshotID,
	})
	if err != nil {
		return ApplySnapshotResult{}, err
	}
	if !download.Found {
		return ApplySnapshotResult{}, fmt.Errorf("snapshot 不存在: %s", snapshotID)
	}
	store, err := snapshotsvc.NewFileStore(l.snapshotRootDir())
	if err != nil {
		return ApplySnapshotResult{}, err
	}
	applySvc := snapshotsvc.NewApplyService(nil, store)
	applied, err := applySvc.ApplyManifestJSONToWorkspace(ctx, snapshotID, download.ManifestJSON, strings.TrimSpace(in.BaseCommit), strings.TrimSpace(in.WorkspaceDir))
	if err != nil {
		return ApplySnapshotResult{}, err
	}
	return ApplySnapshotResult{
		SnapshotID:          applied.SnapshotID,
		BaseCommit:          applied.BaseCommit,
		WorkspaceGeneration: applied.WorkspaceGeneration,
		ManifestDigest:      applied.ManifestDigest,
		PlanPath:            applied.PlanPath,
		WorkspaceDir:        applied.WorkspaceDir,
		AppliedFileCount:    applied.AppliedFileCount,
		Summary:             applied.Summary,
	}, nil
}

func (l *WorkerLoop) snapshotRootDir() string {
	root := strings.TrimSpace(l.cfg.SnapshotDir)
	if root == "" {
		root = filepath.Join(os.TempDir(), "dalek-snapshots", sanitizeSnapshotSegment(l.cfg.ProjectKey), sanitizeSnapshotSegment(l.cfg.NodeName))
	}
	if abs, err := filepath.Abs(root); err == nil && strings.TrimSpace(abs) != "" {
		return abs
	}
	return root
}

func sanitizeSnapshotSegment(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "default"
	}
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '_'
		}
	}, raw)
	if strings.TrimSpace(cleaned) == "" {
		return "default"
	}
	return cleaned
}

func (l *WorkerLoop) SubmitAndInspectRun(ctx context.Context, in SubmitRunInput, lines int) (SubmitAndInspectResult, error) {
	submission, err := l.SubmitRun(ctx, in)
	if err != nil {
		return SubmitAndInspectResult{}, err
	}
	result := SubmitAndInspectResult{Submission: submission}
	if !submission.Accepted || submission.RunID == 0 {
		return result, nil
	}
	inspect, err := l.InspectRun(ctx, submission.RunID, lines)
	if err != nil {
		return SubmitAndInspectResult{}, err
	}
	result.Inspect = inspect
	return result, nil
}

func (l *WorkerLoop) WaitForRunTerminal(ctx context.Context, runID uint, cfg WaitConfig, lines int) (InspectRunResult, int, int, error) {
	if l == nil || l.client == nil {
		return InspectRunResult{}, 0, 0, fmt.Errorf("worker loop 未初始化")
	}
	if runID == 0 {
		return InspectRunResult{}, 0, 0, fmt.Errorf("run_id 不能为空")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.HeartbeatInterval < 0 {
		return InspectRunResult{}, 0, 0, fmt.Errorf("heartbeat interval 不能为负值")
	}
	meta := RequestMeta{
		ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
		SessionEpoch:    l.sessionEpoch,
		RunID:           runID,
		ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
	}
	pollCount := 0
	heartbeatCount := 0
	lastHeartbeatAt := l.LastHeartbeat()
	for {
		if cfg.HeartbeatInterval > 0 && !lastHeartbeatAt.IsZero() && l.now().UTC().Sub(lastHeartbeatAt) >= cfg.HeartbeatInterval {
			if _, err := l.Heartbeat(ctx); err != nil {
				return InspectRunResult{}, pollCount, heartbeatCount, err
			}
			heartbeatCount++
			lastHeartbeatAt = l.LastHeartbeat()
		}
		runResp, err := l.client.QueryRun(ctx, RunQueryRequest{Meta: meta})
		if err != nil {
			return InspectRunResult{}, pollCount, heartbeatCount, err
		}
		pollCount++
		if !runResp.Found {
			return InspectRunResult{Run: runResp}, pollCount, heartbeatCount, nil
		}
		if isTerminalRunStatus(runResp.Status) {
			inspect, err := l.InspectRun(ctx, runID, lines)
			if err != nil {
				return InspectRunResult{}, pollCount, heartbeatCount, err
			}
			return inspect, pollCount, heartbeatCount, nil
		}
		select {
		case <-ctx.Done():
			return InspectRunResult{}, pollCount, heartbeatCount, ctx.Err()
		case <-time.After(cfg.PollInterval):
		}
	}
}

func (l *WorkerLoop) WaitForStageChange(ctx context.Context, runID uint, stage string, cfg WaitConfig) (StageWatchResult, error) {
	if l == nil || l.client == nil {
		return StageWatchResult{}, fmt.Errorf("worker loop 未初始化")
	}
	if runID == 0 {
		return StageWatchResult{}, fmt.Errorf("run_id 不能为空")
	}
	if strings.TrimSpace(stage) == "" {
		return StageWatchResult{}, fmt.Errorf("stage 不能为空")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.HeartbeatInterval < 0 {
		return StageWatchResult{}, fmt.Errorf("heartbeat interval 不能为负值")
	}

	result := StageWatchResult{}
	lastHeartbeatAt := l.LastHeartbeat()
	meta := RequestMeta{
		ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
		SessionEpoch:    l.sessionEpoch,
		RunID:           runID,
		ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
	}

	for {
		if cfg.HeartbeatInterval > 0 && !lastHeartbeatAt.IsZero() && l.now().UTC().Sub(lastHeartbeatAt) >= cfg.HeartbeatInterval {
			if _, err := l.Heartbeat(ctx); err != nil {
				return StageWatchResult{}, err
			}
			result.HeartbeatCount++
			lastHeartbeatAt = l.LastHeartbeat()
		}

		runResp, err := l.client.QueryRun(ctx, RunQueryRequest{Meta: meta})
		if err != nil {
			return StageWatchResult{}, err
		}
		result.PollCount++
		if !runResp.Found {
			return result, nil
		}
		if strings.TrimSpace(runResp.LifecycleStage) != strings.TrimSpace(stage) {
			inspect, err := l.InspectRun(ctx, runID, 20)
			if err != nil {
				return StageWatchResult{}, err
			}
			result.Inspect = inspect
			return result, nil
		}

		select {
		case <-ctx.Done():
			return StageWatchResult{}, ctx.Err()
		case <-time.After(cfg.PollInterval):
		}
	}
}

func (l *WorkerLoop) SubmitWaitAndInspectRun(ctx context.Context, in SubmitRunInput, cfg WaitConfig, lines int) (SubmitAndInspectResult, error) {
	submission, err := l.SubmitRun(ctx, in)
	if err != nil {
		return SubmitAndInspectResult{}, err
	}
	result := SubmitAndInspectResult{Submission: submission}
	if !submission.Accepted || submission.RunID == 0 {
		return result, nil
	}
	inspect, pollCount, heartbeatCount, err := l.WaitForRunTerminal(ctx, submission.RunID, cfg, lines)
	if err != nil {
		return SubmitAndInspectResult{}, err
	}
	result.Inspect = inspect
	result.PollCount = pollCount
	result.HeartbeatCount = heartbeatCount
	return result, nil
}

func (l *WorkerLoop) InspectRun(ctx context.Context, runID uint, lines int) (InspectRunResult, error) {
	if l == nil || l.client == nil {
		return InspectRunResult{}, fmt.Errorf("worker loop 未初始化")
	}
	if runID == 0 {
		return InspectRunResult{}, fmt.Errorf("run_id 不能为空")
	}
	meta := RequestMeta{
		ProjectKey:      strings.TrimSpace(l.cfg.ProjectKey),
		SessionEpoch:    l.sessionEpoch,
		RunID:           runID,
		ProtocolVersion: strings.TrimSpace(l.cfg.ProtocolVersion),
	}
	runResp, err := l.client.QueryRun(ctx, RunQueryRequest{Meta: meta})
	if err != nil {
		return InspectRunResult{}, err
	}
	if !runResp.Found {
		return InspectRunResult{Run: runResp}, nil
	}
	logsResp, err := l.client.RunLogs(ctx, RunLogsRequest{Meta: meta, Lines: lines})
	if err != nil {
		return InspectRunResult{}, err
	}
	artifactsResp, err := l.client.RunArtifacts(ctx, RunQueryRequest{Meta: meta})
	if err != nil {
		return InspectRunResult{}, err
	}
	return InspectRunResult{
		Run:       runResp,
		Logs:      logsResp,
		Artifacts: artifactsResp,
	}, nil
}

func isTerminalRunStatus(raw string) bool {
	switch contracts.RunStatus(strings.TrimSpace(raw)) {
	case contracts.RunSucceeded, contracts.RunFailed, contracts.RunCanceled, contracts.RunTimedOut:
		return true
	default:
		return false
	}
}

func isRecoverableSessionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.TrimSpace(strings.ToLower(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "session epoch") ||
		strings.Contains(msg, "session_conflict") ||
		strings.Contains(msg, "不存在") ||
		strings.Contains(msg, "not found")
}
