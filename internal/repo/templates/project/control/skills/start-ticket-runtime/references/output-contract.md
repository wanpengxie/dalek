# Output Contract

本 skill 不写文件化 receipt。

本 skill 的权威产物是目标 worktree 中的三份上下文文件：

- `.dalek/agent-kernel.md`
- `.dalek/PLAN.md`
- `.dalek/state.json`

约束：

1. skill 只负责准备 worker 上下文文件。
2. runtime 接单与执行结果由后续 `worker run` / `deliver_ticket` 链路在数据库中统一记录。
