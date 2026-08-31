# M1 设计：syscall 闭集、运行时定义、洞、c0 的构造流程

理论在 DALEK.md。本文回答四个问题，供审阅；`runtime.py` 等是按第一版直觉写的草稿，与本文不一致处以本文为准，第 3 节逐条列出。

---

## 1. syscall 的闭集

先分两层，两层都要闭：

### 1.1 介质动作（运行时提供，转移表里的行）

| 动作 | 谁能发 | 效果 |
|---|---|---|
| **msg** `>>> <tag>` + 正文 | 任何 program actor | R 将 channel 内逻辑 tag 解析为内部数字 addr 后追加 msg；不存在则丢弃 |
| **place** `>>> place <channel> <kind> [in] [bind=…] [tag=…]` + text | 持有 `bind=place` 的 actor | R 分配唯一有效 tag，在账本追加带完整 text 的 place；回执为 `<channel>/<tag>` |
| **world 动词** `>>> spawn <dir>` / `>>> stop <pid>` | 持有 `bind=<动词>` 的 actor | 一张表，用 Ω 实现（spawn 按 loader 协议起一台机器）：`Exec.spawn(init, dir)` / `Exec.stop(pid)`；给调用者追加 `msg(from=<动词>, body=…)` |

门的抄写不是动作，是 door kind 的转移。**介质动作闭集 = {msg, place, spawn}。** 没有 stop、没有删、没有读账本。

### 1.2 syscall（写给 c0 接待员的请求，组织层）

| 请求 | 定义 | 地位 |
|---|---|---|
| **add** `add <channel> <kind> [in] [bind=…]\n<text>` | = 一次 place | **原始** |
| **spawn** `spawn <name>` | pack + place(门) + spawn + 经门踢 | **原始**（唯一向机器外的动作） |
| peer `peer <a> <b>` | = `add a door\nb` + `add b door\na` | 派生 |
| realize `realize\n<G>` | = 对 G 里除自己之外的每个成员 add，对每条 peers peer | 派生 |

**闭集 = {add, spawn}；{peer, realize} 是它们上的程序。**

**为什么闭**：合法 G 的全部内容是 channels × members(kind, text, bind) × receptionist × peers。`add` 覆盖成员（含 `in` 和 `bind`），门也是成员（kind=door），所以 `add` 单独就能从空生成任何 G；`spawn` 是唯一让另一台机器存在的动作。没有第三种改变机器形态的方式。

**故意没有的**：
- born —— channel 存在当且仅当它有 actor；第一次 add 就是出生。
- copy —— 复制 = 用同一段 text 再 add。B 不需要专门动作。
- start —— 第一条消息是普通消息，经根门进来。
- delete / retire —— M2 定为第三个 syscall `channel.retire.actor`（追加 `retire` 行，R 不再 step 它，decl 略过，地址不复用）。抹掉账本行的 delete 被原则排除。M1 没有。
- rebuild / rebind / clone —— 管理器动作，都是 spawn 与 add 的组合，属于 c1/c2 的程序，不是 syscall。
- 改接待员 —— `add … in` 会把新 actor 设为接待员（fold 规则），所以可以改，但没有"只改接待员不加 actor"的动作。这是有意的：接待员是某个 actor 的属性，不是独立状态。

---

## 2. 运行时的完整定义（M3.3，2026-08-31 同步；旧的 Exec.run / 视图 / 批量 pending 模型见 git 历史）

### 2.1 状态

```
S = (order, {name → Ledger}, Σ)        Σ = {actor → fn 的内部状态}：易失，不由 H 折出，重启即空（DALEK 1.7）；个体 = (order, Ledgers)
Ledger = 行的序列，seq 从 1 严格递增，单写者（本机器的运行时进程）
```

行（四种，字段闭集）：

```
place  {seq, k:"place", addr, kind ∈ {program, door}, text, bind ⊆ {syscall, spawn, stop}, in: bool, by, tag, iface?, at?}
retire {seq, k:"retire", addr}
msg    {seq, k:"msg",   from, to, body, run?, at?, by?}     from ∈ 地址 ∪ {0, door, channel.*, spawn, stop}
step   {seq, k:"step",  actor, upto, out, err, run?}
```

派生量（全部由行折叠得到）：

```
actors(c)        = 按 seq 折 place / retire：addr → (kind, text, bind, tag, iface, retired, fn)；addr = 该 channel 第 n 条 place 的 n
receptionist(c)  = 最后一条 in=true 的 place 的 addr；没有就没有
holder(c, tag)   = 唯一满足 tag 相同且未退役的成员；没有则为空
cursor(c, a)     = 该 actor 最后一条不带 run 的 step 的 upto；无则 0
pending(c, a)    = {msg 行 : to = a, 不带 run, seq > cursor(c, a)}
offset(box)      = 带 at 的行里最大的 at（该收件箱读到哪）
fn(a)            = place 行折到时实例化一次：program → Exec.load(text, {call, me, channel})（L 也是 program，text 是它自己的 agent loop）；door → 介质的函数体；retire 折到时丢弃
```

`fn` 是常驻函数；它里面的东西（globals、对话、中间值）是易失运行状态 Σ，**不入账**：H 记的是 call 边界（2.2）。活着的机器是 (G, H, Σ)，个体是 (G, H)；Σ 是工程（DALEK 1.7）。重启 = 重新折叠 = 重新实例化，是一个事件（入账归 M4，H10）。`order` 不在账本里——见 H12。

### 2.2 调用与转移

事件 = `pending(c, a) ≠ ∅`，取最早一条 m。转移 `invoke(c, a, m)`：

```
invoke(a, m):    reply = fn(a)(m)，m = {seq, from, to, body, channel}；期间 a 的每次 call(head, body) 依次 dispatch
                 追加 step(a, upto = m.seq, out = 各次 call 的帧 + ">>> re\n<reply>\n<<<", err)
                 事件：reply 非空 → dispatch(a, m.from, reply)（送回发送者；门则出去；不记帧）
                 嵌套（caller 存在）：step 带 run；reply 非空 → 追加 msg(a → caller, reply, run)；返回 reply
dispatch(a, head, body):
  head = "0", body = "show [a] [b]" | "who"        → 追加 msg(0 → a, body, run)；返回行 / 成员表（内容可重算，不入账）
  head = "channel.* …" ∧ syscall ∈ bind(a)         → 执行；追加 msg(head → a, 回执, run)；返回回执
  head = "<动词> <参数>" ∧ 动词 ∈ bind(a)           → 执行；追加 msg(动词 → a, 结果, run)；返回结果
  head 解析为成员 t（tag；数字序号仅内部兼容）         → 追加 msg(a → t, body, run)；t 退役则返回 ""，否则返回 invoke(t, 该行, caller = a)
  其他                                             → 返回 ""（丢弃）
fn(door)(m):     Port.send(text, {from: 本 channel 的端点, body})；返回 ""
inbox(c):        收件箱新行 {from, body} @at → receptionist(c) 存在时追加 msg(from = 指回 from 的门 ∨ "door", to = receptionist, body, at)
```

`run` = 当前事件的 seq，一次事件里追加的每一行都带它；只有事件推游标。调用是嵌套的（调用栈），可重入；名字在调用那一刻解析，所以先放的用得了后放的。

### 2.3 调度

```
loop:
  inbox 全部 channel
  对每个 channel（按 order）：取 pending 非空且最早消息 seq 最小的 actor，invoke 一次
  一轮无人 invoke → 静止；serve 模式则轮询收件箱后继续
预算（max_steps）是宿主的事
```

actor 跨调用的持久记忆是账本（`call("0", …)`）；fn 里存的东西是易失记忆（Σ），活不过重启。

### 2.4 不变量

| | 不变量 | 靠什么 |
|---|---|---|
| I1 | 每本账单写者，seq 严格递增 | 运行时进程是唯一写者；外来消息只经收件箱 |
| I2 | actor 的每次调用都在它的账本上（step），每次 call 都是两行 msg（请求、返回），每个放入/退役都是一行 | 转移定义 |
| I3 | `A = fold(place / retire 行)`：place 行带完整 text、bind、tag、iface | place 行的定义 |
| I3a | 活跃成员的 tag 在各自 channel 内唯一；未指定时以 `t` 为基名，冲突由 R 原子分配 `t1/t2/…`；数字 addr 只用于 H 内部 | `Runtime._tag`；T21/T30 |
| I4 | 内容盲：运行时源码无组织词汇；G 全部改名后账本同构 | T6 |
| I5 | 确定性：给定账本 + 收件箱内容 + 各 fn 在 call 边界上的行为（返回值、call 序列），追加的行序列唯一 | 调度只依赖账本状态 |
| I6 | 膜（2026-09-01 定）：进入本 channel 账本的只有 call；改变形态的只有 syscall（bind）；出去的边只有门，进来的只有收件箱 → 接待员（接收侧只认合法句柄）。actor 在 run 里面对世界的访问（文件、网络、random）不是机器的动作：不入账、不遗传、不受限——限制它是安全，不在模型里。强版本（内外作用都走注入的 handle）做得到但刻意：它把 R 做成 OS，且 handle 的返回值会穿过 call 边界把模型原文拉回 H。**接收侧句柄本身是工程、演示模型不实现**：因此本原型里任意 actor 能伪造 placed 使假形态遗传（H17），"只有一条进入路径"是理论句而非实现保证 | dispatch / inbox 定义；Port 的句柄 |

### 2.5 运行时明确不做的

不删、不改已有行、不检查权限（只看 bind 标志）、不路由、不重试、不认识时间、不读 G、不认识任何名字、不隔离、不限时。

---

## 3. 洞（未定义或定义与理论不一致的地方）

按严重程度排。**A** = 理论层要定；**B** = 设计层要定；**C** = 工程。

**H1 · A · G 怎么进 c0。** 理论说"经门传输一个配置文本"。草稿里 realize 收到的是 `realize G.json`，然后**从文件系统读 G**——这是一个未声明的能力（actor 只该看到视图）。应定为：**踢的那一脚正文就是 G**（`realize\n<G JSON>`），realize 不碰文件。P 里的 `G.json` 只给 init 用（放第一个 actor）和给 pack 抄（B）。

**H2 · A · 根门与 G 原样的矛盾。** 理论：根门在 G 里，对面是创造者。但 G 逐字遗传，子代 G 里根门的 text 不可能是父代的地址；创造者是每个个体不同的。草稿里根门 text 是常量 "creator"，从不匹配，所有外来消息都署名字面量 `door`。三个选项：(a) 根门不在 G 里，由 init 放，text = 第一条收件箱行的 from；(b) 保留 G 里的根门，承认它是"占位"，外来消息署名 `door`；(c) 根门是运行时概念，`door` 这个字面量就是它。倾向 (a)：根门是出生证明，不是基因。

**H3 · A · 谁接外来请求。**（已关，M2：接待员把 `spawn` 转给持有 spawn 绑定的成员，`decl` 转给登记员） 外来消息只到接待员。`spawn` 请求发给 C actor，所以外面（人、父代）没法叫 dalek0 生孩子——草稿里只能由 c0 内部的 actor 转发。应定：**c0 的接待员是一个分发 actor**（按动词把 add/peer/realize 留给装配器、spawn 交给 C），或者装配器自己转发 spawn。二选一。

**H4 · A · pack 抄什么。**（已关，M2：C 经接待员向登记员要 `decl`，pack 抄它） 理论：`pack(c1.decl())`。M1 没有 c1，草稿抄 P 里的 `G.json`。这意味着今天的 dalek0 **不能遗传运行中的形态改动**（add 出来的 actor 不进子代）。明天 c1 落地前，M1 的复制只对"未改动过的机器"成立。要在验收里写明。

**H5 · B · pack 的能力。** 关闭（M3.4，2026-08-31；膜按窄版定后确认关闭，I6，2026-09-01）：不存在"未声明的 Store 能力"。程序的能力 = 解释器（Ω 的 Exec = python3）的能力，`shutil` / `open` / `os` 都是它的；bind 门控的只有介质动作（syscall / spawn / stop），不门控宿主。

**H6 · B · 收件箱偏移是进程内存。** 关闭（M3.2）：收进来的行带 `at`（该收件箱行之后的字节偏移），R 折叠账本时把各收件箱的偏移折出来；重起不重收。

**H7 · B · 程序 actor 的沙箱。** 关闭（M3.4，2026-08-31；同 H5，I6 定后确认）：前提错了——"actor 只有 stdin/stdout"不是理论，text 是 python，在 run 里能做的就是 python 能做的（同 H5）；对机器的作用仍只有 call（I6）。cwd = P 是 C 用文件系统 pack 的工程约定，留着。隔离不要（§6）。

**H8 · B · 消息给不存在的地址。** 现在静默丢弃。定为规则（已写入 1.1），并考虑给发送者一条 `from=runtime` 的错误消息——但那会引入运行时开口说话，倾向不加。

**H9 · B · L 的端点。** 关闭（M3.0，2026-08-30）：text 第一行 `<url> <model> <key>`，其余提示语；R 调 `Port.request(text, 视图)`，Port 讲 Anthropic messages 报文；失败记 err。见 DALEK 1.7（oracle 与门的区分）与 4.8。M3.4：`Port.request` 删除——端点、报文、组装、帧都在 L 的 text（`actors/l.py`）里，R 与 Ω 没有 LLM 专用的东西。

**H10 · B · 崩溃语义。** 大半已关（M4，2026-09-01）：actor 失败 → err 入账、机器不死（T16）；**重启入账**——`up`/`down` 行，硬杀 = 有 up 没 down，pending 重跑（T25）；本地损伤照登记处重建（T26）。剩：R 在一次事件中间崩溃（行写了一半，动作执行了一半）——未定。

**H11 · B · 运行时的"返回消息"是伪发送者。** `from ∈ {place, spawn}` 不是地址。要写进 ABI：视图里的 from 可能是介质词。

**H12 · C · `_order` 文件。** channel 创建顺序在账本之外。可接受（它是调度用的元数据），但要列为状态。

**H13 · C · 活性。** 两个互相回复的 actor 永不静止；预算是宿主的。已写明。

**H14 · C · 全局序。** 每 channel 一本账，跨 channel 只有门抄写给出的因果序，没有全局时钟。重演的确定性靠调度定义（2.3），不靠时间。

---

## 4. c0 的构造流程

两个方向：c0 **被构造**（出生）和 c0 **构造**（realize、spawn）。全部写成账本行。约定：G 是 dalek0 的（c0 里 realize、spawn、根门三个成员）；`→` 表示追加一行。

### 4.1 出生（创造者 = 人）

```
Ω.run(init, P)
  init 读 G.json，place(c0, program, <realize>, bind=[place], in)
                                          c0#1  place addr=1 program realize bind=[place] in
  运行时驱动：无 pending → 静止（serve 则轮询）

人：Port.send(file:P#c0, {from: human, body: "realize\n<G>"})      （按 H1 改后；草稿是 "realize G.json"）
  inbox → c0#2  msg  door → 1  "realize\n<G>"
  step(c0, 1)：Exec.run(realize, view=[#2])
     out = ">>> place c0 program bind=place,spawn\n<spawn 源码>\n>>> place c0 door\ncreator\n>>> 1\nnote door"
           → c0#3  step actor=1 upto=2 out=…
     execute 依次：
           → c0#4  place addr=2 program spawn bind=[place,spawn]
           → c0#5  msg  place → 1  "c0/2"
           → c0#6  place addr=3 door creator
           → c0#7  msg  place → 1  "c0/3"
           → c0#8  msg  1 → 1  "note door"
  step(c0, 1)：view=[#5, #7, #8]
     out = ">>> door\nplaced c0/2\nplaced c0/3"   → "door" 不是地址，无效果
           → c0#9  step actor=1 upto=8
  静止。c0 = {1: realize(接待员), 2: spawn, 3: 根门}
```

G 有第二个 channel x（一个 program，peers [[c0, x]]）时，第 3 步多出：

```
  → x#1   place addr=1 program <text> in
  → c0#…  msg place → 1 "x/1"
  → c0#…  place addr=4 door x          （c0 → x）
  → x#2   place addr=2 door c0         （x → c0）
```

之后 c0 的 1 写给 4 的消息经门到 x#… `msg 2 → 1`（署名 x 里指回 c0 的门），x 的回信原路回到 c0 的接待员。

### 4.2 realize 的一般规则

```
realize(G):
  for c in G.channels: for i, m in enumerate(c.members):
      if (c, i) 是我自己（第一个 channel 的第一个成员且我在那个位置）: skip
      place(c.name, m.kind, m.text, in = (i+1 == c.receptionist), bind = m.bind)
  for (a, b) in G.peers: place(a, door, b); place(b, door, a)
  note 请求者
下一步：把所有 from=place 的返回转发给便签里的请求者（当前 ABI 为 `placed <channel>/<tag>`）
```

realize 不读文件、不记状态；它的记忆是写给自己的便签。add / peer 是同一规则的单步版。

### 4.3 生子（创造者 = dalek0 的 C actor）

```
某 actor r（c0 内；按 H3 改后可以是外来请求经分发）：
  → c0#a  msg  r → 2  "spawn d1"
  step(c0, 2)：spawn actor
     pack：把 omega.py runtime.py init.py（世界）和 G.json（描述，原样；H4）抄进 spawn/d1/
     out = ">>> spawn spawn/d1\n>>> place c0 door\nfile:<P>/spawn/d1#c0\n>>> 2\nnote r <dir>"
           → c0#b  step actor=2
           → 介质：Exec.spawn(init, spawn/d1)   → 子代进程起来
           → c0#c  msg  spawn → 2  "<dir> pid=N"
           → c0#d  place addr=4 door file:<dir>#c0
           → c0#e  msg  place → 2  "c0/4"
           → c0#f  msg  2 → 2  "note r <dir>"
  step(c0, 2)：view=[#c, #e, #f]
     out = ">>> 4\nrealize\n<G>\n>>> r\nspawned <dir> pid=N door=4"
           → c0#g  step actor=2
           → c0#h  msg  2 → 4  "realize\n<G>"          （踢那一脚）
           → c0#i  msg  2 → r  "spawned …"
  step(c0, 4)：门 → Port.send(file:<dir>#c0, {from: "file:<P>#c0", body: "realize\n<G>"})
           → c0#j  step actor=4
  父代义务结束。

子代进程（独立）：
  init：d1/c0#1 place addr=1 program realize in
  inbox：d1/c0#2 msg door → 1 "realize\n<G>"          （from 是 "door"：子代 G 里的根门 text 不匹配父代地址，H2）
  以下与 4.1 完全相同：d1/c0 = {1: realize, 2: spawn, 3: 根门}
  子代能再做 4.3。父代进程死掉不影响它（T7）。
```

### 4.4 流程里 G 出现的三个位置（与 DALEK.md 的三个身影对应）

| 位置 | 时刻 | 用法 |
|---|---|---|
| `spawn/d1/G.json` | pack | 抄，不读（B） |
| 踢那一脚的正文 | c0#h → d1/c0#2 | 传输，不读 |
| d1/c0 与其他 channel 的 place 行 | realize | 解释（A）；带完整 text，所以 `fold(place 行) = G` |

三者相等是验收条件。H4 成立前，第一项等于父代出生时的 G，不等于父代当前形态。

---

## 5. 创世与根门（2026-08-29 补，按用户表述；**已定为 M1 的实现版本**，取代 §1.1/§4 的 place 写法）

**词汇固定**：runtime 的 **syscall** 是两个词——`channel.create(name)`、`channel.add.actor(channel, kind, text, bind[, in, tag]) → channel/effective-tag`（含 actor.create：actor 只在被加入 channel 时诞生）。R 用自己从 H 折出的活跃路由表保证 tag 唯一，重名原子分配 `tag1/tag2/…`；数字 addr 只写 place 行。`place` = 后者落账的行。c0 的 `add / build / spawn` 叫**请求**，是 syscall 上的程序。

**创世**（被父代 spawn 出来）：
1. Ω.run → 进程起来（`init.py` = R 的入口）；2. R 折叠已有账本（出生时为空）；3. 根门开着——不挂在任何 channel 上（还没有 channel），Space 级，门那边送来的每一行当 syscall 执行、记进目标账本。没有第 4 步：没有 boot。

**旧 boot（硬编码，已废）**：boot 自己经根门执行 `channel.create(c0)`、`actor.create(G.channels[0].members[0])`、`channel.add.actor(c0, 它, in)`，然后根门关闭构造 handler。→ 子代的 A 是世界放的，**H15 · A**。

**严谨版（已采用）**：R 起来根门就开着，不需要任何人打开；"boot"缩到零。父代的 A（realize）读 G 的第一个 channel，经根门发 syscall 造 c0：`channel.create c0`，每个成员 `channel.add.actor`（realize 带 in、C、……），最后放一扇指回父代的普通门（出生证明，不在 G 里；放在最后，成员地址才与 G 里的序号一致）。父代的 C 经根门发 `start\n<G>` → 根门关闭构造 handler → 子代的 realize 收到 G，本地 syscall 长出其余 channel 与连线（发育版，2026-08-30 定；之前的"父代造全部"撤回）→ 从此只收消息，形态改动只经子代自己的 c0。

推论：
- **关门的时刻 = start**。准静止有了操作定义：构造门开着时机器无消息；第一条消息一到门就关。"切离 = 什么都不用做"改为"切离 = 关构造门，由 start 顺带完成"。
- **根门在 channel 之前存在**，是 R 的一部分（Space 级），不是 G 的成员、不是 c0 的成员。放进 c0 的"指回父代的门"是另一样东西：出生证明，普通门。H2 消失。
- H1 消失：c0 经根门以 syscall 行进入，G 整体作为 `start` 的正文进入；realize 不读文件。


## 6. 原型采用的结构性保障（工程，不是理论；2026-08-30）

判据：**理论句 = 换一种实现仍然成立的句子。** 下面这些换实现可以不要，靠纪律也能满足同一目的；原型里保留它们是为了让测试可判定。

| 保障 | 在哪 | 对应的理论句 |
|---|---|---|
| R 拒绝 actor 退役自己（同 channel 同地址） | `runtime.retire` | 器官不登记自己的死亡（升级纪律） |
| R 拒绝退役当前接待员 | `runtime.retire` | 接待员先换再退 |
| syscall 的回执就是回复（同一次运行），失败回 `refused` | `runtime._dispatch` | 介质约定（ABI）：请求有回复 |
| 登记员只认来自本机门（含退役）的 born/placed/retired | `registrar.py` | 登记处记 c0 的宣称；信任本机 |
| 交出的门行带 `local`；登记员 = 第一扇 local 门那边 | `runtime._annot` / `realize.py` | c0 与 c1 有一条连线（G 的第一条连线） |
| L 组装时带成员表（工具列表） | `actors/l.py` | 运行中能请求的地址 = 它对机器的作用面；程序靠角色不需要表 |
| 0 的回复是全量行；L 组装读 1..当前 | `runtime._dispatch` / `actors/l.py` | 读账本是介质的地址 0，对全部成员开放；oracle 的读在转移行里（理论）。全量还是句柄、读多长是工程 |
| actor 是常驻函数：放入时 `exec` 一次，globals 里能存东西，不拦 | `runtime._instantiate` | H 记 call 边界，内部状态不入账；实例化是 H 中的事件（重启入账归 M4）。想活过重启的东西自己从账本重建 |
| 程序在 R 进程内跑：没有隔离、没有超时；L 最多 16 轮；L 的模型原文不入账 | `omega.Exec.load` / `actors/l.py` | Ω 是契约边界不是进程边界；隔离与上限是工程（不要）。oracle 是完整 actor，端点回话在它的 run 里面（DALEK 1.4） |
| 帧语法是 L 的 text 的事（`>>> 地址` 起、单独一行 `<<<` 收）；R 记 call 用同一格式写 step.out | `actors/l.py` / `runtime.parse` | LLM 要能拼写 call；用什么记号是 L 的 text 的工程，R 不认识它 |
| 程序的 cwd = P（每次事件 `os.chdir`） | `runtime._invoke` | C 用文件系统 pack（H5/H7）；工程 |
| tag 分配与解析：channel 内活跃 tag 唯一；重名由 R 分配后缀 | `runtime._tag` / `_resolve` | `channel/tag` 是组织层地址；数字 addr 只属于 H |
| 门的 local 在交出账本时计算 | `runtime._annot` | 指向本机 channel 是此刻的事实；channel 只增所以单调 |
| place 的数字 addr 只增不复用；组织协议按 tag | ABI | `channel/tag` 属于形态；数字 addr 是 H 内部位置，actor 不得硬编码 |
| 收件箱是文件，人人可 append；text 拿着 urllib / open 也能绕过门写进别的机器 | `omega.Port.send/recv` | 膜的接收侧：收件箱只认合法句柄写入，句柄只在门那里——那时"穿过介质的只有 call"是 Port 的性质，不是约定。文件收件箱是 M1 的工程简化（2026-08-31）；其后果 = 可伪造 placed 遗传假形态（H17），真实系统靠句柄解决、演示模型靠约定 |
| 首 channel 必须显式接待员（genesis / build 拒绝） | `genesis.py` / `realize.py` | 出生需要一个入口（理论合法性条件）；拒绝是保障 |
| 一个 P 只有一个活 R：`--serve` 启动抢 `P/lock` 的 flock，抢不到即退出 | `init.lock` | 单写者 I1 是理论前提；flock 是工程兜底（alive→spawn 无操作、dormant→spawn 唤醒）。H18 |

**H16 · C · c0 内部经合法门说假形态事实。** 原型不防：π(A) ≅ G 是 c0 维护的。工程方案：`bind=registry` 的媒介能力——R 每落一条 place/retire 行就把该行原样投给持有它的 actor（像 syscall 回执一样 `from=place`），登记事实的来源变成 R，伪造路径消失，三段记账变回同时；R 仍内容盲。代价：c1 不再是"只经门喂出来的"。

**H18 · C · 同一个 P 起两个 R，双写破坏单写者 I1（工程洞，2026-09-01；codex review #3）。** `spawn <P>` 无条件起进程，没查目标是否已活。一台机器活着时再 spawn 同一个 P（reporter 失联误判、或两个 peer 同时判死各 spawn 一次），两个 R 各自把 `c.seq` 折在内存里、各自 `c.seq+1` append，**序号相撞**（实测账本 32 行去重剩 20，`_pending`/游标/折叠全乱），账本永久不可折叠——而账本是唯一真相。**已加最小工程保障**（`init.lock`）：`--serve` 的 R 进程启动即对 `P/lock` 抢 `flock(LOCK_EX|LOCK_NB)`，抢不到 = 已有活 R → `exit 3`，load/wake 之前退出，一字节不写。flock 内核在进程死亡时自动释放，正好定义 Space 五态：alive→spawn 拿不到锁 = 无操作，dormant→spawn 拿得到 = 唤醒。这是**工程保障**（DESIGN §6 类，非理论）：真实系统本就该做单实例/存活检查，演示模型用一个 P 本地锁兜住；理论层"一个 P 一个 R"是 I1 的前提，不靠锁成立。进程内 `up()`（测试、膜外读）不抢锁，避免误伤。

**H17 · C · 任意 actor 伪造 placed，绕过 syscall 使假形态遗传（工程洞，2026-09-01；codex review）。** 收件箱是明文文件、`Port.send` 对谁都开，所以一个普通 program 能在 run 里 `open("in/c1.jsonl","a")` 写一行署名 `file:<P>#c0` 的 `placed …`，登记员采信 → 新 channel/成员进 `decl` 并遗传——**新能力不经 door→c0→syscall→R 就进了基因组**。比 H16 更宽：H16 只是 c0 自己作恶，H17 是任何 actor 都能走。**理论层不破**：模型里 `Port` 接收侧有写句柄，text 拿不到别人的句柄，"只有一条进入路径"成立；破的是**当前实现见证这条膜**——M1 用明文文件收件箱（同 §6 那行、I6 的"接收侧只认合法句柄"未实现）。**真实系统必须解决（句柄/能力，Atoll 已在传输层焊死）；演示模型不做——复杂度对最小理论模型太高，靠约定：不伪造收件箱。** 所以"只有一条进入路径"是**理论句**，不是本原型的实现保证。

**测试里的 π**：`t/test_c0.py` 的 `form_of` 按出处投影（`by`：脐带放的不指向本机的门 = 出生证明；c0 里持 spawn 绑定的成员放的门 = 生子的临时门；退役），与 DALEK 0.0 的定义一致，realize 放的外部门保留。

**其他工程记录（2026-08-30 晚）**
- 人不单独建模：对机器来说人就是一个 L（门那边的一个作者/端点）。genesis 里 creator 用字面量 `human` 只是原型省事；正确做法是一个真实端点，否则写给出生证明门的消息会在 P 里生出 `in/human.jsonl`（无功能影响）。
- 人经 c0 收件箱问 `decl`，回答按 realize 的规则转给 C 而不是提问者；要 decl 直接问 c1。
- `Exec.spawn` 的日志句柄父进程已关闭（曾泄漏）。
- 生子测试（T7/T9/T10）靠 sleep 与 20 s 上限等子代，慢机器可能假失败。
- 出生后再收到 `start` 忽略（有内门 = 已发育），registrar 只认第一个 `born`（T17）。
- U 在进程内 exec L 写的代码，不隔离；L 每次调用读整本账，长寿命要一个折叠器官（S，上下文策略在它的源码里）；L 输出不合语法时的清洗程序是退路，未做。
- L 的一步是同步的（最长 120 s），期间调度器不走别的 channel——单线程轮转的代价；要并发得让 R 按 channel 分线程或把 request 做成异步的门，M4 以后。
- 外面直接写收件箱而 channel 里没有指回去的门时，署名是 `door`，回话写给 `door` 会被丢（H8）：任务发起者要收到 `done`，先给自己在 c2 放一扇门（README 的跑法）。测试从 step.out 读。
- `actors/l.py` 把 `stop_reason == max_tokens` 当失败抛出（截断的代码不能当动作）。
