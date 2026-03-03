package feishudoc

import (
	"fmt"
	"net/url"
	"strings"
)

// ParsedURL holds the token and type extracted from a feishu document URL.
type ParsedURL struct {
	Token     string `json:"token"`
	TokenType string `json:"token_type"`
}

// ParseURL extracts the token and token_type from a feishu URL.
// Supported patterns:
//
//	https://feishu.cn/docx/{token}
//	https://feishu.cn/wiki/{token}
//	https://xxx.feishu.cn/docx/{token}
//	https://xxx.larksuite.com/docx/{token}
func ParseURL(raw string) (*ParsedURL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("URL 不能为空")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("URL 格式无效: %w", err)
	}

	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, "feishu.cn") && !strings.HasSuffix(host, "larksuite.com") {
		return nil, fmt.Errorf("不是飞书 URL: %s", host)
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < 2 {
		return nil, fmt.Errorf("URL 路径不完整，无法解析 token: %s", u.Path)
	}

	pathType := strings.ToLower(segments[0])
	token := segments[1]
	// strip query fragments from token if any slipped through
	if idx := strings.IndexAny(token, "?#"); idx >= 0 {
		token = token[:idx]
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("URL 中未找到 token: %s", raw)
	}

	tokenType, err := pathTypeToTokenType(pathType)
	if err != nil {
		return nil, err
	}

	return &ParsedURL{
		Token:     token,
		TokenType: tokenType,
	}, nil
}

func pathTypeToTokenType(pathType string) (string, error) {
	switch pathType {
	case "docx":
		return "docx", nil
	case "docs":
		return "doc", nil
	case "wiki":
		return "wiki", nil
	case "sheets":
		return "sheet", nil
	case "slides":
		return "slides", nil
	case "base":
		return "bitable", nil
	case "mindnotes":
		return "mindnote", nil
	case "file":
		return "file", nil
	default:
		return "", fmt.Errorf("无法识别的飞书文档类型: %s", pathType)
	}
}
