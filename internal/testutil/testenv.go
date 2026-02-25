package testutil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func RunCmd(t testing.TB, dir string, env []string, name string, args ...string) (stdout string, stderr string, err error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func RunCmdOK(t testing.TB, dir string, env []string, name string, args ...string) (stdout string, stderr string) {
	t.Helper()
	stdout, stderr, err := RunCmd(t, dir, env, name, args...)
	if err != nil {
		t.Fatalf("command failed: %s %s\nstderr:\n%s\nerr=%v", name, strings.Join(args, " "), stderr, err)
	}
	return stdout, stderr
}

func InitGitRepo(t testing.TB) string {
	t.Helper()
	repo := t.TempDir()
	RunCmdOK(t, repo, nil, "git", "init")
	RunCmdOK(t, repo, nil, "git", "config", "user.email", "dalek-test@example.com")
	RunCmdOK(t, repo, nil, "git", "config", "user.name", "dalek-test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	RunCmdOK(t, repo, nil, "git", "add", "README.md")
	RunCmdOK(t, repo, nil, "git", "commit", "-m", "init")
	return repo
}

func InstallTmuxShim(t testing.TB) (shimDir string, statePath string) {
	t.Helper()
	shimDir = t.TempDir()
	statePath = filepath.Join(shimDir, "tmux-state.txt")
	shimPath := filepath.Join(shimDir, "tmux")

	const script = `#!/usr/bin/env bash
set -euo pipefail

state="${DALEK_TEST_TMUX_STATE:-}"
if [[ -z "$state" ]]; then
  state="$(dirname "$0")/tmux-state.txt"
fi
mkdir -p "$(dirname "$state")"
touch "$state"

first_session() {
  while IFS= read -r line; do
    if [[ -n "$line" ]]; then
      echo "$line"
      return 0
    fi
  done < "$state"
  echo ""
}

has_session() {
  local target="$1"
  while IFS= read -r line; do
    [[ "$line" == "$target" ]] && return 0
  done < "$state"
  return 1
}

remove_session() {
  local target="$1"
  local tmp
  tmp="$(mktemp)"
  grep -Fxv "$target" "$state" > "$tmp" || true
  mv "$tmp" "$state"
}

arg_value() {
  local want="$1"
  shift
  local arr=("$@")
  local i
  for ((i=0; i<${#arr[@]}; i++)); do
    if [[ "${arr[$i]}" == "$want" ]]; then
      if [[ $((i+1)) -lt ${#arr[@]} ]]; then
        echo "${arr[$((i+1))]}"
      else
        echo ""
      fi
      return 0
    fi
  done
  echo ""
}

session_from_target() {
  local target="$1"
  if [[ "$target" == %* ]]; then
    first_session
    return 0
  fi
  echo "${target%%:*}"
}

format_session_line() {
  local fmt="$1"
  local s="$2"
  local line="$fmt"
  line="${line//\#\{session_name\}/$s}"
  line="${line//\#\{session_id\}/\$1}"
  line="${line//\#\{session_created\}/0}"
  line="${line//\#\{session_activity\}/0}"
  line="${line//\#\{session_attached\}/0}"
  line="${line//\#\{session_last_attached\}/0}"
  line="${line//\#\{session_windows\}/1}"
  line="${line//\#\{session_path\}/$PWD}"
  line="${line//\#\{session_alerts\}/}"
  echo "$line"
}

format_window_line() {
  local fmt="$1"
  local line="$fmt"
  line="${line//\#\{window_index\}/0}"
  line="${line//\#\{window_id\}/@1}"
  line="${line//\#\{window_name\}/main}"
  line="${line//\#\{window_active\}/1}"
  line="${line//\#\{window_zoomed_flag\}/0}"
  line="${line//\#\{window_layout\}/even-horizontal}"
  line="${line//\#\{window_visible_layout\}/even-horizontal}"
  line="${line//\#\{window_activity\}/0}"
  line="${line//\#\{window_activity_flag\}/0}"
  line="${line//\#\{window_active_clients\}/0}"
  line="${line//\#\{window_flags\}/*}"
  line="${line//\#\{window_panes\}/1}"
  echo "$line"
}

format_pane_line() {
  local fmt="$1"
  local s="$2"
  local line="$fmt"
  line="${line//\#\{session_name\}/$s}"
  line="${line//\#\{window_index\}/0}"
  line="${line//\#\{pane_index\}/0}"
  line="${line//\#\{pane_id\}/%1}"
  line="${line//\#\{pane_active\}/1}"
  line="${line//\#\{pane_last\}/0}"
  line="${line//\#\{pane_width\}/120}"
  line="${line//\#\{pane_height\}/30}"
  line="${line//\#\{pane_dead\}/0}"
  line="${line//\#\{pane_dead_status\}/0}"
  line="${line//\#\{pane_dead_signal\}/}"
  line="${line//\#\{pane_dead_time\}/0}"
  line="${line//\#\{pane_in_mode\}/0}"
  line="${line//\#\{pane_mode\}/}"
  line="${line//\#\{pane_input_off\}/0}"
  line="${line//\#\{pane_pipe\}/1}"
  line="${line//\#\{pane_pid\}/1234}"
  line="${line//\#\{pane_tty\}/\/dev\/pts\/1}"
  line="${line//\#\{pane_current_command\}/bash}"
  line="${line//\#\{pane_start_command\}/bash}"
  line="${line//\#\{pane_current_path\}/$PWD}"
  line="${line//\#\{pane_title\}/shim-pane}"
  line="${line//\#\{cursor_x\}/0}"
  line="${line//\#\{cursor_y\}/0}"
  line="${line//\#\{cursor_flag\}/1}"
  line="${line//\#\{insert_flag\}/0}"
  line="${line//\#\{cursor_character\}/ }"
  line="${line//\#\{scroll_position\}/0}"
  line="${line//\#\{selection_present\}/0}"
  line="${line//\#\{selection_start_x\}/0}"
  line="${line//\#\{selection_start_y\}/0}"
  line="${line//\#\{selection_end_x\}/0}"
  line="${line//\#\{selection_end_y\}/0}"
  echo "$line"
}

format_client_line() {
  local fmt="$1"
  local s="$2"
  local line="$fmt"
  line="${line//\#\{client_tty\}/\/dev\/pts\/2}"
  line="${line//\#\{client_name\}/shim-client}"
  line="${line//\#\{client_session\}/$s}"
  line="${line//\#\{client_created\}/0}"
  line="${line//\#\{client_activity\}/0}"
  line="${line//\#\{client_termname\}/xterm-256color}"
  echo "$line"
}

args=("$@")
idx=0
while [[ $idx -lt ${#args[@]} ]]; do
  case "${args[$idx]}" in
    -L|-S)
      idx=$((idx+2))
      ;;
    *)
      break
      ;;
  esac
done

if [[ $idx -ge ${#args[@]} ]]; then
  exit 0
fi

cmd="${args[$idx]}"
idx=$((idx+1))
rest=("${args[@]:$idx}")

case "$cmd" in
  -V)
    echo "tmux 3.3a"
    ;;
  new-session)
    name="$(arg_value "-s" "${rest[@]}")"
    if [[ -z "$name" ]]; then
      echo "missing session name" >&2
      exit 1
    fi
    if ! has_session "$name"; then
      echo "$name" >> "$state"
    fi
    ;;
  list-sessions)
    if [[ ! -s "$state" ]]; then
      echo "no server running on /tmp/tmux-shim" >&2
      exit 1
    fi
    fmt="$(arg_value "-F" "${rest[@]}")"
    while IFS= read -r s; do
      [[ -z "$s" ]] && continue
      if [[ -z "$fmt" || "$fmt" == "#{session_name}" ]]; then
        echo "$s"
      else
        format_session_line "$fmt" "$s"
      fi
    done < "$state"
    ;;
  kill-session)
    target="$(arg_value "-t" "${rest[@]}")"
    session="$(session_from_target "$target")"
    if [[ -n "$session" ]]; then
      remove_session "$session"
    fi
    ;;
  kill-server)
    : > "$state"
    ;;
  send-keys)
    ;;
  pipe-pane)
    ;;
  capture-pane)
    echo "shim output line 1"
    echo "shim output line 2"
    ;;
  display-message)
    target="$(arg_value "-t" "${rest[@]}")"
    fmt="$(arg_value "-p" "${rest[@]}")"
    if [[ -z "$fmt" ]]; then
      fmt="${rest[${#rest[@]}-1]:-}"
    fi
    session="$(session_from_target "$target")"
    if [[ -z "$session" ]]; then
      session="$(first_session)"
    fi
    if [[ -z "$session" ]]; then
      echo "no sessions" >&2
      exit 1
    fi
    format_pane_line "$fmt" "$session"
    ;;
  list-windows)
    target="$(arg_value "-t" "${rest[@]}")"
    session="$(session_from_target "$target")"
    if [[ -z "$session" ]]; then
      session="$(first_session)"
    fi
    if [[ -z "$session" ]]; then
      exit 1
    fi
    fmt="$(arg_value "-F" "${rest[@]}")"
    format_window_line "$fmt"
    ;;
  list-panes)
    fmt="$(arg_value "-F" "${rest[@]}")"
    target="$(arg_value "-t" "${rest[@]}")"
    all=0
    for it in "${rest[@]}"; do
      if [[ "$it" == "-a" ]]; then
        all=1
      fi
    done
    if [[ $all -eq 1 ]]; then
      while IFS= read -r s; do
        [[ -z "$s" ]] && continue
        format_pane_line "$fmt" "$s"
      done < "$state"
    else
      session="$(session_from_target "$target")"
      if [[ -z "$session" ]]; then
        session="$(first_session)"
      fi
      if [[ -z "$session" ]]; then
        exit 1
      fi
      format_pane_line "$fmt" "$session"
    fi
    ;;
  list-clients)
    if [[ ! -s "$state" ]]; then
      exit 1
    fi
    fmt="$(arg_value "-F" "${rest[@]}")"
    s="$(first_session)"
    format_client_line "$fmt" "$s"
    ;;
  *)
    echo "unsupported tmux command in shim: $cmd" >&2
    exit 1
    ;;
esac
`

	if err := os.WriteFile(shimPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write tmux shim failed: %v", err)
	}
	return shimDir, statePath
}

func UseTmuxShim(t testing.TB) string {
	t.Helper()
	shimDir, statePath := InstallTmuxShim(t)
	if ts, ok := t.(interface {
		Setenv(key, value string)
	}); ok {
		ts.Setenv("DALEK_TEST_TMUX_STATE", statePath)
		ts.Setenv("PATH", fmt.Sprintf("%s:%s", shimDir, os.Getenv("PATH")))
	}
	return statePath
}
