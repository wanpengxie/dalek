# MEDIUM（优先解决子集）

> 来源：`docs/arch_debt/source/ARCH_AUDIT_REPORT_2026-02-26.md`
> 生成日期：2026-02-26
> 入选条目数：13

入选标准：影响正确性/稳定性，或明显阻碍 CRITICAL/HIGH 清零的结构性问题。

## cmd/dalek（CLI 入口层）

- `CMD-M3` `cmd/dalek/cmd_task.go`：**cmd 层包含业务状态推导逻辑**。`deriveRunStatus()` 和 `mapTaskStatusPublic()` 在 cmd 层进行 task 运行状态推导和 DTO 映射，包含业务判断逻辑（如多时间戳确定最新状态）。应移到 `app.Task` 或 `app.Project`。 重要性：业务推导逻辑泄露到 CLI，会增加重构成本与一致性风险。

## internal/app（Facade 层）

- `APP-M1` `internal/app/home.go` 行275-428：**openProject/initProjectFiles 存在大量重复逻辑**。两个函数各自独立构造 `core.Project`，字段赋值基本相同，差异仅在于 init 额外写入 agent entry point 和 config.json。应抽取 `buildCoreProject()` 工具函数。 重要性：重复构造逻辑易分叉，后续重构时会出现行为不一致。

## internal/repo（仓库操作与模板）

- `RP-M2` `config.go` 行57-66：**模型名硬编码在 config 默认值中**。`defaultCodexModel = "gpt-5.3-codex"`、`defaultClaudeModel = "opus"`、`defaultCodexReasoningEffort = "xhigh"`。与 app/home.go 硬编码是同一类问题。应集中到一处或通过配置注入。 重要性：模型名/默认值硬编码分散，会与 APP-H6 等问题互相放大。

## internal/services/channel（Channel 服务 + agentcli）

- `CH-M3` 28 处 `context.Background()`：**context.Background() 在非顶层位置大量使用**。`failTurn` 中用 `context.Background()`(原始 ctx 可能已 cancelled)意味着 failure 路径脱离上层取消控制。每个方法开头 `if ctx == nil { ctx = context.Background() }` 应统一到入口 guard。应使用 `context.WithoutCancel(ctx)`(Go 1.21+)。 重要性：失败路径脱离上层取消控制，容易造成超时/资源释放异常。
- `CH-M5` `gateway_runtime.go` 行446-480：**streamedAny 标志位闭包捕获并发语义不明确**。通过闭包在回调中被写入、在主 goroutine 中读取。当前同步调用不存在 race，但语义依赖 ProcessInbound 实现细节。应改为 `atomic.Bool`。 重要性：隐含并发语义，未来实现改动可能引入数据竞争。

## internal/services/core（Core 领域模型）

- `CR-M2` `task_runtime.go` 行17-33：**TaskRuntime 接口定义 13 个方法接口过大**。违反接口隔离原则。不同消费者只用子集：执行器用 MarkRunRunning/RenewLease/MarkRunSucceeded 等；查询方用 FindRunByID/ListStatus；PM 用 CreateRun/CancelActiveWorkerRuns。mock 必须实现全部 13 方法。应按角色拆分为 TaskRunReader/TaskRunWriter/TaskRunCreator。 重要性：接口过大导致 mock/依赖扩散，阻碍 agent/run 解耦与测试。

## internal/services/daemon（Daemon 服务）

- `DM-M3` `api_internal_ws.go` 行133-146：**TrimSpace 对事件流 Text 字段的语义破坏**。对 `ev.Text` 做 TrimSpace 可能破坏消息内容语义（如 markdown 格式以空行开头）。这不是"防御性处理"而是"数据篡改"。应对 Text 字段不做 TrimSpace，只对 ID/状态等元数据 trim。 重要性：对 Text 做 TrimSpace 属于数据篡改，可能破坏用户内容/Markdown。
- `DM-M4` `execution_host.go` 行848-866：**probeWorkerRunID 使用忙等轮询**。2 秒内以 80ms 间隔轮询数据库（最多 25 次）探测 worker run ID。`DirectDispatchWorker` 接口设计缺少返回值（应直接返回 run ID），上层被迫轮询弥补。应修改接口使 worker run 创建后直接返回 run ID。 重要性：忙等轮询是接口设计缺陷的补丁，影响性能并增加复杂度。

## internal/services/pm（PM 服务）

- `PM-M5` `dispatch_runner.go` 行52-65：**lease renew goroutine 错误被静默吞掉**。`_ = s.renewPMDispatchJobLease(...)` 错误完全忽略。且使用 `context.Background()` 而非 parent context，任务取消后续租仍可能继续。lease 续租失败意味着 job 可能被抢占，不应静默。应至少 log 失败并在连续失败 N 次后主动取消。 重要性：可能导致 lease 续租失败被抢占且无告警，影响调度可靠性。
- `PM-M6` `context_cancel.go` 行11-30：**newCancelOnlyContext 存在 goroutine 泄露风险**。parent context DeadlineExceeded（非 Canceled）退出时不取消 child，启动的 goroutine 会等到 child 被手动 cancel 才退出。增加 goroutine 生命周期管理心智负担。 重要性：存在 goroutine 泄露风险，长期运行会积累隐患。

## internal/services/task（Task 服务）

- `TS-M1` `service_runs.go` 各 MarkRun* 方法：**task 状态转换没有显式状态机依赖 WHERE 条件实现幂等**。合法转换规则散布在 SQL WHERE 中，无集中的状态转换表。新增状态时容易遗漏某个方法的 WHERE 条件。应提取 `canTransition(from, to)` 纯函数。 重要性：状态机规则散布在 SQL WHERE，扩展状态时很容易遗漏导致逻辑错。

## internal/services/ticket（Ticket 服务）

- `TK-M1` `service.go` 行29-56：**Create 方法永远失败**。`Create(ctx, title)` 内部调用 `CreateWithDescription(ctx, title, "")`，而后者在 `description == ""` 时返回错误。提供了一个永远失败的公开方法。应删除 `Create` 或让它使用默认 description。 重要性：直接功能错误（公开方法永远失败），会误导调用方。

## internal/store（持久化层）

- `ST-M2` `models.go` 行452-459 vs `contracts/channel_gateway.go` 行15-18：**ChannelType 枚举在 store 和 contracts 重复定义**。同一套值("web"/"im"/"cli"/"api")两个包分别定义，命名风格不同(ChannelWeb vs ChannelTypeWeb)。应统一到一处。 重要性：重复枚举容易漂移，跨层协议会出现不一致与隐性 bug。
