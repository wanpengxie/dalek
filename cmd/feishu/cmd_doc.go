package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"dalek/internal/services/feishudoc"
)

const docTimeoutDefault = 12 * time.Second

func cmdDoc(args []string) {
	if len(args) == 0 {
		printDocUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "create":
		cmdDocCreate(args[1:])
	case "read":
		cmdDocRead(args[1:])
	case "write":
		cmdDocWrite(args[1:])
	case "ls":
		cmdDocList(args[1:])
	case "help", "-h", "--help":
		printDocUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 doc 子命令: %s", sub),
			"doc 仅支持 create|read|write|ls",
			"运行 feishu doc --help 查看可用命令",
		)
	}
}

func printDocUsage() {
	printGroupUsage("飞书文档操作", "feishu doc <command> [flags]", []string{
		"create   创建文档",
		"read     读取文档 Markdown 内容（含格式）",
		"write    向文档追加 Markdown/文本内容",
		"ls       列出目录下文档",
	})
	fmt.Fprintln(os.Stderr, "Use \"feishu doc <command> --help\" for more information.")
}

func cmdDocCreate(args []string) {
	fs := flag.NewFlagSet("feishu doc create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"创建飞书文档",
			"feishu doc create [--title <title>] [--folder <token>] [--timeout 12s] [--output text|json]",
			"feishu doc create --title \"迭代报告\"",
			"feishu doc create --title \"需求整理\" --folder fldxxxxxxxx -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	title := fs.String("title", "", "文档标题（可选）")
	folder := fs.String("folder", "", "目标文件夹 token（可选）")
	timeout := fs.Duration("timeout", docTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu doc create 参数解析失败", "运行 feishu doc create --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu doc create --timeout 12s")
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

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
			"schema":   "feishu.doc.create.v1",
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

func cmdDocRead(args []string) {
	fs := flag.NewFlagSet("feishu doc read", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"读取飞书文档内容并保存为 Markdown 文件",
			"feishu doc read --url <飞书链接> [output.md] [--timeout 12s] [--output text|json]",
			"feishu doc read --url https://feishu.cn/docx/xxxxxxxx",
			"feishu doc read --url https://feishu.cn/docx/xxxxxxxx report.md",
			"feishu doc read --doc doxcxxxxxxxx -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	urlFlag := fs.String("url", "", "飞书文档链接（与 --doc 二选一）")
	docID := fs.String("doc", "", "文档 ID（与 --url 二选一）")
	timeout := fs.Duration("timeout", docTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu doc read 参数解析失败", "运行 feishu doc read --help 查看参数")
	out := parseOutputOrExit(*output, true)
	resolvedDocID := resolveDocID(out, *urlFlag, *docID, "例如: feishu doc read --url https://feishu.cn/docx/xxxxxxxx")
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu doc read --timeout 12s")
	}

	// positional arg: output file
	outFile := ""
	if rest := fs.Args(); len(rest) > 0 {
		outFile = strings.TrimSpace(rest[0])
	}

	readOptions := feishudoc.ReadDocumentOptions{}
	if outFile != "" {
		imageDir := filepath.Join(filepath.Dir(outFile), "images")
		if err := os.MkdirAll(imageDir, 0o755); err != nil {
			exitRuntimeError(out, "创建图片目录失败", err.Error(), fmt.Sprintf("检查目录权限: %s", imageDir))
		}
		readOptions.ImagesDir = imageDir
		readOptions.ImagePathPrefix = "./images"
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.ReadDocument(ctx, resolvedDocID, readOptions)
	if err != nil {
		exitRuntimeError(out, "读取飞书文档失败", err.Error(), "检查 document_id 与飞书权限后重试")
	}

	if out == outputJSON {
		payload := map[string]any{
			"schema":   "feishu.doc.read.v1",
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

	// write to file if specified, otherwise stdout
	if outFile != "" {
		if err := os.WriteFile(outFile, []byte(result.Content), 0644); err != nil {
			exitRuntimeError(out, "写入文件失败", err.Error(), "检查文件路径与权限")
		}
		fmt.Fprintf(os.Stderr, "已保存到 %s\n", outFile)
		if result.Document.URL != "" {
			fmt.Fprintf(os.Stderr, "url=%s\n", result.Document.URL)
		}
		return
	}

	fmt.Print(result.Content)
}

func cmdDocWrite(args []string) {
	fs := flag.NewFlagSet("feishu doc write", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"向文档追加 Markdown 内容（从文件、stdin 或 --content 读取）",
			"feishu doc write --url <飞书链接> <input.md> [--timeout 12s] [--output text|json]",
			"feishu doc write --url https://feishu.cn/docx/xxxxxxxx notes.md",
			"cat report.md | feishu doc write --url https://feishu.cn/docx/xxxxxxxx -",
			"feishu doc write --url https://feishu.cn/docx/xxxxxxxx --content \"简短内容\"",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	urlFlag := fs.String("url", "", "飞书文档链接（与 --doc 二选一）")
	docID := fs.String("doc", "", "文档 ID（与 --url 二选一）")
	contentFlag := fs.String("content", "", "直接传入内容（与文件参数二选一）")
	timeout := fs.Duration("timeout", docTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu doc write 参数解析失败", "运行 feishu doc write --help 查看参数")
	out := parseOutputOrExit(*output, true)
	resolvedDocID := resolveDocID(out, *urlFlag, *docID, "例如: feishu doc write --url https://feishu.cn/docx/xxxxxxxx notes.md")
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu doc write --timeout 12s")
	}

	// resolve content: positional file arg > --content > stdin detection
	var content string
	inFile := ""
	if rest := fs.Args(); len(rest) > 0 {
		inFile = strings.TrimSpace(rest[0])
	}

	switch {
	case inFile == "-":
		// read from stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			exitRuntimeError(out, "读取 stdin 失败", err.Error(), "检查管道输入")
		}
		content = string(data)
	case inFile != "":
		// read from file
		data, err := os.ReadFile(inFile)
		if err != nil {
			exitRuntimeError(out, "读取文件失败", err.Error(), fmt.Sprintf("检查文件是否存在: %s", inFile))
		}
		content = string(data)
	case strings.TrimSpace(*contentFlag) != "":
		content = *contentFlag
	default:
		exitUsageError(out,
			"缺少写入内容",
			"必须提供文件参数、stdin（-）或 --content",
			"例如: feishu doc write --url <链接> notes.md",
		)
	}

	if strings.TrimSpace(content) == "" {
		exitUsageError(out, "写入内容为空", "文件或 --content 内容为空", "检查输入文件内容")
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.WriteDocument(ctx, feishudoc.WriteDocumentInput{
		DocumentID: resolvedDocID,
		Content:    content,
	})
	if err != nil {
		exitRuntimeError(out, "写入飞书文档失败", err.Error(), "检查文档权限、内容格式后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "feishu.doc.write.v1",
			"write":  result,
		})
		return
	}
	fmt.Printf("document_id=%s\n", result.DocumentID)
	fmt.Printf("document_revision=%d\n", result.DocumentRevision)
	fmt.Printf("added_blocks=%d\n", len(result.AddedBlockIDs))
}

func cmdDocList(args []string) {
	fs := flag.NewFlagSet("feishu doc ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出目录中的文档",
			"feishu doc ls [--folder <token>] [--page-size 50] [--page-token <token>] [--timeout 12s] [--output text|json]",
			"feishu doc ls",
			"feishu doc ls --folder fldxxxxxxxx --page-size 20 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	folder := fs.String("folder", "", "文件夹 token（可选）")
	pageSize := fs.Int("page-size", 50, "分页大小（1-200）")
	pageToken := fs.String("page-token", "", "分页 token（可选）")
	timeout := fs.Duration("timeout", docTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu doc ls 参数解析失败", "运行 feishu doc ls --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *pageSize <= 0 {
		exitUsageError(out, "非法参数 --page-size", "--page-size 必须大于 0", "例如: feishu doc ls --page-size 50")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: feishu doc ls --timeout 12s")
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

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
			"schema":          "feishu.doc.list.v1",
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
