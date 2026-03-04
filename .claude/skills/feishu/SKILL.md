---
name: feishu
description: 飞书文档协同 CLI。创建、读取、写入飞书文档，管理知识空间，设置文档权限与分享。当需要与飞书文档交互时使用此工具。
allowed-tools: Bash(feishu), Bash(feishu *)
---

# 飞书文档协同 (feishu)

`feishu` 是独立的飞书文档 CLI，支持文档 CRUD、知识空间管理、权限控制和评论管理。

所有命令通过 `--url` 接受飞书链接，自动解析文档 ID 和类型，用户无需手动提取 token。

## Quick start

```bash
# 验证凭据
feishu auth

# 创建文档
feishu doc create --title "周报"

# 读取文档 → 保存为 md 文件
feishu doc read --url https://feishu.cn/docx/xxxxxxxx output.md

# 读取文档 → stdout
feishu doc read --url https://feishu.cn/docx/xxxxxxxx

# 写入文档 ← 从 md 文件
feishu doc write --url https://feishu.cn/docx/xxxxxxxx notes.md

# 写入文档 ← 从 stdin
cat report.md | feishu doc write --url https://feishu.cn/docx/xxxxxxxx -

# 公开分享
feishu perm share --url https://feishu.cn/docx/xxxxxxxx --link-share anyone_readable

# 创建评论并回复
feishu comment create --url https://feishu.cn/docx/xxxxxxxx --content "请补充验收标准"
feishu comment reply --url https://feishu.cn/docx/xxxxxxxx --id <comment_id> --content "已补充"
```

## 文档操作 (doc)

```bash
feishu doc create --title "标题" [--folder fldxxxxxxxx]
feishu doc read --url <飞书链接> [output.md]
feishu doc write --url <飞书链接> <input.md>
feishu doc write --url <飞书链接> --content "短内容"
feishu doc ls [--folder fldxxxxxxxx]
```

## 知识空间 (wiki)

```bash
feishu wiki ls
feishu wiki nodes --space <space_id> [--parent <node_token>]
feishu wiki create --space <space_id> --title "节点标题"
```

## 权限管理 (perm)

```bash
# 分享设置（链接权限）
feishu perm share --url <飞书链接> --link-share <策略>
# 策略: tenant_readable | tenant_editable | anyone_readable | anyone_editable | closed

# 添加协作者
feishu perm add --url <飞书链接> --member-type email --member-id user@example.com --perm edit
# --perm: view | edit | full_access
# --member-type: email | openid | userid

# 列出协作者
feishu perm ls --url <飞书链接>
```

## 评论管理 (comment)

```bash
# 列出评论
feishu comment ls --url <飞书链接>

# 获取单条评论
feishu comment get --url <飞书链接> --id <comment_id>

# 创建评论
feishu comment create --url <飞书链接> --content "请补充验收标准"

# 回复评论
feishu comment reply --url <飞书链接> --id <comment_id> --content "已补充"

# 标记评论已解决
feishu comment resolve --url <飞书链接> --id <comment_id>
```

## 通用参数

- `--url <飞书链接>` — 直接粘贴浏览器地址栏链接，自动解析
- `--home <path>` — 配置目录（默认 ~/.dalek）
- `-o json` — JSON 格式输出
- `--timeout 15s` — 自定义超时

## 典型工作流

```bash
# 创建文档 → 写入内容 → 分享给团队
feishu doc create --title "Sprint Review" -o json
feishu doc write --url <返回的链接> review.md
feishu perm share --url <链接> --link-share anyone_editable

# 读取现有文档 → 本地编辑 → 追加写回
feishu doc read --url <链接> draft.md
# ... 编辑 draft.md ...
feishu doc write --url <链接> draft.md
```
