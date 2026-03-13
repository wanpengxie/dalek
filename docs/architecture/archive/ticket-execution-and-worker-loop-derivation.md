# Ticket Execution And Worker Loop Derivation

> 本文基于 [system-foundation-premises.md](/Users/xiewanpeng/agi/dalek/docs/architecture/system-foundation-premises.md) 与 [worker-loop-foundation.md](/Users/xiewanpeng/agi/dalek/docs/architecture/worker-loop-foundation.md) 做自上而下推导。
>
> 目标不是补实现细节，而是回答：
>
> - dalek 在顶层目标与底层 agent 原理之间，缺失的中间层抽象是什么
> - `ticket`、`raw agent run`、`single step`、`worker loop` 应该怎样重新落位
> - 整个系统的基本运行机制应该如何理解

## 1. 推导起点

### 1.1 顶层目标

dalek 的目标不是做一个能跑通一次任务的 agent demo，而是：

- 让 `ticket` 成为 repo 演化的系统原语
- 让 PM agent 能持续、稳定、可治理地推进 ticket
- 让多次执行可以被接续、被恢复、被审计、被接管

也就是说，dalek 关心的不是单次 run 有没有“看起来成功”，而是：

**ticket 是否被系统性推进。**

### 1.2 底层原理

dalek 依赖的底层执行体是 `raw agent run`。

`raw agent run` 的基本性质是：

- 它是高能力但非确定性的语义执行器
- 它在单次运行内可以真实地读代码、调工具、改文件、观察结果
- 它天然受 context 限制，无法被假定能完整吞下一个复杂 ticket
- 它的自然产物是 `trajectory + effects`
- 它可以高概率遵守约定，但不能被假定为必然遵守
- 它可以吃外部反馈，并基于反馈修正后续推理和执行

因此：

- `raw agent run` 可以承担单次局部执行
- 但不能直接承担 dalek 的持续控制真相
- 也不能被误当成 ticket 的完整执行单元

### 1.3 直接矛盾

到这里会出现一个直接矛盾：

- 顶层要求 ticket 推进必须稳定、可治理、可恢复
- 底层执行体却是概率性的、context 有限的，且可能漏报、错报、迟到报

所以系统中间一定要有一层，把：

- `raw agent run` 的概率性执行
- dalek 自己的控制契约
- ticket 的稳定推进

接起来。

如果没有这层，中间会退化成：

- `raw agent run` 最好能报对
- `worker loop` 最好能兜住
- PM 最后自己补猜测逻辑

这正是应该避免的状态。

## 2. 推导出的中间层对象

### 2.1 `ticket`

`ticket` 是持久治理原语。

它至少承载：

- 目标
- 约束
- 已接受推进
- 过程历史
- 验收边界
- 当前治理状态

`ticket` 不是执行器，而是整个系统持续真相的外部锚点。

### 2.2 `worker`

`worker` 是执行载体。

它提供：

- worktree
- session
- 环境
- 工具宿主
- 局部运行连续性

但 `worker` 不是权威真相载体。
它保存局部上下文，不直接定义 ticket 的 accepted progress。

### 2.3 `raw agent run`

`raw agent run` 是一次底层 agent 语义执行突发。

它会：

- 读取上下文
- 调工具
- 产生修改
- 观察反馈
- 继续修正

它天然产出：

- `trajectory`
- `effects`
- 一个或多个候选控制输出

它是单次 bounded tool loop 的实现材料，不是 ticket 的直接推进砖块。

### 2.4 `candidate report`

`candidate report` 是 `raw agent run` 试图对 dalek 提交的候选控制输出。

它不是天然权威结果，而是：

- 待校验
- 可被拒绝
- 可被纠正
- 可被重报

没有这一层，就会把 `raw agent run` 的一次自然输出误当成系统真相。

### 2.5 `control violation`

`control violation` 是 dalek 自己的契约反馈对象。

它描述的不是任务语义错误，而是控制契约错误，例如：

- 缺失 report
- report 非法
- 状态转移不合法
- generation 过期
- ownership 不匹配

关键点在于：

**控制器不应该只拒绝 violation，而应该把 violation 回灌给 `raw agent run`，驱动其自我纠正。**

### 2.6 `step settlement`

`step settlement` 是单步推进结束时，系统对本次执行结果的确定性安放。

它负责回答：

- 本次单步是否形成了被接受的 ticket 推进
- 如果没有形成 accepted progress，本次结果应被记成什么
- 下一步控制结论是什么

`step settlement` 是 ticket accepted progress 的唯一写回面。

### 2.7 `single step`

这是本次推导里最关键的中间执行对象。

`single step` 是 worker loop 的最小标准推进单位。

它不是裸 `raw agent run`，而是：

- 围绕 ticket 当前 frontier 发起的一次受控推进周期
- 内含一个或多个 `raw agent run` 的语义执行
- 内含 dalek 的控制契约校验与反馈回灌
- 最终以一次 `step settlement` 收束

所以更准确地说：

`single step = raw execution loop + control feedback loop + step settlement`

它的职责不是“执行代码”，而是：

- 接住一次概率性执行
- 校验控制契约
- 把契约违规回灌纠正
- 对 ticket 做一次有边界推进
- 最终把结果安放进 ticket 状态

关键点在于：

- 一个复杂 ticket 往往不可能由一次 `single step` 完成
- 一个 `ticket` 的推进过程，天然会由多个 `single step` 串起来

### 2.8 `ticket execution`

`ticket execution` 是一张 ticket 被持续推进的整个执行过程。

它不是单步，也不是单次 raw run，而是：

- 围绕同一个 ticket
- 由多个 `single step` 组成
- 在 `worker loop` 的控制下持续推进
- 直到进入 done / blocked / escalated / archived 等治理终态

### 2.9 `worker loop`

`worker loop` 不是最底层基石，也不只是简单调度器。

它更准确的位置是：

**ticket 绑定的持续推进内核。**

它工作在一个 `ticket execution` 之上，负责驱动多个 `single step`，并负责：

- ownership
- 连续推进
- 恢复
- 中断
- 重试
- 停机判断
- 把控制权交回 PM 或继续自推进

因此：

- `ticket` 是持久治理原语
- `single step` 是最小标准推进单位
- `ticket execution` 是 ticket 的完整执行过程
- `worker loop` 是持续推进内核

## 3. 系统的三层闭环

按上面的对象分层，dalek 不是一层 loop，而是三层闭环叠在一起。

### 3.1 语义执行闭环

位于 `raw agent run` 内部。

它处理的是：

- 任务理解
- 工具调用
- 结果观察
- 基于外部反馈继续修正

这是 agent 自己最擅长的层。

### 3.2 控制契约闭环

位于 `single step` 内部。

它处理的是：

- report 是否提交
- report 是否合法
- 状态转移是否合法
- ownership / generation 是否匹配

如果这里发生违规，系统不应该只记失败，而应该：

- 生成 `control violation`
- 回灌给 `raw agent run`
- 让语义执行器自行重构、修正、重报

这层闭环产生的是 dalek 自己的控制面能力和契约能力。

### 3.3 持续推进闭环

位于 `worker loop` 层。

它处理的是：

- 当前 ticket 是否继续推进
- 何时再开下一次 `single step`
- 何时等待用户
- 何时阻塞
- 何时恢复
- 何时升级人工
- 何时进入终态

这层闭环保证的是 ticket 的长期连续性，而不是单次执行正确性。

## 4. 基本运行机制

### 4.1 正常推进路径

一个正常的 ticket 推进过程应该这样理解：

1. PM 创建一个有明确目标和约束的 `ticket`。
2. `worker loop` 围绕这张 ticket 启动一个 `ticket execution`。
3. `worker loop` 为当前需要推进的 frontier 选择并启动一次 `single step`。
4. `single step` 以当前 ticket accepted state 作为输入快照。
5. 一个或多个 `raw agent run` 在 worker 上执行，进行自己的语义闭环。
6. `raw agent run` 产出 `candidate report`。
7. dalek 控制器校验这个候选输出。
8. 如果合法，则进入 `step settlement`。
9. `step settlement` 把本次 accepted progress 写回 ticket，并产出下一步控制结论。
10. `worker loop` 读取 settlement，决定继续下一步、暂停、阻塞、终止还是交回 PM。

### 4.2 report 缺失或非法时

这是本次推导最关键的机制之一。

如果某个 `raw agent run`：

- 没有提交 report
- 提交了非法 report
- 提交了过期或不匹配的 report

正确机制不是“直接拒绝然后结束”，而是：

1. 控制器生成明确的 `control violation`
2. violation 回灌给当前 step 内的 `raw agent run`
3. `raw agent run` 读取当前 ticket 状态、已有轨迹和工作区
4. `raw agent run` 自行纠正并重报
5. 控制器再次校验
6. 直到：
   - report 合法并进入 settlement
   - 达到预算上限
   - 触发中断/升级/终止

因此，dalek 的契约能力不是“单次 report 必须正确”，而是：

**系统总能把 report 契约错误转成可纠正的控制反馈。**

### 4.3 crash 或打断时

如果某个 `raw agent run` 中途 crash 或被打断：

- `ticket` 的 accepted progress 不应被污染
- `worker` 上的局部状态可以保留
- 当前 `single step` 可以被记为未完成、被打断、待恢复或待重试
- `worker loop` 决定是否继续当前 step、重开 step，或转入下一个 step

恢复时，系统不应该依赖“上次 agent 心里还记得什么”，而应该依赖：

- ticket accepted state
- 上次 step settlement 结果
- 仍然可用的 worker 局部状态
- 未解决的 control violation

## 5. 为什么 `single step` 是缺失的关键中间层

只看 `worker-loop-foundation.md`，最容易得出：

- `worker loop` 很重要
- `single run` 也很重要

但还少一步：

**单步推进到底应该如何成为 ticket 世界里可接受的一步。**

如果没有 `single step` 这层，中间会出现断裂：

- `raw agent run` 在做语义执行
- 外部控制器在做校验
- `worker loop` 在做持续推进

但三者之间没有一个明确对象把它们收束成一次完整的 ticket 级执行。

这会导致：

- report 缺失时，语义执行与控制结算脱节
- recovery 时，不知道恢复的是裸执行、当前一步，还是整个 ticket execution
- `worker loop` 不得不直接消费过多底层噪音
- PM 最后仍然需要补猜测逻辑

而 `single step` 正好把这个断裂补上。

## 6. 当前系统分层

基于这轮推导，我认为 dalek 的结构应该这样理解：

### 6.1 治理层

- `PM`
- `ticket`

负责：

- 目标
- 约束
- 推进判断
- 中断/升级/接管
- 终态治理

### 6.2 执行控制层

- `worker loop`
- `ticket execution`
- `single step`
- `step settlement`
- `control violation`

负责：

- 持续推进
- 契约校验
- 反馈回灌
- 结果安放
- 恢复与接续

### 6.3 语义执行层

- `worker`
- `raw agent run`

负责：

- 代码和工具操作
- 语义理解
- 反馈吸收
- 候选结果生成

## 7. 阶段性结论

这轮演绎的核心结论是：

1. `worker loop` 不是最底层砖块，它驱动的是一个 `ticket execution`。
2. `ticket execution` 由多个 `single step` 组成，而不是由裸 `raw agent run` 直接拼成。
3. `raw agent run` 是概率性的语义执行体，但它能吃反馈并自纠。
4. dalek 自己必须拥有控制反馈链路，而且这条链路必须回到 `raw agent run`。
5. `single step` 是把随机执行转成稳定 ticket 推进的关键中间层。
6. `step settlement` 是 ticket accepted progress 的唯一写回面。

因此，当前最合理的系统表达不是：

- `ticket -> raw agent run`

而是：

- `ticket -> worker loop -> ticket execution -> single step -> raw agent run`
- `raw agent run -> candidate report -> control validation -> feedback correction -> step settlement`
- `step settlement -> worker loop -> next single step / stop / escalate`

## 8. 待继续明确的问题

这份文档之后，最值得继续明确的是：

1. `single step` 的边界
   何时开始，何时视为结束，何时视为未结算

2. `control violation` 的最小类型集
   哪些可纠正，哪些必须中断，哪些必须升级人工

3. `step settlement` 的最小写回面
   具体写回哪些字段，才能支撑 recovery / handoff / governance

4. `worker loop` 的状态闭合
   哪些状态属于 loop，哪些属于 ticket，哪些属于 ticket execution，哪些属于 single step
