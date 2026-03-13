# Worker Loop Acceptance Cases

> 本文回答的不是“spec 长什么样”，而是：
>
> **新体系上线时，我们到底拿什么真实 case 来验收。**
>
> 这些 case 分成三类：
>
> 1. 现有系统已经能跑通，改造后必须继续正常工作的
> 2. 现有系统处理不稳或处理不了，新系统必须能处理的
> 3. 新系统当前阶段可以明确不处理的
>
> 本文只写真实链路 case，不写 mock 测试。

## 1. 验收原则

所有验收 case 都必须满足：

- 走真实 `dalek` CLI / daemon / worker 链路
- 使用真实 ticket / worker / task_run / report
- 允许用 `go test`、`go build`、日志作为辅助证据
- 但最终判定以“真实产品链路是否按预期运转”为准

这里的“通过”不是指代码完全没有 bug，而是指：

- 系统控制面行为正确
- ticket 状态推进正确
- worker loop 行为符合预期
- 异常场景下不会把系统推入脏状态或不可恢复状态

## 2. 第一类：必须保持正常运行的基本 case

这些 case 是现有系统已经大体支持的。  
新系统改造后，必须全部还能跑通。

### Case A1：标准完成链路

场景：

- 创建一张简单 ticket，例如“修改某个 CLI 帮助文案并补对应测试”
- 执行 `ticket start`
- 执行 `worker run`
- worker 最终 `report(done)`

期望：

- ticket 从 `backlog/queued` 进入 `active`
- worker loop 正常结束
- ticket 最终进入 `done`
- integration 状态进入 `needs_merge`
- task run / ticket events / worker events 可追踪

为什么必须保住：

- 这是当前系统最基本的 happy path
- 如果这个都退化，新体系就没有资格继续讨论

### Case A2：需要人工输入的阻塞链路

场景：

- 创建一张有明确歧义的 ticket，例如“新增一个配置项，但默认值需要产品决策”
- worker 执行到一半发现无法继续，`report(wait_user)`

期望：

- ticket 从 `active` 进入 `blocked`
- 自动创建 needs_user inbox
- loop 停止，不再继续偷偷推进
- PM/用户补充信息后，允许再次推进同一张 ticket

为什么必须保住：

- 这是当前系统里 `wait_user` 的核心存在价值
- 新体系不能为了收口更严，把人机协作链路打坏

### Case A3：多轮 continue 的正常推进

场景：

- 创建一张明显不可能一轮做完的 ticket，例如“重构一组命令输出并补若干测试”
- worker 第一轮 `report(continue)`
- worker 第二轮继续执行
- 最终再 `report(done)` 或 `report(wait_user)`

期望：

- ticket 在中间阶段保持 `active`
- worker loop 能继续下一轮
- 多轮 run 的因果顺序清楚
- 最终收口结果正确进入 ticket workflow

为什么必须保住：

- 这正是 `worker loop` 存在的基础价值
- 如果多轮 continue 退化，loop 改造就没有意义

### Case A4：自动补报兜底仍然存在

场景：

- worker 本轮实际执行了任务，但第一次没有成功写 `next_action`
- 系统触发当前已有的补报路径

期望：

- 系统仍然会尝试要求 worker 补报
- 不会因为新机制引入后，直接失去当前已有兜底能力

为什么必须保住：

- 这是现有系统已经有的最弱控制闭环
- 新体系至少不能比当前更弱

## 3. 第二类：现有系统处理不稳，新系统必须能处理的 case

这些 case 才是这轮改造真正要买到的能力。

### Case B1：report 非法，但当前 stage 内可纠正

场景：

- worker 完成了一轮真实执行
- 但 report 字段不完整，或 `next_action` 语义不合法
- 当前 run 仍然活着，或者刚结束不久

现状问题：

- 当前系统主要只对“空 report”做一次补报
- 对“非法 report”没有统一的 stage 内纠偏闭环

新系统必须做到：

- 不把这轮执行直接判死
- 把控制违规明确反馈给当前 worker
- 允许它在当前 stage 内补齐并重报
- 只有纠正后的闭合结果才能推进 ticket

这类真实 case 示例：

- worker 漏填 `next_action`
- worker 填了 summary，但 `next_action=foo`
- worker 报 `done`，但缺必要收口字段

### Case B2：一轮 raw run 做了很多事，但第一次没收口成功

场景：

- worker 已经改了代码、跑了测试、工作区有明确进展
- 但第一次收口失败，例如 report 缺失或不合法

现状问题：

- 当前系统很容易把这种情况直接推向“空 report 异常”
- 导致“实际已推进”和“控制面未收口”之间断裂

新系统必须做到：

- 不把“第一次没收口成功”直接升级成 ticket 级失败
- 允许当前 stage 内继续纠偏
- 直到产生一个合法闭合结果，或明确升级人工

这是当前系统最关键的一个真实缺口。

### Case B3：复杂 ticket 的多阶段推进必须可恢复

场景：

- ticket 明显大于单次 raw run 容量
- worker 已经连续跑了两三轮
- 中途 daemon 重启、tmux 中断或 worker 重建

现状问题：

- 当前系统对“从哪里继续”的定义还不够硬
- 容易依赖日志、worktree 残留、人工猜测

新系统必须做到：

- 恢复点基于“最后一个已闭合 stage”
- 后续继续依赖压缩 handoff，而不是读完整历史硬猜
- 旧的半截产物不能直接冒充已提交推进

实际 case 示例：

- 一个 dalek 自身架构 ticket，连续 `continue` 两轮后 daemon 重启
- 重启后重新 `worker run`，应该能继续，而不是退回完全失忆或脏推进

### Case B4：用户中途澄清后，旧 loop 结果不能污染新世界

场景：

- worker 正在推进一张 ticket
- 用户通过 PM 补充了关键澄清，改变了后续推进语义
- 旧 loop / 旧 run 的迟到结果随后到达

现状问题：

- 当前系统没有正式 loop-level 治理规范来定义“旧结果何时失效”

新系统必须做到：

- 用户澄清会切断旧 loop 的权威性
- 旧结果不能再污染当前 ticket 主状态
- 新 loop 从新的治理前提继续推进

这是 `worker-loop-governance` 必须买到的核心能力。

### Case B5：worker 更换或重建后，ticket 仍能接续推进

场景：

- 原 worker session 坏了，或者需要重建 worktree / tmux
- 系统创建了新的 worker 绑定
- ticket 不是重新从零开始，而是继续推进

现状问题：

- 当前系统更多是“资源重建”语义
- 但“控制连续性”还没有被正式定义清楚

新系统必须做到：

- 换 worker 不等于换 ticket 世界
- 新 worker 只能从最后一个已闭合 stage 的 handoff 继续
- 旧 worker 的迟到结果不会再推进 ticket

### Case B6：loop 不能无限 continue，必须有升级/停止规则

场景：

- worker 连续多轮都 `continue`
- 但没有实质推进，或者一直在同一类控制问题里打转

现状问题：

- 当前 loop 级停止条件和升级条件没有正式 spec

新系统必须做到：

- 存在 loop 级预算或停机规则
- 连续无进展时，要么升级 PM，要么阻塞，要么显式失败
- 不能靠 worker 无限自循环

这不是性能优化问题，而是系统治理问题。

## 4. 第三类：当前阶段可以明确不处理的 case

这些 case 不是“永远不做”，而是这轮系统改造不用试图一起解决。

### Case C1：语义上完全正确

场景：

- worker 形式上完成了 ticket
- 但业务逻辑还是错的，或者有隐藏 bug

当前阶段结论：

- 新系统不负责保证语义真理
- 这类问题由测试、lint、运行时反馈、人工验收、后继修复 ticket 解决

也就是说：

- 新系统要保证“可收口、可治理、可继续修”
- 不保证“一次 ticket 就绝对做对”

### Case C2：同一 ticket 多 worker 并行协作编辑同一工作区

场景：

- 一张 ticket 同时让多个 worker 并行写同一份代码

当前阶段结论：

- 这不是当前 worker loop 改造要解决的问题
- 当前仍坚持“一张 ticket 一个有效 worker loop”

否则会把问题直接推向通用并发协作引擎。

### Case C3：任意复杂 graph/phase/workflow 可配置编排

场景：

- 用户希望像 langgraph 一样自由定义节点、边、状态机、恢复逻辑

当前阶段结论：

- 不做
- dalek 当前只服务 ticket 驱动的软件开发链路
- 不构建通用 workflow 平台

### Case C4：所有外部副作用的 exactly-once 保证

场景：

- worker 已经触发了外部命令或副作用
- 系统希望所有外部世界副作用都具备严格 exactly-once 语义

当前阶段结论：

- 不承诺
- 当前阶段只要求 ticket 主世界状态的控制面不被脏写
- 不扩展成完整分布式事务系统

### Case C5：自动理解所有历史 artifact 并完美恢复语义

场景：

- 没有正式 handoff
- 只有海量日志、worktree diff、零散文件残留
- 希望系统自动推断出正确恢复点

当前阶段结论：

- 不应作为基础能力承诺
- 正确方向是让 stage 闭合时留下最小必要 handoff
- 不是指望 runtime 事后“读心式恢复”

## 5. 验收的最低判断线

如果只压成最短的判断标准，新体系至少要达到：

### 5.1 不退化

下面这些当前链路必须继续工作：

- `done`
- `wait_user`
- 多轮 `continue`
- 缺 report 的基本补报兜底

### 5.2 真增益

下面这些当前不稳的场景，必须真正变稳：

- 非法 report 的 stage 内纠偏
- 复杂 ticket 的多轮推进后恢复
- 用户澄清后的旧结果失效
- worker 更换后的接续推进
- loop 无限 continue 的升级/停止规则

### 5.3 有边界

下面这些问题当前阶段明确不承诺：

- 语义绝对正确
- 多 worker 同票并发编辑
- 通用图编排平台
- 外部副作用 exactly-once
- 从任意脏 artifact 完美恢复

## 6. 最后的收敛

这份验收清单的作用，是把“新体系是否值得做”从抽象争论拉回到真实产品链路。

一句话说：

**这轮改造的验收标准，不是 spec 看起来多完备，而是这些真实 ticket/worker/run/report case 能不能比现在更稳地跑通。**
