package agentcli

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func Run(ctx context.Context, backend Backend, req RunRequest) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	command := strings.TrimSpace(backend.Command)
	if command == "" {
		return Result{}, fmt.Errorf("agent backend command 为空")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Result{}, fmt.Errorf("用户消息为空，无法调用 project manager agent")
	}

	args, stdin, outputMode := buildArgsAndInput(backend, req)

	cmd := exec.CommandContext(ctx, command, args...)
	if strings.TrimSpace(req.WorkDir) != "" {
		cmd.Dir = strings.TrimSpace(req.WorkDir)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()
	stdout := strings.TrimSpace(stdoutBuf.String())
	stderr := strings.TrimSpace(stderrBuf.String())

	result := Result{
		Command:    strings.TrimSpace(command),
		Stdout:     stdout,
		Stderr:     stderr,
		OutputMode: outputMode,
	}

	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			detail := buildProcessErrorDetail(stdout, stderr, ctxErr.Error())
			return result, fmt.Errorf("project manager agent 调用失败: %s", detail)
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return result, fmt.Errorf("project manager agent 调用失败: %s", buildProcessErrorDetail(stdout, stderr, fmt.Sprintf("exit code=%d", exitErr.ExitCode())))
		}
		return result, fmt.Errorf("project manager agent 调用失败: %s", buildProcessErrorDetail(stdout, stderr, runErr.Error()))
	}

	text, sessionID, events := parseOutput(stdout, outputMode, backend)
	result.Text = strings.TrimSpace(text)
	result.SessionID = strings.TrimSpace(sessionID)
	result.Events = events
	return result, nil
}

func buildArgsAndInput(backend Backend, req RunRequest) (args []string, stdin string, outputMode OutputMode) {
	useResume := strings.TrimSpace(req.SessionID) != "" && len(backend.ResumeArgs) > 0

	baseArgs := backend.Args
	if useResume {
		baseArgs = replaceSessionPlaceholder(backend.ResumeArgs, req.SessionID)
	}
	args = append(args, baseArgs...)

	model := normalizeModel(strings.TrimSpace(req.Model), backend.ModelAliases)
	if !useResume && strings.TrimSpace(backend.ModelArg) != "" && model != "" {
		args = append(args, strings.TrimSpace(backend.ModelArg), model)
	}

	sessionID := resolveSessionIDToSend(backend.SessionMode, strings.TrimSpace(req.SessionID))
	if !useResume && sessionID != "" {
		if len(backend.SessionArgs) > 0 {
			args = append(args, replaceSessionPlaceholder(backend.SessionArgs, sessionID)...)
		} else if strings.TrimSpace(backend.SessionArg) != "" {
			args = append(args, strings.TrimSpace(backend.SessionArg), sessionID)
		}
	}

	prompt := strings.TrimSpace(req.Prompt)
	useStdin := backend.Input == InputStdin
	if !useStdin && backend.MaxPromptArgChars > 0 && len(prompt) > backend.MaxPromptArgChars {
		useStdin = true
	}
	if useStdin {
		stdin = prompt
	} else {
		args = append(args, prompt)
	}

	outputMode = backend.Output
	if outputMode == "" {
		outputMode = OutputText
	}
	if useResume && backend.ResumeOutput != "" {
		outputMode = backend.ResumeOutput
	}
	return args, stdin, outputMode
}

func normalizeModel(model string, aliases map[string]string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if aliases == nil {
		return model
	}
	if v := strings.TrimSpace(aliases[model]); v != "" {
		return v
	}
	lower := strings.ToLower(model)
	if v := strings.TrimSpace(aliases[lower]); v != "" {
		return v
	}
	return model
}

func resolveSessionIDToSend(mode SessionMode, current string) string {
	current = strings.TrimSpace(current)
	switch mode {
	case SessionNone:
		return ""
	case SessionExisting:
		return current
	case SessionAlways:
		if current != "" {
			return current
		}
		return randomUUIDv4()
	default:
		return current
	}
}

func replaceSessionPlaceholder(args []string, sessionID string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, strings.ReplaceAll(arg, "{sessionId}", strings.TrimSpace(sessionID)))
	}
	return out
}

func randomUUIDv4() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	// RFC 4122 v4: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func buildProcessErrorDetail(stdout, stderr, fallback string) string {
	parts := make([]string, 0, 3)
	if s := strings.TrimSpace(stderr); s != "" {
		parts = append(parts, "stderr="+truncateForError(s, 4000))
	}
	if s := strings.TrimSpace(stdout); s != "" {
		parts = append(parts, "stdout="+truncateForError(s, 4000))
	}
	if s := strings.TrimSpace(fallback); s != "" {
		parts = append(parts, "cause="+truncateForError(s, 800))
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, " | ")
}

func truncateForError(s string, max int) string {
	if max <= 0 {
		return strings.TrimSpace(s)
	}
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max])) + "...(truncated)"
}
