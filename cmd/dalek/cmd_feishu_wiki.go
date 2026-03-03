package main

import (
	"fmt"
	"os"
	"strings"
)

func cmdFeishuWiki(args []string) {
	if len(args) == 0 {
		printFeishuWikiUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls", "nodes", "create":
		exitFeishuSubcommandNotImplemented("wiki", sub)
	case "help", "-h", "--help":
		printFeishuWikiUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 feishu wiki 子命令: %s", sub),
			"feishu wiki 仅支持 ls|nodes|create",
			"运行 dalek feishu wiki --help 查看可用命令",
		)
	}
}

func printFeishuWikiUsage() {
	printGroupUsage("飞书知识空间操作", "dalek feishu wiki <command> [flags]", []string{
		"ls       列出知识空间（待实现）",
		"nodes    列出节点（待实现）",
		"create   创建节点（待实现）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek feishu wiki <command> --help\" for more information.")
}
