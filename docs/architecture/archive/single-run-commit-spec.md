# Single Run Commit Spec（草案）

> 本文是目标态规格草案，不描述当前代码实现。
> 它承接 [worker-loop-foundation.md](/Users/xiewanpeng/agi/dalek/docs/architecture/worker-loop-foundation.md) 中的抽象讨论，把 `single run` 进一步形式化为“语义声明的协议化提交过程”。
> 本文优先回答：在 `只有 agent 能理解语义，代码层只能负责流程和规则` 的前提下，dalek 应如何定义最小的可托付执行单元。

## 1. 目的

`single run` 不是一段 agent 会话时长，也不是一次普通执行尝试。

本文的目标是定义：

- 什么叫一次 `single run`
- 什么叫一次被系统承认的 `commit`
- runtime 能判断什么，不能判断什么
- `single run` 如何成为 `ticket-level worker loop` 的最小真相推进单元

如果这层定义不清楚，后续实现会反复混淆：

- 执行过的 artifact
- 被承认的 progress
- agent 的语义判断
- runtime 的流程裁定
- ticket / PM 的治理闭合

## 2. 顶层公理

### A1. 语义只存在于 agent 侧

只有 agent 能理解：

- 需求语义
- 代码语义
- 验证语义
- handoff 的真实含义

代码层不能真正理解“是否完成”“是否满足需求”“这份证据是否充分”。

### A2. runtime 只能管理协议真相

runtime 可以判断：

- 某条声明是否来自当前有效 owner
- 是否属于当前 generation
- 是否满足 claim / commit 结构
- 是否 stale / duplicate / late
- 是否应被接受进入控制面

runtime 不能判断：

- 这条 `done` 是否语义上真的完成
- 这条 `wait_user` 是否真的合理
- 这份 evidence 是否足以证明验收通过

### A3. ticket / PM 拥有治理解释权

ticket / PM 决定：

- 哪些语义声明足以推进 ticket
- 哪些语义声明只构成阶段性 progress
- 哪些 `done claim` 足以闭合 ticket
- 何时 rerun / replan / interrupt / archive

### A4. 系统真相必须可恢复、可追踪、可裁定

任意时刻系统都必须能回答：

- 当前谁有资格继续推进
- 最后一次被承认的语义声明是什么
- 当前控制面为什么处于此状态
- 哪些东西只是 artifact，哪些东西已经进入真相

## 3. 权威分层

由上述公理直接推出，dalek 必须拆开三种 authority：

### 3.1 语义 authority

由 agent 持有。

它负责产出：

- 当前理解到的 prestate
- 本轮实际做了什么
- 本轮认为的后继意图
- handoff
- rationale
- evidence 引用

### 3.2 协议 authority

由 runtime 持有。

它负责裁定：

- 这条 claim 是否来自有效上下文
- 这条 claim 是否可以 commit
- 这条 claim 是否被更晚的 generation / owner 淘汰
- 这条 claim 是否应被拒绝为 stale / duplicate / malformed

### 3.3 治理 authority

由 ticket / PM 持有。

它负责解释：

- 一个 committed claim 对 ticket 意味着什么
- ticket 是否进入 blocked / active / done 等治理状态
- 是否还需要 acceptance、人工确认或后续 ticket

## 4. 核心定义

### 4.1 Run Attempt

`run attempt` 是一次 agent 执行尝试。

它可能产生：

- 代码 diff
- 日志
- 临时文件
- 草稿
- 半完成输出

这些都只是 artifact，不自动进入系统真相。

### 4.2 Semantic Claim

`semantic claim` 是 agent 针对本轮执行所做的结构化语义声明。

它至少回答：

- 我基于哪个前态工作
- 我理解到的当前状态是什么
- 我做了什么
- 我认为什么是本轮结果
- 我建议下一步怎么走
- 我把控制权交给谁 / 什么上下文

### 4.3 Committed Claim

`committed claim` 是一条已经穿过协议校验、被 runtime 承认进入控制面的 `semantic claim`。

只有 `committed claim` 能：

- 成为后继 run 的权威前态
- 影响 loop continuity
- 被 ticket / PM 当成治理输入

### 4.4 Single Run

`single run = 一次 semantic claim 的生成与协议化提交过程`

它的边界不是“进程启动到退出”，而是：

- claim identity 被分配
- 执行发生
- semantic claim 被产出
- runtime 判断该 claim 是否 commit
- 结果进入 committed / rejected / superseded 之一

## 5. Claim Model

一条 `semantic claim` 至少应包含以下字段。

### 5.1 身份字段

- `ticket_id`
- `worker_id`
- `run_id`
- `owner_id`
- `loop_generation`
- `claim_id`

这些字段用于回答：

- 这是谁在说
- 这句话代表哪一轮上下文
- 这句话与哪个连续性谱系绑定

### 5.2 语义输入字段

- `prestate_ref`
- `context_version`
- `accepted_inputs`
- `user_updates_ref`

这些字段用于回答：

- 这条 claim 依据什么前态形成
- 它是否吸收了最新用户澄清

### 5.3 语义输出字段

- `state_summary`
- `actions_taken`
- `artifact_refs`
- `evidence_refs`
- `rationale`
- `next_intent`
- `handoff`

其中：

- `artifact_refs` 指向执行残留或工作产物
- `evidence_refs` 指向支撑语义判断的材料
- `next_intent` 是受限枚举，例如 `continue / wait_user / done / failed / canceled / unknown`

### 5.4 协议字段

- `created_at`
- `submitted_at`
- `claim_digest`
- `supersedes_claim_id`（可选）
- `idempotency_key`

这些字段用于：

- 去重
- 幂等
- 审计
- 迟到裁定

## 6. Commit Rules

runtime 对 claim 的判断，只能基于协议规则，不能基于语义理解。

### 6.1 commit 前置条件

一条 claim 只有在以下条件全部满足时，才有资格 commit：

- `ticket_id / worker_id / run_id / claim_id` 完整
- claim 来自当前有效 owner
- claim 属于当前有效 `loop_generation`
- claim 没有被相同 `idempotency_key` 成功提交过
- claim 不是已经被更新上下文淘汰的旧声明
- claim 结构完整，具备最小 handoff 与 next intent

### 6.2 runtime 允许的裁定结果

runtime 对 claim 只能给出有限协议结果：

- `committed`
- `rejected_malformed`
- `rejected_stale`
- `rejected_duplicate`
- `rejected_invalid_owner`
- `rejected_invalid_generation`
- `superseded`

runtime 不得输出：

- `语义正确`
- `语义错误`
- `任务确实完成`

### 6.3 commit barrier

`commit barrier` 的意义是：

- artifact 可以先存在
- 语义声明可以先产出
- 但只有跨过 barrier 的 claim 才能进入系统真相

因此：

- 代码 diff 不等于 committed progress
- 日志存在不等于 committed progress
- 某次 agent 自述 `done` 不等于 ticket 已闭合

## 7. Artifact 与 Evidence

### 7.1 Artifact

`artifact` 是执行中产生的对象，例如：

- worktree diff
- 编译产物
- 日志片段
- 临时文件
- 测试输出

对 runtime 来说，artifact 是可索引的，但不是可理解的。

### 7.2 Evidence

`evidence` 是 agent 或 PM 用来支撑语义判断的材料。

对 runtime 来说，evidence 也是不透明对象。runtime 只能负责：

- 记录 provenance
- 关联到 `ticket_id / run_id / claim_id`
- 保证可追踪、不可静默篡改

runtime 不负责判断 evidence 是否“足够”。

### 7.3 未提交 artifact 的再利用

未提交 artifact 可以被后续 run 读取和再利用，但必须满足：

- 它只能作为未承诺材料存在
- 它不能自动继承前一条未提交 claim 的 authority
- 后继 run 若采用这些 artifact，必须在自己的 committed claim 中重新声明和接管

这条规则保证：

- 不浪费真实工作
- 同时不绕穿 commit barrier

## 8. Single Run 的状态结果

从协议层看，一次 `single run` 不需要暴露过多语义态，而应收敛为有限结果集：

- `committed`
- `rejected`
- `superseded`
- `interrupted`
- `abandoned`

说明：

- `committed` 表示本轮 claim 已进入控制面
- `rejected` 表示 claim 产出了，但未通过协议裁定
- `superseded` 表示 claim 在提交前后被更新 generation 或更晚 claim 覆盖
- `interrupted` 表示 run 被合法打断，尚未形成 committed claim
- `abandoned` 表示运行结束但没有留下可提交 claim

这套结果描述的是协议结局，不是 ticket 业务结局。

## 9. Loop Continuity Rules

`worker loop` 不理解 claim 的语义内容，但必须维护 claim 的连续性。

它至少要保证：

- 任意时刻只有一个有效 owner 能提交下一条 claim
- 下一条 run 总是基于最后一条 committed claim 启动
- 用户澄清、人工接管、interrupt 会形成新的上下文边界
- 旧 generation 的 claim 不会污染新连续性
- late claim 不会改写当前真相

因此，loop 的本质不是“循环执行”，而是：

`维护已承认语义声明之间的合法接力关系`

## 10. Ticket Governance Rules

ticket 不负责产生 claim，但它定义 claim 的治理意义。

### 10.1 ticket 消费 committed claim

ticket / PM 会消费：

- `next_intent`
- `handoff`
- `rationale`
- `evidence_refs`

并据此决定：

- 是否继续 dispatch
- 是否进入 blocked
- 是否接受 done claim
- 是否需要 acceptance
- 是否生成新的 ticket 或 merge 动作

### 10.2 `done claim` 不等于 ticket closed

这是一个强约束：

- agent 可以提交 `done claim`
- runtime 可以 commit `done claim`
- 但只有治理层能决定 ticket 是否真正闭合

这意味着系统至少要区分：

- `done_claimed`
- `done_committed`
- `ticket_closed`

否则语义层、协议层、治理层会再次混层。

## 11. Failure Model

本文假定以下失败会真实发生：

- daemon 崩溃
- worker 进程中断
- late report
- duplicate report
- 用户在中途补充澄清
- 人工接管
- worktree 中残留未提交 artifact

目标不是消除这些失败，而是保证失败发生后仍然可裁定：

- 哪条 claim 仍有效
- 哪些 artifact 只是残留
- 下一条 run 从哪里继续
- ticket 当前治理状态是否可信

## 12. What Runtime Must Never Infer

为了保持边界清晰，runtime 明确禁止推断以下内容：

- 代码改动是否已经满足需求
- 测试输出是否足以证明完成
- 某张截图是否说明验收通过
- `wait_user` 是否真的必要
- `done` 是否代表语义完成

runtime 只负责：

- 协议合法性
- 时序合法性
- 身份合法性
- 追踪与恢复

## 13. 验证场景

下面这个完整场景应被本规格稳定覆盖：

1. `run1` 提交阶段性 committed claim
2. `run2` 改了代码，留下 diff 与日志，但未完成 commit
3. 用户补充了一条澄清
4. daemon 崩溃并恢复
5. `run3` 基于 `run1` 的 committed claim 和用户澄清重新启动
6. `run3` 读取并吸收 `run2` 的 artifact，在自己的 claim 中重新接管
7. `run2` 的迟到 `done claim` 到达
8. runtime 因 generation / owner / freshness 拒绝该迟到 claim
9. `run3` 提交新的 `done claim`
10. ticket / PM 结合 evidence 决定是否真正闭合 ticket

如果系统无法在这个场景中无歧义回答“谁的 claim 当前有效、哪些东西只是 artifact、ticket 是否真的闭合”，则本 spec 仍不完备。

## 14. 非目标

本文不定义：

- claim 的具体存储表结构
- 具体 CLI 入口命名
- SDK / tmux / worktree 的实现细节
- acceptance runner 的实现
- merge queue 的完整治理模型

这些可以在后续实现设计中展开。

## 15. 待正式化的问题

本文仍有以下问题待继续收敛：

- `claim object` 的最小字段集是否还要进一步缩减
- `loop_generation` 的 bump 规则应如何定义
- `interrupted` 与 `abandoned` 是否需要继续合并
- `done_claimed -> ticket_closed` 之间的治理状态机应如何建模
- evidence 是否需要独立的摘要字段，以支持治理层快速判断

## 16. 一句话总结

`single run` 不是一次执行尝试，而是一次语义声明被协议化提交、并可被 ticket 治理消费的最小过程。
