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

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

var ErrDaemonUnavailable = errors.New("daemon unavailable")

type DaemonAPIClientConfig struct {
	BaseURL string
}

type DaemonAPIClient struct {
	baseURL string
	client  *http.Client
}

type DaemonTicketStartRequest struct {
	Project    string
	TicketID   uint
	BaseBranch string
}

type DaemonTicketStartReceipt struct {
	Started        bool
	Project        string
	TicketID       uint
	WorkerID       uint
	WorkflowStatus string
	WorktreePath   string
	Branch         string
	LogPath        string
}

type DaemonTicketLoopSubmitRequest struct {
	Project    string
	TicketID   uint
	RequestID  string
	Prompt     string
	AutoStart  *bool
	BaseBranch string
}

type DaemonTicketLoopSubmitReceipt struct {
	Accepted  bool
	Project   string
	RequestID string
	TaskRunID uint
	TicketID  uint
	WorkerID  uint
}

type DaemonTicketLoopProbeResult struct {
	Found                bool
	OwnedByCurrentDaemon bool
	Phase                string
	Project              string
	TicketID             uint
	RequestID            string
	RunID                uint
	WorkerID             uint
	CancelRequestedAt    *time.Time
	LastError            string
}

type DaemonFocusStartRequest struct {
	Project string
	FocusStartInput
}

type DaemonFocusAddTicketsRequest struct {
	Project   string
	TicketIDs []uint
	RequestID string
}

type DaemonFocusStopRequest struct {
	Project   string
	FocusID   uint
	RequestID string
}

type DaemonFocusCancelRequest struct {
	Project   string
	FocusID   uint
	RequestID string
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

type DaemonTicketLoopCancelResult struct {
	TicketID  uint
	Found     bool
	Canceled  bool
	Project   string
	RequestID string
	Reason    string
}

type DaemonTaskRunCancelResult struct {
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

func (c *DaemonAPIClient) FocusStart(ctx context.Context, req DaemonFocusStartRequest) (FocusStartResult, error) {
	if c == nil {
		return FocusStartResult{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project":          strings.TrimSpace(req.Project),
		"mode":             strings.TrimSpace(req.Mode),
		"scope_ticket_ids": append([]uint(nil), req.ScopeTicketIDs...),
		"agent_budget":     req.AgentBudget,
		"request_id":       strings.TrimSpace(req.RequestID),
	}
	if req.MaxPMRuns > 0 {
		payload["max_pm_runs"] = req.MaxPMRuns
	}
	var out FocusStartResult
	code, err := c.doJSON(ctx, http.MethodPost, "/api/v1/focus/start", payload, &out)
	if err != nil {
		return FocusStartResult{}, err
	}
	if code != http.StatusAccepted && code != http.StatusOK {
		return FocusStartResult{}, fmt.Errorf("focus start 响应码异常: %d", code)
	}
	return out, nil
}

func (c *DaemonAPIClient) FocusAddTickets(ctx context.Context, req DaemonFocusAddTicketsRequest) (FocusAddTicketsResult, error) {
	if c == nil {
		return FocusAddTicketsResult{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project":    strings.TrimSpace(req.Project),
		"ticket_ids": append([]uint(nil), req.TicketIDs...),
		"request_id": strings.TrimSpace(req.RequestID),
	}
	var out FocusAddTicketsResult
	code, err := c.doJSON(ctx, http.MethodPost, "/api/v1/focus/add", payload, &out)
	if err != nil {
		return FocusAddTicketsResult{}, err
	}
	if code != http.StatusOK {
		return FocusAddTicketsResult{}, fmt.Errorf("focus add tickets 响应码异常: %d", code)
	}
	return out, nil
}

func (c *DaemonAPIClient) FocusGet(ctx context.Context, project string, focusID uint, active bool) (FocusRunView, error) {
	if c == nil {
		return FocusRunView{}, fmt.Errorf("daemon client 为空")
	}
	project = strings.TrimSpace(project)
	var path string
	switch {
	case focusID > 0:
		path = fmt.Sprintf("/api/v1/focus/%d?project=%s", focusID, url.QueryEscape(project))
	case active:
		path = fmt.Sprintf("/api/v1/focus?project=%s&active=true", url.QueryEscape(project))
	default:
		return FocusRunView{}, fmt.Errorf("focus_id 不能为空")
	}
	var out struct {
		Focus FocusRunView `json:"focus"`
	}
	code, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	if err != nil {
		if code == http.StatusNotFound {
			return FocusRunView{}, gorm.ErrRecordNotFound
		}
		return FocusRunView{}, err
	}
	if code != http.StatusOK {
		return FocusRunView{}, fmt.Errorf("focus get 响应码异常: %d", code)
	}
	return out.Focus, nil
}

func (c *DaemonAPIClient) FocusGetCurrent(ctx context.Context, project string) (FocusRunView, error) {
	return c.FocusGet(ctx, project, 0, true)
}

func (c *DaemonAPIClient) FocusPoll(ctx context.Context, project string, focusID uint, sinceEventID uint) (FocusPollResult, error) {
	if c == nil {
		return FocusPollResult{}, fmt.Errorf("daemon client 为空")
	}
	if focusID == 0 {
		return FocusPollResult{}, fmt.Errorf("focus_id 不能为空")
	}
	path := fmt.Sprintf("/api/v1/focus/%d/poll?project=%s&since_event_id=%d", focusID, url.QueryEscape(strings.TrimSpace(project)), sinceEventID)
	var out FocusPollResult
	code, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	if err != nil {
		if code == http.StatusNotFound {
			return FocusPollResult{}, gorm.ErrRecordNotFound
		}
		return FocusPollResult{}, err
	}
	if code != http.StatusOK {
		return FocusPollResult{}, fmt.Errorf("focus poll 响应码异常: %d", code)
	}
	return out, nil
}

func (c *DaemonAPIClient) FocusStop(ctx context.Context, req DaemonFocusStopRequest) error {
	if c == nil {
		return fmt.Errorf("daemon client 为空")
	}
	if req.FocusID == 0 {
		return fmt.Errorf("focus_id 不能为空")
	}
	path := fmt.Sprintf("/api/v1/focus/%d/stop?project=%s", req.FocusID, url.QueryEscape(strings.TrimSpace(req.Project)))
	payload := map[string]any{"request_id": strings.TrimSpace(req.RequestID)}
	code, err := c.doJSON(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		if code == http.StatusNotFound {
			return gorm.ErrRecordNotFound
		}
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("focus stop 响应码异常: %d", code)
	}
	return nil
}

func (c *DaemonAPIClient) FocusCancel(ctx context.Context, req DaemonFocusCancelRequest) error {
	if c == nil {
		return fmt.Errorf("daemon client 为空")
	}
	if req.FocusID == 0 {
		return fmt.Errorf("focus_id 不能为空")
	}
	path := fmt.Sprintf("/api/v1/focus/%d/cancel?project=%s", req.FocusID, url.QueryEscape(strings.TrimSpace(req.Project)))
	payload := map[string]any{"request_id": strings.TrimSpace(req.RequestID)}
	code, err := c.doJSON(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		if code == http.StatusNotFound {
			return gorm.ErrRecordNotFound
		}
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("focus cancel 响应码异常: %d", code)
	}
	return nil
}

func (c *DaemonAPIClient) StartTicket(ctx context.Context, req DaemonTicketStartRequest) (DaemonTicketStartReceipt, error) {
	if c == nil {
		return DaemonTicketStartReceipt{}, fmt.Errorf("daemon client 为空")
	}
	payload := map[string]any{
		"project":   strings.TrimSpace(req.Project),
		"ticket_id": req.TicketID,
	}
	if strings.TrimSpace(req.BaseBranch) != "" {
		payload["base_branch"] = strings.TrimSpace(req.BaseBranch)
	}
	var out struct {
		Started        bool   `json:"started"`
		Project        string `json:"project"`
		TicketID       uint   `json:"ticket_id"`
		WorkerID       uint   `json:"worker_id"`
		WorkflowStatus string `json:"workflow_status"`
		WorktreePath   string `json:"worktree"`
		Branch         string `json:"branch"`
		LogPath        string `json:"log_path"`
	}
	code, err := c.doJSON(ctx, http.MethodPost, "/api/tickets/start", payload, &out)
	if err != nil {
		return DaemonTicketStartReceipt{}, err
	}
	if code != http.StatusOK {
		return DaemonTicketStartReceipt{}, fmt.Errorf("ticket start 响应码异常: %d", code)
	}
	return DaemonTicketStartReceipt{
		Started:        out.Started,
		Project:        strings.TrimSpace(out.Project),
		TicketID:       out.TicketID,
		WorkerID:       out.WorkerID,
		WorkflowStatus: strings.TrimSpace(out.WorkflowStatus),
		WorktreePath:   strings.TrimSpace(out.WorktreePath),
		Branch:         strings.TrimSpace(out.Branch),
		LogPath:        strings.TrimSpace(out.LogPath),
	}, nil
}

func (c *DaemonAPIClient) SubmitTicketLoop(ctx context.Context, req DaemonTicketLoopSubmitRequest) (DaemonTicketLoopSubmitReceipt, error) {
	if c == nil {
		return DaemonTicketLoopSubmitReceipt{}, fmt.Errorf("daemon client 为空")
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
	code, err := c.doJSON(ctx, http.MethodPost, "/api/ticket-loops/submit", payload, &out)
	if err != nil {
		return DaemonTicketLoopSubmitReceipt{}, err
	}
	if code != http.StatusAccepted {
		return DaemonTicketLoopSubmitReceipt{}, fmt.Errorf("ticket-loop submit 响应码异常: %d", code)
	}
	return DaemonTicketLoopSubmitReceipt{
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

func (c *DaemonAPIClient) ProbeTicketLoop(ctx context.Context, project string, ticketID uint) (DaemonTicketLoopProbeResult, error) {
	if c == nil {
		return DaemonTicketLoopProbeResult{}, fmt.Errorf("daemon client 为空")
	}
	if strings.TrimSpace(project) == "" {
		return DaemonTicketLoopProbeResult{}, fmt.Errorf("project 不能为空")
	}
	if ticketID == 0 {
		return DaemonTicketLoopProbeResult{}, fmt.Errorf("ticket_id 不能为空")
	}
	var out struct {
		Found                bool       `json:"found"`
		OwnedByCurrentDaemon bool       `json:"owned_by_current_daemon"`
		Phase                string     `json:"phase"`
		Project              string     `json:"project"`
		TicketID             uint       `json:"ticket_id"`
		WorkerID             uint       `json:"worker_id"`
		RunID                uint       `json:"run_id"`
		RequestID            string     `json:"request_id"`
		CancelRequestedAt    *time.Time `json:"cancel_requested_at"`
		LastError            string     `json:"last_error"`
	}
	path := fmt.Sprintf("/api/ticket-loops/%d?project=%s", ticketID, url.QueryEscape(strings.TrimSpace(project)))
	code, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	if err != nil {
		return DaemonTicketLoopProbeResult{}, err
	}
	if code != http.StatusOK {
		return DaemonTicketLoopProbeResult{}, fmt.Errorf("ticket-loop probe 响应码异常: %d", code)
	}
	return DaemonTicketLoopProbeResult{
		Found:                out.Found,
		OwnedByCurrentDaemon: out.OwnedByCurrentDaemon,
		Phase:                strings.TrimSpace(out.Phase),
		Project:              strings.TrimSpace(out.Project),
		TicketID:             out.TicketID,
		RequestID:            strings.TrimSpace(out.RequestID),
		RunID:                out.RunID,
		WorkerID:             out.WorkerID,
		CancelRequestedAt:    out.CancelRequestedAt,
		LastError:            strings.TrimSpace(out.LastError),
	}, nil
}

func (c *DaemonAPIClient) CancelTicketLoop(ctx context.Context, project string, ticketID uint) (DaemonTicketLoopCancelResult, error) {
	return c.CancelTicketLoopWithCause(ctx, project, ticketID, contracts.TaskCancelCauseUnknown)
}

func (c *DaemonAPIClient) CancelTicketLoopWithCause(ctx context.Context, project string, ticketID uint, cause contracts.TaskCancelCause) (DaemonTicketLoopCancelResult, error) {
	if c == nil {
		return DaemonTicketLoopCancelResult{}, fmt.Errorf("daemon client 为空")
	}
	if strings.TrimSpace(project) == "" {
		return DaemonTicketLoopCancelResult{}, fmt.Errorf("project 不能为空")
	}
	if ticketID == 0 {
		return DaemonTicketLoopCancelResult{}, fmt.Errorf("ticket_id 不能为空")
	}
	var out struct {
		TicketID  uint   `json:"ticket_id"`
		Found     bool   `json:"found"`
		Canceled  bool   `json:"canceled"`
		Project   string `json:"project"`
		RequestID string `json:"request_id"`
		Reason    string `json:"reason"`
	}
	path := fmt.Sprintf("/api/ticket-loops/%d/cancel?project=%s", ticketID, url.QueryEscape(strings.TrimSpace(project)))
	if cause.Valid() {
		path += "&cause=" + url.QueryEscape(string(cause))
	}
	code, err := c.doJSON(ctx, http.MethodPost, path, nil, &out)
	if err != nil {
		return DaemonTicketLoopCancelResult{}, err
	}
	if code != http.StatusOK {
		return DaemonTicketLoopCancelResult{}, fmt.Errorf("ticket-loop cancel 响应码异常: %d", code)
	}
	return DaemonTicketLoopCancelResult{
		TicketID:  out.TicketID,
		Found:     out.Found,
		Canceled:  out.Canceled,
		Project:   strings.TrimSpace(out.Project),
		RequestID: strings.TrimSpace(out.RequestID),
		Reason:    strings.TrimSpace(out.Reason),
	}, nil
}

func (c *DaemonAPIClient) CancelTaskRun(ctx context.Context, runID uint) (DaemonTaskRunCancelResult, error) {
	return c.CancelTaskRunWithCause(ctx, runID, contracts.TaskCancelCauseUnknown)
}

func (c *DaemonAPIClient) CancelTaskRunWithCause(ctx context.Context, runID uint, cause contracts.TaskCancelCause) (DaemonTaskRunCancelResult, error) {
	if c == nil {
		return DaemonTaskRunCancelResult{}, fmt.Errorf("daemon client 为空")
	}
	if runID == 0 {
		return DaemonTaskRunCancelResult{}, fmt.Errorf("run_id 不能为空")
	}
	var out struct {
		RunID     uint   `json:"run_id"`
		Found     bool   `json:"found"`
		Canceled  bool   `json:"canceled"`
		Project   string `json:"project"`
		RequestID string `json:"request_id"`
		Reason    string `json:"reason"`
	}
	path := fmt.Sprintf("/api/task-runs/%d/cancel", runID)
	if cause != contracts.TaskCancelCauseUnknown {
		if !cause.Valid() {
			return DaemonTaskRunCancelResult{}, fmt.Errorf("未知 cancel cause: %s", cause)
		}
		path += "?cause=" + url.QueryEscape(string(cause))
	}
	code, err := c.doJSON(ctx, http.MethodPost, path, nil, &out)
	if err != nil {
		return DaemonTaskRunCancelResult{}, err
	}
	if code != http.StatusOK {
		return DaemonTaskRunCancelResult{}, fmt.Errorf("task-run cancel 响应码异常: %d", code)
	}
	return DaemonTaskRunCancelResult{
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
