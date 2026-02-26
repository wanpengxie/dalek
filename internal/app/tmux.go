package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dalek/internal/infra"
)

// TmuxSocketDir 返回 tmux socket 目录路径。
func TmuxSocketDir(tmpDir string, uid int) string {
	tmpDir = strings.TrimSpace(tmpDir)
	if tmpDir == "" {
		tmpDir = strings.TrimSpace(os.Getenv("TMUX_TMPDIR"))
	}
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	if uid <= 0 {
		uid = os.Getuid()
	}
	return filepath.Join(tmpDir, "tmux-"+strconv.Itoa(uid))
}

// ListTmuxSocketFiles 列出 tmux socket 目录下所有 socket 文件。
func ListTmuxSocketFiles(tmpDir string) ([]string, error) {
	dir := TmuxSocketDir(tmpDir, 0)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSpace(e.Name())
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// ListTmuxSessions 列出指定 socket 下的所有 tmux 会话。
func ListTmuxSessions(ctx context.Context, socket string) (map[string]bool, error) {
	return infra.NewTmuxExecClient().ListSessions(ctx, strings.TrimSpace(socket))
}

// KillTmuxServer 终止指定 socket 的 tmux 服务器。
func KillTmuxServer(ctx context.Context, socket string) error {
	return infra.NewTmuxExecClient().KillServer(ctx, strings.TrimSpace(socket))
}

// KillTmuxSession 终止指定 socket 下的指定 tmux 会话。
func KillTmuxSession(ctx context.Context, socket, session string) error {
	return infra.NewTmuxExecClient().KillSession(ctx, strings.TrimSpace(socket), strings.TrimSpace(session))
}
