# Ticket Machine Foundation 思考稿（重置版）

> 本文是重置稿。
> 目标不是在既有说法上继续缝补，而是从 `agent 是概率推理机` 这个基本前提出发，重新定义 dalek 的系统原语、基石、执行原语与调度层。
> 本文暂时不讨论具体代码实现，只讨论对象边界与架构结论。

## 1. 为什么要重置

前面的讨论之所以越来越乱，根因不是术语不统一，而是根基前提没有写死：

- `agent` 到底是什么对象
- `raw run` 到底是什么对象
- `worker loop` 到底是不是基石

只要这三个问题没先钉死，后面就会不断把：

- 执行尝试
- 状态提交
- 连续性
- 持久真相
- 调度策略

混成一层。

所以这份文档只做一件事：

**先接受 agent 的基本物理现实，再重新演绎整个系统。**

## 2. Agent 的基本设定

这里不写愿望，只写必须承认的事实。

### 2.1 Agent 是概率推理机

`agent` 不是确定性程序。

给定：

- 上下文
- 工具
- 指令

它不会产生唯一确定的正确轨迹，而是会从一组可能轨迹中采样出一次实际执行。

### 2.2 Agent 是高能力而非弱智

这点必须明确。

`agent` 不是“偶尔猜对几句文本”的弱组件。
模型能力越强、上下文越好、工具可观测性越高，它一次做对任务的概率就越高。

这也是为什么 dalek 这类系统要等到今天才开始真正有机会。

### 2.3 Agent 的错误是非零且相关的

即使成功率很高，也永远不是 `1`。

更重要的是，错误往往不是独立的。

例如：

- 坏上下文会让多轮连续失败
- 错误假设会让后续多步都建立在错前提上
- 忘记 `report` 这类协议错误不会只发生在一种场景

所以不能把 agent 理解成“每次独立抛一次硬币”的组件。

### 2.4 Agent 的原生产物是 trajectory + effects

一次真实 agent run 产出的东西至少包括：

- 文本
- 工具调用
- 工具结果
- 文件改动
- 测试输出
- 中间观察与局部结论

也就是说，它的原生产物不是“一个契约对象”，而是：

`trajectory + effects`

### 2.5 Agent 可以高概率遵守协议，但不能被假定为必然遵守

例如一个 agent 在一次运行结束后，可能有 `99%` 的概率去正确执行 `report`。

这说明：

- `report` 可以是高质量主信号
- 但不能成为系统连续性的唯一承载体

否则长期运行时，成功率会按轮数乘法塌缩。

### 2.6 Agent 能作为主要执行者，但不能作为持续真相的载体

这条是本稿最关键的前提结论：

- agent 负责做事
- agent 不负责成为系统持续真相本身

持续性必须外置。

## 3. 从 Agent 基本设定直接推出的结论

### 3.1 连续 agent loop 不能成为基石

如果一个基石要求：

- 每一轮 agent 都正确理解前态
- 每一轮 agent 都正确 handoff
- 每一轮 agent 都正确 `report`
- 每一轮 agent 都正确继续

那么它的长期可靠性一定按轮数塌缩。

例如，若每轮正确 `report` 概率为 `0.99`：

- 10 轮全对：`0.99^10 ≈ 0.904`
- 100 轮全对：`0.99^100 ≈ 0.366`

现实还更差，因为错误相关。

所以：

`worker loop` 可以是策略层，但不能是 foundation。

### 3.2 单次 raw run 不能直接成为事务单元

一轮 agent 执行结束时，系统可能面对的情况包括：

- 做对了，也正确 `report`
- 做对了，但忘了 `report`
- 没做对，却说自己 done
- 中途崩溃，只留下部分 artifact
- 留下了 report，但 report 语义有误

因此：

一次 raw run 本身不能自动等于“一个已提交事务”。

### 3.3 连续性不能锚在 agent 心智里

如果系统需要靠：

- agent 回忆上轮做了什么
- agent 自己恢复 handoff
- agent 自己稳定维持 ownership

那长期一定不稳定。

所以连续性必须锚在 agent 之外的持久状态里。

## 4. 重新定义系统对象

### 4.1 `raw agent run`

`raw agent run = 一次真实的 agent 执行尝试`

它的性质：

- 概率性
- 有边界
- 有 trajectory
- 有 effects
- 可能成功，也可能失败，也可能半成功

它不是事务，也不是 durable truth。

### 4.2 `settlement`

`settlement = 系统在 raw run 结束后，对这次尝试进行的确定性归档与结算`

它至少回答：

- 这次运行是谁发起的
- 它产生了哪些 trajectory / effects / reports
- 哪些结果进入 accepted state
- 哪些结果只是 artifact
- ticket 当前应如何分类：继续、阻塞、待验收、失败、待人工

关键点：

**事务化发生在 settlement，而不是发生在 raw run 内。**

### 4.3 `ticket run`

`ticket run = 针对某个 ticket snapshot 发起的一次 raw run + 运行结束后的 settlement`

这才是 dalek 的执行原语。

换句话说：

- `raw run` 是尝试
- `ticket run` 是一次被系统结算过的尝试

### 4.4 `ticket`

`ticket = dalek 的持久原语`

它至少包含：

- goal
- constraints
- accepted state
- history
- evidence refs
- acceptance boundary

ticket 不等于一次运行，也不等于 loop。
它是那台“任务状态机”的持久外壳。

### 4.5 `worker`

`worker = 资源容器`

它负责：

- worktree
- runtime
- 会话宿主
- 环境隔离

worker 不是基石，也不是执行原语。

### 4.6 `worker loop`

`worker loop = 围绕 ticket run 的调度策略层`

它负责：

- 何时发起下一次 `ticket run`
- 何时等待
- 何时重试
- 何时升级人工
- 何时触发 acceptance

它不是 foundation，而是 policy。

### 4.7 `acceptance boundary / oracle`

`acceptance boundary = 把“agent 说 done”与“ticket 真闭合”分开的硬边界`

oracle 可以是：

- automated tests
- acceptance runner
- formal checks
- human review

没有这个边界，系统最多只能得到控制层闭合，不能得到交付层闭合。

## 5. 真正的基石是什么

### 5.1 不是 `worker loop`

因为它依赖连续多轮 agent 正确运行，长期会塌。

### 5.2 不是 `raw agent run`

因为它只是一次概率尝试，没有事务化，也没有外部连续性。

### 5.3 dalek 的基石是 `ticket machine`

更准确地说：

`ticket machine = ticket + ticket run + acceptance boundary`

其中：

- `ticket` 是持久原语
- `ticket run` 是执行原语
- `acceptance boundary` 是闭合原语

这三者组合起来，才构成真正可托付的基础。

## 6. 为什么 `ticket run` 才是执行原语

这里必须和 `raw run` 区分。

### 6.1 `raw run`

只说明：

- agent 实际跑过一次
- 留下了一些 trajectory 与 effects

但不说明：

- ticket 是否推进
- 哪些结果被接受
- 当前 accepted state 是否变化

### 6.2 `ticket run`

说明：

- 系统承认发生过一次尝试
- 这次尝试被放进了 ticket history
- 系统完成了对这次尝试的 settlement
- accepted state 被更新或明确未更新

所以：

`ticket run` 是执行原语，不是因为它比 raw run 更像代码流程，
而是因为只有它才是一个对 ticket machine 有确定意义的执行单位。

## 7. 新的对象分层

### 7.1 Foundation

- `ticket`
- `ticket run`
- `acceptance boundary`

### 7.2 Policy / Scheduling

- `worker loop`
- retry policy
- timeout policy
- escalation policy

### 7.3 Runtime / Resources

- `worker`
- worktree
- session
- env

### 7.4 Execution substrate

- `raw agent run`
- tools
- trajectory
- effects

## 8. 用完整场景验证

下面这个场景用来验证这套分层：

1. `ticket` 当前 accepted state 为 `S0`
2. 发起 `ticket run #1`
3. raw agent run 发生：
   - 读代码
   - 改文件
   - 跑测试
   - 但忘了 `report`
4. raw run 结束
5. 系统执行 settlement：
   - 记录这次 run 存在
   - 记录其 trajectory 与 artifact refs
   - 发现没有可接受的 report
   - 因而 `ticket.accepted_state` 仍然停留在 `S0`
   - 但 ticket history 增加一条“未完成结算的执行记录”
6. `worker loop` 根据策略决定再发起 `ticket run #2`
7. `ticket run #2` 可以读取 `#1` 的 artifact，但连续性锚点仍然是 `ticket.accepted_state`
8. `ticket run #2` 正确 report，settlement 将 accepted state 推进到 `S1`
9. acceptance/oracle 检查通过后，ticket 才能真正闭合

这套场景里，如果系统始终能回答：

- 当前 accepted state 是什么
- 哪些东西只是 artifact
- 哪次 ticket run 已经结算
- ticket 是否已越过 acceptance boundary

那这套 foundation 才算成立。

## 9. 对现有 `worker-loop-foundation.md` 的处理建议

### 9.1 应保留

- 上层不该自己补 ownership / recovery / handoff / dedupe
- artifact durability 不等于 accepted progress
- 需要用完整生命周期场景来验证定义

### 9.2 应改写

- `worker loop = 基石`
  应改成：
  `worker loop = 调度策略层`

- `single run = 事务性步进`
  应改成：
  `raw run = 概率尝试`
  `ticket run = 带 settlement 的执行原语`

- `committable run`
  应从 raw run 内部概念，改为 settlement 结果概念

### 9.3 应废弃

- 把 `worker loop` 直接定义成 foundation
- 把 raw single run 直接定义成可提交事务
- 把 agent 的输出直接当成 authoritative state

## 10. 当前最值得继续的问题

在这套新框架下，最值得继续的不是再抽象 runner，而是：

1. `ticket.accepted_state` 的最小状态集是什么
2. `settlement` 的最小输入与输出是什么
3. `ticket run` 的边界如何形式化
4. acceptance/oracle 如何接入 ticket machine

## 11. 一句话总结

在 `agent 是概率推理机` 的前提下，dalek 的基石不是 `worker loop`，而是：

`ticket machine = ticket + ticket run + acceptance boundary`

其中：

- `ticket` 是持久原语
- `ticket run` 是执行原语
- `worker loop` 只是调度策略层
