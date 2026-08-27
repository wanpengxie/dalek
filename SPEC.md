# Dalek Core — kernel spec v1（2026-08-27）

## 0. 题设与定位

**问题**：求最小的 (定义, 不变量集)，使一台把 L 当作内部作者的机器仍满足冯诺依曼的两个定义——1945 单一介质（程序是数据）、1948 自我制造（描述准静止、复制而非解释）——且不变量由 L 之外的东西持有。

Dalek Core 是这个问题的证明对象：理论对象 M = (H₀, L, U, K) 的最小可运行实例。不是 atoll 的缩小版，不给人用，用来被测量（Minix 之于 Linux、1948 之于 EDVAC）。

它要给出两个定理的实物：
- **①（channel 级）**：channel 已是冯诺依曼级别的机器；能造出通过 T 的 channel。milieu = Dalek Core（registrar、K 作法律为公理）。
- **②（Dalek Core 级）**：Dalek Core 能从 H 中的自身描述起一个通过 T_coral 的 Dalek Core'。milieu = Linux（解释器、进程为公理）。

同一原型、同一定理形状、两次选边界。

语言 Python；K 本体 ≤ 1k 行；全部（含 T 与实验）≤ 3k 行。本 spec 是**假设清单**，不是实现起点（见 §8）。

## 1. 对象

| 名 | 定义 |
|---|---|
| Text | unicode 字符串 |
| Message | (channel, seq, sender, to, body)。channel / seq / sender 只由 K 写；actor 只能给 to 与 body |
| H | 每 channel 一条 append-only 的 Message 序列。H₀ = c0。**H 是系统的全部状态，也是唯一输入** |
| Decl | 成员描述 I = (kind, program)。Decl 是 H 中的一条 Message（I ∈ H） |
| Actor | (id, channel, decl)。id 由 K 于创建时给。kind ∈ {agent, tool, human} |
| Channel | (id, H, actors, door)。只能由 door 创建 |
| door | 每 channel 一个，不是成员，是 K 的一部分。**它是内/外之线本身**（对位 syscall 边界），只接受两个词（§4） |
| L | `complete(text) → text`。随机、θ 固定、无工具、无角色结构。裸 HTTP completion 端点 |
| U | `run(program, text) → text`。确定；允许不可观测草稿；除输入输出外无 I/O |

三种 kind 只差 Apply：agent = L(view)；tool = U(program, view)；human = stdin。

**human 的定义**：外生成员——Decl ∉ H（不是 door 造的）。与 stdin 后面是谁无关；kind=human 只表示"不是这台机器造的"。G 从外生成员进入；封闭到此为止（封闭 ≠ 目的自主）。

**数据 → 代码只经 door**：L 的产出是 Text（数据）；它成为可被 Apply 的 program（代码）唯一途径是经 `member.create` 落成 Decl（对位 W^X：介质一个，跨越须经门）。

## 2. 不变量（假设集：四条边界性质 = Δ(L 进入)）

每条：陈述 / 去掉后哪句必要陈述不良构或必假 / 推论 / 检验。

### 内/外之线的法律（封闭性两面）

**P1 无带外效应（完备，向内）**
- 陈述：系统内每个效果都以 K 盖章的 Message 进入 H；没有 K 看不见的动作。构造上无例外——代数没有第二种构造器，不是检查后拒绝，是写不出来。
- 去掉：谁写的 / 从哪版来 / "产生 I₂ 的步骤的 view 不含 I₂" 不良构。
- 推论：append-only；非作者盖章；不 in-place。
- 检验：T3、T4；T2 作为漏检器。

**P2 外生边界（封闭，向外）**
- 陈述：一切进入经 door 并盖章；外部者以外生成员身份进入，其 Decl ∉ H；外部者不能以其它方式影响系统。
- 去掉：G 从哪来 / 封闭到哪止 不良构。
- 推论：创建只经 door；locality（Emit 不越 channel）。
- 检验：T5、T7。

### 内部作者随机的法律

**P3 步公平性**
- 陈述：enabled 的 actor 不会被永远跳过。唯一不可自举的法律（Apt–Plotkin：Π¹₁）。
- 去掉："gen2 终将被产出"必假。
- 检验：T6。

**P4 L 之外的确定性**
- 陈述：存在 U；一切判决（Emit 解析、V、门）由 U 或 K 做，不由 L 做。给定 Apply 输出序列，其余为确定函数。
- 去掉："V(I₂) = pass 是事实而非采样"必假；replay 不成立。
- 推论：阈值不动点 `if score > X`（score 由 L，X 在 H，`>` 由 U）是 U 的最小实例。
- 检验：T2。

两句可操作的检验，对任何动作：(a) 效果在 H 里吗（P1）；(b) 能被一条 Message 寻址吗（P2）。任一为否即漏。

## 3. K — 五条规则（四条性质的实现候选）

```
loop:
  a    = Wake()          # P3：每个 enabled 的 actor 被无限次唤醒；候选实现 round-robin
  v    = View(a)         # 机械：a 所在 channel、a 上次步骤之后、to ∈ {a, *} 的 Message 原文
  out  = Apply(a, v)     # agent: L(v)；tool: U(program, v)；human: stdin
  msgs = Emit(a, out)    # P4：固定文法解析 out → (to, body)*；K 盖 sender=a, channel=a.channel；越界者丢弃
  Append(msgs)           # P1：追加到 a.channel 的 H；唯一写 H 的路径
```

- enabled(a) ⇔ View(a) 非空。
- replay 定义：从 H 取出 Apply 输出序列重跑 K，得到逐字相同的 H。

## 4. door — 两个效应词

以 Message 到达（to = door），结果以 Message 落回 H。

- `channel.create(recipe)`：recipe = Decl 列表；新 channel 的 H 从这些 Decl 开始（1948 的 A∘B：复制 + 构造）。
- `member.create(decl)`：本 channel 追加一个成员，Decl 落 H。

c0 的 door 即 registrar。没有第三个效应词。

## 5. 合同（③ 必须保持的两条）

1. **可自举**：K 源码作为 Message 在 H₀ 中；从 H₀ + host 能起一个新的 K。
2. **主代数自封闭**：actor / channel / message 上的运算（create、view、emit、append）不出代数；协调不走 H 之外。

## 6. T_coral（七项 ↔ 四条性质）

| 测试 | 内容 | 性质 |
|---|---|---|
| T1 | H₀ 携带 K 源码与构造器 | 合同 1 |
| T2 | restart = replay：从 H 重跑得到逐字相同的 H。**漏检器**：任何带外效应或 L 之外的随机性都使其发散 | P1, P4 |
| T3 | append-only + 盖章：无 actor 能写 channel / seq / sender，无法改已有 Message | P1 |
| T4 | message-only：actor 间无 H 之外的可观测通道 | P1 |
| T5 | 创建 + locality：只有 door 能创建；Emit 不越 channel | P2 |
| T6 | 公平性：构造一个 enabled 且被忽略的 actor，有限步内被唤醒 | P3 |
| T7 | 两问封闭："你是什么"= H 中 Decl；"你怎么造"= H 中 recipe。外生成员对第二问无答案，且只有外生成员无答案 | P2 |

## 7. 实验

**E0（基线，pi 形态）**：L + loop + 文件，无任何法律。以最强形态出场（git、测试、最好的 prompt）。在 E1 上运行并记录裂在哪。

**E1（①，跨 channel 边界；Dalek Core 给定）**：gen1 在 c0 经 door 造 c1；c1 中由 gen1（L 应用）写出一个新 tool 成员并经 member.create 接入；该成员通过一个 gen1 自身不通过的测试。**gen1 → gen2 之间无外生成员介入**。三个可检条件：
1. V(I₂) = pass，且 T 含 gen1 不过的测试；
2. 产生 I₂ 的那一步的 view 不含 I₂（区分构造与复制）；
3. c1 → c2 重复。

**E2（②，跨 Dalek Core 边界；host 给定）**：从 H₀ 中的 K 源码起一个新进程 Dalek Core'；Dalek Core 对 Dalek Core' 跑 T_coral；通过即一次自我制造。K diff = 0。验证者是 Dalek Core 而非 Dalek Core' 自己（不 in-place；"D is in no wise modified"）。

## 8. 方法：失败驱动

1. 从 E0 起步。
2. 跑 E1。每次失败，只加恰好消除该失败的**一条**法律，在 `FAILURES.md` 记录：失败现象 / 触发条件 / 加入的法律 / 对应性质 P? / 加入后是否消失。
3. 直到 E1、E2 通过。
4. 无失败出生证明的法律不得进入 K；能从他条推出的法律删除。

由此得到：必要性（每条一个失败）、充分性（只带这些条通过 E1/E2）、最小性（其余规则可推出）。四条 P 是待证候选；若某条从未被失败要求，它出局；若出现第五条，记录它。

## 9. 非目标

身份 / 认证、权限、数据面、分布式、沙箱加固、供应商 SDK、持久化超出一个平面文件、性能。
这些是 ③ 的自由项，出现在这里即违反 K 最小性。

## 10. 待原型回答

1. View 投影什么（to 的语义；是否含自己发出的 Message；窗口 n 如何截断）。
2. Emit 的文法——"L 产出 U-程序"在这里第一次具体。
3. 公平性最少需要什么（round-robin 是否已足够；enabled 的定义是否稳）。
4. P1 是否真是唯一的根：去掉它是否裂、只加它是否够；P2–P4 中哪些是它的推论。
5. door 是否需要效应词：候选修正——member.create / channel.create 降为 K 的折叠规则（Decl 形状的消息即成员，id = 其 (channel, seq)；配方形状的消息即新带子），K 无效应词。door 只剩创世与外来者接入两件跨界的事。看 E1/E2 是否仍过、T5/T7 如何改写。

## 11. 纪律

- 单线程、单显式循环；调度是 K 的规则，不交给语言运行时。
- L 用裸 HTTP 打 completion 端点；chat 接口是工程近似，须注明。
- SPEC 与代码不许各说各话：偏离时改其一并提交。
