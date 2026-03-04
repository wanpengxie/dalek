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

const commentTimeoutDefault = 12 * time.Second

func cmdComment(args []string) {
	if len(args) == 0 {
		printCommentUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdCommentList(args[1:])
	case "get":
		cmdCommentGet(args[1:])
	case "create":
		cmdCommentCreate(args[1:])
	case "reply":
		cmdCommentReply(args[1:])
	case "resolve":
		cmdCommentResolve(args[1:])
	case "help", "-h", "--help":
		printCommentUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 comment 子命令: %s", sub),
			"comment 仅支持 ls|get|create|reply|resolve",
			"运行 feishu comment --help 查看可用命令",
		)
	}
}

func printCommentUsage() {
	printGroupUsage("飞书评论操作", "feishu comment <command> [flags]", []string{
		"ls       列出文档评论",
		"get      获取单条评论及回复",
		"create   创建评论",
		"reply    回复评论",
		"resolve  标记评论已解决",
	})
	fmt.Fprintln(os.Stderr, "Use \"feishu comment <command> --help\" for more information.")
}

func cmdCommentList(args []string) {
	fs := flag.NewFlagSet("feishu comment ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出文档评论",
			"feishu comment ls --url <飞书链接> [--timeout 12s] [--output text|json]",
			"feishu comment ls --url https://feishu.cn/docx/xxxxxxxx",
			"feishu comment ls --token doxcxxxxxxxx --type docx -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	urlFlag := fs.String("url", "", "飞书文档链接（与 --token 二选一，自动识别类型）")
	token := fs.String("token", "", "文档 token（与 --url 二选一）")
	tokenType := fs.String("type", "docx", "文档类型（使用 --url 时自动识别）")
	timeout := fs.Duration("timeout", commentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment ls 参数解析失败", "运行 feishu comment ls --help 查看参数")
	out := parseOutputOrExit(*output, true)
	resolvedToken, resolvedType := resolveTokenAndType(out, *urlFlag, *token, *tokenType, "例如: feishu comment ls --url https://feishu.cn/docx/xxxxxxxx")
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu comment ls --timeout 12s")
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	comments, err := svc.ListComments(ctx, resolvedToken, resolvedType)
	if err != nil {
		exitRuntimeError(out, "列出评论失败", err.Error(), "检查 token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "feishu.comment.list.v1",
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
			commentTableCell(comment.Quote),
		)
	}
	_ = tw.Flush()
}

func cmdCommentGet(args []string) {
	fs := flag.NewFlagSet("feishu comment get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"获取评论详情",
			"feishu comment get --url <飞书链接> --id <comment_id> [--timeout 12s] [--output text|json]",
			"feishu comment get --url https://feishu.cn/docx/xxxxxxxx --id 7491462635118153738",
			"feishu comment get --token doxcxxxxxxxx --type docx --id 7491462635118153738 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	urlFlag := fs.String("url", "", "飞书文档链接（与 --token 二选一，自动识别类型）")
	token := fs.String("token", "", "文档 token（与 --url 二选一）")
	tokenType := fs.String("type", "docx", "文档类型（使用 --url 时自动识别）")
	commentID := fs.String("id", "", "评论 ID（必填）")
	timeout := fs.Duration("timeout", commentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment get 参数解析失败", "运行 feishu comment get --help 查看参数")
	out := parseOutputOrExit(*output, true)
	resolvedToken, resolvedType := resolveTokenAndType(out, *urlFlag, *token, *tokenType, "例如: feishu comment get --url <链接> --id <comment_id>")
	if strings.TrimSpace(*commentID) == "" {
		exitUsageError(out, "缺少必填参数 --id", "--id 不能为空", "例如: feishu comment get --url <链接> --id <comment_id>")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu comment get --timeout 12s")
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	comment, err := svc.GetComment(ctx, resolvedToken, resolvedType, strings.TrimSpace(*commentID))
	if err != nil {
		exitRuntimeError(out, "获取评论失败", err.Error(), "检查 comment_id、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "feishu.comment.get.v1",
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
			commentTableCell(reply.Content),
		)
	}
	_ = tw.Flush()
}

func cmdCommentCreate(args []string) {
	fs := flag.NewFlagSet("feishu comment create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"创建评论",
			"feishu comment create --url <飞书链接> --content \"内容\" [--timeout 12s] [--output text|json]",
			"feishu comment create --url https://feishu.cn/docx/xxxxxxxx --content \"测试评论\"",
			"feishu comment create --token doxcxxxxxxxx --type docx --content \"测试评论\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	urlFlag := fs.String("url", "", "飞书文档链接（与 --token 二选一，自动识别类型）")
	token := fs.String("token", "", "文档 token（与 --url 二选一）")
	tokenType := fs.String("type", "docx", "文档类型（使用 --url 时自动识别）")
	content := fs.String("content", "", "评论内容（必填）")
	timeout := fs.Duration("timeout", commentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment create 参数解析失败", "运行 feishu comment create --help 查看参数")
	out := parseOutputOrExit(*output, true)
	resolvedToken, resolvedType := resolveTokenAndType(out, *urlFlag, *token, *tokenType, "例如: feishu comment create --url <链接> --content \"测试评论\"")
	if strings.TrimSpace(*content) == "" {
		exitUsageError(out, "缺少必填参数 --content", "--content 不能为空", "例如: feishu comment create --url <链接> --content \"测试评论\"")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu comment create --timeout 12s")
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	comment, err := svc.CreateComment(ctx, resolvedToken, resolvedType, strings.TrimSpace(*content))
	if err != nil {
		exitRuntimeError(out, "创建评论失败", err.Error(), "检查 content、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "feishu.comment.create.v1",
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

func cmdCommentReply(args []string) {
	fs := flag.NewFlagSet("feishu comment reply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"回复评论",
			"feishu comment reply --url <飞书链接> --id <comment_id> --content \"回复\" [--timeout 12s] [--output text|json]",
			"feishu comment reply --url https://feishu.cn/docx/xxxxxxxx --id 7491462635118153738 --content \"收到\"",
			"feishu comment reply --token doxcxxxxxxxx --type docx --id 7491462635118153738 --content \"收到\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	urlFlag := fs.String("url", "", "飞书文档链接（与 --token 二选一，自动识别类型）")
	token := fs.String("token", "", "文档 token（与 --url 二选一）")
	tokenType := fs.String("type", "docx", "文档类型（使用 --url 时自动识别）")
	commentID := fs.String("id", "", "评论 ID（必填）")
	content := fs.String("content", "", "回复内容（必填）")
	timeout := fs.Duration("timeout", commentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment reply 参数解析失败", "运行 feishu comment reply --help 查看参数")
	out := parseOutputOrExit(*output, true)
	resolvedToken, resolvedType := resolveTokenAndType(out, *urlFlag, *token, *tokenType, "例如: feishu comment reply --url <链接> --id <comment_id> --content \"收到\"")
	if strings.TrimSpace(*commentID) == "" {
		exitUsageError(out, "缺少必填参数 --id", "--id 不能为空", "例如: feishu comment reply --url <链接> --id <comment_id> --content \"收到\"")
	}
	if strings.TrimSpace(*content) == "" {
		exitUsageError(out, "缺少必填参数 --content", "--content 不能为空", "例如: feishu comment reply --url <链接> --id <comment_id> --content \"收到\"")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu comment reply --timeout 12s")
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := svc.ReplyComment(ctx, resolvedToken, resolvedType, strings.TrimSpace(*commentID), strings.TrimSpace(*content)); err != nil {
		exitRuntimeError(out, "回复评论失败", err.Error(), "检查 comment_id、content、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":     "feishu.comment.reply.v1",
			"comment_id": strings.TrimSpace(*commentID),
			"replied":    true,
		})
		return
	}

	fmt.Printf("comment_id=%s\n", strings.TrimSpace(*commentID))
	fmt.Println("replied=true")
}

func cmdCommentResolve(args []string) {
	fs := flag.NewFlagSet("feishu comment resolve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"标记评论已解决",
			"feishu comment resolve --url <飞书链接> --id <comment_id> [--timeout 12s] [--output text|json]",
			"feishu comment resolve --url https://feishu.cn/docx/xxxxxxxx --id 7491462635118153738",
			"feishu comment resolve --token doxcxxxxxxxx --type docx --id 7491462635118153738 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	urlFlag := fs.String("url", "", "飞书文档链接（与 --token 二选一，自动识别类型）")
	token := fs.String("token", "", "文档 token（与 --url 二选一）")
	tokenType := fs.String("type", "docx", "文档类型（使用 --url 时自动识别）")
	commentID := fs.String("id", "", "评论 ID（必填）")
	timeout := fs.Duration("timeout", commentTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu comment resolve 参数解析失败", "运行 feishu comment resolve --help 查看参数")
	out := parseOutputOrExit(*output, true)
	resolvedToken, resolvedType := resolveTokenAndType(out, *urlFlag, *token, *tokenType, "例如: feishu comment resolve --url <链接> --id <comment_id>")
	if strings.TrimSpace(*commentID) == "" {
		exitUsageError(out, "缺少必填参数 --id", "--id 不能为空", "例如: feishu comment resolve --url <链接> --id <comment_id>")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu comment resolve --timeout 12s")
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := svc.ResolveComment(ctx, resolvedToken, resolvedType, strings.TrimSpace(*commentID)); err != nil {
		exitRuntimeError(out, "解决评论失败", err.Error(), "检查 comment_id、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":     "feishu.comment.resolve.v1",
			"comment_id": strings.TrimSpace(*commentID),
			"resolved":   true,
		})
		return
	}

	fmt.Printf("comment_id=%s\n", strings.TrimSpace(*commentID))
	fmt.Println("resolved=true")
}

func commentTableCell(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.ReplaceAll(v, "\t", " ")
	return strings.TrimSpace(v)
}
