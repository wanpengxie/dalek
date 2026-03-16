package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"dalek/internal/app"
)

func cmdNodeList(args []string) {
	fs := flag.NewFlagSet("node ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出节点",
			"dalek node ls [--status online|offline|unknown|degraded] [--role run|dev|control] [--provider-mode codex|claude|run_executor] [--limit 100] [--output text|json]",
			"dalek node ls",
			"dalek node ls --status online -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	status := fs.String("status", "", "节点状态过滤（可选）")
	role := fs.String("role", "", "角色能力过滤（可选）")
	provider := fs.String("provider-mode", "", "provider 模式过滤（可选）")
	limit := fs.Int("limit", 100, "返回数量上限（默认 100）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "node ls 参数解析失败", "运行 dalek node ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	project := mustOpenProjectWithOutput(out, *home, *proj)
	nodes, err := project.ListNodes(nil, app.ListNodesOptions{
		Status:         strings.TrimSpace(*status),
		RoleCapability: strings.TrimSpace(*role),
		ProviderMode:   strings.TrimSpace(*provider),
		Limit:          *limit,
	})
	if err != nil {
		exitRuntimeError(out, "读取 node 列表失败", err.Error(), "检查项目数据库是否可访问")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.node.list.v1",
			"nodes":  nodes,
		})
		return
	}

	if len(nodes) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, node := range nodes {
		fmt.Printf("%s\t%s\t%s\n", node.Name, node.Status, node.Endpoint)
	}
}
