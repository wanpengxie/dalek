# 架构债务执行 DAG（2026-02-26）

> 范围：`docs/arch_debt/TICKETS.md` 的 `T01~T39`
> 调度约束：每次执行 1~4 个 ticket；严格按依赖优先级推进

## 执行规则

1. 基础组件与状态/迁移基础设施优先（`T38/T39/T24`），避免后续返工。
2. 同一批次内不放置“硬依赖”关系，保证可并行。
3. 同一子域（PM/Channel/Store）尽量连续推进，减少上下文切换成本。

## 关键依赖边（DAG）

- `T03 -> T04`
- `T07 -> T08 -> T09`
- `T14 -> T15 -> T16 -> T17`
- `T21 -> T22 -> T26 -> T27`
- `T21 -> T23 -> T28`
- `T39 -> T20`
- `T39 -> T27`
- `T39 -> T34`
- `T24 -> T25`
- `T29 -> T30`
- `T31 -> T35`
- `T12 -> T13`
- `T19 -> T13`
- `T26 -> T13`
- `T04 -> T05`
- `T11 -> T05`
- `T12 -> T05`
- `T33 -> T36`
- `T34 -> T36`
- `T35 -> T36`
- `T38 -> T32`

## 执行批次（拓扑分层）

| 批次 | 票数 | tickets | 说明 |
|---|---:|---|---|
| W01 | 4 | `T38` `T39` `T24` `T06` | 基础设施先行：日志/FSM/迁移/PM 配置 |
| W02 | 4 | `T19` `T21` `T03` `T10` | 核心模型与通道基建起步 |
| W03 | 4 | `T01` `T02` `T04` `T11` | app/cmd 第一轮归位（含 Feishu 复用） |
| W04 | 4 | `T22` `T14` `T31` `T29` | 类型迁移第二阶段 + Channel/Daemon 拆分启动 |
| W05 | 4 | `T23` `T15` `T30` `T37` | 类型迁移收尾 + Channel/Daemon/Sdkrunner 并行 |
| W06 | 4 | `T26` `T12` `T16` `T07` | ticket/app/channel/agentexec 边界同步收敛 |
| W07 | 4 | `T27` `T28` `T17` `T08` | workflow/query/channel/agentexec 第二轮收口 |
| W08 | 4 | `T20` `T13` `T18` `T25` | TaskRuntime/DaemonManager/Provider/Store 类型化 |
| W09 | 4 | `T09` `T33` `T34` `T35` | PM 调度主链与 agentexec 收尾 |
| W10 | 3 | `T36` `T32` `T05` | 可靠性与测试补齐 + 日志包收口 + cmd 测试闭环 |

## 每轮启动 Dispatch 必带指令（强制）

每次启动 `Wxx` 前，dispatch prompt 必须包含以下 7 项，缺一不可：

1. `Wave` 与本轮 tickets（1-4 个）。
2. 前置依赖校验（列出本轮依赖的上游 tickets，并确认已完成）。
3. 当前“架构状态增量”（已完成 waves 产出的新组件/新边界）。
4. 本轮“必须复用/必须遵循”的组件与边界（禁止绕过）。
5. 本轮验收口径（功能等价、架构约束、测试要求）。
6. 本轮结束回写动作（更新 `EXECUTION_DAG.md` 与 `.dalek/AGENTS.md` 的 `<current_phase>`）。
7. 阻塞分叉规则（若上游未完成，如何调整 DAG 与改派）。

推荐模板（直接粘贴后替换占位符）：

```text
[ARCH-DEBT Wxx DISPATCH CONTRACT]
Wave: Wxx
Tickets: <T.. T.. T..>
前置依赖校验: <依赖票> = done
架构状态增量: <已落地组件/接口/边界>
本轮必须复用:
- <组件/接口 A>
- <组件/接口 B>
本轮禁止事项:
- 禁止绕过 <新服务/新状态机/新迁移入口>
- 禁止在 <层> 直接访问 <下层实现>
验收口径:
- 功能回归: <命令/路径>
- 架构约束: <import/边界/状态机规则>
- 测试: <新增或更新测试>
完成后回写:
- 更新 docs/arch_debt/EXECUTION_DAG.md（依赖变化与下一轮）
- 更新 .dalek/AGENTS.md <current_phase>（状态与关注点）
阻塞分叉:
- 若 <上游票> 未完成，则只执行 <不依赖子集>，并更新 DAG
```

## 分轮架构状态提醒（启动时必须写入 dispatch）

| Wave | 本轮 tickets | 启动时必须提醒下游的“架构状态变化” |
|---|---|---|
| W01 | `T38` `T39` `T24` `T06` | 产出日志统一入口（slog + 注入点）、通用 FSM、migration runner、统一 env builder。后续票禁止再引入并行实现。 |
| W02 | `T19` `T21` `T03` `T10` | 核心类型与 project 结构开始归位；Feishu/WS 归位后，下游 cmd/app 必须复用共享服务，不再自建链路。 |
| W03 | `T01` `T02` `T04` `T11` | app/cmd 第一轮归位完成后，后续票不得把业务逻辑留在 cmd/app；配置逻辑必须走统一入口。 |
| W04 | `T22` `T14` `T31` `T29` | 类型迁移第 2 阶段后，store 不再作为跨层类型中心；channel 入站与 gatewaysend 分层成为默认路径。 |
| W05 | `T23` `T15` `T30` `T37` | 类型迁移收尾后，新增跨层类型统一落在 core/contracts；daemon/channel/sdkrunner 继续按分层边界推进。 |
| W06 | `T26` `T12` `T16` `T07` | ticket service、facade 边界、channel 无 store 直连、agentexec 服务层入口开始生效，下游必须按新入口开发。 |
| W07 | `T27` `T28` `T17` `T08` | workflow/query/channel/agentexec 第二轮收口，ticket 生命周期与查询语义进入统一权威实现。 |
| W08 | `T20` `T13` `T18` `T25` | TaskRuntime 必须复用 FSM；DaemonManager 与 Provider/默认值单点化；高频 JSON 字段类型化路径确定。 |
| W09 | `T09` `T33` `T34` `T35` | PM 调度主链与通知解耦完成后，续后 PM 变更必须遵循拆分后的职责边界。 |
| W10 | `T36` `T32` `T05` | 可靠性/测试/日志命名收口，后续优化票必须以该轮产出的测试护栏和日志体系为基线。 |

## 每批执行建议

1. 每批结束后，先清单回写：更新 `CRITICAL/HIGH/MEDIUM_SELECTED/LOW_SELECTED` 中对应 ID 状态。
2. 每批结束后，执行一次依赖回归：至少覆盖本批 tickets 涉及的命令路径和核心服务单测。
3. 若某票超出 2000 行，立即在同批内拆分子票，不要把过大改动压到下一批。
