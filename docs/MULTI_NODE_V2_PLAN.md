# Dalek 多节点架构 V2 方案

## 1. 文档目标

本文档基于 V1 方案，重新整理出一份更完整的多节点架构设计说明。

V2 的目标不是推翻 V1，而是把已经明确的核心约束、对象关系、通信链路和落地顺序整合为一份结构连续、语义一致、便于后续实现与评审的方案文档。

本文档关注的是：

- 多角色部署下的 Dalek 总体架构
- 控制面、开发面、运行面的职责边界
- 本地项目与多节点项目如何统一建模
- 跨节点代码传递、执行、观测、恢复和授权模型
- 后续实现时必须先拍板的架构约束

本文档不包含具体代码实现。

## 2. 设计目标

V2 要解决的问题是：

- 让一套中心 Dalek 实例管理“开发发生在一处、运行发生在另一处”的项目
- 保持现有本地项目工作流继续可用
- 让单机、双机、多机部署落在同一套抽象下
- 支持不同节点使用不同 provider，而不是把模型框架写死
- 让跨节点验证具备稳定的状态机、审计、恢复和调试能力

V2 追求的是“统一模型下的渐进扩展”，而不是做一套只适用于远程节点的新系统。

## 3. 非目标

V2 仍然有边界，不试图一次解决所有远程协作问题。

当前不包含：

- 远程 tmux attach 或完整远程交互终端
- 节点之间共享文件系统
- 多主控制面
- 让控制面直接通过 SSH 远程操作仓库路径
- 默认的全量实时日志推送
- 复杂交互式调试协议
- 把运行节点做成通用远程 shell

## 4. 核心抽象

### 4.1 三类角色，不是三台机器

V2 的基本拓扑不是“三台固定机器”，而是三类角色：

- A：`control role`，控制面，负责编排、注册、状态聚合、审计和用户入口
- B：`dev role`，开发面，负责源码工作区、开发 agent、代码变更产生，以及开发过程中的本地运行与中间测试
- C：`run role`，运行面，负责重要阶段验证、准生产运行、日志、产物、运行诊断，以及最终线上运行承载

这三个角色的部署关系必须满足以下约束：

- A、B、C 可以部署在同一台机器
- A、B、C 也可以部署在不同机器
- 一个物理节点可以同时承载多个角色
- B 和 C 是角色，不是单实例限制；后续可以扩展为多个 dev node / run node

因此，V2 讨论的是“角色分工模型”，不是“三机特化模型”。

### 4.2 本地项目是多角色模型的特例

现有 Dalek 直接操作本地项目的能力必须完整保留。

V2 的正确理解方式是：

- 单机本地项目不是另一套架构
- 单机本地项目是 A/B/C 三类角色共置时的特例
- 多节点能力是对现有本地能力的扩展，而不是替换

因此后续实现必须同时保留：

- `local facade`：服务纯本地项目
- `remote / multi-node facade`：服务多角色、多节点项目

上层编排逻辑应逐步依赖统一抽象，而不是把“本地逻辑”和“远程特判”混写在一起。

### 4.3 角色与 provider 解耦

节点角色和模型框架选择必须正交建模。

节点是否承担 `control`、`dev`、`run` 角色，与其使用什么 provider 是两回事。A、B、C 都应允许按节点独立配置 provider。

当前 provider 选项至少包括：

- `Claude`
- `codex`
- `kimi`
- `DeepSeek`
- `run_executor`

其中：

- `run_executor` 用于标准化验证执行
- 其他 provider 主要用于开发或诊断型 agent 交互

必须明确以下规则：

- 某个节点启用哪个 provider，由用户配置决定
- 一个节点可以同时声明多个 provider
- provider 选择不能在 workflow 中硬编码
- provider 路由优先级应为：run/task 显式指定 > project 默认策略 > node 默认 provider

V2 不要求 A/B/C 使用同一种 provider，只要求能力声明、协议和状态映射兼容。

## 5. 架构总原则

### 5.1 控制面统一，事实分层

V2 的核心不是“所有事情都在 A 上执行”，而是“所有事情都在 A 上编排”。

职责权威应这样划分：

- A 是状态机权威，负责 `ticket`、`task`、`run` 的聚合视图和推进
- B 是源码上下文权威，负责“这次想验证哪份代码”以及开发过程中的快速本地执行结果
- C 是运行结果事实权威，负责重要阶段验证、准生产验证和最终线上运行结果
- 节点内 provider runtime 是该节点 thread / turn / approval / event stream 的事实权威

同时必须满足：

- 一个 `run` 一旦绑定 `snapshot_id`，其代码上下文不可变
- A 必须记录 `run_id -> snapshot_id -> base_commit -> source_workspace_generation`
- 节点事实可以补充观测字段，但不能直接回写主状态机最终态

### 5.2 开发执行与运行执行分层

V2 明确区分两类执行面：

- 开发面执行：发生在 B 上，用于开发中的快速运行、中间测试、局部验证和反馈闭环
- 运行面执行：发生在 C 上，用于重要阶段验证、准生产验证、稳定日志采集和最终线上运行
- 诊断型执行：无论发生在 B 还是 C，只要需要 agent 推理、会话、approval、交互决策，就由节点上已配置的 provider 承载

其中标准化命令执行仍然优先由 `run_executor` 承载，但不意味着只有 C 才运行代码。

这条原则的目的很明确：

- 让 B 保持高频、低成本的本地反馈闭环
- 让 C 承担更稳定、更接近线上环境的重要阶段验证
- 保证关键验证路径可审计、可缓存、可恢复、可重放
- 不把所有运行链路都会话化
- 把 agent 参与限制在确有需要的诊断场景

### 5.3 数据不直连，统一经控制面编排

V2 默认不采用 B/C 直连。

控制链、状态链、数据链都以 A 为编排中心：

- 控制命令经 A 路由
- 状态事件回报到 A 聚合
- snapshot 默认经 A 的 `SnapshotCatalog` 中转和管理

这样做的收益是：

- 审计收敛在 A
- 节点之间不必互信和互相暴露入口
- 幂等、恢复、TTL、GC 有统一落点
- 节点短暂离线时，控制面仍可保有最小观测能力

### 5.4 单机路径不退化

支持多节点不能破坏当前单机体验。

必须坚持：

- 本地项目继续可直接运行
- 不要求用户为了本地项目先搭一套远程 node agent 体系
- 同一套产品对象要同时覆盖本地和多节点

V2 的抽象边界应该是“同一产品模型，两种运行后端”，而不是“两套产品”。

## 6. 总体结构

### 6.1 总体组件

V2 架构由以下组件构成：

- `Control Plane`：运行在 A，负责编排、状态机、项目/节点注册、审计、CLI/TUI/API 入口
- `Node Agent`：运行在各执行节点，负责承接控制协议、管理本地 workspace、provider、executor 和日志接口
- `Provider Runtime`：节点内的 agent runtime，如 Claude、codex、kimi、DeepSeek 对应的桥接层
- `Run Executor`：节点内标准化运行执行器，负责测试、服务启动、脚本执行和结构化结果回报，可部署在 B 或 C，但 C 上的结果承担更高权威
- `SnapshotCatalog`：由 A 持有的 snapshot 元数据和 payload 存储目录
- `Artifact / Log Store`：默认由运行节点 C 保留完整日志和原始产物，A 只缓存摘要和索引

### 6.2 逻辑分层

推荐把系统切成三层：

1. 编排层
   A 侧 Dalek 状态机、项目拓扑、任务编排、恢复与审计。
2. 节点协议层
   `A <-> node agent` 的统一控制协议，负责节点注册、任务路由、snapshot 传输、日志查询、状态回补。
3. 节点执行层
   `node agent <-> local provider/run_executor` 的本地桥接协议，负责会话、执行、approval、日志和产物。

这三层分清之后，控制面不需要理解各 provider 的全部进程细节，也不需要持有远程 shell。

### 6.3 支持的部署形态

V2 至少应支持：

- 单机形态：A/B/C 共用一台机器
- 双机形态：A+B 同机、C 独立；或 A+C 同机、B 独立
- 多机形态：A、B、C 分散部署
- 扩展形态：多个 B 或多个 C 组成 dev/run 节点池

这些形态应共享同一套对象模型，只在节点拓扑与调度策略上有差异。

## 7. 领域对象与关系

### 7.1 Node

`node` 是控制面上的一等公民，表示一个可被 Dalek 调度的节点实例。

建议字段：

- `name`
- `role_capabilities`
- `endpoint`
- `auth_mode`
- `status`
- `version`
- `protocol_version`
- `provider_modes`
- `provider_status`
- `default_provider`
- `provider_configs`
- `provider_capabilities`
- `session_affinity`
- `last_seen_at`

设计约束：

- `role_capabilities` 和 `provider_modes` 必须正交
- provider 能力必须显式上报，不能靠推断
- 节点暴露给控制面的入口是 `node agent`，不是裸 provider

### 7.2 Project

`project` 描述项目的拓扑、默认策略和运行约束。

建议字段：

- `name`
- `key`
- `default_branch`
- `env_profile`
- `node_provider_policy`
- `default_dev_provider`
- `default_verify_provider`
- `default_dev_assignment`
- `default_run_assignment`

设计约束：

- `project` 只保存拓扑和默认策略
- provider 凭证、启动方式等节点实例细节不放进 `project`
- `project.dev_node` / `project.run_node` 只适合作为默认选择，不应成为长期唯一模型

更稳妥的底层关系应接近：

- `project -> workspace assignment -> node`

### 7.3 Workspace

`workspace` 表示某个项目在某个节点上的一个可执行工作区绑定。

建议字段：

- `project`
- `node`
- `role`
- `repo_root`
- `default_branch`
- `bootstrap_status`
- `env_status`
- `workspace_generation`
- `desired_revision`
- `current_revision`
- `dirty_policy`
- `bootstrap_fingerprint`
- `capacity_hint`
- `last_verified_at`
- `last_error`

其中 `workspace_generation` 是恢复和日志对账的关键字段。控制面需要知道一次 run 绑定的是哪一代工作区，避免节点重建后串台。

### 7.4 Snapshot

`snapshot` 表示一次可传输、可校验、可应用的代码状态描述。

建议字段：

- `snapshot_id`
- `project`
- `source_node`
- `source_workspace`
- `base_commit`
- `mode`
- `includes_untracked`
- `file_count`
- `payload_ref`
- `snapshot_digest`
- `payload_size`
- `payload_encoding`
- `supports_binary`
- `supports_symlink`
- `supports_rename`
- `created_at`
- `created_by`
- `expires_at`

设计约束：

- snapshot 必须基于明确的 `base_commit`
- snapshot 必须绑定明确冻结点
- digest 必须能校验完整性，不能只靠 `snapshot_id`
- 影响运行结果的环境声明文件也必须纳入 snapshot 上下文

### 7.5 Task / Run / Thread

V2 明确三者关系如下：

- `task` 是唯一持久化执行事实源
- `run` 是 `task(kind=run_verify)` 的跨节点读模型
- `thread` 仅表示节点内 provider 的会话事实和诊断上下文

这意味着：

- CLI/TUI/API 的核心产品对象仍是 `ticket` 与 `task`
- `run` 用于日志、产物、运行摘要等 specialized view
- 控制动作如取消、重试、恢复应最终落到 `task`
- provider 事件只能补充 `task/run` 观测字段，不能直接改写最终状态机

## 8. 关键链路

### 8.1 节点注册与能力声明

每个节点启动后通过 `node agent` 主动连到 A。

注册时至少要上报：

- 节点身份
- 角色能力
- provider 能力
- 版本与协议版本
- 可用 workspace 概况

控制面据此建立节点注册表，并据此做拓扑绑定和后续调度。

### 8.2 开发执行链

开发发生在 B 上：

- A 将开发任务派发到 B
- B 上 provider 在本地 dev workspace 中执行开发 agent
- B 可以直接运行项目代码，完成开发中的快速运行、中间测试和局部验证
- B 产生代码变更、thread、turn、approval 和本地上下文
- 这些会话事实由节点 provider 管理，再由 node agent 投影给 A

因此 B 不是“只写代码不运行”的角色。B 本身就承担开发反馈闭环，只是这些运行更多服务于快速迭代，而不是最终权威验证。

### 8.3 运行验证链

运行验证链路分成两层：

- B 上的本地运行，服务于开发中的中间测试和快速反馈
- C 上的运行，服务于重要阶段验证、准生产验证和最终线上运行

跨节点验证的标准链路是：

1. B 发起 `run request`
2. A 完成幂等和授权校验，创建 `task(kind=run_verify)` 及 `run view`
3. A 要求 B 生成 snapshot
4. B 冻结源码上下文并生成 snapshot
5. B 将 snapshot 上传至 A 的 `SnapshotCatalog`
6. A 校验并把 run 路由到 C
7. C 拉取 snapshot，应用到 run workspace
8. C 执行环境准备与 preflight
9. C 通过 `run_executor` 执行重要阶段的标准 verify
10. 若需要诊断，C 再调用节点上已配置 provider
11. C 向 A 回报状态、日志尾部、摘要、产物索引
12. A 聚合后反馈给 B

这条链路的目标是把“B 上高频开发反馈”与“C 上关键阶段运行事实”通过 A 稳定地衔接起来。

## 9. 通信与数据传递模型

### 9.1 通信模型

V2 优先采用节点主动连接控制面的模式。

推荐拆成两类协议：

- `A <-> node agent`
  负责节点注册、任务路由、snapshot 传输、日志查询、状态回补。
- `node agent <-> local provider/run_executor`
  负责会话调用、运行执行、approval、事件桥接。

跨节点命令统一要求：

- `request_id`
- `message_id`
- `project_key`
- `task_id` / `run_id`
- `attempt`
- `sent_at`
- `deadline_at`
- `protocol_version`

### 9.2 Snapshot 传递

V2 默认不做 B/C 直连传输。

默认链路是：

- B 生成 snapshot
- B 上传到 A 的 `SnapshotCatalog`
- A 校验并登记引用
- C 从 A 拉取 payload
- C 校验 digest 后 apply

这个设计应保持为默认值，因为它同时解决了：

- 代码验证审计
- payload 生命周期管理
- 幂等与恢复
- 节点之间的安全边界

### 9.3 Snapshot 最小实现

V2 推荐的最小实现是：

1. B 在冻结点生成 `manifest + payload`
2. `payload` 默认为 `tar.gz` 或 patch 包
3. B 通过 node agent 分块上传到 A
4. A 落盘到 `SnapshotCatalog`
5. A 校验 digest 后，把 `snapshot_ref` 绑定到 `run_id`
6. C 流式下载 payload
7. C 再次校验 digest 后 apply
8. C 返回结构化 `apply_result`

边界约束：

- V2 默认不做 P2P 直传
- snapshot 不存入数据库大字段
- `SnapshotCatalog` 负责 TTL、引用计数、大小限制和 GC
- 大 payload 必须支持 chunking

### 9.4 日志与产物

V2 采用查询型日志模型，而不是默认实时全量流式模型。

职责分工：

- C 保留完整日志和原始产物
- A 缓存摘要、tail、索引和小型元数据
- B 需要调试时，再经 A 查询 C 上日志和产物

日志查询必须至少绑定：

- `run_id`
- `snapshot_id` 或 `commit_id`
- `ticket_id`
- `node`

否则日志上下文会混淆。

## 10. 运行环境模型

### 10.1 环境一致性的定义

B 和 C 不需要操作系统层完全一致，但 C 必须具备可重建、可校验的运行环境。

项目运行所需依赖必须落在项目声明中，而不能只存在于 B 的临时机器状态里。

### 10.2 环境准备三阶段

V2 明确把运行环境处理拆为三层：

- `bootstrap`
  低频、可缓存、可重建的基础环境准备。
- `repair`
  检测到 drift 时才执行的依赖修复。
- `preflight`
  每次 run 前的轻量只读校验。

这样可以避免 C 退化成“每次运行都做一遍半部署”。

### 10.3 运行声明纳入 snapshot 上下文

影响运行结果的项目内声明文件应默认纳入 snapshot 上下文，例如：

- 源码
- lockfile
- 项目内 bootstrap 脚本
- `.dalek/runtime-env.yaml`

缓存必须建立在这组输入的 fingerprint 上，而不是只靠 `base_commit` 猜测。

## 11. 状态机与观测模型

### 11.1 Run 状态机

跨节点验证必须有显式状态机。

建议最小状态集合：

- `requested`
- `queued`
- `snapshot_preparing`
- `snapshot_ready`
- `dispatching`
- `env_preparing`
- `ready_to_run`
- `waiting_approval`
- `running`
- `canceling`
- `node_offline`
- `reconciling`
- `timed_out`
- `succeeded`
- `failed`
- `canceled`

终态仅应包括：

- `succeeded`
- `failed`
- `canceled`
- `timed_out`

其他术语应作为原因码、子状态或补充字段，而不是和终态并列。

### 11.2 Approval 映射

若节点 provider 触发 approval：

- approval 先映射为 Dalek inbox
- 对应 task 进入 `waiting_user`
- 必要时 ticket 进入 `blocked`
- approval 超时默认拒绝
- 标准 verify target 默认禁止进入 approval

### 11.3 观测对象

用户视角下的观测主入口应保持稳定：

- `ticket`：需求闭环
- `task`：执行与事件链
- `run`：日志、产物、运行摘要视图

不要让 CLI、TUI、API 分别使用不同的主对象语言。

## 12. 安全、恢复与调度

### 12.1 安全边界

V2 最少要做到：

- 节点认证
- 项目级授权
- 动作级授权
- 请求幂等
- 心跳与存活检测
- 版本兼容性检查
- node agent 与本地 provider/executor 的进程级隔离

并且必须满足：

- C 只接受来自受信 A 的 allowlist 动作
- 执行命令必须来自 project-scoped 模板，而不是任意 shell
- provider 默认只监听本机回环或受限本地 socket

### 12.2 恢复策略

恢复面至少要支持：

- A 重启后，按 `run_id` / `request_id` 向节点补拉状态
- run 状态事件、thread 事件、artifact/log 索引分开回补
- C 断线时默认继续执行，连回后补报最终状态
- snapshot apply 失败时，明确是否已回滚
- 产物上传失败时，不覆盖执行结果本身

推荐引入：

- 节点连接 lease
- 控制面 session epoch
- per-run fencing token

否则旧消息可能覆盖新状态。

### 12.3 调度最小规则

V2 在调度上至少先定以下规则：

- 单个 `run workspace` 串行执行
- 单节点最大并发静态配置
- 默认先到先服务
- 单项目默认不能占满全部运行容量
- V2 不做抢占，只支持排队和显式取消

## 13. 产品面与实现落点

### 13.1 产品对象

建议新增或补强以下用户入口：

- `dalek node add|ls|show|rm`
- `dalek run request|show|logs|artifact ls`

但语义上要坚持：

- `run` 是 `task(kind=run_verify)` 的 specialized view
- 用户主入口仍然是 `ticket` 和 `task`

### 13.2 内部组件建议

V2 推荐的内部落点包括：

- `NodeRegistry`
- `NodeSessionManager`
- `NodeAgentClient`
- `RemoteProjectFacade`
- `ProviderBridge`
- `RunScheduler`
- `RunService`
- `RunReconciler`
- `SnapshotCatalog`
- `ArtifactIndexService`
- `ThreadMirror`

### 13.3 本地与远程 facade 分层

实现前必须先切开：

- 本地项目走 `local facade`
- 多节点项目走 `remote facade`

facade 之上禁止再散落“直接读本地 repo/SQLite/路径”的远程特判。

## 14. 分阶段落地建议

### Phase 0：定模型

- 定义 node 模型
- 定义 project / workspace assignment 模型
- 定义 snapshot 与 run 契约
- 定义 provider 配置模型

### Phase 1：先打通 A + C

- 引入 control role + run role
- 先验证远程 run、日志查询、状态回补
- 保持开发仍走本地路径

### Phase 2：引入 B

- dev role 成为正式远程角色
- 打通 `B -> A -> C` 的 run 请求链路
- 引入 snapshot 同步

### Phase 3：补足稳定性

- 重试与恢复协议
- lease / epoch / fencing token
- 产物保留策略
- 节点版本协商
- 更好的 TUI 支持
- B/C 节点池与容量调度

## 15. 最终结论

V2 的核心不是“把 Dalek 改成远程 SSH 编排器”，而是把 Dalek 现有的本地项目模型提升为一套统一的多角色运行模型。

在这套模型里：

- A 负责控制与状态机
- B 负责源码上下文与开发执行
- C 负责运行验证与结果事实
- provider 按节点配置，不按角色写死
- snapshot 默认经 A 管理，不做节点间裸传
- 本地项目路径继续保留

这样做的结果是：

- 单机、本地、多机都在同一套抽象下
- 标准验证路径保持可审计、可恢复、可重放
- 诊断链路可以引入异构 provider，但不会污染主状态机
- 后续扩展到多 dev / 多 run 节点池时不需要翻掉模型

## 16. 基于当前代码的落地判断

V2 文档本身是完整的目标设计，但结合当前仓库实现，Dalek 还处于“具备控制面基座、尚未进入多节点对象化实现”的阶段。

当前已经具备、可以直接复用的基础包括：

- `daemon + ExecutionHost + InternalAPI` 已经提供中心编排、异步 submit、request_id 幂等、run 查询与取消能力
- `Project facade` 已经把 CLI/TUI 和底层 services 隔开，具备演进为 `local facade / remote facade` 的结构基础
- task/run 观测链路、事件链、runtime health、semantic next action 已经存在
- provider 配置、daemon 进程管理、gateway 链路、subagent 执行框架已经形成较稳定基座

当前明确缺失、必须补齐的核心能力包括：

- 缺少多节点一等对象：`node`、`workspace assignment`、`snapshot`、`run`
- 缺少 `task(kind=run_verify)` 及其对应的 run 视图
- 缺少 node agent 协议与节点注册、心跳、能力声明、授权模型
- 缺少 `SnapshotCatalog`、snapshot 打包/传输/apply/GC
- 缺少远程运行调度、恢复、fencing、跨节点日志与产物查询
- 缺少 `local facade / remote facade` 清晰分层

因此，推荐的落地顺序不是“先写远程节点通信”，而是：

1. 先把领域模型、状态机和 facade 边界切出来
2. 再打通 `A + C`
3. 再补 `B -> A -> C` snapshot 链路
4. 最后补恢复、安全、调度和产品化入口

## 17. 实施策略

### 17.1 总体原则

- `run` 不是独立于 task 的第二套执行体系，而是 `task(kind=run_verify)` 的 specialized view
- 本地单机路径必须始终保持可用，且不能因多节点引入而退化
- 多节点实现优先复用现有 daemon/task/event/CLI 基座，而不是重写一套平行系统
- 所有跨节点关键状态都必须形成事件链和结构化错误，不允许“卡住但不可诊断”
- 每个 phase 都必须带测试与验收，不接受“功能先落地、验证后补”

### 17.2 推荐 phase 顺序

1. 先完成模型、migration、run service、facade 分层
2. 再完成节点注册与 `A + C` 远程运行闭环
3. 再完成 snapshot catalog 与 `B -> A -> C`
4. 最后完成恢复、安全、调度、CLI/TUI 收口

## 18. 可执行 Ticket 拆解

以下 ticket 是基于当前代码结构给出的建议拆解。目标是让后续 backlog 直接映射到 package 和测试文件，而不是停留在概念层。

### T1：Run / Node / Snapshot 领域建模

目标：

- 引入 V2 最小一等对象模型
- 在 contracts 层正式定义 `run_verify`、node、snapshot、workspace assignment

建议落点：

- `internal/contracts/task_status.go`
- `internal/contracts/node.go`
- `internal/contracts/run.go`
- `internal/contracts/snapshot.go`
- `internal/contracts/workspace_assignment.go`

主要改动：

- 新增 `TaskTypeRunVerify`
- 定义 run 状态枚举
- 定义 node role/provider 能力模型
- 定义 snapshot 元数据与 digest 契约
- 定义 workspace generation 与绑定关系

验证要求：

- contracts 单测覆盖枚举、normalize、兼容旧 task 类型
- 明确 `run_id -> snapshot_id -> base_commit -> workspace_generation` 的唯一绑定规则

### T2：DB Migration 与持久化骨架

目标：

- 把 V2 新对象落入持久化层，并保持老项目自动升级

建议落点：

- `internal/store`
- `internal/repo`
- `internal/app/home.go`

主要改动：

- 新增 `nodes`
- 新增 `workspace_assignments`
- 新增 `snapshots`
- 新增 `run_views` 或等价读模型表
- 增加 migration 与索引

验证要求：

- migration 测试
- 老 DB 升级测试
- 唯一索引、幂等创建、关联完整性测试

### T3：RunService 与 Run View

目标：

- 正式引入 `task(kind=run_verify)` 与 run specialized view

建议落点：

- `internal/services/run/service.go`
- `internal/services/task`
- `internal/app/daemon_runtime.go`

主要改动：

- create run request
- run 状态机推进
- run show / events / cancel / artifact 索引查询
- task 到 run view 的投影

验证要求：

- `run_verify` 幂等测试
- run/task 状态一致性测试
- 取消、失败、超时、终态投影测试

### T4：Local Facade / Remote Facade 分层

目标：

- 提前切清本地与多节点边界，避免后续远程逻辑污染现有 facade

建议落点：

- `internal/app/project.go`
- `internal/app/project_local_facade.go`
- `internal/app/project_remote_facade.go`
- `internal/app/daemon.go`

主要改动：

- 本地项目继续走既有路径
- 多节点项目走 remote facade
- app 层对上保持稳定接口

验证要求：

- facade contract tests
- 现有 ticket / worker / dispatch 回归测试

### T5：NodeRegistry / NodeSessionManager

目标：

- 建立控制面的节点注册表和会话期管理能力

建议落点：

- `internal/services/node/registry.go`
- `internal/services/node/session_manager.go`
- `internal/services/daemon/execution_host.go`

主要改动：

- 节点注册
- 心跳与 last_seen
- lease / session epoch 基础管理
- provider 与 role capability 上报

验证要求：

- 注册、重复注册、失联、重连测试
- epoch 递增与旧 session 拒绝测试

### T6：A + C 最小 Node Agent 协议

目标：

- 在不引入 B 的前提下，先打通 control role 与 run role 的最小闭环

建议落点：

- `internal/services/daemon/api_internal.go`
- `internal/services/nodeagent/protocol.go`
- `internal/services/nodeagent/client.go`
- `cmd/dalek/cmd_node.go`

主要改动：

- node register
- heartbeat
- run dispatch / query / cancel
- logs tail / artifact index 查询
- 鉴权从 loopback-only 演进到 node token / allowlist

验证要求：

- 协议编解码测试
- handler 测试
- `A + C` 双进程集成测试

### T7：Run Executor 与远程 Verify

目标：

- 让 C 节点能执行标准化 verify target，而不是任意 shell

建议落点：

- `internal/services/runexecutor/service.go`
- `internal/repo/config.go`

主要改动：

- verify target 模板
- `bootstrap / repair / preflight` 三阶段接口
- run status、日志、artifact 索引上报

验证要求：

- target 模板校验测试
- 成功、失败、超时、取消测试
- 禁止任意 shell 注入测试

### T8：SnapshotCatalog 与 B -> A -> C 同步

目标：

- 引入 dev role，支持未提交工作区验证

建议落点：

- `internal/services/snapshot/catalog.go`
- `internal/services/snapshot/pack.go`
- `internal/services/snapshot/apply.go`
- `internal/services/snapshot/gc.go`

主要改动：

- snapshot manifest + payload
- chunk upload / download
- digest 校验
- apply 到 run workspace
- TTL / refcount / GC

验证要求：

- 打包、下载、digest、apply、GC 单测
- `B -> A -> C` 集成测试
- base commit mismatch、apply conflict、partial failure 测试

### T9：恢复、安全、调度

目标：

- 把多节点链路从“能跑”提升到“可恢复、可控、可审计”

建议落点：

- `internal/services/run/reconciler.go`
- `internal/services/node/session_manager.go`
- `internal/services/run/scheduler.go`

主要改动：

- fencing token
- A 重启回补
- node offline -> reconciling
- 单 run workspace 串行
- 单节点并发上限
- 单项目容量限制
- 请求幂等与旧消息抑制

验证要求：

- A/C 重启恢复测试
- 重复消息、旧 epoch 回放测试
- 并发排队与串行约束测试
- artifact 上传失败不污染终态测试

### T10：CLI / TUI / 文档收口

目标：

- 将 V2 多节点能力产品化，对用户可见且可操作

建议落点：

- `cmd/dalek/cmd_node*.go`
- `cmd/dalek/cmd_run*.go`
- `internal/ui/tui`
- `docs/`

主要改动：

- `dalek node add|ls|show|rm`
- `dalek run request|show|logs|artifact ls|cancel`
- TUI run 视图
- 部署、恢复、排障文档

验证要求：

- CLI golden tests
- TUI model tests
- 文档驱动 smoke test

## 19. 推荐开发顺序

建议顺序如下：

1. `T1 -> T2 -> T3 -> T4`
2. `T5 -> T6 -> T7`
3. `T8`
4. `T9`
5. `T10`

原因：

- 当前最大风险不在“网络通信写不出来”，而在“对象模型和本地/远程边界还没切开”
- 如果在 `T1-T4` 之前直接写 node agent，很容易把远程特判散落到现有本地路径里
- `A + C` 能先把最关键的远程运行链路跑通，且对当前代码侵入最小
- `B -> A -> C` 与 snapshot 明显更复杂，应该建立在 run service、node session、executor 都稳定之后

## 20. 验证与测试计划

### 20.1 测试分层

- 单元测试
  - 目标：状态机、schema、digest、策略选择、payload 校验
- 组件测试
  - 目标：单组件带 fake / stub 依赖运行
- 集成测试
  - 目标：真实 DB + 本地 HTTP/WS + 多 goroutine
- E2E 拓扑测试
  - 目标：验证单机、双机、三机部署形态
- 故障注入测试
  - 目标：验证恢复、幂等、断线、旧消息、部分失败

### 20.2 Phase 对应验证要求

`T1-T3`

- `go test ./internal/contracts ./internal/services/task ./internal/services/run`
- 验证 `run_verify` 创建、投影、状态推进与终态一致性

`T4-T7`

- `go test ./internal/app ./internal/services/daemon ./internal/services/nodeagent ./internal/services/runexecutor`
- 验证 local / remote facade contract
- 验证 `A + C` 双节点最小远程运行闭环

`T8`

- `go test ./internal/services/snapshot/...`
- 验证打包、传输、校验、apply、GC

`T9`

- 新增多进程集成测试，例如 `cmd/dalek/e2e_multi_node_*_test.go`
- 验证恢复、安全、调度与并发行为

`T10`

- CLI golden tests
- TUI model tests
- 文档驱动 smoke test

### 20.3 必须具备的 E2E 场景

- 单机 `A=B=C` 不回归现有工作流
- `A + C` 双节点：commit-based run 成功 / 失败 / 取消
- `A + B + C` 三节点：snapshot-based run 成功 / 失败
- A 重启恢复
- C 断线恢复
- snapshot digest mismatch
- run workspace 串行约束
- 同 `request_id` 幂等
- artifact 上传失败但执行结果仍保留

### 20.4 质量门槛

每个 phase 都应满足以下门槛：

- 新能力必须带单测，且至少有一层组件或集成测试
- 所有新状态都必须有事件链与结构化错误
- 失败后必须能通过 CLI 查询到原因
- `go test ./...` 必须通过
- 现有 daemon / dispatch / worker 相关 E2E 不得回归

## 21. 里程碑与验收

### M1：模型与边界切开

完成范围：

- `T1-T4`

验收标准：

- 本地路径零回归
- `run_verify` 已可在本地模型中创建和观测
- `local facade / remote facade` 已切开

### M2：A + C 远程运行闭环

完成范围：

- `T5-T7`

验收标准：

- control role 可调度 run role
- 远程 verify、日志查询、状态回补可用
- 双节点集成测试通过

### M3：B -> A -> C Snapshot 闭环

完成范围：

- `T8`

验收标准：

- 支持未提交工作区验证
- snapshot 生成、上传、下载、apply、校验完整可用
- 三节点集成测试通过

### M4：恢复、安全、产品化

完成范围：

- `T9-T10`

验收标准：

- 恢复、安全、调度达到可发布标准
- CLI / TUI / 文档齐备
- 多拓扑 E2E 与故障注入测试通过

## 22. 执行 Backlog

本节将前述 10 个 ticket 进一步细化为可执行 backlog。每个 ticket 都按“目标 / 改动文件 / 验收标准 / 测试用例”展开，便于直接进入开发。

---

### Ticket T1：Run / Node / Snapshot 领域建模

目标：

- 在 contracts 层正式引入多节点最小对象模型
- 定义 `task(kind=run_verify)` 的领域语言
- 建立 node、snapshot、workspace assignment 的统一命名和约束

改动文件：

- `internal/contracts/task_status.go`
- `internal/contracts/task_runtime_input.go`
- `internal/contracts/task.go`
- `internal/contracts/node.go`
- `internal/contracts/run.go`
- `internal/contracts/snapshot.go`
- `internal/contracts/workspace_assignment.go`
- `internal/contracts/*_test.go`

验收标准：

- 新增 `TaskTypeRunVerify`
- run 状态枚举与 V2 文档保持一致，至少覆盖 `requested / queued / snapshot_preparing / snapshot_ready / dispatching / env_preparing / ready_to_run / waiting_approval / running / canceling / node_offline / reconciling / timed_out / succeeded / failed / canceled`
- node 模型可表达 `role_capabilities`、`provider_modes`、`default_provider`、`protocol_version`
- snapshot 模型可表达 `snapshot_id / base_commit / source_workspace_generation / digest / payload_ref`
- workspace assignment 模型可表达 `project -> workspace -> node` 绑定关系
- contracts 层不存在与当前 worker / dispatch 语义冲突的字段定义

测试用例：

- `TaskTypeRunVerify` 可被正常解析、持久化、投影
- run 状态枚举 normalize 与非法值拒绝
- node capability/provider capability 正交性校验
- snapshot 元数据缺字段时报错
- `run_id -> snapshot_id -> base_commit -> workspace_generation` 绑定结构可完整表达

---

### Ticket T2：DB Migration 与持久化骨架

目标：

- 将 node、workspace assignment、snapshot、run view 落入数据库
- 保持旧项目 schema 自动升级且不破坏现有数据

改动文件：

- `internal/store/*`
- `internal/repo/*`
- `internal/app/home.go`
- `internal/app/upgrade.go`
- `internal/contracts/*`
- migration 相关测试文件

验收标准：

- 新增表：`nodes`
- 新增表：`workspace_assignments`
- 新增表：`snapshots`
- 新增表：`run_views` 或等价读模型表
- migration 可从当前 schema 无损升级
- 新表具备核心唯一约束：
  - node name 唯一
  - `(project, request_id)` 或等价 run 请求幂等键
  - snapshot id 唯一
- 老项目升级后现有 ticket / worker / task 查询仍可运行

测试用例：

- 从空库初始化新 schema
- 从现有 schema 升级到新 schema
- 重复创建 node 被唯一索引拒绝
- 重复写入 snapshot id 被拒绝
- run view 与 task run 关联可回查
- migration 后现有 `go test ./internal/app` 不回归

---

### Ticket T3：RunService 与 Run View

目标：

- 引入 `RunService`
- 把 `run = task(kind=run_verify)` 作为正式读模型建立起来
- 让 run 生命周期、状态机、事件、取消、查询有统一服务入口

改动文件：

- `internal/services/run/service.go`
- `internal/services/run/types.go`
- `internal/services/run/view.go`
- `internal/services/run/service_test.go`
- `internal/services/task/*`
- `internal/app/project_task.go`
- `internal/app/daemon_runtime.go`
- `internal/app/api_types.go`

验收标准：

- 可创建 `run_verify` task
- 可按 `run_id` 查询 run view
- 可按 `task_run_id` 回查 run 视图
- run 终态只能是 `succeeded / failed / canceled / timed_out`
- run query / events / cancel API 形成稳定 facade
- task 状态推进与 run 状态推进保持一致，不产生双写冲突

测试用例：

- `request_id` 相同的 run request 幂等
- run 成功结束时 task/run 都进入终态
- run 取消时 task/run 都进入 canceled
- 非法状态回滚被拒绝
- run show 能返回 snapshot 绑定信息
- run events 能反映关键状态流转

---

### Ticket T4：Local Facade / Remote Facade 分层

目标：

- 将“本地项目路径”和“多节点路径”在 app 层切开
- 为后续 remote facade 留出稳定接口

改动文件：

- `internal/app/project.go`
- `internal/app/project_local_facade.go`
- `internal/app/project_remote_facade.go`
- `internal/app/facade_types.go`
- `internal/app/daemon.go`
- `internal/app/*_test.go`

验收标准：

- 现有本地项目仍通过 local facade 路径工作
- remote facade 至少定义出 run/node/snapshot 所需接口
- CLI/TUI 不直接依赖底层 repo/path/SQLite 远程特判
- 现有 gateway / daemon / worker 路径不因 facade 拆分而回归

测试用例：

- 本地 ticket start / dispatch / worker report 行为不变
- local facade 与 remote facade 都能满足统一接口
- remote facade 未实现的动作返回明确错误而不是 panic
- facade 选择逻辑可根据项目拓扑稳定切换

---

### Ticket T5：NodeRegistry / NodeSessionManager

目标：

- 建立控制面的节点注册表、会话和存活检测能力
- 为调度、恢复、权限控制打基础

改动文件：

- `internal/services/node/registry.go`
- `internal/services/node/session_manager.go`
- `internal/services/node/types.go`
- `internal/services/node/registry_test.go`
- `internal/services/node/session_manager_test.go`
- `internal/services/daemon/execution_host.go`
- `internal/services/daemon/execution_host_types.go`

验收标准：

- 节点可注册、查询、更新状态
- 节点会话带 `session epoch`
- heartbeat 可更新 `last_seen_at`
- 超过 lease 的节点会进入离线或不可调度状态
- 节点能力包括 role/provider/protocol version
- registry 提供最小调度查询能力

测试用例：

- 新节点注册成功
- 同名节点重复注册行为符合预期
- heartbeat 更新 `last_seen_at`
- lease 过期后节点进入 offline
- 旧 session epoch 心跳被拒绝
- provider capability 查询结果正确

---

### Ticket T6：A + C 最小 Node Agent 协议

目标：

- 打通 control role 与 run role 的最小远程运行协议
- 先支持注册、心跳、run submit、run query、run cancel、日志尾部查询

改动文件：

- `internal/services/daemon/api_internal.go`
- `internal/services/nodeagent/protocol.go`
- `internal/services/nodeagent/client.go`
- `internal/services/nodeagent/server.go`
- `internal/services/nodeagent/*_test.go`
- `cmd/dalek/cmd_node.go`
- `cmd/dalek/cmd_node_agent.go`

验收标准：

- 节点可向 A 注册并维持 heartbeat
- A 可向 C 下发 `run_verify`
- C 可回报状态、日志 tail、artifact index 摘要
- C 可接受 cancel
- 鉴权不再依赖 loopback-only，支持 node token 或等价认证
- 协议载荷包含 `request_id / project_key / task_id or run_id / attempt / protocol_version`

测试用例：

- node register 成功
- 未授权 token 注册失败
- run submit 到远程 C 成功进入 queued/running
- run query 返回远程状态
- run cancel 生效
- 远程节点断线后查询状态返回可解释结果

---

### Ticket T7：Run Executor 与远程 Verify

目标：

- 在 C 上引入标准化 verify 执行器
- 避免“node agent = 任意远程 shell”

改动文件：

- `internal/services/runexecutor/service.go`
- `internal/services/runexecutor/types.go`
- `internal/services/runexecutor/template.go`
- `internal/services/runexecutor/service_test.go`
- `internal/repo/config.go`
- `internal/app/project_remote_facade.go`

验收标准：

- verify 目标来自 project-scoped 模板
- 执行过程分为 `bootstrap / repair / preflight / run`
- run_executor 能返回结构化结果：
  - exit code
  - duration
  - summary
  - artifact 索引
- run_executor 默认不支持任意 shell 透传
- 重要失败原因能映射成 run state 和 error code

测试用例：

- 合法 verify target 可执行
- 非法 verify target 被拒绝
- preflight 失败进入 `failed` 或 `env_preparing` 失败原因
- 超时后进入 `timed_out`
- cancel 中断执行
- artifact 索引可回传到 A

---

### Ticket T8：SnapshotCatalog 与 B -> A -> C 同步

目标：

- 建立 snapshot 打包、上传、下载、apply、GC 能力
- 打通 `B -> A -> C` 未提交代码验证链路

改动文件：

- `internal/services/snapshot/catalog.go`
- `internal/services/snapshot/pack.go`
- `internal/services/snapshot/apply.go`
- `internal/services/snapshot/manifest.go`
- `internal/services/snapshot/gc.go`
- `internal/services/snapshot/*_test.go`
- `internal/services/nodeagent/*`
- `internal/services/run/service.go`

验收标准：

- B 能冻结工作区并生成 snapshot
- snapshot 包含 manifest + payload
- A 能分块接收并校验 digest
- A 能把 `snapshot_ref` 绑定到 `run_id`
- C 能下载、校验、apply 到 run workspace
- apply 结果结构化，能区分成功、冲突、部分失败、已回滚/未回滚
- `SnapshotCatalog` 具备 TTL、refcount、GC

测试用例：

- 普通 snapshot 打包与 apply 成功
- digest mismatch 被拒绝
- base commit mismatch 被拒绝
- apply conflict 返回结构化错误
- payload 分块上传/下载成功
- snapshot 被 run 引用时 GC 不删除
- snapshot 引用释放后 GC 能回收

---

### Ticket T9：恢复、安全、调度

目标：

- 补齐 lease / epoch / fencing token、恢复与并发控制
- 让多节点运行在中断、重启、重复消息下仍可控

改动文件：

- `internal/services/run/reconciler.go`
- `internal/services/run/scheduler.go`
- `internal/services/run/reconciler_test.go`
- `internal/services/run/scheduler_test.go`
- `internal/services/node/session_manager.go`
- `internal/services/nodeagent/client.go`
- `internal/services/daemon/execution_host.go`

验收标准：

- A 重启后可按 `run_id / request_id` 补拉状态
- node offline 时 run 进入 `node_offline` 或 `reconciling`
- 旧 epoch/旧 fencing token 消息不会覆盖新状态
- 单 `run workspace` 串行执行
- 单节点并发上限可配置
- 单项目默认不能吃满全部运行容量
- artifact 上传失败不覆盖执行终态

测试用例：

- A 重启后恢复中的 run 能重新被查询
- C 断线继续执行，恢复后补报成功
- 旧 session 消息被丢弃
- 同一 workspace 上两个 run 不并发执行
- 节点并发上限达到后进入排队
- artifact 上传失败后 run 仍保持 succeeded/failed 原始结果

---

### Ticket T10：CLI / TUI / 文档收口

目标：

- 对外提供完整的多节点操作入口和可观测性
- 完成部署、恢复、运维侧产品收口

改动文件：

- `cmd/dalek/cmd_node.go`
- `cmd/dalek/cmd_node_add.go`
- `cmd/dalek/cmd_node_ls.go`
- `cmd/dalek/cmd_node_show.go`
- `cmd/dalek/cmd_node_rm.go`
- `cmd/dalek/cmd_run.go`
- `cmd/dalek/cmd_run_request.go`
- `cmd/dalek/cmd_run_show.go`
- `cmd/dalek/cmd_run_logs.go`
- `cmd/dalek/cmd_run_artifact.go`
- `cmd/dalek/cmd_run_cancel.go`
- `internal/ui/tui/*`
- `docs/*`
- CLI/TUI 对应测试文件

验收标准：

- 支持 `dalek node add|ls|show|rm`
- 支持 `dalek run request|show|logs|artifact ls|cancel`
- run query 中能看到 node、snapshot、状态、关键错误
- TUI 中 ticket / task / run 三层视图术语一致
- 部署文档覆盖单机、双机、三机
- 排障文档覆盖断线、恢复、snapshot 失败、权限失败

测试用例：

- CLI golden tests
- `dalek run request` 成功输出 query 提示
- `dalek run show` 能展示 node/snapshot 关键信息
- `dalek run logs` 能查询到 tail
- `dalek run artifact ls` 能展示 artifact 索引
- TUI model/update tests
- 按部署文档完成 smoke test

---

### Ticket T11：Dev Role / 开发任务远程化

目标：

- 把 8.2 中定义的“开发发生在 B 上”从架构描述补成可执行 backlog
- 让 A 能把开发型任务稳定派发到 B，而不是只支持 `run_verify`

改动文件：

- `internal/contracts/task_status.go`
- `internal/contracts/node.go`
- `internal/app/project_remote_facade.go`
- `internal/app/daemon_runtime.go`
- `internal/services/nodeagent/*`
- `internal/services/task/*`
- `internal/services/pm/*`
- `cmd/dalek/*`

验收标准：

- A 可显式区分 `dev role` 与 `run role`
- A 可将开发任务路由到 B，而不要求 B/C 直连
- B 上 provider / agent 执行形成可回查的 task 视图
- 开发型任务的 thread / turn / approval / runtime sample 可投影回 A
- 本地开发路径仍然保留，A/B/C 同机时行为不退化

测试用例：

- A 向 B 提交开发任务成功进入 running
- B 上 provider 事件可回写为 task status / events
- 开发任务取消后 B 侧执行中断且 A 侧状态一致
- 本地项目与多节点项目共享统一 facade，不出现远程特判 panic

### Ticket T12：开发反馈与运行事实闭环衔接

目标：

- 把 8.3 中“B 上高频开发反馈”与“C 上关键阶段运行事实”通过 A 稳定衔接起来
- 让 C 失败后的关键结论可以回灌为 B 可继续消费的开发上下文，而不是只能人工抄日志

改动文件：

- `internal/services/run/*`
- `internal/services/task/*`
- `internal/services/pm/*`
- `internal/app/project_run.go`
- `internal/app/project_task.go`
- `internal/app/project_remote_facade.go`
- `internal/ui/tui/*`
- `cmd/dalek/*`

验收标准：

- C 回报的关键错误、日志摘要、artifact issue 会被 A 聚合到 task 语义层
- A 可把一次失败的关键运行结论标记为“开发继续所需上下文”
- B 查询任务时可看到来自 C 的最新运行事实，而不需要直接登录 C
- 失败重试时能明确区分“继续在 B 开发”与“重新在 C 验证”
- CLI/TUI 中可区分开发反馈与运行事实，但两者共享同一条用户工作流

测试用例：

- C 上 verify 失败后，A 上 task status 包含可用于开发继续的错误摘要
- B 查询同一 task 时可见最近一次 C 运行失败的关键上下文
- 重新触发开发任务不会丢失上一次 C 运行事实链
- artifact / log 缺失时仍能返回结构化诊断而不是空白状态

### Ticket T13：A 侧自动编排策略与角色切换

目标：

- 明确“什么时候留在 B 开发、什么时候切到 C 运行验证”的控制面策略
- 让 A 成为真正的编排中心，而不是要求用户手动记住 B/C 切换命令

改动文件：

- `internal/contracts/project.go` 或等价 project policy 模型
- `internal/app/project_remote_facade.go`
- `internal/app/daemon_runtime.go`
- `internal/services/pm/*`
- `internal/services/run/*`
- `cmd/dalek/*`
- `docs/*`

验收标准：

- project 可声明默认 dev assignment / run assignment / verify policy
- A 可根据 task 类型和阶段自动选择 B 或 C
- 手动显式触发仍然保留，但不是唯一切换方式
- 角色切换产生结构化事件链，用户可审计“为何切到 C / 为何回到 B”
- 飞书 / CLI / TUI 入口面对用户呈现为统一工作流，而不是三套命令心智

测试用例：

- 开发型任务默认进入 B，关键验证型任务默认进入 C
- C 失败后策略可要求回到 B 继续开发
- 明确指定节点/角色时可覆盖默认策略
- 策略变更后旧 task 不被错误重路由

### Ticket T14：用户入口与多角色工作流统一

目标：

- 把飞书 / gateway / CLI / TUI 入口与多角色编排统一起来
- 让用户从 A 进入时面对的是一条完整工作流，而不是先开发再手动切另一套运行命令

改动文件：

- `internal/services/channel/*`
- `internal/services/gatewaysend/*`
- `internal/app/project_manager.go`
- `internal/app/project_ticket.go`
- `internal/app/project_run.go`
- `internal/ui/tui/*`
- `cmd/dalek/*`
- `docs/*`

验收标准：

- 从 A 的用户入口触发任务时，可进入统一的多角色编排链
- 用户可在 ticket/task 维度看到当前位于 B 还是 C，以及下一步动作
- C 的运行结果会自然回流到同一 ticket/task 上下文
- 不要求用户理解底层 node 命令才能完成一次完整开发 -> 验证闭环

测试用例：

- gateway / CLI 发起的任务在 UI 中能看到统一上下文
- 同一 ticket 下开发反馈、运行事实、人工动作能串成连续事件链
- 入口层错误不会把任务卡在“已提交但无节点可见状态”

## 22A. 最终系统验收标准

本节用于回答一个比 ticket 更高层的问题：

- 当本计划所有 ticket 都完成时，系统是否真正达到了 V2 设计目标？

判断标准不能只看“单个能力是否存在”，还必须看“整条多角色工作流是否闭环”。

### 22A.1 用户入口统一

必须满足：

- 用户从 A 的统一入口进入系统：
  - 飞书 / gateway
  - CLI
  - TUI
- 用户面对的核心对象始终是 `ticket / task / run`
- 用户不需要为了完成一次完整链路而理解底层 node 协议细节
- 单机、双机、多机三种拓扑下，产品对象和基本操作方式保持一致

验收问题：

- 一个新用户是否可以只理解 A 的入口，就完成一次“创建任务 -> 开发推进 -> 关键验证 -> 查看结果”的主链路？
- 是否不存在“必须登录某个节点手工查看局部状态，A 上却不可见”的关键环节？

### 22A.2 A 统一编排

必须满足：

- A 是任务、运行、节点、状态聚合的编排中心
- B 与 C 的动作都通过 A 路由、登记或聚合
- A 可解释当前任务为何位于 B、为何切到 C、为何从 C 回到 B
- 所有关键切换都形成结构化事件链，而不是仅靠隐式约定

验收问题：

- 任一时刻，A 是否能回答“当前 task 正在哪个角色执行、最近一次切换原因是什么、下一步应由哪个角色继续”？
- 是否不存在绕过 A 的关键控制链？

### 22A.3 B 开发闭环可用

必须满足：

- 开发型任务默认发生在 B
- B 上 provider / agent 的执行事实可稳定投影回 A
- B 可进行快速运行、中间测试、局部验证，而不依赖 C
- 本地项目路径仍然可用；A/B/C 同机时不退化

验收问题：

- 在不使用 C 的情况下，开发主链是否仍然完整可用？
- B 上的开发反馈是否能在 A 的 task 视图中直接看到？

### 22A.4 C 运行事实闭环可用

必须满足：

- 关键验证、准生产运行、运行诊断由 C 承担
- C 的状态、日志摘要、错误、artifact issue 会自动回传给 A
- A 上能够区分“开发反馈”和“运行事实”，但两者属于同一工作流
- B 不需要直接 attach C 才能获得继续开发所需的关键信息

验收问题：

- C 失败后，A 是否能提供足够让 B 继续开发的结构化上下文？
- 是否不存在“必须人工抄日志回 B 才能继续”的常规路径？

### 22A.5 B <-> C 通过 A 稳定衔接

必须满足：

- A 能把 B 上的开发结果稳定衔接到 C 的关键验证
- A 能把 C 上的关键运行事实稳定回灌到 B 的开发上下文
- 用户看到的是一条连续工作流，而不是“开发系统”和“运行系统”两套割裂产品
- 明确重试、继续开发、重新验证三类动作的语义差异

验收问题：

- 一次从 B 开发到 C 验证再回 B 修复的循环，是否可以在同一条 ticket/task 语义链上完成？
- 同一条链路中的关键事实是否都能在 A 上回查？

### 22A.6 自动编排优先，人工显式控制保底

必须满足：

- 默认路径下，A 可根据 project policy / task phase / run phase 自动决定留在 B 还是切到 C
- 人工显式触发仍然保留，用于诊断、强制重跑、策略覆盖
- 系统不应要求用户在每一次 B/C 切换时手工下发底层节点命令

验收问题：

- 如果没有显式人工干预，系统是否仍能完成主链路中的常规角色切换？
- 人工命令是否主要用于覆盖和诊断，而不是日常必经步骤？

### 22A.7 恢复、审计、可诊断

必须满足：

- A 重启后可恢复 task/run/node 核心状态
- B/C 节点短暂离线后，可在 A 上看到恢复中的结构化状态
- snapshot、日志、artifact、错误摘要具备统一可查询入口
- 关键失败不会表现为“卡住但无解释”

验收问题：

- 任意一次失败后，CLI/TUI/API 是否都能查到原因？
- 是否可以在不登录节点的前提下完成大部分一线排障？

### 22A.8 最终通过标准

只有当以下三类验收同时成立时，才可以认定 V2 设计目标达成：

1. 组件验收成立：
   - 各 ticket 自身的完成标准与测试全部通过。
2. 工作流验收成立：
   - A/B 开发主链、A/C 运行主链、B -> A -> C -> A -> B 回流链全部可用。
3. 产品验收成立：
   - 用户从 A 的统一入口发起任务时，面对的是单一系统心智，而不是多套割裂命令与人工搬运流程。

## 22B. Ticket 与系统验收对齐

本节用于回答另一个实操问题：

- `T1-T14` 是否已经覆盖 `22A` 中定义的最终系统验收标准？

结论：

- 当前 `T1-T14` 已基本覆盖 `22A.1 - 22A.8`
- 其中 `T1-T10` 主要覆盖多节点运行基础设施
- `T11-T14` 负责把“开发闭环”和“统一用户工作流”补齐

对齐关系如下：

- `22A.1 用户入口统一`
  - 主要由 `T10`、`T14` 覆盖
- `22A.2 A 统一编排`
  - 主要由 `T4`、`T5`、`T9`、`T13` 覆盖
- `22A.3 B 开发闭环可用`
  - 主要由 `T11` 覆盖
- `22A.4 C 运行事实闭环可用`
  - 主要由 `T6`、`T7`、`T8`、`T9`、`T12` 覆盖
- `22A.5 B <-> C 通过 A 稳定衔接`
  - 主要由 `T8`、`T12`、`T13`、`T14` 覆盖
- `22A.6 自动编排优先，人工显式控制保底`
  - 主要由 `T13` 覆盖
- `22A.7 恢复、审计、可诊断`
  - 主要由 `T3`、`T5`、`T8`、`T9`、`T10`、`T12` 覆盖
- `22A.8 最终通过标准`
  - 由全部 `T1-T14` 联合满足，不能只以单个 ticket 完成替代

仍需注意的边界：

- `T1-T10` 完成，不等于 `22A` 达成
- 只有 `T11-T14` 也完成，并通过 `22A.8` 中的组件 / 工作流 / 产品三类联合验收，才能认定 V2 目标达成
- `24. Implementation Slices` 当前仍只细拆到 `T1-T10`；若后续进入实施阶段，还需继续为 `T11-T14` 补切片

## 23. Backlog 使用建议

建议将上述 ticket 继续拆成 implementation slices，每个 slice 控制在以下规模：

- 1 到 3 个 package
- 1 条明确主链路
- 1 组必须通过的测试

例如：

- `T1-1` 先补 `TaskTypeRunVerify + run contracts`
- `T1-2` 再补 node contracts
- `T1-3` 最后补 snapshot / workspace assignment contracts

这样做的好处是：

- 更适合并行开发
- 更适合逐步验证而不是大包提交
- 一旦方向有偏差，可以在小切片上回收，而不是在大 phase 上返工

## 24. Implementation Slices

本节把当前计划中的 ticket 继续切成可提交、可回归、可并行的小切片。当前仅细化 `T1-T10`，`T11-T14` 仍需后续补充。默认约定：

- 每个 slice 只推动一条主链路
- 每个 slice 都要求有对应测试
- 每个 slice 完成后，分支应保持可合并状态

### 24.1 T1 分片

#### T1-1：引入 `TaskTypeRunVerify` 与最小 run contracts

目标：

- 先让 task 系统认识 `run_verify`
- 先定义最小 run 状态与基础字段

前置依赖：

- 无

改动文件：

- `internal/contracts/task_status.go`
- `internal/contracts/task.go`
- `internal/contracts/run.go`
- `internal/contracts/*_test.go`

完成标准：

- `TaskTypeRunVerify` 可用
- 最小 run 状态枚举可编译、可测试
- 不影响现有 task 类型逻辑

测试：

- contracts 单测
- task type parse / persist / derive 测试

#### T1-2：补 node contracts

目标：

- 定义 node、role capability、provider capability、protocol version 模型

前置依赖：

- `T1-1`

改动文件：

- `internal/contracts/node.go`
- `internal/contracts/*_test.go`

完成标准：

- node 模型可表达 control/dev/run 角色
- provider 能力与角色能力分离

测试：

- capability normalize
- 非法 capability 拒绝

#### T1-3：补 snapshot / workspace assignment contracts

目标：

- 定义 snapshot 与 workspace assignment 契约

前置依赖：

- `T1-1`

改动文件：

- `internal/contracts/snapshot.go`
- `internal/contracts/workspace_assignment.go`
- `internal/contracts/*_test.go`

完成标准：

- snapshot 能表达 digest/base_commit/generation/payload_ref
- workspace assignment 能表达 project -> workspace -> node

测试：

- snapshot 元数据校验
- generation 绑定字段完整性校验

### 24.2 T2 分片

#### T2-1：为 run view 引入 migration

目标：

- 先把 `run_views` 或等价表落库

前置依赖：

- `T1-1`

改动文件：

- `internal/store/*`
- `internal/app/upgrade.go`
- migration tests

完成标准：

- 新库包含 run view 表
- 老库升级不报错

测试：

- 空库 migration
- 旧库升级 migration

#### T2-2：为 node / workspace assignment 引入 migration

目标：

- 落地 nodes 和 workspace assignments

前置依赖：

- `T1-2`
- `T1-3`

改动文件：

- `internal/store/*`
- `internal/repo/*`
- migration tests

完成标准：

- nodes / workspace_assignments 表落库
- 唯一索引和必要字段齐全

测试：

- 唯一约束测试
- 基础 CRUD 测试

#### T2-3：为 snapshots 引入 migration

目标：

- 落地 snapshot 元数据表

前置依赖：

- `T1-3`

改动文件：

- `internal/store/*`
- `internal/repo/*`
- migration tests

完成标准：

- snapshots 表落库
- snapshot id 唯一
- run 与 snapshot 可关联

测试：

- snapshot insert/query
- 重复 snapshot id 拒绝

### 24.3 T3 分片

#### T3-1：创建 `RunService` 与 run request 创建链路

目标：

- 能创建 `task(kind=run_verify)`

前置依赖：

- `T1-1`
- `T2-1`

改动文件：

- `internal/services/run/service.go`
- `internal/services/task/*`
- `internal/app/project_task.go`

完成标准：

- run request 可创建 task run
- request_id 幂等

测试：

- run request 创建测试
- 幂等测试

#### T3-2：run 状态机与 run view 投影

目标：

- 建立 run 生命周期与 task 的投影关系

前置依赖：

- `T3-1`

改动文件：

- `internal/services/run/view.go`
- `internal/services/run/service.go`
- `internal/app/daemon_runtime.go`

完成标准：

- task 状态能投影到 run 状态
- run 终态受限

测试：

- 状态推进测试
- run/task 一致性测试

#### T3-3：run 查询 / 事件 / 取消 facade

目标：

- 暴露稳定的 run 查询接口

前置依赖：

- `T3-2`

改动文件：

- `internal/app/api_types.go`
- `internal/app/project_task.go`
- `internal/app/daemon_runtime.go`
- `internal/services/run/*`

完成标准：

- run show / events / cancel 接口可用

测试：

- show/events/cancel 服务测试

### 24.4 T4 分片

#### T4-1：抽出 local facade

目标：

- 把现有本地路径显式归拢到 local facade

前置依赖：

- 无

改动文件：

- `internal/app/project_local_facade.go`
- `internal/app/project.go`

完成标准：

- 本地行为不变

测试：

- 现有本地行为回归

#### T4-2：定义 remote facade 接口

目标：

- 先抽象 remote facade 接口，不急着全部实现

前置依赖：

- `T4-1`
- `T3-3`

改动文件：

- `internal/app/project_remote_facade.go`
- `internal/app/facade_types.go`

完成标准：

- remote facade 至少包含 run/node/snapshot 接口

测试：

- interface contract tests

#### T4-3：接入 facade 选择逻辑

目标：

- 根据项目拓扑选择 local / remote facade

前置依赖：

- `T4-2`

改动文件：

- `internal/app/project.go`
- `internal/app/home.go`
- `internal/app/daemon.go`

完成标准：

- local / remote 路径可稳定切换

测试：

- facade routing tests

### 24.5 T5 分片

#### T5-1：NodeRegistry 基础 CRUD

目标：

- 先有节点注册表

前置依赖：

- `T1-2`
- `T2-2`

改动文件：

- `internal/services/node/registry.go`
- `internal/services/node/registry_test.go`

完成标准：

- 节点注册、查询、更新可用

测试：

- CRUD tests

#### T5-2：SessionManager 与 heartbeat / lease

目标：

- 增加节点 session、heartbeat、lease

前置依赖：

- `T5-1`

改动文件：

- `internal/services/node/session_manager.go`
- `internal/services/node/session_manager_test.go`

完成标准：

- heartbeat 更新 last_seen
- lease 超时后节点降级

测试：

- heartbeat / timeout tests

#### T5-3：session epoch 与最小调度查询

目标：

- 引入 epoch 与可调度节点筛选

前置依赖：

- `T5-2`

改动文件：

- `internal/services/node/session_manager.go`
- `internal/services/node/registry.go`

完成标准：

- 旧 epoch 被拒绝
- 可按 role/provider 查节点

测试：

- old session reject
- scheduler lookup tests

### 24.6 T6 分片

#### T6-1：Node agent 协议 DTO

目标：

- 固化 A <-> node agent 协议载荷

前置依赖：

- `T5-1`

改动文件：

- `internal/services/nodeagent/protocol.go`
- `internal/services/nodeagent/*_test.go`

完成标准：

- register / heartbeat / run submit / run query / cancel DTO 明确

测试：

- 协议编解码测试

#### T6-2：A 侧 node agent server 路由

目标：

- 在 A 侧提供节点协议入口

前置依赖：

- `T6-1`
- `T5-2`

改动文件：

- `internal/services/nodeagent/server.go`
- `internal/services/daemon/api_internal.go`

完成标准：

- 支持 register / heartbeat / run query

测试：

- handler tests
- auth tests

#### T6-3：A 侧 NodeAgentClient 与远程 run submit

目标：

- A 能向 C 下发 run

前置依赖：

- `T6-2`
- `T3-3`

改动文件：

- `internal/services/nodeagent/client.go`
- `internal/services/run/service.go`
- `internal/app/project_remote_facade.go`

完成标准：

- A 能提交远程 run request

测试：

- 远程 submit/query 集成测试

#### T6-4：最小 cancel / logs tail / artifact 摘要

目标：

- 补齐远程最小观测与控制面

前置依赖：

- `T6-3`

改动文件：

- `internal/services/nodeagent/client.go`
- `internal/services/nodeagent/server.go`
- `internal/services/run/*`

完成标准：

- 远程 cancel、logs tail、artifact summary 可用

测试：

- cancel/log tail/artifact tests

### 24.7 T7 分片

#### T7-1：verify target 模板与配置模型

目标：

- 先把可执行目标模板化

前置依赖：

- `T4-2`

改动文件：

- `internal/services/runexecutor/template.go`
- `internal/services/runexecutor/types.go`
- `internal/repo/config.go`

完成标准：

- verify target 不走任意 shell

测试：

- 模板合法性测试

#### T7-2：run executor 基础执行链

目标：

- 先打通 `preflight -> run`

前置依赖：

- `T7-1`

改动文件：

- `internal/services/runexecutor/service.go`
- `internal/services/runexecutor/service_test.go`

完成标准：

- 可执行标准 verify 并返回结构化结果

测试：

- success/fail/timeout/cancel tests

#### T7-3：补 bootstrap / repair 三阶段

目标：

- 把环境处理完整拆成三阶段

前置依赖：

- `T7-2`

改动文件：

- `internal/services/runexecutor/service.go`
- `internal/services/runexecutor/*_test.go`

完成标准：

- bootstrap / repair / preflight 语义完整

测试：

- env drift / repair tests

### 24.8 T8 分片

#### T8-1：snapshot manifest 与 digest

目标：

- 先定义 snapshot manifest、hash、打包边界

前置依赖：

- `T1-3`

改动文件：

- `internal/services/snapshot/manifest.go`
- `internal/services/snapshot/pack.go`
- `internal/services/snapshot/*_test.go`

完成标准：

- 可从冻结输入生成 manifest + digest

测试：

- manifest/digest tests

#### T8-2：A 侧 SnapshotCatalog 落盘与查询

目标：

- 先建立 catalog 元数据和 payload 落盘

前置依赖：

- `T2-3`
- `T8-1`

改动文件：

- `internal/services/snapshot/catalog.go`
- `internal/services/snapshot/catalog_test.go`

完成标准：

- 可存储、查询、引用 snapshot

测试：

- catalog CRUD / refcount tests

#### T8-3：B 上传 -> A 接收分块链路

目标：

- 支持 snapshot chunk upload

前置依赖：

- `T8-2`
- `T6-2`

改动文件：

- `internal/services/nodeagent/server.go`
- `internal/services/nodeagent/client.go`
- `internal/services/snapshot/*`

完成标准：

- 可分块上传 payload 并校验 digest

测试：

- chunk upload tests

#### T8-4：A 下载 -> C apply 链路

目标：

- 支持 snapshot 下载和应用

前置依赖：

- `T8-3`
- `T7-2`

改动文件：

- `internal/services/snapshot/apply.go`
- `internal/services/nodeagent/*`
- `internal/services/run/*`

完成标准：

- C 能下载并 apply snapshot

测试：

- apply success/conflict tests

#### T8-5：Snapshot GC

目标：

- 增加 TTL / refcount / GC

前置依赖：

- `T8-2`

改动文件：

- `internal/services/snapshot/gc.go`
- `internal/services/snapshot/gc_test.go`

完成标准：

- 不误删被引用 snapshot

测试：

- GC safety tests

### 24.9 T9 分片

#### T9-1：RunReconciler 基础回补

目标：

- A 重启后可补拉 run 状态

前置依赖：

- `T6-4`

改动文件：

- `internal/services/run/reconciler.go`
- `internal/services/run/reconciler_test.go`

完成标准：

- 可按 run_id / request_id 补拉状态

测试：

- restart reconcile tests

#### T9-2：fencing token 与旧消息抑制

目标：

- 防止旧节点、旧消息覆盖新状态

前置依赖：

- `T5-3`
- `T9-1`

改动文件：

- `internal/services/run/reconciler.go`
- `internal/services/node/session_manager.go`

完成标准：

- 旧 epoch / 旧 token 消息失效

测试：

- stale message reject tests

#### T9-3：RunScheduler 串行与并发上限

目标：

- 增加运行调度约束

前置依赖：

- `T5-3`
- `T7-2`

改动文件：

- `internal/services/run/scheduler.go`
- `internal/services/run/scheduler_test.go`

完成标准：

- 单 workspace 串行
- 单节点并发限制

测试：

- serial / queue / capacity tests

#### T9-4：artifact 部分失败与终态分离

目标：

- 保证产物失败不覆盖执行终态

前置依赖：

- `T6-4`
- `T7-2`

改动文件：

- `internal/services/run/service.go`
- `internal/services/run/*_test.go`

完成标准：

- 执行结果与 artifact 上传结果解耦

测试：

- artifact partial failure tests

### 24.10 T10 分片

#### T10-1：`dalek node` CLI

目标：

- 提供 node 管理入口

前置依赖：

- `T5-1`
- `T6-2`

改动文件：

- `cmd/dalek/cmd_node*.go`
- CLI tests

完成标准：

- `node add|ls|show|rm` 可用

测试：

- CLI golden tests

#### T10-2：`dalek run` CLI

目标：

- 提供 run 请求和观测入口

前置依赖：

- `T3-3`
- `T6-4`

改动文件：

- `cmd/dalek/cmd_run*.go`
- CLI tests

完成标准：

- `run request|show|logs|artifact ls|cancel` 可用

测试：

- CLI golden tests

#### T10-3：TUI run 视图

目标：

- 在 TUI 中补 run 观测面

前置依赖：

- `T10-2`

改动文件：

- `internal/ui/tui/*`
- TUI tests

完成标准：

- ticket/task/run 术语一致
- run 关键字段可见

测试：

- TUI model/update tests

#### T10-4：部署与排障文档

目标：

- 完成可运维文档

前置依赖：

- `T10-1`
- `T10-2`
- `T9-1`

改动文件：

- `docs/*`

完成标准：

- 单机、双机、三机部署手册齐全
- 故障排查手册齐全

测试：

- 按文档执行 smoke test

## 25. 依赖图与建议执行顺序

建议主线顺序：

1. `T1-1 -> T2-1 -> T3-1 -> T3-2 -> T3-3`
2. `T1-2 -> T2-2 -> T5-1 -> T5-2 -> T5-3`
3. `T4-1 -> T4-2 -> T4-3`
4. `T6-1 -> T6-2 -> T6-3 -> T6-4`
5. `T7-1 -> T7-2 -> T7-3`
6. `T1-3 -> T2-3 -> T8-1 -> T8-2 -> T8-3 -> T8-4 -> T8-5`
7. `T9-1 -> T9-2 -> T9-3 -> T9-4`
8. `T10-1 -> T10-2 -> T10-3 -> T10-4`

建议并行组：

- 组 A：`T1-1 / T1-2 / T1-3`
- 组 B：`T2-1 / T2-2 / T2-3`
- 组 C：`T4-1 / T4-2`
- 组 D：`T5-1` 与 `T3-1` 可并行
- 组 E：`T7-1` 可在 `T6-3` 前并行准备

不建议并行的部分：

- `T8-3` 之前不要启动 `T8-4`
- `T9-2` 之前不要做大规模恢复语义判定
- `T10-2` 之前不要大做 TUI run 视图

## 26. 首批建议开工切片

如果要选一组最适合立刻开工、风险最低、回报最高的切片，建议是：

1. `T1-1`
2. `T2-1`
3. `T3-1`
4. `T3-2`
5. `T4-1`
6. `T1-2`
7. `T2-2`
8. `T5-1`

这组切片的收益是：

- 能先把 `run_verify` 从概念变成正式对象
- 能尽早暴露 schema / facade / service 设计问题
- 对现有远程链路改动最小
- 最适合作为后续 `A + C` 远程运行的稳定起点
