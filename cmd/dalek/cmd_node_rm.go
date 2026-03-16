package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func cmdNodeRemove(args []string) {
	fs := flag.NewFlagSet("node rm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"删除节点",
			"dalek node rm --name <node> [--output text|json]",
			"dalek node rm --name node-c",
			"dalek node rm --name node-c -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	name := fs.String("name", "", "node 名（必填）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "node rm 参数解析失败", "运行 dalek node rm --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*name) == "" {
		exitUsageError(out, "缺少必填参数 --name", "node rm 需要 node 名", "dalek node rm --name node-c")
	}

	project := mustOpenProjectWithOutput(out, *home, *proj)
	removed, err := project.RemoveNode(nil, strings.TrimSpace(*name))
	if err != nil {
		exitRuntimeError(out, "删除 node 失败", err.Error(), "检查项目数据库是否可访问")
	}
	if !removed {
		exitRuntimeError(out, "node 不存在", fmt.Sprintf("node=%s", strings.TrimSpace(*name)), "使用 dalek node ls 查看现有节点")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.node.rm.v1",
			"name":    strings.TrimSpace(*name),
			"removed": true,
		})
		return
	}
	fmt.Printf("%s\n", strings.TrimSpace(*name))
}
