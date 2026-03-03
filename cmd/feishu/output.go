package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

type cliOutputFormat string

const (
	outputText cliOutputFormat = "text"
	outputJSON cliOutputFormat = "json"
)

type cliErrorJSON struct {
	Schema   string `json:"schema"`
	Error    string `json:"error"`
	Cause    string `json:"cause"`
	Fix      string `json:"fix"`
	ExitCode int    `json:"exit_code"`
}

func addOutputFlag(fs *flag.FlagSet, usage string) *string {
	if fs == nil {
		v := string(outputText)
		return &v
	}
	v := fs.String("output", string(globalOutput), usage)
	fs.StringVar(v, "o", string(globalOutput), usage)
	return v
}

func parseOutputOrExit(raw string, allowJSON bool) cliOutputFormat {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		v = string(globalOutput)
	}
	if v != string(outputText) && v != string(outputJSON) {
		exitUsageError(outputText,
			fmt.Sprintf("非法输出格式: %q", strings.TrimSpace(raw)),
			"--output 仅支持 text 或 json",
			"将参数改为 --output text 或 --output json",
		)
	}
	if !allowJSON && v == string(outputJSON) {
		exitUsageError(outputText,
			"该命令不支持 --output json",
			"当前命令仅支持文本输出",
			"移除 --output json，或改用支持 JSON 的查询命令",
		)
	}
	return cliOutputFormat(v)
}

func parseFlagSetOrExit(fs *flag.FlagSet, args []string, out cliOutputFormat, what, fix string) {
	if fs == nil {
		return
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		exitUsageError(out, what, err.Error(), fix)
	}
}

func flagProvided(fs *flag.FlagSet, name string) bool {
	if fs == nil {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	var provided bool
	fs.Visit(func(f *flag.Flag) {
		if strings.TrimSpace(f.Name) == name {
			provided = true
		}
	})
	return provided
}

func printGroupUsage(title, usage string, commands []string) {
	out := os.Stderr
	title = strings.TrimSpace(title)
	usage = strings.TrimSpace(usage)
	if title != "" {
		fmt.Fprintln(out, title)
		fmt.Fprintln(out)
	}
	if usage != "" {
		fmt.Fprintln(out, "Usage:")
		fmt.Fprintf(out, "  %s\n", usage)
		fmt.Fprintln(out)
	}
	if len(commands) > 0 {
		fmt.Fprintln(out, "Commands:")
		for _, c := range commands {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			fmt.Fprintf(out, "  %s\n", c)
		}
		fmt.Fprintln(out)
	}
}

func printSubcommandUsage(fs *flag.FlagSet, title, usage string, examples ...string) {
	out := os.Stderr
	title = strings.TrimSpace(title)
	usage = strings.TrimSpace(usage)
	if title != "" {
		fmt.Fprintln(out, title)
		fmt.Fprintln(out)
	}
	if usage != "" {
		fmt.Fprintln(out, "Usage:")
		fmt.Fprintf(out, "  %s\n", usage)
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "Flags:")
	if fs != nil {
		fs.PrintDefaults()
	}
	examples = slices.DeleteFunc(examples, func(v string) bool { return strings.TrimSpace(v) == "" })
	if len(examples) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Examples:")
		for _, ex := range examples {
			fmt.Fprintf(out, "  %s\n", strings.TrimSpace(ex))
		}
	}
}

func printJSONOrExit(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: JSON 序列化失败\nCause: %v\nFix: 检查输出数据结构并重试\n", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}

func exitUsageError(out cliOutputFormat, what, cause, fix string) {
	exitWithError(out, 2, what, cause, fix)
}

func exitRuntimeError(out cliOutputFormat, what, cause, fix string) {
	exitWithError(out, 1, what, cause, fix)
}

func exitWithError(out cliOutputFormat, exitCode int, what, cause, fix string) {
	what = strings.TrimSpace(what)
	cause = strings.TrimSpace(cause)
	fix = strings.TrimSpace(fix)
	if what == "" {
		what = "命令执行失败"
	}
	if cause == "" {
		cause = "未知原因"
	}
	if fix == "" {
		fix = "运行 feishu --help 查看可用命令"
	}
	fmt.Fprintf(os.Stderr, "Error: %s\nCause: %s\nFix: %s\n", what, cause, fix)
	if out == outputJSON {
		printJSONOrExit(cliErrorJSON{
			Schema:   "feishu.error.v1",
			Error:    what,
			Cause:    cause,
			Fix:      fix,
			ExitCode: exitCode,
		})
	}
	os.Exit(exitCode)
}
