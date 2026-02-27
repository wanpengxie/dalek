package app

import (
	"testing"

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
