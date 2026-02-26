# 架构债务 Ticket 拆分（每票 500-2000 行）

> 来源：`docs/arch_debt/source/ARCH_AUDIT_REPORT_2026-02-26.md` + `docs/arch_debt/issues.tsv`
> 目标：CRITICAL/HIGH 全量清零；MEDIUM/LOW 选重要项一并处理
> 估算：约 39 个 ticket（以“每票 500-2000 行”口径归并）

说明：

1. 每个 ticket 都必须把“覆盖 ID”清零（对应的问题从 `CRITICAL.md/HIGH.md` 删除或标记为已解决的方式由后续流程确定）。
2. 大型治理（例如 `ST-H1` 类型归位）拆成多个阶段 ticket，避免单票超 2000 行。
3. Ticket 标题建议保留覆盖 ID，方便回溯审计与复核。

---

## Tier 1：重构前/中必须先引入的基础组件

这类 ticket 的目标是“搭地基”，让后续迁移/拆包/归位时直接使用新组件，避免重构一半又回头补基础设施。

## T38 结构化日志（slog）+ 注入 Logger + daemon HTTP recover

目标改动量：~1200-2000 行  
覆盖 ID：`CH-M4` `GS-L2` `SR-L2` `CMD-L3`  
范围：引入 Go 1.21+ `log/slog` 作为统一日志；通过依赖注入传递 `*slog.Logger`（服务层可在测试中替换 handler/级别）；逐步替换 services 层的 `log.Printf`；补齐 daemon HTTP 的 panic recover 中间件（panic 不再击垮 daemon 进程，且用 slog 记录）。  
验收：services 层不再裸 `log.Printf`；可按 ticket/worker/request 维度带字段；测试可控制日志输出；daemon handler panic 不再 crash 进程。

## T39 通用状态机（transition table）基础组件

目标改动量：~600-1200 行  
覆盖 ID：`TS-M1`  
范围：新增通用 FSM/transition table（`ValidTransitions`/`CanTransition` 纯函数或泛型 `FSM[S comparable]`）；为 Ticket/Worker/PMDispatchJob/TaskRun 至少落 1 份“权威转换表 + 单测”，作为 T20/T27 的迁移目标（避免转换规则散落在 SQL WHERE + if）。  
验收：合法转换路径一眼可读；状态机本身可单测；T20/T27 不再需要自建一套“隐式规则”。

## P0：优先清零 CRITICAL（并同步消灭相关 HIGH）

## T01 Notebook 归位：Facade → services/notebook

目标改动量：~1200-1800 行  
覆盖 ID：`APP-C1`  
范围：将 `internal/app/note.go` 的 shaping/审批/兼容逻辑迁移到 `internal/services/notebook/`，app 仅保留 Facade API。  
验收：原有功能等价；核心流程有单测或回归路径；app 层不再承载 notebook 流程实现。

## T02 Subagent 归位：Facade → services/subagent（并准备统一执行入口）

目标改动量：~900-1800 行  
覆盖 ID：`APP-C2`  
范围：将 `internal/app/project_subagent.go` 的编排与 I/O 下沉到 `internal/services/subagent/`，app 只做参数转发与权限边界。  
验收：subagent 运行产物（prompt/stream/result）仍可追踪；行为与旧实现一致；减少 app 对底层 agent 实现的直接依赖（为后续 `AGT-C2` 铺路）。

## T03 Feishu 归位（1/2）：提取 feishu 适配服务并改造 app

目标改动量：~1500-2000 行  
覆盖 ID：`APP-C3`  
范围：抽出 `services/channel/feishu/`（或 `adapters/feishu/`）承载 webhook/卡片/发送/去重/缓存；`internal/app/daemon_public_feishu.go` 改为调用该服务。  
验收：飞书收发与卡片行为不变；app 包代码量下降；feishu 逻辑可被 daemon/CLI 复用。

## T04 Feishu 归位（2/2）：cmd gateway feishu 复用共享实现

目标改动量：~800-1500 行  
覆盖 ID：`CMD-C1`  
范围：`cmd/dalek/cmd_gateway_feishu.go` 改为复用 T03 的共享实现，仅保留 CLI 参数/路由/启动逻辑。  
验收：CLI gateway 功能不变；feishu 业务逻辑不再在 cmd 层重复实现。

## T05 cmd 测试边界修复：测试不再绕过 Facade

目标改动量：~800-1500 行  
覆盖 ID：`CMD-C2`  
范围：重构 `cmd/dalek/*_test.go`（列举的 5 个文件）让测试通过 app/服务入口而非直连 `store/services`；补充/扩展架构约束测试覆盖测试文件。  
验收：测试仍覆盖原场景；cmd 层测试不再直接依赖 `internal/store`/`internal/services/*`（或有明确的“允许清单”并受约束测试保护）。

## T06 PM 配置卫生：env builder + 时间常量集中 + prompt 外置

目标改动量：~900-1600 行  
覆盖 ID：`PM-C2` `PM-H1` `PM-H2`  
范围：提取 `buildBaseEnv()`；把分散的 timeout/interval 常量集中到 `constants.go`；把 dispatch prompt 模板外置（`go:embed`/文件模板）。  
验收：三处 env map 完全一致；常量单点维护；prompt 演进不需要改 Go 代码主体。

## T07 服务层 AgentExec（1/3）：迁移 run 到 services 并改造 PM 使用

目标改动量：~1500-2000 行  
覆盖 ID：`PM-C1` `RN-C1`  
范围：将 “带 TaskRuntime/状态落盘的执行编排” 从 `internal/agent/run` 迁到 `internal/services/agentexec/`（或等价命名），PM 改用该服务层入口。  
验收：PM 不再直接依赖 `agent/provider + agent/run`；`agent/run` 不再反向依赖 services/core/store（通过迁层或等价结构修正达成）。

## T08 AgentExec（2/3）：channel/app 统一执行入口（消除绕过）

目标改动量：~1000-1800 行  
覆盖 ID：`AGT-C2`  
范围：channel 与 app 统一通过 AgentExec/统一入口执行（无论 SDK/CLI/tmux），收敛组装逻辑与行为差异。  
验收：三条执行路径统一为单一入口；TaskRuntime/事件追踪行为一致且可测试。

## T09 AgentExec（3/3）：生命周期去重 + config 拆分 + Wait 可取消

目标改动量：~1000-1800 行  
覆盖 ID：`RN-H1` `RN-H2` `RN-H3`  
范围：抽出生命周期管理器去掉三 executor 重复；拆分臃肿 config；给 Wait 增加 ctx/超时/cancel 支持并补测试。  
验收：重复代码显著减少；Wait 不再无取消轮询；核心执行链路回归通过。

---

## P1：清零 HIGH（按主题分组，仍保持每票 500-2000 行）

## T10 Gateway WS 归位：WS server 下沉 + WS client 抽包

目标改动量：~1500-2000 行  
覆盖 ID：`CMD-H1` `CMD-H4`  
范围：把 ws server 逻辑从 cmd 下沉到 `services/channel/ws/`；把 daemon WS 客户端协议抽到 `internal/gateway/client/`。  
验收：CLI 行为不变；cmd 层只剩启动/参数；协议实现可被复用与测试。

## T11 cmd_config 归位：配置逻辑下沉 + import 约束补齐 + CLI 小修

目标改动量：~1500-2000 行  
覆盖 ID：`CMD-H2` `CMD-H3` `CMD-H5` `CMD-H6` `CMD-M3`  
范围：把配置解析/合并/写入逻辑迁到 `internal/config/` 或 app 层；补齐 cmd→repo 的 import 约束；顺手统一 usage/DALEK_HOME 解析；把 cmd_task 的业务状态推导下沉到 app。  
验收：cmd 不再直接 import repo 做配置业务；命令使用体验不回退；状态推导结果与旧一致。

## T12 App Facade 封装修复：消除透明类型别名与 service 直出

目标改动量：~900-1800 行  
覆盖 ID：`APP-H1` `APP-H2` `APP-H4`  
范围：收敛 `facade_types.go` 的透明别名策略；`gateway_facade.go` 不再暴露 `*channelsvc.Service`；清理不应出现在 app 的 tmux 工具函数。  
验收：Facade 真正隔离内部实现；上层无法通过 app 直接拿到 services/channel 的内部类型。

## T13 App DaemonManager 收敛：恢复/对账逻辑下沉到 service（并修补 DB 直访）

目标改动量：~1200-2000 行  
覆盖 ID：`APP-H3` `APP-H5`  
范围：将 `daemon_manager_component.go` 中直接 DB 事务与恢复逻辑下沉到 pm/task/worker service；修复 `action_executor.go` 的 DB 直访遗漏。  
验收：同类逻辑要么全走 service，要么全走 db（不混用）；恢复流程可复用并可测试。

## T14 Channel 入站持久化单路径化（消除分裂）

目标改动量：~1500-2000 行  
覆盖 ID：`CH-H1`  
范围：抽取统一的 inboundPersistence 组件，Service 与 Gateway 复用同一持久化流程。  
验收：两条路径不再复制粘贴；字段/行为不再分叉；回归测试覆盖。

## T15 Channel turn 执行治理：runTurnJob 拆分 + pending_actions 分层

目标改动量：~1500-2000 行  
覆盖 ID：`CH-H2` `CH-H3`  
范围：把 runTurnJob 拆成若干子步骤；把 pending_actions 拆成 CRUD/审批/执行 三层并明确依赖。  
验收：核心路径更短可读；职责边界清晰；行为与旧一致。

## T16 Channel action_executor 去耦：移除 channel→store 直连

目标改动量：~800-1500 行  
覆盖 ID：`CH-H5`  
范围：删除或迁移 channel 的 action_executor（list/create/detail 等），改为走 app/服务层 ActionHandler 注入。  
验收：channel 不再直接 import store 并操作 DB；action 执行路径单一。

## T17 Channel 清洗边界：TrimSpace 降噪 + cancel 语义修复 + 并发风险消除

目标改动量：~1000-2000 行  
覆盖 ID：`CH-H4` `CH-M3` `CH-M5` `CH-L2`  
范围：定义“入口清洗、内部信任”的边界；修复失败路径 `context.Background()`；并发标志改为明确语义（atomic/通道）；SetActionHandler 并发安全。  
验收：TrimSpace 数量显著下降；失败路径遵循上层取消；无 data race。

## T18 Provider/默认值/客户端归位：Provider 角色清晰化 + openai_compat 迁层

目标改动量：~1200-2000 行  
覆盖 ID：`PV-H1` `PV-M1` `IF-H1` `APP-H6` `RP-M2` `APP-M3`  
范围：拆分 AgentConfig（配置）与 Provider（执行）；明确 Provider 只服务 CLI 或拆接口；把 `infra/openai_compat.go` 迁到 agent/provider 体系；统一模型默认值与 provider 白名单来源。  
验收：配置读取不再被执行依赖绑架；OpenAI 兼容客户端不在 infra；默认值/白名单单点维护。

## T19 Core.Project 拆分：按需注入依赖（消灭 God Object）

目标改动量：~1500-2000 行  
覆盖 ID：`CR-H1` `CR-M1` `APP-M1`  
范围：将 core.Project 拆为 Identity/Paths/Runtime 等小结构；统一 Project 构造；修复 app 层构造重复。  
验收：service 只接收用到的依赖；构造有校验；相关测试构造复杂度下降。

## T20 TaskRuntime 归并：消除 core↔task 镜像 + 去重 helper + 显式状态机

目标改动量：~1200-2000 行  
覆盖 ID：`TS-H1` `TS-H2` `TS-M1` `TS-L1` `CR-M2`  
范围：统一 Input struct 定义；删除机械 adapter；NextActionToSemanticPhase 去重；把“合法状态转换”集中为纯函数/表；唯一约束冲突检测优先用 `errors.Is(err, gorm.ErrDuplicatedKey)`。  
验收：新增字段不再三处联动；状态机规则可读可测；dup key 检测更稳。

## T21 类型归位（1/3）：引入 core/model 或扩展 contracts，统一枚举

目标改动量：~1500-2000 行  
覆盖 ID：`CT-H1` `ST-M2`  
范围：把跨层枚举与核心领域类型承载迁出 store（至少包含 ChannelType 等），明确 contracts/core-model 的边界。  
验收：跨层重复枚举消失；上层为使用类型不必 import store。

## T22 类型归位（2/3）：迁移 Ticket 领域类型出 store 并收敛 facade_types

目标改动量：~1500-2000 行  
覆盖 ID：`ST-H1` `APP-H1`  
范围：先迁移 Ticket/WorkflowStatus/WorkflowEvent 等高传播类型到 core/model；同步把 app 的透明别名替换为独立类型或引用 core/model。  
验收：store import 数量明显下降；store 变更不再穿透 Facade。

## T23 类型归位（3/3）：迁移 Worker/TaskRun/Channel 领域类型出 store（收尾）

目标改动量：~1500-2000 行  
覆盖 ID：`ST-H1` `CT-H1`  
范围：完成剩余高传播类型迁移（Worker/TaskRun/SubagentRun/Channel* 视情况分批）；store 只保留 ORM 映射与 DB 操作。  
验收：store 不再是“类型中心”；contracts/core-model 成为跨层类型权威来源。

## T24 Store 迁移版本化：引入 schema_migrations

目标改动量：~1000-2000 行  
覆盖 ID：`ST-H2`  
范围：为 sqlite 引入 migration 版本号表；把当前 AutoMigrate 中的“硬切迁移”按版本组织（至少支持幂等与升级路径）。  
验收：启动不再反复尝试 DROP；迁移可追踪、可审计、可回归。

## T25 Store JSON 字段类型化：对高频字段加结构体与 Serializer

目标改动量：~800-1500 行  
覆盖 ID：`ST-L2`  
范围：为高频 JSON 字段引入 Go 结构体与 GORM serializer（先做 3-5 个最关键字段），减少到处手写 JSON 解析。  
验收：读写更类型安全；关键路径错误更早暴露；迁移成本可控。

## T26 Ticket service 修复与补齐：Create bug + GetByID，并让 worker 不再直连 ticket 表

目标改动量：~800-1500 行  
覆盖 ID：`TK-M1` `TK-L1` `XWT-R1`  
范围：修复 `ticket.Service.Create()` 永远失败；补齐 `GetByID` 等；worker 的 ticket 读取改为走 ticket service。  
验收：worker 不再直接 `db.First(&store.Ticket{})`；ticket service 成为读入口。

## T27 Ticket workflow 权威归位：集中状态机与生命周期

目标改动量：~1500-2000 行  
覆盖 ID：`TK-H1` `XWT-R2`  
范围：把 workflow 转换规则集中定义（CanTransition/Transition）；收敛 archive/workflow 入口；明确 ticket/worker/pm/app 边界协议。  
验收：ticket 支持的操作与状态迁移有单一权威位置；碎片化问题消失。

## T28 Ticket 视图查询归位：新增 query service，拆分 ListTicketViews 并移出 worker 包

目标改动量：~1200-2000 行  
覆盖 ID：`WK-H1` `WK-H2`  
范围：把 TicketView/capability/query 逻辑从 worker 包迁到 query service 或 ticket 领域；将巨函数拆成“数据抓取层 + 计算层”。  
验收：worker 不再承载 ticket 领域语义；view 计算可测试、可演进。

## T29 Daemon ExecutionHost 拆分：类型独立 + 逻辑分文件

目标改动量：~1500-2000 行  
覆盖 ID：`DM-H1` `DM-H2`  
范围：把 `execution_host.go` 拆为至少 3 文件；DTO types 迁到 `execution_host_types.go`；梳理职责边界。  
验收：文件规模下降；类型与并发逻辑不混杂；外部接口不回退。

## T30 Daemon 清洗边界：TrimSpace 降噪 + Text 不再 TrimSpace + 消除忙等轮询

目标改动量：~1000-1800 行  
覆盖 ID：`DM-H3` `DM-M3` `DM-M4`  
范围：建立 daemon 的输入清洗边界；WS 事件 Text 字段不再 TrimSpace；probeWorkerRunID 忙等改为接口返回值或事件驱动。  
验收：内容语义不被篡改；性能/可读性提升；减少无效 TrimSpace。

## T31 GatewaySend 分层拆分：send.go 分文件 + Service 化 + 可测试数据层

目标改动量：~1000-1800 行  
覆盖 ID：`GS-H1` `GS-M1` `GS-M2` `GS-M3`  
范围：将 `send.go` 拆分为 handler/service/persist/helpers；引入最小 repository 接口以便单测。  
验收：职责清晰；核心逻辑可测试；未来扩展非飞书 adapter 成本可控。

## T32 Logs 重命名与职责对齐：避免与“真正日志服务”命名冲突

目标改动量：~600-1200 行  
覆盖 ID：`LG-H1` `LG-M1` `LG-M2`  
范围：将 logs 包重命名为 preview/tmuxpreview（或等价），并把对 worker.Service 的依赖收口为接口。  
验收：包名语义一致；依赖更清晰；未来引入真正日志系统不冲突。

## T33 PM ManagerTick 分解：把 595 行巨函数拆成可测试单元

目标改动量：~800-1500 行  
覆盖 ID：`PM-H3`  
范围：将 `manager_tick.go` 拆分为多个子步骤（事件消费/worker 扫描/merge/调度等），减少嵌套分支，补齐关键单测或可回归路径。  
验收：tick 行为不回退；复杂度下降；关键决策点可测试。

## T34 PM dispatch_queue 分解：队列/状态机/查询解耦

目标改动量：~1000-1800 行  
覆盖 ID：`PM-H5`  
范围：把 dispatch_queue 的 promote/demote 等 workflow 推进移到 workflow 归口位置；拆分队列操作与查询/等待逻辑。  
验收：职责清晰；API 更小；核心调度路径回归通过。

## T35 PM 通知解耦：workflow_notify 迁出 pm（消除 gatewaysend 横向依赖）

目标改动量：~600-1200 行  
覆盖 ID：`PM-H4`  
范围：把 `GatewayStatusNotifier` 移出 pm（app 或独立 notifier 包）；pm 只保留 hook 接口与调用点。  
验收：pm 不再 import gatewaysend；通知实现可替换/可测试。

## T36 PM 可靠性与测试补齐：续租错误可观测 + context 泄露治理

目标改动量：~1200-2000 行  
覆盖 ID：`PM-M5` `PM-M6` `PM-L2`  
范围：续租 goroutine 不再吞错（可观测、可降级/可取消）；修复/替换 `newCancelOnlyContext` 的潜在泄露；为核心文件补齐单测（至少覆盖 worker_loop/worker_ready_wait 等关键路径）。  
验收：续租失败有日志/事件；取消语义正确；核心路径有测试护城河。

## T37 Sdkrunner 治理：拆分单文件 + settings embed + renderer 不丢 block

目标改动量：~1200-2000 行  
覆盖 ID：`SR-H1` `SR-H2` `SR-M1` `SR-M3` `ER-L2`  
范围：拆分 `sdkrunner/runner.go`；Claude settings JSON 改为 `go:embed` 或可覆盖配置；修复 eventrender 只渲染第一个 content block 的潜在丢事件问题。  
验收：sdkrunner 职责清晰、可演进；权限配置易审查；渲染不丢内容。
