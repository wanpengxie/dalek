package app

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	agentprovider "dalek/internal/agent/provider"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/services/core"
	notebooksvc "dalek/internal/services/notebook"
	pmsvc "dalek/internal/services/pm"
	previewsvc "dalek/internal/services/preview"
	subagentsvc "dalek/internal/services/subagent"
	tasksvc "dalek/internal/services/task"
	ticketsvc "dalek/internal/services/ticket"
	workersvc "dalek/internal/services/worker"

	"gorm.io/gorm"
)

// Project 是对"单个已打开项目"的应用层 Facade。
//
// 约束：
// - cmd/tui 只能依赖 app，不应直接依赖下层实现包。
// - Project 不承载业务流程实现，只是把 services 组合成一个尽量稳定的 API。
//
// 方法按职责拆分到以下文件：
//   - project_ticket.go      ticket 查询与写入
//   - project_worker.go      worker 生命周期（interrupt/stop/attach/cleanup）
//   - project_dispatch.go    dispatch & start
//   - project_task.go        task 执行观测与 subagent
//   - project_manager.go     PM manager 管理
//   - project_inbox_merge.go inbox 与 merge queue
type Project struct {
	core *core.Project

	ticket      *ticketsvc.Service
	ticketQuery *ticketsvc.QueryService
	worker      *workersvc.Service
	preview     *previewsvc.Service
	notebook    *notebooksvc.Service
	pm          *pmsvc.Service
	subagent    *subagentsvc.Service
	task        *tasksvc.Service
	channel     *channelsvc.Service

	closeOnce sync.Once
	closeErr  error
}

func (p *Project) Name() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.Name)
}

func (p *Project) Key() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.Key)
}

func (p *Project) RepoRoot() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.RepoRoot)
}

func (p *Project) ProjectDir() string {
	if p == nil || p.core == nil {
		return ""
	}
	return p.core.ProjectDir()
}

func (p *Project) TmuxSocket() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.Config.WithDefaults().TmuxSocket)
}

func (p *Project) RefreshInterval() time.Duration {
	if p == nil || p.core == nil {
		return time.Second
	}
	ms := p.core.Config.WithDefaults().RefreshIntervalMS
	if ms <= 0 {
		return time.Second
	}
	return time.Duration(ms) * time.Millisecond
}

func (p *Project) PMDispatchTimeout() time.Duration {
	if p == nil || p.core == nil {
		return 0
	}
	ms := p.core.Config.WithDefaults().PMDispatchTimeoutMS
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (p *Project) GatewayTurnTimeout() time.Duration {
	if p == nil || p.core == nil {
		return 0
	}
	ms := p.core.Config.WithDefaults().GatewayAgent.TurnTimeoutMS
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// ChannelService 暂时暴露底层 channel service 以兼容 gateway 链路。
// 这是一个已知的 facade 边界泄露点：仅允许在 gateway 相关 cmd 中受控使用，
// 后续应通过专门的 gateway facade/runtime 接口收敛，避免扩散到更多调用方。
func (p *Project) ChannelService() *channelsvc.Service {
	if p == nil {
		return nil
	}
	return p.channel
}

func (p *Project) OpenDBForTest() (*gorm.DB, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return nil, fmt.Errorf("project db 为空")
	}
	return p.core.DB, nil
}

func (p *Project) DBPath() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.DBPath())
}

func (p *Project) ConfigPath() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.ConfigPath())
}

func (p *Project) SchemaVersion() int {
	if p == nil || p.core == nil {
		return 0
	}
	return p.core.Config.SchemaVersion
}

func (p *Project) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		errs := make([]error, 0, 2)
		if p.channel != nil {
			if err := p.channel.Close(); err != nil {
				errs = append(errs, fmt.Errorf("关闭 channel service 失败: %w", err))
			}
		}
		if p.core != nil && p.core.DB != nil {
			sqlDB, err := p.core.DB.DB()
			if err != nil {
				errs = append(errs, fmt.Errorf("获取 db 连接失败: %w", err))
			} else if err := sqlDB.Close(); err != nil {
				errs = append(errs, fmt.Errorf("关闭 db 失败: %w", err))
			}
		}
		p.closeErr = errors.Join(errs...)
	})
	return p.closeErr
}

func (p *Project) ApplyAgentProviderModel(provider, model string) error {
	if p == nil || p.core == nil {
		return fmt.Errorf("project 为空")
	}
	provider = agentprovider.NormalizeProvider(provider)
	model = strings.TrimSpace(model)
	if provider != "" && !agentprovider.IsSupportedProvider(provider) {
		return fmt.Errorf("agent provider 仅支持 codex|claude: %s", provider)
	}
	cfg := applyAgentProviderModel(p.core.Config, provider, model)
	p.core.Config = cfg.WithDefaults()
	return nil
}
