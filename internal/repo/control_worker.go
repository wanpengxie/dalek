package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	controlWorkerKernelSeedPath = "templates/project/control/worker/worker-kernel.md"
	controlWorkerStateSeedPath  = "templates/project/control/worker/state.json"
)

func ControlWorkerKernelPath(layout Layout) string {
	return filepath.Join(layout.ControlWorkerDir, "worker-kernel.md")
}

func ControlWorkerStatePath(layout Layout) string {
	return filepath.Join(layout.ControlWorkerDir, "state.json")
}

func ReadControlWorkerKernelTemplate(layout Layout) (string, error) {
	return readControlWorkerTemplate(ControlWorkerKernelPath(layout), controlWorkerKernelSeedPath)
}

func ReadControlWorkerStateTemplate(layout Layout) (string, error) {
	return readControlWorkerTemplate(ControlWorkerStatePath(layout), controlWorkerStateSeedPath)
}

func ensureControlWorkerTemplates(layout Layout, force bool) ([]ControlPlaneChange, error) {
	specs := []struct {
		path    string
		content string
	}{
		{path: ControlWorkerKernelPath(layout), content: defaultControlWorkerKernelTemplate()},
		{path: ControlWorkerStatePath(layout), content: defaultControlWorkerStateTemplate()},
	}

	changes := make([]ControlPlaneChange, 0, len(specs))
	for _, spec := range specs {
		existed := true
		if _, statErr := os.Stat(spec.path); statErr != nil {
			existed = false
		}
		var changed bool
		var err error
		if force {
			changed, err = writeFileForce(spec.path, spec.content, 0o644)
		} else {
			changed, err = writeFileIfMissing(spec.path, spec.content, 0o644)
		}
		if err != nil {
			return nil, err
		}
		if changed {
			action := "create"
			if existed {
				action = "update"
			}
			changes = append(changes, ControlPlaneChange{Path: spec.path, Action: action})
		}
	}
	return changes, nil
}

func planControlWorkerTemplateChanges(layout Layout) ([]ControlPlaneChange, error) {
	specs := []struct {
		path    string
		content string
	}{
		{path: ControlWorkerKernelPath(layout), content: defaultControlWorkerKernelTemplate()},
		{path: ControlWorkerStatePath(layout), content: defaultControlWorkerStateTemplate()},
	}
	changes := make([]ControlPlaneChange, 0, len(specs))
	for _, spec := range specs {
		differs, err := fileContentDiffers(spec.path, spec.content)
		if err != nil {
			return nil, err
		}
		if differs {
			action := "create"
			if _, statErr := os.Stat(spec.path); statErr == nil {
				action = "update"
			}
			changes = append(changes, ControlPlaneChange{Path: spec.path, Action: action})
		}
	}
	return changes, nil
}

func defaultControlWorkerKernelTemplate() string {
	return mustReadSeedTemplate(controlWorkerKernelSeedPath)
}

func defaultControlWorkerStateTemplate() string {
	return mustReadSeedTemplate(controlWorkerStateSeedPath)
}

func readControlWorkerTemplate(path, fallbackSeedPath string) (string, error) {
	path = strings.TrimSpace(path)
	if path != "" {
		raw, err := os.ReadFile(path)
		if err == nil {
			return string(raw), nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("读取 control/worker 模板失败(%s): %w", path, err)
		}
	}
	return ReadSeedTemplate(fallbackSeedPath)
}
