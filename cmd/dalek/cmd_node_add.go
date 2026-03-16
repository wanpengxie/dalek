package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"dalek/internal/app"
)

func cmdNodeAdd(args []string) {
	fs := flag.NewFlagSet("node add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"添加节点",
			"dalek node add --name <node> [--status online|offline|unknown|degraded] [--roles run,dev] [--provider-modes run_executor,codex]",
			"dalek node add --name node-c --roles run --provider-modes run_executor",
			"dalek node add --name node-b --roles dev --provider-modes codex --status online -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	name := fs.String("name", "", "node 名（必填）")
	endpoint := fs.String("endpoint", "", "node endpoint（可选）")
	status := fs.String("status", "", "node 状态（可选）")
	roles := fs.String("roles", "", "role 能力（逗号分隔，可选）")
	providers := fs.String("provider-modes", "", "provider 模式（逗号分隔，可选）")
	defaultProvider := fs.String("default-provider", "", "默认 provider（可选）")
	authMode := fs.String("auth-mode", "", "认证模式（可选）")
	protocol := fs.String("protocol-version", "", "协议版本（可选）")
	version := fs.String("version", "", "node 版本（可选）")
	sessionAffinity := fs.String("session-affinity", "", "session affinity（可选）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "node add 参数解析失败", "运行 dalek node add --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*name) == "" {
		exitUsageError(out, "缺少必填参数 --name", "node add 需要 node 名", "dalek node add --name node-c")
	}

	project := mustOpenProjectWithOutput(out, *home, *proj)
	node, err := project.RegisterNode(nil, app.RegisterNodeOptions{
		Name:             strings.TrimSpace(*name),
		Endpoint:         strings.TrimSpace(*endpoint),
		AuthMode:         strings.TrimSpace(*authMode),
		Status:           strings.TrimSpace(*status),
		Version:          strings.TrimSpace(*version),
		ProtocolVersion:  strings.TrimSpace(*protocol),
		RoleCapabilities: splitCommaList(*roles),
		ProviderModes:    splitCommaList(*providers),
		DefaultProvider:  strings.TrimSpace(*defaultProvider),
		SessionAffinity:  strings.TrimSpace(*sessionAffinity),
	})
	if err != nil {
		exitRuntimeError(out, "添加 node 失败", err.Error(), "检查 node 名是否重复或字段是否合法")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.node.add.v1",
			"node":   node,
		})
		return
	}
	fmt.Printf("%s\n", node.Name)
}

func splitCommaList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}
