# Inbox Reply / Continue 修复规格

## 1. 背景

`ticket 119` 已将 `inbox continue` / `inbox done` 的最小链路并入主线，但当前实现与
[inbox-reply-continue-minimal-spec.md](/Users/xiewanpeng/agi/dalek/docs/architecture/inbox-reply-continue-minimal-spec.md)
仍有几处关键偏差。

本规格的目的不是重新设计整条链路，而是对已合入实现做一次收口修复，使其重新满足最小规格。

## 2. 当前已确认的问题

### 2.1 Focus batch 回复路径绕过 controller

当前 `ReplyInboxItem` 在识别到 ticket 属于当前 focus active item 后，会直接：

- 启动 ticket
- 提交 worker run
- 修改 focus run / focus item 状态
- 关闭 inbox

这违背了 batch v1 的控制面约束：

- CLI/API 只能提交 reply intent
- controller 才能决定何时恢复 blocked item
- blocked item 未收口前，后续 item 不得被任何旁路推进

### 2.2 Focus 审计 payload 不完整

当前 focus resume 事件没有保存最小审计字段：

- `inbox_id`
- `action`
- `reply`
- `reply_excerpt`

这不满足最小规格对审计的要求。

### 2.3 单 ticket 路径过早关闭 inbox

当前单 ticket 路径会先关闭 inbox，再启动恢复执行。

这会产生中间态风险：

- 启动 worker 失败时，inbox 已被提前消费
- reopen 只是 best-effort 补偿
- 进程在消费 inbox 后崩溃时，状态不一致

### 2.4 Prompt 缺少显式动作声明

当前 prompt 已包含 `<context> / <reply> / <check>`，但 `<context>` 中没有显式写出：

- `当前动作：continue`
- `当前动作：done`

这会把动作语义降级为靠模型从上下文推断。

### 2.5 三轮上限后缺少显式人工处理标记

当前实现到达 `wait_user` 三轮上限后，只会停止自动恢复，但不会明确标记：

- 当前链路已达上限
- 需要 PM / 用户手工处理

这不满足最小规格对人工接管提示的要求。

### 2.6 duplicate needs_user inbox 的链条归属不够严谨

当前 duplicate inbox 在被关闭时，没有同步写入 `chain_resolved_at`。

与此同时，active chain 的加载条件又主要依赖 `chain_resolved_at IS NULL`。

这意味着如果历史上出现重复 inbox，后续链条选择可能误把已关闭副本当作“仍活跃的链条候选”。

## 3. 修复目标

本次修复必须满足以下目标：

1. `focus batch` 下，`inbox continue/done` 只写 reply intent，不直接恢复执行。
2. controller 成为 focus blocked item 恢复的唯一执行入口。
3. 单 ticket 路径只有在恢复 run 成功提交后，才允许消费 inbox。
4. 恢复 prompt 的 `<context>` 中必须显式包含当前动作。
5. `wait_user` 三轮上限后，inbox / focus 必须明确显示“需要人工处理”。
6. needs_user 链条选择与 duplicate inbox 关闭语义必须一致，不允许已关闭副本污染 active chain 判定。

## 4. 非目标

本规格不做以下扩展：

- 不引入新的 reply artifact 系统
- 不把 reply 拆成结构化多字段模型
- 不扩展 `action` 到 `continue|done` 之外
- 不重做 focus batch 的整体控制面架构

## 5. 目标行为

### 5.1 单 ticket 模式

`dalek inbox continue --id <id> --reply '...'`
或
`dalek inbox done --id <id> --reply '...'`
的标准流程应为：

1. 校验 inbox 为 `open + needs_user`
2. 校验当前链路 `wait_round_count < 3`
3. 写入 reply intent
4. 生成恢复 prompt
5. 启动 ticket / 提交 worker run
6. 仅当提交成功后，消费 inbox
7. 若启动或提交失败：
   - inbox 保持 open
   - reply intent 可保留或按实现需要回滚
   - 但不能出现“inbox 已 done、run 未启动”的状态

### 5.2 Focus batch 模式

focus batch 下，CLI/API 只能做：

1. 校验 inbox 为 `open + needs_user`
2. 校验当前 blocked item 就是该 ticket
3. 校验当前链路 `wait_round_count < 3`
4. 写入 reply intent
5. 追加 focus event，payload 至少包含：
   - `ticket_id`
   - `inbox_id`
   - `action`
   - `reply`
   - `reply_excerpt`
6. 唤醒 controller
7. 立刻返回 accepted

CLI/API 不得直接：

- 启动 ticket
- 提交 worker run
- 修改 focus run 状态
- 修改 focus item 状态
- 消费 inbox

这些动作只能由 controller 执行。

### 5.3 Controller 恢复 blocked item

controller 在发现 blocked item 存在未消费的 reply intent 后：

1. 再次检查该 item 仍是当前 active blocked item
2. 再次检查当前链路未达 3 轮上限
3. 生成恢复 prompt
4. 启动 ticket / 提交 worker run
5. 成功后：
   - 将 item 推进到 `executing`
   - 将 run 推进到 `running`
   - 消费 inbox
6. 失败后：
   - 保持 item / run 为 blocked
   - inbox 保持 open
   - 写入失败原因，供后续人工排查

### 5.4 Prompt 模板要求

恢复 prompt 仍使用 `<context> / <reply> / <check>` 三段。

`<context>` 中必须显式包含：

- 你此前因需要人工输入而暂停。
- 以下内容来自当前 needs_user inbox 中记录的阻塞说明：
- inbox title
- inbox body
- `当前动作：continue` 或 `当前动作：done`

`done` 语义仍然是：

- 发起一轮 closeout-only 执行
- 不允许直接改 ticket 字段
- 是否真正 `done` 仍由 worker 在本轮执行后上报

### 5.5 三轮上限

当同一条链路累计到第 3 次 `wait_user` 请求后：

- 系统不得再自动恢复
- ticket / focus item 保持 blocked
- inbox 中必须明确体现：
  - 已达到 3 轮上限
  - 需要 PM / 用户手工处理

该提示至少要在用户可见字段中出现，不能只体现在日志或内部错误字符串里。

### 5.6 Duplicate inbox 语义

系统必须保证以下任一实现成立，且语义一致：

方案 A：

- active chain loader 只认 `status = open`
- duplicate 关闭后无需再参与 chain 选择

方案 B：

- duplicate 关闭时同步写 `chain_resolved_at`
- active chain loader 继续以 `chain_resolved_at IS NULL` 为主条件

无论采用哪种方案，都必须保证：

- 已关闭 duplicate 不能再被当作 active chain
- 当前链条的 `origin_task_run_id/current_task_run_id/wait_round_count` 不被旧副本污染

## 6. 数据与审计要求

最小审计要求如下：

- 单 ticket：
  - 记录谁对哪个 inbox 提交了何种 action
  - 记录恢复 run 是否成功提交
- focus batch：
  - focus event payload 至少包含 `ticket_id/inbox_id/action/reply/reply_excerpt`
  - controller 消费 reply 时应留下可回放痕迹

## 7. 验收

### 7.1 单 ticket continue

预期：

- run 成功提交后 inbox 才关闭
- prompt 的 `<context>` 含 `当前动作：continue`
- 若启动失败，inbox 保持 open

### 7.2 单 ticket done

预期：

- prompt 的 `<context>` 含 `当前动作：done`
- worker 收到的是 closeout-only 语义
- 未成功提交 run 前，不关闭 inbox

### 7.3 Focus batch continue/done

预期：

- CLI/API 只写 intent，不直接改 focus run/item 状态
- controller 才是恢复 blocked item 的唯一入口
- focus event payload 含 `ticket_id/inbox_id/action/reply/reply_excerpt`
- 当前 blocked item 未收口前，后续 item 仍保持 pending

### 7.4 三轮上限

预期：

- 第 3 次 wait_user 后再次回复时，系统拒绝自动恢复
- inbox 或 focus 上存在明确“需要人工处理”的可见提示

### 7.5 Duplicate inbox

预期：

- 构造 duplicate inbox 后，active chain 选择仍稳定指向当前有效链条
- 已关闭 duplicate 不会影响后续轮次统计与恢复判定

## 8. 交付要求

本修复至少需要包含：

- 代码修正
- 单元测试 / 集成测试补齐
- 对 `ticket 119` 已知偏差的回归覆盖

完成标准是：

- 行为重新满足最小规格
- 关键偏差被测试锁住，避免再次回归
