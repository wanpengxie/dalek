# Worker Loop 控制面当前改造文档

> 这不是抽象 spec，也不是新架构设计稿。
>
> 这份文档只回答一件事：
>
> **基于当前代码，我现在到底要改什么。**

## 1. 最小模型

系统里只保留 4 个东西：

1. `ticket`
   任务包。

2. `worker loop handler`
   当前这张 ticket 在线运行时的唯一控制真相。
   它持有：
   - `ctx`
   - `cancel`
   - `ticket_id`
   - `worker_id`
   - 当前 daemon 的控制权

3. `worker` 自维护状态
   - `.dalek/agent-kernel.md`
   - `.dalek/state.json`

4. `closure`
   只在 `worker loop` 准备退出时检查：
   - `done` 合不合法
   - `wait_user` 合不合法
   - 缺 report / 非法 report 怎么处理

基于这个模型：

- 不新增独立的 `raw run handler`
- `raw run` 直接继承 `worker loop handler` 的 `ctx`
- 系统不接 `continue` 的语义
- 系统不支持“暂停一个 raw run，之后再恢复同一个 raw run”
- 中断/取消针对的是 `worker loop`
- daemon 重启后的恢复，是基于现存 `kernel/state/worktree` 启动新的 loop，不是恢复旧 raw run

## 2. 这份改造到底解决什么

### 2.1 `done` 假终态

现在的风险是：

- worker 报了 `done`
- PM reducer 可能直接把 ticket 推到 `done`
- 但本地 `phase/state` 其实没全收口

这部分由 `closure` 解决：

- `report=done` 只是候选终态
- 只有 closure 通过，ticket 才能真的进 `done`

### 2.2 `ticket.workflow_status` 不是事实，只是投影

在最小模型下，这个问题收缩成两条规则：

1. `active` 由 `worker loop handler` 的 claim / release 投影
2. `done / blocked` 由 `closure` 投影

因此：

- 已经有真实在线 loop，但 ticket 还在 `backlog/queued`
  - 由 `loop handler claim -> active` 解决
- loop 已经死了，但 ticket 还挂着 `active`
  - 由 `loop handler release/dead/supersede -> active 失效` 解决
- `done` 过早
  - 由 `closure` 解决

## 3. 当前代码里的真实锚点

### 3.1 现有 loop handler 基础已经存在

daemon 里已经有内存态执行句柄：

- [execution_host_types.go](/Users/xiewanpeng/agi/dalek/internal/services/daemon/execution_host_types.go)
- [execution_host_runner.go](/Users/xiewanpeng/agi/dalek/internal/services/daemon/execution_host_runner.go)
- [execution_host.go](/Users/xiewanpeng/agi/dalek/internal/services/daemon/execution_host.go)

当前 `executionRunHandle` 已经持有：

- `ctx`
- `cancel`
- `ticketID`
- `workerID`
- `runID`

而且 `executeTicketRun(...)` 已经把这个 `ctx` 传进：

- [RunTicketWorker](/Users/xiewanpeng/agi/dalek/internal/services/daemon/execution_host_runner.go#L61)

所以：

**现在不是缺 handler，而是没有把它提升成 worker loop 的控制真相。**

### 3.1.1 handler 需要补哪些字段

在现有 `executionRunHandle` 上补最小控制字段：

- `phase`
  - `claimed`
  - `running`
  - `closing`
  - `canceling`
- `cancel_requested_at`
- `last_error`

注意：

- 这里的 `phase` 不是 worker 语义 phase
- 它只是 loop 的在线控制状态
- 不负责表达业务进展

### 3.1.2 `ExecutionHost` 需要补什么索引

除了现有：

- `runs map[runID]*executionRunHandle`
- `requests map[requestID]*executionRunHandle`

还要补一个 ticket 级索引：

- `ticketLoops map[project/ticketID]*executionRunHandle`

它的作用只有两个：

1. 强制同一 ticket 同时只有一个 live loop handle
2. 提供 ticket 级 probe / cancel 入口

### 3.1.3 handler 需要暴露什么接口

`ExecutionHost` 增加两个接口：

- `ProbeTicketLoop(project, ticketID)`
- `CancelTicketLoop(project, ticketID)`

`ProbeTicketLoop(...)` 返回的只是在线控制快照：

- `found`
- `owned_by_current_daemon`
- `phase`
- `ticket_id`
- `worker_id`
- `run_id`
- `cancel_requested_at`
- `last_error`

它不返回业务语义判断。

也就是说：

- 是否 `active`
- 是否已失去在线控制

这些都由上层基于这个快照做判断。

注意：

- `ProbeTicketLoop(...)` 不负责 heartbeat / stalled / dead 判断
- handler 只负责在线 owner 与 cancel 真相
- 活性判断继续沿用当前 `task runtime + lease + zombie checker` 这条链路

当前真实活性信号来源是：

- `agentexec` 流式事件触发的 `RenewLease(...)`
- `worker report` 写入的 `AppendRuntimeSample(...)`
- `worker report` 写入的 `AppendSemanticReport(...)`
- task event 更新出的 `LastEventAt`
- `zombieVisibilityTimedOut(...)` 对 `RuntimeObservedAt / SemanticReportedAt / LastEventAt / LeaseExpiresAt` 的统一判断

### 3.2 当前 `worker loop` 仍然太依赖 report

当前循环在这里：

- [worker_loop.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_loop.go)

现在逻辑基本是：

- 启 raw run
- 等结束
- 读 `next_action`
- `continue` 就继续
- 否则退出

这还没有：

- `done / wait_user` 的硬收口
- 缺 report / 非法 report 的统一 closure
- `active` 与在线 loop handler 生命周期的强绑定

### 3.2.1 loop handler 和 worker loop 怎么连起来

这里不新增新对象，只补一条很薄的注入链。

做法：

1. daemon 在 `executeTicketRun(handle)` 里，基于当前 handle 构造一个 `loop control sink`
2. 把这个 sink 注入到 `handle.ctx`
3. `pm.executeWorkerLoop(...)` 从 `ctx` 里取出 sink
4. loop 在真实边界点调用 sink 更新 handler

`loop control sink` 只需要 5 个能力：

- `LoopClaimed(ticketID, workerID)`
- `LoopRunAttached(runID, workerID, phase)`
- `LoopClosing()`
- `LoopCancelRequested()`
- `LoopErrored(err)`

它不做业务逻辑，只改 handler 内存状态。

### 3.2.2 哪些点要更新 handler

这里只更新控制状态，不做 heartbeat。

第一版只在这些点写：

1. loop claim 成功
2. 一轮 raw run 启动
3. 一轮 raw run 返回
4. 进入 closure
5. 收到 cancel

这就足够做：

- 当前 loop 是否已 claim
- 当前 raw run 是否已附着
- 当前 loop 是否进入 closing / canceling

### 3.3 当前中断还是假的

当前中断在：

- [interrupt.go](/Users/xiewanpeng/agi/dalek/internal/services/worker/interrupt.go)

它现在做的是：

- 记 `interrupt_requested`
- `MarkRunCanceled(...)`

但真正能停掉当前执行的是：

- [ExecutionHost.CancelRun](/Users/xiewanpeng/agi/dalek/internal/services/daemon/execution_host.go#L521)

所以当前问题不是“没有中断按钮”，而是：

**控制动作还没真正打到在线 loop handler 上。**

## 4. 增量改造方案

这里只做 4 件事。

### 4.1 把在线 loop handler 升成 `active` 的唯一来源

当前不要再让：

- `report=continue`
- `next_action=continue`

去推 ticket `active`。

改成：

- 当前 ticket 的 `worker loop handler` 一旦 claim 成功，ticket 投影为 `active`
- 当前 handler 一旦 release / cancel / supersede / dead，`active` 必须失效

这意味着：

- `active` 不再是 report 投影
- `active` 是在线控制真相的投影

具体顺序：

1. `submitTicketRun(...)` 注册 ticket 索引并 claim handler
2. claim 成功后，loop 真正开始执行
3. `RunTicketWorker(...)` 入口将 ticket promote 到 `active`
4. 只要 handler 仍 live，ticket 就保持 `active`
5. 当 handler finalize/release 后，`active` 失效

这里有一个关键原则：

**`active` 只来自在线 handler 生命周期，不从 report、next_action、孤儿 run 反推。**

对应改动点：

- [worker_run.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_run.go)
- [workflow.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/workflow.go)
- [recovery.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/recovery.go)
- [zombie_check.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/zombie_check.go)

### 4.2 `continue` 彻底退出控制面

`continue` 只表示：

- 当前 loop 继续下一轮 raw run

系统不做：

- 不解析 handoff
- 不解释 phase 语义
- 不靠 `continue` 推 `active`
- 不让 `continue` 进 closure

worker 自己靠：

- `kernel`
- `state.json`
- 当前 worktree / git 事实

继续下一轮。

对应改动点：

- [worker_loop.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_loop.go)
- [workflow.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/workflow.go)

### 4.3 只在 loop 退出时走 `closure`

closure 只处理：

- `done`
- `wait_user`
- 缺 report
- 非法 report
- 异常退出

不处理：

- `continue`

closure 的最小规则：

#### `done`

至少检查：

- report 合法
- `summary` 非空
- `state.json` 里所有 phase 都是 `done`
- `blockers` 为空
- git/worktree 没有明显未收口矛盾

#### `wait_user`

至少检查：

- report 合法
- `blockers` 非空
- `summary` 能说明为什么需要人工介入

#### 缺 report / 非法 report / 异常退出

统一进入 closure fallback。

对应改动点：

- [worker_loop.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_loop.go)
- [worker_report_closure.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_report_closure.go)

### 4.3.1 `closure` 和 `finalize` 的严格时序

这条时序必须写死：

1. loop 判断将要退出
2. handler 进入 `closing`
3. 执行 `closure`
4. closure 成功后，才做 terminal projection
   - `done -> ticket done`
   - `wait_user -> ticket blocked`
5. terminal projection 完成后，loop 返回
6. `executeTicketRun(...)` 的 `defer finalizeHandle(handle)` 才真正释放 handler

也就是说：

- `closure` 在 `finalize` 前
- `finalize` 是 release 在线控制权
- terminal 投影成功之前，ticket 仍由当前 handler 占有

### 4.4 `interrupt` / `cancel` 先打到 loop handler，再写 DB

当前顺序是反的：

- 先 `MarkRunCanceled`
- 再假设底层会停

要改成：

1. 优先通过在线 loop handler 真 cancel 当前执行
2. 然后再写：
   - `interrupt_requested`
   - `task_canceled`
   - 相关 worker/task 事件

如果当前 daemon 已经没有这个 handle：

- 不能假装“已经真中断”
- 只能进入：
  - supersede
  - recovery
  - dead/stale 收敛路径

### 4.4.1 cancel 后怎么避免卡死和双 loop

cancel 的规则是：

1. 收到 cancel 时，不立刻释放 handler
2. 先把 handler 标成：
   - `phase = canceling`
   - `cancel_requested_at = now`
3. 再调用 `handle.cancel()`
4. 只有 loop goroutine 真退出，`finalizeHandle(...)` 才释放 ticket claim

这条规则的目的：

- 避免 cancel 请求发出后，旧 loop 其实还没停，新 loop 已经接管
- 宁可 fail-closed，也不要双 loop

如果 cancel 后长时间没有新的 runtime 可见性信号：

- 上层继续按现有 `task runtime + zombie checker` 视它为 `stalled`
- 但 handler 仍然占有 claim
- 不自动放开给新 loop

只有两种情况会真的释放：

1. loop goroutine 正常/异常返回并 finalize
2. daemon 整体退出，内存态 handle 消失；后续由 recovery 走离线收敛

对应改动点：

- [interrupt.go](/Users/xiewanpeng/agi/dalek/internal/services/worker/interrupt.go)
- [execution_host.go](/Users/xiewanpeng/agi/dalek/internal/services/daemon/execution_host.go)

## 5. `worker report` 的吸收口径要改

这是这次改造里最关键的一条，不然 `closure` 无法真正生效。

当前问题在这里：

- [workflow.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/workflow.go)
- [report.go](/Users/xiewanpeng/agi/dalek/internal/services/worker/report.go)

现在的口径基本是：

- worker report 一到
- PM reducer 就可能直接按 `next_action` 推 workflow

这会导致：

- `continue` 直接推 `active`
- `done` 直接推 `done`
- `wait_user` 直接推 `blocked`

这和最小模型冲突。

要改成：

### 5.1 report 先落运行观测

`worker.ApplyWorkerReport(...)` 继续负责：

- task runtime 观测
- semantic report 落库
- append-only 事件

也就是：

**report 先作为运行观测输入保留下来。**

### 5.2 `continue` 不再直接推进 workflow

`continue` 不再通过 reducer 推 `ticket active`。

`active` 的来源改成：

- 在线 `worker loop handler` claim / release

### 5.3 `done / wait_user` 不再直接推进 workflow

`done / wait_user` 不能在收到 report 时直接推进 ticket。

改成：

1. report 先落观测
2. loop 退出
3. 进入 `closure`
4. closure 通过后，再由 loop 明确投影：
   - `done -> ticket done`
   - `wait_user -> ticket blocked`

也就是：

**report 不再直接成为 ticket 终态；report 只是 closure 的输入材料。**

对应改动点：

- [workflow.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/workflow.go)
- [worker_loop.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_loop.go)
- [report.go](/Users/xiewanpeng/agi/dalek/internal/services/worker/report.go)

## 6. `kernel` 和 `state.json` 只做最小对齐

这里不新增独立 spec，不重做 schema。

只做最小对齐，让 closure 真有东西可判。

### 6.1 `worker-kernel.md`

位置：

- [worker-kernel.md](/Users/xiewanpeng/agi/dalek/internal/repo/templates/project/control/worker/worker-kernel.md)

只补清楚这些规则：

- `continue` 由 worker 自己继续
- `wait_user` 必须带 blockers
- `done` 只有在本地 phase 全部收口时才允许上报
- 非法 report / 非法状态会被 loop 打回 closure
- `report` 是本轮退出动作，不是直接推进 ticket 的终态真相

### 6.2 `state.json`

位置：

- [state.json](/Users/xiewanpeng/agi/dalek/internal/repo/templates/project/control/worker/state.json)

不重做结构，只把现有字段正式拿来判：

- `phases.items[*].status` 用来判断 `done`
- `blockers` 用来判断 `wait_user`
- `code.*` 用来辅助判断是否已收口
- 模板残留 `{{...}}` 说明初始化没完成，不能作为合法退出材料

## 7. 完整运行时序

这是第一版实现的完整链路。

### 7.1 启动

1. 外部提交 ticket worker run
2. `ExecutionHost.submitTicketRun(...)` 为 `project + ticketID` claim 一个唯一 loop handler
3. handler 注册到：
   - `requests`
   - `ticketLoops`
   - 后续 attach 后进入 `runs`
4. `executeTicketRun(handle)` 启动
5. daemon 构造 `loop control sink` 并注入 `handle.ctx`
6. `project.RunTicketWorker(handle.ctx, ...)` 进入 PM loop
7. PM 在 loop 真正开始时，把 ticket promote 到 `active`

### 7.2 运行中

8. `executeWorkerLoop(...)` 启动 raw run
9. 每轮 raw run 都继承同一个 loop `ctx`
10. loop 在以下边界点 touch handler：
    - raw run start
    - raw run done
    - entering closure
    - cancel requested
11. 如果 `next_action == continue`
    - 直接继续下一轮
    - 不进 closure
    - 不推 workflow

### 7.3 退出

12. 遇到：
    - `done`
    - `wait_user`
    - 缺 report
    - 非法 report
    - 异常退出
13. loop 先把 handler 标成 `closing`
14. 执行 `closure`
15. closure 成功后，才做 terminal projection
16. loop 返回
17. `finalizeHandle(handle)` 释放 handler，并撤销 ticket 的在线控制权

### 7.4 中断

18. 外部调用 `CancelTicketLoop(project, ticketID)`
19. 若当前 daemon 仍持有 handler：
    - 标记 `canceling`
    - 调 `handle.cancel()`
20. loop 收到 ctx cancel 后退出
21. `finalizeHandle(handle)` 释放 claim
22. 然后再写 DB 侧 canceled / event

### 7.5 崩溃恢复

23. daemon 崩溃后，内存 handle 全丢
24. 此时没有在线控制真相
25. recovery 只做离线收敛，不再把 orphan run 直接修成 `active`
26. 只有新 loop 重新 claim 后，ticket 才再次 `active`

## 8. 这次明确不做什么

这次不做：

- 不新增 `stage` 层
- 不做 `single-step runtime`
- 不新增独立 `raw run handler`
- 不做系统级 `continue` 语义接续
- 不支持“暂停并恢复同一个 raw run”
- 不重做 worker ownership
- 不重做 `state.json` 大 schema

## 9. 一句话总结

**这次改造只做两件事：**

1. **让在线 `worker loop handler` 成为 `active` 的唯一真相源**
2. **让 `done / wait_user / 缺 report / 非法 report / 异常退出` 在 loop 退出时统一走 `closure`**

除此之外，不再加层，不再搞新对象宇宙。
