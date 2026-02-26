# HIGH 架构债务（必须清零）

> 来源：`docs/arch_debt/source/ARCH_AUDIT_REPORT_2026-02-26.md`
> 生成日期：2026-02-26
> 条目数：44

## cmd/dalek（CLI 入口层）

- `CMD-H1` `cmd/dalek/cmd_gateway_ws.go` (587行)：**完整 WebSocket 服务器基础设施放在 CLI 层**。包含连接生命周期管理、帧类型定义、Inbox 轮询 goroutine、Turn 处理流水线、Agent 事件流式转发、服务器选项。应下沉到 `services/channel/ws/`。
- `CMD-H2` `cmd/dalek/cmd_config.go` 第1行：**直接 import internal/repo 绕过 Facade**。用于 local scope 配置读写(`repo.LoadConfigFromDir`/`repo.SaveConfigToDir`)。架构约束测试未覆盖 `repo` 包（只检查 services/* 和 store），是漏网之鱼。应增加 `TestCmdDoesNotImportRepoDirectly` 约束。
- `CMD-H3` `cmd/dalek/cmd_config.go` (828行)：**大量配置解析业务逻辑泄露到 CLI 层**。包含 `resolveConfigValue()`(多 scope 配置合并)、`setConfigValue()`(多 scope 写入)、`buildEffectiveProjectConfig()`(有效配置构建)、JSON 路径解析、配置元数据定义。应迁移到 `app` 层或 `internal/config/`。
- `CMD-H4` `cmd/dalek/cmd_gateway.go` 行200+：**runGatewayChatViaDaemon 包含完整 WebSocket 客户端协议处理**(449行中约200行)。连接建立、ready frame 等待、消息发送、多帧接收循环（4种帧类型）、超时管理、错误分类。应提取到 `internal/gateway/client/`。
- `CMD-H5` `cmd/dalek/cmd_ticket.go`：**usage 打印方式不一致**。大部分子命令用 `printSubcommandUsage` 统一格式，但 ls/create/show/events 四个命令用内联 `usage := func() {...}` 方式。同一命令组两种风格。
- `CMD-H6` `cmd/dalek/cmd_gateway_log.go` 行37-38：**独立解析 DALEK_HOME 环境变量**。`gatewayDailyLogger` 在 `openFile()` 中直接 `os.Getenv("DALEK_HOME")` 获取 home 目录，绕过 `main.go` 中已有的 `mustOpenHome()` 路径。两个独立的 home 路径解析逻辑。

## internal/agent/provider

- `PV-H1` `provider.go` 行4-8：**Provider 接口职责分裂**。三个方法 Name()/BuildCommand()/ParseOutput() 只服务于 CLI 进程执行模式。SDK 模式(run/sdk.go + sdkrunner)完全不使用 Provider 接口。但接口名和位置暗示它是通用抽象。应明确为"CLI 命令构建器"角色或拆分为 CLIBuilder + OutputParser。

## internal/agent/run

- `RN-H1` `process.go` 行66-133 + `sdk.go` 行74-128 + `tmux.go` 行116-154：**三个 executor 300+ 行任务状态管理代码重复**。三处几乎一字不差地执行：CreateRun → MarkRunRunning → AppendEvent → MarkRunSucceeded/Failed/Canceled。应提取 `runLifecycleManager` 封装所有 TaskRuntime 操作。
- `RN-H2` `sdk.go` 行18-48：**SDKConfig 24 字段膨胀**。承担"SDK 执行配置"+"tmux 实时回显配置"+"任务运行时跟踪配置"三种职责。很多 tmux playback 字段与 SDK 执行无关。应拆分为 SDKExecConfig + PlaybackConfig + RuntimeTrackingConfig。
- `RN-H3` `tmux.go` 行272-293：**TmuxHandle.Wait() 使用 500ms 轮询无 context cancellation**。忙等待无 `case <-ctx.Done()`，无超时保护。任务永不终止则 goroutine 永远轮询。应添加 context 参数并监听 cancellation。

## internal/agent/sdkrunner

- `SR-H1` `runner.go` (798行)：**798 行单文件巨模块**。包含接口定义、DefaultTaskRunner、Claude 权限设置 JSON(200行)、Run() 函数、runCodex/runClaude、Claude SDK 消息转换、Codex 事件提取、reasoning effort 映射、环境变量处理、项目路径解析等。应至少拆分为 runner.go + codex.go + claude.go + settings.go + env.go。
- `SR-H2` `runner.go` 行67-206：**200 行 Claude 权限设置 JSON 字符串常量硬编码在源码**。修改任何权限规则需修改 Go 源码重编译。不同项目可能需要不同权限配置。应使用 `go:embed` 嵌入 .json 文件或提供配置覆盖机制。

## internal/app（Facade 层）

- `APP-H1` `internal/app/facade_types.go` (210行)：**类型别名策略制造封装假象**。48 个 `type X = store.X` / `type Y = contracts.Y` 重导出。类型别名完全透明，编译器不阻止直接访问 store 内部字段，store 类型变化直接穿透 Facade。还包含不应出现的 `TmuxSocketDir()`/`ListTmuxSocketFiles()`/`KillTmuxServer()` 等 tmux 工具函数。`api_types.go` 已展示正确做法（独立定义 `TaskStatus`/`TicketView`），但两种风格并存。
- `APP-H2` `internal/app/project.go` (975行)：**膨胀和机械透传模式**。60+ 个公开方法，67 处 strings.TrimSpace，大量方法呈现"入参 TrimSpace → 调下层 → 出参 TrimSpace"的机械模式。nil guard 模板代码重复 60+ 次（`if p == nil || p.xxx == nil`），但 Project 实例不可能在正常使用中为 nil。
- `APP-H3` `internal/app/daemon_manager_component.go` (879行)：**daemon component 直接操作 store 绕过 service 层**。`recoverStuckDispatchJobs()` 直接操作 `store.PMDispatchJob`/`store.TaskRun`；`reconcileWorkerSessions()` 直接访问 worker 内部方法；`checkExpiredDispatchLeases()` 大量直接 DB 事务。同一函数中一半走 service 一半直接 DB，风格不一致。应将恢复/对账逻辑下沉到 `pm.RecoverDispatchJobs`/`task.RecoverStuckRuns` 等 service 方法。
- `APP-H4` `internal/app/gateway_facade.go`：**类型别名暴露 channelsvc 全部内部类型**。12 个类型别名（Gateway, ChannelService, GatewayOptions 等）。更严重的是 `ChannelService()` 方法(project.go:112-117)直接返回 `*channelsvc.Service`，彻底破坏封装。
- `APP-H5` `internal/app/action_executor.go` 行109：**executeTicketDetail 直接访问 `e.project.core.DB`**。`e.project.core.DB.WithContext(ctx).First(&tk, ticketID)` 绕过 ticket service。同文件其他方法（如 `executeListTickets`）正确使用 `e.project.ListTickets()`。显示该处是遗漏。
- `APP-H6` `internal/app/home.go` 行467,470 + `project_subagent.go` 行462：**硬编码模型名 "gpt-5.3-codex" 出现 3 处**。`applyAgentProviderModel` 和 `resolveSubagentAgentSettings` 都包含硬编码默认模型。应提取为常量或放到 agent/provider 配置中。

## internal/contracts（跨层共享类型）

- `CT-H1` 整个包：**承载范围过窄未发挥解耦作用**。只定义 7 个类型 + 1 个接口（跨模块通信协议）。但系统中 20+ 个核心领域类型和 10+ 组状态枚举全在 store 中。contracts 没真正发挥"跨层共享类型定义"作用，让 store 成为了"类型中心"（99 个文件 import）。应扩展为"领域类型+跨层接口"或新建 core/model 包。

## internal/infra（基础设施工具）

- `IF-H1` `openai_compat.go` (213行)：**OpenAI 兼容客户端不应在 infra 层**。infra 定位是系统级工具（exec/git/tmux/text/shell），但 openai_compat 是 AI 业务基础设施。未来加 Anthropic/Gemini 客户端 infra 会膨胀为"AI SDK 集合"。应移到 `agent/provider/` 或 `agent/openai/`。

## internal/services/channel（Channel 服务 + agentcli）

- `CH-H1` `service.go` 行380-563 + `gateway_runtime.go` 行490-839：**Service 与 Gateway 双路径持久化逻辑大面积重复且已开始分裂**。两套几乎相同的 "ensure binding → ensure conversation → create inbound → create turn job" 流程。两套 `ensureBinding`/`ensureConversation` 核心逻辑一致仅参数来源不同。Gateway 版本已多出 `project` 字段和 `peer_project_key` 处理而 Service 版本没有——分裂已经发生。应提取统一的 `inboundPersistence` 组件。
- `CH-H2` `service.go` 行565-882：**runTurnJob 方法膨胀到 318 行承担过多职责**。串联：Claim job + lease → DB 加载 entities → 初始化 event collector/logger → turn context(超时/cancel) → 调用 agent(SDK/CLI) → 解析 TurnResponse + 执行 actions → pending actions → 写 outbound + outbox → 发送 outbox → 状态流转。应拆分为 `claimAndLoadTurnContext`/`executeTurnAgent`/`processTurnResponse`/`persistTurnResult` 四个子方法。
- `CH-H3` `pending_actions.go` (679行)：**混合 CRUD/审批决策/action执行三种职责**。约 1/3 是 view 转换和辅助函数。应拆分为 `pending_action_store.go`(CRUD) + `pending_action_workflow.go`(审批) + 将 `executeAction`/`renderActionExecutionSummary` 移入 action_executor.go。
- `CH-H4` 24 个文件共 566 处：**strings.TrimSpace 过度防御**。包括内部方法间传递已 trimmed 的值再 trim、DB 读取后已 clean 的值、枚举值比较(`strings.TrimSpace(string(status))`)、方法内多次对同一变量重复 trim。`event_bus.go` 的 Publish 一次性 trim GatewayEvent 12个字段（构造时已 trim）；`gateway_runtime.go` 的 `publishFromResult` 中 40+ 处。应定义"数据清洗边界"，内部信任已清洗数据，估计可减少 400+ 处无效调用。
- `CH-H5` `action_executor.go`：**localActionExecutor 直接操作 store 且留在 channel 包内**。直接 import store 用 `e.project.DB` 做 GORM 查询(行56-157)。实现 list_tickets/ticket_detail/create_ticket 三个 action。当 app 层 ActionHandler 注入后这是死代码，但仍建立了 channel→store 耦合。应删除或标记为 test-only。还有 100+ 行参数解析辅助函数(actionArgString 等)应提取到独立文件。

## internal/services/core（Core 领域模型）

- `CR-H1` `project.go` 行12-32：**Project 作为 God Object 承载过多角色**。12 个字段覆盖 4 个完全不同的关注域：身份信息(Name/Key)、文件系统布局(RepoRoot/Layout/ProjectDir/ConfigPath/DBPath/WorktreesDir/WorkersDir)、配置与持久化(Config/DB)、运行时基础设施(Tmux/Git/TaskRuntime)。22 个文件引用它，每个 service 只使用 2-4 个字段但被迫接收整个 Project。测试必须构造完整 Project。

## internal/services/daemon（Daemon 服务）

- `DM-H1` `execution_host.go` (1337行)：**膨胀至 1337 行职责边界模糊**。5 种不同职责混合：任务提交与幂等性管理、任务执行编排、运行状态查询与 project 索引、取消逻辑、handle 生命周期管理。`h.mu` 保护语义完全不同的数据路径。应至少拆分为 3 个文件。
- `DM-H2` `execution_host.go` 行46-231：**DTO 类型爆炸——18 个 public struct 散布**。DispatchSubmitOptions/Request/Receipt、WorkerRunOptions/Result/Request/Receipt、SubagentSubmitOptions/Request/Receipt、NoteSubmitRequest/Receipt、RunStatus/RunEvent/CancelResult 等。约 180 行纯类型定义与复杂并发逻辑混在一起。许多 request/receipt 字段高度重复。应移到 `execution_host_types.go`。
- `DM-H3` 全部文件 168 处：**strings.TrimSpace 过度防御**。`handle.project` 构造时已 TrimSpace，后续不同方法中重复调用 5-10 次。api_internal.go 的 receipt 字段构造时已 trim 但 writeJSON 前又全部 trim。168 处噪音淹没业务逻辑。应入口校验一次存储后信任。

## internal/services/gatewaysend（Gateway Send 服务）

- `GS-H1` `send.go` (565行)：**单文件混合 5 层职责**。HTTP handler 层(行85-143)、业务逻辑层(行145-252)、去重逻辑(行254-311)、持久化状态机(行313-472：createPending/markSending/markSent/markFailed)、工具函数(行474-564)。应拆分为 send_outbox.go(~170行) + send_helpers.go(~90行)。

## internal/services/logs（日志预览服务）

- `LG-H1` 整个包：**包名与职责不匹配**。`logs` 暗示通用日志管理，实际只做 tmux capture-pane 抓取 worker session 屏幕输出尾部。代码注释承认"当前实现以 tmux capture-pane 作为最小可用的 tail 预览源"。当"真正的日志服务"需创建时会命名冲突。应重命名为 `preview` 或 `tmuxpreview`。

## internal/services/pm（PM 服务）

- `PM-H1` 16+ 处分散：**硬编码时间常量散布且含语义重复**。workerReadyTimeout `8s` 在 service.go:29 和 worker_ready_wait.go:14 各定义一次；workerReadyPollInterval `200ms` 同样两处；pollInterval `100ms` 在 dispatch.go:120 和 dispatch_queue.go:694 各一次；tmux timeout `2s` 在 session.go:47 和 direct_dispatch.go:103 各一次；lease TTL `2*time.Minute` 在三处重复。修改时必须同时改多处否则行为不一致。应新建 `constants.go` 集中定义。
- `PM-H2` `dispatch_agent_exec.go` 行200-249：**内嵌巨大 prompt 模板**（250 行文件中 40 行纯文本 XML prompt）。包含角色定义、规则列表、输出契约。prompt 演进需修改 Go 代码重编译，非技术人员无法审查。应抽取为 `embed.FS` 外部文件或独立的 `dispatch_prompt.go`。
- `PM-H3` `manager_tick.go` (595行)：**ManagerTick 是 410 行控制流巨函数**（行77-488）。串行完成：事件消费 → running worker 扫描 → autopilot 检查 → merge queue → capacity 调度 → 状态落盘。多层嵌套循环和复杂条件分支。应拆分为 `consumeTaskEvents`/`scanRunningWorkers`/`proposeMergeItems`/`scheduleQueuedTickets` 子方法。
- `PM-H4` `workflow_notify.go` 行12：**引入 gatewaysend 服务的横向依赖**。`GatewayStatusNotifier` 直接 import 并使用 `gatewaysendsvc.MessageSender`/`gatewaysendsvc.SendProjectText`。pm 已定义 `WorkflowStatusChangeHook` 接口，但实现放在 pm 包内削弱了抽象。应将 `GatewayStatusNotifier` 移到 app 层或独立 notifier 包。
- `PM-H5` `dispatch_queue.go` (720行)：**dispatch_queue 膨胀职责过重**。包含 ID 生成、job enqueue、job claim、lease renew、job success/fail completion、ticket status promote/demote、force fail、dispatch job query、wait polling。混合了队列操作/状态机转换/ticket workflow 推进三种不同层次。应将 promote/demote 提取到 workflow.go。

## internal/services/task（Task 服务）

- `TS-H1` `core/task_runtime.go` 行35-107 + `task/runtime_adapter.go` 全文件(150行) + `task/service_runs.go` 行15-40 + `task/service_events.go` 多处：**core.TaskRuntime 接口和 task.Service 之间完整 Input 类型镜像**。5 对几乎完全相同的 Input struct：CreateRunInput/EventInput/RuntimeSampleInput/SemanticReportInput/ListStatusOptions 各在 core 和 task 中定义一份。runtime_adapter.go 150 行中 ~130 行是逐字段复制。完全符合 ARCH_GOVERNANCE.md 案例 C。每次扩展需改三处。
- `TS-H2` `core/task_runtime.go` 行109-120 + `task/service_helpers.go` 行10-21：**NextActionToSemanticPhase 在 core 和 task 包中重复定义**。两处完全相同的函数实现。

## internal/services/ticket（Ticket 服务）

- `TK-H1` `service.go` 全文件：**ticket service 是空壳——只有 CRUD 缺失生命周期管理**。只提供 Create/List/BumpPriority/UpdateText。核心操作全不在 ticket 包：start/stop 在 worker；dispatch 在 pm；archive 在 app；workflow 状态枚举和转换规则分散在 store/worker/views.go/app 等。"ticket 的工作流规则在哪里"没有单一权威答案。应增加 `TransitionWorkflow`/`CanTransition`/`GetByID`/`HasActiveDispatch`。

## internal/services/worker（Worker 服务）

- `WK-H1` `views.go` 行107-338：**ListTicketViews 是 230 行 God Method**。一个方法聚合：查 tickets、查 workers、探测所有 tmux sockets session 存活性、查 task runtime 状态、查 dispatch jobs、计算 capability、计算 derived runtime health。测试需同时 mock 所有依赖。capability 已提取为纯函数（正确方向），但 derived health 计算还嵌在主函数体内。应拆分为 data fetching 层 + computation 层。
- `WK-H2` `views.go` 行13-105：**TicketView 和 computeTicketCapability 不应在 worker 包内**。`TicketView` struct 和 `computeTicketCapability` 函数定义在 worker 包，但语义是"ticket 的完整视图"和"ticket 可执行的操作能力"——属于 ticket 领域。因计算需要 worker 状态数据就放在 worker 包，导致 ticket 行为语义分散到两个包。应提升到 app 层或独立 query service。

## internal/store（持久化层）

- `ST-H1` `models.go` (623行)：**类型定义与持久化逻辑混合**（根源问题）。同时承担领域实体数据结构（20+ struct）+ 状态枚举（10+ 组常量）+ 数据库操作。上层 `facade_types.go` 120+ 行别名转发是其后果。全系统 99 个文件 import store 很多只为使用类型。应将领域类型迁移到 contracts 或 core/model。
- `ST-H2` `db.go` 行49-101 + 行310-426：**AutoMigrate 嵌入大量破坏性迁移逻辑且无版本化**。每次启动执行：8 处 DROP COLUMN、1 处 DROP TABLE、6 个条件分支 UPDATE(含 CASE WHEN)、多处 DROP INDEX + 重建、DROP VIEW + 重建。有幂等保护但每次启动做无用"尝试删除已删除列"。应引入 schema_migrations 版本号表。

## 跨 worker/ticket/task 关系

- `XWT-R2` （跨模块/未注明文件行）：**ticket 职责在 worker/app/pm 之间碎片化**。Create/List/BumpPriority/UpdateText 在 ticket；StopTicket/AttachCmd/ListTicketViews/CleanupWorktreeWorktree/computeTicketCapability 在 worker；Archive/workflow 状态转换在 app；Dispatch 在 pm。没有单一位置回答"ticket 支持哪些操作"。
