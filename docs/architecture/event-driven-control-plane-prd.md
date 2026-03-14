# Event-Driven Control Plane PRD

## 1. 一句话结论

dalek 当前的主执行平面不是“事件驱动系统”，而是“事件表 + 直接状态机 + 观测日志”混跑。

本 PRD 的目标不是把整个仓库所有字段都 event-sourcing 化，而是把 **主执行平面** 收敛成：

- 事实只写不可变事件
- `ticket / execution / focus / inbox` 的可见状态全部降级为 projection
- scheduler / recovery / batch controller 只读 projection，不再直接维护业务状态机

主执行平面包括：

- `ticket lifecycle`
- `worker execution`
- `wait_user / continue`
- `focus batch`
- `integration / merge observe`

不包括：

- workspace 渲染产物
- notebook / acceptance 等外围读模型
- 已经移除的旧 autopilot / planner loop

## 2. 背景与问题陈述

### 2.1 用户已经踩到的真实问题

1. `t119` 一类问题：
   - ticket 仍然在 `queued`
   - 没有真实 active `task_run`
   - 但 `worker.status=running`
   - queue consumer 误以为这张票已经在执行，导致永远不再消费

2. `wait_user` 问题：
   - worker 提出阻塞后，系统缺少“当前这条人工介入链”的一等建模
   - reply/inbox/ticket/run 之间没有单一真相源
   - 于是会出现 “ticket 看起来恢复了，run 还挂着” 或 “reply 无法稳定绑定当前 block” 的情况

3. `focus batch` 问题：
   - batch 的严格串行语义本来很硬
   - 但 `focus_runs` / `focus_run_items` 目前由 controller 直接改状态
   - `focus_events` 只是审计，不是 authority
   - 所以恢复、重放、重新选主、手工修复时都容易和历史脱节

### 2.2 这不是单点 bug，而是系统性问题

当前系统的根本问题有 5 类：

1. **真相源割裂**
   - 同一个现实事实，可能同时由 `tickets.workflow_status`、`ticket_lifecycle_events`、`workers.status`、`task_runs.orchestration_state`、`focus_run_items.status` 表达

2. **事件语义混型**
   - 有的 event 是事实账本
   - 有的 event 是状态迁移影子
   - 有的 event 是 runtime 观测
   - 有的 event 只是审计
   - 但代码和讨论里经常把它们都叫“事件”

3. **命令与投影未分离**
   - controller / service 直接 `UPDATE status`
   - 然后顺手补一条 event
   - 或者某些路径根本没有 event

4. **调度器相信错了东西**
   - queue / recovery / focus controller 经常直接相信 projection 字段
   - 而这些 projection 又不是严格从事件重建出来的

5. **修复机制继续加重割裂**
   - zombie / recovery / repair 会继续直接写状态
   - 使“修复”本身也无法回放

## 3. 本 PRD 要解决的本质问题

本 PRD 不把问题定义成“把状态字段删掉”。

真正的问题是：

**系统缺少对“事实、观测、命令、投影”四类对象的硬边界。**

因此 PRD 的核心不是新增更多 event，而是把现有对象重新分层：

1. **命令（Command / Intent）**
   - 用户或系统请求做什么
   - 例如：`ticket start`、`focus stop`、`inbox continue`

2. **事实事件（Domain Fact）**
   - 世界里已经发生了什么
   - 不可变、可重放、可审计
   - 例如：`ticket.start_requested`、`execution.run_accepted`、`execution.wait_user_requested`

3. **运行观测（Runtime Observation）**
   - worker 运行时看到了什么
   - 例如：task event、semantic report、runtime sample、stream log
   - 这些不是 ticket 世界的真相

4. **投影（Projection / Read Model）**
   - 给 UI、scheduler、CLI、controller 读取的当前快照
   - 例如：`tickets`、`workers`、`focus_runs`、`focus_run_items`、`inbox_items`

只有把这 4 层拆开，系统才会稳定。

## 4. 产品目标

### 4.1 必达目标

1. 所有主执行状态变化，都必须先落事实事件，再更新 projection
2. `ticket / execution / focus / inbox` 不能再有业务路径直接写状态
3. 系统重启后，projection 可以通过事件重建
4. queue consumer 只根据事件驱动出来的执行投影做判断
5. `wait_user` 的 reply、round count、resume 必须绑定到当前 execution chain
6. `focus batch` 的串行推进必须能从 `focus_events` 单独回放出来
7. repair / recovery 只能通过补偿事件修复世界，不能绕过事件直接改业务状态

### 4.2 非目标

1. 不要求第一版把所有外围表都迁移到统一 event store
2. 不要求第一版删掉所有 snapshot 表
3. 不要求第一版把 stream log、lease、文件偏移这类运行缓存也事件化
4. 不要求恢复已经删除的旧 planner/autopilot 主循环

## 5. 范围

### 5.1 In Scope

1. `ticket` 生命周期推进
2. `start -> queued -> accepted -> active -> blocked/done`
3. `wait_user -> reply -> continue/done`
4. `execution lost -> requeue/escalate`
5. `integration freeze / merge observed / merge abandoned`
6. `focus batch` 严格串行推进与 handoff
7. `inbox` 改造成 projection
8. `workers` / `task_runs` 在主执行平面中的角色重定义

### 5.2 Out of Scope

1. notebook
2. acceptance workspace 文档
3. channel gateway
4. 非 batch 的未来 planner 设计
5. UI 视觉改造

## 6. 设计原则

### 6.1 事件是唯一业务真相源

对主执行平面来说：

- 真相不是 `status` 字段
- 真相不是某一时刻的 view
- 真相只能是 append-only 事实事件

### 6.2 projection 只能被 projector 更新

业务命令不允许：

- 直接写 `tickets.workflow_status`
- 直接写 `workers.status`
- 直接写 `focus_runs.status`
- 直接写 `focus_run_items.status`
- 直接开关 `inbox.status`

这些表只能由 projector 更新。

### 6.3 先写事件，再投影；不能反过来

同一事务内的顺序必须是：

1. 验证 command
2. append domain events
3. projector 消费刚写入的事件
4. 更新 projection
5. 发 wake hook / 通知

禁止：

1. 先写 snapshot，再补 event
2. 无 event 的状态修复
3. 读 snapshot 决定业务，再绕过事件直接修 snapshot

### 6.4 运行观测不是 ticket 世界

以下内容默认都不是业务真相：

- `task_events`
- `task_runtime_samples`
- `task_semantic_reports`
- stream log
- `.dalek/state.json`

它们只能作为：

- worker loop closure 的输入
- runtime 诊断依据
- projector / controller 的辅助证据

只有在 closure / reducer 接纳后，才能变成 domain fact。

### 6.5 repair 只能发补偿事件

允许修复，但修复也必须留下不可变事实。

例如：

- `ticket.repaired`
- `execution.recovered`
- `focus.replayed`

不允许修复 silently 改表。

## 7. 事件类型分层

这是本 PRD 最重要的抽象。

### 7.1 事实事件

这些事件定义业务世界。

包括：

- `ticket lifecycle events`
- 新增的 `execution events`
- 升级后的 `focus events`

### 7.2 运行观测

这些记录 worker/runtime 看到的东西，但不裁决 ticket/focus 世界。

包括：

- `task_events`
- `task_runtime_samples`
- `task_semantic_reports`

### 7.3 投影事件 / 影子事件

当前的：

- `ticket_workflow_events`
- `worker_status_events`

本质上是状态迁移影子，不该再被当 authority。

第一版可以兼容保留，但要降级为：

- 只读审计
- 或由 projector 兼容生成

不能再成为写路径。

### 7.4 运维缓存

这些不是业务真相，也不是 event：

- lease / heartbeat
- runtime log 文件大小
- stream hash
- `RuntimeUpdatedAt`

它们可以继续 mutable，但不能参与 ticket/focus 的 authority 判断。

## 8. 目标聚合与真相源

本 PRD 定义 3 个 authority stream。

### 8.1 Ticket Aggregate

Authority stream：

- `ticket_lifecycle_events`

它描述 ticket 世界里的稳定事实：

- created
- start_requested
- activated
- execution_lost
- requeued
- execution_escalated
- wait_user_reported
- done_reported
- merge_observed
- merge_abandoned
- archived
- repaired

`tickets` 表变成 projection：

- `workflow_status`
- `integration_status`
- `merge_anchor_sha`
- `target_branch`
- `merged_at`
- `abandoned_reason`
- `superseded_by_ticket_id`

### 8.2 Execution Aggregate

Authority stream：

- **新增** `execution_events`

这是当前系统缺失的关键对象。

它回答的问题是：

- 当前 ticket 正在跑的是哪一条 execution chain
- 这条 chain 已经 wait_user 过几次
- 最新一次 human reply 是什么
- 当前 active run 是哪一个
- 当前 chain 是 running / blocked / done / escalated / superseded

#### 8.2.1 为什么必须新增 execution stream

当前系统的问题就在于：

- `ticket.active` 和 `worker.running` 不足以表达 execution 真相
- `task_run` 只表达某一次 run，不表达跨 reply / retry / wait_user 的链
- `inbox` 也不是 execution 真相

所以必须引入 `execution chain` 这一层。

#### 8.2.2 execution chain 的定义

每次“从 backlog/blocked 进入一轮新的可推进执行”时，打开一条新 chain。

推荐键：

- `chain_id`
- `ticket_id`
- `origin_ticket_lifecycle_sequence`

规则：

1. `ticket start` 从 backlog/blocked 发起时，创建新 chain
2. 同一 chain 可跨多次 run、retry、wait_user、reply、resume
3. 自动 requeue 不切新 chain
4. 用户明确重新启动一个已 blocked 的 ticket 时，切新 chain

#### 8.2.3 execution 事实事件

第一版最小事件集：

- `execution.chain_opened`
- `execution.run_requested`
- `execution.run_accepted`
- `execution.wait_user_requested`
- `execution.human_reply_recorded`
- `execution.resume_requested`
- `execution.done_closed`
- `execution.blocked_closed`
- `execution.lost`
- `execution.escalated`
- `execution.canceled`
- `execution.superseded`

### 8.3 Focus Aggregate

Authority stream：

- `focus_events`

但语义必须改变：

- 现在它是“审计事件”
- 目标是“事实事件账本”

`focus_runs` / `focus_run_items` 变成 projection。

#### 8.3.1 focus 事实事件

第一版最小事件集：

- `focus.run_created`
- `focus.run_desired_state_changed`
- `focus.item_selected`
- `focus.item_execution_requested`
- `focus.item_execution_bound`
- `focus.item_blocked`
- `focus.item_handoff_created`
- `focus.item_handoff_resolved`
- `focus.item_completed`
- `focus.item_stopped`
- `focus.item_canceled`
- `focus.run_completed`
- `focus.run_stopped`
- `focus.run_canceled`

## 9. 现有表的重定位

### 9.1 保留为 authority

1. `ticket_lifecycle_events`
2. 新增 `execution_events`
3. `focus_events`（语义升级）

### 9.2 保留为 runtime observation

1. `task_events`
2. `task_runtime_samples`
3. `task_semantic_reports`

### 9.3 保留为 projection

1. `tickets`
2. `workers`
3. `task_runs`
4. `focus_runs`
5. `focus_run_items`
6. `inbox_items`

### 9.4 降级 / 逐步淘汰

1. `ticket_workflow_events`
2. `worker_status_events`

它们第一版可以继续留着，但只能：

- 由 projector 兼容生成
- 或只作为历史审计

不能再被业务逻辑读为 authority。

## 10. 关键 read model 设计

### 10.1 Ticket Projection

存放在现有 `tickets` 表。

投影来源：

- `ticket_lifecycle_events`

它负责输出：

- workflow
- integration
- merge anchor
- target ref
- supersede 关系

### 10.2 Execution Projection

新增 projection，建议表名：

- `execution_chains`

字段建议：

- `chain_id`
- `ticket_id`
- `status`
- `current_task_run_id`
- `current_worker_id`
- `wait_user_round_count`
- `last_reply_markdown`
- `last_reply_action`
- `last_block_reason`
- `opened_at`
- `updated_at`
- `closed_at`

这是 scheduler / inbox projector / focus projector 的主要输入。

### 10.3 Worker Projection

继续使用 `workers` 表，但角色改变为：

1. 资源注册表
   - worktree_path
   - branch
   - log_path

2. 运行时快照
   - status
   - runtime_updated_at
   - retry_count

其中：

- `status` 不再是 authority
- 它由 `execution_events + runtime liveness` 投影出来

### 10.4 Inbox Projection

`inbox_items` 变成纯 projection。

它不再是真相源。

它只来源于：

- `execution.wait_user_requested`
- `execution.escalated`
- `execution.human_reply_recorded`
- `focus.item_blocked`

特别是：

- “open / done / snoozed” 这种 UI 状态也必须通过 inbox command -> fact event -> projection 完成
- 不能再直接 `UPDATE inbox_items.status`

### 10.5 Focus Projection

`focus_runs` / `focus_run_items` 只由 `focus_events` 投影。

controller 不再直接写：

- `focus_runs.status`
- `focus_run_items.status`

## 11. 四条关键链路

### 11.1 `start -> queue -> accepted -> active`

目标规则：

1. `ticket start` 只追加事实，不直接改业务状态
2. `queued` 只表示“等待调度资源”
3. “是否已经在执行”只由 `execution.run_accepted` 决定
4. 裸 `worker.status=running` 不能阻止 queue 再消费

目标事件流：

1. command: `StartTicket(ticket_id)`
2. append:
   - `ticket.start_requested`
   - `execution.chain_opened`
   - `execution.run_requested`
3. projector:
   - `tickets.workflow_status = queued`
   - `execution_chains.status = queued`
4. scheduler 消费 queued execution
5. append:
   - `execution.run_accepted`
   - `ticket.activated`
6. projector:
   - `tickets.workflow_status = active`
   - `execution_chains.status = active`
   - `workers.status = running`

### 11.2 `wait_user -> reply -> continue / done`

目标规则：

1. `wait_user` 的 authority 是 execution chain，不是 inbox
2. round count = 该 chain 上 `execution.wait_user_requested` 的次数
3. reply 绑定 chain，不绑定“当前某条 open inbox”
4. worker 看见的 reply 是 chain 上最新的 reply 事实，不是某个临时状态字段

目标事件流：

1. worker report / closure 接纳 runtime 观测
2. append:
   - `execution.wait_user_requested`
   - `ticket.wait_user_reported`
3. projector:
   - `execution_chains.status = blocked`
   - `execution_chains.wait_user_round_count += 1`
   - `tickets.workflow_status = blocked`
   - `inbox_items` 打开 blocker
4. user command:
   - `InboxContinue(chain_id, reply_markdown, action)`
5. append:
   - `execution.human_reply_recorded`
   - `execution.resume_requested` 或 `execution.done_closed`
6. projector:
   - 更新 `execution_chains.last_reply_*`
   - 关闭对应 inbox projection
7. scheduler / focus controller 再决定是否重新请求 run

### 11.3 `execution lost -> requeue / escalate`

目标规则：

1. 恢复器不能直接把 ticket 改成 queued/blocked
2. 必须先写 `execution.lost`
3. 再由 convergence reducer 根据 retry policy 追加：
   - `ticket.requeued`
   - 或 `ticket.execution_escalated`

目标事件流：

1. detector 发现 active chain 丢失
2. append `execution.lost`
3. reducer 判定：
   - 可重试：append `ticket.requeued` + `execution.run_requested`
   - 已耗尽：append `execution.escalated`
4. projector 更新 ticket / inbox / retry projection

### 11.4 `focus batch` 严格串行推进

目标规则：

1. `focus` 不是例外，必须事件驱动
2. 当前 item 的推进只取决于：
   - `focus_events`
   - 绑定 ticket 的 `ticket/execution` projection
3. 下一 item 只有在当前 item `completed` 后才能选中

目标事件流：

1. command: `FocusStart(scope=[...])`
2. append `focus.run_created`
3. append `focus.item_selected(seq=1)`
4. append `focus.item_execution_requested`
5. 绑定到 ticket execution chain 后，append `focus.item_execution_bound`
6. 若 ticket blocked / handoff / merge conflict，append `focus.item_blocked` 或 `focus.item_handoff_created`
7. 若当前 item 收口，append `focus.item_completed`
8. projector 选中下一 pending item

## 12. `wait_user` 的特殊规则

### 12.1 round count

`round_count` 不由 worker loop 维护，不由 inbox 维护，而是：

- 由 execution chain projection 统计
- 统计规则是该 chain 上 `execution.wait_user_requested` 的累计次数

### 12.2 三轮上限

默认：

- 同一 chain 超过 3 次 `wait_user_requested`
- 不能再自动 `resume_requested`
- 必须进入 `execution.escalated`

### 12.3 reply 格式

用户回复保持最小：

- 一段原始 markdown
- 一个 action：`continue | done`

用户在 reply 里自己写文件路径。

worker 在 resume prompt 里必须先做 verify：

1. reply 是否足以推进
2. reply 中提到的路径是否可读
3. 若不可读，再次 `wait_user_requested`

## 13. 强约束

### 13.1 调度器约束

queue consumer / focus controller / recovery 只能基于 projection 做决策，且 projection 必须来自 authority events。

它们不能：

- 直接看 `workers.status==running` 就断言 ticket 正在执行
- 直接用 `focus_run_items.status` 当未经过事件支撑的真相

### 13.2 单写者约束

每个聚合只能有一个写入口：

- ticket：ticket lifecycle command handler
- execution：execution command / closure reducer
- focus：focus controller command handler

### 13.3 事件幂等约束

所有 authority event 需要：

- `stream_id`
- `stream_seq`
- `idempotency_key`
- `causation_id`
- `correlation_id`

至少要保证：

1. 同一命令重放不会重复推进世界
2. projector 可以断点续投
3. 重启后可以安全重放

## 14. 迁移方案

### Phase 0：冻结错误写法

目标：

1. 禁止新增任何直接写 `status` 的业务路径
2. 文档中明确哪些表是 projection
3. 给现有 direct write 路径打迁移标签

### Phase 1：补 execution authority

目标：

1. 新增 `execution_events`
2. 新增 `execution_chains` projection
3. queue consumer 改为只看 execution projection
4. `wait_user/reply` 首先落 execution events

这是最关键阶段，直接解决：

- `t119`
- wait_user 断链
- stale worker 卡队列

### Phase 2：把 focus 从状态机改成事件账本

目标：

1. `focus_events` 升级为 authority
2. `focus_runs/items` 改成 projector 输出
3. controller 不再直接写 status

### Phase 3：把 inbox 改成纯 projection

目标：

1. reply 不再以 inbox 为真相源
2. inbox close / snooze 也改走 event
3. 所有 open inbox 都可从 authority stream 重建

### Phase 4：清理影子事件与 repair-only 入口

目标：

1. `ticket_workflow_events` 停止作为写路径依赖
2. `worker_status_events` 停止作为 authority
3. `SetTicketWorkflowStatus` 等入口只保留迁移期 repair，用 feature flag 管控

## 15. 验收标准

### 15.1 t119 类问题

给定：

- ticket 是 `queued`
- worker projection 意外显示 `running`
- 但没有 `execution.run_accepted`

则：

- queue consumer 仍然必须消费该 ticket
- 不允许再被 stale worker 卡死

### 15.2 wait_user 链路

给定：

- 同一 chain 连续 3 次 `execution.wait_user_requested`

则：

- round count 必须可由事件重建
- 第 4 次自动恢复必须被拒绝并升级

### 15.3 focus 串行

给定：

- focus run 中 item1 blocked / handoff / merge conflict

则：

- item2 不得提前启动
- 重启 daemon 后从 `focus_events` 重放，结果必须一致

### 15.4 rebuild

给定：

- 清空 projection 表后
- 从 authority events 重建

则：

- `tickets`
- `execution_chains`
- `focus_runs`
- `focus_run_items`
- `inbox_items`

必须恢复到一致状态。

## 16. 风险与权衡

### 16.1 风险

1. 当前文档与代码存在漂移，迁移时必须以代码为准
2. `task_runs` 当前也承担部分 execution identity，需要小心切边界
3. 历史 repair 逻辑很多，迁移期双写容易出错

### 16.2 权衡

本 PRD 选择：

- 不追求“一张 event 大表统一宇宙”
- 追求“按聚合明确 authority stream”

原因：

1. 更容易和当前代码对接
2. 不会把迁移范围一次性炸穿
3. 已足够满足“事件是唯一业务真相源”的原则

## 17. 最终判断

当前系统的核心问题不是“状态字段太多”，而是：

- 没有 execution chain
- 没有把 focus 当 authority stream
- 没有把 inbox 彻底降级为 projection
- 没有把 task observation 和 domain fact 分开

因此，正确的改造不是“继续修状态机”，而是：

1. 保住 `ticket_lifecycle_events`
2. 新增 `execution_events`
3. 升级 `focus_events`
4. 全部业务状态改为投影

这才是把系统从“状态驱动”收回到“事件驱动”的最小完整路径。
