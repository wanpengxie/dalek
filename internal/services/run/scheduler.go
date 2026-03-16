package run

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type SchedulerOptions struct {
	NodeCapacity         map[string]int
	ProjectCapacity      map[string]int
	DefaultProjectFactor int
}

type RunScheduler struct {
	mu             sync.Mutex
	cond           *sync.Cond
	capacity       map[string]int
	projectCap     map[string]int
	queues         map[string][]queuedRun
	running        map[uint]runningRun
	workspaceInUse map[string]uint
}

type queuedRun struct {
	RunID        uint
	ProjectKey   string
	WorkspaceKey string
}

type runningRun struct {
	RunID        uint
	NodeName     string
	ProjectKey   string
	WorkspaceKey string
}

func NewRunScheduler(opt SchedulerOptions) *RunScheduler {
	capacity := make(map[string]int, len(opt.NodeCapacity))
	projectCap := make(map[string]int, len(opt.NodeCapacity))
	for name, limit := range opt.NodeCapacity {
		name = strings.TrimSpace(name)
		if name == "" || limit <= 0 {
			continue
		}
		capacity[name] = limit
		projectLimit := normalizeProjectLimit(limit, opt.ProjectCapacity[name], opt.DefaultProjectFactor)
		projectCap[name] = projectLimit
	}
	s := &RunScheduler{
		capacity:       capacity,
		projectCap:     projectCap,
		queues:         map[string][]queuedRun{},
		running:        map[uint]runningRun{},
		workspaceInUse: map[string]uint{},
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *RunScheduler) Enqueue(nodeName string, runID uint, projectKey, workspaceKey string) error {
	if s == nil {
		return fmt.Errorf("run scheduler 未初始化")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return fmt.Errorf("node_name 不能为空")
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	projectKey = strings.TrimSpace(projectKey)
	if projectKey == "" {
		return fmt.Errorf("project_key 不能为空")
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return fmt.Errorf("workspace_key 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.running[runID]; exists {
		return nil
	}
	for _, item := range s.queues[nodeName] {
		if item.RunID == runID {
			return nil
		}
	}
	s.queues[nodeName] = append(s.queues[nodeName], queuedRun{
		RunID:        runID,
		ProjectKey:   projectKey,
		WorkspaceKey: workspaceKey,
	})
	return nil
}

func (s *RunScheduler) ScheduleNext(nodeName string) (uint, bool) {
	if s == nil {
		return 0, false
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := s.capacity[nodeName]
	if limit <= 0 {
		limit = 1
	}
	runningCount := 0
	for _, entry := range s.running {
		if entry.NodeName == nodeName {
			runningCount++
		}
	}
	if runningCount >= limit {
		return 0, false
	}
		queue := s.queues[nodeName]
		for i, item := range queue {
			if _, inUse := s.workspaceInUse[item.WorkspaceKey]; inUse {
				continue
			}
			if s.projectRunningCountLocked(nodeName, item.ProjectKey) >= s.projectLimit(nodeName) {
				continue
			}
			runID := item.RunID
			s.running[runID] = runningRun{
				RunID:        runID,
				NodeName:     nodeName,
				ProjectKey:   item.ProjectKey,
				WorkspaceKey: item.WorkspaceKey,
			}
			s.workspaceInUse[item.WorkspaceKey] = runID
		s.queues[nodeName] = append(queue[:i], queue[i+1:]...)
		return runID, true
	}
	return 0, false
}

func (s *RunScheduler) Finish(runID uint) {
	if s == nil || runID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.running[runID]
	if !ok {
		return
	}
	delete(s.running, runID)
	if s.workspaceInUse[entry.WorkspaceKey] == runID {
		delete(s.workspaceInUse, entry.WorkspaceKey)
	}
	s.cond.Broadcast()
}

func (s *RunScheduler) Acquire(ctx context.Context, nodeName string, runID uint, projectKey, workspaceKey string) error {
	if s == nil {
		return fmt.Errorf("run scheduler 未初始化")
	}
	if err := s.Enqueue(nodeName, runID, projectKey, workspaceKey); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			if s.cond != nil {
				s.cond.Broadcast()
			}
			s.mu.Unlock()
		case <-done:
		}
	}()
	defer close(done)

	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			s.removeQueuedRunLocked(strings.TrimSpace(nodeName), runID)
			return err
		}
		limit := s.capacity[strings.TrimSpace(nodeName)]
		if limit <= 0 {
			limit = 1
		}
			runningCount := 0
			for _, entry := range s.running {
				if entry.NodeName == strings.TrimSpace(nodeName) {
					runningCount++
				}
			}
			if runningCount < limit {
				queue := s.queues[strings.TrimSpace(nodeName)]
				if len(queue) > 0 && queue[0].RunID == runID {
					if _, inUse := s.workspaceInUse[strings.TrimSpace(workspaceKey)]; !inUse &&
						s.projectRunningCountLocked(strings.TrimSpace(nodeName), strings.TrimSpace(projectKey)) < s.projectLimit(strings.TrimSpace(nodeName)) {
						s.running[runID] = runningRun{
							RunID:        runID,
							NodeName:     strings.TrimSpace(nodeName),
							ProjectKey:   strings.TrimSpace(projectKey),
							WorkspaceKey: strings.TrimSpace(workspaceKey),
						}
						s.workspaceInUse[strings.TrimSpace(workspaceKey)] = runID
					s.queues[strings.TrimSpace(nodeName)] = queue[1:]
					return nil
				}
			}
		}
		s.cond.Wait()
	}
}

func (s *RunScheduler) removeQueuedRunLocked(nodeName string, runID uint) {
	queue := s.queues[nodeName]
	for i, item := range queue {
		if item.RunID == runID {
			s.queues[nodeName] = append(queue[:i], queue[i+1:]...)
			return
		}
	}
}

func (s *RunScheduler) projectLimit(nodeName string) int {
	if s == nil {
		return 1
	}
	limit := s.projectCap[strings.TrimSpace(nodeName)]
	if limit <= 0 {
		nodeCap := s.capacity[strings.TrimSpace(nodeName)]
		return normalizeProjectLimit(nodeCap, 0, 0)
	}
	return limit
}

func (s *RunScheduler) projectRunningCountLocked(nodeName, projectKey string) int {
	count := 0
	for _, entry := range s.running {
		if entry.NodeName == nodeName && entry.ProjectKey == projectKey {
			count++
		}
	}
	return count
}

func normalizeProjectLimit(nodeCapacity, configuredLimit, defaultFactor int) int {
	if configuredLimit > 0 {
		if nodeCapacity > 0 && configuredLimit > nodeCapacity {
			return nodeCapacity
		}
		return configuredLimit
	}
	if nodeCapacity <= 1 {
		return 1
	}
	if defaultFactor > 0 && defaultFactor < nodeCapacity {
		return defaultFactor
	}
	return nodeCapacity - 1
}

func (s *RunScheduler) waitForTesting() {
	time.Sleep(10 * time.Millisecond)
}
