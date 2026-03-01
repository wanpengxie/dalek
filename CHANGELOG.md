# Changelog

## v0.1.0 — 2026-03-02

**Dalek is born.** 首个可用版本，多 AI Agent 并行研发管理系统的核心闭环达成。

### Core — 调度引擎

- Ticket 全生命周期管理：backlog → queued → active → blocked → done → archived
- FSM 状态机守护（ticket workflow guards + worker lifecycle table）
- Dispatch 调度引擎：编译 context 并驱动 worker 执行循环
- Worker 隔离执行环境：git worktree + tmux session + runtime process manager
- PM Manager tick 循环：zombie worker 检测与自动恢复
- Dispatch-depth guard 防止嵌套派发
- Inbox 决策队列：自动生成待处理事项（blocker / approval / question / incident）
- Merge queue 代码集成流程（proposed → checks → ready → approved → merged）
- Notebook 需求漏斗：note → shaping → shaped → approved → ticket
- Subagent 异步子任务执行通道
- Ticket 优先级与 backlog 排序
- Ticket 标签（label）元数据支持

### Gateway — 通信平面

- Gateway ingress + send 双向通信架构
- 飞书（Feishu/Lark）集成：chat、slash commands（/help）、quiet mode
- WebSocket 服务端
- GatewaySend outbox + retry fallback 统一发送
- Channel runtime 结构化日志
- 状态通知推送

### TUI — 终端交互

- 基于 charmbracelet（bubbletea + lipgloss）的 TUI 界面
- Ticket table inspector 与表单
- Notebook 管理页面
- Worker run 快捷键（r）提交

### CLI — 命令行

- noun-verb 命令范式（dalek ticket ls / dalek daemon start / ...）
- 结构化输出 `-o json`
- Project init / upgrade 命令
- Version 命令（ldflags 注入版本号）

### Architecture — 架构

- 四平面架构：控制平面 / 执行平面 / 通信平面 / 数据平面
- 大规模架构债务清理（20+ arch-debt tickets）
- 分层边界收紧：cmd → app/services → store
- Contracts 跨模块契约与领域类型迁移
- App facade 模式统一服务访问
- 迁移到 slog 结构化日志
- SQLite schema migrations 版本化
- Type-safe JSON 字段（Value/Scan wrappers）

### Infra — 基础设施

- Daemon 后台常驻进程（start / stop / restart / status / logs）
- Worker runtime + daemon process manager
- 事件溯源审计日志
- `.dalek/control/` 策略层（skills / knowledge / tools / worker）
- Agent kernel + userspace 指令架构

---

*DEVELOP! DEVELOP! DEVELOP!*
