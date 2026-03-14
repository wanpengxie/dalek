# Focus Run Batch V1 修复规格

## 1. 目的

本文件不是新的主设计文档。

它的作用是对 [focus-run-batch-v1-lean-spec.md](/Users/xiewanpeng/agi/dalek/docs/architecture/focus-run-batch-v1-lean-spec.md) 做一次明确的修复收口：

- 记录当前实现相对 spec 的确认偏差
- 指定必须删除的旧设计残留
- 规定剩余实现的修复方式
- 给出真实场景下的验收标准

适用背景：

- `t110` 与 `t113` 已 merged
- 新的 `focus_controller.go` 是主推进路径
- 当前主要问题不在 controller 主骨架，而在外围契约、stop/cancel 收口、merge 成功判定、integration ticket 基线、以及旧 v0 残留清理

本文件的优先级高于旧实现习惯，但低于主设计：

- 主设计： [focus-run-batch-v1-lean-spec.md](/Users/xiewanpeng/agi/dalek/docs/architecture/focus-run-batch-v1-lean-spec.md)
- 修复增量：本文

## 2. 结论

结论很直接：

1. 新 controller 方向正确，不重写
2. 当前需要的是“修复 + 删除旧路径”的清理手术
3. 修复完成后，`batch focus v1` 仍保持原有边界：
   - daemon-owned
   - 严格串行
   - 无 daemon 时 `focus run` 不工作
   - 任意 merge conflict 一律 handoff 到 integration ticket

## 3. 当前确认问题

### 3.1 P0：integration ticket 的 worktree 基线没有真正绑定 `target_ref`

主设计要求：

- replacement integration ticket 必须从干净 `target_ref` 当前 HEAD 建立 worktree

当前问题：

- 创建 integration ticket 时只写了 `ticket.target_branch`
- 后续 focus/controller 提交 worker run 时，没有把这个 `target_ref` 作为 `BaseBranch` 继续传到 execution host
- execution host 虽然支持 `BaseBranch`，但当前提交链没有使用
- `RunTicketWorker` 在 `BaseBranch` 为空时，会退回 repo 当前分支或 `HEAD`

风险：

- replacement ticket 可能在错误基线上开发
- 真实 repo 下，这会直接破坏 integration ticket 的 git 合同

修复要求：

1. controller 在 adopt/restart/start replacement integration ticket 时，必须读取 replacement ticket 的 `target_branch`
2. `SubmitTicketLoop` / `StartTicket` 链路必须显式传入 `BaseBranch`
3. `BaseBranch` 必须使用规范化后的 `target_ref`
4. 没有有效 `target_ref` 时直接 fail/block，不允许静默退回当前分支

### 3.2 P0：`stop/cancel` 仍直接把 run 写成终态，绕过 controller

主设计要求：

- API handler 只写 `desired_state`
- controller 在步骤边界收口

当前问题：

- `queued/blocked` run 上执行 stop/cancel 时，会直接把 run 改成 `stopped/canceled`
- item 不会同步进入终态
- controller 看到 run 已 terminal 后，不再补 item 收口

风险：

- 出现 `run terminal + item non-terminal` 的控制面不一致
- handoff blocked item 可能永远失去收口机会

修复要求：

1. `FocusStop/FocusCancel` 只能写：
   - `desired_state`
   - `updated_at`
   - 审计事件
2. 禁止 API handler 直接改 `focus_runs.status`
3. run/item 的 terminal 收口只能由 controller 执行
4. `queued/blocked` item 也必须经过 controller 收口，不能 shortcut

### 3.3 P0：`FocusRun.RequestID` 被 stop/cancel 请求覆盖

主设计里：

- `FocusStart.request_id` 是 run 的启动幂等身份
- stop/cancel 是后续动作请求，不应覆盖 run 身份

当前问题：

- 更新 `desired_state` 时会把 `focus_runs.request_id` 改成 stop/cancel 的 request id

风险：

- run 的启动幂等身份被破坏
- 运维审计会混淆“创建请求”和“后续控制请求”

修复要求：

1. `focus_runs.request_id` 永远只表示 start request
2. stop/cancel request id 只能进入：
   - `focus_events.payload`
   - 或新增专用 action 字段
3. 禁止后续动作覆盖 run identity

### 3.4 P0：`cancel` 路径过于乐观，本地先 repair，再等待 host

主设计要求：

- `stop --force` 写 `desired_state=canceling`
- controller 负责取消当前执行
- 状态推进必须以真实执行收口为准

当前问题：

- controller 请求 host cancel 后，会先本地把 task run 标成 canceled
- 还会提前 `StopTicket` / repair ticket workflow
- host cancel 如果失败、延迟、或晚到，执行面与控制面会分叉

风险：

- task runtime、ticket workflow、focus item 三者状态不一致
- daemon 重启恢复时容易误判

修复要求：

1. `canceling` 只发取消意图，不做本地“成功假定”
2. controller 应先请求：
   - `CancelTicketLoop`
   - `CancelTaskRun`
3. 只有在执行面确认 terminal 后，才允许收口 item/run
4. 本地 repair 只能作为明确的恢复分支，不能作为正常 cancel 主路径

### 3.5 P0：merge 成功路径没有执行 repo root clean 验收

主设计要求：

- merge 成功不只看退出码
- 必须校验：
  - 无 unmerged files
  - `MERGE_HEAD` 不存在
  - repo root clean
  - 最终仍以 integration observe 作为 merged 真相

当前问题：

- 代码里实现了 `gitMergeClean()`
- 但 success path 没有用它作为推进门槛

风险：

- 脏 repo root 也可能被继续推进到 `awaiting_merge_observation`
- 导致“merge 看起来成功，root 实际不干净”

修复要求：

1. `git merge` 成功后必须立即执行 clean 验证
2. 验证失败时：
   - 不进入 `awaiting_merge_observation`
   - 当前 item 进入 `blocked(merge_failed)`
3. 只有 clean 验证通过，才允许 `SyncRef` 和后续 observe

### 3.6 P1：`CreateIntegrationTicket` 契约比 spec 弱

主设计要求：

- integration ticket 描述必须带够证据
- source ticket 与 `target_ref` 要校验一致性

当前问题：

- 现在只校验 source tickets 存在
- 不校验 source tickets 是否处于 `done + needs_merge`
- 不校验它们是否共享同一个 `target_ref`
- controller 调用时没传 `EvidenceRefs`

风险：

- 公共 facade 容易被误用
- integration ticket 描述退化成证据不足的“空票”

修复要求：

1. `CreateIntegrationTicket` 必须校验：
   - source tickets 存在
   - source tickets 处于 `done + needs_merge`
   - source tickets 的 `target_branch` 与输入 `target_ref` 一致
2. controller 调用时必须提供：
   - `source_anchor_shas`
   - `conflict_files`
   - `merge_summary`
   - `evidence_refs`
3. 若证据不足，允许 blocked，但不允许创建弱描述 ticket

### 3.7 P1：`readonly-stale` 降级输出不可信

主设计要求：

- daemon 不可达时，`show/status` 可本地只读降级
- 但必须 stale 且可信

当前问题：

- 现在降级读的是 legacy `ActiveFocusRun`
- 打印的 `CompletedCount / TotalCount / ActiveTicketID / Summary` 是 `gorm:"-"` 字段
- 这些不是 DB 真相

风险：

- 看似给了降级诊断，实际展示的是空壳字段

修复要求：

1. 删除 `printLegacyFocus`
2. 降级路径必须直接组装 `FocusRunView`
   - 读 `focus_runs`
   - 读 `focus_run_items`
   - 计算 `ActiveItem`
   - 读最新 `focus_event.id`
3. JSON/text 都必须显式标记 `readonly_stale=true`
4. 降级模式下禁止展示非持久化兼容字段

### 3.8 P1：旧 v0 focus 实现仍保留，形成第二套执行模型

当前残留包括：

- 旧本地 loop
- 旧 CRUD
- 旧 project facade
- 进程内 `focusCancelFn`
- legacy CLI 降级输出

风险：

- split-brain 虽然不再是主路径，但代码树里仍有第二套模型
- 后续回归时极易被重新接入

结论：

- 这是必须做的删除手术，不是技术债待办

## 4. 必须删除的旧设计

下表中的内容全部视为本次修复的必删项。

| 删除项 | 范围 | 删除原因 |
| --- | --- | --- |
| `focus_batch.go` 整个文件 | [focus_batch.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/focus_batch.go) | 旧本地 loop，带旧 merge / conflict 自动解 / `markTicketIntegrationMerged` 语义 |
| `focus.go` 中 legacy CRUD | [focus.go:19](/Users/xiewanpeng/agi/dalek/internal/services/pm/focus.go#L19) [focus.go:69](/Users/xiewanpeng/agi/dalek/internal/services/pm/focus.go#L69) [focus.go:86](/Users/xiewanpeng/agi/dalek/internal/services/pm/focus.go#L86) | 旧 CRUD 不创建 items，stop 依赖进程内 cancelFn |
| `project_manager.go` 中 legacy focus facade | [project_manager.go:66](/Users/xiewanpeng/agi/dalek/internal/app/project_manager.go#L66) [project_manager.go:73](/Users/xiewanpeng/agi/dalek/internal/app/project_manager.go#L73) [project_manager.go:80](/Users/xiewanpeng/agi/dalek/internal/app/project_manager.go#L80) [project_manager.go:101](/Users/xiewanpeng/agi/dalek/internal/app/project_manager.go#L101) | 继续向上暴露旧接口 |
| `service.go` 中 `focusCancelMu / focusCancelFn` | [service.go:46](/Users/xiewanpeng/agi/dalek/internal/services/pm/service.go#L46) | 进程内 stop 机制，违背 daemon-owned |
| `cmd_manager_focus.go` 中 `printLegacyFocus` | [cmd_manager_focus.go:262](/Users/xiewanpeng/agi/dalek/cmd/dalek/cmd_manager_focus.go#L262) | 读取 `gorm:\"-\"` 字段，显示结果不可信 |

说明：

- 新 controller 本身不删
- `focus_run` / `focus_run_items` / `focus_events` 持久化模型不回退

## 5. 修复后的实现规格

### 5.1 controller 保持为唯一推进者

保留现有原则：

- daemon 内 `FocusController` 是唯一推进者
- API handler 只能：
  - 创建 run
  - 写 `desired_state`
  - 记事件
  - `NotifyProject`

不得新增第二套推进 loop。

### 5.2 start/restart/adopt 必须显式传递 `BaseBranch`

修复后的合同：

1. 普通 ticket：
   - `BaseBranch` 可以为空
   - 维持现有 worker start 语义
2. integration ticket：
   - `BaseBranch` 必须等于其 `target_branch`
   - controller 提交 execution host 前必须显式传入
3. execution host 若收到 integration ticket 且 `BaseBranch` 为空，必须返回错误

建议实现点：

- 在 controller 中增加“为当前 item 计算 base branch”的逻辑
- 对 label=`integration` 的 ticket 读取并规范化 `target_branch`
- 贯穿：
  - controller
  - focus submitter
  - daemon manager component
  - execution host
  - worker start

### 5.3 stop/cancel 只写意图，不写终态

修复后的 stop/cancel 合同：

1. `FocusStop`
   - 只写 `desired_state=stopping`
   - 追加 `run.desired_state_changed`
2. `FocusCancel`
   - 只写 `desired_state=canceling`
   - 追加 `run.desired_state_changed`
3. 不允许 API handler 修改：
   - `focus_runs.status`
   - `focus_run_items.status`
   - `focus_runs.finished_at`
4. controller 在 item 边界收口：
   - pending/queued/blocked -> `stopped/canceled`
   - executing -> 等 host 侧 cancel 真实收口
   - merging -> `git merge --abort` 后再收口

### 5.4 cancel 的执行面收口合同

修复后的 force cancel 路径：

1. controller 检测到 `desired_state=canceling`
2. 向 host 请求：
   - `CancelTicketLoop`
   - `CancelTaskRun`
3. 等执行面进入 terminal
4. 再决定是否需要 workflow repair
5. 最后由 controller 统一 terminalize item/run

禁止行为：

- 先本地把 task run 标 canceled
- 先把 ticket repair 回 backlog
- 以“请求已发送”冒充“取消已完成”

### 5.5 merge success 必须做 clean gate

修复后的 success path：

1. `git merge`
2. 若退出码非 0：
   - conflict -> handoff 或 blocked
   - 非 conflict -> `blocked(merge_failed)`
3. 若退出码为 0：
   - 调 `gitMergeClean()`
   - 只有 clean gate 通过，才允许 `SyncRef`
   - clean gate 失败则 `blocked(merge_failed)`
4. `merged` 真相仍只来自 integration observe

### 5.6 integration ticket 创建必须强校验

修复后的 `CreateIntegrationTicket` 契约：

1. 输入 source tickets 必须全部：
   - 存在
   - `workflow_status=done`
   - `integration_status=needs_merge`
2. 输入 `target_ref` 必须与 source tickets 的 `target_branch` 一致
3. 描述必须包含：
   - source tickets
   - conflict target head sha
   - source anchors
   - conflict files
   - merge summary
   - docs/evidence refs
4. 若当前票已经是 integration ticket，controller 不再调用创建接口，而是直接 `blocked(handoff_recursion_requires_user)`

### 5.7 `readonly-stale` 统一走新 view

修复后的 CLI 降级规则：

1. 先尝试 daemon `FocusGet`
2. daemon 不可达时：
   - 本地查询 active `focus_runs`
   - 本地查询对应 `focus_run_items`
   - 本地查询 `latest_event_id`
   - 组装 `FocusRunView`
3. 输出统一走 `printFocusView`
4. 删除 `printLegacyFocus`

### 5.8 事件集要么补齐，要么删掉未实现项

当前事件常量中，以下事件需要做一次一致性清理：

- `item.accepted`
- `recovery.resumed`

修复原则：

1. 若该事件是 spec 必需事件，则补写入路径和测试
2. 若当前 v1 不需要，则从最小事件集与常量里删掉

禁止继续保留“文档里有、代码里永远不会写”的伪事件。

## 6. 建议实施顺序

### 6.1 第一阶段：修 correctness

先修会破坏真实运行正确性的点：

1. `BaseBranch/target_ref` 贯穿传递
2. stop/cancel 不再直接 terminalize
3. stop/cancel 不再覆盖 run `request_id`
4. cancel 路径移除乐观本地 repair
5. merge success clean gate

### 6.2 第二阶段：修契约和诊断

1. `CreateIntegrationTicket` 强校验与证据完整性
2. `readonly-stale` 走新 `FocusRunView`
3. 事件集清理

### 6.3 第三阶段：做删除手术

1. 删 `focus_batch.go`
2. 删 `focus.go` legacy CRUD
3. 删 `project_manager.go` legacy facade
4. 删 `service.go` 中 `focusCancelFn`
5. 删 `printLegacyFocus`

删除顺序要求：

- 先保证主路径修好
- 再删 legacy
- 禁止先删再修，导致短时间无可用控制面

## 7. 验收标准

### 7.1 daemon / CLI 验收

1. 无 daemon 时：
   - `manager run --mode batch` 失败
   - `manager stop` 失败
   - `manager tail` 失败
2. daemon 不可达时：
   - `manager show` 能返回 `readonly_stale=true`
   - 输出来自新 `FocusRunView`
   - 不展示 legacy 派生空字段

### 7.2 严格串行验收

1. `t1` 自己 `merged` 前，`t2` 不启动
2. `t1` handoff 后，replacement 未 merged 前，`t2` 不启动
3. replacement merged 且 source ticket superseded 收口后，`t2` 才启动

### 7.3 integration ticket 基线验收

真实 repo case：

1. 让 repo 当前分支故意不是 replacement ticket 的 `target_ref`
2. 触发 handoff 创建 integration ticket
3. 启动 replacement ticket worker
4. 验证其 worktree base 必须来自 `target_ref`
5. 若未传 `BaseBranch`，测试必须失败

### 7.4 stop/cancel 一致性验收

1. `queued` 状态 stop：
   - run 先只改 `desired_state`
   - controller 收口后 run/item 一起终态
2. `blocked` 状态 stop：
   - 不允许只停 run、不停 item
3. `executing` 状态 cancel：
   - host cancel 成功后再 terminalize
   - 不允许先本地标 canceled
4. daemon 重启后：
   - 已持久化的 `stopping/canceling` 仍能继续收口

### 7.5 merge clean gate 验收

1. repo root 预先有脏文件时，merge success path 必须 blocked，不能继续 observe
2. `MERGE_HEAD` 残留时，不能进入 `awaiting_merge_observation`
3. clean gate 通过时，才允许 `SyncRef`

### 7.6 integration ticket 契约验收

1. source ticket 不是 `done + needs_merge` 时，禁止创建 integration ticket
2. `target_ref` 与 source ticket 不一致时，禁止创建
3. 创建成功时 description 必须包含：
   - source tickets
   - target ref
   - conflict head sha
   - conflict files
   - merge summary
   - docs/evidence refs

### 7.7 删除手术验收

以下内容必须不再存在生产路径：

1. `focus_batch.go`
2. `focus.go` 的 legacy CRUD
3. `project_manager.go` 的 legacy focus facade
4. `service.go` 中 `focusCancelFn`
5. `printLegacyFocus`

验收方式：

- 代码删除
- 无生产调用路径
- 测试和编译通过

## 8. 非目标

本修复规格不做以下扩张：

- 不新增 attempt 历史表
- 不引入自动冲突解决
- 不引入第二层 integration ticket 自动递归
- 不把 focus 演化成通用 orchestrator

## 9. 最终标准

修复完成后，`batch focus v1` 必须满足这句话：

**它是一个 daemon-owned、严格串行、可恢复、可审计的最小批处理协调器；它没有旧本地 loop，没有伪降级输出，没有错误的 integration 基线，也没有绕过 controller 的 stop/cancel shortcut。**
