package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

func NewHandler(staticFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
