package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dalek/internal/infra"
)

const (
	referenceTransactionHookName       = "reference-transaction"
	referenceTransactionHookMarkerHead = "# >>> DALEK:MERGE_SYNC_REF:BEGIN >>>"
	referenceTransactionHookMarkerTail = "# <<< DALEK:MERGE_SYNC_REF:END <<<"
)

var referenceTransactionHookBlockPattern = regexp.MustCompile(`(?s)# >>> DALEK:MERGE_SYNC_REF:BEGIN >>>\n.*?# <<< DALEK:MERGE_SYNC_REF:END <<<`)

type ReferenceTransactionHookResult struct {
	Path      string
	Installed bool
	Skipped   bool
	Warning   string
}

func EnsureReferenceTransactionHook(repoRoot, homeDir, projectName string) (ReferenceTransactionHookResult, error) {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return ReferenceTransactionHookResult{}, fmt.Errorf("repoRoot 不能为空")
	}
	homeDir = strings.TrimSpace(homeDir)
	projectName = strings.TrimSpace(projectName)

	hooksDir, err := resolveGitHooksDir(repoRoot)
	if err != nil {
		return ReferenceTransactionHookResult{}, err
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return ReferenceTransactionHookResult{}, err
	}
	hookPath := filepath.Join(hooksDir, referenceTransactionHookName)
	result := ReferenceTransactionHookResult{Path: hookPath}
	wantBlock := renderReferenceTransactionHookBlock(homeDir, projectName)

	data, readErr := os.ReadFile(hookPath)
	if readErr != nil {
		if !os.IsNotExist(readErr) {
			return result, readErr
		}
		content := renderReferenceTransactionHookFile(wantBlock)
		if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
			return result, err
		}
		if err := os.Chmod(hookPath, 0o755); err != nil {
			return result, err
		}
		result.Installed = true
		return result, nil
	}

	current := string(data)
	hasHead := strings.Contains(current, referenceTransactionHookMarkerHead)
	hasTail := strings.Contains(current, referenceTransactionHookMarkerTail)
	if hasHead != hasTail {
		return result, fmt.Errorf("reference-transaction hook marker 损坏: %s", hookPath)
	}
	if !hasHead {
		result.Skipped = true
		result.Warning = fmt.Sprintf("检测到已有自定义 %s，因无 dalek marker 跳过注入: %s", referenceTransactionHookName, hookPath)
		return result, nil
	}
	if !referenceTransactionHookBlockPattern.MatchString(current) {
		return result, fmt.Errorf("reference-transaction hook dalek marker 区块不完整: %s", hookPath)
	}

	updated := referenceTransactionHookBlockPattern.ReplaceAllLiteralString(current, strings.TrimRight(wantBlock, "\n"))
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	if updated != current {
		if err := os.WriteFile(hookPath, []byte(updated), 0o755); err != nil {
			return result, err
		}
		result.Installed = true
	}
	if err := os.Chmod(hookPath, 0o755); err != nil {
		return result, err
	}
	return result, nil
}

func renderReferenceTransactionHookFile(block string) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("set -euo pipefail\n\n")
	b.WriteString("stage=\"${1:-}\"\n")
	b.WriteString("if [[ \"$stage\" != \"committed\" ]]; then\n")
	b.WriteString("  exit 0\n")
	b.WriteString("fi\n\n")
	b.WriteString(strings.TrimRight(block, "\n"))
	b.WriteString("\n")
	return b.String()
}

func renderReferenceTransactionHookBlock(homeDir, projectName string) string {
	syncCmd := "dalek merge sync-ref"
	if strings.TrimSpace(homeDir) != "" {
		syncCmd += " --home " + shellSingleQuote(homeDir)
	}
	if strings.TrimSpace(projectName) != "" {
		syncCmd += " --project " + shellSingleQuote(projectName)
	}
	syncCmd += ` --ref "$ref_name" --old "$old_sha" --new "$new_sha"`

	var b strings.Builder
	b.WriteString(referenceTransactionHookMarkerHead + "\n")
	b.WriteString("while read -r old_sha new_sha ref_name; do\n")
	b.WriteString("  if [[ -z \"${ref_name:-}\" ]]; then\n")
	b.WriteString("    continue\n")
	b.WriteString("  fi\n")
	b.WriteString("  case \"$ref_name\" in\n")
	b.WriteString("    refs/heads/*)\n")
	b.WriteString("      " + syncCmd + " >/dev/null 2>&1 || true\n")
	b.WriteString("      ;;\n")
	b.WriteString("  esac\n")
	b.WriteString("done\n")
	b.WriteString(referenceTransactionHookMarkerTail + "\n")
	return b.String()
}

func resolveGitHooksDir(repoRoot string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := infra.Run(ctx, repoRoot, "git", "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	if err != nil {
		return "", fmt.Errorf("解析 git hooks 目录失败: %w", err)
	}
	hooksDir := strings.TrimSpace(out)
	if hooksDir == "" {
		return "", fmt.Errorf("git hooks 目录为空")
	}
	return hooksDir, nil
}

func shellSingleQuote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(raw, "'", `'"'"'`) + "'"
}
