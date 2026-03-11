package pm

import (
	"os"
	"strconv"
	"strings"
)

func nextWorkerRunDepthEnvValue() string {
	depth := parseWorkerRunDepth(os.Getenv(dispatchDepthEnvKey))
	return strconv.Itoa(depth + 1)
}

func parseWorkerRunDepth(raw string) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	depth, err := strconv.Atoi(trimmed)
	if err != nil || depth < 0 {
		return 0
	}
	return depth
}
