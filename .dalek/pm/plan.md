# PM Plan (Rendered)

> Source of truth: `.dalek/pm/plan.json`
> Generated at: 2026-03-09T21:29:35Z
> This file is rendered from plan.json. Do not edit directly.

## Current Feature: 优化 PM 自治编排与 plan 系统

current_phase: ticket
current_ticket: T-pm-runtime-ledger
current_status: pending
last_action: synced from plan.json @ 2026-03-09T21:29:35Z
next_action: 从 t24 开始推进任务运行态可信化，再依赖解锁后续 tickets
blocker: 无

## Goal
- feature_id: feature-pm-plan
- goal: 优化 PM 自治编排与 plan 系统
- doc: docs/product/web-console-prd.md (需求文档)
- doc: docs/architecture/web-console-design.md (设计文档)

## Current Status
- current_focus: ticket-t-pm-runtime-ledger
- next_pm_action: 从 t24 开始推进任务运行态可信化，再依赖解锁后续 tickets
- node_counts: pending=13 in_progress=0 done=2 blocked=0 failed=0

## Execution Graph
| id | type | owner | status | depends_on | title | done_when |
| --- | --- | --- | --- | --- | --- | --- |
| requirement-main | requirement | pm | done | - | Requirement Baseline | 需求文档已完成并覆盖目标/范围/验收口径 |
| design-main | design | pm | done | requirement-main | Design Baseline | 设计文档已完成并与需求对齐 |
| ticket-t-pm-runtime-ledger | ticket | worker | pending | design-main | dispatch 生命周期拆分、task terminal guard、duplicate worker done guard、冲突事件与测试 | dispatch 生命周期拆分、task terminal guard、duplicate worker done guard、冲突事件与测试 |
| ticket-t-plan-graph-sot | ticket | worker | pending | ticket-t-pm-runtime-ledger | 引入 `plan.json` feature graph、`plan.md` 渲染模板、state/evidence 同步链 | 引入 `plan.json` feature graph、`plan.md` 渲染模板、state/evidence 同步链 |
| ticket-t-pm-integration-observability | ticket | worker | pending | ticket-t-pm-runtime-ledger,ticket-t-plan-graph-sot | `touch_surfaces`、integration 策略、自治健康指标与展示 | `touch_surfaces`、integration 策略、自治健康指标与展示 |
| ticket-t-pm-oplog-recovery | ticket | worker | pending | ticket-t-pm-runtime-ledger,ticket-t-plan-graph-sot | planner 结构化 `PMOps`、journal、checkpoint、daemon/planner 恢复逻辑 | planner 结构化 `PMOps`、journal、checkpoint、daemon/planner 恢复逻辑 |
| ticket-t-pm-acceptance-engine | ticket | worker | pending | ticket-t-plan-graph-sot,ticket-t-pm-oplog-recovery | acceptance nodes、真实 runner、evidence bundle、失败后 gap ticket 自动生成 | acceptance nodes、真实 runner、evidence bundle、失败后 gap ticket 自动生成 |
| acceptance-01 | acceptance | pm | pending | ticket-t-plan-graph-sot,ticket-t-pm-acceptance-engine,ticket-t-pm-integration-observability,ticket-t-pm-oplog-recovery,ticket-t-pm-runtime-ledger | Acceptance Gate 01 | 启动真实 dalek 服务，而不是只运行测试。 |
| acceptance-02 | acceptance | pm | pending | ticket-t-plan-graph-sot,ticket-t-pm-acceptance-engine,ticket-t-pm-integration-observability,ticket-t-pm-oplog-recovery,ticket-t-pm-runtime-ledger | Acceptance Gate 02 | 在浏览器中打开 web 管理页面。 |
| acceptance-03 | acceptance | pm | pending | ticket-t-plan-graph-sot,ticket-t-pm-acceptance-engine,ticket-t-pm-integration-observability,ticket-t-pm-oplog-recovery,ticket-t-pm-runtime-ledger | Acceptance Gate 03 | 看到概览页真实展示项目状态。 |
| acceptance-04 | acceptance | pm | pending | ticket-t-plan-graph-sot,ticket-t-pm-acceptance-engine,ticket-t-pm-integration-observability,ticket-t-pm-oplog-recovery,ticket-t-pm-runtime-ledger | Acceptance Gate 04 | 进入 tickets 页面，确认 ticket 列表和详情可用。 |
| acceptance-05 | acceptance | pm | pending | ticket-t-plan-graph-sot,ticket-t-pm-acceptance-engine,ticket-t-pm-integration-observability,ticket-t-pm-oplog-recovery,ticket-t-pm-runtime-ledger | Acceptance Gate 05 | 进入 planner/runtime 页面，确认 planner 状态真实展示。 |
| acceptance-06 | acceptance | pm | pending | ticket-t-plan-graph-sot,ticket-t-pm-acceptance-engine,ticket-t-pm-integration-observability,ticket-t-pm-oplog-recovery,ticket-t-pm-runtime-ledger | Acceptance Gate 06 | 进入 merge / inbox 页面，确认对应数据真实展示。 |
| acceptance-07 | acceptance | pm | pending | ticket-t-plan-graph-sot,ticket-t-pm-acceptance-engine,ticket-t-pm-integration-observability,ticket-t-pm-oplog-recovery,ticket-t-pm-runtime-ledger | Acceptance Gate 07 | 至少完成一个真实用户操作链路，并观察状态变化： 例如创建 ticket 后页面刷新可见； 或处理 merge/inbox 后页面状态变化可见。 |
| acceptance-08 | acceptance | pm | pending | ticket-t-plan-graph-sot,ticket-t-pm-acceptance-engine,ticket-t-pm-integration-observability,ticket-t-pm-oplog-recovery,ticket-t-pm-runtime-ledger | Acceptance Gate 08 | 将验收过程记录到 acceptance evidence 中，包含： 使用的启动命令 访问 URL 关键页面截图或快照 关键操作步骤 最终结论 |

## Ready Nodes
- `ticket-t-pm-runtime-ledger` (ticket): dispatch 生命周期拆分、task terminal guard、duplicate worker done guard、冲突事件与测试

## Blocked Nodes
- `ticket-t-plan-graph-sot`: 引入 `plan.json` feature graph、`plan.md` 渲染模板、state/evidence 同步链 (waiting_on: ticket-t-pm-runtime-ledger)
- `ticket-t-pm-integration-observability`: `touch_surfaces`、integration 策略、自治健康指标与展示 (waiting_on: ticket-t-pm-runtime-ledger, ticket-t-plan-graph-sot)
- `ticket-t-pm-oplog-recovery`: planner 结构化 `PMOps`、journal、checkpoint、daemon/planner 恢复逻辑 (waiting_on: ticket-t-pm-runtime-ledger, ticket-t-plan-graph-sot)
- `ticket-t-pm-acceptance-engine`: acceptance nodes、真实 runner、evidence bundle、失败后 gap ticket 自动生成 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-oplog-recovery)
- `acceptance-01`: Acceptance Gate 01 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-acceptance-engine, ticket-t-pm-integration-observability, ticket-t-pm-oplog-recovery, ticket-t-pm-runtime-ledger)
- `acceptance-02`: Acceptance Gate 02 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-acceptance-engine, ticket-t-pm-integration-observability, ticket-t-pm-oplog-recovery, ticket-t-pm-runtime-ledger)
- `acceptance-03`: Acceptance Gate 03 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-acceptance-engine, ticket-t-pm-integration-observability, ticket-t-pm-oplog-recovery, ticket-t-pm-runtime-ledger)
- `acceptance-04`: Acceptance Gate 04 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-acceptance-engine, ticket-t-pm-integration-observability, ticket-t-pm-oplog-recovery, ticket-t-pm-runtime-ledger)
- `acceptance-05`: Acceptance Gate 05 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-acceptance-engine, ticket-t-pm-integration-observability, ticket-t-pm-oplog-recovery, ticket-t-pm-runtime-ledger)
- `acceptance-06`: Acceptance Gate 06 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-acceptance-engine, ticket-t-pm-integration-observability, ticket-t-pm-oplog-recovery, ticket-t-pm-runtime-ledger)
- `acceptance-07`: Acceptance Gate 07 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-acceptance-engine, ticket-t-pm-integration-observability, ticket-t-pm-oplog-recovery, ticket-t-pm-runtime-ledger)
- `acceptance-08`: Acceptance Gate 08 (waiting_on: ticket-t-plan-graph-sot, ticket-t-pm-acceptance-engine, ticket-t-pm-integration-observability, ticket-t-pm-oplog-recovery, ticket-t-pm-runtime-ledger)

## Acceptance Gates
- `acceptance-01` [pending]: 启动真实 dalek 服务，而不是只运行测试。
- `acceptance-02` [pending]: 在浏览器中打开 web 管理页面。
- `acceptance-03` [pending]: 看到概览页真实展示项目状态。
- `acceptance-04` [pending]: 进入 tickets 页面，确认 ticket 列表和详情可用。
- `acceptance-05` [pending]: 进入 planner/runtime 页面，确认 planner 状态真实展示。
- `acceptance-06` [pending]: 进入 merge / inbox 页面，确认对应数据真实展示。
- `acceptance-07` [pending]: 至少完成一个真实用户操作链路，并观察状态变化： 例如创建 ticket 后页面刷新可见； 或处理 merge/inbox 后页面状态变化可见。
- `acceptance-08` [pending]: 将验收过程记录到 acceptance evidence 中，包含： 使用的启动命令 访问 URL 关键页面截图或快照 关键操作步骤 最终结论

## Latest Evidence
- /Users/xiewanpeng/agi/dalek/.dalek/pm/acceptance.md

## Next PM Action
- 从 t24 开始推进任务运行态可信化，再依赖解锁后续 tickets
