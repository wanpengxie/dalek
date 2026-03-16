package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"dalek/internal/services/core"
	daemonsvc "dalek/internal/services/daemon"
	gatewaysendsvc "dalek/internal/services/gatewaysend"
	pmsvc "dalek/internal/services/pm"
)

type DaemonPaths = daemonsvc.ProcessPaths
type DaemonStatus = daemonsvc.ProcessStatus

func (h *Home) ResolveDaemonPaths() (DaemonPaths, error) {
	if h == nil {
		return DaemonPaths{}, ErrNotInitialized
	}
	cfg := h.Config.WithDefaults().Daemon
	return daemonsvc.ResolveProcessPaths(h.Root, daemonsvc.ProcessPathConfig{
		PIDFile:  strings.TrimSpace(cfg.PIDFile),
		LockFile: strings.TrimSpace(cfg.LockFile),
		LogFile:  strings.TrimSpace(cfg.LogFile),
	})
}

func EnsureDaemonPaths(paths DaemonPaths) error {
	return daemonsvc.EnsureProcessPaths(paths)
}

func InspectDaemon(paths DaemonPaths) (DaemonStatus, error) {
	return daemonsvc.Inspect(paths)
}

func RemoveDaemonPID(paths DaemonPaths) error {
	return daemonsvc.RemovePID(strings.TrimSpace(paths.PIDFile))
}

func TerminateDaemonPID(pid int) error {
	return daemonsvc.TerminatePID(pid)
}

func WaitDaemonExit(pid int, timeout time.Duration) error {
	return daemonsvc.WaitForExit(pid, timeout)
}

func RunDaemon(ctx context.Context, paths DaemonPaths, logger *slog.Logger) error {
	logger = core.EnsureLogger(logger).With("app", "daemon")
	home, err := OpenHome(paths.HomeDir)
	if err != nil {
		return err
	}
	if err := EnsureHomeSecrets(home); err != nil {
		return err
	}
	cfg := home.Config.WithDefaults()

	registry := NewProjectRegistry(home)
	resolver := newDaemonProjectResolver(home, registry)
	manager := newDaemonManagerComponent(home, logger, registry)
	notebook := newDaemonNotebookComponent(home, logger, cfg.Daemon.Notebook.WorkerCount, registry)
	host, err := daemonsvc.NewExecutionHost(resolver, daemonsvc.ExecutionHostOptions{
		Logger:        logger,
		MaxConcurrent: cfg.Daemon.MaxConcurrent,
		OnRunSettled:  manager.NotifyProject,
		OnNoteAdded:   notebook.NotifyProject,
	})
	if err != nil {
		return err
	}
	manager.setDispatchHost(host)
	internalSendDB, err := home.OpenGatewayDB()
	if err != nil {
		return err
	}
	gatewayResolver := newDaemonGatewayProjectResolver(home, registry)
	gatewaySender := newDaemonFeishuSender(cfg.Daemon.Public.Feishu, logger)
	manager.setStatusChangeHookFactory(func(projectName string, p *Project) pmsvc.WorkflowStatusChangeHook {
		if p == nil || p.core == nil || p.core.DB == nil {
			return nil
		}
		notifierLogger := logger.With("project", strings.TrimSpace(projectName), "service", "pm_status_notifier")
		return pmsvc.NewGatewayStatusNotifier(projectName, p.core.DB, internalSendDB, gatewayResolver, gatewaySender, notifierLogger)
	})
	internalAPI, err := daemonsvc.NewInternalAPI(host, daemonsvc.InternalAPIConfig{
		ListenAddr:     strings.TrimSpace(cfg.Daemon.Internal.Listen),
		AllowCIDRs:     append([]string(nil), cfg.Daemon.Internal.AllowCIDRs...),
		NodeAgentToken: strings.TrimSpace(cfg.Daemon.Internal.NodeAgentToken),
	}, daemonsvc.InternalAPIOptions{
		Logger:              logger,
		GatewaySendDB:       internalSendDB,
		GatewayResolver:     gatewayResolver,
		GatewaySendResolver: gatewayResolver,
		GatewaySendSender:   gatewaySender,
		GatewayQueueDepth:   cfg.Gateway.QueueDepth,
		CloseGatewaySendDB:  true,
		NodeProjectResolver: resolver,
	})
	if err != nil {
		_ = closeDaemonGatewayDB(internalSendDB)
		return err
	}
	sendSweeper := gatewaysendsvc.NewSweeperWithDB(internalSendDB, gatewayResolver, gatewaySender, logger, gatewaysendsvc.SweeperOptions{})

	d, err := daemonsvc.New(paths, daemonsvc.Options{
		Logger: logger,
		Components: []daemonsvc.Component{
			newProjectRegistryComponent(registry),
			host,
			internalAPI,
			sendSweeper,
			newDaemonPublicGatewayComponent(home, logger, gatewayResolver),
			manager,
			notebook,
		},
	})
	if err != nil {
		return err
	}
	return d.Run(ctx)
}
