package channel

import (
	"context"
)

type streamEventEmitter func(AgentEvent)

type streamEmitterContextKey struct{}

func withStreamEventEmitter(ctx context.Context, emitter streamEventEmitter) context.Context {
	if ctx == nil || emitter == nil {
		return ctx
	}
	return context.WithValue(ctx, streamEmitterContextKey{}, emitter)
}

func emitStreamAgentEvent(ctx context.Context, ev AgentEvent) {
	if ctx == nil {
		return
	}
	emitter, _ := ctx.Value(streamEmitterContextKey{}).(streamEventEmitter)
	if emitter == nil {
		return
	}
	emitter(ev)
}
