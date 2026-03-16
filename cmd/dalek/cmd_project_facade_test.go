package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRemoteProject_UsesDaemonInternalConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
		})
	}))
	defer server.Close()

	root := t.TempDir()
	addr := strings.TrimPrefix(server.URL, "http://")
	cfg := `{"schema_version":2,"daemon":{"internal":{"listen":"` + addr + `"}}}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, remote, err := openRemoteProject(root, "demo")
	if err != nil {
		t.Fatalf("openRemoteProject failed: %v", err)
	}
	if got := remote.Project(); got != "demo" {
		t.Fatalf("unexpected project name: %q", got)
	}
}
