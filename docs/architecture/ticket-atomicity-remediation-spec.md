# Ticket 原子性修复规格

## 1. 目的

本文件不是新的 ticket 主设计文档。

它的目标是：

- 在 **不重做现有命令面** 的前提下，修复 ticket 被复合化的问题
- 不先引入新的 `ref-op` 对象
- 保留当前 `ticket / merge / worker / focus` 体系
- 通过 **增加约束、禁止违规、给出明确提示** 的方式，把 ticket 收敛回“原子任务”

本文件优先级：

- 低于全局 kernel 约束
- 高于当前实现习惯
- 作为 [ticket-lifecycle-merge-redesign.md](/Users/xiewanpeng/agi/dalek/docs/architecture/ticket-lifecycle-merge-redesign.md) 与 [merge-command-redesign.md](/Users/xiewanpeng/agi/dalek/docs/architecture/merge-command-redesign.md) 的修复增量

## 2. 结论

结论直接写清楚：

1. ticket 是 **单 ref 的原子交付单元**
2. 一张 ticket 只能有一个 git 合同，这个合同就是 `target_ref`
3. ticket 的 worktree base 必须等于 `target_ref`
4. ticket 的最终交付也必须回到 `target_ref`
5. 任何跨 ref 过程，不塞进单 ticket 语义里
6. 现有命令可以保留，但凡会破坏上述合同的用法，必须被系统拒绝

换句话说：

- 不需要现在就定义 `ref-op`
- 跨 ref 能力先由人工直接做 git 操作
- 系统只需要确保：**单张 ticket 不再偷偷承担跨 ref 编排**

## 3. 核心原则

### 3.1 ticket 的唯一 git 合同

对当前系统，ticket 的唯一 git 合同是：

- `target_branch`（语义上等同 `target_ref`）

它表达的是：

- 这张 ticket 从哪个 ref 起步
- 这张 ticket 最终交付到哪个 ref
- `integration_status` 和 `merge observe` 相对于哪个 ref 成立

因此：

- `target_ref` 不是可随意修改的“目标偏好”
- 它是 ticket 原子性的边界

### 3.2 `base_ref` 不是独立业务概念

当前实现中存在 `BaseBranch` / `--base` 这样的执行参数。

本规格收敛它的语义：

- `base_ref` 不是 ticket 自己的第二个 git 真相
- 它只是 ticket.git 合同在执行时的落实
- 所以必须满足：
  - `base_ref == target_ref`

一旦不相等，就不是“灵活”，而是在把单张 ticket 复合化。

### 3.3 跨 ref 过程不是 ticket 内部语义

像下面这种链路：

- `main -> dev_branch -> ticket -> main`

不是一张 ticket 的内部语义，而是多个原子动作的组合。

在当前阶段，这类组合可以先手工完成：

1. 手工同步 `dev_branch`
2. 创建一张 `target_ref=dev_branch` 的 ticket
3. 手工把结果集成回 `main`

如果第 3 步发生冲突：

- `abort merge`
- 创建一张新的 `integration ticket`
- 这张新票自己的 `target_ref` 是 `main`

因此：

- feature ticket 仍是原子 ticket
- integration ticket 仍是原子 ticket
- backport ticket 仍是原子 ticket
- 复杂流程来自组合，不来自单 ticket 内部复合

## 4. 当前确认的原子性破坏点

### 4.1 `ticket start --base`

当前问题：

- `ticket start` 暴露了 `--base`
- 当 `target_ref` 已冻结后，这个参数仍可能试图改写 worktree 基线

这会导致：

- ticket 的执行基线依赖“启动时输入”
- 而不是依赖 ticket 自己

本质上是在把单张 ticket 变成“可重新解释的容器”。

### 4.2 `merge retarget`

当前问题：

- `merge retarget` 允许在 `done + needs_merge` 后修改 `target_ref`

这会导致：

- ticket 做完之后再修改“最终回哪里去”
- 破坏 done 后冻结的交付合同

这等价于说：

- ticket 的 git 合同不是在创建/启动时建立
- 而是允许在交付阶段后置改写

这与“ticket 是原子交付单元”冲突。

### 4.3 `target_ref` 建立时机不稳定

当前问题：

- `target_ref` 有时在 `ticket create` 时建立
- 有时在 first `ticket start` 时兜底冻结
- 在错误实现里，甚至可能继续被 repo 当前分支影响

这会导致：

- ticket 合同建立时机不稳定
- 不同操作时机可能得到不同合同

### 4.4 worktree base 回退到当前 branch / HEAD

当前问题：

- 当 `BaseBranch` 为空时，worker start 会退回当前分支或 `HEAD`

这意味着：

- ticket 的基线可能被“当前仓库恰好在哪个分支”偷偷决定
- 而不是被 ticket 自己决定

这直接破坏单 ticket 原子性。

## 5. 修复策略

### 5.1 总体策略

本次修复不要求：

- 删除现有命令
- 新增 `ref-op`
- 重做 command surface

本次修复只要求：

1. 明确 ticket 的单 ref 合同
2. 在关键入口上增加守卫
3. 违反时直接拒绝
4. 错误信息必须给出正确下一步

### 5.2 守卫优先于兼容

这里的原则是：

- 兼容不能破坏 ticket 原子性
- 任何会让单 ticket 变成跨 ref 编排容器的路径，都要被 guard 掉

也就是说：

- 命令可以还在
- 但其可用语义会被收窄

## 6. 约束式修复合同

### 6.1 `ticket create`

#### 规则

1. 若显式传入 `--target-ref`
   - 直接规范化并冻结到 ticket
2. 若未传入 `--target-ref`
   - 若当前 repo 在明确 branch 上，则冻结为当前 branch 对应的 ref
   - 若当前 repo 处于 detached HEAD，则直接失败
3. `ticket create` 成功后，`target_ref` 就是 ticket 的 git 合同

#### 禁止

- 禁止创建后仍保持 `target_ref=''` 的普通 ticket
- 禁止依赖后续 `start` 再去“猜测”普通 ticket 的合同

#### 错误提示

建议报错：

```text
无法创建 ticket：当前仓库处于 detached HEAD，且未提供 --target-ref。
ticket 必须在创建时冻结唯一 target_ref。
请显式指定 --target-ref，或先 checkout 到目标分支后重试。
```

### 6.2 `ticket start`

#### 规则

1. 若 ticket 已有 `target_ref`
   - worker/worktree 必须从该 `target_ref` 建立
2. `--base` 若存在，只能作为一致性断言
   - 即：`normalize(--base) == ticket.target_ref`
3. 若 ticket 尚无 `target_ref`
   - 仅允许在明确 branch 上，且本次 start 会把该 ref 冻结为 `target_ref`
4. integration ticket 必须总是显式满足：
   - `base_ref == target_ref`

#### 禁止

- 禁止用 `ticket start --base other-branch` 改写已冻结 ticket 的基线
- 禁止 target 已存在时退回当前 branch / `HEAD`
- 禁止 integration ticket 在缺失 `target_ref` 时启动

#### 错误提示

建议报错：

```text
ticket start 被拒绝：--base 与 ticket.target_ref 不一致。
当前 ticket 是单 ref 原子任务，不能从一个 ref 开始却回到另一个 ref。
如需在新 ref 上执行，请创建新 ticket。
```

### 6.3 worker start / execution host

#### 规则

1. 一旦 ticket 有 `target_ref`
   - `StartTicket -> StartTicketResources -> RunTicketWorker`
   - 整条链必须显式传递规范化后的 `BaseBranch`
2. `BaseBranch` 为空时：
   - 对已冻结 `target_ref` 的 ticket 不允许回退到当前 branch / `HEAD`
3. 仅对极旧数据修复场景允许兜底
   - 但必须先把 ticket 标记为 blocked / repair-needed

#### 禁止

- 禁止对正常 ticket 在执行面“静默猜基线”

#### 错误提示

建议报错：

```text
ticket 执行被拒绝：未提供有效 BaseBranch，且系统禁止对已冻结 ticket 回退到当前分支。
请修复 ticket.target_ref，或新建一张正确 target_ref 的 ticket。
```

### 6.4 `merge retarget`

#### 规则

`merge retarget` 保留命令，但不再作为普通 ticket 的正常路径。

对当前系统，默认规则是：

- 普通 ticket：禁止 retarget
- integration ticket：禁止 retarget
- backport 需求：新建 ticket，不 retarget 旧 ticket

也就是说，`merge retarget` 在主模型里应视为：

- 暂时保留的修复入口
- 默认被拒绝

#### 禁止

- 禁止把 `done + needs_merge` 的普通 ticket 改送到另一条 ref
- 禁止用 retarget 表达 backport
- 禁止用 retarget 表达“其实我想回另一条分支”

#### 错误提示

建议报错：

```text
merge retarget 被拒绝：ticket 是单 ref 原子任务，done 后不能改写 target_ref。
如需交付到新的 ref，请创建一张新的 ticket（例如 integration ticket 或 backport ticket）。
```

### 6.5 `merge abandon`

#### 规则

`merge abandon` 仍然允许保留。

原因：

- 它不是把 ticket 改送到别处
- 它只是明确声明“此票不再从本票交付”

这不破坏 ticket 原子性。

### 6.6 integration ticket 创建

#### 规则

integration ticket 仍然是普通 ticket，不引入新对象。

但其合同必须更硬：

1. `target_ref` 必须明确
2. worktree base 必须等于 `target_ref`
3. 冲突现场只做证据采集，随后必须 `merge --abort`
4. 不允许继承 conflicted index

#### 禁止

- 禁止 integration ticket 继续原 merge 现场
- 禁止 integration ticket 在与 `target_ref` 不一致的 ref 上启动

### 6.7 backport

#### 规则

backport 不通过 retarget 原票表达。

正确方式：

- 新建一张 `target_ref=目标发布分支` 的新 ticket

这张票可以在描述中引用源 ticket，但 git 合同是它自己的。

#### 禁止

- 禁止把源票直接 retarget 到 release 分支

## 7. 字段层面的结论

### 7.1 保留的字段

下列字段不视为原子性问题，应保留：

- `target_branch`
- `merge_anchor_sha`
- `integration_status`
- `merged_at`
- `abandoned_reason`
- `superseded_by_ticket_id`

原因：

- 它们没有天然让 ticket 复合化
- 它们表达的是：
  - git 合同
  - 交付锚点
  - 交付收口
  - replacement 关系

真正的问题不在字段，而在于：

- 系统允许后续操作继续改写这些语义

### 7.2 需要收窄语义的入口

重点不是删字段，而是收窄这些入口的语义：

- `ticket start --base`
- worker start 的 base fallback
- `merge retarget`

## 8. 兼容策略

### 8.1 命令兼容

本次修复不要求删除命令。

可以继续保留：

- `ticket start --base`
- `merge retarget`

但要做到：

- 命令仍存在
- 正常违规用法被拒绝
- 错误消息给出替代路径

### 8.2 数据兼容

对历史 ticket：

1. 若 `target_ref` 已存在
   - 视为已冻结合同
2. 若 `target_ref` 为空
   - 允许在首次 start 时做一次性冻结
3. 若历史 ticket 已出现 `base != target`
   - 视为历史脏数据
   - 不继续扩大
   - 在 repair 路径显式处理

## 9. 实现要求

### 9.1 P0 守卫

必须优先实现：

1. `ticket create` 强制冻结普通 ticket 的 `target_ref`
2. `ticket start` 校验 `--base == target_ref`
3. worker/execution host 禁止对已冻结 ticket 回退当前 branch / `HEAD`
4. integration ticket 强制 `base_ref == target_ref`
5. `merge retarget` 默认拒绝并提示新建 ticket

### 9.2 P1 可观测性

建议补充：

1. ticket events 中追加 atomicity guard 事件
2. CLI 错误文本统一带：
   - 当前 ticket id
   - 当前 `target_ref`
   - 用户传入 ref
   - 正确替代动作

## 10. 验收标准

### Case 1：普通 ticket 创建与启动

场景：

- 当前分支 `dev_branch`
- `ticket create` 未显式传 `--target-ref`
- `ticket start`

期望：

- ticket.target_ref 被冻结为 `refs/heads/dev_branch`
- worktree base 也是 `refs/heads/dev_branch`

### Case 2：普通 ticket 试图跨 ref 启动

场景：

- ticket.target_ref = `refs/heads/dev_branch`
- 执行 `ticket start --base main`

期望：

- start 被拒绝
- 错误信息明确说明：
  - 当前 ticket 是单 ref 原子任务
  - 如需在 `main` 上执行，请创建新 ticket

### Case 3：done 后试图 retarget

场景：

- ticket 已 `done + needs_merge`
- 执行 `merge retarget --ref refs/heads/main`

期望：

- retarget 被拒绝
- 错误提示建议新建 integration/backport ticket

### Case 4：integration ticket

场景：

- `main` merge 某票冲突
- abort merge
- 创建 integration ticket，`target_ref=refs/heads/main`
- start integration ticket

期望：

- integration ticket worktree 从 `main` 建
- 不继承 conflicted index
- 不允许从其他 ref 启动

### Case 5：backport

场景：

- 某票已在 `main` 完成
- 希望把语义交付到 `release/v1`

期望：

- 不通过 retarget 原票完成
- 正确方式是创建一张新的 `target_ref=refs/heads/release/v1` ticket

### Case 6：历史空 target_ref 兼容

场景：

- 历史 ticket.target_ref 为空
- 当前仓库位于明确 branch
- 第一次 start

期望：

- 允许一次性冻结
- 冻结后后续不得再改

### Case 7：detached HEAD

场景：

- 当前仓库 detached HEAD
- 执行 `ticket create` 且未提供 `--target-ref`

期望：

- create 被拒绝
- 明确提示用户显式指定 `--target-ref`

## 11. 一句话收束

本修复规格不新增复杂对象，也不重做命令体系。

它只做一件事：

**把 ticket 的 git 合同钉死成单 ref 原子，并把所有会破坏这个合同的路径改成显式拒绝。**
