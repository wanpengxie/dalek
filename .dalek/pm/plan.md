# PM 控制面 — 自动开发模式

开始：2026-03-07 | 分支：feature/auto-dev-mode | 状态：active

## 目标

建立最小可行的 PM 控制面（planner loop），使 dalek 从"ticket autopilot"进化为"PM autopilot"。
系统事件发生后能自动唤起 PM 做一轮规划和决策，而不是生成 inbox 后等人消费。

## 成功标准

完整闭环验证：worker done → 系统标脏 → manager tick 调度 planner run → PM 读取状态 → 执行决策（关闭 inbox / 推进 merge / 创建下一 ticket）→ plan.md 更新。

## 约束

- 不引入常驻 PM 进程，planner run 是短生命周期 task run
- 不替换 manager tick，只在末尾追加 maybeSchedulePlannerRun
- 同一项目同时最多一个 planner run
- 每个 ticket 必须有单元测试覆盖核心逻辑
- 所有状态变更可在 task/inbox/event 中审计

---

## 功能列表

### F0: 底层执行稳定性 [done]

dispatch handoff 权限问题修复。

commits:
- `83d7e93` Refactor agent permission config handling
- `1031385` fix(sdkrunner): use QueryStream for Claude SDK with CanUseTool callback

验收：E2E 全链路（create → start → dispatch → worker dev → report done → merge proposed）无阻塞通过。
结果：2026-03-07 验证通过。

---

### F1: PMState 扩展 + pm_planner_run 实体 [active]

**目标**：为 planner loop 提供数据基础。

**scope**：
1. PMState 新增字段：planner_dirty, planner_wake_version, planner_active_task_run_id, planner_cooldown_until, planner_last_error, planner_last_run_at
2. DB auto-migrate 覆盖新字段
3. TaskType 新增 `pm_planner_run`
4. `dalek manager status` 输出 planner 状态字段

**验收标准（全部必须通过）**：
- [ ] `PMState` 结构体包含所有 planner 字段，GORM tag 正确
- [ ] `getOrInitPMState` 兼容新字段的零值初始化
- [ ] `TaskTypePMPlannerRun` 常量存在且可被 task ls 过滤
- [ ] `dalek manager status` 输出包含 planner_dirty / planner_active_task_run_id
- [ ] 单元测试：PMState 新字段读写、零值兼容
- [ ] `go test ./...` 全量通过
- [ ] git commit 包含所有变更，无遗漏文件

ticket: 待创建
commits: 无

---

### F2: ManagerTick 增加 maybeSchedulePlannerRun + 事件标脏 [pending]

**目标**：在 tick 末尾自动调度 planner run。

**scope**：
1. manager tick 末尾追加 `maybeSchedulePlannerRun` 步骤
2. 判断条件：planner_dirty=true && 无活跃 run && 不在 cooldown && autopilot enabled
3. 事件标脏：inbox 新增 / ticket done|blocked / merge proposed → planner_dirty=true
4. 防抖：debounce window + cooldown on noop
5. wake_version 递增与 run 后回检

**验收标准（全部必须通过）**：
- [ ] `ManagerTick` 返回 `ManagerTickResult` 中包含 planner 调度信息
- [ ] 当 planner_dirty=true 且满足条件时，创建 pm_planner_run task run
- [ ] 当已有活跃 planner run 时，不创建第二个
- [ ] cooldown 期间不调度
- [ ] inbox upsert / ticket workflow change / merge propose 正确标脏
- [ ] 单元测试覆盖：标脏、调度、防重、cooldown、wake_version 回检
- [ ] `go test ./...` 全量通过
- [ ] git commit 包含所有变更

ticket: 待创建（依赖 F1）
commits: 无

---

### F3: PM planner run 执行宿主 [pending]

**目标**：planner run 作为真实 agent 执行一轮 PM 决策。

**scope**：
1. planner run 接入 execution host（复用 SDK executor）
2. planner run prompt：读 plan.md + ticket ls + inbox ls + merge ls + 触发原因
3. planner run 完成后：更新 PMState（清 dirty / 写 last_run_at / 清 active_run_id）
4. planner run 失败/超时：写 last_error、保留 dirty、由下一轮 tick 恢复
5. planner run settled → NotifyProject 触发下一轮 tick

**验收标准（全部必须通过）**：
- [ ] execution host 能启动 pm_planner_run 类型的 task run
- [ ] planner run prompt 包含 plan.md + runtime facts
- [ ] planner run 完成后 PMState 正确更新
- [ ] planner run 失败后 PMState.planner_last_error 有值，dirty 保留
- [ ] run settled 触发 NotifyProject
- [ ] `dalek task ls` 可看到 pm_planner_run 类型的 task
- [ ] 单元测试覆盖：正常完成、失败恢复、settled 通知
- [ ] `go test ./...` 全量通过
- [ ] git commit 包含所有变更

ticket: 待创建（依赖 F1, F2）
commits: 无

---

### F4: E2E 验证 — planner loop 闭环 [pending]

**目标**：端到端验证自动开发模式。

**验收标准（全部必须通过）**：
- [ ] 创建测试 repo，dispatch ticket，worker 完成并 report done
- [ ] 系统自动标脏 → manager tick 调度 planner run
- [ ] planner run 读取状态、更新 plan.md、推进 merge 或创建下一 ticket
- [ ] 无人工干预，从 ticket done 到 planner 响应全自动
- [ ] 全流程在 10 分钟内完成

ticket: 无（PM 直接执行验证）
commits: 无

---

## 进度跟踪

| Feature | Ticket | 状态 | Commits | 验收 |
|---------|--------|------|---------|------|
| F0 | - | done | 83d7e93, 1031385 | E2E 通过 |
| F1 | 待创建 | active | - | 0/7 |
| F2 | 待创建 | pending | - | 0/8 |
| F3 | 待创建 | pending | - | 0/9 |
| F4 | - | pending | - | 0/5 |

## 决策记录

### 03-07: dispatch 权限问题根因

- 问题：Claude SDK 的 `WithCanUseTool` 回调要求 `QueryStream` 而非 `Query`
- 之前三轮 E2E 演练中误归因为"复合命令审批策略"，实际是 SDK API 不兼容
- 修复：`runClaude` 改用 `QueryStream` + input channel 发送 prompt
- 验证：E2E 全链路通过，dispatch handoff 无阻塞

### 03-07: 阶段 2 拆分策略

- F1/F2/F3 串行依赖，但各自有独立的验收标准
- 每个 feature 完成后必须跑 `go test ./...` 全量通过再提交
- F4 作为集成验收，由 PM 直接执行，不走 ticket
