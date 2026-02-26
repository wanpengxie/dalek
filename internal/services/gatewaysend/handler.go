package gatewaysend

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func NewHandler(svc *Service, cfg HandlerConfig) http.HandlerFunc {
	authToken := strings.TrimSpace(cfg.AuthToken)

	return func(w http.ResponseWriter, r *http.Request) {
		if authToken != "" && !isRequestAuthorized(r, authToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if svc == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"code":  1,
				"error": "gateway send service 未初始化",
			})
			return
		}
		if err := svc.Ready(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"code":  1,
				"error": err.Error(),
			})
			return
		}

		var req Request
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code":  1,
				"error": "invalid json",
			})
			return
		}
		req.Project = strings.TrimSpace(req.Project)
		req.Text = strings.TrimSpace(req.Text)
		if req.Project == "" || req.Text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code":  1,
				"error": "project/text 不能为空",
			})
			return
		}

		result, err := svc.Send(r.Context(), req.Project, req.Text)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrBindingNotFound) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{
				"code":    1,
				"error":   err.Error(),
				"project": req.Project,
			})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func extractRequestToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if tok := strings.TrimSpace(r.URL.Query().Get("token")); tok != "" {
		return tok
	}
	if tok := strings.TrimSpace(r.Header.Get("X-Dalek-Token")); tok != "" {
		return tok
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authz) >= len("Bearer ") && strings.EqualFold(authz[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(authz[len("Bearer "):])
	}
	return ""
}

func isRequestAuthorized(r *http.Request, expectedToken string) bool {
	expectedToken = strings.TrimSpace(expectedToken)
	if expectedToken == "" {
		return false
	}
	actualToken := strings.TrimSpace(extractRequestToken(r))
	if actualToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actualToken), []byte(expectedToken)) == 1
}
