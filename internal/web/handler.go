package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

func NewHandler(staticFS fs.FS, internalAPIAddr string) http.Handler {
	apiProxy := newAPIProxy(internalAPIAddr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiProxy != nil && shouldProxyAPI(r.URL.Path) {
			apiProxy.ServeHTTP(w, r)
			return
		}
		target := resolveTargetPath(r.URL.Path)
		if serveFile(staticFS, w, r, target) {
			return
		}
		if hasExtension(target) {
			http.NotFound(w, r)
			return
		}
		if serveFile(staticFS, w, r, "index.html") {
			return
		}
		http.Error(w, "index.html not found", http.StatusInternalServerError)
	})
}

func shouldProxyAPI(requestPath string) bool {
	return requestPath == "/api/v1" || strings.HasPrefix(requestPath, "/api/v1/")
}

func newAPIProxy(internalAPIAddr string) http.Handler {
	addr := strings.TrimSpace(internalAPIAddr)
	if addr == "" {
		return nil
	}
	target := &url.URL{Scheme: "http", Host: addr}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
	}
	return proxy
}

func resolveTargetPath(rawPath string) string {
	p := path.Clean("/" + strings.TrimSpace(rawPath))
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." {
		return "index.html"
	}
	return p
}

func hasExtension(name string) bool {
	base := path.Base(name)
	return strings.Contains(base, ".")
}

func serveFile(files fs.FS, w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := files.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	if readSeeker, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, info.Name(), info.ModTime(), readSeeker)
		return true
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return false
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), bytes.NewReader(data))
	return true
}
