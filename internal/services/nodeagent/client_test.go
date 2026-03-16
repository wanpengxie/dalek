package nodeagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClient_RegisterHeartbeatAndQueryRun(t *testing.T) {
	var gotAuth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, strings.TrimSpace(r.Header.Get("Authorization")))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			var req RegisterRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode register failed: %v", err)
			}
			if req.Name != "node-a" || req.Meta.ProjectKey != "demo" {
				t.Fatalf("unexpected register request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(RegisterResponse{
				Accepted:     true,
				Name:         req.Name,
				SessionEpoch: 2,
				Status:       "online",
			})
		case "/api/node/heartbeat":
			var req HeartbeatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode heartbeat failed: %v", err)
			}
			if req.SessionEpoch != 2 {
				t.Fatalf("unexpected heartbeat request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Accepted:     true,
				Name:         req.Name,
				SessionEpoch: req.SessionEpoch,
				Status:       "online",
			})
		case "/api/node/run/submit":
			var req RunSubmitRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode run submit failed: %v", err)
			}
			if req.VerifyTarget != "test" || req.Meta.RequestID != "run-req-1" {
				t.Fatalf("unexpected run submit request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(RunSubmitResponse{
				Accepted:  true,
				RunID:     42,
				TaskRunID: 42,
				RequestID: req.Meta.RequestID,
				Status:    "snapshot_ready",
			})
		case "/api/node/run/query":
			var req RunQueryRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode run query failed: %v", err)
			}
			if req.Meta.RunID != 42 {
				t.Fatalf("unexpected run query request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(RunQueryResponse{
				Found:          true,
				RunID:          42,
				TaskRunID:      42,
				Status:         "snapshot_ready",
				LifecycleStage: "ready",
				Summary:        "verify prepared",
				VerifyTarget:   "go test ./...",
				SnapshotID:     "snap-1",
				ArtifactCount:  1,
				LastEventType:  "run_verify_prepared",
				LastEventNote:  "verify accepted for target=test",
				UpdatedAt:      time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC),
				ProtocolSource: ProtocolVersionV1,
			})
		case "/api/node/run/logs":
			var req RunLogsRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode run logs failed: %v", err)
			}
			_ = json.NewEncoder(w).Encode(RunLogsResponse{
				Found: true,
				RunID: req.Meta.RunID,
				Tail:  "2026-03-14T12:00:00Z run_preflight_accepted",
			})
		case "/api/node/run/artifacts":
			var req RunQueryRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode run artifacts failed: %v", err)
			}
			_ = json.NewEncoder(w).Encode(RunArtifactsResponse{
				Found: true,
				RunID: req.Meta.RunID,
				Artifacts: []ArtifactSummary{
					{Name: "apply-plan.json", Kind: "plan", Ref: "/tmp/run-42/apply-plan.json"},
				},
				Issues: []ArtifactIssue{
					{Name: "report.json", Status: "upload_failed", Reason: "upload failed"},
				},
			})
		case "/api/node/run/cancel":
			var req RunCancelRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode run cancel failed: %v", err)
			}
			_ = json.NewEncoder(w).Encode(RunCancelResponse{
				Accepted: true,
				Found:    true,
				Canceled: true,
				Reason:   "cancel signal sent",
			})
		case "/api/node/snapshot/upload":
			var req SnapshotUploadRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode snapshot upload failed: %v", err)
			}
			_ = json.NewEncoder(w).Encode(SnapshotUploadResponse{
				Accepted:            true,
				SnapshotID:          req.SnapshotID,
				Status:              "ready",
				ManifestDigest:      "sha256:snapshot-upload",
				ArtifactPath:        "/tmp/" + req.SnapshotID + "/manifest.json",
				BaseCommit:          req.BaseCommit,
				WorkspaceGeneration: req.WorkspaceGeneration,
			})
		case "/api/node/snapshot/upload-chunk":
			var req SnapshotChunkUploadRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode snapshot chunk upload failed: %v", err)
			}
			status := "preparing"
			nextIndex := req.ChunkIndex + 1
			if req.IsFinal {
				status = "ready"
				nextIndex = 0
			}
			_ = json.NewEncoder(w).Encode(SnapshotChunkUploadResponse{
				Accepted:            true,
				SnapshotID:          req.SnapshotID,
				Status:              status,
				NextIndex:           nextIndex,
				ManifestDigest:      "sha256:snapshot-upload",
				ArtifactPath:        "/tmp/" + req.SnapshotID + "/manifest.json",
				BaseCommit:          req.BaseCommit,
				WorkspaceGeneration: req.WorkspaceGeneration,
			})
		case "/api/node/snapshot/download":
			var req SnapshotDownloadRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode snapshot download failed: %v", err)
			}
			_ = json.NewEncoder(w).Encode(SnapshotDownloadResponse{
				Found:               true,
				SnapshotID:          req.SnapshotID,
				Status:              "ready",
				ManifestDigest:      "sha256:snapshot-upload",
				ManifestJSON:        `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
				ArtifactPath:        "/tmp/" + req.SnapshotID + "/manifest.json",
				BaseCommit:          "abc123",
				WorkspaceGeneration: "wg-1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:   server.URL,
		AuthToken: "node-token-1",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	registerResp, err := client.Register(context.Background(), RegisterRequest{
		Meta:             RequestMeta{ProjectKey: "demo", ProtocolVersion: ProtocolVersionV1},
		Name:             "node-a",
		ProtocolVersion:  ProtocolVersionV1,
		RoleCapabilities: []string{"run"},
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if !registerResp.Accepted || registerResp.SessionEpoch != 2 {
		t.Fatalf("unexpected register response: %+v", registerResp)
	}

	heartbeatResp, err := client.Heartbeat(context.Background(), HeartbeatRequest{
		Meta:         RequestMeta{ProjectKey: "demo", ProtocolVersion: ProtocolVersionV1},
		Name:         "node-a",
		SessionEpoch: 2,
		ObservedAt:   time.Now().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}
	if !heartbeatResp.Accepted || heartbeatResp.SessionEpoch != 2 {
		t.Fatalf("unexpected heartbeat response: %+v", heartbeatResp)
	}

	submitResp, err := client.SubmitRun(context.Background(), RunSubmitRequest{
		Meta: RequestMeta{
			ProjectKey:      "demo",
			RequestID:       "run-req-1",
			ProtocolVersion: ProtocolVersionV1,
		},
		NodeName:     "node-a",
		VerifyTarget: "test",
		SnapshotID:   "snap-1",
		BaseCommit:   "abc123",
	})
	if err != nil {
		t.Fatalf("SubmitRun failed: %v", err)
	}
	if !submitResp.Accepted || submitResp.RunID != 42 || submitResp.Status != "snapshot_ready" {
		t.Fatalf("unexpected submit response: %+v", submitResp)
	}

	runResp, err := client.QueryRun(context.Background(), RunQueryRequest{
		Meta: RequestMeta{
			ProjectKey:      "demo",
			RunID:           42,
			ProtocolVersion: ProtocolVersionV1,
		},
	})
	if err != nil {
		t.Fatalf("QueryRun failed: %v", err)
	}
	if !runResp.Found || runResp.RunID != 42 || runResp.Status != "snapshot_ready" || runResp.LifecycleStage != "ready" || runResp.SnapshotID != "snap-1" || runResp.ArtifactCount != 1 || runResp.LastEventType != "run_verify_prepared" {
		t.Fatalf("unexpected run query response: %+v", runResp)
	}

	logsResp, err := client.RunLogs(context.Background(), RunLogsRequest{
		Meta: RequestMeta{
			ProjectKey:      "demo",
			RunID:           42,
			ProtocolVersion: ProtocolVersionV1,
		},
		Lines: 10,
	})
	if err != nil {
		t.Fatalf("RunLogs failed: %v", err)
	}
	if !logsResp.Found || !strings.Contains(logsResp.Tail, "run_preflight_accepted") {
		t.Fatalf("unexpected logs response: %+v", logsResp)
	}

	artifactsResp, err := client.RunArtifacts(context.Background(), RunQueryRequest{
		Meta: RequestMeta{
			ProjectKey:      "demo",
			RunID:           42,
			ProtocolVersion: ProtocolVersionV1,
		},
	})
	if err != nil {
		t.Fatalf("RunArtifacts failed: %v", err)
	}
	if !artifactsResp.Found || len(artifactsResp.Artifacts) != 1 || artifactsResp.Artifacts[0].Name != "apply-plan.json" {
		t.Fatalf("unexpected artifacts response: %+v", artifactsResp)
	}
	if len(artifactsResp.Issues) != 1 || artifactsResp.Issues[0].Status != "upload_failed" {
		t.Fatalf("unexpected artifact issues response: %+v", artifactsResp)
	}

	cancelResp, err := client.CancelRun(context.Background(), RunCancelRequest{
		Meta: RequestMeta{
			ProjectKey:      "demo",
			RunID:           42,
			ProtocolVersion: ProtocolVersionV1,
		},
		Reason: "stop",
	})
	if err != nil {
		t.Fatalf("CancelRun failed: %v", err)
	}
	if !cancelResp.Found || !cancelResp.Canceled {
		t.Fatalf("unexpected cancel response: %+v", cancelResp)
	}

	uploadResp, err := client.UploadSnapshot(context.Background(), SnapshotUploadRequest{
		Meta:                RequestMeta{ProjectKey: "demo", ProtocolVersion: ProtocolVersionV1},
		SnapshotID:          "snap-1",
		NodeName:            "node-b",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		ManifestJSON:        `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
	})
	if err != nil {
		t.Fatalf("UploadSnapshot failed: %v", err)
	}
	if !uploadResp.Accepted || uploadResp.SnapshotID != "snap-1" {
		t.Fatalf("unexpected snapshot upload response: %+v", uploadResp)
	}

	chunkResp, err := client.UploadSnapshotChunk(context.Background(), SnapshotChunkUploadRequest{
		Meta:                RequestMeta{ProjectKey: "demo", ProtocolVersion: ProtocolVersionV1},
		SnapshotID:          "snap-1",
		NodeName:            "node-b",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		ChunkIndex:          0,
		ChunkData:           "bWFuaWZlc3Q=",
		IsFinal:             true,
	})
	if err != nil {
		t.Fatalf("UploadSnapshotChunk failed: %v", err)
	}
	if !chunkResp.Accepted || chunkResp.SnapshotID != "snap-1" || chunkResp.Status != "ready" {
		t.Fatalf("unexpected snapshot chunk upload response: %+v", chunkResp)
	}

	downloadResp, err := client.DownloadSnapshot(context.Background(), SnapshotDownloadRequest{
		Meta:       RequestMeta{ProjectKey: "demo", ProtocolVersion: ProtocolVersionV1},
		SnapshotID: "snap-1",
	})
	if err != nil {
		t.Fatalf("DownloadSnapshot failed: %v", err)
	}
	if !downloadResp.Found || downloadResp.SnapshotID != "snap-1" || downloadResp.Status != "ready" {
		t.Fatalf("unexpected snapshot download response: %+v", downloadResp)
	}

	for i, auth := range gotAuth {
		if auth != "Bearer node-token-1" {
			t.Fatalf("request %d missing bearer token: %q", i, auth)
		}
	}
}

func TestClient_PropagatesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "forbidden",
			"cause": "node agent token 无效",
		})
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	_, err = client.Register(context.Background(), RegisterRequest{
		Meta: RequestMeta{ProjectKey: "demo", ProtocolVersion: ProtocolVersionV1},
		Name: "node-a",
	})
	if err == nil || !strings.Contains(err.Error(), "node agent token 无效") {
		t.Fatalf("expected api cause, got=%v", err)
	}
}
