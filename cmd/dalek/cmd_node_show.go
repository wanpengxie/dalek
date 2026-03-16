package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func cmdNodeShow(args []string) {
	fs := flag.NewFlagSet("node show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看节点详情",
			"dalek node show --name <node> [--output text|json]",
			"dalek node show --name node-c",
			"dalek node show --name node-c -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	name := fs.String("name", "", "node 名（必填）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "node show 参数解析失败", "运行 dalek node show --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*name) == "" {
		exitUsageError(out, "缺少必填参数 --name", "node show 需要 node 名", "dalek node show --name node-c")
	}

	project := mustOpenProjectWithOutput(out, *home, *proj)
	node, err := project.GetNodeByName(nil, strings.TrimSpace(*name))
	if err != nil {
		exitRuntimeError(out, "读取 node 失败", err.Error(), "检查项目数据库是否可访问")
	}
	if node == nil {
		exitRuntimeError(out, "node 不存在", fmt.Sprintf("node=%s", strings.TrimSpace(*name)), "使用 dalek node ls 查看现有节点")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.node.show.v1",
			"node":   node,
		})
		return
	}
	fmt.Printf("name: %s\n", node.Name)
	fmt.Printf("status: %s\n", node.Status)
	fmt.Printf("endpoint: %s\n", node.Endpoint)
	fmt.Printf("roles: %s\n", strings.Join(node.RoleCapabilities, ","))
	fmt.Printf("provider_modes: %s\n", strings.Join(node.ProviderModes, ","))
	fmt.Printf("default_provider: %s\n", node.DefaultProvider)
	fmt.Printf("protocol_version: %s\n", node.ProtocolVersion)
	fmt.Printf("version: %s\n", node.Version)
	if node.LastSeenAt != nil {
		fmt.Printf("last_seen_at: %s\n", node.LastSeenAt.Format(time.RFC3339))
	}
}
