# Worker Loop Foundation 思考稿

> 本文是抽象层的阶段性思考稿。
> 目标是先定义 `ticket-level worker loop`、`ticket execution` 与 `single step` 的问题空间、context 与性质要求。
> 本文暂时不讨论具体技术实现，也不把当前代码实现直接当成目标态。

## 1. 问题

当前真正要回答的不是“worker loop 怎么实现”，而是更高一层的问题：

1. 如果我们希望把 `ticket-level worker loop` 做成 dalek 的基石，它到底是什么对象？
2. 它的检验标准是什么？
3. 它的衡量标准是什么？
4. 什么程度才算“完备到足够”？
5. 如果 `worker loop` 的推进基石其实是单次 `step`，那这个单步推进的基本性质又必须是什么？

这几个问题之所以要先于实现，是因为：

- 如果对象定义不清楚，后续讨论很容易退化成“补 bug”和“加 guard”
- 如果检验标准不清楚，系统会把“能跑通”误当成“可托付”
- 如果单次 `step` 的性质不清楚，`worker loop` 就会变成若干不稳定会话的堆叠

一句话说：

我们现在要定义的，不是一个循环，而是一层可以被上层能力真正依赖的执行底座。

## 2. Context

### 2.1 dalek 的上层能力依赖什么

在 dalek 的整体结构里，`worker loop` 不是孤立存在的。
它一旦被当成基石，上层能力都会默认它是可信的：

- PM 自治编排
- ticket 生命周期推进
- 多轮 handoff
- crash recovery / rerun / restart
- acceptance / merge / archive 之前的收口
- 审计与回放

这意味着：

- 上层不应该再自己补一套“谁拥有 ticket”“从哪继续”“上一次到底成功没成功”的逻辑
- 上层应该能够把 `worker loop` 当成一个可信执行单元，而不是一个脆弱 helper

如果上层仍然需要自己处理：

- ownership
- recovery
- handoff
- dedupe
- 审计补偿

那 `worker loop` 还不能叫“基石”。

### 2.2 当前问题为什么容易被想窄

这个问题很容易被想成：

- “worker 能不能多跑几轮”
- “report 能不能收口”
- “start / run / done / archive 是否连起来”

但这些都还是实现层视角。

更高维的抽象其实是：

- `ticket-level worker loop` 不是流程片段
- 它更像一个绑定在 ticket 上的自治执行体
- `single step` 不是一次普通 agent 会话
- 它更像这个自治执行体的一次离散推进
- `raw agent run` 只是这个单步推进的底层实现材料

如果这个视角不先建立，后面所有讨论都会掉回“某个命令入口是否合理”的局部优化。

### 2.3 为什么 single step 也必须被重新定义

如果 `worker loop` 要成为基石，它不可能建立在“若干模糊 raw run 的自然拼接”上。

原因很直接：

- loop 的连续性来自 step 之间的 handoff
- loop 的恢复性来自 step 的 durable 提交
- loop 的唯一性来自 step 的 ownership
- loop 的可审计性来自 step 的因果链

所以 `step` 不是可有可无的执行颗粒。
它是 loop 的最小砖块。

如果 step 自身不是一个最小完备单元，loop 再怎么包装也只是堆不稳定砖头。

### 2.4 为什么必须明确 `ticket execution`

另一个此前没有钉死的问题是：

- `ticket` 是顶层任务包
- `raw agent run` 是底层一次 bounded tool loop

但一个复杂 ticket 几乎不可能刚好被一次 raw agent run 吞下。

原因不是实现技巧，而是底层 agent 原理本身决定的：

- context 有限
- 单次运行时长有限
- 单次可稳定维持的局部状态有限

这直接推出：

- 一个 ticket 的完整推进过程必然跨越多个单步推进
- `worker loop` 不是围绕一个 run 工作
- `worker loop` 实际上围绕的是整张 ticket 的 `execution`

因此，当前更准确的层级应该是：

- `ticket`
  顶层治理任务包
- `ticket execution`
  这张 ticket 被持续推进的完整过程
- `single step`
  loop 的最小标准推进单位
- `raw agent run`
  single step 的底层实现材料

## 3. 阶段性思考

下面不是最终答案，而是当前迭代过程中的关键阶段性收敛。

### 3.1 阶段一：先重新定义对象

最初最容易犯的错误，是把 `worker loop` 理解成：

- 多次 run 组成的流程
- 或者 ticket 状态机的一段执行逻辑

但这个定义太弱。

如果它只是流程，那么：

- 它天然依赖外部来判断当前走到哪
- 它天然依赖外部补 recovery
- 它天然依赖外部确认 handoff 是否有效

这不符合“基石”的要求。

当前更合适的定义是：

`ticket-level worker loop = 绑定在 ticket 上、可恢复、可治理、可审计的自治执行体`

这里的关键不在于“循环”，而在于：

- 自治
- 可恢复
- 可托付

### 3.2 阶段二：重新定义“基石”

一开始很容易把“基石”理解成“足够稳定、可以一直跑”。
但这仍然不够本质。

更准确的说法应该是：

`基石 = 上层能力可以直接信任，而不必再自带补偿逻辑的执行单位`

也就是说，判断它是不是基石，不看它会不会做代码实现，而看上层是否还能假定：

- 它知道谁在拥有当前 ticket
- 它知道下一步从哪里继续
- 它能把一次 run 的结果可靠交给后继
- 它崩了之后仍能恢复到可判断的状态

只要上层仍然需要自己猜：

- 当前 owner 是谁
- 上一次是否真的完成
- 此刻是否应该 rerun
- 最后一次有效 handoff 是什么

那它还不是基石。

### 3.3 阶段三：重新定义单步推进

另一个关键转折是：

不能把 `single step` 看成“一次 agent 会话”，也不能把它看成“一整张票跑完”。

如果这样理解，step 就只是：

- 某个 prompt
- 某段执行
- 某次退出

这对底层 runner 足够，但对基石不够。

更强的抽象应该是：

`single step = worker loop 围绕 ticket frontier 的一次标准化推进`

但到这里还不够。
还必须继续区分两个容易混掉的对象：

`raw agent run != committed step`

- `raw agent run`
  是一次底层 agent 执行尝试。它可以产生日志、diff、临时文件、半完成输出，但这些都只是 artifact

- `committed step`
  是一次穿过 commit barrier、被 ticket loop 正式承认的推进。只有它的后继信号、handoff、poststate 才能进入系统控制面

这个区分非常关键，因为：

- recovery 不能依赖“最后跑过什么”，只能依赖“最后提交了什么”
- dedupe 不能依赖“看起来像完成”，只能依赖“是否已被系统承认”
- handoff 不能消费裸执行残留，只能消费 authoritative commit

“标准化推进”这个表述之所以关键，是因为它天然要求：

- 有明确前态
- 有唯一 claim
- 有边界
- 有 durable 输出
- 有 handoff
- 有状态压缩

也就是说，一次 step 不只是“做了点什么”，而是“完成了一次可验证、可提交、可恢复、可续接的状态推进”。

### 3.4 阶段四：把检验标准分层

到这里，检验标准不能再混成一句“可靠性高”。

更合适的做法，是分成三层：

#### A. 定义性标准

这是二元标准，过不了就不合格：

- 是否存在唯一 owner
- 是否支持恢复
- 是否有明确 handoff
- 是否有闭合收敛态
- 是否可审计

#### B. 运行性指标

这是连续指标，用来衡量质量：

- 重复 owner 率
- 恢复歧义率
- 丢失 handoff 率
- 未提交 artifact 误判率
- 假完成率
- 假阻塞率
- 人工救援率
- 票级审计完整率

#### C. 完备性边界

这是最容易被忽略的一层：

什么叫“已经够了”？

当前阶段的回答是：

当上层能力不再需要自己补一套：

- ownership 判断
- recovery 逻辑
- last good state 推断
- handoff 补偿

这时才叫“完备到足够”。

### 3.5 阶段五：把契约拆成三层

到这里我开始觉得，如果把所有要求都压成“run 的性质”，文档会混层。

更合理的方式，是把契约拆成三层：

#### A. agent assumptions

这是我们对底层 `raw agent run` 能做的最小能力假定：

- 能读取显式前态，而不是依赖隐性记忆
- 能在有边界的 scope 内执行，而不是无限会话
- 能通过受控工具产生可观测副作用
- 能输出候选控制结果，而不是天然权威结果
- 能吃外部反馈并继续修正后续执行
- 能诚实地失败，而不是伪造完成

#### B. runtime guarantees

这是不能寄望 agent 自觉、必须由 worker loop / step runtime 保证的性质：

- claim 分配与唯一 ownership
- commit barrier
- stale / duplicate result 拒绝
- interrupt / resume 的安全治理
- append-only 审计与可追踪事件链
- 对复杂 ticket 的单步状态压缩与回收

#### C. ticket governance obligations

这是 ticket 作为治理原语必须继续承担、不能偷塞给 loop 的责任：

- 目标与约束定义
- 验收与闭合语义
- 人工接管与授权边界
- blocked / continue / done 的业务解释权
- 何时允许 rerun / replan / archive 的治理决策

只有把这三层拆开，后续 spec 才不会把 agent 能力、runtime 责任、ticket 治理混成一团。

### 3.6 阶段六：单次推进的最小完备条件

如果 `single step` 是 loop 的最小完备单元，那么更准确地说，它不是“裸 raw run”，而是一个 `committable step`。

它至少要包含这些结构：

- `claim`
  必须明确：我代表哪个 ticket、哪个 owner、哪个 loop generation、哪个 run_id 在工作

- `readable prestate`
  必须能读取足够前态，而不是依赖模型的隐性记忆

- `bounded execution`
  必须有明确边界，不是无限延展的会话

- `artifact set`
  可以产生代码 diff、日志、草稿、临时输出，但这些 artifact 本身不等于系统已承认推进

- `candidate output`
  raw agent run 可以留下一个或多个候选后继信号，但这些都不是天然 authoritative

- `control feedback loop`
  如果候选输出缺失或非法，系统必须能生成 control violation 并回灌给当前 step 内的 raw agent run，让其继续修正

- `step settlement`
  必须在 step 结束时把 authoritative poststate / handoff / decision durable 地写入控制面，供后继消费

- `state compression`
  必须把当前 frontier 相关的上下文压缩成后继 step 可消费的状态包，而不是把所有原始轨迹继续滚下去

- `self handoff`
  后继 step 不靠猜测，也能继续

- `stale / duplicate safety`
  重试、重复提交、迟到提交不能破坏一致性

- `honest failure`
  不知道就是不知道，应该明确交回 blocked / needs_user，而不是伪造完成

- `interrupt point`
  必须允许被安全打断，并把控制权还给 loop

这里最重要的一点是：

一次 step 不是“执行过”，而是“被承认地提交过”。

如果只有 artifact，没有 step settlement，那它对系统来说仍然不是可靠步进。

## 4. 当前阶段性结论

### 4.1 对层级对象的当前定义

当前更稳妥的分层是：

- `ticket`
  是治理原语。它定义目标、约束、验收、闭合语义与人工介入权限

- `ticket execution`
  是这张 ticket 被持续推进的完整过程

- `worker loop`
  是绑定在 ticket 上的自治执行内核。它负责 ownership、recovery、handoff、dedupe、interrupt，并驱动多个 single step

- `single step`
  是 loop 内的一次 `committable step`，负责完成一次有边界的可验证推进

- `raw agent run`
  是 single step 内部的底层 bounded tool loop 实现

因此，真正可托付的不是裸 `worker loop`，而是“被 ticket 治理的、由 committed steps 组成的 worker loop”。

### 4.2 对它的必要属性

如果要成为基石，它至少必须具备：

- `提交性`
  只有 committed step 才能推进控制面

- `唯一性`
  任意时刻只能有一个被承认的推进 owner

- `连续性`
  每一步都从可确认前态进入，并把后态明确交给后继

- `可恢复性`
  崩溃、中断、人工接管后能恢复到可判断状态

- `可治理性`
  系统和人都能暂停、恢复、切换 ownership，而不打断因果链

- `可收敛性`
  它不是无限执行，而是走向闭合结果集

- `可审计性`
  每次推进都能回答 why / from / do / result / next

### 4.3 对完备性的当前判断

我现在认为“完备到足够”的标准不是：

- 功能很多
- 自动化很强
- happy path 很顺

而是：

上层是否已经可以不再为 ticket 执行自己补一套控制逻辑，并且不再把未提交 artifact 误认成 authoritative progress。

如果答案是“还得补”，那它就还没到基石级。

### 4.4 对 artifact 与 commit 的当前判断

我现在认为必须明确区分：

- `artifact durability`
  代码 diff、日志、tmux 残留、临时文件仍然存在

- `control commit`
  这一轮推进已经被系统承认，并进入后继可消费的控制面

前者可以说明“发生过执行”，但不能单独说明“发生过可靠推进”。

如果这两个层级不分开，crash recovery、late report、rerun 判定都会长期含糊。

## 5. 用完整场景做验证

为了避免抽象变成自我安慰，当前用下面这个完整场景来验证定义：

1. `step1`：理解需求，内部可能包含多个 raw agent run，最终提交一个阶段性 handoff
2. `step2`：实现改动，worktree 中已经产生代码 diff，但还没有完成 step settlement
3. daemon 崩溃
4. 系统重启
5. 用户插入一次澄清
6. `step3`：系统基于最后一次 committed handoff 恢复后继续验证
7. `step2` 内某个 raw agent run 的迟到 report 到达
8. `step4`：完成收口并进入 done

如果这时候系统始终能无歧义回答：

- 现在谁拥有 ticket
- 最后一次已提交 handoff 是什么
- 下一次从哪里继续
- `step2` 的代码 diff 属于 artifact 还是 committed progress
- 哪些副作用已经提交
- 哪些副作用尚未提交
- `step2` 的迟到 report 是否应该被拒绝
- 当前 done / blocked / continue 是否可信

那这个 loop 才配叫基石。

如果任何一步开始需要：

- PM 猜测
- 人工翻日志
- 模型自己回忆
- 上层额外补偿

那它就还没有完备。

## 6. 被推翻的假设

### 6.1 被推翻的假设一

原本很容易把 `worker loop` 当成“多次 run 的串联流程”。

现在看，这个定义太弱。
流程不等于自治执行体。

### 6.2 被推翻的假设二

原本很容易把“可靠性”理解成“可以持续跑很多轮”。

现在看，真正更关键的是：

- 能不能稳定交接控制权
- 能不能稳定恢复
- 能不能让上层不再补偿

### 6.3 被推翻的假设三

原本容易把 `single run` 当成一次普通 agent 执行。

现在看，它必须被理解成一次标准化单步推进。
否则它不是系统单元，只是执行尝试。

### 6.4 被推翻的假设四

原本也容易把 worktree 中已有代码、日志残留，当成系统已经承认的推进。

现在看，`artifact durability != control commit`。
前者说明“执行发生过”，后者才说明“状态推进被承认过”。

## 7. 还没解决的问题

这份文档目前还停在定义层，下面这些问题还没正式收敛：

- `worker loop` 的闭合状态集合到底怎么定义才最自然
- `single step` 的 settlement object 应该包含哪些字段
- handoff 的最小协议应该长什么样
- 什么才算 `loop generation`
- 人工介入是外部中断，还是 loop 内的合法状态

这些问题会决定后续的正式设计。

## 8. 下一步建议

在这份抽象思考之后，最值得继续的不是马上改代码，而是继续做两份文档：

1. `single-step-spec.md`
   只写 `raw agent run`、candidate report、control violation、step settlement、stale result rule

2. `ticket-loop-governance-spec.md`
   只写 ownership、generation、interrupt、recovery、closure、治理边界，以及 ticket execution 与 single step 的状态关系

这样后续实现讨论才不会继续混进：

- 当前代码怎么写
- 某个命令入口叫什么
- 某个字段该不该复用

这些局部问题里。
