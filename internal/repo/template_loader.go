package repo

import (
	"bytes"
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/**
var seedTemplateFS embed.FS

func MustReadSeedTemplate(path string) string {
	out, err := ReadSeedTemplate(path)
	if err != nil {
		panic(err.Error())
	}
	return out
}

func MustRenderSeedTemplate(path string, data map[string]string) string {
	out, err := RenderSeedTemplate(path, data)
	if err != nil {
		panic(err.Error())
	}
	return out
}

func mustReadSeedTemplate(path string) string {
	return MustReadSeedTemplate(path)
}

func mustRenderSeedTemplate(path string, data map[string]string) string {
	return MustRenderSeedTemplate(path, data)
}

func ReadSeedTemplate(path string) (string, error) {
	clean := strings.TrimSpace(path)
	b, err := seedTemplateFS.ReadFile(clean)
	if err != nil {
		return "", fmt.Errorf("读取内置种子模板失败: %s: %w", clean, err)
	}
	return string(b), nil
}

func RenderSeedTemplate(path string, data any) (string, error) {
	tpl, err := ReadSeedTemplate(path)
	if err != nil {
		return "", err
	}
	t, err := template.New(filepath.Base(path)).Parse(tpl)
	if err != nil {
		return "", fmt.Errorf("解析内置种子模板失败: %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("渲染内置种子模板失败: %s: %w", path, err)
	}
	return buf.String(), nil
}
