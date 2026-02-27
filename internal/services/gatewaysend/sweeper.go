package gatewaysend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

const (
	defaultSweeperInterval = 30 * time.Second
	defaultSweeperBatch    = 20
)

type SweeperOptions struct {
	Interval    time.Duration
	BatchSize   int
	RetryPolicy RetryPolicy
}

type Sweeper struct {
	repo     Repository
	service  *Service
	logger   *slog.Logger
	interval time.Duration
	batch    int
	now      func() time.Time

	mu         sync.Mutex
	running    bool
	cancel     context.CancelFunc
	stopParent func() bool
	wg         sync.WaitGroup
}

func NewSweeper(repo Repository, sender MessageSender, resolver contracts.ProjectMetaResolver, logger *slog.Logger, opt SweeperOptions) *Sweeper {
	if sender == nil {
		sender = &NoopSender{}
	}
	svc := NewService(repo, sender, resolver, logger)
	policy := DefaultRetryPolicy()
	if !isZeroRetryPolicy(opt.RetryPolicy) {
		policy = opt.RetryPolicy.normalize()
	}
	svc.policy = policy
	interval := opt.Interval
	if interval <= 0 {
		interval = defaultSweeperInterval
	}
	batch := opt.BatchSize
	if batch <= 0 {
		batch = defaultSweeperBatch
	}
	nowFn := time.Now
	svc.now = nowFn
	return &Sweeper{
		repo:     repo,
		service:  svc,
		logger:   core.EnsureLogger(logger).With("service", "gateway_send_sweeper"),
		interval: interval,
		batch:    batch,
		now:      nowFn,
		stopParent: func() bool {
			return true
		},
	}
}

func NewSweeperWithDB(db *gorm.DB, resolver contracts.ProjectMetaResolver, sender MessageSender, logger *slog.Logger, opt SweeperOptions) *Sweeper {
	return NewSweeper(NewGormRepository(db), sender, resolver, logger, opt)
}

func (sw *Sweeper) Name() string {
	return "gateway_send_sweeper"
}

func (sw *Sweeper) Start(ctx context.Context) error {
	if sw == nil || sw.repo == nil || sw.service == nil {
		return fmt.Errorf("gateway send sweeper 未初始化")
	}
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.running {
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	stopParent := func() bool { return true }
	if ctx != nil {
		stopParent = context.AfterFunc(ctx, cancel)
	}
	sw.cancel = cancel
	sw.stopParent = stopParent
	sw.running = true
	sw.wg.Add(1)
	go func() {
		defer sw.wg.Done()
		sw.loop(runCtx)
	}()
	return nil
}

func (sw *Sweeper) Stop(ctx context.Context) error {
	if sw == nil {
		return nil
	}
	sw.mu.Lock()
	if !sw.running {
		sw.mu.Unlock()
		return nil
	}
	cancel := sw.cancel
	stopParent := sw.stopParent
	sw.running = false
	sw.cancel = nil
	sw.stopParent = nil
	sw.mu.Unlock()

	if stopParent != nil {
		stopParent()
	}
	if cancel != nil {
		cancel()
	}

	done := make(chan struct{})
	go func() {
		sw.wg.Wait()
		close(done)
	}()
	if ctx == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sw *Sweeper) RunOnce(ctx context.Context) (int, error) {
	if sw == nil || sw.repo == nil || sw.service == nil {
		return 0, fmt.Errorf("gateway send sweeper 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sw.service.now = sw.nowOrDefault
	items, err := sw.repo.FindRetryableOutbox(ctx, sw.nowOrDefault(), sw.batch)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		if err := sw.service.sendRetryableOutbox(ctx, item); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return len(items), err
			}
			sw.logger.Warn("gateway send retry failed",
				"outbox_id", item.state.outbox.ID,
				"binding_id", item.binding.ID,
				"retry_count", item.state.outbox.RetryCount,
				"error", err,
			)
			continue
		}
		sw.logger.Info("gateway send retry success",
			"outbox_id", item.state.outbox.ID,
			"binding_id", item.binding.ID,
			"retry_count", item.state.outbox.RetryCount+1,
		)
	}
	return len(items), nil
}

func (sw *Sweeper) loop(ctx context.Context) {
	if _, err := sw.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		sw.logger.Warn("gateway send sweeper run failed", "error", err)
	}
	ticker := time.NewTicker(sw.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := sw.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				sw.logger.Warn("gateway send sweeper tick failed", "error", err)
			}
		}
	}
}

func (sw *Sweeper) nowOrDefault() time.Time {
	if sw != nil && sw.now != nil {
		return sw.now()
	}
	return time.Now()
}

func isZeroRetryPolicy(p RetryPolicy) bool {
	return p.MaxRetries == 0 && p.InitialBackoff == 0 && p.MaxBackoff == 0 && p.BackoffFactor == 0
}
