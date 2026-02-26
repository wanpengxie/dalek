package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Service) runtimeDir(taskRunID uint) string {
	if taskRunID == 0 {
		return ""
	}
	projectName := strings.TrimSpace(s.projectName())
	if projectName == "" {
		projectName = strings.TrimSpace(s.projectKey())
	}
	homeDir := inferHomeRootFromWorktreesDir("")
	if s != nil && s.p != nil {
		homeDir = inferHomeRootFromWorktreesDir(strings.TrimSpace(s.p.WorktreesDir))
	}
	if homeDir == "" {
		homeDir = strings.TrimSpace(s.projectDir())
	}
	return filepath.Join(homeDir, "agents", projectName, strconv.FormatUint(uint64(taskRunID), 10))
}

func prepareSubagentRuntime(runtimeDir string, prompt string) (*os.File, *os.File, error) {
	runtimeDir = strings.TrimSpace(runtimeDir)
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("创建 runtime 目录失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "prompt.txt"), []byte(strings.TrimSpace(prompt)+"\n"), 0o644); err != nil {
		return nil, nil, fmt.Errorf("写入 prompt 失败: %w", err)
	}

	streamPath := filepath.Join(runtimeDir, "stream.log")
	sdkStreamPath := filepath.Join(runtimeDir, "sdk-stream.log")
	streamFile, err := os.OpenFile(streamPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("打开 stream.log 失败: %w", err)
	}
	sdkStreamFile, err := os.OpenFile(sdkStreamPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		_ = streamFile.Close()
		return nil, nil, fmt.Errorf("打开 sdk-stream.log 失败: %w", err)
	}
	return streamFile, sdkStreamFile, nil
}

func writeSubagentResult(runtimeDir string, payload []byte) {
	_ = os.WriteFile(filepath.Join(strings.TrimSpace(runtimeDir), "result.json"), payload, 0o644)
}
