package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/app"
)

var (
	globalHome          string
	globalProject       string
	globalAgentProvider string
	globalAgentModel    string
	globalOutput        = outputText
)

func main() {
	gfs := flag.NewFlagSet("dalek", flag.ContinueOnError)
	gfs.SetOutput(os.Stderr)
	gh := gfs.String("home", "", "dalek Home 目录（默认 ~/.dalek，env: DALEK_HOME）")
	gp := gfs.String("project", "", "项目名（可选，默认从当前目录推断）")
	gfs.StringVar(gp, "p", "", "项目名（可选，默认从当前目录推断）")
	gap := gfs.String("agent-provider", "", "全局覆盖 agent provider（codex|claude）")
	gam := gfs.String("agent-model", "", "全局覆盖 agent model")
	goOutput := gfs.String("output", string(outputText), "输出格式: text|json（默认 text）")
	gfs.StringVar(goOutput, "o", string(outputText), "输出格式: text|json（默认 text）")
	help := gfs.Bool("help", false, "显示帮助")
	helpShort := gfs.Bool("h", false, "显示帮助")
	if err := gfs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			usage(0)
		}
		exitUsageError(outputText,
			"命令参数解析失败",
			err.Error(),
			"运行 dalek --help 查看完整用法",
		)
	}
	if *help || *helpShort {
		usage(0)
	}

	globalHome = strings.TrimSpace(*gh)
	globalProject = strings.TrimSpace(*gp)
	globalAgentProvider = strings.TrimSpace(strings.ToLower(*gap))
	globalAgentModel = strings.TrimSpace(*gam)
	globalOutput = parseOutputOrExit(*goOutput, true)

	if globalAgentProvider != "" && globalAgentProvider != "codex" && globalAgentProvider != "claude" {
		exitUsageError(globalOutput,
			fmt.Sprintf("非法 --agent-provider: %s", globalAgentProvider),
			"--agent-provider 仅支持 codex 或 claude",
			"改为 --agent-provider codex 或 --agent-provider claude",
		)
	}

	rest := gfs.Args()
	if len(rest) == 0 {
		runTUI(nil)
		return
	}

	switch rest[0] {
	case "init":
		cmdInit(rest[1:])
	case "tui":
		runTUI(rest[1:])
	case "ticket":
		cmdTicket(rest[1:])
	case "note":
		cmdNote(rest[1:])
	case "task":
		cmdTask(rest[1:])
	case "manager":
		cmdManager(rest[1:])
	case "inbox":
		cmdInbox(rest[1:])
	case "merge":
		cmdMerge(rest[1:])
	case "worker":
		cmdWorker(rest[1:])
	case "agent":
		cmdAgent(rest[1:])
	case "project":
		cmdProject(rest[1:])
	case "config":
		cmdConfig(rest[1:])
	case "tmux":
		cmdTmux(rest[1:])
	case "gateway":
		cmdGateway(rest[1:])
	case "daemon":
		cmdDaemon(rest[1:])
	case "help", "-h", "--help":
		usage(0)
	default:
		if migrated, ok := legacyTopLevelCommand(strings.TrimSpace(rest[0])); ok {
			exitUsageError(globalOutput,
				fmt.Sprintf("旧命令已移除: %s", strings.TrimSpace(rest[0])),
				"CLI 已切换为 noun-verb 结构（硬切，不向后兼容）",
				fmt.Sprintf("改用：%s", migrated),
			)
		}
		exitUsageError(globalOutput,
			fmt.Sprintf("未知命令: %s", strings.TrimSpace(rest[0])),
			"命令不在当前 CLI 命令树中",
			"运行 dalek --help 查看可用命令",
		)
	}
}

func usage(code int) {
	out := os.Stderr
	fmt.Fprintln(out, "dalek - Agent-driven parallel development runtime (git worktree + tmux + sqlite)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  dalek <command> [flags]")
	fmt.Fprintln(out, "  dalek [flags]            # 默认启动 TUI")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  ticket     Ticket 生命周期管理（创建/编辑/启动/派发/中断/清理/归档）")
	fmt.Fprintln(out, "  note       Notebook 需求漏斗（add/ls/show/approve/reject/discard）")
	fmt.Fprintln(out, "  task       任务运行时观测（查看/事件）")
	fmt.Fprintln(out, "  manager    PM 调度器控制（状态/调度/暂停/恢复）")
	fmt.Fprintln(out, "  inbox      人工待处理项（查看/关闭/延后）")
	fmt.Fprintln(out, "  merge      合并队列管理（提议/审批/标记）")
	fmt.Fprintln(out, "  worker     Worker 内部命令（报告/直接运行）")
	fmt.Fprintln(out, "  agent      Agent 子任务运行（run/ls/show/cancel/logs/finish）")
	fmt.Fprintln(out, "  project    项目注册管理（添加/删除/列表）")
	fmt.Fprintln(out, "  config     统一配置管理（ls/get/set）")
	fmt.Fprintln(out, "  tmux       Tmux 基础设施管理（socket/session）")
	fmt.Fprintln(out, "  gateway    Channel Gateway（对话/通知/绑定）")
	fmt.Fprintln(out, "  daemon     Daemon 进程管理（start/stop/status/logs）")
	fmt.Fprintln(out, "  init       初始化项目（注册当前 git repo）")
	fmt.Fprintln(out, "  tui        启动交互式 TUI")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Global Flags:")
	fmt.Fprintln(out, "  --home string               dalek Home 目录 (默认 ~/.dalek, env: DALEK_HOME)")
	fmt.Fprintln(out, "  --project, -p string        项目名 (可选，默认从当前目录推断)")
	fmt.Fprintln(out, "  --output, -o string         输出格式: text|json (默认 text；查询命令支持 json)")
	fmt.Fprintln(out, "  --agent-provider string     全局覆盖 agent provider (codex|claude)")
	fmt.Fprintln(out, "  --agent-model string        全局覆盖 agent model")
	fmt.Fprintln(out, "  -h, --help                  显示帮助")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Use \"dalek <command> --help\" for more information about a command.")
	os.Exit(code)
}

func legacyTopLevelCommand(cmd string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(cmd)) {
	case "ls":
		return "dalek ticket ls", true
	case "create":
		return "dalek ticket create --title \"...\" --desc \"...\"", true
	case "edit":
		return "dalek ticket edit --ticket N --title \"...\"", true
	case "start":
		return "dalek ticket start --ticket N", true
	case "dispatch":
		return "dalek ticket dispatch --ticket N", true
	case "interrupt":
		return "dalek ticket interrupt --ticket N", true
	case "stop":
		return "dalek ticket stop --ticket N", true
	case "archive":
		return "dalek ticket archive --ticket N", true
	case "cleanup":
		return "dalek ticket cleanup --ticket N", true
	case "events":
		return "dalek ticket events --ticket N", true
	default:
		return "", false
	}
}

func mustOpenProject(homeFlag, projectFlag string) *app.Project {
	return mustOpenProjectWithOutput(globalOutput, homeFlag, projectFlag)
}

func mustOpenProjectWithOutput(out cliOutputFormat, homeFlag, projectFlag string) *app.Project {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		exitRuntimeError(out,
			"无法解析 dalek Home 目录",
			err.Error(),
			"通过 --home 指定有效目录，或设置 DALEK_HOME",
		)
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out,
			"打开 Home 失败",
			err.Error(),
			"检查 Home 目录权限与文件完整性后重试",
		)
	}

	proj := strings.TrimSpace(projectFlag)
	if proj != "" {
		p, oerr := h.OpenProjectByName(proj)
		if oerr != nil {
			exitRuntimeError(out,
				fmt.Sprintf("打开项目 %q 失败", proj),
				oerr.Error(),
				"运行 dalek project ls 确认项目名，或修正 --project",
			)
		}
		if aerr := p.ApplyAgentProviderModel(globalAgentProvider, globalAgentModel); aerr != nil {
			exitRuntimeError(out,
				"应用全局 agent 配置失败",
				aerr.Error(),
				"检查 --agent-provider/--agent-model 参数后重试",
			)
		}
		return p
	}

	wd, _ := os.Getwd()
	p, oerr := h.OpenProjectFromDir(wd)
	if oerr != nil {
		exitRuntimeError(out,
			"无法识别当前目录的项目",
			oerr.Error(),
			"使用 --project 指定项目名，或切换到已注册项目目录，或先运行 dalek init",
		)
	}
	if aerr := p.ApplyAgentProviderModel(globalAgentProvider, globalAgentModel); aerr != nil {
		exitRuntimeError(out,
			"应用全局 agent 配置失败",
			aerr.Error(),
			"检查 --agent-provider/--agent-model 参数后重试",
		)
	}
	return p
}

func trimOneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func projectCtx(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), timeout)
}
