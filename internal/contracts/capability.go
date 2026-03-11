package contracts

// TicketCapability 是“是否允许操作”的统一输出，供 TUI/CLI 使用。
//
// 约束：
// - capability 必须只依赖可解释的事实（workflow/worker/session/job/run 等）。
// - capability 只做“是否可操作”的门禁，不负责推进状态机。
type TicketCapability struct {
	CanStart    bool `json:"can_start"`
	CanQueueRun bool `json:"can_queue_run"`
	// CanDispatch 保留为兼容 alias；主语义已迁移到 CanQueueRun。
	CanDispatch bool `json:"can_dispatch"`
	CanAttach   bool `json:"can_attach"`
	CanStop     bool `json:"can_stop"`
	CanArchive  bool `json:"can_archive"`

	// Reason 是不可用或风险提示原因（必须可读，可为空）。
	Reason string `json:"reason,omitempty"`
}
