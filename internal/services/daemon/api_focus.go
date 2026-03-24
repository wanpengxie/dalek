package daemon

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

const (
	internalFocusRoot   = "/api/v1/focus"
	internalFocusPrefix = "/api/v1/focus/"
)

type focusStartPayload struct {
	Project        string `json:"project"`
	Mode           string `json:"mode"`
	ScopeTicketIDs []uint `json:"scope_ticket_ids"`
	AgentBudget    int    `json:"agent_budget"`
	RequestID      string `json:"request_id"`
	MaxPMRuns      int    `json:"max_pm_runs,omitempty"`
}

type focusAddTicketsPayload struct {
	Project   string `json:"project"`
	TicketIDs []uint `json:"ticket_ids"`
	RequestID string `json:"request_id"`
}

type focusActionPayload struct {
	RequestID string `json:"request_id"`
}

func (s *InternalAPI) handleFocus(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == internalFocusRoot:
		switch r.Method {
		case http.MethodGet:
			s.handleFocusActive(w, r)
		default:
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method/path 不支持")
		}
		return
	case r.URL.Path == internalFocusRoot+"/start":
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
			return
		}
		s.handleFocusStart(w, r)
		return
	case r.URL.Path == internalFocusRoot+"/add":
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 POST")
			return
		}
		s.handleFocusAddTickets(w, r)
		return
	}

	focusID, tail, ok := parseFocusRoute(r.URL.Path)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "路径不存在")
		return
	}
	switch {
	case r.Method == http.MethodGet && tail == "":
		s.handleFocusShow(w, r, focusID)
	case r.Method == http.MethodGet && tail == "poll":
		s.handleFocusPoll(w, r, focusID)
	case r.Method == http.MethodPost && tail == "stop":
		s.handleFocusStop(w, r, focusID)
	case r.Method == http.MethodPost && tail == "cancel":
		s.handleFocusCancel(w, r, focusID)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method/path 不支持")
	}
}

func (s *InternalAPI) handleFocusStart(w http.ResponseWriter, r *http.Request) {
	var payload focusStartPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.host.FocusStart(r.Context(), strings.TrimSpace(payload.Project), contracts.FocusStartInput{
		Mode:           strings.TrimSpace(payload.Mode),
		ScopeTicketIDs: append([]uint(nil), payload.ScopeTicketIDs...),
		AgentBudget:    payload.AgentBudget,
		RequestID:      strings.TrimSpace(payload.RequestID),
		MaxPMRuns:      payload.MaxPMRuns,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "start_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *InternalAPI) handleFocusActive(w http.ResponseWriter, r *http.Request) {
	projectName, err := requiredProjectName(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !parseBoolQuery(r.URL.Query().Get("active")) {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "仅支持 active=true 查询")
		return
	}
	view, err := s.host.FocusGet(r.Context(), projectName, 0)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "focus 不存在")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": projectName,
		"focus":   view,
	})
}

func (s *InternalAPI) handleFocusShow(w http.ResponseWriter, r *http.Request, focusID uint) {
	projectName, err := requiredProjectName(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	view, err := s.host.FocusGet(r.Context(), projectName, focusID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "focus 不存在")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": projectName,
		"focus":   view,
	})
}

func (s *InternalAPI) handleFocusPoll(w http.ResponseWriter, r *http.Request, focusID uint) {
	projectName, err := requiredProjectName(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	since, err := parseUintQuery(r.URL.Query().Get("since_event_id"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	out, err := s.host.FocusPoll(r.Context(), projectName, focusID, since)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "focus 不存在")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *InternalAPI) handleFocusStop(w http.ResponseWriter, r *http.Request, focusID uint) {
	projectName, err := requiredProjectName(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var payload focusActionPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.host.FocusStop(r.Context(), projectName, focusID, strings.TrimSpace(payload.RequestID)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "focus 不存在")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "stop_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"focus_id": focusID,
	})
}

func (s *InternalAPI) handleFocusCancel(w http.ResponseWriter, r *http.Request, focusID uint) {
	projectName, err := requiredProjectName(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var payload focusActionPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.host.FocusCancel(r.Context(), projectName, focusID, strings.TrimSpace(payload.RequestID)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "focus 不存在")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "cancel_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"focus_id": focusID,
	})
}

func (s *InternalAPI) handleFocusAddTickets(w http.ResponseWriter, r *http.Request) {
	var payload focusAddTicketsPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.host.FocusAddTickets(r.Context(), strings.TrimSpace(payload.Project), contracts.FocusAddTicketsInput{
		TicketIDs: append([]uint(nil), payload.TicketIDs...),
		RequestID: strings.TrimSpace(payload.RequestID),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "add_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func parseFocusRoute(path string) (uint, string, bool) {
	if !strings.HasPrefix(path, internalFocusPrefix) {
		return 0, "", false
	}
	rest := strings.Trim(strings.TrimPrefix(path, internalFocusPrefix), "/")
	if rest == "" {
		return 0, "", false
	}
	parts := strings.Split(rest, "/")
	focusID64, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || focusID64 == 0 {
		return 0, "", false
	}
	tail := ""
	if len(parts) >= 2 {
		tail = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		return 0, "", false
	}
	return uint(focusID64), tail, true
}

func parseUintQuery(raw string) (uint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(v), nil
}

func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}
