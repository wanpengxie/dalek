package pm

import (
	"context"
	"errors"
)

// newCancelOnlyContext 返回一个不继承 deadline 的 context：
// - parent 被主动 cancel（Err()==context.Canceled）时会透传取消信号；
// - parent 的 deadline 超时（Err()==context.DeadlineExceeded）不会透传。
//
// 使用 context.AfterFunc 替代手动 goroutine，避免 goroutine 泄露风险。
// 返回的 CancelFunc 同时停止 AfterFunc 回调并取消 child context。
func newCancelOnlyContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	childCtx, childCancel := context.WithCancel(base)
	if parent == nil {
		return childCtx, childCancel
	}
	stop := context.AfterFunc(parent, func() {
		if errors.Is(parent.Err(), context.Canceled) {
			childCancel()
		}
	})
	return childCtx, func() {
		stop()
		childCancel()
	}
}
