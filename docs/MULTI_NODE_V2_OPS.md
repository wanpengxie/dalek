# Dalek 多节点 V2 操作手册（当前落地范围）

本手册只覆盖当前已落地的 CLI/TUI 能力。多节点完整链路仍在演进中，未实现的能力不要按文档假设可用。

## 1. 运行前提

- 已完成项目初始化：`dalek init`
- daemon 已启动：`dalek daemon start`
- `~/.dalek/config.json` 中 `daemon.internal` 已配置或已由 daemon 自动补全

## 2. Node 管理（控制面）

当前支持节点注册信息的 CRUD（本地 registry）：

```bash
dalek node add --name node-c --roles run --provider-modes run_executor
dalek node ls
dalek node show --name node-c
dalek node rm --name node-c
```

说明：
- `--roles` 与 `--provider-modes` 为逗号分隔
- 目前 node 仅保存元信息，不会启动远端 agent 进程

## 3. Node run-loop（最小协议探测）

`node run-loop` 用于验证 node agent 的最小协议路径（register/heartbeat/inspect）。

```bash
dalek node run-loop --name node-c --project demo
dalek node run-loop --name node-c --run-id 88 -o json
```

最小 JSON 响应示例：

```json
{
  "schema": "dalek.node.run-loop.v1",
  "project": "demo",
  "node_name": "node-c",
  "session_epoch": 61,
  "status": "online",
  "run": {
    "found": true,
    "run_id": 171,
    "status": "succeeded",
    "lifecycle_stage": "recovery",
    "last_event_type": "run_artifact_upload_failed"
  },
  "artifacts": {
    "found": true,
    "run_id": 171,
    "artifacts": [],
    "issues": [
      {
        "name": "report.json",
        "status": "upload_failed",
        "reason": "upload failed"
      }
    ]
  },
  "warnings": [
    "run 仍处于 recovery 阶段，等待关键节点恢复或修复后重试",
    "artifact 上传部分失败，但执行状态保持为 succeeded"
  ]
}
```

常见失败：
- `node agent token 未初始化`：先运行 `dalek daemon start`
- `daemon internal listen` 不可达：检查 `daemon.internal.listen` 配置
- 文本模式下若输出 `is in recovery`，表示远端 run 仍在恢复阶段
- 文本模式下若输出 `artifact upload partially failed`，表示执行状态已确定，但产物上传有部分失败

## 4. Run CLI（本地 run view）

当前 run 作为 `task(kind=run_verify)` 的读模型使用：

```bash
dalek run request --verify-target test
dalek run show --id 1
dalek run logs --id 1
dalek run artifact ls --id 1
dalek run cancel --id 1
```

`run show -o json` 响应示例：

```json
{
  "schema": "dalek.run.show.v1",
  "run": {
    "RunID": 41,
    "RunStatus": "succeeded",
    "RequestID": "req-reconcile-41"
  },
  "task_status": {
    "RuntimeSummary": "verify accepted for target=test",
    "SemanticMilestone": "verify_succeeded",
    "LastEventType": "run_artifact_upload_failed",
    "LastEventNote": "upload failed"
  },
  "warnings": [
    "执行终态已确定，artifact 上传存在部分失败，请单独检查产物链路"
  ]
}
```

`run artifact ls -o json` 响应示例：

```json
{
  "schema": "dalek.run.artifacts.v1",
  "artifacts": {
    "Found": true,
    "RunID": 41,
    "Artifacts": [],
    "Issues": [
      {
        "Name": "report.json",
        "Status": "upload_failed",
        "Reason": "upload failed"
      }
    ]
  },
  "warnings": [
    "当前没有可用产物索引，但存在 artifact 上传失败记录",
    "发现 1 条 artifact issue"
  ]
}
```

说明：
- `--verify-target` 取自项目配置 `run_targets`（默认 `test/lint/build`）
- `run logs` 基于 task 事件尾部拼装
- `run artifact ls` 当前展示 snapshot apply 相关索引，并会把 `run_artifact_upload_failed` 映射为 `issues`
- `node run-loop` 中的 `artifacts` 也会返回远端 `issues`
- `run show` 会附带 `task_status` 聚合信息；当状态为 `node_offline` / `reconciling` 时会尝试自动 reconcile
- 若最近事件为 `run_artifact_upload_failed`，说明执行终态已确定，但产物上传存在部分失败
- 文本模式下 `run show` 会额外输出 `summary / milestone / last_event / hint`
- 文本模式下 `run artifact ls` 若只有问题没有索引，会输出 `(no indexed artifacts)` 与 `WARN ... upload_failed ...`
- JSON 模式下 `run show` / `run artifact ls` / `node run-loop` 会附带 `warnings`
- 默认调度保护：同一 node 上单个项目默认最多占用 `node_capacity - 1` 个并发槽位；若节点容量为 `1`，仍允许单项目串行执行

## 5. TUI Run 视图

在 TUI 的 monitor 页面按 `R` 进入 run 视图：

- `R` 进入 run 视图
- `Esc` 返回 monitor
- `r` 刷新 run 列表

当前 run 视图展示：
- run 列表（状态、target、snapshot）
- 选中 run 的详细字段
- 最近 task status 摘要（summary / milestone / last_event）
- 若存在 artifact 问题，顶部状态栏会统计 `artifact 异常` 数量

## 6. 排障速查

- `node` 命令提示 “daemon 不在线”
  - 检查 `dalek daemon status`
  - 确认 `daemon.internal.listen` 可访问
- `run request` 失败
  - 校验 `--verify-target` 是否存在
  - 检查项目 `.dalek/config.json` 中的 `run_targets` 配置

## 7. 部署形态与启动顺序

当前版本的多节点仍以“控制面 + 本地协议”为主，远端节点与 snapshot 传输处于演进中。部署文档以“可运行 + 可观测 + 可排障”为目标，不包含完整生产化细节。

共通前提：
- 每个参与节点都需要可运行 `dalek` 二进制
- 控制面（A）必须能访问其本地 repo
- 远端节点（B/C）必须能访问自身的工作目录（当前仍以本地工作区为主）
- `daemon.internal.listen` 与 `daemon.internal.token` 必须在 A 配置

### 7.1 单机（A/B/C 同机）

适用场景：本地演示、单机验证、最小闭环。

1. 初始化并启动：

```bash
dalek init
dalek daemon start
```

2. 验证基础：

```bash
dalek node ls
dalek run request --verify-target test
dalek run show --id 1
dalek run artifact ls --id 1
```

单机 smoke 通过标准：
- `run show` 能看到 `run/task_status/warnings`
- `run artifact ls` 能返回 artifact 索引或明确的 `issues`
- 若使用 `node run-loop -o json`，输出中包含 `session_epoch`

### 7.2 双机（A+B 同机，C 独立）

适用场景：开发在本地，验证在独立机器。

在 A 机器上：
1. 启动 daemon：
```bash
dalek daemon start
```
2. 记录 `daemon.internal.listen` 与 `daemon.internal.token`。

在 C 机器上：
1. 准备 `dalek` 二进制与工作目录。
2. 使用 `node run-loop` 验证协议连通性：
```bash
dalek node run-loop --name node-c --project demo --endpoint http://<A-host>:<port> --token <node-agent-token>
```
3. 在 A 机器上发起 run 并检查聚合状态：
```bash
dalek run request --verify-target test
dalek run show --id <run_id> -o json
dalek run logs --id <run_id>
```

双机 smoke 通过标准：
- `node run-loop` 能成功 register/heartbeat/query
- `run show -o json` 中 `task_status` 与远端状态一致
- 节点短暂断线后，再次执行 `run show` 能触发 reconcile 并返回可解释状态

### 7.3 三机（A/B/C 分离）

适用场景：控制面与开发/运行分离。

步骤类似双机，但 B/C 均需要与 A 打通 internal API：
- A：启动 daemon，配置 listen/token。
- B：用于开发与 snapshot 生成（当前 snapshot 仍以 manifest 为主）。
- C：用于运行/验证（支持 run-loop、下载并 apply manifest）。

三机 smoke 建议顺序：
1. 在 B 生成 snapshot 输入：
```bash
dalek node run-loop --name node-b --project demo --snapshot-id snap-smoke --workspace-dir . -o json
```
2. 在 C 验证运行节点协议：
```bash
dalek node run-loop --name node-c --project demo --endpoint http://<A-host>:<port> --token <node-agent-token>
```
3. 在 A 侧查询 run 与 snapshot 聚合状态：
```bash
dalek run show --id <run_id> -o json
dalek run artifact ls --id <run_id> -o json
```

三机 smoke 通过标准：
- B 侧 snapshot manifest/build/upload 成功
- C 侧能查询到对应 run 或 snapshot apply 结果
- A 侧 `run show` / `run artifact ls` 能看到 snapshot、warning、artifact issue

## 8. 恢复与排障建议

### 8.1 断线与恢复

- 节点短暂断线：A 侧 run 状态保持，节点恢复后通过 `run query` 回补。
- 若状态卡住：
  - `dalek run show --id <run_id>` 查看状态与最近事件
  - `dalek run logs --id <run_id>` 查看事件尾部
- 若 `run show` 中 `last_event=run_artifact_upload_failed`：
  - 优先信任 `status` / `summary` 判断执行是否已经成功
  - 再单独检查 artifact 存储或上传链路
- 若 JSON 响应中的 `warnings` 非空：
  - 文本提示和高层风险说明以 `warnings` 为准
  - 自动化脚本不要再去解析自然语言 stdout

### 8.2 Snapshot 失败

- `run_snapshot_apply_failed` 出现时：
  - 检查 snapshot manifest 是否完整
  - 检查 `base_commit` 与 `workspace_generation`
  - 若需要，重新生成 snapshot 并重试

### 8.3 权限/鉴权失败

- `node agent token 未初始化`：先运行 `dalek daemon start`
- `authorization bearer token` 错误：
  - 检查 `daemon.internal.token` 与 node 侧传入 token 是否一致
  - 确认 `daemon.internal.listen` 可达且端口未被阻断

## 9. Smoke Test（最小验证清单）

按以下步骤验证部署路径：

1. `dalek daemon status`
2. `dalek node ls`
3. `dalek run request --verify-target test`
4. `dalek run show --id <run_id>`
5. `dalek run logs --id <run_id>`
6. `dalek run artifact ls --id <run_id>`

建议额外核对：
- `dalek run show --id <run_id> -o json`
- `dalek node run-loop --name <node> --run-id <run_id> -o json`
- 若并发提交多个同项目 run，观察不会占满节点全部容量，至少保留一个可调度槽位给其他项目

推荐补充检查：

1. `dalek run show --id <run_id> -o json`
2. 确认 `warnings` 是否为空
3. 若 `run artifact ls -o json` 中 `Issues` 非空，单独检查 artifact 上传链路
