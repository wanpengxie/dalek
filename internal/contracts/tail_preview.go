package contracts

import "time"

// TailPreview 是某个运行体最近输出的尾部抓取结果。
// 输出来源可能是 worker 日志文件，也可能是 manager tmux pane。
type TailPreview struct {
	TicketID uint
	WorkerID uint

	Source  string
	LogPath string

	TmuxSocket  string
	TmuxSession string
	PaneID      string
	Target      string

	CapturedAt time.Time
	Lines      []string
}
