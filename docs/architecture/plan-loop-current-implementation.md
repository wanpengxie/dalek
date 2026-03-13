# PM Plan Loop 当前实现梳理

## 1. 结论

当前 dalek 里的 `plan loop`，不是一个常驻、自由运行的 planner agent，也不是一个抽象的通用 loop engine。

它的真实实现是：

`manager tick`
-> 观察运行态并把 planner 标记为 dirty
-> 创建一条 `pm_planner_run` task run
-> daemon 提交 planner run
-> planner 读取当前项目快照并输出结构化 `PMOps`
-> executor 串行执行 `PMOps`
-> journal / checkpoint / PMState 收口

所以当前系统里，`plan loop` 更准确的定义是：

- 一个由 `manager tick` 驱动的、按需触发的 planner 调度闭环
- 不是一个一直在线的“planner 会话”
- 也不是一个独立于现有 manager/runtime 的通用 loop 基础设施

## 2. 当前实现里的核心对象

### 2.1 PMState：planner runtime 真相

planner 的运行态真相不在 `.dalek/pm/state.json`，而在 DB 的 `PMState`。

关键字段：

- `planner_dirty`
- `planner_wake_version`
- `planner_active_task_run_id`
- `planner_cooldown_until`
- `planner_last_error`
- `planner_last_run_at`

这些字段定义在：

- `internal/contracts/pm_state.go`

它们回答的是：

- planner 当前是否需要再跑一轮
- 当前是否已经有一条 active planner run
- 最近一次 planner 运行是否失败
- 当前是否在 cooldown

### 2.2 pm_planner_run：单次 planner 执行真相

每次 planner 真正执行时，系统不是直接“调用 planner 一下”，而是创建一条 task run：

- `owner_type = pm`
- `task_type = pm_planner_run`

这一层的真相在 task runtime：

- orchestration state
- 事件链
- error_code / error_message
- result payload

关键实现：

- `internal/services/pm/manager_tick.go`
- `internal/services/pm/planner_run.go`

### 2.3 plan.json：feature graph 真相

`.dalek/pm/plan.json` 是当前 feature graph 的 source of truth。

它描述：

- `feature_id`
- `goal`
- `docs`
- `nodes`
- `edges`
- `current_focus`
- `next_pm_action`
- `updated_at`

关键实现：

- `internal/app/pm_feature_graph.go`

### 2.4 plan.md：人类可读视图

`.dalek/pm/plan.md` 不是 source of truth。

它只是从 `plan.json` 渲染出来的可读视图，用来给 PM 和人看。

关键实现：

- `RenderPlanMarkdown(...)`
- `syncPlanMarkdownFromGraph(...)`

对应文件：

- `internal/app/pm_feature_graph.go`

### 2.5 .dalek/pm/state.json：workspace snapshot

`.dalek/pm/state.json` 也不是真相。

它是一个混合快照，里面同时放了：

- planner runtime snapshot
- dashboard ticket/worker/merge/inbox 统计
- feature graph 派生运行态
- acceptance 摘要

关键实现：

- `internal/app/project_pm_workspace.go`

它的角色更接近：

- 当前 PM workspace 的渲染产物
- 不是 planner runtime 的权威状态

### 2.6 PMOps journal / checkpoint：planner 执行持久化

planner 不是直接做动作，而是输出 `PMOps`，系统会把这些动作落成两类持久化对象：

- `loop_op_journal`
- `loop_checkpoints`

定义在：

- `internal/contracts/pm_ops.go`

实现在：

- `internal/services/pm/pmops_journal.go`
- `internal/services/pm/pmops_runner.go`
- `internal/services/pm/pmops_recovery.go`

## 3. plan loop 的真实时序

### 3.1 manager tick 负责唤醒 planner

主入口：

- `internal/services/pm/manager_tick.go`

每次 `dalek manager tick` 做的事情是：

1. 读取或初始化 `PMState`
2. 先对账当前 `planner_active_task_run_id`
3. 消费 task events
4. 扫 running workers
5. 处理 merge freeze / integration 观察
6. 根据上述变化决定是否 `markPlannerDirty`
7. 如果满足条件，就 `maybeSchedulePlannerRun`

planner 被唤醒的条件不是“固定周期到了”，而是：

- `planner_dirty = true`
- 当前没有 active planner run
- 不在 cooldown 中
- autopilot 开启

### 3.2 dirty 是怎么来的

planner 变 dirty 的来源，目前主要有三类：

1. `consumeTaskEvents(...)`
   例如：
   - `watch_error`
   - `interrupt_error`
   - `runtime_observation`
   - `semantic_reported`

2. `scanRunningWorkers(...)`
   例如：
   - worker `needs_user`
   - worker `stalled`

3. `freezeMergesForDoneTickets(...)`
   例如：
   - done ticket 需要补 integration freeze
   - merge 状态出现新变化

也就是说，planner 不是主动轮询世界，而是被系统运行态的变化“脏化”。

### 3.3 maybeSchedulePlannerRun 只创建 task run

`maybeSchedulePlannerRun(...)` 本身不执行 planner。

它只做：

- 创建一条 `pm_planner_run` task run
- 把 `planner_active_task_run_id` 指向它
- 清掉 `planner_dirty`

这一步发生在：

- `internal/services/pm/manager_tick.go`

### 3.4 daemon 再把 planner run 真正提交出去

manager tick 结束后，daemon manager component 会看：

- `PlannerRunScheduled`

如果为真，再走：

- `submitPlannerRunIfScheduled(...)`
- `buildPlannerSubmitRequest(...)`
- `ExecutionHost.SubmitPlannerRun(...)`

实现位置：

- `internal/app/daemon_manager_component.go`

也就是说：

- PM service 负责“决定该跑 planner”
- daemon execution host 负责“真正执行 planner”

### 3.5 planner run 真正执行时做什么

planner run 的主逻辑在：

- `internal/services/pm/planner_run.go`

执行过程是：

1. `MarkRunRunning`
2. 构造 planner prompt
3. 用 SDK runner 跑 PM agent
4. 流式事件不断写 task event 并续租 lease
5. 执行完成后，从输出里解析 `PMOps`
6. 执行 `PMOps`
7. 写 checkpoint
8. 最后把 planner run 标成 succeeded / failed / canceled

## 4. planner 读什么输入

planner prompt 是动态构造的，不是只读 `plan.md`。

当前 prompt 主要包含这些输入：

- `plan.md`
- `dalek pm state sync` 的结果
- `ticket ls`
- `merge ls`
- `inbox ls`
- `planner recovery snapshot`
- `surface_conflicts`

关键实现：

- `buildPlannerPrompt(...)`
- `internal/app/daemon_manager_component.go`

这意味着当前 planner 的输入已经不是单文档，而是一组聚合快照。

## 4.1 从 PM agent 视角看，这条 loop 是怎么运行的

如果只站在 PM agent 自己的视角，这条链路可以压成 4 件事：

### A. 它什么时候会被激活

PM agent 不会一直在线运行。

它只会在以下条件同时满足时被系统唤醒：

- `autopilot = true`
- `planner_dirty = true`
- `planner_active_task_run_id = nil`
- 当前不在 `planner_cooldown_until` 冷却窗口内

也就是说：

- 不是“固定间隔跑一次”
- 也不是“有 plan.json 就一直跑”
- 而是 manager 观察到运行态变化后，把 planner 标成 dirty，再创建一轮新的 `pm_planner_run`

### B. 它被唤醒时到底看到了什么

planner 被真正提交执行时，看到的不是裸 repo，也不是单一 `plan.md`。

它收到的是一段聚合 prompt，里面至少包含：

- `plan.md`
- `dalek pm state sync` 的结果
- `ticket ls`
- `merge ls`
- `inbox ls`
- `planner recovery snapshot`
- `surface_conflicts`

换句话说，PM agent 当前看到的是：

- 一份人类可读的计划视图
- 一份结构化 PM workspace 快照
- 一组 ticket / merge / inbox 列表
- 一份 planner recovery 上下文

而不是：

- 直接操作数据库
- 直接拥有 planner runtime 真相

### C. 它收到的指令到底是什么

planner prompt 不是开放式“随便决策”。

当前 prompt 明确要求它：

- 不要直接执行任何 dalek CLI 或 shell
- 不要直接改产品源码
- 只输出结构化 `PMOps`
- 输出必须包在 `<pmops>...</pmops>` JSON 块中
- 执行由系统 executor 串行处理

所以从 PM agent 视角看：

- 它不是执行者
- 它是一个结构化决策器

### D. 它实际完成的任务是什么

PM agent 在单次 planner run 里的真实任务只有一个：

**读当前项目快照 -> 产出一组结构化 PMOps**

它自己不直接：

- `create ticket`
- `start ticket`
- `close inbox`
- `run acceptance`

这些动作虽然看起来像是“planner 做的”，但实际是 executor 做的。

### E. 它的结果是什么

从 PM agent 视角，单次 planner run 的结果不是“项目已经推进了”，而是：

- 输出了一组 `PMOps`
- 这些 `PMOps` 被 parser 解析
- executor 尝试执行
- journal 记录每个 op 的状态
- checkpoint 记录这一轮 planner run 的执行摘要

所以 planner 自己交付的结果，其实是：

- **结构化决策**

不是：

- 最终状态变更本身

### F. 它看不到什么

PM agent 自己并不直接拥有以下真相：

- `PMState` 的持久化写入
- `plan.json` 的最终写回
- journal / checkpoint 的最终落库结果
- task run 的最终 succeeded / failed / canceled 收口

这些都属于系统侧收口。

所以从 agent 视角看，它更像：

- “被唤醒一次”
- “看一份大快照”
- “吐出一组结构化动作”
- “然后系统去落”

这也是当前实现里 planner 和 executor 已经明显分开的原因。

## 5. planner 产什么输出

当前 planner 不该直接执行 CLI。

prompt 明确要求它：

- 只输出 `<pmops>...</pmops>` JSON
- 顶层格式是 `{"ops":[...]}`

解析逻辑在：

- `internal/services/pm/pmops_parser.go`

支持：

- `<pmops>...</pmops>`
- 裸 JSON
- `ops` / `pmops` / `operations` 三种顶层键

### 当前支持的 PMOp kind

- `write_requirement_doc`
- `write_design_doc`
- `create_ticket`
- `start_ticket`
- `create_integration_ticket`
- `close_inbox`
- `run_acceptance`
- `set_feature_status`

注意：

- `write_requirement_doc`
- `write_design_doc`

目前还是 `noop`，只是占了一个结构化动作的位置。

## 6. PMOps 怎么执行

执行器实现：

- `internal/services/pm/pmops_executor.go`
- `internal/services/pm/acceptance_engine.go`

执行顺序是：

1. 先把 op 写成 journal entry，状态为 `planned`
2. 对每个 op 先 `Reconcile(...)`
3. 若已满足，就直接 mark succeeded
4. 若未满足，再真正 `Execute(...)`
5. 执行成功 -> `succeeded`
6. 执行失败 -> `failed`
7. 若是 critical op 失败，后续 op 会被 `superseded`

这一步是当前 planner loop 已经明显“工程化”的部分，因为它已经不再是简单地“planner 给建议”，而是一个显式的 op 执行系统。

## 7. recovery 现在怎么做

planner recovery 当前不是“恢复 planner 思维”，而是：

**恢复正在 running 的 PMOps journal entry**

逻辑在：

- `internal/services/pm/pmops_recovery.go`

做法：

1. 找到 `status=running` 的 journal entry
2. 判断对应的 planner run 是否已经 terminal
3. 如果 terminal：
   - 调 executor 的 `Reconcile(...)`
   - 能对账就补成 `succeeded`
   - 对不上就改成 `failed`

这说明当前 recovery 是：

- `op-level reconcile recovery`

不是：

- `whole planner loop replay`

## 8. 现在到底有哪些真相源

这是当前实现最容易让人混乱的地方。

### 真相 1：planner runtime 真相

- DB 里的 `PMState`

它回答：

- planner 是否 dirty
- planner 是否有 active run
- planner 是否在 cooldown

### 真相 2：planner execution 真相

- `pm_planner_run` 对应的 task run

它回答：

- 本轮 planner 有没有在跑
- 跑成功了还是失败了
- 输出了什么结果

### 真相 3：feature graph 真相

- `.dalek/pm/plan.json`

它回答：

- feature 当前有哪些 node
- 哪些 node done / pending / blocked
- 当前 focus 是什么

### 投影 1：plan.md

- 从 `plan.json` 渲染出来的人类可读视图

### 投影 2：.dalek/pm/state.json

- 从 graph + dashboard + acceptance 派生出来的 workspace snapshot

所以当前系统里不是“一条真相”，而是：

- runtime 真相
- execution 真相
- graph 真相
- 两个渲染/快照投影

## 9. 现在已经开始过度工程化的地方

我认为主要有 4 处。

### 9.1 planner runtime / planner execution / graph / snapshot 已经有四层

当前一个 `plan loop` 已经同时涉及：

- `PMState`
- `pm_planner_run`
- `plan.json`
- `plan.md`
- `state.json`
- journal
- checkpoint

这已经不是轻量系统了。

如果继续往上叠：

- 通用 `Loop Engine`
- 通用 `TaskLoop`
- feature loop / milestone loop / acceptance loop

就很容易把系统推到“为了管理 loop 而管理 loop”。

### 9.2 PMOps + executor 已经是一套 mini workflow engine

当前 planner 不只是“提出计划”，而是：

- 输出结构化 op
- journal
- checkpoint
- reconcile
- idempotency
- executor

这已经具有明显的 engine 特征。

如果再引入一层新的通用 engine，很可能是重复包装。

### 9.3 `plan.md` 和 `.dalek/pm/state.json` 都是投影，容易制造额外认知负担

当前系统里：

- `plan.json` 是真相
- `plan.md` 是可读视图
- `state.json` 是 workspace 快照

这本来可以接受，但它已经要求使用者始终记住：

- 哪个是真的
- 哪个只是渲染
- 哪个只是当前状态快照

如果后续再增加新的“更高层 loop 状态文档”，就更容易走向多重投影地狱。

### 9.4 `write_requirement_doc / write_design_doc` 还是 noop，说明抽象超前于真实能力

当前 PMOps 已经为文档写入预留了 kind，
但真实执行还没有落地，只能 noop。

这说明现在有一部分抽象已经先于真实实现出现了。

如果在这个阶段继续扩 generic loop abstraction，风险很高。

## 10. 当前更稳的收敛判断

如果按现状收敛，我认为更稳的判断是：

1. **不要再新增一层通用 `Loop Engine` 抽象**
   当前系统实际上已经有：
   - `manager tick`
   - planner run
   - PMOps executor
   - journal/checkpoint

2. **把当前 planner loop 视为一个“特化的调度闭环”**
   不要急着把它抬升成通用 loop 平台。

3. **坚持 `plan.json` 是唯一 feature graph 真相**
   不要再给 `plan.md` 或 `state.json` 增加决策责任。

4. **优先修 runtime truth 和 graph truth 的边界**
   而不是继续增加更多层次。

## 11. 最后一版压缩结论

当前 `plan loop` 的最准确描述是：

- `manager tick` 负责观察世界并唤醒 planner
- `pm_planner_run` 是 planner 的单次执行实体
- planner 读取项目快照，输出结构化 `PMOps`
- `PMOps executor + journal + checkpoint` 负责动作落地和恢复
- `plan.json` 是 feature graph 真相
- `plan.md` 和 `.dalek/pm/state.json` 都是投影

如果继续往前设计，最大的风险不是“功能不够”，而是：

**已经有一套半成形的 loop engine 了，再继续抽象，很容易过度工程化。**
