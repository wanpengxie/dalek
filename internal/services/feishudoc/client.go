package feishudoc

import (
	"context"
	"fmt"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

const defaultOpenBaseURL = "https://open.feishu.cn"
const defaultWebBaseURL = "https://feishu.cn"

type Config struct {
	AppID     string
	AppSecret string
	BaseURL   string
}

type Service struct {
	client    *lark.Client
	appID     string
	appSecret string
	baseURL   string
}

type AuthResult struct {
	AppID   string `json:"app_id"`
	BaseURL string `json:"base_url"`
	Expire  int    `json:"expire"`
}

func New(cfg Config) (*Service, error) {
	cfg = cfg.normalized()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	client := lark.NewClient(
		cfg.AppID,
		cfg.AppSecret,
		lark.WithOpenBaseUrl(cfg.BaseURL),
	)

	return &Service{
		client:    client,
		appID:     cfg.AppID,
		appSecret: cfg.AppSecret,
		baseURL:   cfg.BaseURL,
	}, nil
}

func (s *Service) Auth(ctx context.Context) (*AuthResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("feishu service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	resp, err := s.client.GetTenantAccessTokenBySelfBuiltApp(ctx, &larkcore.SelfBuiltTenantAccessTokenReq{
		AppID:     s.appID,
		AppSecret: s.appSecret,
	})
	if err != nil {
		return nil, fmt.Errorf("调用 tenant_access_token 接口失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("调用 tenant_access_token 接口失败: 响应为空")
	}
	if !resp.Success() {
		logID := ""
		if resp.Err != nil {
			logID = strings.TrimSpace(resp.Err.LogID)
		}
		return nil, newCodeError("飞书认证失败", resp.Code, resp.Msg, logID)
	}
	if strings.TrimSpace(resp.TenantAccessToken) == "" {
		return nil, fmt.Errorf("飞书认证成功但 tenant_access_token 为空")
	}

	return &AuthResult{
		AppID:   s.appID,
		BaseURL: s.baseURL,
		Expire:  resp.Expire,
	}, nil
}

func (c Config) normalized() Config {
	c.AppID = strings.TrimSpace(c.AppID)
	c.AppSecret = strings.TrimSpace(c.AppSecret)
	c.BaseURL = normalizeBaseURL(c.BaseURL)
	return c
}

func (c Config) validate() error {
	if c.AppID == "" {
		return fmt.Errorf("app_id 不能为空")
	}
	if c.AppSecret == "" {
		return fmt.Errorf("app_secret 不能为空")
	}
	return nil
}

func normalizeBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		base = defaultOpenBaseURL
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		base = defaultOpenBaseURL
	}
	return base
}

func newCodeError(prefix string, code int, msg, logID string) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "飞书接口调用失败"
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "未知错误"
	}
	logID = strings.TrimSpace(logID)
	if logID != "" {
		return fmt.Errorf("%s(code=%d,msg=%s,log_id=%s)", prefix, code, msg, logID)
	}
	return fmt.Errorf("%s(code=%d,msg=%s)", prefix, code, msg)
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func intValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func boolValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}
