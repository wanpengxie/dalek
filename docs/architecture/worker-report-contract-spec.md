# Worker Report Contract Spec

> 本文定义 `dalek worker report` 的输入契约。
>
> 它回答的问题是：
>
> **worker 到 control plane 的输入面到底是什么，以及它不是什么。**

## 1. 目的

当前系统里，`worker report` 同时承担了两种角色：

- worker 到 control plane 的输入契约
- 高权威 ticket 推进信号

这两个角色混在一起，会导致边界不清楚。

因此，本 spec 的目标是明确：

- `worker report` 首先是什么输入
- 它什么时候算合法
- 它什么时候足以参与 stage 闭合
- 它什么时候才能进一步参与 ticket 投影

## 2. 范围

本文只定义：

- report 的最小 schema
- report 的合法性约束
- report 在控制链中的位置

本文不定义：

- stage 如何闭合
- loop 如何治理
- ticket workflow 如何推进

## 3. 核心原则

### 3.1 report 是候选控制输入

`worker report` 的本质是：

**worker 发给 control plane 的候选控制输出。**

它不是：

- ticket 主状态本身
- 最终 accepted progress
- 语义真理证明

### 3.2 report 必须绑定当前 run

每个 report 必须能绑定到当前具体执行：

- `worker_id`
- `ticket_id`
- `task_run_id`

如果做不到这件事，系统就无法判断：

- 它属于谁
- 它是不是迟到结果
- 它是不是错误 worker 发来的

### 3.3 report 合法不等于 ticket 已推进

一个 report 就算 schema 合法，也只是：

- 可以进入 stage closure 判断

不是：

- 自动推进 ticket

## 4. 最小 schema

当前阶段，最小 report schema 仍以现有字段集为基础：

- `schema`
- `reported_at`
- `project_key`
- `worker_id`
- `ticket_id`
- `task_run_id`
- `head_sha`
- `dirty`
- `summary`
- `needs_user`
- `blockers`
- `next_action`

## 5. 字段要求

### 5.1 必填字段

以下字段必须存在，才能算基本合法 report：

- `schema`
- `worker_id`
- `task_run_id`

如果当前链路能稳定拿到 `ticket_id`，则也应要求存在。

### 5.2 `schema`

当前阶段只接受已知 schema 版本。

未知 schema 不能被静默接纳。

### 5.3 `next_action`

当前阶段允许的最小值集合仍为：

- `continue`
- `wait_user`
- `done`

扩展值：

- `failed`
- `escalated`

可以在后续版本中加入，但必须先在投影和治理语义上有定义。

空 `next_action` 的含义不是“合法完成”，而是：

- 本次 report 不足以直接收口
- 当前 stage 需要补报或纠偏

### 5.4 `summary`

`summary` 不一定决定控制流，但它必须足够让：

- PM
- 后继 stage
- 恢复逻辑

理解这轮 worker 认为自己做了什么。

对于能进入闭合的 report，`summary` 不应为空白占位。

### 5.5 `blockers`

当：

- `next_action=wait_user`
- 或 `needs_user=true`

时，`blockers` 应提供最小可操作信息。
当前契约定义为 JSON 字符串数组，例如 `["等待评审","请提供 staging 凭证"]`；不接受对象数组。

否则系统虽然能知道“被卡住了”，却不知道“为什么被卡住”。

## 6. report 的三个层次

为了避免继续混层，当前建议把 report 分成三层理解：

### 6.1 report attempt

worker 发起了一次 `dalek worker report`。

这只能说明：

- 有一次上报尝试

### 6.2 valid report

这次 report 通过了最小 schema 和绑定校验。

这说明：

- 它可以进入 control plane

### 6.3 closure-sufficient report

这次 report 不仅合法，而且足以支撑当前 stage 闭合。

这时它才能参与：

- stage closure
- 后续 ticket state projection

## 7. 非法 report 的处理

### 7.1 非法 report 不应直接推进 ticket

以下情况都属于非法或不足 report：

- schema 不合法
- task_run_id 不合法
- worker/ticket 绑定不匹配
- next_action 非法
- next_action 为空，且当前场景要求收口

这些 report 不能直接推进 ticket。

### 7.2 非法 report 不一定等于当前 ticket 失败

非法 report 首先应交给 `stage closure` 机制处理：

- 记录
- 反馈
- 允许补报或重报

而不是直接把 ticket 主世界打成失败。

## 8. report 与当前代码的边界修正

当前实现中：

- [report.go](/Users/xiewanpeng/agi/dalek/internal/services/worker/report.go) 把 report 作为高权威信号写回
- [workflow.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/workflow.go) 直接按 `next_action` 推 workflow

本 spec 的修正是：

- report 仍然是高价值信号
- 但它首先是输入契约
- 是否足以推进 ticket，要看：
  - 它是否通过合法性校验
  - 它是否足以支撑 stage 闭合
  - 它是否来自当前有效 loop

## 9. 非目标

本文不做这些事：

- 不保证 report 所表达的业务语义一定正确
- 不把 report 扩成通用消息协议
- 不处理 ticket 世界的全部 side effects

## 10. 当前代码的直接改造点

本 spec 主要落在：

- [report.go](/Users/xiewanpeng/agi/dalek/internal/contracts/report.go)
- [report.go](/Users/xiewanpeng/agi/dalek/internal/services/worker/report.go)
- worker kernel prompt 中对 `dalek worker report` 的约束

当前代码已有基础：

- schema 版本
- `worker_id / ticket_id / task_run_id` 绑定
- `next_action` 最小枚举

当前代码仍缺：

- report 作为“候选输入”而非“天然权威写回”的清晰分层
- `valid report` 与 `closure-sufficient report` 的区别
- 非法 report 进入 stage 内纠偏而不是直接溢出到 ticket 世界
