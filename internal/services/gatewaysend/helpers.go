package gatewaysend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/contracts"
)

func resolveCardProjectName(projectName string, resolver contracts.ProjectMetaResolver) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return ""
	}
	if resolver != nil {
		if project, err := resolver.ResolveProjectMeta(projectName); err == nil && project != nil {
			if base := repoBaseName(project.RepoRoot); base != "" {
				return base
			}
		}
	}
	return projectName
}

func repoBaseName(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return ""
	}
	base := strings.TrimSpace(filepath.Base(filepath.Clean(repoRoot)))
	if base == "" || base == "." {
		return ""
	}
	return base
}

func buildCardTitle(projectName string) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "dalek 通知"
	}
	return projectName
}

func marshalPayload(payload map[string]any) string {
	b, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func randomHex(nbytes int) string {
	if nbytes <= 0 {
		nbytes = 4
	}
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
