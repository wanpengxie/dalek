package nodeagent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRegisterRequest_JSONRoundTrip(t *testing.T) {
	src := RegisterRequest{
		Meta: RequestMeta{
			RequestID:       "req-node-register-1",
			MessageID:       "msg-1",
			ProtocolVersion: ProtocolVersionV1,
			SentAt:          "2026-03-14T09:00:00Z",
		},
		Name:                 "run-node-1",
		Endpoint:             "https://node.example.test",
		AuthMode:             "token",
		Version:              "0.1.0",
		ProtocolVersion:      ProtocolVersionV1,
		RoleCapabilities:     []string{"run"},
		ProviderModes:        []string{"run_executor"},
		DefaultProvider:      "run_executor",
		ProviderCapabilities: map[string]any{"run_executor": map[string]any{"verify": true}},
		SessionAffinity:      "node-run-1",
		LastSeenAt:           "2026-03-14T09:00:00Z",
	}

	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	text := string(raw)
	for _, want := range []string{`"request_id":"req-node-register-1"`, `"protocol_version":"dalek.nodeagent.v1"`, `"role_capabilities":["run"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("register payload missing %s in %s", want, text)
		}
	}

	var dst RegisterRequest
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if dst.Name != src.Name || dst.Meta.RequestID != src.Meta.RequestID {
		t.Fatalf("round trip mismatch: got=%+v want=%+v", dst, src)
	}
	if len(dst.RoleCapabilities) != 1 || dst.RoleCapabilities[0] != "run" {
		t.Fatalf("unexpected role capabilities after round trip: %+v", dst.RoleCapabilities)
	}
}

func TestRunQueryResponse_JSONRoundTrip(t *testing.T) {
	src := RunQueryResponse{
		Found:          true,
		RunID:          88,
		TaskRunID:      88,
		Status:         "running",
		LifecycleStage: "execute",
		Summary:        "verify executing",
		UpdatedAt:      time.Date(2026, 3, 14, 17, 30, 0, 0, time.UTC),
		SnapshotID:     "snap-123",
		VerifyTarget:   "test",
		ArtifactCount:  2,
		LastEventType:  "run_dispatched",
		LastEventNote:  "worker accepted",
		ProtocolSource: ProtocolVersionV1,
	}

	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var dst RunQueryResponse
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !dst.Found || dst.RunID != 88 || dst.Status != "running" || dst.LifecycleStage != "execute" {
		t.Fatalf("unexpected round trip response: %+v", dst)
	}
	if dst.UpdatedAt.IsZero() {
		t.Fatalf("expected updated_at preserved")
	}
}

func TestHeartbeatRequest_JSONShape(t *testing.T) {
	src := HeartbeatRequest{
		Meta: RequestMeta{
			RequestID:       "hb-1",
			ProtocolVersion: ProtocolVersionV1,
			ProjectKey:      "demo",
			SessionEpoch:    3,
		},
		Name:         "run-node-1",
		SessionEpoch: 3,
		ObservedAt:   "2026-03-14T09:05:00Z",
		Status:       "online",
	}

	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	text := string(raw)
	for _, want := range []string{`"name":"run-node-1"`, `"session_epoch":3`, `"project_key":"demo"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("heartbeat payload missing %s in %s", want, text)
		}
	}
}

func TestSnapshotChunkUploadRequest_JSONRoundTrip(t *testing.T) {
	src := SnapshotChunkUploadRequest{
		Meta: RequestMeta{
			RequestID:       "snap-chunk-1",
			ProtocolVersion: ProtocolVersionV1,
			ProjectKey:      "demo",
			SessionEpoch:    4,
		},
		SnapshotID:          "snap-1",
		NodeName:            "node-b",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		ChunkIndex:          2,
		ChunkData:           "bWFuaWZlc3Q=",
		IsFinal:             true,
	}

	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	text := string(raw)
	for _, want := range []string{`"snapshot_id":"snap-1"`, `"chunk_index":2`, `"chunk_data":"bWFuaWZlc3Q="`} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot chunk payload missing %s in %s", want, text)
		}
	}

	var dst SnapshotChunkUploadRequest
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if dst.SnapshotID != src.SnapshotID || dst.ChunkIndex != 2 || !dst.IsFinal {
		t.Fatalf("unexpected round trip: %+v", dst)
	}
	if dst.Meta.SessionEpoch != 4 {
		t.Fatalf("expected session epoch preserved, got=%d", dst.Meta.SessionEpoch)
	}
}

func TestRunArtifactsResponse_JSONRoundTrip(t *testing.T) {
	src := RunArtifactsResponse{
		Found: true,
		RunID: 42,
		Artifacts: []ArtifactSummary{
			{Name: "apply-plan.json", Kind: "plan", Ref: "/tmp/run-42/apply-plan.json"},
		},
		Issues: []ArtifactIssue{
			{Name: "report.json", Status: "upload_failed", Reason: "upload failed"},
		},
	}

	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var dst RunArtifactsResponse
	if err := json.Unmarshal(raw, &dst); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !dst.Found || dst.RunID != 42 || len(dst.Artifacts) != 1 || len(dst.Issues) != 1 {
		t.Fatalf("unexpected round trip artifacts response: %+v", dst)
	}
	if dst.Issues[0].Status != "upload_failed" {
		t.Fatalf("expected issue status preserved, got=%+v", dst.Issues[0])
	}
}
