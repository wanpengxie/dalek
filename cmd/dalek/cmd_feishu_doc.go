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

const feishuDocTimeoutDefault = 12 * time.Second

func cmdFeishuDoc(args []string) {
	if len(args) == 0 {
		printFeishuDocUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "create":
		cmdFeishuDocCreate(args[1:])
	case "read":
		cmdFeishuDocRead(args[1:])
	case "write":
		cmdFeishuDocWrite(args[1:])
	case "ls":
		cmdFeishuDocList(args[1:])
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
		"create   创建文档",
		"read     读取文档 Markdown 内容（含格式）",
		"write    向文档追加 Markdown/文本内容",
		"ls       列出目录下文档",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek feishu doc <command> --help\" for more information.")
}

func cmdFeishuDocCreate(args []string) {
	fs := flag.NewFlagSet("feishu doc create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"创建飞书文档",
			"dalek feishu doc create [--title <title>] [--folder <token>] [--timeout 12s] [--output text|json]",
			"dalek feishu doc create --title \"迭代报告\"",
			"dalek feishu doc create --title \"需求整理\" --folder fldxxxxxxxx -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	title := fs.String("title", "", "文档标题（可选）")
	folder := fs.String("folder", "", "目标文件夹 token（可选）")
	timeout := fs.Duration("timeout", feishuDocTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu doc create 参数解析失败", "运行 dalek feishu doc create --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu doc create --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	doc, err := svc.CreateDocument(ctx, feishudoc.CreateDocumentInput{
		Title:       strings.TrimSpace(*title),
		FolderToken: strings.TrimSpace(*folder),
	})
	if err != nil {
		exitRuntimeError(out, "创建飞书文档失败", err.Error(), "检查 folder token 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "dalek.feishu.doc.create.v1",
			"document": doc,
		})
		return
	}
	fmt.Printf("document_id=%s\n", doc.DocumentID)
	fmt.Printf("title=%s\n", doc.Title)
	if doc.URL != "" {
		fmt.Printf("url=%s\n", doc.URL)
	}
}

func cmdFeishuDocRead(args []string) {
	fs := flag.NewFlagSet("feishu doc read", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"读取飞书文档 Markdown 内容",
			"dalek feishu doc read --doc <document_id> [--timeout 12s] [--output text|json]",
			"dalek feishu doc read --doc doxcxxxxxxxx",
			"dalek feishu doc read --doc doxcxxxxxxxx -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	docID := fs.String("doc", "", "文档 ID（必填）")
	timeout := fs.Duration("timeout", feishuDocTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu doc read 参数解析失败", "运行 dalek feishu doc read --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*docID) == "" {
		exitUsageError(out, "缺少必填参数 --doc", "--doc 不能为空", "例如: dalek feishu doc read --doc doxcxxxxxxxx")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu doc read --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.ReadDocument(ctx, strings.TrimSpace(*docID), feishudoc.ReadDocumentOptions{})
	if err != nil {
		exitRuntimeError(out, "读取飞书文档失败", err.Error(), "检查 document_id 与飞书权限后重试")
	}

	if out == outputJSON {
		payload := map[string]any{
			"schema":   "dalek.feishu.doc.read.v1",
			"document": result.Document,
			"content":  result.Content,
		}
		if len(result.Warnings) > 0 {
			payload["warnings"] = result.Warnings
		}
		printJSONOrExit(payload)
		return
	}

	for _, warning := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}

	fmt.Printf("document_id=%s\n", result.Document.DocumentID)
	if result.Document.Title != "" {
		fmt.Printf("title=%s\n", result.Document.Title)
	}
	if result.Document.URL != "" {
		fmt.Printf("url=%s\n", result.Document.URL)
	}
	fmt.Println("content:")
	fmt.Println(result.Content)
}

func cmdFeishuDocWrite(args []string) {
	fs := flag.NewFlagSet("feishu doc write", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"向文档追加内容",
			"dalek feishu doc write --doc <document_id> --content <markdown> [--timeout 12s] [--output text|json]",
			"dalek feishu doc write --doc doxcxxxxxxxx --content \"## 今日进展\\n- 已完成 auth\"",
			"dalek feishu doc write --doc doxcxxxxxxxx --content \"hello\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	docID := fs.String("doc", "", "文档 ID（必填）")
	content := fs.String("content", "", "要追加的 Markdown/文本内容（必填）")
	timeout := fs.Duration("timeout", feishuDocTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu doc write 参数解析失败", "运行 dalek feishu doc write --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*docID) == "" {
		exitUsageError(out, "缺少必填参数 --doc", "--doc 不能为空", "例如: dalek feishu doc write --doc doxcxxxxxxxx --content \"...\"")
	}
	if strings.TrimSpace(*content) == "" {
		exitUsageError(out, "缺少必填参数 --content", "--content 不能为空", "例如: dalek feishu doc write --doc doxcxxxxxxxx --content \"...\"")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu doc write --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.WriteDocument(ctx, feishudoc.WriteDocumentInput{
		DocumentID: strings.TrimSpace(*docID),
		Content:    *content,
	})
	if err != nil {
		exitRuntimeError(out, "写入飞书文档失败", err.Error(), "检查文档权限、内容格式后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.feishu.doc.write.v1",
			"write":  result,
		})
		return
	}
	fmt.Printf("document_id=%s\n", result.DocumentID)
	fmt.Printf("document_revision=%d\n", result.DocumentRevision)
	fmt.Printf("added_blocks=%d\n", len(result.AddedBlockIDs))
}

func cmdFeishuDocList(args []string) {
	fs := flag.NewFlagSet("feishu doc ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出目录中的文档",
			"dalek feishu doc ls [--folder <token>] [--page-size 50] [--page-token <token>] [--timeout 12s] [--output text|json]",
			"dalek feishu doc ls",
			"dalek feishu doc ls --folder fldxxxxxxxx --page-size 20 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	folder := fs.String("folder", "", "文件夹 token（可选）")
	pageSize := fs.Int("page-size", 50, "分页大小（1-200）")
	pageToken := fs.String("page-token", "", "分页 token（可选）")
	timeout := fs.Duration("timeout", feishuDocTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu doc ls 参数解析失败", "运行 dalek feishu doc ls --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *pageSize <= 0 {
		exitUsageError(out, "非法参数 --page-size", "--page-size 必须大于 0", "例如: dalek feishu doc ls --page-size 50")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu doc ls --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.ListDocuments(ctx, feishudoc.ListDocumentsInput{
		FolderToken: strings.TrimSpace(*folder),
		PageToken:   strings.TrimSpace(*pageToken),
		PageSize:    *pageSize,
	})
	if err != nil {
		exitRuntimeError(out, "列出飞书文档失败", err.Error(), "检查 folder token 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":          "dalek.feishu.doc.list.v1",
			"documents":       result.Documents,
			"has_more":        result.HasMore,
			"next_page_token": result.NextPageToken,
		})
		return
	}

	if len(result.Documents) == 0 {
		fmt.Println("(empty)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "DOCUMENT_ID\tTITLE\tURL")
	for _, doc := range result.Documents {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", doc.DocumentID, doc.Title, doc.URL)
	}
	_ = tw.Flush()
}
