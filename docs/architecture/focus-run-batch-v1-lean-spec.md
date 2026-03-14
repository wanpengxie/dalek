# Focus Run Batch V1 精简架构规格

## 1. 结论

`focus run` 的 `batch v1` 不应继续朝“通用工作流引擎”演化。

本规格把它重新定义为：

- daemon-owned 的串行批处理协调器
- 只负责硬约束、幂等、恢复、审计边界
- 低频复杂异常允许 `blocked`，交给 agent 判断与人工兜底

核心判断：

1. 只有 daemon 可以推进 `focus run`
2. 同项目同时最多一个 active focus
3. `stop/cancel` 意图必须可持久化、可恢复
4. 当前执行绑定必须明确
5. `merged` 的唯一真相仍是 integration observe
6. `batch` 模式严格串行，前一 item 未完成交付前，后一 item 绝不能启动
7. 任意 merge conflict 在 v1 都不自动解，一律 `abort merge + create integration ticket + focus blocked`

这份规格用于取代 [focus-run-daemon-owned-design.md](/Users/xiewanpeng/agi/dalek/docs/architecture/focus-run-daemon-owned-design.md) 作为 `batch focus v1` 的实现依据。

## 2. 设计哲学

### 2.1 系统只做 4 件事

系统只保证：

1. 控制权唯一
2. 状态真相最小但可恢复
3. repo root 永远不被脏 merge 现场长期占用
4. 关键动作有审计事件

### 2.2 agent 负责判断，不负责越权

agent 可以做：

- 判断失败是否值得 restart
- 归纳 `blocked_reason`
- 汇总冲突上下文并生成 integration ticket 描述
- 在恢复时辅助判断现场是否一致

agent 不可以做：

- 代替 daemon 做状态推进
- 绕过 `target_ref / anchor / integration observe`
- 在 repo root 修改产品实现文件解决冲突

### 2.3 人工兜底是设计的一部分

以下场景在 v1 中允许明确落到人工兜底：

- 复杂恢复分歧
- 多次 restart 仍不稳定的执行异常
- integration ticket 再次 merge conflict

这不是能力不足，而是为了避免把低频异常固化成过厚的状态机。

## 3. 范围与非目标

### 3.1 范围

本规格只覆盖：

- `manager run --mode batch`
- daemon-owned focus controller
- 严格串行处理 scope 中的 ticket
- 真实 repo merge
- 冲突后创建 integration ticket

### 3.2 非目标

本规格不覆盖：

- `plan focus`
- 冲突自动解
- 独立 attempts 历史表
- 自动恢复并继续原 blocked focus 的复杂编排
- feature graph 的完整 integration 节点编排

## 4. 硬边界

### 4.1 daemon 边界

以下命令没有 daemon 时直接失败：

- `manager run`
- `manager stop`
- `manager stop --force`
- `manager tail`

`show/status` 可以允许本地只读降级，但必须标记 `readonly-stale`。

### 4.2 repo root 边界

repo root 是共享资源，必须满足：

- merge 只允许在 daemon 统一持有的 repo executor 上执行
- 冲突现场只允许短暂停留，用于采集证据
- 创建 integration ticket 前必须 `git merge --abort`
- integration ticket 的 worktree 绝不继承 conflicted index

### 4.3 merge ownership 边界

遵循 [.dalek/agent-kernel.md](/Users/xiewanpeng/agi/dalek/.dalek/agent-kernel.md)：

- PM 可以执行集成动作
- PM 不得直接修改产品实现文件
- 只要 merge conflict 涉及产品实现文件，必须转 integration ticket

本规格进一步收紧为：

- v1 中任意 merge conflict 都不自动解
- 一律 `abort merge + create integration ticket + focus blocked`

这样可以彻底去掉“哪类冲突可以自动解”的分类复杂度。

### 4.4 严格串行边界

`batch v1` 的语义不是“尽量串行”，而是“严格串行”：

- `t1` 只有在交付真正收口后，`t2` 才能启动
- “真正收口”只允许两种情况：
  - `t1` 自己已经 `merged`
  - `t1` 已交棒给 replacement integration ticket，且 replacement 已 `merged`，源 ticket 已完成 supersede 收口

因此：

- 不允许 `t1` 还在 `needs_merge` 时启动 `t2`
- 不允许 `t1` 刚创建 handoff ticket 就启动 `t2`
- 不允许“先跑后面的，回头再补 merge”

## 5. 总体架构

### 5.1 角色划分

#### daemon

- 持有唯一 `FocusController`
- 负责 run/item 状态推进
- 负责 merge 执行
- 负责写审计事件

#### CLI / Web

- 只提交意图
- 只读取状态
- 不执行本地 loop
- 不启动本地 queue consumer

#### worker

- 只在 ticket 自己的 worktree 中实现代码
- 不感知 focus batch 细节

#### PM agent

- 只做判断、归纳、ticket 描述生成
- 不拥有状态机

### 5.2 单写者原则

同一 project 内，只有 daemon 的 `FocusController` 能推进：

- `focus_runs`
- `focus_run_items`
- `focus_events`

API handler 只能：

- 创建 run
- 写 `desired_state`
- 追加事件
- 调 `NotifyProject`

recover 也必须走同一个 controller，不允许平行恢复器直接改 focus 状态。

## 6. 数据模型

本规格故意压缩到 3 个对象：

- `focus_runs`
- `focus_run_items`
- `focus_events`

不引入独立 `focus_run_item_attempts` 表。

### 6.1 `focus_runs`

```go
type FocusRun struct {
    ID             uint
    ProjectKey     string
    Mode           string // batch
    RequestID      string

    DesiredState   string // running | stopping | canceling
    Status         string // queued | running | blocked | completed | stopped | failed | canceled

    ScopeTicketIDs string // JSON: [1,2,3]

    AgentBudget    int
    AgentBudgetMax int

    StartedAt      *time.Time
    FinishedAt     *time.Time
}
```

说明：

- 不保留 `ActiveSeq / ActiveTicketID / CompletedCount / Summary / LastError` 这类缓存字段
- 这些都可由 `items + events` 派生，不应成为控制面真相

### 6.2 `focus_run_items`

```go
type FocusRunItem struct {
    ID               uint
    FocusRunID       uint
    Seq              int
    TicketID         uint

    Status           string // pending | queued | executing | merging | awaiting_merge_observation | blocked | completed | stopped | failed | canceled

    CurrentAttempt   int
    CurrentWorkerID  *uint
    CurrentTaskRunID *uint
    HandoffTicketID  *uint

    BlockedReason    string
    LastError        string

    StartedAt        *time.Time
    FinishedAt       *time.Time
}
```

说明：

- 只保留“当前绑定”，不结构化存历史 attempts
- 历史通过 `focus_events + task_runs + ticket_lifecycle_events` 回放
- `blocked_reason` 是 durable 的，`triage` 过程本身不是 durable state
- `handoff_ticket_id` 是最小交棒语义：表示当前 item 已转交给 replacement ticket，等待它完成交付

### 6.3 `focus_events`

```go
type FocusEvent struct {
    ID          uint
    FocusRunID   uint
    FocusItemID *uint

    Kind        string
    Summary     string
    PayloadJSON string
    CreatedAt   time.Time
}
```

最小事件集：

- `run.created`
- `run.desired_state_changed`
- `item.selected`
- `item.start_requested`
- `item.adopted`
- `item.accepted`
- `item.restarted`
- `item.blocked`
- `item.completed`
- `merge.started`
- `merge.aborted`
- `merge.observed`
- `integration_ticket.created`
- `handoff.resolved`
- `recovery.resumed`

### 6.4 约束

必须有数据库级约束：

1. 同 project 同时最多一个 active focus
2. `focus_run_items(focus_run_id, seq)` 唯一
3. `focus_events(id)` 可单调轮询

active focus 定义为：

- `queued`
- `running`
- `blocked`

## 7. daemon API

focus 只需要最小 API：

- `FocusStart(project, mode, scope, budget, request_id)`
- `FocusGet(project, focus_id)`
- `FocusPoll(project, focus_id, since_event_id)`
- `FocusStop(project, focus_id, request_id)`
- `FocusCancel(project, focus_id, request_id)`

`FocusGetActive` 不是必须单独存在的协议，`FocusGet` 可支持“查询当前 active”变体即可。

规则：

1. `FocusStart` 返回稳定 `focus_id`
2. 所有后续动作都以 `focus_id` 为主
3. API handler 不直接推进 item 状态
4. API handler 只负责校验、写意图、记事件、唤醒 daemon

## 8. 控制器行为

### 8.1 item 正常路径

对 scope 中每张 ticket，controller 串行推进：

1. 选择 `pending` item
2. 若 ticket 已 `queued/active`，则 adopt 当前现场
3. 否则调用 `StartTicket`
4. 等 execution host 接受 run，并写入 `CurrentWorkerID / CurrentTaskRunID`
5. item 进入 `executing`
6. 等 lifecycle 收口到 `done + needs_merge` 或 `blocked`
7. 若 `done + needs_merge`，进入 merge
8. 若 merge observe 成功，item `completed`
9. 只有当前 item `completed` 后，controller 才能选择下一个 item

### 8.2 运行时异常

运行时异常仍由既有主路径先处理：

- execution convergence
- zombie checker
- worker loop closure
- ticket lifecycle 投影

focus 自己不重新发明一套 runtime 状态机。

focus 只在主路径收口后做两种动作：

- `restart`
- `blocked`

默认策略建议：

- 最多自动 restart 一次
- 再失败则 `blocked`

### 8.3 blocked 的两类语义

`blocked` 在 v1 中只分两类：

1. `handoff_waiting_merge`
   - 当前 item 已交棒给 `handoff_ticket_id`
   - replacement ticket merged 后，controller 自动继续完成 source ticket supersede 收口
   - 收口完成后，同一个 batch 可继续下一张 ticket

2. 其他 `blocked_reason`
   - 例如 `needs_user`、`restart_exhausted`
   - v1 不自动恢复
   - 需要人工决策后，再显式 stop 当前 focus 或启动新的 batch

### 8.4 stop / cancel

`stop`：

- 只写 `desired_state=stopping`
- controller 在步骤边界停止

`stop --force`：

- 只写 `desired_state=canceling`
- controller 取消当前 ticket loop / task run / merge

这两者都必须跨 daemon 重启可恢复。

### 8.5 handoff 唤醒

当 replacement integration ticket 的 `integration_status` 变成 `merged` 时：

- 必须唤醒对应 project 的 `FocusController`
- controller 检查是否存在 `handoff_ticket_id = replacement_ticket_id` 的 blocked item
- 若存在，则执行 source ticket supersede 收口，并在完成后继续 batch

## 9. merge 策略

### 9.1 v1 只支持两种结果

对 `done + needs_merge` 的 ticket，merge 只有两种结果：

1. 成功
2. 冲突

冲突后不做自动解。

额外约束：

- 若当前冲突 ticket 已经是 integration ticket，则 v1 不再自动创建下一层 integration ticket
- 此时一律 `abort merge + blocked(reason=handoff_recursion_requires_user)`

这样可以避免 `Ti -> Tj -> Tk` 的递归链条。

### 9.2 merge 成功标准

必须同时满足：

1. repo root 在目标 branch 上
2. `git merge` 成功
3. 没有 unmerged files
4. `.git/MERGE_HEAD` 不存在
5. 之后通过 integration observe 看到 `anchor_sha` 被目标 ref 包含

第 5 条仍是 `merged` 的唯一真相。

### 9.3 merge 冲突处理

发生冲突时：

1. 采集证据
2. `git merge --abort`
3. 若当前票不是 integration ticket，则创建 integration ticket
4. 当前 item 写 `handoff_ticket_id`
5. 当前 item 写 `blocked_reason=handoff_waiting_merge`
6. 当前 run 进入 `blocked`
7. 只有 handoff ticket 后续 `merged` 并完成源 ticket supersede 收口后，当前 item 才能转 `completed`

如果当前冲突票本身已经是 integration ticket：

- 不再自动 create replacement
- 当前 item 直接 `blocked`
- `blocked_reason=handoff_recursion_requires_user`

这是一个有限度的自动交棒模型，不是递归自愈模型。

## 10. Integration Ticket 设计

这是本规格最关键的新内容。

### 10.1 语义定义

integration ticket 不是“继续那个冲突中的 merge”。

它的正确语义是：

**在一个干净的 target 分支基线上，重新产出新的交付结果，替代原 ticket 的直接 merge 尝试。**

因此：

- 冲突现场只是证据
- integration ticket 自己有新的 worktree、自己的 worker、自己的 anchor
- 回到 `main` 的是 integration ticket，不是原 ticket 的冲突现场

### 10.2 git 合同

假设原 ticket 是 `Tn`，其 `anchor_sha = An`，目标分支是 `refs/heads/main`。

当 merge `Tn -> main` 冲突时：

1. repo root 当前在 `main@M0`
2. merge `Tn` 发生冲突
3. 采集证据：
   - `source_ticket_ids=[Tn]`
   - `target_ref=refs/heads/main`
   - `conflict_target_head_sha=M0`
   - `source_anchor_shas=[An]`
   - `conflict_files=[...]`
   - merge stderr / 摘要
4. 立即 `git merge --abort`
5. repo root 回到干净的 `main@M0`
6. 创建 integration ticket `Ti`
7. `Ti` 的 worktree 基线是它启动时 `target_ref` 的当前 HEAD

关键约束：

- `Ti` 绝不继承 conflicted index
- `Ti` 的 worktree 一定从干净 ref 建立
- `conflict_target_head_sha` 只是证据，不是 worktree 必须绑定的 SHA

这允许 `Ti` 在 `main` 前进后，仍然面向最新主线做集成。

### 10.3 integration ticket 的任务定义

integration ticket 的任务不是“点掉冲突标记”，而是：

1. 基于干净 `target_ref`
2. 吸收一个或多个 source tickets 的业务意图
3. 产出新的可交付实现
4. 冻结自己的 `anchor_sha`
5. 再像普通 ticket 一样进入 `needs_merge -> merged`

这正是 agent 应该发挥智力的地方：

- 系统只给出 source tickets、冲突文件、target ref、证据
- agent/worker 自行决定是 cherry-pick、重写、还是重新实现

### 10.4 integration ticket 的 ticket 形态

v1 不新增新的 ticket 类型表。

integration ticket 仍然是普通 ticket，复用现有：

- `title`
- `description`
- `label`
- `priority`
- `target_ref`

建议约束：

- `label = integration`
- `target_ref = 原 target_ref`
- 默认优先级 `high`

标题模板：

- 单票：`集成 t12 到 main`
- 多票：`解决 t12 / t15 在 main 上的集成冲突`

### 10.5 integration ticket 的描述模板

description 必须结构化，至少包含：

```md
## 来源
- source_tickets: t12, t15
- trigger: merge_conflict
- target_ref: refs/heads/main

## 现场
- conflict_target_head_sha: <sha>
- source_anchor_shas: <sha list>
- conflict_files:
  - internal/x.go
  - web/y.ts

## 目标
- 基于当前 main 重新整合 source tickets 的交付意图
- 产出新的可交付 anchor

## 约束
- 不得丢失 source tickets 的需求语义
- 允许修改产品实现文件
- 不得依赖 repo root 的冲突现场

## 输入证据
- merge stderr/log: <path or summary>
- docs: <path list>

## 完成标准
- 在干净 target_ref 基线上完成实现
- 编译/测试通过
- 本 ticket done 后进入 needs_merge
```

### 10.6 源 ticket 的收口规则

当 `Ti` 被创建时，源 ticket 不立即改成 `abandoned`。

原因：

- `Ti` 还可能失败
- 过早放弃源 ticket 会丢失恢复空间

因此 v1 规则是：

1. `Ti` 创建后，原 focus item 进入 `blocked`
2. 源 ticket 仍保持原状态，通常是 `done + needs_merge`
3. 当 `Ti` 后续成功 `merged` 后，controller 自动执行源 ticket 收口：
   - 对 source ticket 执行 `integration_status=abandoned`
   - 记录 `reason = superseded by integration ticket Ti`
   - 记录 `superseded_by_ticket_id = Ti`
4. 源 ticket 收口完成后，原 focus item 才能转 `completed`
5. 之后同一个 batch 才允许启动下一张 ticket

这一步必须自动化，因为严格串行语义要求“上一张真正收口后，下一张才允许开始”。

### 10.7 handoff 完成语义

`handoff` 不是“创建了 replacement ticket 就算完成”，而是：

1. replacement ticket 已 `merged`
2. source ticket 已 `abandoned`
3. `source.superseded_by_ticket_id = replacement_ticket_id`

只有三者同时满足，controller 才能把当前 item 从 `blocked` 变成 `completed`。

这就是 `batch v1` 的最小交棒闭环。

## 11. Go 实现规格

### 11.1 数据模型

新增或修改建议：

- `internal/contracts/focus.go`
  - `FocusRun`
  - `FocusRunItem`
  - `FocusEvent`

不新增 `FocusRunItemAttempt`。

同时对 ticket 增加一个最小 audit 字段：

- `superseded_by_ticket_id`

它不是新的关系系统，只是 replacement mapping 的最小持久化载体。

### 11.2 app facade

新增 project facade：

```go
func (p *Project) FocusStart(ctx context.Context, in FocusStartInput) (FocusStartResult, error)
func (p *Project) FocusGet(ctx context.Context, focusID uint) (FocusRunView, error)
func (p *Project) FocusPoll(ctx context.Context, focusID uint, sinceEventID uint) (FocusPollResult, error)
func (p *Project) FocusStop(ctx context.Context, focusID uint, requestID string) error
func (p *Project) FocusCancel(ctx context.Context, focusID uint, requestID string) error
```

新增 integration ticket facade：

```go
type CreateIntegrationTicketInput struct {
    SourceTicketIDs        []uint
    TargetRef              string
    ConflictTargetHeadSHA  string
    SourceAnchorSHAs       []string
    ConflictFiles          []string
    MergeSummary           string
    EvidenceRefs           []string
}

type CreateIntegrationTicketResult struct {
    TicketID uint
}

func (p *Project) CreateIntegrationTicket(ctx context.Context, in CreateIntegrationTicketInput) (CreateIntegrationTicketResult, error)
func (p *Project) FinalizeTicketSuperseded(ctx context.Context, sourceTicketID, replacementTicketID uint, reason string) error
```

### 11.3 pm service

新增服务职责：

1. `FocusController`
   - 串行推进 run/item
   - 只写 `focus_runs / focus_run_items / focus_events`

2. `CreateIntegrationTicket`
   - 校验 source tickets 与 target ref
   - 生成结构化 description
   - 复用现有 `ticket.CreateWithDescriptionAndLabelAndPriorityAndTarget`
   - 追加 `focus_events.integration_ticket.created`

3. `FinalizeTicketSuperseded`
   - 仅允许在 replacement ticket 已 `merged` 时执行
   - 对 source ticket 追加 abandon lifecycle event
   - 写 `superseded_by_ticket_id`
   - 供 controller 在 handoff 完成时调用

伪代码：

```go
func (s *Service) CreateIntegrationTicket(ctx context.Context, in CreateIntegrationTicketInput) (CreateIntegrationTicketResult, error) {
    title := buildIntegrationTicketTitle(in.SourceTicketIDs, in.TargetRef)
    desc := buildIntegrationTicketDescription(in)
    tk, err := s.ticket.CreateWithDescriptionAndLabelAndPriorityAndTarget(
        ctx,
        title,
        desc,
        "integration",
        contracts.TicketPriorityHigh,
        in.TargetRef,
    )
    if err != nil {
        return CreateIntegrationTicketResult{}, err
    }
    return CreateIntegrationTicketResult{TicketID: tk.ID}, nil
}
```

### 11.4 merge 执行逻辑

在 `internal/services/pm/focus_merge.go` 中：

- 成功：走既有 integration observe 路径
- 冲突：
  - 采集 `conflict_files`
  - 采集 `conflict_target_head_sha`
  - `git merge --abort`
  - 若当前票不是 integration ticket，则调 `CreateIntegrationTicket`
  - 写 `handoff_ticket_id`
  - item/run -> `blocked(handoff_waiting_merge)`
  - 若当前票已经是 integration ticket，则直接 `blocked(handoff_recursion_requires_user)`

v1 删除：

- daemon-side 自动解冲突逻辑
- integration-only conflict 分类

### 11.5 CLI

`manager run/stop/tail`：

- 全部切到 daemon API
- CLI 本地不再执行任何 focus loop

`ticket create`：

- 不新增新的命令面
- integration ticket 仍通过既有 ticket 创建服务创建

## 12. Agent 实现规格

### 12.1 agent 输入

当 merge conflict 触发 integration ticket 创建时，传给 agent 的最小上下文：

- source ticket IDs 与标题
- source anchor SHAs
- target ref
- conflict target head SHA
- conflict files
- merge stderr 摘要
- 相关需求/设计文档路径

### 12.2 agent 输出

agent 输出不是代码，而是：

- integration ticket 标题
- description 的结构化内容
- `blocked_reason` 摘要

可选输出：

- 推荐优先级
- 是否建议人工立即介入

### 12.3 agent 不做的事

agent 不做：

- 直接改 repo root 冲突文件
- 直接把源 ticket 标成 merged/abandoned
- 直接推进 focus 状态

## 13. Ticket 层实现规格

### 13.1 不新增 ticket 类型系统

v1 不新增：

- `ticket.type`
- 专门的 integration_ticket 表

但需要保留一个最小 replacement 字段：

- `superseded_by_ticket_id`

原因：

- 现有 merge 语义已经要求 `abandoned` 若要解锁下游，必须存在明确 replacement mapping
- 这是最小而明确的 mapping，不需要再造关系表

### 13.2 v1 的 ticket 关联方式

v1 通过以下方式保存关联：

- integration ticket description 中写 `source_tickets`
- `focus_events` 中写 `integration_ticket_id`
- 源 ticket 上写 `superseded_by_ticket_id`
- 未来若 feature graph 已启用，可额外挂 integration node

这足够支持：

- 人工诊断
- 事件回放
- source -> replacement 的明确替代关系
- downstream 依赖正确判定

## 14. 迁移策略

迁移顺序必须是：

1. 新增精简版 schema
2. 新增 daemon API
3. 把 controller 挂到现有 daemon manager tick / wake
4. 删 CLI 本地 focus loop
5. 接入 merge conflict -> integration ticket
6. 删除旧版 attempts 设计与冲突自动解设想

必须删除的旧设计：

1. CLI 本地 `RunBatchFocus`
2. 本地 `StartQueueConsumer`
3. `focusCancelFn`
4. 独立 `focus_run_item_attempts`
5. 冲突自动解逻辑

## 15. 验收标准

验收必须全部在：

- 真实 daemon
- 真实 git repo
- 真实 worktree

场景完成。

### 15.1 必过 case

1. 基础 happy path
   `manager run --mode batch --tickets a,b` 能串行推进，merge 成功后 ticket `merged`，run `completed`。

2. 跨进程 stop/cancel
   第二个 CLI 能停止或强制取消正在运行的 focus。

3. daemon 重启恢复
   active focus 在 daemon 重启后仍可恢复当前 item。

4. queued/active adopt
   focus 能 adopt 已在运行中的 ticket。

5. runtime 异常 restart 一次
   收口后 focus 最多自动 restart 一次，再失败则 `blocked`。

6. merge conflict 清理现场
   冲突发生后 repo root 被 `merge --abort` 清理干净。

7. integration ticket 创建
   冲突后自动创建普通 ticket：
   - `label=integration`
   - `target_ref` 正确
   - description 包含 source tickets、target ref、conflict files、证据

8. integration ticket worktree 基线正确
   启动 integration ticket 时，其 worktree 基于干净 `target_ref` 当前 HEAD，而不是继承冲突 index。

9. 严格串行 handoff
   `t1` 冲突后创建 `Ti`，在 `Ti` merged 且 `t1.superseded_by_ticket_id=Ti`、`t1=abandoned` 前，`t2` 绝不能启动；收口完成后，同一个 batch 才能继续。

10. integration ticket 再次冲突不递归
   当当前 merge ticket 本身已是 `label=integration`，再次冲突时不得再自动创建下一张 integration ticket，必须进入人工决策态。

11. 无 daemon 边界
   `run/stop/tail` 无 daemon 时直接失败；只读查询可降级但必须标记 `readonly-stale`。

## 16. 最终判断标准

这份规格是否成功，只看三句话：

1. CLI 全退场后，daemon 仍能独立拥有并推进 focus run
2. merge conflict 不再把系统拖进脏 git 现场，而是被转成一张新的 integration ticket
3. 系统没有为了覆盖低频异常而长出第二套厚重状态机

满足这三条，`batch focus v1` 才算真正简洁、可落地、与当前架构一致。
