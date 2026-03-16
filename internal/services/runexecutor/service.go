package runexecutor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var targetNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Stage string

const (
	StagePreflight Stage = "preflight"
	StageBootstrap Stage = "bootstrap"
	StageVerify    Stage = "verify"
	StageRepair    Stage = "repair"
)

type TargetSpec struct {
	Name        string
	Description string
	Command     []string
	Timeout     time.Duration
	Preflight   StageCommand
	Bootstrap   StageCommand
	Repair      StageCommand
}

type TargetConfig struct {
	Description       string
	Command           []string
	TimeoutMS         int64
	PreflightCommand  []string
	PreflightTimeoutMS int64
	BootstrapCommand  []string
	BootstrapTimeoutMS int64
	RepairCommand     []string
	RepairTimeoutMS   int64
}

type StageCommand struct {
	Command []string
	Timeout time.Duration
}

type TargetCatalog interface {
	ResolveTarget(name string) (TargetSpec, error)
	ListTargets() []TargetSpec
}

type Service struct {
	targets map[string]TargetSpec
}

func New(targets []TargetSpec) *Service {
	if len(targets) == 0 {
		targets = DefaultTargets()
	}
	index := make(map[string]TargetSpec, len(targets))
	for _, spec := range targets {
		spec = normalizeTargetSpec(spec)
		if spec.Name == "" {
			continue
		}
		index[spec.Name] = spec
	}
	return &Service{targets: index}
}

func DefaultTargets() []TargetSpec {
	return []TargetSpec{
		{
			Name:        "test",
			Description: "Run the default project test suite.",
			Command:     []string{"go", "test", "./..."},
			Timeout:     20 * time.Minute,
		},
		{
			Name:        "lint",
			Description: "Run the default project linter entrypoint.",
			Command:     []string{"golangci-lint", "run"},
			Timeout:     10 * time.Minute,
		},
		{
			Name:        "build",
			Description: "Run the default project build entrypoint.",
			Command:     []string{"go", "build", "./..."},
			Timeout:     15 * time.Minute,
		},
	}
}

func (s *Service) ResolveTarget(name string) (TargetSpec, error) {
	if s == nil {
		return TargetSpec{}, fmt.Errorf("run executor target catalog 未初始化")
	}
	name = normalizeTargetName(name)
	if name == "" {
		return TargetSpec{}, fmt.Errorf("verify_target 不能为空")
	}
	if !targetNamePattern.MatchString(name) {
		return TargetSpec{}, fmt.Errorf("verify_target 非法，仅允许标准化 target 名称: %s", name)
	}
	spec, ok := s.targets[name]
	if !ok {
		return TargetSpec{}, fmt.Errorf("verify_target 未注册: %s", name)
	}
	return spec, nil
}

func (s *Service) ListTargets() []TargetSpec {
	if s == nil || len(s.targets) == 0 {
		return []TargetSpec{}
	}
	names := make([]string, 0, len(s.targets))
	for name := range s.targets {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]TargetSpec, 0, len(names))
	for _, name := range names {
		out = append(out, s.targets[name])
	}
	return out
}

func normalizeTargetSpec(in TargetSpec) TargetSpec {
	out := in
	out.Name = normalizeTargetName(out.Name)
	out.Description = strings.TrimSpace(out.Description)
	if out.Timeout < 0 {
		out.Timeout = 0
	}
	out.Command = normalizeCommandArgs(out.Command)
	out.Preflight = normalizeStageCommand(out.Preflight)
	out.Bootstrap = normalizeStageCommand(out.Bootstrap)
	out.Repair = normalizeStageCommand(out.Repair)
	return out
}

func normalizeStageCommand(in StageCommand) StageCommand {
	out := in
	if out.Timeout < 0 {
		out.Timeout = 0
	}
	out.Command = normalizeCommandArgs(out.Command)
	return out
}

func normalizeCommandArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	argv := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		argv = append(argv, arg)
	}
	return argv
}

func normalizeTargetName(raw string) string {
	return strings.TrimSpace(strings.ToLower(raw))
}
