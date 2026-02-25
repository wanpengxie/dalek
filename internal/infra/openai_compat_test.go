package infra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatClient_ChatCompletions_Success(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if got := strings.TrimSpace(r.Header.Get("X-Test")); got != "ok" {
			t.Fatalf("unexpected custom header: %q", got)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"state\":\"busy\",\"needs_user\":false,\"summary\":\"ok\"}"}}]}`))
	}))
	defer s.Close()

	c := OpenAICompatClient{
		BaseURL: s.URL,
		APIKey:  "test-key",
		ExtraHeaders: map[string]string{
			"X-Test": "ok",
		},
	}
	out, err := c.ChatCompletions(context.Background(), OpenAICompatChatRequest{
		Model: "demo-model",
		Messages: []OpenAICompatMessage{
			{Role: "system", Content: "s"},
			{Role: "user", Content: "u"},
		},
		JSONResponse: true,
	})
	if err != nil {
		t.Fatalf("ChatCompletions failed: %v", err)
	}
	if !strings.Contains(out.Content, `"state":"busy"`) {
		t.Fatalf("unexpected content: %q", out.Content)
	}
}

func TestOpenAICompatClient_ChatCompletions_HTTPError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad_request"}}`))
	}))
	defer s.Close()

	c := OpenAICompatClient{
		BaseURL: s.URL,
		APIKey:  "test-key",
	}
	_, err := c.ChatCompletions(context.Background(), OpenAICompatChatRequest{
		Model: "demo-model",
		Messages: []OpenAICompatMessage{
			{Role: "system", Content: "s"},
			{Role: "user", Content: "u"},
		},
	})
	if err == nil {
		t.Fatalf("expected bad_request error, got nil")
	}
	var httpErr *OpenAICompatHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected OpenAICompatHTTPError, got=%T err=%v", err, err)
	}
	if httpErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d", httpErr.StatusCode)
	}
	if !strings.Contains(httpErr.Error(), "bad_request") {
		t.Fatalf("expected bad_request in error text, got=%v", httpErr)
	}
}
