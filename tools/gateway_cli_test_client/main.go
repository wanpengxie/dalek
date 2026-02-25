package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type channelRequest struct {
	ChatID   string `json:"chat_id"`
	SenderID string `json:"sender_id,omitempty"`
	Text     string `json:"text"`
}

type channelResponse struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg,omitempty"`
	Reply   string `json:"reply,omitempty"`
	Project string `json:"project,omitempty"`
}

func main() {
	fs := flag.NewFlagSet("gateway_cli_test_client", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", "", "测试通道地址（例如 http://127.0.0.1:18081/cli/test/channel）")
	chatID := fs.String("chat-id", "", "测试 chat_id")
	senderID := fs.String("sender", "cli.test.user", "发送者")
	text := fs.String("text", "", "消息文本")
	timeout := fs.Duration("timeout", 8*time.Second, "请求超时")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if strings.TrimSpace(*url) == "" || strings.TrimSpace(*chatID) == "" || strings.TrimSpace(*text) == "" {
		fmt.Fprintln(os.Stderr, "-url/-chat-id/-text 不能为空")
		os.Exit(2)
	}

	reqPayload := channelRequest{
		ChatID:   strings.TrimSpace(*chatID),
		SenderID: strings.TrimSpace(*senderID),
		Text:     strings.TrimSpace(*text),
	}
	body, err := json.Marshal(reqPayload)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal request failed:", err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: *timeout}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSpace(*url), bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "build request failed:", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "request failed:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "http status=%d body=%s\n", resp.StatusCode, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}

	var out channelResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		fmt.Fprintln(os.Stderr, "decode response failed:", err)
		os.Exit(1)
	}
	if out.Code != 0 {
		msg := strings.TrimSpace(out.Msg)
		if msg == "" {
			msg = "unknown error"
		}
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}

	reply := strings.TrimSpace(out.Reply)
	if reply == "" {
		reply = "(empty reply)"
	}
	fmt.Println(reply)
}
