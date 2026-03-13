# Single Step Spec

> 本文定义 dalek 中 `single step` 的正式语义。
>
> 本文只回答一件事：
>
> **在 `worker loop` 内，什么才算一次最小、标准、可提交、可恢复的推进。**

## 1. 目的

`single step` 是 `worker loop` 的最小标准推进单位。

它存在的原因是：

- `ticket` 是顶层任务包，通常大于单次 agent run 的能力边界
- `raw agent run` 是底层一次 bounded tool loop，通常小于一张复杂 ticket 的完整推进需要
- 因此，系统必须在两者之间定义一个标准执行单元，使 ticket 可以被分步推进

本文不定义：

- PM 如何拆解 ticket
- 语义正确性如何验收
- 具体工具或 provider 的实现细节

本文只定义：

- `single step` 的边界
- `single step` 的最小对象
- `single step` 的生命周期
- `single step` 的不变量
- `single step` 的闭合条件

## 2. 上下文与前提

本文建立在两份前提文档之上：

- [system-foundation-premises.md](/Users/xiewanpeng/agi/dalek/docs/architecture/system-foundation-premises.md)
- [worker-loop-foundation.md](/Users/xiewanpeng/agi/dalek/docs/architecture/worker-loop-foundation.md)

本文默认以下前提为真：

1. `raw agent run` 是高能力但非确定性的语义执行器。
2. `raw agent run` 有 context 和单次运行边界，不能被假定完整吞下复杂 ticket。
3. `raw agent run` 可以吃外部反馈并修正后续执行。
4. `worker loop` 围绕同一张 ticket 持续推进多个单步。
5. 只有被系统承认的推进，才能改变 ticket 的 accepted progress。

## 3. 术语

### 3.1 `ticket`

顶层治理任务包。

它承载：

- 目标
- 约束
- 已接受推进
- 过程历史
- 验收边界
- 当前治理状态

### 3.2 `ticket execution`

一张 ticket 被持续推进的完整执行过程。

它由多个 `single step` 组成，并由 `worker loop` 驱动。

### 3.3 `raw agent run`

底层一次 bounded tool loop。

它会：

- 读取上下文
- 调工具
- 产生轨迹与副作用
- 产出候选控制输出
- 在反馈下继续修正

### 3.4 `single step`

`worker loop` 围绕 ticket 当前 frontier 发起的一次标准化推进。

它不是裸 `raw agent run`，而是：

- 一次有界输入快照
- 一个或多个 `raw agent run`
- 一条控制反馈闭环
- 一次最终 `step settlement`

### 3.5 `accepted progress`

ticket 当前被系统承认、可供后继继续消费的推进状态。

它只能由 `step settlement` 写回。

## 4. 单步最小对象

一个 `single step` 至少包含 5 个对象。

### 4.1 `step claim`

标识“当前是谁、围绕哪张 ticket、在哪个推进上下文里工作”。

最小字段：

- `ticket_id`
- `worker_id`
- `execution_id`
- `step_seq`
- `generation`
- `owner_lease`
- `opened_at`

要求：

- 同一时刻，对同一 ticket，只能有一个有效 `step claim`
- 迟到 claim、重复 claim、过期 claim 不能覆盖当前有效 claim

### 4.2 `step input package`

单步输入快照。

最小字段：

- `accepted_state_ref`
- `frontier`
- `constraints`
- `handoff`
- `budget`
- `relevant_artifact_refs`

要求：

- 单步只能基于显式输入快照推进
- 不允许把“模型隐性记忆”当成权威前态

### 4.3 `candidate report`

`raw agent run` 产出的候选控制输出。

它至少应表达：

- `next_action`
- `summary`
- `needs_user`
- `blockers`
- `head_sha`
- `dirty`

要求：

- `candidate report` 不是天然权威结果
- 它只能作为待校验输入进入控制面

### 4.4 `control violation`

控制契约违规对象。

它描述的不是任务语义错误，而是控制层违规。

最小类型集：

- `missing_report`
- `invalid_report`
- `illegal_transition`
- `stale_generation`
- `ownership_mismatch`
- `budget_exhausted`

要求：

- 发现 violation 时，系统不能只拒绝
- violation 必须回灌给当前 step 内的 `raw agent run`
- 让其继续修正、重报或显式失败

### 4.5 `step settlement`

单步结束时的确定性安放对象。

最小字段：

- `ticket_id`
- `execution_id`
- `step_seq`
- `decision`
- `accepted_progress_patch`
- `compressed_handoff`
- `artifact_refs`
- `reported_at`
- `settled_at`

其中 `decision` 最少应允许：

- `continue`
- `wait_user`
- `done`
- `failed`
- `interrupted`
- `escalated`

要求：

- 只有 `step settlement` 才能改变 ticket 的 accepted progress
- 没有 settlement，只有 artifact，不算可靠推进

## 5. 生命周期

一个 `single step` 必须经过以下生命周期。

### 5.1 opened

`worker loop` 选择当前 frontier，创建 `step claim`，生成 `step input package`。

### 5.2 executing

一个或多个 `raw agent run` 在当前 step 内执行。

它们可以：

- 产生轨迹
- 产生副作用
- 产出候选 report
- 在控制反馈下继续修正

### 5.3 validating

系统对 `candidate report` 做控制契约校验。

可能结果：

- 通过
- 产生 `control violation`

### 5.4 correcting

如果存在 violation，则回灌给当前 step 内的 `raw agent run`。

该阶段可重复发生，直到：

- 通过校验
- 明确失败
- 达到预算上限
- 被中断

### 5.5 settling

系统生成 `step settlement`，写回 accepted progress，并生成后继可消费的压缩状态。

### 5.6 closed

单步关闭，控制权回到 `worker loop`。

## 6. 不变量

`single step` 必须满足以下不变量。

### 6.1 写回不变量

只有 `step settlement` 可以推进 ticket accepted progress。

以下内容单独存在时都不构成 accepted progress：

- worktree diff
- 测试输出
- stream log
- candidate report
- 临时总结

### 6.2 边界不变量

一个 step 必须有明确开始和结束边界。

它不能是：

- 无限会话
- 无预算循环
- 无输入快照的继续聊天

### 6.3 反馈不变量

控制违规必须回灌给 step 内的语义执行器。

系统不能只做：

- 观测
- 拒绝
- 记录

否则它只是 validator，不是控制闭环。

### 6.4 压缩不变量

一个 step 结束时，必须留下压缩后的后继状态包。

后继 step 不应依赖：

- 完整原始轨迹重放
- worker 内隐残留记忆
- PM 人工翻日志猜测

### 6.5 幂等不变量

迟到 report、重复 report、过期 report 不能破坏当前 accepted progress。

### 6.6 中断不变量

step 必须可中断、可恢复、可让渡控制权。

## 7. 闭合条件

一个 `single step` 只有在以下条件之一成立时才可闭合：

1. 产生合法 `step settlement`
2. 达到预算上限并生成 `failed` / `escalated` settlement
3. 被中断并生成 `interrupted` settlement

以下情况不算闭合：

- agent 进程退出
- 产生了代码 diff
- 打印了总结
- 有一个候选 report

这些都只是执行现象，不是闭合。

## 8. 与 `worker loop` 的关系

`worker loop` 不直接管理裸 `raw agent run`。

它管理的是：

- 当前 ticket 的 `ticket execution`
- 其中一个个 `single step`

`worker loop` 至少负责：

- 选择下一步 frontier
- 启动 step
- 消费 settlement
- 决定继续、暂停、阻塞、终止、升级人工

## 9. 非目标

本文明确不负责以下事情：

### 9.1 语义正确性

某次 step 形成 accepted progress，不代表业务上一定正确。

语义正确性仍可能在：

- 测试
- 验收
- 人工审核
- 线上运行反馈

中被否定。

此时系统的动作不是“否认上一步曾经发生”，而是：

- 把新的外部事实纳入 ticket 世界
- 继续发起后继 step 或后继 ticket 推进修复

### 9.2 PM 拆解策略

本文不定义 PM 如何拆 ticket，也不定义 phase 策略。

本文只定义：

- 一旦 `worker loop` 要推进某个 frontier
- 这一步必须长成什么样

## 10. 实现含义

如果要把现有系统收敛到本 spec，最低限度要做到：

1. `worker loop` 从“直接驱动 stage/raw run”升级为“驱动 single step”
2. 控制层从“读 next_action 决策”升级为“candidate report -> control violation -> step settlement”
3. accepted progress 的权威写回面从“report 存在即视作推进”升级为“只有 settlement 才推进”
4. 每一步都必须留下压缩 handoff，而不是只留下日志和 worktree 残留
