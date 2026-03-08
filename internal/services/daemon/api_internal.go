package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	gatewaysendsvc "dalek/internal/services/gatewaysend"

	"gorm.io/gorm"
)

const (
	internalAPIMaxJSONBodyBytes int64 = 1 << 20
	internalAPIDashboardListCap int   = 2000
)

type InternalAPIConfig struct {
	ListenAddr string
}

type InternalAPIOptions struct {
	Logger              *slog.Logger
	GatewaySendDB       *gorm.DB
	GatewayResolver     channelsvc.ProjectResolver
	GatewaySendResolver contracts.ProjectMetaResolver
	GatewaySendSender   gatewaysendsvc.MessageSender
	GatewayQueueDepth   int
	CloseGatewaySendDB  bool
}

type InternalAPI struct {
	cfg InternalAPIConfig

	host   *ExecutionHost
	logger *slog.Logger

	listener    net.Listener
	server      *http.Server
	sendHandler http.HandlerFunc
	gateway     *channelsvc.Gateway
	wsPath      string
	sendDB      *gorm.DB
	closeSendDB bool
}

func NewInternalAPI(host *ExecutionHost, cfg InternalAPIConfig, opt InternalAPIOptions) (*InternalAPI, error) {
	if host == nil {
		return nil, fmt.Errorf("internal api 缺少 execution host")
	}
	listen := strings.TrimSpace(cfg.ListenAddr)
	if listen == "" {
		return nil, fmt.Errorf("internal listen 地址不能为空")
	}
	if err := validateInternalListenAddr(listen); err != nil {
		return nil, err
	}
	logger := opt.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	gateway, gatewayErr := channelsvc.NewGateway(opt.GatewaySendDB, opt.GatewayResolver, channelsvc.GatewayOptions{
		QueueDepth: opt.GatewayQueueDepth,
		Logger:     logger,
	})
	if gatewayErr != nil {
		gateway = nil
		if opt.GatewaySendDB != nil && opt.GatewayResolver != nil {
			return nil, fmt.Errorf("创建 internal gateway runtime 失败: %w", gatewayErr)
		}
	}
	sendService := gatewaysendsvc.NewServiceWithDB(opt.GatewaySendDB, opt.GatewaySendResolver, opt.GatewaySendSender, logger)
	return &InternalAPI{
		cfg: InternalAPIConfig{
			ListenAddr: listen,
		},
		host:        host,
		logger:      logger,
		sendHandler: gatewaysendsvc.NewHandler(sendService, gatewaysendsvc.HandlerConfig{}),
		gateway:     gateway,
		wsPath:      "/ws",
		sendDB:      opt.GatewaySendDB,
		closeSendDB: opt.CloseGatewaySendDB,
	}, nil
}

func (s *InternalAPI) Name() string {
	return "internal_api"
}

func (s *InternalAPI) Start(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("internal api 为空")
	}
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	wsPath, wsHandler := newInternalGatewayWSHandler(s.gateway, internalGatewayWSOptions{
		Path:          s.wsPath,
		DefaultSender: "ws.user",
	}, s.logger)
	mux.HandleFunc("/health", s.withInternalAccess(s.handleHealth))
	mux.HandleFunc("/api/dispatch/submit", s.withInternalAccess(s.handleDispatchSubmit))
	mux.HandleFunc("/api/worker-run/submit", s.withInternalAccess(s.handleWorkerRunSubmit))
	mux.HandleFunc("/api/subagent/submit", s.withInternalAccess(s.handleSubagentSubmit))
	mux.HandleFunc("/api/notes", s.withInternalAccess(s.handleNoteSubmit))
	mux.HandleFunc("/api/runs/", s.withInternalAccess(s.handleRuns))
	mux.HandleFunc("/api/v1/overview", s.withInternalAccess(s.handleOverview))
	mux.HandleFunc("/api/v1/planner", s.withInternalAccess(s.handlePlanner))
	mux.HandleFunc("/api/v1/merges", s.withInternalAccess(s.handleMerges))
	mux.HandleFunc("/api/v1/inbox", s.withInternalAccess(s.handleInbox))
	mux.HandleFunc("/api/v1/tickets", s.withInternalAccess(s.handleTickets))
	mux.HandleFunc("/api/v1/tickets/", s.withInternalAccess(s.handleTickets))
	mux.HandleFunc(gatewaysendsvc.Path, s.withInternalAccess(s.handleSend))
	mux.HandleFunc(wsPath, s.withInternalAccess(wsHandler))

	server := &http.Server{
		Handler:           RecoverMiddleware(s.logger.With("component", "internal_api"))(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.listener = ln
	s.server = server

	go func() {
		if serveErr := server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			s.logger.Error("internal api serve failed", "error", serveErr)
		}
	}()
	s.logger.Info("internal api listening",
		"listen_addr", s.cfg.ListenAddr,
		"ws_path", wsPath,
	)
	return nil
}

func (s *InternalAPI) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var shutdownErr error
	if s.server != nil {
		shutdownCtx := ctx
		if shutdownCtx == nil {
			var cancel context.CancelFunc
			shutdownCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
		}
		shutdownErr = s.server.Shutdown(shutdownCtx)
		s.server = nil
		s.listener = nil
	}
	if s.closeSendDB && s.sendDB != nil {
		if err := closeGatewayDB(s.sendDB); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		s.sendDB = nil
	}
	return shutdownErr
}

func closeGatewayDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *InternalAPI) withInternalAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.authorize(r); err != nil {
			writeAPIError(w, http.StatusForbidden, "forbidden", err.Error())
			return
		}
		next(w, r)
	}
}

func (s *InternalAPI) authorize(r *http.Request) error {
	if r == nil {
		return fmt.Errorf("request 为空")
	}
	ip, err := remoteIP(r.RemoteAddr)
	if err != nil {
		return err
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("internal api 仅允许 loopback 来源: %s", ip.String())
	}
	return nil
}

func (s *InternalAPI) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "dalek.daemon.internal",
	})
}

func (s *InternalAPI) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	projectName, err := parseProjectQuery(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	overview, err := s.host.GetProjectDashboard(r.Context(), projectName)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *InternalAPI) handlePlanner(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	projectName, err := parseProjectQuery(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	planner, err := s.host.GetProjectPlannerState(r.Context(), projectName)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, planner)
}

func (s *InternalAPI) handleMerges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	projectName, err := parseProjectQuery(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	status, err := parseMergeStatusQuery(r.URL.Query().Get("status"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	items, err := s.host.ListProjectMerges(r.Context(), projectName, status, internalAPIDashboardListCap)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	if items == nil {
		items = []contracts.MergeItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *InternalAPI) handleInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	projectName, err := parseProjectQuery(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	status, err := parseInboxStatusQuery(r.URL.Query().Get("status"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	items, err := s.host.ListProjectInbox(r.Context(), projectName, status, internalAPIDashboardListCap)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	if items == nil {
		items = []contracts.InboxItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *InternalAPI) handleSend(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.sendHandler == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "gateway send handler 未初始化")
		return
	}
	s.sendHandler(w, r)
}

type dispatchSubmitPayload struct {
	RequestID string `json:"request_id"`
	Project   string `json:"project"`
	TicketID  uint   `json:"ticket_id"`
	Prompt    string `json:"prompt"`
	AutoStart *bool  `json:"auto_start"`
	Sync      bool   `json:"sync"`
	TimeoutMS int64  `json:"timeout_ms"`
}

type workerRunSubmitPayload struct {
	RequestID string `json:"request_id"`
	Project   string `json:"project"`
	TicketID  uint   `json:"ticket_id"`
	Prompt    string `json:"prompt"`
	Sync      bool   `json:"sync"`
	TimeoutMS int64  `json:"timeout_ms"`
}

type subagentSubmitPayload struct {
	RequestID string `json:"request_id"`
	Project   string `json:"project"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	Sync      bool   `json:"sync"`
	TimeoutMS int64  `json:"timeout_ms"`
}

type noteSubmitPayload struct {
	Project string `json:"project"`
	Text    string `json:"text"`
}

func (s *InternalAPI) handleDispatchSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	var payload dispatchSubmitPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if payload.Sync {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "daemon submit 不支持 sync=true")
		return
	}
	receipt, err := s.host.SubmitDispatch(r.Context(), DispatchSubmitRequest{
		Project:   strings.TrimSpace(payload.Project),
		TicketID:  payload.TicketID,
		RequestID: strings.TrimSpace(payload.RequestID),
		Prompt:    strings.TrimSpace(payload.Prompt),
		AutoStart: payload.AutoStart,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "submit_failed", err.Error())
		return
	}
	runID := receipt.TaskRunID
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":    true,
		"project":     receipt.Project,
		"request_id":  receipt.RequestID,
		"task_run_id": runID,
		"ticket_id":   receipt.TicketID,
		"worker_id":   receipt.WorkerID,
		"query": map[string]string{
			"show":   fmt.Sprintf("dalek task show --id %d", runID),
			"events": fmt.Sprintf("dalek task events --id %d", runID),
			"cancel": fmt.Sprintf("dalek task cancel --id %d", runID),
		},
	})
}

func (s *InternalAPI) handleWorkerRunSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	var payload workerRunSubmitPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if payload.Sync {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "daemon submit 不支持 sync=true")
		return
	}
	receipt, err := s.host.SubmitWorkerRun(r.Context(), WorkerRunSubmitRequest{
		Project:   strings.TrimSpace(payload.Project),
		TicketID:  payload.TicketID,
		RequestID: strings.TrimSpace(payload.RequestID),
		Prompt:    strings.TrimSpace(payload.Prompt),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "submit_failed", err.Error())
		return
	}
	runID := receipt.TaskRunID
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":    true,
		"project":     receipt.Project,
		"request_id":  receipt.RequestID,
		"task_run_id": runID,
		"ticket_id":   receipt.TicketID,
		"worker_id":   receipt.WorkerID,
		"query": map[string]string{
			"show":   fmt.Sprintf("dalek task show --id %d", runID),
			"events": fmt.Sprintf("dalek task events --id %d", runID),
			"cancel": fmt.Sprintf("dalek task cancel --id %d", runID),
		},
	})
}

func (s *InternalAPI) handleSubagentSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	var payload subagentSubmitPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if payload.Sync {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "daemon submit 不支持 sync=true")
		return
	}
	receipt, err := s.host.SubmitSubagentRun(r.Context(), SubagentSubmitRequest{
		Project:   strings.TrimSpace(payload.Project),
		RequestID: strings.TrimSpace(payload.RequestID),
		Provider:  strings.TrimSpace(payload.Provider),
		Model:     strings.TrimSpace(payload.Model),
		Prompt:    strings.TrimSpace(payload.Prompt),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "submit_failed", err.Error())
		return
	}
	runID := receipt.TaskRunID
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":    true,
		"project":     receipt.Project,
		"request_id":  receipt.RequestID,
		"provider":    receipt.Provider,
		"model":       receipt.Model,
		"runtime_dir": receipt.RuntimeDir,
		"task_run_id": runID,
		"query": map[string]string{
			"show":   fmt.Sprintf("dalek agent show --run-id %d", runID),
			"logs":   fmt.Sprintf("dalek agent logs --run-id %d", runID),
			"cancel": fmt.Sprintf("dalek agent cancel --run-id %d", runID),
		},
	})
}

func (s *InternalAPI) handleNoteSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	var payload noteSubmitPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	receipt, err := s.host.SubmitNote(r.Context(), NoteSubmitRequest{
		Project: strings.TrimSpace(payload.Project),
		Text:    strings.TrimSpace(payload.Text),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "submit_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":       true,
		"project":        receipt.Project,
		"note_id":        receipt.NoteID,
		"shaped_item_id": receipt.ShapedItemID,
		"deduped":        receipt.Deduped,
	})
}

func (s *InternalAPI) handleRuns(w http.ResponseWriter, r *http.Request) {
	runID, tail, ok := parseRunRoute(r.URL.Path)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "路径不存在")
		return
	}
	switch {
	case r.Method == http.MethodGet && tail == "":
		s.handleRunShow(w, r, runID)
	case r.Method == http.MethodGet && tail == "events":
		s.handleRunEvents(w, r, runID)
	case r.Method == http.MethodPost && tail == "cancel":
		s.handleRunCancel(w, r, runID)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method/path 不支持")
	}
}

func (s *InternalAPI) handleRunShow(w http.ResponseWriter, r *http.Request, runID uint) {
	status, err := s.host.GetRunStatus(r.Context(), runID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	if status == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "run 不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run": status,
	})
}

func (s *InternalAPI) handleRunEvents(w http.ResponseWriter, r *http.Request, runID uint) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	events, err := s.host.ListRunEvents(r.Context(), runID, limit)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	if events == nil {
		events = []RunEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id": runID,
		"events": events,
	})
}

func (s *InternalAPI) handleRunCancel(w http.ResponseWriter, r *http.Request, runID uint) {
	result, err := s.host.CancelRun(runID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "cancel_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":     runID,
		"found":      result.Found,
		"canceled":   result.Canceled,
		"project":    result.Project,
		"request_id": result.RequestID,
		"reason":     result.Reason,
	})
}

func parseRunRoute(path string) (uint, string, bool) {
	const prefix = "/api/runs/"
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return 0, "", false
	}
	parts := strings.Split(rest, "/")
	runID64, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || runID64 == 0 {
		return 0, "", false
	}
	tail := ""
	if len(parts) >= 2 {
		tail = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		return 0, "", false
	}
	return uint(runID64), tail, true
}

func parseProjectQuery(r *http.Request) (string, error) {
	if r == nil || r.URL == nil {
		return "", fmt.Errorf("request 为空")
	}
	projectName := strings.TrimSpace(r.URL.Query().Get("project"))
	if projectName == "" {
		return "", fmt.Errorf("project 不能为空")
	}
	return projectName, nil
}

func parseMergeStatusQuery(raw string) (contracts.MergeStatus, error) {
	status := contracts.MergeStatus(strings.TrimSpace(strings.ToLower(raw)))
	if status == "" {
		return "", nil
	}
	switch status {
	case contracts.MergeProposed,
		contracts.MergeChecksRunning,
		contracts.MergeReady,
		contracts.MergeApproved,
		contracts.MergeMerged,
		contracts.MergeDiscarded,
		contracts.MergeBlocked:
		return status, nil
	default:
		return "", fmt.Errorf("merge status 非法: %s", strings.TrimSpace(raw))
	}
}

func parseInboxStatusQuery(raw string) (contracts.InboxStatus, error) {
	status := contracts.InboxStatus(strings.TrimSpace(strings.ToLower(raw)))
	if status == "" {
		return "", nil
	}
	switch status {
	case contracts.InboxOpen, contracts.InboxDone, contracts.InboxSnoozed:
		return status, nil
	default:
		return "", fmt.Errorf("inbox status 非法: %s", strings.TrimSpace(raw))
	}
}

func remoteIP(remoteAddr string) (net.IP, error) {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return nil, fmt.Errorf("remote_addr 为空")
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		ip := net.ParseIP(strings.TrimSpace(host))
		if ip != nil {
			return ip, nil
		}
	}
	ip := net.ParseIP(remoteAddr)
	if ip == nil {
		return nil, fmt.Errorf("无法解析 remote ip: %s", remoteAddr)
	}
	return ip, nil
}

func validateInternalListenAddr(listenAddr string) error {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		return fmt.Errorf("internal listen 地址不能为空")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("internal listen 格式非法（需 host:port）: %w", err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("internal listen 必须显式使用 loopback 地址（127.0.0.1 或 ::1）")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("internal listen 仅允许 loopback 地址，当前=%s", host)
	}
	return nil
}

func decodeJSONBody(r *http.Request, out any) error {
	if r == nil || r.Body == nil {
		return fmt.Errorf("request body 为空")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, internalAPIMaxJSONBodyBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > internalAPIMaxJSONBodyBytes {
		return fmt.Errorf("request body 过大（max=%d bytes）", internalAPIMaxJSONBodyBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("request body 必须是单个 JSON 对象")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	if w == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal_error","cause":"json marshal failed"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func writeAPIError(w http.ResponseWriter, code int, codeName, cause string) {
	writeJSON(w, code, map[string]any{
		"error": codeName,
		"cause": cause,
	})
}
