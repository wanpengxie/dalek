# Merge Design Redesign

## Context

当前 merge 相关设计仍然有三层混乱：

1. merge 虽然已经从独立 queue 退化成了 ticket 上的 `integration_status`，但主路径仍然依赖 manager tick / autopilot 语义，收口不够直接。
2. 命令面已经切到了 `dalek merge`，但缺少一个真正融入现有命令体系的 hook 回收入口。
3. `target_branch` 的语义仍然和“当前 checkout branch”纠缠，无法稳定表达 ticket 的交付契约。

本设计的目标是把 merge 完整收敛成：

- hook 驱动
- ticket owned
- git fact based
- 与现有 CLI 命令组一致

## Core Decisions

### 1. merge 不是独立对象

merge 不是 merge_queue，也不是 merge_item。

merge 是 ticket 的交付收口状态，只存在于 ticket 上。

ticket 只保留：

- `workflow_status`
- `integration_status`
- `target_ref`
- `anchor_sha`
- `merged_at`
- `abandoned_reason`

### 2. 主路径不是 tick，是 repo hook

merge 状态推进的主路径不是：

- autopilot
- planner
- manager tick
- daemon 常驻循环

而是：

- repo root 的 git ref update hook
- hook 直接执行 `dalek merge sync-ref`
- dalek 二进制本地读取当前 repo 的 ticket DB 并回收状态

tick 只保留为 repair path，不再是主路径。

### 3. 判定 merged 只看 git 事实

一张 ticket 被判为 `merged`，必须满足：

- `workflow_status = done`
- `integration_status = needs_merge`
- 本次更新的 `ref == ticket.target_ref`
- `git merge-base --is-ancestor ticket.anchor_sha new_sha` 返回成功

不依赖：

- approve
- mark merged
- planner 推理
- 当前 checkout branch

### 4. 默认不依赖 marker

默认可靠主路径只支持“不改写提交身份”的合入方式：

- merge commit
- fast-forward

因此默认设计不依赖 marker。

`squash / rebase / cherry-pick` 不再被视为默认自动可判定路径。
如果未来要支持，必须走受控 merge 入口，由系统自动补充额外识别机制。这不属于当前设计范围。

## Terminology

### target_ref

ticket 最终要并入的目标 ref。

例子：

- `refs/heads/main`
- `refs/heads/dev`
- `refs/heads/feature/auto-dev-mode`

注意：

- 语义上是 ref，不是“当前 branch”
- 不能在 `done` 时再去读 repo 当前 branch
- 必须在 ticket 执行契约建立时冻结

### anchor_sha

ticket 完成时冻结的最终交付 commit。

这是后续判定 merged 的锚点。

### integration_status

merge 状态只保留：

- `none`
- `needs_merge`
- `merged`
- `abandoned`

## State Model

### workflow_status

`workflow_status` 只表达 ticket 的业务执行阶段：

- `backlog`
- `active`
- `blocked`
- `done`
- `archived`

`queued` 不应再是 PM-visible 生命周期的一部分。

### integration_status

`integration_status` 表达 ticket 的交付收口阶段：

- `none`
  - ticket 还未进入交付收口
  - 通常对应 `workflow_status != done`
- `needs_merge`
  - ticket 已 done，但目标 ref 还没包含它
- `merged`
  - 目标 ref 已包含它
- `abandoned`
  - 这张票不再从本票交付

## Freeze Timing

### target_ref 何时冻结

`target_ref` 必须在 ticket 的执行契约建立时冻结。

推荐时机：

- `ticket create`

或兜底时机：

- 第一次 `ticket start`

规则：

- 如果 create 时 repo 当前有明确 branch，则冻结为该 branch 对应的 ref
- 如果是 detached HEAD，则必须显式指定
- 一旦冻结，后续切 branch 不影响这张票

### anchor_sha 何时冻结

`anchor_sha` 必须在 ticket 从 `active -> done` 时冻结。

规则：

- `worker report done` 与 `anchor_sha` 冻结必须同事务完成
- 冻结失败则不能进入 `done`
- 进入 `done` 后必须立即进入 `integration_status = needs_merge`

所以最终是：

- `target_ref` 早冻结
- `anchor_sha` 晚冻结

## Merge State Machine

### Allowed transitions

- `none -> needs_merge`
  - 触发：ticket 完成并冻结 `anchor_sha`
- `needs_merge -> merged`
  - 触发：hook 驱动的 `dalek merge sync-ref`
- `needs_merge -> abandoned`
  - 触发：显式 `dalek merge abandon`

### Forbidden transitions

- `merged -> needs_merge`
- `merged -> abandoned`
- `abandoned -> needs_merge`
- `abandoned -> merged`

`merged` 和 `abandoned` 都视为 integration terminal state。

如果未来出现 force-push 抹掉已 merged 提交，不做正常状态回退，而是记 `merge_drift` incident，交给 repair 流程处理。

## Command System

### Current command surface

当前 merge 命令面已经在 `dalek merge` 下：

- `dalek merge ls`
- `dalek merge status`
- `dalek merge abandon`

这条方向是对的，应当保留。

### Target command surface

#### PM-visible commands

保留或新增：

- `dalek merge ls`
  - 列出 `done` tickets 的 merge 状态
- `dalek merge status --ticket <id>`
  - 查看单票 merge 状态
- `dalek merge abandon --ticket <id> --reason "..."`
  - 显式放弃本票交付
- `dalek merge retarget --ticket <id> --ref <ref>`
  - 显式修改 `target_ref`
  - 仅允许在 `integration_status = needs_merge` 时执行

删除或保持迁移提示：

- `ticket integration ...`
- 所有 merge queue 写命令
- 所有 `approve/mark merged` 语义

#### Hook/internal command

新增一个内部命令：

- `dalek merge sync-ref`

用途：

- 由 git hook 直接调用
- 不依赖 daemon
- 不作为 PM 常用操作宣传，但必须属于现有 `dalek merge` 命令体系

建议参数：

```text
dalek merge sync-ref --repo-root <path> --ref <ref> --old <old_sha> --new <new_sha>
```

语义：

- 根据一次 ref 更新事件，回收本 repo 自己的 merge 状态

命名理由：

- `sync-ref` 明确表达“把 ticket merge 状态同步到 ref 事实”
- 比 `update` 更精确
- 比 `reconcile-ref` 更贴近当前 CLI 风格

#### Manual repair command

建议再补一个 repair 入口：

- `dalek merge rescan [--ref <ref>]`

用途：

- hook 丢失后的手工修复
- init/upgrade 后首轮对账

这不是主路径，但应存在。

## Hook Design

### Installation

hook 由 dalek 管理，不由 repo 自己维护。

在以下场景执行注入：

- `dalek init`
- `dalek upgrade`

安装位置：

- repo root 下的 [`.git/hooks`](/home/xiewanpeng/tardis/dalek/.git/hooks)

不使用：

- `core.hooksPath`
- daemon 常驻订阅

### Hook type

首选 hook：

- `reference-transaction`

原因：

- 能拿到完整的 ref 变更
- 能覆盖 branch ref 更新
- 比 `post-merge` 更贴近“ref 事实变化”

hook 只在 `committed` 阶段处理。

### Hook behavior

hook 本身必须很薄，只负责：

1. 读取 ref 更新事件
2. 过滤出 `refs/heads/*`
3. 调用 `dalek merge sync-ref`

hook 不负责：

- 打开数据库做复杂逻辑
- 自己计算 merge 状态
- 依赖 daemon RPC

### Example hook behavior

伪代码：

```bash
phase="$1"
[ "$phase" = "committed" ] || exit 0

while read -r old new ref; do
  case "$ref" in
    refs/heads/*)
      dalek merge sync-ref \
        --repo-root "$PWD" \
        --ref "$ref" \
        --old "$old" \
        --new "$new" || true
      ;;
  esac
done
```

## `merge sync-ref` Behavior

`dalek merge sync-ref` 的逻辑应该固定为：

1. 打开 `--repo-root` 对应项目
2. 只查询本 repo 自己数据库里的 tickets
3. 过滤条件：
   - `workflow_status = done`
   - `integration_status = needs_merge`
   - `target_ref = 传入 ref`
4. 对每张票做：
   - `is-ancestor(anchor_sha, new_sha)`
5. 成功命中则更新：
   - `integration_status = merged`
   - `merged_at = now`
6. 未命中则保持不变
7. 如果 `anchor_sha` 缺失或非法，记录 error/incident

这条路径不依赖：

- manager tick
- planner
- daemon
- autopilot

## Retarget Semantics

`retarget` 是显式 PM 决策，不是系统猜测。

规则：

- 只允许在 `integration_status = needs_merge`
- 修改内容：
  - `target_ref`
- 必须写审计事件
- 不修改 `anchor_sha`

典型场景：

- ticket 从 `main` 开出
- 做完后决定改投 `dev`
- 执行：
  - `dalek merge retarget --ticket N --ref refs/heads/dev`

之后只由 `dev` 的 ref 更新去回收它。

## Branch Switch Example

场景：

1. 你当前在 `main`
2. 创建 ticket
3. 系统冻结：
   - `target_ref = refs/heads/main`
4. 开发过程中你切到 `dev`
5. ticket done，冻结：
   - `anchor_sha = abc123`
6. 如果代码只合进 `dev`
   - ticket 仍然是 `needs_merge`
7. 只有 `refs/heads/main` 更新并包含 `abc123`
   - ticket 才进入 `merged`

结论：

- 当前 checkout branch 只是工作上下文
- 不是 ticket 交付契约

## Archive Rules

归档必须受 merge 状态约束。

只允许：

- `done + merged -> archived`
- `done + abandoned -> archived`

不允许：

- `done + needs_merge -> archived`

否则会把尚未交付的票从工作面直接清掉。

## Why This Is Complete Enough

这套设计已经完整覆盖：

- repo 当前 branch 切换
- 不依赖 autopilot / daemon
- repo 自己发出去的 ticket 自己回收
- merge 命令体系内存在 hook/internal 子命令
- ticket 归档与 merge 状态的一致性

这套设计刻意不覆盖：

- squash/rebase/cherry-pick 的自动可靠判定
- 已 merged 后 force-push 的自动回退

这两类场景需要更强的受控 merge 入口，不属于当前默认主路径。

## Migration Notes

### 命名层

对外命名统一用：

- `merge`
- `target_ref`
- `anchor_sha`

不再继续扩张：

- `ticket integration`
- merge queue
- approve / mark merged

### 数据层

如果短期内 DB 字段仍保留 `target_branch` 命名，可以先做语义兼容：

- `target_branch` 实际保存完整 ref

但长期建议迁移为：

- `target_ref`

避免继续误导成“当前 branch 名”。

## Summary

最终 merge 设计应收敛为：

- ticket create 时冻结 `target_ref`
- ticket done 时冻结 `anchor_sha` 并进入 `needs_merge`
- `dalek init/upgrade` 向 repo 的 [`.git/hooks`](/home/xiewanpeng/tardis/dalek/.git/hooks) 注入 ref update hook
- hook 直接执行 `dalek merge sync-ref`
- `merge sync-ref` 只回收本 repo 自己的 `done + needs_merge` tickets
- 命中 `target_ref + anchor_sha` 后推进为 `merged`
- `abandon` 和 `retarget` 是唯一显式管理动作
- 只有 `merged` 或 `abandoned` 才允许 `archive`
