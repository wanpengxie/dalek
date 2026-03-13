# Stage Closure Spec

> 本文定义当前 `worker loop` 中一轮 `stage` 如何闭合。
>
> 它只回答一个问题：
>
> **什么情况下，这一轮执行可以被系统承认为一次有效推进。**

## 1. 目的

当前代码中，一轮 `stage` 基本等于：

- 启动一次 `raw agent run`
- 等待退出
- 读取一次 `worker report.next_action`
- 决定继续还是停止

这个模型太薄。

它的问题不是没有 loop，而是：

- `report` 缺失或非法时，当前 stage 缺少统一纠偏机制
- `raw run` 产生了很多有效工作，但第一次没有收口成功时，系统容易直接溢出到 loop 层
- artifact、report、ticket 推进之间的边界不够硬

因此，本 spec 的目标是：

**在不引入通用 step runtime 的前提下，给当前 stage 增加一个最小但必要的闭合机制。**

## 2. 范围

本文只定义：

- stage 的开始与结束边界
- stage 内最小纠偏闭环
- 什么结果可以算 stage 闭合
- 闭合后最少要留下哪些事实

本文不定义：

- loop 级治理
- ticket workflow 如何推进
- 语义正确性如何验证
- PM 如何拆解 ticket

## 3. 术语

### 3.1 `stage`

`worker loop` 围绕当前 ticket frontier 发起的一轮标准推进。

它是当前系统里最小的执行控制单元，但不是独立运行时子系统。

### 3.2 `raw agent run`

stage 内部的一次底层 agent 执行尝试。

一次 stage 可以包含：

- 1 次 raw run
- 或少量多次 raw run，用于当前 stage 内纠偏

### 3.3 `candidate report`

worker 通过 `dalek worker report` 提交的候选控制输出。

它是 stage 闭合的输入，不是 ticket 主世界的权威写回。

### 3.4 `stage closure`

当前 stage 的最终闭合结果。

只有形成 `stage closure`，这轮 stage 才能被系统承认为一次有效推进。

## 4. 每个 stage 必须保存的最小事实

当前阶段不要求引入大量新实体，但系统至少必须能保存以下事实：

### 4.1 stage 身份事实

- `ticket_id`
- `worker_id`
- `stage_seq`
- 本 stage 关联的一个或多个 `task_run_id`
- `opened_at`

### 4.2 stage 输入事实

- 启动本 stage 时使用的 ticket/worker 上下文快照
- 上一轮已闭合 stage 留下的 handoff 或状态摘要
- 当前 stage 的 entry prompt / closure prompt

### 4.3 stage 闭合事实

- `decision`
- `summary`
- `handoff`
- `artifact_refs`
- `closed_at`
- 哪一个 report 被接受为本轮闭合输入

这里的 `decision` 当前最少支持：

- `continue`
- `wait_user`
- `done`
- `failed`
- `escalated`
- `interrupted`

## 5. stage 生命周期

当前阶段不引入细粒度 FSM，只要求 stage 具备下面 4 个实际阶段：

### 5.1 打开

`worker loop` 选择当前 frontier，启动一轮新的 stage。

### 5.2 执行

stage 内至少执行一次 `raw agent run`。

它会产生：

- trajectory
- effects
- candidate report

### 5.3 纠偏

如果这次 raw run 结束后，report 还不足以闭合 stage，则系统必须在当前 stage 内进行纠偏。

允许的纠偏原因至少包括：

- 缺 report
- report 非法
- report 信息不足，无法支撑闭合

纠偏动作至少包括：

- 生成明确的 closure feedback
- 将其反馈给当前 worker
- 允许其在当前 stage 内补报、重报或显式失败

### 5.4 闭合

只有在满足闭合条件后，系统才为当前 stage 生成正式的闭合结果。

## 6. stage 闭合条件

一个 stage 只有在满足以下条件之一时，才算闭合：

1. 接收到了足够的 report，形成合法的 `stage closure`
2. 明确失败，并形成 `failed` 或 `escalated` 的闭合结果
3. 被中断，并形成 `interrupted` 的闭合结果

以下情况都**不算**闭合：

- raw run 进程退出
- worktree 有 diff
- 跑出了测试结果
- agent 打印了总结
- 有一个未通过校验的 report

## 7. report 纠偏规则

### 7.1 纠偏不是 ticket 级失败

如果当前 stage 中：

- 没有 report
- report 非法
- report 还不足以收口

系统不能直接把这轮 ticket 推入最终失败路径。

首先应该做的是：

- 在当前 stage 内反馈问题
- 让 worker 继续补充或修正

### 7.2 纠偏应当有边界

stage 内纠偏不能无限循环。

系统至少要有以下边界之一：

- 最多纠偏次数
- stage 级时间预算
- PM/loop 显式中断

超过边界后，当前 stage 必须以：

- `failed`
- `escalated`
- 或 `interrupted`

之一闭合。

### 7.3 纠偏反馈必须具体

反馈给 worker 的 closure feedback 至少要说明：

- 缺了什么
- 哪个字段非法
- 当前还不能闭合的原因
- 需要它补什么，而不是重新做整张票

## 8. artifact 与闭合结果的边界

stage 内允许产生大量 artifact：

- 代码 diff
- 命令输出
- 测试结果
- 临时文件
- 日志

但这些 artifact 只说明：

**当前 stage 发生过执行。**

它们不自动说明：

**当前 stage 已经形成可承认推进。**

只有 `stage closure` 才能把：

- summary
- handoff
- 可用 artifact 引用
- 当前 decision

带入后继控制面。

## 9. stage 闭合后的最小输出

每个闭合 stage，至少要留下：

1. `decision`
2. 一段可读 `summary`
3. 一段压缩后的 `handoff`
4. 本轮可保留的 `artifact refs`
5. `closed_at`

这里最重要的是 `handoff`。

后续 stage 的恢复和继续，原则上应该依赖：

- 上一个已闭合 stage 的 handoff
- 当前 ticket accepted state

而不是依赖：

- 完整原始日志
- worker 的隐性记忆
- PM 人工翻历史猜状态

## 10. 恢复语义

如果系统在 stage 闭合前崩溃：

- 本轮 raw run 产生的 artifact 可以保留
- 但它们不能自动进入 accepted progress
- 恢复后只能从“最后一个已闭合 stage”继续

如果某个迟到 report 在 stage 已被替换或终止后才到达：

- 它不能自动改写当前 stage 结果
- 是否接纳，由 loop governance 决定

## 11. 非目标

本文明确不做这些事：

- 不把 stage 变成独立执行引擎
- 不引入一整套通用状态机语言
- 不保证这轮 stage 的语义结果一定正确
- 不要求从任意脏 artifact 自动完美恢复语义

## 12. 当前代码的直接改造点

本 spec 主要落在：

- [worker_loop.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_loop.go)
- `emptyReportRetryPrompt`
- `worker report` 的消费与 stage 完成判定

当前代码里已经有的基础：

- stage 概念已经存在
- 空 report 补报一次的最弱闭环已经存在
- `next_action` 已驱动 loop 行为

当前代码里缺的能力：

- 非空但非法 report 的统一纠偏
- stage 闭合结果的明确事实面
- 压缩 handoff 的正式输出
- “artifact != 已闭合推进”的硬边界
