# CRITICAL 架构债务（必须清零）

> 来源：`docs/arch_debt/source/ARCH_AUDIT_REPORT_2026-02-26.md`
> 生成日期：2026-02-26
> 条目数：10

说明：源报告首页统计与逐条清单存在不一致，以本目录 `issues.tsv`/`issues.json` 的提取结果为准。
- 源报告首页：CRITICAL=11, HIGH=42
- 提取结果：CRITICAL=10, HIGH=44

## cmd/dalek（CLI 入口层）

- `CMD-C1` `cmd/dalek/cmd_gateway_feishu.go` (1413行)：**完整飞书 IM 服务实现放在 CLI 层**。包含 HTTP 客户端（含 token 管理/TTL 缓存）、卡片构建器(200+行 JSON)、Markdown 正规化、Webhook handler(400+行)、Slash 命令处理、进度流管道。无法被 daemon 复用，测试需编译整个 cmd 包。应迁移到 `services/channel/feishu/` 或 `adapters/feishu/`。
- `CMD-C2` `cmd/dalek/*_test.go` (5个测试文件)：**测试文件绕过 Facade 直接依赖 services/store**。`cmd_gateway_feishu_test.go` 直接 import `services/channel`+`store`；`e2e_cli_test.go` 直接 import `services/channel`+`store`；`cmd_gateway_ws_e2e_test.go` import `repo`+`store`；`e2e_cli_daemon_task_cancel_test.go` import `repo`+`store`；`cmd_gateway_ws_test.go` import `store`。架构约束测试已对非测试文件生效但测试文件被跳过。

## internal/agent/run

- `RN-C1` `process.go` 行14-15 + `sdk.go` 行14-15 + `tmux.go` 行13-14：**run 包反向依赖 services/core 和 store 破坏 agent 层独立性**。ProcessConfig 包含 `store.TaskOwnerType`；三个 executor 直接使用 `store.TaskPending/Running/Succeeded/Failed/Canceled` 状态常量；通过 `core.TaskRuntime` 接口管理任务生命周期。agent 层不应了解 services 层概念。应定义 `RunLifecycleHook` 回调接口替代。

## internal/app（Facade 层）

- `APP-C1` `internal/app/note.go` (1150行)：**完整 notebook shaping 业务实现放在 Facade 层**。YAML front matter 解析、note 去重（normalized hash + dedup key）、shaping 状态机（open→shaping→shaped）、shaped item CRUD、inbox upsert、note approval/rejection 工作流、遗留状态兼容层。违反 project.go 头部注释声明的"不承载业务流程实现"约束。应创建 `services/notebook/` 包。
- `APP-C2` `internal/app/project_subagent.go` (526行)：**完整 subagent 编排实现放在 Facade 层**。直接调用 `sdkrunner.Run()` 执行 agent、管理文件 I/O（prompt.txt/stream.log/result.json）、Provider 解析和 agent 配置组装、完整状态机和事件追踪。直接 import `agent/provider` 和 `agent/sdkrunner`，app 层与底层 agent 实现直接耦合。应创建 `services/subagent/`。
- `APP-C3` `internal/app/daemon_public_feishu.go` (2087行)：**完整飞书 IM 适配层放在 Facade 层**。webhook handler、消息解析、卡片构建、消息发送、用户名缓存、事件去重、流式响应中继、Markdown 截断。占 app 包代码量的 ~15%。应迁移到 `services/channel/feishu/`。

## internal/services/pm（PM 服务）

- `PM-C1` `dispatch_agent_exec.go` 行10-11,63-151 + `dispatch_worker_sdk.go` 行9-10,35-116：**pm 直接 import agent/provider + agent/run 跨越架构层级**。两个文件各自手动构造 `provider.AgentConfig → provider.NewFromConfig → run.NewSDKExecutor/run.NewProcessExecutor`。pm 成为 agent 层深度耦合消费者，需理解 SDK/Process 两种模式、组装 20+ 字段的 config。应抽取 `AgentLauncher` 接口由 app 层实现注入。
- `PM-C2` `bootstrap.go` 行39-51 + `dispatch_agent_exec.go` 行85-101 + `dispatch_worker_sdk.go` 行75-88：**环境变量 map 三处重复构造且已出现不一致**。10+ 个 DALEK_* 环境变量在三处各自手写 `map[string]string{...}`。已有微妙差异：dispatch_agent_exec 多出 DALEK_DISPATCH_REQUEST_ID/ENTRY_PROMPT/PROMPT_TEMPLATE，dispatch_worker_sdk 多出 DALEK_DISPATCH_DEPTH，bootstrap 缺少 DALEK_DISPATCH_DEPTH。应抽取 `buildBaseEnv()` 公共方法。

## 跨 agent 层整体

- `AGT-C2` （跨模块/未注明文件行）：**channel 和 app 绕过 run 编排层直接使用 sdkrunner，agent 层无统一入口**。pm 用 provider+run（CLI/tmux 模式）；channel 用 auditlog+eventlog+eventrender+sdkrunner（直接 SDK，无 TaskRuntime 跟踪）；app 用 provider+sdkrunner。三条路径三种组装方式行为不一致。run 实质是"pm 专用编排"而非公共 API。应新增统一入口包。

## 跨 worker/ticket/task 关系

- `XWT-R1` （跨模块/未注明文件行）：**worker 完全绕过 ticket service 直接操作 ticket DB**。start.go:155 `db.First(&t, ticketID)`；cleanup.go:71 `p.DB.First(&t, ticketID)`；views.go:125-131 直接查 ticket 列表。worker 没有任何对 ticket service 的依赖。ticket service 被架空。
