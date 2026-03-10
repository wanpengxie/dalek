# Merge 命令重设计：从 merge_queue 到 ticket merge

## Context

ticket lifecycle redesign（`6e95b60`）将 merge 从独立状态机（merge_queue）降级为 ticket 自有的 integration_status 字段。但 CLI 层产生了两个问题：

1. **命名割裂**：新命令叫 `dalek ticket integration status/abandon`，但 merge 是 git 原生概念，AI agent 天然理解。`integration` 是自造抽象，增加学习成本。
2. **大量熔岩**：merge_queue 的写路径（Propose/Approve/Discard/MarkMerged）已在 CLI 层被拦截不可达，但底层服务代码、PMOps executor、channel action executor、测试代码全部残留。

## Goals

- **统一 merge 语义**：`dalek merge` 顶层命令组，底层查 ticket.integration_status。agent 零学习成本。
- **清除全部死代码**：merge_queue 写路径相关的服务方法、PMOps、channel action、app facade、测试——全部删除。
- **kernel 对齐**：agent-kernel.md 中 merge 概念描述与新命令一致。

## Non-goals

- 不删除 MergeItem DB 模型和表结构（历史数据保留）。
- 不删除 ListMergeItems（dashboard / daemon planner prompt 仍在调用，作为历史数据查询保留）。
- 不重命名 Go 内部字段 `integration_status`（DB 字段重命名风险过高）。

## Target CLI

```
dalek merge <command> [flags]

Commands:
  ls         按 merge 状态列出 tickets
  status     查看单个 ticket 的 merge 状态
  abandon    放弃 ticket merge
```

### `merge ls`

查询 ticket 表，按 integration_status 过滤。

```
dalek merge ls [--status needs_merge|merged|abandoned] [-n 50] [--output text|json]
```

默认只显示 `workflow_status=done` 且 `integration_status` 非空的 tickets。

输出字段：ticket_id / workflow_status / integration_status / merge_anchor_sha / target_branch / merged_at

JSON schema: `dalek.merge.list.v1`

### `merge status`

查询单个 ticket 的 merge 详情。

```
dalek merge status --ticket <id> [--timeout 5s] [--output text|json]
```

等价于原 `ticket integration status`，逻辑直接迁移。

JSON schema: `dalek.merge.status.v1`

### `merge abandon`

放弃 ticket merge。

```
dalek merge abandon --ticket <id> --reason "..." [--timeout 5s] [--output text|json]
```

等价于原 `ticket integration abandon`，调用 `AbandonTicketIntegration`。

JSON schema: `dalek.merge.abandon.v1`

## `ticket integration` 处理

`ticket integration` 子命令删除，替换为兼容提示：

```go
case "integration":
    exitUsageError(globalOutput,
        "ticket integration 已迁移到 merge 命令",
        "请改用 dalek merge status / dalek merge abandon",
        "例如: dalek merge status --ticket 1",
    )
```

同时从 `printTicketUsage()` 中删除 integration 行。

## 死代码清除清单

### 1. `internal/services/pm/merge.go`

**删除**以下方法（保留 ListMergeItems + ListMergeOptions）：

| 方法 | 行号 | 原因 |
|------|------|------|
| mergeTerminalStatuses() | ~18 | 仅被 ProposeMerge 调用 |
| ProposeMerge() | ~49 | CLI 已拦截 |
| ApproveMerge() | ~103 | CLI 已拦截 |
| DiscardMerge() | ~131 | CLI 已拦截 |
| MarkMergeMerged() | ~171 | CLI 已拦截 |

### 2. `internal/services/pm/merge_test.go`

**整个文件删除**。所有测试都是测试上述死方法。

ListMergeItems 的测试（如果有）应迁移到其他测试文件，但检查后发现没有——ListMergeItems 无独立单元测试。

### 3. `internal/app/project_inbox_merge.go`

**删除**以下 facade 方法（保留 inbox facade 和 ListMergeItems facade）：

| 方法 | 行号 |
|------|------|
| ProposeMerge() | ~71 |
| ApproveMerge() | ~78 |
| DiscardMerge() | ~85 |
| MarkMergeMerged() | ~92 |

### 4. `internal/contracts/pm_ops.go`

**删除**以下常量：

```go
PMOpApproveMerge  PMOpKind = "approve_merge"
PMOpDiscardMerge  PMOpKind = "discard_merge"
```

### 5. `internal/services/pm/pmops_executor.go`

**删除**以下内容：

| 内容 | 行号 |
|------|------|
| `case contracts.PMOpApproveMerge:` 分支 | ~28 |
| `case contracts.PMOpDiscardMerge:` 分支 | ~30 |
| `approveMergePMOpExecutor` 结构体 + Reconcile + Execute | ~250-291 |
| `discardMergePMOpExecutor` 结构体 + Reconcile + Execute | ~293-342 |

### 6. `internal/services/pm/pmops_parser.go`

**删除** PMOpApproveMerge / PMOpDiscardMerge 在 kind 校验 case 中的引用（~line 233）。

### 7. `internal/services/channel/action_executor.go`

**删除**以下内容：

| 内容 | 行号 |
|------|------|
| PMActionService 接口中 ApproveMerge / DiscardMerge 声明 | ~49-50 |
| `case contracts.ActionApproveMerge:` 分支 | ~107-108 |
| `executeApproveMerge()` 方法 | ~372 |
| discard 逻辑（紧跟 approve 之后） | ~390+ |

### 8. `internal/contracts/channel_gateway.go`

**删除**：

```go
ActionApproveMerge = "approve_merge"
```

保留 `ActionListMergeItems`（仍被 channel 使用）。

### 9. `internal/app/channel_action_adapter.go`

**删除** ApproveMerge / DiscardMerge 适配方法（~line 60-66）。

### 10. `internal/services/channel/service_test.go`

**删除** testPMActionAdapter 中 ApproveMerge / DiscardMerge 方法（~line 1408-1413）。

### 11. `internal/services/pm/acceptance_engine_test.go`

**审查** `TestApproveMergePMOpExecutor_OptionalAcceptanceGate`（~line 242）。如果测试仅覆盖被删的 PMOp executor，整个测试函数删除。

### 12. `cmd/dalek/cmd_merge.go`

**删除**以下函数（被新命令替代）：

| 函数 |
|------|
| cmdMergePropose() |
| cmdMergeApprove() |
| cmdMergeDiscard() |
| cmdMergeMarked() |
| exitMergeDeprecated() |
| printMergeDeprecatedSubUsage() |
| isHelpArgs() |

## manager_tick 输出字段重命名

`ManagerTickResult.MergeProposed` 和 `mergeProposalResult.MergeProposed` 的语义已变为"冻结了 integration anchor 的 tickets"。

**重命名**：

| 位置 | 旧名 | 新名 |
|------|------|------|
| manager_tick.go:45 | MergeProposed | MergeFrozen |
| manager_tick.go:68 | MergeProposed | MergeFrozen |
| manager_tick.go:341, 597, 607 | MergeProposed | MergeFrozen |
| cmd_manager.go JSON key | "merge_proposed" | "merge_frozen" |
| cmd_manager.go text output | merge_proposed | merge_frozen |

## Kernel Template 更新

文件：`internal/repo/templates/project/agent-kernel.md`

### 概念映射

```
merge     ≈ 将 worker 分支合入主线的 git 集成动作（结果通过 ticket.integration_status 自动观测）
```

### operations

```
Merge：ls|status|abandon（ID: --ticket N）
```

替代原来的：
```
Merge：ls（只读审计，已废弃）
```

以及删除 `Ticket` operations 中的 `integration status|integration abandon`。

### entity_map

保持 ticket 下的 integration_status / merge_anchor_sha / target_branch 字段，无变化。

### capability_index

```
待办与 merge 决策    → dalek inbox ls / inbox show --id N / dalek merge status --ticket N / dalek merge ls
```

替代原来的 `ticket integration status` 引用。

### ticket_integration section

section 名和内容保留（描述 integration_status 的状态空间），不重命名 section tag——内部标签不影响 agent 理解。

### merge_queue section

当前已写为"已废弃"。改为：

```
<merge_queue>
  merge_queue 仅保留历史数据查询（ListMergeItems）。
  PM 交付判断和操作统一使用 dalek merge ls|status|abandon。
</merge_queue>
```

### invariants / SOP

扫描所有 "integration" 字样，确认是内部字段引用（保留）还是 PM 可见命令引用（改为 merge）。

## 验收标准

1. `dalek merge ls` 输出 done tickets 的 merge 状态
2. `dalek merge status --ticket N` 输出单 ticket merge 详情
3. `dalek merge abandon --ticket N --reason "test"` 成功执行
4. `dalek ticket integration` 输出兼容迁移提示
5. `go test ./...` 全部通过
6. `go build ./cmd/dalek` 编译通过
7. 搜索 `ProposeMerge|ApproveMerge|DiscardMerge|MarkMergeMerged` 仅出现在保留的历史查询代码中（不应出现可调用路径）
8. kernel template 中 merge 概念、operations、capability_index 与新 CLI 一致
