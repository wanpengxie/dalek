package main

import (
	"strings"

	"dalek/internal/services/feishudoc"
)

// resolveTokenAndType resolves token + type from either --url or --token/--doc + --type.
// urlFlag takes precedence. Returns (token, tokenType, ok).
func resolveTokenAndType(out cliOutputFormat, urlFlag, tokenFlag, typeFlag, cmdHint string) (string, string) {
	urlVal := strings.TrimSpace(urlFlag)
	tokenVal := strings.TrimSpace(tokenFlag)

	if urlVal != "" {
		parsed, err := feishudoc.ParseURL(urlVal)
		if err != nil {
			exitRuntimeError(out, "URL 解析失败", err.Error(), "请提供有效的飞书文档 URL，例如: https://feishu.cn/docx/xxxxxxxx")
		}
		return parsed.Token, parsed.TokenType
	}

	if tokenVal != "" {
		return tokenVal, strings.TrimSpace(typeFlag)
	}

	exitUsageError(out,
		"缺少文档标识",
		"必须提供 --url 或 --token",
		cmdHint,
	)
	return "", "" // unreachable
}

// resolveDocID resolves document ID from either --url or --doc.
func resolveDocID(out cliOutputFormat, urlFlag, docFlag, cmdHint string) string {
	urlVal := strings.TrimSpace(urlFlag)
	docVal := strings.TrimSpace(docFlag)

	if urlVal != "" {
		parsed, err := feishudoc.ParseURL(urlVal)
		if err != nil {
			exitRuntimeError(out, "URL 解析失败", err.Error(), "请提供有效的飞书文档 URL，例如: https://feishu.cn/docx/xxxxxxxx")
		}
		return parsed.Token
	}

	if docVal != "" {
		return docVal
	}

	exitUsageError(out,
		"缺少文档标识",
		"必须提供 --url 或 --doc",
		cmdHint,
	)
	return "" // unreachable
}
