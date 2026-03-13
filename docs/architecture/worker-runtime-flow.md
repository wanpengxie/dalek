# Worker 运行流程梳理（按当前代码实现）

> 本文描述的是当前仓库里的真实实现，不是目标态设计。若与重构方案文档冲突，以代码为准。

## 1. 先分清 4 个核心对象

- `ticket`：业务任务本体，回答“要做什么”
- `worker`：ticket 的长期执行宿主，回答“在哪做、用哪套 worktree/runtime 做”
- `task_run`：worker 下面某一轮具体执行，回答“这一次 run 是什么状态”
- `worker report`：这一轮执行结束后提交的语义收口，回答“下一步是 continue、done 还是 wait_user”

当前最容易混淆的点是：`worker` 不是一次执行，也不是 ticket 本身。  
更准确地说，`worker = ticket 绑定的一套长期 worktree + runtime 上下文`，而 `task_run` 才是一轮一轮的实际执行记录。

相关代码：

- `internal/contracts/worker.go`
- `internal/contracts/task.go`

## 2. 一句话总览

当前 worker 主链路可以概括为：

```text
ticket start
  -> 准备 worker 资源
  -> ticket 进入 queued

worker run
  -> 解析 ticket / worker
  -> 必要时 auto-start
  -> 等 worker ready
  -> 写 bootstrap 文件
  -> 启动 stage 1
  -> run 被系统接受时把 ticket 推到 active
  -> 等 agent 退出
  -> 读取本轮 task_run 的 next_action
     -> continue: 再开下一轮 stage
     -> wait_user: report 收口，ticket -> blocked
     -> done: report 收口，ticket -> done，integration -> needs_merge
```

核心结论：

- `start` 只负责资源准备，不负责真正执行
- `queued` 只是“待调度 / 待执行”的中间态
- `active` 不是 `start` 推进去的，而是第一次 `worker run` 被系统接受时推进的
- loop 的真实控制信号不是进程退出码，而是 `worker report` 写入的 `next_action`

## 3. 主时序图

下面这张图只画主干，不展开所有异常分支。

```mermaid
sequenceDiagram
    participant Caller as CLI / ManagerTick
    participant PM as pm.RunTicketWorker
    participant Start as pm.StartTicket
    participant Bootstrap as bootstrap writer
    participant Loop as executeWorkerLoop
    participant SDK as launchWorkerSDK / SDKExecutor
    participant Runtime as task_runtime
    participant Agent as Worker Agent
    participant Report as dalek worker report
    participant Reducer as pm.ApplyWorkerReport
    participant Ticket as ticket workflow

    Caller->>PM: RunTicketWorker(ticketID, opt)
    PM->>PM: inspectWorkerRunTarget()
    alt worker 缺失或不 ready 且 autoStart=true
        PM->>Start: StartTicketWithOptions()
        Start-->>PM: worker + ticket queued
    end
    PM->>PM: waitWorkerReadyForDispatch()
    PM->>Bootstrap: ensureWorkerBootstrap()
    PM->>Loop: executeWorkerLoopWithHook()

    loop 每一轮 stage
        Loop->>SDK: launchWorkerSDKHandle()
        SDK->>Runtime: CreateRun + MarkRunRunning
        SDK-->>Loop: handle(run_id)
        opt 第一轮 stage
            Loop->>PM: acceptWorkerRun(ticket, worker, run_id)
            PM->>Ticket: queued/backlog/blocked -> active
        end
        Loop->>Agent: handle.Wait()
        Agent->>Report: dalek worker report --next ...
        Report->>Runtime: 写 runtime sample / semantic report / task event
        Report->>Reducer: ApplyWorkerReport(...)
        alt next=continue
            Reducer->>Ticket: 补齐 active 投影
            Loop->>Loop: 继续下一轮 stage
        else next=wait_user
            Reducer->>Ticket: active -> blocked
            Loop-->>PM: 停止 loop
        else next=done
            Reducer->>Ticket: active -> done
            Reducer->>Ticket: integration -> needs_merge
            Loop-->>PM: 停止 loop
        else 缺 report
            Loop->>Loop: 补报重试一次
        end
    end
```

读这张图时只要抓住 3 个点：

- `start` 只是准备资源，最多把 ticket 推到 `queued`
- 真正把 ticket 推到 `active` 的，是第一轮 run 被系统接受
- 真正决定 loop 继续还是停止的，是 report 里的 `next_action`

## 4. 入口分两类

### 4.1 `ticket start`

入口：

- `cmd/dalek/cmd_ticket.go`
- `internal/app/daemon_client.go`
- `internal/services/daemon/api_internal.go`
- `internal/services/daemon/execution_host.go`
- `internal/app/project_ticket_start.go`
- `internal/services/pm/start.go`

`StartTicketWithOptions(...)` 的职责是：

1. 校验 ticket 不是 `done/archived`
2. 启动或恢复 worker 资源
3. 让 worker 进入可调度状态
4. 把 ticket 从 `backlog/blocked` 推到 `queued`
5. 冻结 `target_branch`

这里不会直接启动 agent，也不会把 ticket 推到 `active`。

### 4.2 `worker run`

入口有两种：

- 手动：`dalek worker run --ticket N`
- 自动：`manager_tick.go` 中的 `scheduleQueuedTickets()`

两条路最终都会落到：

- `internal/services/pm/worker_run.go`
- `RunTicketWorker(...)`

所以真正进入 worker 执行主循环的入口，不是 `start`，而是 `RunTicketWorker`。

## 5. `start` 之后到底留下了什么

`start` 完成后，系统通常已经具备：

- 一条 `worker` 记录
- 一套 ticket 专属 worktree
- 对应 branch / runtime 锚点
- ticket.workflow_status=`queued`

但这时还没有真正开始一轮 `task_run`。

也就是说，`queued` 的语义更接近：

```text
资源准备好了，可以开始执行
但本轮执行还没有被系统 accept
```

相关代码：

- `internal/services/pm/start.go`
- `internal/services/pm/activation.go`

## 6. `RunTicketWorker` 的前置准备

`RunTicketWorker(...)` 在进入循环前，固定会做几件事。

相关代码：

- `internal/services/pm/worker_run.go`
- `internal/services/pm/runtime_helpers.go`
- `internal/services/pm/worker_ready_wait.go`
- `internal/services/pm/worker_bootstrap.go`

### 6.1 找到 ticket 和目标 worker

先查 ticket，再通过 `inspectWorkerRunTarget(...)` 取这张票最新的 worker。

这里的 guard 很宽：

- 只禁止 `done`
- 只禁止 `archived`
- `backlog / queued / active / blocked` 都还能进入 `worker run`

这也是为什么“手动 run”可以绕过标准的 `queued -> scheduler -> active` 路径。

### 6.2 判断是否有“可用 worker”

当前实现里，“可用 worker”并不是一个很强的语义。

`workerDispatchReady(...)` 现在基本只看一件事：

- `worker.LogPath` 是否非空

也就是说，当前所谓 ready 更像：

```text
这个 worker 已经有 runtime 锚点，可以继续被拿来发起 run
```

它不等于：

- 这个进程一定活着
- 这个 worker 一定正在执行

代码里还有一个更强的概念 `workerDispatchLive(...)`，会额外看：

- `worker.Status == running`
- 或这个 worker 还有 active task run

但 `RunTicketWorker(...)` 前置准备主要依赖的是 `ready`，不是 `live`。

### 6.3 必要时自动 `start`

如果：

- worker 不存在
- worker 不 ready
- worker 不在 `running/stopped`

并且 `autoStart=true`，那 `RunTicketWorker(...)` 会自动回调一次：

- `StartTicketWithOptions(...)`

所以 `worker run` 本身具备自我修复能力，不严格要求用户先显式执行 `ticket start`。

### 6.4 等 `creating` worker 变成可调度

如果最新 worker 还在 `creating`，系统会轮询等待，直到它变成：

- `running`
- 或 `stopped`

同时还得满足 `workerDispatchReady(...)`。

如果超时或状态异常，会直接报错，不进入主循环。

### 6.5 bootstrap 的完整流程

这部分按当前代码实际路径展开。  
bootstrap 不是一个“临时 prompt 拼接动作”，而是一条从项目初始化到 worktree 落盘再到 worker 读取的完整链路。

这里要严格区分两层：

- 模板源：项目级 `control/worker`
- 落地点：当前 worker worktree 下的 `.dalek/*`

### 6.5.1 项目初始化先准备模板源

项目初始化时，`EnsureControlPlaneSeed(...)` 会先确保：

- `.dalek/control/worker/worker-kernel.md`
- `.dalek/control/worker/state.json`

相关代码：

- `internal/repo/control_seed.go`
- `internal/repo/control_worker.go`

这一步的语义是：

- `worker-kernel.md` 是 worker 的项目级策略模板源
- `state.json` 是 worker 的项目级状态模板源
- 它们都放在 repo 自己的 `.dalek/control/worker/` 下

这里不是在为某一张 ticket 做 handoff，而是在为后续所有 worker run 准备“可复制的初始模板”。

如果项目里已经有这两个文件，初始化不会覆盖；如果没有，就从内置 seed 模板补齐。

### 6.5.2 `RunTicketWorker(...)` 进入 bootstrap 的前置输入

当 `RunTicketWorker(...)` 调到 `ensureWorkerBootstrap(...)` 时，bootstrap 已经拿到了：

- 当前 `ticket`
- 当前 `worker`
- 当前 run 的 `entryPrompt`
- 项目级 `layout`
- 当前 worktree 路径

`ensureWorkerBootstrap(...)` 开头会先做硬校验：

1. `ticket.ID` 不能为空
2. `worker.ID` 不能为空
3. `worker.WorktreePath` 不能为空

然后它会重新从 DB 读取一遍最新 ticket，而不是完全相信调用方传进来的旧快照。  
这样是为了保证 bootstrap 用的是最新 `title / description / target_branch`。

### 6.5.3 先确保 worktree 下的 `.dalek/` 契约目录存在

在真正渲染文件前，bootstrap 会先调用：

- `repo.EnsureWorktreeContract(...)`

它只做一件事：

- 确保 `${worktree}/.dalek/` 目录存在

并返回这几个固定落地点：

- `${worktree}/.dalek/agent-kernel.md`
- `${worktree}/.dalek/state.json`

注意这里：

- worktree 下的文件名仍然叫 `.dalek/agent-kernel.md`
- 但它的语义来源是项目里的 `control/worker/worker-kernel.md`
- 也就是“模板名是 worker-kernel，落地名沿用 agent-kernel 的历史约定”

### 6.5.4 再读取当前 worktree 的 git 基线

bootstrap 会调用：

- `repo.InspectWorktreeGitBaseline(...)`

收集当前 worktree 的运行事实：

- `HEAD sha`
- working tree 是 `clean` 还是 `dirty`
- 最近一次 commit subject

这些 git facts 会进入：

- `agent-kernel.md` 的 `<current_state>`
- `state.json.code`

也就是说，bootstrap 不是只读 ticket/worker 元数据，它还会把当前代码现场一起写进去。

### 6.5.5 渲染 `agent-kernel.md`

`agent-kernel.md` 的渲染来源不再是老的 skill assets 主路径，而是：

- 先读 `.dalek/control/worker/worker-kernel.md`
- 如果项目里没有，再回退到内置 seed 模板

相关函数：

- `repo.ReadControlWorkerKernelTemplate(...)`
- `renderWorkerKernelBootstrap(...)`

这一步会做两类替换。

第一类是直接替换占位符：

- `TICKET_TITLE`
- `TICKET_DESCRIPTION`
- `OTHER_DOCUMENTS`

其中：

- `OTHER_DOCUMENTS` 最终会展开成 ticket-local 事实块，里面有 `ticket_id / worker_id / target_ref / worktree / worker_branch`

第二类是重写 `<current_state>` 标签区块：

- 当前阶段
- phases 结构摘要
- 当前阻塞与风险
- 代码状态（HEAD / working tree / last commit）
- 下一步动作
- 更新时间

也就是说，`agent-kernel.md` 不是简单文本拷贝，而是“项目级 worker-kernel 模板 + 当前 ticket/worker/git 事实”的实例化结果。

### 6.5.6 渲染 `state.json`

`state.json` 的来源和 `agent-kernel.md` 一样：

- 先读 `.dalek/control/worker/state.json`
- 如果项目里没有，再回退到内置 seed 模板

相关函数：

- `repo.ReadControlWorkerStateTemplate(...)`
- `renderWorkerStateBootstrap(...)`

当前会替换这些最小运行时变量：

- `DALEK_TICKET_ID`
- `DALEK_WORKER_ID`
- `SUMMARY`
- `HEAD_SHA`
- `WORKING_TREE_STATUS`
- `LAST_COMMIT_SUBJECT`
- `NOW_RFC3339`

然后还会做一个额外校验：

- 替换后的结果必须是合法 JSON

如果不是合法 JSON，bootstrap 直接报错，不继续往下。

这点很关键，因为 `state.json` 是 runtime 协议的一部分，不能只靠“看起来差不多”。

### 6.5.7 bootstrap 只投影两份 runtime 文件

当前 worker bootstrap 主链只投影两份 runtime 文件：

- `agent-kernel.md`
- `state.json`

原来的辅助计划文件已经从 worker runtime 主链移除，不再作为 bootstrap 输入，也不再投影到 worktree。

### 6.5.8 最后落盘时，并不是每次都覆盖

渲染完 2 份内容以后，bootstrap 会调用 `ensureBootstrapFile(...)` 写入 worktree。

这个函数的策略不是“每次强制覆盖”，而是：

- 如果文件不存在：写入
- 如果文件为空：重写
- 如果文件里还残留 `{{...}}` 占位符：重写
- 如果 `state.json` 不是合法 JSON：重写
- 否则：保留现有文件，不覆盖

这意味着当前行为是：

- bootstrap 会在“模板未完成 / 文件损坏”时修复文件
- 但如果 worktree 里已经有一份结构完整、无 placeholder 的文件，它会保留现状

也就是说，当前 bootstrap 更像“ensure + repair”，不是“每轮都全量重编译”。

### 6.5.9 worker 最终会怎么读这些文件

bootstrap 结束后，真正给 worker 的入口 prompt 由 `buildWorkerEntrypointPrompt(...)` 生成。

它会明确要求 worker：

1. 先读 `.dalek/agent-kernel.md`
2. 再按其中 `context_loading` 去读 `.dalek/state.json`
3. 最后本轮结束前必须执行 `dalek worker report --next ...`

所以完整链路其实是：

```text
项目初始化
-> seed control/worker/worker-kernel.md + state.json

worker run
-> ensureWorkerBootstrap
-> 读取项目级 control/worker 模板
-> 读取当前 ticket + worker + git 基线
-> 渲染 worktree/.dalek/agent-kernel.md
-> 渲染 worktree/.dalek/state.json
-> 仅在缺失/损坏/残留 placeholder 时写入
-> worker 按 agent-kernel -> state.json 顺序读取
```

### 6.5.10 哪些是硬协议，哪些不是

在当前实现里，worker loop 与 PM 之间的最小硬协议仍然是：

- `.dalek/agent-kernel.md`：worker 的运行策略入口
- `.dalek/state.json`：基础状态契约
- `dalek worker report`：唯一硬性收口指令

### 6.6 尝试追加一次 `worker_run_start` 事件

在真正进入 loop 之前，代码会先尝试追加一条 worker task event：

- `worker_run_start`

这类事件是 best-effort 的可观测性补充，不是主流程收口条件。

## 7. 真正的核心循环

循环主体在：

- `internal/services/pm/worker_loop.go`
- `executeWorkerLoopWithHook(...)`

进入 loop 后，worker agent 首先读取的是当前 worktree 下的：

- `.dalek/agent-kernel.md`
- `.dalek/state.json`

其中 `.dalek/agent-kernel.md` 的语义来源是 repo/control 侧的 `worker-kernel.md`，只是落地到 worktree 后沿用历史文件名 `agent-kernel.md`。

它的每一轮都叫一个 `stage`。一轮 stage 的固定顺序是：

```text
1. launchWorkerSDK()
2. 得到 handle 和 run_id
3. handle.Wait()
4. 从 DB 读取这个 run 的 semantic_next_action
5. 判断是否继续下一轮
```

### 7.1 启动 stage

`launchWorkerSDKHandle(...)` 会做几件关键事：

1. 取消这个 worker 之前仍活跃的旧 run
2. 构造 worker 环境变量
3. 注入 `DALEK_DISPATCH_DEPTH`
4. 创建 `SDKExecutor`
5. 调 `executor.Execute(...)`

相关代码：

- `internal/services/pm/worker_sdk.go`
- `internal/services/agentexec/sdk.go`
- `internal/services/agentexec/run_lifecycle_tracker.go`

### 7.2 `task_run` 是在 agent 真正执行前创建的

`SDKExecutor.Execute(...)` 会先创建一条 `TaskRun`，然后把它标成 `running`。

因此：

- `run_id` 不是 report 时才有的
- `run_id` 在 agent 开始跑之前就已经存在

同时它还会把 `DALEK_TASK_RUN_ID` 注入环境变量，供 worker report 绑定当前这轮 run。

### 7.3 第一次 stage 启动成功后，ticket 才进 `active`

`RunTicketWorker(...)` 不是在进入 loop 之前就直接把 ticket 改成 `active`。  
它是通过 `executeWorkerLoopWithHook(...)` 的 stage-start hook，在第一轮 `run_id` 确认之后执行：

- `acceptWorkerRun(...)`

这一步会：

1. 把 worker 标记为 `running`
2. 追加 `ticket.activated`
3. 把 ticket 从 `queued/backlog/blocked` 投影到 `active`

所以：

```text
ticket start -> queued
worker run accepted -> active
```

这是当前实现里最关键的分界线。

### 7.4 loop 看的是 `next_action`，不是退出码

agent 退出后，循环会去 DB 里读这轮 `task_run` 的：

- `semantic_next_action`

也就是 `readWorkerNextActionFromRun(...)` 读到的值。

然后按这个值决定下一步：

- `continue`：再开一轮 stage
- `wait_user`：退出 loop，等待 PM report reducer 收口
- `done`：退出 loop，等待 PM report reducer 收口
- 空字符串：视为缺 report，先补报重试一次

所以这个循环本质上是：

```text
report 驱动循环
而不是进程退出驱动循环
```

## 8. `worker report` 怎么收口

相关代码：

- `internal/services/worker/report.go`
- `internal/services/worker/task_runtime.go`
- `internal/services/pm/workflow.go`

### 8.1 第 1 层：先写 task runtime 观测

`worker.ApplyWorkerReport(...)` 会优先把 report 写进 task runtime：

- runtime sample
- semantic report
- task event
- 必要时把 run 标为 succeeded

这里还会严格校验：

- `task_run_id` 必须存在
- 该 run 必须属于当前 worker/ticket
- 它必须是当前 active deliver run，或者是允许补收口的终态 run

所以 report 不是随便一写就能过，run 绑定是很硬的。

### 8.2 第 2 层：PM reducer 再推进 ticket workflow

`pm.ApplyWorkerReport(...)` 在 worker runtime 写入成功后，会根据 `next_action` 推 ticket：

- `continue` -> `active`
- `wait_user` -> `blocked`
- `done` -> `done`

这里的关键点是：

- `continue` 主要是“补齐 active 投影”
- `wait_user` 会顺带创建 needs_user inbox
- `done` 会顺带冻结 integration anchor，并把 `integration_status` 置为 `needs_merge`

所以 `done` 并不只是 workflow 变成 `done`，还会同步冻结集成信息。

## 9. loop 退出的 3 种正常语义

### 9.1 `continue`

本轮 report 表示“继续干”，于是：

- 当前 stage 结束
- loop 再启动下一轮 stage
- ticket 通常仍保持 `active`

### 9.2 `wait_user`

本轮 report 表示“需要人工介入”，于是：

- loop 停止
- PM reducer 把 ticket 推到 `blocked`
- 系统创建 needs_user inbox

### 9.3 `done`

本轮 report 表示“这张 ticket 已完成”，于是：

- loop 停止
- PM reducer 把 ticket 推到 `done`
- 同事务冻结 `anchor_sha/head_sha/target_ref`
- `integration_status` 进入 `needs_merge`

## 10. 关键异常分支

### 10.1 连续两轮没 report

如果一轮 stage 结束后没读到 `next_action`：

1. 系统先给一次补报机会
2. 把下一轮 prompt 改成更强的催促语
3. 如果第二次还没有 `next_action`，就抛 `workerLoopMissingReportError`

随后 `RunTicketWorker(...)` 会自动合成一条：

- `next_action=wait_user`

也就是系统会自动帮这张票阻塞下来，并要求人工介入。

相关代码：

- `internal/services/pm/worker_report_closure.go`

### 10.2 run 终态存在双写路径

当前实现里，`task_run` 的终态并不只有一条写入路径：

第一条路径：

- agent 退出时，`RunLifecycleTracker.Finish()` 会尝试写 `succeeded/failed/canceled`

第二条路径：

- `worker report --next done` 时，也会尝试把 run 标成 `succeeded`

因此代码里专门做了 duplicate terminal guard，避免重复写终态事件。  
这也是当前 worker 链路读起来比较绕的一个重要原因。

## 11. 最后给一个最实用的心智模型

如果只记一件事，记下面这版就够了：

```text
ticket
  是业务任务

worker
  是 ticket 的长期执行宿主
  里面有 worktree、branch、runtime 锚点

task_run
  是 worker 的某一轮实际执行
  每启动一次 agent，通常就新建一条 run

worker report
  是这轮 run 的语义收口
  它决定下一步是 continue / wait_user / done
```

把这四层拆开以后，再看当前代码就不会把这些东西混成一团：

```text
start 准备 worker
bootstrap 把 worker-kernel/state 投影到 worktree
run 启动 task_run
accept 把 ticket 推 active
report 决定 loop 继续还是收口
workflow reducer 负责把 ticket 推到 blocked/done
```
