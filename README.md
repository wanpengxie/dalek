# Dalek Core（v5）

一台可以自我维持、自我改进、自我复制、自我组织的智能单元的最小内核。副标题：如何优雅地把 LLM 塞进冯诺依曼机器。

## 模型（整合版）

**先验（相对于 milieu Ω）**
- **H** 账本：被动的追加介质，一台 channel 一条（`h/<name>.jsonl`）。死的，自己不跑。
- **R** runtime：读消息、成 view、调成员、落带盖章、投递、恢复。白盒——就是 `kernel.py` 里那几条；由 Ω 的一个 U 执行。
- **D ⊂ H**：账本里能被解释为机器描述的行（`#decl` `#peer` `#in`）。不是每一行都是。描述是唯一机器特异的、需要保存和遗传的结构。

**channel = (H, R, bindings, boundary)**。活的单元；封闭：影响它的一切都经门落在它的账本上。膜内地址只是序号；channel 名属于门。

**零件（按对描述做什么分）**
| | 做什么 | 性质 |
|---|---|---|
| Author：L、人 | 产生候选描述 | 同一接口类型：可寻址、可调用、可重复；随机性不是本质 |
| U | 执行 / 验证描述 | 确定、可重算；它自己是另一台 channel，经门当零件用 |
| **D = A + B + C** | c0 里的构造子系统 | c0 的一个成员（kind `D`） |
| F | 普通成员、普通 channel | 被造的 |

**构造器 D**（在 c0 里；A 建空 channel、构造成员、绑定；B 复制 Genome；C 构造 → 复制 → 装进子代 → 接 peer → 启动）。一个 Author 给 D 发请求：
```
build <name>      A：genesis
part <addr>       B：把请求者所在 channel 里 <addr> 的描述逐字复制进去
decl <L|U>        A：用新描述构造成员（其后各行是正文，直到下一个关键字）
in #1             C：接待员
peer <channel>    C：双向接 peer
start <text>      C：以 c0 门的名义把启动消息交给接待员
attach here|<channel> …   对已有 channel 做上述动作（组织）
```
D 对目标 channel 做的每一步都是 door 写在目标账本上的行；D 的回执发给请求者。D 对非请求沉默。

**Genome(H)** = 可遗传的部件描述 + bindings + peer 拓扑 + 启动。整盘账本复制 = checkpoint（恢复）；复制 Genome = 繁殖。

**c0** 承载 D + I_D（I_D = Genome(H_c0) 里对 D 的描述）；**E = Space** = c0 + 它造的 channel + peer 拓扑。

**词**（行首；作者约束）：`#genesis #admit #decl #peer #in` 只有 door 能说；`#step` 只有 R 能说；成员说的一切都是文本。输出文法：`>>> 地址` + 正文。

**宿主 Ω**（`run`）：轮流让每台 channel 沿自己的账本跑到静止；全部静止时轮询外生者一轮；无人开口即停。步数预算是宿主的事。

**replay**：同一段 `run`；Author/人照抄记录（每个地址一条队列）；U 重算；D 重算；逐字节比较每条账本。检验因果自洽，不检验 Author 的真实性。

## 状态
- `kernel.py` 335 行；`t_dalek/test_t.py` 6/6；`experiments/e1.py` 通过：c0 → c0.5 → c0.5.2，子 channel 经 peer 请 c0 的 D 造下一台，人只说一句，replay identical。
- 真 L 未接（`l.py` 是 v4 接口，待改）。
- 旧 E2（v4 的 K 自举）归档于 `experiments/old/`：自举是独立目标，不是 E 的定义。

## 跑
```
python3 t_dalek/test_t.py
python3 experiments/e1.py /tmp/e1
python3 kernel.py show|book|replay DIR
```

## 文件
```
kernel.py        H、R、D、宿主、零件实现（U、录音带）、replay、CLI
experiments/     e1.py；old/
t_dalek/         T1–T6
FAILURES.md      每条法律的出生证明（F1–F23）
REVIEW.md SPEC.md DESIGN.md   旧版审阅与设计；以本文、代码、FAILURES 为准
```

## 纪律
- 死的不动手：账本、描述只被读被写。
- 构造只由 D 做，且全部落账；成员只写文本。
- 创生来自外面：第一个 U、第一台 c0 来自 Ω；描述、组织的原因、启动请求可以来自里面。
- 每条进 R 的规则先有一份 FAILURES 记录。
