# PM 自主开发模式 (Auto-Dev)

分支：feature/auto-dev-mode

## 运行态（冷启动必读）

current_phase: vnext-foundation
current_ticket: t24
current_status: ready
last_action: 2026-03-08 已完成 PM 自治优化 PRD / 设计文档，并创建 t24-t28 五张粗粒度实现票
next_action: 从 t24 开始推进任务运行态可信化，再依赖解锁后续 tickets
blocker: 无

## 语义边界（避免自指混淆）

- **当前代 dalek**：指现在这套正在运行、用于开发本仓库的 PM/worker/ticket 系统。它是“开发执行器”。
- **下一代 dalek**：指本仓库未来要实现的、更强的自治开发系统。它是“被开发的产品”。
- **本计划中的“PM”默认指当前代 dalek 的 PM agent**，负责推动代码库朝“下一代 dalek”演进。
- **“真实验收场景”指下一代 dalek 面向真实用户的产品能力**，不是当前代 dalek 自身的调试脚本。
- **因此要避免两层语义混淆**：
  - 当前代 dalek 可以用 CLI / tmux / worktree / 浏览器自动化去开发代码。
  - 下一代 dalek 的验收标准，必须站在真实用户视角验证产品功能是否完成。

## 核心标准

- **ticket + worker 的主标准**：以测试 / 构建 / lint / 静态检查为主，确保单 ticket 产出在工程上可合入。
- **PM 的主标准**：以真实场景验收为主，测试 / 构建只是辅助手段，不能替代真实产品验证。
- **判定边界**：
  - worker 可以证明“这段代码在工程层面看起来正确”
  - PM 必须证明“这个需求在真实用户场景里真的完成了”
- **因此**：
  - worker 完成 != feature 完成
  - go test 通过 != PM 可以宣布完成
  - 只有真实场景通过，PM 才能给出最终验收结论

## 停止条件

- **单个 ticket 停止条件**：worker 已完成必要测试 / 构建 / lint，代码具备工程可合入性。
- **单个 feature 停止条件**：PM 已完成真实场景验收，并留下可审计 evidence。
- **整轮 next-gen dalek 目标停止条件**：PM 已能围绕真实产品需求，自主生成文档、自维护状态、拆解并推进依赖 ticket，并最终通过真实场景完成验收。

## vNext 目标能力

### 0. 需求文档与设计文档先行
- PM 接收用户需求后，先产出**需求文档**和**设计文档**，再进入开发 ticket 拆解。
- 文档必须能独立回答：
  - 为什么做
  - 目标用户是谁
  - 范围与非目标是什么
  - 关键交互、架构、风险、验收方式是什么
- 文档不是附属物，而是后续 ticket 拆解、验收、续跑恢复的主上下文。

### 1. PM 自维护自身状态，且可随时冷启动续跑
- PM 需要像 worker 一样维护自己的状态面，而不是只依赖“脑内上下文”。
- 目标状态面建议参照 worker 的三层模型：
  - **Docs**：`.dalek/pm/plan.md`，记录语义状态、目标、依赖、验收口径。
  - **State**：新增结构化 `.dalek/pm/state.json`，记录 current phase、open features、tickets、依赖、acceptance progress、last decision、next action。
  - **Evidence**：新增 `.dalek/pm/acceptance.md` 或等价结构化记录，保存真实验收过程、截图/日志路径、失败判据与结论。
- 要求：任何时刻中断后，PM 重新启动都能只靠这些状态面恢复工作，而不是依赖对话历史。

### 2. 能拆成多个有依赖关系的 tickets，并持续推进
- PM 要能把一个 feature 拆成多个 ticket。
- ticket 之间可以有显式依赖、批次关系、并行关系。
- PM 需要能：
  - 创建 ticket
  - 描述清楚交付物与验收标准
  - 识别哪些 ticket 可以并行
  - 在前置 ticket 完成后自动启动下游 ticket

### 3. PM 能自主验收并启动新 ticket，直到需求完成
- worker 完成单 ticket 后，PM 不只是看 report，而是要主动验收结果。
- 验收通过：
  - 处理 merge
  - 关闭本 ticket
  - 解锁并启动后续 ticket
- 验收不通过：
  - 明确失败原因
  - 生成新 ticket 或 redispatch 原 ticket
- 直到整个 feature 完整交付，PM 才能宣布结束。

### 4. PM 必须跑真实场景，而不是只跑 go test / build
- `go test ./...`、`go build`、lint 只能作为基础守门，不是最终验收。
- 最终验收必须是**真实产品场景**：
  - 真正启动系统
  - 真实访问页面 / API / CLI
  - 按用户路径操作
  - 观察结果是否满足需求
- 只有真实场景跑通，PM 才能判定“需求完成”。

## 终极 E2E 验收标准

在一个真实 repo 上：
- 输入一个真实 feature 需求。
- PM 先产出需求文档和设计文档。
- PM 自维护 plan/state/evidence，并能在中断后恢复。
- PM 自主拆解、调度、验收、merge 所有 ticket。
- PM 能管理 ticket 依赖，并按批次推进。
- PM 能处理失败（redispatch / 方案调整 / 新 ticket）。
- PM 最终通过真实产品场景验收需求，而不是只看单元测试。
- Feature 完整交付，代码 merged，功能对真实用户可用。
- 全程零人工干预

---

## Planner Agent 操作手册

当 planner agent 被唤醒时，请按以下优先级处理：

### PM 角色边界（硬约束）
- PM 负责拆解、调度、验收、merge 和证据沉淀，不直接实现 ticket 对应的产品代码。
- 除 `.dalek/pm/*`、需求/设计文档、验收记录，以及 merge 集成动作外，PM 不得直接修改 `cmd/`、`internal/`、`web/`、测试文件或其他功能实现文件。
- 一旦发现自己正准备直接写功能代码，必须立刻停止，改为创建 / dispatch / redispatch 合适的 worker ticket。
- 如果 `git merge` 在产品文件上产生冲突，PM 必须立刻 `git merge --abort`，保持主线干净，然后创建 integration ticket 交给 worker 处理；PM 自己不能手工解冲突。

### 0. 若当前 feature 尚无需求/设计文档，先补文档
- 先补齐需求文档与设计文档，再拆 ticket。
- 文档未完成前，不进入大规模开发派发。

### 1. 检查 inbox 阻塞项（最高优先）
- `dalek inbox ls` 查看待处理项
- severity=blocker 的必须优先解决
- 能自行解决的直接处理后 `dalek inbox close --id N`

### 2. 处理待合并项
- `dalek merge ls` 查看合并队列
- 对 status=proposed 的 merge：
  a. 先按 worker 标准检查工程质量：`cd <worktree> && go test ./... && go build ./cmd/dalek`
  b. 检查 diff：`git diff HEAD...<branch> --stat`
  c. 再按 PM 标准执行当前 feature 定义中的**真实验收场景**
  d. 只有真实场景通过，才允许：`dalek merge approve --id N` → `git merge <branch> --no-edit` → `dalek merge merged --id N`
  e. 验收不通过：`dalek merge discard --id N` → 创建新 ticket 附失败原因
- 若 `git merge` 发生产品文件冲突：`git merge --abort` → 创建 integration ticket（描述冲突文件、两侧分支、需要保留的行为）→ `dalek ticket dispatch --ticket N`

### 3. 推进 feature 进度
- 读取下方【当前 Feature】定义
- 查看已有 tickets 和依赖关系
- 前置 ticket 已 merged → 创建并 dispatch 下游 ticket
- 有空闲 capacity → dispatch 可并行的 ticket

### 4. 创建和 dispatch 新 ticket
- `dalek ticket create --title "..." --description "..." --priority 3`
- `dalek ticket dispatch --ticket N`
- ticket description 必须包含：目标、具体交付物、约束条件、验收标准
- 一次最多创建 2-3 个 ticket，避免过度前瞻

### 5. 本轮收口
- 确认所有动作已执行
- 给出本轮状态总结

---

## 实现路径

### P1: Planner Loop 基础闭环 [done]

commits: 83d7e93, 1031385, 8ebe636, 6dfba59, 507493e, bd72022
E2E 验收: 2026-03-07 merge proposed → dirty=true → planner run scheduled → run completed → state updated → cooldown set

修复的关键 bug (t7 bd72022):
- 根因：ManagerTick 的 finalize 阶段使用了可能被 scheduleQueuedTickets 取消的 ctx
- 修复：context.WithoutCancel + explicit Updates(map[string]any{...}) 代替 db.Save()

### P2: Planner 决策引擎 [done]

commit: 1e03c8c (t10, merged via merge#8)
实现：
- planner stub 替换为真实 sdkrunner agent 执行
- prompt 构建：plan.md + ticket/merge/inbox 快照
- 事件流回调 + 结果持久化
- timeout/cancellation 处理
- 测试通过，build clean

### P3: 并行调度与依赖管理 [verified-by-infrastructure]

基础设施已验证完备：
- 并行 dispatch：scheduleQueuedTickets 使用 capacity 自动调度
- 自动触发：merge proposed → planner dirty → planner wakes up
- 依赖管理：通过 plan.md 中的依赖定义 + planner agent 决策
- 无需额外代码，P4 测试中验证实际效果

### P4: E2E 验证 — 自主交付真实 Feature [done]

目标：planner agent 自主交付下方定义的 test feature，全程零干预

验收标准：
- planner 自主创建 3+ tickets
- planner 自主 dispatch tickets 给 workers
- planner 自主验收 worker 产出（test + build）
- planner 自主 merge 验收通过的 ticket
- 管理依赖顺序（前置 merged 后才 dispatch 下游）
- feature 功能完整可用
- 全程零干预

---

## 当前分支对照评估

目标能力对照：

| 目标 | 当前分支情况 | 结论 |
|---|---|---|
| 0. 自动产出需求文档和设计文档 | 当前 workspace 已补上 web 管理页面的 PRD / design 初始骨架，但这仍是人工落盘，不是 PM 主流程自动生成。当前分支会把 `.dalek/pm/plan.md` 提供给 planner，当作上下文输入；repo 里也有 note/shaping 和 worker `PLAN.md` 模板，但 branch 还没有把“PRD + design doc 生成”做成 PM 主流程。 | **部分完成** |
| 1. PM 像 worker 一样自维护状态并可续跑 | 当前分支已实现 DB 中的 `PMState`、planner dirty/wake/cooldown/active run 持久化，也有 daemon recovery。本轮新增了 `.dalek/pm/state.json`、`dalek pm state sync/show`，并把结构化状态接进 planner prompt；但 acceptance evidence 与更细粒度 feature/ticket 依赖状态还没有完全自动维护。 | **部分完成** |
| 2. 多 ticket + 依赖推进 | 当前分支已能创建 planner run、自动调度 queued tickets、利用 capacity 并行 dispatch；在实际演示中也完成了 t11/t12 -> t14 的依赖推进。但依赖关系仍主要靠 `plan.md` + planner 推理，没有系统级 dependency graph。 | **部分完成** |
| 3. PM 自主验收、继续开下游 ticket，直至完成 | 当前分支能对 done ticket 自动提 merge，planner 也可基于 merge/ticket/inbox 状态继续推进，t14 证明了这条链路能跑通；但“feature complete”的判定仍主要依赖 planner prompt 和人工写入的 plan。 | **部分完成** |
| 4. PM 通过真实场景验收，不只看 go test/build | 当前分支的验收主路径仍以 `go test`、`go build`、CLI 断言为主；缺少系统化的“真实用户场景 runner”，也没有把浏览器/API/端到端交互沉淀成 PM 的标准 acceptance 流。 | **未完成** |

分支已实现的关键基础设施：
- planner run 持久化与恢复：`PMState` + dirty/wake/cooldown/active run
- daemon 自动提交 planner run：读取 `.dalek/pm/plan.md` + ticket/merge/inbox 快照构造 prompt
- queued tickets 自动并发调度
- done ticket 自动生成 merge proposal
- planner agent 已从 stub 变成真实 sdkrunner 执行

分支当前缺失的关键能力：
- PM 主流程自动生成并维护需求文档 / 设计文档
- PM 主流程自动维护 acceptance evidence
- 系统级 ticket dependency graph
- 面向真实产品场景的自动验收框架

---

## 当前 Feature：优化 PM 自治编排与 plan 系统

### Feature 概述
基于 web 管理页面这个真实 case 暴露出的执行问题，优化当前代 dalek 的 PM 编排能力，使其具备更可信的状态维护、更稳定的恢复能力，以及以真实 acceptance 为核心的 feature 完成判定。

### 目标文档
- **需求文档**：`docs/product/pm-autonomy-hardening-prd.md`
- **设计文档**：`docs/architecture/pm-autonomy-hardening-design.md`

### 关键问题
- dispatch task 会出现“语义成功但终态失败”的矛盾事实
- worker terminal report 可以重复写入，导致同一 run 多次 `done`
- planner 缺少结构化 op journal 和 checkpoint，超时 / crash 后难以恢复
- `plan.md` 不是稳定真相源，容易与真实历史漂移
- acceptance 还不是 feature graph 里的硬 gate
- 并行改同一表面时缺少前置 integration 策略

### 优化目标
1. **任务生命周期可信**
- dispatch 结果、worker 结果、task terminal state 彼此一致
- 不再允许 terminal state 被后续写入覆盖

2. **planner 可恢复**
- planner 以结构化 `PMOps` 推进
- 每步有 journal、checkpoint 和幂等恢复

3. **plan 结构化**
- `plan.json` 成为 feature graph 真相源
- `plan.md` 只做渲染

4. **acceptance 一等化**
- 真实浏览器 / API / CLI 验收成为 feature gate
- 失败时自动生成 gap / integration ticket

5. **integration 风险前置**
- ticket 需要表达 `touch_surfaces`
- planner 提前决定串行 / 并行 / integration 策略

### 粗粒度 ticket 拆解

#### Batch A：状态可信与真相源
- **T-pm-runtime-ledger**
  - 目标：修 dispatch 生命周期、task terminal guard、duplicate done guard
  - 规模：约 700-1400 行
- **T-plan-graph-sot**
  - 目标：引入 `plan.json` feature graph，并实现 `plan.md` 渲染与状态同步
  - 规模：约 900-1600 行

#### Batch B：planner 可恢复编排
- **T-pm-oplog-recovery**
  - 目标：planner 输出 `PMOps`、journal、checkpoint、crash recovery
  - 规模：约 1200-2000 行

#### Batch C：acceptance 与并行治理
- **T-pm-acceptance-engine**
  - 目标：实现 acceptance nodes、真实 runner、evidence bundle、失败后 gap ticket 流
  - 规模：约 1000-1800 行
- **T-pm-integration-observability**
  - 目标：实现 `touch_surfaces`、integration 策略、merge churn / terminal conflict 健康指标
  - 规模：约 700-1400 行

### 依赖关系
```text
T-pm-runtime-ledger ───┐
                       ├──→ T-plan-graph-sot ───────┐
                       │                            ├──→ T-pm-oplog-recovery ──┐
                       └────────────────────────────┘                           ├──→ T-pm-acceptance-engine
T-plan-graph-sot ───────────────────────────────────────────────────────────────┤
T-pm-runtime-ledger ────────────────────────────────────────────────────────────┘

T-plan-graph-sot ───────────────────────────────→ T-pm-integration-observability
T-pm-runtime-ledger ────────────────────────────→ T-pm-integration-observability
```

### 执行 ticket 表（结构化）
| ticket | runtime_id | batch | depends_on | pm_state | deliverable |
| --- | --- | --- | --- | --- | --- |
| T-pm-runtime-ledger | t24 | Batch A | - | backlog / ready | dispatch 生命周期拆分、task terminal guard、duplicate worker done guard、冲突事件与测试 |
| T-plan-graph-sot | t25 | Batch A | T-pm-runtime-ledger | backlog | 引入 `plan.json` feature graph、`plan.md` 渲染模板、state/evidence 同步链 |
| T-pm-oplog-recovery | t26 | Batch B | T-pm-runtime-ledger, T-plan-graph-sot | backlog | planner 结构化 `PMOps`、journal、checkpoint、daemon/planner 恢复逻辑 |
| T-pm-acceptance-engine | t27 | Batch C | T-plan-graph-sot, T-pm-oplog-recovery | backlog | acceptance nodes、真实 runner、evidence bundle、失败后 gap ticket 自动生成 |
| T-pm-integration-observability | t28 | Batch C | T-pm-runtime-ledger, T-plan-graph-sot | backlog | `touch_surfaces`、integration 策略、自治健康指标与展示 |

### Feature 完成标准
1. 不再出现 `dispatch summary=success` 但 `task failed` 的冲突 run。
2. 同一 worker run 的 `done` 只能生效一次。
3. planner 超时 / daemon 重启后能从 journal + checkpoint 自动恢复。
4. `plan.json` 成为真相源，`plan.md` 从其渲染。
5. acceptance 失败会自动生成 gap / integration ticket，并留下 evidence。
6. 再用一个真实 feature 回归时，全程零人工接管。

---

## 已完成 Feature：为 dalek 开发 Web 管理页面（真实产品验收场景）

### Feature 概述
为 dalek 开发一个真实可用的 web 管理页面，让用户可以在浏览器中查看并管理项目状态，而不是只能依赖 CLI / TUI。

### 为什么选这个场景
- 这是一个真实产品需求，不是内部调试功能。
- 它天然要求跨多 ticket 协作：需求文档、设计文档、后端 API、前端页面、状态联动、真实浏览器验收。
- 它能很好地检验“下一代 dalek 是否真的能从需求走到交付”。
- 它能清楚地区分：
  - 当前代 dalek：开发这套功能的执行者
  - 下一代 dalek：交付给真实用户使用的 web 产品能力

### 必须先产出的文档
- **需求文档**：建议路径 `docs/product/web-console-prd.md`
- **设计文档**：建议路径 `docs/architecture/web-console-design.md`

需求文档至少回答：
- 目标用户：谁会使用这个 web 页面
- 核心任务：用户要在页面上完成什么操作
- MVP 范围：第一版必须支持什么，不支持什么
- 成功标准：什么叫“web 管理页面可用”

设计文档至少回答：
- 页面结构和信息架构
- API / data flow
- 认证与权限策略
- 前后端技术方案
- 风险与降级策略
- 真实验收方案

### MVP 功能需求
第一版 web 管理页面至少包含以下模块：

1. **项目概览页**
- 展示 ticket 概览、worker 利用率、planner 状态、merge 队列、inbox 状态。

2. **Tickets 页面**
- 列表查看 ticket
- 查看 ticket 详情
- 能识别 backlog / queued / active / blocked / done / archived 状态

3. **Merges / Inbox 页面**
- 查看 merge 队列
- 查看 inbox 待办
- 至少支持基础审批/处理入口的展示

4. **Planner / Runtime 页面**
- 展示 planner dirty、active run、last error、last run 等运行态
- 让用户知道系统当前是否在自动推进

5. **最小可用交互**
- 页面能真实访问
- 数据能真实从 dalek backend 读取
- 至少有一个真实操作链路能跑通（例如创建 ticket 后页面可见，或 approve merge 后状态变化可见）

### Ticket 拆解建议

#### Batch A：文档与状态设计
- **T-web-prd**：产出 web 管理页面需求文档
- **T-web-design**：产出 web 管理页面设计文档
- **T-pm-state-design**：设计 PM 自维护状态面（`.dalek/pm/plan.md` + `.dalek/pm/state.json` + acceptance evidence）

#### Batch B：后端读模型 / API
- **T-web-api-overview**：提供 dashboard / planner / merge / inbox 聚合 API
- **T-web-api-ticket**：提供 ticket list/detail API

#### Batch C：前端骨架
- **T-web-ui-shell**：页面路由、布局、导航、基础样式
- **T-web-ui-overview**：项目概览页

#### Batch D：功能页
- **T-web-ui-ticket**：tickets list/detail 页面
- **T-web-ui-runtime**：planner/runtime 页面
- **T-web-ui-merge-inbox**：merge / inbox 页面

#### Batch E：真实验收
- **T-web-real-acceptance**：启动真实系统，跑浏览器场景，沉淀 acceptance evidence

### 依赖关系
```text
T-web-prd ───────┐
T-web-design ────┼──→ T-web-api-overview ──┐
T-pm-state-design ┘                         ├──→ T-web-ui-overview ─┐
T-web-design ─────────────────────────────→ T-web-api-ticket ───────┼──→ T-web-real-acceptance
T-web-design ─────────────────────────────→ T-web-ui-shell ─────────┤
T-web-ui-shell ───────────────────────────→ T-web-ui-ticket ────────┤
T-web-ui-shell ───────────────────────────→ T-web-ui-runtime ───────┤
T-web-ui-shell ───────────────────────────→ T-web-ui-merge-inbox ───┘
```

### 执行 ticket 表（结构化）
| ticket | batch | depends_on | pm_state | deliverable |
| --- | --- | --- | --- | --- |
| T-web-prd | Batch A | - | drafted | 产出 web 管理页面需求文档并沉淀用户任务、MVP 范围、成功标准 |
| T-web-design | Batch A | T-web-prd | drafted | 产出 web 管理页面设计文档并明确 IA、数据流、风险和真实验收方案 |
| T-pm-state-design | Batch A | T-web-prd, T-web-design | in_progress | 把 PM 的 feature/ticket/acceptance 状态纳入 `.dalek/pm/state.json` 并可续跑 |
| T-web-api-overview | Batch B | T-web-prd, T-web-design, T-pm-state-design | planned | 提供 overview / planner / merge / inbox 聚合 API |
| T-web-api-ticket | Batch B | T-web-prd, T-web-design | planned | 提供 ticket list/detail API |
| T-web-ui-shell | Batch C | T-web-design | planned | 提供 web app shell、导航、布局和基础样式 |
| T-web-ui-overview | Batch C | T-web-api-overview, T-web-ui-shell | planned | 实现 overview 页面并渲染真实项目状态 |
| T-web-ui-ticket | Batch D | T-web-api-ticket, T-web-ui-shell | planned | 实现 tickets list/detail 页面 |
| T-web-ui-runtime | Batch D | T-web-api-overview, T-web-ui-shell | planned | 实现 planner/runtime 页面 |
| T-web-ui-merge-inbox | Batch D | T-web-api-overview, T-web-ui-shell | done (delivered via t19) | 实现 merge / inbox 页面 |
| T-web-real-acceptance | Batch E | T-web-ui-overview, T-web-ui-ticket, T-web-ui-runtime, T-web-ui-merge-inbox | done | 启动真实系统、跑浏览器路径、记录 acceptance evidence 并给出 PM 结论 |

### 真实验收标准（核心）
以下流程必须由 PM 自己执行，并留下可审计证据；只要任一步未通过，就不能宣布 feature 完成：

1. 启动真实 dalek 服务，而不是只运行测试。
2. 在浏览器中打开 web 管理页面。
3. 看到概览页真实展示项目状态。
4. 进入 tickets 页面，确认 ticket 列表和详情可用。
5. 进入 planner/runtime 页面，确认 planner 状态真实展示。
6. 进入 merge / inbox 页面，确认对应数据真实展示。
7. 至少完成一个真实用户操作链路，并观察状态变化：
   - 例如创建 ticket 后页面刷新可见；
   - 或处理 merge/inbox 后页面状态变化可见。
8. 将验收过程记录到 acceptance evidence 中，包含：
   - 使用的启动命令
   - 访问 URL
   - 关键页面截图或快照
   - 关键操作步骤
   - 最终结论

### 明确禁止
- 不允许只用 `go test ./...`、`go build`、snapshot unit test 就宣布完成。
- 不允许把“CLI 输出正确”替代“真实 web 页面可用”。
- 不允许把当前代 dalek 的内部调试信息误当成下一代 dalek 的真实产品能力。

---

## 已完成 tickets / merges

| ticket | 内容 | merge | 状态 |
|--------|------|-------|------|
| t7 | P1 planner_dirty persistence fix | - | merged (bd72022) |
| t10 | P2 planner run real agent execution | merge#8 | merged (1e03c8c) |
| t11 | P4 CLI 命令骨架 (pm dashboard) | merge#10 | merged |
| t12 | P4 Dashboard 数据聚合逻辑 | merge#9 | merged |
| t13 | P4 fix: mark planner dirty for open merges | merge#12 | merged |
| t14 | P4 渲染层 + CLI 集成 (pm dashboard) | merge#13 | merged (cd726d2) |

## 决策记录

### 03-07: 需求目标重新定义
- 原目标：planner loop 技术闭环
- 新目标：PM 自主完成大型 feature 全生命周期交付，零干预

### 03-07: dispatch 权限根因
- Claude SDK 的 WithCanUseTool 要求 QueryStream 而非 Query

### 03-07: 状态控制机制
- plan.md 运行态区块 + 状态机协议 + 验收流程

### 03-08: P2 完成，P3 基础设施验证
- P2 planner agent 实际执行已实现并 merged
- P3 并行调度基础设施已在 scheduleQueuedTickets + plannerDirty 机制中完备
- 进入 P4 E2E 验证：定义 `dalek pm dashboard` 作为测试 feature
- planner agent 需自主拆解、调度、验收、merge 所有 tickets

### 03-08: P4 E2E Feature 交付完成
- Feature: `dalek pm dashboard` 全量交付
- Batch 1 并行: t11(CLI骨架) + t12(数据聚合) → 同时 dispatch, 分别 merged
- 中间修复: t13(planner dirty bug) → merged
- Batch 2 依赖: t14(渲染层+集成) → 前置 merged 后 dispatch, 验收通过, merged
- 验收结果: text/json 双格式输出正确, go test 通过, go build 通过
- P4 验收标准达成: 3+ tickets 自主创建/dispatch/验收/merge, 依赖管理正确, 功能完整可用

### 03-08: PM 自治优化 feature 启动
- 已产出 `docs/product/pm-autonomy-hardening-prd.md`
- 已产出 `docs/architecture/pm-autonomy-hardening-design.md`
- 已创建第一批粗粒度实现票：
  - t24 `fix(pm): harden task lifecycle and dispatch ledger`
  - t25 `feat(pm): introduce feature graph source of truth`
  - t26 `feat(pm): add planner PMOps journal and recovery`
  - t27 `feat(pm): add acceptance engine and evidence flow`
  - t28 `feat(pm): add integration planning and health metrics`
- 执行顺序确定为：先修事实一致性，再推进结构化 plan、恢复能力和 acceptance
