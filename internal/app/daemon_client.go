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
	Project    string
	TicketID   uint
	RequestID  string
	Prompt     string
	AutoStart  *bool
	BaseBranch string
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
	if strings.TrimSpace(req.BaseBranch) != "" {
		payload["base_branch"] = strings.TrimSpace(req.BaseBranch)
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
