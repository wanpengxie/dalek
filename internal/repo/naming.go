package repo

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"
)

func ProjectKey(repoRoot string) string {
	h := sha1.Sum([]byte(strings.TrimSpace(repoRoot)))
	return hex.EncodeToString(h[:6])
}

// DeriveProjectName 把 repo 路径转成一个稳定的 project 名：
// - 路径分隔符 `/` -> `-`
// - 去掉前后多余的 `-`
// - 过长时使用 `<base>-<key>` 兜底（避免目录/会话名过长）
func DeriveProjectName(repoRoot string) string {
	p := strings.TrimSpace(repoRoot)
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil && abs != "" {
		p = abs
	}
	name := strings.ReplaceAll(p, string(filepath.Separator), "-")
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.Trim(name, "-")
	name = strings.TrimSpace(name)
	if name == "" {
		name = "project-" + ProjectKey(p)
	}
	if len(name) > 80 {
		base := strings.TrimSpace(filepath.Base(p))
		base = strings.ReplaceAll(base, " ", "_")
		base = strings.Trim(base, "-")
		if base == "" {
			base = "project"
		}
		name = base + "-" + ProjectKey(p)
	}
	return name
}
