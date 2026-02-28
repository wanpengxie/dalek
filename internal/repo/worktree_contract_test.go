package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureWorktreeContract_OnlyEnsuresContractDir(t *testing.T) {
	root := t.TempDir()

	cp, err := EnsureWorktreeContract(root)
	if err != nil {
		t.Fatalf("EnsureWorktreeContract failed: %v", err)
	}

	if _, err := os.Stat(cp.Dir); err != nil {
		t.Fatalf("expected contract dir exists: %s err=%v", cp.Dir, err)
	}

	mustNotExist := []string{
		cp.AgentKernelMD,
		cp.StateJSON,
		cp.PlanMD,
		filepath.Join(cp.Dir, "contract.json"),
		filepath.Join(cp.Dir, "requests"),
		filepath.Join(cp.Dir, "responses"),
	}
	for _, p := range mustNotExist {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("path should not be created by contract ensure: %s err=%v", p, err)
		}
	}
}
