package agentexec

import (
	"context"
	"time"
)

// AppendEventFunc 用于把执行器的关键事件写入外层事件流。
type AppendEventFunc func(ctx context.Context, eventType, note string, payload any, createdAt time.Time)

// SemanticWatchFunc 用于请求 worker 语义观察。
type SemanticWatchFunc func(ctx context.Context, requestedAt time.Time)

// 兼容旧命名：历史上这些类型定义在 tmux executor 文件中。
type TmuxAppendEventFunc = AppendEventFunc
type TmuxSemanticWatchFunc = SemanticWatchFunc
