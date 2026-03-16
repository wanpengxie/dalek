package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"dalek/internal/app"
)

func configureNodeAgentInternalForE2E(t *testing.T, homeDir, daemonURL, token string) {
	t.Helper()
	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	u, err := url.Parse(strings.TrimSpace(daemonURL))
	if err != nil {
		t.Fatalf("parse daemon url failed: %v", err)
	}
	cfg := h.Config.WithDefaults()
	cfg.Daemon.Internal.Listen = strings.TrimSpace(u.Host)
	cfg.Daemon.Internal.NodeAgentToken = strings.TrimSpace(token)
	if err := app.WriteHomeConfigAtomic(h.ConfigPath, cfg); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}
}

func TestCLI_NodeRunLoop_Once_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var registerCalled atomic.Bool
	var heartbeatCalled atomic.Bool
	var queryCalled atomic.Bool
	var logsCalled atomic.Bool
	var artifactsCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			registerCalled.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 7,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			heartbeatCalled.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 7,
				"status":        "online",
			})
		case "/api/node/run/query":
			queryCalled.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":           true,
				"run_id":          88,
				"task_run_id":     88,
				"status":          "succeeded",
				"summary":         "verify accepted for target=test",
				"snapshot_id":     "snap-88",
				"verify_target":   "test",
				"artifact_count":  1,
				"last_event_type": "run_verify_succeeded",
				"last_event_note": "verify accepted for target=test",
				"protocol_source": "dalek.nodeagent.v1",
			})
		case "/api/node/run/logs":
			logsCalled.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 88,
				"tail":   "2026-03-14T12:00:00Z run_verify_succeeded verify accepted for target=test",
			})
		case "/api/node/run/artifacts":
			artifactsCalled.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 88,
				"artifacts": []map[string]any{
					{"name": "apply-plan.json", "kind": "plan", "ref": "/tmp/run-88/apply-plan.json"},
				},
				"issues": []map[string]any{
					{"name": "report.json", "status": "upload_failed", "reason": "upload failed"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--run-id", "88",
		"-o", "json",
	)
	var resp struct {
		Schema         string `json:"schema"`
		Project        string `json:"project"`
		NodeName       string `json:"node_name"`
		SessionEpoch   int    `json:"session_epoch"`
		HeartbeatCount int    `json:"heartbeat_count"`
		Status         string `json:"status"`
		Run            struct {
			Found         bool   `json:"found"`
			RunID         uint   `json:"run_id"`
			Status        string `json:"status"`
			ArtifactCount int    `json:"artifact_count"`
		} `json:"run"`
		Artifacts struct {
			Artifacts []struct {
				Name string `json:"name"`
			} `json:"artifacts"`
			Issues []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"issues"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode node run-loop response failed: %v raw=%s", err, stdout)
	}
	if resp.Schema != "dalek.node.run-loop.v1" {
		t.Fatalf("unexpected schema: %q", resp.Schema)
	}
	if resp.Project != "demo" || resp.NodeName != "node-c" || resp.SessionEpoch != 7 || resp.HeartbeatCount != 1 || resp.Status != "online" {
		t.Fatalf("unexpected top-level response: %+v", resp)
	}
	if !resp.Run.Found || resp.Run.RunID != 88 || resp.Run.Status != "succeeded" || resp.Run.ArtifactCount != 1 {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
	if len(resp.Artifacts.Artifacts) != 1 || resp.Artifacts.Artifacts[0].Name != "apply-plan.json" {
		t.Fatalf("unexpected artifacts response: %+v", resp.Artifacts)
	}
	if len(resp.Artifacts.Issues) != 1 || resp.Artifacts.Issues[0].Status != "upload_failed" {
		t.Fatalf("unexpected artifact issues response: %+v", resp.Artifacts)
	}
	if !registerCalled.Load() || !heartbeatCalled.Load() || !queryCalled.Load() || !logsCalled.Load() || !artifactsCalled.Load() {
		t.Fatalf("expected all node endpoints called: register=%v heartbeat=%v query=%v logs=%v artifacts=%v",
			registerCalled.Load(), heartbeatCalled.Load(), queryCalled.Load(), logsCalled.Load(), artifactsCalled.Load())
	}
}

func TestCLI_NodeRunLoop_MultipleHeartbeats_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var registerCount atomic.Int32
	var heartbeatCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			registerCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 11,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			heartbeatCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 11,
				"status":        "online",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--heartbeat-count", "3",
		"-o", "json",
	)
	var resp struct {
		SessionEpoch   int    `json:"session_epoch"`
		HeartbeatCount int    `json:"heartbeat_count"`
		Status         string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode node run-loop response failed: %v raw=%s", err, stdout)
	}
	if resp.SessionEpoch != 11 || resp.HeartbeatCount != 3 || resp.Status != "online" {
		t.Fatalf("unexpected top-level response: %+v", resp)
	}
	if registerCount.Load() != 1 || heartbeatCount.Load() != 3 {
		t.Fatalf("unexpected endpoint counts: register=%d heartbeat=%d", registerCount.Load(), heartbeatCount.Load())
	}
}

func TestCLI_NodeRunLoop_SubmitRun_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var submitCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 15,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 15,
				"status":        "online",
			})
		case "/api/node/run/submit":
			submitCalled.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"run_id":      99,
				"task_run_id": 99,
				"request_id":  "req-99",
				"status":      "succeeded",
			})
		case "/api/node/run/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":          true,
				"run_id":         99,
				"task_run_id":    99,
				"status":         "succeeded",
				"verify_target":  "test",
				"artifact_count": 1,
				"summary":        "verify accepted for target=test",
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 99,
				"tail":   "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 99,
				"artifacts": []map[string]any{
					{"name": "apply-plan.json", "kind": "plan"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--verify-target", "test",
		"--snapshot-id", "snap-99",
		"--request-id", "req-99",
		"-o", "json",
	)
	var resp struct {
		SessionEpoch int `json:"session_epoch"`
		Submission   struct {
			Accepted  bool   `json:"accepted"`
			RunID     uint   `json:"run_id"`
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		} `json:"submission"`
		Run struct {
			Found  bool   `json:"found"`
			RunID  uint   `json:"run_id"`
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode submit node run-loop response failed: %v raw=%s", err, stdout)
	}
	if resp.SessionEpoch != 15 || !resp.Submission.Accepted || resp.Submission.RunID != 99 || resp.Submission.RequestID != "req-99" {
		t.Fatalf("unexpected submission response: %+v", resp)
	}
	if !resp.Run.Found || resp.Run.RunID != 99 || resp.Run.Status != "succeeded" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
	if !submitCalled.Load() {
		t.Fatalf("expected submit endpoint to be called")
	}
}

func TestCLI_NodeRunLoop_UploadSnapshotAndSubmitRun_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"files":[{"path":"go.mod","sha256":"deadbeef"}]}`), 0o644); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}

	var uploadCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 21,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 21,
				"status":        "online",
			})
		case "/api/node/snapshot/upload":
			uploadCalled.Store(true)
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode upload request failed: %v", err)
			}
			if strings.TrimSpace(req["snapshot_id"].(string)) != "snap-21" {
				t.Fatalf("unexpected snapshot id: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":             true,
				"snapshot_id":          "snap-21",
				"status":               "ready",
				"manifest_digest":      "sha256:21",
				"artifact_path":        "/tmp/snap-21/manifest.json",
				"base_commit":          "abc123",
				"workspace_generation": "wg-21",
			})
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"run_id":      121,
				"task_run_id": 121,
				"request_id":  "req-121",
				"status":      "succeeded",
			})
		case "/api/node/run/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":          true,
				"run_id":         121,
				"task_run_id":    121,
				"status":         "succeeded",
				"snapshot_id":    "snap-21",
				"verify_target":  "test",
				"artifact_count": 1,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 121,
				"tail":   "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 121,
				"artifacts": []map[string]any{
					{"name": "apply-plan.json", "kind": "plan"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--verify-target", "test",
		"--snapshot-id", "snap-21",
		"--manifest-file", manifestPath,
		"-o", "json",
	)
	var resp struct {
		SessionEpoch   int `json:"session_epoch"`
		SnapshotUpload struct {
			Accepted       bool   `json:"accepted"`
			SnapshotID     string `json:"snapshot_id"`
			ManifestDigest string `json:"manifest_digest"`
		} `json:"snapshot_upload"`
		Submission struct {
			Accepted bool `json:"accepted"`
			RunID    uint `json:"run_id"`
		} `json:"submission"`
		Run struct {
			Found      bool   `json:"found"`
			RunID      uint   `json:"run_id"`
			SnapshotID string `json:"snapshot_id"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode upload+submit response failed: %v raw=%s", err, stdout)
	}
	if resp.SessionEpoch != 21 || !resp.SnapshotUpload.Accepted || resp.SnapshotUpload.SnapshotID != "snap-21" {
		t.Fatalf("unexpected upload response: %+v", resp)
	}
	if !resp.Submission.Accepted || resp.Submission.RunID != 121 {
		t.Fatalf("unexpected submission response: %+v", resp.Submission)
	}
	if !resp.Run.Found || resp.Run.RunID != 121 || resp.Run.SnapshotID != "snap-21" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
	if !uploadCalled.Load() {
		t.Fatalf("expected upload endpoint to be called")
	}
}

func TestCLI_NodeRunLoop_LargeManifestUsesChunkUpload_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	manifestPath := filepath.Join(t.TempDir(), "manifest-large.json")
	largeManifest := `{"base_commit":"abc123","workspace_generation":"wg-big","files":[{"path":"go.mod","digest":"sha256:deadbeef"}],"padding":"` +
		strings.Repeat("x", 220000) + `"}`
	if err := os.WriteFile(manifestPath, []byte(largeManifest), 0o644); err != nil {
		t.Fatalf("write large manifest failed: %v", err)
	}

	var directUploadCalls atomic.Int32
	var chunkUploadCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 31,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 31,
				"status":        "online",
			})
		case "/api/node/snapshot/upload":
			directUploadCalls.Add(1)
			t.Fatalf("large manifest should not use direct upload endpoint")
		case "/api/node/snapshot/upload-chunk":
			chunkUploadCalls.Add(1)
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode chunk upload failed: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":             true,
				"snapshot_id":          "snap-big",
				"status":               map[bool]string{true: "ready", false: "preparing"}[req["is_final"] == true],
				"next_index":           int(req["chunk_index"].(float64)) + 1,
				"manifest_digest":      "sha256:big",
				"artifact_path":        "/tmp/snap-big/manifest.json",
				"base_commit":          "abc123",
				"workspace_generation": "wg-big",
			})
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"run_id":      131,
				"task_run_id": 131,
				"request_id":  "req-131",
				"status":      "succeeded",
			})
		case "/api/node/run/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":          true,
				"run_id":         131,
				"task_run_id":    131,
				"status":         "succeeded",
				"snapshot_id":    "snap-big",
				"verify_target":  "test",
				"artifact_count": 1,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 131,
				"tail":   "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 131,
				"artifacts": []map[string]any{
					{"name": "apply-plan.json", "kind": "plan"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--verify-target", "test",
		"--snapshot-id", "snap-big",
		"--manifest-file", manifestPath,
		"-o", "json",
	)
	var resp struct {
		SnapshotUpload struct {
			Accepted   bool   `json:"accepted"`
			SnapshotID string `json:"snapshot_id"`
		} `json:"snapshot_upload"`
		Run struct {
			Found      bool   `json:"found"`
			RunID      uint   `json:"run_id"`
			SnapshotID string `json:"snapshot_id"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode chunked run-loop response failed: %v raw=%s", err, stdout)
	}
	if directUploadCalls.Load() != 0 {
		t.Fatalf("unexpected direct upload calls: %d", directUploadCalls.Load())
	}
	if chunkUploadCalls.Load() < 2 {
		t.Fatalf("expected multiple chunk uploads, got=%d", chunkUploadCalls.Load())
	}
	if !resp.SnapshotUpload.Accepted || resp.SnapshotUpload.SnapshotID != "snap-big" {
		t.Fatalf("unexpected snapshot upload response: %+v", resp.SnapshotUpload)
	}
	if !resp.Run.Found || resp.Run.RunID != 131 || resp.Run.SnapshotID != "snap-big" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
}

func TestCLI_NodeRunLoop_WorkspaceDirBuildsManifest_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var gotManifest atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 41,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 41,
				"status":        "online",
			})
		case "/api/node/snapshot/upload":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode upload request failed: %v", err)
			}
			manifestJSON := strings.TrimSpace(req["manifest_json"].(string))
			if !strings.Contains(manifestJSON, `"workspace_generation":"snap-workspace"`) {
				t.Fatalf("manifest missing workspace_generation: %s", manifestJSON)
			}
			if !strings.Contains(manifestJSON, `"path":"AGENTS.md"`) && !strings.Contains(manifestJSON, `"path":"README.md"`) {
				t.Fatalf("manifest missing repo files: %s", manifestJSON)
			}
			if strings.Contains(manifestJSON, `.dalek/runtime/`) {
				t.Fatalf("manifest should exclude runtime files: %s", manifestJSON)
			}
			if !strings.Contains(manifestJSON, `"base_commit":"`) {
				t.Fatalf("manifest missing base_commit: %s", manifestJSON)
			}
			gotManifest.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":             true,
				"snapshot_id":          "snap-workspace",
				"status":               "ready",
				"manifest_digest":      "sha256:workspace",
				"artifact_path":        "/tmp/snap-workspace/manifest.json",
				"base_commit":          req["base_commit"],
				"workspace_generation": "snap-workspace",
			})
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"run_id":      141,
				"task_run_id": 141,
				"request_id":  "req-141",
				"status":      "succeeded",
			})
		case "/api/node/run/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":          true,
				"run_id":         141,
				"task_run_id":    141,
				"status":         "succeeded",
				"snapshot_id":    "snap-workspace",
				"verify_target":  "test",
				"artifact_count": 1,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 141,
				"tail":   "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 141,
				"artifacts": []map[string]any{
					{"name": "apply-plan.json", "kind": "plan"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--verify-target", "test",
		"--snapshot-id", "snap-workspace",
		"--workspace-dir", repo,
		"-o", "json",
	)
	var resp struct {
		SnapshotUpload struct {
			Accepted   bool   `json:"accepted"`
			SnapshotID string `json:"snapshot_id"`
		} `json:"snapshot_upload"`
		Run struct {
			Found      bool   `json:"found"`
			RunID      uint   `json:"run_id"`
			SnapshotID string `json:"snapshot_id"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode workspace run-loop response failed: %v raw=%s", err, stdout)
	}
	if !gotManifest.Load() {
		t.Fatalf("expected generated manifest upload")
	}
	if !resp.SnapshotUpload.Accepted || resp.SnapshotUpload.SnapshotID != "snap-workspace" {
		t.Fatalf("unexpected snapshot upload response: %+v", resp.SnapshotUpload)
	}
	if !resp.Run.Found || resp.Run.RunID != 141 || resp.Run.SnapshotID != "snap-workspace" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
}

func TestCLI_NodeRunLoop_SubmitRunWait_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var queryCount atomic.Int32
	var heartbeatCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 31,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			heartbeatCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 31,
				"status":        "online",
			})
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"run_id":      131,
				"task_run_id": 131,
				"request_id":  "req-131",
				"status":      "running",
			})
		case "/api/node/run/query":
			call := queryCount.Add(1)
			status := "running"
			if call >= 2 {
				status = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":          true,
				"run_id":         131,
				"task_run_id":    131,
				"status":         status,
				"verify_target":  "test",
				"artifact_count": 1,
				"summary":        "verify accepted for target=test",
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 131,
				"tail":   "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 131,
				"artifacts": []map[string]any{
					{"name": "apply-plan.json", "kind": "plan"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--verify-target", "test",
		"--wait",
		"--poll-interval", "1ms",
		"-o", "json",
	)
	var resp struct {
		SessionEpoch       int `json:"session_epoch"`
		PollCount          int `json:"poll_count"`
		WaitHeartbeatCount int `json:"wait_heartbeat_count"`
		Submission         struct {
			Accepted bool   `json:"accepted"`
			Status   string `json:"status"`
			RunID    uint   `json:"run_id"`
		} `json:"submission"`
		Run struct {
			Found  bool   `json:"found"`
			Status string `json:"status"`
			RunID  uint   `json:"run_id"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode wait response failed: %v raw=%s", err, stdout)
	}
	if resp.SessionEpoch != 31 || resp.PollCount < 2 || !resp.Submission.Accepted || resp.Submission.RunID != 131 {
		t.Fatalf("unexpected wait response: %+v", resp)
	}
	if !resp.Run.Found || resp.Run.RunID != 131 || resp.Run.Status != "succeeded" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
	if queryCount.Load() < 2 {
		t.Fatalf("expected at least 2 query polls, got=%d", queryCount.Load())
	}
	if resp.WaitHeartbeatCount != 0 || heartbeatCount.Load() != 1 {
		t.Fatalf("unexpected wait heartbeat count: resp=%d server=%d", resp.WaitHeartbeatCount, heartbeatCount.Load())
	}
}

func TestCLI_NodeRunLoop_SubmitRunWaitWithHeartbeat_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var queryCount atomic.Int32
	var heartbeatCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 32,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			heartbeatCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 32,
				"status":        "online",
			})
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"run_id":      132,
				"task_run_id": 132,
				"request_id":  "req-132",
				"status":      "running",
			})
		case "/api/node/run/query":
			call := queryCount.Add(1)
			status := "running"
			if call >= 2 {
				status = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":       true,
				"run_id":      132,
				"task_run_id": 132,
				"status":      status,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 132,
				"tail":   "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 132,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--verify-target", "test",
		"--wait",
		"--poll-interval", "1ms",
		"--heartbeat-interval", "1ms",
		"-o", "json",
	)
	var resp struct {
		PollCount          int `json:"poll_count"`
		WaitHeartbeatCount int `json:"wait_heartbeat_count"`
		Run                struct {
			Found  bool   `json:"found"`
			RunID  uint   `json:"run_id"`
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode wait heartbeat response failed: %v raw=%s", err, stdout)
	}
	if resp.PollCount < 2 || resp.WaitHeartbeatCount < 1 {
		t.Fatalf("unexpected wait heartbeat response: %+v", resp)
	}
	if !resp.Run.Found || resp.Run.RunID != 132 || resp.Run.Status != "succeeded" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
	if queryCount.Load() < 2 || heartbeatCount.Load() < 2 {
		t.Fatalf("expected wait heartbeat traffic: query=%d heartbeat=%d", queryCount.Load(), heartbeatCount.Load())
	}
}

func TestCLI_NodeRunLoop_WaitHeartbeatAutoReregister_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var registerCount atomic.Int32
	var heartbeatCount atomic.Int32
	var queryCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			call := registerCount.Add(1)
			epoch := 50
			if call >= 2 {
				epoch = 51
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": epoch,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			call := heartbeatCount.Add(1)
			if call == 1 {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": "session_conflict",
					"cause": "node session epoch 不匹配: name=node-c want=50 got=51",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 51,
				"status":        "online",
			})
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"run_id":      151,
				"task_run_id": 151,
				"request_id":  "req-151",
				"status":      "running",
			})
		case "/api/node/run/query":
			call := queryCount.Add(1)
			status := "running"
			if call >= 2 {
				status = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":       true,
				"run_id":      151,
				"task_run_id": 151,
				"status":      status,
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 151,
				"tail":   "verify ok",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 151,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--verify-target", "test",
		"--wait",
		"--poll-interval", "1ms",
		"--heartbeat-interval", "1ms",
		"-o", "json",
	)
	var resp struct {
		SessionEpoch       int `json:"session_epoch"`
		PollCount          int `json:"poll_count"`
		WaitHeartbeatCount int `json:"wait_heartbeat_count"`
		Run                struct {
			Found  bool   `json:"found"`
			RunID  uint   `json:"run_id"`
			Status string `json:"status"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode re-register wait response failed: %v raw=%s", err, stdout)
	}
	if resp.SessionEpoch != 51 || resp.PollCount < 2 || resp.WaitHeartbeatCount < 1 {
		t.Fatalf("unexpected re-register response: %+v", resp)
	}
	if !resp.Run.Found || resp.Run.RunID != 151 || resp.Run.Status != "succeeded" {
		t.Fatalf("unexpected run response: %+v", resp.Run)
	}
	if registerCount.Load() < 2 || heartbeatCount.Load() < 2 || queryCount.Load() < 2 {
		t.Fatalf("expected recovery traffic: register=%d heartbeat=%d query=%d", registerCount.Load(), heartbeatCount.Load(), queryCount.Load())
	}
}

func TestCLI_NodeRunLoop_RecoveryStageJSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var queryCall atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 60,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 60,
				"status":        "online",
			})
		case "/api/node/run/control":
			http.NotFound(w, r)
		case "/api/node/run/submit":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"run_id":      161,
				"task_run_id": 161,
				"request_id":  "req-161",
				"status":      "running",
			})
		case "/api/node/run/query":
			call := queryCall.Add(1)
			stage := "recovery"
			if call >= 2 {
				stage = "ready"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":           true,
				"run_id":          161,
				"task_run_id":     161,
				"status":          "node_offline",
				"lifecycle_stage": stage,
				"summary":         "node offline",
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 161,
				"tail":   "recovery in progress",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 161,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--verify-target", "test",
		"--watch-stage", "recovery",
		"--stage-interval", "1ms",
		"--heartbeat-interval", "1ms",
		"-o", "json",
	)
	var resp struct {
		Run struct {
			LifecycleStage string `json:"lifecycle_stage"`
		} `json:"run"`
		RecoveryStage   bool     `json:"recovery_stage"`
		StageWatchCount int      `json:"stage_watch_count"`
		Warnings        []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode recovery response failed: %v raw=%s", err, stdout)
	}
	if resp.Run.LifecycleStage != "ready" || !resp.RecoveryStage || resp.StageWatchCount == 0 {
		t.Fatalf("unexpected recovery response: %+v", resp)
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("expected warnings in recovery response: %+v", resp)
	}
}

func TestCLI_NodeRunLoop_TextHintsRecoveryAndArtifactFailure(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/register":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 61,
				"status":        "online",
			})
		case "/api/node/heartbeat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"name":          "node-c",
				"session_epoch": 61,
				"status":        "online",
			})
		case "/api/node/run/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":           true,
				"run_id":          171,
				"task_run_id":     171,
				"status":          "succeeded",
				"lifecycle_stage": "recovery",
				"summary":         "recovered after reconnect",
				"last_event_type": "run_artifact_upload_failed",
				"last_event_note": "upload failed",
			})
		case "/api/node/run/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 171,
				"tail":   "recovery in progress",
			})
		case "/api/node/run/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 171,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "run-loop",
		"--name", "node-c",
		"--run-id", "171",
	)
	if !strings.Contains(stdout, "is in recovery") {
		t.Fatalf("expected recovery hint, got=%s", stdout)
	}
	if !strings.Contains(stdout, "artifact upload partially failed") {
		t.Fatalf("expected artifact failure hint, got=%s", stdout)
	}
}
