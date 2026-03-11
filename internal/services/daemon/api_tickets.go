package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

const (
	internalTicketsRoot   = "/api/v1/tickets"
	internalTicketsPrefix = "/api/v1/tickets/"
)

var allowedTicketStatusFilter = map[contracts.TicketWorkflowStatus]struct{}{
	contracts.TicketBacklog:  {},
	contracts.TicketQueued:   {},
	contracts.TicketActive:   {},
	contracts.TicketBlocked:  {},
	contracts.TicketDone:     {},
	contracts.TicketArchived: {},
}

func (s *InternalAPI) handleTickets(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == internalTicketsRoot || r.URL.Path == internalTicketsPrefix {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
			return
		}
		s.handleTicketList(w, r)
		return
	}

	ticketID, tail, ok := parseTicketRoute(r.URL.Path)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "路径不存在")
		return
	}
	switch {
	case r.Method == http.MethodGet && tail == "":
		s.handleTicketShow(w, r, ticketID)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method/path 不支持")
	}
}

func (s *InternalAPI) handleTicketList(w http.ResponseWriter, r *http.Request) {
	projectName, err := requiredProjectName(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	status, hasStatus, err := parseTicketStatusFilter(r.URL.Query().Get("status"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	views, err := s.host.ListTicketViews(r.Context(), projectName)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	items := make([]map[string]any, 0, len(views))
	for _, view := range views {
		if hasStatus {
			ticketStatus := contracts.CanonicalTicketWorkflowStatus(view.Ticket.WorkflowStatus)
			if ticketStatus != status {
				continue
			}
		}
		items = append(items, ticketViewToJSON(view))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": projectName,
		"tickets": items,
	})
}

func (s *InternalAPI) handleTicketShow(w http.ResponseWriter, r *http.Request, ticketID uint) {
	projectName, err := requiredProjectName(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	view, err := s.host.GetTicketViewByID(r.Context(), projectName, ticketID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "ticket 不存在")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "query_failed", err.Error())
		return
	}
	if view == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "ticket 不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": projectName,
		"ticket":  ticketViewToJSON(*view),
	})
}

func parseTicketRoute(path string) (uint, string, bool) {
	if !strings.HasPrefix(path, internalTicketsPrefix) {
		return 0, "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(path, internalTicketsPrefix))
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return 0, "", false
	}
	parts := strings.Split(rest, "/")
	ticketID64, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || ticketID64 == 0 {
		return 0, "", false
	}
	tail := ""
	if len(parts) >= 2 {
		tail = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		return 0, "", false
	}
	return uint(ticketID64), tail, true
}

func requiredProjectName(r *http.Request) (string, error) {
	if r == nil || r.URL == nil {
		return "", fmt.Errorf("request 为空")
	}
	projectName := strings.TrimSpace(r.URL.Query().Get("project"))
	if projectName == "" {
		return "", fmt.Errorf("project 不能为空")
	}
	return projectName, nil
}

func parseTicketStatusFilter(raw string) (contracts.TicketWorkflowStatus, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	status := contracts.CanonicalTicketWorkflowStatus(contracts.TicketWorkflowStatus(raw))
	if _, ok := allowedTicketStatusFilter[status]; !ok {
		return "", false, fmt.Errorf("status 参数非法: %s", raw)
	}
	return status, true, nil
}

func ticketViewToJSON(v TicketView) map[string]any {
	capability := map[string]any{
		"can_start":     v.Capability.CanStart,
		"can_queue_run": v.Capability.CanQueueRun,
		"can_dispatch":  v.Capability.CanDispatch,
		"can_attach":    v.Capability.CanAttach,
		"can_stop":      v.Capability.CanStop,
		"can_archive":   v.Capability.CanArchive,
		"reason":        strings.TrimSpace(v.Capability.Reason),
	}
	return map[string]any{
		"ticket":               ticketToJSON(v.Ticket),
		"latest_worker":        workerToJSON(v.LatestWorker),
		"session_alive":        v.SessionAlive,
		"session_probe_failed": v.SessionProbeFailed,
		"derived_status":       contracts.CanonicalTicketWorkflowStatus(v.DerivedStatus),
		"capability":           capability,
		"task_run_id":          v.TaskRunID,
		"runtime_health_state": strings.TrimSpace(string(v.RuntimeHealthState)),
		"runtime_needs_user":   v.RuntimeNeedsUser,
		"runtime_summary":      strings.TrimSpace(v.RuntimeSummary),
		"runtime_observed_at":  v.RuntimeObservedAt,
		"semantic_phase":       strings.TrimSpace(string(v.SemanticPhase)),
		"semantic_next_action": strings.TrimSpace(v.SemanticNextAction),
		"semantic_summary":     strings.TrimSpace(v.SemanticSummary),
		"semantic_reported_at": v.SemanticReportedAt,
		"last_event_type":      strings.TrimSpace(v.LastEventType),
		"last_event_note":      strings.TrimSpace(v.LastEventNote),
		"last_event_at":        v.LastEventAt,
	}
}

func ticketToJSON(t contracts.Ticket) map[string]any {
	return map[string]any{
		"id":              t.ID,
		"title":           strings.TrimSpace(t.Title),
		"description":     strings.TrimSpace(t.Description),
		"label":           strings.TrimSpace(t.Label),
		"workflow_status": contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus),
		"priority":        t.Priority,
		"created_at":      t.CreatedAt,
		"updated_at":      t.UpdatedAt,
	}
}

func workerToJSON(w *contracts.Worker) map[string]any {
	if w == nil {
		return nil
	}
	return map[string]any{
		"id":         w.ID,
		"ticket_id":  w.TicketID,
		"status":     strings.TrimSpace(string(w.Status)),
		"branch":     strings.TrimSpace(w.Branch),
		"log_path":   strings.TrimSpace(w.LogPath),
		"last_error": strings.TrimSpace(w.LastError),
		"started_at": w.StartedAt,
		"stopped_at": w.StoppedAt,
		"created_at": w.CreatedAt,
		"updated_at": w.UpdatedAt,
	}
}
