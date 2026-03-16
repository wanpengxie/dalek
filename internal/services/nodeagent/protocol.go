package nodeagent

import "time"

const ProtocolVersionV1 = "dalek.nodeagent.v1"

type RequestMeta struct {
	RequestID       string `json:"request_id"`
	MessageID       string `json:"message_id,omitempty"`
	ProjectKey      string `json:"project_key,omitempty"`
	SessionEpoch    int    `json:"session_epoch,omitempty"`
	TaskRunID       uint   `json:"task_run_id,omitempty"`
	RunID           uint   `json:"run_id,omitempty"`
	Attempt         int    `json:"attempt,omitempty"`
	SentAt          string `json:"sent_at,omitempty"`
	DeadlineAt      string `json:"deadline_at,omitempty"`
	ProtocolVersion string `json:"protocol_version"`
}

type RegisterRequest struct {
	Meta RequestMeta `json:"meta"`

	Name                 string         `json:"name"`
	Endpoint             string         `json:"endpoint,omitempty"`
	AuthMode             string         `json:"auth_mode,omitempty"`
	Version              string         `json:"version,omitempty"`
	ProtocolVersion      string         `json:"protocol_version"`
	RoleCapabilities     []string       `json:"role_capabilities,omitempty"`
	ProviderModes        []string       `json:"provider_modes,omitempty"`
	DefaultProvider      string         `json:"default_provider,omitempty"`
	ProviderCapabilities map[string]any `json:"provider_capabilities,omitempty"`
	SessionAffinity      string         `json:"session_affinity,omitempty"`
	LastSeenAt           string         `json:"last_seen_at,omitempty"`
}

type RegisterResponse struct {
	Accepted     bool   `json:"accepted"`
	Name         string `json:"name"`
	SessionEpoch int    `json:"session_epoch,omitempty"`
	Status       string `json:"status,omitempty"`
}

type HeartbeatRequest struct {
	Meta         RequestMeta `json:"meta"`
	Name         string      `json:"name"`
	SessionEpoch int         `json:"session_epoch,omitempty"`
	ObservedAt   string      `json:"observed_at,omitempty"`
	Status       string      `json:"status,omitempty"`
}

type HeartbeatResponse struct {
	Accepted     bool   `json:"accepted"`
	Name         string `json:"name"`
	SessionEpoch int    `json:"session_epoch,omitempty"`
	Status       string `json:"status,omitempty"`
}

type RunSubmitRequest struct {
	Meta RequestMeta `json:"meta"`

	NodeName     string `json:"node_name,omitempty"`
	VerifyTarget string `json:"verify_target"`
	SnapshotID   string `json:"snapshot_id,omitempty"`
	BaseCommit   string `json:"base_commit,omitempty"`
}

type RunSubmitResponse struct {
	Accepted  bool   `json:"accepted"`
	RunID     uint   `json:"run_id,omitempty"`
	TaskRunID uint   `json:"task_run_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

type RunQueryRequest struct {
	Meta RequestMeta `json:"meta"`
}

type RunQueryResponse struct {
	Found          bool      `json:"found"`
	RunID          uint      `json:"run_id,omitempty"`
	TaskRunID      uint      `json:"task_run_id,omitempty"`
	Status         string    `json:"status,omitempty"`
	LifecycleStage string    `json:"lifecycle_stage,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
	SnapshotID     string    `json:"snapshot_id,omitempty"`
	VerifyTarget   string    `json:"verify_target,omitempty"`
	ArtifactCount  int       `json:"artifact_count,omitempty"`
	LastEventType  string    `json:"last_event_type,omitempty"`
	LastEventNote  string    `json:"last_event_note,omitempty"`
	ProtocolSource string    `json:"protocol_source,omitempty"`
}

type RunCancelRequest struct {
	Meta   RequestMeta `json:"meta"`
	Reason string      `json:"reason,omitempty"`
}

type RunCancelResponse struct {
	Accepted bool   `json:"accepted"`
	Found    bool   `json:"found"`
	Canceled bool   `json:"canceled"`
	Reason   string `json:"reason,omitempty"`
}

type RunLogsRequest struct {
	Meta  RequestMeta `json:"meta"`
	Lines int         `json:"lines,omitempty"`
}

type RunLogsResponse struct {
	Found bool   `json:"found"`
	RunID uint   `json:"run_id,omitempty"`
	Tail  string `json:"tail,omitempty"`
}

type ArtifactSummary struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
	Size int64  `json:"size,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

type ArtifactIssue struct {
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type RunArtifactsResponse struct {
	Found     bool              `json:"found"`
	RunID     uint              `json:"run_id,omitempty"`
	Artifacts []ArtifactSummary `json:"artifacts,omitempty"`
	Issues    []ArtifactIssue   `json:"issues,omitempty"`
}

type SnapshotUploadRequest struct {
	Meta RequestMeta `json:"meta"`

	SnapshotID          string `json:"snapshot_id"`
	NodeName            string `json:"node_name,omitempty"`
	BaseCommit          string `json:"base_commit,omitempty"`
	WorkspaceGeneration string `json:"workspace_generation,omitempty"`
	ManifestJSON        string `json:"manifest_json"`
	ExpiresAt           string `json:"expires_at,omitempty"`
}

type SnapshotUploadResponse struct {
	Accepted            bool   `json:"accepted"`
	SnapshotID          string `json:"snapshot_id,omitempty"`
	Status              string `json:"status,omitempty"`
	ManifestDigest      string `json:"manifest_digest,omitempty"`
	ArtifactPath        string `json:"artifact_path,omitempty"`
	BaseCommit          string `json:"base_commit,omitempty"`
	WorkspaceGeneration string `json:"workspace_generation,omitempty"`
}

type SnapshotChunkUploadRequest struct {
	Meta RequestMeta `json:"meta"`

	SnapshotID          string `json:"snapshot_id"`
	NodeName            string `json:"node_name,omitempty"`
	BaseCommit          string `json:"base_commit,omitempty"`
	WorkspaceGeneration string `json:"workspace_generation,omitempty"`
	ChunkIndex          int    `json:"chunk_index"`
	ChunkData           string `json:"chunk_data"`
	IsFinal             bool   `json:"is_final,omitempty"`
	ExpiresAt           string `json:"expires_at,omitempty"`
}

type SnapshotChunkUploadResponse struct {
	Accepted            bool   `json:"accepted"`
	SnapshotID          string `json:"snapshot_id,omitempty"`
	Status              string `json:"status,omitempty"`
	NextIndex           int    `json:"next_index,omitempty"`
	ManifestDigest      string `json:"manifest_digest,omitempty"`
	ArtifactPath        string `json:"artifact_path,omitempty"`
	BaseCommit          string `json:"base_commit,omitempty"`
	WorkspaceGeneration string `json:"workspace_generation,omitempty"`
}

type SnapshotDownloadRequest struct {
	Meta       RequestMeta `json:"meta"`
	SnapshotID string      `json:"snapshot_id"`
}

type SnapshotDownloadResponse struct {
	Found               bool   `json:"found"`
	SnapshotID          string `json:"snapshot_id,omitempty"`
	Status              string `json:"status,omitempty"`
	ManifestDigest      string `json:"manifest_digest,omitempty"`
	ManifestJSON        string `json:"manifest_json,omitempty"`
	ArtifactPath        string `json:"artifact_path,omitempty"`
	BaseCommit          string `json:"base_commit,omitempty"`
	WorkspaceGeneration string `json:"workspace_generation,omitempty"`
}
