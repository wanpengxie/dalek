# MULTI_NODE_V2 Final Acceptance

本文件用于最终验收 `MULTI_NODE_V2` 的用户主链，而不是底层单点能力。

验收目标：

- 用户从 A 的统一入口进入系统
- 开发请求可以稳定进入 B
- 关键验证请求可以稳定进入 C
- A 能持续展示角色路由、路由原因、运行结果与后续查询入口

## 1. 前置条件

验收前应满足：

- A/B/C 对应 daemon 已启动
- A 已完成项目配置：
  - `multi_node.auto_route=true`
  - `multi_node.dev_base_url`
  - `multi_node.run_base_url`
- 项目已存在可用 `ticket`
- 当前 `dalek` 二进制已是最新构建产物

推荐先人工确认：

1. `./dalek daemon status`
2. `./dalek node ls`
3. `./dalek task ls --all`

## 2. Smoke Harness

仓库提供一个 smoke harness：

```bash
go run ./tools/multinode_smoke \
  --dalek ./dalek \
  --project demo \
  --ticket 12 \
  --dev-prompt "继续开发并修复最近失败" \
  --verify-target test
```

这个工具会顺序验证：

1. `daemon status`
2. `node ls`
3. `task request --prompt ...`
4. `task show --id ...`
5. `task events --id ...`
6. `task request --verify-target ...`
7. `task show --id ...`
8. `task events --id ...`
9. `run show --id ...`
10. `run logs --id ...`
11. `run artifact ls --id ...`

通过标准：

- 输出最后一行是 `SMOKE_OK`
- `dev` 请求返回 `role=dev`
- `run` 请求返回 `role=run`
- `task events` 中包含 `task_request_routed`
- `task show` 中能看到 `role_source` 与 `route_reason`

## 3. 单机场景验收

适用场景：

- A/B/C 同机
- 用于验证最小闭环与统一入口

建议命令：

```bash
go run ./tools/multinode_smoke \
  --dalek ./dalek \
  --project demo \
  --ticket 12 \
  --verify-target test
```

补充人工检查：

1. `./dalek task show --id <run_id>`
2. `./dalek run show --id <run_id>`
3. `./dalek run logs --id <run_id>`

通过标准：

- 统一入口可提交
- `task show` 可见角色与路由
- `run show/logs/artifact` 能继续观测

## 4. 双机场景验收

适用场景：

- A+B 同机
- C 独立

建议命令：

```bash
go run ./tools/multinode_smoke \
  --dalek ./dalek \
  --project demo \
  --ticket 12 \
  --dev-prompt "继续开发并修复最近失败" \
  --verify-target test
```

补充人工检查：

1. `./dalek task show --id <dev_task_run_id>`
2. `./dalek task show --id <run_task_run_id>`
3. `./dalek run show --id <run_task_run_id>`

通过标准：

- 开发请求走到 B
- 验证请求走到 C
- A 上两条记录都能看到 `role_source` 和 `route_reason`

## 5. 三机场景验收

适用场景：

- A 控制面
- B 开发面
- C 运行面

建议顺序：

1. 从 A 提交开发请求
2. 在 A 观察开发任务的 `task show/events`
3. 从 A 提交验证请求
4. 在 A 观察 `run show/logs/artifact`
5. 验证失败后，再次从 A 提交开发请求

建议命令：

```bash
go run ./tools/multinode_smoke \
  --dalek ./dalek \
  --project demo \
  --ticket 12 \
  --dev-prompt "继续开发并处理刚才的验证失败" \
  --verify-target test
```

补充人工检查：

1. `./dalek task events --id <dev_task_run_id>`
2. `./dalek task events --id <run_task_run_id>`
3. `./dalek run show --id <run_task_run_id> -o json`
4. `./dalek task ls --all`
5. `./dalek tui`

通过标准：

- 用户只从 A 入口操作即可完成开发 -> 验证 -> 再开发
- A 上能看到 B/C 的角色切换与原因
- C 的失败事实能回流到 A，并支撑下一次 B 开发请求

## 6. 最终验收问题

只有以下问题都能回答“是”，才算通过最终验收：

1. 用户是否只需要理解 A 的入口，而不需要直接操作底层 node 命令？
2. 开发请求是否默认进入 B，验证请求是否默认进入 C？
3. A 是否能解释“为什么这次进入 B / 为什么这次进入 C”？
4. 同一条 ticket/task 语义链上，是否能看到开发、验证、失败回流与再次开发？
5. CLI、gateway、TUI 三个入口是否共享同一套路由与观测语义？
