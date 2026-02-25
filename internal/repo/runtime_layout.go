package repo

import (
	"fmt"
	"path/filepath"
)

// runtime workers/ 目录下的路径约定（与 services 无关，属于 repo/runtime layout 的一部分）。

func WorkerRuntimeDir(workersDir string, workerID uint) string {
	return filepath.Join(workersDir, fmt.Sprintf("w%d", workerID))
}

func WorkerStreamLogPath(workersDir string, workerID uint) string {
	return filepath.Join(WorkerRuntimeDir(workersDir, workerID), "stream.log")
}

func WorkerSDKStreamLogPath(workersDir string, workerID uint) string {
	return filepath.Join(WorkerRuntimeDir(workersDir, workerID), "sdk-stream.log")
}

