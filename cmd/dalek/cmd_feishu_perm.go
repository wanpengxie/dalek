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

const feishuPermTimeoutDefault = 12 * time.Second

func cmdFeishuPerm(args []string) {
	if len(args) == 0 {
		printFeishuPermUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "share":
		cmdFeishuPermShare(args[1:])
	case "add":
		cmdFeishuPermAdd(args[1:])
	case "ls":
		cmdFeishuPermList(args[1:])
	case "help", "-h", "--help":
		printFeishuPermUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 feishu perm 子命令: %s", sub),
			"feishu perm 仅支持 share|add|ls",
			"运行 dalek feishu perm --help 查看可用命令",
		)
	}
}

func printFeishuPermUsage() {
	printGroupUsage("飞书权限管理", "dalek feishu perm <command> [flags]", []string{
		"share    更新公开分享设置并返回可访问链接",
		"add      添加协作者",
		"ls       列出协作者权限",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek feishu perm <command> --help\" for more information.")
}

func cmdFeishuPermShare(args []string) {
	fs := flag.NewFlagSet("feishu perm share", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"更新公开分享设置",
			"dalek feishu perm share --token <token> [--type docx] [--link-share tenant_editable] [--external-access] [--timeout 12s] [--output text|json]",
			"dalek feishu perm share --token doxcxxxxxxxx",
			"dalek feishu perm share --token doxcxxxxxxxx --link-share anyone_editable --external-access -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	token := fs.String("token", "", "文档 token（必填）")
	tokenType := fs.String("type", "docx", "文档类型（默认 docx）")
	linkShare := fs.String("link-share", "tenant_editable", "链接分享策略（tenant_readable|tenant_editable|anyone_readable|anyone_editable|closed）")
	externalAccess := fs.Bool("external-access", false, "允许内容被分享到组织外")
	securityEntity := fs.String("security-entity", "", "复制/导出权限范围（anyone_can_view|anyone_can_edit|only_full_access）")
	commentEntity := fs.String("comment-entity", "", "评论权限范围（anyone_can_view|anyone_can_edit）")
	shareEntity := fs.String("share-entity", "", "管理协作者权限范围（anyone|same_tenant|only_full_access）")
	inviteExternal := fs.Bool("invite-external", false, "允许非可管理权限用户分享到组织外")
	timeout := fs.Duration("timeout", feishuPermTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu perm share 参数解析失败", "运行 dalek feishu perm share --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*token) == "" {
		exitUsageError(out, "缺少必填参数 --token", "--token 不能为空", "例如: dalek feishu perm share --token doxcxxxxxxxx")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu perm share --timeout 12s")
	}

	var externalAccessPtr *bool
	if flagProvided(fs, "external-access") {
		externalAccessPtr = externalAccess
	}
	var inviteExternalPtr *bool
	if flagProvided(fs, "invite-external") {
		inviteExternalPtr = inviteExternal
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.UpdatePublicShare(ctx, feishudoc.UpdatePublicShareInput{
		Token:           strings.TrimSpace(*token),
		TokenType:       strings.TrimSpace(*tokenType),
		LinkShareEntity: strings.TrimSpace(*linkShare),
		ExternalAccess:  externalAccessPtr,
		SecurityEntity:  strings.TrimSpace(*securityEntity),
		CommentEntity:   strings.TrimSpace(*commentEntity),
		ShareEntity:     strings.TrimSpace(*shareEntity),
		InviteExternal:  inviteExternalPtr,
	})
	if err != nil {
		exitRuntimeError(out, "更新公开分享设置失败", err.Error(), "检查 token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.feishu.perm.share.v1",
			"share":  result,
		})
		return
	}

	fmt.Printf("token=%s\n", result.Token)
	fmt.Printf("type=%s\n", result.TokenType)
	if result.URL != "" {
		fmt.Printf("url=%s\n", result.URL)
	}
	fmt.Printf("link_share_entity=%s\n", result.Permission.LinkShareEntity)
	fmt.Printf("external_access=%t\n", result.Permission.ExternalAccess)
	if result.Permission.SecurityEntity != "" {
		fmt.Printf("security_entity=%s\n", result.Permission.SecurityEntity)
	}
	if result.Permission.CommentEntity != "" {
		fmt.Printf("comment_entity=%s\n", result.Permission.CommentEntity)
	}
	if result.Permission.ShareEntity != "" {
		fmt.Printf("share_entity=%s\n", result.Permission.ShareEntity)
	}
}

func cmdFeishuPermAdd(args []string) {
	fs := flag.NewFlagSet("feishu perm add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"添加协作者",
			"dalek feishu perm add --token <token> --member-type <type> --member-id <id> [--perm edit] [--perm-type container] [--collab-type user] [--type docx] [--notify] [--timeout 12s] [--output text|json]",
			"dalek feishu perm add --token doxcxxxxxxxx --member-type openid --member-id ou_xxxxx --perm edit",
			"dalek feishu perm add --token doxcxxxxxxxx --member-type email --member-id user@example.com --perm view -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	token := fs.String("token", "", "文档 token（必填）")
	tokenType := fs.String("type", "docx", "文档类型（默认 docx）")
	memberType := fs.String("member-type", "", "协作者 ID 类型（如 openid/email/userid）")
	memberID := fs.String("member-id", "", "协作者 ID（必填）")
	perm := fs.String("perm", "edit", "权限角色（view|edit|full_access）")
	permType := fs.String("perm-type", "container", "权限范围（container|single_page）")
	collabType := fs.String("collab-type", "user", "协作者类型（user|chat|department|group|wiki_space_member|wiki_space_viewer|wiki_space_editor）")
	notify := fs.Bool("notify", false, "添加权限后通知协作者")
	timeout := fs.Duration("timeout", feishuPermTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu perm add 参数解析失败", "运行 dalek feishu perm add --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*token) == "" {
		exitUsageError(out, "缺少必填参数 --token", "--token 不能为空", "例如: dalek feishu perm add --token doxcxxxxxxxx --member-type openid --member-id ou_xxxxx")
	}
	if strings.TrimSpace(*memberType) == "" {
		exitUsageError(out, "缺少必填参数 --member-type", "--member-type 不能为空", "例如: dalek feishu perm add --member-type openid")
	}
	if strings.TrimSpace(*memberID) == "" {
		exitUsageError(out, "缺少必填参数 --member-id", "--member-id 不能为空", "例如: dalek feishu perm add --member-id ou_xxxxx")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu perm add --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	member, err := svc.AddPermissionMember(ctx, feishudoc.AddPermissionMemberInput{
		Token:            strings.TrimSpace(*token),
		TokenType:        strings.TrimSpace(*tokenType),
		MemberType:       strings.TrimSpace(*memberType),
		MemberID:         strings.TrimSpace(*memberID),
		Perm:             strings.TrimSpace(*perm),
		PermType:         strings.TrimSpace(*permType),
		CollaboratorType: strings.TrimSpace(*collabType),
		NeedNotification: *notify,
	})
	if err != nil {
		exitRuntimeError(out, "添加协作者失败", err.Error(), "检查 member 参数、token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.feishu.perm.add.v1",
			"member": member,
		})
		return
	}

	fmt.Printf("member_type=%s\n", member.MemberType)
	fmt.Printf("member_id=%s\n", member.MemberID)
	fmt.Printf("perm=%s\n", member.Perm)
	fmt.Printf("perm_type=%s\n", member.PermType)
	if member.Type != "" {
		fmt.Printf("type=%s\n", member.Type)
	}
}

func cmdFeishuPermList(args []string) {
	fs := flag.NewFlagSet("feishu perm ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出协作者权限",
			"dalek feishu perm ls --token <token> [--type docx] [--fields name,type,avatar,external_label] [--perm-type container] [--timeout 12s] [--output text|json]",
			"dalek feishu perm ls --token doxcxxxxxxxx",
			"dalek feishu perm ls --token doxcxxxxxxxx --perm-type single_page -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	token := fs.String("token", "", "文档 token（必填）")
	tokenType := fs.String("type", "docx", "文档类型（默认 docx）")
	fields := fs.String("fields", "name,type,avatar,external_label", "返回字段（可选，支持 *）")
	permType := fs.String("perm-type", "", "权限范围过滤（container|single_page，可选）")
	timeout := fs.Duration("timeout", feishuPermTimeoutDefault, "请求超时（例如 12s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu perm ls 参数解析失败", "运行 dalek feishu perm ls --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*token) == "" {
		exitUsageError(out, "缺少必填参数 --token", "--token 不能为空", "例如: dalek feishu perm ls --token doxcxxxxxxxx")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek feishu perm ls --timeout 12s")
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := svc.ListPermissionMembers(ctx, feishudoc.ListPermissionMembersInput{
		Token:     strings.TrimSpace(*token),
		TokenType: strings.TrimSpace(*tokenType),
		Fields:    strings.TrimSpace(*fields),
		PermType:  strings.TrimSpace(*permType),
	})
	if err != nil {
		exitRuntimeError(out, "列出协作者失败", err.Error(), "检查 token/type 与飞书权限后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.feishu.perm.list.v1",
			"members": result.Members,
		})
		return
	}

	if len(result.Members) == 0 {
		fmt.Println("(empty)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MEMBER_ID\tMEMBER_TYPE\tPERM\tPERM_TYPE\tTYPE\tNAME")
	for _, member := range result.Members {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", member.MemberID, member.MemberType, member.Perm, member.PermType, member.Type, member.Name)
	}
	_ = tw.Flush()
}
