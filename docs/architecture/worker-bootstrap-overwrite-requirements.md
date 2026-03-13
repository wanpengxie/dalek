# Worker Bootstrap 覆盖策略需求文档

## 1. 文档定位

本文是对 [worker-kernel-bootstrap-construction.md](/Users/xiewanpeng/agi/dalek/docs/architecture/worker-kernel-bootstrap-construction.md) 的补充约束。

它只解决一个具体问题：

- worker worktree 初始化时，`.dalek/agent-kernel.md` 和 `.dalek/state.json` 何时必须强制覆盖
- 何时只能做保守修复，不能无条件覆盖

本文不讨论：

- worker kernel 模板内容如何设计
- stage closure / worker report 语义
- ticket workflow 如何投影

## 2. 问题背景

当前 worker bootstrap 会把项目级 `control/worker/*` 模板投影到 worker worktree：

- `worktree/.dalek/agent-kernel.md`
- `worktree/.dalek/state.json`

但现实现有一个关键缺陷：

- worktree 如果已经存在一个“看起来完整”的 `.dalek/agent-kernel.md`
- 即使该文件实际上是 repo 自带的 PM kernel，而不是 worker kernel
- bootstrap 也会因为“文件非空、无占位符、格式合法”而跳过覆盖

这会导致 worker 在自己的 worktree 中继续读取 PM kernel，破坏 worker 自驱动执行前提。

## 3. 根需求

根需求只有一条：

**首次 worker bootstrap 必须以 worker 运行时模板为真相源，强制覆盖 worktree 中已有的 bootstrap 文件；只有 recovery/repair 才允许走保守刷新逻辑。**

换句话说：

- 首次 bootstrap：覆盖优先
- recovery/repair：保守优先

这两个阶段必须显式区分，不能复用同一套“看起来像成品就跳过”的判断。

## 4. 术语

### 4.1 模板源

项目级模板源指：

- `.dalek/control/worker/worker-kernel.md`
- `.dalek/control/worker/state.json`

它们是 worker bootstrap 的唯一模板真相源。

### 4.2 运行时投影

worktree 运行时投影指：

- `worktree/.dalek/agent-kernel.md`
- `worktree/.dalek/state.json`

它们是模板源在当前 worker worktree 中的物化结果。

### 4.3 首次 bootstrap

首次 bootstrap 的定义是：

**当前 `worker_id` 在系统中还没有任何历史 `deliver_ticket` 类型的 `task_run`。**

注意：

- 这是 worker 级定义，不是 ticket 级定义
- 不能通过 worktree 文件是否存在来判断
- 不能通过 ticket 是否曾经运行过来判断

### 4.4 recovery/repair

recovery/repair 指：

- 当前 `worker_id` 已经存在历史 `deliver_ticket` 运行记录
- 系统正在尝试恢复既有 worker 上下文，而不是第一次把该 worker 投入执行

## 5. 为什么不能靠文件状态判断

以下判断都不可靠：

### 5.1 不能靠 `.dalek/agent-kernel.md` 是否存在

因为 git worktree 或 checkout 流程可能把 repo 内已有 `.dalek/agent-kernel.md` 一并带入 worker worktree。

该文件可能是：

- PM kernel
- 历史残留文件
- 与当前 worker 不匹配的旧内容

所以“文件存在”不等于“worker 已 bootstrap”。

### 5.2 不能靠文件是否非空、无占位符、JSON 合法

这些条件只能说明“像个成品文件”，不能说明：

- 它是不是 worker kernel
- 它是不是属于当前 worker
- 它是不是本次运行应该使用的 bootstrap 内容

### 5.3 不能靠 ticket 是否跑过

因为同一张 ticket 可能重建出新的 worker。

对于新 worker：

- 即使 ticket 曾经跑过
- 该 worker 的首次 bootstrap 仍然必须强制覆盖

## 6. 行为需求

### 6.1 首次 bootstrap 必须强制覆盖

当系统判定当前 worker 为首次 bootstrap 时：

- 必须无条件重写 `worktree/.dalek/agent-kernel.md`
- 必须无条件重写 `worktree/.dalek/state.json`

这里的“无条件”指：

- 不看目标文件是否已存在
- 不看目标文件是否非空
- 不看目标文件是否包含占位符
- 不看目标文件内容是否格式合法

只要进入首次 bootstrap，就必须以模板源重新物化运行时文件。

### 6.2 recovery/repair 才允许保守刷新

当系统判定当前 worker 不是首次 bootstrap，而是 recovery/repair 时，才允许使用保守刷新策略。

保守刷新至少可以覆盖以下场景：

- 文件为空
- 文件仍含模板占位符
- `state.json` 不是合法 JSON

是否要进一步识别“这其实是 PM kernel 而不是 worker kernel”，可以作为增强项，但不属于本次最低落地要求。

### 6.3 判定必须基于运行态事实

系统必须在进入 `ensureWorkerBootstrap` 之前，基于数据库中的历史 `task_run` 事实判定：

- 当前 worker 是否已有历史 `deliver_ticket` run

推荐判定条件：

- `owner_type = worker`
- `task_type = deliver_ticket`
- `worker_id = 当前 worker`

若不存在任何匹配记录，则视为首次 bootstrap。

### 6.4 模板源优先级必须高于 worktree 现有文件

首次 bootstrap 时，`control/worker/*` 必须是唯一真相源。

不能让 worktree 中已有文件反向决定是否采用模板源。

## 7. 最低实现约束

本次改动至少需要满足以下实现约束：

### 7.1 bootstrap 链路必须能接收 force 语义

bootstrap 写文件链路必须显式支持 `force` 语义。

也就是说，系统要能表达：

- 这次写入是首次 bootstrap，必须覆盖
- 这次写入是 recovery，按保守策略处理

### 7.2 首次/恢复分流必须在运行链路上游完成

首次 bootstrap 与 recovery 的判断，必须在 worker run 主链路进入 bootstrap 之前完成。

不能把这个判断继续下沉为“读到文件以后再猜”。

### 7.3 `agent-kernel.md` 与 `state.json` 策略必须一致

首次 bootstrap 时，这两个文件都必须由同一判定结果驱动。

不能出现：

- kernel 强制覆盖
- state 仍按保守逻辑跳过

否则会产生跨文件不一致。

## 8. 验收场景

### 8.1 新 worker worktree 已带有 PM kernel

给定：

- 新建 worker，且该 `worker_id` 从未有过 `deliver_ticket` run
- worktree 中预先存在一个完整的 PM `.dalek/agent-kernel.md`

要求：

- 执行 bootstrap 后，最终文件必须被 worker kernel 覆盖

### 8.2 新 worker worktree 已带有完整 state.json

给定：

- 新建 worker，且该 `worker_id` 从未有过 `deliver_ticket` run
- worktree 中预先存在一个看起来合法的 `.dalek/state.json`

要求：

- 执行 bootstrap 后，最终文件必须被当前 worker 的 runtime state 覆盖

### 8.3 已跑过的 worker 再次进入 run

给定：

- 当前 `worker_id` 已存在历史 `deliver_ticket` run
- worktree 中已有完整 worker kernel 与合法 state

要求：

- recovery 路径不应无条件覆盖

### 8.4 recovery 时文件损坏

给定：

- 当前 `worker_id` 已存在历史 `deliver_ticket` run
- `agent-kernel.md` 为空，或 `state.json` 非法

要求：

- recovery 路径仍应修复损坏文件

## 9. 非目标

本次需求不要求同时解决以下问题：

- 识别所有历史错误 kernel 的类型并自动迁移
- 重命名 `worktree/.dalek/agent-kernel.md` 的历史文件名
- 引入新的 bootstrap 生命周期表
- 对 PM kernel/worker kernel 做复杂版本协商

本次只要求把“首次 bootstrap 必须强制覆盖”这条行为约束落地。

## 10. 结论

worker bootstrap 不能继续把“目标文件看起来像成品”当作是否覆盖的主要依据。

真正需要区分的是：

- 这是当前 worker 的第一次 bootstrap
- 还是一次既有 worker 的 recovery/repair

只有把这条分界线前移到运行态事实层，才能保证：

- 新 worker 不会误读 PM kernel
- recovery 不会不必要地破坏既有上下文
- worker bootstrap 真正成为 worker 运行前的强制初始化步骤
