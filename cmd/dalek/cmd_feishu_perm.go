package main

import (
	"fmt"
	"os"
	"strings"
)

func cmdFeishuPerm(args []string) {
	if len(args) == 0 {
		printFeishuPermUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "share", "add", "ls":
		exitFeishuSubcommandNotImplemented("perm", sub)
	case "help", "-h", "--help":
		printFeishuPermUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 feishu perm 子命令: %s", sub),
			"feishu perm 仅支持 share|add|ls",
			"运行 dalek feishu perm --help 查看可用命令",
		)
	}
}

func printFeishuPermUsage() {
	printGroupUsage("飞书权限管理", "dalek feishu perm <command> [flags]", []string{
		"share    创建公开分享链接（待实现）",
		"add      添加协作者（待实现）",
		"ls       列出权限成员（待实现）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek feishu perm <command> --help\" for more information.")
}

func exitFeishuSubcommandNotImplemented(group, sub string) {
	group = strings.TrimSpace(group)
	sub = strings.TrimSpace(sub)
	exitRuntimeError(globalOutput,
		fmt.Sprintf("feishu %s %s 尚未实现", group, sub),
		"当前阶段仅完成 feishu auth 能力",
		"先运行 dalek feishu auth 验证凭据；其余子命令将在后续阶段实现",
	)
}
