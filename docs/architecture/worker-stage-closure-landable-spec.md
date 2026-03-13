# Worker Stage Closure Landable Spec

> 本文不是抽象推导稿，而是**第一阶段真正要落地实现**的规格。
>
> 它只解决一个问题：
>
> **在不破坏当前 dalek 架构、不过度工程化、且不侵犯 worker 自维护状态 ownership 的前提下，怎么把当前 worker loop 的一轮执行做成“可收口的 stage”。**

## 1. 为什么只落这一份

当前系统真正最脏、最阻碍后续演进的，不是：

- ticket workflow 本身
- PM 拆解策略
- merge 流程

而是当前 `worker loop` 的一轮执行还太薄：

- 一次 raw run 结束
- 读一次 report
- 看 `next_action`
- 不够就只补报一次

这导致：

- `report` 缺失或非法时，系统缺少统一纠偏
- worker 明明做了很多事，但第一次没收口成功时，控制面很脆
- runtime、report、state.json、ticket 推进之间边界不硬

所以第一阶段只落这一份：

**把当前一轮 worker stage 做成最小但可靠的闭合单元。**

## 2. 绝对前提

### 2.1 不引入 PM 每轮介入 worker 语义状态

本方案严格遵守 invariant 7：

- PM/runtime 不在初始化之后改 worker 的语义状态文件
- `.dalek/state.json` 始终由 worker 自维护

### 2.2 不构建通用 step runtime

本方案不引入：

- 通用 step 引擎
- 独立 step 状态机系统
- 独立 execution object 宇宙

我们只增强当前已有的：

- `worker loop`
- `stage`
- `worker report`
- `state.json`

### 2.3 `worker-kernel.md` 是稳定规则，不是动态状态面

`worker-kernel.md` 负责定义长期稳定规则：

- worker 应如何读上下文
- worker 应如何维护 `state.json`
- worker 应如何收口与 report

它不是每轮由 PM 重写的动态 contract 文件。

### 2.4 prompt 是每轮唯一的动态控制输入

每轮 raw run 的动态控制输入只来自：

- entry prompt / retry prompt

而不是：

- PM 每轮改 `state.json`
- PM 每轮重写 worker 的本地语义状态

## 3. 三个 ownership

### 3.1 runtime / PM 拥有

- `worker loop`
- `task_run`
- stage 是否闭合
- ticket 主状态投影
- closure retry 次数与预算

### 3.2 worker 拥有

- `.dalek/state.json`
- 本地 phases / progress / blockers / handoff
- 每轮如何具体完成语义执行

### 3.3 kernel 拥有

- 稳定规则
- 长期 hard rules
- `state.json` 的最小维护义务

## 4. 三个关键对象

当前阶段真正要依赖的只有 3 个对象：

### 4.1 `raw agent run`

一次底层 bounded tool loop。

### 4.2 `worker report`

worker 发给 control plane 的候选控制输出。

### 4.3 `worker state snapshot`

worker 在本轮结束前写下的 `.dalek/state.json`。

它不是系统真相，但它是：

- worker 自维护的本地语义摘要
- 当前 stage 闭合判断的必要输入之一

## 5. 什么是 `stage`

在本落地方案里：

`stage = worker loop 内的一次可闭合推进尝试`

它不是：

- worker 自己显式理解的一套系统阶段
- 一个独立持久化实体系统

它只是 runtime/control plane 眼里的一个边界：

- 这轮是否已经闭合
- 不够的话是否在本轮内部再补一次收口

### 5.1 stage 与 raw run 的关系

一个 stage 可以包含：

- 1 次 raw run
- 或 2 次 raw run

其中第二次 raw run 只用于：

- 本轮收口补救

### 5.2 stage 与 phase 的关系

`phase` 是 worker 在 `state.json` 里的本地组织方式。  
`stage` 是 runtime 的控制闭合边界。

两者没有一一对应关系。

系统不依赖：

- worker 当前 phase 的具体名字

系统只依赖：

- 这轮是否已经形成足够收口材料

## 6. 第一阶段真实运行链路

这是本方案最核心的部分。

### 6.1 初始化

在 `ticket start / worker bootstrap` 时：

- 写入初始 `worker-kernel.md`
- 写入初始 `state.json`

此后：

- `state.json` ownership 转给 worker

### 6.2 正常 stage 运行

1. `worker loop` 打开一轮新 stage
2. runtime 用正常 prompt 启动一次 raw run
3. worker 读取：
   - `worker-kernel.md`
   - `state.json`
   - 当前 prompt
4. worker 做事
5. worker 在退出前：
   - 更新 `state.json`
   - 执行 `dalek worker report`

### 6.3 closure check

raw run 结束后，runtime 读取：

- 当前 run 的 `worker report`
- 当前 `.dalek/state.json`
- 当前 git/worktree facts

然后做一次 `closure check`。

### 6.4 closure check 的三种结果

#### A. 闭合成功

如果当前轮满足闭合条件：

- 生成 stage 闭合结果
- 再进入 ticket 主状态投影

#### B. 可补救

如果当前轮没有完全闭合，但属于可补救情况：

- 不结束当前 stage
- 发起一轮 `closure repair run`
- 这仍属于同一个 stage

#### C. 补救失败 / 超预算

如果已经达到 closure retry 上限，或当前情况不宜继续补救：

- 当前 stage 以 `wait_user / escalated / failed` 之一收口
- loop 停止或升级

## 7. 什么叫“闭合成功”

第一阶段，闭合条件必须收得很保守。

### 7.1 report 条件

至少要求：

- report 绑定当前 `task_run_id`
- `next_action` 合法且非空
- `summary` 非空
- 若 `next_action=wait_user`，`blockers` 非空

### 7.2 state 条件

至少要求：

- `.dalek/state.json` 是合法 JSON
- 是 worker 本轮结束前最新写下的状态
- 含有可供后继消费的最小 handoff 信息

### 7.3 handoff 最小字段

第一阶段不引入复杂 handoff 协议，只要求 `state.json` 至少能提供：

- `progress.summary`
- `progress.blockers`
- `progress.verification`
- `handoff.next_frontier`
- `handoff.risks`
- `handoff.open_questions`

### 7.4 artifact 不是闭合

以下都不算闭合成功：

- 有 diff
- 跑过测试
- agent 退出
- 打印了总结
- 只写了半截 `state.json`

必须：

- `report + state snapshot + git facts`

一起满足最小闭合条件。

## 8. closure repair run 的真实语义

### 8.1 它是什么

`closure repair run` 不是新 stage，也不是新 ticket 任务。

它只是：

**同一 stage 内的一次收口补救尝试。**

### 8.2 它怎么控制 worker

不是靠 PM 改 `state.json`。

而是靠一条更窄的 prompt，明确告诉 worker：

- 上一轮执行已经结束
- 当前 stage 尚未闭合
- 缺失项是什么
- 请基于当前 worktree、测试结果和 `state.json` 补齐收口
- 不要扩展任务范围

### 8.3 raw run2 会不会继续做别的

会，有可能。

系统不能假设它 100% 只补收口。

第一阶段的控制点是：

- prompt 把范围收窄
- retry 次数严格受限
- 在 stage 闭合前，run2 新做的一切仍然都只是当前未闭合 stage 的 artifact
- **不闭合，就不承认**

## 9. `worker-kernel.md` 的落地改法

当前 [worker-kernel.md](/Users/xiewanpeng/agi/dalek/internal/repo/templates/project/control/worker/worker-kernel.md) 应新增或强化 3 条稳定规则：

### 9.1 `state.json` 由 worker 自维护

明确写死：

- worker 自己维护 `.dalek/state.json`
- PM/runtime 不在初始化后写它

### 9.2 每轮退出前必须同步最小收口信息

worker 每轮结束前，至少要把以下内容同步进 `state.json`：

- 当前 summary
- blockers
- verification 结果
- next frontier
- 风险与 open questions

### 9.3 closure repair 模式规则

如果 prompt 明确表明当前是“收口补救”，worker 必须：

- 优先补齐本轮收口
- 不扩展任务范围
- 再执行一次 `dalek worker report`

## 10. `state.json` 的落地改法

当前 [state.json](/Users/xiewanpeng/agi/dalek/internal/repo/templates/project/control/worker/state.json) 中固定 4 phase 结构保留与否，不作为第一阶段控制关键点。

第一阶段真正要补的是两块稳定字段：

### 10.1 `progress`

建议最小结构：

```json
"progress": {
  "summary": "",
  "blockers": [],
  "verification": {
    "ran": [],
    "status": "unknown"
  }
}
```

### 10.2 `handoff`

建议最小结构：

```json
"handoff": {
  "next_frontier": "",
  "risks": [],
  "open_questions": []
}
```

注意：

- 这是 worker 自维护 schema
- 不是 PM/runtime 每轮填进去的 contract

## 11. runtime 侧要怎么改

第一阶段 runtime 只做 4 个改动。

### 11.1 从 `executeWorkerLoop` 中抽出 `runStageUntilClosed`

当前 [worker_loop.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_loop.go) 的一轮 stage 逻辑，需要收成：

- 开一轮 stage
- raw run #1
- closure check
- 必要时 repair run
- 最终产出一个 stage 结果

### 11.2 新增 `evaluateStageClosure(...)`

这是 pm service 内部 helper，不是新系统。

它只做一件事：

- 读取 report + `state.json` + git facts
- 判断当前 stage 是否闭合

### 11.3 升级 retry prompt

当前 [constants.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/constants.go#L29) 里的 `emptyReportRetryPrompt` 太窄。

第一阶段要升级成：

- 通用 closure repair prompt
- 能表达缺失项和收口要求

### 11.4 升级 `worker_report_closure.go`

当前 [worker_report_closure.go](/Users/xiewanpeng/agi/dalek/internal/services/pm/worker_report_closure.go) 只处理“连续两轮没有 report -> 自动 wait_user”。

第一阶段要把它升级成：

- closure retries exhausted 的统一兜底路径

## 12. stage 成功后的最小结果

第一阶段不引入新表。

只要求 stage 成功后，系统最少能留下这些事实：

- `ticket_id`
- `worker_id`
- `stage_seq`
- 关联的 `task_run_ids`
- `decision`
- 本轮接受的 `summary`
- 本轮摘取的 `handoff`
- `closed_at`

第一阶段可以先通过：

- task event
- worker event
- ticket lifecycle event

来持久化这些事实。

## 13. 非目标

第一阶段明确不做：

- PM 每轮编译 worker 语义状态
- 通用 step runtime
- 复杂 stage FSM
- 通用 lease / generation 系统
- 语义正确性保证
- exactly-once 外部副作用控制

## 14. 第一阶段验收 case

这一份落地规格至少要通过下面这些真实 case：

### 14.1 继续正常工作的 case

- 简单 ticket 正常 `done`
- `wait_user` 正常阻塞并创建 inbox
- 多轮 `continue` 正常推进

### 14.2 新体系必须补上的 case

- report 非法，但能在当前 stage 内补救后收口
- 一轮 raw run 做了大量工作，但第一次收口失败，不直接把 ticket 打坏
- 复杂 ticket 在多轮推进后，恢复时从上一个已闭合 stage 继续

## 15. 最终收敛

第一阶段真正要落地的，不是：

- 新的通用执行框架

而是：

**把当前 `worker loop` 的一轮 stage，从“raw run 结束后读一次 report”升级成“worker 自维护状态 + runtime 闭合检查 + 必要时同轮补救”的最小可靠单元。**
