package contracts

// ProjectMeta 是跨模块共享的最小项目元信息。
type ProjectMeta struct {
	Name     string
	RepoRoot string
}

// ProjectMetaResolver 用于按项目名解析最小元信息。
type ProjectMetaResolver interface {
	ResolveProjectMeta(name string) (*ProjectMeta, error)
}
