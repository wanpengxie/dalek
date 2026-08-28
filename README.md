# Dalek Core（v6）

一台可以自我维持、自我改进、自我复制、自我组织的智能单元的最小内核。副标题：如何优雅地把 LLM 塞进冯诺依曼机器。

## 模型（整合版）

**两个对象，分开**
- **配置 Config**（描述）：这台 channel **是什么**——成员（kind, 描述）、接待员、peer。**造机器只需要配置**；繁殖复制它。`conf/<name>.json`。
- **账本 H**（历史）：这台 channel **发生过什么**——消息、每一步、配置改动的记录。恢复重演它。`h/<name>.jsonl`。死的，自己不跑。
- **R** runtime：读消息、成 view、调成员、落带盖章、投递。白盒——就是 `kernel.py` 里那几条；由 Ω 的一个 U 执行。

**机器 = Space（细胞）。channel = Space 里的功能单元 = (Config, H, R)**，不是机器。封闭：影响它的一切都经门落在它的账本上。膜内地址 = 成员在配置里的序号；channel 名属于 peer。

**零件（按对描述做什么分）**
| | 做什么 | 性质 |
|---|---|---|
| Author：L、人 | 产生候选描述 | 同一接口类型：可寻址、可调用、可重复；随机性不是本质 |
| U | 执行 / 验证描述 | 确定、可重算；它自己是另一个 channel，经门当零件用 |
| **D = A + B + C** | c0 里的构造子系统 | c0 的一个成员（kind `D`）；B、C 集中在 c0，其他 channel 不带 |
| F | 普通成员、普通 channel | 被造的功能单元 |

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

**Genome = 配置。** 复制配置 = 繁殖；重演账本 = 恢复。两个操作读两个不同的对象。

**E = Space** = c0 + 它造的 channel + peer 拓扑。Space 的基因 = 各 channel 的配置 + peer 拓扑，其中 c0 的配置含 A、B、C（它们只是配置里的成员）。复制 Space = 按基因再造一个 Space。

**词**（行首；作者约束）：`#born`、`#conf add|in`（配置改动的记录）只有 door 能说；`#step` 只有 R 能说；成员说的一切都是文本。输出文法：`>>> 地址` + 正文。

**宿主 Ω**（`run`）：轮流让每台 channel 沿自己的账本跑到静止；全部静止时轮询外生者一轮；无人开口即停。步数预算是宿主的事。

**replay**：同一段 `run`；Author/人照抄记录（每个地址一条队列）；U 重算；D 重算；逐字节比较每条账本。检验因果自洽，不检验 Author 的真实性。

## 状态
- `kernel.py` 338 行；`t_dalek/test_t.py` 7/7；`experiments/e1.py` 通过：c0 → c0.3 → c0.3.1，子 channel 经 peer 请 c0 的 D 造下一个 channel，人只说一句，replay identical。
- 真 L 未接（`l.py` 是 v4 接口，待改）。
- 旧 E2（v4 的 K 自举）归档于 `experiments/old/`：自举是独立目标，不是 E 的定义。

## 跑
```
python3 t_dalek/test_t.py
python3 experiments/e1.py /tmp/e1
python3 kernel.py show|conf|replay DIR
```

## 文件
```
kernel.py        Config、H、R、D、宿主、零件实现（U、录音带）、replay、CLI
experiments/     e1.py；old/
t_dalek/         T1–T7
FAILURES.md      每条法律的出生证明（F1–F24）
REVIEW.md SPEC.md DESIGN.md   旧版审阅与设计；以本文、代码、FAILURES 为准
```

## 纪律
- 死的不动手：账本、描述只被读被写。
- 构造只由 D 做，且全部落账；成员只写文本。
- 创生来自外面：第一个 U、第一个 c0 来自 Ω；描述、组织的原因、启动请求可以来自里面。
- 每条进 R 的规则先有一份 FAILURES 记录。
