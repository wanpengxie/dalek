package main

import (
	"fmt"
	"os"
	"strings"
)

func cmdFeishuDoc(args []string) {
	if len(args) == 0 {
		printFeishuDocUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "create", "read", "write", "ls":
		exitFeishuSubcommandNotImplemented("doc", sub)
	case "help", "-h", "--help":
		printFeishuDocUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 feishu doc 子命令: %s", sub),
			"feishu doc 仅支持 create|read|write|ls",
			"运行 dalek feishu doc --help 查看可用命令",
		)
	}
}

func printFeishuDocUsage() {
	printGroupUsage("飞书文档操作", "dalek feishu doc <command> [flags]", []string{
		"create   创建文档（待实现）",
		"read     读取文档（待实现）",
		"write    写入文档（待实现）",
		"ls       列出文档（待实现）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek feishu doc <command> --help\" for more information.")
}
