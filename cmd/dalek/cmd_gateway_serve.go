package main

import (
	"flag"
	"os"
)

func cmdGatewayServe(args []string) {
	fs := flag.NewFlagSet("gateway serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"gateway serve（已迁移到 daemon）",
			"dalek gateway serve",
			"dalek gateway serve",
			"dalek daemon start",
		)
	}
	_ = fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek；仅保留兼容）")
	_ = fs.String("listen", "", "已废弃（gateway serve 实现已移除）")
	_ = fs.String("internal-listen", "", "已废弃（gateway serve 实现已移除）")
	_ = fs.Int("queue-depth", 0, "已废弃（gateway serve 实现已移除）")
	parseFlagSetOrExit(fs, args, globalOutput, "gateway serve 参数解析失败", "运行 dalek gateway serve --help 查看参数")

	exitRuntimeError(globalOutput,
		"gateway serve 已迁移到 daemon，实现已移除",
		"请使用 dalek daemon start 启动统一入口（public listener + internal API + manager + notebook）",
		"gateway serve 不再可用",
	)
}
