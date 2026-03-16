package nodeagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWorkerLoop_RegisterHeartbeatAndInspectRun(t *testing.T) {
	var gotPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			var req RegisterRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode register failed: %v", err)
			}
			if req.Name != "node-c" || req.Meta.ProjectKey != "demo" {
				t.Fatalf("unexpected register request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         req.Name,
				SessionEpoch: 5,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			var req HeartbeatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode heartbeat failed: %v", err)
			}
			if req.Name != "node-c" || req.SessionEpoch != 5 {
				t.Fatalf("unexpected heartbeat request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         req.Name,
				SessionEpoch: req.SessionEpoch,
				Status:       "online",
			})
		case "/api/node/run/query":
			_ = json.NewEncoder(w).Encode(RunQueryResponse{
				Found:          true,
				RunID:          88,
				TaskRunID:      88,
				Status:         "succeeded",
				Summary:        "verify accepted for target=test",
				SnapshotID:     "snap-88",
				VerifyTarget:   "test",
				ArtifactCount:  1,
				LastEventType:  "run_verify_succeeded",
				LastEventNote:  "verify accepted for target=test",
				ProtocolSource: ProtocolVersionV1,
				UpdatedAt:      time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC),
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(RunLogsResponse{
				Found: true,
				RunID: 88,
				Tail:  "2026-03-14T12:00:00Z run_verify_succeeded verify accepted for target=test",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(RunArtifactsResponse{
				Found: true,
				RunID: 88,
				Artifacts: []ArtifactSummary{
					{Name: "apply-plan.json", Kind: "plan", Ref: "/tmp/run-88/apply-plan.json"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:       "demo",
		NodeName:         "node-c",
		ProtocolVersion:  ProtocolVersionV1,
		RoleCapabilities: []string{"run"},
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	fixedNow := time.Date(2026, 3, 14, 12, 30, 0, 0, time.UTC)
	loop.now = func() time.Time { return fixedNow }

	registerResp, err := loop.Register(context.Background())
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if !registerResp.Accepted || loop.SessionEpoch() != 5 {
		t.Fatalf("unexpected register state: resp=%+v epoch=%d", registerResp, loop.SessionEpoch())
	}

	heartbeatResp, err := loop.Heartbeat(context.Background())
	if err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}
	if !heartbeatResp.Accepted || loop.LastHeartbeat() != fixedNow.UTC() {
		t.Fatalf("unexpected heartbeat state: resp=%+v last=%s", heartbeatResp, loop.LastHeartbeat())
	}

	inspect, err := loop.InspectRun(context.Background(), 88, 20)
	if err != nil {
		t.Fatalf("InspectRun failed: %v", err)
	}
	if !inspect.Run.Found || inspect.Run.LastEventType != "run_verify_succeeded" {
		t.Fatalf("unexpected inspect run response: %+v", inspect.Run)
	}
	if !strings.Contains(inspect.Logs.Tail, "run_verify_succeeded") {
		t.Fatalf("unexpected inspect logs: %+v", inspect.Logs)
	}
	if len(inspect.Artifacts.Artifacts) != 1 || inspect.Artifacts.Artifacts[0].Name != "apply-plan.json" {
		t.Fatalf("unexpected inspect artifacts: %+v", inspect.Artifacts)
	}
	if len(gotPaths) != 5 {
		t.Fatalf("unexpected call count: got=%d paths=%v", len(gotPaths), gotPaths)
	}
}

func TestWorkerLoop_HeartbeatRequiresRegister(t *testing.T) {
	client, err := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	if _, err := loop.Heartbeat(context.Background()); err == nil || !strings.Contains(err.Error(), "register") {
		t.Fatalf("expected register prerequisite error, got=%v", err)
	}
}

func TestWorkerLoop_RunHeartbeatLoop(t *testing.T) {
	var registerCount int
	var heartbeatCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			registerCount++
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 9,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			heartbeatCount++
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 9,
				Status:       "online",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	result, err := loop.RunHeartbeatLoop(context.Background(), HeartbeatLoopConfig{
		Count:    3,
		Interval: 0,
	})
	if err != nil {
		t.Fatalf("RunHeartbeatLoop failed: %v", err)
	}
	if !result.Register.Accepted || !result.LastHeartbeat.Accepted || result.HeartbeatCount != 3 {
		t.Fatalf("unexpected loop result: %+v", result)
	}
	if registerCount != 1 || heartbeatCount != 3 {
		t.Fatalf("unexpected server call count: register=%d heartbeat=%d", registerCount, heartbeatCount)
	}
}

func TestWorkerLoop_SubmitAndInspectRun(t *testing.T) {
	var gotSubmit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 12,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 12,
				Status:       "online",
			})
		case "/api/node/run/submit":
			gotSubmit = true
			var req RunSubmitRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode submit failed: %v", err)
			}
			if req.NodeName != "node-c" || req.VerifyTarget != "test" || req.SnapshotID != "snap-77" || req.Meta.SessionEpoch != 12 {
				t.Fatalf("unexpected submit request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(RunSubmitResponse{
				Accepted:  true,
				RunID:     77,
				TaskRunID: 77,
				RequestID: "req-77",
				Status:    "succeeded",
			})
		case "/api/node/run/query":
			_ = json.NewEncoder(w).Encode(RunQueryResponse{
				Found:         true,
				RunID:         77,
				TaskRunID:     77,
				Status:        "succeeded",
				VerifyTarget:  "test",
				ArtifactCount: 1,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(RunLogsResponse{
				Found: true,
				RunID: 77,
				Tail:  "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(RunArtifactsResponse{
				Found: true,
				RunID: 77,
				Artifacts: []ArtifactSummary{
					{Name: "apply-plan.json", Kind: "plan"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	if _, err := loop.RunHeartbeatLoop(context.Background(), HeartbeatLoopConfig{Count: 1}); err != nil {
		t.Fatalf("RunHeartbeatLoop failed: %v", err)
	}
	res, err := loop.SubmitAndInspectRun(context.Background(), SubmitRunInput{
		RequestID:    "req-77",
		VerifyTarget: "test",
		SnapshotID:   "snap-77",
		BaseCommit:   "abc123",
	}, 20)
	if err != nil {
		t.Fatalf("SubmitAndInspectRun failed: %v", err)
	}
	if !gotSubmit || !res.Submission.Accepted || res.Submission.RunID != 77 {
		t.Fatalf("unexpected submission result: %+v", res.Submission)
	}
	if !res.Inspect.Run.Found || res.Inspect.Run.RunID != 77 || len(res.Inspect.Artifacts.Artifacts) != 1 {
		t.Fatalf("unexpected inspect result: %+v", res.Inspect)
	}
}

func TestWorkerLoop_UploadSnapshotThenSubmitRun(t *testing.T) {
	var gotUpload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 16,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 16,
				Status:       "online",
			})
		case "/api/node/snapshot/upload":
			gotUpload = true
			var req SnapshotUploadRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode upload failed: %v", err)
			}
			if req.SnapshotID != "snap-16" || req.NodeName != "node-c" || req.Meta.SessionEpoch != 16 {
				t.Fatalf("unexpected upload request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(SnapshotUploadResponse{
				Accepted:            true,
				SnapshotID:          "snap-16",
				Status:              "ready",
				ManifestDigest:      "sha256:abc",
				ArtifactPath:        "/tmp/snap-16/manifest.json",
				BaseCommit:          "abc123",
				WorkspaceGeneration: "wg-16",
			})
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(RunSubmitResponse{
				Accepted:  true,
				RunID:     116,
				TaskRunID: 116,
				RequestID: "req-116",
				Status:    "succeeded",
			})
		case "/api/node/run/query":
			_ = json.NewEncoder(w).Encode(RunQueryResponse{
				Found:         true,
				RunID:         116,
				TaskRunID:     116,
				Status:        "succeeded",
				SnapshotID:    "snap-16",
				VerifyTarget:  "test",
				ArtifactCount: 1,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(RunLogsResponse{
				Found: true,
				RunID: 116,
				Tail:  "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(RunArtifactsResponse{
				Found: true,
				RunID: 116,
				Artifacts: []ArtifactSummary{
					{Name: "apply-plan.json", Kind: "plan"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	if _, err := loop.RunHeartbeatLoop(context.Background(), HeartbeatLoopConfig{Count: 1}); err != nil {
		t.Fatalf("RunHeartbeatLoop failed: %v", err)
	}
	upload, err := loop.UploadSnapshot(context.Background(), UploadSnapshotInput{
		SnapshotID:          "snap-16",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-16",
		ManifestJSON:        `{"files":[{"path":"go.mod","sha256":"deadbeef"}]}`,
	})
	if err != nil {
		t.Fatalf("UploadSnapshot failed: %v", err)
	}
	if !gotUpload || !upload.Accepted || upload.SnapshotID != "snap-16" {
		t.Fatalf("unexpected upload result: %+v", upload)
	}
	res, err := loop.SubmitAndInspectRun(context.Background(), SubmitRunInput{
		RequestID:    "req-116",
		VerifyTarget: "test",
		SnapshotID:   upload.SnapshotID,
		BaseCommit:   upload.BaseCommit,
	}, 20)
	if err != nil {
		t.Fatalf("SubmitAndInspectRun failed: %v", err)
	}
	if !res.Inspect.Run.Found || res.Inspect.Run.SnapshotID != "snap-16" {
		t.Fatalf("unexpected inspect result: %+v", res.Inspect.Run)
	}
}

func TestWorkerLoop_DownloadSnapshotAndApply(t *testing.T) {
	var gotDownload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 20,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 20,
				Status:       "online",
			})
		case "/api/node/snapshot/download":
			gotDownload = true
			var req SnapshotDownloadRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode download failed: %v", err)
			}
			if req.Meta.SessionEpoch != 20 {
				t.Fatalf("unexpected download request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(SnapshotDownloadResponse{
				Found:               true,
				SnapshotID:          "snap-20",
				Status:              "ready",
				ManifestDigest:      "sha256:snap-20",
				ManifestJSON:        `{"base_commit":"abc123","workspace_generation":"wg-20","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
				ArtifactPath:        "/tmp/snap-20/manifest.json",
				BaseCommit:          "abc123",
				WorkspaceGeneration: "wg-20",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
		SnapshotDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	if _, err := loop.RunHeartbeatLoop(context.Background(), HeartbeatLoopConfig{Count: 1}); err != nil {
		t.Fatalf("RunHeartbeatLoop failed: %v", err)
	}
	res, err := loop.DownloadSnapshotAndApply(context.Background(), ApplySnapshotInput{
		SnapshotID:   "snap-20",
		BaseCommit:   "abc123",
		WorkspaceDir: "/tmp/run-20",
	})
	if err != nil {
		t.Fatalf("DownloadSnapshotAndApply failed: %v", err)
	}
	if !gotDownload || res.SnapshotID != "snap-20" || res.AppliedFileCount != 1 {
		t.Fatalf("unexpected apply result: %+v", res)
	}
	if _, err := os.Stat(res.PlanPath); err != nil {
		t.Fatalf("expected plan file to exist: %v", err)
	}
}

func TestWorkerLoop_UploadSnapshot_UsesChunkUploadForLargeManifest(t *testing.T) {
	var uploadCalls int
	var chunkCalls int
	var totalChunkBytes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 30,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 30,
				Status:       "online",
			})
		case "/api/node/snapshot/upload":
			uploadCalls++
			t.Fatalf("large manifest should use chunk upload")
		case "/api/node/snapshot/upload-chunk":
			chunkCalls++
			var req SnapshotChunkUploadRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode chunk upload failed: %v", err)
			}
			raw, err := decodeChunkPayload(req.ChunkData)
			if err != nil {
				t.Fatalf("decode chunk payload failed: %v", err)
			}
			totalChunkBytes += len(raw)
			_ = json.NewEncoder(w).Encode(SnapshotChunkUploadResponse{
				Accepted:            true,
				SnapshotID:          req.SnapshotID,
				Status:              map[bool]string{true: "ready", false: "preparing"}[req.IsFinal],
				NextIndex:           req.ChunkIndex + 1,
				ManifestDigest:      "sha256:big-manifest",
				ArtifactPath:        "/tmp/" + req.SnapshotID + "/manifest.json",
				BaseCommit:          "abc123",
				WorkspaceGeneration: "wg-big",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	if _, err := loop.RunHeartbeatLoop(context.Background(), HeartbeatLoopConfig{Count: 1}); err != nil {
		t.Fatalf("RunHeartbeatLoop failed: %v", err)
	}
	largeManifest := `{"base_commit":"abc123","workspace_generation":"wg-big","files":[{"path":"go.mod","digest":"sha256:deadbeef"}],"padding":"` +
		strings.Repeat("x", defaultSnapshotUploadChunkBytes+1024) + `"}`
	res, err := loop.UploadSnapshot(context.Background(), UploadSnapshotInput{
		SnapshotID:          "snap-big",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-big",
		ManifestJSON:        largeManifest,
	})
	if err != nil {
		t.Fatalf("UploadSnapshot failed: %v", err)
	}
	if uploadCalls != 0 {
		t.Fatalf("unexpected direct upload calls: %d", uploadCalls)
	}
	if chunkCalls < 2 {
		t.Fatalf("expected chunk upload to split manifest, got chunkCalls=%d", chunkCalls)
	}
	if totalChunkBytes != len(largeManifest) {
		t.Fatalf("unexpected chunk byte total: got=%d want=%d", totalChunkBytes, len(largeManifest))
	}
	if !res.Accepted || res.SnapshotID != "snap-big" || res.Status != "ready" {
		t.Fatalf("unexpected upload result: %+v", res)
	}
}

func TestWorkerLoop_SubmitWaitAndInspectRun(t *testing.T) {
	var queryCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 22,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 22,
				Status:       "online",
			})
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(RunSubmitResponse{
				Accepted:  true,
				RunID:     122,
				TaskRunID: 122,
				RequestID: "req-122",
				Status:    "running",
			})
		case "/api/node/run/query":
			queryCount++
			status := "running"
			if queryCount >= 2 {
				status = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(RunQueryResponse{
				Found:        true,
				RunID:        122,
				TaskRunID:    122,
				Status:       status,
				VerifyTarget: "test",
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(RunLogsResponse{
				Found: true,
				RunID: 122,
				Tail:  "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(RunArtifactsResponse{
				Found: true,
				RunID: 122,
				Artifacts: []ArtifactSummary{
					{Name: "apply-plan.json", Kind: "plan"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	if _, err := loop.RunHeartbeatLoop(context.Background(), HeartbeatLoopConfig{Count: 1}); err != nil {
		t.Fatalf("RunHeartbeatLoop failed: %v", err)
	}
	res, err := loop.SubmitWaitAndInspectRun(context.Background(), SubmitRunInput{
		RequestID:    "req-122",
		VerifyTarget: "test",
	}, WaitConfig{PollInterval: 1 * time.Millisecond}, 20)
	if err != nil {
		t.Fatalf("SubmitWaitAndInspectRun failed: %v", err)
	}
	if res.PollCount < 2 || !res.Inspect.Run.Found || res.Inspect.Run.Status != "succeeded" {
		t.Fatalf("unexpected wait result: %+v", res)
	}
}

func TestWorkerLoop_WaitForRunTerminalSendsHeartbeat(t *testing.T) {
	var heartbeatCount int
	var queryCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 25,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			heartbeatCount++
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 25,
				Status:       "online",
			})
		case "/api/node/run/query":
			queryCount++
			status := "running"
			if queryCount >= 2 {
				status = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(RunQueryResponse{
				Found:     true,
				RunID:     125,
				TaskRunID: 125,
				Status:    status,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(RunLogsResponse{
				Found: true,
				RunID: 125,
				Tail:  "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(RunArtifactsResponse{
				Found: true,
				RunID: 125,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	loop.now = func() time.Time {
		current := now
		now = now.Add(2 * time.Second)
		return current
	}
	if _, err := loop.RunHeartbeatLoop(context.Background(), HeartbeatLoopConfig{Count: 1}); err != nil {
		t.Fatalf("RunHeartbeatLoop failed: %v", err)
	}
	inspect, pollCount, waitHeartbeatCount, err := loop.WaitForRunTerminal(context.Background(), 125, WaitConfig{
		PollInterval:      1 * time.Millisecond,
		HeartbeatInterval: 1 * time.Second,
	}, 20)
	if err != nil {
		t.Fatalf("WaitForRunTerminal failed: %v", err)
	}
	if !inspect.Run.Found || inspect.Run.Status != "succeeded" || pollCount < 2 {
		t.Fatalf("unexpected inspect result: inspect=%+v polls=%d", inspect, pollCount)
	}
	if heartbeatCount < 2 || waitHeartbeatCount < 1 {
		t.Fatalf("expected wait heartbeat activity: server=%d wait=%d", heartbeatCount, waitHeartbeatCount)
	}
}

func TestWorkerLoop_WaitForStageChange(t *testing.T) {
	var queryCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 33,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 33,
				Status:       "online",
			})
		case "/api/node/run/query":
			queryCount++
			stage := "recovery"
			if queryCount >= 2 {
				stage = "ready"
			}
			_ = json.NewEncoder(w).Encode(RunQueryResponse{
				Found:          true,
				RunID:          133,
				TaskRunID:      133,
				Status:         "node_offline",
				LifecycleStage: stage,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(RunLogsResponse{
				Found: true,
				RunID: 133,
				Tail:  "ready logs",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(RunArtifactsResponse{
				Found: true,
				RunID: 133,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	if _, err := loop.RunHeartbeatLoop(context.Background(), HeartbeatLoopConfig{Count: 1}); err != nil {
		t.Fatalf("RunHeartbeatLoop failed: %v", err)
	}
	res, err := loop.WaitForStageChange(context.Background(), 133, "recovery", WaitConfig{
		PollInterval:      1 * time.Millisecond,
		HeartbeatInterval: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("WaitForStageChange failed: %v", err)
	}
	if res.PollCount < 2 || res.Inspect.Run.Status != "node_offline" || res.Inspect.Run.LifecycleStage != "ready" {
		t.Fatalf("unexpected stage watch result: %+v", res)
	}
	if queryCount < 2 {
		t.Fatalf("expected multiple stage polls, got=%d", queryCount)
	}
}

func TestWorkerLoop_HeartbeatAutoReregisterOnSessionConflict(t *testing.T) {
	var registerCount int
	var heartbeatCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			registerCount++
			epoch := 40
			if registerCount >= 2 {
				epoch = 41
			}
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: epoch,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			heartbeatCount++
			var req HeartbeatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode heartbeat failed: %v", err)
			}
			if heartbeatCount == 1 {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": "session_conflict",
					"cause": "node session epoch 不匹配: name=node-c want=40 got=41",
				})
				return
			}
			if req.SessionEpoch != 41 {
				t.Fatalf("expected recovered heartbeat with epoch=41, got=%d", req.SessionEpoch)
			}
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         "node-c",
				SessionEpoch: 41,
				Status:       "online",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, AuthToken: "node-token-1"})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	loop, err := NewWorkerLoop(client, WorkerLoopConfig{
		ProjectKey:      "demo",
		NodeName:        "node-c",
		ProtocolVersion: ProtocolVersionV1,
	})
	if err != nil {
		t.Fatalf("NewWorkerLoop failed: %v", err)
	}
	if _, err := loop.Register(context.Background()); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	resp, err := loop.Heartbeat(context.Background())
	if err != nil {
		t.Fatalf("Heartbeat with recovery failed: %v", err)
	}
	if !resp.Accepted || loop.SessionEpoch() != 41 {
		t.Fatalf("unexpected recovered heartbeat response: resp=%+v epoch=%d", resp, loop.SessionEpoch())
	}
	if registerCount != 2 || heartbeatCount != 2 {
		t.Fatalf("unexpected recovery call count: register=%d heartbeat=%d", registerCount, heartbeatCount)
	}
}

func decodeChunkPayload(raw string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
}
