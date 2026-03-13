# Worker Kernel Bootstrap 施工文档

## 目标

把 worker bootstrap 从“直接使用内置 seed 模板生成 worktree 运行文件”，迁移到“项目级 `control/worker` 模板源 -> worktree 运行时投影”的模型。

## 本轮范围

本轮先落最小闭环：

1. 项目初始化时 seed `control/worker/worker-kernel.md`
2. 项目初始化时 seed `control/worker/state.json`
3. worker bootstrap 优先从这两个项目模板读取
4. bootstrap 时投影到 worktree，并替换最小运行时变量

## 已改动的代码点

- `internal/repo/control_worker.go`
  - 新增 `control/worker` 模板路径和读取函数
  - 新增 `ensureControlWorkerTemplates` / `planControlWorkerTemplateChanges`
- `internal/repo/control_seed.go`
  - init/upgrade 时自动创建 `control/worker` 模板
- `internal/repo/templates/project/control/worker/worker-kernel.md`
  - 新增 worker-kernel 初始模板
- `internal/repo/templates/project/control/worker/state.json`
  - 新增 worker state 初始模板
- `internal/services/pm/worker_bootstrap.go`
  - bootstrap 改为优先从项目里的 `control/worker` 读取模板
  - `state.json` 改为“读模板 + 替换变量”，不再由 Go 直接拼整份结构

## 当前行为

项目初始化后会存在：

- `.dalek/control/worker/worker-kernel.md`
- `.dalek/control/worker/state.json`

worker bootstrap 时会投影到：

- `worktree/.dalek/agent-kernel.md`
- `worktree/.dalek/state.json`

并替换：

- `ticket_id`
- `worker_id`
- `head_sha`
- `working_tree`
- `last_commit_subject`
- `updated_at`
- 以及 worker-kernel 中的 title / description / attachments / current_state

## 当前刻意没收口的地方

这轮只先接通主干，下面这些还保留旧实现：

1. `.dalek/agent-kernel.md` 的 worktree 文件名仍是历史命名，没有额外暴露 “worker-kernel runtime materialization” 概念

## 下一步施工建议

1. 统一术语
   - repo 侧统一叫 `worker-kernel`
   - worktree 侧保留 `.dalek/agent-kernel.md` 落地名，但在代码注释和文档里显式说明其来源

2. 收敛 ABI
   - 把 `state.json` 和 `worker report` 明确成 runtime ABI

3. 继续清理术语
   - 明确 `control/worker/*` 是模板源
   - 明确 `worktree/.dalek/*` 是运行时投影

4. 补测试
   - init/upgrade 对 `control/worker` 的覆盖
   - bootstrap 对项目级自定义模板的覆盖
   - 老项目缺失 `control/worker` 文件时的 fallback/repair 行为
