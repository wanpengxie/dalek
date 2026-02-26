package daemon

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"dalek/internal/services/core"
)

// RecoverMiddleware wraps handlers to prevent panic from crashing daemon process.
func RecoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	base := core.EnsureLogger(logger)
	return func(next http.Handler) http.Handler {
		if next == nil {
			next = http.NotFoundHandler()
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					method := ""
					path := ""
					remote := ""
					if r != nil {
						method = r.Method
						path = r.URL.Path
						remote = r.RemoteAddr
					}
					base.Error("http handler panic recovered",
						"panic", rec,
						"method", method,
						"path", path,
						"remote_addr", remote,
						"stack", string(debug.Stack()),
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal_server_error"}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
