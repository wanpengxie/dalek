package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	// <root>/internal/arch/constraints_test.go -> <root>
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func listGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".go") {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

func listGoFilesRecursive(t *testing.T, dir string) []string {
	t.Helper()
	out := make([]string, 0, 64)
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.TrimSpace(path), ".go") {
			out = append(out, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk dir failed: %v", err)
	}
	return out
}

func parseFile(t *testing.T, path string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s failed: %v", path, err)
	}
	return f
}

func TestWorkerDoesNotOwnPMDispatchOrBootstrapFiles(t *testing.T) {
	root := repoRoot(t)
	workerDir := filepath.Join(root, "internal", "services", "worker")
	ents, err := os.ReadDir(workerDir)
	if err != nil {
		t.Fatalf("read worker dir failed: %v", err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.Contains(name, "pm_dispatch") || strings.Contains(name, "pm_bootstrap") {
			t.Fatalf("worker 目录不应包含 PM 语义执行文件: %s", name)
		}
	}
}

func TestWorkerDoesNotImportPMService(t *testing.T) {
	root := repoRoot(t)
	workerDir := filepath.Join(root, "internal", "services", "worker")
	files := listGoFiles(t, workerDir)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "dalek/internal/services/pm" {
				t.Fatalf("worker 不应 import pm（%s）", filepath.Base(path))
			}
		}
	}
}

func TestInfraDoesNotImportInternalPackages(t *testing.T) {
	root := repoRoot(t)
	infraDir := filepath.Join(root, "internal", "infra")
	files := listGoFiles(t, infraDir)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, "dalek/internal/") {
				t.Fatalf("infra 不应 import internal 包（%s import %s）", filepath.Base(path), p)
			}
		}
	}
}

func TestContractsDoNotImportOtherInternalPackages(t *testing.T) {
	root := repoRoot(t)
	contractsDir := filepath.Join(root, "internal", "contracts")
	files := listGoFiles(t, contractsDir)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(p, "dalek/internal/") {
				continue
			}
			if p == "dalek/internal/contracts" {
				continue
			}
			t.Fatalf("contracts 不应 import 其他 internal 包（%s import %s）", rel, p)
		}
	}
}

func TestServicesDoNotImportOSExecExceptChannelRunner(t *testing.T) {
	root := repoRoot(t)
	servicesDir := filepath.Join(root, "internal", "services")
	files := listGoFilesRecursive(t, servicesDir)
	allow := map[string]bool{
		"internal/services/agentexec/process.go":  true,
		"internal/services/pm/session.go":         true,
		"internal/services/worker/start.go":       true,
		"internal/services/tunnel/cloudflared.go": true,
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p != "os/exec" {
				continue
			}
			if !allow[rel] {
				t.Fatalf("services 不应直接 import os/exec（%s）", rel)
			}
		}
	}
}

func TestServicesAgentPathNoBareInfraRun(t *testing.T) {
	root := repoRoot(t)
	servicesDir := filepath.Join(root, "internal", "services")
	files := listGoFilesRecursive(t, servicesDir)
	allow := map[string]bool{
		"internal/services/pm/bootstrap.go": true,
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		foundInfraRun := false
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if x.Name == "infra" && sel.Sel != nil && sel.Sel.Name == "Run" {
				foundInfraRun = true
			}
			return true
		})
		if !foundInfraRun {
			continue
		}
		if !allow[rel] {
			t.Fatalf("services 禁止裸 infra.Run（%s）", rel)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s failed: %v", rel, err)
		}
		if !strings.Contains(string(raw), "non-agent-exec") {
			t.Fatalf("%s 使用 infra.Run 必须标注 non-agent-exec", rel)
		}
	}
}

func TestChannelServiceDoesNotDirectlyCallAgentCLIRun(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "internal", "services", "channel", "service.go")
	f := parseFile(t, path)
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if x.Name == "agentcli" && sel.Sel != nil && sel.Sel.Name == "Run" {
			t.Fatalf("channel/service.go 不应直接调用 agentcli.Run（请走包装层）")
		}
		return true
	})
}

func TestCmdChannelServiceImportAllowlist(t *testing.T) {
	root := repoRoot(t)
	cmdDir := filepath.Join(root, "cmd", "dalek")
	files := listGoFiles(t, cmdDir)

	// 只有 gateway 相关的 cmd 文件允许直接引用 channel service 包。
	// 新增 cmd 文件不应直接 import channel/子包——应通过 app.Project facade 访问。
	allow := map[string]bool{
		"cmd/dalek/cmd_gateway_feishu.go":  true,
		"cmd/dalek/cmd_gateway_ws.go":      true,
		"cmd/dalek/cmd_gateway_runtime.go": true,
		"cmd/dalek/cmd_gateway_bind.go":    true,
	}

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(p, "dalek/internal/services/channel") {
				continue
			}
			if !allow[rel] {
				t.Fatalf("cmd 非 gateway 文件不应直接 import channel service（%s import %s）——请通过 app.Project facade", rel, p)
			}
		}
	}
}

func TestGatewaySendDoesNotImportChannelSubpackages(t *testing.T) {
	root := repoRoot(t)
	targetDir := filepath.Join(root, "internal", "services", "gatewaysend")
	files := listGoFilesRecursive(t, targetDir)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, "dalek/internal/services/channel/") {
				t.Fatalf("gatewaysend 不应 import channel 子包（%s import %s）", rel, p)
			}
		}
	}
}

func TestChannelServicesDoNotImportPMTicketWorkerPackages(t *testing.T) {
	root := repoRoot(t)
	targetDir := filepath.Join(root, "internal", "services", "channel")
	files := listGoFilesRecursive(t, targetDir)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			switch p {
			case "dalek/internal/services/pm", "dalek/internal/services/ticket", "dalek/internal/services/worker":
				t.Fatalf("channel service 不应直接 import %s（%s）", p, rel)
			}
		}
	}
}

func TestCmdTestsDoNotImportStore(t *testing.T) {
	root := repoRoot(t)
	cmdDir := filepath.Join(root, "cmd", "dalek")
	files := listGoFiles(t, cmdDir)
	for _, path := range files {
		if !strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "dalek/internal/store" {
				t.Fatalf("cmd 测试不应 import internal/store（%s）", rel)
			}
		}
	}
}

func TestCmdProductionFilesDoNotImportStore(t *testing.T) {
	root := repoRoot(t)
	cmdDir := filepath.Join(root, "cmd", "dalek")
	files := listGoFiles(t, cmdDir)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "dalek/internal/store" {
				t.Fatalf("cmd 生产代码不应 import internal/store（%s）", rel)
			}
		}
	}
}

func TestConfigDoesNotImportAppPackage(t *testing.T) {
	root := repoRoot(t)
	targetDir := filepath.Join(root, "internal", "config")
	files := listGoFilesRecursive(t, targetDir)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "dalek/internal/app" {
				t.Fatalf("config 层不应 import app（%s）", rel)
			}
		}
	}
}

func TestServiceAndUITestsDoNotUseStoreContractAliasTypes(t *testing.T) {
	root := repoRoot(t)
	targetDirs := []string{
		filepath.Join(root, "internal", "services"),
		filepath.Join(root, "internal", "ui"),
	}
	banned := map[string]bool{
		"Ticket":              true,
		"Worker":              true,
		"MergeItem":           true,
		"InboxItem":           true,
		"TaskStatusView":      true,
		"TicketWorkflowEvent": true,
		"ChannelBinding":      true,
		"ChannelTurnJob":      true,
		"ChannelMessage":      true,
		"ChannelConversation": true,
		"ChannelOutbox":       true,
	}

	for _, dir := range targetDirs {
		files := listGoFilesRecursive(t, dir)
		for _, path := range files {
			if !strings.HasSuffix(path, "_test.go") {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			f := parseFile(t, path)
			usesStore := false
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if p == "dalek/internal/store" {
					usesStore = true
					break
				}
			}
			if !usesStore {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s failed: %v", rel, err)
			}
			text := string(raw)
			for typ := range banned {
				pattern := regexp.MustCompile(`\bstore\.` + typ + `\b`)
				if pattern.MatchString(text) {
					t.Fatalf("services/ui 测试不应使用 store.%s（%s），请改用 contracts.%s", typ, rel, typ)
				}
			}
		}
	}
}

func TestCoreTaskRuntimeDoesNotImportStore(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "internal", "services", "core", "task_runtime.go")
	f := parseFile(t, path)
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if p == "dalek/internal/store" {
			t.Fatalf("core/task_runtime.go 不应 import store（请改用 contracts 视图类型）")
		}
	}
}

func TestCmdTestsServicesImportAllowlist(t *testing.T) {
	root := repoRoot(t)
	cmdDir := filepath.Join(root, "cmd", "dalek")
	files := listGoFiles(t, cmdDir)
	allowlist := map[string]map[string]bool{
		"cmd/dalek/cmd_gateway_feishu_test.go": {
			"dalek/internal/services/channel": true,
		},
		"cmd/dalek/e2e_cli_test.go": {
			"dalek/internal/services/channel": true,
		},
	}

	for _, path := range files {
		if !strings.HasSuffix(path, "_test.go") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f := parseFile(t, path)
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(p, "dalek/internal/services/") {
				continue
			}
			allow := allowlist[rel]
			if !allow[p] {
				t.Fatalf("cmd 测试 import internal/services 必须在 allowlist（%s import %s）", rel, p)
			}
		}
	}
}

func TestAppIntegrationTestDoesNotUseProjectCoreOrStore(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "internal", "app", "integration_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read integration_test.go failed: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, ".core.") {
		t.Fatalf("integration_test.go 不应直接访问 p.core（请通过 app facade 方法）")
	}
	if strings.Contains(text, `"dalek/internal/store"`) || strings.Contains(text, "store.") {
		t.Fatalf("integration_test.go 不应直接依赖 store 包（请使用 contracts 或 app facade）")
	}
}
