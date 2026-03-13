# Worker Loop Governance Spec

> 本文定义 `worker loop` 这一层的最小治理机制。
>
> 它回答的问题是：
>
> **即使每一轮 stage 都能闭合，整张 ticket 的持续推进如何保持可控、可恢复、不失真。**

## 1. 目的

`stage closure` 只能解决局部问题：

- 一轮 stage 怎么收口
- 当前 report 不合法时怎么纠偏

但它解决不了 loop 级问题，例如：

- 同一 ticket 是否同时有多个有效 loop
- 用户澄清后旧结果是否还能生效
- worker 重建后从哪里继续
- loop 何时该停、该升级、该交回 PM

因此，`worker loop` 这一层必须有自己的治理规则。

## 2. 范围

本文只定义：

- 哪个 loop 当前有效
- loop 如何恢复、让渡、失效
- loop 级停止、升级、中断规则
- late result / stale result 如何处理

本文不定义：

- 单个 stage 如何闭合
- report schema
- ticket workflow 具体投影

## 3. 必须能回答的 loop 级事实

系统至少必须在任意时刻能回答：

1. 当前这张 ticket 是否有一个有效 loop
2. 当前有效 loop 绑定哪个 worker
3. 当前 loop 最近一个已闭合的 `stage_seq` 是多少
4. 下一轮继续时，应该从哪一个闭合点恢复
5. 最近一次 loop 级治理事件是什么

如果系统答不出来这些问题，loop level 仍然不可靠。

## 4. 核心规则

### 4.1 单 ticket 单有效 loop

同一张 ticket 在任意时刻只能承认一个有效 loop。

这里的“有效”不是指：

- 进程还活着

而是指：

- 它仍然被允许继续推进 ticket 主世界

### 4.2 恢复只基于最后一个已闭合 stage

loop 恢复时，唯一可靠的继续点是：

- 当前 ticket accepted state
- 最后一个已闭合 stage 的 handoff

不允许把以下内容直接当作恢复锚点：

- 半截 report
- 裸日志
- 未闭合 raw run 的副作用
- agent“应该还记得”的上下文

### 4.3 loop 级治理事件必须切断旧权威

当发生下列事件时，系统必须允许旧 loop 结果失效：

- 用户关键澄清
- PM 明确 replan
- worker 重建/换绑
- 人工接管

这里不要求先实现一套复杂 generation/lease 系统，但必须有最小机制保证：

**旧 loop 的迟到结果不能继续污染新世界。**

### 4.4 loop 必须有停机和升级规则

loop 不是无限 `continue`。

至少应存在下面几类停机条件：

- 连续多轮没有实质推进
- 连续多轮只能纠偏但无法闭合
- 连续多轮都在请求同类人工输入
- 达到时间/轮次预算
- PM 或系统明确中断

达到条件后，loop 必须：

- 升级 PM
- 或进入 blocked
- 或进入失败/中断态

不能无限自旋。

## 5. loop 生命周期语义

当前阶段不引入复杂 FSM，只定义 4 种实际语义：

### 5.1 active

当前 loop 被承认拥有推进权，允许继续发起下一轮 stage。

### 5.2 paused

当前 loop 暂停推进，但其控制权没有彻底废弃。

典型原因：

- 等用户输入
- 等 PM 决策
- 短期暂停

### 5.3 superseded

当前 loop 已被新治理前提替代。

典型原因：

- 用户关键澄清
- worker 重建
- PM 重规划

此时旧 loop 的迟到结果不得继续推进 ticket 主状态。

### 5.4 closed

当前 loop 不再继续推进当前 ticket。

典型原因：

- ticket 已 done
- ticket 已 blocked 并交回 PM
- ticket 已失败
- ticket 被人工终止

## 6. late result / stale result 规则

### 6.1 stale result 的定义

满足以下任一条件的结果，应视为 stale：

- 来自已失效 worker
- 来自已被 superseded 的 loop
- 来自早于当前恢复锚点的旧 stage

### 6.2 stale result 的处理

stale result 可以：

- 被记录
- 被审计
- 被关联到历史 run

但不能：

- 推进当前 ticket accepted state
- 覆盖当前 loop 的最新闭合点
- 回滚 ticket 到旧理解

## 7. 中断、接管、恢复

### 7.1 中断

中断是合法治理动作，不是事故。

中断之后，系统至少要保存：

- 中断时的有效 loop 身份
- 最后一个已闭合 stage
- 中断原因

### 7.2 接管

人工接管或 PM 接管后：

- 旧 loop 不再拥有推进权
- 后续推进必须在新的治理前提下进行

### 7.3 恢复

恢复时，系统只能从：

- 最后一个已闭合 stage
- 当前 ticket accepted state

重新组织下一轮推进。

恢复不是“把上次 tmux 画面接着跑”，而是“从最后可靠控制点继续”。

## 8. 与 stage closure 的边界

两者边界必须分清：

- `stage closure`
  解决局部一轮如何收口

- `worker loop governance`
  解决多轮推进如何不失控

也就是说：

- 一轮 stage 闭合成功，不代表整个 loop 仍然有效
- loop 被 superseded 后，即使旧 stage 迟到闭合，也不能自动推进当前 ticket

## 9. 非目标

本文不做这些事：

- 不实现完整分布式 lease 协议
- 不把 loop 治理做成通用工作流调度框架
- 不定义 PM 如何理解业务语义
- 不要求系统自动从任意历史脏状态推断出唯一正确世界

## 10. 当前代码的直接改造点

本 spec 主要落在：

- `worker loop` 的启动与恢复入口
- active worker / active run 的有效性判断
- 用户澄清 / PM 接管 / worker 重建后的旧结果处理
- loop 级停止与升级策略

当前代码已有基础：

- ticket/worker 单活约束
- active run 绑定约束
- ticket workflow guard

当前代码仍缺：

- 最后一个已闭合 stage 作为恢复锚点
- 用户澄清导致旧 loop 失效的明确机制
- loop 级预算 / 升级 / 停机规则
- stale result 的统一治理规则
