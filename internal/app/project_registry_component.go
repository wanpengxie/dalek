package app

import "context"

type projectRegistryComponent struct {
	registry *ProjectRegistry
}

func newProjectRegistryComponent(registry *ProjectRegistry) *projectRegistryComponent {
	return &projectRegistryComponent{registry: registry}
}

func (c *projectRegistryComponent) Name() string {
	return "project_registry"
}

func (c *projectRegistryComponent) Start(ctx context.Context) error {
	_ = ctx
	return nil
}

func (c *projectRegistryComponent) Stop(ctx context.Context) error {
	_ = ctx
	if c == nil || c.registry == nil {
		return nil
	}
	return c.registry.CloseAll()
}
