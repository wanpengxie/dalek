package daemon

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dalek/internal/testutil"
)

func TestRecoverMiddleware_RecoversPanic(t *testing.T) {
	logger, buf := testutil.NewSlogBufferLogger(slog.LevelDebug)
	handler := RecoverMiddleware(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status mismatch: got=%d want=%d", rec.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(rec.Body.String(), "internal_server_error") {
		t.Fatalf("response body mismatch: %s", rec.Body.String())
	}

	logs := buf.String()
	if !strings.Contains(logs, "http handler panic recovered") {
		t.Fatalf("expected panic recover log, got=%s", logs)
	}
	if !strings.Contains(logs, `"method":"GET"`) || !strings.Contains(logs, `"path":"/panic"`) {
		t.Fatalf("expected method/path fields in logs, got=%s", logs)
	}
	if !strings.Contains(logs, `"panic":"boom"`) {
		t.Fatalf("expected panic value in logs, got=%s", logs)
	}
}

func TestRecoverMiddleware_PassthroughWhenNoPanic(t *testing.T) {
	logger, buf := testutil.NewSlogBufferLogger(slog.LevelDebug)
	handler := RecoverMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status mismatch: got=%d want=%d", rec.Code, http.StatusNoContent)
	}
	if strings.Contains(buf.String(), "panic recovered") {
		t.Fatalf("unexpected panic log: %s", buf.String())
	}
}
