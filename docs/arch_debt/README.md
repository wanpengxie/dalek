# 架构债务目录（从审计报告提取）

本目录用于把架构审计的“问题清单”沉淀为可执行的架构债务 backlog，便于直接建任务推进清零。

## 数据来源

- 源报告（已归档）：`docs/arch_debt/source/ARCH_AUDIT_REPORT_2026-02-26.md`
- 提取清单（机器可读）：`docs/arch_debt/issues.tsv`、`docs/arch_debt/issues.json`

> 注意：源报告首页统计与逐条清单不一致，本目录以 `issues.tsv/issues.json` 的提取结果为准。  
> 当前提取结果：CRITICAL=10、HIGH=44、MEDIUM=55、LOW=39（共 148 条）。

## 按严重度的治理清单

- 必须清零：`docs/arch_debt/CRITICAL.md`
- 必须清零：`docs/arch_debt/HIGH.md`
- 建议优先解决的 MEDIUM 子集：`docs/arch_debt/MEDIUM_SELECTED.md`
- 建议解决的 LOW 子集：`docs/arch_debt/LOW_SELECTED.md`
- Ticket 拆分（每票 500-2000 行）：`docs/arch_debt/TICKETS.md`

## 建任务建议（最小模板）

建议每条问题至少落成 1 个 ticket（或归并到 epic 下的子任务），并在标题保留原始 ID 方便追踪：

- 标题：`[ARCH-DEBT][<ID>] <一句话摘要>`
- 描述包含：
  - 现状/问题（可直接引用对应严重度文档的描述）
  - 目标状态（清零标准）
  - 影响面/依赖（涉及哪些包/命令/DB/接口）
  - 验收方式（测试、依赖约束、接口变更、回归路径）

## 主题归并（便于做 Epic）

以下是常见的“天然需要一起做”的归并方向（不改变严重度，只是便于组织实施）：

- Facade 边界回收：`APP-*` + `CMD-*`（把错层实现迁回 services/adapter）
- Agent 执行入口统一：`AGT-*` + `RN-*` + `SR-*` + `PV-*`
- Ticket 生命周期归位：`XWT-*` + `TK-*` + `WK-*` + `PM-*`（workflow/能力视图/领域归属）
- Store 类型解耦与迁移版本化：`ST-*` + 相关 `APP-*`/`CT-*`
- Channel 持久化单路径化：`CH-*` + `GS-*`
