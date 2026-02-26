package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

const defaultShutdownTimeout = 10 * time.Second

type Component interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type Options struct {
	Logger          *slog.Logger
	Components      []Component
	ShutdownTimeout time.Duration
}

type Daemon struct {
	paths           ProcessPaths
	logger          *slog.Logger
	components      []Component
	shutdownTimeout time.Duration
}

func New(paths ProcessPaths, opt Options) (*Daemon, error) {
	if strings.TrimSpace(paths.PIDFile) == "" || strings.TrimSpace(paths.LockFile) == "" || strings.TrimSpace(paths.LogFile) == "" {
		return nil, fmt.Errorf("daemon 进程路径不完整")
	}
	if err := EnsureProcessPaths(paths); err != nil {
		return nil, err
	}

	logger := opt.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	shutdownTimeout := opt.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}

	return &Daemon{
		paths:           paths,
		logger:          logger,
		components:      append([]Component(nil), opt.Components...),
		shutdownTimeout: shutdownTimeout,
	}, nil
}

func (d *Daemon) Paths() ProcessPaths {
	return d.paths
}

func (d *Daemon) Run(ctx context.Context) error {
	if d == nil {
		return fmt.Errorf("daemon 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	lock, err := AcquireLock(d.paths.LockFile)
	if err != nil {
		if err == ErrAlreadyRunning {
			return fmt.Errorf("daemon 已在运行（lock=%s）", d.paths.LockFile)
		}
		return err
	}
	defer func() { _ = lock.Release() }()

	if err := WritePID(d.paths.PIDFile, os.Getpid()); err != nil {
		return err
	}
	defer func() { _ = RemovePID(d.paths.PIDFile) }()

	d.logger.Info("daemon started", "pid", os.Getpid())

	started := make([]Component, 0, len(d.components))
	for _, comp := range d.components {
		if comp == nil {
			continue
		}
		name := strings.TrimSpace(comp.Name())
		if name == "" {
			name = "unnamed"
		}
		d.logger.Info("component start", "component", name)
		if err := comp.Start(ctx); err != nil {
			d.logger.Error("component start failed", "component", name, "error", err)
			stopComponentsWithTimeout(d.logger, started, d.shutdownTimeout)
			return fmt.Errorf("组件启动失败（%s）: %w", name, err)
		}
		started = append(started, comp)
	}

	<-ctx.Done()
	d.logger.Info("daemon stopping", "error", ctx.Err())
	stopComponentsWithTimeout(d.logger, started, d.shutdownTimeout)
	d.logger.Info("daemon stopped")
	return nil
}

func stopComponentsWithTimeout(logger *slog.Logger, comps []Component, timeout time.Duration) {
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	active := 0
	for _, comp := range comps {
		if comp != nil {
			active++
		}
	}
	if active == 0 {
		return
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	globalDeadline, _ := stopCtx.Deadline()

	perComponentCap := timeout / time.Duration(active)
	if perComponentCap <= 0 {
		perComponentCap = timeout
	}
	if perComponentCap <= 0 {
		perComponentCap = 200 * time.Millisecond
	}

	for i := len(comps) - 1; i >= 0; i-- {
		comp := comps[i]
		if comp == nil {
			continue
		}
		name := strings.TrimSpace(comp.Name())
		if name == "" {
			name = "unnamed"
		}
		remaining := time.Until(globalDeadline)
		if remaining <= 0 {
			remaining = 50 * time.Millisecond
		}
		componentTimeout := perComponentCap
		if remaining < componentTimeout {
			componentTimeout = remaining
		}
		if componentTimeout <= 0 {
			componentTimeout = 50 * time.Millisecond
		}
		if logger != nil {
			logger.Info("component stop",
				"component", name,
				"timeout", componentTimeout.String(),
				"remaining", remaining.String(),
			)
		}
		componentCtx, componentCancel := context.WithTimeout(stopCtx, componentTimeout)
		startedAt := time.Now()
		err := comp.Stop(componentCtx)
		componentCancel()
		if err != nil && logger != nil {
			logger.Warn("component stop failed", "component", name, "error", err)
		}
		if logger != nil {
			logger.Info("component stop done",
				"component", name,
				"elapsed", time.Since(startedAt).String(),
			)
		}
	}
}
