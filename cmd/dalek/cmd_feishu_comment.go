package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

const feishuCommentTimeoutDefault = 12 * time.Second

func cmdFeishuComment(args []string) {
	if len(args) == 0 {
		printFeishuCommentUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdFeishuCommentList(args[1:])
	case "get":
		cmdFeishuCommentGet(args[1:])
	case "create":
		cmdFeishuCommentCreate(args[1:])
	case "reply":
		cmdFeishuCommentReply(args[1:])
	case "resolve":
		cmdFeishuCommentResolve(args[1:])
	case "help", "-h", "--help":
		printFeishuCommentUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 feishu comment 子命令: %s", sub),
			"feishu comment 仅支持 ls|get|create|reply|resolve",
			"运行 dalek feishu comment --help 查看可用命令",
		)
	}
}

func printFeishuCommentUsage() {
	printGroupUsage("飞书评论管理", "dalek feishu comment <command> [flags]", []string{
		"ls       列出文档评论",
		"get      获取单条评论及回复",
		"create   创建评论",
		"reply    回复评论",
		"resolve  标记评论已解决",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek feishu comment <command> --help\" for more information.")
}

func cmdFeishuCommentList(args []string) {
	fs := flag.NewFlagSet("feishu comment ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出文档评论",
			"dalek feishu comment ls --token <token> [--type docx] [--timeout 12s] [--output text|json]",
			"dalek feishu comment ls --token doxcxxxxxxxx",
			"dalek feishu comment ls --token doxcxxxxxxxx --type docx -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	token := fs.String("token", "", "文档 token（必填）")
	tokenType := fs.String("type", "docx", "文档类型（默认 docx）")
	timeout := fs.Duration("timeout", feishuCommentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment ls 参数解析失败", "运行 dalek feishu comment ls --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*token) == "" {
		exitUsageError(out, "缺少必填参数 --token", "--token 不能为空", "例如: dalek feishu comment ls --token doxcxxxxxxxx")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu comment ls --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	comments, err := svc.ListComments(ctx, strings.TrimSpace(*token), strings.TrimSpace(*tokenType))
	if err != nil {
		exitRuntimeError(out, "列出评论失败", err.Error(), "检查 token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "dalek.feishu.comment.list.v1",
			"comments": comments,
		})
		return
	}

	if len(comments) == 0 {
		fmt.Println("(empty)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "COMMENT_ID\tSOLVED\tREPLIES\tCREATE_TIME\tUPDATE_TIME\tQUOTE")
	for _, comment := range comments {
		fmt.Fprintf(tw, "%s\t%t\t%d\t%d\t%d\t%s\n",
			comment.CommentID,
			comment.IsSolved,
			len(comment.Replies),
			comment.CreateTime,
			comment.UpdateTime,
			feishuCommentTableCell(comment.Quote),
		)
	}
	_ = tw.Flush()
}

func cmdFeishuCommentGet(args []string) {
	fs := flag.NewFlagSet("feishu comment get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"获取评论详情",
			"dalek feishu comment get --token <token> --id <comment_id> [--type docx] [--timeout 12s] [--output text|json]",
			"dalek feishu comment get --token doxcxxxxxxxx --id 7491462635118153738",
			"dalek feishu comment get --token doxcxxxxxxxx --id 7491462635118153738 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	token := fs.String("token", "", "文档 token（必填）")
	tokenType := fs.String("type", "docx", "文档类型（默认 docx）")
	commentID := fs.String("id", "", "评论 ID（必填）")
	timeout := fs.Duration("timeout", feishuCommentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment get 参数解析失败", "运行 dalek feishu comment get --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*token) == "" {
		exitUsageError(out, "缺少必填参数 --token", "--token 不能为空", "例如: dalek feishu comment get --token doxcxxxxxxxx --id <comment_id>")
	}
	if strings.TrimSpace(*commentID) == "" {
		exitUsageError(out, "缺少必填参数 --id", "--id 不能为空", "例如: dalek feishu comment get --token doxcxxxxxxxx --id <comment_id>")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu comment get --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	comment, err := svc.GetComment(ctx, strings.TrimSpace(*token), strings.TrimSpace(*tokenType), strings.TrimSpace(*commentID))
	if err != nil {
		exitRuntimeError(out, "获取评论失败", err.Error(), "检查 comment_id、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.feishu.comment.get.v1",
			"comment": comment,
		})
		return
	}

	fmt.Printf("comment_id=%s\n", comment.CommentID)
	fmt.Printf("is_solved=%t\n", comment.IsSolved)
	fmt.Printf("create_time=%d\n", comment.CreateTime)
	fmt.Printf("update_time=%d\n", comment.UpdateTime)
	if comment.Quote != "" {
		fmt.Printf("quote=%s\n", comment.Quote)
	}
	fmt.Printf("replies=%d\n", len(comment.Replies))
	if len(comment.Replies) == 0 {
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "REPLY_ID\tUSER_ID\tCREATE_TIME\tUPDATE_TIME\tCONTENT")
	for _, reply := range comment.Replies {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n",
			reply.ReplyID,
			reply.UserID,
			reply.CreateTime,
			reply.UpdateTime,
			feishuCommentTableCell(reply.Content),
		)
	}
	_ = tw.Flush()
}

func cmdFeishuCommentCreate(args []string) {
	fs := flag.NewFlagSet("feishu comment create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"创建评论",
			"dalek feishu comment create --token <token> --content \"内容\" [--type docx] [--timeout 12s] [--output text|json]",
			"dalek feishu comment create --token doxcxxxxxxxx --content \"测试评论\"",
			"dalek feishu comment create --token doxcxxxxxxxx --content \"测试评论\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	token := fs.String("token", "", "文档 token（必填）")
	tokenType := fs.String("type", "docx", "文档类型（默认 docx）")
	content := fs.String("content", "", "评论内容（必填）")
	timeout := fs.Duration("timeout", feishuCommentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment create 参数解析失败", "运行 dalek feishu comment create --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*token) == "" {
		exitUsageError(out, "缺少必填参数 --token", "--token 不能为空", "例如: dalek feishu comment create --token doxcxxxxxxxx --content \"测试评论\"")
	}
	if strings.TrimSpace(*content) == "" {
		exitUsageError(out, "缺少必填参数 --content", "--content 不能为空", "例如: dalek feishu comment create --token doxcxxxxxxxx --content \"测试评论\"")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu comment create --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	comment, err := svc.CreateComment(ctx, strings.TrimSpace(*token), strings.TrimSpace(*tokenType), strings.TrimSpace(*content))
	if err != nil {
		exitRuntimeError(out, "创建评论失败", err.Error(), "检查 content、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.feishu.comment.create.v1",
			"comment": comment,
		})
		return
	}

	fmt.Printf("comment_id=%s\n", comment.CommentID)
	fmt.Printf("is_solved=%t\n", comment.IsSolved)
	fmt.Printf("create_time=%d\n", comment.CreateTime)
	fmt.Printf("update_time=%d\n", comment.UpdateTime)
	if comment.Quote != "" {
		fmt.Printf("quote=%s\n", comment.Quote)
	}
	fmt.Printf("replies=%d\n", len(comment.Replies))
}

func cmdFeishuCommentReply(args []string) {
	fs := flag.NewFlagSet("feishu comment reply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"回复评论",
			"dalek feishu comment reply --token <token> --id <comment_id> --content \"回复\" [--type docx] [--timeout 12s] [--output text|json]",
			"dalek feishu comment reply --token doxcxxxxxxxx --id 7491462635118153738 --content \"收到\"",
			"dalek feishu comment reply --token doxcxxxxxxxx --id 7491462635118153738 --content \"收到\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	token := fs.String("token", "", "文档 token（必填）")
	tokenType := fs.String("type", "docx", "文档类型（默认 docx）")
	commentID := fs.String("id", "", "评论 ID（必填）")
	content := fs.String("content", "", "回复内容（必填）")
	timeout := fs.Duration("timeout", feishuCommentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment reply 参数解析失败", "运行 dalek feishu comment reply --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*token) == "" {
		exitUsageError(out, "缺少必填参数 --token", "--token 不能为空", "例如: dalek feishu comment reply --token doxcxxxxxxxx --id <comment_id> --content \"收到\"")
	}
	if strings.TrimSpace(*commentID) == "" {
		exitUsageError(out, "缺少必填参数 --id", "--id 不能为空", "例如: dalek feishu comment reply --token doxcxxxxxxxx --id <comment_id> --content \"收到\"")
	}
	if strings.TrimSpace(*content) == "" {
		exitUsageError(out, "缺少必填参数 --content", "--content 不能为空", "例如: dalek feishu comment reply --token doxcxxxxxxxx --id <comment_id> --content \"收到\"")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu comment reply --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := svc.ReplyComment(ctx, strings.TrimSpace(*token), strings.TrimSpace(*tokenType), strings.TrimSpace(*commentID), strings.TrimSpace(*content)); err != nil {
		exitRuntimeError(out, "回复评论失败", err.Error(), "检查 comment_id、content、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":     "dalek.feishu.comment.reply.v1",
			"comment_id": strings.TrimSpace(*commentID),
			"replied":    true,
		})
		return
	}

	fmt.Printf("comment_id=%s\n", strings.TrimSpace(*commentID))
	fmt.Println("replied=true")
}

func cmdFeishuCommentResolve(args []string) {
	fs := flag.NewFlagSet("feishu comment resolve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"标记评论已解决",
			"dalek feishu comment resolve --token <token> --id <comment_id> [--type docx] [--timeout 12s] [--output text|json]",
			"dalek feishu comment resolve --token doxcxxxxxxxx --id 7491462635118153738",
			"dalek feishu comment resolve --token doxcxxxxxxxx --id 7491462635118153738 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	token := fs.String("token", "", "文档 token（必填）")
	tokenType := fs.String("type", "docx", "文档类型（默认 docx）")
	commentID := fs.String("id", "", "评论 ID（必填）")
	timeout := fs.Duration("timeout", feishuCommentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment resolve 参数解析失败", "运行 dalek feishu comment resolve --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*token) == "" {
		exitUsageError(out, "缺少必填参数 --token", "--token 不能为空", "例如: dalek feishu comment resolve --token doxcxxxxxxxx --id <comment_id>")
	}
	if strings.TrimSpace(*commentID) == "" {
		exitUsageError(out, "缺少必填参数 --id", "--id 不能为空", "例如: dalek feishu comment resolve --token doxcxxxxxxxx --id <comment_id>")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu comment resolve --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := svc.ResolveComment(ctx, strings.TrimSpace(*token), strings.TrimSpace(*tokenType), strings.TrimSpace(*commentID)); err != nil {
		exitRuntimeError(out, "解决评论失败", err.Error(), "检查 comment_id、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":     "dalek.feishu.comment.resolve.v1",
			"comment_id": strings.TrimSpace(*commentID),
			"resolved":   true,
		})
		return
	}

	fmt.Printf("comment_id=%s\n", strings.TrimSpace(*commentID))
	fmt.Println("resolved=true")
}

func feishuCommentTableCell(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.ReplaceAll(v, "\t", " ")
	return strings.TrimSpace(v)
}
