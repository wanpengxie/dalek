package app

import (
	"testing"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	pmsvc "dalek/internal/services/pm"
	"dalek/internal/services/worker"
)

func TestWireStatusChangeHook_NilHome(t *testing.T) {
	var h *Home
	// 不 panic 即为通过
	h.wireStatusChangeHook(&Project{}, "test")
}

func TestWireStatusChangeHook_NilProject(t *testing.T) {
	h := &Home{}
	h.wireStatusChangeHook(nil, "test")
}

func TestWireStatusChangeHook_NilPM(t *testing.T) {
	h := &Home{}
	p := &Project{core: &core.Project{}}
	h.wireStatusChangeHook(p, "test")
}

func TestWireStatusChangeHook_EmptyProjectName(t *testing.T) {
	h := &Home{}
	p := &Project{core: &core.Project{}, pm: pmsvc.New(nil, nil)}
	h.wireStatusChangeHook(p, "")
}

func TestWireStatusChangeHook_GatewayDBUnavailable(t *testing.T) {
	h := &Home{
		GatewayDBPath: "/nonexistent/path/gateway.sqlite3",
	}
	cp := &core.Project{
		Logger: core.DiscardLogger(),
	}
	pmSvc := pmsvc.New(cp, worker.New(cp, nil))
	p := &Project{core: cp, pm: pmSvc}
	// gateway DB 不可用 → 静默跳过，不 panic
	h.wireStatusChangeHook(p, "test-project")
}

func TestStaticProjectMetaResolver(t *testing.T) {
	r := &staticProjectMetaResolver{
		name:     "my-project",
		repoRoot: "/path/to/repo",
	}
	meta, err := r.ResolveProjectMeta("anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if meta.Name != "my-project" {
		t.Fatalf("expected name=my-project, got=%s", meta.Name)
	}
	if meta.RepoRoot != "/path/to/repo" {
		t.Fatalf("expected repoRoot=/path/to/repo, got=%s", meta.RepoRoot)
	}
}

func TestStaticProjectMetaResolver_Nil(t *testing.T) {
	var r *staticProjectMetaResolver
	meta, err := r.ResolveProjectMeta("anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatal("expected nil meta for nil resolver")
	}
}

func TestStaticProjectMetaResolver_ImplementsInterface(t *testing.T) {
	// 编译时验证
	var _ contracts.ProjectMetaResolver = (*staticProjectMetaResolver)(nil)
}
