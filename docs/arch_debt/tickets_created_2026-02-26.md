# 架构债务 Tickets 已创建（2026-02-26）

> project: `Users-xiewanpeng-agi-dalek`
> base branch: `arch-debt/base-20260226`
> created: 39
> skipped: 0

| Txx | ticket_id | title | start 命令 |
|---|---:|---|---|
| T01 | 8 | [ARCH-DEBT][2026-02-26][T01] Notebook 归位：Facade → services/notebook | `dalek ticket start --ticket 8 --base arch-debt/base-20260226` |
| T02 | 9 | [ARCH-DEBT][2026-02-26][T02] Subagent 归位：Facade → services/subagent（并准备统一执行入口） | `dalek ticket start --ticket 9 --base arch-debt/base-20260226` |
| T03 | 10 | [ARCH-DEBT][2026-02-26][T03] Feishu 归位（1/2）：提取 feishu 适配服务并改造 app | `dalek ticket start --ticket 10 --base arch-debt/base-20260226` |
| T04 | 11 | [ARCH-DEBT][2026-02-26][T04] Feishu 归位（2/2）：cmd gateway feishu 复用共享实现 | `dalek ticket start --ticket 11 --base arch-debt/base-20260226` |
| T05 | 12 | [ARCH-DEBT][2026-02-26][T05] cmd 测试边界修复：测试不再绕过 Facade | `dalek ticket start --ticket 12 --base arch-debt/base-20260226` |
| T06 | 13 | [ARCH-DEBT][2026-02-26][T06] PM 配置卫生：env builder + 时间常量集中 + prompt 外置 | `dalek ticket start --ticket 13 --base arch-debt/base-20260226` |
| T07 | 14 | [ARCH-DEBT][2026-02-26][T07] 服务层 AgentExec（1/3）：迁移 run 到 services 并改造 PM 使用 | `dalek ticket start --ticket 14 --base arch-debt/base-20260226` |
| T08 | 15 | [ARCH-DEBT][2026-02-26][T08] AgentExec（2/3）：channel/app 统一执行入口（消除绕过） | `dalek ticket start --ticket 15 --base arch-debt/base-20260226` |
| T09 | 16 | [ARCH-DEBT][2026-02-26][T09] AgentExec（3/3）：生命周期去重 + config 拆分 + Wait 可取消 | `dalek ticket start --ticket 16 --base arch-debt/base-20260226` |
| T10 | 17 | [ARCH-DEBT][2026-02-26][T10] Gateway WS 归位：WS server 下沉 + WS client 抽包 | `dalek ticket start --ticket 17 --base arch-debt/base-20260226` |
| T11 | 18 | [ARCH-DEBT][2026-02-26][T11] cmd_config 归位：配置逻辑下沉 + import 约束补齐 + CLI 小修 | `dalek ticket start --ticket 18 --base arch-debt/base-20260226` |
| T12 | 19 | [ARCH-DEBT][2026-02-26][T12] App Facade 封装修复：消除透明类型别名与 service 直出 | `dalek ticket start --ticket 19 --base arch-debt/base-20260226` |
| T13 | 20 | [ARCH-DEBT][2026-02-26][T13] App DaemonManager 收敛：恢复/对账逻辑下沉到 service（并修补 DB 直访） | `dalek ticket start --ticket 20 --base arch-debt/base-20260226` |
| T14 | 21 | [ARCH-DEBT][2026-02-26][T14] Channel 入站持久化单路径化（消除分裂） | `dalek ticket start --ticket 21 --base arch-debt/base-20260226` |
| T15 | 22 | [ARCH-DEBT][2026-02-26][T15] Channel turn 执行治理：runTurnJob 拆分 + pending_actions 分层 | `dalek ticket start --ticket 22 --base arch-debt/base-20260226` |
| T16 | 23 | [ARCH-DEBT][2026-02-26][T16] Channel action_executor 去耦：移除 channel→store 直连 | `dalek ticket start --ticket 23 --base arch-debt/base-20260226` |
| T17 | 24 | [ARCH-DEBT][2026-02-26][T17] Channel 清洗边界：TrimSpace 降噪 + cancel 语义修复 + 并发风险消除 | `dalek ticket start --ticket 24 --base arch-debt/base-20260226` |
| T18 | 25 | [ARCH-DEBT][2026-02-26][T18] Provider/默认值/客户端归位：Provider 角色清晰化 + openai_compat 迁层 | `dalek ticket start --ticket 25 --base arch-debt/base-20260226` |
| T19 | 26 | [ARCH-DEBT][2026-02-26][T19] Core.Project 拆分：按需注入依赖（消灭 God Object） | `dalek ticket start --ticket 26 --base arch-debt/base-20260226` |
| T20 | 27 | [ARCH-DEBT][2026-02-26][T20] TaskRuntime 归并：消除 core↔task 镜像 + 去重 helper + 显式状态机 | `dalek ticket start --ticket 27 --base arch-debt/base-20260226` |
| T21 | 28 | [ARCH-DEBT][2026-02-26][T21] 类型归位（1/3）：引入 core/model 或扩展 contracts，统一枚举 | `dalek ticket start --ticket 28 --base arch-debt/base-20260226` |
| T22 | 29 | [ARCH-DEBT][2026-02-26][T22] 类型归位（2/3）：迁移 Ticket 领域类型出 store 并收敛 facade_types | `dalek ticket start --ticket 29 --base arch-debt/base-20260226` |
| T23 | 30 | [ARCH-DEBT][2026-02-26][T23] 类型归位（3/3）：迁移 Worker/TaskRun/Channel 领域类型出 store（收尾） | `dalek ticket start --ticket 30 --base arch-debt/base-20260226` |
| T24 | 31 | [ARCH-DEBT][2026-02-26][T24] Store 迁移版本化：引入 schema_migrations | `dalek ticket start --ticket 31 --base arch-debt/base-20260226` |
| T25 | 32 | [ARCH-DEBT][2026-02-26][T25] Store JSON 字段类型化：对高频字段加结构体与 Serializer | `dalek ticket start --ticket 32 --base arch-debt/base-20260226` |
| T26 | 33 | [ARCH-DEBT][2026-02-26][T26] Ticket service 修复与补齐：Create bug + GetByID，并让 worker 不再直连 ticket 表 | `dalek ticket start --ticket 33 --base arch-debt/base-20260226` |
| T27 | 34 | [ARCH-DEBT][2026-02-26][T27] Ticket workflow 权威归位：集中状态机与生命周期 | `dalek ticket start --ticket 34 --base arch-debt/base-20260226` |
| T28 | 35 | [ARCH-DEBT][2026-02-26][T28] Ticket 视图查询归位：新增 query service，拆分 ListTicketViews 并移出 worker 包 | `dalek ticket start --ticket 35 --base arch-debt/base-20260226` |
| T29 | 36 | [ARCH-DEBT][2026-02-26][T29] Daemon ExecutionHost 拆分：类型独立 + 逻辑分文件 | `dalek ticket start --ticket 36 --base arch-debt/base-20260226` |
| T30 | 37 | [ARCH-DEBT][2026-02-26][T30] Daemon 清洗边界：TrimSpace 降噪 + Text 不再 TrimSpace + 消除忙等轮询 | `dalek ticket start --ticket 37 --base arch-debt/base-20260226` |
| T31 | 38 | [ARCH-DEBT][2026-02-26][T31] GatewaySend 分层拆分：send.go 分文件 + Service 化 + 可测试数据层 | `dalek ticket start --ticket 38 --base arch-debt/base-20260226` |
| T32 | 39 | [ARCH-DEBT][2026-02-26][T32] Logs 重命名与职责对齐：避免与“真正日志服务”命名冲突 | `dalek ticket start --ticket 39 --base arch-debt/base-20260226` |
| T33 | 40 | [ARCH-DEBT][2026-02-26][T33] PM ManagerTick 分解：把 595 行巨函数拆成可测试单元 | `dalek ticket start --ticket 40 --base arch-debt/base-20260226` |
| T34 | 41 | [ARCH-DEBT][2026-02-26][T34] PM dispatch_queue 分解：队列/状态机/查询解耦 | `dalek ticket start --ticket 41 --base arch-debt/base-20260226` |
| T35 | 42 | [ARCH-DEBT][2026-02-26][T35] PM 通知解耦：workflow_notify 迁出 pm（消除 gatewaysend 横向依赖） | `dalek ticket start --ticket 42 --base arch-debt/base-20260226` |
| T36 | 43 | [ARCH-DEBT][2026-02-26][T36] PM 可靠性与测试补齐：续租错误可观测 + context 泄露治理 | `dalek ticket start --ticket 43 --base arch-debt/base-20260226` |
| T37 | 44 | [ARCH-DEBT][2026-02-26][T37] Sdkrunner 治理：拆分单文件 + settings embed + renderer 不丢 block | `dalek ticket start --ticket 44 --base arch-debt/base-20260226` |
| T38 | 45 | [ARCH-DEBT][2026-02-26][T38] 结构化日志（slog）+ 注入 Logger + daemon HTTP recover | `dalek ticket start --ticket 45 --base arch-debt/base-20260226` |
| T39 | 46 | [ARCH-DEBT][2026-02-26][T39] 通用状态机（transition table）基础组件 | `dalek ticket start --ticket 46 --base arch-debt/base-20260226` |
