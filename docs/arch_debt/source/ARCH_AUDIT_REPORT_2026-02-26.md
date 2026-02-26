# dalek 全系统架构盘点报告

> 审计日期：2026-02-26
> 审计方法：按 `docs/ARCH_GOVERNANCE.md` 治理原则，8 个 Opus Agent 并行逐文件深度审计
> 审计范围：cmd/dalek、internal/app、internal/services/*、internal/agent/*、internal/store、internal/contracts、internal/infra、internal/repo
> 代码规模：~45,000+ 行 Go 代码（不含测试）

---

## 一、全局统计

| 严重性 | 数量 |
|--------|------|
| CRITICAL | 11 |
| HIGH | 42 |
| MEDIUM | 47 |
| LOW | 28 |
| **合计** | **128** |

---

## 二、逐模块逐条发现清单

---

### 模块 1: cmd/dalek（CLI 入口层）

**概况**：24 个非测试源文件，~8,850 行；12 个测试文件，~4,880 行。

#### CRITICAL

| ID | 位置 | 描述 |
|----|------|------|
| CMD-C1 | `cmd/dalek/cmd_gateway_feishu.go` (1413行) | **完整飞书 IM 服务实现放在 CLI 层**。包含 HTTP 客户端（含 token 管理/TTL 缓存）、卡片构建器(200+行 JSON)、Markdown 正规化、Webhook handler(400+行)、Slash 命令处理、进度流管道。无法被 daemon 复用，测试需编译整个 cmd 包。应迁移到 `services/channel/feishu/` 或 `adapters/feishu/`。 |
| CMD-C2 | `cmd/dalek/*_test.go` (5个测试文件) | **测试文件绕过 Facade 直接依赖 services/store**。`cmd_gateway_feishu_test.go` 直接 import `services/channel`+`store`；`e2e_cli_test.go` 直接 import `services/channel`+`store`；`cmd_gateway_ws_e2e_test.go` import `repo`+`store`；`e2e_cli_daemon_task_cancel_test.go` import `repo`+`store`；`cmd_gateway_ws_test.go` import `store`。架构约束测试已对非测试文件生效但测试文件被跳过。 |

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| CMD-H1 | `cmd/dalek/cmd_gateway_ws.go` (587行) | **完整 WebSocket 服务器基础设施放在 CLI 层**。包含连接生命周期管理、帧类型定义、Inbox 轮询 goroutine、Turn 处理流水线、Agent 事件流式转发、服务器选项。应下沉到 `services/channel/ws/`。 |
| CMD-H2 | `cmd/dalek/cmd_config.go` 第1行 | **直接 import internal/repo 绕过 Facade**。用于 local scope 配置读写(`repo.LoadConfigFromDir`/`repo.SaveConfigToDir`)。架构约束测试未覆盖 `repo` 包（只检查 services/* 和 store），是漏网之鱼。应增加 `TestCmdDoesNotImportRepoDirectly` 约束。 |
| CMD-H3 | `cmd/dalek/cmd_config.go` (828行) | **大量配置解析业务逻辑泄露到 CLI 层**。包含 `resolveConfigValue()`(多 scope 配置合并)、`setConfigValue()`(多 scope 写入)、`buildEffectiveProjectConfig()`(有效配置构建)、JSON 路径解析、配置元数据定义。应迁移到 `app` 层或 `internal/config/`。 |
| CMD-H4 | `cmd/dalek/cmd_gateway.go` 行200+ | **runGatewayChatViaDaemon 包含完整 WebSocket 客户端协议处理**(449行中约200行)。连接建立、ready frame 等待、消息发送、多帧接收循环（4种帧类型）、超时管理、错误分类。应提取到 `internal/gateway/client/`。 |
| CMD-H5 | `cmd/dalek/cmd_ticket.go` | **usage 打印方式不一致**。大部分子命令用 `printSubcommandUsage` 统一格式，但 ls/create/show/events 四个命令用内联 `usage := func() {...}` 方式。同一命令组两种风格。 |
| CMD-H6 | `cmd/dalek/cmd_gateway_log.go` 行37-38 | **独立解析 DALEK_HOME 环境变量**。`gatewayDailyLogger` 在 `openFile()` 中直接 `os.Getenv("DALEK_HOME")` 获取 home 目录，绕过 `main.go` 中已有的 `mustOpenHome()` 路径。两个独立的 home 路径解析逻辑。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| CMD-M1 | 全局性 | **strings.TrimSpace 过度防御性使用**。几乎所有 flag/参数/返回值都被 TrimSpace。包括 `p.Name()` 返回值（app 层已保证干净）、测试 fixture 自己构造的数据也做 TrimSpace。 |
| CMD-M2 | `cmd/dalek/cmd_gateway_send.go` | **不必要的类型别名**。`type gatewayServeSendPath = app.GatewaySendPath` 和 `type gatewaySendDelivery = app.DaemonGatewaySendDelivery`，在同文件内使用但无语义区分。直接用 `app.GatewaySendPath` 更清晰。 |
| CMD-M3 | `cmd/dalek/cmd_task.go` | **cmd 层包含业务状态推导逻辑**。`deriveRunStatus()` 和 `mapTaskStatusPublic()` 在 cmd 层进行 task 运行状态推导和 DTO 映射，包含业务判断逻辑（如多时间戳确定最新状态）。应移到 `app.Task` 或 `app.Project`。 |
| CMD-M4 | 多处 | **硬编码默认值分散**。`defaultGatewayDaemonWSURL = "ws://127.0.0.1:18081/ws"`、默认路径 "/ws"、默认 sender "ws.user"、各种进程管理路径、日志目录 "gateway/"。应收拢到 `app.Defaults`。 |
| CMD-M5 | `cmd/dalek/e2e_cli_test.go` 行726-853 | **构建 fake agent 的内联 Python 脚本过于复杂**。`installFakeClaudeForE2E()` 包含 50+ 行 Python 脚本用 bash heredoc 嵌入，实现 fake agent 完整路由逻辑。应独立到 `testdata/` 或改为 Go 实现。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| CMD-L1 | `cmd/dalek/cmd_project.go` | **直接 import internal/ui/tui**。为 `project tui` 子命令引入，绕过 app 层。严格来说 TUI 属于表现层，cmd→TUI 可接受，但需确认 TUI 的依赖链只到 app 层。 |
| CMD-L2 | `cmd/dalek/cmd_worker_agent.go` | **直接调用 git 命令**。`cmdWorkerReport` 通过 `exec.Command("git", ...)` 获取 HEAD SHA 和 working tree 状态，而非通过 app 层封装。 |
| CMD-L3 | `cmd/dalek/cmd_gateway_feishu.go` | **log.Printf 使用不一致**。`feishuHTTPSender.GetUserName()` 用 `log.Printf`，同文件其他地方用 `fmtGatewayLog`，日志缺失 gateway 前缀。 |

---

### 模块 2: internal/app（Facade 层）

**概况**：24 个非测试文件，~14,279 行；11 个测试文件。strings.TrimSpace 745 处。核心 struct：Home、Project、ActionExecutor、DaemonAPIClient、ProjectRegistry。

#### CRITICAL

| ID | 位置 | 描述 |
|----|------|------|
| APP-C1 | `internal/app/note.go` (1150行) | **完整 notebook shaping 业务实现放在 Facade 层**。YAML front matter 解析、note 去重（normalized hash + dedup key）、shaping 状态机（open→shaping→shaped）、shaped item CRUD、inbox upsert、note approval/rejection 工作流、遗留状态兼容层。违反 project.go 头部注释声明的"不承载业务流程实现"约束。应创建 `services/notebook/` 包。 |
| APP-C2 | `internal/app/project_subagent.go` (526行) | **完整 subagent 编排实现放在 Facade 层**。直接调用 `sdkrunner.Run()` 执行 agent、管理文件 I/O（prompt.txt/stream.log/result.json）、Provider 解析和 agent 配置组装、完整状态机和事件追踪。直接 import `agent/provider` 和 `agent/sdkrunner`，app 层与底层 agent 实现直接耦合。应创建 `services/subagent/`。 |
| APP-C3 | `internal/app/daemon_public_feishu.go` (2087行) | **完整飞书 IM 适配层放在 Facade 层**。webhook handler、消息解析、卡片构建、消息发送、用户名缓存、事件去重、流式响应中继、Markdown 截断。占 app 包代码量的 ~15%。应迁移到 `services/channel/feishu/`。 |

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| APP-H1 | `internal/app/facade_types.go` (210行) | **类型别名策略制造封装假象**。48 个 `type X = store.X` / `type Y = contracts.Y` 重导出。类型别名完全透明，编译器不阻止直接访问 store 内部字段，store 类型变化直接穿透 Facade。还包含不应出现的 `TmuxSocketDir()`/`ListTmuxSocketFiles()`/`KillTmuxServer()` 等 tmux 工具函数。`api_types.go` 已展示正确做法（独立定义 `TaskStatus`/`TicketView`），但两种风格并存。 |
| APP-H2 | `internal/app/project.go` (975行) | **膨胀和机械透传模式**。60+ 个公开方法，67 处 strings.TrimSpace，大量方法呈现"入参 TrimSpace → 调下层 → 出参 TrimSpace"的机械模式。nil guard 模板代码重复 60+ 次（`if p == nil || p.xxx == nil`），但 Project 实例不可能在正常使用中为 nil。 |
| APP-H3 | `internal/app/daemon_manager_component.go` (879行) | **daemon component 直接操作 store 绕过 service 层**。`recoverStuckDispatchJobs()` 直接操作 `store.PMDispatchJob`/`store.TaskRun`；`reconcileWorkerSessions()` 直接访问 worker 内部方法；`checkExpiredDispatchLeases()` 大量直接 DB 事务。同一函数中一半走 service 一半直接 DB，风格不一致。应将恢复/对账逻辑下沉到 `pm.RecoverDispatchJobs`/`task.RecoverStuckRuns` 等 service 方法。 |
| APP-H4 | `internal/app/gateway_facade.go` | **类型别名暴露 channelsvc 全部内部类型**。12 个类型别名（Gateway, ChannelService, GatewayOptions 等）。更严重的是 `ChannelService()` 方法(project.go:112-117)直接返回 `*channelsvc.Service`，彻底破坏封装。 |
| APP-H5 | `internal/app/action_executor.go` 行109 | **executeTicketDetail 直接访问 `e.project.core.DB`**。`e.project.core.DB.WithContext(ctx).First(&tk, ticketID)` 绕过 ticket service。同文件其他方法（如 `executeListTickets`）正确使用 `e.project.ListTickets()`。显示该处是遗漏。 |
| APP-H6 | `internal/app/home.go` 行467,470 + `project_subagent.go` 行462 | **硬编码模型名 "gpt-5.3-codex" 出现 3 处**。`applyAgentProviderModel` 和 `resolveSubagentAgentSettings` 都包含硬编码默认模型。应提取为常量或放到 agent/provider 配置中。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| APP-M1 | `internal/app/home.go` 行275-428 | **openProject/initProjectFiles 存在大量重复逻辑**。两个函数各自独立构造 `core.Project`，字段赋值基本相同，差异仅在于 init 额外写入 agent entry point 和 config.json。应抽取 `buildCoreProject()` 工具函数。 |
| APP-M2 | 遍布 23 个文件 | **strings.TrimSpace 745 处系统性过度防御**。分布：daemon_public_feishu(171)、note(86)、project_subagent(65)、home_config(58)、project(67)、daemon_client(41)、daemon_runtime(43)。典型：已从 DB 读出的数据再 TrimSpace、已 TrimSpace 过的值传递链中再 TrimSpace、整数字段字符串表示也 TrimSpace。 |
| APP-M3 | `project.go` 行149 + `home_config.go` 行266-270 | **provider 白名单硬编码在 app 层**。`provider != "codex" && provider != "claude"` 和 `case "", "codex", "claude"` 散落多处，与 `agent/provider` 包的实际注册机制脱节。应由 `agent/provider` 包统一提供白名单。 |
| APP-M4 | 7 个 daemon_* 文件共 ~4,950 行 | **daemon 组件散落在 app 层**（daemon.go 121行 + client 438行 + runtime 260行 + gateway_runtime 147行 + manager_component 879行 + notebook_component 228行 + public_component 321行 + tunnel 467行 + feishu 2087行）。占 app 包 ~35%。`services/daemon/` 已存在但 app 层的 daemon 代码量远超 service 层。应将具体实现迁移到 `services/daemon/`。 |
| APP-M5 | `gateway_facade.go` 行83-92 + `daemon_public_component.go` 行297-306 | **closeDaemonGatewayDB 重复定义**。`closeGatewayDB` 和 `closeDaemonGatewayDB` 实现完全相同（关闭 gorm.DB 连接），只是名称不同。应合并为一个共享函数。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| APP-L1 | 几乎每个 Project 方法 | **nil guard 模板代码高度重复**。`if p == nil || p.xxx == nil { return ..., fmt.Errorf("...") }` 在 60+ 个方法中重复，但 Project 由 `assembleProject` 保证所有 service 字段非 nil。可在 `assembleProject` 中 assert 非 nil，正常路径去掉冗余 guard。 |
| APP-L2 | `api_types.go` vs `facade_types.go` | **两种类型定义风格并存**。`api_types.go` 定义独立类型（正确），`facade_types.go` 使用类型别名（错误），同模块内增加认知负担。 |

---

### 模块 3: internal/services/channel（Channel 服务 + agentcli）

**概况**：channel 包 19 个文件约 6,200 行 + agentcli 子包 4 个文件约 640 行，总计 ~6,840 行。strings.TrimSpace 566 处。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| CH-H1 | `service.go` 行380-563 + `gateway_runtime.go` 行490-839 | **Service 与 Gateway 双路径持久化逻辑大面积重复且已开始分裂**。两套几乎相同的 "ensure binding → ensure conversation → create inbound → create turn job" 流程。两套 `ensureBinding`/`ensureConversation` 核心逻辑一致仅参数来源不同。Gateway 版本已多出 `project` 字段和 `peer_project_key` 处理而 Service 版本没有——分裂已经发生。应提取统一的 `inboundPersistence` 组件。 |
| CH-H2 | `service.go` 行565-882 | **runTurnJob 方法膨胀到 318 行承担过多职责**。串联：Claim job + lease → DB 加载 entities → 初始化 event collector/logger → turn context(超时/cancel) → 调用 agent(SDK/CLI) → 解析 TurnResponse + 执行 actions → pending actions → 写 outbound + outbox → 发送 outbox → 状态流转。应拆分为 `claimAndLoadTurnContext`/`executeTurnAgent`/`processTurnResponse`/`persistTurnResult` 四个子方法。 |
| CH-H3 | `pending_actions.go` (679行) | **混合 CRUD/审批决策/action执行三种职责**。约 1/3 是 view 转换和辅助函数。应拆分为 `pending_action_store.go`(CRUD) + `pending_action_workflow.go`(审批) + 将 `executeAction`/`renderActionExecutionSummary` 移入 action_executor.go。 |
| CH-H4 | 24 个文件共 566 处 | **strings.TrimSpace 过度防御**。包括内部方法间传递已 trimmed 的值再 trim、DB 读取后已 clean 的值、枚举值比较(`strings.TrimSpace(string(status))`)、方法内多次对同一变量重复 trim。`event_bus.go` 的 Publish 一次性 trim GatewayEvent 12个字段（构造时已 trim）；`gateway_runtime.go` 的 `publishFromResult` 中 40+ 处。应定义"数据清洗边界"，内部信任已清洗数据，估计可减少 400+ 处无效调用。 |
| CH-H5 | `action_executor.go` | **localActionExecutor 直接操作 store 且留在 channel 包内**。直接 import store 用 `e.project.DB` 做 GORM 查询(行56-157)。实现 list_tickets/ticket_detail/create_ticket 三个 action。当 app 层 ActionHandler 注入后这是死代码，但仍建立了 channel→store 耦合。应删除或标记为 test-only。还有 100+ 行参数解析辅助函数(actionArgString 等)应提取到独立文件。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| CH-M1 | `service.go` 行87-104 + `gateway_runtime.go` 行99-119 | **turnJobResult 和 gatewayTurnResult 近乎相同的 struct**。仅差 3 个字段（Gateway 多 BindingID/JobStatus/JobError/JobErrorType），其余 12 字段完全一致。应统一为一个 `TurnResultPayload`。 |
| CH-M2 | `chat_runner.go` 行118-299 | **stateful runner 池使用字符串 key 匹配缺乏类型安全**。`buildStatefulRunnerKey`(`provider|conversationID`) 和 `buildStatefulRunnerSignature`(多字段拼接 `provider=xxx|model=xxx|...`) 脆弱，字段新增/格式变化可能导致错误复用。应使用 struct key 或 hash。 |
| CH-M3 | 28 处 `context.Background()` | **context.Background() 在非顶层位置大量使用**。`failTurn` 中用 `context.Background()`(原始 ctx 可能已 cancelled)意味着 failure 路径脱离上层取消控制。每个方法开头 `if ctx == nil { ctx = context.Background() }` 应统一到入口 guard。应使用 `context.WithoutCancel(ctx)`(Go 1.21+)。 |
| CH-M4 | 4 个文件共 24 处 | **log.Printf 直接使用标准 log 包**。主要在 tool_approval.go/pending_actions.go/service.go/gateway_runtime.go。无级别控制，不可结构化查询，测试时无法静音。应引入统一 structured logger。 |
| CH-M5 | `gateway_runtime.go` 行446-480 | **streamedAny 标志位闭包捕获并发语义不明确**。通过闭包在回调中被写入、在主 goroutine 中读取。当前同步调用不存在 race，但语义依赖 ProcessInbound 实现细节。应改为 `atomic.Bool`。 |
| CH-M6 | `chat_runner_claude.go` 行37-39, 112-118 | **toolApprovalFn 生命周期与 turn 绑定但通过 runner 级字段传递**。per-turn 的回调存储为 struct 字段，在 RunTurn 开始时 set 结束时 clear。`runMu` 保证正确性但模式不直观。应通过 context 传递。 |
| CH-M7 | `agentcli/runner.go` 行13-69 | **agentcli CLI runner 缺乏超时保护**。使用 `exec.CommandContext` 但无额外超时保护。上层通常设置 turn timeout，但 agentcli 作为独立包应自保。建议增加可配置的 max execution time 默认 5 分钟。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| CH-L1 | `service.go` 行1328-1334 + `chat_runner_claude.go` 行404-413 + `agentcli/runner.go` 行158-167 | **三处独立的随机 ID 生成**（randomHex/randomSessionToken/randomUUIDv4），接口和格式各不相同。应统一到一个 idgen 工具。 |
| CH-L2 | `service.go` 行50-55 | **Service.SetActionHandler 非并发安全**。直接写 `s.actionHandler` 无 mutex 保护。如果在 Service 运行中调用与 executeAction 中的读取存在 race。应文档化"必须在运行前设置"或加 mutex。 |
| CH-L3 | `events.go` 行203-235 | **copyAgentEvents 静默丢弃 RunID 或 Stream 为空的事件无日志**。可能隐藏合法事件被意外过滤的 bug。 |
| CH-L4 | `service.go` 行1082-1148 | **dispatchOutbox 方法名有误导性**。在一个事务中先标记 Sending 再立即标记 Sent，中间无实际投递操作。实际投递由 Gateway 层完成。应重命名为 `markOutboxLocalCompleted`。 |

---

### 模块 4: internal/services/pm（PM 服务）

**概况**：25 个生产代码文件 ~4,752 行；14 个测试文件 ~3,011 行。strings.TrimSpace 257 处。

#### CRITICAL

| ID | 位置 | 描述 |
|----|------|------|
| PM-C1 | `dispatch_agent_exec.go` 行10-11,63-151 + `dispatch_worker_sdk.go` 行9-10,35-116 | **pm 直接 import agent/provider + agent/run 跨越架构层级**。两个文件各自手动构造 `provider.AgentConfig → provider.NewFromConfig → run.NewSDKExecutor/run.NewProcessExecutor`。pm 成为 agent 层深度耦合消费者，需理解 SDK/Process 两种模式、组装 20+ 字段的 config。应抽取 `AgentLauncher` 接口由 app 层实现注入。 |
| PM-C2 | `bootstrap.go` 行39-51 + `dispatch_agent_exec.go` 行85-101 + `dispatch_worker_sdk.go` 行75-88 | **环境变量 map 三处重复构造且已出现不一致**。10+ 个 DALEK_* 环境变量在三处各自手写 `map[string]string{...}`。已有微妙差异：dispatch_agent_exec 多出 DALEK_DISPATCH_REQUEST_ID/ENTRY_PROMPT/PROMPT_TEMPLATE，dispatch_worker_sdk 多出 DALEK_DISPATCH_DEPTH，bootstrap 缺少 DALEK_DISPATCH_DEPTH。应抽取 `buildBaseEnv()` 公共方法。 |

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| PM-H1 | 16+ 处分散 | **硬编码时间常量散布且含语义重复**。workerReadyTimeout `8s` 在 service.go:29 和 worker_ready_wait.go:14 各定义一次；workerReadyPollInterval `200ms` 同样两处；pollInterval `100ms` 在 dispatch.go:120 和 dispatch_queue.go:694 各一次；tmux timeout `2s` 在 session.go:47 和 direct_dispatch.go:103 各一次；lease TTL `2*time.Minute` 在三处重复。修改时必须同时改多处否则行为不一致。应新建 `constants.go` 集中定义。 |
| PM-H2 | `dispatch_agent_exec.go` 行200-249 | **内嵌巨大 prompt 模板**（250 行文件中 40 行纯文本 XML prompt）。包含角色定义、规则列表、输出契约。prompt 演进需修改 Go 代码重编译，非技术人员无法审查。应抽取为 `embed.FS` 外部文件或独立的 `dispatch_prompt.go`。 |
| PM-H3 | `manager_tick.go` (595行) | **ManagerTick 是 410 行控制流巨函数**（行77-488）。串行完成：事件消费 → running worker 扫描 → autopilot 检查 → merge queue → capacity 调度 → 状态落盘。多层嵌套循环和复杂条件分支。应拆分为 `consumeTaskEvents`/`scanRunningWorkers`/`proposeMergeItems`/`scheduleQueuedTickets` 子方法。 |
| PM-H4 | `workflow_notify.go` 行12 | **引入 gatewaysend 服务的横向依赖**。`GatewayStatusNotifier` 直接 import 并使用 `gatewaysendsvc.MessageSender`/`gatewaysendsvc.SendProjectText`。pm 已定义 `WorkflowStatusChangeHook` 接口，但实现放在 pm 包内削弱了抽象。应将 `GatewayStatusNotifier` 移到 app 层或独立 notifier 包。 |
| PM-H5 | `dispatch_queue.go` (720行) | **dispatch_queue 膨胀职责过重**。包含 ID 生成、job enqueue、job claim、lease renew、job success/fail completion、ticket status promote/demote、force fail、dispatch job query、wait polling。混合了队列操作/状态机转换/ticket workflow 推进三种不同层次。应将 promote/demote 提取到 workflow.go。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| PM-M1 | 几乎所有文件 | **strings.TrimSpace 过度使用(257处)**。大量对已在上游清洗的字符串做冗余 trim，如 `p.Key`/`p.RepoRoot`/`w.Branch` 等从 DB 读出的值。应在入口边界一次 normalize，内部信任。 |
| PM-M2 | 几乎每个接收 ctx 的方法(51处) | **每个 public method 开头 `ctx == nil` 检查**。`if ctx == nil { ctx = context.Background() }` 普遍出现。Go 惯用法是调用方负责传非 nil context。应在唯一入口做一次检查或 doc 声明 ctx 不可为 nil。 |
| PM-M3 | `dispatch.go` 行187-242 + `direct_dispatch.go` 行47-129 | **resolveDispatchTarget 逻辑高度重复**。两个 dispatch 入口各自实现"查 ticket → 检查状态 → 查 worker → autoStart → waitReady"。direct_dispatch 更复杂（多了 stopped worker tmux 存活检测）但核心流程高度相似。应抽取 `resolveAndReadyWorker()` 公共方法。 |
| PM-M4 | `workflow_notify.go` 行37-38 | **GatewayStatusNotifier 持有两个 DB 连接**(projectDB + gatewayDB)。notifier 需同时知道两个数据库。应通过"发送通知"抽象接口隔离。 |
| PM-M5 | `dispatch_runner.go` 行52-65 | **lease renew goroutine 错误被静默吞掉**。`_ = s.renewPMDispatchJobLease(...)` 错误完全忽略。且使用 `context.Background()` 而非 parent context，任务取消后续租仍可能继续。lease 续租失败意味着 job 可能被抢占，不应静默。应至少 log 失败并在连续失败 N 次后主动取消。 |
| PM-M6 | `context_cancel.go` 行11-30 | **newCancelOnlyContext 存在 goroutine 泄露风险**。parent context DeadlineExceeded（非 Canceled）退出时不取消 child，启动的 goroutine 会等到 child 被手动 cancel 才退出。增加 goroutine 生命周期管理心智负担。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| PM-L1 | `service.go` 行70-87 | **require() 方法每次调用检查 5 个前置条件**（p/p.DB/p.Tmux/s.worker/p.TaskRuntime），都是构造时确定的依赖。应在 `New()` 中验证，运行时简化为 nil receiver 检查。 |
| PM-L2 | 6 个文件共 817 行 | **缺失测试的关键文件**。session.go(186行)、inbox.go(188行)、inbox_upsert.go(110行)、bootstrap.go(58行)、worker_loop.go(137行)、worker_ready_wait.go(138行) 均无单元测试。其中 worker_loop 和 worker_ready_wait 是 dispatch 核心路径。 |
| PM-L3 | `inbox.go` 行152-188 | **ensureInboxUniqueOpenKey 注释承认不是强一致**。"不是强一致约束（不引入唯一索引）"。单进程 manager tick 下可接受，多进程可能产生重复 inbox。 |

---

### 模块 5: internal/services/worker（Worker 服务）

**概况**：12 个源文件 + 4 个测试文件，约 1,100 行业务代码。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| WK-H1 | `views.go` 行107-338 | **ListTicketViews 是 230 行 God Method**。一个方法聚合：查 tickets、查 workers、探测所有 tmux sockets session 存活性、查 task runtime 状态、查 dispatch jobs、计算 capability、计算 derived runtime health。测试需同时 mock 所有依赖。capability 已提取为纯函数（正确方向），但 derived health 计算还嵌在主函数体内。应拆分为 data fetching 层 + computation 层。 |
| WK-H2 | `views.go` 行13-105 | **TicketView 和 computeTicketCapability 不应在 worker 包内**。`TicketView` struct 和 `computeTicketCapability` 函数定义在 worker 包，但语义是"ticket 的完整视图"和"ticket 可执行的操作能力"——属于 ticket 领域。因计算需要 worker 状态数据就放在 worker 包，导致 ticket 行为语义分散到两个包。应提升到 app 层或独立 query service。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| WK-M1 | `start.go` 行237 | **使用 goto 语句实现 worktree 分支冲突恢复**。`goto addWorktree` 在 200+ 行函数中引入非线性跳转。应提取 `ensureWorktree(repoRoot, path, branch, baseBranch)` 子函数封装 prune-retry 逻辑。 |
| WK-M2 | `start.go` 行118-419 | **StartTicketResourcesWithOptions 方法过长(300+行)多关注点混合**。参数校验、已有 worker 复用检查、worktree 创建/复用、分支冲突检测与恢复、DB 记录创建/更新、tmux session 清理与创建、环境变量注入、日志管道设置、事务与回滚。defer 回滚依赖多个 flag 变量。应分解为子步骤。 |
| WK-M3 | `task_runtime.go` 全文件 | **两套获取 TaskRuntime 的路径 "with runtime" 模式重复**。每个业务方法有两个版本：`xxx()` 和 `xxxWithRuntime()`，前者通过 `s.taskRuntime()` 获取，后者接受外部传入(事务内 tx-scoped)。6 对重复方法签名。应统一路径或让 `taskRuntime()` 接受可选 `*gorm.DB`。 |
| WK-M4 | 全包范围 | **strings.TrimSpace 防御性调用过度密集**。如 start.go:174 `if strings.TrimSpace(branch) == ""` 中 branch 刚从 `strings.TrimSpace(w.Branch)` 赋值。模糊"哪些是真正边界校验"，降低可读性。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| WK-L1 | `queries.go` 行141-143 | **nowLocal() 是对 time.Now() 的无意义包装**。如果意图是测试时替换时间源应使用 clock 接口。 |
| WK-L2 | `cleanup.go` 行108-117 | **查询 dispatch job 时直接引用 store.PMDispatchJob 泄露 PM 领域概念**。worker 不应关心 PM dispatch 数据模型。应通过 core 接口提供 `HasActiveDispatch(ticketID) bool`。 |

---

### 模块 6: internal/services/ticket（Ticket 服务）

**概况**：1 个源文件(127行) + 1 个测试文件。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| TK-H1 | `service.go` 全文件 | **ticket service 是空壳——只有 CRUD 缺失生命周期管理**。只提供 Create/List/BumpPriority/UpdateText。核心操作全不在 ticket 包：start/stop 在 worker；dispatch 在 pm；archive 在 app；workflow 状态枚举和转换规则分散在 store/worker/views.go/app 等。"ticket 的工作流规则在哪里"没有单一权威答案。应增加 `TransitionWorkflow`/`CanTransition`/`GetByID`/`HasActiveDispatch`。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| TK-M1 | `service.go` 行29-56 | **Create 方法永远失败**。`Create(ctx, title)` 内部调用 `CreateWithDescription(ctx, title, "")`，而后者在 `description == ""` 时返回错误。提供了一个永远失败的公开方法。应删除 `Create` 或让它使用默认 description。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| TK-L1 | `service.go` | **缺少 ByID 查询方法**。其他服务（worker.start.go:155）获取 ticket 时都是直接 `db.First(&t, ticketID)` 绕过 service 层。应补充 `GetByID` 作为单一入口。 |

---

### 模块 7: internal/services/task（Task 服务）

**概况**：7 个源文件 + 2 个测试文件，约 750 行业务代码。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| TS-H1 | `core/task_runtime.go` 行35-107 + `task/runtime_adapter.go` 全文件(150行) + `task/service_runs.go` 行15-40 + `task/service_events.go` 多处 | **core.TaskRuntime 接口和 task.Service 之间完整 Input 类型镜像**。5 对几乎完全相同的 Input struct：CreateRunInput/EventInput/RuntimeSampleInput/SemanticReportInput/ListStatusOptions 各在 core 和 task 中定义一份。runtime_adapter.go 150 行中 ~130 行是逐字段复制。完全符合 ARCH_GOVERNANCE.md 案例 C。每次扩展需改三处。 |
| TS-H2 | `core/task_runtime.go` 行109-120 + `task/service_helpers.go` 行10-21 | **NextActionToSemanticPhase 在 core 和 task 包中重复定义**。两处完全相同的函数实现。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| TS-M1 | `service_runs.go` 各 MarkRun* 方法 | **task 状态转换没有显式状态机依赖 WHERE 条件实现幂等**。合法转换规则散布在 SQL WHERE 中，无集中的状态转换表。新增状态时容易遗漏某个方法的 WHERE 条件。应提取 `canTransition(from, to)` 纯函数。 |
| TS-M2 | `service_subagent.go` (170行) | **Subagent 功能职责放置存疑**（监控点）。当前放在 task 包基本合理（subagent run 是 task run 子资源），但随功能复杂化应考虑拆为独立包。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| TS-L1 | `service_runs.go` 行349-358 + `service_subagent.go` 行154-169 | **唯一约束冲突检测依赖错误消息字符串匹配**。通过检查 "unique constraint failed" 字符串判断。GORM `errors.Is(err, gorm.ErrDuplicatedKey)` 是第一优先级检查，字符串匹配是 fallback。依赖 SQLite driver 具体错误格式。 |

---

### 模块 8: 跨 worker/ticket/task 关系

| ID | 级别 | 描述 |
|----|------|------|
| XWT-R1 | **CRITICAL** | **worker 完全绕过 ticket service 直接操作 ticket DB**。start.go:155 `db.First(&t, ticketID)`；cleanup.go:71 `p.DB.First(&t, ticketID)`；views.go:125-131 直接查 ticket 列表。worker 没有任何对 ticket service 的依赖。ticket service 被架空。 |
| XWT-R2 | **HIGH** | **ticket 职责在 worker/app/pm 之间碎片化**。Create/List/BumpPriority/UpdateText 在 ticket；StopTicket/AttachCmd/ListTicketViews/CleanupWorktreeWorktree/computeTicketCapability 在 worker；Archive/workflow 状态转换在 app；Dispatch 在 pm。没有单一位置回答"ticket 支持哪些操作"。 |
| XWT-R3 | MEDIUM | **task 和 worker 之间的适配层过重**。150 行 adapter + 两套镜像 Input 类型。单体应用内两个包之间这个间接层性价比存疑。 |
| XWT-R4 | MEDIUM | **worker 和 ticket 之间缺少清晰的"谁拥有什么"协议**。worker "代替" ticket 做了很多事（查询/视图/capability），但没有显式协议说明边界。应建立领域归属规则。 |

---

### 模块 9: internal/services/daemon（Daemon 服务）

**概况**：6 个生产代码文件 ~2,642 行；测试代码 ~1,966 行。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| DM-H1 | `execution_host.go` (1337行) | **膨胀至 1337 行职责边界模糊**。5 种不同职责混合：任务提交与幂等性管理、任务执行编排、运行状态查询与 project 索引、取消逻辑、handle 生命周期管理。`h.mu` 保护语义完全不同的数据路径。应至少拆分为 3 个文件。 |
| DM-H2 | `execution_host.go` 行46-231 | **DTO 类型爆炸——18 个 public struct 散布**。DispatchSubmitOptions/Request/Receipt、WorkerRunOptions/Result/Request/Receipt、SubagentSubmitOptions/Request/Receipt、NoteSubmitRequest/Receipt、RunStatus/RunEvent/CancelResult 等。约 180 行纯类型定义与复杂并发逻辑混在一起。许多 request/receipt 字段高度重复。应移到 `execution_host_types.go`。 |
| DM-H3 | 全部文件 168 处 | **strings.TrimSpace 过度防御**。`handle.project` 构造时已 TrimSpace，后续不同方法中重复调用 5-10 次。api_internal.go 的 receipt 字段构造时已 trim 但 writeJSON 前又全部 trim。168 处噪音淹没业务逻辑。应入口校验一次存储后信任。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| DM-M1 | `api_internal.go` (579行) | **混合路由/handler/工具函数三层职责**。HTTP 服务生命周期、路由注册、7 个业务 handler、认证中间件、JSON 工具函数、地址校验、数据库关闭、URL 路由解析。应抽取 `api_internal_helpers.go`。 |
| DM-M2 | `api_internal.go` 行39-52 | **InternalAPI 同时拥有 Gateway 和 GatewaySend 两条不相关路径**。持有 gateway(WS通道) + sendHandler(REST接口) + sendDB，两条路径职责/数据源完全不同但共享生命周期。Stop() 需同时关心 HTTP shutdown 和 gateway DB close。应将 sendHandler 提取为独立 Component。 |
| DM-M3 | `api_internal_ws.go` 行133-146 | **TrimSpace 对事件流 Text 字段的语义破坏**。对 `ev.Text` 做 TrimSpace 可能破坏消息内容语义（如 markdown 格式以空行开头）。这不是"防御性处理"而是"数据篡改"。应对 Text 字段不做 TrimSpace，只对 ID/状态等元数据 trim。 |
| DM-M4 | `execution_host.go` 行848-866 | **probeWorkerRunID 使用忙等轮询**。2 秒内以 80ms 间隔轮询数据库（最多 25 次）探测 worker run ID。`DirectDispatchWorker` 接口设计缺少返回值（应直接返回 run ID），上层被迫轮询弥补。应修改接口使 worker run 创建后直接返回 run ID。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| DM-L1 | `daemon.go` 行15-19 | **Component 接口 Start 方法不传生命周期 context**。ExecutionHost.Start 是空实现，真正"启动"在 SubmitXxx 中。接口设计与实际使用模式不完全对齐。 |
| DM-L2 | `api_internal.go` 行17-18 | **daemon 包直接依赖 channel 和 gatewaysend 两个业务 service**。daemon 作为基础设施层直接 import 业务 service。鉴于 daemon 天然是"组装点"可接受，但中长期可考虑接口抽象。 |

---

### 模块 10: internal/services/core（Core 领域模型）

**概况**：2 个文件(project.go 33行 + task_runtime.go 121行)，共 154 行。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| CR-H1 | `project.go` 行12-32 | **Project 作为 God Object 承载过多角色**。12 个字段覆盖 4 个完全不同的关注域：身份信息(Name/Key)、文件系统布局(RepoRoot/Layout/ProjectDir/ConfigPath/DBPath/WorktreesDir/WorkersDir)、配置与持久化(Config/DB)、运行时基础设施(Tmux/Git/TaskRuntime)。22 个文件引用它，每个 service 只使用 2-4 个字段但被迫接收整个 Project。测试必须构造完整 Project。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| CR-M1 | `project.go` | **所有字段 public 无构造校验**。无构造函数，任何包可直接 `&core.Project{Name: "x"}` 构造不完整实例。缺少构造时校验是 God Object 常见后果。应增加 `Validate()` 方法。 |
| CR-M2 | `task_runtime.go` 行17-33 | **TaskRuntime 接口定义 13 个方法接口过大**。违反接口隔离原则。不同消费者只用子集：执行器用 MarkRunRunning/RenewLease/MarkRunSucceeded 等；查询方用 FindRunByID/ListStatus；PM 用 CreateRun/CancelActiveWorkerRuns。mock 必须实现全部 13 方法。应按角色拆分为 TaskRunReader/TaskRunWriter/TaskRunCreator。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| CR-L1 | `task_runtime.go` 行13-15 | **TaskRuntimeFactory 的 ForDB 方法暗示 DB 切换场景**。实际每个 Project 只有一个 DB，间接层增加复杂度。如确认单 DB 可简化为直接持有实例而非 factory。 |

---

### 模块 11: internal/services/gatewaysend（Gateway Send 服务）

**概况**：1 个文件(565行) + 1 个测试文件(390行)。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| GS-H1 | `send.go` (565行) | **单文件混合 5 层职责**。HTTP handler 层(行85-143)、业务逻辑层(行145-252)、去重逻辑(行254-311)、持久化状态机(行313-472：createPending/markSending/markSent/markFailed)、工具函数(行474-564)。应拆分为 send_outbox.go(~170行) + send_helpers.go(~90行)。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| GS-M1 | `send.go` 行145-200 | **SendProjectText 直接操作 gorm.DB 缺少 repository 抽象**。函数接收裸 `*gorm.DB` 直接 GORM 查询。测试必须用真实数据库，无法 mock 数据层。应定义 `BindingFinder` 接口封装查询。 |
| GS-M2 | `send.go` 行166 | **硬编码飞书适配器扩展性受限**。`WHERE adapter = ? AND ...` 硬编码只查飞书 binding。未来支持 Slack/Discord 需修改核心查询。应将 adapter 类型参数化。 |
| GS-M3 | `send.go` 行85-143 | **NewHandler 闭包捕获与参数传递两种注入风格并存**。handler 闭包捕获 `opt.DB`/`opt.Resolver`/`sender`，但核心调用 `SendProjectText(... opt.DB, opt.Resolver, sender ...)` 又逐个参数传递。应统一为 Service struct 方法。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| GS-L1 | `send.go` 行29 | **去重窗口硬编码 30 秒**。`sendDedupWindow = 30 * time.Second` 是 magic number。应提取为配置参数。 |
| GS-L2 | `send.go` 行214 | **log.Printf 使用全局 logger**。与 daemon 中使用注入 logger 的模式不一致。应在 HandlerOptions 中增加可选 Logger。 |

---

### 模块 12: internal/services/logs（日志预览服务）

**概况**：1 个文件(101行)，无测试。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| LG-H1 | 整个包 | **包名与职责不匹配**。`logs` 暗示通用日志管理，实际只做 tmux capture-pane 抓取 worker session 屏幕输出尾部。代码注释承认"当前实现以 tmux capture-pane 作为最小可用的 tail 预览源"。当"真正的日志服务"需创建时会命名冲突。应重命名为 `preview` 或 `tmuxpreview`。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| LG-M1 | `service.go` 行21-27 | **直接依赖 worker.Service 具体类型而非接口**。只调用 `s.worker.LatestWorker(ctx, ticketID)` 一个方法，完全可用接口替代。应定义 `workerLookup` 本地接口。 |
| LG-M2 | `service.go` 全文件 | **101 行只有一个 public 方法包粒度可能过细**。如果未来不扩展更多日志功能，可以是 worker 包或 app 包中的一个方法。保留观察。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| LG-L1 | `service.go` 行58-64 | **tmux socket 三级回退链条分散在方法体中**。worker socket → config socket → 硬编码 "dalek"。应提取 `resolveTmuxSocket()` 辅助函数。 |
| LG-L2 | 整个包 | **没有测试覆盖**。至少 `require()` 方法和错误路径可以单元测试。 |

---

### 模块 13: internal/agent/auditlog

**概况**：1 个文件(109行)，无测试。

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| AL-L1 | 整个包 | **缺少测试覆盖**。`Append()` 和 `resolveAuditPath()` 路径解析逻辑（环境变量优先级、默认路径 fallback）较复杂，没有测试保证正确性。 |
| AL-L2 | `auditlog.go` 行99-107 | **SortedEnvKeys 手写插入排序**。标准库 `sort.Strings()` 或 `slices.Sort()` 不会引入额外依赖。 |
| AL-L3 | `auditlog.go` 行31,53,57,59,68,72 等 | **strings.TrimSpace 过度使用**。对 map key、工作目录路径、环境变量等反复 TrimSpace。 |

---

### 模块 14: internal/agent/eventlog

**概况**：2 个文件(180行) + 1 个测试文件(270行)。

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| EL-M1 | `eventlog.go` 行152-167 | **ResolveProjectName 职责不属于 eventlog**。从工作目录推断项目名是通用功能，与"事件日志"无关。被 sdkrunner 调用导致 sdkrunner 必须 import eventlog 获取无关功能。应移到 `agent/common/` 或使用方附近。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| EL-L1 | `eventlog.go` 行58-88 | **WriteHeader 手动构造 map 而非利用 struct tag**。RunMeta 的 json tag 只是文档作用，真正序列化在 map 构造中。新增字段若忘记加到 map 会静默丢失。应直接 `json.Marshal(meta)`。 |
| EL-L2 | `eventlog.go` 行169-180 vs `auditlog.go` 行52-82 | **resolveLogDir 与 auditlog.resolveAuditPath 路径解析逻辑重复**。两处都做 DALEK_HOME→~/.dalek 的 fallback。 |

---

### 模块 15: internal/agent/eventrender

**概况**：4 个文件(404行) + 1 个测试文件(400行)。**设计最干净的子包之一。**

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| ER-L1 | `claude.go` 行97-100 | **Claude text-only assistant 事件的隐式约定**。只包含 text block 时返回 nil，注释说"由 AppendAssistantText 单独处理"。renderer 行为依赖上层隐式约定。应在 Renderer 接口文档中明确说明。 |
| ER-L2 | `claude.go` 行79 | **只渲染第一个 content block**。`renderAssistant()` 只解析 `msg.Content[0]`，多 block assistant message（如 thinking + tool_use）后续 block 被静默丢弃。应遍历所有 content blocks。 |

---

### 模块 16: internal/agent/provider

**概况**：6 个文件(295行) + 1 个测试文件(43行)。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| PV-H1 | `provider.go` 行4-8 | **Provider 接口职责分裂**。三个方法 Name()/BuildCommand()/ParseOutput() 只服务于 CLI 进程执行模式。SDK 模式(run/sdk.go + sdkrunner)完全不使用 Provider 接口。但接口名和位置暗示它是通用抽象。应明确为"CLI 命令构建器"角色或拆分为 CLIBuilder + OutputParser。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| PV-M1 | 整个包 | **配置(AgentConfig)和执行(Provider)混在一个包**。AgentConfig 被多处使用（app/pm/project配置），Provider 是执行逻辑。任何想使用 AgentConfig 的包间接依赖 Provider 全部执行逻辑。如 AgentConfig 使用范围远大于 Provider 可移到更通用位置。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| PV-L1 | `parse.go` 行75-99 vs `sdkrunner/runner.go` 行763-798 | **collectText 与 collectAnyText 功能高度重复**。两处都实现"从 JSON any 结构递归提取文本"，字段优先级略有不同。 |

---

### 模块 17: internal/agent/run

**概况**：10 个文件(~1420行不含测试) + 2 个测试文件(~302行)。

#### CRITICAL

| ID | 位置 | 描述 |
|----|------|------|
| RN-C1 | `process.go` 行14-15 + `sdk.go` 行14-15 + `tmux.go` 行13-14 | **run 包反向依赖 services/core 和 store 破坏 agent 层独立性**。ProcessConfig 包含 `store.TaskOwnerType`；三个 executor 直接使用 `store.TaskPending/Running/Succeeded/Failed/Canceled` 状态常量；通过 `core.TaskRuntime` 接口管理任务生命周期。agent 层不应了解 services 层概念。应定义 `RunLifecycleHook` 回调接口替代。 |

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| RN-H1 | `process.go` 行66-133 + `sdk.go` 行74-128 + `tmux.go` 行116-154 | **三个 executor 300+ 行任务状态管理代码重复**。三处几乎一字不差地执行：CreateRun → MarkRunRunning → AppendEvent → MarkRunSucceeded/Failed/Canceled。应提取 `runLifecycleManager` 封装所有 TaskRuntime 操作。 |
| RN-H2 | `sdk.go` 行18-48 | **SDKConfig 24 字段膨胀**。承担"SDK 执行配置"+"tmux 实时回显配置"+"任务运行时跟踪配置"三种职责。很多 tmux playback 字段与 SDK 执行无关。应拆分为 SDKExecConfig + PlaybackConfig + RuntimeTrackingConfig。 |
| RN-H3 | `tmux.go` 行272-293 | **TmuxHandle.Wait() 使用 500ms 轮询无 context cancellation**。忙等待无 `case <-ctx.Done()`，无超时保护。任务永不终止则 goroutine 永远轮询。应添加 context 参数并监听 cancellation。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| RN-M1 | `process.go` 行159-164, 173-186 | **processHandle 的 sync.Once + doneCh 模式不直观**。doneCh 在 once.Do 内部初始化，并发调用 Wait() 时虽然功能正确但初始化模式增加审查心智负担。应在构造时初始化 doneCh。 |
| RN-M2 | `sdk_tmux_playback.go` 行56-80 vs `tmux.go` 行84-106 | **pane 选择和验证逻辑重复**。两处都调用 `infra.PickObservationTarget()` + 检查 InputOff/InMode/CurrentCommand。 |

---

### 模块 18: internal/agent/sdkrunner

**概况**：2 个文件(798+22行)。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| SR-H1 | `runner.go` (798行) | **798 行单文件巨模块**。包含接口定义、DefaultTaskRunner、Claude 权限设置 JSON(200行)、Run() 函数、runCodex/runClaude、Claude SDK 消息转换、Codex 事件提取、reasoning effort 映射、环境变量处理、项目路径解析等。应至少拆分为 runner.go + codex.go + claude.go + settings.go + env.go。 |
| SR-H2 | `runner.go` 行67-206 | **200 行 Claude 权限设置 JSON 字符串常量硬编码在源码**。修改任何权限规则需修改 Go 源码重编译。不同项目可能需要不同权限配置。应使用 `go:embed` 嵌入 .json 文件或提供配置覆盖机制。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| SR-M1 | `runner.go` 行16 | **sdkrunner 直接 import repo 包做路径发现**。`RepoRootFromWorkDir()` 调用 `repo.FindRepoRoot()`。配置准备逻辑而非执行逻辑。应由调用方预先计算并通过 Request 传入。 |
| SR-M2 | `runner.go` 行325-398 vs 行400-464 | **runClaude 和 runCodex 流式事件处理模式不一致**。Codex 用 `for ev := range streamed.Events`，Claude 用 `for msg := range msgs` + 单独 `<-errs`。由外部 SDK API 差异决定，但适配层未统一。 |
| SR-M3 | `runner.go` 行635-717 | **GlobalDalekDir/RepoRootFromWorkDir/SDKAdditionalDirectories 不属于 sdkrunner 职责**。通用路径/配置发现逻辑放在 SDK runner 中增加概念负载。应移到 agent/common 或 internal/config。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| SR-L1 | `runner_test.go` (22行) | **测试覆盖极度不足**。只有一个测试验证 JSON 合法性。Run()/runCodex()/runClaude() 完全没有测试。 |
| SR-L2 | `runner.go` 行269 | **使用标准库 log.Printf**。eventlog.Open 失败时使用全局 logger，可能与项目日志方案不一致。 |

---

### 模块 19: 跨 agent 层整体

| ID | 级别 | 描述 |
|----|------|------|
| AGT-C2 | **CRITICAL** | **channel 和 app 绕过 run 编排层直接使用 sdkrunner，agent 层无统一入口**。pm 用 provider+run（CLI/tmux 模式）；channel 用 auditlog+eventlog+eventrender+sdkrunner（直接 SDK，无 TaskRuntime 跟踪）；app 用 provider+sdkrunner。三条路径三种组装方式行为不一致。run 实质是"pm 专用编排"而非公共 API。应新增统一入口包。 |

---

### 模块 20: internal/store（持久化层）

**概况**：6 个源文件(~1567行) + 1 个测试文件(539行)。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| ST-H1 | `models.go` (623行) | **类型定义与持久化逻辑混合**（根源问题）。同时承担领域实体数据结构（20+ struct）+ 状态枚举（10+ 组常量）+ 数据库操作。上层 `facade_types.go` 120+ 行别名转发是其后果。全系统 99 个文件 import store 很多只为使用类型。应将领域类型迁移到 contracts 或 core/model。 |
| ST-H2 | `db.go` 行49-101 + 行310-426 | **AutoMigrate 嵌入大量破坏性迁移逻辑且无版本化**。每次启动执行：8 处 DROP COLUMN、1 处 DROP TABLE、6 个条件分支 UPDATE(含 CASE WHEN)、多处 DROP INDEX + 重建、DROP VIEW + 重建。有幂等保护但每次启动做无用"尝试删除已删除列"。应引入 schema_migrations 版本号表。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| ST-M1 | `db.go` 行243-297 | **task_status_view 使用相关子查询**。3 个 LEFT JOIN 各含相关子查询（SELECT...WHERE...ORDER BY...LIMIT 1）。SQLite 下通常不是瓶颈但数据增长后退化。应确保组合索引存在。 |
| ST-M2 | `models.go` 行452-459 vs `contracts/channel_gateway.go` 行15-18 | **ChannelType 枚举在 store 和 contracts 重复定义**。同一套值("web"/"im"/"cli"/"api")两个包分别定义，命名风格不同(ChannelWeb vs ChannelTypeWeb)。应统一到一处。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| ST-L1 | `db.go` 行135-166 | **迁移锁使用文件系统目录锁**。os.Mkdir 原子性 + owner.json + PID 检测。processExists 使用 syscall.Kill(pid, 0) 不跨平台，容器中 PID 可能复用。当前场景够用。 |
| ST-L2 | `models.go` 多处 | **15+ 个 JSON 字段缺乏类型约束**。PayloadJSON/ResultJSON/ChecksJSON/ActionJSON/MetricsJSON 等全部 `string` 类型，读取需手动 JSON 解析每个调用点有失败风险，写入无类型保护。应对高频字段定义 Go 结构体用 GORM Serializer。 |

---

### 模块 21: internal/contracts（跨层共享类型）

**概况**：7 个源文件 ~310 行，零内部依赖。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| CT-H1 | 整个包 | **承载范围过窄未发挥解耦作用**。只定义 7 个类型 + 1 个接口（跨模块通信协议）。但系统中 20+ 个核心领域类型和 10+ 组状态枚举全在 store 中。contracts 没真正发挥"跨层共享类型定义"作用，让 store 成为了"类型中心"（99 个文件 import）。应扩展为"领域类型+跨层接口"或新建 core/model 包。 |

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| CT-M1 | `channel_gateway.go` 行43-92 | **InboundEnvelope Normalize/Validate 重复 TrimSpace**。Normalize() 对所有字段 TrimSpace，Validate() 检查前又对部分字段 TrimSpace。按 Normalize→Validate 顺序 Validate 中全部冗余。应让 Validate 入口直接调 Normalize() 或文档明确先 Normalize。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| CT-L1 | `project_meta.go` | **ProjectMetaResolver 接口使用面有限**。ProjectMeta 只有 Name+RepoRoot 两字段，信息量极少。当前够用，未来可能需扩展。 |

---

### 模块 22: internal/infra（基础设施工具）

**概况**：8 个源文件(~939行) + 2 个测试文件，零内部依赖。

#### HIGH

| ID | 位置 | 描述 |
|----|------|------|
| IF-H1 | `openai_compat.go` (213行) | **OpenAI 兼容客户端不应在 infra 层**。infra 定位是系统级工具（exec/git/tmux/text/shell），但 openai_compat 是 AI 业务基础设施。未来加 Anthropic/Gemini 客户端 infra 会膨胀为"AI SDK 集合"。应移到 `agent/provider/` 或 `agent/openai/`。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| IF-L1 | `exec.go` 行66-72 | **包级别函数每次创建临时实例**。Run()/RunExitCode() 每次 NewExecRunner()。ExecRunner 无状态空 struct，无性能问题但模式不直观。低优先级。 |
| IF-L2 | `tmux_target.go` 行14 | **PickObservationTarget 参数顺序不一致**。ctx 不是第一个参数，违反 Go 社区惯例。应调整为 ctx 在首位。 |

---

### 模块 23: internal/repo（仓库操作与模板）

**概况**：12 个源文件(~1034行) + 4 个测试文件，依赖 infra。**架构最健康的包。**

#### MEDIUM

| ID | 位置 | 描述 |
|----|------|------|
| RP-M1 | `config.go` (364行) | **config.go 承载配置 schema/默认值/合并策略三重职责**。结构定义 + 12 个 default 常量 + WithDefaults(70行) + 归一化逻辑 + 合并策略(MergeConfig 60行 + mergeAgentExecConfig 30行) + I/O + 自定义 JSON 反序列化。应拆分为 config_types.go + config_ops.go。 |
| RP-M2 | `config.go` 行57-66 | **模型名硬编码在 config 默认值中**。`defaultCodexModel = "gpt-5.3-codex"`、`defaultClaudeModel = "opus"`、`defaultCodexReasoningEffort = "xhigh"`。与 app/home.go 硬编码是同一类问题。应集中到一处或通过配置注入。 |

#### LOW

| ID | 位置 | 描述 |
|----|------|------|
| RP-L1 | `entrypoints.go` 行25-84 | **CLAUDE.md/AGENTS.md 双入口维护逻辑复杂**。4 种组合情况处理、symlink 创建、注入块管理。分支多但是需求驱动的复杂度。可添加更多注释说明分支设计意图。 |

---

## 三、系统性问题专题分析

### 3.1 store 包成为"类型中心"（根源问题）

**现状**：store 同时承担数据库操作 + 领域类型定义（20+ struct, 10+ 状态枚举）。全系统 99 个文件 import store，大量只为使用类型。

**连锁反应**：
```
store 定义 Ticket/Worker/TaskRun 等类型
  → app/facade_types.go 需要 120+ 行 type X = store.X 别名转发
  → contracts 包未能发挥解耦作用（只有 310 行协议类型）
  → 上层任何包引用 Ticket 都被迫 import store（拉入 gorm 传递依赖）
```

### 3.2 Facade 边界系统性突破

**现状**：app 14,279 行中约 60% 是错误放置的业务实现：

| 应在 services 层的代码 | 当前位置 | 行数 |
|------------------------|----------|------|
| Notebook shaping 全流程 | app/note.go | 1,150 |
| Subagent 编排 | app/project_subagent.go | 526 |
| 飞书 IM 适配 | app/daemon_public_feishu.go | 2,087 |
| Daemon 管理组件 | app/daemon_manager_component.go | 879 |
| Daemon 公共组件 | app/daemon_public_*.go | 788 |

### 3.3 core.Project God Object

12 字段覆盖 4 个不同关注域（身份/文件系统/配置/运行时），22 个文件引用。每个 service 只用 2-4 个字段但被迫接收整个 Project。

### 3.4 ticket 生命周期碎片化

| 操作 | 所在包 |
|------|--------|
| Create/List/BumpPriority/UpdateText | ticket |
| Start/Stop/Attach/Cleanup/Views/Capability | worker |
| Dispatch | pm |
| Archive/workflow 转换 | app |

### 3.5 agent 层缺少统一入口

- pm: provider + run（CLI/tmux 模式）
- channel: sdkrunner 直接调用（绕过 run 编排层）
- app: provider + sdkrunner
- 三条路径三种组装方式，行为不一致

### 3.6 strings.TrimSpace 1800+ 处过度防御

| 模块 | 处数 |
|------|------|
| app | 745 |
| channel | 566 |
| pm | 257 |
| daemon | 168 |
| worker + agent + 其他 | ~64+ |

---

## 四、正面评价

1. **ActionHandler 接口注入**：t3 重构干净彻底，channel 横向服务耦合已消除
2. **agentcli 子包**：零外部依赖，职责单一，系统中边界最清晰的子模块
3. **eventrender 子包**：策略模式得当，测试全面，抽象层次正确
4. **EventDeduplicator**：LRU+TTL 去重器实现精良
5. **EventBus 发布不阻塞**：慢消费者不拖垮系统
6. **worker 资源管理防御设计**：孤儿 session 清理、worktree prune-retry、defer 回滚
7. **task 状态幂等性**：canceled 不可覆盖、duplicate request_id 返回已有记录
8. **daemon Component 模式**：接口清晰，生命周期管理合理
9. **infra 包整体**：接口抽象合理（CommandRunner/GitClient/TmuxClient），依赖注入支持
10. **contracts 零依赖设计**：正确的叶子包定位
11. **noun-verb CLI 模式**：命令组织一致，三段式错误处理语义明确
12. **架构约束测试**：已建立 cmd 层 import 防护网
13. **repo 包**：职责清晰，Layout 纯值对象设计良好，整体最健康

---

## 五、治理优先级路线图

### Phase 0: 止血（立即可做，低风险）

| 动作 | 预估 | 收益 |
|------|------|------|
| 大文件纯拆分：execution_host→3文件, gatewaysend→3文件, sdkrunner→5文件 | 1天 | 可读性 |
| 移除 WS 事件 Text 字段 TrimSpace(DM-M3) | 0.5h | 修复潜在数据篡改 bug |
| 集中 pm 时间常量到 constants.go | 0.5天 | 消除重复定义 |
| 统一 pm 环境变量 map 构造 | 0.5天 | 消除已有不一致 bug 风险 |

### Phase 1: 类型归位（高收益结构性改造）

| 动作 | 预估 | 收益 |
|------|------|------|
| store 领域类型迁移到 contracts/core/model | 3-5天 | 消除 120+ 别名，减少 store import |
| facade_types.go 别名逐步替换为独立类型 | 随上 | Facade 真正隔离 |
| ChannelType 等重复枚举统一 | 0.5天 | 消除重复 |

### Phase 2: Facade 边界修正

| 动作 | 预估 | 收益 |
|------|------|------|
| note.go → services/notebook/ | 2-3天 | app 减少 1150 行 |
| project_subagent.go → services/subagent/ | 2天 | app 减少 526 行 |
| daemon_public_feishu.go → services/channel/feishu/ | 3-5天 | app 减少 2087 行 |
| cmd_gateway_feishu.go → 同上 | 随上 | cmd 减少 1413 行 |
| daemon_manager_component DB 操作下沉到 service | 1-2天 | 消除 app 直接操作 DB |

### Phase 3: 服务边界修正

| 动作 | 预估 | 收益 |
|------|------|------|
| 重建 ticket service 完整职责 | 3-5天 | 消除 ticket 碎片化 |
| worker 改为通过 ticket service 操作 ticket | 2天 | ticket 成为单一权威 |
| pm 抽取 AgentLauncher 接口 | 2天 | 消除跨层穿透 |
| agent/run 改为 hook/callback 消除→services 反向依赖 | 2-3天 | agent 层独立性恢复 |
| pm/workflow_notify 移出 pm 包 | 0.5天 | 消除横向依赖 |

### Phase 4: 内部优化

| 动作 | 预估 | 收益 |
|------|------|------|
| core.Project God Object 拆分 | 5天+ | 22 文件受益 |
| channel 双路径持久化统一 | 3-5天 | 消除分裂 |
| core↔task Input 类型镜像消除 | 1天 | 消除 150 行机械复制 |
| agent 层统一入口包 | 2-3天 | 消除三条并行路径 |
| strings.TrimSpace 系统性清理 | 3-5天 | 消除 1800+ 处噪音 |
| store AutoMigrate 版本化 | 2天 | 消除每次启动无效 DDL |

### Phase 5: 长期演进

- cmd_config.go 配置解析业务逻辑下沉
- cmd_gateway_ws.go WS 服务器逻辑下沉
- TaskRuntime 接口按 ISP 拆分
- 结构化日志统一（替代 log.Printf）
- 硬编码模型名/provider 白名单集中管理
- openai_compat 从 infra 移到 agent 层
