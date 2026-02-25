package repo

import (
	"bytes"
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/project/**
var seedTemplateFS embed.FS

func MustReadSeedTemplate(path string) string {
	return mustReadSeedTemplate(path)
}

func MustRenderSeedTemplate(path string, data map[string]string) string {
	return mustRenderSeedTemplate(path, data)
}

func mustReadSeedTemplate(path string) string {
	clean := strings.TrimSpace(path)
	b, err := seedTemplateFS.ReadFile(clean)
	if err != nil {
		panic(fmt.Sprintf("读取内置种子模板失败: %s: %v", clean, err))
	}
	return string(b)
}

func mustRenderSeedTemplate(path string, data map[string]string) string {
	tpl := mustReadSeedTemplate(path)
	t, err := template.New(filepath.Base(path)).Parse(tpl)
	if err != nil {
		panic(fmt.Sprintf("解析内置种子模板失败: %s: %v", path, err))
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("渲染内置种子模板失败: %s: %v", path, err))
	}
	return buf.String()
}
