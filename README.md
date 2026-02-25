# dalek（v0）

用 `git worktree + tmux + sqlite` 管理“ticket -> worktree -> agent 会话”的最小闭环。

更长远的产品愿景见 `VISION.md`。

## 依赖

- Go 1.25+
- git（必须在一个 git 仓库内运行，且仓库至少有 1 个 commit）
- tmux

## 快速开始

在你的 git 仓库根目录（或任意子目录）：

```bash
go build -o dalek ./cmd/dalek
./dalek init
./dalek
```

初始化后会生成：

- `.dalek/config.json`
- `.dalek/dalek.sqlite3`
- `.dalek/workers/`（每个 worker 的输出与调试工件：stream.log、task 快照等）
- `~/.dalek/worktrees/<project-name>/tickets/`（每个 ticket 一个 worktree；可用 `-home` 或 `DALEK_HOME` 改位置）

## 核心概念与状态

dalek 的“最小单元”是 `ticket`，但运行与观测会拆成三层状态，避免混淆：

1. `Ticket.Status`（管理/流程状态）
   - `backlog` 待办
   - `queued` 排队
   - `blocked` 阻塞
   - `running` 进行中
   - `done` 完成
2. `Worker.Status`（tmux 生命周期）
   - `creating / running / stopped / failed`
3. `Worker.RuntimeState + NeedsUser + Summary`（业务运行态，由 watcher/agent 推断并写回 DB；report 为主，tmux 观测为辅）
   - `idle` 空闲（通常是 shell prompt、近期无输出推进）
   - `running` 运行中（输出/状态在推进）
   - `waiting_user` 等待输入（明确卡在交互输入/确认）
   - `error` 错误（尾部/语义明显错误）
   - `stopped / unknown`

TUI 里的两列分别对应：

- `状态`：Ticket 的管理状态
- `运行`：Worker 的业务运行态（含 needs_user）

## TUI 操作

在列表页（table）：

- `n` 新建 ticket
- `s` 启动（创建 worktree + tmux session）
- `p` 派发任务（`a` 自动 / `m` 手动；会写入 worktree 的 `.dalek/task.md`，并在 auto 模式启动 agent）
- `i` 软中断（向目标 pane 发送 `Ctrl+C`，不 kill session）
- `a` attach 进入会话（detach 后回到 TUI）
- `k` 停止（kill 对应 tmux session）
- `d` 归档（archive：不删除记录，默认列表不显示）
- `r` 观测（对选中 ticket 运行一次 watcher，并刷新列表）
- `e` 编辑标题/描述（`Ctrl+S` 保存，`Esc` 返回）
- `v` 查看事件时间线（`r` 刷新，`Esc` 返回）
- `+/-` 调整优先级
- `0-4` 设置管理状态：`0/1 backlog`、`2 queued`、`3 blocked`、`4 done`
- `t` 手动切换浅色/深色配色（部分终端主题下自动检测不可靠）
- `q` 退出

## CLI 使用（noun-verb）

`dalek` CLI 使用统一的 noun-verb 结构：

```bash
dalek <noun> <verb> [flags]
dalek init
dalek tui
dalek            # 默认启动 TUI
```

全局参数（放在 noun 前）：

- `--home`：dalek Home（默认 `~/.dalek`）
- `--project`, `-p`：项目名（默认按当前目录推断）
- `--output`, `-o`：输出格式 `text|json`（查询命令支持 `json`）
- `--agent-provider`：全局覆盖 agent provider（`codex|claude`）
- `--agent-model`：全局覆盖 agent model

命令分组总览：

- `ticket`：`ls/create/show/start/dispatch/interrupt/stop/archive/events`
- `note`：`add/ls/show/approve/reject/discard`
- `task`：`ls/show/events/cancel`
- `manager`：`status/tick/run/pause/resume`
- `inbox`：`ls/show/close/snooze/unsnooze`
- `merge`：`ls/propose/approve/merged`
- `worker`：`report/run`
- `agent`：`finish`
- `project`：`ls/add/rm`
- `tmux`：`sockets/sessions/prune-sockets/kill-server/kill-session/kill-prefix`
- `gateway`：`serve/chat/ingress/send/bind/unbind/ws-server`
- `daemon`：`start/stop/restart/status/logs`

完整命令树：

```bash
dalek --help
dalek ticket --help
dalek ticket create --help
```

### daemon 模式与执行语义（重要）

`dalek ticket dispatch` 和 `dalek worker run` 在当前版本中默认是**异步**：

- 命令提交到 daemon 后立即返回 accepted 回执，不会阻塞到任务完成。
- 如需阻塞执行，请显式传 `--sync --timeout`。

异步回执（text）示例：

```bash
dalek ticket dispatch --ticket 12 --prompt "先补测试再改代码"

dispatch accepted: ticket=12 worker=7 request=req_01J... run=184
query: dalek task show --id 184
events: dalek task events --id 184
cancel: dalek task cancel --id 184
```

异步回执（json）示例（关键字段）：

```bash
dalek ticket dispatch --ticket 12 -o json
# schema: dalek.ticket.dispatch.accepted.v1
# mode: async
# accepted: true
# request_id: req_01J...
# query.show/query.events/query.cancel: task 查询命令
```

daemon 不在线时（默认异步路径）会报错，并提示同步兜底用法：

```text
Error: daemon 不在线，无法异步派发
Cause: <daemon 连接错误详情>
Fix: daemon unavailable: 请先执行 `dalek daemon start`
如需同步执行（会阻塞当前终端），可使用：
  dalek ticket dispatch --ticket 12 --sync --timeout 120m
```

同步兜底（显式阻塞）：

```bash
dalek ticket dispatch --ticket 12 --sync --timeout 120m
dalek worker run --ticket 12 --sync --timeout 120m
```

如果你选择“统一 sync，但后台跑”，可用 tmux/nohup 包裹：

```bash
# 方案 A：tmux（推荐，可随时 attach）
tmux new-session -d -s ts-sync-dispatch \
  'dalek ticket dispatch --ticket 12 --sync --timeout 120m'
tmux attach -t ts-sync-dispatch

# 方案 B：nohup（无交互日志）
nohup dalek worker run --ticket 12 --sync --timeout 120m \
  > worker-run.log 2>&1 &
```

常见工作流：

```bash
# 1) 初始化项目
dalek init --name demo

# 2) ticket 生命周期
dalek ticket create --title "smoke test" --desc "验证 CLI 流程"
dalek ticket ls -o json
dalek ticket start --ticket 1
# 默认是异步；这里显式使用同步兜底（阻塞直到完成）
dalek ticket dispatch --ticket 1 --prompt "先补测试再改代码" --sync --timeout 120m
dalek ticket events --ticket 1 -n 50 -o json
dalek ticket stop --ticket 1
dalek ticket archive --ticket 1

# 3) PM / inbox / merge
dalek manager status -o json
dalek manager tick --dry-run -o json
dalek inbox ls -o json
dalek merge ls -o json

# 4) daemon 与 note
dalek daemon start
dalek note add "需要支持导出 CSV"
dalek note ls --shaped

# 5) 项目和 tmux 基础设施
dalek project ls -o json
dalek tmux sockets -o json
dalek tmux sessions --socket dalek -o json
```

JSON 输出与错误格式：

- 查询命令加 `-o json` 返回结构化数据。
- 失败时统一三段式错误：
  - `Error: <发生了什么>`
  - `Cause: <为什么>`
  - `Fix: <如何修复>`
- 当使用 `-o json` 且命令失败时，`stdout` 会输出：
  - `{"schema":"dalek.error.v1", ...}`

迁移说明（硬切，不向后兼容）：

- `dalek ls` -> `dalek ticket ls`
- `dalek create` -> `dalek ticket create`
- `dalek start` -> `dalek ticket start`
- `dalek dispatch` -> `dalek ticket dispatch`
- `dalek interrupt` -> `dalek ticket interrupt`
- `dalek stop` -> `dalek ticket stop`
- `dalek archive` -> `dalek ticket archive`
- `dalek events` -> `dalek ticket events`
- `dalek tmux ls` -> `dalek tmux sessions`
- `dalek agent run finish` -> `dalek agent finish`
- `dalek gateway serve` -> 已迁移到 `dalek daemon start`（`gateway serve` 与 `gateway serve --legacy` 均报错退出）
- `dalek ticket dispatch` / `dalek worker run`：默认从“同步阻塞”迁移为“异步 accepted 回执”
- 若需要旧行为（阻塞等待结果），请显式加 `--sync --timeout`

## watcher 的数据获取与判断策略（v0）

目标：像人盯 tmux 一样“读上下文 + 看变化”，把运行态写回 DB，并把关键观测记录成事件用于回放。

v0 把 watcher 拆成两层，避免“所有 session 每分钟都跑一次小模型”导致成本爆炸：

- cheap sampler（不调用模型）：按 `watcher_sample_interval_ms` 轮询所有 `Worker.Status=running` 的 session
- 语义判断（调用 `watcher_command`）：只在“状态可能变化/需要解释”时触发，并按 `watcher_interval_ms` 节流（每个 worker 最多一次）

cheap sampler 每轮对每个 running worker 会做：

1. `tmux list-sessions` 判断 session 是否还活着
2. 选择观测目标 pane：
   - 优先抓“活动 pane”（尽量与人 attach 看到的一致）
   - 失败才回退 `session:0.0`
3. `pipe-pane` 把输出流写到 `.dalek/workers/w<workerID>/stream.log`
4. `capture-pane` 抓取当前屏幕末尾 N 行文本
5. 计算“时序 diff”指标（用于降低误判）
   - 输出流字节数 `stream_bytes`、增量 `delta`、距离上次变化的时间 `stream_age_sec`
   - 屏幕内容的 hash（可见区），pane 是否处于 copy-mode 等 `in_mode`
6. cheap 状态更新（不调用模型）：
   - 只要检测到“输出/屏幕在推进”，就可强信号判定 `runtime_state=running`
   - 如果没有推进，则保持上次语义判断结果（避免抖动）
7. 写回 DB：
   - 更新 `worker.runtime_*` 字段（状态/needs_user/summary/指标/最后变化时间等）
   - 仅在“语义判断”或“状态变化/错误”时追加一条 `worker_events`（避免高频写爆 DB）

触发语义判断的典型场景：

- `dispatch/interrupt` 等事件触发（系统会标记 “watch_requested”，下一轮尽快跑一次语义判断）
- `runtime_state` 处于 `running/unknown`，且超过 `watcher_stall_ms` 没有进展（判断是否完成/等待输入/报错）

补充：

- 手动观测（TUI `r`）会把 watcher 的 `prompt/input/output`（截断）写进 `worker_events`，方便你 debug “为什么会被判成某个状态”。

## 配置

配置文件：`.dalek/config.json`

常用字段：

- `tmux_socket`：默认 `dalek`（等价于 `tmux -L dalek ...`）
- `refresh_interval_ms`：TUI 列表自动刷新间隔（默认 1000ms，只读 DB，不跑 watcher）
- `watcher_sample_interval_ms`：cheap sampler 轮询间隔（默认 10s，不调用模型）
- `watcher_interval_ms`：语义判断节流间隔（默认 60s；每个 worker 最多一次）
- `watcher_concurrency`：后台并行观测的并发数（默认 4）
- `watcher_stall_ms`：无进展多久后触发一次语义判断（默认 max(20s, 2*sample_interval)）
- `watcher_command`：外部 watcher 命令（空则使用内置启发式）
- `watcher_command_timeout_ms`：外部 watcher 超时（默认 60s）
- `worker_command`：auto 派发时执行的“默认 worker”（会执行：`<worker_command> "<entry_prompt>"`）
  - task.md 路径：`<worktree>/.dalek/task.md`
  - 默认值是 `codex -m gpt-5.3-codex -c 'model_reasoning_effort="xhigh"' exec --full-auto --sandbox workspace-write ...`（需要你本机有 `codex` 命令可用）

仓库自带两个示例 watcher：

- `scripts/watcher_openrouter.py`：走 OpenRouter（OpenAI 兼容接口），默认模型 `openai/gpt-oss-120b`；需要在仓库根目录放 `.env`（参考 `.env.example`）。
- `scripts/watcher_codex.sh`：走本机 `codex` 命令（需要你本机有 `codex` 命令可用）。

## 排障速记

查看 tmux socket 下的 session：

```bash
tmux -L dalek list-sessions
```

直接 attach：

```bash
tmux -L dalek attach -t ts-<projectKey>-t<ticketID>-w<workerID>
```

一键停止本项目所有 worker（对应 session 会被关闭）：

```bash
./dalek ticket stop --all
```

## 已知限制（v0）

- `queued/blocked/done` 目前是“管理状态”，还没有自动调度器去按优先级拉起/并发控制。
- 运行态主要基于“输出流/屏幕变化 + 语义判断”；如果程序长时间无输出但内部仍在跑，仍可能难以精准判断（后续可加进程/CPU 采样补强）。
