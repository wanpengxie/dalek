package agentexec

import (
	"context"
	"time"
)

// AppendEventFunc 用于把执行器的关键事件写入外层事件流。
type AppendEventFunc func(ctx context.Context, eventType, note string, payload any, createdAt time.Time)

// SemanticWatchFunc 用于请求 worker 语义观察。
type SemanticWatchFunc func(ctx context.Context, requestedAt time.Time)
