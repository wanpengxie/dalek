package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func cmdVersion(args []string) {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"打印 dalek binary 版本",
			"dalek version [--output text|json]",
			"dalek version",
			"dalek version -o json",
		)
	}
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "version 参数解析失败", "运行 dalek version --help 查看参数")
	out := parseOutputOrExit(*output, true)

	ver := strings.TrimSpace(version)
	if ver == "" {
		ver = "dev"
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.version.v1",
			"version": ver,
		})
		return
	}
	fmt.Println(ver)
}
