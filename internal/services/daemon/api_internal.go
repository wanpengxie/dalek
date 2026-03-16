package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
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
	nodeagentsvc "dalek/internal/services/nodeagent"

	"gorm.io/gorm"
)

const internalAPIMaxJSONBodyBytes int64 = 1 << 20

type InternalAPIConfig struct {
	ListenAddr     string
	AllowCIDRs     []string
	NodeAgentToken string
}

type InternalAPIOptions struct {
	Logger              *slog.Logger
	GatewaySendDB       *gorm.DB
	GatewayResolver     channelsvc.ProjectResolver
	GatewaySendResolver contracts.ProjectMetaResolver
	GatewaySendSender   gatewaysendsvc.MessageSender
	GatewayQueueDepth   int
	CloseGatewaySendDB  bool
	NodeProjectResolver InternalNodeProjectResolver
}

type InternalAPI struct {
	cfg InternalAPIConfig

	host   *ExecutionHost
	logger *slog.Logger

	listener            net.Listener
	server              *http.Server
	sendHandler         http.HandlerFunc
	gateway             *channelsvc.Gateway
	wsPath              string
	sendDB              *gorm.DB
	closeSendDB         bool
	nodeProjectResolver InternalNodeProjectResolver
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
			ListenAddr:     listen,
			AllowCIDRs:     normalizeInternalAllowCIDRs(cfg.AllowCIDRs),
			NodeAgentToken: strings.TrimSpace(cfg.NodeAgentToken),
		},
		host:                host,
		logger:              logger,
		sendHandler:         gatewaysendsvc.NewHandler(sendService, gatewaysendsvc.HandlerConfig{}),
		gateway:             gateway,
		wsPath:              "/ws",
		sendDB:              opt.GatewaySendDB,
		closeSendDB:         opt.CloseGatewaySendDB,
		nodeProjectResolver: opt.NodeProjectResolver,
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
	mux.HandleFunc("/api/node/register", s.withNodeAgentAccess(s.handleNodeRegister))
	mux.HandleFunc("/api/node/heartbeat", s.withNodeAgentAccess(s.handleNodeHeartbeat))
	mux.HandleFunc("/api/node/snapshot/upload", s.withNodeAgentAccess(s.handleNodeSnapshotUpload))
	mux.HandleFunc("/api/node/snapshot/upload-chunk", s.withNodeAgentAccess(s.handleNodeSnapshotUploadChunk))
	mux.HandleFunc("/api/node/snapshot/download", s.withNodeAgentAccess(s.handleNodeSnapshotDownload))
	mux.HandleFunc("/api/node/run/submit", s.withNodeAgentAccess(s.handleNodeRunSubmit))
	mux.HandleFunc("/api/node/run/cancel", s.withNodeAgentAccess(s.handleNodeRunCancel))
	mux.HandleFunc("/api/node/run/logs", s.withNodeAgentAccess(s.handleNodeRunLogs))
	mux.HandleFunc("/api/node/run/artifacts", s.withNodeAgentAccess(s.handleNodeRunArtifacts))
	mux.HandleFunc("/api/node/run/query", s.withNodeAgentAccess(s.handleNodeRunQuery))
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

func (s *InternalAPI) withNodeAgentAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.authorizeNodeAgent(r); err != nil {
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
	if !ipAllowedByCIDRs(ip, normalizeInternalAllowCIDRs(s.cfg.AllowCIDRs)) {
		return fmt.Errorf("internal api 来源不在 allow_cidrs 内: %s", ip.String())
	}
	return nil
}

func (s *InternalAPI) authorizeNodeAgent(r *http.Request) error {
	if r == nil {
		return fmt.Errorf("request 为空")
	}
	token := strings.TrimSpace(s.cfg.NodeAgentToken)
	if token == "" {
		return fmt.Errorf("node agent token 未配置")
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		return fmt.Errorf("缺少 authorization bearer token")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return fmt.Errorf("authorization 必须为 Bearer token")
	}
	if strings.TrimSpace(strings.TrimPrefix(authHeader, prefix)) != token {
		return fmt.Errorf("node agent token 无效")
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

func (s *InternalAPI) handleSend(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.sendHandler == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "gateway send handler 未初始化")
		return
	}
	s.sendHandler(w, r)
}

func (s *InternalAPI) handleNodeRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.RegisterRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	lastSeenAt, err := parseOptionalRFC3339(payload.LastSeenAt)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "last_seen_at 非法")
		return
	}
	registration, err := project.RegisterNode(r.Context(), NodeRegisterOptions{
		Name:                 strings.TrimSpace(payload.Name),
		Endpoint:             strings.TrimSpace(payload.Endpoint),
		AuthMode:             strings.TrimSpace(payload.AuthMode),
		Status:               string(contracts.NodeStatusOnline),
		Version:              strings.TrimSpace(payload.Version),
		ProtocolVersion:      strings.TrimSpace(payload.ProtocolVersion),
		RoleCapabilities:     append([]string(nil), payload.RoleCapabilities...),
		ProviderModes:        append([]string(nil), payload.ProviderModes...),
		DefaultProvider:      strings.TrimSpace(payload.DefaultProvider),
		ProviderCapabilities: cloneStringAnyMap(payload.ProviderCapabilities),
		SessionAffinity:      strings.TrimSpace(payload.SessionAffinity),
		LastSeenAt:           lastSeenAt,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	lease, err := project.BeginNodeSession(r.Context(), registration.Name, lastSeenAt)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "session_conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.RegisterResponse{
		Accepted:     true,
		Name:         registration.Name,
		SessionEpoch: lease.SessionEpoch,
		Status:       string(contracts.NodeStatusOnline),
	})
}

func (s *InternalAPI) handleNodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.HeartbeatRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	observedAt, err := parseOptionalRFC3339(payload.ObservedAt)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "observed_at 非法")
		return
	}
	if err := project.HeartbeatNodeWithEpoch(r.Context(), strings.TrimSpace(payload.Name), payload.SessionEpoch, observedAt); err != nil {
		status := http.StatusConflict
		code := "session_conflict"
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
			code = "not_found"
		}
		writeAPIError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.HeartbeatResponse{
		Accepted:     true,
		Name:         strings.TrimSpace(payload.Name),
		SessionEpoch: payload.SessionEpoch,
		Status:       string(contracts.NodeStatusOnline),
	})
}

func (s *InternalAPI) handleNodeRunSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.RunSubmitRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := validateNodeSessionEpoch(r.Context(), project, strings.TrimSpace(payload.NodeName), payload.Meta.SessionEpoch); err != nil {
		writeAPIError(w, http.StatusConflict, "session_conflict", err.Error())
		return
	}
	submission, err := project.SubmitRun(r.Context(), NodeRunSubmitOptions{
		RequestID:    strings.TrimSpace(payload.Meta.RequestID),
		TicketID:     0,
		VerifyTarget: strings.TrimSpace(payload.VerifyTarget),
		SnapshotID:   strings.TrimSpace(payload.SnapshotID),
		BaseCommit:   strings.TrimSpace(payload.BaseCommit),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.RunSubmitResponse{
		Accepted:  submission.Accepted,
		RunID:     submission.RunID,
		TaskRunID: submission.TaskRunID,
		RequestID: submission.RequestID,
		Status:    strings.TrimSpace(submission.RunStatus),
	})
}

func (s *InternalAPI) handleNodeRunQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.RunQueryRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var view *nodeagentsvc.RunQueryResponse
	var nodeView *NodeRunView
	switch {
	case payload.Meta.RunID != 0:
		nodeView, err = project.GetRun(r.Context(), payload.Meta.RunID)
	case strings.TrimSpace(payload.Meta.RequestID) != "":
		nodeView, err = project.GetRunByRequestID(r.Context(), strings.TrimSpace(payload.Meta.RequestID))
	default:
		writeAPIError(w, http.StatusBadRequest, "bad_request", "run_id 或 request_id 至少提供一个")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if nodeView == nil {
		writeJSON(w, http.StatusOK, nodeagentsvc.RunQueryResponse{Found: false})
		return
	}
	view = &nodeagentsvc.RunQueryResponse{
		Found:          true,
		RunID:          nodeView.RunID,
		TaskRunID:      nodeView.TaskRunID,
		Status:         strings.TrimSpace(nodeView.RunStatus),
		LifecycleStage: strings.TrimSpace(nodeView.LifecycleStage),
		Summary:        strings.TrimSpace(nodeView.Summary),
		UpdatedAt:      nodeView.UpdatedAt,
		SnapshotID:     nodeView.SnapshotID,
		VerifyTarget:   nodeView.VerifyTarget,
		ArtifactCount:  nodeView.ArtifactCount,
		LastEventType:  strings.TrimSpace(nodeView.LastEventType),
		LastEventNote:  strings.TrimSpace(nodeView.LastEventNote),
		ProtocolSource: nodeagentsvc.ProtocolVersionV1,
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *InternalAPI) handleNodeRunCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.RunCancelRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if payload.Meta.RunID == 0 {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "run_id 不能为空")
		return
	}
	result, err := project.CancelRun(r.Context(), payload.Meta.RunID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "cancel_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.RunCancelResponse{
		Accepted: result.Found,
		Found:    result.Found,
		Canceled: result.Canceled,
		Reason:   result.Reason,
	})
}

func (s *InternalAPI) handleNodeSnapshotUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.SnapshotUploadRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := validateNodeSessionEpoch(r.Context(), project, strings.TrimSpace(payload.NodeName), payload.Meta.SessionEpoch); err != nil {
		writeAPIError(w, http.StatusConflict, "session_conflict", err.Error())
		return
	}
	expiresAt, err := parseOptionalRFC3339(payload.ExpiresAt)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "expires_at 非法")
		return
	}
	res, err := project.UploadSnapshot(r.Context(), NodeSnapshotUploadOptions{
		SnapshotID:          strings.TrimSpace(payload.SnapshotID),
		NodeName:            strings.TrimSpace(payload.NodeName),
		BaseCommit:          strings.TrimSpace(payload.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(payload.WorkspaceGeneration),
		ManifestJSON:        strings.TrimSpace(payload.ManifestJSON),
		ExpiresAt:           expiresAt,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "upload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.SnapshotUploadResponse{
		Accepted:            true,
		SnapshotID:          res.SnapshotID,
		Status:              res.Status,
		ManifestDigest:      res.ManifestDigest,
		ArtifactPath:        res.ArtifactPath,
		BaseCommit:          res.BaseCommit,
		WorkspaceGeneration: res.WorkspaceGeneration,
	})
}

func (s *InternalAPI) handleNodeSnapshotUploadChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.SnapshotChunkUploadRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := validateNodeSessionEpoch(r.Context(), project, strings.TrimSpace(payload.NodeName), payload.Meta.SessionEpoch); err != nil {
		writeAPIError(w, http.StatusConflict, "session_conflict", err.Error())
		return
	}
	expiresAt, err := parseOptionalRFC3339(payload.ExpiresAt)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "expires_at 非法")
		return
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload.ChunkData))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "chunk_data 非法")
		return
	}
	res, err := project.UploadSnapshotChunk(r.Context(), NodeSnapshotChunkUploadOptions{
		SnapshotID:          strings.TrimSpace(payload.SnapshotID),
		NodeName:            strings.TrimSpace(payload.NodeName),
		BaseCommit:          strings.TrimSpace(payload.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(payload.WorkspaceGeneration),
		ChunkIndex:          payload.ChunkIndex,
		ChunkData:           data,
		IsFinal:             payload.IsFinal,
		ExpiresAt:           expiresAt,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "upload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.SnapshotChunkUploadResponse{
		Accepted:            res.Accepted,
		SnapshotID:          res.SnapshotID,
		Status:              res.Status,
		NextIndex:           res.NextIndex,
		ManifestDigest:      res.ManifestDigest,
		ArtifactPath:        res.ArtifactPath,
		BaseCommit:          res.BaseCommit,
		WorkspaceGeneration: res.WorkspaceGeneration,
	})
}

func validateNodeSessionEpoch(ctx context.Context, project InternalNodeProject, nodeName string, sessionEpoch int) error {
	if project == nil {
		return fmt.Errorf("node project 未初始化")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return fmt.Errorf("node_name 不能为空")
	}
	if sessionEpoch <= 0 {
		return fmt.Errorf("session_epoch 不能为空")
	}
	return project.HeartbeatNodeWithEpoch(ctx, nodeName, sessionEpoch, nil)
}

func (s *InternalAPI) handleNodeSnapshotDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.SnapshotDownloadRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	res, err := project.DownloadSnapshot(r.Context(), strings.TrimSpace(payload.SnapshotID))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.SnapshotDownloadResponse{
		Found:               res.Found,
		SnapshotID:          res.SnapshotID,
		Status:              res.Status,
		ManifestDigest:      res.ManifestDigest,
		ManifestJSON:        res.ManifestJSON,
		ArtifactPath:        res.ArtifactPath,
		BaseCommit:          res.BaseCommit,
		WorkspaceGeneration: res.WorkspaceGeneration,
	})
}

func (s *InternalAPI) handleNodeRunLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.RunLogsRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if payload.Meta.RunID == 0 {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "run_id 不能为空")
		return
	}
	logs, err := project.GetRunLogs(r.Context(), payload.Meta.RunID, payload.Lines)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.RunLogsResponse{
		Found: logs.Found,
		RunID: logs.RunID,
		Tail:  logs.Tail,
	})
}

func (s *InternalAPI) handleNodeRunArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
		return
	}
	if s == nil || s.nodeProjectResolver == nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "node project resolver 未初始化")
		return
	}
	var payload nodeagentsvc.RunQueryRequest
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	project, err := s.openNodeProject(strings.TrimSpace(payload.Meta.ProjectKey))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if payload.Meta.RunID == 0 {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "run_id 不能为空")
		return
	}
	artifacts, err := project.ListRunArtifacts(r.Context(), payload.Meta.RunID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	out := make([]nodeagentsvc.ArtifactSummary, 0, len(artifacts.Artifacts))
	for _, item := range artifacts.Artifacts {
		out = append(out, nodeagentsvc.ArtifactSummary{
			Name: item.Name,
			Kind: item.Kind,
			Size: item.Size,
			Ref:  item.Ref,
		})
	}
	issues := make([]nodeagentsvc.ArtifactIssue, 0, len(artifacts.Issues))
	for _, item := range artifacts.Issues {
		issues = append(issues, nodeagentsvc.ArtifactIssue{
			Name:   item.Name,
			Status: item.Status,
			Reason: item.Reason,
		})
	}
	writeJSON(w, http.StatusOK, nodeagentsvc.RunArtifactsResponse{
		Found:     artifacts.Found,
		RunID:     artifacts.RunID,
		Artifacts: out,
		Issues:    issues,
	})
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

func (s *InternalAPI) openNodeProject(projectKey string) (InternalNodeProject, error) {
	if s == nil || s.nodeProjectResolver == nil {
		return nil, fmt.Errorf("node project resolver 未初始化")
	}
	projectKey = strings.TrimSpace(projectKey)
	if projectKey == "" {
		return nil, fmt.Errorf("project_key 不能为空")
	}
	return s.nodeProjectResolver.OpenNodeProject(projectKey)
}

func parseOptionalRFC3339(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	local := parsed.Local()
	return &local, nil
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
		return fmt.Errorf("internal listen 必须显式使用 host:port")
	}
	return nil
}

func normalizeInternalAllowCIDRs(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return []string{"127.0.0.1/32", "::1/128"}
	}
	return out
}

func ipAllowedByCIDRs(ip net.IP, allowCIDRs []string) bool {
	if ip == nil {
		return false
	}
	for _, item := range allowCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(item))
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
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
