package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const (
	feishuToolHomeEnv     = "FEISHU_TOOL_HOME"
	feishuDefaultToolHome = ".dalek/tools/feishu"
	feishuSkillSubdir     = "skill"
	feishuProjectSkillRel = ".claude/skills/feishu"
)

func feishuGlobalSkillDir() string {
	if v := os.Getenv(feishuToolHomeEnv); v != "" {
		return filepath.Join(v, feishuSkillSubdir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, feishuDefaultToolHome, feishuSkillSubdir)
}

func cmdInitSkill(args []string) {
	fs := flag.NewFlagSet("feishu init-skill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"从全局缓存安装 feishu skill 到当前项目",
			"feishu init-skill",
			"feishu init-skill",
		)
	}
	parseFlagSetOrExit(fs, args, globalOutput, "feishu init-skill 参数解析失败", "运行 feishu init-skill --help 查看参数")

	globalSkillDir := feishuGlobalSkillDir()
	if globalSkillDir == "" {
		exitRuntimeError(globalOutput,
			"无法确定全局 skill 缓存路径",
			"$HOME 环境变量未设置",
			"设置 FEISHU_TOOL_HOME 环境变量或确保 $HOME 可用",
		)
	}

	info, err := os.Stat(globalSkillDir)
	if err != nil || !info.IsDir() {
		exitRuntimeError(globalOutput,
			fmt.Sprintf("全局 skill 缓存不存在: %s", globalSkillDir),
			"尚未运行过 tools/feishu setup",
			"先在 dalek 主仓库执行 ./tools/feishu setup 建立全局种子",
		)
	}

	targetDir := feishuProjectSkillRel
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		exitRuntimeError(globalOutput,
			"创建项目 skill 目录失败",
			err.Error(),
			"检查当前目录权限",
		)
	}

	entries, err := os.ReadDir(globalSkillDir)
	if err != nil {
		exitRuntimeError(globalOutput,
			"读取全局 skill 缓存失败",
			err.Error(),
			fmt.Sprintf("检查 %s 目录权限", globalSkillDir),
		)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(globalSkillDir, entry.Name())
		dst := filepath.Join(targetDir, entry.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[feishu] warning: skip %s: %v\n", entry.Name(), err)
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "[feishu] warning: write %s failed: %v\n", dst, err)
			continue
		}
	}

	fmt.Printf("[feishu] skill installed to %s/\n", targetDir)
}
