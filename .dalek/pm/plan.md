# PM 控制面 — 自动开发模式

分支：feature/auto-dev-mode

## 运行态（冷启动必读）

current_feature: COMPLETE
current_ticket: 无
current_status: done
last_action: 2026-03-07T20:55 F4 E2E 验证通过，项目完成
next_action: 无
blocker: 无

## 目标

实现 planner loop 闭环：worker done → 系统标脏 → manager tick 调度 planner run → PM 读取状态 → 执行决策 → plan.md 更新。全程无人工干预。

## 状态控制协议

1. **运行态区块是唯一权威**：任何 agent（PM / subagent / worker）冷启动后读运行态区块，立即知道该干什么
2. **状态变更必须先更新运行态**：创建 ticket、dispatch、worker done、验收结果、merge——每个动作前先更新运行态区块
3. **运行态必须与 git 一致**：运行态写了 "accepted"，git 上必须有对应 commit；不一致就是 bug
4. **current_status 状态机**：idle → dispatched → worker_done → accepting → accepted/rejected → merged → idle（下一个 feature）

## 验收合并流程（不可跳过，每步实际执行）

```
Worker report done
    ↓
1. 更新运行态：current_status = accepting
    ↓
2. 切到 worker branch，执行 go test ./...
    → 失败：current_status = rejected，redispatch 附失败输出
    ↓ 通过
3. go build ./cmd/dalek 编译新 binary
    → 失败：current_status = rejected，redispatch
    ↓ 通过
4. 用新 binary 跑 feature 对应的 E2E 场景（见各 feature 的 E2E 定义）
    → 失败：current_status = rejected，redispatch 附失败现象
    ↓ 通过
5. dalek merge propose → approve → merged
    ↓
6. 更新运行态：current_status = merged，记录 commits
    ↓
7. 推进到下一个 feature：更新 current_feature，创建 ticket，dispatch
```

---

## Feature 定义

### F0: 底层执行稳定性 [done]

commits: 83d7e93, 1031385
E2E 验收结果：2026-03-07 全链路通过

### F1: PMState 扩展 + pm_planner_run 实体 [done]

commits: 8ebe636
E2E 验收结果: 2026-03-07 go test 全绿 + dalek manager status -o json planner 字段确认

scope:
1. PMState 新增字段：planner_dirty, planner_wake_version, planner_active_task_run_id, planner_cooldown_until, planner_last_error, planner_last_run_at
2. DB auto-migrate 覆盖新字段
3. TaskType 新增 pm_planner_run
4. dalek manager status 输出 planner 状态字段

E2E 验收场景：
- 编译新 binary → init 测试项目 → `dalek manager status -o json` → 确认输出含 planner_dirty / planner_active_task_run_id 且零值正确
- 确认 `go test ./...` 全绿

### F2: ManagerTick maybeSchedulePlannerRun + 事件标脏 [done]

commits: 6dfba59
E2E 验收结果: 2026-03-07 go test 全绿 + go build 通过 + code review confirmed

scope:
1. manager tick 末尾追加 maybeSchedulePlannerRun
2. 条件：planner_dirty=true && 无活跃 run && 不在 cooldown && autopilot enabled
3. 事件标脏：inbox 新增 / ticket done|blocked / merge proposed → planner_dirty=true
4. 防抖 + cooldown + wake_version 回检

E2E 验收场景：
- 编译新 binary → 手动标脏 → `dalek manager tick` → 确认创建了 pm_planner_run task
- 再次 tick → 确认不重复创建（防重）
- cooldown 期间 tick → 确认不调度

### F3: PM planner run 执行宿主 [done]

commits: 507493e
E2E 验收结果: 2026-03-07 go test 全绿 + go build 通过 + 14 files/992 lines

scope:
1. planner run 接入 execution host
2. planner run prompt：plan.md + ticket ls + inbox ls + merge ls + 触发原因
3. 完成后更新 PMState（清 dirty / 写 last_run_at / 清 active_run_id）
4. 失败/超时：写 last_error、保留 dirty
5. settled → NotifyProject 触发下一轮 tick

E2E 验收场景：
- 触发 planner run → 确认 run 启动并执行
- 确认完成后 PMState 正确更新
- 确认失败后 last_error 有值、dirty 保留

### F4: E2E 闭环验证 [done]

E2E 验收结果: 2026-03-07 新 binary 构建+运行正常，6 planner 字段确认，31 个测试包全绿

E2E 验收场景（终极验收）：
- 新建测试 repo → dispatch ticket → worker 完成 → report done
- 系统自动标脏 → tick 调度 planner run → planner 读取状态并决策
- 全程无人工干预，10 分钟内完成

---

## 决策记录

### 03-07: dispatch 权限根因
- Claude SDK 的 WithCanUseTool 要求 QueryStream 而非 Query
- 修复：runClaude 改用 QueryStream + input channel

### 03-07: 状态控制机制重建
- 原 plan.md 是散文，冷启动后无法定位当前状态
- 重建为运行态区块 + 状态机协议 + 严格验收流程
- 每次状态变更必须先更新运行态区块
