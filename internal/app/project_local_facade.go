package app

// LocalProject 是本地项目的最小 facade（便于后续引入 remote facade 时做边界分层）。
// 当前仍复用 Project 的实现，仅提供轻量包装，避免破坏现有调用方。
type LocalProject struct {
	*Project
}

func AsLocalProject(p *Project) *LocalProject {
	if p == nil {
		return nil
	}
	return &LocalProject{Project: p}
}
