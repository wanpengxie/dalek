# Focus Run Daemon-Owned 架构设计

## 1. 结论

`focus run` 必须改成 **daemon-owned execution**。

这句话在本项目里有 6 个硬含义：

1. 只有 daemon 可以推进 `focus run`。
2. CLI / Web 只能提交意图、读取状态、请求停止、轮询事件。
3. `run/stop/tail` 没有 daemon 时直接失败。
4. `show/status` 可以做本地只读降级，但必须显式标记为 `readonly-stale`，且绝不能推进任何状态。
5. `focus` 运行时活性判断以 `task_run + execution host loop handler` 为主。
6. `focus` 的业务门槛判断以现有 lifecycle 投影后的 ticket snapshot 为主，而不是再造一套 focus 自己的 reducer。

当前实现范围只覆盖 `batch focus v1`。
`plan` 后续复用同一套骨架，但不纳入本次实现与验收范围。

这份设计对齐以下既有文档：

- [PM Loop 重建方案](/Users/xiewanpeng/agi/dalek/docs/architecture/pm-loop-rebuild.md)
- [Worker Loop 控制面当前改造文档](/Users/xiewanpeng/agi/dalek/docs/architecture/worker-loop-control-plane-spec.md)
- [Ticket Lifecycle and Merge Redesign](/Users/xiewanpeng/agi/dalek/docs/architecture/ticket-lifecycle-merge-redesign.md)
- [.dalek/agent-kernel.md](/Users/xiewanpeng/agi/dalek/.dalek/agent-kernel.md)

## 2. 为什么要重做

当前 `focus` 失败的根因不是某个 bug，而是执行权分裂：

1. `manager run --mode batch` 在 CLI 进程里直接跑本地 loop。
2. 真正有 `workerRunSubmitter`、`queue consumer`、`execution host` 的 PM service 在 daemon 进程里。
3. `stop` 依赖进程内 `focusCancelFn`，天然不能跨进程。
4. `focus` 直接轮询 ticket snapshot，却没有和现有 `execution_lost / closure / lifecycle / integration` 主路径形成一个统一控制面。
5. 旧设计默认 PM agent 可以直接解 merge conflict，这和内核的 ownership boundary 冲突。

所以这次不是“把 loop 挪到 daemon”这么简单，而是要把：

- 执行权
- 状态真相
- 停止恢复
- merge ownership

一次性统一。

## 3. 硬边界

### 3.1 daemon 边界

- `manager run`
- `manager stop`
- `manager stop --force`
- `manager tail`

以上命令必须依赖 daemon。

没有 daemon 时：

- 这些命令直接报错
- 报错文案必须明确指出“focus run 依赖 daemon”
- 不能回退到任何本地 loop / 本地 queue consumer / 本地 PM service 执行路径

### 3.2 只读降级边界

`manager show/status` 可以在 daemon 不可用时降级为本地只读查询，但必须满足：

- 输出显式标记 `readonly-stale`
- 只读路径不能修改任何 `focus` 状态
- 只读路径不能推断增量事件
- daemon 恢复可用后，daemon 结果总是比本地降级结果优先

### 3.3 PM ownership 边界

按内核硬约束：

- PM 允许做集成动作
- PM 不允许直接修改产品实现文件
- 当 merge conflict 涉及产品实现文件时，PM 不得手工解冲突

因此：

- `focus` 可以驱动 merge
- 但不能让 PM agent 在 repo root 任意改冲突文件然后提交 merge
- 如果冲突只涉及集成允许范围内的文件，允许 daemon-side integration actor 解决
- 如果冲突涉及产品实现文件，必须 `abort merge`，并创建 integration ticket 交给 worker 处理

## 4. 核心原则

### 4.1 单写者原则

daemon 内必须只有一个 project 级 `FocusController` 逻辑角色。

它不要求是新的常驻 goroutine；
`batch focus v1` 直接挂到现有 daemon manager 的 `runTickProject + wakeCh` 框架上即可。

单写者规则：

- API handler 只能创建 run、写入 stop/cancel 意图、追加事件、触发 wake
- `FocusController` 才能推进 `focus_runs` / `focus_run_items` / `focus_run_item_attempts` 的业务状态
- recover 也必须复用同一个 controller 路径，不允许另一套平行恢复器直接改 focus 状态

### 4.2 真相分层

新的 `focus` 必须明确四层真相：

1. **运行时真相**
   - `task_runs`
   - daemon `execution host` loop handler
   - execution loss / runtime convergence 结果

2. **生命周期真相**
   - worker loop closure
   - `ticket_lifecycle_events`
   - `ticket.workflow_status`
   - `ticket.integration_status`
   - `merge_anchor_sha`
   - `target_branch`

3. **批次控制真相**
   - `focus_runs`
   - `focus_run_items`
   - `focus_run_item_attempts`

4. **批次事件真相**
   - `focus_events`

读取规则：

- 运行时活性判断读第 1 层
- 业务是否可 merge / 是否 blocked / 是否已 merged 读第 2 层
- 当前 item、当前 attempt、stop/cancel/recover 进度读第 3 层
- CLI tail / 审计 / recover 追踪读第 4 层

`focus_events` 不是业务状态真相，但它是必需的控制面审计日志，不是可选对象。

## 5. 数据模型

## 5.1 `focus_runs`

```go
type FocusRun struct {
    ID             uint
    ProjectKey     string
    Mode           string // batch

    RequestID      string // start 请求幂等键

    DesiredState   string // running | stopping | canceling
    Status         string // queued | running | blocked | completed | stopped | failed | canceled

    ScopeTicketIDs string // JSON: [1,2,3]
    ActiveSeq      *int
    ActiveTicketID *uint
    CompletedCount int
    TotalCount     int

    AgentBudget    int
    AgentBudgetMax int

    Summary        string
    LastError      string
    StartedAt      *time.Time
    FinishedAt     *time.Time
}
```

状态定义：

- `queued`: run 已创建，但 controller 尚未开始推进
- `running`: controller 正在推进
- `blocked`: 当前 run 因用户输入、integration ticket、预算耗尽或系统事故而停止推进
- `completed`: 全部 item 已完成
- `stopped`: graceful stop 结束
- `failed`: run 自身失败
- `canceled`: force cancel 结束

`active focus` 的定义：

- `queued`
- `running`
- `blocked`

只有这三个状态参与“同项目最多一个 active focus”唯一约束。

`DesiredState` 只允许：

- `running`
- `stopping`
- `canceling`

合法迁移：

- `queued -> running | canceled | failed`
- `running -> blocked | completed | stopped | failed | canceled`
- `blocked -> running | stopped | failed | canceled`

### 5.2 `focus_run_items`

```go
type FocusRunItem struct {
    ID             uint
    FocusRunID     uint
    Seq            int
    TicketID       uint

    Status         string // pending | starting | queued | executing | triage | waiting_user | merging | awaiting_merge_observation | blocked | completed | stopped | failed | canceled

    CurrentAttempt int
    RestartCount   int
    ConflictCount  int

    LastOutcome    string
    LastError      string

    StartedAt      *time.Time
    FinishedAt     *time.Time
}
```

注意：

- item 不再持有单个 `WorkerID/TaskRunID`
- 当前 attempt 的运行上下文转移到 append-only 的 `focus_run_item_attempts`
- item 的 `completed` 表示“focus 视角完成”，不是“ticket 自己写了 merged 字段”

### 5.3 `focus_run_item_attempts`

```go
type FocusRunItemAttempt struct {
    ID            uint
    FocusRunID    uint
    FocusRunItemID uint

    Attempt       int
    Kind          string // start | adopt | restart
    Status        string // created | queued | accepted | running | lost | requeued | blocked | done | canceled | failed

    WorkerID      *uint
    TaskRunID     *uint

    RequestID     string
    LastError     string
    StartedAt     *time.Time
    FinishedAt    *time.Time
}
```

这个表是必须的。

用途：

- 区分历史 attempt 和当前 attempt
- 支撑 adopt queued/active ticket
- 支撑 restart / cancel / recover
- 防止旧 run 晚到的 closure 污染当前现场

### 5.4 `focus_events`

```go
type FocusEvent struct {
    ID          uint
    FocusRunID   uint
    FocusItemID *uint
    AttemptID   *uint

    Kind        string
    Summary     string
    PayloadJSON string
    CreatedAt   time.Time
}
```

`focus_events` 是必需对象。

最少事件：

- run.created
- run.desired_state_changed
- item.selected
- item.start_requested
- item.adopted
- attempt.created
- attempt.accepted
- attempt.requeued
- attempt.blocked
- attempt.done
- attempt.canceled
- attempt.failed
- merge.started
- merge.conflict_integration_only
- merge.conflict_requires_integration_ticket
- merge.aborted
- merge.observed

### 5.5 约束

必须有数据库级约束：

1. 同一 `project_key` 同时最多一个 active `focus_run`
2. `focus_run_items(focus_run_id, seq)` 唯一
3. `focus_run_item_attempts(focus_run_item_id, attempt)` 唯一
4. `focus_events(id)` 作为单调递增 cursor
5. merge 执行必须受 project repo lock 保护
6. `FocusStart` 必须拒绝：
   - 空 scope
   - 重复 ticket ID
   - 不存在的 ticket ID

## 6. daemon API

focus 相关 API 收敛为：

- `FocusStart(project, mode, scope, budget, request_id)`
- `FocusGet(project, focus_id)`
- `FocusGetActive(project)`
- `FocusStop(project, focus_id, request_id)`
- `FocusCancel(project, focus_id, request_id)`
- `FocusPoll(project, focus_id, since_event_id)`

规则：

1. `FocusStart` 返回稳定的 `focus_id`
2. 后续 stop/cancel/poll 都以 `focus_id` 为主
3. `request_id` 用于幂等
4. API handler 只做：
   - 参数校验
   - 创建 run / 写 desired_state
   - 追加 `focus_event`
   - `NotifyProject(project)`
5. API handler 不直接推进 item 状态

`manager run` 的默认交互：

1. 调 `FocusStart`
2. 拿到 `focus_id`
3. 进入 `FocusPoll(focus_id, since_event_id)` 轮询 tail
4. `Ctrl+C` 只退出 tail，不隐式 stop

## 7. 唤醒与调度

`batch focus v1` 直接复用现有 daemon manager 框架。

controller 推进来源：

1. 标准 manager tick
2. 明确的 wake 事件

必须存在的 wake 生产者：

- `FocusStart`
- `FocusStop`
- `FocusCancel`
- execution host `OnRunSettled`
- execution loss / runtime convergence 收口完成
- worker loop closure 导致 workflow 变化
- integration `SyncRef/RescanMergeStatus`
- daemon recovery 发现 active focus

如果上述任一生产者缺失，设计就会退化成“只能等周期 tick”。

## 8. item 执行模型

### 8.1 item 状态机

```text
pending
  -> starting
  -> queued
  -> executing
  -> triage | merging | waiting_user | blocked | failed
  -> awaiting_merge_observation
  -> completed | stopped | canceled
```

### 8.2 start / adopt / accepted

`StartTicket` 只负责：

- 创建/修复 worker
- 把 ticket 推到 `queued`

它不代表已经拿到 `task_run_id`。

因此 item 流程必须分开：

1. `pending -> starting`
   - controller 选择 ticket
   - 写 `item.selected`

2. `starting -> queued`
   - 调 `StartTicket`
   - 创建 `attempt(kind=start, status=queued)`
   - 写 `item.start_requested`

3. `queued -> executing`
   - 只有 execution host 真正 accepted 某次 run
   - 才回填 `attempt.task_run_id`
   - attempt 进入 `accepted/running`
   - item 进入 `executing`

### 8.3 adopt 现有 queued/active ticket

如果 focus 启动时 scope 中某张 ticket 已经是 `queued` 或 `active`：

- controller 不重复 `StartTicket`
- 创建 `attempt(kind=adopt)`
- 绑定当前 worker / task_run
- 继续从当前现场推进

### 8.4 runtime 异常不是 focus 自己的第一收口器

当出现：

- execution lost
- lease 超时
- loop handler 丢失
- zombie runtime

第一责任人仍然是现有：

- execution convergence
- zombie checker
- recovery
- ticket lifecycle 投影

focus 不应自己绕开这条正式主路径，直接在 item 内发明一套重试/升级规则。

正确顺序是：

1. 运行时异常先走现有 `execution_lost -> requeued / execution_escalated`
2. ticket snapshot 收口到 `queued` 或 `blocked`
3. focus 再根据 lifecycle 结果决定：
   - 重新进入下一次 attempt
   - 进入 `triage`
   - 进入 `blocked`

### 8.5 triage

`batch focus v1` 的 triage 只允许：

- `restart`
- `wait_user`

`accept_done` 不在 v1 范围内。

这意味着：

- “代码已可交付，但运行失败只是环境问题”这一类 case
- 在 v1 中明确视为 `wait_user / blocked`
- 不允许用新的 shortcut 绕回“跳过 closure 直接 merge”

## 9. Merge 模型

### 9.1 merge 的位置

merge 必须发生在 daemon 内 repo executor 上。

原因：

- repo root 是 project 共享资源
- merge 需要 recover、stop、审计
- merge 冲突要和 focus item 状态一致

### 9.2 merge 成功标准

以下条件必须同时满足：

1. 当前 target branch checkout 成功，且不是 detached HEAD
2. 不存在 unmerged files
3. `.git/MERGE_HEAD` 不存在
4. `git status --porcelain` 为空
5. integration observe 看到 `merge_anchor_sha` 已被 target branch 包含

第 5 条仍然是 ticket `merged` 的唯一真相来源。

### 9.3 冲突处理边界

冲突分两类：

1. **仅集成允许范围内的冲突**
   - 允许 daemon-side integration actor 解决
   - 例如 PM 文档、设计文档、集成元数据

2. **涉及产品实现文件的冲突**
   - 必须 `git merge --abort`
   - 必须创建 integration ticket
   - 原 item 进入 `blocked`
   - PM 不允许手工改产品文件后提交 merge

### 9.4 merge 后推进

merge actor 只做两件事：

1. 改变 git 现实
2. 追加 focus 事件

它不直接做：

- `UPDATE tickets SET integration_status='merged'`
- 直接写 `merged_at`

落地后必须立刻触发：

- `SyncRef/RescanMergeStatus`
- 或等价的 integration wake

然后 item 进入 `awaiting_merge_observation`。

只有 observe 成功后，item 才能进入 `completed`。

## 10. Stop / Cancel / Recover

### 10.1 graceful stop

`manager stop`：

- 只把 `desired_state` 改成 `stopping`
- 追加 `run.desired_state_changed`
- `NotifyProject`

controller 在步骤边界处理：

- `pending`: 直接 `stopped`
- `starting/queued`: 不再发起新 attempt，当前 item 收口后 `stopped`
- `executing`: 等当前 attempt 通过正式主路径收口
- `merging/awaiting_merge_observation`: 等当前 merge 尝试结束后停
- `blocked`: 直接 `stopped`

### 10.2 force cancel

`manager stop --force`：

- 把 `desired_state` 改成 `canceling`
- 追加 `run.desired_state_changed`
- `NotifyProject`

controller 的映射规则：

- `pending`: item 直接 `canceled`
- `starting`: item 直接 `canceled`
- `queued`: 尝试取消已绑定 ticket loop；若尚无 loop，则 item `canceled`
- `executing`: 先 `CancelTicketLoop`，再以 `CancelTaskRun` 作为兜底
- `merging/conflict`: 先 `git merge --abort`，再 item `canceled` 或 `failed`
- `awaiting_merge_observation`: 不再继续下一步，item `canceled`

force cancel 必须幂等。

### 10.3 recover

daemon 启动后：

1. 通过现有 daemon recovery 入口扫描 active focus
2. 对每个 active focus 调 `NotifyProject`
3. controller 读取：
   - run / item / attempt
   - task runtime
   - loop handler
   - ticket snapshot
   - repo root merge 现场
4. 恢复到单一 controller 路径继续推进

recover 不能另起一套平行状态机。

### 10.4 升级切换门槛

从旧版本地 loop 切到 daemon-owned v1 时，必须有 rollout gate：

1. 检查当前项目是否存在 legacy active focus
2. 若存在，必须先 drain / stop 到 idle
3. v1 不做 legacy active focus 的自动 backfill 接管
4. gate 通过后，才允许启用新的 daemon-owned focus

否则升级窗口会出现 orphan focus 或错误恢复。

## 11. 迁移顺序

正确顺序必须是：

1. **schema 先行**
   - `focus_runs`
   - `focus_run_items`
   - `focus_run_item_attempts`
   - `focus_events`
   - active focus 唯一约束

2. **daemon API**
   - `FocusStart/Get/GetActive/Stop/Cancel/Poll`
   - request_id / focus_id 约束

3. **controller 挂载**
   - 挂到现有 daemon manager tick / wake
   - 补全 wake 生产者

4. **recover / merge / lifecycle 对接**
   - attempt adopt
   - execution_lost 主路径对接
   - integration observe wake

5. **CLI 切换**
   - `manager run`
   - `manager stop`
   - `manager stop --force`
   - `manager tail`
   - `manager show/status` 的只读降级

6. **rollout gate**
   - 拒绝在 legacy active focus 未清空时切换

7. **删除旧实现**
   - CLI 本地 loop
   - 本地 queue consumer
   - 进程内 `focusCancelFn`

如果顺序反过来，一定会被迫加临时兼容层，然后重新制造 split-brain。

## 12. 必须删除的旧设计

以下设计必须暴力删除：

1. CLI 本地 `RunBatchFocus(...)`
2. `TicketStarter` 回调注入式启动
3. `RunBatchFocus(...)` 里本地 `StartQueueConsumer(...)`
4. `focusCancelFn` / `softStop chan`
5. 通过轮询 `ticket.workflow_status` 驱动整个 focus
6. 没有严格 closure guard 的 `accept_done`
7. PM 直接解决产品文件 merge conflict
8. merge 成功后直接写 `integration_status=merged`
9. “没有 daemon 时本地凑合跑”的 fallback loop

## 13. 验收标准

### 13.1 总原则

验收必须全部在：

- 真实 daemon
- 真实 git repo
- 真实 worktree

场景完成。

禁止：

- fake `TicketStarter`
- fake submitter
- 直接改 DB 模拟 merged
- 直接改 DB 模拟 task done
- 跳过 daemon 只在 CLI 进程内跑

### 13.2 必过场景

1. 基础 happy path
   `manager run --mode batch --tickets a,b` 创建 focus，真实 `task_run` 出现，第一张 ticket `done + needs_merge`，merge 落地并立即触发 integration observe，ticket `merged` 后第二张才开始，run 最终 `completed`。

2. 跨进程 graceful stop
   一个 CLI 在 tail，另一个 CLI 执行 `manager stop`，当前 item 收口后 run `stopped`，不会启动下一张 ticket。

3. 跨进程 force cancel
   一个 CLI 执行 `manager stop --force`，按状态映射真正取消 loop/task/merge，run 最终 `canceled` 或 `failed`。

4. stop intent 持久化恢复
   `manager stop` 或 `manager stop --force` 发出后，当前 item 尚未收口时 daemon 重启；重启后 stop/cancel 意图不能丢失，controller 必须继续按原意图推进到 `stopped/canceled/failed`。

5. activation 失败路径
   queued ticket 激活失败时，必须先走现有 `execution_lost / requeued / escalated` 主路径；focus 不会无限挂在 queued。

6. adopt queued/active
   focus 启动时 scope 中 ticket 已经 `queued/active`，controller 能 adopt 当前 attempt，不重复 start。

7. restart after execution_lost
   runtime 异常后，经现有 convergence 收口，再由 focus 决定 restart；旧 run 晚到的 closure 不得污染新 attempt。

8. integration-only conflict
   仅涉及集成允许范围内的冲突，daemon-side integration actor 可解决，最终 observe merged。

9. product-file conflict
   一旦冲突涉及产品实现文件，必须 `abort merge` + 创建 integration ticket；PM 不得手工解冲突。

10. merge observe wake
   merge 落地后必须立即触发 integration observe / wake，而不是只能等周期 tick。

11. daemon restart recover
   focus 执行中途 daemon 重启，controller 通过既有 recovery 入口恢复，不重复启动已完成 item，不丢失 merge/conflict 现场。

12. 并发 focus 拒绝
   同项目已存在 active focus 时，第二次 `manager run` 被拒绝。

13. scope 校验
   空 scope、重复 ticket ID、不存在的 ticket ID 全部被 daemon API 明确拒绝，不创建脏记录。

14. 无 daemon 边界
   `run/stop/tail` 直接报错；`show/status` 如做降级，必须只读且明确标记 `readonly-stale`。

15. 升级切换 gate
   存在 legacy active focus 时，不允许切换到 daemon-owned v1；必须先 drain 到 idle。

## 14. 最终判断标准

只看一句话：

**当 CLI 全部退出时，daemon 仍然能独立拥有、推进、停止、恢复这次 focus run；而当 daemon 不存在时，`run/stop/tail` 完全不可用，本地最多只能做只读诊断；而当 merge conflict 触及产品实现文件时，PM 也不能越权修改产品代码。**

做到这三点，`focus` 才算和当前系统架构真正一致。
