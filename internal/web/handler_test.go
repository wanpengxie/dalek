package web

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler_ServesStaticAsset(t *testing.T) {
	handler := NewHandler(fstest.MapFS{
		"index.html":  {Data: []byte("<!doctype html><title>test</title>")},
		"style.css":   {Data: []byte("body{color:#000;}")},
		"favicon.svg": {Data: []byte("<svg/>")},
	}, "")

	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for static asset, got=%d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "body{color:#000;}") {
		t.Fatalf("unexpected style.css response body: %q", body)
	}
}

func TestHandler_FallsBackToIndexForRoutePath(t *testing.T) {
	handler := NewHandler(fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><h1>shell</h1>")},
		"app.js":     {Data: []byte("console.log('ok')")},
	}, "")

	req := httptest.NewRequest(http.MethodGet, "/tickets/123", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for route fallback, got=%d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "<h1>shell</h1>") {
		t.Fatalf("unexpected fallback body: %q", body)
	}
}

func TestHandler_MissingAssetWithExtensionReturns404(t *testing.T) {
	handler := NewHandler(fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><h1>shell</h1>")},
	}, "")

	req := httptest.NewRequest(http.MethodGet, "/missing.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing static asset, got=%d", rec.Code)
	}
}

func TestHandler_ProxiesAPIRequest(t *testing.T) {
	var gotPath string
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	handler := NewHandler(fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><h1>shell</h1>")},
	}, strings.TrimPrefix(upstream.URL, "http://"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview?project=demo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for api proxy, got=%d", rec.Code)
	}
	if gotPath != "/api/v1/overview" {
		t.Fatalf("unexpected proxied path: got=%q", gotPath)
	}
	if gotQuery != "project=demo" {
		t.Fatalf("unexpected proxied query: got=%q", gotQuery)
	}
	if body := rec.Body.String(); body != `{"ok":true}` {
		t.Fatalf("unexpected api response body: %q", body)
	}
}

func TestHandler_APIProxyUnavailableReturnsBadGateway(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	handler := NewHandler(fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><h1>shell</h1>")},
	}, addr)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when api proxy target unavailable, got=%d", rec.Code)
	}
}

func TestEmbeddedStaticFSIncludesIndex(t *testing.T) {
	files, err := StaticFS()
	if err != nil {
		t.Fatalf("StaticFS failed: %v", err)
	}

	f, err := files.Open("index.html")
	if err != nil {
		t.Fatalf("open index.html failed: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read index.html failed: %v", err)
	}
	if !strings.Contains(string(b), "Dalek Web Console") {
		t.Fatalf("embedded index.html should contain title")
	}
}
