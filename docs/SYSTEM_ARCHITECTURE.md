# TmuxStudio 系统架构契约

> 文档类型：系统约定（Architecture Contract）  
> 生效日期：2026-02-21  
> 适用范围：本仓库 `ticket -> worker -> run -> project queues` 全链路  
> 约束级别：实现、TUI、CLI、调度器必须遵守

## 1. 目标与原则

本契约解决两个问题：
- 把“业务流转”与“资源/运行时”彻底解耦，避免状态语义混淆。
- 明确唯一写者与驱动边界，避免多模块并发改同一状态。

硬原则：
1. `ticket.workflow_status` 只表示业务工作流。
2. `worker.status` 只表示资源生命周期。
3. 运行时状态（run/health/phase/log）只作为观测与审计，不直接写 workflow。
4. TUI 门禁只读 capability，不按分区文本做硬判断。

## 2. 业务实体与关系

核心关系：
- `project (1) -> (N) ticket`
- `ticket (1) -> (1) worker`（当前实现是单 worker 记录复用）
- `worker (1) -> (N) task_run`
- `task_run (1) -> (N) task_runtime_sample / task_semantic_report / task_event`
- `ticket (1) -> (N) pm_dispatch_job`
- `ticket (1) -> (N) inbox_item`
- `ticket (1) -> (N) merge_item`

术语约定：
- 一次 `run` = 一次 agent 执行 attempt（可成功/失败/取消）。
- 一个 ticket 完成过程中，可以发生多次 run（串行重试或多轮派发）。

## 3. 状态主线与唯一写者

### 3.1 Ticket 业务工作流（权威）

字段：`ticket.workflow_status`  
唯一写者：`PM reducer`

状态集合：
- `backlog`
- `queued`
- `active`
- `blocked`
- `done`
- `archived`

语义：
- `backlog`：待办，未纳入执行队列。
- `queued`：已纳入执行队列，等待或准备执行。
- `active`：正在推进（业务层面在进行）。
- `blocked`：受阻，等待用户/审批/外部条件。
- `done`：开发工作完成，进入合并与交付流程。
- `archived`：业务闭环结束（通常在 merge 完成后）。

强约束：
1. `archived` 是终态，不允许自动回滚。
2. `done` 不因资源波动回滚。
3. `stop/kill` 只影响资源态，不自动改回 `backlog`。

推荐推进：
- `backlog -> queued`：start/入队。
- `queued -> active`：dispatch 成功。
- `active -> blocked`：语义 `next_action=wait_user`。
- `blocked -> active`：问题解除后继续执行。
- `active -> done`：语义 `next_action=done`。
- `done -> archived`：merge 完成（或人工确认关闭）。

### 3.2 Worker 资源生命周期（权威）

字段：`worker.status`  
唯一写者：`worker service/reconciler`

状态集合：
- `creating`
- `running`
- `stopped`
- `failed`

语义：
- `creating`：worktree/session/环境创建中。
- `running`：资源可运行、可注入、可 attach。
- `stopped`：资源已停（可再次 start）。
- `failed`：启动或运行失败。

驱动来源：
- 命令：start/stop/interrupt。
- 资源事件：session 丢失、启动失败、执行器异常。

## 4. 运行时状态（非 workflow）

### 4.1 Run 编排状态

字段：`task_runs.orchestration_state`

状态集合：
- `pending`
- `running`
- `succeeded`
- `failed`
- `canceled`

约束：
- 用于表示“一次 run 的调度生命周期”。
- 不直接映射为 ticket workflow。

### 4.2 运行健康状态

字段：`task_runtime_samples.runtime_health_state`

状态集合：
- `unknown`
- `alive`
- `idle`
- `busy`
- `stalled`
- `waiting_user`
- `dead`

约束：
- 仅用于实时健康监控与诊断。
- 不直接驱动 workflow，仅作为 reducer 输入证据。

### 4.3 语义阶段状态

字段：`task_semantic_reports.semantic_phase`

状态集合：
- `init`
- `planning`
- `implementing`
- `testing`
- `reviewing`
- `done`
- `blocked`

补充：
- `next_action` 是语义动作提示（常见值：`continue`、`wait_user`、`done`）。
- phase/next_action 面向人和 agent，可读优先，不是业务态主线。

### 4.4 实时日志约定

- 运行日志最终由 SDK executor 提供结构化流（实时、可解析、可回放）。
- 在 SDK executor 完整接入前，日志能力可退化，但不能反向污染 workflow 语义。

## 5. Project 队列状态（管理维度）

### 5.1 需求队列（Demand Queue）

- 来源：`ticket.workflow_status` 的派生视图。
- 主体：`backlog/queued`。
- 作用：决定“下一步做什么”。

### 5.2 任务队列（Task Queue）

字段：`pm_dispatch_jobs.status`

状态集合：
- `pending`
- `running`
- `succeeded`
- `failed`

约束：
- 同一 ticket 同时最多一个 active dispatch job（`pending/running`）。

### 5.3 通知队列（Notification Queue）

字段：`inbox_items.status`

状态集合：
- `open`
- `done`
- `snoozed`

配套维度：
- `severity`：`info/warn/blocker`
- `reason`：`needs_user/approval_required/question/incident`

### 5.4 Merge 队列（Merge Queue）

字段：`merge_items.status`

状态集合：
- `proposed`
- `checks_running`
- `ready`
- `approved`
- `merged`
- `blocked`

与 ticket 对齐约定：
- `ticket.done` 表示进入合并阶段，不等于已并入主分支。
- `merge.merged` 之后再推进 `ticket.archived`。
- 现阶段允许人工维护 merge 运行时（人工审批/人工合并）。

## 6. 操作语义契约（TUI/CLI 一致）

能力字段：
- `can_start`
- `can_dispatch`
- `can_attach`
- `can_stop`
- `can_archive`
- `reason`

门禁规则：
1. 动作可用性由 capability 决定。
2. TUI 分区仅用于展示，不是操作权限来源。
3. 后端仍做最终校验，前端不绕过状态约束。

## 7. 标准链路语义

标准业务链路：
- `backlog -> queued -> active -> done -> archived`

资源链路（可独立波动）：
- `creating -> running -> stopped/failed`

关键解释：
1. `active + worker.stopped` 是允许态，表示“业务仍在进行，但资源已停”。
2. stop 后不能 attach（资源已停）是预期行为。
3. stop 后是否可 archive 由 capability 与后端规则决定。

## 8. 非法模式（明确禁止）

以下行为视为架构违约：
1. worker/report/runtime 直接写 `ticket.workflow_status`。
2. 用 `runtime_health_state` 直接替代 workflow。
3. TUI 用分区文本硬编码门禁导致操作锁死。
4. 用手改 DB 代替正常命令流推进状态。

## 9. 变更流程

任何新增/修改状态，必须同时更新：
1. 本文档（系统约定）。
2. 对应状态写者代码与测试。
3. capability 计算与 TUI 展示。

如果代码与本文冲突，以本文为架构契约，代码必须修正。

## 10. CLI 交付规范（Agent-Friendly）

本节是 `cmd/tmuxstudio` 的补充硬约束，目标是确保 noun-verb CLI 在长期演进中保持可发现、可验证、可回归。

### 10.1 提交信息规范（对应改进项 1）

凡是修改以下任一范围，必须使用结构化提交说明（建议 Conventional Commits）：
- `cmd/tmuxstudio/**`
- `internal/repo/templates/**` 中的 CLI 注入或说明
- `README.md` 的 CLI 文档

提交标题要求：
- `feat(cli): ...`
- `fix(cli): ...`
- `refactor(cli): ...`
- `docs(cli): ...`

提交正文必须至少包含 4 段信息：
1. `What`：改了哪些命令/flag/help/error/json 行为。
2. `Why`：解决了什么问题（可发现性、一致性、可维护性等）。
3. `Migration`：旧命令映射与硬切提示（如适用）。
4. `Validation`：执行过的验证命令与结论。

最小验证基线（需写入提交正文）：
- `go build ./...`
- `go vet ./...`
- `go test ./...`

### 10.2 命令级 Examples 一致性检查（对应改进项 2）

命令级 help 是 agent 的第一发现入口，属于架构契约，不允许退化。

硬约束：
1. 每个命令级 help 必须包含 `Usage:`、`Flags:`、`Examples:` 三段。
2. `Examples:` 下至少 2 条示例（最简用法 + `-o json` 用法；不支持 json 的命令可给两个不同场景）。
3. `<noun> <verb> --help` 必须 `exit 0`，不得输出 `Error:`。

自动化检查：
1. 测试门禁：`go test ./cmd/tmuxstudio -run 'TestCLI_HelpShouldExitZero|TestCLI_OldTopLevelCommandFails|TestCLI_JSONErrorForGatewayChat' -count=1`
2. 文档门禁：`bash system_docs/check_cli_help_examples.sh`

当命令树新增/删除时，必须同步更新：
- `system_docs/check_cli_help_examples.sh` 的命令清单
- 对应命令文件中的 help 示例
