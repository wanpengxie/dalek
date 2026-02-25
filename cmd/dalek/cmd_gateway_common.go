package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeGatewayListenAddr(fieldName, raw string) (string, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return "", fmt.Errorf("%s 不能为空", strings.TrimSpace(fieldName))
	}
	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err != nil || strings.TrimSpace(u.Host) == "" {
			return "", fmt.Errorf("%s 格式无效: %q", strings.TrimSpace(fieldName), addr)
		}
		return "", fmt.Errorf("%s 需为 host:port，不支持 URL: %q", strings.TrimSpace(fieldName), addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%s 需为 host:port: %w", strings.TrimSpace(fieldName), err)
	}
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if port == "" {
		return "", fmt.Errorf("%s 缺少端口", strings.TrimSpace(fieldName))
	}
	return net.JoinHostPort(host, port), nil
}
