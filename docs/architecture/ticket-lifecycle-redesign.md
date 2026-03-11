# Ticket 生命周期重设计 — 技术方案

## 1. 变更概述

1. **物理删除 dispatch 过程**：不是合并到 start，是彻底不存在。PM 不再为 worker "编译 context"，worker 通过 kernel 自己完成初始化
2. **消除裸 set 状态操作**：所有 workflow_status 变更必须经由事件驱动入口
3. **收紧状态空间**：`queued` 语义重定义为"等待资源配额"，约束组合状态

## 2. 核心模型变更

### 2.1 旧模型 vs 新模型

```
旧模型：
  create → start(准备资源, backlog→queued) → dispatch(PM编译context, queued→active) → worker 执行

新模型：
  create → start(请求配额) → queued(等配额) → [scheduler 分配配额] → active(worker 自主运行)
```

| 维度 | 旧模型 | 新模型 |
|------|--------|--------|
| context 编译 | PM dispatch agent 编译（`dispatch_agent_exec.go`）→ 注入 worker | **不存在**。worker 启动后读自己的 kernel，自己初始化 |
| dispatch 过程 | 独立实体 PMDispatchJob，有队列+lease+状态机 | **物理删除** |
| queued 语义 | 已 start（资源就绪），等 dispatch（PM 编译 context） | 已请求 start，**等资源配额** |
| start 职责 | 准备 worktree + worker 记录 + bootstrap + 推 queued | 请求配额 → 排队 |
| worker 启动 | SDK goroutine 调用（claude/codex/gemini SDK），由 dispatch runner 驱动 | scheduler 直接通过 SDK 启动 agent |
| worker 初始化 | PM dispatch agent 注入 prompt，worker loop 按 prompt 执行 | **worker kernel 自驱动** |

### 2.2 新状态转换图

```
                                ┌─── 无配额 ───┐
                                ↓              │
backlog ──[ticket start]──→ queued ──[拿到配额]──→ active ──[report(done)]──→ done
                                                    ↑  ↓                       │
                                                    │  └─[report(wait_user)]→ blocked
                                                    │                          │
                                                    └────[ticket start]────────┘
```

注：`needs_merge`/`merged`/`abandoned` 是 **`IntegrationStatus`**（独立维度），不是 `workflow_status`。
```
integration_status（仅 done 后有效）：
  none → needs_merge（report(done) 同事务冻结 anchor）
  needs_merge → merged（git hook sync-ref 检测 is-ancestor）
  needs_merge → abandoned（merge abandon）
```

### 2.3 workflow_status 枚举（新语义）

| 状态 | 语义 | 入口操作 | 前置状态 |
|---|---|---|---|
| `backlog` | 已创建，等待启动 | `ticket create` | （初始态） |
| `queued` | **等待资源配额** | `ticket start` | backlog, blocked（worker 不存在时） |
| `active` | worker 正在自主执行（SDK goroutine） | scheduler 分配配额 | queued |
| `blocked` | 等待外部介入 | `worker report(wait_user)` | active |
| `done` | worker 完成任务 | `worker report(done)` | active |
| `archived` | 已归档 | `ticket archive` | done+merged, done+abandoned, backlog |

### 2.4 `ticket start` 的新流程

```
ticket start(ticketID)
│
├─ 1. Guard: CanEnqueueTicket（允许 backlog / blocked）
├─ 2. blocked 分支判断：
│     ├─ worker 存在且 running → 直接推 active（配额没释放过）→ 返回
│     ├─ worker 存在但 stopped/failed → 尝试重启 → 成功推 active → 返回
│     └─ worker 不存在 → 继续走排队路径 ↓
├─ 3. 推进到 queued（记录事件）
├─ 4. 请求资源配额（同步快速路径）
│     ├─ 无配额 → 留在 queued，等 scheduler 后续调度
│     └─ 有配额 → ActivateTicket → active
└─ 5. 返回
```

**start 不做的事情：**
- 不编译 context（没有 PM dispatch agent）
- 不注入 prompt 给 worker
- 不运行 worker loop（删除 dispatch 后 worker loop 概念不存在）

### 2.5 资源配额调度

当前系统已有配额概念（`PMState.MaxRunningWorkers`，默认 3，范围 [1, 32]）。

**当前实现（`manager_tick.go` 中 `scheduleQueuedTickets` private 方法）：**
- 配额基准：`Progressable` workers 数量（running 且非 blocked 的 worker）
- `RunningBlocked` workers（needs_user/stalled）**不计入配额**
- `capacity = maxRunning - Progressable`
- 按 `priority DESC, updated_at ASC, id ASC` 排序

**新模型改造：**
- 配额计算逻辑保持，但移除 dispatch 调用
- `scheduleQueuedTickets` 改为：对每个可调度 ticket 直接调用 `ActivateTicket`（不再 Start + Dispatch 两步）
- 新增触发点：`ApplyWorkerReport(done)` 和 `ArchiveTicket` 后触发调度（当前仅 tick 周期调度）

### 2.6 组合状态约束

| 约束 | 执行机制 |
|---|---|
| done + integration=none 不可达 | report(done) 同事务冻结 anchor + 设 needs_merge（已有实现） |
| active + integration=needs_merge 不可达 | guard 校验 |
| queued/backlog + running worker 不可达 | scheduler 启动前检查（如有 → 直接推 active） |
| archived + integration=needs_merge 不可达 | `CanArchiveTicket` guard（已有实现） |

### 2.7 事件驱动入口表

| 目标状态 | 唯一事件入口 | side effects |
|---|---|---|
| queued | `ticket start` | 提交配额请求 |
| active | scheduler 分配配额 / blocked 恢复 | 配置 worktree + SDK 启动 worker agent |
| blocked | `worker report(wait_user)` | 自动创建 inbox blocker |
| done | `worker report(done)` | 冻结 anchor_sha → 设 integration=needs_merge |
| merged | git hook `sync-ref` | 检测 is-ancestor 后自动推进 integration_status |
| abandoned | `merge abandon` | 关闭相关 inbox |
| archived | `ticket archive` | 检查 integration guard → 请求 worktree cleanup |

## 3. 当前架构快照

### 3.1 dispatch 全栈（物理删除目标）

实际有 **12 个非测试文件**（+ 6 个测试文件）：

| 文件 | 职责 | 处理 |
|------|------|------|
| `dispatch.go` | dispatch 入口：`DispatchTicket`, `SubmitDispatchTicket`, `DispatchTicketWithOptions`, `resolveDispatchTarget`, `ensureDispatchWorkerStarted` 等 | **删除** |
| `dispatch_runner.go` | dispatch 编排：`runPMDispatchJob`（claim → lease → target resolution → promote active → execute → complete），`startLeaseRenewal` | **删除** |
| `dispatch_agent_exec.go` | PM dispatch agent 执行：`runPMDispatchAgent`, `executePMDispatchAgent`, `buildDispatchPrompt` | **删除** |
| `dispatch_queue.go` | job 排队/claim/lease：`enqueuePMDispatchJob`, `claimPMDispatchJob`, `renewPMDispatchJobLease` | **删除** |
| `dispatch_queue_complete.go` | job 完成/失败：`completePMDispatchJobSuccess`, `completePMDispatchJobFailed`, `ForceFailActiveDispatchesForTicket` | **删除** |
| `dispatch_queue_workflow.go` | ticket 状态推进：`promoteTicketActiveOnDispatchClaimTx`, `demoteTicketBlockedOnDispatchFailedTx` | **逻辑迁入 scheduler** |
| `dispatch_queue_helpers.go` | 辅助：`isPMDispatchTerminalStatus`, `newPMDispatchRequestID`, `waitPMDispatchJob`, `getPMDispatchJob` | **删除** |
| `dispatch_request_payload.go` | payload 编解码：`dispatchTaskRequestPayload`, `bindDispatchJobWorker` | **删除** |
| `dispatch_submitter.go` | `DispatchSubmitter` 接口 | **删除** |
| `dispatch_depth_env.go` | `DALEK_DISPATCH_DEPTH` 环境变量管理 | **评估保留**（worker 仍需防止嵌套 dispatch） |
| `direct_dispatch.go` | `DirectDispatchWorker`：绕过 dispatch queue 直接写 `workflow_status=active` | **删除**（额外的裸 set 路径） |

**必须保留和改造的文件：**

| 文件 | 职责 | 处理 |
|------|------|------|
| `worker_loop.go` | `executeWorkerLoop`：SDK 调用的主循环（launch → wait → read next_action → loop if continue） | **改造**：从 dispatch 编排中解耦，由 scheduler 直接调用 |
| `dispatch_worker_sdk.go` | `launchWorkerSDKHandle`：构建 SDK config → 调用 `agentexec.NewSDKExecutor` → `executor.Execute` | **改造/重命名**：移除 dispatch 前缀，保留 SDK 启动逻辑 |

### 3.2 workflow_status 写入路径（现状，完整枚举）

| # | 路径 | 文件 | 新模型中的处理 |
|---|------|------|----------------|
| 1 | `SetTicketWorkflowStatus` | `workflow.go:29` | **降级**为 ForceSet + reason |
| 2 | `promoteTicketQueuedOnStart` | `start.go:120`（WHERE 只匹配 backlog/空，blocked 静默跳过） | **保留**，语义变为"进入配额排队" |
| 3 | `promoteTicketActiveOnDispatchClaimTx` | `dispatch_queue_workflow.go:14` | **迁移**到 scheduler |
| 4 | `demoteTicketBlockedOnDispatchFailedTx` | `dispatch_queue_workflow.go:45` | **迁移**到 scheduler 失败处理 |
| 5 | `ApplyWorkerReport` | `workflow.go:183` | **保持** |
| 6 | `ArchiveTicket` | `workflow.go:83` | **保持** |
| 7 | `DirectDispatchWorker` 直接写 `active` | `direct_dispatch.go:170` | **删除**（随 dispatch 一起删） |
| 8 | `DirectDispatchWorker` 回滚写 `blocked` | `direct_dispatch.go:231` | **删除** |

### 3.3 worker 执行机制（现状）

worker 通过 **SDK goroutine** 执行，不是 tmux 进程：

```
dispatch_runner.runPMDispatchJob()
  → executePMDispatchJob()
    → runPMDispatchAgent()         [dispatch_agent_exec.go — PM agent 编译 prompt]
    → executeWorkerLoop()          [worker_loop.go — worker 主循环]
      → launchWorkerSDKHandle()    [dispatch_worker_sdk.go — SDK 配置]
        → agentexec.NewSDKExecutor [agentexec/sdk.go — SDK executor]
          → executor.Execute()     [启动 goroutine]
            → sdkrunner.Run()      [sdkrunner/runner.go — 调用 claude/codex/gemini SDK]
      → handle.Wait()              [阻塞等待 agent 完成]
      → readWorkerNextActionFromRun() [从 DB 读 next_action]
      → 如果 continue → 再次 launch，循环
```

SDK 库：
- `github.com/wanpengxie/go-claude-agent-sdk`
- `github.com/wanpengxie/go-codex-sdk`
- `github.com/wanpengxie/go-gemini-sdk`

Worker report 通过 `dalek worker report --next-action {done|wait_user|continue}` CLI 命令写入 DB，PM 从 DB 读取。

### 3.4 当前 start.go 实现（如实描述）

```go
// 当前签名
func (s *Service) StartTicket(ctx context.Context, ticketID uint) (*contracts.Worker, error)
func (s *Service) StartTicketWithOptions(ctx context.Context, ticketID uint, opt StartOptions) (*contracts.Worker, error)

type StartOptions struct {
    BaseBranch string
}
```

当前 StartTicket 实际做的事情（不是简单的排队）：
1. Guard: `fsm.CanStartTicket`（允许一切非 done/archived，包括 queued 和 active）
2. `s.worker.StartTicketResourcesWithOptions()` — 创建/恢复 worktree + worker DB 记录
3. 快速路径：worker 已 running 且 `workerDispatchReady` → 直接推 queued + 返回
4. `s.executePMBootstrapEntrypoint()` — 执行 bootstrap 脚本（如果存在）
5. `s.worker.MarkWorkerRunning()` — 推 worker 状态到 running
6. `s.promoteTicketQueuedOnStart()` — 推 ticket 到 queued（WHERE 只匹配 backlog/空）
7. `s.ensureTicketTargetRefOnStart()` — 冻结 target_ref
8. 返回 worker

### 3.5 当前 guard 函数（如实描述）

| 函数 | 签名 | 逻辑 |
|---|---|---|
| `CanStartTicket` | `(status TicketWorkflowStatus) bool` | `status != done && !IsTerminal(status)`，即允许 backlog/queued/active/blocked |
| `CanQueueRunTicket` | `(status) bool` | 直接委托 `CanStartTicket` |
| `ShouldPromoteOnDispatchClaim` | `(status) bool` | done/archived/active 返回 false，其余检查 FSM 可达性 |
| `ShouldDemoteOnDispatchFailed` | `(status) bool` | done/archived/blocked 返回 false，其余检查 FSM 可达性 |
| `CanManualSetWorkflowStatus` | `(current) bool` | `!IsTerminal(current)`，仅阻止 archived |
| `ShouldApplyWorkerReport` | `(status) bool` | `!IsTerminal(status)`，注意 **done 返回 true**（done 不是 terminal state） |
| `CanReportPromoteTo` | `(from, to) bool` | 特殊阻止 done→active，其余检查 FSM 可达性 |
| `CanArchiveTicket` | `(wf, integ) bool` | 阻止 done+needs_merge，阻止已 archived |
| `CanFreezeIntegrationAnchor` | `(wf, integ) bool` | 要求 wf=done 且 integ=none |
| `CanObserveTicketMerged` | `(wf, integ, anchorSHA, targetBranch) bool` | 要求 wf=done 且 integ=needs_merge 且 anchorSHA/targetBranch 非空 |
| `CanAbandonTicketIntegration` | `(wf, integ) bool` | 要求 wf=done 且 integ ∈ {needs_merge, merged} |

注意：所有现有 guard 接收 `TicketWorkflowStatus`（string），不接收 `contracts.Ticket` struct。

### 3.6 当前配额/调度机制

`manager_tick.go` 中 `scheduleQueuedTickets`（private，line 713）：
1. 查询 `workflow_status = 'queued'` 的 tickets，按 `priority DESC, updated_at ASC, id ASC`
2. `capacity = maxRunning - scanResult.Progressable`
3. 对每个可调度 ticket：`StartTicket` → `dispatchScheduledTicket`（调用 `DispatchTicket` 或通过 submitter 异步提交）
4. 表面冲突检查：如果 ticket 的 surface 与 running ticket 冲突且策略是 Serial，则跳过

当前是 **Start + Dispatch 两步**。scheduler 后续 tick 周期执行，当前不存在事件驱动触发。

zombie 重启链（`recoverWorkerByRestartChain`，`zombie_check.go:504`）：
```go
StopTicket → StartTicket → DispatchTicket  // 三步
```

zombie 检测阈值（`constants.go`）：
- stall 阈值：10 分钟
- 最大重试：3 次
- 重试退避：60 秒起，指数增长，上限 10 分钟

## 4. 变更清单

### 4.1 新增/改造：资源配额调度器

改造现有 `scheduleQueuedTickets`（不新建文件，保持在 `manager_tick.go` 或抽取到 `scheduler.go`）：

```go
// 改造后的调度逻辑（伪代码）
func (s *Service) scheduleQueuedTickets(ctx, db, opts) {
    // 1. 配额计算（保持现有逻辑）
    //    capacity = maxRunning - Progressable
    //    RunningBlocked 不计入配额
    // 2. 查询 queued tickets（保持现有排序）
    // 3. 对每个可调度 ticket：
    //    a. ActivateTicket（创建 worktree + SDK 启动 agent + 推 active）
    //    b. 不再调用 DispatchTicket
}

func (s *Service) ActivateTicket(ctx, ticketID) error {
    // 1. 查找/复用已有 worker
    // 2. 无 worker → 创建 worktree + worker 记录
    //    有 worker 但 stopped → 重启
    //    有 worker 且 running → 跳过资源准备
    // 3. 通过 SDK 启动 worker agent（goroutine）
    //    worker 在 worktree 中运行，通过 kernel 自主初始化
    // 4. 推进 queued → active（事务 + 事件）
    //    WHERE workflow_status = 'queued' 保证幂等
}
```

**ActivateTicket 与当前代码的关系：**
- 资源准备：复用 `worker.StartTicketResourcesWithOptions`（来自 `internal/services/worker/start.go`）
- SDK 启动：复用 `launchWorkerSDKHandle`（来自 `dispatch_worker_sdk.go`，重命名去掉 dispatch 前缀）
- worker 主循环：复用 `executeWorkerLoop`（来自 `worker_loop.go`），但 entry prompt 不再由 PM dispatch agent 生成，而是由 worker kernel 驱动
- 状态推进：从 `dispatch_queue_workflow.go` 迁入 promote/demote 逻辑

### 4.2 FSM 层

#### `internal/fsm/tables.go`

```go
// BEFORE
contracts.TicketBacklog: {TicketActive, TicketQueued, TicketArchived},
contracts.TicketQueued:  {TicketActive, TicketBlocked, TicketArchived},

// AFTER
contracts.TicketBacklog: {TicketQueued, TicketArchived},
contracts.TicketQueued:  {TicketActive, TicketBacklog, TicketArchived},
contracts.TicketBlocked: {TicketActive, TicketQueued, TicketArchived},  // 新增 blocked→queued（worker 不存在时走排队）
```

变化说明：
- `backlog → active` **移除**——必须经过 queued
- `queued → blocked` **移除**——queued 状态下没有 worker
- `queued → backlog` **新增**——取消排队
- `blocked → queued` **新增**——worker 不存在时需要重新排队拿配额
- `blocked → active` **保留**——worker 存在时直接恢复（不经 queued）

#### `internal/fsm/ticket_workflow_guards.go`

保持现有风格：guard 接收 `TicketWorkflowStatus`（string），不接收 `Ticket` struct。

| 函数 | 变更 |
|---|---|
| `CanStartTicket(status)` | **收紧**：只允许 backlog 和 blocked（现在允许一切非 done/archived） |
| `CanDispatchTicket(status)` | **删除** |
| `ShouldPromoteOnDispatchClaim(status)` | **删除**，替换为 `ShouldActivateOnSchedule(status)` |
| `ShouldDemoteOnDispatchFailed(status)` | **删除** |
| `CanManualSetWorkflowStatus(current)` | **重命名**为 `CanForceSetWorkflowStatus(current)` |
| `ShouldApplyWorkerReport(status)` | **保持**（注意：done 返回 true，`CanReportPromoteTo` 负责阻止 done→active） |
| 其余 guard | **保持** |

新增：
```go
func ShouldActivateOnSchedule(status contracts.TicketWorkflowStatus) bool {
    return contracts.CanonicalTicketWorkflowStatus(status) == contracts.TicketQueued
}
```

### 4.3 Contracts 层

`TicketQueued` 常量保留，注释更新：
```go
TicketQueued TicketWorkflowStatus = "queued" // 已请求启动，等待资源配额
```

`contracts/pm_dispatch.go`：struct 保留供历史数据读取，标注 deprecated。
`contracts/dispatch_status.go`：保留。

### 4.4 Service 层

#### `start.go` — **重写**

```go
// 新签名
func (s *Service) StartTicket(ctx context.Context, ticketID uint) (*StartTicketResult, error)

type StartTicketResult struct {
    Ticket    contracts.Ticket
    Worker    *contracts.Worker  // nil if still queued
    Scheduled bool               // true if immediately activated
}
```

新 StartTicket 逻辑：
1. 加载 ticket
2. Guard: 只允许 backlog 和 blocked（收紧自当前的 `CanStartTicket`）
3. **blocked 分支**：
   - worker 存在且 running → 直接推 active → 返回（配额没释放过）
   - worker 存在但 stopped/failed → 尝试重启 → 成功推 active → 返回
   - worker 不存在 → 继续排队路径
4. **排队路径**：推到 queued（修改 `promoteTicketQueuedOnStart` 的 WHERE 子句，增加匹配 blocked）
5. 尝试立即调度（快速路径）
6. 返回

#### dispatch 相关文件 — **删除/改造**

| 文件 | 处理 |
|------|------|
| `dispatch.go` | 删除 |
| `dispatch_runner.go` | 删除 |
| `dispatch_agent_exec.go` | 删除（PM dispatch agent 不再存在） |
| `dispatch_queue.go` | 删除 |
| `dispatch_queue_complete.go` | 删除 |
| `dispatch_queue_workflow.go` | promote/demote 逻辑迁入 scheduler |
| `dispatch_queue_helpers.go` | 删除 |
| `dispatch_request_payload.go` | 删除 |
| `dispatch_submitter.go` | 删除 |
| `dispatch_depth_env.go` | 评估保留（worker 防嵌套） |
| `direct_dispatch.go` | 删除 |
| `worker_loop.go` | **保留改造**：去掉 dispatch 依赖，由 ActivateTicket 直接调用 |
| `dispatch_worker_sdk.go` | **重命名为 `worker_sdk.go`**，保留 SDK 启动逻辑 |

#### `workflow.go`

```go
// SetTicketWorkflowStatus → ForceSetTicketWorkflowStatus
func (s *Service) ForceSetTicketWorkflowStatus(ctx, ticketID, status, reason) error
```

`ApplyWorkerReport` 和 `ArchiveTicket` 不变。

### 4.5 Manager / Daemon 层

`manager_tick.go` 中 `scheduleQueuedTickets`：
- 移除 `dispatchScheduledTicket` 调用
- 替换为 `ActivateTicket`

zombie 重启链：
```go
// BEFORE
StopTicket → StartTicket → DispatchTicket  // 三步

// AFTER
StopTicket → ActivateTicket  // 两步（start 推 queued + activate 推 active）
// 或者如果 worker 已存在：直接 restartWorker + 推 active
```

daemon recovery：
- 移除 `RecoverStuckDispatchJobs`（没有 dispatch job 了）
- 保持 `RecoverActiveTaskRuns`
- 新增：扫描 queued tickets 确认状态一致性

新增事件驱动触发点（当前仅 tick 周期调度）：
- `ApplyWorkerReport(done)` 后触发 `scheduleQueuedTickets`
- `ArchiveTicket` 后触发 `scheduleQueuedTickets`

### 4.6 Worker Kernel 层

worker 需要能自主初始化。当前 worker 依赖 dispatch agent 注入的 prompt。

改造点：
- worker kernel 模板需包含自主初始化 SOP（读 ticket → 理解任务 → 执行）
- `executeWorkerLoop` 的 entry prompt 不再由 PM dispatch agent 生成
- worker 可通过 `dalek ticket show --ticket $TICKET_ID` 获取任务信息

### 4.7 CLI 层

| 子命令 | 变更 |
|---|---|
| `ticket start` | 调用新 `StartTicket`，返回 queued 或 active |
| `ticket dispatch` | 已标记 removed → 确认删除，输出迁移提示 |

### 4.8 PM Kernel 模板

- `<ticket>` 状态空间：queued 语义改为"等待资源配额"
- `<dispatch>` 部分：整节删除
- `<operations>`：移除 `ticket dispatch`
- `<sop>`：首次执行改为 `create → start` 两步

### 4.9 数据兼容

```sql
-- queued + 有 running worker → active
UPDATE tickets SET workflow_status = 'active', updated_at = CURRENT_TIMESTAMP
WHERE workflow_status = 'queued'
AND EXISTS (SELECT 1 FROM workers WHERE workers.ticket_id = tickets.id AND workers.status = 'running');

-- queued + 无 running worker → backlog
UPDATE tickets SET workflow_status = 'backlog', updated_at = CURRENT_TIMESTAMP
WHERE workflow_status = 'queued'
AND NOT EXISTS (SELECT 1 FROM workers WHERE workers.ticket_id = tickets.id AND workers.status = 'running');
```

PMDispatchJob 历史数据：保留表和数据，不再写入。

## 5. 执行分解

### Phase 1: FSM + Guards 更新（~200 行）

- `tables.go`：修改转换表
- `ticket_workflow_guards.go`：收紧 `CanStartTicket`，新增 `ShouldActivateOnSchedule`，删除 dispatch guard
- `contracts/ticket_status.go`：更新注释

### Phase 2: 删除 dispatch + 改造调度（~1000 行净变更）

- 删除 11 个 dispatch 文件（~2157 行删除）
- 改造 `worker_loop.go`（去 dispatch 依赖）
- 重命名 `dispatch_worker_sdk.go` → `worker_sdk.go`
- 改造 `scheduleQueuedTickets`（移除 dispatch 调用，替换为 ActivateTicket）
- 改造 `start.go`（简化为排队 + blocked 分支）
- 改造 zombie 重启链（移除 dispatch 步骤）
- 新增 ~500 行（ActivateTicket、调度触发、guard 逻辑）

### Phase 3: 裸 Set 降级（~100 行）

- `workflow.go`：`SetTicketWorkflowStatus` → `ForceSetTicketWorkflowStatus`

### Phase 4: Worker 自主初始化 + Kernel 对齐（~300 行）

- worker kernel 模板补充自主初始化指引
- PM kernel 模板删除 dispatch 概念
- CLI 对齐

### Phase 5: 数据迁移 + daemon recovery 改造（~150 行）

- migration script
- 移除 `RecoverStuckDispatchJobs`
- PMDispatchJob struct 标注 deprecated

## 6. 风险与决策点

### 6.1 Worker 自主初始化的充分性

当前 worker 依赖 PM dispatch agent 注入 prompt + structured_context。删除后 worker 需自己获取信息。

应对：
- ticket create 时 title + description + 验收标准必须写清楚
- worker kernel 包含初始化 SOP
- 不充分时 worker 通过 `report(wait_user)` 反馈

### 6.2 blocked 恢复路径

**决策：方案 B（分支判断）**

```
ticket start on blocked:
  worker 存在且 running → 直接推 active（配额没释放过）
  worker 存在但 stopped → 尝试重启 → 成功推 active，失败保持 blocked
  worker 不存在 → 走 queued 排队（需要新配额）
```

理由：blocked ticket 的配额从未释放，重新排队会被插队且配额计算矛盾。

### 6.3 并发安全

scheduler 在 manager tick 中串行执行（当前已是串行）。`ticket start` 的快速路径通过 `WHERE workflow_status = 'backlog'` 行锁保证幂等。

### 6.4 executeWorkerLoop 的 entry prompt

当前 worker loop 接收 PM dispatch agent 生成的 `entryPrompt`。删除 dispatch 后需要替代方案：
- 方案 A：固定 bootstrap prompt（从 worker kernel 模板生成）
- 方案 B：空 prompt，完全靠 kernel 里的指引
- 推荐方案 A——给 worker 一个标准化的初始 prompt 告知 ticket ID 和基本上下文

## 7. 故障模式与状态完备性分析

### 7.1 转换点故障矩阵

#### T1: backlog → queued（ticket start）

纯 DB 操作，无外部副作用。事务原子性保证要么完成要么不变。

| 故障 | ticket 状态 | 恢复 |
|------|------------|------|
| guard 拒绝 | 不变 | 预期行为 |
| DB 事务失败 | backlog | 重试 start |
| 并发 start | 幂等（WHERE 行锁） | 安全 |

#### T2: queued → active（ActivateTicket）

故障面最大的转换点。ActivateTicket 涉及外部副作用（worktree、SDK goroutine），不能全部放进 DB 事务。

```
ActivateTicket 步骤与可能的中间态：

  ① 创建 worktree      ← git 操作（不可事务回滚）
  ② 创建 worker 记录    ← DB
  ③ SDK 启动 agent      ← goroutine（不可事务回滚）
  ④ 推进 queued→active  ← DB 事务
```

| 故障 | ticket | worker | worktree | 恢复 |
|------|--------|--------|----------|------|
| ① 失败 | queued | 无 | 无 | 下次 tick 重试 |
| ① 成功 ② 失败 | queued | 无 | 孤儿 | defer 清理；失败则 worktree GC |
| ② 成功 ③ 失败 | queued | 存在/stopped | 存在 | 下次 tick 检测 worker → 重启 |
| ③ 成功 ④ 失败 | **queued** | **running** | 存在 | **关键场景**：下次 tick 幂等重试 ActivateTicket |

**幂等保证：**
```
ActivateTicket 重试时：
  检查 worktree → 已存在 → 跳过 ①
  检查 worker → 已存在 → 跳过 ②
  检查 worker → 已 running → 跳过 ③
  只执行 ④ → 推进 active
```

#### T3: active → blocked（worker report wait_user）

| 故障 | ticket | inbox | 恢复 |
|------|--------|-------|------|
| runtime 写成功 + workflow 事务失败 | active | 无 | **runtime-workflow 不一致** |
| 事务内 inbox 失败 | active（回滚） | 无 | worker 重发 report |
| 正常 | blocked | 创建 | 预期 |

runtime-workflow 不一致应对：zombie check 对比 runtime 最新 report 与 ticket 状态，发现不一致时重新 apply。

#### T4: active → done（worker report done）

`done` 与 `needs_merge` 在同一事务中。anchor 解析失败则 report 被拒绝，ticket 留在 active。

#### T5: blocked → 恢复

| 场景 | 路径 | 恢复 |
|------|------|------|
| worker 存在且 running | blocked → active | 直接推进 |
| worker 存在但 stopped | blocked → 尝试重启 → active/blocked | 失败保持 blocked |
| worker 不存在 | blocked → queued → active | 走排队路径 |

#### T6-T7: done → merged / archived

与旧模型一致，不受 dispatch 删除影响。

#### T8: worker 死亡（无 report）

Worker 是 SDK goroutine 调用（claude/codex/gemini SDK），死亡 = SDK 调用返回 error 或超时（30 分钟不活跃 watchdog）。

检测：zombie check 扫描 running workers，检查：
- 无 runtime handle（LogPath 为空）→ dead
- 有 runtime handle + 无 task status view → alive（允许刚启动尚未创建 run 的 worker）
- 有 runtime handle + 有 task status view + task run 已终态（非 pending/running）→ dead
- 活跃但 idle > 10 分钟 → stalled

恢复链（新模型）：
```
stop → ActivateTicket（而不是旧的 stop→start→dispatch）
最多重试 3 次，指数退避 60s→120s→240s，上限 10 分钟
全部失败 → blocked + incident inbox
```

#### T9: daemon 重启

| 残留状态 | 恢复 |
|----------|------|
| queued tickets | 下次 tick 的 `scheduleQueuedTickets` 继续调度 |
| active + worker alive | worker 继续运行（SDK goroutine 在 daemon 内） |
| active + worker dead | **注意**：daemon 重启 = 进程重启 = 所有 goroutine 死亡。所有 active workers 都变成 dead |

**daemon 重启 = 所有 SDK goroutine 终止。** 这是关键——当前 worker 作为 goroutine 运行在 daemon 进程内，daemon 重启意味着所有 worker 死亡。

恢复：
1. `RecoverActiveTaskRuns` 标记所有 pending/running task run 为 failed
2. zombie check 检测所有 active 但 dead 的 worker → 重启链
3. `scheduleQueuedTickets` 处理 queued tickets

#### T10: 配额计算

配额基准（保持现有逻辑）：
```
Progressable = running workers 中非 blocked 的
RunningBlocked = running workers 中 needs_user/stalled 的（不计入配额）
capacity = maxRunning - Progressable
```

以 worker 运行状态为准（不以 ticket 状态为准）。zombie check 修正异常 worker 状态后，配额自然修正。

### 7.2 状态完备性检查

#### backlog

| 事件 | 处理 |
|------|------|
| ticket start | → queued |
| ticket archive | → archived |
| worker report | 不应发生（无 worker），guard 不阻止但 FSM 无合法转换 |

#### queued

| 事件 | 处理 |
|------|------|
| scheduler 分配配额 | → active |
| ticket start（重复） | 幂等跳过 |
| ticket archive | → archived |
| 取消排队 | → backlog |

#### active

| 事件 | 处理 |
|------|------|
| worker report(done) | → done + needs_merge |
| worker report(wait_user) | → blocked + inbox |
| worker report(continue) | 留在 active |
| worker 死亡 | zombie check → 重启链 |
| ticket start | 幂等跳过 |
| ticket archive | 需先 stop worker |

#### blocked

| 事件 | 处理 |
|------|------|
| ticket start（worker 存在且 running） | → active |
| ticket start（worker 存在但 stopped） | 尝试重启 |
| ticket start（worker 不存在） | → queued |
| ticket archive | → archived |

#### done

| 事件 | 处理 |
|------|------|
| sync-ref 检测 merged | → integration=merged |
| merge abandon | → integration=abandoned |
| ticket archive（merged/abandoned） | → archived |
| ticket archive（needs_merge） | guard 拒绝 |
| worker report | `ShouldApplyWorkerReport` 返回 true，但 `CanReportPromoteTo` 阻止 done→active；done→blocked 无 FSM 转换 |
| ticket start | guard 拒绝 |

#### archived

终态。所有操作被 guard/FSM 拒绝。

### 7.3 与旧模型故障面对比

| 故障面 | 旧模型 | 新模型 |
|--------|--------|--------|
| dispatch job stuck | daemon recovery 清理 | **消除** |
| lease 过期竞争 | 存在风险 | **消除** |
| PM dispatch agent 编译失败 | ticket active 但 worker 没收到指令 | **消除** |
| DirectDispatchWorker 裸 set | 绕过 FSM | **消除**（直接删除） |
| worker 死亡重启 | stop→start→dispatch（三步） | stop→ActivateTicket（两步） |

## 8. 预估规模

| Phase | 改动量 | 说明 |
|---|---|---|
| Phase 1: FSM + Guards | ~200 行 | 转换表 + guard 收紧/新增/删除 |
| Phase 2: 删 dispatch + 改造调度 | ~1000 行净 | 删 ~2157 行，新增 ~500 行（ActivateTicket + 调度改造） |
| Phase 3: 裸 Set 降级 | ~100 行 | 重命名 + 签名 |
| Phase 4: Worker 初始化 + Kernel | ~300 行 | 模板 + CLI |
| Phase 5: 迁移 + recovery | ~150 行 | migration + daemon recovery 改造 |
| **合计** | **~1750 行净变更** | |

## 9. 验收标准

1. dispatch 物理删除 — 11 个 dispatch 文件删除，PMDispatchJob 不再写入新记录
2. ticket start → queued — start 推进到 queued 等配额（blocked 且 worker 存在时直接 active）
3. scheduler → active — 有配额时 queued → active，通过 SDK 直接启动 worker agent
4. worker 自主初始化 — worker 启动后无 PM dispatch agent 注入，通过 kernel 自行初始化
5. 配额排队 — 满配额时保持 queued，配额释放后自动调度
6. 状态转换全事件驱动 — 无裸 set 操作（ForceSet 仅供 repair）
7. 组合状态约束 — done+integration=none 不可达
8. `go test ./...` 通过
9. `go build ./cmd/dalek` 通过
10. kernel 模板与实际代码一致
