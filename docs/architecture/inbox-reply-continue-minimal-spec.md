# Inbox Reply / Continue 最小规格

## 1. 目的

本规格定义 `needs_user` / `wait_user` 场景下，PM 如何把“用户回复”重新注入运行环境，并让 ticket 继续推进或直接收尾。

本规格只解决一个最小问题：

- worker 为什么被 block
- 用户现在回复了什么
- 回复里附带的资料在哪里看
- 当前动作是继续推进还是直接收尾

它不试图引入新的 artifact 系统，也不把 reply 设计成复杂 payload。

## 2. 结论

结论只有 5 条：

1. `reply` 就是一段原样文本，默认按 markdown 处理。
2. 唯一需要结构化的字段是 `action`，只支持：
   - `continue`
   - `done`
3. reply 里提到的文件路径，由用户自己决定写在哪里；系统不替用户重定位文件。
4. worker 恢复时，不读“回复对象模型”，只读 prompt 中的上下文摘要和 reply 原文。
5. 与 `focus batch` 保持一致：
   - 单 ticket 可以直接提交恢复执行
   - `focus batch` 不能绕过 controller，CLI/API 只能提交“人类已答复”的 intent，由 controller 决定恢复或收尾

## 3. 设计边界

### 3.1 范围

本规格覆盖：

- `worker report --next wait_user`
- `needs_user inbox`
- 用户通过 inbox 给出回复
- 通过 reply 恢复 blocked ticket
- 通过 reply 触发“直接收尾”
- `focus batch` 下 blocked item 的恢复

### 3.2 非目标

本规格不覆盖：

- 通用文件资产系统
- 长期保留的 artifact registry
- 多种复杂 payload 类型
- `/tmp` 之外的长期存储编排
- 把 reply 拆成 `text/file/kv/url` 独立命令

## 4. 与现有 Focus Batch 的一致性

本规格必须服从 [focus-run-batch-v1-lean-spec.md](/Users/xiewanpeng/agi/dalek/docs/architecture/focus-run-batch-v1-lean-spec.md)：

- `batch v1` 严格串行
- 同一时刻只有 controller 能推进 `focus_runs` / `focus_run_items`
- blocked item 未收口前，后续 item 不启动

因此：

- 单 ticket 模式下，`inbox continue` / `inbox done` 可以直接驱动 ticket 的下一步执行
- `focus batch` 模式下，`inbox continue` / `inbox done` 只能提交 intent，并唤醒 controller
- controller 消费 intent 后，才能把当前 blocked item 从 `blocked` 推到：
  - `queued/executing`，或
  - `completed`

这保证 batch 的“严格串行”语义不被 reply 命令绕过。

## 5. 用户接口

对 `needs_user` inbox，规范命令只有两类：

```bash
dalek inbox continue --id <inbox_id> --reply '...'
dalek inbox done --id <inbox_id> --reply '...'
```

语义：

- `continue`
  - 用户/PM 已补充信息
  - 需要 worker 再跑一轮
- `done`
  - 用户/PM 认为当前 ticket 不需要继续开发
  - 只需要做收尾校验、必要的最小修正，然后报 `done`

`inbox close` 仍可保留给一般性通知，但不再是 `needs_user` ticket 的主路径。

## 6. Reply 格式

### 6.1 基础格式

`reply` 是一整段原样文本。

系统不对正文做结构化解析，不区分“正文 / 文件 / kv / 说明”。

如果用户需要提供资料，直接在 reply 里写明：

- 为什么这份资料能解决当前 block
- 资料文件的实际路径是什么
- 如果有多个文件，各自用途是什么

示例：

````markdown
已补充配置，请继续。

配置文件在：
- /tmp/feishu-prod.json

部署脚本在：
- /Users/me/bin/deploy-prod.sh

如需环境变量说明，请看：
- /tmp/deploy-notes.md
````

系统不负责把这些文件复制到统一目录，也不强制用户把文件放到固定位置。

## 7. 运行环境注入

### 7.1 Prompt 必须包含的上下文

无论 `continue` 还是 `done`，恢复 prompt 都必须显式包含 4 组信息：

1. 当前为什么被 block
2. 用户现在回复了什么
3. 用户提到的补充资料在哪里
4. 当前动作是继续推进还是直接收尾

上下文来源必须尽量保守：

- 阻塞原因：只使用当前 open `needs_user inbox` 中已有的信息载荷
- v1 的最低要求：`title + body`
- 不要求额外回查 task run 的 `summary/blockers`
- 回复正文：`reply` 原文
- 资料路径：用户在 `reply` 中自己写出的实际路径

也就是说，worker 看到的 `<context>`，本质上只是“上一次 wait_user 时写进 inbox 的阻塞说明”，而不是 ticket 世界外部的控制面标识。

### 7.2 Verify 规则

worker 在真正继续推进前，必须先做一轮 verify：

1. `reply` 是否足以回答 inbox 中描述的阻塞问题
2. `reply` 中提到的资料路径是否真实存在
3. 这些路径是否可读
4. 如果当前 action 是 `done`，现有代码/状态是否真的足以收尾

若 verify 失败，worker 必须重新进入 `wait_user`，并明确告诉用户：

- 哪个文件不存在或不可读
- 它为什么是继续推进/收尾所必需的
- 建议用户把该资料放到一个稳定路径，例如 `/tmp/xxx.md` 后再回复

注意：

- `/tmp/xxx.md` 只是建议路径，不是强制路径
- worker 不得在 verify 失败时假装继续执行
- worker 不得自己脑补“用户大概想给的是哪个文件”

### 7.3 `wait_user` 循环控制

除了 worker prompt，还必须有一个外部 `wait_user controller`，负责防止：

- 用户回复不充分
- worker 再次 `wait_user`
- 系统继续自动恢复
- 最终在 `wait_user -> continue -> wait_user` 之间无限循环

最小规则：

1. `wait_user` 的计数作用域，不是全局 inbox，也不是整个 ticket 生命周期，而是“当前 ticket 当前这条 `wait_user` 链条”
2. 一条链条的推荐定义是：
   - `ticket_id`
   - `origin_task_run_id`
   这里的 `origin_task_run_id` 指“第一次把该 ticket 推入这条 `wait_user` 链条的那次 task run”
3. 对 focus/batch 来说，这条链条等价于“当前 focus item 的当前 `wait_user` 链条”
4. `round_count` 由外部 controller 维护，不由 worker loop 维护
5. `round_count` 可以实现为挂在当前 open `needs_user inbox` 上的控制字段，或挂在等价的 controller-owned 记录上；但它的语义始终是“链条级”，不是“全局 inbox 级”
6. 同一条未解决的 `needs_user` 链条，最多只允许发起 3 次 `wait_user` 请求
7. `round_count` 的定义非常简单：当前链条每发起一次新的 `wait_user` 请求，就 `+1`
8. 第一次把 ticket 推入当前这条 `wait_user` 链条时，第一次 `wait_user` 请求就使 `round_count = 1`
9. 用户执行 `inbox continue` / `inbox done`、controller 消费 reply、或恢复执行成功与否，都不会直接改变 `round_count`
10. 如果第 1、2 次 `wait_user` 请求后 worker 再次 `wait_user`：
   - 保持 ticket / focus item 为 `blocked`
   - 更新 inbox 中的阻塞说明
   - 允许用户继续下一轮回复
11. 如果当前链条已经累计到第 3 次 `wait_user` 请求：
   - 不再自动继续
   - ticket / focus item 保持 `blocked`
   - inbox 明确标记“已达 3 轮上限，需要 PM/用户手工处理”
   - focus/batch 流程停在当前 item，不再继续后续 item

注意：

- 这个上限由当前链条对应的外部 controller 执行，不由 worker 自己决定
- worker 仍然只负责如实报告 `wait_user`
- v1 默认上限为 `3`

## 8. Prompt 模板

### 8.1 `continue` 模板

```text
你正在恢复一个因需要人工输入而阻塞的 ticket。

<context>
你此前因需要人工输入而暂停。

以下内容来自当前 needs_user inbox 中记录的阻塞说明：

{inbox_title}

{inbox_body}

当前动作：continue
</context>

<reply>
{reply 原文}
</reply>

<check>
先校验 reply 是否足以解决当前 block。

校验规则：
- 检查 reply 是否真正回应了上面的阻塞说明
- 检查 reply 中提到的资料路径是否存在且可读
- 如果 reply 提到的文件不存在、不可读、或仍不足以解决 block：
  - 不要继续实现
  - 再次进入 wait_user
  - 明确告诉用户缺了哪个文件/哪条信息
  - 建议用户把资料放到稳定路径，例如 /tmp/xxx.md 后再回复

若校验通过：
- 只基于这些补充信息继续当前 ticket
- 不要重做无关探索
- 本轮结束时按真实状态上报 continue / done / wait_user
</check>
```

### 8.2 `done` 模板

```text
你正在恢复一个因需要人工输入而阻塞的 ticket。

<context>
你此前因需要人工输入而暂停。

以下内容来自当前 needs_user inbox 中记录的阻塞说明：

{inbox_title}

{inbox_body}

当前动作：done
</context>

<reply>
{reply 原文}
</reply>

<check>
先校验 reply 是否足以支持当前 ticket 直接收尾。

校验规则：
- 检查 reply 是否真正回答了上面的阻塞说明
- 检查 reply 中提到的资料路径是否存在且可读
- 检查当前代码/状态是否真的足以收尾
- 如果 reply 提到的文件不存在、不可读、或实际上仍不能直接结束：
  - 不要报 done
  - 再次进入 wait_user
  - 明确告诉用户缺了哪个文件/哪条信息
  - 建议用户把资料放到稳定路径，例如 /tmp/xxx.md 后再回复

若校验通过：
- 不要继续扩展实现
- 只做收尾校验与必要的最小修正
- 如果 closure 条件满足，则报 done
- 如果最终仍不能收尾，则报 wait_user，并明确说明为什么不能直接结束
</check>
```

关键点：

- `done` 不是直接改 ticket 字段
- `done` 仍然走一次最小收尾执行，让 worker 自己完成 closure

这样单 ticket 与 focus batch 共享同一套“run 是真相源”的原则。

## 9. 状态推进

### 9.1 单 Ticket 模式

`inbox continue`：

1. 校验 inbox 属于 `needs_user`
2. 用 `reply` 原文生成 `continue` prompt
3. 若当前 `wait_user` 链条的 `round_count >= 3`，拒绝自动继续，保持 `blocked`
4. 启动下一轮 worker run
5. 启动成功后关闭 inbox

`inbox done`：

1. 校验 inbox 属于 `needs_user`
2. 用 `reply` 原文生成 `done` prompt
3. 若当前 `wait_user` 链条的 `round_count >= 3`，拒绝自动继续，保持 `blocked`
4. 启动一轮“只收尾”的 worker run
5. 启动成功后关闭 inbox

### 9.2 Focus Batch 模式

`focus batch` 下，`inbox continue` / `inbox done` 不直接改：

- `focus_runs.status`
- `focus_run_items.status`

而是：

1. 追加一个 focus event，payload 至少包含：
   - `ticket_id`
   - `inbox_id`
   - `action`
   - `reply`
   - `reply_excerpt`
2. 唤醒 project/controller
3. controller 先检查当前 focus item 对应的 `wait_user` 链条是否已达到上限；若 `round_count >= 3`，则保持 blocked
4. controller 发现 blocked item 且存在新的 resolution intent 后：
   - `continue` -> queue/executing
   - `done` -> queue 一个 closeout-only run
5. controller 接管后，再关闭 inbox

这样能保证：

- batch 串行语义不被 CLI 绕过
- 当前 item 先收口，后续 item 才能继续

## 10. 审计要求

本规格不要求保存复杂 artifact 元数据，但必须保留最小审计：

- 谁触发了 `continue/done`
- 针对哪个 inbox / ticket / task_run
- reply 摘要

focus batch 下，这些信息优先进入 `focus_events.payload`。

## 11. 验收用例

### 11.1 单 ticket continue

场景：

- worker 因缺少配置报 `wait_user`
- 用户执行 `dalek inbox continue --reply '已补充配置 ...'`

预期：

- worker 下一轮 prompt 包含 `<context> / <reply> / <check>`
- `<context>` 只包含当前 inbox 里已有的阻塞说明，不依赖 ticket id / inbox id / task run 摘要
- worker 会先验证 reply 是否足以解决问题，以及其中提到的文件是否可读
- inbox 关闭
- ticket 从 `blocked` 回到执行态

### 11.2 单 ticket done

场景：

- worker 因需要人工确认而 block
- PM 认为无需继续实现，只需收尾

预期：

- worker 下一轮 prompt 包含 `<context> / <reply> / <check>` 和 `action=done`
- worker 只做最小收尾，不扩展实现
- 如果 closure 条件满足，ticket 正常进入 `done`

### 11.3 Focus batch continue

场景：

- focus item 因 `needs_user` blocked
- 用户给出 reply 并选择 `continue`

预期：

- CLI/API 不直接改 focus item 状态
- controller 消费 resolution intent 后恢复当前 item
- 后续 pending item 仍然等待，不提前启动
- 如果当前链条累计 3 次 `wait_user` 请求后仍未收口，focus 保持 blocked，不再自动推进

### 11.4 Focus batch done

场景：

- focus item 因 `needs_user` blocked
- PM 选择 `done`

预期：

- controller 为当前 item 安排一轮 closeout-only 执行
- 当前 item 收口后，focus 才能推进到下一个 item
- 如果当前链条累计到第 3 次 `wait_user` 请求后仍无法收尾，focus 停在当前 blocked item，等待人工处理

## 12. 取舍

本规格有意保持粗糙：

- 用原始 reply 文本，不做复杂 schema
- 只靠 `action=continue|done` 区分控制语义
- 文件路径由用户在 reply 里自己说明，系统不重定位

这是刻意选择，不是缺陷。

这里要解决的不是“回复资产管理”，而是“如何把人类补充信息以最小成本重新注入执行环境，并且不破坏 focus batch 的控制面一致性”。
