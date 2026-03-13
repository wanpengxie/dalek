# Worker Loop Spec Stack

> 本文的目的不是继续发明抽象，而是把 dalek 在当前阶段**真正需要实现的 spec** 一次性梳理清楚。
>
> 它回答三个问题：
>
> 1. 哪些文档只是前提和推导，不是实现 spec
> 2. 哪些 spec 是必须实现的
> 3. 哪些东西现在不该做，否则会把系统推向过度工程化

## 1. 先澄清：什么叫“需要实现的 spec”

在当前语境里，`spec` 不是泛泛的思考稿，也不是完整未来图景。

这里的 `spec` 指的是：

- 能直接约束代码实现
- 能定义运行时边界
- 能回答状态推进由谁负责
- 能回答出错时谁来纠偏
- 能回答什么时候允许写回 ticket 主世界状态

因此，不是所有架构文档都属于“实现 spec”。

## 2. 当前文档分层

### 2.1 前提文档

- [system-foundation-premises.md](/Users/xiewanpeng/agi/dalek/docs/architecture/system-foundation-premises.md)

它回答：

- 底层 agent 的基本性质是什么
- dalek 的顶层目标是什么

它是前提，不是实现 spec。

### 2.2 foundation 推导文档

- [worker-loop-foundation.md](/Users/xiewanpeng/agi/dalek/docs/architecture/worker-loop-foundation.md)

它回答：

- 为什么 `worker loop` 必须存在
- 为什么 `single step` 不能等于裸 `raw agent run`
- 为什么 `ticket execution` 必然跨越多个单步
- 为什么上层不能自己补 ownership / recovery / handoff

它是 foundation 推导，不是最终实现 spec。

### 2.3 当前过厚的 spec 草案

- [single-step-spec.md](/Users/xiewanpeng/agi/dalek/docs/architecture/single-step-spec.md)

这份文档的方向大体是对的，但当前版本有过度工程化风险：

- 把太多概念写成了潜在 runtime 一等对象
- 生命周期状态拆得过细
- 容易把 dalek 推向“通用 step runtime / graph runtime”

因此，它目前更像“中间过渡 spec”，不能直接当最终落地蓝图。

## 3. 真正需要实现的 spec 集合

当前阶段，dalek 真正需要的实现 spec 只有 4 份。

### 3.1 `stage-closure-spec.md`

这是最小、最先要落地的 spec。

它定义：

- 当前 `worker loop` 中一轮 `stage` 的闭合条件
- `raw agent run` 结束后，什么才算这轮真的收口
- 缺 report / 非法 report / 不足 report 时，如何在当前 stage 内纠偏
- 哪些结果可以写回 ticket，哪些只能算 artifact
- 当前 stage 结束时，必须留下什么 handoff / state summary

它只解决一个问题：

**当前一轮 stage 怎么从“裸 raw run + report”变成一个最小但可靠的推进单元。**

这是当前最必要的机制。

### 3.2 `worker-loop-governance-spec.md`

这是 loop level 的治理 spec。

它定义：

- 同一 ticket 任意时刻谁拥有有效 loop
- loop 从哪里恢复
- 用户澄清、人工接管、worker 重建之后，旧 loop 是否失效
- loop 何时继续、何时暂停、何时升级、何时停止
- late result / stale result 在 loop 级别如何处理

它解决的问题不是某一小步怎么收口，而是：

**整张 ticket 的持续推进怎么不失控。**

### 3.3 `ticket-state-projection-spec.md`

这是 ticket 主世界状态投影 spec。

它定义：

- 哪些 stage 闭合结果可以推进 ticket 主状态
- `continue / wait_user / done / failed / escalated` 如何映射到 ticket workflow
- 什么时候创建 inbox
- 什么时候冻结 merge / integration 语义
- 哪些只是运行时观测，哪些会改变 ticket 世界

它解决的问题是：

**闭合后的 stage 结果，如何进入 ticket 主世界状态。**

当前代码里这部分散落在：

- `worker report` 摄入
- PM workflow reducer
- inbox side effects
- done integration freeze

因此必须单独抽成一份 spec。

### 3.4 `worker-report-contract-spec.md`

这是最窄的一份契约 spec。

它定义：

- 当前 `dalek worker report` 的最小字段集
- 哪些字段是必填
- 哪些字段是候选控制输出，不是权威写回
- report 合法性校验规则
- report 与 stage closure 的关系

它不该独立膨胀成大系统，但也不能继续只靠零散代码注释和 prompt 约定。

它解决的问题是：

**worker 到 control plane 的输入面到底是什么。**

## 4. 这 4 份 spec 的依赖关系

实现顺序应该是：

1. `stage-closure-spec.md`
2. `worker-report-contract-spec.md`
3. `worker-loop-governance-spec.md`
4. `ticket-state-projection-spec.md`

依赖关系如下：

- `stage closure` 先定义“单步如何闭合”
- `report contract` 定义“闭合输入面长什么样”
- `loop governance` 定义“多个 stage 如何在同一 ticket 内持续推进”
- `ticket state projection` 最后定义“闭合结果如何进入 ticket 主世界”

换句话说：

- 没有 `stage closure`，后面都站不住
- 没有 `loop governance`，系统只能保证局部收口，不能保证长期控制
- 没有 `ticket state projection`，闭合结果仍然会散落在运行时和 PM reducer 里

## 5. 当前不该做的 spec

下面这些东西现在不该做，至少不该作为一等实现目标去做。

### 5.1 不该做“通用 single-step runtime”

不应该把 `single step` 做成一套独立执行引擎。

原因：

- dalek 不是通用 agent orchestration 平台
- dalek 的核心对象是 `ticket`
- 我们要做的是强化现有 `worker loop`，不是再造一个 graph engine

### 5.2 不该把所有概念都落成一等实体

例如下面这些概念，目前不该先做成独立持久化对象：

- `step claim`
- `execution_id`
- `owner_lease`
- `control violation` 独立实体
- `step settlement` 独立表
- `ticket execution` 独立实体

这些可以作为解释层概念，但不该直接转成运行时对象宇宙。

### 5.3 不该先做复杂生命周期状态机

例如：

- `opened`
- `executing`
- `validating`
- `correcting`
- `settling`
- `closed`

这类细粒度状态适合帮助思考，不适合现在直接实现成完整 FSM。

真正需要实现的是：

- 当前 stage 是否已闭合
- 当前 loop 是否仍有效
- 当前闭合结果是否允许推进 ticket

### 5.4 不该先做通用 generation/lease 系统

`generation`、`epoch`、`lease` 这些概念是有价值的，但现在不该先做成一套通用分布式控制协议。

只有在 `worker-loop-governance-spec` 写清楚下面这些具体场景后，才允许最小化引入：

- 用户澄清导致旧 loop 结果失效
- worker 换绑导致旧 loop 结果失效
- 人工接管导致旧推进链让渡控制权

## 6. 每份 spec 必须回答的最小问题

### 6.1 `stage-closure-spec.md` 必须回答

1. 当前 stage 何时开始，何时结束
2. 当前 stage 内允许多少次 raw run 纠偏
3. 缺 report / 非法 report 时如何反馈给 agent
4. 当前 stage 闭合时最少必须写下什么
5. 哪些结果只算 artifact，哪些可以进入 accepted progress

### 6.2 `worker-loop-governance-spec.md` 必须回答

1. 同一 ticket 当前哪个 loop 有效
2. loop 恢复时从哪一个已闭合点继续
3. 用户插话、worker 重建、人工接管后如何处理旧 loop
4. loop 何时必须停止 / 升级 / 交回 PM
5. late result / stale result 如何不污染当前世界

### 6.3 `ticket-state-projection-spec.md` 必须回答

1. stage 闭合结果到 ticket workflow 的映射规则是什么
2. 什么时候创建/关闭 inbox
3. 什么时候进入 blocked / done / active
4. 哪些 side effect 属于 ticket 世界的一部分
5. 哪些只是 runtime 观测，不应写入主状态

### 6.4 `worker-report-contract-spec.md` 必须回答

1. report 的最小 schema 是什么
2. report 缺字段时当前系统如何处理
3. `next_action` 是否仍保留，以及它在什么层生效
4. report 与 stage closure 的关系是什么
5. report 与 ticket state projection 的边界是什么

## 7. 当前代码与这套 spec 的对应关系

### 7.1 `stage-closure-spec`

主要落点：

- [worker_loop.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_loop.go)
- `emptyReportRetryPrompt`
- 当前 stage 完成后的判断逻辑

当前问题：

- 只有“补报一次”这一种纠偏
- 没有正式的 stage closure 结果
- handoff / state compression 还没收口成正式机制

### 7.2 `worker-loop-governance-spec`

主要落点：

- PM dispatch / worker loop 启停
- active worker / active run 约束
- ticket lifecycle 守卫

当前问题：

- loop 级恢复锚点没有正式定义
- 用户澄清 / 接管 / worker 重建后的旧结果污染边界不清晰
- loop 级停止条件、预算和升级条件没有正式 spec

### 7.3 `ticket-state-projection-spec`

主要落点：

- [workflow.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/workflow.go)
- inbox 生成逻辑
- done integration freeze 逻辑

当前问题：

- ticket 主世界投影逻辑散在 reducer 和 side effect 里
- 缺少统一 spec 描述什么才算推进 ticket 世界

### 7.4 `worker-report-contract-spec`

主要落点：

- [report.go](/Users/xiewanpeng/agi/dalek/internal/contracts/report.go)
- [report.go](/Users/xiewanpeng/agi/dalek/internal/services/worker/report.go)
- worker kernel prompt 约定

当前问题：

- 现在 report 既像输入契约，又像高权威写回
- 这两个角色混在一起，导致系统边界不清楚

## 8. 推荐的实施顺序

### 第一步：收缩并替换 `single-step-spec.md`

不要继续扩它。

应该把它收缩成：

- `stage-closure-spec.md`

只保留最小闭合机制，不再写通用 runtime 宇宙。

### 第二步：补 `worker-loop-governance-spec.md`

这是当前讨论里最缺的一层。

如果没有它，系统只会变成：

- 每一步局部更干净
- 但整张票的长期推进仍然可能失控

### 第三步：补 `ticket-state-projection-spec.md`

把当前 PM reducer、inbox、done freeze 这套逻辑从代码散点提升成统一规范。

### 第四步：最后补 `worker-report-contract-spec.md`

把 report 从“代码里散落的高权威信号”收成正式输入契约。

## 9. 最终收敛

当前阶段，dalek 不该做成：

- 通用 agent graph runtime
- 通用 step orchestration engine
- 通用 execution object system

当前阶段，dalek 真正该做的是：

- 保留 `ticket`
- 保留 `worker loop`
- 保留 `raw agent run`
- 在中间补一个最小 `stage closure`
- 在 loop 层补一个最小 `governance`
- 在 ticket 层补一个明确的 `state projection`

一句话说：

**我们不是在构建一套通用 raw run 标准化层，而是在给当前 dalek 的 ticket 执行链补齐最小、必要、可实现的控制与收口 spec。**
