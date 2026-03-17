package channel

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"dalek/internal/contracts"
)

func (s *Service) tryHandleDirectTaskRequest(ctx context.Context, tctx *turnContext) (string, bool) {
	if tctx == nil {
		return "", false
	}
	action, usage, handled := parseDirectTaskRequestAction(strings.TrimSpace(tctx.inbound.ContentText))
	if !handled {
		return "", false
	}
	if usage != "" {
		return usage, true
	}
	result := s.executeAction(ctx, action)
	reply := strings.TrimSpace(result.Message)
	if reply == "" {
		if result.Success {
			reply = "task request 已提交"
		} else {
			reply = "task request 执行失败"
		}
	}
	return reply, true
}

func parseDirectTaskRequestAction(text string) (contracts.TurnAction, string, bool) {
	fields, err := splitDirectCommandLine(strings.TrimSpace(text))
	if err != nil {
		return contracts.TurnAction{}, "命令解析失败：" + err.Error(), true
	}
	if len(fields) < 2 {
		return contracts.TurnAction{}, "", false
	}
	head := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fields[0])), "/")
	verb := strings.ToLower(strings.TrimSpace(fields[1]))
	if verb != "request" {
		return contracts.TurnAction{}, "", false
	}
	if head != "task" && head != "run" {
		return contracts.TurnAction{}, "", false
	}

	args := map[string]any{}
	if head == "run" {
		args["role"] = "run"
	}
	for i := 2; i < len(fields); i++ {
		token := strings.TrimSpace(fields[i])
		if token == "" {
			continue
		}
		key := token
		value := ""
		if idx := strings.Index(token, "="); idx >= 0 {
			key = strings.TrimSpace(token[:idx])
			value = token[idx+1:]
		} else {
			if i+1 >= len(fields) {
				return contracts.TurnAction{}, directTaskRequestUsage(), true
			}
			value = fields[i+1]
			i++
		}
		switch key {
		case "--ticket":
			id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil || id == 0 {
				return contracts.TurnAction{}, "命令错误：--ticket 必须是正整数", true
			}
			args["ticket_id"] = uint(id)
		case "--request-id":
			args["request_id"] = strings.TrimSpace(value)
		case "--prompt":
			args["prompt"] = strings.TrimSpace(value)
		case "--verify-target":
			args["verify_target"] = strings.TrimSpace(value)
		case "--role":
			args["role"] = strings.ToLower(strings.TrimSpace(value))
		case "--remote-base-url":
			args["remote_base_url"] = strings.TrimSpace(value)
		case "--remote-project":
			args["remote_project"] = strings.TrimSpace(value)
		default:
			return contracts.TurnAction{}, fmt.Sprintf("未知参数：%s\n%s", key, directTaskRequestUsage()), true
		}
	}
	if _, ok := args["ticket_id"]; !ok {
		return contracts.TurnAction{}, "命令错误：缺少 --ticket\n" + directTaskRequestUsage(), true
	}
	prompt := ""
	if v, ok := args["prompt"]; ok {
		prompt = strings.TrimSpace(fmt.Sprint(v))
	}
	verifyTarget := ""
	if v, ok := args["verify_target"]; ok {
		verifyTarget = strings.TrimSpace(fmt.Sprint(v))
	}
	if prompt == "" && verifyTarget == "" {
		return contracts.TurnAction{}, "命令错误：--prompt 或 --verify-target 至少提供一个\n" + directTaskRequestUsage(), true
	}
	return contracts.TurnAction{
		Name: contracts.ActionSubmitTaskRequest,
		Args: args,
	}, "", true
}

func splitDirectCommandLine(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil
	}
	var (
		fields   []string
		current  []rune
		inQuote  rune
		escaping bool
	)
	flush := func() {
		if len(current) == 0 {
			return
		}
		fields = append(fields, string(current))
		current = current[:0]
	}
	for _, r := range input {
		switch {
		case escaping:
			current = append(current, r)
			escaping = false
		case r == '\\':
			escaping = true
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
			} else {
				current = append(current, r)
			}
		case r == '"' || r == '\'':
			inQuote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current = append(current, r)
		}
	}
	if escaping || inQuote != 0 {
		return nil, fmt.Errorf("存在未闭合的引号或转义")
	}
	flush()
	return fields, nil
}

func directTaskRequestUsage() string {
	return strings.Join([]string{
		"用法：",
		"/task request --ticket 12 --prompt \"继续开发\"",
		"/run request --ticket 12 --verify-target test",
	}, "\n")
}
