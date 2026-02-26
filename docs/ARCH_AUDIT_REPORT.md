# dalek 全系统架构盘点报告

> 审计日期：2026-02-26
> 审计方法：按 `docs/ARCH_GOVERNANCE.md` 治理原则，8 个 Opus Agent 并行逐文件深度审计
> 审计范围：cmd/dalek、internal/app、internal/services/*、internal/agent/*、internal/store、internal/contracts、internal/infra、internal/repo
> 代码规模：~45,000+ 行 Go 代码（不含测试）

---

## 一、全局架构健康度总览

### 1.1 发现统计

| 严重性 | 数量 | 说明 |
|--------|------|------|
| CRITICAL | 11 | 架构边界被穿透、职责严重错位 |
| HIGH | 40 | 膨胀、重复、耦合、类型泄露 |
| MEDIUM | 44 | 代码异味、次优设计、缺失抽象 |
| LOW | 22 | 风格、测试、命名问题 |
| **合计** | **117** | |

### 1.2 系统性问题（跨模块反复出现）

| 系统性问题 | 涉及模块 | 总计处 |
|------------|----------|--------|
| `strings.TrimSpace` 过度防御 | app(745), channel(566), pm(257), daemon(168), worker, agent | **1800+处** |
| Facade 边界穿透（业务实现放在错误的层） | app, cmd | 6 个大文件 |
| store 类型直接暴露（类型别名/直接 import） | store→app→cmd, 全系统 99 个文件 import store | 120+ 别名 |
| 大文件膨胀（>500行） | 16 个文件超过 500 行 | ~16,000 行 |
| 硬编码（模型名、时间常量、provider） | app, pm, repo, channel | 30+ 处 |

---

## 二、CRITICAL 级发现汇总

### 架构层级穿透类

| ID | 模块 | 描述 | 影响 |
|----|------|------|------|
| **APP-C1** | app/note.go | 1150 行完整 notebook shaping 业务实现放在 Facade 层 | app 膨胀，无法复用 |
| **APP-C2** | app/project_subagent.go | 526 行完整 subagent 编排实现放在 Facade 层，直接 import agent/provider + sdkrunner | Facade 变成业务实现层 |
| **APP-C3** | app/daemon_public_feishu.go | 2087 行完整飞书 IM 适配层放在 Facade 层 | app 包占 35% 是 daemon 代码 |
| **CMD-C1** | cmd/dalek/cmd_gateway_feishu.go | 1413 行完整飞书服务实现放在 CLI 层 | 无法被 daemon 复用 |
| **AGT-C1** | agent/run/*.go | run 包反向依赖 services/core 和 store | agent 层独立性被破坏 |
| **AGT-C2** | 跨层使用模式 | channel/app 绕过 run 编排层直接使用 sdkrunner | agent 层无统一入口 |

### 职责错位类

| ID | 模块 | 描述 | 影响 |
|----|------|------|------|
| **WTT-R1** | worker→ticket | worker 完全绕过 ticket service 直接操作 ticket DB | ticket service 被架空 |
| **WTT-T1** | ticket service | ticket service 是空壳——只有 CRUD，缺失生命周期管理 | 状态机分散在 3+ 个包 |
| **PM-C1** | pm/dispatch_*.go | pm 直接 import agent/provider + agent/run，跨越架构层级 | services→agent 深耦合 |
| **PM-C2** | pm/bootstrap.go + dispatch_*.go | 环境变量 map 三处重复构造，已出现不一致 | bug 风险 |
| **CMD-C2** | cmd/dalek/*_test.go | 测试文件大量绕过 Facade 直接依赖 services/store | 重构时测试连锁失败 |

---

## 三、结构性问题深度分析

### 3.1 store 包成为"类型中心"（根源问题）

**现状**：store 包同时承担了数据库操作 + 领域类型定义（20+ struct, 10+ 状态枚举）。全系统 99 个文件 import store，其中大量只是为了使用类型。

**连锁反应**：
```
store 定义 Ticket/Worker/TaskRun 等类型
  → app/facade_types.go 需要 120+ 行 type X = store.X 别名转发
  → contracts 包未能发挥解耦作用（只有 310 行协议类型）
  → 上层任何包引用 Ticket 都被迫 import store（拉入 gorm 传递依赖）
```

**治理方向**：将领域类型从 store 迁移到 contracts 或新建 `core/model` 包，store 只保留 ORM 映射。

### 3.2 Facade 边界系统性突破

**现状**：app 包声明了"不承载业务实现"，但实践中：

| 应在 services 层的代码 | 当前位置 | 行数 |
|------------------------|----------|------|
| Notebook shaping 全流程 | app/note.go | 1,150 |
| Subagent 编排 | app/project_subagent.go | 526 |
| 飞书 IM 适配 | app/daemon_public_feishu.go | 2,087 |
| Daemon 管理组件 | app/daemon_manager_component.go | 879 |
| Daemon 公共组件 | app/daemon_public_*.go | 788 |

app 包 14,279 行中约 60% 是错误放置的业务实现。合理的 Facade 层应在 2,000-3,000 行。

### 3.3 core.Project God Object

**现状**：12 个字段覆盖 4 个完全不同的关注域（身份/文件系统/配置/运行时），22 个文件引用它。每个 service 只使用 2-4 个字段，但被迫接收整个 Project。

**影响**：测试必须构造完整 Project；字段变更影响 22 个文件；无法从类型签名判断 service 的真实依赖。

### 3.4 ticket 生命周期碎片化

**现状**：ticket 的完整操作分散在 4 个包中：

| 操作类型 | 所在包 |
|----------|--------|
| Create, List, BumpPriority, UpdateText | ticket service |
| Start, Stop, Attach, CleanupWorktree | worker service |
| Dispatch | pm service |
| Archive, workflow 状态转换 | app facade |
| TicketView, capability 计算 | worker/views.go |

没有单一位置可以回答"ticket 支持哪些操作"和"状态转换规则是什么"。

### 3.5 agent 层缺少统一入口

**现状**：
- pm 通过 `agent/provider` + `agent/run` 执行 agent（CLI/tmux 模式）
- channel 通过 `agent/sdkrunner` 直接执行 agent（绕过 run 编排层）
- app 通过 `agent/provider` + `agent/sdkrunner` 执行 subagent

三条路径，三种组装方式，行为不一致。run 包实质上是"pm 专用编排"而非 agent 层公共 API。

---

## 四、模块级发现汇总

### 4.1 cmd/dalek（CLI 层）

| 级别 | 数量 | 重点发现 |
|------|------|----------|
| CRITICAL | 2 | 飞书实现放在 cmd 层(1413行)；测试绕过 Facade |
| HIGH | 6 | WS 服务器放在 cmd 层(587行)；config 绕过 Facade import repo；配置解析业务逻辑(828行) |
| MEDIUM | 5 | TrimSpace 过度；task 状态推导逻辑泄露到 cmd 层 |

### 4.2 internal/app（Facade 层）

| 级别 | 数量 | 重点发现 |
|------|------|----------|
| CRITICAL | 3 | note.go/subagent/feishu 三块完整业务实现错位 |
| HIGH | 6 | facade_types.go 类型泄露；project.go 膨胀(975行)；daemon_manager 直接操作 DB；action_executor 残留 DB 直接访问；硬编码模型名 |
| MEDIUM | 5 | TrimSpace 745处；provider 白名单硬编码；daemon 组件散落(~4950行) |

### 4.3 internal/services/channel（Channel 服务）

| 级别 | 数量 | 重点发现 |
|------|------|----------|
| CRITICAL | 0 | 之前的横向耦合已修复干净 |
| HIGH | 5 | Service↔Gateway 双路径持久化已分裂；runTurnJob 膨胀(300+行)；pending_actions 混合三种职责；TrimSpace 566处；localActionExecutor 残留 |
| MEDIUM | 7 | turnResult 重复 struct；string key 匹配脆弱；context.Background() 滥用 |

### 4.4 internal/services/pm（PM 服务）

| 级别 | 数量 | 重点发现 |
|------|------|----------|
| CRITICAL | 2 | 直接 import agent/provider+run 跨层；env map 三处重复已不一致 |
| HIGH | 5 | 16+处硬编码时间常量含重复；prompt 模板内嵌 Go 代码；ManagerTick 595行巨函数；横向依赖 gatewaysend；dispatch_queue 720行膨胀 |
| MEDIUM | 6 | TrimSpace 257处；ctx==nil 检查 51处；dispatch target 重复逻辑 |

### 4.5 internal/services/worker+ticket+task（执行类服务）

| 级别 | 数量 | 重点发现 |
|------|------|----------|
| CRITICAL | 1 | worker 完全绕过 ticket service 直接操作 DB |
| HIGH | 6 | ticket 是空壳；职责碎片化；core↔task 150行 Input 类型镜像；ListTicketViews God Method；TicketView 放错位置 |
| MEDIUM | 8 | task 状态转换无显式状态机；goto 语句；方法过长；两套 taskRuntime 路径 |

### 4.6 internal/services/daemon+core+gatewaysend+logs（基础设施服务）

| 级别 | 数量 | 重点发现 |
|------|------|----------|
| HIGH | 6 | core.Project God Object；execution_host 1337行；TrimSpace 168处；logs 包名与职责不匹配；gatewaysend 单文件混合5层 |
| MEDIUM | 7 | probeWorkerRunID 忙等；TaskRuntime 接口过大(13方法)；缺少 repository 抽象 |

### 4.7 internal/agent/*（Agent 层）

| 级别 | 数量 | 重点发现 |
|------|------|----------|
| CRITICAL | 2 | run 反向依赖 services/core+store；agent 层无统一入口 |
| HIGH | 6 | 三个 executor 300+行重复；SDKConfig 24字段膨胀；TmuxHandle.Wait() 无限轮询；sdkrunner 798行单文件；200行 Claude 权限 JSON 硬编码 |
| MEDIUM | 7 | Provider 接口只服务 CLI 模式；配置和执行混在一个包 |

### 4.8 internal/store+contracts+infra+repo（基础层）

| 级别 | 数量 | 重点发现 |
|------|------|----------|
| HIGH | 4 | store 类型与持久化耦合(根源问题)；contracts 承载范围过窄；AutoMigrate 破坏性 DDL 无版本化；openai_compat 不应在 infra |
| MEDIUM | 5 | ChannelType 重复定义；config.go 三重职责；模型名硬编码分散 |

---

## 五、治理优先级路线图

### Phase 0: 止血（立即可做，低风险）

| 动作 | 预估工作量 | 收益 |
|------|-----------|------|
| 大文件纯拆分（不改逻辑，只分文件）：execution_host→3文件, gatewaysend→3文件, sdkrunner→5文件 | 1天 | 可读性 |
| 移除 WS 事件 Text 字段的 TrimSpace（D-6，数据篡改 bug） | 0.5小时 | 修复潜在 bug |
| 集中 pm 时间常量到 constants.go | 0.5天 | 消除重复定义 |
| 统一 pm 环境变量 map 构造（消除三处重复+不一致） | 0.5天 | 消除 bug 风险 |

### Phase 1: 类型归位（高收益结构性改造）

| 动作 | 预估工作量 | 收益 |
|------|-----------|------|
| 将 store 领域类型迁移到 contracts 或 core/model | 3-5天 | 消除 120+ 别名，减少 99→~30 store import |
| facade_types.go 别名逐步替换为独立类型 | 随 Phase 1 一起 | Facade 真正隔离 |
| ChannelType 等重复枚举统一 | 0.5天 | 消除重复 |

### Phase 2: Facade 边界修正（高优先级）

| 动作 | 预估工作量 | 收益 |
|------|-----------|------|
| note.go → services/notebook/ | 2-3天 | app 减少 1150 行 |
| project_subagent.go → services/subagent/ | 2天 | app 减少 526 行，消除 app→agent 直接依赖 |
| daemon_public_feishu.go → services/channel/feishu/ | 3-5天 | app 减少 2087 行 |
| cmd_gateway_feishu.go → 同上 | 随上一步 | cmd 减少 1413 行 |
| daemon_manager_component 的 DB 操作下沉到 service 层 | 1-2天 | 消除 app 直接操作 DB |

### Phase 3: 服务边界修正

| 动作 | 预估工作量 | 收益 |
|------|-----------|------|
| 重建 ticket service 完整职责（状态机、生命周期） | 3-5天 | 消除 ticket 碎片化 |
| worker 改为通过 ticket service 操作 ticket（而非直接 DB） | 2天 | ticket 成为单一权威 |
| pm 抽取 AgentLauncher 接口解除→agent 层耦合 | 2天 | 消除跨层穿透 |
| agent/run 改为 hook/callback 模式，消除→services/core+store 反向依赖 | 2-3天 | agent 层独立性恢复 |
| pm/workflow_notify 移出 pm 包 | 0.5天 | 消除横向依赖 |

### Phase 4: 内部优化

| 动作 | 预估工作量 | 收益 |
|------|-----------|------|
| core.Project God Object 拆分（ProjectPaths + 按需注入） | 5天+ | 22 文件受益 |
| channel 双路径持久化统一 | 3-5天 | 消除 Service↔Gateway 分裂 |
| core↔task Input 类型镜像消除 | 1天 | 消除 150 行机械复制 |
| agent 层统一入口包 | 2-3天 | 消除三条并行执行路径 |
| strings.TrimSpace 系统性清理（建立输入净化边界） | 3-5天 | 消除 1800+ 处噪音 |
| store AutoMigrate 版本化 | 2天 | 消除每次启动的无效 DDL |

### Phase 5: 长期演进

- cmd_config.go 配置解析业务逻辑下沉到 app/config
- cmd_gateway_ws.go WS 服务器逻辑下沉
- TaskRuntime 接口按 ISP 拆分
- 结构化日志统一（替代 log.Printf）
- 硬编码模型名/provider 白名单集中管理

---

## 六、正面评价

审计不仅发现问题，也应记录做得好的地方：

1. **ActionHandler 接口注入**：t3 重构干净彻底，channel 的横向服务耦合已消除
2. **agentcli 子包**：零外部依赖，职责单一，是系统中边界最清晰的子模块
3. **eventrender 子包**：策略模式使用得当，测试覆盖全面，抽象层次正确
4. **EventDeduplicator**：基于 LRU+TTL 的去重器，实现精良
5. **EventBus 发布不阻塞**：慢消费者不拖垮系统
6. **worker 资源管理防御设计**：孤儿 session 清理、worktree prune-retry、defer 回滚
7. **task 状态幂等性**：canceled 不可覆盖、duplicate request_id 返回已有记录
8. **daemon Component 模式**：接口定义清晰，生命周期管理合理
9. **infra 包整体**：接口抽象合理（CommandRunner, GitClient, TmuxClient），依赖注入支持
10. **contracts 包零依赖设计**：正确的叶子包定位
11. **noun-verb CLI 模式**：命令组织一致，三段式错误处理语义明确
12. **架构约束测试**：已建立 cmd 层的 import 防护网

---

## 附录：按模块的大文件清单

| 文件 | 行数 | 模块 | 主要问题 |
|------|------|------|----------|
| app/daemon_public_feishu.go | 2,087 | app | 错误放置在 Facade 层 |
| channel/service.go | 1,436 | channel | 内部膨胀 |
| channel/gateway_runtime.go | 1,351 | channel | 双路径持久化 |
| cmd/cmd_gateway_feishu.go | 1,413 | cmd | 错误放置在 CLI 层 |
| cmd/cmd_ticket.go | 1,328 | cmd | 体量偏大 |
| daemon/execution_host.go | 1,337 | daemon | 5 种职责混合 |
| app/note.go | 1,150 | app | 错误放置在 Facade 层 |
| app/project.go | 975 | app | 膨胀+机械透传 |
| app/daemon_manager_component.go | 879 | app | 直接操作 DB |
| cmd/cmd_config.go | 828 | cmd | 业务逻辑泄露 |
| sdkrunner/runner.go | 798 | agent | 单文件巨模块 |
| pm/dispatch_queue.go | 720 | pm | 职责过重 |
| cmd/cmd_agent.go | 715 | cmd | 体量偏大 |
| channel/pending_actions.go | 679 | channel | 三种职责混合 |
| pm/manager_tick.go | 595 | pm | 巨函数 |
| cmd/cmd_gateway_ws.go | 587 | cmd | WS 服务器放在 cmd 层 |
