# PM Loop 重建方案

## 1. 结论

当前 `pm loop` 模块需要整体删除并重建。

重建后的核心收敛只有四点：

1. `tick` 默认只做标准对账与收口，不再因为全局 `autopilot=true` 而自动推进 ticket。
2. PM 不再维持一个持续在线、持续建模的 planner loop，而是引入一个**唯一的、临时的 PM Focus 运行态**。
3. PM CLI 只暴露三种业务模式：
   - `manual`
   - `autopilot`
   - `plan`
4. `autopilot` 和 `plan` 都是**一批任务的临时运行上下文**，跑完就清空，不是 PM 的长期内核状态。

一句话：

`tick` 负责对账和驱动当前 Focus；
`Focus` 负责这一批 ticket/plan 的推进；
批次结束即停，不留下持续自动驾驶状态。

## 2. 为什么要推倒重来

当前实现的问题不是某个判断不准，而是整个 PM loop 已经长成了一个过重的系统：

- 全局 `autopilot` 开关会把 `tick` 变成默认自动推进器。
- `planner_dirty / planner_active_task_run_id / cooldown / wake_version` 把 PM runtime 搞成了一套特殊状态机。
- `pm_planner_run + PMOps + journal + checkpoint + recovery` 已经接近一个小型 workflow engine。
- `plan.json / plan.md / .dalek/pm/state.json / PMState` 同时存在，造成 PM 的运行面、图真相、渲染视图、运行态真相分裂。

这套东西的问题不是“不能用”，而是：

- 抽象过重
- 控制边界不清
- 自动推进范围过大
- 很难做明确止损

所以这次不修补，直接换模型。

## 3. 新模型

### 3.1 最小对象

重建后，PM 平面只保留这四个核心对象：

1. `ticket`
   基本执行单元，仍然是 repo 推进原语。

2. `pm tick`
   标准的对账与驱动动作。
   默认不自动 start/merge ticket。

3. `pm focus run`
   唯一的、临时的 PM 运行态。
   当且仅当进入 `autopilot` 或 `plan` 模式时存在。

4. `focus artifact`
   Focus 的输入与工作材料：
   - backlog snapshot
   - dag
   - doc
   - plan runtime overlay

### 3.2 关键原则

#### 原则 A：默认 tick 不自动推进

`tick` 在没有 active focus 时，只做：

- worker/ticket/runtime 对账
- 僵尸/异常状态收敛
- merge/integration 事实同步
- 各类派生视图更新

它**不会**：

- 自动 start 下一个 ticket
- 自动 merge 当前 ticket
- 自动唤醒 planner
- 自动改写 plan

#### 原则 B：自动推进只发生在 active focus 内

只有存在 active `pm focus run` 时，`tick` 才允许推进业务动作。

也就是说：

- `manual` 模式下：`tick` 只做控制面
- `autopilot` 模式下：`tick` 只推进当前 autopilot focus
- `plan` 模式下：`tick` 只推进当前 plan focus

#### 原则 C：Focus 是临时上下文，不是持续人格状态

`autopilot` 和 `plan` 的状态，不再长期挂在 PM agent kernel / user 上。

它们应该被定义成：

- 一次运行期的 Focus 上下文
- 可以共享给 PM agent
- 可以被 `tick` 驱动
- 但在 Focus 完成后，必须清空

所以 PM 的长期内核只保留：

- 通用 PM 规则
- 通用项目状态读取能力

不保留：

- 持续 autopilot 记忆
- 持续 plan 记忆
- 持续 planner dirty runtime

## 4. 三种模式

### 4.1 manual

这是默认模式。

语义：

- 人工决定何时 start ticket
- 人工决定何时 merge ticket
- `tick` 只对账，不自动推进
- 没有 active PM Focus

换句话说：

`manual = 没有 active focus`

### 4.2 autopilot

语义：

- 启动时，抓取一组 backlog ticket 作为当前批次
- 这组 ticket 构成当前 autopilot focus 的作用域
- 运行过程中：
  - 一个 ticket 完成后，先尝试合并
  - 若 merge 冲突，则异步提交一次 PM agent 解决冲突
  - 合并完成后，再顺序 start 下一个 ticket
- 当前批次跑完，autopilot 自动结束并清空

重要边界：

- autopilot 不是常驻开关
- 它只对应“当前这一批 backlog”
- 这一批跑完就停

### 4.3 plan

语义：

- 输入可以是 DAG，也可以是文档
- 如果输入是 DAG：
  - 直接创建一个 plan focus
  - 按 DAG 依赖顺序推进这一组 ticket
- 如果输入是文档：
  - 先异步提交一次 PM agent 任务，把文档转成可交付 DAG
  - DAG 生成成功后，再启动该 DAG 对应的 plan focus

最关键的点：

plan 模式的目标不是机械执行一个静态 DAG。

它真正要做的是：

- 维护整个 plan 的运行态
- 根据 ticket 完成/失败/阻塞情况更新 plan runtime
- 在需要时动态新增 ticket
- 在需要时动态调整/删除 ticket

但这些动态调整必须受预算约束，见第 7 节。

## 5. PM CLI 语义

我建议新 CLI 统一收敛成这组语义。

### 5.1 状态查看

```bash
dalek pm status
```

输出：

- 当前模式：`manual | autopilot | plan`
- 当前是否存在 active focus
- active focus 的 id / mode / status
- 当前 focus 的输入来源
- 当前批次进度
- 新增 ticket 预算使用情况

### 5.2 标准 tick

```bash
dalek pm tick
```

语义：

- 永远是标准 tick
- 在 `manual` 模式下只做对账
- 在有 active focus 时，额外驱动该 focus 前进一步

### 5.3 启动 autopilot

```bash
dalek pm run autopilot start
dalek pm run autopilot start --tickets 12,13,14
dalek pm run autopilot start --max-added 10
```

语义：

- 创建一个 `mode=autopilot` 的 focus
- 默认抓取当前 backlog 作为批次
- 也可以显式指定 ticket 集
- 同时记录本次 focus 的 `max_added_tickets`

### 5.4 启动 plan

```bash
dalek pm run plan start --dag path/to/dag.json
dalek pm run plan start --doc path/to/spec.md
dalek pm run plan start --doc path/to/spec.md --max-added 10
```

语义：

- `--dag`：直接进入 plan focus
- `--doc`：先创建文档到 DAG 的异步生成任务；生成成功后自动转入 plan focus

### 5.5 停止当前 focus

```bash
dalek pm run stop
dalek pm run stop --reason "manual takeover"
```

语义：

- 停止当前 active focus
- 停止后模式回到 `manual`
- 不保留持续自动推进状态

### 5.6 查看当前 focus

```bash
dalek pm run show
```

输出：

- active focus 的 mode/status
- 输入来源
- 当前 scope / DAG 运行态
- 已完成 / 运行中 / 阻塞 ticket
- 动态新增 ticket 次数 / 上限

## 6. PM Focus Run

### 6.1 定义

`PM Focus Run` 是 PM 平面的唯一运行态定义。

它不是长期 PMState，不是永久 planner 状态，而是：

- 一次临时运行上下文
- 当前最多一个 active
- 完成后必须清除

### 6.2 最小字段

第一版建议至少包含：

- `id`
- `mode`
  - `autopilot`
  - `plan`
- `status`
  - `queued`
  - `running`
  - `blocked`
  - `stopping`
  - `completed`
  - `failed`
  - `canceled`
- `input_type`
  - `backlog_snapshot`
  - `dag`
  - `doc`
- `input_ref`
- `scope_ticket_ids`
- `active_ticket_id`
- `dynamic_added_count`
- `dynamic_added_limit`
- `summary`
- `created_at`
- `started_at`
- `finished_at`

### 6.3 Plan 模式额外字段

如果 `mode=plan`，则 Focus 还需要挂一份当前 plan runtime：

- `dag_ref`
- `node_states`
- `ready_queue`
- `closed_nodes`
- `added_ticket_ids`
- `removed_ticket_ids`

这里的关键不是把 plan 长期写进 PM kernel，而是：

- 作为当前 focus 的临时运行态存在
- focus 完成后清空

## 7. 止损规则

这是新 PM loop 的核心硬约束。

### 7.1 批次跑完就停

#### autopilot

- scope 是启动时抓取的 backlog snapshot
- 这批 ticket 都收口后，focus 自动完成
- 系统模式自动回到 `manual`

#### plan

- 当前 DAG 跑完后，focus 自动完成
- 系统模式自动回到 `manual`

所以：

- 没有“永久 autopilot”
- 没有“永久 plan mode”
- 每次都需要显式重新启动下一批

### 7.2 动态新增 ticket 有预算上限

默认：

- `dynamic_added_limit = 10`

语义：

- 在 active focus 内，PM agent 或系统逻辑新增 ticket 时，都会消耗预算
- 达到上限后：
  - 不再允许自动新增
  - 当前 focus 进入 `blocked` 或 `needs_user`
  - 等人工决定是否开启下一轮 focus

这个预算同时适用于：

- `autopilot` 中因冲突/补救新增的 ticket
- `plan` 中因运行时调整 DAG 新增的 ticket

## 8. Tick 在三种模式下的行为

### 8.1 manual 下的 tick

只做：

- worker/ticket/runtime 对账
- merge 状态同步
- 僵尸/异常 worker 收敛
- 视图更新

不做：

- start ticket
- merge ticket
- 生成 plan
- 更新 DAG

### 8.2 autopilot 下的 tick

在标准对账之上，增加：

1. 看当前 focus 是否已有 active ticket
2. 如果有 active ticket 且未收口：
   - 只观察，不插手
3. 如果当前 ticket 已完成并可 merge：
   - 执行 merge
4. 如果 merge 冲突：
   - 异步提交 PM agent 冲突处理任务
   - 当前 focus 等待该任务结果
5. 如果当前 ticket 已 merge：
   - 从 scope 中选择下一个 ticket
   - start 它
6. 如果 scope 耗尽：
   - focus 完成
   - 回到 manual

### 8.3 plan 下的 tick

在标准对账之上，增加：

1. 如果当前 focus 的输入是文档，且 DAG 尚未生成：
   - 若没有 active DAG build run，则异步提交一次 PM agent 生成 DAG
   - 若 DAG build run 仍在跑，则等待
   - 若 DAG 生成成功，则进入 plan 执行阶段
2. 如果 DAG 已存在：
   - 维护当前 DAG runtime
   - 根据依赖关系选择 ready ticket
   - 推进当前 ready ticket
3. 当某个 ticket 收口后：
   - 更新 node state
   - 必要时允许 DAG 变更
   - 必要时新增/删减 ticket（受预算约束）
4. 所有节点收口后：
   - focus 完成
   - 回到 manual

## 9. PM Agent 在新模型里的职责

### 9.1 在 autopilot 模式下

PM agent 不是持续在线 planner。

它只在必要时被异步提交做局部工作，例如：

- 解决 merge 冲突
- 处理当前批次无法机械推进的异常分支

### 9.2 在 plan 模式下

PM agent 主要做两类事：

1. 文档 -> DAG
2. 运行中的 DAG 调整

它依然不是持续内驻的 loop，而是：

- 在当前 focus 上下文内被按需唤醒
- 输出 DAG 或 DAG patch
- 然后由系统应用到当前 focus runtime

## 10. 需要删除的旧模型

这次重建后，下面这些旧概念应该整体下线：

- 全局 `autopilot` 开关
- `planner_dirty`
- `planner_wake_version`
- `planner_active_task_run_id`
- `planner_cooldown_until`
- “manager tick 负责脏化并自动调度 planner run” 这条主路径
- 把 `pm_planner_run + PMOps + journal + checkpoint` 作为 PM 主循环骨架

保留但降级的部分：

- `plan.json`
  仍可以作为 DAG/plan artifact
- `plan.md`
  仅作为人类视图
- `.dalek/pm/state.json`
  仅作为 PM workspace 快照

也就是说：

- `plan.json / plan.md / state.json` 都不再驱动 PM loop
- 真正驱动 PM loop 的是 `PM Focus Run`

### 10.1 旧模块删除原则

这次不是“保留原链路，再加一层 Focus”，而是直接换主链。

所以删除原则是：

1. 任何“planner 自动唤醒”逻辑都应下线
2. 任何“全局 autopilot 开关驱动 tick 自动推进”的逻辑都应下线
3. 任何“planner loop 自己维护持续运行态”的字段都应下线
4. `PMOps + journal + checkpoint` 不再作为 PM 主循环基础设施

### 10.2 旧实现里应下线的重点对象

第一批应明确下线或降级的对象：

- `PMState.AutopilotEnabled`
- `PMState.PlannerDirty`
- `PMState.PlannerWakeVersion`
- `PMState.PlannerActiveTaskRunID`
- `PMState.PlannerCooldownUntil`
- `TaskTypePMPlannerRun`
- `maybeSchedulePlannerRun(...)`
- `planner recovery snapshot`
- `PMOps runner/journal/checkpoint` 作为 planner 主路径的一整套运行逻辑

### 10.3 新模型中保留的旧资产

以下资产不必删除，但要重新定位：

- `plan.json`
  从“planner loop 真相源”降级成“plan focus 可选输入/产物”
- `plan.md`
  只保留为可读视图
- `.dalek/pm/state.json`
  只保留为 PM workspace snapshot
- `pm state sync`
  只保留为观察/渲染工具，不再驱动 planner 决策

## 11. 新模型下的真相分层

### 11.1 对外业务态

PM 对外只暴露：

- 当前模式：`manual | autopilot | plan`
- 当前 active focus（若存在）

### 11.2 运行态真相

运行态真相在：

- `PM Focus Run`
- 当前 ticket / worker / merge 的真实运行态

### 11.3 视图层

以下都只是投影：

- `.dalek/pm/plan.md`
- `.dalek/pm/state.json`
- dashboard 文本/JSON

## 12. 实现顺序建议

### 第一阶段：拆旧自动驾驶

1. 去掉全局 `autopilot` 作为 manager tick 的门控
2. 把 `tick` 收缩成标准对账动作
3. 切断 `planner_dirty -> pm_planner_run` 的自动调度链

### 第二阶段：引入 Focus Run

1. 新增 `PM Focus Run` 定义
2. 保证当前最多只有一个 active focus
3. 为 `manual / autopilot / plan` 建立统一运行入口

### 第三阶段：重建 CLI

实现：

- `dalek pm status`
- `dalek pm tick`
- `dalek pm run autopilot start`
- `dalek pm run plan start --dag/--doc`
- `dalek pm run stop`
- `dalek pm run show`

### 第四阶段：把 PM agent 收缩成 Focus 内的异步能力

1. 文档生成 DAG
2. merge 冲突处理
3. plan 运行中的 DAG patch

而不是再保留一个全局 planner loop。

## 13. 最终判断

这次重建最重要的，不是把 PM agent 做得更聪明，而是把 PM 控制面做小：

- `tick` 默认不再自动推进
- 自动推进只存在于一个显式启动、显式结束的 Focus 中
- `manual / autopilot / plan` 都只是 Focus 的模式
- Focus 是临时运行态，不是持续 PM 人格

如果压成一句话：

**删掉现在的 PM 自动驾驶主链，把 PM 重建成“标准 tick + 唯一 Focus 运行态 + 模式化 CLI”的系统。**
