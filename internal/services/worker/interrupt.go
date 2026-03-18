package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
)

type InterruptResult struct {
	TicketID uint
	WorkerID uint

	Mode      string
	TaskRunID uint
	LogPath   string
}

func (s *Service) InterruptTicket(ctx context.Context, ticketID uint) (InterruptResult, error) {
	w, err := s.LatestWorker(ctx, ticketID)
	if err != nil {
		return InterruptResult{}, err
	}
	if w == nil {
		return InterruptResult{}, fmt.Errorf("该 ticket 还没有 worker")
	}
	return s.InterruptWorker(ctx, w.ID)
}

// InterruptWorker 软中断：请求中断当前活跃 task run。
func (s *Service) InterruptWorker(ctx context.Context, workerID uint) (InterruptResult, error) {
	db, err := s.db()
	if err != nil {
		return InterruptResult{}, err
	}
	rt, err := s.taskRuntime()
	if err != nil {
		return InterruptResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()

	var w contracts.Worker
	if err := db.First(&w, workerID).Error; err != nil {
		return InterruptResult{}, err
	}
	run, err := rt.LatestActiveWorkerRun(ctx, w.ID)
	if err != nil {
		return InterruptResult{}, err
	}
	if res, attempted, cancelErr := s.cancelTicketLoop(ctx, w.TicketID, contracts.TaskCancelCauseUserInterrupt); attempted {
		if cancelErr == nil && res.Canceled {
			taskRunID := uint(0)
			if run != nil {
				taskRunID = run.ID
			}
			return InterruptResult{
				TicketID:  w.TicketID,
				WorkerID:  w.ID,
				Mode:      "ticket_loop_cancel",
				TaskRunID: taskRunID,
				LogPath:   strings.TrimSpace(w.LogPath),
			}, nil
		}
	}
	if run == nil {
		return InterruptResult{}, fmt.Errorf("worker 当前没有可中断的活跃任务: w%d", workerID)
	}
	reason := fmt.Sprintf("worker interrupt requested: w%d run=%d", w.ID, run.ID)
	if err := rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "interrupt_requested",
		Note:      reason,
		Payload: map[string]any{
			"worker_id": w.ID,
			"ticket_id": w.TicketID,
			"source":    "worker.interrupt",
		},
		CreatedAt: now,
	}); err != nil {
		return InterruptResult{}, err
	}
	if err := rt.MarkRunCanceled(ctx, run.ID, "manual_interrupt", reason, now); err != nil {
		return InterruptResult{}, err
	}
	_ = rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "task_canceled",
		FromState: map[string]any{
			"orchestration_state": run.OrchestrationState,
		},
		ToState: map[string]any{
			"orchestration_state": contracts.TaskCanceled,
		},
		Note: reason,
		Payload: map[string]any{
			"source": "worker.interrupt",
		},
		CreatedAt: now,
	})
	_ = s.appendWorkerTaskEvent(ctx, w.ID, "interrupt_sent", fmt.Sprintf("run=%d", run.ID), map[string]any{
		"mode":        "task_cancel",
		"task_run_id": run.ID,
		"log_path":    strings.TrimSpace(w.LogPath),
	}, now)
	// 事件触发：尽快做一次语义观测（例如中断后可能马上回到 prompt / 报错 / 等待输入）。
	_ = s.RequestWorkerSemanticWatch(ctx, w.ID, time.Now())

	return InterruptResult{
		TicketID:  w.TicketID,
		WorkerID:  w.ID,
		Mode:      "task_cancel",
		TaskRunID: run.ID,
		LogPath:   strings.TrimSpace(w.LogPath),
	}, nil
}
