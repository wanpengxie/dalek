# 主世界收口环规格

## 1. 目的

本文档定义一个未来阶段的通用能力：

- 当单张 ticket 的原子工作已经结束
- 但主世界（repo root / target ref）出现了需要解释或治理的状态变化
- 系统如何进入一个单独的“主世界收口环”

这不是本期 `focus-run-batch-v1` 的补丁设计。

它预期与后续 DAG / 完整 loop engine 一起实现，用来处理：

- merge 前主世界不干净
- merge 后、下一张启动前主世界不干净
- integration handoff 之后的主世界恢复

## 2. 核心判断

### 2.1 ticket 仍然是原子任务

ticket 继续保持原子：

- 一张 ticket 只绑定一个 `target_ref`
- worker worktree 基于该 `target_ref`
- ticket 只负责自己的实现与交付

主世界状态问题不是 ticket 内部语义。

它是一个独立的 PM 治理问题。

### 2.2 主世界收口不是 merge，也不是产品开发

`world reconcile` 的职责是：

- 判断主世界状态变化意味着什么
- 决定是否允许继续主线
- 在允许范围内原地治理系统/集成状态
- 不能越权修改产品实现代码

### 2.3 Go 控制面只做检测与约束

Go 控制面负责：

- 检测“进入了主世界收口场景”
- 给出结构化现场上下文
- 执行硬边界
- 执行 PM agent 的合法决策

Go 控制面不负责：

- 用规则穷举所有主世界脏状态语义
- 代替 PM agent 理解复杂现场

### 2.4 PM agent 负责解释与决策

PM agent 负责：

- 理解主世界发生了什么变化
- 判断是否可以继续
- 判断是否可以做有限度的 inplace 治理
- 判断是否必须升级成新 ticket

## 3. 适用范围

本文档只定义两个入口：

1. `pre_merge_world_check`
2. `pre_next_item_world_check`

不扩展到：

- worker 自己 worktree 的收口校验
- note / inbox 一般流程
- 任意 ref-op 的通用建模

## 4. 两个入口

### 4.1 merge 前入口

流程：

`Tn_DONE -> pre_merge_world_check`

含义：

- 当前 ticket 已完成 worker 侧原子工作
- 系统准备在 repo root 上执行 merge
- 在真正 merge 前检查主世界状态

### 4.2 下一张启动前入口

流程：

`item completed -> pre_next_item_world_check`

含义：

- 当前 item 已完成交付语义
- 系统准备启动下一张票
- 在启动下一张前检查主世界是否适合继续

## 5. 不进入 PM agent 的确定性分支

以下场景不需要 PM agent 先判断：

### 5.1 worker worktree dirty

这是 ticket 自己未收口。

它属于 worker closure 问题，不进入 `world reconcile`。

### 5.2 merge conflict

这是确定性分支：

- 若 merge 冲突
- 直接按既有规则处理：
  - `git merge --abort`
  - 创建 integration ticket
  - 当前 batch blocked

这里不需要先问 PM agent“要不要 handoff”。

### 5.3 已经 `merged`

一旦 integration observe 看到 `merged`，交付真相已成立。

后续主世界不干净，只影响：

- 能不能继续下一张

不允许反向把当前 item 改成 `merge_failed`。

## 6. 进入 PM agent 的条件

只有当控制面确定：

- 当前不属于 worker closure 问题
- 当前不属于明确 merge conflict 分支
- 但主世界状态仍不满足继续条件

才进入 `pm_world_reconcile`。

也就是：

- `pre_merge_world_check -> not_ready`
- `pre_next_item_world_check -> not_ready`

## 7. PM agent 输入

PM agent 必须收到结构化上下文，而不是只看到一段报错。

最小输入：

```json
{
  "phase": "pre_merge_world_check | pre_next_item_world_check",
  "project": "dalek",
  "ticket_id": 123,
  "target_ref": "refs/heads/main",
  "repo_root_head": "abc123",
  "repo_root_branch": "main",
  "git_status_porcelain": ["M .dalek/.gitignore", "?? .gitignore"],
  "merge_state": {
    "merge_head_exists": false,
    "has_unmerged_files": false
  },
  "ticket_snapshot": {
    "workflow_status": "done",
    "integration_status": "merged",
    "merge_anchor_sha": "..."
  },
  "system_owned_paths": [
    ".dalek/**",
    ".dalek/runtime/**"
  ]
}
```

可选补充：

- 最近一次 merge 输出摘要
- 最近一次 focus / loop 事件
- 哪些路径被控制面归类为 system-owned / unknown

## 8. PM agent 只允许 4 种输出

PM agent 不允许自由生成动作脚本。

它只能返回这 4 种决策之一：

### 8.1 `ready`

含义：

- 主世界状态可接受
- 直接继续主线

### 8.2 `repair_inplace`

含义：

- 主世界确实有问题
- 但问题属于 PM 可以治理的范围
- 可在 repo root 原地修复

### 8.3 `create_ticket`

含义：

- 这已经不是主世界治理
- 而是一项新的工程工作
- 必须创建新 ticket 处理

### 8.4 `wait_user`

含义：

- PM 不能安全判断
- 或缺少用户独有决策/权限
- 当前 loop 进入 blocked

## 9. `repair_inplace` 的合法边界

### 9.1 允许

PM 只能 inplace 处理以下类型：

1. 系统拥有的控制面文件
   - `.dalek/**`
   - runtime / bootstrap / manager seed 生成的中间状态

2. 明确的 merge 事务残留
   - `MERGE_HEAD`
   - unresolved conflict metadata
   - 仅限恢复 merge 事务状态

3. 可确定的集成治理动作
   - `sync-ref`
   - `rescan`
   - 回到目标分支
   - 清理确定无语义的系统残留

### 9.2 禁止

PM 不得 inplace 做这些事：

1. 修改产品实现文件
2. 修改测试文件
3. 修改业务配置以改变产品语义
4. 在 repo root 手工解决产品冲突
5. 借“清理主世界状态”为名完成实际产品开发

## 10. `git merge --abort` 的边界

`git merge --abort` 不是一般性的主世界处理动作。

它只允许出现在：

- 当前确实存在未完成 merge 事务
- 例如存在 `MERGE_HEAD`
- 或确有 merge conflict residue

以下情况不允许使用 `git merge --abort`：

- merge 之前的普通脏状态
- merge 已成功结束之后的脏状态

## 11. 何时必须 `create_ticket`

只要问题已经变成真实工程工作，就必须升级成新 ticket。

典型例子：

1. 需要修改产品代码才能继续
2. 需要修改测试或项目配置才能继续
3. repo root 的变化具有真实业务/工程语义
4. 无法在“不改产品实现”的前提下把世界状态恢复到安全可继续

这时：

- 当前 loop 不再试图 inplace 吞掉问题
- 必须创建新 ticket
- 当前 batch 保持 blocked，等待新 ticket 收口

## 12. 主世界状态分类

为了让 PM agent 决策更稳定，控制面应先给出粗分类。

### 12.1 `merge_residue`

表示 merge 事务没结束：

- unmerged files
- `MERGE_HEAD` 存在

### 12.2 `system_owned_drift`

表示系统自己拥有的状态漂移：

- `.dalek/**`
- runtime / seed / bootstrap 生成内容

### 12.3 `semantic_dirty`

表示具有工程语义的脏状态：

- 产品代码
- 测试
- 项目级配置
- repo 根下非系统拥有文件

### 12.4 `unknown_dirty`

控制面无法可靠归类。

## 13. loop 行为

### 13.1 `pre_merge_world_check`

```text
Tn_DONE
  -> pre_merge_world_check
    -> ready
      -> merge
    -> not_ready
      -> pm_world_reconcile
        -> ready
          -> merge
        -> repair_inplace
          -> recheck
        -> create_ticket
          -> blocked(waiting_world_ticket)
        -> wait_user
          -> blocked(waiting_user)
```

### 13.2 `pre_next_item_world_check`

```text
item completed
  -> pre_next_item_world_check
    -> ready
      -> start next item
    -> not_ready
      -> pm_world_reconcile
        -> ready
          -> start next item
        -> repair_inplace
          -> recheck
        -> create_ticket
          -> blocked(waiting_world_ticket)
        -> wait_user
          -> blocked(waiting_user)
```

## 14. 与 DAG / loop engine 的关系

本能力不应作为一堆 if-else 散落在 focus controller 中。

更合理的落点是：

- 作为 DAG / loop engine 中的标准节点类型
- 由 loop engine 决定：
  - 何时进入 world check
  - 何时进入 reconcile
  - 何时 blocked
  - 何时恢复

推荐节点：

- `node:pre_merge_world_check`
- `node:pm_world_reconcile`
- `node:pre_next_item_world_check`

## 15. 当前版本的落地建议

本期先不实现。

当前阶段只做两件事：

1. 保持已有 batch v1 继续最小收敛
2. 把“主世界状态治理”明确留给后续 DAG / loop engine 一期统一处理

## 16. 验收标准

未来实现该能力时，至少要通过这些 case：

1. `done` 后、merge 前 repo root 存在 system-owned drift  
   PM agent 可 `repair_inplace`，随后继续 merge。

2. `done` 后、merge 前 repo root 存在 semantic dirty  
   PM agent 必须选择 `create_ticket` 或 `wait_user`，不得直接改产品代码。

3. merge 成功且已 observe merged，但 repo root 在启动下一张前出现 system-owned drift  
   当前 item 保持 `completed`，PM agent 治理后继续下一张。

4. merge 成功且已 observe merged，但 repo root 存在 semantic dirty  
   当前 item 保持 `completed`，loop blocked，不得回写 `merge_failed`。

5. merge residue 场景  
   只有在存在真实 merge 事务残留时，才允许 `git merge --abort`。

6. 当前问题升级成新 ticket 后  
   原 batch 必须有明确 blocked reason，且恢复路径可追踪。
