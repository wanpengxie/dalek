package contracts

import "time"

// TailPreview 是对某个 tmux pane 当前屏幕“尾部输出”的抓取结果。
// 用途：UI 在不 attach 的情况下，让用户快速了解该 session 正在输出什么。
type TailPreview struct {
	TicketID uint
	WorkerID uint

	TmuxSocket  string
	TmuxSession string
	PaneID      string
	Target      string

	CapturedAt time.Time
	Lines      []string
}
