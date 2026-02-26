package client

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"dalek/internal/app"
)

const DefaultDaemonWSURL = "ws://127.0.0.1:18081/ws"

func ResolveDaemonWSURL(cliValue, homeFlag string) string {
	if strings.TrimSpace(cliValue) != "" {
		return strings.TrimSpace(cliValue)
	}
	if fromEnv := strings.TrimSpace(os.Getenv("DALEK_GATEWAY_WS_URL")); fromEnv != "" {
		return fromEnv
	}
	if fromConfig := resolveDaemonWSURLFromHome(homeFlag); fromConfig != "" {
		return fromConfig
	}
	return DefaultDaemonWSURL
}

func resolveDaemonWSURLFromHome(homeFlag string) string {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		return ""
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		return ""
	}
	listenAddr := strings.TrimSpace(h.Config.Daemon.Internal.Listen)
	if listenAddr == "" {
		return ""
	}
	if strings.Contains(listenAddr, "://") {
		u, err := url.Parse(listenAddr)
		if err != nil || strings.TrimSpace(u.Host) == "" {
			return ""
		}
		scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
		switch scheme {
		case "http":
			u.Scheme = "ws"
		case "https":
			u.Scheme = "wss"
		case "ws", "wss":
			// keep as-is
		default:
			return ""
		}
		if strings.TrimSpace(u.Path) == "" || strings.TrimSpace(u.Path) == "/" {
			u.Path = "/ws"
		}
		return u.String()
	}
	return "ws://" + listenAddr + "/ws"
}

func BuildChatWSURL(baseURL, projectName, conversationID, senderID string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("ws url 不能为空")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "", fmt.Errorf("project 不能为空")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(strings.TrimSpace(u.Scheme), "http") {
		u.Scheme = "ws"
	}
	if strings.EqualFold(strings.TrimSpace(u.Scheme), "https") {
		u.Scheme = "wss"
	}
	if strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("ws url 非法: %s", baseURL)
	}
	if strings.TrimSpace(u.Path) == "" || strings.TrimSpace(u.Path) == "/" {
		u.Path = "/ws"
	}

	query := u.Query()
	query.Set("project", projectName)
	if strings.TrimSpace(conversationID) != "" {
		query.Set("conv", strings.TrimSpace(conversationID))
	}
	if strings.TrimSpace(senderID) != "" {
		query.Set("sender", strings.TrimSpace(senderID))
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}
