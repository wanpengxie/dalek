package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type OpenAICompatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAICompatChatRequest struct {
	Model        string                `json:"-"`
	Messages     []OpenAICompatMessage `json:"-"`
	JSONResponse bool                  `json:"-"`
	Temperature  *float64              `json:"-"`
	MaxTokens    int                   `json:"-"`
}

type OpenAICompatChatResult struct {
	Content     string
	RawResponse string
}

type OpenAICompatClient struct {
	BaseURL      string
	APIKey       string
	ExtraHeaders map[string]string
	HTTPClient   *http.Client
}

type openAICompatResponseFormat struct {
	Type string `json:"type"`
}

type openAICompatChatPayload struct {
	Model          string                      `json:"model"`
	Messages       []OpenAICompatMessage       `json:"messages"`
	ResponseFormat *openAICompatResponseFormat `json:"response_format,omitempty"`
	Temperature    *float64                    `json:"temperature,omitempty"`
	MaxTokens      int                         `json:"max_tokens,omitempty"`
}

type openAICompatChatResp struct {
	Choices []struct {
		Message struct {
			Content any `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

type OpenAICompatHTTPError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *OpenAICompatHTTPError) Error() string {
	if e == nil {
		return "openai_compat http error"
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = strings.TrimSpace(e.Body)
	}
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("openai_compat http %d: %s", e.StatusCode, msg)
}

func (c OpenAICompatClient) ChatCompletions(ctx context.Context, req OpenAICompatChatRequest) (OpenAICompatChatResult, error) {
	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return OpenAICompatChatResult{}, fmt.Errorf("openai_compat base_url 为空")
	}
	apiKey := strings.TrimSpace(c.APIKey)
	if apiKey == "" {
		return OpenAICompatChatResult{}, fmt.Errorf("openai_compat api_key 为空")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return OpenAICompatChatResult{}, fmt.Errorf("openai_compat model 为空")
	}
	if len(req.Messages) == 0 {
		return OpenAICompatChatResult{}, fmt.Errorf("openai_compat messages 为空")
	}

	payload := openAICompatChatPayload{
		Model:    model,
		Messages: req.Messages,
	}
	if req.JSONResponse {
		payload.ResponseFormat = &openAICompatResponseFormat{Type: "json_object"}
	}
	if req.Temperature != nil {
		payload.Temperature = req.Temperature
	}
	if req.MaxTokens > 0 {
		payload.MaxTokens = req.MaxTokens
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return OpenAICompatChatResult{}, err
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return OpenAICompatChatResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range c.ExtraHeaders {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		httpReq.Header.Set(key, val)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return OpenAICompatChatResult{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return OpenAICompatChatResult{}, err
	}
	rawText := strings.TrimSpace(string(raw))

	var decoded openAICompatChatResp
	_ = json.Unmarshal(raw, &decoded)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(rawText)
		if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
			msg = strings.TrimSpace(decoded.Error.Message)
		}
		return OpenAICompatChatResult{}, &OpenAICompatHTTPError{
			StatusCode: resp.StatusCode,
			Message:    msg,
			Body:       rawText,
		}
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return OpenAICompatChatResult{}, fmt.Errorf("openai_compat error: %s", strings.TrimSpace(decoded.Error.Message))
	}

	content := ""
	for _, choice := range decoded.Choices {
		content = extractOpenAICompatContent(choice.Message.Content)
		if strings.TrimSpace(content) != "" {
			break
		}
	}
	if strings.TrimSpace(content) == "" {
		return OpenAICompatChatResult{}, fmt.Errorf("openai_compat 返回内容为空")
	}

	return OpenAICompatChatResult{
		Content:     strings.TrimSpace(content),
		RawResponse: rawText,
	}, nil
}

func extractOpenAICompatContent(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case map[string]any:
		if s, ok := t["text"].(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			switch node := item.(type) {
			case string:
				if s := strings.TrimSpace(node); s != "" {
					parts = append(parts, s)
				}
			case map[string]any:
				if s, ok := node["text"].(string); ok && strings.TrimSpace(s) != "" {
					parts = append(parts, strings.TrimSpace(s))
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}
