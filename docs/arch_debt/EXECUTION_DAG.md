# 架构债务执行 DAG（2026-02-26）

> 范围：`docs/arch_debt/TICKETS.md` 的 `T01~T39`
> 调度约束：每次执行 1~4 个 ticket；严格按依赖优先级推进

## 执行规则

1. 基础组件与状态/迁移基础设施优先（`T38/T39/T24`），避免后续返工。
2. 同一批次内不放置“硬依赖”关系，保证可并行。
3. 同一子域（PM/Channel/Store）尽量连续推进，减少上下文切换成本。

## 当前执行状态（Kernel 回写）

- 更新时间：`2026-02-27`
- 已完成批次：`W01` `W02` `W03` `W04` `W05` `W06A` `W06B` `W07A` `W07B` `W08A` `W08B` `W09A` `W10A` `W10B` `W10C`
- 当前批次：`W10B + W10C`（`done`）
- W01 完成票：`T06(13)` `T24(31)` `T38(45)` `T39(46)`（均已 merge/archived）
- W02 完成票：`T19(26)` `T21(28)` `T03(10)` `T10(17)`（均已 merge/archived）
- W03 完成票：`T01(8)` `T02(9)` `T04(11)` `T11(18)`（均已 merge/archived）
- W04 已完成票：`T22(29)` `T14(21)` `T31(38)` `T29(36)`
- W05 已完成票：`T23(30)` `T15(22)` `T30(37)` `T37(44)`
- W06A 已完成票：`T26(33)` `T12(19)`
- W06B 已完成票：`T07(14)`
- W07A 已完成票：`T28(35)`（query service 归位）
- W07B 已完成票：`T27(34)`（ticket workflow 守卫收敛到 FSM）
- W08A 已完成票：`T18(25)`（Provider/默认值/客户端归位）
- W08B 已完成票：`T13(20)`（DaemonManager recovery/对账逻辑下沉 service + ActionExecutor 构造注入）
- W09A 已完成票：`T33(40)`
- W09A 进行中票：`T35(42)`
- W10A 已完成票：`T32(39)`（logs->preview 重命名 + WorkerLookup 收口）
- W10C 已完成票：`T08(15)`（Channel CLI/SDK 执行入口统一到 services/agentexec）
- 下游强制约束：
  - 状态机相关改造必须复用 `internal/fsm/*`（`T20/T27/T34` 不得再写隐式转换）。
  - 迁移相关改造必须复用 migration runner + `schema_migrations`（`T25` 直接沿用）。
  - PM 配置相关改造必须复用 `buildBaseEnv()` + `constants.go` + 外置 prompt 模板。
  - 日志与 daemon handler 必须走 `*slog.Logger` 注入链路 + recover middleware。
  - Feishu 改造必须复用 `internal/services/channel/feishu/*`（`T04` 禁止维护第二套实现）。
  - gateway/ws 改造必须复用 `internal/services/channel/ws/*` 与 `internal/gateway/client/*`。
  - 跨层枚举/状态类型统一引用 `internal/contracts/*`（禁止回流到 `store` 常量）。

## W06B 回写（T07 AgentExec 服务层入口 1/3）

- 状态：`T07` 已完成（2026-02-27）。
- 关键产物：
  - 新增 `internal/services/agentexec/`，承接原 `internal/agent/run` 的 Process/SDK/Tmux 执行编排与测试代码。
  - PM 执行入口已收敛到服务层：`dispatch_agent_exec.go`、`dispatch_worker_sdk.go` 改为依赖 `services/agentexec`。
  - 删除旧 `internal/agent/run` 实现，消除 `agent/run -> services/core.TaskRuntime` 的反向依赖。
  - 架构守卫白名单已同步：`internal/arch/constraints_test.go` 允许 `internal/services/agentexec/process.go` 受控使用 `os/exec`。
- 回归验证：
  - `go test ./internal/services/agentexec/...`
  - `go test ./internal/services/pm/...`
  - `go test ./...`
  - `go build ./...`
  - 以上命令全部通过。
- 依赖推进：
  - `T07 -> T08 -> T09` 前置已满足，T08 可基于 `services/agentexec` 继续统一多路径执行入口。

## W10C 回写（T08 AgentExec（2/3）：Channel/App 统一执行入口）

- 状态：`T08` 已完成（2026-02-27）。
- 关键产物：
  - `internal/services/channel/agent_exec.go` 的 CLI/SDK 两条入口均已改为复用 `agentexec`：
    - CLI：`runAgentCLI` -> `agentexec.NewProcessExecutor`
    - SDK：`runAgentSDK` -> `agentexec.NewSDKExecutor`
  - Channel 执行链路已注入 TaskRuntime 元数据：`OwnerType=channel`、`TaskType=channel_turn`、`SubjectType=channel_conversation`。
  - `internal/contracts/task_status.go` 新增权威枚举：`TaskOwnerChannel`、`TaskTypeChannelTurn`；并同步 `internal/services/task/service_helpers.go`、`internal/app/facade_types.go` 的 owner 校验。
  - `internal/services/channel/agentcli/runner.go` 已移除 `os/exec` 直接依赖，统一走 `agentexec.ProcessExecutor`；`internal/services/agentexec/process.go` 增加 `Stdin` 支持兼容 `InputStdin` 场景。
  - `internal/arch/constraints_test.go` 已移除 `channel/agentcli/runner.go` 的 `os/exec` 白名单，防止绕过回归。
- 回归验证：
  - `go test ./internal/services/channel/...`
  - `go test ./internal/services/agentexec/...`
  - `go test ./internal/services/task/...`
  - `go test ./internal/arch/...`
  - `DALEK_DISPATCH_DEPTH=0 go test ./...`
  - `go build ./...`
  - `DALEK_DISPATCH_DEPTH=0 go vet ./...`
  - 以上命令全部通过。
- 依赖推进：
  - `T07 -> T08` 已闭环，`T09` 可在统一执行入口之上继续推进会话/收口改造。

## W10B 回写（T36 PM 可靠性与测试补齐）

- 状态：`T36` 已完成（2026-02-27）。
- 关键产物：
  - **续租 goroutine 可观测性**：`internal/services/pm/dispatch_runner.go` 中的续租 goroutine 已从 `_ = s.renewPMDispatchJobLease(...)` 重构为 `startLeaseRenewal` 方法。新增连续失败计数器、Warn/Error 分级日志（达到 `leaseRenewalEscalateThreshold=3` 次后升级为 Error）、`lease_renewal_failed` 事件记录。
  - **context 泄露治理**：`internal/services/pm/context_cancel.go` 的 `newCancelOnlyContext` 已从 goroutine 监听模式重写为 `context.AfterFunc`（Go 1.21+），消除 goroutine 泄露风险。取消语义不变：仅传播 `context.Canceled`，不传播 `context.DeadlineExceeded`。
  - **核心路径测试补齐**：
    - 新增 `worker_loop_test.go`（8 个测试）：覆盖 StopsOnDone / StopsOnWaitUser / StopsOnEmptyNextAction / ContinuesThenStops / LaunchError / WaitError / DefaultPromptOnEmpty / ContextCancellation。
    - 新增 `worker_ready_wait_test.go`（9 个测试）：覆盖 AlreadyRunning / NilWorker / EmptySession / StoppedWorkerRejects / FailedWorkerRejects / TimeoutWhileCreating / TransitionCreatingToRunning / ParentContextCancel / TransitionToStopped。
    - 新增 `dispatch_runner_test.go`（5 个测试）：覆盖 LogsOnFailure / EscalatesAfterThreshold / StopsOnClose / StopsOnContextCancel / RecordsEvent。
  - 测试注入模式：`service.go` 新增 `sdkHandleLauncher` 可选函数字段，`worker_loop.go` 新增 `launchWorkerSDK` 委托方法，生产路径零影响，测试可注入 fake handle。
- 验收结果：
  - `go test ./internal/services/pm/...` 通过（22 个新增测试 + 全部已有测试）
  - `go test ./...` 通过（65.947s）
  - `go build ./...` 通过
  - `go vet ./...` 通过
  - 2026-02-27 复验：在派发上下文环境执行 `go test ./...` 时会被 `DALEK_DISPATCH_DEPTH=1` 的二次派发保护拦截；按执行合约覆写 `DALEK_DISPATCH_DEPTH=0 go test ./... -count=1` 后全量通过。
- 变更统计：9 files changed, 963 insertions(+), 29 deletions(-)。
- 下游约束更新：
  - 后续涉及续租逻辑改动必须维护 `startLeaseRenewal` 方法的日志/事件可观测性，禁止回退为静默吞错模式。
  - 后续涉及 context 取消传播的改动必须复用 `context.AfterFunc` 模式，禁止新增 goroutine 监听 parent context。
  - `sdkHandleLauncher` 注入口仅供测试使用，生产代码不得设置此字段。

## W08A 回写（T18 Provider/默认值/客户端归位）

- 状态：`T18` 已完成（2026-02-26）。
- 关键产物：
  - Provider 默认值与白名单统一入口落地到 `internal/agent/provider/defaults.go`，并提供 `NormalizeProvider/IsSupportedProvider/DefaultModel/DefaultReasoningEffort`。
  - `AgentConfig` 与 provider 执行工厂职责拆分：`config.go` 仅保留结构化配置；`factory.go` 负责 `NewFromConfig` 构建。
  - openai 兼容客户端已从 `internal/infra/openai_compat.go` 迁移到 `internal/agent/provider/openai_compat.go`，infra 层不再承载该 LLM 客户端实现。
  - `Config -> AgentConfig` 转换入口收敛为 `repo.AgentConfigFromExecConfig`，`pm dispatch` 与 `subagent settings` 统一复用，移除重复手写映射。
  - `eventrender.ForProvider` 与配置校验（`app/project`、`internal/config`）已改为复用 provider 白名单入口，减少 provider 枚举散布。
- 回归验证：
  - `go test ./internal/agent/... ./internal/services/task/...`
  - `go test ./...`
  - `go build ./...`
  - `go vet ./...`
- 以上命令全部通过。

## W08B 回写（T13 App DaemonManager 收敛）

- 状态：`T13` 已完成（2026-02-27）。
- 关键产物：
  - 新增 `internal/services/pm/recovery.go`，收敛 `recover stuck dispatch jobs`、`expired lease`、`active task run recovery`、`PMState recovery summary` 持久化逻辑。
  - `internal/app/daemon_manager_component.go` 已去除 app 层 `gorm` 直访与事务实现，恢复/对账与 warmup 全部改为调用 `pm service`/`worker service`。
  - `internal/services/pm/inbox_upsert.go` 暴露 `UpsertOpenInbox`，worker session recovery 的 inbox 创建不再由 app 直连 DB。
  - `internal/services/channel/action_executor.go` 改为持有注入的 `ticket/pm/worker` service；`internal/services/channel/service.go` 统一装配并复用 executor，消除执行路径内重复 `New(...)`。
- 验收结果：
  - `go build ./...` 通过
  - `go vet ./...` 通过
  - `go test ./internal/app/... ./internal/services/pm/... ./internal/services/channel/...` 通过
- 约束对齐：
  - 复用 `core.NewProject/buildCoreProject` 既有构造边界（未新增 `core.Project{}` 直写构造）。
  - app 层不再直接访问 DB（DaemonManager 已改为 service facade）。

## W07B 回写（T27 Ticket workflow 权威归位）

- 状态：`T27` 已完成（2026-02-27）。
- 关键产物：
  - 新增 `internal/fsm/ticket_workflow_guards.go`，集中落地 ticket workflow 高阶守卫：`CanStartTicket`、`CanDispatchTicket`、`CanArchiveTicket`、`CanManualSetWorkflowStatus`、`ShouldPromoteOnDispatchClaim`、`ShouldDemoteOnDispatchFailed`、`ShouldApplyWorkerReport`、`CanReportPromoteTo`。
  - 新增 `internal/fsm/ticket_workflow_guards_test.go`，覆盖守卫函数全部分支与关键状态矩阵（含历史别名归一化分支）。
  - `internal/services/pm/workflow.go`、`start.go`、`direct_dispatch.go`、`dispatch_queue.go` 的目标 hardcoded workflow 守卫已改为统一调用 `fsm` 守卫函数，PM 层不再散落重复规则。
  - 全流程继续复用 `fsm.TicketWorkflowTable` 与 `fsm.CanTicketWorkflowTransition`，未在 `internal/fsm/` 外新增 workflow 转换定义。
- 回归验证：
  - `go test ./internal/fsm/... ./internal/services/pm/...`
  - `go test ./...`
  - `go build ./...`
  - `go vet ./...`
  - 以上命令全部通过。

## 关键依赖边（DAG）

- `T03 -> T04`
- `T07 -> T08 -> T09`
- `T14 -> T15 -> T16 -> T17`
- `T21 -> T22 -> T26 -> T27`
- `T21 -> T23 -> T28`（`T28` 已完成，依赖链闭环）
- `T39 -> T20`
- `T39 -> T27`
- `T39 -> T34`
- `T24 -> T25`
- `T29 -> T30`
- `T31 -> T35`
- `T12 -> T13`
- `T19 -> T13`
- `T26 -> T13`
- `T04 -> T05`
- `T11 -> T05`
- `T12 -> T05`
- `T33 -> T36`
- `T34 -> T36`
- `T35 -> T36`
- `T38 -> T32`

## W01 回写（T39 FSM 基础组件）

- 状态：`T39` 已交付，`internal/fsm/` 成为状态转换权威入口。
- 通用 API（下游统一复用）：
  - `fsm.CanTransition`
  - `fsm.ValidTransitions` / `fsm.ValidTargets`
  - `fsm.IsTerminal`
- 已落地权威转换表：
  - `fsm.TicketWorkflowTable`
  - `fsm.WorkerLifecycleTable`
  - `fsm.PMDispatchJobTable`
  - `fsm.TaskRunOrchestrationTable`
- T20/T27/T34 已解锁复用入口：
  - `T20`：复用 `fsm.TaskRunOrchestrationTable` + `fsm.CanTaskRunTransition`，禁止新增 TaskRun 隐式转换条件。
  - `T27`：复用 `fsm.TicketWorkflowTable` + `fsm.CanTicketWorkflowTransition`，收敛 ticket workflow 规则到单一来源。
  - `T34`：复用 `fsm.PMDispatchJobTable`/`fsm.WorkerLifecycleTable` + `fsm.CanTransition`，统一调度链路状态守卫。

## 执行批次（拓扑分层）

| 批次 | 票数 | tickets | 说明 |
|---|---:|---|---|
| W01 | 4 | `T38` `T39` `T24` `T06` | 基础设施先行：日志/FSM/迁移/PM 配置 |
| W02 | 4 | `T19` `T21` `T03` `T10` | 核心模型与通道基建起步 |
| W03 | 4 | `T01` `T02` `T04` `T11` | app/cmd 第一轮归位（含 Feishu 复用） |
| W04 | 4 | `T22` `T14` `T31` `T29` | 类型迁移第二阶段 + Channel/Daemon 拆分启动 |
| W05 | 4 | `T23` `T15` `T30` `T37` | 类型迁移收尾 + Channel/Daemon/Sdkrunner 并行 |
| W06 | 4 | `T26` `T12` `T16` `T07` | ticket/app/channel/agentexec 边界同步收敛 |
| W07 | 4 | `T27` `T28` `T17` `T08` | workflow/query/channel/agentexec 第二轮收口 |
| W08 | 4 | `T20` `T13` `T18` `T25` | TaskRuntime/DaemonManager/Provider/Store 类型化 |
| W09 | 4 | `T09` `T33` `T34` `T35` | PM 调度主链与 agentexec 收尾 |
| W10 | 3 | `T36` `T32` `T05` | 可靠性与测试补齐 + 日志包收口 + cmd 测试闭环 |

## W01 回写（T24）

- 状态：`T24` 已完成（2026-02-26）
- 交付物：
  - `internal/store` 已引入版本化迁移入口（`RunMigrations`）与 `schema_migrations` 版本记录表。
  - `AutoMigrate` 已改为统一走 migration runner，启动流程不再内联执行破坏性迁移语句。
  - 已补齐迁移相关测试：基线迁移、幂等重跑、失败中断、老库升级路径。
- 依赖确认：
  - `T25 -> T24` 的前置已稳定，可基于同一 migration 入口继续追加类型化迁移版本。

## W01 产出回写（T38）

- 统一日志入口：落地 `log/slog` + 依赖注入，`internal/services/core/project.go` 新增 `Logger *slog.Logger`，并新增 `internal/services/core/logger.go`（默认/静默/兜底 logger）。
- services 层日志迁移：`internal/services/channel`、`internal/services/pm`、`internal/services/gatewaysend` 已移除 `log.Printf`，改为注入 logger 输出结构化字段。
- daemon 安全屏障：新增 `internal/services/daemon/middleware.go::RecoverMiddleware`，并接入 Internal API 与 Public Gateway，handler panic 不再击穿进程。
- 测试补齐：新增 logger 与 recover 行为测试（`core/logger_test.go`、`daemon/middleware_test.go`、`channel/tool_approval_test.go`），支持可控日志输出断言。
- 下游约束更新：后续票（含 `T32`）必须复用 `*slog.Logger` 注入链路，且 daemon HTTP handler 必须经过 recover middleware。

## W02 回写（T03）

- 状态：`T03` 已完成（2026-02-26）。
- 交付物：
  - 新增 `internal/services/channel/feishu/`，承载飞书 sender/webhook/card/command/message 主链路，Feishu 业务逻辑已从 app 层下沉。
  - `internal/app/daemon_public_feishu.go` 已收敛为 facade，仅保留配置适配与服务转调。
  - 新服务默认走 `slog` 注入（`core.EnsureLogger`），daemon 的 HTTP 接入仍复用 `RecoverMiddleware`，未新增裸 handler 路径。
  - 已补齐服务层关键测试（`internal/services/channel/feishu/service_test.go`），并通过 app/cmd 侧回归测试。
- T04 复用入口（强制）：
  - sender：`feishu.NewSender(feishu.SenderConfig)`
  - webhook：`feishu.NewWebhookHandler(...)`
  - path：`feishu.BuildWebhookPath(...)` / `feishu.NormalizeWebhookSecretPath(...)`
  - `cmd/dalek/cmd_gateway_feishu.go` 在 T04 中必须直接复用以上入口，禁止再维护独立实现分支。

## W03 回写（T04）

- 状态：`T04` 已完成（2026-02-26）。
- 交付物：
  - `cmd/dalek/cmd_gateway_feishu.go` 已重写为 thin facade（`92` 行），cmd 层仅保留参数读取、类型别名与服务转发，不再承载飞书业务实现。
  - 发送与 webhook 主链路已复用共享服务入口：`feishu.NewSender(feishu.SenderConfig)`、`feishu.NewWebhookHandler(...)`。
  - webhook 路径能力已复用共享入口：`feishu.BuildWebhookPath(...)` / `feishu.NormalizeWebhookSecretPath(...)`。
  - 为 cmd/e2e 兼容场景新增服务层导出转发（命令处理/提示文案等），避免 cmd 层回流业务逻辑。
- 回归结果：
  - `go build ./cmd/dalek/...` 通过
  - `go test ./cmd/dalek/... -run Feishu -count=1` 通过
  - `go test ./cmd/dalek/... -run CLI -count=1` 通过
- `T05` 前置满足情况（`T04 -> T05`, `T11 -> T05`, `T12 -> T05`）：
  - `T04`：`done`
  - `T11`：`pending`
  - `T12`：`pending`

## W02 回写（T19 Core.Project 拆分）

- 状态：`T19` 已完成（2026-02-26）。
- 交付物：
  - `core.Project` 去除 `ProjectDir/ConfigPath/DBPath` 三个冗余字段；按 W01 logger 基线计数为 `15 -> 12`（历史口径对应 `14 -> 11`），统一通过 `ProjectDir()/ConfigPath()/DBPath()` 便捷方法读取。
  - 新增 `core.NewProject()` + `Validate()`，集中校验核心依赖（`Name/Key/RepoRoot/Layout/DB/Logger/Tmux/Git/TaskRuntime`）。
  - app 层新增 `buildCoreProject()`，`openProject/initProjectFiles` 构造路径统一，避免重复 literal 构造。
  - 新增 `internal/testutil/project.go`，沉淀共享 `FakeTmuxClient/FakeGitClient/NewTestProject`，worker/pm 测试 helper 完成复用。
  - 补齐构造与注入回归测试：新增 `internal/services/core/project_test.go`，并更新 app/worker/pm/channel 相关测试。
- 对后续票影响：
  - `T13`（Facade/Service 边界收口）应复用 `core.NewProject/buildCoreProject`，不再新增 `core.Project{...}` 直写构造。
  - `T21/T03/T10` 在接入 core 依赖时统一走按需注入，不再依赖历史冗余路径字段。
  - 后续新增测试 fixture 统一落在 `internal/testutil/`，禁止在域内重复维护 fake tmux/git 实现。

## 每轮启动 Dispatch 必带指令（强制）

每次启动 `Wxx` 前，dispatch prompt 必须包含以下 8 项，缺一不可：

1. `Wave` 与本轮 tickets（1-4 个）。
2. 前置依赖校验（列出本轮依赖的上游 tickets，并确认已完成）。
3. 当前“架构状态增量”（已完成 waves 产出的新组件/新边界）。
4. 本轮“必须复用/必须遵循”的组件与边界（禁止绕过）。
5. 本轮验收口径（功能等价、架构约束、测试要求）。
6. 本轮结束回写动作（更新 `EXECUTION_DAG.md` 与 `.dalek/AGENTS.md` 的 `<current_phase>`）。
7. 阻塞分叉规则（若上游未完成，如何调整 DAG 与改派）。
8. 执行方式约束：禁止调用 `dalek ticket dispatch`、`dalek worker run`（含脚本间接调用）；必须在当前 ticket/worktree 直接执行所需命令自行推进。

推荐模板（直接粘贴后替换占位符）：

```text
[ARCH-DEBT Wxx DISPATCH CONTRACT]
Wave: Wxx
Tickets: <T.. T.. T..>
前置依赖校验: <依赖票> = done
架构状态增量: <已落地组件/接口/边界>
本轮必须复用:
- <组件/接口 A>
- <组件/接口 B>
本轮禁止事项:
- 禁止绕过 <新服务/新状态机/新迁移入口>
- 禁止在 <层> 直接访问 <下层实现>
- 禁止调用 dalek ticket dispatch / dalek worker run / 二次派发脚本
- 当 DALEK_DISPATCH_DEPTH != 0 时，严禁二次派发；仅允许本地执行或必要时 `dalek worker report --next wait_user`
验收口径:
- 功能回归: <命令/路径>
- 架构约束: <import/边界/状态机规则>
- 测试: <新增或更新测试>
完成后回写:
- 更新 docs/arch_debt/EXECUTION_DAG.md（依赖变化与下一轮）
- 更新 .dalek/AGENTS.md <current_phase>（状态与关注点）
阻塞分叉:
- 若 <上游票> 未完成，则只执行 <不依赖子集>，并更新 DAG
```

## 分轮架构状态提醒（启动时必须写入 dispatch）

| Wave | 本轮 tickets | 启动时必须提醒下游的“架构状态变化” |
|---|---|---|
| W01 | `T38` `T39` `T24` `T06` | 产出日志统一入口（slog + 注入点）、通用 FSM、migration runner、PM 配置基线（`buildBaseEnv()` + `constants.go` + dispatch prompt 模板外置）。后续票禁止再引入并行实现。 |
| W02 | `T19` `T21` `T03` `T10` | 核心类型与 project 结构开始归位；Feishu/WS 归位后，下游 cmd/app 必须复用共享服务，不再自建链路。 |
| W03 | `T01` `T02` `T04` `T11` | app/cmd 第一轮归位完成后，后续票不得把业务逻辑留在 cmd/app；配置逻辑必须走统一入口。 |
| W04 | `T22` `T14` `T31` `T29` | 类型迁移第 2 阶段后，store 不再作为跨层类型中心；channel 入站与 gatewaysend 分层成为默认路径。 |
| W05 | `T23` `T15` `T30` `T37` | 类型迁移收尾后，新增跨层类型统一落在 core/contracts；daemon/channel/sdkrunner 继续按分层边界推进。 |
| W06 | `T26` `T12` `T16` `T07` | ticket service、facade 边界、channel 无 store 直连、agentexec 服务层入口开始生效，下游必须按新入口开发。 |
| W07 | `T27` `T28` `T17` `T08` | workflow/query/channel/agentexec 第二轮收口，ticket 生命周期与查询语义进入统一权威实现。 |
| W08 | `T20` `T13` `T18` `T25` | TaskRuntime 必须复用 FSM；DaemonManager 与 Provider/默认值单点化；高频 JSON 字段类型化路径确定。 |
| W09 | `T09` `T33` `T34` `T35` | PM 调度主链与通知解耦完成后，续后 PM 变更必须遵循拆分后的职责边界。 |
| W10 | `T36` `T32` `T05` | 可靠性/测试/日志命名收口，后续优化票必须以该轮产出的测试护栏和日志体系为基线。 |

## W01 PM 配置基线（T06）

- `internal/services/pm/env.go`：新增 `buildBaseEnv()`，PM dispatch/bootstrap/worker SDK 三条路径统一复用基础 env 构建。
- `internal/services/pm/constants.go`：集中维护 PM 域 timeout/interval 与关键字符串常量，禁止新增分散 magic number。
- `internal/repo/templates/pm/dispatch_prompt_v1.tmpl`：dispatch prompt 模板外置，Go 代码仅传递变量并渲染。

## W02 回写（T10 Gateway WS 归位）

- 状态：`T10` 已完成（2026-02-26），WS server/client 已完成 cmd 层瘦身与服务层复用。
- cmd/service 边界变化：
  - `cmd/dalek/cmd_gateway_ws.go` 仅保留参数解析与 HTTP 启动，WS 业务处理下沉到 `internal/services/channel/ws.NewSyncHandler`。
  - `cmd/dalek/cmd_gateway.go` 的 `gateway chat` 已改为调用 `internal/gateway/client`，cmd 不再承载 WS 握手/收发/帧解析实现。
- 协议与复用层归位：
  - 新增 `internal/services/channel/ws/`：统一 `InboundFrame/OutboundFrame`、帧常量、辅助函数与同步 WS server handler。
  - 新增 `internal/gateway/client/`：统一 daemon WS URL 解析与 chat client 连接/握手/收发逻辑。
  - `internal/services/daemon/api_internal_ws.go` 改为复用 `channel/ws` 协议类型与 helper，移除 daemon 侧重复帧定义。
  - `tools/gateway_ws_chat/main.go` 改为复用 `channel/ws` 协议类型，移除 tools 侧重复帧定义。
- 测试与回归：
  - 新增 `internal/services/channel/ws/{protocol_test.go,handler_test.go}`。
  - 新增 `internal/gateway/client/{url_test.go,ws_client_test.go}`。
  - 已通过 `go test ./...` 全量回归，CLI 与 daemon WS 通道行为保持一致。

## W02 回写（T21 类型归位 1/3）

- 状态：`T21` 已完成（2026-02-26），跨层高传播枚举已归位到 `internal/contracts`。
- 关键产物：
  - 新增 `contracts` 权威类型文件：`ticket_status.go`、`worker_status.go`、`dispatch_status.go`、`task_status.go`、`inbox_status.go`、`merge_status.go`、`channel_status.go`。
  - `store/models.go` 不再定义上述枚举，ORM 字段统一改为引用 `contracts` 类型。
  - `CanonicalTicketWorkflowStatus` 已迁入 `contracts`，并补充兼容回归测试 `contracts/ticket_status_test.go`。
  - `ChannelType` 双重定义已消除，统一为 `contracts.ChannelType` 强类型。
- T22 解锁点（`T21 -> T22`）：
  - 可以在既有 `contracts` 类型边界上继续迁移第二批跨层类型，不再以 `store` 作为类型来源。
  - 可以继续清理依赖面大的消费方，默认直接引用 `contracts` 枚举/常量。
- T23 解锁点（`T21 -> T23`）：
  - 可以沿同一边界完成第三批与收尾迁移（含 facade 进一步收口），无需再引入新的类型落点。
  - 新增跨层领域类型必须落在 `contracts`，禁止回流到 `store` 或新增重复定义。

## W07A 回写（T28 Ticket 视图查询归位）

- 状态：`T28` 已完成（2026-02-27），ticket 查询语义已收敛到 `internal/services/ticket` 单一入口。
- 交付物：
  - 新增 `internal/services/ticket/query.go`：`QueryService` 负责 `ListTicketViews` 编排，按 `fetchTicketViewData + buildTicketView` 分层。
  - 新增 `internal/services/ticket/views.go`：`TicketView`、`ComputeTicketCapability` 与 `computeDerivedRuntimeHealth`，保留现网查询语义。
  - `internal/app/project.go` + `internal/app/home.go` 已改为注入并调用 `ticketQuery.ListTicketViews`，不再通过 worker 包聚合查询。
  - 删除 `internal/services/worker/views.go`，并将 `ListTicketViews` 相关测试迁移到 `internal/services/ticket/query_test.go` 与 `views_test.go`。
- 验收结果：
  - `go test ./internal/services/ticket/... ./internal/services/worker/...` 通过
  - `go test ./... && go build ./... && go vet ./...` 通过
- 后续解锁：
  - `T21 -> T23 -> T28` 依赖链已闭环。
  - W07 余下 `T27` `T17` `T08` 可按既有 DAG 继续推进（无新增硬依赖）。

## W04 回写（T22 类型归位 2/3）

- 状态：`T22` 已完成（2026-02-26），Ticket 高传播领域类型已迁移到 `internal/contracts`，store 不再作为该批类型的权威定义入口。
- 关键产物：
  - 新增 `contracts` 领域模型文件：`ticket.go`、`worker.go`、`inbox.go`、`merge.go`，承接 `Ticket/Worker/MergeItem/InboxItem/TicketWorkflowEvent/WorkerStatusEvent`。
  - `internal/store/models.go` 删除上述 6 个结构体定义，改为 `type alias` 指向 `contracts`；store 继续承载 ORM 与持久化职责。
  - `internal/app/facade_types.go` 的四个透明别名改为 `contracts.*`，并移除 `store` import，阻断 store 变更直穿 app facade。
  - `services/pm`、`services/worker`、`services/ticket`、`services/notebook`、`services/channel/ws`、`app`、`cmd_gateway_ws` 的生产代码已切换到 `contracts` 类型引用。
- 回归验证：
  - `go test ./...` 通过
  - `go build ./...` 通过
  - `go vet ./...` 通过
- 对后续票约束：
  - `T23` 继续迁移剩余高传播结构（`PMState/TaskRun/SubagentRun/Channel*`）时，必须沿用本轮 `contracts` 权威边界，不得回流到 `store`。
  - `T26` 修复 ticket service 与 worker 读写路径时，新增跨层类型只允许使用 `contracts`，不得新增 facade 透明别名泄露底层实现。

## W06A 回写（T26 Ticket Service 修复与收敛）

- 状态：`T26` 已完成（2026-02-26），ticket create/get/report 主链路保持稳定，worker 不再直连 ticket 表。
- 关键产物：
  - `internal/services/ticket/service.go`：修复 `Create` 空 description 场景，新增 `GetByID(ctx, id)`。
  - `internal/services/worker/{service,start,views,cleanup}.go`：引入 `TicketReader` 依赖并替换 3 处 `tickets` 直连查询，ticket 读取统一走 service。
  - `internal/app/home.go` 与 `channel/pm` 相关装配点已适配 `worker.New(project, ticketSvc)`，避免构造回退导致绕层访问。
  - `internal/services/ticket/service_test.go` 补齐空 description 与 `GetByID` 测试；worker/pm/channel 测试装配同步更新。
- 解锁状态（T26 对下游）：
  - `T27`：`T21 -> T22 -> T26` 链路已满足，且 `T39` 已完成，workflow 收口可直接进入实施。
  - `T13`：`T12/T19/T26` 前置已满足，且已于 `2026-02-27` 完成收口与回归。
- 回归验证：
  - `go test ./internal/services/ticket/... ./internal/services/worker/...` 通过
  - `go test ./...` 通过
  - `go build ./...` 通过
  - `go vet ./...` 通过

## W03 回写（T01 Notebook 归位）

- 状态：`T01` 已完成（2026-02-26），notebook 业务逻辑已从 `internal/app/note.go` 下沉到 `internal/services/notebook/service.go`，app 层只保留 facade 转调。
- 交付物：
  - 新增 `internal/services/notebook/`，统一承载 Add/List/Get/Process/Approve/Reject/Discard 与 shaping/helpers 逻辑。
  - `app.Project` 已注册 `notebook` service（`assembleProject()` 注入 `notebook.New(cp)`）；`internal/app/note.go` 已去业务化。
  - notebook 类型迁移到 service 层，`internal/app/api_types.go` 仅保留兼容别名，CLI/daemon 调用链无回退。
  - 新增 `internal/services/notebook/service_test.go` 覆盖 Add/Process/List/Approve 核心流程，并通过 `go build ./...` + `go test ./...` 全量回归。
- 对 W04/W05 的影响：
  - W04（`T22` `T14` `T31` `T29`）推进类型迁移与拆分时，禁止将 notebook 编排逻辑回流到 app/cmd；统一复用 `services/notebook` 入口。
  - W05（`T23` `T15` `T30` `T37`）继续类型收口时，notebook 跨层契约优先落在 `services/notebook`/`contracts`，禁止新增上层对 `store` 常量的直接依赖。

## W03 回写（T02 Subagent 归位）

- 状态：`T02` 已完成（2026-02-26），subagent 编排与 I/O 已从 app 层下沉到 `internal/services/subagent/`。
- 交付物：
  - 新增 `internal/services/subagent`，落地 `Service.Submit/Run` 主链路与 `settings/runtime/helpers` 逻辑。
  - `internal/app/project_subagent.go` 已瘦身为 facade（`41` 行），仅保留参数映射与服务转调。
  - `internal/app/home.go` 的 `assembleProject()` 已按 `core.Project` 注入 `subagent` service，复用 T19 的 Project 收敛方式。
  - `SubmitSubagentRun/RunSubagentJob` 公开签名保持不变；daemon/cmd 调用链路无需改动。
  - 运行产物路径与文件保持不变：`prompt.txt`、`stream.log`、`sdk-stream.log`、`result.json`。
- 测试与回归：
  - 新增 `internal/services/subagent/service_test.go`，覆盖 submit 幂等与 run 成功/失败/取消关键路径。

## W04 回写（T14 Channel 入站持久化单路径化）

- 状态：`T14` 已完成（2026-02-26），Service/Gateway/Gatewaysend 入站持久化已收敛到统一路径。
- 交付物：
  - 新增 `internal/services/channel/inbound_persistence.go`，统一 `EnsureBindingTx` / `EnsureConversationTx` / `PersistInboundMessageTx` / `PersistTurnResultTx`。
  - 新增 `TurnResultRecord`（`dalek.channel_turn_result.v2`）与 `decodeTurnResult`，替换 service/gateway 双 schema 双解码路径。
  - `internal/services/channel/service.go` 的 `ProcessInbound` 与 `runTurnJob` 已切换到共享持久化组件，删除本地 `ensureBindingTx`/`ensureConversationTx` 与旧 result 写入分支。
  - `internal/services/channel/gateway_runtime.go` 的 `persistInboundAccepted` / `persistTurnResult` 已切换到共享持久化组件，删除 `ensureGatewayBindingTx`/`ensureGatewayConversationTx` 与 `gatewayTurnResult`。
  - `internal/services/gatewaysend/send.go` 删除本地 `ensureConversationTx` 副本，改为复用统一入口（通过 `internal/services/channel/inbounddb` 共享实现）。
  - 新增 `internal/services/channel/inbound_persistence_test.go` 覆盖 binding 超集行为、入站字段统一与 dedup、turn 成功/失败关键分支。
- 回归结果：
  - `go test ./internal/services/channel/...` 通过
  - `go test ./internal/services/gatewaysend/...` 通过
  - `go test ./...` 全量通过
  - 已通过 `go test ./internal/services/subagent/...`、`go test ./internal/app/...`、`go test ./internal/services/daemon/...` 与 `go test ./...` 全量回归。
- 下游约束更新：
  - app 层禁止回流 subagent 底层 I/O 编排；后续统一执行入口（AgentExec）应直接复用/包裹 `services/subagent`。

## W03 回写（T11 cmd_config 归位）

- 状态：`T11` 已完成（2026-02-26），cmd 配置业务与状态推导已按约束下沉。
- 交付物：
  - 新增 `internal/config/config.go`，承载配置键元数据、`ResolveValue/SetValue`、effective merge、JSON presence 检测、project config 加载能力。
  - `cmd/dalek/cmd_config.go` 已移除 `internal/repo` 直接依赖，仅保留参数解析/输出与 `internal/config` 调用；文件体量从 `829` 行降至 `488` 行。
  - 新增 `internal/app/task_run_status.go`，集中 `DeriveRunStatus` 与 `TaskStatusUpdatedAt`；`cmd/dalek/cmd_task.go` 与 `internal/app/daemon_runtime.go` 已统一复用。
  - `cmd/dalek/cmd_gateway_log.go` 已改为走 `app.ResolveHomeDir`，不再分散直读 `DALEK_HOME`。
- 测试与回归：
  - 新增 `internal/config/config_test.go`（配置路径/写入/presence/merge 覆盖）。
  - 新增 `internal/app/task_run_status_test.go`（run_status 分支与更新时间聚合覆盖）。
  - 已通过：`go test ./cmd/dalek -run TestCLI_E2E_ConfigCommands`、`go test ./...`、`go vet ./...`、`go build ./...`。
- 下游约束更新：
  - 后续涉及 CLI 配置读写的改动必须通过 `internal/config` 入口，不得回流到 cmd 层承载配置业务。
  - task run 状态推导必须复用 `app.DeriveRunStatus`，禁止在 cmd/daemon 侧新增并行分支逻辑。

## W04 回写（T31 GatewaySend 分层拆分）

- 状态：`T31` 已完成（2026-02-26），`internal/services/gatewaysend/send.go` 已拆分为 handler/service/repository/types/helpers 分层结构。
- 交付物：
  - 新增 `internal/services/gatewaysend/{types.go,helpers.go,repository.go,service.go,handler.go}`，原 `send.go` 删除。
  - 引入 `Repository` 接口与 `GormRepository` 实现，发送链路数据访问集中收敛，避免 handler/service 直接操作裸 `*gorm.DB`。
  - `Service` 层统一编排 dedup + outbox 状态机（`pending -> sending -> sent/failed`），并保留 `SendProjectText*` 兼容入口。
  - handler 层改为 `NewHandler(*Service, HandlerConfig)`，daemon 与 PM 调用方已适配为 service 注入模式。
  - 新增关键测试：`service_test.go`（业务编排）与 `repository_test.go`（数据层事务与状态流转）；原 handler/集成测试保持通过。
- 回归结果：
  - `go test ./internal/services/gatewaysend/...` 通过
  - `go test ./internal/services/daemon/... -run Send -count=1` 通过
  - `go test ./internal/services/pm/... -run GatewayStatusNotifier -count=1` 通过
  - `go test ./...` 全量通过
- 下游约束更新：
  - `T35` 必须复用 `gatewaysend.Service` 入口做 PM 通知解耦，禁止回退到包内直接裸 DB 访问。

## W04 回写（T29 Daemon ExecutionHost 拆分）

- 状态：`T29` 已完成（2026-02-26），`execution_host.go` 已完成类型独立与并发逻辑分文件。
- 交付物：
  - 新增 `internal/services/daemon/execution_host_types.go`，迁移 interfaces/DTO/错误类型/运行时 handle 类型。
  - 新增 `internal/services/daemon/execution_host_runner.go`，迁移 execute* goroutine、slot 控制、probe 与异步通知链路。
  - 新增 `internal/services/daemon/execution_host_index.go`，迁移索引系统、请求幂等查询、handle 生命周期管理与扫描回退查询。
  - 新增 `internal/services/daemon/execution_host_component.go` 与 `execution_host_note.go`，保持组件接口与 Note API 清晰分层。
  - `internal/services/daemon/execution_host.go` 已瘦身至 `494` 行，仅保留核心结构、生命周期与主提交流程。
- 回归结果：
  - `go test ./internal/services/daemon/... -count=1` 通过
  - `go build ./...` 通过
  - `go vet ./internal/services/daemon/...` 通过
- 依赖与下游影响：
  - `T30` 可直接在拆分后的 `runner/index/types` 边界上继续推进，不再需要先做文件级切分。
  - daemon recover/logger 与 `contracts/core project` 注入方式保持不变，外部 API 行为无回退。

## W05 回写（T30 Daemon 清洗边界）

- 状态：`T30` 已完成（2026-02-26）。
- 交付物：
  - 在 `execution_host_{types,runner,index,component,note}.go` 既有边界内完成改造，未回退为巨文件耦合实现。
  - `execution_host_types.go` 新增 `WorkerRunResult.RunID`；`internal/app/daemon_runtime.go` 已透传 `DirectDispatchResult.LastRunID`。
  - `execution_host_runner.go` 删除 `probeWorkerRunID` 忙等轮询与 `80ms sleep` 轮询链路，worker run 改为直接消费 `DirectDispatchWorker` 返回的 `RunID/WorkerID`。
  - `api_internal_ws.go` 的 WS 出站帧改为保留内容字段原文（`Text/Stream/JobError` 不再 `TrimSpace`）；`channel/ws/helpers.go::ParseInboundText` 改为仅用 `TrimSpace` 判空并返回原始文本。
  - `execution_host.go`、`execution_host_index.go`、`api_internal.go` 清理中下游冗余 `TrimSpace`，保留入口层清洗边界。
- 测试与验证：
  - `go test ./internal/services/daemon/...` 通过
  - `go test ./...` 通过
  - `go build ./...` 通过
  - `go vet ./...` 通过
- 依赖变化：
  - `T29 -> T30` 依赖已闭环，`T30` 不再阻塞 W05 其他票推进。

## W05 回写（T15 Channel turn 执行治理）

- 状态：`T15` 已完成（2026-02-26），`runTurnJob` 与 `pending_actions` 已完成职责拆分，执行链路行为保持等价。
- 交付物：
  - 新增 `internal/services/channel/turn_executor.go`，引入 `turnContext` 并拆分 `claimAndLoadTurnContext` / `executeTurnAgent` / `processTurnResponse` / `finalizeTurn`，`runTurnJob` 收敛为编排入口。
  - `pending_actions.go` 已拆分为 `pending_action_store.go`（CRUD + 类型 + 编解码）与 `pending_action_workflow.go`（审批工作流）。
  - `renderActionExecutionSummary` 与 `actionExecuteResult` 已迁移到 `action_executor.go`，执行辅助不再与 pending_action 存储逻辑混置。
  - 入站持久化与 turn 结果落盘继续复用 `internal/services/channel/inbound_persistence.go` 与 `PersistTurnResultTx`，未新增并行持久化路径或重复 schema/decoder。
- 回归结果：
  - `go test ./internal/services/channel/...` 通过
  - `go test ./...` 通过
  - `go build ./...` 通过
  - `go vet ./...` 通过
- 依赖与下游影响：
  - `T14 -> T15` 依赖闭环已完成，`T16`（channel service 生命周期治理）可直接基于新的 turn 执行边界继续推进。
  - 后续涉及 pending action 变更，必须分别落在 store/workflow 边界，禁止回退为单文件混合职责实现。

## 每批执行建议

1. 每批结束后，先清单回写：更新 `CRITICAL/HIGH/MEDIUM_SELECTED/LOW_SELECTED` 中对应 ID 状态。
2. 每批结束后，执行一次依赖回归：至少覆盖本批 tickets 涉及的命令路径和核心服务单测。
3. 若某票超出 2000 行，立即在同批内拆分子票，不要把过大改动压到下一批。
