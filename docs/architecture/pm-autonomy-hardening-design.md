# Dalek PM Autonomy Hardening Design

## 目标

把当前代 dalek 的 PM 从“能跑通 demo feature 的调度器”升级成“状态可信、可恢复、以真实场景验收为中心的自治控制面”。

## 设计原则

- 先修事实一致性，再增强自治能力
- 结构化状态优先，markdown 只是可读视图
- terminal state 不可回写，事件只追加
- planner 负责决策，executor 负责按步骤执行
- acceptance 是 feature state machine 的一等节点
- 当前代 dalek 是执行系统，下一代 dalek 是被交付产品，两个语义层必须隔离

## 分层语义

### L0: 执行层

当前代 dalek 自己的编排系统，负责：

- ticket / worker / dispatch / planner / merge / inbox / acceptance 的调度
- feature graph 的维护
- 真实验收的执行与证据采集

### L1: 产品层

被当前代 dalek 交付的真实需求，例如 web console。L1 的完成标准来自真实用户场景，而不是 L0 的内部日志。

所有文档和状态都必须标注自己服务于 L0 还是 L1，避免把“系统在忙”误判成“产品已完成”。

## 问题归因

### 1. dispatch 生命周期耦合过深

当前 `dispatch_ticket` 既承担“编译上下文并启动 worker”的职责，又被用于承载长时间 worker loop 的完成结果。这样会导致：

- dispatch 语义已经成功
- worker 已经开始并完成
- 但 PM 在 `ticket stop` 时仍能把 dispatch run 改写为 failed

这使得 task history 不再可信。

### 2. task terminal state 允许被重复更新

当前 `MarkRunSucceeded` / `MarkRunFailed` 只禁止覆盖 `canceled`，没有禁止覆盖其他 terminal state。worker 多次 `report done` 时，同一个 run 还能被反复标成成功并追加不同语义结论。

### 3. planner 输出缺少结构化执行边界

planner 现在更像“自由发挥的 agent + CLI 操作者”。一旦超时或 crash，很难知道：

- 哪些动作已经执行
- 哪些动作还没执行
- 下一轮应从哪里恢复

### 4. plan 不是稳定真相源

`plan.md` 同时承担了：

- narrative
- 当前运行态
- 历史决策
- 已完成 feature 归档
- ticket 计划

这会天然产生漂移，不适合作为 PM 的冷启动状态源。

### 5. acceptance 没有被建模为 feature 节点

当前 acceptance evidence 已存在，但它还不是 feature graph 中的显式 gate。系统缺少一条稳定规则：只有 acceptance nodes 全部通过，feature 才能完成。

### 6. integration 风险只在 merge 时暴露

并行改同一表面时，系统没有前置表达 `touch_surfaces`，于是 `t19/t20` 这种冲突直到 merge 才被动暴露，导致多次 discard。

## 目标架构

```text
User Requirement
  -> Requirement Doc
  -> Design Doc
  -> Feature Graph (source of truth)
       -> Ticket Nodes
       -> Integration Nodes
       -> Acceptance Nodes
  -> Planner (decides PMOps)
  -> PMOps Journal (idempotent execution log)
  -> Executors
       -> ticket/merge/inbox executors
       -> acceptance runner
  -> Evidence + Metrics
  -> Feature completion decision
```

## 详细设计

### A. Task lifecycle hardening

#### A1. 拆分 dispatch 与 delivery 语义

- `dispatch_ticket` 只代表“本轮 dispatch 编排是否成功完成”
- 它的完成标准是：
  - worker 已准备好
  - context 已注入
  - worker loop 已被成功启动
- 后续长期执行结果只由 worker 的 `deliver_ticket` run 表达

如果 worker 继续运行、完成、失败或等待用户，全部只更新 worker run，不再回写 dispatch run。

#### A2. terminal state 不可变

task runtime 层新增统一规则：

- 允许：`pending -> running -> done|failed|canceled`
- 禁止：任何 terminal -> 另一个 terminal
- 禁止：terminal -> running

若收到非法终态更新：

- 不覆盖原状态
- 追加 `terminal_update_rejected` 事件
- 把冲突计入自治健康指标

#### A3. worker done 只生效一次

worker report 流程增加 guard：

- 如果 run 已是 terminal，后续 `report done` 不再调用 `MarkRunSucceeded`
- 只记录 `duplicate_terminal_report`
- PM 在消费事件时可据此诊断 worker 行为异常

### B. Planner PMOps + recovery

#### B1. planner 输出结构化 `PMOps`

planner 不直接自由执行整个编排流程，而是输出结构化操作：

- `write_requirement_doc`
- `write_design_doc`
- `create_ticket`
- `dispatch_ticket`
- `create_integration_ticket`
- `close_inbox`
- `run_acceptance`
- `set_feature_status`

每个 op 都带：

- `op_id`
- `feature_id`
- `request_id`
- `kind`
- `arguments`
- `preconditions`
- `idempotency_key`

#### B2. 引入 PMOps journal

新增 PM journal，用于持久化每个 op 的执行状态：

- `planned`
- `running`
- `succeeded`
- `failed`
- `superseded`

恢复策略：

- daemon 重启或 planner timeout 后，先读取 journal
- 对 `running` 且无完成记录的 op 做恢复判断
- 若 op 可判定已生效，则补记成功
- 若 op 未生效，则重试

#### B3. planner checkpoint

每轮 planner run 写入 checkpoint：

- 输入的 feature graph version
- 已确认完成的 op list
- 下一步候选节点
- 失败上下文

这样 planner 重新运行时，不需要重新推导整轮历史。

### C. Feature graph source of truth

#### C1. 结构化状态

新增 `plan.json` 作为 feature graph 真相源，至少包含：

- `feature_id`
- `goal`
- `docs`
- `nodes`
- `edges`
- `current_focus`
- `next_pm_action`
- `updated_at`

节点类型：

- `requirement`
- `design`
- `ticket`
- `integration`
- `acceptance`

节点字段：

- `id`
- `type`
- `title`
- `owner`
- `status`
- `depends_on`
- `done_when`
- `touch_surfaces`
- `evidence_refs`
- `estimated_size`

#### C2. `plan.md` 只渲染

`plan.md` 变成一个稳定模板的只读渲染结果，区块固定为：

- Goal
- Current Status
- Execution Graph
- Ready Nodes
- Blocked Nodes
- Acceptance Gates
- Latest Evidence
- Next PM Action

PM 不再整篇改写 narrative，而是对 `plan.json` 做增量操作，然后重新渲染。

#### C3. 文档拆分

建议文件布局：

- `docs/product/<feature>-prd.md`
- `docs/architecture/<feature>-design.md`
- `.dalek/pm/plan.json`
- `.dalek/pm/plan.md`
- `.dalek/pm/state.json`
- `.dalek/pm/acceptance.md`
- `.dalek/pm/decisions.jsonl`

### D. Acceptance engine

#### D1. acceptance node

每个 feature 至少有一个 acceptance node；复杂 feature 可有多个 gate，例如：

- `A-browser-overview`
- `A-api-real-json`
- `A-user-flow-status-change`

只有全部 acceptance nodes 通过，feature 才能完成。

#### D2. acceptance runner

acceptance runner 是 PM 的专用执行器，支持：

- `pw` 浏览器脚本
- `curl` / CLI / HTTP 校验
- stdout / stderr / snapshot / screenshot 采集

每次运行都会生成 evidence bundle，并写回：

- acceptance node 状态
- `acceptance.md`
- `state.json`

#### D3. acceptance failure -> gap ticket

如果 acceptance 失败：

- 将 acceptance node 标成 `failed`
- 记录 evidence
- 自动生成 `gap` 或 `integration` ticket
- feature 状态从 `verifying` 回到 `running`

### E. Integration policy + observability

#### E1. touch surfaces

每个 ticket 节点必须声明改动表面，例如：

- `internal/web/static/app.js`
- `internal/web/static/style.css`
- `internal/app/daemon_web_component.go`

planner 在创建新 ticket 时必须考虑：

- 是否与已有活跃 ticket 冲突
- 是否需要串行
- 是否应提前创建 integration node

#### E2. merge churn metrics

引入以下健康指标：

- `planner_timeout_rate`
- `dispatch_bootstrap_failure_rate`
- `terminal_state_conflict_count`
- `duplicate_terminal_report_count`
- `merge_discard_count`
- `integration_ticket_count`
- `manual_intervention_count`
- `real_acceptance_pass_rate`

这些指标将成为 PM 判断“当前代 dalek 是否真的自治稳定”的依据。

## 数据迁移

### Phase 1

- 保留现有 `.dalek/pm/plan.md`
- 新增 `plan.json`
- 渲染器从 `plan.json` 生成 `plan.md`

### Phase 2

- planner prompt 从直接读 `plan.md` 升级成：
  - `plan.json` 摘要
  - `plan.md` 渲染视图
  - `state.json`
  - `acceptance.md`

### Phase 3

- 默认所有新 feature 都从 requirement/design/feature graph 启动

## 兼容性与回滚

- 旧 feature 没有 `plan.json` 时，系统可从现有 `plan.md` 生成一次初始 graph
- 若新 executor 出现问题，可回退到旧 planner direct-exec 模式，但仍保留 terminal guard

## 实施顺序

1. 修 task lifecycle：dispatch 语义拆分、terminal guard、duplicate done guard
2. 引入 `plan.json` 和 `plan.md` 渲染链
3. 引入 `PMOps journal` 与 checkpoint recovery
4. 实现 acceptance engine
5. 实现 integration policy 与健康指标
6. 用真实 feature 再跑一轮零接管回归

## 风险

- planner 改成 `PMOps` 后，prompt 需要重新收敛，否则会影响当前成功率
- `plan.json` 与旧 `plan.md` 共存阶段，若渲染链不稳会短期增加状态复杂度
- acceptance runner 一旦权限模型不清晰，可能误把执行器问题归咎到产品问题

## 预期结果

改造完成后，dalek 的 PM 将具备以下能力：

- 任何时刻都能从结构化状态冷启动恢复
- 不再因为 dispatch / terminal 记账冲突而误判世界
- 不再把 narrative markdown 当成唯一真相源
- 能把真实 acceptance 作为 feature 完成的硬门槛
- 能在并行开发时更早处理 integration 风险
