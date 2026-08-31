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
| **world 动词** `>>> spawn <dir>` / `>>> stop`（无参） | 持有 `bind=<动词>` 的 actor | 一张表，用 Ω 实现：`spawn` 按 loader 协议起一台机器（`Exec.spawn(init, dir)`）；`stop` 停**本 Space**——只置意向，根边界的 down 由主循环在两个事件之间写。给调用者追加 `msg(from=<动词>, body=…)`。Ω 不再提供"杀某个 pid"：物理强杀是膜外的事，不是机器的能力 |

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

行（五种，字段闭集）：

```
place  {seq, k:"place", addr, kind ∈ {program, door}, text, bind ⊆ {syscall, spawn, stop}, in: bool, by, tag, iface?, at?}
retire {seq, k:"retire", addr}
msg    {seq, k:"msg",   from, to, body, run?, at?, by?}     from ∈ 地址 ∪ {0, door, channel.*, spawn, stop}
step   {seq, k:"step",  actor, upto, out, err, run?}
down   {seq, k:"down"}                                      关闭的完成标记：不是消息，没有收信人（死后无人可叫醒）
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

`fn` 是常驻函数；它里面的东西（globals、对话、中间值）是易失运行状态 Σ，**不入账**：H 记的是 call 边界（2.2）。活着的机器是 (G, H, Σ)，个体是 (G, H)；Σ 是工程（DALEK 1.7）。已出生机器的重启 = 重新折叠 = 重新实例化；H 只在生命周期边界（G 的首 channel）记一条 incarnation 事件，各 actor 的重新实例化由该事件 + 各自有效 place/retire 派生，不在每个 channel 复制 up（M4，H10）。`order` 不在账本里，它是**介质的引导索引**（哪些账本、按什么次序读），不是个体状态——见 H12。

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
| I6 | 膜（2026-09-01 定）：进入本 channel 账本的只有 call（介质自己的两笔除外：醒来的 up 与关闭的 down，都只落根边界）；改变形态的只有 syscall（bind）；出去的边只有门，进来的只有收件箱 → 接待员（接收侧只认合法句柄）。actor 在 run 里面对世界的访问（文件、网络、random）不是机器的动作：不入账、不遗传、不受限——限制它是安全，不在模型里。强版本（内外作用都走注入的 handle）做得到但刻意：它把 R 做成 OS，且 handle 的返回值会穿过 call 边界把模型原文拉回 H。**接收侧句柄本身是工程、演示模型不实现**：因此本原型里任意 actor 能伪造 placed 使假形态遗传（H17），"只有一条进入路径"是理论句而非实现保证 | dispatch / inbox 定义；Port 的句柄 |

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

**H10 · B · 崩溃语义。** 大半已关（M4，2026-09-01）：actor 失败 → err 入账、机器不死（T16）；**重启入账**收紧为一条机器级边界行——已出生时只在 `_order` 的首 channel 记 `_root→接待员 up/down`，其他 channel 永远不受 R 的物理广播。硬杀 = 根 channel 最后的开启边界（start 或 up）没有匹配 down；pending 重跑（T25/T28）。A 把 up 翻译为给 c1 的 `reconcile`，本地损伤照登记处重建（T26）。T31 证明无接待员 channel 的 actor 仍被重新实例化、Σ 归零，而其账本无 up/down：重新实例化是根事件与 place/retire 的派生事实，不是逐 actor 行。**剩项已关（2026-08-31 定，用户）——入账即历史，中途的一律丢弃。** 三句取代原来的"幂等或无害"前提：
  1. **入账即已发生。** 一行落进账本就是历史，效应已经产生，不可撤销、不待确认；没有"暂定"的行。这个谓词由 Ω 契约给定而非约定：`Store.append` 是单行 append + flush + fsync，声明为原子。
  2. **中途的东西直接丢弃。** 事件跑到一半崩溃：未落账的一切（Σ、局部变量、没写完的行）不属于机器。**"行写了一半"不由 R 处理**——Ω 已声明单行 append 原子，撕裂的行是宿主契约被破坏，与 H12/H20 同类（理论模型不为自己的 H 被损伤负责），不是机器状态。原句"坏行截断"撤回。
  3. **重放与否是设计项，不是理论义务。** 于是那条隐含前提**不再是理论的负担**：不重放就不需要幂等。它降级为"选择重放的一侧自己付的价"，不再是每个新动词的验收条件。

  **为什么这样就够——半成品不是正确性的洞，是损伤。** 事件中途崩溃确实会留下已落账的部分效应（`_dispatch` 里 syscall 当场执行、place/msg 行当场落账，外层 step 行到 `_invoke` 末尾才写）。按第 1 条这些效应是历史、确实发生了，机器于是处在"形态被改了一半"的状态——而**这正是 D 的日常工作**（组织完整性不由 R 保证，由 D 维护；DALEK 2.1、H19）。所以 R 不需要事务、不需要两阶段提交、不需要为半成品兜底：它只保证账本是真实历史，修复归器官。这也是幂等前提能被**删掉**而不是被证明的原因。

  **两个设计轴（本原型的选择，都可另选）**

  | 轴 | 两端 | 本原型 | 代价 |
  |---|---|---|---|
  | 未完成的事件 | 重放 / 丢弃 | **重放**（at-least-once：cursor 只由 step 行推进，`_pending` 会再取一次） | 动作幂等参差（下），且有**毒消息坑** |
  | 恢复什么 | 恢复状态 / 恢复能力 | **恢复能力**（Σ 归零、重新实例化；想活过重启的 actor 自己读账本重建——DALEK 1.7、T31） | 易失现场不保证；换来重启语义不依赖重放 |

  **实测：旧前提不只是没证，是假的（2026-08-31 查码）。** 逐动词看重放：`channel.create` 幂等（回 `exists`）、`retire` 幂等（第二次找不到活成员，回 `refused`）、`spawn` 幂等（flock，H18）、`stop` 只置意向——但 **`channel.add.actor` 不幂等**：`add` 无条件写新 place 行，`_tag` 见重名就分配 `X1`，于是重放产出**第二个器官** `X1`。窗口不小：syscall 在 `_dispatch` 里当场执行并落账，外层 step 行要等 actor 的 `run` 全部结束才写——L 装完零件还要跟端点说几十秒话，这段全在窗口里。后果是 A 有两个而 G 只记一个（c1 在崩溃前没收到 `placed`），**π(A) ≅ G 漂移**。
  这恰好说明为什么该**删**前提而不是证它。而且后果不需要新机制接住：多出来的器官就是损伤，把 π(A) 拉回 G 正是自维护的定义（DALEK 0.0、M4）。原句"`place`/`retire` 与行同写因而原子"混淆了原子与幂等——原子成立（效应就是那一行），幂等只对 `retire` 成立。

  **毒消息坑（用户点名，2026-08-31）：纯回放逃不掉崩溃循环，而且一个坏点坏全部。**
  - **范围先划清**：普通 actor 异常**不**进这个坑——`_invoke` 接住它，err 入账、step 行照写、游标照推（T16）。进坑的是**能杀掉 R 的**消息（OOM 被内核 SIGKILL、段错误、actor 里的 `os._exit`）：step 行没写 ⇒ 游标没推 ⇒ 醒来它仍是本 channel 最低的未办 seq ⇒ 再杀一次。
  - **坏全部**：一台 Space 一个进程一个 R，所以毒消息冻住的是**整台机器**，不只它自己的 channel。
  - **D 修不了它——这是"完整性由 D 维护"的第二条边界条件**（第一条是 c0 接待员，见 H19）。D 修损伤的前提是机器跑得起来，而毒消息在任何器官动之前就杀掉了 R；E2-c 那条邻居唤醒的路也救不了：peer 判死 → spawn → 再崩 → 判死，变成无限重生循环。两条边界条件都是活性条件——一条是**手够不着**（接待员断），一条是**身体醒不来**（毒消息）。
  - **收窄一句**：困住回放的不是回放，是**失败没在 H 上留痕**——没写 step 行 ⇒ H 逐字不变 ⇒ 下一次决定相同 ⇒ 同样的崩溃。是确定性困住了它。所以这不是死局：写前日志、重试计数、隔离/死信是现成办法，工程史上解决过无数次（§6 第二条判据）。**本模型不设计解法，只划边界**：毒消息 = 机器醒不来 = D 的前提不成立，归工程与宿主。原句"要治只能加策略、那是器官的事"撤回——器官在这个坑里没机会跑。

  **关机三层（2026-09-01 晚）**：意图是 policy；唯一合法路径是 `门/actor → A:stop → C:stop → C 调无参 stop 动词 → R 置意向 → 当前调用结束后**放弃剩下的事** → 在根边界记一行 `{k:"down"}` → 退出`（对象天然是本 Space，不需要 pid；停成员是 retire）。**放弃而不排空**是有意的：没写 step 行的消息下次醒来照样重跑（at-least-once 已经兜住），于是 down 不必表示"做完了"，只表示"这次关闭是自己走协议关的"——写在最后一刻，"有 down ⟺ 上次经协议干净关闭"才是双向命题（若边写边排空，崩在排空途中会留下 down 却不干净，判定就会给出错误答案）。down **不投递给任何人**：醒来必须是投递（机器静止，不叫醒就没有东西会动），停下不必（动作已经发生）；这个不对称由方向决定。想在死前道别是第一层的事：决定停机的器官在调 stop 之前自己说。外部 SIGTERM/SIGKILL 不是合法停止而是故障，不写 down。R 不再装 SIGTERM 处理器——它从前会在 `_append` 算完序号、`_fold` 之前重入，写出重复 seq（与 H18 同类的账本损坏，实测可复现）。判定：根边界最后一个开启边界没有配对的 down = 上次非协议终止（T28/T32）。

**H11 · B · 运行时的"返回消息"是伪发送者。** `from ∈ {place, spawn}` 不是地址。要写进 ABI：视图里的 from 可能是介质词。

**H12 · C · `_order` 文件（2026-09-01 定位：不是洞）。** `h/_order` 记 channel 的创建顺序，`load` 按它决定读哪些账本、`_boundary` 取它的首项。它**不是个体状态**，是**介质的引导索引**——分区表/超级块那一类：介质要起来必须先读的最小索引，位置由宿主保护，没了就起不来。所以：
  · **不进 (G, H)**：个体仍然是 (G, H)。同一份顺序机器自己也有——c1 的 `decl` 里 channel 的次序就是构造序（`t/test_c0.py:315` 断言 `decl` 顺序 == `_order`），它可遗传、可变异、损坏能重建；`_order` 只是介质自己的一份引导拷贝。
  · **不由 R 折**：曾考虑取消这个文件、让 R 从账本折出构造序（跟 `channel.create` 的回执链）。放弃——那是给 R 加结构知识。R 只负责起来；构造序与依赖关系是 c1 的记账内容，不是介质的知识。
  · **删掉它之后会怎样不在模型范围内**：实测删掉 `h/_order`（账本一字节不动）后重启，零 channel → 收件箱偏移一起丢 → 根收件箱从字节 0 重读 → 创世被重放一遍，账本出现重复 seq（46 行 23 个唯一 seq，与 H18 同类）。这与"删掉 `h/` 机器就没了"是同一件事：**理论模型不为自己的文件被删负责**，引导区的保护是宿主的事（Linux/Windows 早已如此）。记在这里只为说明它是引导数据，不是要在模型里兜。

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

**第二条判据（2026-08-31 定，用户）——工程史上解决过多次的问题，模型只划边界，不重解。** 崩溃恢复、至少一次投递、毒消息、写前日志、单实例、隔离，这些没有一个是新问题，也没有一个构成理论困难；它们对本模型**只是边界划在哪的问题**。所以处置一律是：把边界写清楚（哪些属于机器、哪些属于宿主/工程），指一句现成办法，**不在演示模型里设计解法**。为一小段边界去论证复杂情形的处理，是把工程债搬进理论。已按此处置的：H5/H7（沙箱）、H12（引导索引）、H17（收件箱句柄）、H18（单写者）、H20（H 被外科损伤）、H10（崩溃与重放）。

| 保障 | 在哪 | 对应的理论句 |
|---|---|---|
| R 拒绝 actor 退役自己（同 channel 同地址） | `runtime.retire` | 器官不登记自己的死亡（升级纪律） |
| R 拒绝退役当前接待员 | `runtime.retire` | 接待员先换再退 |
| syscall 的回执就是回复（同一次运行），失败回 `refused` | `runtime._dispatch` | 介质约定（ABI）：请求有回复 |
| 登记员只认来自本机门（含退役）的 born/placed/retired | `registrar.py` | 登记处记 c0 的宣称；信任本机 |
| 交出的门行带 `local`；登记员 = 第一扇 local 门那边 | `runtime._annot` / `realize.py` | c0 与 c1 有一条连线（G 的第一条连线） |
| L 组装时带成员表（工具列表） | `actors/l.py` | 运行中能请求的地址 = 它对机器的作用面；程序靠角色不需要表 |
| 0 的回复是全量行；L 组装读 1..当前 | `runtime._dispatch` / `actors/l.py` | 读账本是介质的地址 0，对全部成员开放；oracle 的读在转移行里（理论）。全量还是句柄、读多长是工程 |
| actor 是常驻函数：放入时 `exec` 一次，globals 里能存东西，不拦 | `runtime._instantiate` | H 记 call 边界，内部状态不入账；place 记初次实例化，根 channel 的 incarnation 事件与 place/retire 共同推出重启时的全部重新实例化。想活过重启的东西自己从账本重建 |
| 生命周期物理事件只落 G 的首 channel；A 经门发 `reconcile` | `runtime._lifecycle` / `realize.py` / `registrar.py` | c0 是唯一生命周期/形态边界；物理不渗透内脏，内脏只看到 actor 协作（T25/T26/T28/T31） |
| 程序在 R 进程内跑：没有隔离、没有超时；L 最多 16 轮；L 的模型原文不入账 | `omega.Exec.load` / `actors/l.py` | Ω 是契约边界不是进程边界；隔离与上限是工程（不要）。oracle 是完整 actor，端点回话在它的 run 里面（DALEK 1.4） |
| 帧语法是 L 的 text 的事（`>>> 地址` 起、单独一行 `<<<` 收）；R 记 call 用同一格式写 step.out | `actors/l.py` / `runtime.parse` | LLM 要能拼写 call；用什么记号是 L 的 text 的工程，R 不认识它 |
| 程序的 cwd = P（每次事件 `os.chdir`） | `runtime._invoke` | C 用文件系统 pack（H5/H7）；工程 |
| tag 分配与解析：channel 内活跃 tag 唯一；重名由 R 分配后缀 | `runtime._tag` / `_resolve` | `channel/tag` 是组织层地址；数字 addr 只属于 H |
| 门的 local 在交出账本时计算 | `runtime._annot` | 指向本机 channel 是此刻的事实；channel 只增所以单调 |
| place 的数字 addr 只增不复用；组织协议按 tag | ABI | `channel/tag` 属于形态；数字 addr 是 H 内部位置，actor 不得硬编码 |
| `bind` 门控单次介质动作，不门控能力的获得 | `runtime._dispatch` | 成员可以造出带任意 bind 的新成员（E5 里 D 重写的 C 就申请了 `syscall,spawn,stop`）——自我改造正是目的。所以 bind 是**账上的记录**，不是权限系统；别读成一套授权模型 |
| 收件箱是文件，人人可 append；text 拿着 urllib / open 也能绕过门写进别的机器 | `omega.Port.send/recv` | 膜的接收侧：收件箱只认合法句柄写入，句柄只在门那里——那时"穿过介质的只有 call"是 Port 的性质，不是约定。文件收件箱是 M1 的工程简化（2026-08-31）；其后果 = 可伪造 placed 遗传假形态（H17），真实系统靠句柄解决、演示模型靠约定 |
| 首 channel 必须显式接待员（genesis / build 拒绝） | `genesis.py` / `realize.py` | 出生需要一个入口（理论合法性条件）；拒绝是保障 |
| 一个 P 只有一个活 R：`--serve` 启动抢 `P/lock` 的 flock，抢不到即退出 | `init.lock` | 单写者 I1 是理论前提；flock 是工程兜底（alive→spawn 无操作、dormant→spawn 唤醒）。H18 |
| 关机只有一条路：`stop` 动词置意向，主循环在两个事件之间写 down；没有 SIGTERM 处理器 | `runtime._stop` / `runtime.run` | 停止是内到外的组织动作（意图是 policy，路径是机制）；信号处理器写账会重入 `_append` 撞序号 |

**H16 · C · c0 内部经合法门说假形态事实（2026-09-01 重新定性：不是洞）。** π(A) ≅ G 是 c0 维护的，所以持门的成员能把形态记错或记假。**这不该防**：一个器官把描述写歪，就是一次变异——它产生的后代形态不同或者不可存活，由后续世代淘汰。膜的作用是划定机器与世界的边界，不是保护机器不受自己器官的影响；**能改自己**正是我们要的东西。原来写的"原型不防"措辞错了，它暗示了应该防。（若哪天真要让登记事实的来源变成介质，工程方案是：`bind=registry` 的媒介能力——R 每落一条 place/retire 行就把该行原样投给持有它的 actor（像 syscall 回执一样 `from=place`），登记事实的来源变成 R，伪造路径消失，三段记账变回同时；R 仍内容盲。代价：c1 不再是"只经门喂出来的"，而且要小心别让这个动词回全量 text——那样 C 直接问 R 就能 pack，B 塌回世界，是 H15 高一层的重演。）

**H18 · C · 同一个 P 起两个 R，双写破坏单写者 I1（工程洞，2026-09-01；codex review #3）。** `spawn <P>` 无条件起进程，没查目标是否已活。一台机器活着时再 spawn 同一个 P（reporter 失联误判、或两个 peer 同时判死各 spawn 一次），两个 R 各自把 `c.seq` 折在内存里、各自 `c.seq+1` append，**序号相撞**（实测账本 32 行去重剩 20，`_pending`/游标/折叠全乱），账本永久不可折叠——而账本是唯一真相。**已加最小工程保障**（`init.lock`）：`--serve` 的 R 进程启动即对 `P/lock` 抢 `flock(LOCK_EX|LOCK_NB)`，抢不到 = 已有活 R → `exit 3`，load/wake 之前退出，一字节不写。flock 内核在进程死亡时自动释放，正好定义 Space 五态：alive→spawn 拿不到锁 = 无操作，dormant→spawn 拿得到 = 唤醒。这是**工程保障**（DESIGN §6 类，非理论）：真实系统本就该做单实例/存活检查，演示模型用一个 P 本地锁兜住；理论层"一个 P 一个 R"是 I1 的前提，不靠锁成立。进程内 `up()`（测试、膜外读）不抢锁，避免误伤。

**H19 · C · 能停自己是一个器官，不是天赋（2026-09-01 晚，实测；同日重新定位）。** 合法关机是一条链：A 转发 `stop` → 存在 tag=C 的活成员 → 它持 `bind=stop` → 它的 text 真的调 `call("stop")`。四环都是可变异的 text，断任何一环，机器就再也无法经协议停止，只能被杀。实测：把 C 去掉的机器收到 `stop`，`_resolve("C")` 落空、消息按"不存在的地址"丢弃，`_stopping` 仍是 False、根边界一行不写，机器照常活着。
  **这不是机制的洞，是完整性的定义**（DALEK "完整的 Dalek = A + B + C + D"）：完整的 Dalek 是 {c0, c1, c2}；丢了 C 的东西只是一台不完整的机器，不再具备它本该有的能力。**组织完整性不由 R 保证，由 D 维护**——只要 c0 的接待员还在转发、c2 还在，机器就能重新写一个 C 装回去（这正是与 VN48 的分别：VN 的机器掉了零件就是死了，Dalek 的 D 能补）。R 不该代劳：拒绝自退役、拒绝退接待员是不认名字的结构规则，而"别退最后一个持 bind=stop 的成员"必须认识组织角色，越过内容盲。要防就写成 A 的策略（转发 retire 前自己查），那是 policy 层。
  **同构的第二条：能观测自己的失效也是一个器官（2026-09-01 补）。** 介质不提供失效信号——写给不存在地址的消息静默丢弃（H8），装入后才暴露的错误 R 一个字不说。E2 里模型写的 `call("spawn", d)` 参数形式错了、U 的测试没覆盖到，装上去后就是这样静默失效的。但这不是模型的缺口：同一次实验里，两台 peer 探活失败、判定同伴死了、把它拉起来——**失效的观测由器官做，不需要介质提供**。所以"D = L + U 的生成—检验回路"里，U 只是装入前的一道判据；装入后的判据是器官的事，弱就是这台机器的这个器官弱。
  **唯一无法自愈的单点**：c0 的接待员。所有形态改动都要经它（c2 的门通向它、syscall 只在它那边），它换成一个不转发的 A′，D 的手就够不着自己的形态——这是可自愈与不可自愈的分界线。
  **收进主张（2026-08-31）——"D 维护完整性"是有条件命题**：最小条件三条 = (i) 机器跑得起来；(ii) 接待员活着（或按 M2"先换后退"备好后继）；(iii) 有作者（内生 D 或经门的外生作者）可用。三条齐备时其它一切可经构造路重建。两个边界都是活性失败、不是设计缺口：**手够不着**（接待员死于无活后继；活着时可"先加新的、指过去、再退旧的"换掉自己）与**身体醒不来**（毒消息杀 R，见 H10——D 在任何器官动之前就没机会跑，邻居唤醒变成无限重生循环）。同步写入 DALEK 2.1。

**H20 · C · 根门的关闭是 H 的派生事实（2026-09-01 晚，实测）。** `root_open = 所有账本都没有 msg 行`——它不是一个存下来的开关，是从历史折出来的。于是外科式的损伤能让构造口重新打开：实测删掉每本账的全部 msg 行、保留 place 行后，`root_open` 变回 True，四个器官都还在，膜外又能经根门发 syscall 放东西了。这与"H 是真相"一致（要做到这一步必须先摧毁历史，而历史被摧毁的个体本就不再是原来那个），所以不改实现；但边界的这条性质要写下来：**"出生一次、根门永久关闭"是关于完整 H 的命题，不是关于进程的**。
  **处置（2026-08-31）：标注为约定，同 H12——理论演示原型不为自己 H 的完整性负责**，H 的引导/完整性由宿主保护；本条只作边界命题记录，不改实现，不列为开放洞。

**H17 · C · 任意 actor 伪造 placed，绕过 syscall 使假形态遗传（工程洞，2026-09-01；codex review）。** 收件箱是明文文件、`Port.send` 对谁都开，所以一个普通 program 能在 run 里 `open("in/c1.jsonl","a")` 写一行署名 `file:<P>#c0` 的 `placed …`，登记员采信 → 新 channel/成员进 `decl` 并遗传——**新能力不经 door→c0→syscall→R 就进了基因组**。比 H16 更宽：H16 只是 c0 自己作恶，H17 是任何 actor 都能走。**理论层不破**：模型里 `Port` 接收侧有写句柄，text 拿不到别人的句柄，"只有一条进入路径"成立；破的是**当前实现见证这条膜**——M1 用明文文件收件箱（同 §6 那行、I6 的"接收侧只认合法句柄"未实现）。**真实系统必须解决（句柄/能力，Atoll 已在传输层焊死）；演示模型不做——复杂度对最小理论模型太高，靠约定：不伪造收件箱。** 所以"只有一条进入路径"是**理论句**，不是本原型的实现保证。
  **处置（2026-08-31）：标注为约定，理论演示原型不守护此项**——封闭性的执法归模型（Port 接收侧句柄）与真实系统（Atoll 传输层已焊死）；本原型靠"不伪造收件箱"的约定，不列为开放洞。原型见证的是构造/自愈/遗传，不见证封闭性的执法。

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
