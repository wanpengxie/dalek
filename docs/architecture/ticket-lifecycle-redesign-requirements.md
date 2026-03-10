# Ticket 流程重构 — 需求、现状审计与设计方案

## 1. 文档定位

这份文档不替换旧方案文档，也不假设旧方案已经正确。

它要做三件事：

1. 把这次 `ticket` 流程重构到底要解决什么需求写清楚
2. 基于仓库当前实现，指出旧方案里哪些地方是幻觉、哪些地方和代码对不上
3. 在真实代码约束下，给出一份可落地的新设计方案

后续如果要继续写实现分解、迁移步骤、测试计划，都应以这份文档为基线。

## 2. 主需求

本次重构的主需求只有一个：

**重构 ticket 主流程，让 `ticket -> worker -> merge/archive` 这条链路更简单、更一致、可恢复，并且状态能够和真实事件事实对齐。**

这不是单纯删一个命令，也不是单纯补一个 scheduler。
这是对 ticket 生命周期模型本身的重构。

## 3. 这次重构必须同时解决的两个根问题

### 3.1 删除 dispatch

要解决的问题不是“弱化 dispatch”，而是：

- `dispatch` 不能再是 ticket 主链路中的生命周期阶段
- PM 不再承担“编译执行指令并注入给 worker”的职责
- PM 只负责：
  - 接收启动/恢复请求
  - 排队
  - 分配运行资源
  - 接收 worker 上报
  - 推进 merge / archive 收口
- worker 在获得运行资源后，自己读取 ticket/kernel/worktree 信息并开始执行

一句话：

**把 "PM 编译指令驱动 worker" 模型改成 "worker 自驱动" 模型，PM 只负责排队和资源分配。**

### 3.2 状态只是事件投影

要解决的另一个根问题是：

- 现在系统里有太多地方把 `workflow_status`、`integration_status`、worker 运行态直接当真相去写
- 这些状态之间缺少统一的事实源
- 一旦出现半成功、重复上报、daemon 重启、hook 丢失，就容易互相打架

这次重构要求：

- 不可变事件才是权威事实
- 状态只是事件的 projection / reducer 结果
- 事件必须 append-only
- 状态重算或重放后应得到同样结果

一句话：

**系统必须从“直接写状态”转成“记录事件，再由事件投影状态”。**

### 3.3 worker 失败、失联、额度不足都属于常态，不是边缘异常

这次重构还有一个必须写死的运行假设：

- worker 执行中异常退出，是常态运行条件
- worker / daemon / execution host 临时失联，是常态运行条件
- 配额不足导致 ticket 长时间停在 `queued`，是常态运行条件
- runtime 事实和 lifecycle 投影短时间不一致，是常态运行条件

因此新设计必须满足：

- 这些情况要有显式事实模型，而不是靠人工猜状态
- 这些情况要有正式收敛路径，而不是只留 repair 兜底
- 文档和 ticket 不得把它们写成“补充异常 case”

## 4. 目标主链路

目标主链路如下：

```text
create -> start -> queued -> [有配额] -> active -> report(done) -> done -> merged -> archived
                   ^                              |
                   |                              v
                   +--------- start <- blocked <- report(wait_user)
```

这里的 `merged` 是生命周期阶段，不要求把 `workflow_status` 新增成 `merged`。
在存储层，`merged` 仍然可以继续表示为：

- `workflow_status = done`
- `integration_status = merged`

也就是说：

- 这条链路是业务语义
- 底层存储仍然可以是 `workflow_status + integration_status` 的组合态

## 5. 仓库现状审计

这一节只讲代码事实，不讲理想方案。

### 5.1 `dispatch` 目前仍然是主链路核心，不是“已经删掉”

当前仓库里，`dispatch` 仍然深度存在于主链路：

| 位置 | 代码事实 | 结论 |
|---|---|---|
| [cmd/dalek/cmd_ticket.go](/home/xiewanpeng/tardis/dalek/cmd/dalek/cmd_ticket.go) | `ticket dispatch` CLI 子命令虽然提示“已移除”，但 `ticket start` 实际调用 daemon `SubmitDispatch` | CLI 表面移除了 dispatch，后端主链路没有移除 |
| [internal/services/pm/manager_tick.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/manager_tick.go) | `scheduleQueuedTickets` 对 `queued` 票先 `StartTicket`，再 `dispatchScheduledTicket` | manager tick 仍然把 dispatch 当主执行步骤 |
| [internal/services/pm/dispatch.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/dispatch.go) | `DispatchTicket / SubmitDispatchTicket / RunDispatchJob` 仍然完整存在 | dispatch 不是兼容壳，而是实心实现 |
| [internal/services/pm/dispatch_runner.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/dispatch_runner.go) | dispatch runner 负责 claim job、promote active、执行 PM dispatch agent、再执行 worker loop、再同步 task terminal | dispatch 目前承担了实际执行编排职责 |
| [internal/app/project_dispatch.go](/home/xiewanpeng/tardis/dalek/internal/app/project_dispatch.go) | project facade 仍然暴露 dispatch API | app 层仍以 dispatch 为正式能力 |
| [internal/app/daemon_runtime.go](/home/xiewanpeng/tardis/dalek/internal/app/daemon_runtime.go) | daemon adapter 仍暴露 `SubmitDispatchTicket / RunDispatchJob` | daemon 执行宿主仍以 dispatch 为一等运行类型 |
| [internal/contracts/pm_dispatch.go](/home/xiewanpeng/tardis/dalek/internal/contracts/pm_dispatch.go) | `PMDispatchJob` 仍是正式持久化模型，且带 `ActiveTicketKey` 等互斥字段 | dispatch 仍是数据库层主实体之一 |
| [internal/contracts/pm_ops.go](/home/xiewanpeng/tardis/dalek/internal/contracts/pm_ops.go) | planner 仍有 `PMOpDispatchTicket` | planner 仍把 dispatch 当正式操作 |

结论：

**当前仓库不是“dispatch 已经删了，只差文档同步”；而是“CLI 表面收口了，但系统主链路仍然围绕 dispatch 运转”。**

### 5.2 `start` 目前不等于“开始执行”

旧方案里最严重的失真之一，是把 `start` 写成了“拿到资源后直接启动 worker 执行”。

代码现实不是这样：

| 位置 | 代码事实 | 结论 |
|---|---|---|
| [internal/services/worker/start.go](/home/xiewanpeng/tardis/dalek/internal/services/worker/start.go) | `StartTicketResourcesWithOptions` 的核心动作是：创建/修复 worktree、创建 worker 记录、准备 log path/runtime anchor | worker start 目前是“准备资源”，不是“开始跑任务” |
| [internal/services/worker/start.go](/home/xiewanpeng/tardis/dalek/internal/services/worker/start.go) | 代码注释明确写了“runtime-first：start 仅准备运行锚点（log path），不再拉起壳进程” | start 不会直接拉起 agent 执行 |
| [internal/services/pm/start.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/start.go) | PM start 会 `MarkWorkerRunning`，并把 ticket 推到 `queued` | 现在的 `running` 有很强的“状态先行”成分，不等于真正已有 deliver run 在跑 |
| [internal/services/pm/worker_loop.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/worker_loop.go) | 真正启动 worker SDK loop 的地方仍然是 dispatch/direct dispatch 路径 | 真正的执行启动仍在 dispatch 侧 |

结论：

**当前的 `start` 语义是“准备资源并使 ticket 进入可 dispatch 状态”，不是“开始执行 ticket”。**

### 5.3 `queued` 和 `active` 的当前语义，和旧方案写的不一样

当前语义更接近：

| 状态 | 当前真实语义 | 证据 |
|---|---|---|
| `queued` | 资源已准备，等待 dispatch | [internal/services/pm/start.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/start.go), [internal/services/pm/manager_tick.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/manager_tick.go) |
| `active` | dispatch 已 claim，PM dispatch agent / worker loop 已进入执行编排 | [internal/services/pm/dispatch_queue_workflow.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/dispatch_queue_workflow.go), [internal/services/pm/dispatch_runner.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/dispatch_runner.go) |

这意味着旧方案中的这几句话都不成立：

- “queued 已经是等配额态”
- “start 之后如有配额会直接 active”
- “active 表示 worker 已经自驱动运行”

在当前实现里：

- `queued + worker running` 是正常中间态，不是非法组合
- `active` 可以发生在 PM dispatch agent 还没结束时
- `backlog -> active` 兼容路径仍在 FSM 里保留

### 5.4 `blocked -> start -> queued` 在当前服务层里并不成立

这一点很关键。

从代码看：

- [internal/fsm/ticket_workflow_guards.go](/home/xiewanpeng/tardis/dalek/internal/fsm/ticket_workflow_guards.go) 的 `CanStartTicket` 允许 `blocked`
- 但 [internal/services/pm/start.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/start.go) 里的 `promoteTicketQueuedOnStart` 只会在 `workflow_status = backlog` 或空值时把 ticket 推到 `queued`
- 也就是说，**纯服务层的 `StartTicket` 并不会把 `blocked` 正式推进到 `queued`**
- 当前 `blocked` 恢复之所以“看起来能继续”，是因为 CLI `ticket start` 其实直接走 daemon `SubmitDispatch`

结论：

**当前的 blocked 恢复并不是“start -> queued -> active”这条新流程，而是“start 这个命令名下面仍然藏着 dispatch”。**

### 5.5 `workflow_status` 目前不是事件投影，只是“写状态时顺手记一条事件”

仓库已经有 append-only 事件表，但它们现在还不是权威真相源。

当前现实：

| 事实 | 现状 |
|---|---|
| 有 `ticket_workflow_events` | 是 |
| `workflow_status` 从事件重算得出 | 不是 |
| 状态推进先写事件再投影 | 不是 |
| 多条路径直接 `UPDATE tickets.workflow_status` | 是 |
| 事件现在只是旁路审计 | 是 |

直接写 `workflow_status` 的主要入口至少包括：

- [internal/services/pm/start.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/start.go)
- [internal/services/pm/workflow.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/workflow.go)
- [internal/services/pm/dispatch_queue_workflow.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/dispatch_queue_workflow.go)
- [internal/services/pm/direct_dispatch.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/direct_dispatch.go)
- [internal/services/pm/manager_tick_worker_not_ready.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/manager_tick_worker_not_ready.go)
- [internal/services/pm/zombie_check.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/zombie_check.go)
- [internal/services/pm/recovery.go](/home/xiewanpeng/tardis/dalek/internal/services/pm/recovery.go)

而且现有 workflow event 也不完整：

- `ticket create` 目前不会写 lifecycle 事件
- `integration_status` 没有自己的 append-only 事件流
- 事件结构只是 `from_status -> to_status`，不够表达 create / merge / repair / duplicate report / activation failure 等事实

结论：

**当前实现已经有“事件链雏形”，但还没有进入“事件是事实，状态是投影”的模式。**

### 5.6 merge 只能作为目标参照，不能被当成“已经完全事件化的现状”

当前 merge 相关实现是混合态：

| 事实 | 现状 |
|---|---|
| `integration_status` 已经挂到 ticket 上 | 是 |
| `merge sync-ref / rescan / retarget` 已有实现 | 是 |
| `integration_status` 当前也还是直接 update | 是 |
| 有统一 integration 事件流 | 没有 |
| `MergeItem` 已经彻底退出主模型 | 没有，仓库里仍有 [internal/contracts/merge.go](/home/xiewanpeng/tardis/dalek/internal/contracts/merge.go) 和相关读取 |

所以新设计里可以“参考 merge 已经把交付状态挂回 ticket 上”这件事；
但不能把 merge 写成“现成可直接照抄的事件投影实现”。

## 6. 审计结论

基于上面的代码事实，旧方案里最主要的问题可以收口为 5 条：

1. 把“CLI 表面移除了 dispatch”误写成了“系统已经物理删除 dispatch”
2. 把“start 准备资源”误写成了“start 直接启动 worker 执行”
3. 把“queued 等 dispatch”误写成了“queued 等配额”
4. 把“写状态时附带审计事件”误写成了“状态已经是事件投影”
5. 把“blocked 当前靠 dispatch 恢复”误写成了“blocked 已经自然收敛到 start -> queued -> active”

所以新设计不能从“目标状态图长什么样”直接开始写；
必须先承认当前仓库仍然是 **dispatch-centric**，再设计怎么迁走。

## 7. 新设计总原则

### 7.1 dispatch 从主链路物理消失

目标不是兼容 shim，而是：

- 不再有 `PMDispatchJob`
- 不再有 `dispatch runner`
- 不再有 `PM dispatch agent`
- 不再有 `TaskTypeDispatchTicket`
- 不再有 `TaskTypePMDispatchAgent`
- 不再有 `queued -> dispatch -> active` 这层生命周期语义

### 7.2 保留 ticket snapshot，但把它降级为 projection cache

不要求读路径一夜之间全改成“每次现算事件”。

更实际的方案是：

- `tickets` 表继续保留
- `workflow_status / integration_status / merge_anchor_sha / target_branch` 继续保留在 `tickets` 上
- 但它们不再是独立真相源
- 它们是从 lifecycle ledger 计算出来的 materialized snapshot

也就是说：

- 写路径以事件为主
- 读路径仍可先读 snapshot
- 出现漂移时可以从事件重算修复

### 7.3 worker 资源状态和 ticket 生命周期状态解耦

当前代码里 `worker.status=running` 有很强的“状态先行”意味。

新设计里必须明确：

- `ticket` 生命周期描述“任务正在什么阶段”
- `worker` 记录描述“资源容器是否存在、是否有活跃运行”
- `task_run` / `task_events` 描述“本轮实际执行发生了什么”

其中：

- lifecycle truth 不应依赖 `worker.status` 单独成立
- 容量统计也不应只看 `worker.status = running`
- `active` 应该和“存在被系统接受的活跃执行 run”对齐

### 7.4 `merged` 是 ticket 生命周期阶段，不必变成 workflow 新枚举

新设计继续保留：

- `workflow_status`: `backlog | queued | active | blocked | done | archived`
- `integration_status`: `none | needs_merge | merged | abandoned`

因此业务上说“ticket 已 merged”时，存储上仍然是：

- `workflow_status = done`
- `integration_status = merged`

不强行引入 `workflow_status = merged`

## 8. 目标模型

### 8.1 核心实体与真相源

| 实体 | 是否保留 | 角色 |
|---|---|---|
| `tickets` | 保留 | lifecycle snapshot cache |
| `workers` | 保留 | ticket 的资源容器记录 |
| `task_runs` / `task_events` | 保留 | worker 实际执行与运行观测事实 |
| `PMDispatchJob` | 删除主路径，迁移期只读 | 旧模型遗留 |
| `MergeItem` | 迁移期可保留只读/兼容 | 不是 ticket 生命周期真相 |
| `ticket_lifecycle_events` | 新增 | ticket 生命周期的唯一权威事实流 |

### 8.2 新增 `ticket_lifecycle_events`

现有 `ticket_workflow_events` 不够表达这次重构要的事件模型。

因此引入新的 lifecycle ledger：

```text
ticket_lifecycle_events
  - id
  - created_at
  - ticket_id
  - sequence
  - event_type
  - source
  - actor_type
  - worker_id
  - task_run_id
  - idempotency_key
  - payload_json
```

设计要求：

- append-only
- 每 ticket 单调递增 `sequence`
- 支持按 `ticket_id + idempotency_key` 去重
- 不只记录“状态变成了什么”，还要记录“发生了什么事实”

### 8.3 生命周期事件集合

状态推进相关的核心事件至少包括：

| 事件 | 产生者 | 主要 payload | projection 结果 |
|---|---|---|---|
| `ticket.created` | ticket create | title, description, priority, target_ref | `workflow=backlog` |
| `ticket.start_requested` | PM start | requested_by, reason, base_branch | `workflow=queued` |
| `ticket.activated` | scheduler / execution host | worker_id, task_run_id, slot_id | `workflow=active` |
| `ticket.wait_user_reported` | worker report reducer | worker_id, task_run_id, summary, blockers | `workflow=blocked` |
| `ticket.done_reported` | worker report reducer | worker_id, task_run_id, head_sha, anchor_sha, target_ref | `workflow=done`, `integration=needs_merge` |
| `ticket.merge_observed` | merge sync-ref / rescan | ref, anchor_sha, merged_sha | `integration=merged` |
| `ticket.merge_abandoned` | PM merge abandon | reason | `integration=abandoned` |
| `ticket.archived` | PM archive | archived_by, reason | `workflow=archived` |

补充事件：

| 事件 | 用途 | 是否直接改 workflow |
|---|---|---|
| `ticket.activation_failed` | 激活失败但仍可自动重试 | 否 |
| `ticket.execution_lost` | 活跃执行异常退出、失联、心跳超时、execution host 丢视野 | 否 |
| `ticket.requeued` | 系统决定自动重试，把 ticket 重新放回等待执行 | 是，投影到 `queued` |
| `ticket.execution_escalated` | 自动恢复耗尽，升级人工介入 | 可投影到 `blocked` |
| `ticket.repaired` | repair path 补投影 / 补事件 / 回填 snapshot | 否 |

### 8.4 projection 规则

`workflow_status` 的目标投影规则：

| 最后一条状态型事件 | workflow_status |
|---|---|
| `ticket.created` | `backlog` |
| `ticket.start_requested` | `queued` |
| `ticket.requeued` | `queued` |
| `ticket.activated` | `active` |
| `ticket.wait_user_reported` | `blocked` |
| `ticket.done_reported` | `done` |
| `ticket.archived` | `archived` |

`integration_status` 的目标投影规则：

| 最后一条 integration 相关事件 | integration_status |
|---|---|
| 无 | `none` |
| `ticket.done_reported` | `needs_merge` |
| `ticket.merge_observed` | `merged` |
| `ticket.merge_abandoned` | `abandoned` |

补充规则：

- `ticket.done_reported` 必须同时携带 `anchor_sha`
- `ticket.merge_observed` 不能单独让 workflow 变成 `merged`
- `ticket.archived` 前必须保证 integration 已经是 terminal 或 ticket 还没进入 done 收口

## 9. 目标行为设计

### 9.1 `ticket create`

目标行为：

1. 创建 `tickets` snapshot 初始行
2. 追加 `ticket.created`
3. snapshot 投影为 `workflow=backlog`

为什么要补这个：

- 当前实现创建 ticket 不写 lifecycle 事件
- 如果不补 create event，后面的“状态由事件重算”天然就缺首帧

### 9.2 `ticket start`

新设计里，`start` 只做“请求开始/恢复执行”，不直接等价于 dispatch。

目标行为：

1. 校验 ticket 当前允许 start
2. 追加 `ticket.start_requested`
3. snapshot 投影为 `queued`
4. 唤醒 scheduler / execution host
5. 返回 `queued` 或 `active`

关键变化：

- `start` 不再直接提交 daemon dispatch
- `start` 不再接受“本轮 PM 注入 prompt”作为主路径参数
- `blocked -> start` 必须稳定落到 `queued`
- `active -> start` 只能作为幂等请求处理，不重新派发

### 9.3 激活流程：`queued -> active`

这一层替代现在的 dispatch 主链路。

新设计里，激活流程由 scheduler / execution host 负责：

1. 选择一个 `queued` ticket
2. 确认容量是否可用
3. 确保 worker 资源存在
4. 提交真正的 worker 执行 run
5. 只有 run 被系统接受后，才追加 `ticket.activated`
6. snapshot 投影为 `active`

注意：

- `active` 的成立点不能再是“dispatch claim 成功”
- `active` 必须对齐到“系统已经接受了一轮真实 worker 执行”
- 如果资源准备成功但 run 尚未被接受，ticket 仍应保持 `queued`
- 如果当前没有可用配额，ticket 继续保持 `queued`，这不是失败，也不写 incident 事件

### 9.4 worker 自驱动启动

删掉 dispatch 后，worker 的启动机制改成：

- 不再先跑 PM dispatch agent 构造 prompt
- 不再通过 PM agent 向 worker 注入执行指令
- worker 直接以固定 bootstrap 进入 `deliver_ticket` 运行
- bootstrap 内容必须只依赖可读事实：
  - ticket title / description / target_ref
  - 当前 worktree
  - worker kernel / bootstrap 模板
  - repo 内现成文档

这不是一个独立主需求，而是“删除 dispatch”后的必要实现约束。

### 9.5 `worker report`

`worker report` 继续保留为 worker 向 PM 上报的高权威信号，但 ticket 生命周期不直接依赖 raw report 本身，而是依赖 report 被 reducer 接受后生成的 lifecycle event。

目标语义：

| `next_action` | 生命周期事件 | projection |
|---|---|---|
| `continue` | 不追加状态型 lifecycle event | ticket 维持 `active` |
| `wait_user` | `ticket.wait_user_reported` | `workflow=blocked` |
| `done` | `ticket.done_reported` | `workflow=done`, `integration=needs_merge` |

关键要求：

- `done` 必须和 anchor 冻结一起发生
- `continue` 不应产生无意义的 active 重复事件
- terminal report 必须可去重

### 9.6 `merge sync-ref`

新设计里 merge 仍由 git 事实驱动，但它也必须写入 lifecycle ledger。

目标行为：

1. 检查 `workflow=done + integration=needs_merge`
2. 检查 `anchor_sha` 是否已并入目标 ref
3. 成功时追加 `ticket.merge_observed`
4. snapshot 投影为 `integration=merged`

`rescan` 仍保留，但只作为 repair path，不是主路径。

### 9.7 `ticket archive`

目标行为：

1. 校验是否允许 archive
2. 追加 `ticket.archived`
3. snapshot 投影为 `workflow=archived`
4. 请求 worktree cleanup

规则保持：

- `done + needs_merge` 不允许 archive
- `merged` / `abandoned` 后允许 archive
- `backlog` / `queued` / `blocked` 允许显式放弃后 archive

## 10. 目标状态语义

### 10.1 `queued` 的新语义

新设计中，`queued` 的唯一语义是：

**ticket 已收到 start 请求，正在等待系统接受一轮真实执行。**

这包括两类情况：

- 还没拿到容量
- 正在准备资源，但还没形成被接受的 active run
- 上一轮 active run 已被判定可自动重试，系统已把它重新放回等待执行

因此新设计里：

- `queued` 不再表示“等 dispatch”
- `queued` 也不强行只表示“纯配额不足”
- 它表达的是“还没真正进入 active 执行”

这样更贴近代码迁移现实，也避免把“资源准备中”和“等 capacity”拆成两个无必要状态。

### 10.2 `active` 的新语义

`active` 的唯一语义是：

**系统已经接受了一轮 ticket 的真实执行 run，worker 正在执行或理论上应当正在执行。**

这比当前实现更严格：

- 不是 dispatch claim 成功就算 active
- 不是 worker 资源存在就算 active
- 不是单纯 `worker.status=running` 就算 active
- 一旦确认 active run 已经异常退出或失联，ticket 就不能无限期停留在 `active`

### 10.3 `blocked` 的新语义

`blocked` 的主语义保留为：

**ticket 当前需要外部介入，不能继续自主推进。**

但新设计要补清楚：

- `report(wait_user)` 是显式 blocked 来源
- 系统级恢复耗尽也可以通过 `ticket.execution_escalated` 投影为 blocked
- 文档和 UI 必须区分：
  - `blocked(reason=user_wait)`
  - `blocked(reason=system_incident)`

这样才能覆盖真实非 happy path，而不是把所有 blocked 都误当成“用户没回复”。

## 11. 写路径设计

### 11.1 正常写路径

正常路径统一遵循：

1. 接收命令 / 外部事实
2. 生成 lifecycle event
3. 在同一事务内：
   - 追加 lifecycle event
   - 更新 `tickets` snapshot
   - 处理同因果 side effects
4. 提交事务

### 11.2 repair 写路径

保留 repair，但必须从主路径中降级出去。

要求：

- 不再暴露常规 `SetTicketWorkflowStatus` 作为正常能力
- 手工改状态只能走 repair / force-set 工具
- repair 必须显式写 `ticket.repaired`
- repair 必须说明来源、原因、关联 ticket/run/worker

也就是说：

- 主路径是“事件 -> projection”
- repair 是“补事实 / 补 projection / 修漂移”
- 两者不能混用

## 12. 常态运行分支与恢复设计

这次设计必须把这些运行条件写进主文档，不再作为附录或“补充异常”：

- 容量不足
- worker 执行失败
- worker / host / daemon 失联
- runtime 事实先落库、lifecycle 投影稍后补齐

这些都不是边缘 case，而是系统的日常运行现实。

### 12.1 start 重复触发

目标行为：

- `backlog -> start`：写 `ticket.start_requested`
- `blocked -> start`：写 `ticket.start_requested`
- `queued -> start`：幂等，不重复排队
- `active -> start`：幂等，不重复激活
- `done/archived -> start`：拒绝

### 12.2 容量不足 / 配额不足

这是 `queued` 的正常停留原因，不是异常。

新设计要求：

- 没有可用容量时，ticket 保持 `queued`
- 不写 `ticket.activation_failed`
- 不写 `ticket.execution_escalated`
- 不把长时间 `queued` 自动视为状态漂移
- scheduler 在容量释放后继续尝试激活

### 12.3 资源准备成功，但 active run 尚未接受

当前代码里这是典型半成功来源。

新设计要求：

- 在 worker run 被系统接受前，不允许追加 `ticket.activated`
- 资源侧失败只写 `ticket.activation_failed`
- snapshot 保持 `queued`
- 达到重试上限再 `ticket.execution_escalated`

### 12.4 active 运行中 worker 异常退出

这不是 repair-only 的罕见例外，而是常态运行分支。

新设计要求：

- 一旦 execution host 观察到活跃 run 非预期退出，必须追加 `ticket.execution_lost`
- 在收到 terminal report 之前，禁止把 crash 误投影成 `done` 或 `blocked(reason=user_wait)`
- 如果自动重试预算未耗尽，系统追加 `ticket.requeued`，snapshot 回到 `queued`
- 如果自动重试预算耗尽，系统追加 `ticket.execution_escalated`，snapshot 进入 `blocked(reason=system_incident)`

### 12.5 active 运行中失联 / 心跳超时 / execution host 丢视野

这里的重点不是“有没有网络”这个字面问题，而是系统何时失去对 active run 的可信观测。

新设计要求：

- 如果 execution host 直接托管 worker 子进程，进程退出事实优先于心跳
- 如果 worker 只能被远端或间接观测，必须有 heartbeat / lease / liveness monitor
- heartbeat / lease 属于 runtime 事实源，不直接裸写 workflow
- 一旦达到超时阈值，必须追加 `ticket.execution_lost`
- 后续收敛规则与“active 运行中 worker 异常退出”一致：
  - 可自动恢复则 `ticket.requeued`
  - 自动恢复耗尽则 `ticket.execution_escalated`

### 12.6 worker run 已接受，但 active 投影缺失

这是删除 dispatch 后新的关键 repair 场景。

新设计要求：

- 用 `task_run_id` 作为 activation 的幂等锚点
- 如果发现存在活跃 `deliver_ticket` run，但没有对应 `ticket.activated`
- 由 reconcile 追加 `ticket.repaired` 或补写 `ticket.activated`
- 最终 snapshot 修正为 `active`

### 12.7 runtime 事件已写入，但 lifecycle reducer 失败

这是当前 `ApplyWorkerReport` 已经暴露出来的问题形态。

新设计要求：

- task/runtime 事件链继续 append-only
- lifecycle reducer 失败时，不覆盖 runtime 事实
- 后续 reconcile 应能按 `run_id + next_action + report fingerprint` 重放为 lifecycle event

### 12.8 terminal report 重复写入

新设计要求：

- `wait_user` / `done` 必须有去重键
- 同一执行轮次的 terminal report 只能生效一次
- 后续重复上报只记 duplicate incident，不再重复推进 workflow/integration

### 12.9 daemon 重启

新设计里 daemon 重启后的恢复不再围绕 dispatch lease，而应围绕两类事实：

- `queued` tickets
- 活跃 `deliver_ticket` runs

恢复流程：

1. 重建活跃 run 索引
2. 修复 `active` / `queued` projection 漂移
3. 继续调度剩余 queued tickets

### 12.10 merge hook 丢失

保持现有原则：

- `sync-ref` 是主路径
- `rescan` 是 repair path

但新设计要求：

- `rescan` 成功后也要写 `ticket.merge_observed`
- 不能只 update snapshot，不留 lifecycle 事实

## 13. 对现有模块的具体改造要求

### 13.1 PM / daemon

必须修改：

- `ticket start` 不再调用 daemon `SubmitDispatch`
- daemon execution host 不再维护 dispatch lane
- `scheduleQueuedTickets` 不再 `StartTicket + dispatchScheduledTicket`
- 激活路径改成“接受 worker run -> 追加 activated event”
- execution host / reconcile 必须能观察 active run 的退出、失联、超时，并写入 `ticket.execution_lost`
- 自动重试和升级人工介入必须是正式主路径，不得只靠 repair 命令救火

### 13.2 worker

必须修改：

- `StartTicketResourcesWithOptions` 不再暗示“已经开始执行”
- `MarkWorkerRunning` 只能在真实 active run 建立后使用
- worker bootstrap 改为自驱动入口，不依赖 PM dispatch agent 产物
- worker 或其宿主必须能提供足够的 liveness / exit 事实，供 execution host 判断 run 是存活、退出还是失联

### 13.3 ticket query / capability / UI

必须修改：

- `CanDispatch` 从主 UI/CLI 能力中移除或降级为兼容字段
- `activeDispatchByTicket` 不再是 ticket view 的核心判断条件
- ticket 能力判断改为基于：
  - lifecycle snapshot
  - active worker run
  - worker resource availability

### 13.4 PMOps

必须修改：

- `PMOpDispatchTicket` 从正式 planner op 中退出
- 替换为 `PMOpStartTicket` 或等价语义
- planner 不再以“dispatch 成功”作为 ticket 进入执行的标志
- planner 观察的是：
  - `queued`
  - `active`
  - `blocked`
  - `done + integration`

### 13.5 merge

必须修改：

- `sync-ref / rescan / abandon` 都要写 lifecycle event
- `MergeItem` 只作为兼容读取对象，不再是 ticket 生命周期主真相

## 14. 迁移策略

这次不能一步切掉，因为当前代码对 dispatch 的依赖太深。

### Phase A: 先把事件账本立起来，不改主行为

目标：

- 新增 `ticket_lifecycle_events`
- 现有 create/start/dispatch claim/fail/report/archive/merge 路径全部双写新 ledger
- `tickets` 仍按旧逻辑更新

收益：

- 先把事实账补齐
- 不影响现有运行链路

### Phase B: 让 `start` 回归“排队请求”

目标：

- `ticket start` 改成真正的 `start_requested`
- CLI `ticket start` 不再 `SubmitDispatch`
- `blocked -> start -> queued` 先闭合

收益：

- 先把最混乱的用户语义纠正

### Phase C: 用 activation 替代 dispatch

目标：

- scheduler / daemon 直接提交 `deliver_ticket` run
- `ticket.activated` 取代 dispatch claim promote active
- `queued` 和 `active` 语义切换到新模型

收益：

- 主生命周期不再依赖 dispatch

### Phase D: 移除 dispatch 主实现

目标：

- 移除 `PMDispatchJob` 写入
- 移除 dispatch runner / PM dispatch agent / dispatch task type
- 旧表只读保留一段时间用于审计

收益：

- 物理删除 dispatch 主链路

### Phase E: 清理遗留直写状态路径

目标：

- `SetTicketWorkflowStatus` 改成 repair-only
- 删除依赖 dispatch 的 capability / view / planner 假设
- 用 lifecycle reconcile 替代旧 recovery 逻辑

## 15. 明确非目标

这次设计不是：

- 只改一张状态图
- 只把 `ticket dispatch` 命令删掉
- 只做一个 scheduler
- 只补 worker bootstrap 文本
- 只给现有状态写路径再包一层 event log

如果最终结果仍然是：

- `start` 背后其实还在 dispatch
- `workflow_status` 还是多处裸写
- 发生故障后还得靠人工猜哪个状态才是真的

那就不算完成这次重构。

## 16. 验收标准

这一节只定义“什么叫这次重构完成”，不规定研发具体怎么实现。

### A1. dispatch 必须从 ticket 主链路物理消失

验收要求：

- `ticket start` 不再提交或触发 dispatch
- `queued -> active` 不再经过 dispatch job / dispatch runner / PM dispatch agent
- planner 不再把 `dispatch_ticket` 当正式 op
- daemon execution host 不再维护 dispatch 作为 ticket 主执行 lane

验收口径：

- 描述 ticket 主生命周期时，不再需要 `dispatch`
- 从 CLI、daemon、manager tick、planner 四层观察，主执行链都不再依赖 dispatch

### A2. worker 执行必须改成自驱动

验收要求：

- worker 在没有 PM 注入执行 prompt 的前提下可以启动并执行 ticket
- ticket 的执行入口不再依赖 PM dispatch agent 生成 handoff context
- worker 启动所依赖的信息都来自 ticket/worktree/kernel/bootstrap 可读事实

验收口径：

- 对一个新 ticket，只执行 `create -> start`，系统即可进入真实执行链
- 执行过程中不需要 PM 再编译“本轮要做什么”

### A3. `queued`、`active`、`blocked` 的语义必须稳定且唯一

验收要求：

- `queued` 只表示“已收到 start 请求，但尚未进入被系统接受的 active 执行”
- `queued` 必须能自然覆盖：
  - 配额不足
  - 资源准备中
  - active run 异常后被系统自动重试
- `active` 只表示“系统已接受一轮真实 worker 执行 run”
- `active` 一旦被确认退出或失联，必须通过正式事件收敛，不允许无限悬空
- `blocked` 必须可区分至少两类来源：
  - worker 显式 `wait_user`
  - 系统恢复失败 / incident 升级

验收口径：

- 任一时刻抽一张 ticket，都能不依赖猜测解释其状态含义
- 不允许同一个状态同时背两套语义

### A4. ticket 状态必须是事件投影，不是直接真相源

验收要求：

- ticket 生命周期存在明确的 append-only ledger
- 至少以下事实必须有独立 lifecycle 事件：
  - create
  - start requested
  - activated
  - execution lost
  - requeued
  - wait_user reported
  - done reported
  - merge observed
  - merge abandoned
  - archived
  - repaired
- `tickets` 上的 `workflow_status / integration_status` 必须可由 lifecycle 事件重建

验收口径：

- 随机抽一张 ticket，可以从事件链解释当前 snapshot
- 清空 snapshot 后从事件重放，结果与现存状态一致，或能明确识别出漂移

### A5. 正常路径和 repair 路径必须分离

验收要求：

- 正常生命周期推进只能通过正式事件入口发生
- 手工修复状态不再伪装成正常主路径
- repair 必须留下显式 repair 事件与原因

验收口径：

- 研发可以明确列出“正常状态推进入口”和“repair 入口”
- 不允许继续把 `SetTicketWorkflowStatus` 一类能力当常规业务路径使用

### A6. `done` 之后的 merge/archive 收口必须进入同一条 ticket 生命周期

验收要求：

- `done` 不是 ticket 生命周期终点
- `merge sync-ref` 或等价 git fact 检测成功后，ticket 必须进入 `integration=merged`
- `done + needs_merge` 不能 archive
- `merged` 或 `abandoned` 后才能 archive

验收口径：

- ticket 生命周期必须覆盖“开发完成”到“交付完成/放弃”再到“归档完成”
- 不允许把 merge 收口留在 ticket 生命周期之外

### A7. 常态运行故障必须闭合

至少要覆盖并通过以下场景：

- 重复 `start`
- 长时间无配额，ticket 稳定保持 `queued`
- `blocked -> start -> queued -> active`
- 激活半成功：资源准备了，但 active run 未建立
- active worker run 异常退出后自动重试，ticket 回到 `queued`
- active worker run 失联或心跳超时后，系统能区分“继续等待”和“升级人工介入”
- active run 已建立，但 active 投影缺失
- 重复 `wait_user` / `done` report
- terminal report 重复写入
- worker crash / runtime 丢失
- daemon 重启后恢复
- merge hook 丢失后 `rescan` 修复

验收口径：

- 每个场景都必须能回答：
  - 真实事实是什么
  - snapshot 应该长什么样
  - 如何自动恢复
  - 自动恢复失败后如何升级为人工处理

### A8. 当前状态必须可审计、可解释

验收要求：

- ticket 当前状态能追溯到明确 lifecycle event
- worker 当前运行态能追溯到 task run / task events
- merge 当前状态能追溯到 integration 相关事件或 git fact sync 事件

验收口径：

- 对任一 ticket，PM 能回答：
  - 为什么它现在是 queued/active/blocked/done
  - 上一次状态变化是谁触发的
  - 对应 run / worker / merge 事实是什么

### A9. 迁移完成后，旧 dispatch 只允许作为历史遗留，不允许继续驱动主流程

验收要求：

- 旧 dispatch 数据如果保留，只用于历史审计/兼容读取
- 新 ticket 的主链路不再写入新的 `PMDispatchJob`
- 新 planner / daemon / manager tick 路径不再以 dispatch 作为核心运行机制

验收口径：

- 可以保留旧表，但不能再靠它推进新的 ticket 生命周期

### A10. 最终验收样例

至少应能在一个真实 repo 上完成下面这条链路：

```text
create
-> start
-> queued
-> active
-> report(wait_user)
-> blocked
-> start
-> queued
-> active
-> report(done)
-> done + needs_merge
-> merge sync-ref
-> merged
-> archive
-> archived
```

并且在这条链路中同时满足：

- 全程不经过 dispatch
- 每一步都有对应 lifecycle 事件
- snapshot 与事件投影一致
- 至少插入一次非 happy path 并能恢复

## 17. 结论

基于仓库现状，这次 ticket 流程重构的正确方向不是“在旧 dispatch 模型上修修补补”，而是：

1. 承认当前系统仍是 dispatch-centric
2. 先建立真正的 ticket lifecycle ledger
3. 让 `start` 回归排队请求
4. 用 activation 直接启动 worker run，取代 dispatch
5. 把 `tickets` 上的状态降级成事件投影

最后得到的模型应当是：

- 主链路没有 dispatch
- `queued` / `active` / `blocked` / `done` 都有清晰语义
- merge/archive 被纳入同一条 ticket 生命周期
- 非 happy path 可以靠 ledger + reconcile 恢复，而不是靠临时直写状态救火
