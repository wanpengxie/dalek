package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrDaemonUnavailable = errors.New("daemon unavailable")

type DaemonAPIClientConfig struct {
	BaseURL string
}

type DaemonAPIClient struct {
	baseURL string
	client  *http.Client
}

type DaemonDispatchSubmitRequest struct {
	Project   string
	TicketID  uint
	RequestID string
	Prompt    string
	AutoStart *bool
}

type DaemonDispatchSubmitReceipt struct {
	Accepted  bool
	Project   string
	RequestID string
	TaskRunID uint
	TicketID  uint
	WorkerID  uint
}

type DaemonWorkerRunSubmitRequest struct {
	Project   string
	TicketID  uint
	RequestID string
	Prompt    string
}

type DaemonWorkerRunSubmitReceipt struct {
	Accepted  bool
	Project   string
	RequestID string
	TaskRunID uint
	TicketID  uint
	WorkerID  uint
}

type DaemonSubagentSubmitRequest struct {
	Project string

	RequestID string
	Provider  string
	Model     string
	Prompt    string
}

type DaemonSubagentSubmitReceipt struct {
	Accepted bool

	Project    string
	TaskRunID  uint
	RequestID  string
	Provider   string
	Model      string
	RuntimeDir string
}

type DaemonNoteSubmitRequest struct {
	Project string
	Text    string
}

type DaemonNoteSubmitReceipt struct {
	Accepted     bool
	Project      string
	NoteID       uint
	ShapedItemID uint
	Deduped      bool
}

type DaemonGatewaySendRequest struct {
	Project string
	Text    string
}

type DaemonGatewaySendDelivery struct {
	BindingID      uint   `json:"binding_id"`
	ConversationID uint   `json:"conversation_id"`
	MessageID      uint   `json:"message_id"`
	OutboxID       uint   `json:"outbox_id"`
	ChatID         string `json:"chat_id"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

type DaemonGatewaySendResponse struct {
	Schema    string                      `json:"schema"`
	Project   string                      `json:"project"`
	Text      string                      `json:"text"`
	Delivered int                         `json:"delivered"`
	Failed    int                         `json:"failed"`
	Results   []DaemonGatewaySendDelivery `json:"results,omitempty"`
}

type DaemonRunCancelResult struct {
	RunID     uint
	Found     bool
	Canceled  bool
	Project   string
	RequestID string
	Reason    string
}

type DaemonRunSubmitRequest struct {
	Project             string
	RequestID           string
	TicketID            uint
	VerifyTarget        string
	SnapshotID          string
	BaseCommit          string
	WorkspaceGeneration string
}

type DaemonRunSubmitReceipt struct {
	Accepted            bool              `json:"accepted"`
	Project             string            `json:"project"`
	RunID               uint              `json:"run_id"`
	TaskRunID           uint              `json:"task_run_id"`
	RequestID           string            `json:"request_id"`
	RunStatus           string            `json:"run_status"`
	VerifyTarget        string            `json:"verify_target"`
	SnapshotID          string            `json:"snapshot_id"`
	BaseCommit          string            `json:"base_commit"`
	WorkspaceGeneration string            `json:"source_workspace_generation"`
	Query               map[string]string `json:"query,omitempty"`
}

type DaemonRunStatus struct {
	RunID              uint       `json:"run_id"`
	Project            string     `json:"project"`
	OwnerType          string     `json:"owner_type"`
	TaskType           string     `json:"task_type"`
	TicketID           uint       `json:"ticket_id"`
	WorkerID           uint       `json:"worker_id"`
	OrchestrationState string     `json:"orchestration_state"`
	RuntimeHealthState string     `json:"runtime_health_state"`
	RuntimeNeedsUser   bool       `json:"runtime_needs_user"`
	RuntimeSummary     string     `json:"runtime_summary"`
	SemanticNextAction string     `json:"semantic_next_action"`
	SemanticSummary    string     `json:"semantic_summary"`
	ErrorCode          string     `json:"error_code"`
	ErrorMessage       string     `json:"error_message"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type DaemonRunEvent struct {
	ID            uint      `json:"id"`
	TaskRunID     uint      `json:"task_run_id"`
	EventType     string    `json:"event_type"`
	FromStateJSON string    `json:"from_state_json"`
	ToStateJSON   string    `json:"to_state_json"`
	Note          string    `json:"note"`
	PayloadJSON   string    `json:"payload_json"`
	CreatedAt     time.Time `json:"created_at"`
}

type DaemonRunLogs struct {
	Found bool   `json:"found"`
	RunID uint   `json:"run_id"`
	Tail  string `json:"tail"`
}

type DaemonRunArtifact struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
	Ref  string `json:"ref"`
}

type DaemonRunArtifactIssue struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type DaemonRunArtifacts struct {
	Found     bool                     `json:"found"`
	RunID     uint                     `json:"run_id"`
	Artifacts []DaemonRunArtifact      `json:"artifacts"`
	Issues    []DaemonRunArtifactIssue `json:"issues"`
}

type daemonAPIError struct {
	Error string `json:"error"`
	Cause string `json:"cause"`
}

func NewDaemonAPIClient(cfg DaemonAPIClientConfig) (*DaemonAPIClient, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return nil, fmt.Errorf("daemon base url 不能为空")
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("daemon base url 非法: %w", err)
	}
	if strings.TrimSpace(u.Scheme) == "" {
		u.Scheme = "http"
	}
	if strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("daemon base url 缺少 host")
	}
	u.Path = strings.TrimRight(strings.TrimSpace(u.Path), "/")
	return &DaemonAPIClient{
		baseURL: u.String(),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

func NewDaemonAPIClientFromHome(h *Home) (*DaemonAPIClient, error) {
	if h == nil {
		return nil, fmt.Errorf("home 为空")
	}
	cfg := h.Config.WithDefaults()
	baseURL := "http://" + strings.TrimSpace(cfg.Daemon.Internal.Listen)
	return NewDaemonAPIClient(DaemonAPIClientConfig{
		BaseURL: baseURL,
	})
}

func IsDaemonUnavailable(err error) bool {
	return errors.Is(err, ErrDaemonUnavailable)
}

func (c *DaemonAPIClient) Health(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("daemon client 为空")
	}
	var out map[string]any
	if _, err := c.doJSON(ctx, http.MethodGet, "/health", nil, &out); err != nil {
		return err
	}
	return nil
}

func (c *DaemonAPIClient) SubmitDispatch(ctx context.Context, req DaemonDispatchSubmitRequest) (DaemonDispatchSubmitReceipt, error) {
	if c == nil {
		return DaemonDispatchSubmitReceipt{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project":    strings.TrimSpace(req.Project),
		"ticket_id":  req.TicketID,
		"request_id": strings.TrimSpace(req.RequestID),
		"prompt":     strings.TrimSpace(req.Prompt),
	}
	if req.AutoStart != nil {
		payload["auto_start"] = *req.AutoStart
	}
	var out struct {
		Accepted  bool   `json:"accepted"`
		Project   string `json:"project"`
		RequestID string `json:"request_id"`
		TaskRunID uint   `json:"task_run_id"`
		TicketID  uint   `json:"ticket_id"`
		WorkerID  uint   `json:"worker_id"`
	}
	code, err := c.doJSON(ctx, http.MethodPost, "/api/dispatch/submit", payload, &out)
	if err != nil {
		return DaemonDispatchSubmitReceipt{}, err
	}
	if code != http.StatusAccepted {
		return DaemonDispatchSubmitReceipt{}, fmt.Errorf("dispatch submit 响应码异常: %d", code)
	}
	return DaemonDispatchSubmitReceipt{
		Accepted:  out.Accepted,
		Project:   strings.TrimSpace(out.Project),
		RequestID: strings.TrimSpace(out.RequestID),
		TaskRunID: out.TaskRunID,
		TicketID:  out.TicketID,
		WorkerID:  out.WorkerID,
	}, nil
}

func (c *DaemonAPIClient) SubmitWorkerRun(ctx context.Context, req DaemonWorkerRunSubmitRequest) (DaemonWorkerRunSubmitReceipt, error) {
	if c == nil {
		return DaemonWorkerRunSubmitReceipt{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project":    strings.TrimSpace(req.Project),
		"ticket_id":  req.TicketID,
		"request_id": strings.TrimSpace(req.RequestID),
		"prompt":     strings.TrimSpace(req.Prompt),
	}
	var out struct {
		Accepted  bool   `json:"accepted"`
		Project   string `json:"project"`
		RequestID string `json:"request_id"`
		TaskRunID uint   `json:"task_run_id"`
		TicketID  uint   `json:"ticket_id"`
		WorkerID  uint   `json:"worker_id"`
	}
	code, err := c.doJSON(ctx, http.MethodPost, "/api/worker-run/submit", payload, &out)
	if err != nil {
		return DaemonWorkerRunSubmitReceipt{}, err
	}
	if code != http.StatusAccepted {
		return DaemonWorkerRunSubmitReceipt{}, fmt.Errorf("worker-run submit 响应码异常: %d", code)
	}
	return DaemonWorkerRunSubmitReceipt{
		Accepted:  out.Accepted,
		Project:   strings.TrimSpace(out.Project),
		RequestID: strings.TrimSpace(out.RequestID),
		TaskRunID: out.TaskRunID,
		TicketID:  out.TicketID,
		WorkerID:  out.WorkerID,
	}, nil
}

func (c *DaemonAPIClient) SubmitSubagentRun(ctx context.Context, req DaemonSubagentSubmitRequest) (DaemonSubagentSubmitReceipt, error) {
	if c == nil {
		return DaemonSubagentSubmitReceipt{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project":    strings.TrimSpace(req.Project),
		"request_id": strings.TrimSpace(req.RequestID),
		"provider":   strings.TrimSpace(req.Provider),
		"model":      strings.TrimSpace(req.Model),
		"prompt":     strings.TrimSpace(req.Prompt),
	}
	var out struct {
		Accepted bool `json:"accepted"`

		Project    string `json:"project"`
		TaskRunID  uint   `json:"task_run_id"`
		RequestID  string `json:"request_id"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		RuntimeDir string `json:"runtime_dir"`
	}
	code, err := c.doJSON(ctx, http.MethodPost, "/api/subagent/submit", payload, &out)
	if err != nil {
		return DaemonSubagentSubmitReceipt{}, err
	}
	if code != http.StatusAccepted {
		return DaemonSubagentSubmitReceipt{}, fmt.Errorf("subagent submit 响应码异常: %d", code)
	}
	return DaemonSubagentSubmitReceipt{
		Accepted:   out.Accepted,
		Project:    strings.TrimSpace(out.Project),
		TaskRunID:  out.TaskRunID,
		RequestID:  strings.TrimSpace(out.RequestID),
		Provider:   strings.TrimSpace(out.Provider),
		Model:      strings.TrimSpace(out.Model),
		RuntimeDir: strings.TrimSpace(out.RuntimeDir),
	}, nil
}

func (c *DaemonAPIClient) SubmitNote(ctx context.Context, req DaemonNoteSubmitRequest) (DaemonNoteSubmitReceipt, error) {
	if c == nil {
		return DaemonNoteSubmitReceipt{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project": strings.TrimSpace(req.Project),
		"text":    strings.TrimSpace(req.Text),
	}
	var out struct {
		Accepted     bool   `json:"accepted"`
		Project      string `json:"project"`
		NoteID       uint   `json:"note_id"`
		ShapedItemID uint   `json:"shaped_item_id"`
		Deduped      bool   `json:"deduped"`
	}
	code, err := c.doJSON(ctx, http.MethodPost, "/api/notes", payload, &out)
	if err != nil {
		return DaemonNoteSubmitReceipt{}, err
	}
	if code != http.StatusAccepted {
		return DaemonNoteSubmitReceipt{}, fmt.Errorf("note submit 响应码异常: %d", code)
	}
	return DaemonNoteSubmitReceipt{
		Accepted:     out.Accepted,
		Project:      strings.TrimSpace(out.Project),
		NoteID:       out.NoteID,
		ShapedItemID: out.ShapedItemID,
		Deduped:      out.Deduped,
	}, nil
}

func (c *DaemonAPIClient) SendProjectText(ctx context.Context, req DaemonGatewaySendRequest) (DaemonGatewaySendResponse, error) {
	if c == nil {
		return DaemonGatewaySendResponse{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project": strings.TrimSpace(req.Project),
		"text":    strings.TrimSpace(req.Text),
	}
	var out DaemonGatewaySendResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/send", payload, &out)
	if err != nil {
		return DaemonGatewaySendResponse{}, err
	}
	if code != http.StatusOK {
		return DaemonGatewaySendResponse{}, fmt.Errorf("gateway send 响应码异常: %d", code)
	}
	out.Project = strings.TrimSpace(out.Project)
	out.Text = strings.TrimSpace(out.Text)
	return out, nil
}

func (c *DaemonAPIClient) CancelRun(ctx context.Context, runID uint) (DaemonRunCancelResult, error) {
	if c == nil {
		return DaemonRunCancelResult{}, fmt.Errorf("daemon client 为空")
	}
	if runID == 0 {
		return DaemonRunCancelResult{}, fmt.Errorf("run_id 不能为空")
	}
	var out struct {
		RunID     uint   `json:"run_id"`
		Found     bool   `json:"found"`
		Canceled  bool   `json:"canceled"`
		Project   string `json:"project"`
		RequestID string `json:"request_id"`
		Reason    string `json:"reason"`
	}
	code, err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/runs/%d/cancel", runID), nil, &out)
	if err != nil {
		return DaemonRunCancelResult{}, err
	}
	if code != http.StatusOK {
		return DaemonRunCancelResult{}, fmt.Errorf("run cancel 响应码异常: %d", code)
	}
	return DaemonRunCancelResult{
		RunID:     out.RunID,
		Found:     out.Found,
		Canceled:  out.Canceled,
		Project:   strings.TrimSpace(out.Project),
		RequestID: strings.TrimSpace(out.RequestID),
		Reason:    strings.TrimSpace(out.Reason),
	}, nil
}

func (c *DaemonAPIClient) SubmitRun(ctx context.Context, req DaemonRunSubmitRequest) (DaemonRunSubmitReceipt, error) {
	if c == nil {
		return DaemonRunSubmitReceipt{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project":              strings.TrimSpace(req.Project),
		"request_id":           strings.TrimSpace(req.RequestID),
		"ticket_id":            req.TicketID,
		"verify_target":        strings.TrimSpace(req.VerifyTarget),
		"snapshot_id":          strings.TrimSpace(req.SnapshotID),
		"base_commit":          strings.TrimSpace(req.BaseCommit),
		"workspace_generation": strings.TrimSpace(req.WorkspaceGeneration),
	}
	var out DaemonRunSubmitReceipt
	code, err := c.doJSON(ctx, http.MethodPost, "/api/runs", payload, &out)
	if err != nil {
		return DaemonRunSubmitReceipt{}, err
	}
	if code != http.StatusAccepted {
		return DaemonRunSubmitReceipt{}, fmt.Errorf("run submit 响应码异常: %d", code)
	}
	return out, nil
}

func (c *DaemonAPIClient) GetRun(ctx context.Context, runID uint) (*DaemonRunStatus, error) {
	if c == nil {
		return nil, fmt.Errorf("daemon client 为空")
	}
	if runID == 0 {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	var out struct {
		Run DaemonRunStatus `json:"run"`
	}
	code, err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/runs/%d", runID), nil, &out)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("run show 响应码异常: %d", code)
	}
	return &out.Run, nil
}

func (c *DaemonAPIClient) ListRunEvents(ctx context.Context, runID uint, limit int) ([]DaemonRunEvent, error) {
	if c == nil {
		return nil, fmt.Errorf("daemon client 为空")
	}
	if runID == 0 {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	path := fmt.Sprintf("/api/runs/%d/events", runID)
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var out struct {
		Events []DaemonRunEvent `json:"events"`
	}
	code, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("run events 响应码异常: %d", code)
	}
	if out.Events == nil {
		return []DaemonRunEvent{}, nil
	}
	return out.Events, nil
}

func (c *DaemonAPIClient) GetRunLogs(ctx context.Context, runID uint, lines int) (DaemonRunLogs, error) {
	if c == nil {
		return DaemonRunLogs{}, fmt.Errorf("daemon client 为空")
	}
	if runID == 0 {
		return DaemonRunLogs{}, fmt.Errorf("run_id 不能为空")
	}
	path := fmt.Sprintf("/api/runs/%d/logs", runID)
	if lines > 0 {
		path += fmt.Sprintf("?lines=%d", lines)
	}
	var out DaemonRunLogs
	code, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	if err != nil {
		return DaemonRunLogs{}, err
	}
	if code != http.StatusOK {
		return DaemonRunLogs{}, fmt.Errorf("run logs 响应码异常: %d", code)
	}
	return out, nil
}

func (c *DaemonAPIClient) GetRunArtifacts(ctx context.Context, runID uint) (DaemonRunArtifacts, error) {
	if c == nil {
		return DaemonRunArtifacts{}, fmt.Errorf("daemon client 为空")
	}
	if runID == 0 {
		return DaemonRunArtifacts{}, fmt.Errorf("run_id 不能为空")
	}
	var out DaemonRunArtifacts
	code, err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/runs/%d/artifacts", runID), nil, &out)
	if err != nil {
		return DaemonRunArtifacts{}, err
	}
	if code != http.StatusOK {
		return DaemonRunArtifacts{}, fmt.Errorf("run artifacts 响应码异常: %d", code)
	}
	return out, nil
}

func (c *DaemonAPIClient) doJSON(ctx context.Context, method, path string, payload any, out any) (int, error) {
	if c == nil {
		return 0, fmt.Errorf("daemon client 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	u := strings.TrimRight(strings.TrimSpace(c.baseURL), "/") + path

	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		var apiErr daemonAPIError
		if len(raw) > 0 && json.Unmarshal(raw, &apiErr) == nil {
			cause := strings.TrimSpace(apiErr.Cause)
			if cause == "" {
				cause = strings.TrimSpace(apiErr.Error)
			}
			if cause == "" {
				cause = strings.TrimSpace(string(raw))
			}
			if cause == "" {
				cause = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
			return resp.StatusCode, fmt.Errorf("daemon api 错误（%d）: %s", resp.StatusCode, cause)
		}
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return resp.StatusCode, fmt.Errorf("daemon api 错误（%d）: %s", resp.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
