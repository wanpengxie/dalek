# 深度架构反模式审查报告

> 分支 `arch-debt/base-20260226` vs `main`
> 审查日期：2026-02-27
> 审查方式：5 并行 code-reviewer subagent + PM 综合分析
> 审查维度：依赖方向与耦合、channel 子系统、FSM 与状态管理、执行层与服务拆分、God Object 与复杂度残留
> 定位：超越已有 AUDIT_REPORT 和 CODE_REVIEW 的覆盖范围，聚焦**新引入的反模式**和**已有报告定性不足的问题**

---

## 总览

| 级别 | 新发现 | 已知确认 | 合计 |
|------|--------|----------|------|
| CRITICAL | 4 | 1 | 5 |
| HIGH | 11 | 2 | 13 |
| MEDIUM | 8 | 2 | 10 |
| **合计** | **23** | **5** | **28** |

> "新发现" = 不在 AUDIT_REPORT / CODE_REVIEW 中的问题
> "已知确认" = 已有报告中记录但本次审查发现了更深层问题或确认其严重性

---

## 一、CRITICAL（5 项）

### CR-1: MarkWorkerFailed 吞掉事务错误，worker 状态更新静默失败 [NEW]

**文件**: `internal/services/worker/transition.go:101-117`

```go
_ = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    // ... worker status → WorkerFailed
})
return nil  // 无条件返回 nil
```

**问题**: `_ =` 丢弃了整个事务的返回值，且无条件 `return nil`。对比同文件 `MarkWorkerRunning`（正确处理错误）。后果：
- DB 写入失败时，worker 在 DB 中仍然是 `running` 状态，但事件链已追加 `worker_failed` 事件——**DB 状态与事件链不一致**
- manager autopilot 依赖 `worker.status` 调度容量，僵尸 `running` worker 占用槽位
- 调用方 `pm/worker_loop.go:140` 也 `_ =` 了返回值，形成**双重吞错**

### CR-2: ProcessExecutor — MarkRunning 失败后进程泄漏（僵尸进程）[NEW]

**文件**: `internal/services/agentexec/process.go:80-94`

`cmd.Start()` 成功后进程已在运行，但 `MarkRunning()` 失败时：
- 调用 `cancel()` 取消 context
- 返回 `nil, err`——**nil handle，无人 Wait()**

`exec.CommandContext` 的 kill 在 `cmd.Wait()` 时才生效，此处没人调用 Wait()，进程成为僵尸。

**修复**: MarkRunning 失败时，在 cancel() 之后显式 `_ = cmd.Wait()` 回收进程。

### CR-3: SubmitDispatch — handle 字段在持锁期间修改，但运行中 goroutine 无锁读取 [NEW]

**文件**: `internal/services/daemon/execution_host.go:181-206`

existing 分支在持 `h.mu.Lock()` 时修改 handle 字段（`existing.project`、`existing.workerID`），但 `executeDispatch` goroutine 在不持锁的情况下读取这些字段（`execution_host_runner.go:33-34`）。`*executionRunHandle` 无字段级同步，这是**数据竞态**。

### CR-4: newDaemonFeishuWebhookHandler — 616 行超级闭包，3 把 mutex + 6 个嵌套闭包 [NEW]

**文件**: `internal/services/channel/feishu/service.go:273-889`

从 app 层"下沉"来的代码，**只改变了物理位置，没有分解职责**。595 行闭包体内包含：
- HTTP 请求解析与鉴权
- 命令路由
- EventBus 订阅与 relay goroutine
- 进度卡片创建/更新
- 最终回复的三条路径
- 异步 goroutine 事件 select 循环

3 把 mutex（`finalMu`、`progressMu`、`writeMu`）+ 6 个嵌套闭包互相引用共享状态——无法独立测试任何子逻辑。

**修复方向**: 提取 `replyStrategy`（回复路径选择）、`progressTracker`（进度卡片管理）为独立 struct；relay goroutine 提取为独立方法。

### CR-5: CanReportPromoteTo 末尾 return true 使 FSM guard 形同虚设 [已知 CODE_REVIEW C1，本次确认]

**文件**: `internal/fsm/ticket_workflow_guards.go:88-89`

本次审查进一步确认了完整的**绕过 FSM 路径清单**：

| 路径 | 是否经过 FSM |
|---|---|
| `workflow.ApplyWorkerReport` | 经过 CanReportPromoteTo（但兜底 return true） |
| `recovery.applyTicketStatusForRecovery` | **完全绕过** |
| `direct_dispatch` 路径 | **完全绕过**，只检查 done/archived |
| `merge` 自动 archive | 无 FSM 检查 |
| `worker.MarkWorkerFailed` | 无 FSM 检查 |

FSM 当前角色更接近"文档"而非"约束"。

---

## 二、HIGH（13 项）

### H-1: recovery.go 中 applyTicketStatusForRecoveryTx 完全绕过 FSM [NEW]

**文件**: `internal/services/pm/recovery.go:295-331`

Recovery 路径可将 ticket 推为 `queued`（autopilot=true）或 `blocked`，前置检查只排除 done/archived，**不调用 `fsm.CanTicketWorkflowTransition()`**。例如 `active→queued` 在 FSM table 中不存在，但 recovery 会直接写入。

### H-2: core.TaskRuntime 接口签名泄露 store 层类型 [NEW]

**文件**: `internal/services/core/task_runtime.go:31`

```go
ListStatus(ctx, opt) ([]store.TaskStatusView, error)
```

`core.TaskRuntime` 是横跨所有 service 层的核心接口，但其方法返回 `store.TaskStatusView`。所有依赖此接口的 service 都被迫 import `store` 包。而 `TaskStatusView` 是 `store/models.go` 中唯一**未迁移到 contracts** 的完整 struct（非别名），打破了 `store` 层"纯别名"的设计约定。

**修复**: 将 `TaskStatusView` 迁移到 `contracts`，`store/models.go` 改为别名。

### H-3: channel.Service 在内部构造 pm/worker/ticket 完整依赖链 [NEW]

**文件**: `internal/services/channel/service.go:67-72`

```go
func (s *Service) actionExecutor() *ActionExecutor {
    ticketSvc := ticketsvc.New(s.p.DB)
    workerSvc := workersvc.New(s.p, ticketSvc)
    pmSvc := pmsvc.New(s.p, workerSvc)
    ...
}
```

channel→pm 横向 service 依赖，架构约束测试**未检查此方向**（只检查了 worker→pm）。

### H-4: feishu/service.go 2197 行四层职责混合 + 卡片 schema 重复 7 次 [NEW]

**文件**: `internal/services/channel/feishu/service.go`

文件包含 4 个完全不同的职责：
1. Webhook HTTP 处理（273-889 行）
2. 业务命令处理（1117-1264 行）
3. 飞书 UI 卡片构建（1332-1593 行）
4. HTTP 客户端实现（1676-2066 行）

飞书卡片 `schema: "2.0"` 的 base 结构在 7 个 card builder 函数中重复出现，无公共构建器。

**修复方向**: 拆为 `feishu_handler.go`、`feishu_commands.go`、`feishu_card.go`、`feishu_sender.go`。

### H-5: EventBus.Publish() 在热路径上执行同步 DB 写入 [NEW]

**文件**: `internal/services/channel/event_bus.go:117-148`

每个 agent 事件（包括流式 token）发布时都会同步写 `event_bus_logs`。在 Claude streaming 模式下，每条 token 触发一次 DB 写入。**DB 延迟直接阻塞事件分发到所有 WS 订阅者**。

**修复方向**: 异步写入（channel + 后台 goroutine），或只对终态事件写审计日志。

### H-6: WS handler 和 Gateway 的事件重放逻辑完全重复 [NEW]

**文件**:
- `internal/services/channel/ws/handler.go:158-231`
- `internal/services/channel/gateway_runtime.go:679-759`

两处独立实现了相同的 AgentEvent → 输出帧转换逻辑（finalRunID/lastSeq/finalSeq/跳过 lifecycle end/跳过空文本/跳过 reply 重复文本）。业务规则变更时必须同步修改两处。

### H-7: SDK/Process 两条执行路径的 goroutine 启动时机不一致 [NEW]

**文件**: `internal/services/agentexec/sdk.go:96-100` vs `process.go:96-157`

- SDK 路径：`Execute()` 内立即调用 `h.start()`，goroutine 启动
- Process 路径：goroutine 在 `Wait()` 时才启动，如果调用方不调 Wait()，进程成为僵尸

"统一执行层"的设计目标**未完全实现**——接口统一了，但生命周期管理不一致。

### H-8: WorkerLifecycleTable 缺少 TerminalStates 声明 [NEW]

**文件**: `internal/fsm/tables.go:39-58`

其他三个 FSM table 都声明了 TerminalStates，Worker 没有。`IsTerminal()` 对 worker 永远返回 false，未来 guard 函数仿照 ticket 模式写 `IsTerminal(workerStatus)` 判断会得到错误结果。测试中完全没有覆盖 `IsTerminal()` 行为。

### H-9: config 包 import app — "包名骗局" [已知 CODE_REVIEW H5，本次深化]

**文件**: `internal/config/config.go:12`

核心问题不是包名暗示了错误层级，而是**可达性风险**：一个名为 `config` 的包，新开发者很自然会在 service 层 import，但这会通过 `app` 带入整个 facade 层。架构约束测试未检查此边。

**修复**: 重命名为 `internal/clicfg`，并加架构约束测试。

### H-10: isTurnTerminal vs isGatewayTurnTerminalStatus 语义差异 [NEW]

**文件**:
- `internal/services/channel/service.go:996`
- `internal/services/channel/feishu/service.go:2176`

两个函数判断"turn job 是否终态"，但空字符串语义不同：channel 侧返回 false，feishu 侧返回 true。隐性 bug 风险。

### H-11: notebook/subagent 下沉只移动了文件，未抽取最小化依赖接口 [NEW]

**文件**: `internal/services/notebook/service.go:39-53`、`internal/services/subagent/`

两个服务都直接持有 `*core.Project`（重量级对象，12+ 字段），实际只用 3 个字段。`testutil.NewTestProject()` 需要 349 行来构造测试环境——印证了依赖过重。

### H-12: TicketView 双重定义 + 21 行纯转换层 [已知 CODE_REVIEW M9，本次确认为 HIGH]

**文件**: `internal/services/ticket/views.go:10`、`internal/app/api_types.go:97`

13 个字段一一对应，转换层无任何业务逻辑。每次新增字段需同步三处。

### H-13: dispatchOutbox 双重 DB 读取 + TOCTOU 竞态 [NEW]

**文件**: `internal/services/channel/service.go:734-800`

事务外读 adapter，事务内再读 outbox，两次读取之间无保护。成功路径事务内 UPDATE `RowsAffected=0` 时无错误处理。

---

## 三、MEDIUM（10 项）

### M-1: contracts 包承载 Normalize/Validate 业务逻辑，扇出极宽 [已知 CODE_REVIEW M6，深化]

`CanonicalTicketWorkflowStatus` 和 `NextActionToSemanticPhase` 是真正的业务知识，不应在纯数据契约包中。几乎所有包都 import contracts，修改影响面过广。

### M-2: TaskRunOrchestrationTable 的 Terminal 语义矛盾 [NEW]

**文件**: `internal/fsm/tables.go:81-106`

`TaskSucceeded`/`TaskFailed` 不是 terminal（可转向 Canceled），但 recovery.go 直接写入 `TaskFailed` 不经 FSM——task FSM 同样是"只装饰不约束"。

### M-3: RunLifecycleTracker.runID 无并发保护 [NEW]

**文件**: `internal/services/agentexec/run_lifecycle_tracker.go:39-51`

当前使用模式碰巧安全，但 tracker 本身不是并发安全的。

### M-4: processInboundItem 使用无超时的 context.Background() [NEW]

**文件**: `internal/services/channel/gateway_runtime.go:379-381`

所有 DB 操作使用无超时 Background context，DB 连接问题时无限期阻塞 worker goroutine。

### M-5: gatewaysend 包级便捷函数与 Service 方法重复 [NEW]

**文件**: `internal/services/gatewaysend/service.go:50-57`

`SendProjectText()` 每次调用创建新 Service 实例，与 `NewService() + svc.Send()` 功能重复，双入口混乱。

### M-6: logDaemonFeishuf/logf 重复 + 30+ 次绕过结构化日志 [NEW]

**文件**: `internal/services/channel/feishu/service.go:2184,2191`

两个函数行为完全相同，且 `fmt.Sprintf` 绕过了 slog 的结构化日志优势。

### M-7: runTurnJob defer 注册依赖运行时副作用字段 [NEW]

**文件**: `internal/services/channel/service.go:509-537`

cleanup defer 的注册条件（`tctx.slotAcquired`、`tctx.runningTurnSet`）依赖 `executeTurnAgent` 内部修改的字段。任何对 executeTurnAgent 的修改都可能静默破坏清理逻辑。

### M-8: CanArchiveTicket 不检查是否有未完成的 merge item [NEW]

**文件**: `internal/fsm/ticket_workflow_guards.go:21-32`

merge 流程中的 ticket 可被手动 ArchiveTicket 提前归档，因为 guard 只检查 dispatch 不检查 merge。

### M-9: Gateway.logInterrupt 与 Service.logInterrupt 同包重复实现 [NEW]

**文件**: `service.go:165`、`gateway_runtime.go:133`

### M-10: pending_action_store 使用逐行 INSERT 而非批量 INSERT [NEW]

**文件**: `internal/services/channel/pending_action_store.go:99-137`

GORM 支持批量插入，这里在事务内逐行 INSERT，增加不必要的锁持有时间。

---

## 四、系统性反模式总结

本次深度审查发现了 **3 个系统性反模式**，它们不是孤立的代码问题，而是贯穿整个重构的结构性缺陷：

### 系统性反模式 A: FSM "声明性约束与命令式绕过并存"

FSM 提供了优雅的 TransitionTable + Guard 设计，但实际代码中有 **至少 5 条路径完全绕过 FSM**（recovery、direct_dispatch、merge archive、worker status、task status）。FSM 的约束力仅覆盖了正常路径的一部分，在异常恢复和运维路径上完全失效。

**本质问题**: 引入 FSM 是正确的方向，但执行不彻底——需要在所有状态写入点都经过 FSM 校验，而不仅是"主路径"。

### 系统性反模式 B: "机械式文件搬移而非职责分解"

feishu（2197行从 app 搬到 services）、notebook、subagent 的下沉都只改变了物理位置：
- 依赖关系没有重构（仍然强依赖 core.Project）
- 职责边界没有分解（feishu 四层职责仍在一个文件）
- 重复代码没有消除（event replay、isTurnTerminal、logInterrupt 等）

**本质问题**: "下沉"不等于"重构"。正确的服务化需要：接口抽象 → 依赖注入 → 职责分离 → 测试独立化。

### 系统性反模式 C: "统一接口但不统一生命周期"

agentexec 建立了统一的 `Executor` 接口，但三条执行路径（SDK/Process/Tmux）的 goroutine 启动时机、进程回收责任、runID 获取方式仍然不一致。execution_host 的 handle 字段并发安全也没有统一策略。

**本质问题**: 接口统一是第一步，生命周期语义统一是更重要的第二步。

---

## 五、优先修复建议

### 必须立即修复（阻塞合并）

1. **CR-1** MarkWorkerFailed 事务错误处理 — 1 行修改
2. **CR-2** ProcessExecutor 进程泄漏 — 3 行修改（加 `_ = cmd.Wait()`）
3. **CR-3** SubmitDispatch handle 字段竞态 — 需要设计决策（字段级锁 or 禁止修改运行中 handle）

### 高优先级（合并后尽快处理）

4. **H-1** recovery.go 绕过 FSM — 加入 FSM 校验
5. **H-2** TaskStatusView 迁移到 contracts — 联动修复 store "纯别名"约定
6. **H-5** EventBus 审计写入异步化
7. **H-6** 事件重放逻辑去重

### 专项治理（下一个 sprint）

8. **CR-4 + H-4** feishu/service.go 职责分解 — 拆为 4 文件
9. **CR-5** FSM guard 补全 + 绕过路径收口
10. **H-3** channel→pm 依赖通过接口解耦
11. **H-7** 执行层生命周期语义统一
12. **H-11** notebook/subagent 最小化依赖接口

---

## 六、与已有报告的关系

| 已有报告发现 | 本次审查 |
|---|---|
| CODE_REVIEW C1 (CanReportPromoteTo) | 确认并扩展为完整的 FSM 绕过路径清单 |
| CODE_REVIEW H5 (config→app) | 深化为"可达性风险"，建议加架构约束测试 |
| CODE_REVIEW M6 (contracts Normalize) | 确认并量化扇出影响 |
| CODE_REVIEW M9 (TicketView) | 升级为 HIGH，确认增长趋势 |
| AUDIT PM-H5 (dispatch_queue 730行) | 未重复（已有跟踪 T34） |
| AUDIT TS-H1/H2 (core↔task 镜像) | 未重复（已有跟踪 T20） |
| **本次 23 项新发现** | **不在已有两份报告覆盖范围内** |
