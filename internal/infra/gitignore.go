package infra

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func EnsureRepoLocalIgnore(repoRoot, pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := Run(ctx, repoRoot, "git", "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	excludePath := strings.TrimSpace(out)
	if excludePath == "" {
		return fmt.Errorf("git rev-parse --git-path info/exclude 输出为空")
	}
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(repoRoot, excludePath)
	}
	return EnsureLineInFile(excludePath, pattern)
}

func EnsureLineInFile(path, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(path, []byte(line+"\n"), 0o644)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, []byte(line+"\n")...)
	return os.WriteFile(path, b, 0o644)
}
