package runexecutor

import "time"

func TargetSpecsFromConfig(cfg map[string]TargetConfig) []TargetSpec {
	if len(cfg) == 0 {
		return nil
	}
	out := make([]TargetSpec, 0, len(cfg))
	for name, target := range cfg {
		spec := TargetSpec{
			Name:        name,
			Description: target.Description,
			Command:     append([]string(nil), target.Command...),
			Timeout:     time.Duration(target.TimeoutMS) * time.Millisecond,
			Preflight: StageCommand{
				Command: append([]string(nil), target.PreflightCommand...),
				Timeout: time.Duration(target.PreflightTimeoutMS) * time.Millisecond,
			},
			Bootstrap: StageCommand{
				Command: append([]string(nil), target.BootstrapCommand...),
				Timeout: time.Duration(target.BootstrapTimeoutMS) * time.Millisecond,
			},
			Repair: StageCommand{
				Command: append([]string(nil), target.RepairCommand...),
				Timeout: time.Duration(target.RepairTimeoutMS) * time.Millisecond,
			},
		}
		spec = normalizeTargetSpec(spec)
		if spec.Name == "" || !targetNamePattern.MatchString(spec.Name) {
			continue
		}
		if len(spec.Command) == 0 {
			continue
		}
		out = append(out, spec)
	}
	return out
}
