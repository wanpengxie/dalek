# Ticket State Projection Spec

> 本文定义闭合后的 stage 结果如何进入 ticket 主世界状态。
>
> 它回答的问题是：
>
> **什么结果只是运行时现象，什么结果会真正改变 ticket 世界。**

## 1. 目的

当前代码里，ticket 主世界投影逻辑散落在：

- `worker report` 摄入
- PM workflow reducer
- inbox side effects
- done integration freeze

这导致边界容易混：

- report 像输入契约，也像主世界写回
- task runtime 观测和 ticket workflow 推进耦在一起
- integration/inbox side effects 也夹在 reducer 里

因此，需要一份单独 spec 说明：

**闭合结果怎样推进 ticket 主状态。**

## 2. 范围

本文只定义：

- 哪些 stage 闭合结果可以推进 ticket workflow
- 哪些 side effect 属于 ticket 世界
- 哪些结果只属于 runtime 观测

本文不定义：

- stage 如何闭合
- loop 如何治理
- 语义正确性如何验收

## 3. 基本原则

### 3.1 只有闭合结果可以投影

能够进入 ticket 主世界的输入，不是：

- raw run 退出
- worktree diff
- 单次命令输出
- 未闭合 report

而是：

- 已经形成的 `stage closure`

### 3.2 ticket 世界只接受少量稳定语义

当前阶段，ticket 主世界只应接收少量稳定控制语义：

- `continue`
- `wait_user`
- `done`
- `failed`
- `escalated`
- `interrupted`

不是每个 runtime 细节都要进入 ticket 世界。

### 3.3 主世界投影必须是单写者

ticket workflow、inbox、integration freeze 等主世界写回，必须由 PM/control plane 的统一投影逻辑处理。

worker report、task runtime、raw run 自身都不能直接改 ticket 世界。

## 4. 投影输入

每次投影至少应读取以下输入：

- `ticket_id`
- 当前 ticket workflow 状态
- 本轮已闭合 stage 的 `decision`
- 本轮闭合 `summary`
- 本轮闭合 `handoff`
- 相关 `artifact refs`
- worker / run 关联信息

## 5. decision 到 ticket workflow 的映射

### 5.1 `continue`

含义：

- 当前 stage 已闭合
- 但 ticket 还需要继续推进

投影规则：

- ticket 应处于或保持 `active`
- 不创建 needs_user inbox
- 不进入 done

### 5.2 `wait_user`

含义：

- 当前 stage 已闭合
- ticket 进入等待人工输入/决策状态

投影规则：

- ticket 进入 `blocked`
- 创建或更新对应的 needs_user inbox
- loop 暂停，控制权交回 PM/用户

### 5.3 `done`

含义：

- 当前 stage 已闭合
- 当前 loop 认为该 ticket 已完成到可交付边界

投影规则：

- ticket 进入 `done`
- 冻结当前 integration 锚点
- integration 状态进入 `needs_merge`
- 后续 `continue` 不能自动把 done 回滚

### 5.4 `failed`

含义：

- 当前 stage 已闭合
- 当前 loop 认为无法继续推进，且不是简单等待用户输入

投影规则：

- 当前阶段建议投影到 `blocked`
- 创建 incident / escalation 类 inbox 或等价待办
- 不自动把 ticket 置为终态 `done`

原因：

- ticket 失败并不等于问题从世界里消失
- 对 PM 来说，它通常意味着“需要处理”，不是“任务完成”

### 5.5 `escalated`

含义：

- 当前 stage 已闭合
- 需要更高层治理决策

投影规则：

- ticket 进入或保持 `blocked`
- 创建 blocker 级待办
- 明确这不是普通 `wait_user`，而是治理升级

### 5.6 `interrupted`

含义：

- 当前 stage 被治理动作打断

投影规则：

- 默认不直接改 ticket workflow
- 由 loop governance 或上层治理动作决定后续状态

## 6. ticket 世界中的 side effects

当前阶段，以下 side effects 属于 ticket 世界的一部分：

### 6.1 inbox side effects

当 decision 需要人工介入时：

- 创建或更新 inbox
- 保证 PM 能消费到这个事实

### 6.2 integration side effects

当 decision=`done` 时：

- 冻结 integration anchor
- 设置 merge/integration 后续所需的稳定引用

### 6.3 lifecycle event side effects

每次有效投影都应留下：

- ticket lifecycle event
- workflow change event

以保证主世界因果链可追踪。

## 7. 不属于 ticket 世界的内容

以下内容默认只属于 runtime 观测，不应直接写入 ticket 主状态：

- raw run 的 stdout/stderr
- 中间测试输出
- stream log
- 临时文件
- 未被接纳的 report 尝试
- 未闭合 stage 的 artifact

这些内容可以：

- 进入 task runtime
- 进入审计链
- 进入 artifact refs

但不能自动进入 ticket 世界。

## 8. 重复与陈旧投影

### 8.1 重复投影

如果同一个已闭合 stage 的结果被重复提交：

- 可以记录
- 但不应重复推进 ticket 主状态

### 8.2 陈旧投影

如果投影输入来自：

- 旧 loop
- 旧 worker
- 已被 superseded 的治理前提

则该结果不能推进当前 ticket 世界。

## 9. 与现有 workflow reducer 的关系

当前实现里：

- `continue -> active`
- `wait_user -> blocked`
- `done -> done`

这条主映射可以保留。

本 spec 的新增价值在于把下面这些边界写硬：

- 只有闭合结果才能参与映射
- `failed / escalated / interrupted` 也有明确投影语义
- inbox / integration freeze 是主世界 side effect，不是偶发实现细节

## 10. 非目标

本文不做这些事：

- 不验证代码语义是否真的正确
- 不决定是否 merge
- 不处理多 worker 并发编辑同一 ticket
- 不构建通用业务流程图状态机

## 11. 当前代码的直接改造点

本 spec 主要落在：

- [workflow.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/workflow.go)
- inbox side effect 逻辑
- done integration freeze 逻辑

当前代码已有基础：

- `continue / wait_user / done` 映射已经存在
- inbox 和 done integration freeze 已有实际链路

当前代码仍缺：

- “只有闭合结果才能投影”的明确边界
- `failed / escalated / interrupted` 的正式语义
- runtime 观测与 ticket 世界写回的硬分层
