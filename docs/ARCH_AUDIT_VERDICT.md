# 架构审计复核结论（2026-02-26）

> 复核对象：`docs/ARCH_AUDIT_REPORT.md`
> 复核目标：回答“11 个 CRITICAL + 40 个 HIGH 有多少成立”，并沉淀可建任务清单。

## 1. 复核口径与限制

1. 本次以“代码证据”为准，按 `属实 / 部分属实 / 误判` 三档判定。
2. 原报告在 HIGH 计数上存在自相矛盾：
   - 总览写 HIGH=40（`ARCH_AUDIT_REPORT.md:17`）
   - 模块小计合计 HIGH=44（4.1~4.8 的 HIGH 数量相加）
   - 模块“重点发现”实际仅列出 37 个可见主题（按分号拆分）
3. 因为缺少“40 条 HIGH 的逐条清单+ID”，HIGH 无法对“精确 40 条”做一一映射；本复核采用“37 个可见 HIGH 主题”做可执行判定，并给出对 40 口径的下界结论。

---

## 2. 总体结论（可直接用于排期）

| 级别 | 报告口径数量 | 严格属实 | 部分属实 | 误判 | 备注 |
|---|---:|---:|---:|---:|---|
| CRITICAL | 11 | 8 | 2 | 1 | 可逐条核验（有 ID） |
| HIGH（可见主题口径） | 37 | 22 | 13 | 2 | 来自模块“重点发现”可见项 |
| HIGH（报告宣称口径） | 40 | **至少 22** | **另有 13 条部分属实** | **至多 2** | 仍有明细缺失，无法精确到 40 |

### 2.1 你关心的“到底有多少成立”

1. **严格成立（只算属实）**：`30` 项（CRITICAL 8 + HIGH 22）
2. **建议纳入治理（属实+部分属实）**：`45` 项（CRITICAL 10 + HIGH 35）

---

## 3. CRITICAL 逐条复核（11/11）

| ID | 结论 | 证据（示例） |
|---|---|---|
| APP-C1 | 属实 | `internal/app/note.go` 1150 行，含完整 note shaping 流程 |
| APP-C2 | 属实 | `internal/app/project_subagent.go` 直接依赖 `agent/provider` + `agent/sdkrunner` |
| APP-C3 | 属实 | `internal/app/daemon_public_feishu.go` 2087 行，含完整飞书适配逻辑 |
| CMD-C1 | 属实 | `cmd/dalek/cmd_gateway_feishu.go` 在 CLI 层实现 webhook/发送/卡片 |
| AGT-C1 | 属实 | `internal/agent/run/sdk.go`、`tmux.go` 反向依赖 `services/core` 与 `store` |
| AGT-C2 | 部分属实 | `channel/app` 直连 `sdkrunner` 事实成立；但与 PM run 路径差异有“场景化设计”成分 |
| WTT-R1 | 误判 | worker 读 ticket 存在，但未发现其直接写 `ticket.workflow_status/archived`；状态写入主要在 PM |
| WTT-T1 | 部分属实 | `ticket.Service` 以 CRUD 为主属实，但“空壳”表述偏重（仍承载基础入口） |
| PM-C1 | 属实 | `internal/services/pm/dispatch_agent_exec.go` 直接 import `agent/provider` + `agent/run` |
| PM-C2 | 属实 | `bootstrap.go` / `dispatch_agent_exec.go` / `dispatch_worker_sdk.go` 三处重复拼 env map 且不完全一致 |
| CMD-C2 | 属实 | 多个 `cmd/*_test.go` 直接 import `internal/store`、`internal/services/channel` |

---

## 4. HIGH 复核（37 个可见主题）

## 4.1 按模块汇总

| 模块 | 可见 HIGH 主题数 | 属实 | 部分属实 | 误判 |
|---|---:|---:|---:|---:|
| cmd/dalek | 3 | 3 | 0 | 0 |
| internal/app | 5 | 4 | 1 | 0 |
| services/channel | 5 | 3 | 1 | 1 |
| services/pm | 5 | 3 | 1 | 1 |
| services/worker+ticket+task | 5 | 3 | 2 | 0 |
| services/daemon+core+gatewaysend+logs | 5 | 4 | 1 | 0 |
| internal/agent | 5 | 2 | 3 | 0 |
| store+contracts+infra+repo | 4 | 0 | 4 | 0 |
| **合计** | **37** | **22** | **13** | **2** |

## 4.2 明确误判（HIGH）

1. `localActionExecutor 残留`：当前代码未找到同名实现（现为 `ActionExecutor`）。
2. `prompt 模板内嵌 Go 代码`：当前主要是内嵌大段规则模板文本，不是“内嵌 Go 代码”。

## 4.3 典型部分属实（HIGH）

1. `ticket 是空壳`：方向对，但措辞过重。
2. `TicketView 放错位置`：有边界问题，但也与现有 view 聚合设计相关。
3. `logs 包名与职责不匹配`：确有偏差，但属于命名/边界治理，不是硬缺陷。
4. `三个 executor 300+ 行重复`：重复问题成立，但 `process.go` 未超过 300 行。
5. `Claude 权限 JSON 200 行硬编码`：硬编码属实，但行数描述偏大（当前约 140 行区间）。
6. `contracts 承载范围过窄`、`openai_compat 不应在 infra`：更多是治理偏好，需要结合目标架构决策。
7. `AutoMigrate 破坏性 DDL 无版本化`：有风险方向，但当前存在锁与存在性保护，需按“中短期治理”处理。

---

## 5. 建任务建议（按优先级可直接建 ticket）

## P0（先做，止血）

1. **统一 PM env map 构造器**  
   - 覆盖：PM-C2  
   - 验收：三处 env map 合并为单一 helper，字段一致且有单测。
2. **修正 worker/ticket 状态写入边界文档与断言**  
   - 覆盖：WTT-R1 误判澄清 + WTT-T1 部分属实  
   - 验收：明确“谁能写 workflow_status”，补充防回归测试。
3. **拆分超大文件（仅拆文件不改行为）**  
   - 覆盖：`execution_host.go`、`dispatch_queue.go`、`manager_tick.go`、`sdkrunner/runner.go`  
   - 验收：行为不变，文件体量下降，核心路径回归通过。

## P1（结构修正）

1. **Facade 边界回收（app/cmd → services）**  
   - 覆盖：APP-C1/2/3、CMD-C1  
   - 验收：飞书适配、subagent 编排、note shaping 下沉到 services，app/cmd 仅保留编排接口。
2. **Agent 统一入口**  
   - 覆盖：AGT-C1、AGT-C2 + agent 高优先项  
   - 验收：PM/channel/subagent 统一走稳定执行入口，去掉跨层直连。
3. **ticket 生命周期归位**  
   - 覆盖：WTT-T1 + worker/task 高优先项  
   - 验收：状态机集中定义；ticket/worker/pm 责任边界可文档化、可测试。
4. **channel 持久化单路径化**  
   - 覆盖：channel 高优先项（双路径、runTurnJob 膨胀）  
   - 验收：`Service` 与 `Gateway` 的 turn/outbox 写路径收敛。

## P2（中长期治理）

1. **store 类型与 ORM 解耦（引入 model/contracts 分层）**  
   - 覆盖：store 耦合、facade types 泄露、跨层 import store 泛滥  
   - 验收：上层 import store 文件数显著下降，类型别名逐步清零。
2. **AutoMigrate 版本化迁移**  
   - 覆盖：AutoMigrate 风险项  
   - 验收：引入 migration version + up/down 或等价机制，启动不再执行不可追踪 DDL。
3. **命名与职责对齐（logs/openai_compat/contracts）**  
   - 覆盖：多个部分属实项  
   - 验收：包职责文档更新，必要时重命名或迁包。

---

## 6. 结论（给管理层的一句话）

1. 这份审计不是“100% 正确”，但主干问题成立，尤其是边界穿透与大文件膨胀。  
2. 以“严格成立 + 部分属实”口径，**可直接进入治理池的问题为 45 项**。  
3. HIGH 的“40 条”原始明细不完整，建议先补齐带 ID 的清单，再做第二轮精确复核。
