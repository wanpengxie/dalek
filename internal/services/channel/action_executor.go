package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

const actionDispatchTicket = "dispatch_ticket"

// ActionResult 表示单个 action 的执行结果。
type ActionResult struct {
	ActionName string         `json:"action_name"`
	Success    bool           `json:"success"`
	Message    string         `json:"message"`
	Data       map[string]any `json:"data,omitempty"`
}

type ActionExecutor struct {
	ticketSvc      TicketActionService
	pmSvc          PMActionService
	workerSvc      WorkerActionService
	taskRequestSvc TaskRequestActionService
}

type TicketActionService interface {
	List(ctx context.Context, includeArchived bool) ([]contracts.Ticket, error)
	GetByID(ctx context.Context, ticketID uint) (*contracts.Ticket, error)
	CreateWithDescriptionAndLabel(ctx context.Context, title, description, label string) (*contracts.Ticket, error)
}

type DispatchTicketResult struct {
	TicketID  uint
	WorkerID  uint
	TaskRunID uint
}

type PMActionService interface {
	StartTicket(ctx context.Context, ticketID uint, baseBranch string) (*contracts.Worker, error)
	DispatchTicket(ctx context.Context, ticketID uint, entryPrompt string) (DispatchTicketResult, error)
	ArchiveTicket(ctx context.Context, ticketID uint) error
	ListMergeItems(ctx context.Context, status contracts.MergeStatus, limit int) ([]contracts.MergeItem, error)
	ApproveMerge(ctx context.Context, mergeItemID uint, approvedBy string) error
	DiscardMerge(ctx context.Context, mergeItemID uint, note string) error
}

type InterruptTicketResult struct {
	TicketID  uint
	WorkerID  uint
	Mode      string
	TaskRunID uint
	LogPath   string
}

type WorkerActionService interface {
	InterruptTicket(ctx context.Context, ticketID uint) (InterruptTicketResult, error)
	StopTicket(ctx context.Context, ticketID uint) error
}

type SubmitTaskRequestActionInput struct {
	TicketID      uint
	RequestID     string
	Prompt        string
	VerifyTarget  string
	ForceRole     string
	RemoteBaseURL string
	RemoteProject string
}

type SubmitTaskRequestActionResult struct {
	Accepted      bool
	Role          string
	RoleSource    string
	RouteReason   string
	RouteMode     string
	RouteTarget   string
	TaskRunID     uint
	RemoteRunID   uint
	RequestID     string
	TicketID      uint
	WorkerID      uint
	VerifyTarget  string
	LinkedRunID   uint
	LinkedSummary string
}

type TaskRequestActionService interface {
	SubmitTaskRequest(ctx context.Context, in SubmitTaskRequestActionInput) (SubmitTaskRequestActionResult, error)
}

func NewActionExecutor(ticketSvc TicketActionService, pmSvc PMActionService, workerSvc WorkerActionService, taskRequestSvc TaskRequestActionService) *ActionExecutor {
	return &ActionExecutor{
		ticketSvc:      ticketSvc,
		pmSvc:          pmSvc,
		workerSvc:      workerSvc,
		taskRequestSvc: taskRequestSvc,
	}
}

func (e *ActionExecutor) Execute(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if ctx == nil {
		return ActionResult{}, fmt.Errorf("action executor: context 不能为空")
	}
	if e == nil {
		return ActionResult{}, fmt.Errorf("action executor 依赖未完成注入")
	}

	action.Normalize()
	name := strings.ToLower(action.Name)
	if name == "" {
		return ActionResult{}, fmt.Errorf("action.name 不能为空")
	}

	switch name {
	case contracts.ActionListTickets:
		return e.executeListTickets(ctx, action)
	case contracts.ActionTicketDetail:
		return e.executeTicketDetail(ctx, action)
	case contracts.ActionCreateTicket:
		return e.executeCreateTicket(ctx, action)
	case contracts.ActionStartTicket:
		return e.executeStartTicket(ctx, action)
	case actionDispatchTicket:
		return e.executeDispatchTicket(ctx, action)
	case contracts.ActionInterruptTicket:
		return e.executeInterruptTicket(ctx, action)
	case contracts.ActionStopTicket:
		return e.executeStopTicket(ctx, action)
	case contracts.ActionArchiveTicket:
		return e.executeArchiveTicket(ctx, action)
	case contracts.ActionListMergeItems:
		return e.executeListMergeItems(ctx, action)
	case contracts.ActionApproveMerge:
		return e.executeApproveMerge(ctx, action)
	case contracts.ActionRejectMerge:
		return e.executeRejectMerge(ctx, action)
	case contracts.ActionSubmitTaskRequest:
		return e.executeSubmitTaskRequest(ctx, action)
	default:
		return ActionResult{}, fmt.Errorf("不支持的 action: %s", name)
	}
}

func (e *ActionExecutor) executeListTickets(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.ticketSvc == nil {
		return ActionResult{}, fmt.Errorf("list_tickets 依赖未注入")
	}
	includeArchived := actionArgBool(action.Args, false, "include_archived", "includeArchived")
	limit := actionArgInt(action.Args, 20, 1, 200, "limit")
	tickets, err := e.ticketSvc.List(ctx, includeArchived)
	if err != nil {
		return ActionResult{}, err
	}

	total := len(tickets)
	if total > limit {
		tickets = tickets[:limit]
	}
	views := make([]map[string]any, 0, len(tickets))
	for _, tk := range tickets {
		views = append(views, map[string]any{
			"id":       tk.ID,
			"title":    tk.Title,
			"label":    tk.Label,
			"status":   tk.WorkflowStatus,
			"priority": tk.Priority,
		})
	}
	msg := fmt.Sprintf("ticket 共 %d 条，返回 %d 条。", total, len(views))
	return ActionResult{
		ActionName: contracts.ActionListTickets,
		Success:    true,
		Message:    msg,
		Data: map[string]any{
			"total":   total,
			"tickets": views,
		},
	}, nil
}

func (e *ActionExecutor) executeTicketDetail(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.ticketSvc == nil {
		return ActionResult{}, fmt.Errorf("ticket_detail 依赖未注入")
	}
	ticketID, err := actionArgUintRequired(action.Args, "ticket_id", "ticketId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	tk, err := e.ticketSvc.GetByID(ctx, ticketID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ActionResult{}, fmt.Errorf("ticket 不存在: t%d", ticketID)
		}
		return ActionResult{}, err
	}
	if strings.TrimSpace(tk.Label) == "" {
		msg := fmt.Sprintf("t%d %s（状态=%s，优先级=%s）", tk.ID, tk.Title, tk.WorkflowStatus, actionPriorityLabel(tk.Priority))
		return ActionResult{
			ActionName: contracts.ActionTicketDetail,
			Success:    true,
			Message:    msg,
			Data: map[string]any{
				"ticket": map[string]any{
					"id":          tk.ID,
					"title":       tk.Title,
					"description": tk.Description,
					"label":       tk.Label,
					"status":      tk.WorkflowStatus,
					"priority":    tk.Priority,
				},
			},
		}, nil
	}
	msg := fmt.Sprintf("t%d %s（标签=%s，状态=%s，优先级=%s）", tk.ID, tk.Title, tk.Label, tk.WorkflowStatus, actionPriorityLabel(tk.Priority))
	return ActionResult{
		ActionName: contracts.ActionTicketDetail,
		Success:    true,
		Message:    msg,
		Data: map[string]any{
			"ticket": map[string]any{
				"id":          tk.ID,
				"title":       tk.Title,
				"description": tk.Description,
				"label":       tk.Label,
				"status":      tk.WorkflowStatus,
				"priority":    tk.Priority,
			},
		},
	}, nil
}

func (e *ActionExecutor) executeCreateTicket(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.ticketSvc == nil {
		return ActionResult{}, fmt.Errorf("create_ticket 依赖未注入")
	}
	title := actionArgString(action.Args, "title", "name")
	if title == "" {
		return ActionResult{}, fmt.Errorf("create_ticket 缺少 title")
	}
	description := actionArgString(action.Args, "description", "body", "detail")
	if description == "" {
		description = title
	}
	label := actionArgString(action.Args, "label", "tag")

	tk, err := e.ticketSvc.CreateWithDescriptionAndLabel(ctx, title, description, label)
	if err != nil {
		return ActionResult{}, err
	}
	msg := fmt.Sprintf("已创建 t%d：%s", tk.ID, tk.Title)
	if strings.TrimSpace(tk.Label) != "" {
		msg = fmt.Sprintf("%s（标签=%s）", msg, tk.Label)
	}
	return ActionResult{
		ActionName: contracts.ActionCreateTicket,
		Success:    true,
		Message:    msg,
		Data: map[string]any{
			"ticket": map[string]any{
				"id":       tk.ID,
				"title":    tk.Title,
				"label":    tk.Label,
				"status":   tk.WorkflowStatus,
				"priority": tk.Priority,
			},
		},
	}, nil
}

func (e *ActionExecutor) executeStartTicket(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.pmSvc == nil {
		return ActionResult{}, fmt.Errorf("start_ticket 依赖未注入")
	}
	ticketID, err := actionArgUintRequired(action.Args, "ticket_id", "ticketId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	baseBranch := actionArgString(action.Args, "base_branch", "baseBranch", "base")
	worker, err := e.pmSvc.StartTicket(ctx, ticketID, baseBranch)
	if err != nil {
		return ActionResult{}, err
	}
	if worker == nil {
		return ActionResult{}, fmt.Errorf("start_ticket 执行失败：未返回 worker")
	}
	msg := fmt.Sprintf("已启动 t%d，对应 w%d（status=%s）。", ticketID, worker.ID, worker.Status)
	return ActionResult{
		ActionName: contracts.ActionStartTicket,
		Success:    true,
		Message:    msg,
		Data: map[string]any{
			"worker": map[string]any{
				"id":        worker.ID,
				"ticket_id": worker.TicketID,
				"status":    worker.Status,
				"log_path":  worker.LogPath,
			},
		},
	}, nil
}

func (e *ActionExecutor) executeDispatchTicket(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.pmSvc == nil {
		return ActionResult{}, fmt.Errorf("dispatch_ticket 依赖未注入")
	}
	ticketID, err := actionArgUintRequired(action.Args, "ticket_id", "ticketId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	entryPrompt := actionArgString(action.Args, "entry_prompt", "entryPrompt", "prompt")
	res, err := e.pmSvc.DispatchTicket(ctx, ticketID, entryPrompt)
	if err != nil {
		return ActionResult{}, err
	}
	msg := fmt.Sprintf("已派发 t%d -> w%d。", res.TicketID, res.WorkerID)
	return ActionResult{
		ActionName: actionDispatchTicket,
		Success:    true,
		Message:    msg,
		Data: map[string]any{
			"dispatch": map[string]any{
				"ticket_id":   res.TicketID,
				"worker_id":   res.WorkerID,
				"task_run_id": res.TaskRunID,
			},
		},
	}, nil
}

func (e *ActionExecutor) executeInterruptTicket(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.workerSvc == nil {
		return ActionResult{}, fmt.Errorf("interrupt_ticket 依赖未注入")
	}
	ticketID, err := actionArgUintRequired(action.Args, "ticket_id", "ticketId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	res, err := e.workerSvc.InterruptTicket(ctx, ticketID)
	if err != nil {
		return ActionResult{}, err
	}
	msg := fmt.Sprintf("已向 t%d（w%d）发送中断。", res.TicketID, res.WorkerID)
	return ActionResult{
		ActionName: contracts.ActionInterruptTicket,
		Success:    true,
		Message:    msg,
		Data: map[string]any{
			"interrupt": map[string]any{
				"ticket_id":   res.TicketID,
				"worker_id":   res.WorkerID,
				"mode":        res.Mode,
				"task_run_id": res.TaskRunID,
				"log_path":    res.LogPath,
			},
		},
	}, nil
}

func (e *ActionExecutor) executeStopTicket(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.workerSvc == nil {
		return ActionResult{}, fmt.Errorf("stop_ticket 依赖未注入")
	}
	ticketID, err := actionArgUintRequired(action.Args, "ticket_id", "ticketId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	if err := e.workerSvc.StopTicket(ctx, ticketID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{
		ActionName: contracts.ActionStopTicket,
		Success:    true,
		Message:    fmt.Sprintf("已停止 t%d 的 worker。", ticketID),
	}, nil
}

func (e *ActionExecutor) executeArchiveTicket(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.pmSvc == nil {
		return ActionResult{}, fmt.Errorf("archive_ticket 依赖未注入")
	}
	ticketID, err := actionArgUintRequired(action.Args, "ticket_id", "ticketId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	if err := e.pmSvc.ArchiveTicket(ctx, ticketID); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{
		ActionName: contracts.ActionArchiveTicket,
		Success:    true,
		Message:    fmt.Sprintf("已归档 t%d。", ticketID),
	}, nil
}

func (e *ActionExecutor) executeListMergeItems(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.pmSvc == nil {
		return ActionResult{}, fmt.Errorf("list_merge_items 依赖未注入")
	}
	limit := actionArgInt(action.Args, 20, 1, 200, "limit")
	statusRaw := strings.ToLower(actionArgString(action.Args, "status"))
	status := contracts.MergeStatus(statusRaw)
	items, err := e.pmSvc.ListMergeItems(ctx, status, limit)
	if err != nil {
		return ActionResult{}, err
	}
	views := make([]map[string]any, 0, len(items))
	for _, it := range items {
		views = append(views, map[string]any{
			"id":        it.ID,
			"ticket_id": it.TicketID,
			"worker_id": it.WorkerID,
			"status":    it.Status,
			"branch":    it.Branch,
		})
	}
	msg := fmt.Sprintf("merge item 共 %d 条。", len(items))
	return ActionResult{
		ActionName: contracts.ActionListMergeItems,
		Success:    true,
		Message:    msg,
		Data: map[string]any{
			"merge_items": views,
		},
	}, nil
}

func (e *ActionExecutor) executeApproveMerge(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.pmSvc == nil {
		return ActionResult{}, fmt.Errorf("approve_merge 依赖未注入")
	}
	mergeItemID, err := actionArgUintRequired(action.Args, "merge_item_id", "mergeItemId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	approvedBy := actionArgString(action.Args, "approved_by", "approvedBy", "decider")
	if err := e.pmSvc.ApproveMerge(ctx, mergeItemID, approvedBy); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{
		ActionName: contracts.ActionApproveMerge,
		Success:    true,
		Message:    fmt.Sprintf("已批准 merge#%d。", mergeItemID),
	}, nil
}

func (e *ActionExecutor) executeRejectMerge(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.pmSvc == nil {
		return ActionResult{}, fmt.Errorf("reject_merge 依赖未注入")
	}
	mergeItemID, err := actionArgUintRequired(action.Args, "merge_item_id", "mergeItemId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	note := actionArgString(action.Args, "note", "reason")
	if err := e.pmSvc.DiscardMerge(ctx, mergeItemID, note); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{
		ActionName: contracts.ActionRejectMerge,
		Success:    true,
		Message:    fmt.Sprintf("已拒绝 merge#%d。", mergeItemID),
	}, nil
}

func (e *ActionExecutor) executeSubmitTaskRequest(ctx context.Context, action contracts.TurnAction) (ActionResult, error) {
	if e.taskRequestSvc == nil {
		return ActionResult{}, fmt.Errorf("submit_task_request 依赖未注入")
	}
	ticketID, err := actionArgUintRequired(action.Args, "ticket_id", "ticketId", "id")
	if err != nil {
		return ActionResult{}, err
	}
	res, err := e.taskRequestSvc.SubmitTaskRequest(ctx, SubmitTaskRequestActionInput{
		TicketID:      ticketID,
		RequestID:     actionArgString(action.Args, "request_id", "requestId"),
		Prompt:        actionArgString(action.Args, "prompt", "entry_prompt", "entryPrompt"),
		VerifyTarget:  actionArgString(action.Args, "verify_target", "verifyTarget"),
		ForceRole:     strings.ToLower(strings.TrimSpace(actionArgString(action.Args, "role"))),
		RemoteBaseURL: actionArgString(action.Args, "remote_base_url", "remoteBaseURL"),
		RemoteProject: actionArgString(action.Args, "remote_project", "remoteProject"),
	})
	if err != nil {
		return ActionResult{}, err
	}
	msg := formatTaskRequestAcceptedMessage(res)
	return ActionResult{
		ActionName: contracts.ActionSubmitTaskRequest,
		Success:    res.Accepted,
		Message:    msg,
		Data: map[string]any{
			"task_request": map[string]any{
				"accepted":       res.Accepted,
				"role":           res.Role,
				"role_source":    res.RoleSource,
				"route_reason":   res.RouteReason,
				"route_mode":     res.RouteMode,
				"route_target":   res.RouteTarget,
				"task_run_id":    res.TaskRunID,
				"remote_run_id":  res.RemoteRunID,
				"request_id":     res.RequestID,
				"ticket_id":      res.TicketID,
				"worker_id":      res.WorkerID,
				"verify_target":  res.VerifyTarget,
				"linked_run_id":  res.LinkedRunID,
				"linked_summary": res.LinkedSummary,
			},
		},
	}, nil
}

func formatTaskRequestAcceptedMessage(res SubmitTaskRequestActionResult) string {
	lines := []string{
		fmt.Sprintf("任务已受理：t%d", res.TicketID),
		fmt.Sprintf("- role: %s", firstNonEmpty(strings.TrimSpace(res.Role), "-")),
		fmt.Sprintf("- role_source: %s", firstNonEmpty(strings.TrimSpace(res.RoleSource), "-")),
		fmt.Sprintf("- route: %s", firstNonEmpty(strings.TrimSpace(res.RouteTarget), strings.TrimSpace(res.RouteMode), "-")),
	}
	if reason := strings.TrimSpace(res.RouteReason); reason != "" {
		lines = append(lines, fmt.Sprintf("- route_reason: %s", reason))
	}
	lines = append(lines, fmt.Sprintf("- task_run_id: %d", res.TaskRunID))
	if rid := strings.TrimSpace(res.RequestID); rid != "" {
		lines = append(lines, fmt.Sprintf("- request_id: %s", rid))
	}
	if res.RemoteRunID != 0 {
		lines = append(lines, fmt.Sprintf("- remote_run_id: %d", res.RemoteRunID))
	}
	if target := strings.TrimSpace(res.VerifyTarget); target != "" {
		lines = append(lines, fmt.Sprintf("- verify_target: %s", target))
	}
	if summary := strings.TrimSpace(res.LinkedSummary); summary != "" {
		lines = append(lines, fmt.Sprintf("- linked_run_context: %s", summary))
	}
	lines = append(lines,
		fmt.Sprintf("- show: dalek task show --id %d", res.TaskRunID),
		fmt.Sprintf("- events: dalek task events --id %d", res.TaskRunID),
	)
	return strings.Join(lines, "\n")
}

func actionArgString(args map[string]any, keys ...string) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if v, ok := args[key]; ok {
			switch x := v.(type) {
			case string:
				if s := x; s != "" {
					return s
				}
			default:
				raw := fmt.Sprint(v)
				if raw != "" && raw != "<nil>" {
					return raw
				}
			}
		}
	}
	return ""
}

func actionPriorityLabel(priority int) string {
	label := contracts.TicketPriorityLabel(priority)
	if label == strconv.Itoa(priority) {
		return label
	}
	return fmt.Sprintf("%s(%d)", label, priority)
}

func actionArgBool(args map[string]any, defaultValue bool, keys ...string) bool {
	if len(args) == 0 {
		return defaultValue
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		v, ok := args[key]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case bool:
			return x
		case string:
			s := strings.ToLower(x)
			if s == "1" || s == "true" || s == "yes" || s == "y" {
				return true
			}
			if s == "0" || s == "false" || s == "no" || s == "n" {
				return false
			}
		case float64:
			return x != 0
		case int:
			return x != 0
		}
	}
	return defaultValue
}

func actionArgInt(args map[string]any, defaultValue, minValue, maxValue int, keys ...string) int {
	if len(args) == 0 {
		return defaultValue
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		v, ok := args[key]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case int:
			return clampInt(x, minValue, maxValue)
		case int64:
			return clampInt(int(x), minValue, maxValue)
		case float64:
			return clampInt(int(x), minValue, maxValue)
		case string:
			if n, err := strconv.Atoi(x); err == nil {
				return clampInt(n, minValue, maxValue)
			}
		}
	}
	return defaultValue
}

func actionArgUintRequired(args map[string]any, keys ...string) (uint, error) {
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		v, ok := args[key]
		if !ok {
			continue
		}
		if out, ok := anyToUint(v); ok && out > 0 {
			return out, nil
		}
	}
	return 0, fmt.Errorf("action 参数缺少有效 ID（%s）", strings.Join(keys, "/"))
}

func anyToUint(v any) (uint, bool) {
	switch x := v.(type) {
	case uint:
		return x, true
	case uint64:
		return uint(x), true
	case int:
		if x < 0 {
			return 0, false
		}
		return uint(x), true
	case int64:
		if x < 0 {
			return 0, false
		}
		return uint(x), true
	case float64:
		if x < 0 {
			return 0, false
		}
		return uint(x), true
	case json.Number:
		if i, err := x.Int64(); err == nil && i >= 0 {
			return uint(i), true
		}
		return 0, false
	case string:
		s := strings.ToLower(x)
		if s == "" {
			return 0, false
		}
		for len(s) > 0 && (s[0] < '0' || s[0] > '9') {
			s = s[1:]
		}
		if s == "" {
			return 0, false
		}
		u, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, false
		}
		return uint(u), true
	default:
		return 0, false
	}
}

func clampInt(v, minValue, maxValue int) int {
	if maxValue > 0 && v > maxValue {
		return maxValue
	}
	if minValue > 0 && v < minValue {
		return minValue
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type actionExecuteResult struct {
	Action  contracts.TurnAction
	Success bool
	Message string
}

func (s *Service) executeAction(ctx context.Context, action contracts.TurnAction) actionExecuteResult {
	action.Normalize()
	result := actionExecuteResult{Action: action}
	if s == nil || s.p == nil {
		result.Message = "channel service 缺少 project 上下文"
		return result
	}
	executor := s.actionExecutor()
	if executor == nil {
		result.Message = "channel service action executor 初始化失败"
		return result
	}
	execRes, err := executor.Execute(ctx, action)
	if err != nil {
		result.Success = false
		result.Message = err.Error()
		return result
	}
	result.Success = execRes.Success
	result.Message = execRes.Message
	if result.Message == "" {
		if result.Success {
			result.Message = "操作执行成功"
		} else {
			result.Message = "操作执行失败"
		}
	}
	return result
}

func renderActionExecutionSummary(results []actionExecuteResult) string {
	if len(results) == 0 {
		return ""
	}
	lines := make([]string, 0, len(results)+1)
	lines = append(lines, "Action 执行结果：")
	for _, res := range results {
		prefix := "[OK]"
		if !res.Success {
			prefix = "[FAIL]"
		}
		msg := res.Message
		desc := describePendingAction(res.Action)
		if msg == "" {
			lines = append(lines, fmt.Sprintf("- %s %s", prefix, desc))
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s %s -> %s", prefix, desc, msg))
	}
	return strings.Join(lines, "\n")
}

func describePendingAction(action contracts.TurnAction) string {
	action.Normalize()
	name := action.Name
	if name == "" {
		name = "unknown_action"
	}
	if len(action.Args) == 0 {
		return name
	}
	parts := make([]string, 0, len(action.Args))
	for k, v := range action.Args {
		key := k
		if key == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", key, v))
	}
	if len(parts) == 0 {
		return name
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}
