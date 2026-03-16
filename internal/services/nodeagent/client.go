package nodeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ClientConfig struct {
	BaseURL   string
	AuthToken string
	Timeout   time.Duration
}

type Client struct {
	baseURL   string
	authToken string
	client    *http.Client
}

type apiError struct {
	Error string `json:"error"`
	Cause string `json:"cause"`
}

func NewClient(cfg ClientConfig) (*Client, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return nil, fmt.Errorf("node agent base url 不能为空")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("node agent base url 非法: %w", err)
	}
	if strings.TrimSpace(parsed.Scheme) == "" {
		parsed.Scheme = "http"
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("node agent base url 缺少 host")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL:   strings.TrimRight(parsed.String(), "/"),
		authToken: strings.TrimSpace(cfg.AuthToken),
		client: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *Client) Register(ctx context.Context, req RegisterRequest) (RegisterResponse, error) {
	if c == nil {
		return RegisterResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out RegisterResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/register", req, &out)
	if err != nil {
		return RegisterResponse{}, err
	}
	if code != http.StatusOK {
		return RegisterResponse{}, fmt.Errorf("node register 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) Heartbeat(ctx context.Context, req HeartbeatRequest) (HeartbeatResponse, error) {
	if c == nil {
		return HeartbeatResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out HeartbeatResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/heartbeat", req, &out)
	if err != nil {
		return HeartbeatResponse{}, err
	}
	if code != http.StatusOK {
		return HeartbeatResponse{}, fmt.Errorf("node heartbeat 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) SubmitRun(ctx context.Context, req RunSubmitRequest) (RunSubmitResponse, error) {
	if c == nil {
		return RunSubmitResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out RunSubmitResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/run/submit", req, &out)
	if err != nil {
		return RunSubmitResponse{}, err
	}
	if code != http.StatusOK {
		return RunSubmitResponse{}, fmt.Errorf("node run submit 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) QueryRun(ctx context.Context, req RunQueryRequest) (RunQueryResponse, error) {
	if c == nil {
		return RunQueryResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out RunQueryResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/run/query", req, &out)
	if err != nil {
		return RunQueryResponse{}, err
	}
	if code != http.StatusOK {
		return RunQueryResponse{}, fmt.Errorf("node run query 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) CancelRun(ctx context.Context, req RunCancelRequest) (RunCancelResponse, error) {
	if c == nil {
		return RunCancelResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out RunCancelResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/run/cancel", req, &out)
	if err != nil {
		return RunCancelResponse{}, err
	}
	if code != http.StatusOK {
		return RunCancelResponse{}, fmt.Errorf("node run cancel 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) RunLogs(ctx context.Context, req RunLogsRequest) (RunLogsResponse, error) {
	if c == nil {
		return RunLogsResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out RunLogsResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/run/logs", req, &out)
	if err != nil {
		return RunLogsResponse{}, err
	}
	if code != http.StatusOK {
		return RunLogsResponse{}, fmt.Errorf("node run logs 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) RunArtifacts(ctx context.Context, req RunQueryRequest) (RunArtifactsResponse, error) {
	if c == nil {
		return RunArtifactsResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out RunArtifactsResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/run/artifacts", req, &out)
	if err != nil {
		return RunArtifactsResponse{}, err
	}
	if code != http.StatusOK {
		return RunArtifactsResponse{}, fmt.Errorf("node run artifacts 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) UploadSnapshot(ctx context.Context, req SnapshotUploadRequest) (SnapshotUploadResponse, error) {
	if c == nil {
		return SnapshotUploadResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out SnapshotUploadResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/snapshot/upload", req, &out)
	if err != nil {
		return SnapshotUploadResponse{}, err
	}
	if code != http.StatusOK {
		return SnapshotUploadResponse{}, fmt.Errorf("node snapshot upload 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) UploadSnapshotChunk(ctx context.Context, req SnapshotChunkUploadRequest) (SnapshotChunkUploadResponse, error) {
	if c == nil {
		return SnapshotChunkUploadResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out SnapshotChunkUploadResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/snapshot/upload-chunk", req, &out)
	if err != nil {
		return SnapshotChunkUploadResponse{}, err
	}
	if code != http.StatusOK {
		return SnapshotChunkUploadResponse{}, fmt.Errorf("node snapshot chunk upload 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) DownloadSnapshot(ctx context.Context, req SnapshotDownloadRequest) (SnapshotDownloadResponse, error) {
	if c == nil {
		return SnapshotDownloadResponse{}, fmt.Errorf("node agent client 为空")
	}
	var out SnapshotDownloadResponse
	code, err := c.doJSON(ctx, http.MethodPost, "/api/node/snapshot/download", req, &out)
	if err != nil {
		return SnapshotDownloadResponse{}, err
	}
	if code != http.StatusOK {
		return SnapshotDownloadResponse{}, fmt.Errorf("node snapshot download 响应码异常: %d", code)
	}
	return out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, payload any, out any) (int, error) {
	if c == nil || c.client == nil {
		return 0, fmt.Errorf("node agent client 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	target := strings.TrimRight(c.baseURL, "/") + path

	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		var apiErr apiError
		if err := json.Unmarshal(raw, &apiErr); err == nil {
			if strings.TrimSpace(apiErr.Cause) != "" {
				return resp.StatusCode, fmt.Errorf("%s", strings.TrimSpace(apiErr.Cause))
			}
			if strings.TrimSpace(apiErr.Error) != "" {
				return resp.StatusCode, fmt.Errorf("%s", strings.TrimSpace(apiErr.Error))
			}
		}
		return resp.StatusCode, fmt.Errorf("node agent 请求失败: status=%d", resp.StatusCode)
	}
	if out == nil || len(raw) == 0 {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}
