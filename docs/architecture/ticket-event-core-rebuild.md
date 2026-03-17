# Ticket 事件核心重构设计

> 本文不是对现状的小修小补。
>
> 目标是重新定义 dalek 的 ticket 体系：
>
> - 以事件为唯一业务真相源
> - 以 live worker loop 为唯一活性真相源
> - 以 worktree/context 为唯一工件真相源
> - 把所有 `status/running/capability` 都降级为 projection

## 1. 一句话结论

当前 ticket 体系的根问题不是“状态字段太多”，而是：

**系统没有把事实、活性、工件、投影这四类对象硬分层。**

于是同一个现实事实会被下面几层同时表达：

- ticket workflow
- worker status
- task run state
- execution host 内存 handle
- focus item status
- TUI capability

一旦任何控制路径拿错层级做判断，就会出现：

- daemon 重启后内存 loop 已经不存在，但 UI 仍显示 active/running
- `r/k` 被 stale projection 挡住
- focus loop 和 worker loop 互相“看起来”都在控制同一件事
- recovery、zombie、manual control 三条链路彼此打架

## 2. 设计目标

### 2.1 必达目标

1. ticket 世界只接受 append-only 事实事件，所有业务状态都由 projector 生成。
2. 当前 daemon 是否真的“有一个活 worker loop”只能由内存 handle 回答。
3. worktree、`.dalek/state.json`、git 事实只在工件世界内成立，不再被翻译成控制真相。
4. `r/k/focus cancel` 一类手动控制永远先看 live loop，再回退处理 orphan attempt，不能被 projection 反向门禁。
5. daemon 重启后，旧内存 loop 一律视为消失；系统只能基于 durable 事件和工件现场发出新的补偿事件，不能假装旧 loop 还活着。
6. 任何 `running/active/executing/canceling` 一类非终态都不能作为 authority 被持久化使用。

### 2.2 非目标

1. 不要求第一版把所有外围表删掉。
2. 不要求第一版把原始 task runtime 观测完全移除。
3. 不要求第一版改变 worker 的 worktree/kernel/state.json 自管理方式。
4. 不要求第一版重写所有 UI；但 UI 不能再直接相信旧 projection。

## 3. 新的四层模型

### 3.1 Layer A: Live Control Truth

这是唯一回答“现在是否还活着”的层。

定义：

- 当前 daemon 内存中的 worker loop handle
- 当前 daemon 对某张 ticket 的 live ownership
- 当前 daemon 是否还能发送 cancel / attach / probe

性质：

- 只存在于内存
- daemon 一重启全部归零
- 不可持久化复活
- 不参与业务世界的最终判定

它回答的问题只有三个：

1. 现在有没有 live loop
2. 当前 daemon 是否拥有它
3. 能不能立即 stop / attach / restart

### 3.2 Layer B: Artifact Truth

这是唯一回答“现场是什么”的层。

定义：

- worktree 文件内容
- git HEAD / dirty / branch / merge anchor
- `.dalek/agent-kernel.md`
- `.dalek/state.json`
- worker 本地产物与日志

性质：

- 可以跨 daemon 重启保留
- agent 进入 worktree 后应先读取这一层
- 这层可以帮助恢复上下文，但不能证明 live loop 还活着

### 3.3 Layer C: Durable Domain Fact

这是 ticket 世界唯一允许持久化的业务真相。

定义：

- 用户命令已经被接纳
- 执行尝试已经被接受
- 执行尝试已经丢失/取消/终结
- stage closure 已被接纳为 `continue/wait_user/done`
- integration freeze / merged / abandoned 已发生

性质：

- append-only
- 可重放
- 可审计
- 可做补偿
- 不能被就地修改

### 3.4 Layer D: Projection

这是给 UI / scheduler / focus / CLI 读取的快照层。

定义：

- `tickets.workflow_status`
- `workers.status`
- `task_status_view`
- `focus_runs.status`
- `inbox_items.status`
- `TicketView.Capability`

性质：

- 可丢弃
- 可重建
- 不能作为控制 authority
- 只能被 projector 更新

## 4. Ticket Aggregate 的重定义

ticket 不再等于“当前执行状态”。

ticket 只表示一个长期存在的任务包，持久字段只保留三类：

1. 任务元数据
   - `title`
   - `description`
   - `label`
   - `priority`

2. 工件定位
   - `target_branch`
   - `merge_anchor_sha`
   - `superseded_by_ticket_id`

3. 用户可见的稳定语义
   - `backlog`
   - `blocked`
   - `done`
   - `archived`
   - integration 状态

关键点：

- `active` 不再代表“当前 live loop 存在”
- `queued` 不再代表“已经有半活的 runtime 在等”
- ticket 世界不直接表达 live execution ownership

如果需要“这张 ticket 当前正由一个 loop claim”，那应来自 execution projection，而不是 ticket 自身字段。

## 5. 事件模型

### 5.1 事件分两类

#### A. Domain Events

这些事件进入 ticket 世界：

- `ticket.created`
- `ticket.start_requested`
- `execution.claim_requested`
- `execution.attempt_accepted`
- `execution.loop_lost`
- `execution.attempt_canceled`
- `execution.attempt_superseded`
- `closure.continue_accepted`
- `closure.wait_user_accepted`
- `closure.done_accepted`
- `integration.anchor_frozen`
- `integration.merged_observed`
- `integration.abandoned`
- `ticket.archived`

#### B. Runtime Observations

这些不直接进入 ticket 世界：

- task events
- runtime samples
- semantic reports
- stream log
- tmux/runtime probe
- `.dalek/state.json` 中间态

它们只能作为 reducer/closure 的输入证据。

### 5.2 事件流组织方式

建议 ticket 为 aggregate root。

每张 ticket 一条 append-only stream：

- `stream_type = ticket`
- `stream_id = ticket_id`

所有与该 ticket 强相关的执行事件都挂在这条流上，payload 中带：

- `execution_id`
- `attempt_id`
- `worker_id`
- `worktree_path`
- `run_id`
- `request_id`

这样做的原因：

1. ticket 是用户和 PM 理解世界的主入口。
2. 手动控制、focus、integration、inbox 最终都要归到 ticket 世界。
3. 不再把 `task_run` 误当业务 aggregate。

## 6. Execution 模型

### 6.1 两个身份

需要明确区分：

1. `live loop ownership`
   - 内存态
   - 只回答“当前 daemon 还拿不拿得住”

2. `execution attempt identity`
   - durable
   - 只回答“这是不是同一次尝试”

同一次 attempt 可以丢失 live ownership。
失去 live ownership 不等于 attempt 历史消失。

### 6.2 attempt 的最小生命周期

建议的 durable 语义：

1. `execution.claim_requested`
2. `execution.attempt_accepted`
3. 期间消费 runtime observations
4. 终态之一：
   - `execution.attempt_canceled`
   - `execution.loop_lost`
   - `closure.wait_user_accepted`
   - `closure.done_accepted`

注意：

- `running/canceling/closing` 不持久化为 authority
- 它们只存在于 live handle 或 runtime observation 里

## 7. 手动控制流

### 7.1 stop

`stop(ticket)` 的规范流程必须是：

1. probe live loop
2. 如果存在 live loop：取消 handle，等待对应终结事件
3. 如果不存在 live loop，但存在未收口 attempt：发补偿事件 `execution.attempt_canceled` 或 `execution.loop_lost`
4. 如果两者都不存在：no-op

禁止：

- 先看 projection 里的 `CanStop`
- 只取消 ticket loop，不处理 orphan attempt

### 7.2 rerun

`rerun(ticket)` 的规范流程必须是：

1. probe live loop
2. 如果存在 live loop：
   - 要么明确拒绝
   - 要么显式走 `restart_requested`
3. 如果不存在 live loop，但存在 orphan attempt：
   - 先发 `execution.attempt_superseded` 或 `execution.loop_lost`
4. 创建新 attempt，并发 `execution.attempt_accepted`

禁止：

- 因为旧 projection 里还有 `active run` 就拒绝 rerun
- 让 UI capability 决定是否允许发命令

### 7.3 focus

focus 不再“拥有” worker loop。

focus 只能做两件事：

1. 发 ticket 级 command
2. 订阅 ticket 事件流来推进自己的 item projection

focus 不得直接维护一套平行的执行 authority。

## 8. Projection 设计

### 8.1 保留哪些 projection

应保留，但降级为纯读模型：

- `ticket_snapshot`
- `execution_projection`
- `worker_projection`
- `focus_projection`
- `inbox_projection`

### 8.2 删除或降级哪些 authority

以下字段即使保留，也只能是 cache：

- `workers.status=running/stopped/failed`
- `task_runs.orchestration_state=pending/running`
- `TicketView.Capability`
- `focus_run_items.status=executing`
- 任意 `can_*` 字段

这些字段不能再被用作：

- rerun 门禁
- stop 门禁
- recovery 是否需要执行的判断
- focus controller 的唯一依据

## 9. 重启与恢复

### 9.1 daemon 重启的硬规则

daemon 重启后：

- 所有 live handles 立即失效
- 旧 daemon 对任何 loop 的 ownership 立即归零
- 系统不得假设旧 loop 还能继续

### 9.2 启动恢复的正确做法

启动恢复不应该“修状态”，而应该“发补偿事件”：

1. 扫描所有未收口 attempt
2. 对每个 attempt probe live ownership
3. 若无 ownership：
   - 发 `execution.loop_lost`
   - 根据策略投影到 `queued` 或 `blocked`
4. 唤醒调度器消费新的稳定 projection

### 9.3 为什么这比直接改表更好

因为这样恢复本身也是事实。

系统可以回答：

- 这次 attempt 是怎么结束的
- 是正常收口、手动取消，还是 daemon 重启导致 loop lost
- 为什么 ticket 又回到了 queued / blocked

## 10. Keep / Drop 清单

### 10.1 必须保留

- ticket 元数据
- worktree/branch/merge anchor
- append-only domain events
- worktree 现场与 state.json
- raw runtime observations 作为证据

### 10.2 必须降级

- worker running/stopped
- task run pending/running
- active/queued 的即时控制语义
- focus item executing
- capability can_stop/can_rerun

### 10.3 可以逐步删除

- 任何只为“补状态洞”而存在的 repair path
- 任何绕过事件直接写 workflow/status 的命令路径
- 任何把 projection 当 authority 的 TUI 门禁

## 11. 迁移步骤

### Phase 1: 立规矩

1. 新增 ticket event stream 作为唯一业务写入口。
2. 所有 ticket 业务写路径先写事件，再更新 projection。
3. 手动控制流改成 live-first。

### Phase 2: 降 authority

1. `workers.status`、`task_runs.orchestration_state` 从 authority 降为 cache。
2. `TicketView.Capability` 从门禁 authority 降为提示文案。
3. focus 改成只消费 ticket command / ticket events。

### Phase 3: 删旧路径

1. 删除直接 `UPDATE workflow_status/status` 的业务路径。
2. 删除依赖 stale projection 的 recovery / zombie / controller 逻辑。
3. 只保留基于事件补偿的恢复。

## 12. 最终判断标准

重构完成后，系统必须满足下面四条：

1. `worker loop 是否活着` 只能问 live handle。
2. `ticket 世界发生了什么` 只能问事件流。
3. `现场长什么样` 只能看 worktree/context/state。
4. `UI 显示什么` 只能看 projection，且 projection 永远不能反向控制世界。

如果还有任何路径同时相信：

- 内存 loop
- DB running 状态
- UI capability
- focus item executing

那 ticket 体系就还没有真正完成重构。
