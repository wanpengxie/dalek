package pm

import "context"

const (
	WorkerLoopPhaseRunning   = "running"
	WorkerLoopPhaseRepairing = "repairing"
	WorkerLoopPhaseClosing   = "closing"
	WorkerLoopPhaseCanceling = "canceling"
)

type WorkerLoopControlSink interface {
	LoopClaimed(ticketID, workerID uint)
	LoopRunAttached(runID, workerID uint, phase string)
	LoopClosing()
	LoopCancelRequested()
	LoopErrored(err error)
}

type workerLoopControlSinkKey struct{}

func WithWorkerLoopControlSink(ctx context.Context, sink WorkerLoopControlSink) context.Context {
	if ctx == nil || sink == nil {
		return ctx
	}
	return context.WithValue(ctx, workerLoopControlSinkKey{}, sink)
}

func workerLoopControlSinkFromContext(ctx context.Context) WorkerLoopControlSink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(workerLoopControlSinkKey{}).(WorkerLoopControlSink)
	return sink
}
