# Dalek 多节点 V1 方案

## 目标

设计一个 V1 架构，使一套中心 dalek 实例能够管理“代码编辑”和“运行执行”发生在不同角色、不同部署位置上的项目。

目标拓扑不是固定三台机器，而是三类角色：

- A：`control role`，中心 dalek 控制面
- B：`dev role`，负责编程、修改本地仓库
- C：`run role`，负责测试、启动服务、调试运行、保存运行日志

部署约束需要明确：

- A、B、C 可以全部部署在同一台机器
- A、B、C 也可以分散在不同机器
- B 和 C 是角色，不是单实例限制；后续可以扩展为多个 dev node / run node
- 项目当前“直接操作本地项目”的能力必须继续保留，不能因为引入多节点模型而退化

本文档仅为方案设计，不包含代码开发。

## 非目标

V1 有意不解决所有问题。

V1 暂不包含：

- 跨机器完整远程终端 attach 或 tmux attach
- B 和 C 共享文件系统
- 多主 dalek 控制面
- 让 A 直接通过 SSH 按远程路径操作 repo
- 强行让 B 和 C 在操作系统层面完全一致
- 复杂的交互式调试协议

## 为什么现有 Dalek 不够

当前 dalek 的实现建立在“本地执行”假设之上：

- 项目注册只保存本地 `repo_root`
- 打开项目时直接读取仓库内的 `.dalek/config.json` 和 `.dalek/runtime/dalek.sqlite3`
- worker 启动时直接在本机创建 git worktree 和本机运行进程
- daemon internal API 目前只允许 loopback，本质上是同机通信
- TUI attach 默认依赖本地 worktree 路径和本地 tmux session

这个模型适合单机，但无法正确表达以下场景：

- A 管理远程项目
- B 负责写代码，C 负责执行测试
- 同一个 ticket 跨多个执行角色流转
- A/B/C 角色同机部署但逻辑职责不同
- 一个项目绑定多个 B 或多个 C 形成节点池

## V1 架构

### 角色定义

- `control node`：A，负责编排、注册、路由、审计，以及用户侧 CLI/TUI/API
- `dev node`：B，负责可编辑工作区、节点内开发 agent runtime 和编程 agent 执行
- `run node`：C，负责运行工作区、测试/服务运行、日志保留，以及可选的本地诊断 agent runtime

这里的 A/B/C 是角色位，不等于三台固定物理机：

- 一个物理节点可以同时承担多个角色
- 一个角色也可以由多个节点实例承担
- V1 文中的 `A/B/C` 主要用于说明职责分工，不表示部署数量被锁死

### 节点角色与 Provider 解耦

V1 多节点方案不再以“控制面直接起一个临时 codex 进程”作为默认模型，而是采用“每个执行节点暴露节点能力，由 dalek 控制面通过节点代理间接驱动”的模式。

这里必须明确一个新的建模约束：

- 节点角色和 provider 选择是两件事
- A、B、C 都应允许按节点独立配置模型框架
- provider 的可选项至少包括 `Claude`、`codex`、`kimi`、`DeepSeek`
- 某个节点是否启用 provider、启用哪一个、是否启用多个，都由用户配置决定
- 因此 V1 不能把“B 用什么、C 用什么”写死在架构里，而必须抽象成“节点本地 provider 配置 + 能力声明”

建议拓扑：

- A 运行 dalek control plane
- A 可选运行本地 provider，供控制面 PM/gateway/辅助诊断使用
- B 运行 `dalek node agent` + 本地开发 agent runtime
- C 运行 `dalek node agent` + 本地 `run executor`
- C 如需调试/诊断，再额外接入本地 agent runtime

部署上至少应支持三种形态：

- 单机形态：A/B/C 共用一台机器，兼容当前本地项目工作流
- 双机形态：A+B 同机，C 独立；或 A+C 同机，B 独立
- 多机形态：A、B、C 分散部署，且 B/C 可扩展为多个实例

这样 A 面对的是“节点能力 + provider 能力 + 会话协议”，而不是“远程 shell + 一次性 CLI 进程”。

### 核心原则

不要再把一个项目建模为“只存在于一台机器上”。

更合适的建模方式是：

- A 上有一条控制面项目记录
- B 上有开发工作区
- C 上有运行工作区
- A/B/C 都可以声明自己的 provider 能力
- C 上至少有受控 `run executor`
- 任何节点若启用了 agent runtime，则该节点内的 thread / turn / approval 都由本地 provider 管理

### 状态归属

- A 是“编排状态”和“聚合状态”的权威
- B 是“当前代码快照选择”的权威
- C 是“运行结果、日志、产物”的权威
- 任一节点上的 provider runtime 是该节点会话事实的权威
- C 上的 `run executor` 是运行结果事实的直接来源

这样可以避免多处同时写状态造成冲突。

但这个划分需要再补一层“谁负责最终仲裁”的规则，否则实现时仍然会出现状态打架：

- A 对 `ticket`、`task`、`run` 的聚合视图和状态机推进负责
- B 只对“某次 verify 想验证哪份代码”负责，不直接改 `run` 最终状态
- C 只对“某次 run 实际执行出了什么结果”负责，不直接改 `ticket` 最终状态
- 一个 `run` 一旦绑定 `snapshot_id`，其代码上下文不可变；B 后续新的改动必须创建新的 snapshot / 新的 run
- A 必须保存 `run_id -> snapshot_id -> base_commit -> source_workspace_generation` 的绑定关系，避免并发验证时串台

建议把“权威”理解为：

- A 是状态机权威
- B 是源码上下文权威
- C 是执行结果事实权威
- 节点内 provider runtime 是 thread / turn / approval / event stream 的权威
- 三者都不能单独重写对方的领域结论

## V1 典型流程

以项目 `test` 为例：

1. A 注册项目 `test`，并指定 `dev node = B`、`run node = C`
2. A 通过 B 的 `node agent` 创建或恢复一个开发 thread
3. B 上的本地 provider 在本地 worktree 中执行编程 agent
4. B 需要验证时，向 A 提交 run request
5. A 要求 B 生成当前 thread 对应的代码 snapshot
6. A 将 snapshot 和 run 指令路由到 C
7. C 上的 `run executor` 在运行工作区准备环境、应用 snapshot 并执行标准 verify
8. 若标准 verify 失败且需要诊断，C 可再调用该节点已配置的本地 provider 执行 diagnosis/debug
9. C 向 A 回报 run 状态、摘要、日志尾部和必要事件
10. B 需要调试时，可以通过 A 查询对应运行日志或诊断上下文
11. A 维护 ticket / task / run / thread 的统一视图

## V1 范围

### 包含

- 节点注册
- 项目注册时声明 `dev node` 和 `run node`
- 把开发任务派发到 B
- 节点本地 provider / executor 生命周期管理
- A 通过节点代理驱动 thread / turn / approval / event stream，前提是目标节点确实声明支持该类 provider/runtime
- 在 C 上按需执行测试或调试运行
- B 到 C 的代码快照传递
- C 到 A 的运行状态与结果摘要回报
- B 按需查询 C 的日志
- C 在运行前执行环境准备和 preflight 校验
- 保留现有本地项目路径，使单机项目仍可继续走 local facade
- 支持 A/B/C 同机部署，以及 B/C 多实例扩展的建模

### 不包含

- 默认实时日志推送到 B
- 远程 tmux attach
- 在 C 上直接编辑代码
- B/C 双向工作区同步
- 从 B 自动复制整套环境到 C
- A 直接连远端底层 provider 暴露公网入口；V1 仍由 node agent 代理

## 建议的数据模型

### Node

在 A 上新增一等公民的 `node` 注册表。

建议字段：

- `name`
- `role_capabilities`，如 `dev`、`run` 或两者兼有
- `endpoint`
- `auth_mode`
- `last_seen_at`
- `status`
- `version`
- `provider_modes`，如 `claude`、`codex`、`kimi`、`deepseek`、`run_executor`
- `provider_status`
- `default_provider`
- `provider_configs`
- `provider_capabilities`
- `protocol_version`
- `session_affinity`

这里需要强调：

- A/B/C 的 provider 能力都不能靠推断，必须由节点注册时显式上报
- `role_capabilities` 和 `provider_modes` 必须正交建模
- 一个 `run node` 既可以只有 `run_executor`，也可以同时声明 `kimi` / `DeepSeek` / `Claude` / `codex`
- 一个 `control node` 也可以声明本地 provider，供控制面任务使用

这里的 `endpoint` 建议指向 `dalek node agent`，而不是直接暴露底层 runtime。

### Project

项目注册从“仅本地元数据”升级为“多节点拓扑元数据”。

但要注意，这不是要废弃本地项目模型，而是要把“本地项目”视为多角色模型的一种特例：

- 当 A/B/C 共用本机时，项目仍可按本地路径工作
- 当只存在单个本地 workspace 时，仍保留现有 local facade / local runtime
- 多节点模型是扩展，不是替换

建议字段：

- `name`
- `key`
- `dev_node`
- `run_node`
- `dev_repo_root`
- `run_repo_root`
- `default_branch`
- `env_profile`
- `node_provider_policy`
- `default_verify_provider`
- `default_dev_provider`

建议收敛职责：

- `project` 只保存拓扑、策略、默认配置
- provider 的具体凭证、endpoint、启动方式保留在 node 侧配置，不塞进 project 主记录
- 与节点实例强绑定的路径、校验状态、workspace 健康度不要长期堆在 `project` 上
- 如果已经引入 `workspace`，则 `dev_repo_root` / `run_repo_root` 更适合作为 workspace 属性，而不是 project 主字段
- 对于纯本地项目，仍允许 project 退化为单节点 / 单 workspace 绑定

为避免 V1 落地后很快翻模，这里再加一条收敛原则：

- `project.dev_node` / `project.run_node` 更适合作为当前调度策略的默认选择，而不是长期唯一数据模型
- 底层关系应更接近 `project -> workspace assignment -> node`
- V1 可以只调度“一个 dev、一个 run”，但 schema 不应把未来 run pool / standby node 的扩展空间堵死

### Workspace

当前方案里，B 和 C 上都各自持有项目副本，因此仅有 `dev_repo_root` / `run_repo_root` 还不够，建议显式引入 `workspace` 概念。

`workspace` 表示“某个项目在某个节点上的一个可执行工作区绑定”。

建议字段：

- `project`
- `node`
- `role`，如 `dev`、`run`
- `repo_root`
- `default_branch`
- `bootstrap_status`
- `env_status`
- `last_verified_at`
- `last_error`

建议理解方式：

- B 上的开发工作区是一个 `dev workspace`
- C 上的运行工作区是一个 `run workspace`

这样后续很多状态就有明确归属，而不是散落在 project 字段中。

建议再补几个关键字段：

- `workspace_generation`，每次重建 +1
- `desired_revision`
- `current_revision`
- `dirty_policy`，如 `rebuild_required` / `allow_reuse`
- `bootstrap_fingerprint`
- `capacity_hint`，用于后续调度

其中 `workspace_generation` 很关键：A 在恢复 run、查询日志、补拉状态时，需要知道当时 run 绑定的是哪一代 workspace，避免 C 重建后把新工作区误当成旧 run 上下文。

### Snapshot

跨节点运行时，B 需要把“当前待验证代码”传给 C，因此需要显式定义 `snapshot`。

`snapshot` 表示“一次可被传输和应用的代码状态描述”。

建议字段：

- `snapshot_id`
- `project`
- `source_node`
- `source_workspace`
- `base_commit`
- `mode`，如 `commit`、`patch`
- `includes_untracked`
- `file_count`
- `payload_ref`
- `created_at`
- `created_by`

V1 至少要定义以下契约：

- snapshot 必须明确基于哪个 `base_commit`
- patch 模式必须支持新增、修改、删除
- 是否包含未跟踪文件必须明确
- 二进制文件如果不支持，必须在协议中直接报错，而不是静默跳过
- C 应用 snapshot 失败时，必须返回结构化错误

还建议在 V1 直接补齐这些最小协议字段，否则实现会很快失控：

- `snapshot_digest`
- `payload_size`
- `payload_encoding`，如 `tar.gz` / `patch`
- `supports_binary`
- `supports_symlink`
- `supports_rename`
- `expires_at`

以及最小行为约束：

- snapshot 必须可校验完整性，不能只依赖 `snapshot_id`
- patch 模式必须明确是否保留文件权限位
- 大 payload 必须允许拒收，而不是把 A/C 内存打爆
- A 必须明确是“持久化 snapshot”还是“仅做短暂中转”；两者不能实现时再临时决定

### Run

把 C 上的测试/调试执行显式建模为 `run`。

建议字段：

- `run_id`
- `ticket_id`
- `project`
- `verify_target`
- `phase`，如 `dev`、`test`、`debug`
- `node`
- `snapshot_id`
- `status`
- `summary`
- `exit_code`
- `artifact_index`
- `workspace`
- `recovery_state`
- `dev_thread_id`
- `run_thread_id`
- `approval_state`
- `event_cursor`

其中：

- `dev_thread_id` 表示 B 上开发 thread
- `run_thread_id` 表示 C 上运行/验证 thread
- `event_cursor` 用于 A 重启后从节点补拉 provider 事件

但 `event_cursor` 单字段对恢复来说偏弱，V1 最好直接承认存在多条事件流：

- `dev_thread_cursor`
- `run_thread_cursor`
- `run_event_seq`
- `artifact_version` 或等价索引版本

否则 A 重启后很容易出现“run 状态补到了，但 thread 事件没补全”或“日志索引落后于最终状态”的半恢复问题。

### Ticket / Task / Run 的关系

V1 需要明确三者关系，避免后续概念重叠：

- `ticket`：需求与执行闭环的管理对象
- `task`：dalek 现有编排与执行记录对象
- `run`：跨节点“运行验证”对象，主要承载 C 上的测试/调试执行

建议关系：

- 一个 `ticket` 可以关联多次 `run`
- 一次 `run` 必须绑定一次明确的 `snapshot`
- `run` 可以作为 task 体系下的一个专门执行类型，也可以作为 task 的扩展视图，但不应成为孤立体系

如果这里不提前约束，后续很容易出现 ticket、task、run 三套记录重复表达同一件事的问题。

建议在文档中直接定死 V1 方案：

- `ticket` 仍然是需求闭环与 PM 视图的核心对象
- `task` 仍然是执行记录与事件链的核心对象
- `run` 不再作为完全平行的新编排体系，而是 `task` 的一种跨节点执行类型或特化视图
- `worker` 对用户请求 `verify` 时，本质上是请求创建一个 `task(kind=run_verify)`，其结果再投影回 `ticket` / `worker` 视图

这样可以直接复用 dalek 现有的：

- task 事件链
- task 观测字段
- manager / daemon 恢复机制
- inbox / report 的集成路径

还建议在 V1 直接补齐一层“执行契约”，避免 `verify_target=test` 在不同节点上语义漂移：

- `verify_target` 不能只是一个字符串标签，而应绑定到 project-scoped 的受控模板
- 模板至少应定义：命令/入口、工作目录、环境模板、超时、允许的产物类型、日志采集规则
- A 只下发模板标识和参数，不直接下发任意 shell 命令
- C 只执行经过 project 作用域解析后的目标，不能把 run 请求退化成通用远程执行

这样审计、缓存、重试和权限控制才有稳定对象。

## 建议的通信模型

V1 优先采用 B、C 主动连 A 的模式；节点内执行统一经 `node agent`，但底层 provider 允许异构。

原因：

- 更容易穿透 NAT、云防火墙和跨境网络
- 认证集中在 A
- 避免要求 A 主动 SSH 进所有节点
- 可以把节点内 runtime/executor、workspace、日志、审批都封装在 node agent 后面

### 分层通信

建议把多节点链路拆成两层：

- `A <-> node agent`：dalek 自己的控制协议，负责节点注册、任务路由、snapshot 传输、日志查询、状态回补
- `node agent <-> local provider`：节点本地协议，负责 runtime/executor 的调用、thread、turn、approval、event stream

这样 A 不需要直接理解每个远端进程细节，也不需要直接持有远端 shell。

### 三类消息

#### 1. 控制命令

示例：

- A -> B：派发开发任务
- B -> A：请求在 C 上执行测试
- A -> C：执行运行、取消运行、获取产物
- A -> B：创建或恢复开发 agent thread
- A -> C：若 C 声明支持诊断 agent，则创建或恢复 diagnosis thread
- A -> B/C：转发 approval decision

传输方式建议：

- 请求/响应式 API
- 每个请求都携带 `request_id`

除了 `request_id`，还建议所有跨节点命令统一具备：

- `message_id`
- `project_key`
- `ticket_id`（若有）
- `task_id` / `run_id`（若已创建）
- `attempt`
- `sent_at`
- `deadline_at`
- `protocol_version`

#### 2. 状态事件

示例：

- `thread_started`
- `turn_started`
- `run_started`
- `env_prepare_failed`
- `tests_failed`
- `run_finished`
- `artifact_ready`
- `approval_requested`
- `thread_compacted`

传输方式建议：

- B/C 向 A 追加式上报事件

事件模型至少还要补两点：

- 事件顺序以单调递增 `event_id` 或 per-run `seq` 为准，不能只靠机器本地时间
- 每个事件区分 `emitted_at` 和 `received_at`，否则跨机时钟偏差会让 UI 排序错乱
- 节点代理必须保留 provider 原始事件与 dalek 归一化事件的映射，避免丢失 thread/turn 细节

#### 3. 日志查询

V1 默认不要求日志实时订阅。

模型建议：

- C 保存完整日志
- C 向 A 上报摘要和日志尾部
- B 只有在需要调试时才向 A 查询日志
- A 优先返回缓存的 tail，必要时再向 C 拉更多日志

对于开发节点，还要补一个并列能力：

- B 上的 node agent 需要允许 A 查询开发 thread 的最后事件、最后一条 agent message、当前 approval 等会话态

## B 与 C 的代码同步

这是跨节点方案的核心问题。

### V1 默认传递路径

V1 不建议做 B 和 C 直连传输，默认应走：

- B 生成 snapshot
- B 上传到 A 的 `SnapshotCatalog`
- A 完成校验、登记和权限审计
- C 从 A 拉取 snapshot payload
- C 校验 digest 后再应用到本地运行工作区

也就是默认链路是 `B -> A(snapshot store) -> C`，而不是裸 `B -> C`。

这样做的原因：

- A 能审计“到底验证了哪份代码”
- A 能做幂等、TTL、引用计数和恢复
- B/C 不需要彼此暴露额外入口
- 失败恢复时可以从 A 重放，而不是要求 B 必须在线

V1 建议支持两种同步模式：

### 模式 1：提交态同步

- B 指定一个待验证的 branch / commit
- C 在自己的运行 worktree 中切到对应 commit

优点：

- 稳定
- 审计清晰

缺点：

- 无法覆盖“未提交但想先测试”的 AI 开发过程

### 模式 2：快照同步

- B 把当前 worktree 的变更打成 patch / snapshot
- A 存储该快照并向 C 下发可拉取引用
- C 在自己的运行基线上应用这个快照

优点：

- 支持“先测后提”
- 更适合 AI 持续迭代开发

缺点：

- 需要处理快照打包、应用失败、校验失败等问题

### 建议

V1 最终应支持两种模式，但真正有价值的是“快照同步”。

但实现优先级建议再收敛一下：

- Phase 1 只支持 `commit` 模式，把跨节点执行链跑通
- Phase 2 再补 `patch/snapshot` 模式，解决未提交代码验证

原因很简单：真正难的是跨节点状态机、恢复、鉴权，不是 patch 打包本身。若一开始同时做双模式，范围会明显失控。

### V1 快照契约补充

为保证流程闭合，建议 V1 明确以下规则：

- B 发起 run request 时，必须同时提供 `snapshot_id` 或可生成 snapshot 的工作区上下文
- C 只能在与 `base_commit` 匹配的运行基线上应用 snapshot
- 若 C 当前运行工作区脏污或基线不匹配，应优先重建运行 worktree，而不是冒险叠加
- snapshot apply 失败时，C 必须返回：
  - 失败阶段
  - 基线 commit
  - 冲突文件列表
  - 是否已自动回滚

还需要补一条最关键但当前文档尚未定死的规则：

- snapshot 生成必须基于一个明确的“冻结点”，不能直接从持续变化中的 dev workspace 现采

建议最少做到：

- B 在生成 snapshot 前先固定 `workspace_generation` 或等价的只读视图标识
- 同一次 run 绑定的 snapshot 内容必须与该冻结点一一对应
- snapshot 生成期间，如果 dev thread 仍可继续写文件，就必须切到隔离的 staging tree / 临时 worktree 中完成打包
- A 记录的不是“某时刻大概的代码状态”，而是“某个冻结上下文的稳定快照”

这样 B 才能知道自己看到的失败，是代码逻辑失败还是同步失败。

### V1 数据传递实现建议

文档需要把“怎么传”写死到可实现级别。建议 V1 采用下面这套最小实现：

1. B 在冻结点生成 `manifest + payload`
2. `manifest` 记录 `snapshot_id`、`base_commit`、`mode`、`digest`、`file_count`、`payload_size`、`encoding`
3. `payload` 默认为 `tar.gz` 或 patch 包
4. B 通过 node agent 以分块上传方式把 payload 发送给 A
5. A 写入本地 `SnapshotCatalog`，底层可先落盘到控制面文件目录
6. A 校验 digest 成功后，把 `snapshot_ref` 绑定到 `run_id`
7. C 接到 run 指令后，先向 A 请求 `snapshot_ref`
8. C 通过 node agent 流式下载 payload
9. C 本地再次校验 digest，校验通过后解包 / apply
10. C 把 `apply_result`、冲突列表、回滚结果结构化回报给 A

建议同时明确几个实现边界：

- V1 默认不做 B/C P2P 直传
- V1 默认不把 snapshot 放进数据库大字段
- `SnapshotCatalog` 负责 TTL、引用计数、大小限制和垃圾回收
- 大 payload 必须支持 chunking；是否支持断点续传可放到后续 phase
- A 只做受控中转和存储，不把自己退化成通用文件服务

## 环境一致性

B 和 C 不需要完全相同。

C 真正需要的是“可重建、可校验的运行环境”。

### 基本规则

凡是测试和运行需要的依赖，都必须落在项目声明中，不能只存在于 B 的临时机器状态里。

### V1 做法

在 C 上做显式环境准备：

- 使用项目内 bootstrap 脚本或环境清单
- 在 C 上执行运行依赖安装/更新
- 每次运行前做 preflight 校验

建议结合以下项目文件：

- 语言自身的 lockfile，例如 `go.mod`、`package-lock.json`、`poetry.lock`
- 可选 `.dalek/runtime-env.yaml`
- 区分角色的 bootstrap 脚本，例如：
  - `.dalek/bootstrap-dev.sh`
  - `.dalek/bootstrap-run.sh`

### C 上的 preflight 检查

每次执行前，至少检查：

- 必需命令是否存在
- 运行时版本是否正确
- 依赖安装状态是否与项目定义一致
- 必要环境变量和密钥是否已注入
- 依赖服务是否可达

建议把环境策略拆成三层，而不是“每次运行都安装/更新一遍”：

- `bootstrap`：低频、可缓存、可重建的基础环境准备
- `repair`：检测到 drift 时才执行的依赖修复
- `preflight`：每次运行前的轻量只读校验

否则 C 很容易退化成“每次 run 都做一次半部署”，速度和稳定性都会很差。

如果 preflight 失败：

- C 向 A 发送结构化失败结果
- A 将失败原因反馈给 B
- B 应修改项目环境定义，而不是长期依赖人工在 C 上手工修补

### 额外建议：B 侧前置校验

只在 C 上做 preflight 还不够。

因为 B 在开发过程中很可能无意中依赖了“本机偶然存在”的环境，而自己并没有意识到。

因此建议在 B 发起 run request 前，增加一层轻量校验：

- 当前工作区是否存在未声明依赖迹象
- lockfile 是否同步
- 关键运行命令是否存在
- 项目声明文件是否发生变化但尚未纳入 snapshot

这不能替代 C 的权威 preflight，但可以减少“B 改完就发，C 才报环境缺失”的来回试错成本。

## 日志与调试模型

用户已经明确：B 不需要全部日志，也不需要强实时。

因此 V1 使用“查询型日志模型”。

### C 默认向 A 上报的内容

对于每次 run，C 默认上报：

- 当前状态
- 完成后的退出码
- 摘要
- 日志尾部
- 产物索引

### B 按需查询

当 B 需要调试时，可通过 A 获取：

- 最近一次运行摘要
- 最近 N 行日志
- 按阶段过滤的日志
- 按关键字过滤的日志
- 完整日志下载
- junit、coverage、trace、截图等产物

这样 V1 会比“全量实时日志流”简单很多。

但还需要补 retention / cache 规则：

- C 是完整日志与原始产物的默认保留端
- A 只缓存摘要、tail、索引和小型产物元数据
- 每类日志和产物都要有 TTL、大小上限和 GC 策略
- `run` 成功与否，不应因为 artifact 上传失败而被粗暴覆盖

### 日志查询必须绑定代码上下文

调试时，B 不只是“要看日志”，而是“要看某份代码对应的日志”。

因此 run 日志查询至少必须绑定以下上下文：

- `run_id`
- `snapshot_id` 或 `commit_id`
- `ticket_id`
- `node`

否则 B 很容易看错日志，把上一次运行结果误认为当前代码结果。

## CLI / 产品面建议

### 新增 node 命令

- `dalek node add`
- `dalek node ls`
- `dalek node show`
- `dalek node rm`

### 项目注册命令调整

示意：

```bash
dalek project add \
  --name test \
  --dev-node node-b \
  --run-node node-c \
  --dev-repo-root /repo/test \
  --run-repo-root /repo/test
```

### 运行相关命令

示意：

```bash
dalek run request --ticket 42 --target test
dalek run show --id 108
dalek run logs --id 108 --tail 200
dalek run artifact ls --id 108
```

这里只是表达方向，具体 CLI 形态后续再收敛。

## 建议的内部落点

V1 不建议一次性重写全部实现。

更合理的方向是：

- 保留现有“纯本地项目”路径
- 为多节点项目新增 remote / multi-node project facade
- 在控制面和执行节点之间增加 node router
- 节点内通过 provider bridge 承载 agent 生命周期
- A 只做编排，不做脆弱的远程 shell 操作器

可能需要新增：

- `NodeRegistry`
- `RemoteProjectFacade`
- `NodeAgentClient`
- `ProviderBridge`
- 跨节点 `RunService`
- snapshot 打包 / 应用辅助模块
- 日志 / 产物查询 API

建议再显式拆几个内部职责，避免最后所有逻辑都堆进 daemon：

- `NodeSessionManager`：节点连接、心跳、重连、版本协商
- `RunScheduler`：队列、并发度、节点容量、取消
- `SnapshotCatalog`：元数据、校验和、TTL、引用计数
- `ArtifactIndexService`：产物索引与查询
- `RunReconciler`：A 重启后按 run/task 回补状态
- `ThreadMirror`：把 provider 的 thread / turn / approval 投影到 dalek 观测模型

## 跨节点验证状态机

当前方案要真正跑通，必须把 B -> A -> C 的验证流程定义成显式状态机。

如果节点启用了 agent runtime，建议把 run 状态机和 thread 生命周期分开：

- `thread` 生命周期由节点内 runtime 负责
- `run` 生命周期由 A 的 dalek 状态机负责
- A 只消费 thread/turn/event 事实，并把它们映射进 run/task/ticket 视图

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
- `timed_out`
- `reconciling`
- `succeeded`
- `failed`
- `canceled`

建议状态流转：

1. B 发起验证请求，A 完成幂等和授权接纳 -> `queued`
2. A 指示 B 生成 snapshot -> `snapshot_preparing`
3. snapshot 就绪 -> `snapshot_ready`
4. A 路由到 C -> `dispatching`
5. C 准备/修复运行环境 -> `env_preparing`
6. 只读 preflight 通过 -> `ready_to_run`
7. 若运行类 turn 需要人工审批，则进入 `waiting_approval`
8. C 开始执行 -> `running`
9. 执行结束后直接进入 `succeeded` / `failed` / `canceled` / `timed_out`

异常分支：

- snapshot 生成、上传、校验、应用失败 -> `failed`，并带 `failure_reason=snapshot_*`
- C preflight 失败 -> `failed`，并带 `failure_reason=env_preflight`
- 审批被拒绝或审批超时 -> `canceled` 或 `failed`，需在实现中二选一并保持全局一致
- 用户取消中 -> `canceling`
- 用户取消 -> `canceled`
- 节点离线且状态未知 -> `node_offline`
- 超过执行时限 -> `timed_out`
- A 重启或恢复中 -> `reconciling`

没有这个状态机，A 很难稳定展示中间态，也很难在重启或断线后恢复流程。

另外建议加几条硬规则：

- `run` 的最终态仅 `succeeded` / `failed` / `canceled` / `timed_out`
- `requested` / `snapshot_failed` / `env_failed` / `artifact_partial` 这类术语若保留，应实现为原因码或子状态，不应再与最终态并列
- `artifact_partial` 是结果补充态，不应和执行结果本身混成一个互斥终态；更稳妥的做法是拆成 `run_status + artifact_status`
- `bootstrap` / `repair` / `preflight` 必须是三个分离阶段，`preflight` 应保持只读
- 若运行链路允许 approval，必须显式进入 `waiting_approval`，不能让 run 在隐式阻塞中“卡住但状态仍显示 running/ready”

## Verify 流程时序图（文字版）

下面用“B 上开发中的一次验证请求”为例，把一次完整 verify 流程按时序展开。

参与方：

- `B-dev`：开发节点 B 上的开发工作区 / node agent / 开发 runtime
- `A-control`：中心控制面
- `C-run`：运行节点 C 上的运行工作区 / node agent / run executor / 可选 diagnosis runtime

### 主流程：一次成功的 verify

#### Step 0：前置条件

- 项目已经在 A 注册
- 项目已绑定：
  - `dev node = B`
  - `run node = C`
- B 和 C 都已与 A 建立受信连接
- B 和 C 上都存在该项目对应的 `workspace`

#### Step 1：B 发起验证请求

- B 在开发过程中，决定把“当前代码状态”拿去 C 验证
- B 向 A 发起 `verify request`
- A 创建 `task(kind=run_verify)` 及其 `run view`
- 若 B 上对应开发 thread 尚未恢复，A 先要求 B 的 node agent 恢复或创建 `dev_thread`

这里建议改成更严格的接纳流程：

- B 发起的是 `run request`
- A 先做幂等校验和授权校验
- 校验通过后创建 `task(kind=run_verify)` 与其 `run view`
- 创建成功后先进入 `queued`，而不是立即认为“流程已开始执行”

建议请求至少包含：

- `project`
- `ticket_id`
- `source_workspace`
- `source_workspace_generation` 或等价冻结点
- `verify_target`，例如 test / debug
- `request_id`

#### Step 2：B 生成代码快照

- A 要求 B 提交当前待验证代码的上下文
- B 进入 `snapshot_preparing`
- B 的 node agent 先冻结本次 run 使用的源码上下文
- B 的 node agent 从冻结后的 workspace 视图收集代码状态
- B 生成 snapshot，并返回给 A

此处 snapshot 至少要包含：

- `snapshot_id`
- `base_commit`
- `mode`（commit / patch）
- `source_workspace_generation`
- 文件清单
- payload 引用

如果生成失败：

- A 将 run 更新为 `failed`
- 失败原因标记为 `snapshot_prepare`
- A 记录失败原因并结束本次 verify

#### Step 3：A 校验并路由到 C

- A 收到 snapshot 后，先校验：
  - 请求是否幂等
  - `project -> dev node / run node` 绑定是否合法
  - snapshot 元数据是否完整
  - snapshot 是否超过大小和能力限制
- 校验通过后：
  - run 更新为 `snapshot_ready`
  - A 将执行请求路由到 C

#### Step 4：C 准备运行工作区

- C 收到请求后，定位该项目对应的 `run workspace`
- C 的 node agent 创建或恢复一个 `run_thread`
- C 检查当前运行工作区状态：
  - 当前基线 commit 是否匹配
  - 工作区是否脏污
  - 是否需要重建 worktree
- C 将 run 更新为 `env_preparing`

建议策略：

- 若工作区脏污或基线不匹配，优先重建运行 worktree
- 不建议在未知脏状态上继续叠加 snapshot

#### Step 5：C 应用 snapshot

- C 在匹配的基线之上应用 snapshot
- 应用成功后，进入环境准备与 preflight
- 应用失败时：
  - C 返回结构化错误
  - 包含冲突文件、失败阶段、是否已回滚
  - A 将 run 标记为 `failed`
  - 失败原因标记为 `snapshot_apply`

#### Step 6：C 执行环境准备与 preflight

- C 读取项目运行环境定义
- C 先判断当前 workspace 是否需要 `bootstrap` 或 `repair`
- 只有在需要时才执行：
  - bootstrap-run
  - 依赖修复 / 安装
- 然后执行只读 `preflight`：
  - 环境变量检查
  - 服务依赖检查
  - 运行时版本校验
- preflight 通过：
  - run 更新为 `ready_to_run`
- preflight 失败：
  - run 更新为 `failed`
  - 失败原因标记为 `env_preflight`
  - A 将失败摘要反馈给 B

#### Step 7：C 正式执行 verify

- C 默认通过本地 `run executor` 执行 verify
- 若本次需要 agent 参与诊断，C 再调用本地可用 runtime，而不是假设存在 codex
- 若本次 verify 会触发 approval，请先显式进入 `waiting_approval`
- 对于标准化的测试/运行 verify，V1 更建议默认禁止交互式 approval，保持链路可自动重放
- run 更新为 `running`
- C 开始采集：
  - 状态
  - 退出码
  - 日志 tail
  - 产物索引
  - thread / turn 事件

V1 中，C 不需要把全量日志持续流式推给 B，只需向 A 上报：

- 运行状态变化
- 失败摘要
- 日志尾部
- 关键产物元数据
- 必要的 provider 事件摘要（如 approval request、turn completed）

#### Step 8：C 收集产物并结束

- 运行结束后，C 进入 `artifact_collecting`
- C 归档：
  - full log
  - tail log
  - junit / coverage / trace / screenshot 等产物
- 若执行成功：
  - run 更新为 `succeeded`
- 若执行失败：
  - run 更新为 `failed`

若测试本身成功，但产物收集/上传失败：

- 执行结果保持成功
- 单独把 `artifact_status` 标记为 `partial` 或 `failed`
- 不建议把整个 run 粗暴改成 `failed`

#### Step 9：A 汇总并反馈给 B

- A 接收 C 的最终状态与摘要
- A 更新统一 run 视图
- A 将以下内容反馈给 B：
  - 当前 run 结果
  - summary
  - exit code
  - tail log
  - artifact 索引

此时 B 可以决定：

- 继续开发
- 再发起一次 verify
- 主动查询更详细的日志

### B 按需查日志的补充时序

当 B 需要调试时：

1. B 向 A 请求查看某次 run 的日志
2. A 先检查本地是否已有缓存 tail / 摘要
3. 若缓存足够，直接返回给 B
4. 若 B 请求完整日志或更大范围日志，则 A 再向 C 拉取
5. C 返回完整日志或过滤后的日志
6. A 将结果返回给 B

请求必须绑定：

- `run_id`
- `snapshot_id` 或 `commit_id`
- `ticket_id`

否则 B 很容易看错日志上下文。

### 失败分支 1：B 生成 snapshot 失败

时序如下：

1. B 发起 verify
2. A 创建 run，状态 `requested`
3. A 请求 B 生成 snapshot
4. B 生成失败
5. B 返回错误给 A
6. A 将 run 更新为 `snapshot_failed`
7. A 将失败原因反馈给 B

建议错误内容至少包含：

- 失败阶段
- 工作区标识
- 文件范围
- 是否涉及未支持文件类型

### 失败分支 2：C preflight 失败

时序如下：

1. B 发起 verify
2. B 成功提供 snapshot
3. A 路由到 C
4. C 应用 snapshot 成功
5. C 执行 preflight 失败
6. C 向 A 返回结构化环境错误
7. A 将 run 更新为 `env_failed`
8. A 将错误摘要反馈给 B

这里返回内容建议包含：

- 缺失命令
- 版本不匹配项
- 缺失环境变量
- 缺失服务依赖

### 失败分支 3：A 在执行过程中重启

时序如下：

1. B 发起 verify
2. C 已进入 `running`
3. A 发生重启
4. B 和 C 重新连回 A
5. A 根据 `run_id` / `request_id` 向 C 补拉运行状态
6. C 返回当前状态：
  - 仍在运行
  - 已成功
  - 已失败
7. A 回补 run 视图并恢复查询能力

建议 V1 默认规则：

- A 重启不应强制终止 C 上已在执行的 run
- C 恢复连通后应具备补报能力

### 失败分支 4：B 查询日志时 C 不在线

时序如下：

1. B 请求查看 run 日志
2. A 检查本地缓存
3. 若 A 有缓存 tail / summary，则先返回缓存内容
4. 若 B 请求完整日志而 C 不在线：
  - A 返回“完整日志暂不可取”
  - 同时返回最后缓存摘要与 tail

这样即使节点暂时不可用，B 也不会完全失去调试上下文。

### 失败分支 5：verify 期间触发 approval

时序如下：

1. C 进入 `ready_to_run`
2. 本地 diagnosis runtime 产生 approval request
3. node agent 向 A 上报 `approval_requested`
4. A 将 run 切到 `waiting_approval`
5. A 审计并转发 approve / deny
6. 若批准，则 run 回到 `ready_to_run` 或直接进入 `running`
7. 若拒绝或超时，则 run 进入 `canceled` 或 `failed`

V1 至少应明确三件事：

- 哪些 verify target 根本不允许进入 approval
- approval 的默认超时和默认决策
- approval 未处理时，run 在 UI 中应展示为阻塞，而不是伪装成仍在执行

## 安全与信任模型

V1 也必须有最基本的边界。

最低要求：

- 节点认证
- 节点和项目级别的请求授权
- 请求幂等
- 心跳 / 存活检测
- 版本兼容性检查
- node agent 与本地 provider/executor 的进程级隔离
- provider 会话和 dalek 项目作用域的绑定校验

建议：

- V1 先使用 token 鉴权
- 后续如有需要，再升级到 mTLS

但仅有“节点 token”还不够，V1 需要明确三层授权：

- 身份认证：这个连接到底是不是 B / C
- 项目授权：这个节点是否被允许访问该项目
- 动作授权：这个节点是否被允许执行该类动作（生成 snapshot、执行 run、读取日志、拉取产物）

否则 token 最终会演变成“拿到 token 就能干所有事”。

### 权限边界建议

还需要进一步明确授权范围：

- node token 绑定的是“节点身份”，还是“节点 + 项目作用域”
- B 是否可以请求任意项目在任意 C 上运行
- C 是否只接受来自指定 A 的请求

建议 V1 最少做到：

- 每个 node 都有独立身份
- 项目对 node 的绑定关系必须可校验
- run request 必须验证 `project -> dev node / run node` 的合法关系

还建议补两条：

- C 只接受来自受信 A 的、且 action 在 allowlist 内的请求
- “执行什么命令”必须来自 project-scoped 模板或受控配置，不能把 C 做成通用远程 shell

如果节点启用了 agent runtime，还应明确：

- 远端节点对外暴露的是 `node agent`，不是裸 provider
- provider 默认只监听本机回环或受限本地 socket
- 所有 approval decision 都应经过 A 审计，再由 node agent 转发给本地 provider
- A 不能越过项目作用域直接恢复或消费不属于该项目的 thread

这样才能避免未来多项目情况下的越权问题。

## 需要提前规划的失败场景

- B 可用，但 C 不可用
- C 可用，但 preflight 失败
- snapshot 在 C 上应用冲突
- run 完成，但产物上传失败
- A 重启时 B/C 仍然在线
- 重试导致重复 run 请求
- 节点版本不兼容

这些场景都应返回结构化状态，避免出现模糊失败。

## 恢复与回补策略

当前设计里，失败场景已经列出，但恢复动作也需要明确，否则系统很容易停在“不知道下一步怎么办”的状态。

### A 重启

- A 重启后应允许 B/C 重新注册连接
- A 应按 `run_id` / `request_id` 向 C 补拉最近状态
- 若补拉失败，至少应保留最后一次已知摘要，不让 run 直接“消失”
- 若节点使用 agent runtime，A 还应按 `event_cursor` 或 `thread_id` 补拉遗漏的 thread/turn 事件

更稳妥的做法是把回补对象拆开，而不是试图靠单个 cursor 恢复全部上下文：

- run 状态事件单独回补
- dev thread / run thread 事件各自回补
- artifact / log 索引单独对账

否则会出现“执行已结束，但 thread 视图仍停在旧 turn”这类半恢复状态。

### C 运行中断线

- C 与 A 断线时，run 是否继续执行，V1 应明确
- 建议：默认继续执行，待连接恢复后补报最终状态
- 若本地 runtime 仍健康，node agent 恢复后应优先查询 thread 当前 turn 状态，再向 A 回补

### snapshot apply 失败

- C 必须说明是否已回滚
- 建议优先采用“重建运行 worktree”而不是“在脏工作区上继续重试”

### artifact 上传失败

- 需要区分“运行本身成功”和“产物收集部分失败”
- 建议引入 `partial_success` 或额外产物状态，避免把本来成功的测试误判为失败

### 重复请求

- 所有跨节点请求都必须以 `request_id` 幂等
- A 和 C 都需要具备“识别重复请求并返回既有结果”的能力

还建议明确：

- 幂等键的作用域至少包含 `project + requester + request_id + action`
- 同一个 `request_id` 若参数不同，必须返回冲突错误，而不是覆盖旧请求
- 对 thread/provider 类请求，还需要包含 `thread_id` / `turn_id`，避免重复 turn 被误执行

## 可扩展性约束

当前方案可以扩展到更多项目和更多机器，但前提是 V1 不要写成“三台机器特判”。

因此建议从现在就明确以下约束：

- 不把角色字段写死成只能有 `dev_node` / `run_node` 两个最终形态
- 不把项目假设为永远只绑定一台运行机
- 不把日志链路假设为只能 C -> A -> B 的唯一固定路径
- 不把环境策略假设为每个节点都人工维护
- 不把 A/B/C 理解成固定三台机器，而应理解成三类角色
- 不因为支持多节点，就破坏现有单机/本地项目路径

更稳妥的理解方式是：

- V1 只是“多节点通用模型”的最小子集
- 当前主要描述 `control`、`dev`、`run` 三类角色
- 后续可以自然扩展到更多节点池、更多角色、更多项目
- 单机部署只是该模型的一个特例，不是另一套架构

因此数据模型建议从 V1 起就不要把 `project.dev_node` / `project.run_node` 当成唯一终态，而是：

- project 绑定多个 workspace / node assignment
- 当前版本的调度策略只“选择一个 dev，一个 run”
- 未来再扩展到 run pool、备用节点、按容量调度时无需翻模型
- 若项目是纯本地模式，也允许 assignment 退化为“同机单实例”

## 实现前必须拍板的问题

下面这些问题如果不在实现前收敛，后续大概率会演变成返工点，而不是普通缺陷。

### 1. `task`、`run`、`thread` 谁是唯一事实源

当前文档已经倾向于：

- `task` 是现有执行记录与事件链核心
- `run` 是跨节点验证视图
- `thread` 是节点内 runtime 的会话事实

但实现前必须进一步拍板：

- 哪个对象拥有最终持久化状态机
- 哪个对象只是投影视图
- 哪些状态允许从 runtime 事实映射回来，哪些绝不允许反向驱动主状态

更稳妥的方向是：

- `task` 作为唯一执行事实源
- `run` 作为 `task(kind=run_verify)` 的跨节点读模型
- `thread` 仅作为外部事实流和诊断上下文，不直接成为编排主键

否则 A 上最终会出现三套生命周期并存、彼此 reconcile 的局面。

推荐决策：

- `task` 是唯一持久化执行事实源，也是唯一最终状态机
- `run` 是 `task(kind=run_verify)` 的读模型和产品投影视图
- `thread` 只承载节点内会话事实、消息和诊断上下文
- runtime 事件只能补充 `task/run` 观测字段，不能直接回写最终态

### 2. 节点 provider 策略必须按节点配置，而不是按角色写死

当前方案此前把 B 和 C 都建模为固定 provider 组合，这是错误前提。

新的约束应该是：

- A、B、C 使用什么模型框架，由用户按节点配置
- provider 可选项至少包括 `Claude`、`codex`、`kimi`、`DeepSeek`
- 节点角色决定“负责什么工作”，provider 配置决定“通过什么模型/框架做这件事”
- `run node` 可以只有 `run_executor`，也可以再挂一个或多个 provider 做 diagnosis
- `dev node` 和 `control node` 也不应被写死到单一 provider

因此这里不应该再保留为开放问题，而应直接定死为 V1 约束：

- provider 选择在 node 配置层完成，不在 workflow 中硬编码
- 标准化测试/运行验证，优先走受控 `run_executor`
- 只有需要 agent 参与诊断/调试时，才调用该节点已配置 provider
- V1 不追求 B/C 两端会话模型完全一致，而追求“控制面状态机一致、节点 provider 可替换”

推荐决策：

- A/B/C 都支持声明各自 `default_provider`
- B 上开发 agent 默认使用项目或节点策略选出的 provider
- C 上标准 verify 默认走受控 `run_executor`
- 只有 debug / diagnosis / 需要 agent 推理参与的运行才升级为节点本地 provider thread
- provider 路由优先级应为：run/task 显式指定 > project 默认策略 > node 默认 provider
- V1 不要求 A/B/C 使用同一种 provider，只要求 capability 和协议兼容

### 3. approval 如何映射到 dalek 现有产品模型

文档现在已经承认多节点链路里 approval 是正式事件，但还没拍板它在产品面上的归属。

至少要明确：

- approval 是否一律映射为 inbox
- approval 是否会让 ticket 进入 `blocked`
- approval 超时后，run / task / ticket 各自如何收敛
- 默认审批人是谁，是用户、PM 还是项目策略

如果这件事不提前定义，后续 UI 和状态机都会打架：本地 provider 说在等 approval，dalek 侧却不知道该把它显示成 `waiting_approval`、`needs_user` 还是 `blocked`。

推荐决策：

- approval 一律先映射为 dalek inbox，再由 A 审计后转发
- run 进入 `waiting_approval` 时，对应 task 进入 `waiting_user`
- 若 approval 需要用户或策略外输入，则 ticket 进入 `blocked`
- approval 超时默认拒绝，并将 run 收敛到 `canceled`
- 标准 verify target 默认禁止进入 approval

### 4. snapshot 是否天然包含环境声明变化

当前文档已经把源码快照和环境准备拆开，但还没说清：

- lockfile 变化算源码快照的一部分，还是环境层的一部分
- `.dalek/bootstrap-run.sh`、`.dalek/runtime-env.yaml` 这类文件变更是否必须随 snapshot 一起验证
- 环境声明变化发生后，哪些缓存必须失效

如果这件事不定死，后续很容易出现“代码和依赖声明来自两个不一致版本”的伪结果。

更稳妥的规则通常是：

- 影响运行结果的项目内声明文件都属于待验证上下文
- snapshot 或其等价上下文必须能唯一标识这组输入
- C 的环境缓存只能建立在这组输入之上，而不是仅靠 `base_commit` 猜测

推荐决策：

- 影响运行结果的项目内声明文件默认都属于 snapshot 上下文
- 至少包括源码、lockfile、项目内 bootstrap 脚本、`.dalek/runtime-env.yaml` 一类运行声明
- snapshot digest 必须覆盖这组输入，而不只是源码 patch
- 这些文件变化后，C 上依赖相关缓存必须按 fingerprint 失效

### 5. 调度最小规则必须先定，而不是留给实现时补

文档提到了 `RunScheduler` 和 `capacity_hint`，但真正会影响系统行为的规则还没写出来。

V1 最少也要拍板：

- 单个 `run workspace` 是否串行
- 单节点最大并发是多少
- 是否先到先服务
- 同项目是否可以占满全部运行容量
- 长任务是否允许抢占或打断短任务

没有这些规则，多节点系统即使功能正确，也会在资源竞争下表现得不可预测。

推荐决策：

- 单个 `run workspace` 串行执行
- 单节点最大并发先做静态配置
- 调度策略先采用先到先服务
- 单项目默认不得占满全部节点容量，至少预留一个公平份额
- V1 不做抢占，只支持排队和显式取消

### 6. 恢复协议是否需要 lease / epoch

当前文档已经有 `request_id`、`attempt`、`deadline_at`，但这还不足以区分“旧控制会话”和“新控制会话”。

实现前必须明确是否引入：

- 节点连接 lease
- 控制面 session epoch
- per-run 的所有权代号或 fencing token

否则 A 重启、连接抖动或重试后，旧消息有可能覆盖新状态，特别是在 cancel / resume / approval 这类控制动作上。

推荐决策：

- 引入节点连接 lease 和控制面 session epoch
- 每个 run 下发一个 fencing token，节点只接受当前 token 对应的控制命令
- cancel / approval / resume 这类高权威控制动作必须带 epoch / token 校验
- 仅有 `request_id` 不足以表达控制权轮次

### 7. CLI / TUI / API 面向用户的主对象是什么

当前文档一方面坚持 `ticket/task` 是核心，另一方面又新增了 `dalek run ...` 命令。

实现前需要统一：

- 用户排障和观测时，主入口究竟是 `task` 还是 `run`
- `run` 是独立 noun，还是 `task` 的一个 specialized view
- UI 中取消、重试、查看日志等动作最终挂在哪个对象上

如果这点不先收敛，V1 很快就会在 CLI、TUI、API 中出现三套不一致的对象语言。

推荐决策：

- 用户主入口保持 `ticket` 和 `task`
- `run` 保留为 specialized view，用于日志、产物、运行摘要查询
- 取消、重试、恢复等控制动作最终都落到 `task`
- CLI 可以保留 `dalek run ...`，但语义应明确为 `task(kind=run_verify)` 的快捷视图

### 8. local facade 和 remote facade 的分层必须先切开

迁移建议里虽然说先做 `A <-> C`，但当前 dalek 还有大量单机假设。

因此实现前应先明确一个更底层的架构动作：

- 把现有本地执行路径抽象成 local facade
- 为多节点项目补 remote facade
- 上层编排逻辑尽量只依赖抽象接口，而不是散落在各处的“本地路径 + 远端特判”

如果这一步不先做，Phase 1 很容易退化成在本地实现里不断塞 remote if/else，后续会越来越难收敛。

推荐决策：

- 先抽象 `ProjectRuntimeFacade` 一类上层接口
- 本地项目走 local facade，多节点项目走 remote facade
- facade 下再分别实现 workspace、thread、run、artifact、log 的本地/远端适配
- 在 facade 边界之上禁止再出现“直接读本地 repo/SQLite/路径”的多节点特判

## 迁移建议

### Phase 0：仅完成设计

- 定义 node 模型
- 定义项目拓扑模型
- 定义 snapshot 与 run 契约

### Phase 1：先做单控制面 + 远程运行节点

- 只引入 A + C
- 先验证远程执行与日志查询能力
- 开发节点仍可以先本地简化

### Phase 2：引入独立开发节点

- B 成为正式 dev node
- 打通 B -> A -> C 的运行请求链路
- 实现快照同步

### Phase 3：补足稳定性

- 重试
- 心跳
- 产物保留策略
- 节点版本协商
- 更好的 TUI 支持

## 最终建议

最合适的 V1 不是“让主 dalek 直接操作远程路径”。

更合适的 V1 是：

- A 作为控制面
- A、B、C 的 provider 都按节点配置，保持和当前 dalek provider 配置方式一致
- B 作为开发节点，负责开发工作区和开发 agent
- C 作为运行节点，默认承载 `run executor`，必要时再使用该节点已配置 provider 做 diagnosis
- 通过快照把 B 的代码送到 C 验证
- 快照默认经 `B -> A SnapshotCatalog -> C` 传递，而不是 B/C 直连
- B 通过查询方式按需获取 C 的日志
- C 通过显式环境准备和 preflight 保证运行可复现

这个方案既尽量贴合 dalek 现有抽象，又能避免演变成一个脆弱的 SSH 编排系统。

最后建议把真正的落地顺序再收敛为：

1. 先把 `A <-> C` 的远程 run、日志、恢复跑通
2. 再把 `run` 明确收编进现有 `task` 体系
3. 最后再补 `B <-> A <-> C` 的 snapshot 同步与未提交代码验证

这样做可以把 V1 最大的不确定性，优先收敛在“状态机、恢复、鉴权、观测”这些真正难改的核心面上，而不是一开始就把 patch 同步和多节点编程链路全部摊开。
