package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"dalek/internal/services/feishudoc"
)

const feishuWikiTimeoutDefault = 12 * time.Second

func cmdFeishuWiki(args []string) {
	if len(args) == 0 {
		printFeishuWikiUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdFeishuWikiList(args[1:])
	case "nodes":
		cmdFeishuWikiNodes(args[1:])
	case "create":
		cmdFeishuWikiCreate(args[1:])
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
		"ls       列出可访问的知识空间",
		"nodes    列出知识空间节点",
		"create   创建知识空间节点",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek feishu wiki <command> --help\" for more information.")
}

func cmdFeishuWikiList(args []string) {
	fs := flag.NewFlagSet("feishu wiki ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出知识空间",
			"dalek feishu wiki ls [--page-size 50] [--page-token <token>] [--timeout 12s] [--output text|json]",
			"dalek feishu wiki ls",
			"dalek feishu wiki ls --page-size 20 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	pageSize := fs.Int("page-size", 50, "分页大小（1-200）")
	pageToken := fs.String("page-token", "", "分页 token（可选）")
	timeout := fs.Duration("timeout", feishuWikiTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu wiki ls 参数解析失败", "运行 dalek feishu wiki ls --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *pageSize <= 0 {
		exitUsageError(out, "非法参数 --page-size", "--page-size 必须大于 0", "例如: dalek feishu wiki ls --page-size 50")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu wiki ls --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.ListWikiSpaces(ctx, feishudoc.ListWikiSpacesInput{
		PageToken: strings.TrimSpace(*pageToken),
		PageSize:  *pageSize,
	})
	if err != nil {
		exitRuntimeError(out, "列出知识空间失败", err.Error(), "检查飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":          "dalek.feishu.wiki.list.v1",
			"spaces":          result.Spaces,
			"has_more":        result.HasMore,
			"next_page_token": result.NextPageToken,
		})
		return
	}

	if len(result.Spaces) == 0 {
		fmt.Println("(empty)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SPACE_ID\tNAME\tTYPE\tVISIBILITY\tOPEN_SHARING")
	for _, space := range result.Spaces {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", space.SpaceID, space.Name, space.SpaceType, space.Visibility, space.OpenSharing)
	}
	_ = tw.Flush()
}

func cmdFeishuWikiNodes(args []string) {
	fs := flag.NewFlagSet("feishu wiki nodes", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出知识节点",
			"dalek feishu wiki nodes --space <space_id> [--parent <node_token>] [--page-size 50] [--page-token <token>] [--timeout 12s] [--output text|json]",
			"dalek feishu wiki nodes --space 6704147935988285963",
			"dalek feishu wiki nodes --space 6704147935988285963 --parent wikcxxxx -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	spaceID := fs.String("space", "", "知识空间 ID（必填）")
	parent := fs.String("parent", "", "父节点 token（可选）")
	pageSize := fs.Int("page-size", 50, "分页大小（1-200）")
	pageToken := fs.String("page-token", "", "分页 token（可选）")
	timeout := fs.Duration("timeout", feishuWikiTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu wiki nodes 参数解析失败", "运行 dalek feishu wiki nodes --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*spaceID) == "" {
		exitUsageError(out, "缺少必填参数 --space", "--space 不能为空", "例如: dalek feishu wiki nodes --space 6704147935988285963")
	}
	if *pageSize <= 0 {
		exitUsageError(out, "非法参数 --page-size", "--page-size 必须大于 0", "例如: dalek feishu wiki nodes --page-size 50")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu wiki nodes --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.ListWikiNodes(ctx, feishudoc.ListWikiNodesInput{
		SpaceID:         strings.TrimSpace(*spaceID),
		ParentNodeToken: strings.TrimSpace(*parent),
		PageToken:       strings.TrimSpace(*pageToken),
		PageSize:        *pageSize,
	})
	if err != nil {
		exitRuntimeError(out, "列出知识节点失败", err.Error(), "检查 space_id 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":          "dalek.feishu.wiki.nodes.v1",
			"space_id":        strings.TrimSpace(*spaceID),
			"nodes":           result.Nodes,
			"has_more":        result.HasMore,
			"next_page_token": result.NextPageToken,
		})
		return
	}

	if len(result.Nodes) == 0 {
		fmt.Println("(empty)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE_TOKEN\tTITLE\tOBJ_TYPE\tOBJ_TOKEN\tPARENT")
	for _, node := range result.Nodes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", node.NodeToken, node.Title, node.ObjType, node.ObjToken, node.ParentNodeToken)
	}
	_ = tw.Flush()
}

func cmdFeishuWikiCreate(args []string) {
	fs := flag.NewFlagSet("feishu wiki create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"创建知识节点",
			"dalek feishu wiki create --space <space_id> [--title <title>] [--obj-type docx] [--obj-token <token>] [--parent <node_token>] [--timeout 12s] [--output text|json]",
			"dalek feishu wiki create --space 6704147935988285963 --title \"项目周报\"",
			"dalek feishu wiki create --space 6704147935988285963 --obj-type docx --obj-token doxcxxxxxxxx -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	spaceID := fs.String("space", "", "知识空间 ID（必填）")
	title := fs.String("title", "", "节点标题（创建新文档节点时建议填写）")
	objType := fs.String("obj-type", "docx", "对象类型（默认 docx）")
	objToken := fs.String("obj-token", "", "已存在对象 token（可选）")
	parent := fs.String("parent", "", "父节点 token（可选）")
	timeout := fs.Duration("timeout", feishuWikiTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu wiki create 参数解析失败", "运行 dalek feishu wiki create --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*spaceID) == "" {
		exitUsageError(out, "缺少必填参数 --space", "--space 不能为空", "例如: dalek feishu wiki create --space 6704147935988285963 --title \"项目周报\"")
	}
	if strings.TrimSpace(*title) == "" && strings.TrimSpace(*objToken) == "" {
		exitUsageError(out, "缺少必填参数 --title/--obj-token", "创建节点时必须至少提供 --title 或 --obj-token", "例如: dalek feishu wiki create --space <id> --title \"项目周报\"")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu wiki create --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	node, err := svc.CreateWikiNode(ctx, feishudoc.CreateWikiNodeInput{
		SpaceID:         strings.TrimSpace(*spaceID),
		ParentNodeToken: strings.TrimSpace(*parent),
		ObjType:         strings.TrimSpace(*objType),
		ObjToken:        strings.TrimSpace(*objToken),
		Title:           strings.TrimSpace(*title),
	})
	if err != nil {
		exitRuntimeError(out, "创建知识节点失败", err.Error(), "检查参数、space_id 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.feishu.wiki.create.v1",
			"node":   node,
		})
		return
	}

	fmt.Printf("space_id=%s\n", node.SpaceID)
	fmt.Printf("node_token=%s\n", node.NodeToken)
	if node.Title != "" {
		fmt.Printf("title=%s\n", node.Title)
	}
	if node.ObjType != "" {
		fmt.Printf("obj_type=%s\n", node.ObjType)
	}
	if node.ObjToken != "" {
		fmt.Printf("obj_token=%s\n", node.ObjToken)
	}
	if node.ParentNodeToken != "" {
		fmt.Printf("parent_node_token=%s\n", node.ParentNodeToken)
	}
}
