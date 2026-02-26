package pm

import (
	"os"
	"strconv"
	"strings"
)

func nextDispatchDepthEnvValue() string {
	depth := parseDispatchDepth(os.Getenv(dispatchDepthEnvKey))
	return strconv.Itoa(depth + 1)
}

func parseDispatchDepth(raw string) int {
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
