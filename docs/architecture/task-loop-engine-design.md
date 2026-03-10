# Dalek Loop Engine and Task Loop Design

## 背景

当前分支已经有 `manager loop`、planner run、ticket/merge/inbox 自动推进这些能力，但它们仍主要围绕“运行态收敛”工作：

- worker 是否活着
- dispatch 是否完成
- merge 是否 ready
- planner 是否该再跑一轮

这足以驱动单张 ticket 和若干 feature 的演示闭环，但还不足以稳定完成“整个需求图”的长期推进。核心缺口有两个：

1. 系统缺少一个显式的、可恢复的“任务完成控制器”，用于持续把一个需求从文档推进到 acceptance 完成。
2. 不能靠不断手工发明 `feature loop`、`milestone loop`、`phase loop` 来补这个缺口，否则层级会无限膨胀，系统也会变成一堆定制调度器。

下一阶段的目标，是把这两个问题同时解决：

- 底层提供一个通用 `Loop Engine`
- 上层只提供一种抽象的 `Task Loop`

也就是：底层支持多种 loop，顶层只暴露一种“驱动任务图到完成态”的能力。

## 设计目标

- 把现有 daemon 中隐含的循环逻辑收敛成统一的 `Loop Engine`
- 在同一个 engine 中承载 `RuntimeLoop` 和 `TaskLoop`，而不是平级拼接多个手写 daemon loop
- 让 `TaskLoop` 驱动一个通用 task graph，而不是为 feature / milestone / phase 分别做定制 loop
- 保证 loop 可租约、可 checkpoint、可 journal、可 crash recovery
- 保证同一项目内多个 loop 不争写同一状态面
- 保持当前 `cmd -> app -> services -> store` 分层，不把 loop 直接做成随意执行 Bash 的大脚本

## 非目标

- 不把 dalek 改造成一门通用编程语言
- 不废弃现有 ticket / worker / merge / inbox / FSM 模型
- 不让 `TaskLoop` 直接写 worker/task/merge 运行态
- 不引入 `feature loop`、`milestone loop`、`epic loop` 这类无限层级的业务专用 loop
- 不新增第二个独立 daemon 与现有 manager 抢状态

## 核心抽象

### 1. Loop Engine

`Loop Engine` 是 daemon 内的通用控制器框架，负责：

- 注册 loop kind
- 发现需要运行的 loop instance
- 管理 lease / wake / cooldown / retry
- 驱动统一生命周期：`observe -> reduce -> plan -> apply -> checkpoint`
- 记录 op journal、checkpoint 和指标

它不关心某个 loop 在业务上是“runtime”还是“task”；它只负责调度和恢复。

### 2. Loop Kind

当前阶段只正式支持两种 kind：

- `runtime`
- `task`

未来可以扩展更多 kind，但不改变用户层抽象。

### 3. Loop Instance

一个 `LoopInstance` 表示某个 scope 上的一条具体 loop。

示例：

- `runtime:project:1f70ba93279f`
- `task:feature:pm-autonomy-hardening`

每个 instance 都有：

- `kind`
- `scope_key`
- `status`
- `dirty`
- `next_wake_at`
- `cooldown_until`
- `lease_owner`
- `lease_expires_at`
- `active_op_id`
- `last_error`
- `version`

### 4. Fact

`Fact` 是 loop 消费的结构化事实，不是原始日志片段。

示例：

- `ticket_done(t24)`
- `merge_ready(#31)`
- `dispatch_failed(t24, reason=bootstrap_timeout)`
- `acceptance_failed(A-browser-overview)`

只有 `RuntimeLoop` 能直接解释原始 runtime 事件；`TaskLoop` 只消费已经规约好的 facts。

### 5. Op

`Op` 是 loop 发出的结构化意图，由 executor 执行。

典型 op：

- `create_ticket`
- `dispatch_ticket`
- `approve_merge`
- `discard_merge`
- `close_inbox`
- `run_acceptance`
- `write_doc`
- `update_task_graph`

每个 op 必须带：

- `op_id`
- `request_id`
- `kind`
- `scope_key`
- `idempotency_key`
- `arguments`
- `preconditions`

### 6. Checkpoint

`Checkpoint` 用于 crash recovery。它记录：

- 本轮消费到的 facts 位置
- 本轮 graph/version 摘要
- 已确认完成的 op
- 还未完成的 op
- 下一轮推荐从哪里恢复

## Loop 分类与职责

### RuntimeLoop

`RuntimeLoop` 负责系统运行态收敛，scope 默认是 project。

它回答的问题是：

- 哪些 worker / dispatch / task run 进入了新状态
- 哪些 merge / inbox 发生了技术态变化
- 哪些 runtime incident 需要暴露
- planner / acceptance 执行器是否失活

它允许写入的状态域：

- worker lifecycle
- task runs
- dispatch runs
- ticket workflow status
- merge runtime status
- inbox runtime incident
- runtime metrics

它的产出是：

- 已规约的 facts
- 需要唤醒 `TaskLoop` 的 trigger

### TaskLoop

`TaskLoop` 负责把一个 task graph 推进到完成态，scope 默认是一个 root task。

它回答的问题是：

- 当前需求图跑到哪个 node
- 哪些 node 已 ready
- 该创建 / dispatch 哪张 ticket
- 该不该批准 merge
- 什么时候进入 acceptance
- acceptance 失败后如何重新规划
- 根任务是否已经完成

它允许写入的状态域：

- `.dalek/pm/plan.json`
- `.dalek/pm/plan.md` 渲染输出
- `.dalek/pm/state.json`
- `.dalek/pm/acceptance.md`
- `loop_op_journal`
- `loop_checkpoints`
- feature-level decisions / evidence refs

它不能直接写：

- task runs
- worker runtime
- dispatch runtime
- merge runtime

这些动作只能通过 `Op -> Executor -> Services` 间接完成。

## 为什么只需要一种 Task Loop

本设计不把 feature、milestone、phase 当成不同 loop。

它们只是 task graph 中不同粒度的复合节点：

- `ticket` 是原子节点
- `feature` 是复合节点
- `milestone` 也是复合节点
- `phase` 仍然只是复合节点
- `requirement` / `design` / `integration` / `acceptance` 是不同类型的节点

所以系统只需要一种抽象：

`TaskLoop = 驱动一个 task graph 的 root node 到完成态`

这样就不会出现：

- `feature = ticket loop`
- `milestone = feature loop`
- `project = milestone loop`

这种无限手工分层。

## Task Graph 模型

### Node 类型

统一节点模型：

- `atomic`
- `composite`
- `doc`
- `integration`
- `acceptance`

补充属性：

- `executor_kind`: `worker_ticket | pm_doc | acceptance_runner | integration_ticket`
- `label`: 例如 `feature`、`milestone`、`phase`

其中：

- `label` 只是展示语义
- `type` 和 `executor_kind` 才决定调度行为

### Node 字段

每个 node 至少包含：

- `id`
- `title`
- `type`
- `label`
- `status`
- `owner`
- `depends_on`
- `children`
- `completion_policy`
- `done_when`
- `touch_surfaces`
- `evidence_refs`
- `runtime_ref`
- `retry_policy`

### Completion Policy

复合节点支持少量固定策略，不开放任意代码：

- `all_children_done`
- `any_child_done`
- `ordered_children_done`
- `acceptance_gates_passed`

第一版默认只强支持：

- `all_children_done`
- `ordered_children_done`
- `acceptance_gates_passed`

### 例子

```text
ROOT(composite,label=feature)
├─ D-prd(doc)
├─ D-design(doc)
├─ Phase-A(composite,label=phase, policy=ordered_children_done)
│  ├─ T-pm-runtime-ledger(atomic, executor=worker_ticket)
│  └─ T-plan-graph-sot(atomic, executor=worker_ticket)
├─ Phase-B(composite,label=phase, policy=ordered_children_done)
│  └─ T-pm-oplog-recovery(atomic, executor=worker_ticket)
├─ Phase-C(composite,label=phase, policy=all_children_done)
│  ├─ T-pm-acceptance-engine(atomic, executor=worker_ticket)
│  └─ T-pm-integration-observability(atomic, executor=worker_ticket)
└─ A-zero-human-run(acceptance, executor=acceptance_runner)
```

上面这个图里：

- `feature`、`phase` 只是复合节点标签
- 真正推进它们的是同一个 `TaskLoop`

## 执行模型

### 总体顺序

同一个 project 内，daemon 每轮按固定阶段执行：

1. 选出 due 的 `RuntimeLoop` instances
2. 串行运行 `RuntimeLoop`
3. 把 raw runtime events 规约成 facts
4. 唤醒受影响的 `TaskLoop` instances
5. 串行运行 `TaskLoop`
6. 持久化 checkpoint / journal / next wake

这意味着：

- 两类 loop 都在运行
- 但它们不平级争抢原始事件
- 同一项目内由同一个 engine 按固定阶段顺序执行

### RuntimeLoop 生命周期

每轮 runtime tick：

1. `observe`
   - 读取 worker/task/dispatch/merge/inbox 的原始变化
2. `reduce`
   - 推进运行态状态机
   - 生成结构化 facts
3. `checkpoint`
   - 记录本轮已消费到哪里
4. `schedule`
   - 若产生 task facts，则唤醒相关 `TaskLoop`

### TaskLoop 生命周期

每轮 task tick：

1. `observe`
   - 读取 `plan.json`、feature-level state、上轮 checkpoint、pending facts
2. `reduce`
   - 根据 facts 更新 node 状态
3. `plan`
   - 计算 ready frontier
   - 决定下一批 op
4. `apply`
   - 串行执行 op
5. `checkpoint`
   - 落 graph version、completed ops、remaining frontier
6. `schedule`
   - 决定立即重跑、延迟重跑或等待事件唤醒

## 冲突避免机制

### 1. 单一写入者

状态归属必须固定：

| 状态域 | 写入 owner | 读取者 |
| --- | --- | --- |
| worker / task / dispatch | RuntimeLoop + runtime services | RuntimeLoop, TaskLoop |
| ticket workflow | RuntimeLoop + ticket services | RuntimeLoop, TaskLoop |
| merge runtime | RuntimeLoop + merge services | RuntimeLoop, TaskLoop |
| inbox runtime | RuntimeLoop + inbox services | RuntimeLoop, TaskLoop |
| task graph / acceptance / decisions | TaskLoop | RuntimeLoop, TaskLoop |
| op journal / checkpoint | Loop Engine / TaskLoop | RuntimeLoop, TaskLoop |

任何 loop 都不能整对象覆盖别人的状态面，必须按字段归属做精确更新。

### 2. 原始事件只由 RuntimeLoop 解释

例如 worker 发送 `report done`：

- `RuntimeLoop` 负责：
  - 推进 ticket / task run 状态
  - 记录冲突或重复 terminal report
  - 产出 `ticket_done(t24)` fact
- `TaskLoop` 不直接读这条 raw report，只读 `ticket_done(t24)` fact

这样可以避免两边对同一事件做不同解释。

### 3. TaskLoop 只发意图，不直接改运行态

例如 `TaskLoop` 判断某张 ticket 应该启动时，它只能发：

- `dispatch_ticket(ticket=t25)`

真正的运行态推进由 executor 和 service 层负责完成，随后再由 `RuntimeLoop` 观察结果并规约成 fact。

### 4. 每个 instance 单 lease

同一时刻每个 loop instance 只能有一个 active lease owner，避免 daemon 重启或并发调度造成双执行。

## 持久化模型

### Canonical Graph

`TaskLoop` 的任务图真相源仍然是：

- `.dalek/pm/plan.json`

原因：

- 这是 PM 冷启动最需要的 feature-level 状态
- 它属于 workspace，可被版本化、审阅和渲染
- 可以继续生成 `.dalek/pm/plan.md` 作为人类可读视图

### Engine Runtime Metadata

loop engine 的运行态和恢复态放在 DB 中：

- `loop_instances`
- `loop_checkpoints`
- `loop_op_journal`
- `loop_fact_log`

边界是：

- `plan.json` 描述“任务图当前长什么样”
- DB 描述“engine 最近跑到了哪里、有哪些 op 正在/已经执行”

这样可以避免把同一份 graph 同时维护在文件和 DB 中，形成双真相源。

### 建议表结构

#### `loop_instances`

- `id`
- `project_key`
- `kind`
- `scope_key`
- `status`
- `dirty`
- `next_wake_at`
- `cooldown_until`
- `lease_token`
- `lease_expires_at`
- `active_op_id`
- `graph_version`
- `last_error`
- `updated_at`

#### `loop_checkpoints`

- `id`
- `instance_id`
- `revision`
- `consumed_fact_cursor`
- `graph_version`
- `snapshot_json`
- `created_at`

#### `loop_op_journal`

- `id`
- `instance_id`
- `op_id`
- `request_id`
- `kind`
- `idempotency_key`
- `arguments_json`
- `status`
- `executor_ref`
- `last_error`
- `started_at`
- `finished_at`

#### `loop_fact_log`

- `id`
- `project_key`
- `producer_kind`
- `scope_key`
- `fact_kind`
- `payload_json`
- `created_at`

## Executor 设计

`TaskLoop` 不直接执行 Bash。它只调用受限 executor：

- `DocExecutor`
- `TicketExecutor`
- `MergeExecutor`
- `InboxExecutor`
- `AcceptanceExecutor`

每个 executor 必须：

- 支持 `idempotency_key`
- 返回结构化结果和 runtime ref
- 提供 `reconcile(op)` 能力，便于 crash recovery 后判断 op 是否已经生效

例如：

- `dispatch_ticket` op 的 executor 会调用 ticket/dispatch service
- `run_acceptance` op 的 executor 会调用 acceptance runner，并产出 evidence bundle

## 恢复语义

### daemon 重启

daemon 启动后：

1. 重新装载 `loop_instances`
2. 扫描 lease 是否过期
3. 对 `running` 的 op 调用 executor `reconcile`
4. 若已生效，则补记 `succeeded`
5. 若未生效，则重置为 `planned` 并唤醒对应 instance

### planner / acceptance 超时

超时策略不再决定“任务是否失败”，而只决定“本轮 loop 是否需要恢复”。

恢复动作：

- 记录 `op_timeout` 或 `loop_timeout`
- 保留 journal
- 根据 checkpoint 重启当前 instance
- 若 executor 有进展证明，可续跑；若无进展，再按 retry policy 处理

### graph 级失败回流

当 acceptance node 失败时：

1. `TaskLoop` 标记该 node 为 `failed`
2. 写入 evidence refs
3. 根据 failure classifier 生成 repair / gap / integration node
4. 将祖先复合节点从 `verifying` 回退到 `running`
5. 重新计算 ready frontier

## 命令面设计

为了避免“manager 命令”和“plan 命令”越来越像两套系统，下一阶段建议引入统一 `loop` noun。

### 新命令

- `dalek loop ls`
- `dalek loop show --kind runtime --scope project`
- `dalek loop show --kind task --scope <root-task-id>`
- `dalek loop wake --kind task --scope <root-task-id>`
- `dalek loop tick --kind runtime --scope project`
- `dalek loop tick --kind task --scope <root-task-id>`
- `dalek loop pause --kind task --scope <root-task-id>`
- `dalek loop resume --kind task --scope <root-task-id>`
- `dalek loop journal --kind task --scope <root-task-id>`

### 兼容策略

- `dalek manager status` 继续保留，作为 `runtime` loop 的兼容入口
- `dalek pm state` / `plan.md` 继续保留，作为 `task` loop 的人类视图
- 不新增第二套独立的 `plan daemon`

## 迁移路径

### Phase 1: 抽出 Loop Engine 骨架

- 把现有 daemon manager 调度外壳抽成 `Loop Engine`
- 将现有 manager loop 包装成 `RuntimeLoop`
- 保持现有 CLI 行为兼容

### Phase 2: 引入 TaskLoop

- 让 `plan.json` 成为 `TaskLoop` 的 canonical graph
- 让现有 planner 逻辑迁移成 `TaskLoop.plan` 的一个阶段
- 引入 `loop_op_journal` 和 `loop_checkpoints`

### Phase 3: 接管 acceptance 和 recovery

- 将 acceptance runner 纳入 `TaskLoop`
- 将 planner crash / timeout 恢复迁移到 engine 统一处理
- 用 facts 驱动 graph 状态更新

### Phase 4: 收紧抽象边界

- 所有 feature / milestone / phase 均改为 task graph 复合节点
- 不再允许新增业务专用 loop kind 来表达层级语义

## 风险与权衡

- 如果 `TaskLoop` 设计得过于通用，会重新走向“微型 workflow 语言”；因此节点类型和 completion policy 必须受限。
- 如果把 graph 同时放在 DB 和文件里，极易形成双真相源；因此 graph canonical 必须固定为 `plan.json`。
- 同一项目内串行运行两个 loop 会降低峰值吞吐，但换来状态一致性；这对当前阶段是正确权衡。
- 命令面统一为 `loop` 后，短期内会和现有 `manager` 概念并存，需要兼容期说明。

## 与当前 tickets 的关系

- `t25` 负责引入 `plan.json` 任务图真相源，以及 `TaskLoop` 所需 graph 结构
- `t26` 负责引入 `Loop Engine`、`loop_op_journal`、`checkpoint` 和 `TaskLoop` 调度整合
- `t27` 会把 acceptance runner 正式接入 `TaskLoop`
- `t28` 会补全 loop 健康指标和 integration planning 观察面

## 预期结果

这一阶段完成后，dalek 将具备：

- 一个统一的 loop 基础设施，而不是不断增加定制循环
- 一个抽象的 `TaskLoop`，能驱动任意层级的任务图到完成态
- 清晰的 runtime / task 职责边界，不再互相抢事件或抢写状态
- 基于 checkpoint + journal 的稳定恢复路径
- 面向真实需求完成，而不是只面向 ticket 状态流转的 PM 控制器
