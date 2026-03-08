# Dalek PM Autonomy Hardening PRD

## 背景

web console 这个真实产品 case 已经证明当前代 dalek 能把一个真实需求推进到交付，并完成真实浏览器验收。

但执行链路本身仍有明显缺口：

- `dispatch_ticket` 会出现“语义上成功、终态上失败”的冲突记账
- planner 经常因为旧的 timeout / 恢复机制不稳而中断
- `plan.md` 混合了 narrative、运行态和历史，不能作为稳定的执行真相源
- PM 的真实验收证据虽然存在，但没有成为系统内建的 feature state machine
- 并行改同一表面时，系统缺少前置 integration 策略，导致 merge churn

当前目标不是再交付一个新产品页面，而是把当前代 dalek 自身打磨成一个更稳定、更可续跑、更可信的自治 PM。

## 目标

- 让 PM 的执行状态、任务终态和验收结论彼此一致，避免同一条链路里出现冲突事实
- 让 planner / dispatch 在超时、取消、daemon 重启后能够自动恢复，而不是依赖人工接管
- 把 `plan` 从 narrative 文档升级成结构化 feature graph，并以此驱动 PM 续跑
- 把真实场景验收做成系统内建能力，而不是一次性的人工流程
- 把并行开发的 integration / conflict 风险前置建模，减少 merge churn

## 非目标

- 这轮不重写整个 dalek 架构
- 这轮不追求把所有 CLI 行为都改成 web 操作
- 这轮不做新的视觉产品 feature
- 这轮不引入复杂权限系统或多租户模型

## 用户与场景

### 核心用户

- 使用 dalek 管理多个 AI worker 的 PM / 技术负责人
- 依赖 dalek 自主推进真实需求的 self-hosting 使用者

### 核心场景

1. PM 接收一个真实需求后，先生成需求和设计文档。
2. PM 将 feature 拆成多个 ticket，并持续推进。
3. worker 通过测试 / 构建 / lint 证明单 ticket 工程上可合入。
4. PM 通过真实场景验收 feature，而不是把 `go test` 当作最终结论。
5. 中途如果 planner 超时、dispatch 中断、daemon 重启，系统能自己恢复。

## 需求范围

### 1. 任务运行态必须可信

- `dispatch_ticket` 不能再出现“summary 成功但 run_status failed”的冲突
- worker 的 terminal report 只能生效一次
- task run 的 terminal state 不能被后续写入反复覆盖

### 2. planner 必须以可恢复操作流推进

- planner 产出的不是散乱的 Bash 意图，而是结构化 `PMOps`
- `PMOps` 必须支持幂等、重试、checkpoint 和 crash recovery
- planner 超时或 daemon 重启后，必须能从未完成 op 继续

### 3. plan 必须升级为结构化真相源

- feature 至少要有 requirement、design、ticket、integration、acceptance 五类节点
- 节点必须显式表达依赖、owner、状态、done_when 和 evidence
- `plan.md` 只负责渲染，不再作为唯一真相源

### 4. acceptance 必须成为一等能力

- PM 必须能够运行真实浏览器 / API / CLI case
- acceptance 结果必须沉淀 evidence
- acceptance 失败时，系统必须能自动生成 gap / fix / integration ticket

### 5. integration 风险必须前置处理

- ticket 创建时需要表达改动表面或 touch surfaces
- 当多个 ticket 改同一表面时，系统要提前决定串行、集成或冲突策略
- merge churn 需要可观测、可追踪

### 6. PM 自治健康度必须可观测

- 至少提供 planner timeout、dispatch bootstrap failure、terminal conflict、merge conflict churn、manual intervention 等指标
- PM 能从这些指标判断当前自治链路是否退化

## 成功标准

- 用真实需求重新跑一轮 feature 时，不再出现 dispatch 语义成功但 task failed 的矛盾
- planner / dispatch 在超时或 daemon 重启后能自动恢复，不需要人工补 dispatch
- PM 能从结构化 plan/state/evidence 冷启动续跑
- feature 完成结论由真实 acceptance evidence 驱动，而不是 `go test` 驱动
- 并行开发 case 下，merge discard 次数明显下降，integration ticket 是预期动作，不是补锅动作

## 约束

- 遵守现有分层：`cmd -> app -> services -> store`
- app 层不直接访问 DB
- 所有状态推进仍通过 dalek CLI / services / state machine 完成
- PM 不直接改产品实现文件；产品实现仍由 ticket + worker 完成

## 验收口径

- `ticket + worker`：测试 / 构建 / lint / 可合入性优先
- `PM`：真实场景验收优先
- 只有当 requirement、design、ticket、integration、acceptance 这些节点都被 feature graph 判定完成，PM 才能宣布 feature 完成
