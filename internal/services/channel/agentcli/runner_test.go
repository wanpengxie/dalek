package agentcli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRun_TextOutput(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "-p" ]]; then
  echo "reply:${2:-}"
else
  echo "reply:$*"
fi
`
	cmd := writeTestScript(t, script)
	res, err := Run(context.Background(), Backend{
		Command: cmd,
		Args:    []string{"-p"},
		Output:  OutputText,
		Input:   InputArg,
	}, RunRequest{
		WorkDir: t.TempDir(),
		Prompt:  "hello",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if strings.TrimSpace(res.Text) != "reply:hello" {
		t.Fatalf("unexpected text: %q", res.Text)
	}
	if strings.TrimSpace(res.Stdout) != "reply:hello" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
}

func TestRun_JSONOutput(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
cat <<'JSON'
{"session_id":"sess-1","message":{"text":"你好，我是 PM agent"}}
JSON
`
	cmd := writeTestScript(t, script)
	res, err := Run(context.Background(), Backend{
		Command: cmd,
		Output:  OutputJSON,
		Input:   InputArg,
	}, RunRequest{
		Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if strings.TrimSpace(res.Text) != "你好，我是 PM agent" {
		t.Fatalf("unexpected text: %q", res.Text)
	}
	if strings.TrimSpace(res.SessionID) != "sess-1" {
		t.Fatalf("unexpected session id: %q", res.SessionID)
	}
}

func TestRun_JSONLOutput(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
cat <<'JSONL'
{"thread_id":"th-1","type":"message.delta","item":{"type":"message","text":"第一段"}}
{"type":"message","item":{"type":"message","text":"第二段"}}
JSONL
`
	cmd := writeTestScript(t, script)
	res, err := Run(context.Background(), Backend{
		Command: cmd,
		Output:  OutputJSONL,
		Input:   InputArg,
	}, RunRequest{
		Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if strings.TrimSpace(res.SessionID) != "th-1" {
		t.Fatalf("unexpected session id: %q", res.SessionID)
	}
	want := "第一段\n第二段"
	if strings.TrimSpace(res.Text) != want {
		t.Fatalf("unexpected text: %q want=%q", res.Text, want)
	}
	if len(res.Events) != 2 {
		t.Fatalf("unexpected events len: %d", len(res.Events))
	}
}

func TestRun_ExitNonZeroReturnsError(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "boom from agent" >&2
exit 3
`
	cmd := writeTestScript(t, script)
	_, err := Run(context.Background(), Backend{
		Command: cmd,
		Output:  OutputText,
		Input:   InputArg,
	}, RunRequest{
		Prompt: "hello",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom from agent") {
		t.Fatalf("error should include stderr, got: %v", err)
	}
}

func TestRun_ExitNonZeroContainsStdoutAndStderr(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "hello-from-stdout"
echo "boom-from-stderr" >&2
exit 9
`
	cmd := writeTestScript(t, script)
	_, err := Run(context.Background(), Backend{
		Command: cmd,
		Output:  OutputText,
		Input:   InputArg,
	}, RunRequest{
		Prompt: "hello",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stdout=hello-from-stdout") {
		t.Fatalf("error should include stdout detail, got: %v", msg)
	}
	if !strings.Contains(msg, "stderr=boom-from-stderr") {
		t.Fatalf("error should include stderr detail, got: %v", msg)
	}
}

func TestRun_InputStdin(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
input="$(cat)"
echo "stdin:${input}"
`
	cmd := writeTestScript(t, script)
	res, err := Run(context.Background(), Backend{
		Command: cmd,
		Output:  OutputText,
		Input:   InputStdin,
	}, RunRequest{
		Prompt: "from-stdin",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if strings.TrimSpace(res.Text) != "stdin:from-stdin" {
		t.Fatalf("unexpected stdin text: %q", res.Text)
	}
}

func TestRun_TimeoutFromContext(t *testing.T) {
	script := `#!/usr/bin/env bash
set -euo pipefail
sleep 2
echo "done"
`
	cmd := writeTestScript(t, script)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, Backend{
		Command: cmd,
		Output:  OutputText,
		Input:   InputArg,
	}, RunRequest{
		Prompt: "hello",
	})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}

func TestBuildArgsAndInput_SessionAlwaysGeneratesSessionID(t *testing.T) {
	args, stdin, output := buildArgsAndInput(Backend{
		Args:        []string{"-p"},
		Output:      OutputJSON,
		Input:       InputArg,
		SessionArg:  "--session-id",
		SessionMode: SessionAlways,
		ModelArg:    "--model",
	}, RunRequest{
		Prompt: "hello",
		Model:  "gpt-5",
	})

	if stdin != "" {
		t.Fatalf("stdin should be empty in arg mode")
	}
	if output != OutputJSON {
		t.Fatalf("output mode mismatch: %s", output)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--session-id") {
		t.Fatalf("args should include --session-id, got: %v", args)
	}
	sid := ""
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--session-id" {
			sid = strings.TrimSpace(args[i+1])
			break
		}
	}
	if sid == "" {
		t.Fatalf("session id should be generated, got args: %v", args)
	}
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(sid) {
		t.Fatalf("generated session id should be uuid v4, got: %q", sid)
	}
	if !strings.Contains(joined, "--model gpt-5") {
		t.Fatalf("args should include model, got: %v", args)
	}
	if !strings.HasSuffix(joined, " hello") {
		t.Fatalf("args should include prompt at end, got: %v", args)
	}
}

func TestBuildArgsAndInput_ResumeSkipsModelAndSessionArg(t *testing.T) {
	args, _, output := buildArgsAndInput(Backend{
		Args:         []string{"-p", "--output-format", "json"},
		ResumeArgs:   []string{"-p", "--output-format", "json", "--resume", "{sessionId}"},
		Output:       OutputJSON,
		ResumeOutput: OutputJSON,
		Input:        InputArg,
		SessionArg:   "--session-id",
		SessionMode:  SessionAlways,
		ModelArg:     "--model",
	}, RunRequest{
		Prompt:    "继续聊",
		Model:     "gpt-5",
		SessionID: "cli-abc",
	})

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--resume cli-abc") {
		t.Fatalf("resume args mismatch, got: %v", args)
	}
	if strings.Contains(joined, "--model") {
		t.Fatalf("resume mode should skip model arg, got: %v", args)
	}
	if strings.Contains(joined, "--session-id") {
		t.Fatalf("resume mode should skip session-id arg, got: %v", args)
	}
	if output != OutputJSON {
		t.Fatalf("resume output mismatch: %s", output)
	}
}

func writeTestScript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-cli.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write script failed: %v", err)
	}
	return path
}
