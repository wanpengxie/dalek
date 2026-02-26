# LOW（建议解决子集）

> 来源：`docs/arch_debt/source/ARCH_AUDIT_REPORT_2026-02-26.md`
> 生成日期：2026-02-26
> 入选条目数：6

入选标准：影响正确性/稳定性，或明显阻碍 CRITICAL/HIGH 清零的结构性问题。

## internal/agent/eventrender

- `ER-L2` `claude.go` 行79：**只渲染第一个 content block**。`renderAssistant()` 只解析 `msg.Content[0]`，多 block assistant message（如 thinking + tool_use）后续 block 被静默丢弃。应遍历所有 content blocks。 重要性：渲染只取第一个 block 可能丢事件内容，属于潜在数据丢失。

## internal/services/channel（Channel 服务 + agentcli）

- `CH-L2` `service.go` 行50-55：**Service.SetActionHandler 非并发安全**。直接写 `s.actionHandler` 无 mutex 保护。如果在 Service 运行中调用与 executeAction 中的读取存在 race。应文档化"必须在运行前设置"或加 mutex。 重要性：存在 data race 风险（运行时设置 handler），线上难定位。

## internal/services/pm（PM 服务）

- `PM-L2` 6 个文件共 817 行：**缺失测试的关键文件**。session.go(186行)、inbox.go(188行)、inbox_upsert.go(110行)、bootstrap.go(58行)、worker_loop.go(137行)、worker_ready_wait.go(138行) 均无单元测试。其中 worker_loop 和 worker_ready_wait 是 dispatch 核心路径。 重要性：核心调度路径缺测试，任何重构都缺“安全网”。

## internal/services/task（Task 服务）

- `TS-L1` `service_runs.go` 行349-358 + `service_subagent.go` 行154-169：**唯一约束冲突检测依赖错误消息字符串匹配**。通过检查 "unique constraint failed" 字符串判断。GORM `errors.Is(err, gorm.ErrDuplicatedKey)` 是第一优先级检查，字符串匹配是 fallback。依赖 SQLite driver 具体错误格式。 重要性：依赖错误字符串匹配做唯一约束判断，跨 driver/版本易失效。

## internal/services/ticket（Ticket 服务）

- `TK-L1` `service.go`：**缺少 ByID 查询方法**。其他服务（worker.start.go:155）获取 ticket 时都是直接 `db.First(&t, ticketID)` 绕过 service 层。应补充 `GetByID` 作为单一入口。 重要性：缺少 GetByID 逼迫其他服务直连 DB，阻碍 ticket 边界治理。

## internal/store（持久化层）

- `ST-L2` `models.go` 多处：**15+ 个 JSON 字段缺乏类型约束**。PayloadJSON/ResultJSON/ChecksJSON/ActionJSON/MetricsJSON 等全部 `string` 类型，读取需手动 JSON 解析每个调用点有失败风险，写入无类型保护。应对高频字段定义 Go 结构体用 GORM Serializer。 重要性：高频 JSON 字段缺类型约束，写入/读取错误难以及时暴露。
