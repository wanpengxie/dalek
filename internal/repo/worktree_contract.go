package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContractPaths 描述 worktree 下 `.dalek/` 的最小运行态契约路径集合。
// 这些路径是跨 services 的协议，不应该散落在某个具体领域包里。
// 与 AGENTS.md file_layout 定义保持一致。
type ContractPaths struct {
	Dir       string
	AgentsMD  string
	StateJSON string
}

func contractPaths(worktreeRoot string) ContractPaths {
	dir := filepath.Join(worktreeRoot, ".dalek")
	return ContractPaths{
		Dir:       dir,
		AgentsMD:  filepath.Join(dir, "AGENTS.md"),
		StateJSON: filepath.Join(dir, "state.json"),
	}
}

// EnsureWorktreeContract 确保 worktree 下 `.dalek/` 目录存在。
//
// 约束：
// - 该函数仅负责目录建立，不生成任何运行态文件或子目录。
// - PM/worker 的语义产物由各自执行链路按需写入。
func EnsureWorktreeContract(worktreeRoot string) (ContractPaths, error) {
	worktreeRoot = strings.TrimSpace(worktreeRoot)
	if worktreeRoot == "" {
		return ContractPaths{}, fmt.Errorf("worktreeRoot 为空")
	}
	cp := contractPaths(worktreeRoot)
	if err := os.MkdirAll(cp.Dir, 0o755); err != nil {
		return ContractPaths{}, fmt.Errorf("创建目录失败: %w", err)
	}

	return cp, nil
}
