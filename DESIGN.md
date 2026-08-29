# M1 设计：syscall 闭集、运行时定义、洞、c0 的构造流程

理论在 DALEK.md。本文回答四个问题，供审阅；`runtime.py` 等是按第一版直觉写的草稿，与本文不一致处以本文为准，第 3 节逐条列出。

---

## 1. syscall 的闭集

先分两层，两层都要闭：

### 1.1 介质动作（运行时提供，转移表里的行）

| 动作 | 谁能发 | 效果 |
|---|---|---|
| **msg** `>>> <addr>` + 正文 | 任何 program / oracle actor | 在本 channel 账本追加一条 `msg(from=我, to=addr)`。addr 不存在 → 丢弃（只留在 step.out） |
| **place** `>>> place <channel> <kind> [in] [bind=…]` + text | 持有 `bind=place` 的 actor | 在 `<channel>` 账本追加一条 `place`（带完整 text）；channel 不存在则这就是它的第一行；给调用者追加 `msg(from=place, body="<channel>/<addr>")` |
| **spawn** `>>> spawn <dir>` | 持有 `bind=spawn` 的 actor | `Exec.spawn(init, dir)`；给调用者追加 `msg(from=spawn, body="<dir> pid=<n>")` |

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
- delete / retire —— 需要 c1 的注册表（逻辑删除 = 表上划掉）。M1 没有。
- rebuild / rebind / clone —— 管理器动作，都是 spawn 与 add 的组合，属于 c1/c2 的程序，不是 syscall。
- 改接待员 —— `add … in` 会把新 actor 设为接待员（fold 规则），所以可以改，但没有"只改接待员不加 actor"的动作。这是有意的：接待员是某个 actor 的属性，不是独立状态。

---

## 2. 运行时的完整定义

### 2.1 状态

```
S = (order, {name → Ledger}, {name → inbox_offset})
Ledger = 行的序列，seq 从 1 严格递增，单写者（本机器的运行时进程）
```

行（三种，字段闭集）：

```
place  {seq, k:"place", addr, kind ∈ {program, oracle, door}, text, bind ⊆ {place, spawn}, in: bool}
msg    {seq, k:"msg",   from, to, body}      from ∈ 地址 ∪ {door, place, spawn}
step   {seq, k:"step",  actor, upto, out, err}
```

派生量（全部由行折叠得到，无别的状态）：

```
actors(c)        = 按 seq 折叠 place 行：addr → (kind, text, bind)；addr = 该 channel 第 n 条 place 的 n
receptionist(c)  = 最后一条 in=true 的 place 的 addr；若无，第一条 place 的 addr
cursor(c, a)     = 该 actor 最后一条 step 的 upto；无则 0
pending(c, a)    = {msg 行 : to = a, seq > cursor(c, a)}
door_to(c, x)    = c 里 text = x 的 door 的 addr（可无）
```

`order`（channel 创建顺序）和 `inbox_offset` 不在账本里——见第 3 节 H1、H12。

### 2.2 事件与转移

事件 = `pending(c, a) ≠ ∅`。转移 `step(c, a)`，令 `msgs = pending(c, a)`，`upto = max seq`：

```
door:     对每条 m: deliver(text(a), m.body)；追加 step(a, upto, "", "")
program:  (out, err) = Exec.run(text(a), stdin = view)；追加 step(a, upto, out, err)；
          对 parse(out) 的每条 (head, body) 依次 execute
oracle:   同 program，但 out 来自 Ω 侧端点（M1 未定义端点，见 H9）
```

```
view               = {"channel": c, "me": a, "msgs": msgs 的 (seq, from, to, body)}
parse(out)         = 以行首 ">>> " 切分；第一段之前的文本忽略
execute(head, body):
  head = "place ch kind [in] [bind=x,y]" ∧ place ∈ bind(a)  → place 动作
  head = "spawn dir"                    ∧ spawn ∈ bind(a)  → spawn 动作
  head ∈ actors(c)                                        → 追加 msg(from=a, to=head, body)
  其他                                                    → 无效果
deliver(target, body):
  target ∈ channels ∧ receptionist(target) 存在 → 在 target 追加 msg(from = door_to(target, c) ∨ "door", to = receptionist(target), body)
  target ∉ channels                             → Port.send(target, {from: "file:<P>#<c>", body})
  否则                                          → 无效果
inbox(c):  对 in/<c>.jsonl 新行 {from, body}：receptionist(c) 存在时追加 msg(from = door_to(c, from) ∨ "door", to = receptionist(c), body)
```

### 2.3 调度

```
loop:
  inbox 全部 channel
  对每个 channel（按 order）：取 pending 非空且最早未读 seq 最小的 actor，step 一次
  一轮无人 step → 静止；serve 模式则轮询收件箱后继续
预算（max_steps）是宿主的事
```

一个 step 内追加的行（便签、介质返回）seq > upto，因此出现在该 actor 的下一次视图里。这是 actor 唯一的跨步记忆。

### 2.4 不变量

| | 不变量 | 靠什么 |
|---|---|---|
| I1 | 每本账单写者，seq 严格递增 | 运行时进程是唯一写者；外来消息只经收件箱 |
| I2 | actor 的每次动作都在它的账本上（step），每个效果都在目标账本上（msg / place） | 转移定义 |
| I3 | `G = fold(place 行)`：place 行带完整 text | place 行的定义 |
| I4 | 内容盲：运行时源码无组织词汇；G 全部改名后账本同构 | T6 |
| I5 | 确定性：给定账本 + 收件箱内容 + actor 的输出，追加的行序列唯一 | 调度只依赖账本状态 |
| I6 | 膜：actor 只能写本 channel 的地址；出去只经门；进来只经收件箱 → 接待员 | execute / inbox 定义 |

### 2.5 运行时明确不做的

不删、不改已有行、不检查权限（只看 bind 标志）、不路由、不重试、不认识时间、不读 G、不认识任何名字。

---

## 3. 洞（未定义或定义与理论不一致的地方）

按严重程度排。**A** = 理论层要定；**B** = 设计层要定；**C** = 工程。

**H1 · A · G 怎么进 c0。** 理论说"经门传输一个配置文本"。草稿里 realize 收到的是 `realize G.json`，然后**从文件系统读 G**——这是一个未声明的能力（actor 只该看到视图）。应定为：**踢的那一脚正文就是 G**（`realize\n<G JSON>`），realize 不碰文件。P 里的 `G.json` 只给 init 用（放第一个 actor）和给 pack 抄（B）。

**H2 · A · 根门与 G 原样的矛盾。** 理论：根门在 G 里，对面是创造者。但 G 逐字遗传，子代 G 里根门的 text 不可能是父代的地址；创造者是每个个体不同的。草稿里根门 text 是常量 "creator"，从不匹配，所有外来消息都署名字面量 `door`。三个选项：(a) 根门不在 G 里，由 init 放，text = 第一条收件箱行的 from；(b) 保留 G 里的根门，承认它是"占位"，外来消息署名 `door`；(c) 根门是运行时概念，`door` 这个字面量就是它。倾向 (a)：根门是出生证明，不是基因。

**H3 · A · 谁接外来请求。** 外来消息只到接待员。`spawn` 请求发给 C actor，所以外面（人、父代）没法叫 dalek0 生孩子——草稿里只能由 c0 内部的 actor 转发。应定：**c0 的接待员是一个分发 actor**（按动词把 add/peer/realize 留给装配器、spawn 交给 C），或者装配器自己转发 spawn。二选一。

**H4 · A · pack 抄什么。** 理论：`pack(c1.decl())`。M1 没有 c1，草稿抄 P 里的 `G.json`。这意味着今天的 dalek0 **不能遗传运行中的形态改动**（add 出来的 actor 不进子代）。明天 c1 落地前，M1 的复制只对"未改动过的机器"成立。要在验收里写明。

**H5 · B · pack 的能力。** C actor 用 `shutil` 抄文件——未声明的 Store 能力。bind 集合应扩到 Ω 的句柄：`store`（读写 P 与子目录）、`port`。运行时把句柄内容盲地交给 actor（怎么交是工程：环境变量、参数）。

**H6 · B · 收件箱偏移是进程内存。** 崩溃重起后收件箱行会被重新收进账本（重复消息）。定法：把"收到收件箱第 n 行"作为一种账本行记下（`k: "in", offset`），或收件箱行带 id 去重。

**H7 · B · 程序 actor 的沙箱。** `Exec.run` 的 cwd 是 P，actor 能读写任何文件（账本、G）。理论上 actor 只有 stdin/stdout。定法：cwd 是空的临时目录；文件访问只经 `store` 句柄。

**H8 · B · 消息给不存在的地址。** 现在静默丢弃。定为规则（已写入 1.1），并考虑给发送者一条 `from=runtime` 的错误消息——但那会引入运行时开口说话，倾向不加。

**H9 · B · oracle 的端点。** kind=oracle 的 text 是什么（模型 + 提示语？URL？），运行时怎么调（Port.request 的报文）。M1 未定义；明天随 L 定。

**H10 · B · 崩溃语义。** 程序 actor 非零退出：草稿照样解析 stdout，err 记入 step。定为：**退出码非零 → 输出视为空**，err 记录，不重试。

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
下一步：把所有 from=place 的返回转发给便签里的请求者（"placed <channel>/<addr>"）
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

## 5. 创世、根门与 boot（2026-08-29 补，按用户表述；**已定为 M1 的实现版本**，取代 §1.1/§4 的 place 写法）

**词汇固定**：runtime 的 **syscall** 是三个词——`channel.create(name)`、`actor.create(kind, text, bind) → actor`、`channel.add.actor(channel, actor[, in]) → addr`。`place` = 三者合成。c0 的 `add / build / spawn` 叫**请求**，是 syscall 上的程序。

**创世**（被父代 spawn 出来）：
1. Ω.run → 进程起来；2. 加载 runtime；3. 开一扇**根门**——不挂在任何 channel 上（还没有 channel），Space 级，handler = 三个 syscall：门那边送来的每一行当 syscall 执行、记进目标账本；4. 加载 boot。

**旧 boot（硬编码，已废）**：boot 自己经根门执行 `channel.create(c0)`、`actor.create(G.channels[0].members[0])`、`channel.add.actor(c0, 它, in)`，然后根门关闭构造 handler。→ 子代的 A 是世界放的，**H15 · A**。

**严谨版（已采用）**：boot 只做一件事——把根门的 syscall handler 打开交给父代，做完后关闭。父代的 A（realize）读 G，经根门逐条发 syscall：先放一扇指回父代的普通门（出生证明，不在 G 里），再放 realize（in）、C、以及 G 里其余全部。父代的 C 经根门发 `start` → 根门关闭构造 handler → 从此只收消息，形态改动只经子代自己的 c0。

推论：
- **关门的时刻 = start**。准静止有了操作定义：构造门开着时机器无消息；第一条消息一到门就关。"切离 = 什么都不用做"改为"切离 = 关构造门，由 start 顺带完成"。
- **根门在 channel 之前存在**，是 R 的一部分（Space 级），不是 G 的成员、不是 c0 的成员。放进 c0 的"指回父代的门"是另一样东西：出生证明，普通门。H2 消失。
- H1 消失：G 经根门以 syscall 行进入，子代账本前 n 行就是 G；realize 不读文件。
