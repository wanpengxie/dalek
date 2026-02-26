# 架构债务执行 DAG（2026-02-26）

> 范围：`docs/arch_debt/TICKETS.md` 的 `T01~T39`
> 调度约束：每次执行 1~4 个 ticket；严格按依赖优先级推进

## 执行规则

1. 基础组件与状态/迁移基础设施优先（`T38/T39/T24`），避免后续返工。
2. 同一批次内不放置“硬依赖”关系，保证可并行。
3. 同一子域（PM/Channel/Store）尽量连续推进，减少上下文切换成本。

## 当前执行状态（Kernel 回写）

- 更新时间：`2026-02-26`
- 已完成批次：`W01` `W02` `W03`
- 当前批次：`W04`（`in_progress`）
- W01 完成票：`T06(13)` `T24(31)` `T38(45)` `T39(46)`（均已 merge/archived）
- W02 完成票：`T19(26)` `T21(28)` `T03(10)` `T10(17)`（均已 merge/archived）
- W03 完成票：`T01(8)` `T02(9)` `T04(11)` `T11(18)`（均已 merge/archived）
- W04 已完成票：`T14(21)`（入站持久化单路径化）
- W04 在途票：`T22(29)` `T31(38)` `T29(36)`
- 下游强制约束：
  - 状态机相关改造必须复用 `internal/fsm/*`（`T20/T27/T34` 不得再写隐式转换）。
  - 迁移相关改造必须复用 migration runner + `schema_migrations`（`T25` 直接沿用）。
  - PM 配置相关改造必须复用 `buildBaseEnv()` + `constants.go` + 外置 prompt 模板。
  - 日志与 daemon handler 必须走 `*slog.Logger` 注入链路 + recover middleware。
  - Feishu 改造必须复用 `internal/services/channel/feishu/*`（`T04` 禁止维护第二套实现）。
  - gateway/ws 改造必须复用 `internal/services/channel/ws/*` 与 `internal/gateway/client/*`。
  - 跨层枚举/状态类型统一引用 `internal/contracts/*`（禁止回流到 `store` 常量）。

## 关键依赖边（DAG）

- `T03 -> T04`
- `T07 -> T08 -> T09`
- `T14 -> T15 -> T16 -> T17`
- `T21 -> T22 -> T26 -> T27`
- `T21 -> T23 -> T28`
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

每次启动 `Wxx` 前，dispatch prompt 必须包含以下 7 项，缺一不可：

1. `Wave` 与本轮 tickets（1-4 个）。
2. 前置依赖校验（列出本轮依赖的上游 tickets，并确认已完成）。
3. 当前“架构状态增量”（已完成 waves 产出的新组件/新边界）。
4. 本轮“必须复用/必须遵循”的组件与边界（禁止绕过）。
5. 本轮验收口径（功能等价、架构约束、测试要求）。
6. 本轮结束回写动作（更新 `EXECUTION_DAG.md` 与 `.dalek/AGENTS.md` 的 `<current_phase>`）。
7. 阻塞分叉规则（若上游未完成，如何调整 DAG 与改派）。

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

## 每批执行建议

1. 每批结束后，先清单回写：更新 `CRITICAL/HIGH/MEDIUM_SELECTED/LOW_SELECTED` 中对应 ID 状态。
2. 每批结束后，执行一次依赖回归：至少覆盖本批 tickets 涉及的命令路径和核心服务单测。
3. 若某票超出 2000 行，立即在同批内拆分子票，不要把过大改动压到下一批。
