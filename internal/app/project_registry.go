package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ProjectRegistry keeps opened Project instances alive for the daemon process lifetime.
type ProjectRegistry struct {
	home *Home

	mu       sync.Mutex
	projects map[string]*Project
}

func NewProjectRegistry(home *Home) *ProjectRegistry {
	return &ProjectRegistry{
		home:     home,
		projects: map[string]*Project{},
	}
}

func (r *ProjectRegistry) Open(name string) (*Project, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("project registry 未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name 不能为空")
	}

	r.mu.Lock()
	if p := r.projects[name]; p != nil {
		r.mu.Unlock()
		return p, nil
	}
	r.mu.Unlock()

	opened, err := r.home.OpenProjectByName(name)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	cached := r.projects[name]
	if cached == nil {
		r.projects[name] = opened
		r.mu.Unlock()
		return opened, nil
	}
	r.mu.Unlock()

	// Another goroutine won the race and inserted a shared instance.
	if opened != nil {
		_ = opened.Close()
	}
	return cached, nil
}

func (r *ProjectRegistry) ListOpenProjectNames() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.projects))
	for name, p := range r.projects {
		if p == nil {
			continue
		}
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func (r *ProjectRegistry) CloseAll() error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	opened := r.projects
	r.projects = map[string]*Project{}
	r.mu.Unlock()

	if len(opened) == 0 {
		return nil
	}

	errs := make([]error, 0, len(opened))
	for name, p := range opened {
		if p == nil {
			continue
		}
		if err := p.Close(); err != nil {
			errs = append(errs, fmt.Errorf("关闭 project %s 失败: %w", strings.TrimSpace(name), err))
		}
	}
	return errors.Join(errs...)
}
