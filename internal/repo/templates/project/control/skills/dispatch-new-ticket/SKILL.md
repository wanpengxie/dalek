---
name: dispatch-new-ticket
description: 生成或修复一次 ticket dispatch 的 worker 执行上下文（.dalek/agent-kernel.md、PLAN.md、state.json）并执行三阶段校验。用于首次 dispatch、上下文漂移修复、或手动重建 worker handoff 的场景。
---

# Dispatch New Ticket

本 skill 强制三阶段执行，不跳步：

1. `initialize`：收集输入并导入模板产物
2. `edit`：按 ticket 语义编辑关键区块
3. `validate`：校验结构、一致性和可执行性

模板资产：

- `assets/worker-agents.md.template`
- `assets/plan.md.template`
- `assets/state.json.template`

输入（来自环境变量）：

- `DALEK_WORKTREE_PATH`
- `DALEK_TICKET_ID`
- `DALEK_WORKER_ID`

硬约束：

1. 先完成 `initialize`，再进入 `edit`。
2. `validate` 不通过时，必须输出 `blocked` 结论，不得伪装成功。
3. 只编辑和 dispatch 相关的 worker 上下文文件，不改业务代码。
4. `initialize` 阶段必须按模板映射复制文件，不引入额外脚本依赖。
5. `initialize` 只复制，不替换任何 placeholder。
6. `edit` 阶段必须先使用 subagent 深度探索代码库，再进入需求细化与方案编写；禁止跳过探索直接写计划。


## Phase 1: initialize

目标：把模板文件稳定落到目标目录，建立最小工作面。

执行步骤：

1. 校验 `DALEK_WORKTREE_PATH` 非空。
2. 手动执行复制任务：
- `.dalek/control/skills/dispatch-new-ticket/assets/worker-agents.md.template` -> `$DALEK_WORKTREE_PATH/.dalek/agent-kernel.md`
- `.dalek/control/skills/dispatch-new-ticket/assets/plan.md.template` -> `$DALEK_WORKTREE_PATH/.dalek/PLAN.md`
- `.dalek/control/skills/dispatch-new-ticket/assets/state.json.template` -> `$DALEK_WORKTREE_PATH/.dalek/state.json`

3. 检查目标文件存在：
  - `agent-kernel.md`
  - `PLAN.md`
  - `state.json`
4. 检查 `state.json` 可解析。
5. 保持 placeholder 原样，不做语义替换。

阶段退出条件：

1. 复制步骤执行成功。
2. 上述三个文件存在。
3. `state.json` 可解析。
4. 模板 placeholder 仍存在（例如 `{{TICKET_TITLE}}`）。


## Phase 2: edit

目标：基于深度探索结果完成高质量需求细化与设计规划，而不是只做占位替换。

执行步骤：

1. 启动 subagent（opus）执行代码库深度探索，至少覆盖：
  - 与 ticket 相关的现有实现、调用链路、数据流和关键文件。
  - 现有行为边界、历史约束、潜在回归点和测试现状。
  - 可行方案候选及主要 trade-off。
2. 汇总 subagent 结论，先形成“需求理解与边界定义”，再进入文档编辑。
3. 编辑 `agent-kernel.md`：填充身份、执行循环、`<state_update_protocol>` 与 `<current_state>`，并保持 report 约束。
4. 在 `agent-kernel.md` 的 `<task_context>` 替换四个字段占位：`title`、`description`、`attachments`、`plan_ref`。
5. 编辑 `PLAN.md`：必须包含高密度规划内容（顺序不限）：
  - 需求细化：目标、范围边界、非目标、关键约束、成功标准。
  - 现状分析：当前实现、差距、风险点、依赖项。
  - 方案设计：候选方案对比、最终方案与决策理由。
  - 实施规划：分阶段步骤、里程碑、回滚/兜底策略。
  - 验证计划：测试与验收方法、可观测信号、失败判据。
  - 待澄清问题：需要用户确认的关键决策点。
6. 编辑 `state.json`：设置结构化状态（`phases`、`blockers`、`code`、`updated_at`），并与 `<current_state>` 文本摘要语义同步。

阶段退出条件：

1. 不存在关键 placeholder（除允许保留字段外）。
2. `state.json` 可解析。
3. `PLAN.md` 已完成需求探索与执行规划，覆盖问题定义、目标、范围边界、关键方案、风险和验证口径（顺序不限）。
4. `PLAN.md` 包含“现状分析 + 方案对比 + 决策理由 + 分阶段实施 + 验证计划”。
5. `PLAN.md` 中的待澄清问题已显式列出并标注影响范围。
6. `PLAN.md` 保持叙事文档形态，不包含 JSON/YAML 或结构化状态键块（例如 `phases.current_id`、`phases.next_action`、`blockers`）。
7. `agent-kernel.md` 的 `<task_context>` 四字段占位已被业务语义内容替换（不残留 `{{...}}`）。
8.  `agent-kernel.md` 中 `<state_update_protocol>` 与 `<current_state>` 块存在且内容完整。
9.  `agent-kernel.md` 包含系统语义契约区块，且 `next_action` 定义固定为 `continue|done|wait_user`。

## Phase 3: validate

目标：确认产物结构正确且可被后续链路消费。

执行步骤：

1. 结构校验：三个文件都存在且 `state.json` 可解析。

阶段退出条件：

1. 校验通过时明确标记 `ready`。
2. 校验失败时明确标记 `blocked` 且列出问题。
