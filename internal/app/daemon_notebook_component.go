package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	defaultNotebookWorkerCount = 2
	defaultNotebookPollGap     = 2 * time.Second
	defaultNotebookShapeGap    = 60 * time.Second
	defaultNotebookWakeQueue   = 64
)

type daemonNotebookComponent struct {
	home     *Home
	registry *ProjectRegistry
	logger   *log.Logger

	workerCount int
	pollGap     time.Duration
	lastShapeMu sync.Mutex
	lastShapeAt map[string]time.Time
	wakeCh      chan string

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func newDaemonNotebookComponent(home *Home, logger *log.Logger, workerCount int, registries ...*ProjectRegistry) *daemonNotebookComponent {
	var registry *ProjectRegistry
	if len(registries) > 0 {
		registry = registries[0]
	}
	if registry == nil && home != nil {
		registry = NewProjectRegistry(home)
	}
	return &daemonNotebookComponent{
		home:        home,
		registry:    registry,
		logger:      logger,
		workerCount: workerCount,
		pollGap:     defaultNotebookPollGap,
		lastShapeAt: make(map[string]time.Time),
		wakeCh:      make(chan string, defaultNotebookWakeQueue),
		stopCh:      make(chan struct{}),
	}
}

func (c *daemonNotebookComponent) Name() string {
	return "notebook_pool"
}

func (c *daemonNotebookComponent) Start(ctx context.Context) error {
	if c == nil || c.home == nil || c.registry == nil {
		return fmt.Errorf("notebook component 未初始化")
	}
	workers := c.workerCount
	if workers <= 0 {
		workers = defaultNotebookWorkerCount
	}
	for i := 0; i < workers; i++ {
		c.wg.Add(1)
		go c.workerLoop(ctx, i+1)
	}
	return nil
}

func (c *daemonNotebookComponent) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
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

func (c *daemonNotebookComponent) workerLoop(ctx context.Context, idx int) {
	defer c.wg.Done()
	pollGap := c.pollGap
	if pollGap <= 0 {
		pollGap = defaultNotebookPollGap
	}
	wakeProject := ""
	for {
		select {
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		didWork := false
		if strings.TrimSpace(wakeProject) != "" {
			didWork = c.processProject(ctx, idx, wakeProject, true)
			wakeProject = ""
		} else {
			didWork = c.processAllProjects(ctx, idx)
		}
		if didWork {
			continue
		}

		select {
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		case wakeProject = <-c.wakeCh:
		case <-time.After(pollGap):
		}
	}
}

func (c *daemonNotebookComponent) processAllProjects(ctx context.Context, idx int) bool {
	projects, err := c.home.ListProjects()
	if err != nil {
		c.logf("notebook worker list projects failed: err=%v", err)
		return false
	}
	didWork := false
	for _, rp := range projects {
		if c.processProject(ctx, idx, rp.Name, false) {
			didWork = true
		}
	}
	return didWork
}

func (c *daemonNotebookComponent) processProject(ctx context.Context, idx int, projectName string, force bool) bool {
	name := strings.TrimSpace(projectName)
	if name == "" {
		return false
	}
	p, err := c.registry.Open(name)
	if err != nil {
		c.logf("notebook worker open project failed: project=%s err=%v", name, err)
		return false
	}
	notebookCfg := p.core.Config.WithDefaults().Notebook
	if !notebookCfg.Enabled || !notebookCfg.AutoShape {
		return false
	}
	shapeGap := time.Duration(notebookCfg.ShapeIntervalSec) * time.Second
	if !c.shouldShapeProject(name, shapeGap, time.Now(), force) {
		return false
	}
	ok, err := p.ProcessOnePendingNote(ctx)
	if err != nil {
		c.logf("notebook worker process note failed: worker=%d project=%s err=%v", idx, name, err)
		return false
	}
	if ok {
		c.logf("notebook worker processed one note: worker=%d project=%s force=%v", idx, name, force)
	}
	return ok
}

func (c *daemonNotebookComponent) NotifyProject(projectName string) {
	if c == nil || c.wakeCh == nil {
		return
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return
	}
	select {
	case c.wakeCh <- projectName:
	default:
		c.logf("notebook worker notify dropped: channel full project=%s", projectName)
	}
}

func (c *daemonNotebookComponent) logf(format string, args ...any) {
	if c == nil || c.logger == nil {
		return
	}
	c.logger.Printf(format, args...)
}

func (c *daemonNotebookComponent) shouldShapeProject(project string, gap time.Duration, now time.Time, force bool) bool {
	if c == nil {
		return false
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return false
	}
	if gap <= 0 {
		gap = defaultNotebookShapeGap
	}
	c.lastShapeMu.Lock()
	defer c.lastShapeMu.Unlock()
	if c.lastShapeAt == nil {
		c.lastShapeAt = make(map[string]time.Time)
	}
	if force {
		c.lastShapeAt[project] = now
		return true
	}
	last := c.lastShapeAt[project]
	if !last.IsZero() && now.Sub(last) < gap {
		return false
	}
	c.lastShapeAt[project] = now
	return true
}
