# coral — kernel spec v0

理论对象 M = (H₀, L, U, K) 的最小可运行实例。不是 atoll 的缩小版；是 ①② 两篇文章的证明对象。
语言 Python；K 本体 ≤ 1k 行，全部（含 T 与实验）≤ 3k 行。

## 1. 对象

| 名 | 定义 |
|---|---|
| Text | unicode 字符串 |
| Message | (channel, seq, sender, to, body)。seq / sender / channel 只由 K 写 |
| H | 每 channel 一条 append-only 的 Message 序列。H₀ = c0 |
| Decl | 一个成员的描述 I = (kind, program)。Decl 本身是 H 中的一条 Message（故 I ∈ H） |
| Actor | (id, channel, decl)。kind ∈ {human, agent, tool}；id 由 K 在创建时给 |
| Channel | (id, H, actors)。只能由 K 创建 |
| L | `complete(text) → text`。随机、固定、无工具、无角色结构 |
| U | `run(program, text) → text`。确定；允许不可观测草稿；无其它 I/O |

三种 kind 只差 Apply 的实现：human 读 stdin；agent = L；tool = U(program)。

## 2. K — 五条规则

```
loop:
  a    = Wake()          # 公平：每个 enabled 的 actor 被无限次唤醒；实现为 round-robin
  v    = View(a)         # 机械：a 所在 channel 中、a 上次步骤之后、to ∈ {a, *} 的消息，原文
  out  = Apply(a, v)     # human: stdin；agent: L(v)；tool: U(program, v)
  msgs = Emit(a, out)    # 按固定文法把 out 解析为 (to, body)*；K 盖 sender=a、channel=a.channel
  Append(msgs)           # 追加到 a.channel 的 H
```

- enabled(a) ⇔ View(a) 非空。
- 步公平性：enabled 的 actor 不会被永远跳过。这是唯一不可自举的法律。
- locality：Emit 只能产生 a 所在 channel 的消息；to 只能是本 channel 成员或 `*` 或本 channel 的 door。
- 确定性：给定 Apply 输出序列，其余全是确定函数 ⇒ replay = 用记录的 Apply 输出重跑 K。

## 3. K 的门（唯一的两个效应词）

每个 channel 有一个固定 door（不是成员）。它只接受两个词，都以消息形式到达，结果都以消息形式落回 H：

- `channel.create(recipe)`：recipe = Decl 列表。新 channel 的 H 从这些 Decl 开始（A∘B：复制 + 构造）。
- `member.create(decl)`：在本 channel 追加一个成员，Decl 落 H。

c0 的 door 就是 registrar。没有第三个效应词。

## 4. 合同（③ 必须保持的两条）

1. **可自举**：K 源码作为 Message 在 H₀ 中；从 H₀ + host（python 解释器、进程）能起一个新的 K。
2. **主代数自封闭**：actor / channel / message 上的运算（create、view、emit、append）不出代数；协调不走 H 之外。

## 5. T_coral（七项，归两头）

自举：
- T1 H₀ 携带 K 源码与构造器
- T2 restart = replay（从 H 重跑得到同一 H）

封闭：
- T3 append-only + 盖章（无 actor 能写 seq / sender / channel）
- T4 message-only（actor 之间无 H 之外的可观测通道）
- T5 创建 + locality（只有 door 能创建；Emit 不越 channel）
- T6 公平性（构造一个 enabled 且被忽略的 actor，验证有限步内被唤醒）
- T7 两问封闭（"你是什么"= H 中的 Decl；"你怎么造"= H 中的 recipe）

## 6. 两个实验

- **E1（①，跨 channel 边界，coral 给定）**：gen1 在 c0 里通过 door 造 c1，c1 中由 gen1（L 应用）写出一个新 tool 成员并接入；该成员通过一个 gen1 自身不通过的测试。三个可检条件：V 通过；产生它的步骤的 view 不含它自己；c1 再造 c2 重复。
- **E2（②，跨 coral 边界，host 给定）**：从 H₀ 里的 K 源码起一个新进程（coral'），coral 对 coral' 跑 T_coral；通过即为一次自我制造。K diff = 0。

## 7. 非目标

身份 / 认证、权限、数据面、分布式、沙箱加固、供应商 SDK、持久化超出一个平面文件。
这些是 ③ 的自由项，出现在这里即违反 K 最小性。

## 8. 待原型回答的三个问题

1. View 投影什么（to 的语义、是否含自己发出的消息）。
2. Emit 的文法——"L 产出 U-程序"在这里第一次具体。
3. 公平性最少需要什么（round-robin 是否已足够）。

## 9. 方法（2026-08-27 补）

本 spec 是假设清单，不是实现起点。§2 的五条规则是待证候选。
构建顺序是失败驱动的：从 pi 形态（L + loop + 文件，无任何法律）起步，跑 E1 且要求 gen1 → gen2 之间无人；每次失败只加恰好消除该失败的一条法律，并在 `FAILURES.md` 记录该失败。没有失败作为出生证明的法律不得进入 K。
