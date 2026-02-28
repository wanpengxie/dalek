package contracts

import "time"

// TailPreview 是某个运行体最近输出的尾部抓取结果。
type TailPreview struct {
	TicketID uint
	WorkerID uint

	Source  string
	LogPath string

	CapturedAt time.Time
	Lines      []string
}
